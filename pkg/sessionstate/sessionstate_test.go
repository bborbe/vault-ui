// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionstate_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/heartbeat"
	"github.com/bborbe/vault-ui/pkg/sessionstate"
)

// counter is a tiny atomic-ish call counter shared by the read doubles.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) next() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// liveSession builds one live session with the given location.
func liveSession(id, location string) heartbeat.Session {
	return heartbeat.Session{ID: id, Location: location}
}

// startWatcher runs the watcher in the background and returns its result
// channel plus a cancel that the caller must register as cleanup.
func startWatcher(
	ctx context.Context,
	params sessionstate.WatchParams,
) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		done <- sessionstate.NewWatcher(params).Run(ctx)
	}()
	return done
}

// sequenceRead serves a fresh (sessions, known) pair per call, so a spec can
// move the source between polls.
type sequenceRead struct {
	mu      sync.Mutex
	results []readResult
	calls   int
}

type readResult struct {
	sessions []heartbeat.Session
	known    bool
}

func newSequenceRead(results ...readResult) *sequenceRead {
	return &sequenceRead{results: results}
}

// Get returns the next scripted result, repeating the last one once exhausted.
func (r *sequenceRead) Get(_ context.Context) ([]heartbeat.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.results[len(r.results)-1]
	if r.calls < len(r.results) {
		result = r.results[r.calls]
	}
	r.calls++
	return result.sessions, result.known
}

func (r *sequenceRead) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

var _ = Describe("State", func() {
	It("reports a change on a different set and no change on a reordered one", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("b", "pod"),
		})).To(BeTrue())
		Expect(state.Replace([]heartbeat.Session{
			liveSession("b", "pod"), liveSession("a", "local"),
		})).To(BeFalse())
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("b", "pod"), liveSession("c", "local"),
		})).To(BeTrue())
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("b", "pod"),
		})).To(BeTrue())
	})

	It("reports a change when only a location moved", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{liveSession("a", "local")})).To(BeTrue())
		// The id set is unchanged, but the location moved — that is what pushes a
		// board refresh frame.
		Expect(state.Replace([]heartbeat.Session{liveSession("a", "pod")})).To(BeTrue())
		Expect(state.Sessions(context.Background())).To(Equal(
			[]heartbeat.Session{liveSession("a", "pod")},
		))
	})

	It("reports no change when the same id/location set arrives reordered", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("b", "pod"),
		})).To(BeTrue())
		Expect(state.Replace([]heartbeat.Session{
			liveSession("b", "pod"), liveSession("a", "local"),
		})).To(BeFalse())
	})

	It("drops duplicates and empty ids", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("a", "local"), liveSession("", "pod"),
			liveSession("b", "pod"), liveSession("", ""),
		})).To(BeTrue())
		Expect(state.Sessions(context.Background())).To(ConsistOf(
			liveSession("a", "local"), liveSession("b", "pod"),
		))
		Expect(state.Replace([]heartbeat.Session{
			liveSession("b", "pod"), liveSession("a", "local"),
		})).To(BeFalse())
	})

	It("keeps the last row for an id when the same id is reported twice", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("a", "pod"),
		})).To(BeTrue())
		Expect(state.Sessions(context.Background())).To(Equal(
			[]heartbeat.Session{liveSession("a", "pod")},
		))
	})

	It("starts empty and never returns nil", func() {
		state := sessionstate.NewState()
		sessions := state.Sessions(context.Background())
		Expect(sessions).NotTo(BeNil())
		Expect(sessions).To(BeEmpty())
	})

	It("returns each stored location paired with its own id", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("b", "pod"),
		})).To(BeTrue())

		sessions := state.Sessions(context.Background())
		byID := map[string]string{}
		for _, session := range sessions {
			byID[session.ID] = session.Location
		}
		Expect(byID).To(Equal(map[string]string{"a": "local", "b": "pod"}))
	})

	It("returns a copy that does not alias the stored set", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{liveSession("a", "local")})).To(BeTrue())

		sessions := state.Sessions(context.Background())
		sessions[0] = liveSession("mutated", "pod")
		Expect(state.Sessions(context.Background())).To(Equal(
			[]heartbeat.Session{liveSession("a", "local")},
		))
	})

	It("starts authoritative and marks unknown only once", func() {
		state := sessionstate.NewState()
		Expect(state.LiveIDsKnown()).To(BeTrue())

		Expect(state.MarkUnknown()).To(BeTrue())
		Expect(state.LiveIDsKnown()).To(BeFalse())
		// A second mark is a no-op: only the transition is worth a refresh.
		Expect(state.MarkUnknown()).To(BeFalse())
	})

	It("keeps the last known sessions and their locations when it is marked unknown", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{
			liveSession("a", "local"), liveSession("b", "pod"),
		})).To(BeTrue())
		state.MarkUnknown()

		Expect(state.Sessions(context.Background())).To(ConsistOf(
			liveSession("a", "local"), liveSession("b", "pod"),
		))
		Expect(state.LiveIDsKnown()).To(BeFalse())
	})

	It("becomes authoritative again on the next successful replace", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]heartbeat.Session{liveSession("a", "local")})).To(BeTrue())
		state.MarkUnknown()

		Expect(state.Replace([]heartbeat.Session{liveSession("a", "local")})).
			To(BeTrue(), "known flag moved back")
		Expect(state.LiveIDsKnown()).To(BeTrue())
	})
})

var _ = Describe("Watcher", func() {
	It("seeds from the initial read without pushing a refresh", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		read := newSequenceRead(readResult{
			sessions: []heartbeat.Session{liveSession("seed", "local")}, known: true,
		})
		var changedCount counter

		startWatcher(ctx, sessionstate.WatchParams{
			State:    state,
			Read:     read.Get,
			Changed:  func() { changedCount.next() },
			Interval: time.Hour,
		})

		Eventually(func() []heartbeat.Session { return state.Sessions(ctx) }).
			Should(ConsistOf(liveSession("seed", "local")))
		// The seed is not an event: a stable set never pushes a frame.
		Consistently(changedCount.get, 150*time.Millisecond).Should(Equal(0))
	})

	It("pushes a refresh only on a real change", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		read := newSequenceRead(
			readResult{sessions: []heartbeat.Session{liveSession("a", "local")}, known: true},
			readResult{sessions: []heartbeat.Session{
				liveSession("a", "local"), liveSession("b", "pod"),
			}, known: true},
		)
		var changedCount counter

		startWatcher(ctx, sessionstate.WatchParams{
			State:    state,
			Read:     read.Get,
			Changed:  func() { changedCount.next() },
			Interval: 50 * time.Millisecond,
		})

		Eventually(func() []heartbeat.Session { return state.Sessions(ctx) }).
			Should(ConsistOf(liveSession("a", "local")))
		Eventually(func() []heartbeat.Session { return state.Sessions(ctx) }).
			Should(ConsistOf(liveSession("a", "local"), liveSession("b", "pod")))
		Eventually(changedCount.get).Should(Equal(1))

		// Later polls re-read the same set: no further refresh frame.
		Consistently(changedCount.get, 200*time.Millisecond).Should(Equal(1))
	})

	It("keeps the last known sessions and marks them unknown when the source fails", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		read := newSequenceRead(
			readResult{sessions: []heartbeat.Session{liveSession("a", "local")}, known: true},
			readResult{known: false},
		)
		var changedCount counter

		startWatcher(ctx, sessionstate.WatchParams{
			State:    state,
			Read:     read.Get,
			Changed:  func() { changedCount.next() },
			Interval: 50 * time.Millisecond,
		})

		Eventually(state.LiveIDsKnown).Should(BeFalse())
		// "Cannot tell" is not "nothing is live": the sessions survive.
		Expect(state.Sessions(ctx)).To(ConsistOf(liveSession("a", "local")))
		// The transition into the unknown state is worth one frame.
		Eventually(changedCount.get).Should(Equal(1))
		Consistently(changedCount.get, 200*time.Millisecond).Should(Equal(1))
	})

	It("becomes authoritative again when a later read succeeds", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		read := newSequenceRead(
			readResult{known: false},
			readResult{sessions: []heartbeat.Session{liveSession("a", "local")}, known: true},
		)

		startWatcher(ctx, sessionstate.WatchParams{
			State:    state,
			Read:     read.Get,
			Changed:  func() {},
			Interval: 50 * time.Millisecond,
		})

		Eventually(func() []heartbeat.Session { return state.Sessions(ctx) }).
			Should(ConsistOf(liveSession("a", "local")))
		Expect(state.LiveIDsKnown()).To(BeTrue())
	})

	It("returns nil when its context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		state := sessionstate.NewState()
		read := newSequenceRead(readResult{
			sessions: []heartbeat.Session{liveSession("a", "local")}, known: true,
		})
		done := startWatcher(ctx, sessionstate.WatchParams{
			State:    state,
			Read:     read.Get,
			Changed:  func() {},
			Interval: 50 * time.Millisecond,
		})

		Eventually(func() []heartbeat.Session { return state.Sessions(ctx) }).
			Should(ConsistOf(liveSession("a", "local")))
		cancel()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("keeps running with an empty state when the source answers nothing", func() {
		ctx, cancel := context.WithCancel(context.Background())
		state := sessionstate.NewState()
		read := newSequenceRead(readResult{known: true})
		done := startWatcher(ctx, sessionstate.WatchParams{
			State:    state,
			Read:     read.Get,
			Changed:  func() {},
			Interval: 50 * time.Millisecond,
		})

		Consistently(
			func() []heartbeat.Session { return state.Sessions(ctx) },
			150*time.Millisecond,
		).Should(BeEmpty())
		Expect(state.LiveIDsKnown()).To(BeTrue())

		cancel()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})
})
