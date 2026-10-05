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

// execute runs the storage call and publishes its result. It runs on the
// goroutine of the caller that owns the build and finishes even when that
// caller's ctx is cancelled.
func (p *pageIndex) execute(ctx context.Context, key Key, b *build) {
	buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)
	defer cancel()
	pages, err := p.pageStorage.ListPages(buildCtx, key.VaultPath, key.PagesDir)

	p.mu.Lock()
	defer p.mu.Unlock()
	b.pages = pages
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
		e.snapshot = pages
		e.hasSnapshot = true
		e.snapshotSeq = b.startSeq
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
