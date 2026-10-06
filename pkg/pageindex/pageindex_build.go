// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
)

// role describes what the caller that acquired a build has to do with it.
type role int

const (
	// roleRun means the caller owns the build and it is already in flight.
	roleRun role = iota
	// roleFollowUp means the caller owns a queued build and must wait until it
	// is promoted before running it.
	roleFollowUp
	// roleWait means the caller only waits for somebody else's build.
	roleWait
)

// ensure returns the key's pages, waiting for or running a rebuild as needed.
//
// It returns the build error separately from a cancellation of the caller's own
// ctx: a failed rebuild is not an error for a caller that can still serve a
// previous snapshot, while a cancelled caller must not be handed stale pages.
func (p *pageIndex) ensure(
	ctx context.Context,
	key Key,
	refresh bool,
) ([]*domain.Page, error, error) {
	p.mu.Lock()
	e := p.entryLocked(key)
	if refresh {
		e.requestSeq++
	}
	if !refresh && e.hasSnapshot && e.snapshotSeq >= e.dirtySeq {
		snapshot := e.snapshot
		p.mu.Unlock()
		return snapshot, nil, nil
	}
	b, callerRole := p.selectBuildLocked(e, refresh)
	p.mu.Unlock()

	if callerRole == roleWait {
		select {
		case <-b.done:
		case <-ctx.Done():
			return nil, nil, errors.Wrap(ctx, ctx.Err(), "wait for page index build")
		}
		return b.pages, b.err, nil
	}
	if callerRole == roleFollowUp {
		// A follow-up must never be abandoned: it is already the key's queued
		// rebuild, and nobody else would run it.
		<-b.started
	}
	p.execute(ctx, key, b)
	if err := ctx.Err(); err != nil {
		return nil, b.err, errors.Wrap(ctx, err, "page index build")
	}
	return b.pages, b.err, nil
}

// selectBuildLocked picks the build the caller waits on and starts one when
// none is suitable. The caller must hold the mutex.
func (p *pageIndex) selectBuildLocked(e *entry, refresh bool) (*build, role) {
	if refresh {
		return p.selectRefreshBuildLocked(e)
	}
	switch {
	case e.inflight != nil && e.inflight.startSeq >= e.dirtySeq:
		// A build that already covers the last dirty mark: share it.
		return e.inflight, roleWait
	case e.followUp != nil:
		return e.followUp, roleWait
	case e.inflight != nil:
		// The build in flight started before the dirty mark, so its result
		// would still be stale: queue exactly one follow-up.
		return p.queueFollowUpLocked(e), roleFollowUp
	default:
		b := newBuild(e.requestSeq)
		e.inflight = b
		return b, roleRun
	}
}

// selectRefreshBuildLocked picks a build that starts after this refresh. The
// caller must hold the mutex.
func (p *pageIndex) selectRefreshBuildLocked(e *entry) (*build, role) {
	if e.inflight == nil {
		b := newBuild(e.requestSeq)
		e.inflight = b
		return b, roleRun
	}
	if e.followUp != nil {
		return e.followUp, roleWait
	}
	return p.queueFollowUpLocked(e), roleFollowUp
}

// queueFollowUpLocked appends the one queued build a key may have. The caller
// must hold the mutex.
func (p *pageIndex) queueFollowUpLocked(e *entry) *build {
	b := newBuild(e.requestSeq)
	e.followUp = b
	return b
}

// maxUnreadablePageWarnings is the number of per-file skip warnings a single
// build emits before it stops naming files individually and prints one summary
// line instead. It bounds a folder full of unreadable pages.
const maxUnreadablePageWarnings = 10

// execute runs the folder read and publishes its result. It runs on the
// goroutine of the caller that owns the build and finishes even when that
// caller's ctx is cancelled.
func (p *pageIndex) execute(ctx context.Context, key Key, b *build) {
	buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)
	defer cancel()
	reads, err := p.buildSnapshot(buildCtx, key)

	p.mu.Lock()
	defer p.mu.Unlock()
	b.pages = nil
	b.err = err
	e := p.entryLocked(key)
	if err != nil {
		glog.Errorf(
			"page index rebuild failed for vault %q pages dir %q: %v",
			key.VaultPath,
			key.PagesDir,
			err,
		)
	} else {
		pages, fingerprints := mergeSnapshot(e, reads)
		e.snapshot = pages
		e.hasSnapshot = true
		e.snapshotSeq = b.startSeq
		e.fingerprints = fingerprints
		// Callers waiting on this build get the merged snapshot, never a page a
		// later-started read has already superseded.
		b.pages = pages
	}
	// Clearing the build and promoting the follow-up happen under one lock
	// acquisition so no caller can slip a parallel build into the gap.
	e.inflight = nil
	if e.followUp != nil {
		next := e.followUp
		e.followUp = nil
		next.startSeq = e.requestSeq
		e.inflight = next
		close(next.started)
	}
	close(b.done)
}

// fileRead is one listed file's read result within a build. A nil page means
// the read failed, so the file is excluded.
type fileRead struct {
	seq         uint64
	page        *domain.Page
	fingerprint FileFingerprint
}

// snapshotReads is one build's collected reads: the sequence the listing itself
// started at, the listing's base filenames in order and each file's result.
type snapshotReads struct {
	listingSeq uint64
	order      []string
	reads      map[string]fileRead
	// missing reports that the folder does not exist, which lists as nil.
	missing bool
}

// buildSnapshot lists the key's folder and reads each page file through the
// reader seam, reserving one read sequence per file before its read. It returns
// the listing order plus the result and fingerprint of every entry, including
// the ones whose read failed.
func (p *pageIndex) buildSnapshot(ctx context.Context, key Key) (*snapshotReads, error) {
	listingSeq := p.takeReadSeq()
	entries, err := p.lister.ListFiles(ctx, key.VaultPath, key.PagesDir)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		// A missing folder lists as nil, matching vault-cli's nil-vs-empty.
		return &snapshotReads{listingSeq: listingSeq, missing: true}, nil
	}

	result := &snapshotReads{
		listingSeq: listingSeq,
		order:      make([]string, 0, len(entries)),
		reads:      make(map[string]fileRead, len(entries)),
	}
	skipped := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(ctx, err, "build page snapshot")
		}
		seq := p.takeReadSeq()
		page, fingerprint, readErr := p.reader.ReadPage(
			ctx,
			key.VaultPath,
			key.PagesDir,
			entry.Name,
		)
		recordRead(reasonBuild)
		result.order = append(result.order, entry.Name)
		result.reads[entry.Name] = fileRead{seq: seq, page: page, fingerprint: fingerprint}
		if readErr != nil {
			if skipped < maxUnreadablePageWarnings {
				glog.Warningf(
					"skipping unreadable page %q in %q/%q: %v",
					entry.Name,
					key.VaultPath,
					key.PagesDir,
					readErr,
				)
			}
			skipped++
		}
	}
	if skipped >= maxUnreadablePageWarnings {
		glog.Warningf("skipping %d unreadable pages", skipped)
	}
	return result, nil
}

// snapshotMerge accumulates one build's merge into a new snapshot. The entry's
// current snapshot is the other input, so a read a later-started one has
// already superseded is never applied.
type snapshotMerge struct {
	entry        *entry
	reads        *snapshotReads
	pages        []*domain.Page
	fingerprints map[string]FileFingerprint
}

// listed takes a listed file: the build's own read when no later-started read
// has already been applied to that name, else the page currently published.
func (m *snapshotMerge) listed(name string, current *domain.Page) {
	read, ok := m.reads.reads[name]
	if ok && read.seq >= m.entry.fileReadSeq[name] {
		m.entry.fileReadSeq[name] = read.seq
		m.fingerprints[name] = read.fingerprint
		if read.page != nil {
			m.pages = append(m.pages, read.page)
		}
		return
	}
	m.kept(name, current)
}

// unlisted drops a name the listing no longer holds, unless the listing started
// before that name's last applied read — then a newer read put it there.
func (m *snapshotMerge) unlisted(name string, current *domain.Page) {
	if m.reads.listingSeq > m.entry.fileReadSeq[name] {
		return
	}
	m.kept(name, current)
}

// kept carries a currently published page and its fingerprint forward.
func (m *snapshotMerge) kept(name string, current *domain.Page) {
	if current == nil {
		return
	}
	m.pages = append(m.pages, current)
	if fingerprint, ok := m.entry.fingerprints[name]; ok {
		m.fingerprints[name] = fingerprint
	}
}

// mergeSnapshot merges a build's reads into the entry's current snapshot in one
// O(n) pass over both ordered name sequences. The caller must hold the mutex.
func mergeSnapshot(
	e *entry,
	reads *snapshotReads,
) ([]*domain.Page, map[string]FileFingerprint) {
	current := e.snapshot
	m := &snapshotMerge{
		entry:        e,
		reads:        reads,
		pages:        make([]*domain.Page, 0, len(reads.order)+len(current)),
		fingerprints: make(map[string]FileFingerprint, len(reads.order)+len(current)),
	}
	i, j := 0, 0
	for i < len(reads.order) && j < len(current) {
		listed := reads.order[i]
		published := current[j].FileMetadata.Name + ".md"
		switch {
		case listed < published:
			m.listed(listed, nil)
			i++
		case listed > published:
			m.unlisted(published, current[j])
			j++
		default:
			m.listed(listed, current[j])
			i++
			j++
		}
	}
	for ; i < len(reads.order); i++ {
		m.listed(reads.order[i], nil)
	}
	for ; j < len(current); j++ {
		m.unlisted(current[j].FileMetadata.Name+".md", current[j])
	}
	if reads.missing && len(m.pages) == 0 {
		return nil, m.fingerprints
	}
	return m.pages, m.fingerprints
}
