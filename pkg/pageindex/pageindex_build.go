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

// ensure returns the key's pages, resolving whatever work is pending for it.
//
// It returns the build error separately from a cancellation of the caller's own
// ctx: a failed build is not an error for a caller that can still serve a
// previous snapshot, while a cancelled caller must not be handed stale pages.
//
// With refresh set, the caller wants a stat-diff that started after this call
// and gets one; without it the caller blocks only while a mark is pending and
// is served from the current snapshot otherwise.
func (p *pageIndex) ensure(
	ctx context.Context,
	key Key,
	refresh bool,
) ([]*domain.Page, error, error) {
	reason := reasonWrite
	if refresh {
		reason = reasonRescan
	}
	p.mu.Lock()
	e := p.entryLocked(key)
	if refresh {
		e.requestSeq++
	}
	// Only work marked before this call is resolved here: a mark recorded while
	// a read is in flight stays pending for the next reader.
	entryNeedSeq := e.requestSeq
	p.mu.Unlock()

	var last []*domain.Page
	for first := true; ; first = false {
		p.mu.Lock()
		e = p.entryLocked(key)
		kind, needSeq, pending := p.pendingLocked(e, refresh)
		if !pending || (!first && needSeq > entryNeedSeq) {
			snapshot := e.snapshot
			p.mu.Unlock()
			if first {
				return snapshot, nil, nil
			}
			return last, nil, nil
		}
		b, callerRole := p.acquireLocked(e, kind, reason, needSeq)
		p.mu.Unlock()

		pages, buildErr, cancelErr := p.await(ctx, key, b, callerRole)
		if cancelErr != nil {
			return nil, nil, cancelErr
		}
		last = pages
		if buildErr != nil {
			// A failed listing leaves the mark pending for the next reader.
			return pages, buildErr, nil
		}
		if refresh {
			if b.covers(buildStatDiff, needSeq) {
				return pages, nil, nil
			}
			// The build that was in flight re-reads files without listing the
			// folder, so it cannot satisfy a refresh: wait for one that does.
			continue
		}
	}
}

// pendingLocked reports the work the key needs next: a cold build when it has
// no snapshot, otherwise the highest-priority pending mark. The caller must
// hold the mutex.
func (p *pageIndex) pendingLocked(e *entry, refresh bool) (buildKind, uint64, bool) {
	if !e.hasSnapshot {
		return buildCold, e.requestSeq, true
	}
	if refresh {
		return buildStatDiff, e.requestSeq, true
	}
	if e.reloadSeq > e.reloadResolvedSeq {
		return buildReload, e.reloadSeq, true
	}
	if len(e.writeMarked) > 0 {
		return buildFileReads, maxMarkedSeq(e.writeMarked), true
	}
	if e.fileReadsInflightLocked() {
		// A per-file re-read is already running: join it rather than serving a
		// snapshot the mark was recorded against.
		return buildFileReads, 0, true
	}
	if e.dirtySeq > e.dirtyResolvedSeq {
		return buildStatDiff, e.dirtySeq, true
	}
	return 0, 0, false
}

// fileReadsInflightLocked reports whether a per-file re-read is running or
// queued for the key. The caller must hold the mutex.
func (e *entry) fileReadsInflightLocked() bool {
	return (e.inflight != nil && e.inflight.kind == buildFileReads) ||
		(e.followUp != nil && e.followUp.kind == buildFileReads)
}

// maxMarkedSeq returns the highest requestSeq in a write-mark map.
func maxMarkedSeq(marked map[string]uint64) uint64 {
	var max uint64
	for _, seq := range marked {
		if seq > max {
			max = seq
		}
	}
	return max
}

// acquireLocked picks the build the caller waits on and starts one when none
// covers its need. The caller must hold the mutex.
func (p *pageIndex) acquireLocked(
	e *entry,
	kind buildKind,
	reason string,
	needSeq uint64,
) (*build, role) {
	if e.inflight != nil && e.inflight.covers(kind, needSeq) {
		return e.inflight, roleWait
	}
	if e.followUp != nil {
		return e.followUp, roleWait
	}
	if e.inflight != nil {
		return p.queueFollowUpLocked(e, kind, reason), roleFollowUp
	}
	b := newBuild(kind, reason, e.requestSeq)
	e.inflight = b
	return b, roleRun
}

// queueFollowUpLocked appends the one queued build a key may have. The caller
// must hold the mutex.
func (p *pageIndex) queueFollowUpLocked(e *entry, kind buildKind, reason string) *build {
	b := newBuild(kind, reason, e.requestSeq)
	e.followUp = b
	return b
}

// await waits for or runs the build the caller acquired.
func (p *pageIndex) await(
	ctx context.Context,
	key Key,
	b *build,
	callerRole role,
) ([]*domain.Page, error, error) {
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
		// build, and nobody else would run it.
		<-b.started
	}
	p.execute(ctx, key, b)
	if err := ctx.Err(); err != nil {
		return nil, b.err, errors.Wrap(ctx, err, "page index build")
	}
	return b.pages, b.err, nil
}

// execute runs the build's read and publishes its result. It runs on the
// goroutine of the caller that owns the build and finishes even when that
// caller's ctx is cancelled.
func (p *pageIndex) execute(ctx context.Context, key Key, b *build) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)
	defer cancel()

	switch b.kind {
	case buildFileReads:
		p.executeFileReads(readCtx, key, b)
	default:
		p.executeListing(readCtx, key, b)
	}
	p.release(key, b)
}

// executeFileReads re-reads exactly the write-marked files. The marks are taken
// out under the lock as the read starts, so a mark recorded during the read
// stays pending for the next one.
func (p *pageIndex) executeFileReads(ctx context.Context, key Key, b *build) {
	p.mu.Lock()
	e := p.entryLocked(key)
	startSeq := e.requestSeq
	b.startSeq = startSeq
	names := make([]string, 0, len(e.writeMarked))
	for name, seq := range e.writeMarked {
		if seq <= startSeq {
			names = append(names, name)
			delete(e.writeMarked, name)
		}
	}
	p.mu.Unlock()

	for _, name := range names {
		seq := p.takeReadSeq()
		page, fingerprint, readErr := p.reader.ReadPage(
			ctx,
			key.VaultPath,
			key.PagesDir,
			name,
		)
		recordRead(reasonWrite)

		p.mu.Lock()
		p.applyFileReadLocked(e, key, name, seq, page, fingerprint, readErr)
		p.mu.Unlock()
	}
}

// executeListing lists the key's folder and reads the files the build's kind
// asks for, then merges the result into the published snapshot.
func (p *pageIndex) executeListing(ctx context.Context, key Key, b *build) {
	p.mu.Lock()
	e := p.entryLocked(key)
	startSeq := e.requestSeq
	b.startSeq = startSeq
	hadSnapshot := e.hasSnapshot
	full := b.kind == buildCold || b.kind == buildReload
	if !hadSnapshot {
		full = true
	}
	var forced map[string]bool
	if !full {
		for name, seq := range e.writeMarked {
			if seq <= startSeq {
				if forced == nil {
					forced = map[string]bool{}
				}
				forced[name] = true
				delete(e.writeMarked, name)
			}
		}
	} else {
		for name, seq := range e.writeMarked {
			if seq <= startSeq {
				delete(e.writeMarked, name)
			}
		}
	}
	p.mu.Unlock()

	reason := b.reason
	switch {
	case !hadSnapshot, b.kind == buildCold:
		reason = reasonBuild
	case b.kind == buildReload:
		reason = reasonReload
	}
	reads, err := p.collect(ctx, key, full, forced, reason)

	p.mu.Lock()
	defer p.mu.Unlock()
	b.pages = nil
	b.err = err
	e = p.entryLocked(key)
	if err != nil {
		glog.Errorf(
			"page index rebuild failed for vault %q pages dir %q: %v",
			key.VaultPath,
			key.PagesDir,
			err,
		)
		return
	}
	pages, fingerprints := mergeSnapshot(e, reads)
	e.fingerprints = fingerprints
	if !e.hasSnapshot || !samePages(e.snapshot, pages) {
		e.snapshot = pages
		b.pages = pages
	} else {
		b.pages = e.snapshot
	}
	e.hasSnapshot = true
	e.dirtyResolvedSeq = startSeq
	if full {
		e.reloadResolvedSeq = startSeq
	}
}

// release clears the finished build and promotes the key's follow-up, so no
// caller can slip a parallel build into the gap.
func (p *pageIndex) release(key Key, b *build) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entryLocked(key)
	if b.pages == nil && b.err == nil && e.hasSnapshot {
		// A per-file re-read publishes by splicing; its callers still need the
		// snapshot that is current once it is done.
		b.pages = e.snapshot
	}
	if e.inflight == b {
		e.inflight = nil
	}
	if e.followUp != nil {
		next := e.followUp
		e.followUp = nil
		next.startSeq = e.requestSeq
		e.inflight = next
		close(next.started)
	}
	close(b.done)
}

// maxUnreadablePageWarnings is the number of per-file skip warnings a single
// listing emits before it stops naming files individually and prints one
// summary line instead. It bounds a folder full of unreadable pages.
const maxUnreadablePageWarnings = 10

// fileRead is one listed file's read result within a listing. A nil page means
// the read failed, so the file is excluded.
type fileRead struct {
	seq         uint64
	page        *domain.Page
	fingerprint FileFingerprint
}

// snapshotReads is one listing's collected reads: the sequence the listing
// itself started at, the listing's base filenames in order and each re-read
// file's result.
type snapshotReads struct {
	listingSeq uint64
	order      []string
	reads      map[string]fileRead
	// missing reports that the folder does not exist, which lists as nil.
	missing bool
}

// collect lists the key's folder and reads the entries that need it: every
// entry when full is set, every forced entry, and otherwise only the entries
// whose fingerprint differs from the one recorded at their last read. It
// returns the listing order plus the result and fingerprint of every entry it
// re-read, including the ones whose read failed.
func (p *pageIndex) collect(
	ctx context.Context,
	key Key,
	full bool,
	forced map[string]bool,
	reason string,
) (*snapshotReads, error) {
	listingSeq := p.takeReadSeq()
	entries, err := p.lister.ListFiles(ctx, key.VaultPath, key.PagesDir)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		// A missing folder lists as nil, matching vault-cli's nil-vs-empty.
		return &snapshotReads{listingSeq: listingSeq, missing: true}, nil
	}
	recorded := p.recordedFingerprints(key)

	result := &snapshotReads{
		listingSeq: listingSeq,
		order:      make([]string, 0, len(entries)),
		reads:      make(map[string]fileRead, len(entries)),
	}
	skipped := 0
	for _, fe := range entries {
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(ctx, err, "build page snapshot")
		}
		result.order = append(result.order, fe.Name)
		if !full && !forced[fe.Name] {
			previous, ok := recorded[fe.Name]
			if ok && previous == fe.Fingerprint {
				continue
			}
		}
		seq := p.takeReadSeq()
		page, fingerprint, readErr := p.reader.ReadPage(
			ctx,
			key.VaultPath,
			key.PagesDir,
			fe.Name,
		)
		recordRead(reason)
		result.reads[fe.Name] = fileRead{seq: seq, page: page, fingerprint: fingerprint}
		if readErr != nil {
			if skipped < maxUnreadablePageWarnings {
				p.warnf(
					"skipping unreadable page %q in %q/%q: %v",
					fe.Name,
					key.VaultPath,
					key.PagesDir,
					readErr,
				)
			}
			skipped++
		}
	}
	if skipped >= maxUnreadablePageWarnings {
		p.warnf("skipping %d unreadable pages", skipped)
	}
	return result, nil
}

// recordedFingerprints copies the key's recorded fingerprints so the comparison
// runs without holding the mutex across the reads.
func (p *pageIndex) recordedFingerprints(key Key) map[string]FileFingerprint {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if !ok {
		return nil
	}
	recorded := make(map[string]FileFingerprint, len(e.fingerprints))
	for name, fingerprint := range e.fingerprints {
		recorded[name] = fingerprint
	}
	return recorded
}

// samePages reports whether two snapshots hold the same pages in the same
// order, distinguishing a nil snapshot from an empty one.
func samePages(a, b []*domain.Page) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// snapshotMerge accumulates one listing's merge into a new snapshot. The
// entry's current snapshot is the other input, so a read a later-started one
// has already superseded is never applied.
type snapshotMerge struct {
	entry        *entry
	reads        *snapshotReads
	pages        []*domain.Page
	fingerprints map[string]FileFingerprint
}

// listed takes a listed file: the listing's own read when no later-started read
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

// kept carries a currently published page and its fingerprint forward. A name
// the listing excludes has no page but keeps its fingerprint, so an unchanged
// broken file is neither re-read nor re-warned on the next listing.
func (m *snapshotMerge) kept(name string, current *domain.Page) {
	if current != nil {
		m.pages = append(m.pages, current)
	}
	if fingerprint, ok := m.entry.fingerprints[name]; ok {
		m.fingerprints[name] = fingerprint
	}
}

// mergeSnapshot merges a listing's reads into the entry's current snapshot in
// one O(n) pass over both ordered name sequences. The caller must hold the
// mutex.
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
