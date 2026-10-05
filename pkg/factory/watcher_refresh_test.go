// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	vaultmocks "github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/watchrefresh"
	"github.com/bborbe/vault-ui/pkg/websocket"
	websocketmocks "github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

const (
	refreshTasksDir      = "24 Tasks"
	refreshGoalsDir      = "23 Goals"
	refreshThemesDir     = "22 Themes"
	refreshObjectivesDir = "21 Objectives"
)

// refreshVault is one configured vault of the watcher fixture.
type refreshVault struct {
	name string
	path string
}

// key derives the page-index key of one of the vault's folders, exactly as the
// event side does.
func (v refreshVault) key(dir string) pageindex.Key {
	return pageindex.NewKey(v.path, dir)
}

// refreshCall records one storage call together with the content it captured
// when the call started, before any gate blocked it.
type refreshCall struct {
	vaultPath string
	pagesDir  string
	content   []*domain.Page
}

// refreshStorage is a PageStorage fake serving test-controlled content per
// (vaultPath, pagesDir) key. It records every call with the content it saw at
// the call's start, and can block calls on per-call gates.
type refreshStorage struct {
	mu    sync.Mutex
	pages map[[2]string][]*domain.Page
	errs  map[[2]string]error
	calls []refreshCall
	gate  func(callIndex int) <-chan struct{}
}

func newRefreshStorage() *refreshStorage {
	return &refreshStorage{
		pages: map[[2]string][]*domain.Page{},
		errs:  map[[2]string]error{},
	}
}

// fake returns the counterfeiter PageStorage delegating to this fake.
func (s *refreshStorage) fake() *vaultmocks.PageStorage {
	fake := &vaultmocks.PageStorage{}
	fake.ListPagesStub = s.listPages
	return fake
}

func (s *refreshStorage) listPages(
	_ context.Context,
	vaultPath string,
	pagesDir string,
) ([]*domain.Page, error) {
	key := [2]string{vaultPath, pagesDir}
	s.mu.Lock()
	index := len(s.calls)
	content := s.pages[key]
	err := s.errs[key]
	s.calls = append(s.calls, refreshCall{
		vaultPath: vaultPath, pagesDir: pagesDir, content: content,
	})
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		if ch := gate(index); ch != nil {
			<-ch
		}
	}
	return content, err
}

func (s *refreshStorage) setPages(vaultPath, pagesDir string, pages ...*domain.Page) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[[2]string{vaultPath, pagesDir}] = pages
}

func (s *refreshStorage) setError(vaultPath, pagesDir string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs[[2]string{vaultPath, pagesDir}] = err
}

func (s *refreshStorage) setGate(gate func(callIndex int) <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = gate
}

// reset drops the call log so counts are relative to the warm-up.
func (s *refreshStorage) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

func (s *refreshStorage) callsFor(vaultPath, pagesDir string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, call := range s.calls {
		if call.vaultPath == vaultPath && call.pagesDir == pagesDir {
			count++
		}
	}
	return count
}

func (s *refreshStorage) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// refreshFrame is one broadcast as observed by the connection-manager fake: the
// frame bytes plus the page content the index served for the frame's key at the
// instant of the broadcast.
type refreshFrame struct {
	frame    []byte
	indexed  bool
	key      pageindex.Key
	contents []string
}

// refreshFixture wires CreateWatcher over fake storage, a fake watch operation
// and a fake connection manager, and exposes the handler the factory built.
type refreshFixture struct {
	storage *refreshStorage
	index   pageindex.PageIndex
	manager *websocketmocks.WebsocketConnectionManager
	handler func(ops.WatchEvent) error
	alpha   refreshVault
	beta    refreshVault

	mu     sync.Mutex
	frames []refreshFrame
}

func newRefreshFixture() *refreshFixture {
	alpha := refreshVault{name: "alpha", path: "/vaults/alpha"}
	beta := refreshVault{name: "beta", path: "/vaults/beta"}
	vaults := []*config.Vault{
		{
			Name: alpha.name, Path: alpha.path,
			TasksDir: refreshTasksDir, GoalsDir: refreshGoalsDir,
			ThemesDir: refreshThemesDir, ObjectivesDir: refreshObjectivesDir,
		},
		{
			Name: beta.name, Path: beta.path,
			TasksDir: refreshTasksDir, GoalsDir: refreshGoalsDir,
			ThemesDir: refreshThemesDir, ObjectivesDir: refreshObjectivesDir,
		},
	}
	vaultsByName := make(map[string]*config.Vault, len(vaults))
	for _, vault := range vaults {
		vaultsByName[vault.Name] = vault
	}

	loader := &vaultmocks.Loader{}
	loader.GetAllVaultsReturns(vaults, nil)

	storage := newRefreshStorage()
	index := factory.CreatePageIndex(storage.fake(), libtime.NewCurrentDateTime())

	fixture := &refreshFixture{storage: storage, index: index, alpha: alpha, beta: beta}
	fixture.manager = &websocketmocks.WebsocketConnectionManager{}
	fixture.manager.BroadcastStub = func(frame []byte) {
		fixture.record(frame, vaultsByName)
	}

	handlerCh := make(chan func(ops.WatchEvent) error, 1)
	watch := &vaultmocks.WatchOperation{}
	watch.ExecuteStub = func(
		ctx context.Context,
		_ []ops.WatchTarget,
		handler func(ops.WatchEvent) error,
	) error {
		handlerCh <- handler
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = factory.CreateWatcher(loader, fixture.manager, index, watch)(ctx) }()
	Eventually(handlerCh, 5*time.Second).Should(Receive(&fixture.handler))
	DeferCleanup(cancel)
	return fixture
}

// record captures a broadcast: it maps the frame back to its index key and
// reads the content the index serves for that key at that instant. A frame with
// no key (theme, objective, unknown vault) is recorded without a read, so the
// recorder never builds a key the index has never seen.
func (f *refreshFixture) record(frame []byte, vaultsByName map[string]*config.Vault) {
	rec := refreshFrame{frame: frame}
	var wire struct {
		Vault    string `json:"vault"`
		ItemKind string `json:"item_kind"`
	}
	if err := json.Unmarshal(frame, &wire); err == nil {
		event := ops.WatchEvent{Vault: wire.Vault, Type: wire.ItemKind}
		if key, indexed := watchrefresh.EventKey(event, vaultsByName); indexed {
			rec.indexed = true
			rec.key = key
			pages, _ := f.index.ListPages(context.Background(), key.VaultPath, key.PagesDir)
			rec.contents = pageNames(pages)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, rec)
}

func (f *refreshFixture) recorded() []refreshFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]refreshFrame(nil), f.frames...)
}

func (f *refreshFixture) broadcastCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

// warm builds the four keys the fixture's events target and clears the call
// log so later counts are the refreshes the events caused.
func (f *refreshFixture) warm() {
	Expect(f.index.Build(context.Background(), []pageindex.Key{
		f.alpha.key(refreshTasksDir),
		f.alpha.key(refreshGoalsDir),
		f.beta.key(refreshTasksDir),
		f.beta.key(refreshGoalsDir),
	})).To(Succeed())
	f.storage.reset()
}

func testPage(name string) *domain.Page {
	return domain.NewPage(
		map[string]any{"status": "next"},
		domain.FileMetadata{Name: name},
		domain.Content("# "+name+"\n"),
	)
}

func pageNames(pages []*domain.Page) []string {
	names := make([]string, 0, len(pages))
	for _, page := range pages {
		names = append(names, page.FileMetadata.Name)
	}
	return names
}

var _ = Describe("Watcher page-index refresh", func() {
	It("refreshes only the event's folder and serves the new content", func() {
		fixture := newRefreshFixture()
		fixture.storage.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()
		fixture.storage.setPages(
			fixture.alpha.path, refreshTasksDir, testPage("One"), testPage("Two"),
		)

		event := ops.WatchEvent{
			Event: "created", Name: "Two", Vault: fixture.alpha.name,
			Path: refreshTasksDir + "/Two.md", Type: "task",
		}
		Expect(fixture.handler(event)).To(Succeed())

		Expect(fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir)).To(Equal(1))
		Expect(fixture.storage.callsFor(fixture.alpha.path, refreshGoalsDir)).To(Equal(0))
		Expect(fixture.storage.callsFor(fixture.beta.path, refreshTasksDir)).To(Equal(0))
		Expect(fixture.storage.callsFor(fixture.beta.path, refreshGoalsDir)).To(Equal(0))

		pages, err := fixture.index.ListPages(
			context.Background(), fixture.alpha.path, refreshTasksDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(pages)).To(Equal([]string{"One", "Two"}))
	})

	It("broadcasts the frame only after the refreshed snapshot is swapped in", func() {
		fixture := newRefreshFixture()
		fixture.storage.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()

		gate := make(chan struct{})
		fixture.storage.setGate(func(int) <-chan struct{} { return gate })
		fixture.storage.setPages(
			fixture.alpha.path, refreshTasksDir, testPage("One"), testPage("Two"),
		)

		event := ops.WatchEvent{
			Event: "created", Name: "Two", Vault: fixture.alpha.name, Type: "task",
		}
		done := make(chan error, 1)
		go func() { done <- fixture.handler(event) }()

		Eventually(
			func() int { return fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir) },
		).Should(Equal(1))
		Consistently(fixture.broadcastCount, 100*time.Millisecond).Should(Equal(0))

		close(gate)
		Eventually(done).Should(Receive(BeNil()))
		Eventually(fixture.broadcastCount).Should(Equal(1))

		recorded := fixture.recorded()
		Expect(recorded[0].frame).To(Equal(websocket.WatcherFrame(event)))
		Expect(recorded[0].indexed).To(BeTrue())
		Expect(recorded[0].contents).To(Equal([]string{"One", "Two"}))
	})

	It("holds a second event's frame until a rebuild started after it has swapped", func() {
		fixture := newRefreshFixture()
		fixture.storage.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()

		gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
		fixture.storage.setGate(func(index int) <-chan struct{} {
			if index < len(gates) {
				return gates[index]
			}
			return nil
		})
		fixture.storage.setPages(
			fixture.alpha.path, refreshTasksDir, testPage("One"), testPage("v1"),
		)

		first := ops.WatchEvent{
			Event: "modified", Name: "One", Vault: fixture.alpha.name, Type: "task",
		}
		firstDone := make(chan error, 1)
		go func() { firstDone <- fixture.handler(first) }()

		// The first rebuild entered and captured v1 before blocking.
		Eventually(
			func() int { return fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir) },
		).Should(Equal(1))

		fixture.storage.setPages(
			fixture.alpha.path, refreshTasksDir, testPage("One"), testPage("v2"),
		)

		second := ops.WatchEvent{
			Event: "modified", Name: "Two", Vault: fixture.alpha.name, Type: "task",
		}
		secondDone := make(chan error, 1)
		go func() { secondDone <- fixture.handler(second) }()

		// The second event queues a follow-up that cannot start while the first
		// rebuild is still blocked, so no second storage call happens yet.
		Consistently(
			func() int { return fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir) },
			100*time.Millisecond,
		).Should(Equal(1))

		close(gates[0])
		Eventually(firstDone).Should(Receive(BeNil()))
		Eventually(fixture.broadcastCount).Should(Equal(1))
		Expect(fixture.recorded()[0].contents).To(Equal([]string{"One", "v1"}))

		// The follow-up rebuild started, captured v2 and is blocked.
		Eventually(
			func() int { return fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir) },
		).Should(Equal(2))
		Consistently(fixture.broadcastCount, 100*time.Millisecond).Should(Equal(1))

		close(gates[1])
		Eventually(secondDone).Should(Receive(BeNil()))
		Eventually(fixture.broadcastCount).Should(Equal(2))

		recorded := fixture.recorded()
		Expect(recorded[1].frame).To(Equal(websocket.WatcherFrame(second)))
		Expect(recorded[1].contents).To(Equal([]string{"One", "v2"}))
		Expect(fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir)).To(Equal(2))
	})

	It("broadcasts theme and objective frames unchanged without touching the index", func() {
		fixture := newRefreshFixture()
		fixture.warm()

		theme := ops.WatchEvent{
			Event: "modified", Name: "A Theme", Vault: fixture.alpha.name, Type: "theme",
		}
		objective := ops.WatchEvent{
			Event: "modified", Name: "An Objective", Vault: fixture.alpha.name,
			Type: "objective",
		}
		Expect(fixture.handler(theme)).To(Succeed())
		Expect(fixture.handler(objective)).To(Succeed())

		Expect(fixture.manager.BroadcastCallCount()).To(Equal(2))
		Expect(fixture.manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(theme)))
		Expect(fixture.manager.BroadcastArgsForCall(1)).To(Equal(websocket.WatcherFrame(objective)))
		Expect(fixture.storage.totalCalls()).To(Equal(0))
	})

	It("refreshes only the goals key for a goal event", func() {
		fixture := newRefreshFixture()
		fixture.storage.setPages(fixture.beta.path, refreshGoalsDir, testPage("G1"))
		fixture.warm()
		fixture.storage.setPages(
			fixture.beta.path, refreshGoalsDir, testPage("G1"), testPage("G2"),
		)

		Expect(fixture.handler(ops.WatchEvent{
			Event: "created", Name: "G2", Vault: fixture.beta.name, Type: "goal",
		})).To(Succeed())

		Expect(fixture.storage.callsFor(fixture.beta.path, refreshGoalsDir)).To(Equal(1))
		Expect(fixture.storage.callsFor(fixture.beta.path, refreshTasksDir)).To(Equal(0))
		Expect(fixture.storage.callsFor(fixture.alpha.path, refreshTasksDir)).To(Equal(0))
		Expect(fixture.storage.callsFor(fixture.alpha.path, refreshGoalsDir)).To(Equal(0))

		pages, err := fixture.index.ListPages(
			context.Background(), fixture.beta.path, refreshGoalsDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(pages)).To(Equal([]string{"G1", "G2"}))
	})

	It("broadcasts an event for an unknown vault without touching the index", func() {
		fixture := newRefreshFixture()
		fixture.warm()

		event := ops.WatchEvent{
			Event: "modified", Name: "X", Vault: "gamma", Type: "task",
		}
		Expect(fixture.handler(event)).To(Succeed())

		Expect(fixture.manager.BroadcastCallCount()).To(Equal(1))
		Expect(fixture.manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(event)))
		Expect(fixture.storage.totalCalls()).To(Equal(0))
	})

	It("broadcasts the frame and keeps the previous snapshot when a rebuild fails", func() {
		fixture := newRefreshFixture()
		fixture.storage.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()
		fixture.storage.setError(
			fixture.alpha.path, refreshTasksDir, stderrors.New("folder unreadable"),
		)

		event := ops.WatchEvent{
			Event: "modified", Name: "One", Vault: fixture.alpha.name, Type: "task",
		}
		Expect(fixture.handler(event)).To(Succeed())

		Expect(fixture.manager.BroadcastCallCount()).To(Equal(1))
		Expect(fixture.manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(event)))

		pages, err := fixture.index.ListPages(
			context.Background(), fixture.alpha.path, refreshTasksDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(pages)).To(Equal([]string{"One"}))
	})
})
