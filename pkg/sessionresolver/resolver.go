// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionresolver reproduces the Python vault_ui.session_resolver
// display-name to UUID resolution as a behavior-preserving port.
package sessionresolver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
)

// maxLineBytes is the Python _MAX_LINE_BYTES: a raw transcript line longer than
// this is skipped rather than parsed.
const maxLineBytes = 4096

// fingerprintBytes is how many bytes immediately before the cached offset are
// remembered and compared to detect an in-place rewrite of a transcript.
const fingerprintBytes = 64

var uuidRe = regexp.MustCompile(
	`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// transcriptCache caches the title-scan state of each transcript so a cleanup
// sweep over a large project directory reads only the bytes appended since the
// previous pass. The cache is package-level rather than injected because
// ResolveSessionID's signature and both of its callers (the cleanup sweep and
// the HTTP request path) are frozen by this change; it is guarded by
// transcriptCacheMu because those two paths resolve concurrently.
//
// Accepted limitations — a full scan would notice these, the cache cannot, and
// the previous answer is returned instead:
//   - a rewrite that keeps the inode, size and mtime returns the cached result;
//   - an in-place rewrite that only alters bytes before the 64-byte fingerprint
//     window is not detected;
//   - a "custom-title" type value written with JSON escapes rather than the
//     literal bytes is not recognised by the byte prefilter.
var (
	transcriptCacheMu sync.Mutex
	transcriptCache   = map[string]*cacheEntry{}
)

// cacheEntry is the remembered scan state for one transcript path.
type cacheEntry struct {
	info        os.FileInfo
	size        int64
	mtime       time.Time
	offset      int64
	fingerprint []byte
	committed   any
	result      string
	resultOK    bool
}

// scanResult is the outcome of reading a transcript from a starting point.
type scanResult struct {
	offset    int64
	committed any
	title     string
	resultOK  bool
}

// IsUUID reports whether value matches the 8-4-4-4-12 hex UUID format.
func IsUUID(value string) bool {
	return uuidRe.MatchString(value)
}

// ResolveSessionID maps a non-UUID display name to its real UUID. The live
// process table is consulted first; otherwise each `.jsonl` transcript in
// projectDir is scanned and a session's current title is the customTitle of the
// LAST "custom-title" line carrying a customTitle key. Returns (uuid, true) only
// for exactly one current match; ("", false) for no match, for an ambiguous tie,
// and for a missing project directory. Malformed JSON lines and over-long lines
// are skipped.
func ResolveSessionID(
	ctx context.Context,
	displayName, projectDir string,
	liveSessionNames map[string]string,
) (string, bool) {
	if sessionID, ok := liveSessionNames[displayName]; ok {
		glog.V(4).Infof(
			"[SessionResolver] Resolved live '%s' -> '%s' from process table",
			displayName,
			sessionID,
		)
		return sessionID, true
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		glog.V(4).Infof("[SessionResolver] project_dir does not exist: %s", projectDir)
		return "", false
	}

	cacheEvictMissing(projectDir, entries)

	candidates := []string{}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return "", false
		default:
		}
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, name)
		currentTitle, ok := lastCustomTitle(ctx, path)
		if ok && currentTitle == displayName {
			candidates = append(candidates, strings.TrimSuffix(name, ".jsonl"))
		}
	}

	if len(candidates) == 1 {
		glog.V(4).Infof("[SessionResolver] Resolved '%s' -> '%s'", displayName, candidates[0])
		return candidates[0], true
	}
	if len(candidates) > 1 {
		glog.Warningf(
			"[SessionResolver] Ambiguous title '%s' matches %d sessions: %v — refusing to resolve",
			displayName,
			len(candidates),
			candidates,
		)
	}
	return "", false
}

// lastCustomTitle returns the current title of a transcript: the customTitle of
// the last "custom-title" line that carries a customTitle key. A custom-title
// line without a customTitle key does not erase the previous title. A raw line
// longer than maxLineBytes, a malformed JSON line, and an unreadable file each
// contribute nothing.
//
// A process-wide cache remembers each transcript's scan state, so a file that
// only grew is read from the previous offset instead of from the start.
func lastCustomTitle(ctx context.Context, path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		glog.Warningf("[SessionResolver] Cannot read %s: %v", path, err)
		cacheDrop(path)
		return "", false
	}

	entry := cacheGet(path)
	if entry != nil && cacheEntryFresh(entry, info) {
		return entry.result, entry.resultOK
	}

	fh, err := os.Open(path)
	if err != nil {
		glog.Warningf("[SessionResolver] Cannot read %s: %v", path, err)
		cacheDrop(path)
		return "", false
	}
	defer func() { _ = fh.Close() }()

	startOffset, startCommitted := resumePoint(fh, entry, info)
	if _, err := fh.Seek(startOffset, io.SeekStart); err != nil {
		glog.Warningf("[SessionResolver] Cannot read %s: %v", path, err)
		cacheDrop(path)
		return "", false
	}

	result, cancelled := scanTranscript(ctx, fh, path, startOffset, startCommitted)
	if cancelled {
		return "", false
	}

	cacheStore(path, &cacheEntry{
		info:        info,
		size:        info.Size(),
		mtime:       info.ModTime(),
		offset:      result.offset,
		fingerprint: readFingerprint(fh, result.offset),
		committed:   result.committed,
		result:      result.title,
		resultOK:    result.resultOK,
	})
	return result.title, result.resultOK
}

// cacheEntryFresh reports whether the file is unchanged since the cached read,
// in which case the stored result can be returned without opening the file.
func cacheEntryFresh(entry *cacheEntry, info os.FileInfo) bool {
	return os.SameFile(entry.info, info) &&
		entry.size == info.Size() &&
		entry.mtime.Equal(info.ModTime())
}

// resumePoint returns the offset and committed value to resume from. It resets
// to the start of the file when the entry cannot be trusted.
func resumePoint(fh *os.File, entry *cacheEntry, info os.FileInfo) (int64, any) {
	if entry == nil {
		return 0, nil
	}
	if !os.SameFile(entry.info, info) || info.Size() < entry.offset {
		return 0, nil
	}
	if !fingerprintMatches(fh, entry.offset, entry.fingerprint) {
		return 0, nil
	}
	return entry.offset, entry.committed
}

// scanTranscript reads the transcript from its current position, applying every
// complete line to the committed value and any trailing unterminated line to the
// result only. It returns cancelled=true when ctx is done; in that case the
// caller must leave the cache untouched.
func scanTranscript(
	ctx context.Context,
	fh *os.File,
	path string,
	startOffset int64,
	startCommitted any,
) (*scanResult, bool) {
	reader := bufio.NewReaderSize(fh, maxLineBytes+1)
	offset := startOffset
	consumed := startOffset
	current := startCommitted

	for {
		select {
		case <-ctx.Done():
			return nil, true
		default:
		}

		raw, readErr := reader.ReadSlice('\n')
		if readErr == bufio.ErrBufferFull {
			consumed += int64(len(raw))
			var overlongErr error
			consumed, overlongErr = skipOverlongRest(reader, consumed)
			if overlongErr == nil {
				offset = consumed
				continue
			}
			if overlongErr == io.EOF {
				return makeScanResult(offset, current, nil, false), false
			}
			glog.Warningf("[SessionResolver] Cannot read %s: %v", path, overlongErr)
			return makeScanResult(offset, current, nil, false), false
		}

		consumed += int64(len(raw))
		switch {
		case readErr == nil:
			current = applyTitleLine(raw, current, path)
			offset = consumed
		case readErr == io.EOF:
			if len(raw) == 0 {
				return makeScanResult(offset, current, nil, false), false
			}
			return makeScanResult(offset, current, applyTitleLine(raw, current, path), true), false
		default:
			glog.Warningf("[SessionResolver] Cannot read %s: %v", path, readErr)
			if len(raw) == 0 {
				return makeScanResult(offset, current, nil, false), false
			}
			return makeScanResult(offset, current, applyTitleLine(raw, current, path), true), false
		}
	}
}

// skipOverlongRest consumes the remainder of an over-long line, discarding it,
// and returns the number of bytes consumed. The error is nil once the line's
// newline is reached, io.EOF for an unterminated final line, or a read error.
func skipOverlongRest(reader *bufio.Reader, consumed int64) (int64, error) {
	for {
		raw, err := reader.ReadSlice('\n')
		consumed += int64(len(raw))
		if err != bufio.ErrBufferFull {
			return consumed, err
		}
	}
}

// applyTitleLine applies one considered raw line to current and returns the
// updated value. Lines longer than maxLineBytes are ignored. Lines that do not
// contain the bytes "custom-title" are skipped without decoding.
func applyTitleLine(raw []byte, current any, path string) any {
	if len(raw) == 0 || len(raw) > maxLineBytes {
		return current
	}
	line := strings.TrimSpace(string(raw))
	if line == "" || !strings.Contains(line, "custom-title") {
		return current
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		glog.Warningf("[SessionResolver] Malformed JSON in %s, skipping line", path)
		return current
	}
	if parsed["type"] != "custom-title" {
		return current
	}
	if title, ok := parsed["customTitle"]; ok && title != nil {
		return title
	}
	return current
}

// makeScanResult builds the scan outcome from the committed value and, when
// present, the trailing unterminated line applied on top of it.
func makeScanResult(offset int64, committed, partial any, hasPartial bool) *scanResult {
	value := committed
	if hasPartial {
		value = partial
	}
	title, ok := value.(string)
	return &scanResult{
		offset:    offset,
		committed: committed,
		title:     title,
		resultOK:  ok,
	}
}

// readFingerprint returns up to fingerprintBytes bytes immediately before
// offset. It returns nil when nothing can be read.
func readFingerprint(fh *os.File, offset int64) []byte {
	size := int64(fingerprintBytes)
	if offset < size {
		size = offset
	}
	if size <= 0 {
		return nil
	}
	buf := make([]byte, size)
	if _, err := fh.ReadAt(buf, offset-size); err != nil {
		return nil
	}
	return buf
}

// fingerprintMatches reports whether the bytes before offset still equal the
// remembered fingerprint. A missing fingerprint for a non-zero offset does not
// match, forcing a full scan.
func fingerprintMatches(fh *os.File, offset int64, fingerprint []byte) bool {
	want := int64(len(fingerprint))
	if want > fingerprintBytes || offset < want {
		return false
	}
	if want == 0 {
		return offset == 0
	}
	buf := make([]byte, want)
	if _, err := fh.ReadAt(buf, offset-want); err != nil {
		return false
	}
	return bytes.Equal(buf, fingerprint)
}

// cacheGet returns the cached entry for path, or nil.
func cacheGet(path string) *cacheEntry {
	transcriptCacheMu.Lock()
	defer transcriptCacheMu.Unlock()
	return transcriptCache[path]
}

// cacheStore records entry for path. A stale concurrent scan that is behind the
// stored offset for the same file does not overwrite newer state.
func cacheStore(path string, entry *cacheEntry) {
	transcriptCacheMu.Lock()
	defer transcriptCacheMu.Unlock()
	if stored, ok := transcriptCache[path]; ok &&
		stored.info != nil && entry.info != nil &&
		os.SameFile(stored.info, entry.info) &&
		stored.offset > entry.offset {
		return
	}
	transcriptCache[path] = entry
}

// cacheDrop forgets the cached entry for path.
func cacheDrop(path string) {
	transcriptCacheMu.Lock()
	defer transcriptCacheMu.Unlock()
	delete(transcriptCache, path)
}

// cacheEvictMissing forgets entries inside projectDir whose file no longer
// appears in the directory listing.
func cacheEvictMissing(projectDir string, entries []os.DirEntry) {
	dir := filepath.Clean(projectDir)
	present := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		present[filepath.Join(dir, entry.Name())] = struct{}{}
	}
	transcriptCacheMu.Lock()
	defer transcriptCacheMu.Unlock()
	for path := range transcriptCache {
		if filepath.Dir(path) != dir {
			continue
		}
		if _, ok := present[path]; !ok {
			delete(transcriptCache, path)
		}
	}
}
