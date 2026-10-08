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
	"github.com/bborbe/vault-cli/pkg/domain"
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
	item          ops.TaskListItem
	vault         Vault
	blockers      []string
	blocked       bool
	started       *string
	sessionState  *string
	activityDate  *libtime.DateTime
	openQuestions []domain.OpenQuestion
}

// IndexRevisions is the page index as the board's task-list store reads it: the
// per-key revision, which of the key's pages moved since a revision, and the
// key's current pages. pageindex.PageIndex satisfies it.
//
// It is deliberately narrow. A rebuild reads Revision to detect that its page
// input moved, ChangedPagesSince to bound which rows moved, and ListPages to
// re-derive those rows from the pages the index already holds. It exposes no
// write, no mark and no directory listing.
type IndexRevisions interface {
	Revision(key pageindex.Key) uint64
	// ChangedPagesSince returns the names of the pages whose parsed form changed
	// in the key's publications and marks after revision, each with its ".md"
	// suffix, or ok == false when the key cannot bound the set.
	ChangedPagesSince(key pageindex.Key, revision uint64) (names []string, ok bool)
	// ListPages returns the key's current pages, resolving any pending write
	// mark first, so a row patch reads the pages the index already holds rather
	// than the vault.
	ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
}

// generationSource reports a value that advances when a new session snapshot is
// available. sessionsnapshot.Snapshot satisfies it.
type generationSource interface {
	Generation() uint64
}

// taskSnapshotBuildFunc builds one key's rows from scratch. It runs off the
// store mutex and under its own bounded context.
type taskSnapshotBuildFunc func(ctx context.Context, vault Vault) ([]taskSnapshotRow, error)

// taskSnapshotRefreshFunc re-derives the fields that depend on the session
// snapshot, against rows that were already built. It runs off the store mutex
// and under its own bounded context, like Build.
//
// A session-snapshot move changes only the session-derived fields, so
// re-deriving those is enough: the vault list, the blockers and the Open
// Questions sections are page-derived and are unchanged by a session refresh.
// Rebuilding them anyway is what made a session move as expensive as a page
// move — the classified session state and the activity date are the whole of
// what actually moved.
type taskSnapshotRefreshFunc func(
	ctx context.Context,
	vault Vault,
	rows []taskSnapshotRow,
) ([]taskSnapshotRow, error)

// taskSnapshotPatchFunc re-derives only the rows whose pages the page index
// reports as changed, against the rows already built. It runs off the store
// mutex and under its own bounded context, like Build.
//
// names are the changed pages' base filenames including their ".md" suffix, as
// ChangedPagesSince reports them. It returns a new slice: the rows it is given
// are shared with every reader and are never mutated.
type taskSnapshotPatchFunc func(
	ctx context.Context,
	vault Vault,
	rows []taskSnapshotRow,
	names []string,
) ([]taskSnapshotRow, error)

// taskSnapshotParams carries the store's injectable dependencies.
type taskSnapshotParams struct {
	Build taskSnapshotBuildFunc
	// Refresh re-derives only the session-derived fields of rows already built.
	// A nil value means every invalidation takes the full build path, which is
	// correct but pays the page-derived work again on a session move.
	Refresh taskSnapshotRefreshFunc
	// Patch re-derives only the rows whose pages changed, when a page-index
	// revision move can be attributed to a bounded set of single files. A nil
	// value means every page move takes the full build path.
	Patch       taskSnapshotPatchFunc
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
	refresh     taskSnapshotRefreshFunc
	patch       taskSnapshotPatchFunc
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
	// refreshOnly records that only the session generation moved, so the held
	// rows can be re-derived instead of rebuilt from the vault.
	refreshOnly bool
	// patchOnly records that only the page revision moved and the changed set is
	// known, so the named rows can be re-derived instead of rebuilt.
	patchOnly bool
	// changed names the pages a patch re-derives, as base filenames including
	// their ".md" suffix. It is set only when patchOnly is set.
	changed []string
	done    chan struct{}
	rows    []taskSnapshotRow
	err     error
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
		refresh:     params.Refresh,
		patch:       params.Patch,
		revisions:   params.Revisions,
		generations: params.Generations,
		timeout:     timeout,
	}
}

// List returns the key's published rows.
//
// A page-index revision move rebuilds them from the vault, or — when the move
// can be attributed to a bounded set of single files — re-derives only those
// rows from the pages the index already holds. A session-generation move
// re-derives only the session-dependent fields against the rows already held,
// because those are the whole of what a session snapshot can change: the vault
// list, the blockers and the Open Questions sections are page-derived and do not
// move with it. A move of both at once takes the full build, since a row patch
// would publish session fields from before the session snapshot moved. Either
// way a caller that arrives while work is in flight waits on that same work
// rather than starting a second one, and the returned slice is shared with every
// other reader and must be treated as read-only.
func (s *taskSnapshotStore) List(ctx context.Context, vault Vault) ([]taskSnapshotRow, error) {
	key := pageindex.NewKey(vault.Path, vault.TasksFolder)
	for {
		s.mu.Lock()
		e := s.entryLocked(key)
		revision := s.revisions.Revision(key)
		generation := s.generations.Generation()
		pageMoved := !e.hasSnapshot || revision != e.pageRevision
		sessionMoved := !e.hasSnapshot || generation != e.sessionGeneration
		if !pageMoved && !sessionMoved {
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
		// The cheaper kinds are chosen top-down and each carries the qualifier
		// that keeps it honest. A page move that can be attributed to a bounded
		// set of single files is a row patch; a session-only move is a refresh;
		// both moved at once is neither — a patch on a both-moved key would
		// publish stale session fields — so the key rebuilds in full.
		//
		// A cold key has pageMoved and sessionMoved both set, so it never
		// reaches either cheaper kind: e.pageRevision is meaningless until a
		// snapshot has been built.
		refreshOnly := !pageMoved && sessionMoved && s.refresh != nil
		var changed []string
		patchOnly := false
		if pageMoved && !sessionMoved && s.patch != nil {
			names, ok := s.revisions.ChangedPagesSince(key, e.pageRevision)
			if ok {
				patchOnly = true
				changed = names
			}
		}
		b := &taskSnapshotBuild{
			startRevision:   revision,
			startGeneration: generation,
			refreshOnly:     refreshOnly,
			patchOnly:       patchOnly,
			changed:         changed,
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

	var rows []taskSnapshotRow
	var buildErr error
	switch {
	case b.patchOnly:
		// The held rows are read under the mutex and never mutated: Patch splices
		// the changed rows into a copy of them, so the slice a concurrent reader
		// is holding stays intact.
		s.mu.Lock()
		held := s.entryLocked(key).rows
		s.mu.Unlock()
		rows, buildErr = s.patch(buildCtx, vault, held, b.changed)
	case b.refreshOnly:
		// The held rows are read under the mutex and never mutated: Refresh
		// returns a new slice and copies each row before touching a field, so
		// the slice a concurrent reader is holding stays intact.
		s.mu.Lock()
		held := s.entryLocked(key).rows
		s.mu.Unlock()
		rows, buildErr = s.refresh(buildCtx, vault, held)
	default:
		rows, buildErr = s.build(buildCtx, vault)
	}

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
