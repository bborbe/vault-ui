// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionstate keeps the live session ids in memory so the board can
// answer a read without calling the attention store.
//
// The ids come from the attention-controller's session-heartbeat endpoint
// (pkg/heartbeat), not from the harness session registry under
// `~/.claude/sessions`: the store is the single source every liveness reader
// shares, and it carries the heartbeats a cluster session posts, which that
// directory never sees.
//
// The state is read once at startup and re-read every DefaultRescanInterval
// afterwards. A read that cannot reach the store does not clear the ids — it
// keeps the last known set and marks it no longer authoritative, so a caller can
// tell "no live sessions" (an answered store) from "cannot tell" (an unreachable
// one). Only the freshness mechanism lives here: the liveness classification
// contract (the four outcomes, the signal order, the five-minute window) is
// unchanged and lives in pkg/session, documented in docs/liveness-classification.md.
//
// See docs/pane-resolution.md for the staleness bounds and the refresh frames a
// change pushes to connected browsers.
package sessionstate

import (
	"context"
	"slices"
	"sync"
	"time"
)

// DefaultRescanInterval is the period between two reads of the live-id source.
const DefaultRescanInterval = 60 * time.Second

// State is the in-memory set of live session ids. It is safe for concurrent
// readers.
type State interface {
	// RegistrySessionIDs returns the live session ids currently known.
	RegistrySessionIDs(ctx context.Context) []string
	// LiveIDsKnown reports whether those ids are authoritative. It is false
	// after a read that could not reach the store — "cannot tell", which a
	// caller must not read as "nothing is live". It starts true (an empty set is
	// the honest answer before the first read).
	LiveIDsKnown() bool
	// Replace atomically swaps the whole set, marks it authoritative and reports
	// whether the stored state changed — the ids or the authoritative flag.
	Replace(ids []string) bool
	// MarkUnknown records that the live-id source could not be read. The last
	// known ids are kept and stop being authoritative. It reports whether the
	// known flag changed.
	MarkUnknown() bool
}

// NewState creates an empty, authoritative State.
func NewState() State {
	return &state{ids: []string{}, known: true}
}

type state struct {
	mu    sync.RWMutex
	ids   []string
	known bool
}

// RegistrySessionIDs returns a copy of the current set. The result is never nil
// and never aliases the internal slice.
func (s *state) RegistrySessionIDs(_ context.Context) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.ids...)
}

// LiveIDsKnown reports whether the current set is authoritative.
func (s *state) LiveIDsKnown() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.known
}

// Replace swaps the whole set and reports whether the stored state changed — the
// ids or the authoritative flag. The comparison is order-insensitive, duplicates
// are dropped and empty ids are ignored. A swap always marks the set
// authoritative again: a successful read is what clears a previous MarkUnknown,
// and that transition alone is worth a refresh frame.
func (s *state) Replace(ids []string) bool {
	normalized := normalize(ids)

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !s.known || !slices.Equal(s.ids, normalized)
	s.ids = normalized
	s.known = true
	return changed
}

// MarkUnknown keeps the last known ids and clears the authoritative flag. It
// reports whether the flag changed, so a caller pushes a refresh only on the
// transition into the unknown state.
func (s *state) MarkUnknown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known {
		return false
	}
	s.known = false
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

// WatchParams carries the watcher's injectable dependencies.
type WatchParams struct {
	State State
	// Read returns the live session ids and whether the source answered. A false
	// second result is "cannot tell" (an unreachable store), never "nothing is
	// live".
	Read func(ctx context.Context) ([]string, bool)
	// Changed is called when the stored set — or its authoritative flag — moves.
	Changed func()
	// Interval is the period between two reads. A non-positive value falls back
	// to DefaultRescanInterval.
	Interval time.Duration
}

// Watcher keeps State current from the live-id source.
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
}

// Run performs the initial read, then re-reads once per interval until ctx is
// cancelled.
//
// The initial read seeds the state without pushing a refresh: only a later
// change is worth a frame.
//
// A read that cannot reach the store is not fatal and never clears the ids: the
// state keeps the last known set and is marked unknown, and the loop retries on
// the next tick. The board reads that flag so an unreachable store renders
// "cannot tell" rather than a Resume.
func (w *watcher) Run(ctx context.Context) error {
	w.seed(ctx)

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

// seed performs the initial read without pushing a refresh frame.
func (w *watcher) seed(ctx context.Context) {
	ids, known := w.params.Read(ctx)
	if !known {
		w.params.State.MarkUnknown()
		return
	}
	w.params.State.Replace(ids)
}

// apply re-reads the source and swaps the state, pushing a refresh only when the
// stored set or its authoritative flag actually moved, so a no-op poll does not
// broadcast.
func (w *watcher) apply(ctx context.Context) {
	ids, known := w.params.Read(ctx)
	if !known {
		if w.params.State.MarkUnknown() {
			w.params.Changed()
		}
		return
	}
	if w.params.State.Replace(ids) {
		w.params.Changed()
	}
}
