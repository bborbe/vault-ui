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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"
)

// RescanInterval is the time between stat-diff rescans of every known key. It
// is deliberately below the 60 s ceiling so that interval + rescanPollInterval
// + one read stays inside it.
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
	// Refresh re-reads the key's files whose size, modification time or
	// status-change time changed since they were last read, and blocks until a
	// listing that started after this call has been applied. It reads nothing
	// when nothing changed and publishes a new snapshot only when something
	// did. A key with no snapshot yet is built in full.
	Refresh(ctx context.Context, key Key) error
	// RefreshFile re-reads one file of the key's folder and publishes a new
	// snapshot with the result spliced in. It blocks until a snapshot
	// containing a read that started after this call has been published. The
	// file's current on-disk state decides the outcome, not the caller's reason
	// for calling: present and readable replaces or inserts the page at its
	// filename-ordered position, absent or unreadable removes it. The filename
	// is a single plain base name including ".md"; a filename that is empty,
	// contains a path separator, contains "..", or does not end in ".md" is
	// rejected with an error and nothing is read.
	RefreshFile(ctx context.Context, key Key, filename string) error
	// MarkDirty marks the named keys stale at folder level, so the next read of
	// each compares the folder's fingerprints and re-reads only what changed.
	MarkDirty(keys ...Key)
	// MarkFileDirty marks one file of the key stale so the next read re-reads it
	// before serving. The name is the item id; it is applied only when the
	// exactness rule holds, otherwise the whole key is marked folder-level.
	MarkFileDirty(key Key, name string)
	// ForceReload marks every known key so the next read of each re-reads every
	// file, whatever the fingerprints say. It is the only path that ignores
	// fingerprints.
	ForceReload()
	// Revision reports a per-key value that advances whenever the key's page
	// snapshot is published or marked stale. A derived snapshot reads it to
	// detect that its page input changed. An unknown key reports 0.
	Revision(key Key) uint64
	// Rescan refreshes every known key once per RescanInterval until ctx is done.
	Rescan(ctx context.Context) error
}

// buildKind is the work one build does for its key.
type buildKind int

const (
	// buildCold lists the folder and reads every file. It is used when the key
	// has no snapshot yet.
	buildCold buildKind = iota
	// buildStatDiff lists the folder, compares each entry's fingerprint with the
	// one recorded at its last read and re-reads only what changed.
	buildStatDiff
	// buildFileReads re-reads exactly the write-marked files and lists nothing.
	buildFileReads
	// buildReload lists the folder and re-reads every file, ignoring
	// fingerprints.
	buildReload
)

// build is one resolution of one key. It is created under the index mutex and
// its result fields are written under that mutex before done is closed.
type build struct {
	kind buildKind
	// reason is the metric label the creator wants for the reads this build
	// makes; a cold build and a reload override it.
	reason string
	// startSeq is the key's requestSeq when this build last started. A mark with
	// a higher sequence is not covered by this build.
	startSeq uint64
	started  chan struct{}
	done     chan struct{}
	pages    []*domain.Page
	err      error
}

func newBuild(kind buildKind, reason string, startSeq uint64) *build {
	return &build{
		kind:     kind,
		reason:   reason,
		startSeq: startSeq,
		started:  make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// covers reports whether this build, having started at startSeq, resolves work
// recorded at needSeq of the given kind.
func (b *build) covers(kind buildKind, needSeq uint64) bool {
	if b.startSeq < needSeq {
		return false
	}
	switch b.kind {
	case buildCold, buildReload:
		return true
	case buildFileReads:
		return kind == buildFileReads
	case buildStatDiff:
		return kind == buildStatDiff
	}
	return false
}

// entry is the per-key state.
type entry struct {
	snapshot     []*domain.Page
	hasSnapshot  bool
	fingerprints map[string]FileFingerprint

	// fileReadSeq holds, per base filename, the read sequence of the read whose
	// result is currently applied — the page present or removed. A name whose
	// page was removed keeps its entry as a tombstone; it is never deleted.
	fileReadSeq map[string]uint64

	// requestSeq is bumped by every refresh, mark and forced reload.
	requestSeq uint64
	// dirtySeq is requestSeq at the most recent folder-level mark; it is
	// resolved once a listing that started after it succeeded.
	dirtySeq uint64
	// dirtyResolvedSeq is requestSeq at the start of the most recent successful
	// listing — a stat-diff from any caller, a cold build or a forced reload.
	dirtyResolvedSeq uint64
	// reloadSeq is requestSeq at the most recent forced reload; it is resolved
	// only by a successful full re-read.
	reloadSeq uint64
	// reloadResolvedSeq is requestSeq at the start of the most recent successful
	// full re-read.
	reloadResolvedSeq uint64
	// writeMarked holds, per base filename, the requestSeq at which the file was
	// marked for a per-file re-read. An entry is taken out when its read starts.
	writeMarked map[string]uint64

	// revision advances whenever this key's page input can have changed: a mark
	// at folder or file level, a forced reload, or a published snapshot. A
	// derived snapshot polls it to detect that its page input changed.
	revision uint64

	inflight *build
	followUp *build
}

type pageIndex struct {
	mu      sync.Mutex
	entries map[Key]*entry
	// readSeq is bumped under mu at the start of every single-file read. It
	// orders those reads against each other and nothing else.
	readSeq               uint64
	reader                PageReader
	lister                DirectoryLister
	currentDateTimeGetter libtime.CurrentDateTimeGetter
	waiter                libtime.WaiterDuration
	// warnf emits the per-file exclusion warnings. It is a field so a test can
	// capture them per index instance.
	warnf func(format string, args ...any)
}

// NewPageIndex creates an empty page index over the given reader and lister.
func NewPageIndex(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
) PageIndex {
	return &pageIndex{
		entries:               map[Key]*entry{},
		reader:                reader,
		lister:                lister,
		currentDateTimeGetter: currentDateTimeGetter,
		waiter:                waiter,
		warnf:                 glog.Warningf,
	}
}

// ListPages returns the pages of the key's folder from the in-memory snapshot.
// It resolves any pending write mark before serving, so a reader sees the
// content its own write produced. A key whose only pending work is an event or
// a rescan read is served from the current snapshot without waiting.
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

// ReadPage returns one page of the key's folder by its bare base name, without
// the ".md" suffix — the value ListPages reports as the page's name.
//
// It serves the in-memory snapshot when that snapshot holds a page whose file
// name matches, and otherwise falls through to the PageReader seam. Unlike
// ListPages it reads only that one file and it fails when the file is missing
// or unparseable rather than skipping it: a caller naming one page must be told
// it is absent, not handed an empty result.
func (p *pageIndex) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	name string,
) (*domain.Page, error) {
	if !validFileMarkName(name) {
		return nil, errors.Errorf(ctx, "invalid page name %q", name)
	}
	key := NewKey(vaultPath, pagesDir)
	if pages, ok := p.snapshot(key); ok {
		if page := findSnapshotPage(pages, name); page != nil {
			return page, nil
		}
	}
	// The seam's fourth argument is a filename including the ".md" suffix, which
	// is the opposite of this method's bare base name.
	page, _, err := p.reader.ReadPage(ctx, vaultPath, pagesDir, name+".md")
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read page %s", name)
	}
	return page, nil
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

// Refresh re-reads the key's changed files and blocks until a listing that
// started after this call has been applied. It returns nil whether that
// listing succeeded or failed, and a wrapped error only when ctx was cancelled
// first.
func (p *pageIndex) Refresh(ctx context.Context, key Key) error {
	key = NewKey(key.VaultPath, key.PagesDir)
	_, _, cancelErr := p.ensure(ctx, key, true)
	return cancelErr
}

// MarkDirty marks the named keys stale at folder level. Keys the index has
// never seen are ignored: their first read builds them anyway.
func (p *pageIndex) MarkDirty(keys ...Key) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, key := range keys {
		key = NewKey(key.VaultPath, key.PagesDir)
		if e, ok := p.entries[key]; ok {
			e.requestSeq++
			e.dirtySeq = e.requestSeq
			e.revision++
		}
	}
}

// MarkFileDirty marks one file of the key stale so the next read re-reads it
// before serving. The name is the item id; it is applied only when the
// exactness rule holds, otherwise the whole key is marked folder-level.
//
// The exactness rule mirrors vault-cli's own file lookup: the id is taken with
// its "[["/"]]" wrapper stripped, it must be a plain base name without a path
// separator or "..", the key's current snapshot must hold a page whose name
// equals it byte-for-byte, and "<name>.md" must exist at mark time. Anything
// else may have been resolved by vault-cli's case-insensitive substring
// fallback, so the mark widens to the whole folder.
func (p *pageIndex) MarkFileDirty(key Key, name string) {
	key = NewKey(key.VaultPath, key.PagesDir)
	stripped := stripWikilinkName(name)

	p.mu.Lock()
	e, ok := p.entries[key]
	if !ok || !e.hasSnapshot || !validFileMarkName(stripped) ||
		!snapshotHasName(e.snapshot, stripped) {
		p.mu.Unlock()
		p.MarkDirty(key)
		return
	}
	p.mu.Unlock()

	filename := stripped + ".md"
	if _, err := os.Stat(filepath.Join(key.VaultPath, key.PagesDir, filename)); err != nil {
		p.MarkDirty(key)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	e = p.entryLocked(key)
	e.requestSeq++
	e.writeMarked[filename] = e.requestSeq
	e.revision++
}

// ForceReload marks every known key so the next read of each re-reads every
// file, whatever the fingerprints say.
func (p *pageIndex) ForceReload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		e.requestSeq++
		e.reloadSeq = e.requestSeq
		e.revision++
	}
}

// Revision reports the key's revision: a value that advances whenever the
// key's page snapshot is published or marked stale. An unknown key reports 0.
func (p *pageIndex) Revision(key Key) uint64 {
	key = NewKey(key.VaultPath, key.PagesDir)
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if !ok {
		return 0
	}
	return e.revision
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
		e = &entry{
			fileReadSeq: map[string]uint64{},
			writeMarked: map[string]uint64{},
		}
		p.entries[key] = e
	}
	return e
}

// takeReadSeq reserves the next single-file read sequence. The caller must not
// hold the mutex.
func (p *pageIndex) takeReadSeq() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readSeq++
	return p.readSeq
}

// stripWikilinkName removes the "[[" and "]]" wrapper an item id may carry,
// exactly as vault-cli's own file lookup does.
func stripWikilinkName(name string) string {
	name = strings.TrimPrefix(name, "[[")
	return strings.TrimSuffix(name, "]]")
}

// validFileMarkName rejects an id that must never be turned into a file path.
func validFileMarkName(name string) bool {
	return name != "" &&
		!strings.Contains(name, "..") &&
		!strings.ContainsRune(name, filepath.Separator) &&
		!strings.ContainsRune(name, '/')
}

// findSnapshotPage returns the snapshot's page whose file name equals name
// byte-for-byte, or nil when the snapshot holds none.
func findSnapshotPage(pages []*domain.Page, name string) *domain.Page {
	for _, page := range pages {
		if page.FileMetadata.Name == name {
			return page
		}
	}
	return nil
}

// snapshotHasName reports whether the snapshot holds a page whose file name
// equals name byte-for-byte.
func snapshotHasName(pages []*domain.Page, name string) bool {
	return findSnapshotPage(pages, name) != nil
}
