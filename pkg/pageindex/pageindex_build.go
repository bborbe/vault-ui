// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"
	"sort"

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
		if p.store != nil && !e.storeLoaded {
			return buildHydrate, e.requestSeq, true
		}
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
	case buildHydrate:
		p.executeHydrate(readCtx, key, b)
	default:
		p.executeListing(readCtx, key, b)
	}
	p.release(key, b)
	p.writeThrough(readCtx, key, b.delta)
}

// writeThrough persists one publication's delta outside the index mutex. It is
// called after the build is released, so no reader waits on store I/O. A failed
// write leaves the previous store content intact, does not affect what is
// served, and is reported with exactly one warning naming the failure.
func (p *pageIndex) writeThrough(ctx context.Context, key Key, delta *storeDelta) {
	if p.store == nil || delta == nil || (len(delta.puts) == 0 && len(delta.deletes) == 0) {
		return
	}
	if err := p.store.Write(ctx, key, delta.puts, delta.deletes); err != nil {
		p.warnf(
			"page index store write failed for %q/%q: %v",
			key.VaultPath,
			key.PagesDir,
			err,
		)
	}
}

// executeHydrate loads the key's stored entries, seeds them as the stat-diff
// baseline and runs one listing over them. The store read runs outside the
// index mutex. A store that cannot supply entries leaves the key on the
// full-parse path. A failed listing clears the seeded baseline so a stored
// snapshot is never served without its stat-diff.
func (p *pageIndex) executeHydrate(ctx context.Context, key Key, b *build) {
	entries, ok, err := p.store.Load(ctx, key)
	if err != nil {
		// A store that cannot supply entries leaves the key on the full-parse
		// path: a store failure never stops the index from starting or serving.
		ok = false
	}

	p.mu.Lock()
	e := p.entryLocked(key)
	if ok {
		seedStoredBaseline(e, entries)
	}
	e.storeLoaded = true
	p.mu.Unlock()

	p.executeListing(ctx, key, b)
}

// seedStoredBaseline fills the entry's snapshot and fingerprints from the stored
// entries, in the store's key order (byte-ascending file names, which equals
// os.ReadDir order). The caller must hold the mutex. hasSnapshot stays false:
// the baseline is an input to the stat-diff, never a published value.
func seedStoredBaseline(e *entry, entries []StoredEntry) {
	pages := make([]*domain.Page, 0, len(entries))
	fingerprints := make(map[string]FileFingerprint, len(entries))
	for _, entry := range entries {
		fingerprints[entry.Filename] = entry.Fingerprint
		if entry.Page != nil {
			pages = append(pages, entry.Page)
		}
	}
	e.snapshot = pages
	e.fingerprints = fingerprints
	e.storedBaseline = true
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

	var delta *storeDelta
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
		d := p.applyFileReadLocked(e, key, name, seq, page, fingerprint, readErr)
		p.mu.Unlock()
		delta = mergeStoreDeltas(delta, d)
	}

	p.mu.Lock()
	b.delta = delta
	p.mu.Unlock()
}

// mergeStoreDeltas returns a delta holding both inputs' puts and deletes; nil
// inputs contribute nothing and the result is nil when both are nil.
func mergeStoreDeltas(a, b *storeDelta) *storeDelta {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	merged := &storeDelta{
		puts:    make([]StoredEntry, 0, len(a.puts)+len(b.puts)),
		deletes: make([]string, 0, len(a.deletes)+len(b.deletes)),
	}
	merged.puts = append(merged.puts, a.puts...)
	merged.puts = append(merged.puts, b.puts...)
	merged.deletes = append(merged.deletes, a.deletes...)
	merged.deletes = append(merged.deletes, b.deletes...)
	return merged
}

// executeListing lists the key's folder and reads the files the build's kind
// asks for, then merges the result into the published snapshot.
//
// A pass whose listing matches the recorded fingerprints, whose name set
// matches the recorded name set and that has no file to re-read is an
// unchanged pass: it publishes nothing, leaves the published snapshot and the
// recorded fingerprints exactly in place, and allocates nothing beyond the one
// listing it performs. A pass with work to do re-reads exactly the changed,
// new and forced names and updates the recorded fingerprints for those names
// in place.
func (p *pageIndex) executeListing(ctx context.Context, key Key, b *build) {
	p.mu.Lock()
	e := p.entryLocked(key)
	startSeq := e.requestSeq
	b.startSeq = startSeq
	baseline := e.hasSnapshot || e.storedBaseline
	full := b.kind == buildCold || b.kind == buildReload
	if !baseline {
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
	case !baseline, b.kind == buildCold, b.kind == buildHydrate:
		reason = reasonBuild
	case b.kind == buildReload:
		reason = reasonReload
	}

	listingSeq := p.takeReadSeq()
	entries, listErr := p.lister.ListFiles(ctx, key.VaultPath, key.PagesDir)
	if listErr != nil {
		p.failListing(key, b, listErr)
		return
	}

	var toRead map[string]bool
	if !full {
		p.mu.Lock()
		e = p.entryLocked(key)
		for _, fe := range entries {
			recorded, ok := e.fingerprints[fe.Name]
			if forced[fe.Name] || !ok || recorded != fe.Fingerprint {
				if toRead == nil {
					toRead = make(map[string]bool)
				}
				toRead[fe.Name] = true
			}
		}
		// An unchanged pass requires a published snapshot to keep serving: a
		// hydrated baseline has recorded fingerprints but nothing published, so
		// the fast path would publish nothing and leave the key serving nothing.
		// The name set is equal exactly when the lengths match and every listed
		// entry is present and equal, and the missing/empty state did not flip.
		unchanged := len(forced) == 0 &&
			len(toRead) == 0 &&
			e.hasSnapshot &&
			len(entries) == len(e.fingerprints) &&
			(entries == nil) == (e.snapshot == nil)
		if unchanged {
			b.pages = e.snapshot
			e.dirtyResolvedSeq = startSeq
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
	}

	reads, collectErr := p.collect(ctx, key, listingSeq, entries, full, toRead, reason)
	if collectErr != nil {
		p.failListing(key, b, collectErr)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	b.pages = nil
	e = p.entryLocked(key)
	pages, dropped := mergeSnapshot(e, entries, reads)
	delta := listingDelta(reads, dropped)
	b.delta = delta
	if !e.hasSnapshot || !samePages(e.snapshot, pages) {
		e.snapshot = pages
		e.revision++
		// A publication is attributable to a bounded set of single files only when
		// it merged into an already-published snapshot. A key's first publication
		// publishes the whole snapshot whatever the build's kind, so it stays
		// unbounded: the hydrate path reaches here with full false but nothing
		// published, which e.hasSnapshot still reports before it is set below.
		e.recordChangeLocked(listingChangedNames(full, delta), !full && e.hasSnapshot)
		b.pages = pages
	} else {
		b.pages = e.snapshot
	}
	e.hasSnapshot = true
	e.storedBaseline = false
	e.dirtyResolvedSeq = startSeq
	if full {
		e.reloadResolvedSeq = startSeq
	}
}

// failListing records a listing or collection failure: nothing is published,
// the error is logged with the key, a stored baseline that was never
// stat-diffed is dropped, and the mark stays pending so the next read retries.
func (p *pageIndex) failListing(key Key, b *build, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b.pages = nil
	b.err = err
	e := p.entryLocked(key)
	glog.Errorf(
		"page index rebuild failed for vault %q pages dir %q: %v",
		key.VaultPath,
		key.PagesDir,
		err,
	)
	if e.storedBaseline {
		// The stored baseline was never stat-diffed; never serve it.
		e.snapshot = nil
		e.fingerprints = nil
		e.storedBaseline = false
		e.hasSnapshot = false
	}
}

// listingDelta returns the store change one listing produced: a put for every
// entry it re-read (including an excluded entry, stored as fingerprint plus a
// nil page) and a delete for every name the entry held before the merge that
// the merged set no longer holds. dropped is exactly those names, captured by
// mergeSnapshot before its in-place update removed them.
func listingDelta(reads *snapshotReads, dropped []string) *storeDelta {
	delta := &storeDelta{}
	for name, read := range reads.reads {
		delta.puts = append(delta.puts, StoredEntry{
			Filename:    name,
			Page:        read.page,
			Fingerprint: read.fingerprint,
		})
	}
	delta.deletes = append(delta.deletes, dropped...)
	return delta
}

// listingChangedNames returns the page names a listing's publication can be
// attributed to: every name the listing re-read and every name it dropped. A
// full listing — a cold build or a reload — re-reads the whole folder, so it
// cannot be attributed to a bounded set and reports none; the caller records it
// as unbounded instead.
func listingChangedNames(full bool, delta *storeDelta) []string {
	if full {
		return nil
	}
	names := make([]string, 0, len(delta.puts)+len(delta.deletes))
	for _, put := range delta.puts {
		names = append(names, put.Filename)
	}
	return append(names, delta.deletes...)
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
// itself started at, the K-sized map of each re-read file's result and whether
// the folder was missing. It holds no per-name order slice: the listing's
// []FileEntry supplies the order.
type snapshotReads struct {
	listingSeq uint64
	reads      map[string]fileRead
	// missing reports that the folder does not exist, which lists as nil.
	missing bool
}

// collect reads the listed entries that need it: every entry when full is set,
// otherwise only the names in toRead (the changed, new and forced names). It
// lists nothing and copies no recorded fingerprints, so its allocation is
// bounded by the entries it actually re-read. It returns the listing's
// sequence and the result and fingerprint of every entry it re-read, including
// the ones whose read failed.
func (p *pageIndex) collect(
	ctx context.Context,
	key Key,
	listingSeq uint64,
	entries []FileEntry,
	full bool,
	toRead map[string]bool,
	reason string,
) (*snapshotReads, error) {
	result := &snapshotReads{
		listingSeq: listingSeq,
		reads:      map[string]fileRead{},
		missing:    entries == nil,
	}
	skipped := 0
	for _, fe := range entries {
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(ctx, err, "build page snapshot")
		}
		if !full && !toRead[fe.Name] {
			continue
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

// snapshotMerge accumulates one listing's merge into a new snapshot and
// updates the entry's recorded fingerprints in place. The entry's current
// snapshot is the other input, so a read a later-started one has already
// superseded is never applied.
type snapshotMerge struct {
	entry   *entry
	reads   *snapshotReads
	pages   []*domain.Page
	dropped []string
}

// listed takes a listed file: the listing's own read when no later-started read
// has already been applied to that name, else the page currently published. A
// re-read updates the recorded fingerprint in place; a name carried forward
// keeps the fingerprint it already has.
func (m *snapshotMerge) listed(name string, current *domain.Page) {
	read, ok := m.reads.reads[name]
	if ok && read.seq >= m.entry.fileReadSeq[name] {
		m.entry.fileReadSeq[name] = read.seq
		m.entry.fingerprints[name] = read.fingerprint
		if read.page != nil {
			m.pages = append(m.pages, read.page)
		}
		return
	}
	m.kept(current)
}

// unlisted drops a name the listing no longer holds, unless the listing started
// before that name's last applied read — then a newer read put it there. A
// dropped name has its recorded fingerprint deleted in place so the recorded
// name set never keeps a tombstone the next listing could not match.
func (m *snapshotMerge) unlisted(name string, current *domain.Page) {
	if m.reads.listingSeq > m.entry.fileReadSeq[name] {
		m.remove(name)
		return
	}
	m.kept(current)
}

// kept carries a currently published page forward. A name the listing excludes
// has no page but keeps its fingerprint, so an unchanged broken file is neither
// re-read nor re-warned on the next listing.
func (m *snapshotMerge) kept(current *domain.Page) {
	if current != nil {
		m.pages = append(m.pages, current)
	}
}

// remove deletes one recorded fingerprint in place, recording it for the store
// delta when the prior value was non-zero — a zero fingerprint is the "never
// recorded" sentinel and has no stored entry to delete.
func (m *snapshotMerge) remove(name string) {
	previous, ok := m.entry.fingerprints[name]
	if !ok {
		return
	}
	delete(m.entry.fingerprints, name)
	if previous != (FileFingerprint{}) {
		m.dropped = append(m.dropped, name)
	}
}

// pruneFingerprints deletes every recorded fingerprint that is neither listed
// nor currently published — an excluded file the listing no longer holds, whose
// fingerprint the in-place update would otherwise keep forever. A tombstone
// kept past the pass would leave the recorded name set larger than any listing,
// so the unchanged fast path could never fire again.
func (m *snapshotMerge) pruneFingerprints(entries []FileEntry, current []*domain.Page) {
	for name := range m.entry.fingerprints {
		if listedContains(entries, name) || publishedContains(current, name) {
			continue
		}
		m.remove(name)
	}
}

// listedContains reports whether the sorted listing holds name.
func listedContains(entries []FileEntry, name string) bool {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].Name >= name })
	return i < len(entries) && entries[i].Name == name
}

// publishedContains reports whether the sorted snapshot holds name, whose
// ordering key is the page's file name plus ".md".
func publishedContains(pages []*domain.Page, name string) bool {
	i := sort.Search(len(pages), func(i int) bool {
		return pages[i].FileMetadata.Name+".md" >= name
	})
	return i < len(pages) && pages[i].FileMetadata.Name+".md" == name
}

// mergeSnapshot merges a listing's reads into the entry's current snapshot in
// one O(n) pass over both ordered name sequences and updates the entry's
// recorded fingerprints in place. It returns the new pages slice and the names
// whose recorded fingerprints it dropped. The caller must hold the mutex.
func mergeSnapshot(
	e *entry,
	entries []FileEntry,
	reads *snapshotReads,
) ([]*domain.Page, []string) {
	current := e.snapshot
	if e.fingerprints == nil {
		e.fingerprints = map[string]FileFingerprint{}
	}
	m := &snapshotMerge{
		entry: e,
		reads: reads,
		pages: make([]*domain.Page, 0, len(entries)),
	}
	i, j := 0, 0
	for i < len(entries) && j < len(current) {
		listed := entries[i].Name
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
	for ; i < len(entries); i++ {
		m.listed(entries[i].Name, nil)
	}
	for ; j < len(current); j++ {
		m.unlisted(current[j].FileMetadata.Name+".md", current[j])
	}
	m.pruneFingerprints(entries, current)
	if reads.missing && len(m.pages) == 0 {
		return nil, m.dropped
	}
	return m.pages, m.dropped
}
