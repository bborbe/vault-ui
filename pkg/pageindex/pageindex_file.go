// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
)

// RefreshFile re-reads one file of the key's folder and publishes a new
// snapshot with the result spliced in. It blocks until a snapshot containing a
// read that started after this call has been published. The file's current
// on-disk state decides the outcome, not the caller's reason for calling:
// present and readable replaces or inserts the page at its filename-ordered
// position, absent or unreadable removes it. The filename is a single plain
// base name including ".md"; a filename that is empty, contains a path
// separator, contains "..", or does not end in ".md" is rejected with an error
// and nothing is read.
func (p *pageIndex) RefreshFile(ctx context.Context, key Key, filename string) error {
	key = NewKey(key.VaultPath, key.PagesDir)
	if err := validatePageFilename(ctx, filename); err != nil {
		return err
	}

	p.mu.Lock()
	e := p.entryLocked(key)
	if !e.hasSnapshot {
		p.mu.Unlock()
		// The key has nothing published yet, so the folder build includes this
		// file. Never join a build that predates this call.
		return p.Refresh(ctx, key)
	}
	p.readSeq++
	seq := p.readSeq
	p.mu.Unlock()

	// The read outlives the caller: a cancelled caller must not abort work
	// other readers depend on, nor remove a page it never saw.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)
	defer cancel()
	page, fingerprint, readErr := p.reader.ReadPage(
		readCtx,
		key.VaultPath,
		key.PagesDir,
		filename,
	)
	recordRead(reasonEvent)

	p.mu.Lock()
	delta := p.applyFileReadLocked(e, key, filename, seq, page, fingerprint, readErr)
	p.mu.Unlock()

	p.writeThrough(readCtx, key, delta)

	if err := ctx.Err(); err != nil {
		return errors.Wrap(ctx, err, "refresh file")
	}
	return nil
}

// applyFileReadLocked applies one single-file read to the current snapshot and
// returns the store delta the read produced, or nil when a later-started read
// has already been applied. A file that is gone is a delete; a file that is
// present, even one excluded from the snapshot, is a put carrying its
// fingerprint. The caller must hold the mutex.
func (p *pageIndex) applyFileReadLocked(
	e *entry,
	key Key,
	filename string,
	seq uint64,
	page *domain.Page,
	fingerprint FileFingerprint,
	readErr error,
) *storeDelta {
	if seq < e.fileReadSeq[filename] {
		// A read that started later has already been applied.
		return nil
	}
	previous, known := e.fingerprints[filename]
	e.fileReadSeq[filename] = seq
	e.snapshot = splicePage(e.snapshot, filename, page)
	e.revision++
	// One file was re-read, so the advance is attributable to exactly it.
	e.recordChangeLocked([]string{filename}, true)
	if page == nil && fingerprint == (FileFingerprint{}) {
		// A vanished file leaves no recorded fingerprint behind, so the recorded
		// name set stays equal to a later listing's and the unchanged fast path
		// is not blocked by a zero-fingerprint tombstone. An excluded-but-present
		// file keeps its non-zero fingerprint.
		delete(e.fingerprints, filename)
	} else {
		e.fingerprints[filename] = fingerprint
	}
	if readErr != nil {
		switch {
		case fingerprint == (FileFingerprint{}):
			// The stat itself failed: a plain deletion, not a broken page.
			glog.V(2).Infof(
				"page file gone %q in %q/%q: %v",
				filename,
				key.VaultPath,
				key.PagesDir,
				readErr,
			)
		case !known || previous != fingerprint:
			p.warnf(
				"unreadable page excluded %q in %q/%q: %v",
				filename,
				key.VaultPath,
				key.PagesDir,
				readErr,
			)
		}
	}

	// A vanished file is a delete, a present file a put.
	if fingerprint == (FileFingerprint{}) {
		return &storeDelta{deletes: []string{filename}}
	}
	return &storeDelta{
		puts: []StoredEntry{{Filename: filename, Page: page, Fingerprint: fingerprint}},
	}
}

// splicePage returns a new slice holding pages plus the page for name, inserted
// or replaced at its filename-ordered position; a nil page removes name. The
// ordering key is the page's file name plus ".md", which is the os.ReadDir
// order. pages is never mutated, so a snapshot a reader already holds stays
// intact.
func splicePage(pages []*domain.Page, name string, page *domain.Page) []*domain.Page {
	at := sort.Search(len(pages), func(i int) bool {
		return pages[i].FileMetadata.Name+".md" >= name
	})
	found := at < len(pages) && pages[at].FileMetadata.Name+".md" == name

	switch {
	case found && page != nil:
		spliced := make([]*domain.Page, len(pages))
		copy(spliced, pages)
		spliced[at] = page
		return spliced
	case found:
		spliced := make([]*domain.Page, 0, len(pages)-1)
		spliced = append(spliced, pages[:at]...)
		return append(spliced, pages[at+1:]...)
	case page == nil:
		spliced := make([]*domain.Page, len(pages))
		copy(spliced, pages)
		return spliced
	default:
		spliced := make([]*domain.Page, 0, len(pages)+1)
		spliced = append(spliced, pages[:at]...)
		spliced = append(spliced, page)
		return append(spliced, pages[at:]...)
	}
}

// validatePageFilename rejects anything that is not a single plain base name
// ending in ".md", so a read can never escape the key's folder.
func validatePageFilename(ctx context.Context, filename string) error {
	if filename == "" ||
		filepath.Base(filename) != filename ||
		strings.ContainsRune(filename, filepath.Separator) ||
		strings.Contains(filename, "..") ||
		!strings.HasSuffix(filename, ".md") {
		return errors.Errorf(ctx, "invalid page filename %q", filename)
	}
	return nil
}
