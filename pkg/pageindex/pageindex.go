// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pageindex keeps the parsed pages of each vault folder in memory so a
// list read can be answered without opening a vault file.
//
// The index implements vault-cli's storage.PageStorage and sits underneath
// vault-cli's own list operation: filtering, sorting and blocked-state logic
// stay there and only the page parsing is cached. It holds exactly one
// immutable snapshot ([]*domain.Page) per Key — a vault root plus a
// vault-relative pages dir. A published snapshot is never mutated; concurrent
// readers share the same *domain.Page pointers read-only.
//
// See docs/page-index.md for the staleness bounds, frame ordering and key
// derivation rules.
package pageindex

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
)

// RescanInterval is the time between full rescans of every known key. It is
// deliberately below the 60 s ceiling so that interval + rescanPollInterval +
// one rebuild stays inside it.
const RescanInterval = 50 * time.Second

// rescanPollInterval is the granularity at which the rescan loop checks the
// clock. It is not configurable.
const rescanPollInterval = time.Second

// rebuildTimeout bounds a single storage call so a hung filesystem cannot hold
// a key's rebuild — and the callers waiting on it — forever.
const rebuildTimeout = 2 * time.Minute

// Key identifies one indexed folder: a vault root plus a vault-relative pages dir.
type Key struct {
	VaultPath string
	PagesDir  string
}

// NewKey builds a Key, applying filepath.Clean to both parts so the read side
// and the event side derive identical keys from equivalent paths.
func NewKey(vaultPath, pagesDir string) Key {
	return Key{
		VaultPath: filepath.Clean(vaultPath),
		PagesDir:  filepath.Clean(pagesDir),
	}
}

//counterfeiter:generate -o ./mocks/pageindex-page-index.go --fake-name PageIndex . PageIndex

// PageIndex serves vault page lists from immutable in-memory snapshots.
type PageIndex interface {
	storage.PageStorage
	// Build warms the given keys, discarding the pages.
	Build(ctx context.Context, keys []Key) error
	// Refresh rebuilds the key's snapshot and blocks until the rebuild that
	// started after this call has been swapped in.
	Refresh(ctx context.Context, key Key) error
	// MarkDirty marks the keys stale so the next read performs a shared rebuild.
	MarkDirty(keys ...Key)
	// MarkAllDirty marks every known key stale.
	MarkAllDirty()
	// Rescan refreshes every known key once per RescanInterval until ctx is done.
	Rescan(ctx context.Context) error
}

// build is one rebuild of one key. It is created under the index mutex and its
// result fields are written under that mutex before done is closed.
type build struct {
	startSeq uint64
	started  chan struct{}
	done     chan struct{}
	pages    []*domain.Page
	err      error
}

func newBuild(startSeq uint64) *build {
	return &build{
		startSeq: startSeq,
		started:  make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// entry is the per-key state.
type entry struct {
	snapshot    []*domain.Page
	hasSnapshot bool
	snapshotSeq uint64

	// requestSeq is bumped by every Refresh and every dirty mark.
	requestSeq uint64
	// dirtySeq is requestSeq at the most recent dirty mark.
	dirtySeq uint64

	inflight *build
	followUp *build
}

type pageIndex struct {
	mu                    sync.Mutex
	entries               map[Key]*entry
	pageStorage           storage.PageStorage
	currentDateTimeGetter libtime.CurrentDateTimeGetter
	waiter                libtime.WaiterDuration
}

// NewPageIndex creates an empty page index over the given storage.
func NewPageIndex(
	pageStorage storage.PageStorage,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
) PageIndex {
	return &pageIndex{
		entries:               map[Key]*entry{},
		pageStorage:           pageStorage,
		currentDateTimeGetter: currentDateTimeGetter,
		waiter:                waiter,
	}
}

// ListPages returns the pages of the key's folder from the in-memory snapshot.
// It touches the underlying storage only when the key has no snapshot or was
// marked dirty.
func (p *pageIndex) ListPages(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]*domain.Page, error) {
	key := NewKey(vaultPath, pagesDir)
	pages, buildErr, cancelErr := p.ensure(ctx, key, false)
	if cancelErr != nil {
		return nil, cancelErr
	}
	if buildErr != nil {
		if snapshot, ok := p.snapshot(key); ok {
			return snapshot, nil
		}
		return nil, errors.Wrapf(
			ctx,
			buildErr,
			"list pages %s/%s",
			key.VaultPath,
			key.PagesDir,
		)
	}
	return pages, nil
}

// Build warms the given keys concurrently. A per-key failure is logged and does
// not fail the build; only a cancelled ctx makes Build return an error.
func (p *pageIndex) Build(ctx context.Context, keys []Key) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(ctx, err, "build page index")
	}
	funcs := make([]run.Func, 0, len(keys))
	for _, key := range keys {
		key := NewKey(key.VaultPath, key.PagesDir)
		funcs = append(funcs, func(ctx context.Context) error {
			_, _, cancelErr := p.ensure(ctx, key, false)
			return cancelErr
		})
	}
	if err := run.All(ctx, funcs...); err != nil {
		return errors.Wrap(ctx, err, "build page index")
	}
	return nil
}

// Refresh rebuilds the key and blocks until a rebuild that started after this
// call has been swapped in. It returns nil whether that rebuild succeeded or
// failed, and a wrapped error only when ctx was cancelled first.
func (p *pageIndex) Refresh(ctx context.Context, key Key) error {
	key = NewKey(key.VaultPath, key.PagesDir)
	_, _, cancelErr := p.ensure(ctx, key, true)
	return cancelErr
}

// MarkDirty marks the named keys stale. Keys the index has never seen are
// ignored: their first read builds them anyway.
func (p *pageIndex) MarkDirty(keys ...Key) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, key := range keys {
		key = NewKey(key.VaultPath, key.PagesDir)
		if e, ok := p.entries[key]; ok {
			e.requestSeq++
			e.dirtySeq = e.requestSeq
		}
	}
}

// MarkAllDirty marks every known key stale.
func (p *pageIndex) MarkAllDirty() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		e.requestSeq++
		e.dirtySeq = e.requestSeq
	}
}

// Rescan refreshes every known key once per RescanInterval until ctx is done.
// It never touches WebSocket frames.
func (p *pageIndex) Rescan(ctx context.Context) error {
	lastRescan := p.currentDateTimeGetter.Now()
	for {
		if err := p.waiter.Wait(ctx, libtime.Duration(rescanPollInterval)); err != nil {
			return nil
		}
		if p.currentDateTimeGetter.Now().Sub(lastRescan).Duration() < RescanInterval {
			continue
		}
		lastRescan = p.currentDateTimeGetter.Now()
		if err := p.refreshAll(ctx); err != nil {
			return nil
		}
	}
}

// refreshAll refreshes every known key concurrently and waits for all of them,
// so rescans never pile up.
func (p *pageIndex) refreshAll(ctx context.Context) error {
	keys := p.knownKeys()
	funcs := make([]run.Func, 0, len(keys))
	for _, key := range keys {
		funcs = append(funcs, func(ctx context.Context) error {
			return p.Refresh(ctx, key)
		})
	}
	return run.All(ctx, funcs...)
}

// knownKeys returns every key the index has an entry for.
func (p *pageIndex) knownKeys() []Key {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]Key, 0, len(p.entries))
	for key := range p.entries {
		keys = append(keys, key)
	}
	return keys
}

// snapshot returns the current published snapshot, if one exists.
func (p *pageIndex) snapshot(key Key) ([]*domain.Page, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if !ok || !e.hasSnapshot {
		return nil, false
	}
	return e.snapshot, true
}

// entryLocked returns the key's entry, creating it when missing. The caller
// must hold the mutex.
func (p *pageIndex) entryLocked(key Key) *entry {
	e, ok := p.entries[key]
	if !ok {
		e = &entry{}
		p.entries[key] = e
	}
	return e
}
