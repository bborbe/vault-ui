// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package activity reproduces the Python vault_ui.activity activity-date,
// transcript-mtime, and session-registry helpers as a behavior-preserving port.
package activity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// TranscriptMtime returns the mtime of the session transcript, checking
// projectDir first and then every project directory under projectsRoot.
// It returns nil for a missing/blank session id and for a transcript that
// cannot be found or read (an expected absence, never an error).
//
// Deliberately uncached, exactly as the Python docstring says: the caller reads
// it once per request and a cache would keep a dead session looking fresh.
func TranscriptMtime(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime {
	if sessionID == "" {
		return nil
	}

	filename := sessionID + ".jsonl"

	if direct := mtimeOrNone(filepath.Join(projectDir, filename)); direct != nil {
		return direct
	}

	root := projectsRoot
	if root == "" {
		root = DefaultProjectsRoot()
	}

	matches, err := filepath.Glob(filepath.Join(root, "*", filename))
	if err != nil {
		glog.V(4).Infof("[Activity] Cannot scan %s: %v", root, err)
		return nil
	}
	for _, path := range matches {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if found := mtimeOrNone(path); found != nil {
			return found
		}
	}

	return nil
}

// TranscriptMtimeGetter returns a session transcript's mtime, or nil when the
// transcript is absent. activity.TranscriptMtime is the default implementation.
type TranscriptMtimeGetter func(
	ctx context.Context,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime

// ComputeActivityDate returns the newer of the task-file mtime and the transcript
// mtime; nil only when both signals are absent. A nil modifiedDate is an absent
// task-file mtime.
func ComputeActivityDate(
	ctx context.Context,
	modifiedDate *libtime.DateTime,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime {
	return ComputeActivityDateWith(
		ctx, TranscriptMtime, modifiedDate, sessionID, projectDir, projectsRoot,
	)
}

// ComputeActivityDateWith is ComputeActivityDate with an injected transcript
// probe, so a caller can supply a cached probe instead of touching the
// filesystem. A nil probe means TranscriptMtime.
func ComputeActivityDateWith(
	ctx context.Context,
	probe TranscriptMtimeGetter,
	modifiedDate *libtime.DateTime,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime {
	candidates := make([]libtime.DateTime, 0, 2)
	if modifiedDate != nil {
		candidates = append(candidates, modifiedDate.UTC())
	}
	if probe == nil {
		probe = TranscriptMtime
	}
	if transcript := probe(ctx, sessionID, projectDir, projectsRoot); transcript != nil {
		candidates = append(candidates, *transcript)
	}
	if len(candidates) == 0 {
		return nil
	}

	newest := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.After(newest) {
			newest = candidate
		}
	}
	return newest.Ptr()
}

// ReadRegistrySessionIDs returns the sessionId of every entry in the harness
// session registry directory. Never raises: a missing/unreadable directory, an
// unreadable file, a non-JSON file, and a file with no sessionId each contribute
// nothing.
//
// Deliberately uncached — the caller reads it once per request, and a cache
// would keep a registry-live session rendering live after its process exits.
func ReadRegistrySessionIDs(ctx context.Context, root string) []string {
	ids := []string{}

	entries, err := os.ReadDir(root)
	if err != nil {
		glog.V(4).Infof("[Activity] Cannot read session registry %s: %v", root, err)
		return ids
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ids
		default:
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			glog.V(4).Infof("[Activity] Cannot read session registry entry %s: %v", path, readErr)
			continue
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			glog.V(4).Infof(
				"[Activity] Cannot read session registry entry %s: %v",
				path,
				unmarshalErr,
			)
			continue
		}
		if sessionID, ok := payload["sessionId"].(string); ok && sessionID != "" {
			ids = append(ids, sessionID)
		}
	}

	return ids
}

// DefaultRegistryRoot returns the harness registry root (`~/.claude/sessions`).
func DefaultRegistryRoot() string {
	return filepath.Join(homeDir(), ".claude", "sessions")
}

// DefaultProjectsRoot returns the transcript projects root (`~/.claude/projects`).
func DefaultProjectsRoot() string {
	return filepath.Join(homeDir(), ".claude", "projects")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// TranscriptIndex resolves a session transcript from one listing of the
// projects root.
//
// filepath.Glob re-reads that root — and every project directory under it — on
// each call, so a caller asking about N sessions pays for the same listing N
// times. On the board that was measured at 81 % of the process's CPU. The index
// pays for the listing once and answers each lookup with a map hit and a single
// stat.
//
// It is deliberately epoch-scoped rather than long-lived: the caller builds one,
// resolves the sessions it is working on, and drops it, so a transcript that
// appears or disappears is picked up at the next listing instead of being cached
// away. That preserves the property TranscriptMtime's own comment protects — a
// cache must never keep a dead session looking fresh.
// transcriptSuffix is the extension a session transcript carries. Only these
// are indexed or looked up, so the constant is the single place the two agree.
const transcriptSuffix = ".jsonl"

type TranscriptIndex struct {
	// byFilename holds every match for a filename, in listing order, because
	// the glob this replaces returned every match and stat'd each in turn: a
	// first copy that is unreadable or deleted between the listing and the
	// lookup fell through to a later project directory. Keeping only the first
	// would report such a session absent where the old code found it.
	byFilename map[string][]string
}

// NewTranscriptIndex lists projectsRoot once. It never returns a nil index, so a
// caller can carry the result without a nil check on every lookup; a root that
// cannot be listed yields an empty index, which reports every session absent —
// the same answer filepath.Glob gives for an unreadable root.
//
// The second result reports whether the listing completed. It is false when the
// context was cancelled part-way, and the returned index is then a prefix of the
// real one: usable for the lookups in flight, but it must not be cached. A
// truncated listing reads every session it did not reach as absent, so a caller
// that keeps it — as the board's per-epoch cache does — would misreport those
// sessions for as long as it holds it, and one cancelled request is enough.
//
// Entries are walked in name order and so are the files inside each, which is
// the order Glob produced after sorting its matches.
func NewTranscriptIndex(
	ctx context.Context,
	projectsRoot string,
) (*TranscriptIndex, bool) {
	index := &TranscriptIndex{byFilename: map[string][]string{}}
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		glog.V(4).Infof("[Activity] Cannot list %s: %v", projectsRoot, err)
		// An unreadable root is a complete listing of nothing, not a truncation:
		// it is the same answer Glob gives, and caching it is correct.
		return index, true
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return index, false
		}
		dir := filepath.Join(projectsRoot, entry.Name())
		// Stat rather than DirEntry.IsDir: that reports false for a symlink,
		// and Glob followed symlinked project directories — it recurses over
		// readDirNames, which returns every entry name regardless of type. A
		// skipped symlink would silently read its sessions as absent.
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			continue
		}
		files, readErr := os.ReadDir(dir)
		if readErr != nil {
			// An unreadable project directory is an expected absence, not an
			// error: it is the same miss filepath.Glob reports by matching
			// nothing inside it.
			continue
		}
		for _, file := range files {
			// The innermost loop is the hottest one — paying it once per epoch
			// is what the index exists for — so it carries the check too: a
			// single project directory can hold enough entries to matter.
			if ctx.Err() != nil {
				return index, false
			}
			name := file.Name()
			// Only transcripts are ever looked up, so the rest are dead weight
			// in both the map and the build.
			if !strings.HasSuffix(name, transcriptSuffix) {
				continue
			}
			index.byFilename[name] = append(index.byFilename[name], filepath.Join(dir, name))
		}
	}
	return index, true
}

// Mtime returns the transcript's mtime, preferring projectDir and otherwise
// resolving the session id through the index. It returns nil for a blank or
// unknown session id, exactly as TranscriptMtime does.
func (i *TranscriptIndex) Mtime(
	ctx context.Context,
	sessionID, projectDir string,
) *libtime.DateTime {
	if sessionID == "" {
		return nil
	}
	filename := sessionID + ".jsonl"
	if direct := mtimeOrNone(filepath.Join(projectDir, filename)); direct != nil {
		return direct
	}
	if i == nil {
		return nil
	}
	for _, path := range i.byFilename[filename] {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if found := mtimeOrNone(path); found != nil {
			return found
		}
	}
	return nil
}

func mtimeOrNone(path string) *libtime.DateTime {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return libtime.DateTime(info.ModTime()).UTC().Ptr()
}
