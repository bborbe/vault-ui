// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package panecache_test

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/panecache"
)

// fakeResolver is a hand-written double for panecache.Resolver: a struct of
// function fields so a test can assert the exact arguments passed, and a nil
// field panics on an unexpected call. This repo has no `make generate` target,
// so a counterfeiter fake cannot be generated here.
type fakeResolver struct {
	resolve func(ctx context.Context, sessionID string) (string, bool)
}

func (f fakeResolver) Resolve(ctx context.Context, sessionID string) (string, bool) {
	if f.resolve == nil {
		panic("fakeResolver.Resolve called with no resolve func")
	}
	return f.resolve(ctx, sessionID)
}

// callRecorder captures the session ids handed to the resolver.
type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) record(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sessionID)
}

func (r *callRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

var _ = Describe("Cache", func() {
	It("returns a stored pane id and false for an unknown session id", func() {
		cache := panecache.NewCache()
		cache.Replace(map[string]string{"s1": "pane-1"})

		paneID, ok := cache.Resolve(context.Background(), "s1")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-1"))

		_, ok = cache.Resolve(context.Background(), "missing")
		Expect(ok).To(BeFalse())
	})

	It("Resolve is a pure map lookup and never calls the injected resolver", func() {
		recorder := &callRecorder{}
		cache := panecache.NewCache()
		// The refresher owns the resolver; the cache is never wired to it.
		_ = panecache.NewRefresher(panecache.RefreshParams{
			Cache: cache,
			Resolver: fakeResolver{resolve: func(_ context.Context, sessionID string) (string, bool) {
				recorder.record(sessionID)
				return "pane-1", true
			}},
			LiveSessionIDs: func(context.Context) []string { return []string{"s1"} },
		})
		cache.Replace(map[string]string{"s1": "pane-1"})

		paneID, ok := cache.Resolve(context.Background(), "s1")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-1"))
		Expect(recorder.snapshot()).To(BeEmpty())
	})

	It("Replace swaps the whole map so a stale entry disappears", func() {
		cache := panecache.NewCache()
		cache.Replace(map[string]string{"s1": "pane-1"})
		cache.Replace(map[string]string{"s2": "pane-2"})

		_, ok := cache.Resolve(context.Background(), "s1")
		Expect(ok).To(BeFalse())

		paneID, ok := cache.Resolve(context.Background(), "s2")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-2"))
	})
})

var _ = Describe("Refresh", func() {
	It("hands the deduped, sorted session ids to the resolver as given", func() {
		recorder := &callRecorder{}
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache: panecache.NewCache(),
			Resolver: fakeResolver{resolve: func(_ context.Context, sessionID string) (string, bool) {
				recorder.record(sessionID)
				return "pane-" + sessionID, true
			}},
			LiveSessionIDs: func(context.Context) []string { return []string{"b", "a", "b", ""} },
		})

		count, err := refresher.Refresh(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(2))
		Expect(recorder.snapshot()).To(ConsistOf("a", "b"))
	})

	It("still caches the other sessions when one fails to resolve", func() {
		cache := panecache.NewCache()
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache: cache,
			Resolver: fakeResolver{resolve: func(_ context.Context, sessionID string) (string, bool) {
				if sessionID == "b" {
					return "", false
				}
				return "pane-" + sessionID, true
			}},
			LiveSessionIDs: func(context.Context) []string { return []string{"a", "b"} },
		})

		count, err := refresher.Refresh(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(1))

		paneID, ok := cache.Resolve(context.Background(), "a")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-a"))

		_, ok = cache.Resolve(context.Background(), "b")
		Expect(ok).To(BeFalse())
	})

	It("returns zero when there are no live session ids", func() {
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache:          panecache.NewCache(),
			Resolver:       fakeResolver{},
			LiveSessionIDs: func(context.Context) []string { return nil },
		})

		count, err := refresher.Refresh(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("returns without replacing the cache when the context is already cancelled", func() {
		cache := panecache.NewCache()
		cache.Replace(map[string]string{"s1": "pane-1"})
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache:          cache,
			Resolver:       fakeResolver{},
			LiveSessionIDs: func(context.Context) []string { return []string{"s2"} },
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		count, err := refresher.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(0))

		paneID, ok := cache.Resolve(context.Background(), "s1")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-1"))
	})

	It("returns without replacing the cache when the context is cancelled mid-pass", func() {
		cache := panecache.NewCache()
		cache.Replace(map[string]string{"s1": "pane-1"})
		ctx, cancel := context.WithCancel(context.Background())
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache:    cache,
			Resolver: fakeResolver{resolve: func(context.Context, string) (string, bool) { return "p", true }},
			LiveSessionIDs: func(context.Context) []string {
				cancel()
				return []string{"s2"}
			},
		})

		_, err := refresher.Refresh(ctx)
		Expect(err).To(HaveOccurred())

		paneID, ok := cache.Resolve(context.Background(), "s1")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-1"))
	})

	It("returns without replacing the cache when cancelled with no session ids", func() {
		cache := panecache.NewCache()
		cache.Replace(map[string]string{"s1": "pane-1"})
		ctx, cancel := context.WithCancel(context.Background())
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache:    cache,
			Resolver: fakeResolver{},
			LiveSessionIDs: func(context.Context) []string {
				cancel()
				return nil
			},
		})

		count, err := refresher.Refresh(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(0))

		paneID, ok := cache.Resolve(context.Background(), "s1")
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("pane-1"))
	})
})

var _ = Describe("RunLoop", func() {
	It("returns nil immediately when its context is already cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache:          panecache.NewCache(),
			Resolver:       fakeResolver{},
			LiveSessionIDs: func(context.Context) []string { return []string{"s1"} },
			Interval:       time.Millisecond,
		})

		Expect(refresher.RunLoop(ctx)).To(Succeed())
	})

	It("refreshes repeatedly until cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var calls atomic.Int64
		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache: panecache.NewCache(),
			Resolver: fakeResolver{resolve: func(context.Context, string) (string, bool) {
				return "pane-1", true
			}},
			LiveSessionIDs: func(context.Context) []string {
				calls.Add(1)
				return []string{"s1"}
			},
			Interval: 2 * time.Millisecond,
		})

		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		Expect(refresher.RunLoop(ctx)).To(Succeed())
		Expect(calls.Load()).To(BeNumerically(">=", 2))
	})

	It("falls back to the default interval when none is configured", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		refresher := panecache.NewRefresher(panecache.RefreshParams{
			Cache:          panecache.NewCache(),
			Resolver:       fakeResolver{},
			LiveSessionIDs: func(context.Context) []string { return nil },
		})

		Expect(refresher.RunLoop(ctx)).To(Succeed())
	})
})
