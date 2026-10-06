// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionstate keeps the harness session registry's live session ids in
// memory so the board can answer a read without opening the registry directory.
//
// The state is read once at startup, re-read on every file event under the
// registry directory, and re-read every DefaultRescanInterval as the safety net
// for a missed event. Only the freshness mechanism lives here: the liveness
// classification contract (the four outcomes, the signal order, the five-minute
// window) is unchanged and lives in pkg/session, documented in
// docs/liveness-classification.md.
//
// See docs/pane-resolution.md for the staleness bounds and the refresh frames a
// change pushes to connected browsers.
package sessionstate

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
)

// DefaultRescanInterval is the safety-net rescan period for a missed file event.
const DefaultRescanInterval = 60 * time.Second

// State is the in-memory set of live registry session ids. It is safe for
// concurrent readers.
type State interface {
	// RegistrySessionIDs returns the live session ids currently known.
	RegistrySessionIDs(ctx context.Context) []string
	// Replace atomically swaps the whole set and reports whether it changed.
	Replace(ids []string) bool
}

// NewState creates an empty State.
func NewState() State {
	return &state{ids: []string{}}
}

type state struct {
	mu  sync.RWMutex
	ids []string
}

// RegistrySessionIDs returns a copy of the current set. The result is never nil
// and never aliases the internal slice.
func (s *state) RegistrySessionIDs(_ context.Context) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.ids...)
}

// Replace swaps the whole set and reports whether it changed. The comparison is
// order-insensitive, duplicates are dropped and empty ids are ignored.
func (s *state) Replace(ids []string) bool {
	normalized := normalize(ids)

	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(s.ids, normalized) {
		return false
	}
	s.ids = normalized
	return true
}

// normalize dedupes, drops empty ids and sorts, so two spellings of the same
// set compare equal.
func normalize(ids []string) []string {
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		unique[id] = struct{}{}
	}
	normalized := make([]string, 0, len(unique))
	for id := range unique {
		normalized = append(normalized, id)
	}
	slices.Sort(normalized)
	return normalized
}

// Source signals that a watched directory may have changed.
type Source interface {
	// Watch calls changed once per change under dir, until ctx is cancelled.
	// It returns nil on cancellation and an error otherwise.
	Watch(ctx context.Context, dir string, changed func()) error
}

// NewFSNotifySource returns the fsnotify-backed Source.
func NewFSNotifySource() Source {
	return &fsNotifySource{}
}

type fsNotifySource struct{}

// Watch mirrors the select loop of vault-cli's watch operation: ctx.Done ends
// the watch with nil, a closed channel ends it with nil, and an error on the
// error channel is logged and the loop keeps going.
func (s *fsNotifySource) Watch(ctx context.Context, dir string, changed func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Wrap(ctx, err, "create session registry watcher")
	}
	defer func() { _ = watcher.Close() }()

	// A missing directory is not fatal: the caller logs this and the rescan
	// keeps the state current until the directory reappears.
	if addErr := watcher.Add(dir); addErr != nil {
		return errors.Wrapf(ctx, addErr, "watch session registry %s", dir)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			glog.V(2).Infof("session registry watch error for %s: %v", dir, watchErr)
		case _, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			changed()
		}
	}
}

// WatchParams carries the watcher's injectable dependencies.
type WatchParams struct {
	Dir      string
	State    State
	Source   Source
	Read     func(ctx context.Context, dir string) []string
	Changed  func()
	Interval time.Duration
}

// Watcher keeps State current from the registry directory.
type Watcher interface {
	Run(ctx context.Context) error
}

// NewWatcher creates a Watcher. A non-positive Interval falls back to
// DefaultRescanInterval.
func NewWatcher(params WatchParams) Watcher {
	interval := params.Interval
	if interval <= 0 {
		interval = DefaultRescanInterval
	}
	return &watcher{params: params, interval: interval}
}

type watcher struct {
	params   WatchParams
	interval time.Duration

	// applyMu serialises the file-event path and the rescan path. Without it a
	// rescan whose read began before a file event can Replace after the event's
	// fresher read and revert the state to a stale set for up to Interval — a
	// logic race the race detector cannot see.
	applyMu sync.Mutex
}

// Run performs the initial read, then keeps the state current from file events
// and from a periodic rescan until ctx is cancelled.
//
// A failing or panicking Source must not take the board down: the watch error
// is logged at V(2) and swallowed, so the rescan keeps the state current and
// the board keeps serving. The detection is that V(2) line.
func (w *watcher) Run(ctx context.Context) error {
	// The initial read is not an event: it seeds the state without pushing a
	// refresh frame. Only a later change is worth a broadcast.
	w.params.State.Replace(w.params.Read(ctx, w.params.Dir))

	watchFunc := func(ctx context.Context) error {
		err := w.params.Source.Watch(ctx, w.params.Dir, func() { w.apply(ctx) })
		if err != nil {
			glog.V(2).Infof("session registry watch on %s stopped: %v", w.params.Dir, err)
		}
		return nil
	}

	rescanFunc := func(ctx context.Context) error {
		timer := time.NewTimer(w.interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
				w.apply(ctx)
				timer.Reset(w.interval)
			}
		}
	}

	return run.CancelOnFirstErrorWait(
		ctx,
		run.SkipErrors(run.CatchPanic(watchFunc)),
		rescanFunc,
	)
}

// apply re-reads the registry and swaps the state, pushing a refresh only when
// the live set actually changed so a no-op rescan does not broadcast.
func (w *watcher) apply(ctx context.Context) {
	w.applyMu.Lock()
	defer w.applyMu.Unlock()

	if w.params.State.Replace(w.params.Read(ctx, w.params.Dir)) {
		w.params.Changed()
	}
}
