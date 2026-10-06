// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"sync"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// snapshotBuildTimeout bounds one build so a hung vault read cannot hold the
// key's readers past it. It mirrors pkg/pageindex's rebuildTimeout.
const snapshotBuildTimeout = 2 * time.Minute

// taskSnapshotRow is one precomputed task row: the vault-cli list item plus
// every derived field that needs I/O. Fields that depend on the request's
// `now` (upcoming, recently_completed, the recently-completed phase override)
// are NOT precomputed; the read path applies them.
type taskSnapshotRow struct {
	item         ops.TaskListItem
	vault        Vault
	blockers     []string
	blocked      bool
	started      *string
	sessionState *string
	activityDate *libtime.DateTime
}

// IndexRevisions reports a per-key value that advances when the key's page
// snapshot is published or marked stale. pageindex.PageIndex satisfies it.
type IndexRevisions interface {
	Revision(key pageindex.Key) uint64
}

// generationSource reports a value that advances when a new session snapshot is
// available. sessionsnapshot.Snapshot satisfies it.
type generationSource interface {
	Generation() uint64
}

// taskSnapshotBuildFunc builds one key's rows from scratch. It runs off the
// store mutex and under its own bounded context.
type taskSnapshotBuildFunc func(ctx context.Context, vault Vault) ([]taskSnapshotRow, error)

// taskSnapshotParams carries the store's injectable dependencies.
type taskSnapshotParams struct {
	Build       taskSnapshotBuildFunc
	Revisions   IndexRevisions
	Generations generationSource
	// Timeout bounds one build; a non-positive value falls back to
	// snapshotBuildTimeout. It is an internal test seam, not a config knob.
	Timeout time.Duration
}

// taskSnapshotStore holds one published, precomputed row list per page-index
// key. A published list is never mutated: a rebuild assigns a brand-new slice,
// so a reader sees either the whole previous list or the whole new one.
type taskSnapshotStore struct {
	mu          sync.Mutex
	entries     map[pageindex.Key]*taskSnapshotEntry
	build       taskSnapshotBuildFunc
	revisions   IndexRevisions
	generations generationSource
	timeout     time.Duration
}

// taskSnapshotEntry is the per-key state. pageRevision and sessionGeneration
// are the inputs the published rows were built from; the entry is dirty while
// either has moved since.
type taskSnapshotEntry struct {
	rows              []taskSnapshotRow
	hasSnapshot       bool
	pageRevision      uint64
	sessionGeneration uint64
	inflight          *taskSnapshotBuild
}

// taskSnapshotBuild is one in-flight build. Its result fields are written under
// the store mutex before done is closed, so a waiter that returns from done
// observes them.
type taskSnapshotBuild struct {
	startRevision   uint64
	startGeneration uint64
	done            chan struct{}
	rows            []taskSnapshotRow
	err             error
}

// newTaskSnapshotStore creates an empty store. It starts empty and builds on
// demand; nothing is persisted across a restart.
func newTaskSnapshotStore(params taskSnapshotParams) *taskSnapshotStore {
	timeout := params.Timeout
	if timeout <= 0 {
		timeout = snapshotBuildTimeout
	}
	return &taskSnapshotStore{
		entries:     map[pageindex.Key]*taskSnapshotEntry{},
		build:       params.Build,
		revisions:   params.Revisions,
		generations: params.Generations,
		timeout:     timeout,
	}
}

// List returns the key's published rows. It rebuilds them when the page-index
// revision or the session generation moved since the published rows were
// built, and otherwise does no work at all.
//
// A caller that arrives while a build is in flight waits on that same build
// rather than starting a second one. The returned slice is shared with every
// other reader and must be treated as read-only.
func (s *taskSnapshotStore) List(ctx context.Context, vault Vault) ([]taskSnapshotRow, error) {
	key := pageindex.NewKey(vault.Path, vault.TasksFolder)
	for {
		s.mu.Lock()
		e := s.entryLocked(key)
		generation := s.generations.Generation()
		if e.hasSnapshot &&
			s.revisions.Revision(key) == e.pageRevision &&
			generation == e.sessionGeneration {
			rows := e.rows
			s.mu.Unlock()
			return rows, nil
		}
		if e.inflight != nil {
			b := e.inflight
			s.mu.Unlock()
			if err := awaitSnapshotBuild(ctx, b); err != nil {
				return nil, err
			}
			if b.err != nil {
				// The failed build kept whatever was published before it; a
				// cold key has nothing to serve and reports the build error.
				s.mu.Lock()
				rows, hasSnapshot := e.rows, e.hasSnapshot
				s.mu.Unlock()
				if hasSnapshot {
					return rows, nil
				}
				return nil, b.err
			}
			// The build may have been invalidated while it ran: re-evaluate.
			continue
		}
		b := &taskSnapshotBuild{
			startRevision:   s.revisions.Revision(key),
			startGeneration: generation,
			done:            make(chan struct{}),
		}
		e.inflight = b
		s.mu.Unlock()
		return s.runBuild(ctx, key, vault, b)
	}
}

// awaitSnapshotBuild waits for the build or for the caller to give up.
func awaitSnapshotBuild(ctx context.Context, b *taskSnapshotBuild) error {
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx, ctx.Err(), "wait for task snapshot build")
	}
}

// runBuild runs one build and publishes its result. It runs on the goroutine of
// the caller that owns the build, never under the store mutex, and finishes
// even when that caller's ctx is cancelled, so no waiter is left with a build
// nobody completes.
func (s *taskSnapshotStore) runBuild(
	ctx context.Context,
	key pageindex.Key,
	vault Vault,
	b *taskSnapshotBuild,
) ([]taskSnapshotRow, error) {
	buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)
	defer cancel()
	rows, buildErr := s.build(buildCtx, vault)

	s.mu.Lock()
	e := s.entryLocked(key)
	e.inflight = nil
	if buildErr != nil {
		b.err = buildErr
		glog.Errorf(
			"task-list snapshot rebuild failed for vault %q pages dir %q: %v",
			key.VaultPath,
			key.PagesDir,
			buildErr,
		)
		// The entry stays dirty: the revision and generation recorded at build
		// start are older than the current ones, so the next read retries.
		previous, hasSnapshot := e.rows, e.hasSnapshot
		close(b.done)
		s.mu.Unlock()
		if hasSnapshot {
			return previous, nil
		}
		return nil, errors.Wrapf(ctx, buildErr, "build task snapshot for vault %s", vault.Name)
	}

	e.rows = rows
	e.hasSnapshot = true
	e.pageRevision = b.startRevision
	e.sessionGeneration = b.startGeneration
	b.rows = rows
	close(b.done)
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(ctx, err, "task snapshot build")
	}
	return rows, nil
}

// entryLocked returns the key's entry, creating it when missing. The caller
// must hold the mutex.
func (s *taskSnapshotStore) entryLocked(key pageindex.Key) *taskSnapshotEntry {
	e, ok := s.entries[key]
	if !ok {
		e = &taskSnapshotEntry{}
		s.entries[key] = e
	}
	return e
}
