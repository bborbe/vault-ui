// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package watchrefresh_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	pageindexmocks "github.com/bborbe/vault-ui/pkg/pageindex/mocks"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/watchrefresh"
	"github.com/bborbe/vault-ui/pkg/websocket"
	websocketmocks "github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

// alphaVault is the configured vault every handler spec works against.
func alphaVault() *config.Vault {
	return &config.Vault{
		Name:     "alpha",
		Path:     "/vault/alpha",
		TasksDir: "24 Tasks",
		GoalsDir: "23 Goals",
	}
}

var _ = Describe("EventKey", func() {
	vaults := map[string]*config.Vault{"alpha": alphaVault()}

	DescribeTable(
		"maps the event to the folder key it invalidates",
		func(event ops.WatchEvent, expected pageindex.Key, indexed bool) {
			key, ok := watchrefresh.EventKey(event, vaults)
			Expect(ok).To(Equal(indexed))
			if indexed {
				Expect(key).To(Equal(expected))
			}
		},
		Entry(
			"task",
			ops.WatchEvent{Vault: "alpha", Type: "task"},
			pageindex.NewKey("/vault/alpha", "24 Tasks"), true,
		),
		Entry(
			"goal",
			ops.WatchEvent{Vault: "alpha", Type: "goal"},
			pageindex.NewKey("/vault/alpha", "23 Goals"), true,
		),
		Entry(
			"theme",
			ops.WatchEvent{Vault: "alpha", Type: "theme"},
			pageindex.Key{}, false,
		),
		Entry(
			"objective",
			ops.WatchEvent{Vault: "alpha", Type: "objective"},
			pageindex.Key{}, false,
		),
		Entry(
			"unknown vault",
			ops.WatchEvent{Vault: "beta", Type: "task"},
			pageindex.Key{}, false,
		),
	)
})

var _ = Describe("EventFilename", func() {
	key := pageindex.NewKey("/vault/alpha", "24 Tasks")

	DescribeTable(
		"resolves the event's file within the key's folder",
		func(path, expected string, ok bool) {
			filename, resolved := watchrefresh.EventFilename(
				ops.WatchEvent{Path: path}, key,
			)
			Expect(resolved).To(Equal(ok))
			Expect(filename).To(Equal(expected))
		},
		Entry("plain file", "24 Tasks/One.md", "One.md", true),
		Entry("nested path", "24 Tasks/sub/One.md", "", false),
		Entry("parent traversal", "24 Tasks/../x.md", "", false),
		Entry("another folder", "25 Goals/One.md", "", false),
		Entry("non-markdown file", "24 Tasks/One.txt", "", false),
		Entry("the folder itself", "24 Tasks", "", false),
		Entry("empty path", "", "", false),
	)
})

var _ = Describe("NewHandler", func() {
	It("re-reads the event's single file before broadcasting its frame", func() {
		pageIndex := &pageindexmocks.PageIndex{}
		manager := &websocketmocks.WebsocketConnectionManager{}
		var mu sync.Mutex
		var order []string
		pageIndex.RefreshFileStub = func(context.Context, pageindex.Key, string) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, "refresh-file")
			return nil
		}
		manager.BroadcastStub = func([]byte) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, "broadcast")
		}
		handler := watchrefresh.NewHandler(
			context.Background(), []*config.Vault{alphaVault()}, pageIndex,
			statuscache.NewCache(), manager,
		)
		event := ops.WatchEvent{
			Event: "modified", Name: "One", Vault: "alpha",
			Path: "24 Tasks/One.md", Type: "task",
		}

		Expect(handler(event)).To(Succeed())

		Expect(order).To(Equal([]string{"refresh-file", "broadcast"}))
		Expect(pageIndex.RefreshFileCallCount()).To(Equal(1))
		_, key, filename := pageIndex.RefreshFileArgsForCall(0)
		Expect(key).To(Equal(pageindex.NewKey("/vault/alpha", "24 Tasks")))
		Expect(filename).To(Equal("One.md"))
		Expect(pageIndex.RefreshCallCount()).To(Equal(0))
		Expect(manager.BroadcastCallCount()).To(Equal(1))
		Expect(manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(event)))
	})

	It("falls back to the folder refresh for a nested event path", func() {
		pageIndex := &pageindexmocks.PageIndex{}
		manager := &websocketmocks.WebsocketConnectionManager{}
		handler := watchrefresh.NewHandler(
			context.Background(), []*config.Vault{alphaVault()}, pageIndex,
			statuscache.NewCache(), manager,
		)
		event := ops.WatchEvent{
			Event: "modified", Name: "One", Vault: "alpha",
			Path: "24 Tasks/sub/One.md", Type: "task",
		}

		Expect(handler(event)).To(Succeed())

		Expect(pageIndex.RefreshFileCallCount()).To(Equal(0))
		Expect(pageIndex.RefreshCallCount()).To(Equal(1))
		_, key := pageIndex.RefreshArgsForCall(0)
		Expect(key).To(Equal(pageindex.NewKey("/vault/alpha", "24 Tasks")))
		Expect(manager.BroadcastCallCount()).To(Equal(1))
	})

	It("re-reads the goals key's file for a goal event", func() {
		pageIndex := &pageindexmocks.PageIndex{}
		manager := &websocketmocks.WebsocketConnectionManager{}
		handler := watchrefresh.NewHandler(
			context.Background(), []*config.Vault{alphaVault()}, pageIndex,
			statuscache.NewCache(), manager,
		)

		Expect(handler(ops.WatchEvent{
			Vault: "alpha", Path: "23 Goals/One.md", Type: "goal",
		})).To(Succeed())

		Expect(pageIndex.RefreshFileCallCount()).To(Equal(1))
		_, key, filename := pageIndex.RefreshFileArgsForCall(0)
		Expect(key).To(Equal(pageindex.NewKey("/vault/alpha", "23 Goals")))
		Expect(filename).To(Equal("One.md"))
	})

	It("drops the frame when the file refresh fails on shutdown", func() {
		pageIndex := &pageindexmocks.PageIndex{}
		manager := &websocketmocks.WebsocketConnectionManager{}
		pageIndex.RefreshFileStub = func(context.Context, pageindex.Key, string) error {
			return context.Canceled
		}
		handler := watchrefresh.NewHandler(
			context.Background(), []*config.Vault{alphaVault()}, pageIndex,
			statuscache.NewCache(), manager,
		)

		Expect(handler(ops.WatchEvent{
			Event: "modified", Name: "One", Vault: "alpha",
			Path: "24 Tasks/One.md", Type: "task",
		})).To(Succeed())

		Expect(pageIndex.RefreshFileCallCount()).To(Equal(1))
		Consistently(manager.BroadcastCallCount, 100*time.Millisecond).Should(Equal(0))
	})

	DescribeTable(
		"broadcasts without refreshing",
		func(event ops.WatchEvent) {
			pageIndex := &pageindexmocks.PageIndex{}
			manager := &websocketmocks.WebsocketConnectionManager{}
			handler := watchrefresh.NewHandler(
				context.Background(), []*config.Vault{alphaVault()}, pageIndex,
				statuscache.NewCache(), manager,
			)

			Expect(handler(event)).To(Succeed())

			Expect(pageIndex.RefreshCallCount()).To(Equal(0))
			Expect(manager.BroadcastCallCount()).To(Equal(1))
			Expect(manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(event)))
		},
		Entry("theme event", ops.WatchEvent{Vault: "alpha", Type: "theme"}),
		Entry("objective event", ops.WatchEvent{Vault: "alpha", Type: "objective"}),
		Entry("event for an unknown vault", ops.WatchEvent{Vault: "beta", Type: "task"}),
	)

	It("drops the frame when the context is cancelled while refreshing", func() {
		pageIndex := &pageindexmocks.PageIndex{}
		manager := &websocketmocks.WebsocketConnectionManager{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pageIndex.RefreshStub = func(refreshCtx context.Context, _ pageindex.Key) error {
			<-refreshCtx.Done()
			return refreshCtx.Err()
		}
		handler := watchrefresh.NewHandler(
			ctx, []*config.Vault{alphaVault()}, pageIndex, statuscache.NewCache(), manager,
		)
		done := make(chan error, 1)
		go func() {
			done <- handler(ops.WatchEvent{
				Event: "modified", Name: "One", Vault: "alpha", Type: "task",
			})
		}()

		Eventually(pageIndex.RefreshCallCount).Should(Equal(1))
		refreshCtx, _ := pageIndex.RefreshArgsForCall(0)
		// Compared with == rather than a Gomega matcher: a failing matcher
		// would reflect over the cancel context's internals while the handler
		// goroutine is using them.
		Expect(refreshCtx == ctx).To(BeTrue(), "Refresh must receive the handler's context")

		cancel()
		Eventually(done).Should(Receive(BeNil()))
		Consistently(manager.BroadcastCallCount, 100*time.Millisecond).Should(Equal(0))
	})
})

// statusFixture is a real status cache over a temp vault holding one task and
// one goal file, both starting in_progress. The positive cache cases drive this
// real cache so they traverse the real Invalidate -> GetStatus path.
type statusFixture struct {
	vaultPath string
	taskPath  string
	goalPath  string
	cache     statuscache.Cache
}

func newStatusFixture() *statusFixture {
	vaultPath := GinkgoT().TempDir()
	for _, dir := range []string{"24 Tasks", "23 Goals"} {
		ExpectWithOffset(1, os.MkdirAll(filepath.Join(vaultPath, dir), 0o750)).To(Succeed())
	}
	fixture := &statusFixture{
		vaultPath: vaultPath,
		taskPath:  filepath.Join(vaultPath, "24 Tasks", "One.md"),
		goalPath:  filepath.Join(vaultPath, "23 Goals", "Goal.md"),
	}
	fixture.write(fixture.taskPath, "in_progress")
	fixture.write(fixture.goalPath, "in_progress")
	fixture.cache = statuscache.NewCache()
	ExpectWithOffset(1, fixture.cache.LoadVault("alpha", vaultPath, "24 Tasks")).To(Succeed())
	return fixture
}

// write rewrites a fixture file with the given status, simulating a change made
// outside the board.
func (f *statusFixture) write(path, status string) {
	ExpectWithOffset(1, os.WriteFile(
		path, []byte("---\nstatus: "+status+"\n---\n\nbody\n"), 0o600,
	)).To(Succeed())
}

// handler builds the watch handler over this fixture's vault and the given cache.
func (f *statusFixture) handler(cache statuscache.Cache) func(ops.WatchEvent) error {
	return watchrefresh.NewHandler(
		context.Background(),
		[]*config.Vault{{
			Name: "alpha", Path: f.vaultPath, TasksDir: "24 Tasks", GoalsDir: "23 Goals",
		}},
		&pageindexmocks.PageIndex{},
		cache,
		&websocketmocks.WebsocketConnectionManager{},
	)
}

// recordingCache records Invalidate calls. It is used only for the negative
// cases, where the assertion is that Invalidate is never called: it cannot
// return a disk-derived status, so it cannot serve the positive cases.
type recordingCache struct {
	mu            sync.Mutex
	invalidations [][2]string
}

func (c *recordingCache) LoadVault(_, _, _ string) error               { return nil }
func (c *recordingCache) GetStatus(_, _ string) (string, bool)         { return "", false }
func (c *recordingCache) GetSessionStarted(_, _ string) (string, bool) { return "", false }
func (c *recordingCache) Count(_ string) int                           { return 0 }

func (c *recordingCache) Invalidate(vaultName, itemID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidations = append(c.invalidations, [2]string{vaultName, itemID})
}

func (c *recordingCache) calls() [][2]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][2]string(nil), c.invalidations...)
}

var _ = Describe("NewHandler status cache", func() {
	It("drops the task's stale status when its file changed outside the board", func() {
		// Regression: a blocker completed outside the board (via the vault CLI)
		// must clear the blocked-by chip without a restart. The cache holds the
		// old status while the file on disk reads completed, so the event must
		// drop the stale entry and GetStatus must re-read completed.
		fixture := newStatusFixture()
		status, ok := fixture.cache.GetStatus("alpha", "One")
		Expect(ok).To(BeTrue())
		Expect(status).To(Equal("in_progress"))

		fixture.write(fixture.taskPath, "completed")

		Expect(fixture.handler(fixture.cache)(ops.WatchEvent{
			Event: "modified", Name: "One", Vault: "alpha",
			Path: "24 Tasks/One.md", Type: "task",
		})).To(Succeed())

		status, ok = fixture.cache.GetStatus("alpha", "One")
		Expect(ok).To(BeTrue())
		Expect(status).To(Equal("completed"))
	})

	It("drops the goal's stale status for a goal event", func() {
		fixture := newStatusFixture()
		fixture.write(fixture.goalPath, "completed")

		Expect(fixture.handler(fixture.cache)(ops.WatchEvent{
			Event: "modified", Name: "Goal", Vault: "alpha",
			Path: "23 Goals/Goal.md", Type: "goal",
		})).To(Succeed())

		status, ok := fixture.cache.GetStatus("alpha", "Goal")
		Expect(ok).To(BeTrue())
		Expect(status).To(Equal("completed"))
	})

	DescribeTable(
		"leaves the cache untouched",
		func(event ops.WatchEvent) {
			cache := &recordingCache{}
			handler := newStatusFixture().handler(cache)

			Expect(handler(event)).To(Succeed())

			Expect(cache.calls()).To(BeEmpty())
		},
		Entry(
			"theme event",
			ops.WatchEvent{Vault: "alpha", Type: "theme", Name: "One"},
		),
		Entry(
			"objective event",
			ops.WatchEvent{Vault: "alpha", Type: "objective", Name: "One"},
		),
		Entry(
			"event for an unknown vault",
			ops.WatchEvent{Vault: "beta", Type: "task", Name: "One"},
		),
	)
})
