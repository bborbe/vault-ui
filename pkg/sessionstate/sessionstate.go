// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionstate keeps the live sessions in memory so the board can
// answer a read without calling the attention store.
//
// The sessions come from the attention-controller's session-heartbeat endpoint
// (pkg/heartbeat), not from the harness session registry under
// `~/.claude/sessions`: the store is the single source every liveness reader
// shares, and it carries the heartbeats a cluster session posts, which that
// directory never sees.
//
// The state is read once at startup and re-read every DefaultRescanInterval
// afterwards. A read that cannot reach the store does not clear the sessions —
// it keeps the last known set and marks it no longer authoritative, so a caller
// can tell "no live sessions" (an answered store) from "cannot tell" (an
// unreachable one). Only the freshness mechanism lives here: the liveness
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
	"strings"
	"sync"
	"time"

	"github.com/bborbe/vault-ui/pkg/heartbeat"
)

// DefaultRescanInterval is the period between two reads of the live-session
// source.
const DefaultRescanInterval = 60 * time.Second

// State is the in-memory set of live sessions. It is safe for concurrent
// readers.
type State interface {
	// Sessions returns a copy of the current live sessions — ids and locations.
	// The result is never nil and never aliases the internal state.
	Sessions(ctx context.Context) []heartbeat.Session
	// LiveIDsKnown reports whether those sessions are authoritative. It is false
	// after a read that could not reach the store — "cannot tell", which a
	// caller must not read as "nothing is live". It starts true (an empty set is
	// the honest answer before the first read).
	LiveIDsKnown() bool
	// Replace atomically swaps the whole set, marks it authoritative and reports
	// whether the stored state changed — an id, a location or the authoritative
	// flag.
	Replace(sessions []heartbeat.Session) bool
	// MarkUnknown records that the live-session source could not be read. The
	// last known sessions are kept and stop being authoritative. It reports
	// whether the known flag changed.
	MarkUnknown() bool
}

// NewState creates an empty, authoritative State.
func NewState() State {
	return &state{sessions: []heartbeat.Session{}, known: true}
}

type state struct {
	mu       sync.RWMutex
	sessions []heartbeat.Session
	known    bool
}

// Sessions returns a copy of the current live sessions. The result is never nil
// and never aliases the internal state.
func (s *state) Sessions(_ context.Context) []heartbeat.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]heartbeat.Session{}, s.sessions...)
}

// LiveIDsKnown reports whether the current set is authoritative.
func (s *state) LiveIDsKnown() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.known
}

// Replace swaps the whole set and reports whether the stored state changed — an
// id, a location or the authoritative flag. The comparison is order-insensitive,
// duplicates are dropped and empty ids are ignored. A location move is a real
// change: it is what pushes a board refresh frame. A swap always marks the set
// authoritative again: a successful read is what clears a previous MarkUnknown,
// and that transition alone is worth a refresh frame.
func (s *state) Replace(sessions []heartbeat.Session) bool {
	normalized := normalize(sessions)

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !s.known || !slices.Equal(s.sessions, normalized)
	s.sessions = normalized
	s.known = true
	return changed
}

// MarkUnknown keeps the last known sessions and clears the authoritative flag.
// It reports whether the flag changed, so a caller pushes a refresh only on the
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

// normalize dedupes by id (the last row for an id wins), drops empty ids and
// sorts by id, so two spellings of the same set compare equal.
func normalize(sessions []heartbeat.Session) []heartbeat.Session {
	unique := make(map[string]string, len(sessions))
	for _, session := range sessions {
		if session.ID == "" {
			continue
		}
		unique[session.ID] = session.Location
	}
	normalized := make([]heartbeat.Session, 0, len(unique))
	for id, location := range unique {
		normalized = append(normalized, heartbeat.Session{ID: id, Location: location})
	}
	slices.SortFunc(normalized, func(a, b heartbeat.Session) int {
		return strings.Compare(a.ID, b.ID)
	})
	return normalized
}

// WatchParams carries the watcher's injectable dependencies.
type WatchParams struct {
	State State
	// Read returns the live sessions and whether the source answered. A false
	// second result is "cannot tell" (an unreachable store), never "nothing is
	// live".
	Read func(ctx context.Context) ([]heartbeat.Session, bool)
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
	sessions, known := w.params.Read(ctx)
	if !known {
		w.params.State.MarkUnknown()
		return
	}
	w.params.State.Replace(sessions)
}

// apply re-reads the source and swaps the state, pushing a refresh only when the
// stored set or its authoritative flag actually moved, so a no-op poll does not
// broadcast.
func (w *watcher) apply(ctx context.Context) {
	sessions, known := w.params.Read(ctx)
	if !known {
		if w.params.State.MarkUnknown() {
			w.params.Changed()
		}
		return
	}
	if w.params.State.Replace(sessions) {
		w.params.Changed()
	}
}
