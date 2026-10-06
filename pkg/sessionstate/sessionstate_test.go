// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionstate_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/sessionstate"
)

// fakeSource is a hand-written Source double. A nil watchFn panics, so a test
// that did not arm the source fails loudly instead of hanging.
type fakeSource struct {
	watchFn func(ctx context.Context, dir string, changed func()) error
}

func (f *fakeSource) Watch(ctx context.Context, dir string, changed func()) error {
	if f.watchFn == nil {
		panic("fakeSource.Watch called but no watchFn was set")
	}
	return f.watchFn(ctx, dir, changed)
}

// silentWatch is the source that never delivers an event.
func silentWatch(ctx context.Context, _ string, _ func()) error {
	<-ctx.Done()
	return nil
}

// sourceCapture holds the changed callback of a source that never fires on its
// own, so a test can fire one event by hand.
type sourceCapture struct {
	mu      sync.Mutex
	changed func()
	ready   chan struct{}
}

func newSourceCapture() *sourceCapture {
	return &sourceCapture{ready: make(chan struct{})}
}

func (c *sourceCapture) watch(ctx context.Context, _ string, changed func()) error {
	c.mu.Lock()
	c.changed = changed
	c.mu.Unlock()
	close(c.ready)
	<-ctx.Done()
	return nil
}

// awaitReady blocks until the source has been entered and holds the callback.
func (c *sourceCapture) awaitReady() {
	Eventually(c.ready, 5*time.Second).Should(BeClosed())
}

func (c *sourceCapture) fire() {
	c.mu.Lock()
	changed := c.changed
	c.mu.Unlock()
	changed()
}

// fireAsync delivers an event without blocking the spec: a firing event that
// contends for the watcher's apply lock must not stall the assertion that
// releases it.
func (c *sourceCapture) fireAsync() {
	go func() {
		defer GinkgoRecover()
		c.fire()
	}()
}

// replaceCall is one observed State.Replace call: the argument plus whether it
// actually swapped the stored set.
type replaceCall struct {
	ids     []string
	changed bool
}

// recordingState wraps a real State and logs every Replace call in order.
type recordingState struct {
	inner sessionstate.State

	mu    sync.Mutex
	calls []replaceCall
}

func newRecordingState() *recordingState {
	return &recordingState{inner: sessionstate.NewState()}
}

func (r *recordingState) RegistrySessionIDs(ctx context.Context) []string {
	return r.inner.RegistrySessionIDs(ctx)
}

func (r *recordingState) Replace(ids []string) bool {
	changed := r.inner.Replace(ids)
	r.mu.Lock()
	r.calls = append(r.calls, replaceCall{ids: append([]string{}, ids...), changed: changed})
	r.mu.Unlock()
	return changed
}

func (r *recordingState) recorded() []replaceCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]replaceCall{}, r.calls...)
}

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

var _ = Describe("State", func() {
	It("reports a change on a different set and no change on a reordered one", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]string{"a", "b"})).To(BeTrue())
		Expect(state.Replace([]string{"b", "a"})).To(BeFalse())
		Expect(state.Replace([]string{"a", "b", "c"})).To(BeTrue())
		Expect(state.Replace([]string{"a", "b"})).To(BeTrue())
	})

	It("drops duplicates and empty ids", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]string{"a", "a", "", "b", ""})).To(BeTrue())
		Expect(state.RegistrySessionIDs(context.Background())).To(ConsistOf("a", "b"))
		Expect(state.Replace([]string{"b", "a"})).To(BeFalse())
	})

	It("starts empty and never returns nil", func() {
		state := sessionstate.NewState()
		ids := state.RegistrySessionIDs(context.Background())
		Expect(ids).NotTo(BeNil())
		Expect(ids).To(BeEmpty())
	})

	It("returns a copy that does not alias the stored set", func() {
		state := sessionstate.NewState()
		Expect(state.Replace([]string{"a"})).To(BeTrue())

		ids := state.RegistrySessionIDs(context.Background())
		ids[0] = "mutated"
		Expect(state.RegistrySessionIDs(context.Background())).To(Equal([]string{"a"}))
	})
})

var _ = Describe("Watcher", func() {
	It("performs the initial read before it starts the watch", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		source := &fakeSource{}
		observed := make(chan []string, 1)
		source.watchFn = func(watchCtx context.Context, _ string, _ func()) error {
			observed <- state.RegistrySessionIDs(watchCtx)
			<-watchCtx.Done()
			return nil
		}

		startWatcher(ctx, sessionstate.WatchParams{
			Dir:      "registry",
			State:    state,
			Source:   source,
			Read:     func(context.Context, string) []string { return []string{"seed"} },
			Changed:  func() {},
			Interval: time.Hour,
		})

		// The source only runs once the initial read has already populated the
		// state, so what it observes at entry is the initial read's result.
		Eventually(observed, 5*time.Second).Should(Receive(ConsistOf("seed")))
	})

	It("re-reads on a source event and pushes a refresh only on a real change", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		source := newSourceCapture()
		var freshMu sync.Mutex
		fresh := false
		var changedCount counter

		startWatcher(ctx, sessionstate.WatchParams{
			Dir:    "registry",
			State:  state,
			Source: &fakeSource{watchFn: source.watch},
			Read: func(context.Context, string) []string {
				freshMu.Lock()
				defer freshMu.Unlock()
				if fresh {
					return []string{"a", "b"}
				}
				return []string{"a"}
			},
			Changed:  func() { changedCount.next() },
			Interval: time.Hour,
		})

		Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
			Should(ConsistOf("a"))
		source.awaitReady()

		freshMu.Lock()
		fresh = true
		freshMu.Unlock()

		source.fire()
		Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
			Should(ConsistOf("a", "b"))
		Eventually(changedCount.get).Should(Equal(1))

		// A second event re-reads the same set: no further refresh frame.
		source.fire()
		Consistently(changedCount.get, 200*time.Millisecond).Should(Equal(1))
	})

	It("updates the state from the rescan when the source delivers no event", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		state := sessionstate.NewState()
		var freshMu sync.Mutex
		fresh := false

		startWatcher(ctx, sessionstate.WatchParams{
			Dir:    "registry",
			State:  state,
			Source: &fakeSource{watchFn: silentWatch},
			Read: func(context.Context, string) []string {
				freshMu.Lock()
				defer freshMu.Unlock()
				if fresh {
					return []string{"fresh"}
				}
				return []string{"stale"}
			},
			Changed:  func() {},
			Interval: 500 * time.Millisecond,
		})

		// AC7 negative control: the event source is held silent, so the state
		// stays stale until the rescan interval elapses.
		Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
			Should(ConsistOf("stale"))
		freshMu.Lock()
		fresh = true
		freshMu.Unlock()

		Consistently(
			func() []string { return state.RegistrySessionIDs(ctx) },
			200*time.Millisecond,
		).Should(ConsistOf("stale"))
		Eventually(
			func() []string { return state.RegistrySessionIDs(ctx) },
			3*time.Second,
		).Should(ConsistOf("fresh"))
	})

	It("serialises the event path and the rescan path", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		recorder := newRecordingState()
		source := newSourceCapture()
		release := make(chan struct{})
		entered := make(chan struct{})
		var calls counter

		startWatcher(ctx, sessionstate.WatchParams{
			Dir:    "registry",
			State:  recorder,
			Source: &fakeSource{watchFn: source.watch},
			Read: func(context.Context, string) []string {
				switch calls.next() {
				case 1:
					// The initial read: nothing live yet.
					return nil
				case 2:
					// The first rescan read: it began before the file event, so
					// it still sees the old set. It blocks holding the apply lock.
					close(entered)
					<-release
					return []string{"old"}
				default:
					return []string{"new"}
				}
			},
			Changed:  func() {},
			Interval: 50 * time.Millisecond,
		})

		Eventually(entered, 5*time.Second).Should(BeClosed())
		source.awaitReady()
		source.fireAsync()
		time.Sleep(100 * time.Millisecond)
		close(release)

		Eventually(
			func() []string { return recorder.RegistrySessionIDs(ctx) },
			5*time.Second,
		).Should(ConsistOf("new"))
		// Let every queued rescan land before reading the sequence.
		Consistently(
			func() []string { return recorder.RegistrySessionIDs(ctx) },
			150*time.Millisecond,
		).Should(ConsistOf("new"))

		// The stale read must never overwrite the fresh one. Asserting the
		// sequence, not just the final state: a later rescan returns "new" and
		// would mask the revert.
		storedNew := false
		for _, call := range recorder.recorded() {
			if call.changed && slices.Equal(call.ids, []string{"new"}) {
				storedNew = true
				continue
			}
			Expect(
				storedNew && call.changed && slices.Equal(call.ids, []string{"old"}),
			).To(BeFalse(), "stale set %v stored after the fresh set", call.ids)
		}
		Expect(storedNew).To(BeTrue())
	})

	It("returns nil when its context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		state := sessionstate.NewState()
		done := startWatcher(ctx, sessionstate.WatchParams{
			Dir:      "registry",
			State:    state,
			Source:   &fakeSource{watchFn: silentWatch},
			Read:     func(context.Context, string) []string { return []string{"a"} },
			Changed:  func() {},
			Interval: 50 * time.Millisecond,
		})

		Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
			Should(ConsistOf("a"))
		cancel()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("keeps running with an empty state when the directory is missing", func() {
		ctx, cancel := context.WithCancel(context.Background())
		state := sessionstate.NewState()
		done := startWatcher(ctx, sessionstate.WatchParams{
			Dir:      "missing",
			State:    state,
			Source:   &fakeSource{watchFn: silentWatch},
			Read:     func(context.Context, string) []string { return nil },
			Changed:  func() {},
			Interval: 50 * time.Millisecond,
		})

		Consistently(
			func() []string { return state.RegistrySessionIDs(ctx) },
			150*time.Millisecond,
		).Should(BeEmpty())

		cancel()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	DescribeTable("survives a broken source and keeps the rescan running",
		func(name string, watchFn func(ctx context.Context, dir string, changed func()) error) {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			state := sessionstate.NewState()
			var freshMu sync.Mutex
			fresh := false
			done := startWatcher(ctx, sessionstate.WatchParams{
				Dir:    "registry",
				State:  state,
				Source: &fakeSource{watchFn: watchFn},
				Read: func(context.Context, string) []string {
					freshMu.Lock()
					defer freshMu.Unlock()
					if fresh {
						return []string{"a", "b"}
					}
					return []string{"a"}
				},
				Changed:  func() {},
				Interval: 50 * time.Millisecond,
			})

			Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
				Should(ConsistOf("a"))

			freshMu.Lock()
			fresh = true
			freshMu.Unlock()

			// The rescan — not the dead source — repairs the state.
			Eventually(
				func() []string { return state.RegistrySessionIDs(ctx) },
				3*time.Second,
			).Should(ConsistOf("a", "b"))

			// Run did not return: the failure did not cancel the watcher.
			Consistently(done, 100*time.Millisecond).ShouldNot(Receive())

			cancel()
			Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		},
		Entry("an error", "error", func(context.Context, string, func()) error {
			return stderrors.New("registry unreadable")
		}),
		Entry("a panic", "panic", func(context.Context, string, func()) error {
			panic("source exploded")
		}),
	)
})

var _ = Describe("FSNotifySource", func() {
	It("reports a change when a file is written into the directory", func() {
		dir := GinkgoT().TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		source := sessionstate.NewFSNotifySource()
		var changedCount counter
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- source.Watch(ctx, dir, func() { changedCount.next() })
		}()

		// Rewrite on every poll: a single write can precede the watch being
		// armed, so the write must be repeated until the event is observed.
		Eventually(func() int {
			Expect(
				os.WriteFile(filepath.Join(dir, "1.json"), []byte(`{"sessionId":"a"}`), 0600),
			).To(Succeed())
			return changedCount.get()
		}, 5*time.Second, 20*time.Millisecond).Should(BeNumerically(">=", 1))

		cancel()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("returns an error for a missing directory", func() {
		dir := GinkgoT().TempDir()
		source := sessionstate.NewFSNotifySource()
		err := source.Watch(
			context.Background(), filepath.Join(dir, "missing"), func() {},
		)
		Expect(err).To(HaveOccurred())
	})

	It("returns nil when the context is cancelled", func() {
		dir := GinkgoT().TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		source := sessionstate.NewFSNotifySource()
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- source.Watch(ctx, dir, func() {})
		}()

		Consistently(done, 100*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})
})
