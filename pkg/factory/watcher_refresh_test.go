// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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

// refreshRead records one ReadPage call.
type refreshRead struct {
	vaultPath string
	pagesDir  string
	filename  string
}

// refreshFile is one page file of a test-controlled folder: its page plus the
// version counter that changes whenever its content changes.
type refreshFile struct {
	page    *domain.Page
	version int64
}

// refreshFingerprint derives a file's fingerprint from its version counter, so
// a content change always changes the fingerprint.
func refreshFingerprint(version int64) pageindex.FileFingerprint {
	return pageindex.FileFingerprint{
		Size:    version,
		ModTime: time.Unix(version, 0).UTC(),
	}
}

// refreshContent is the test-controlled folder content shared by the reader and
// the lister fakes, so both see the same files and fingerprints.
type refreshContent struct {
	mu       sync.Mutex
	files    map[[2]string]map[string]refreshFile
	readErrs map[[2]string]map[string]error
	listErrs map[[2]string]error
}

func newRefreshContent() *refreshContent {
	return &refreshContent{
		files:    map[[2]string]map[string]refreshFile{},
		readErrs: map[[2]string]map[string]error{},
		listErrs: map[[2]string]error{},
	}
}

// setPages replaces the key's files. A file whose content changed keeps its
// name and bumps its version, so only it has a new fingerprint.
func (c *refreshContent) setPages(vaultPath, pagesDir string, pages ...*domain.Page) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := [2]string{vaultPath, pagesDir}
	previous := c.files[key]
	next := make(map[string]refreshFile, len(pages))
	for _, page := range pages {
		name := page.FileMetadata.Name
		old, ok := previous[name]
		version := old.version
		if !ok || old.page.Content != page.Content {
			version++
		}
		next[name] = refreshFile{page: page, version: version}
	}
	c.files[key] = next
}

func (c *refreshContent) setReadError(vaultPath, pagesDir, name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := [2]string{vaultPath, pagesDir}
	if c.readErrs[key] == nil {
		c.readErrs[key] = map[string]error{}
	}
	c.readErrs[key][name] = err
}

func (c *refreshContent) setListError(vaultPath, pagesDir string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listErrs[[2]string{vaultPath, pagesDir}] = err
}

// listEntriesLocked returns the key's entries in filename-ascending order, as
// os.ReadDir would. The caller must hold the mutex.
func (c *refreshContent) listEntriesLocked(key [2]string) []pageindex.FileEntry {
	names := make([]string, 0, len(c.files[key]))
	for name := range c.files[key] {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]pageindex.FileEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, pageindex.FileEntry{
			Name:        name + ".md",
			Fingerprint: refreshFingerprint(c.files[key][name].version),
		})
	}
	return entries
}

// refreshReader is the counting single-file reader fake. It serves the key's
// live content, records every call per filename and can block a call on a gate.
type refreshReader struct {
	content *refreshContent

	mu    sync.Mutex
	reads []refreshRead
	gate  func(callIndex int) <-chan struct{}
}

func (r *refreshReader) ReadPage(
	_ context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*domain.Page, pageindex.FileFingerprint, error) {
	key := [2]string{vaultPath, pagesDir}
	name := strings.TrimSuffix(filename, ".md")

	r.mu.Lock()
	index := len(r.reads)
	r.reads = append(r.reads, refreshRead{vaultPath, pagesDir, filename})
	gate := r.gate
	r.mu.Unlock()

	// The content is captured at call time, before any gate blocks, so a
	// blocked read still serves the file as it was when the read started.
	r.content.mu.Lock()
	file, known := r.content.files[key][name]
	err := r.content.readErrs[key][name]
	r.content.mu.Unlock()

	if gate != nil {
		if ch := gate(index); ch != nil {
			<-ch
		}
	}

	if err != nil {
		return nil, pageindex.FileFingerprint{}, err
	}
	if !known {
		return nil, pageindex.FileFingerprint{}, stderrors.New("page not found: " + name)
	}
	return file.page, refreshFingerprint(file.version), nil
}

func (r *refreshReader) setGate(gate func(callIndex int) <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gate = gate
}

func (r *refreshReader) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = nil
}

func (r *refreshReader) calls() []refreshRead {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]refreshRead(nil), r.reads...)
}

func (r *refreshReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reads)
}

// filenames returns the base names of every recorded read, in call order.
func (r *refreshReader) filenames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.reads))
	for _, read := range r.reads {
		names = append(names, read.filename)
	}
	return names
}

// refreshLister is the counting directory lister fake.
type refreshLister struct {
	content *refreshContent

	mu    sync.Mutex
	calls [][2]string
}

func (l *refreshLister) ListFiles(
	_ context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	key := [2]string{vaultPath, pagesDir}
	l.mu.Lock()
	l.calls = append(l.calls, key)
	l.mu.Unlock()

	l.content.mu.Lock()
	defer l.content.mu.Unlock()
	if err := l.content.listErrs[key]; err != nil {
		return nil, err
	}
	return l.content.listEntriesLocked(key), nil
}

func (l *refreshLister) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = nil
}

func (l *refreshLister) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *refreshLister) callsFor(vaultPath, pagesDir string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, call := range l.calls {
		if call == [2]string{vaultPath, pagesDir} {
			count++
		}
	}
	return count
}

// refreshFrame is one broadcast as observed by the connection-manager fake: the
// frame bytes plus the snapshot the index served for the frame's key at the
// instant of the broadcast.
type refreshFrame struct {
	frame   []byte
	indexed bool
	key     pageindex.Key
	pages   []*domain.Page
}

// content returns the content the frame's snapshot held for the named page, or
// "" when the page was absent.
func (f refreshFrame) content(name string) string {
	for _, page := range f.pages {
		if page.FileMetadata.Name == name {
			return string(page.Content)
		}
	}
	return ""
}

func (f refreshFrame) names() []string {
	return pageNames(f.pages)
}

// refreshFixture wires CreateWatcher over the reader and lister fakes, a fake
// watch operation and a fake connection manager, and exposes the handler the
// factory built.
type refreshFixture struct {
	content *refreshContent
	reader  *refreshReader
	lister  *refreshLister
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

	content := newRefreshContent()
	reader := &refreshReader{content: content}
	lister := &refreshLister{content: content}
	index := factory.CreatePageIndex(reader, lister, libtime.NewCurrentDateTime())

	fixture := &refreshFixture{
		content: content, reader: reader, lister: lister, index: index,
		alpha: alpha, beta: beta,
	}
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
// reads the snapshot the index serves for that key at that instant. A frame
// with no key (theme, objective, unknown vault) is recorded without a read, so
// the recorder never builds a key the index has never seen.
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
			rec.pages = pages
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

// warm builds the four keys the fixture's events target and clears the reader
// and lister call logs so later counts are the work the events caused.
func (f *refreshFixture) warm() {
	Expect(f.index.Build(context.Background(), []pageindex.Key{
		f.alpha.key(refreshTasksDir),
		f.alpha.key(refreshGoalsDir),
		f.beta.key(refreshTasksDir),
		f.beta.key(refreshGoalsDir),
	})).To(Succeed())
	f.reader.reset()
	f.lister.reset()
}

func testPage(name string) *domain.Page {
	return domain.NewPage(
		map[string]any{"status": "next"},
		domain.FileMetadata{Name: name},
		domain.Content("# "+name+"\n"),
	)
}

// pageWithContent builds a page of the given name holding the given content, so
// a test can rewrite one file in place.
func pageWithContent(name, content string) *domain.Page {
	return domain.NewPage(
		map[string]any{"status": "next"},
		domain.FileMetadata{Name: name},
		domain.Content(content),
	)
}

func pageNames(pages []*domain.Page) []string {
	names := make([]string, 0, len(pages))
	for _, page := range pages {
		names = append(names, page.FileMetadata.Name)
	}
	return names
}

// taskEvent builds a task event whose path names the file inside the vault's
// tasks folder, so the handler re-reads exactly that file.
func taskEvent(v refreshVault, event, name string) ops.WatchEvent {
	return ops.WatchEvent{
		Event: event, Name: name, Vault: v.name,
		Path: filepath.Join(refreshTasksDir, name+".md"), Type: "task",
	}
}

// goalEvent is taskEvent for the vault's goals folder.
func goalEvent(v refreshVault, event, name string) ops.WatchEvent {
	return ops.WatchEvent{
		Event: event, Name: name, Vault: v.name,
		Path: filepath.Join(refreshGoalsDir, name+".md"), Type: "goal",
	}
}

// manyPages builds count pages named "Page 0000" upwards, so the snapshot's
// filename order is well defined and the event's file sits in the middle.
func manyPages(count int) []*domain.Page {
	pages := make([]*domain.Page, 0, count)
	for i := 0; i < count; i++ {
		pages = append(pages, testPage(fmt.Sprintf("Page %04d", i)))
	}
	return pages
}

var _ = Describe("Watcher page-index refresh", func() {
	It("reads exactly the event's file of a 1000-page folder and lists nothing", func() {
		fixture := newRefreshFixture()
		fixture.content.setPages(fixture.alpha.path, refreshTasksDir, manyPages(1000)...)
		fixture.warm()

		// One file changes on disk; the fake bumps only its fingerprint.
		pages := manyPages(1000)
		pages[500] = pageWithContent("Page 0500", "# Page 0500 changed\n")
		fixture.content.setPages(fixture.alpha.path, refreshTasksDir, pages...)

		event := taskEvent(fixture.alpha, "modified", "Page 0500")
		Expect(fixture.handler(event)).To(Succeed())

		// Evidence: exactly one single-file read, naming the event's file, and
		// no folder listing at all.
		Expect(fixture.reader.readCount()).To(Equal(1))
		Expect(fixture.reader.filenames()).To(Equal([]string{"Page 0500.md"}))
		Expect(fixture.lister.callCount()).To(Equal(0))

		// The next read of the key serves the new content.
		served, err := fixture.index.ListPages(
			context.Background(), fixture.alpha.path, refreshTasksDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(served)).To(HaveLen(1000))
		Expect(string(findPage(served, "Page 0500").Content)).To(Equal("# Page 0500 changed\n"))

		// The frame is broadcast only after that snapshot is published, and the
		// snapshot the recorder saw already held the new content.
		Expect(fixture.broadcastCount()).To(Equal(1))
		recorded := fixture.recorded()
		Expect(recorded[0].frame).To(Equal(websocket.WatcherFrame(event)))
		Expect(recorded[0].indexed).To(BeTrue())
		Expect(recorded[0].key).To(Equal(fixture.alpha.key(refreshTasksDir)))
		Expect(recorded[0].content("Page 0500")).To(Equal("# Page 0500 changed\n"))
		Expect(fixture.lister.callCount()).To(Equal(0))
	})

	It("broadcasts the frame only after the file's read is applied", func() {
		fixture := newRefreshFixture()
		fixture.content.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()

		gate := make(chan struct{})
		fixture.reader.setGate(func(int) <-chan struct{} { return gate })
		fixture.content.setPages(
			fixture.alpha.path, refreshTasksDir, testPage("One"), testPage("Two"),
		)

		event := taskEvent(fixture.alpha, "created", "Two")
		done := make(chan error, 1)
		go func() { done <- fixture.handler(event) }()

		Eventually(fixture.reader.readCount).Should(Equal(1))
		Consistently(fixture.broadcastCount, 100*time.Millisecond).Should(Equal(0))

		close(gate)
		Eventually(done).Should(Receive(BeNil()))
		Eventually(fixture.broadcastCount).Should(Equal(1))

		recorded := fixture.recorded()
		Expect(recorded[0].frame).To(Equal(websocket.WatcherFrame(event)))
		Expect(recorded[0].names()).To(Equal([]string{"One", "Two"}))
	})

	It("holds a second event's frame for the same file until its own read is applied", func() {
		fixture := newRefreshFixture()
		fixture.content.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()

		gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
		fixture.reader.setGate(func(index int) <-chan struct{} {
			if index < len(gates) {
				return gates[index]
			}
			return nil
		})
		fixture.content.setPages(
			fixture.alpha.path, refreshTasksDir, pageWithContent("One", "# v1\n"),
		)

		first := taskEvent(fixture.alpha, "modified", "One")
		firstDone := make(chan error, 1)
		go func() { firstDone <- fixture.handler(first) }()
		Eventually(fixture.reader.readCount).Should(Equal(1))

		fixture.content.setPages(
			fixture.alpha.path, refreshTasksDir, pageWithContent("One", "# v2\n"),
		)

		second := taskEvent(fixture.alpha, "modified", "One")
		secondDone := make(chan error, 1)
		go func() { secondDone <- fixture.handler(second) }()

		// Per-file reads are independent: the second read starts without waiting
		// for the first, and neither frame is out yet.
		Eventually(fixture.reader.readCount).Should(Equal(2))
		Consistently(fixture.broadcastCount, 100*time.Millisecond).Should(Equal(0))

		close(gates[0])
		Eventually(firstDone).Should(Receive(BeNil()))
		Eventually(fixture.broadcastCount).Should(Equal(1))
		Expect(fixture.recorded()[0].content("One")).To(Equal("# v1\n"))

		close(gates[1])
		Eventually(secondDone).Should(Receive(BeNil()))
		Eventually(fixture.broadcastCount).Should(Equal(2))

		recorded := fixture.recorded()
		Expect(recorded[1].frame).To(Equal(websocket.WatcherFrame(second)))
		Expect(recorded[1].content("One")).To(Equal("# v2\n"))
		Expect(fixture.reader.readCount()).To(Equal(2))
		Expect(fixture.lister.callCount()).To(Equal(0))
	})

	It("broadcasts theme and objective frames unchanged without touching the index", func() {
		fixture := newRefreshFixture()
		fixture.warm()

		theme := ops.WatchEvent{
			Event: "modified", Name: "A Theme", Vault: fixture.alpha.name,
			Path: filepath.Join(refreshThemesDir, "A Theme.md"), Type: "theme",
		}
		objective := ops.WatchEvent{
			Event: "modified", Name: "An Objective", Vault: fixture.alpha.name,
			Path: filepath.Join(refreshObjectivesDir, "An Objective.md"), Type: "objective",
		}
		Expect(fixture.handler(theme)).To(Succeed())
		Expect(fixture.handler(objective)).To(Succeed())

		Expect(fixture.manager.BroadcastCallCount()).To(Equal(2))
		Expect(fixture.manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(theme)))
		Expect(fixture.manager.BroadcastArgsForCall(1)).To(Equal(websocket.WatcherFrame(objective)))
		Expect(fixture.reader.readCount()).To(Equal(0))
		Expect(fixture.lister.callCount()).To(Equal(0))
	})

	It("reads only the goals key's file for a goal event", func() {
		fixture := newRefreshFixture()
		fixture.content.setPages(fixture.beta.path, refreshGoalsDir, testPage("G1"))
		fixture.warm()
		fixture.content.setPages(
			fixture.beta.path, refreshGoalsDir, testPage("G1"), testPage("G2"),
		)

		Expect(fixture.handler(goalEvent(fixture.beta, "created", "G2"))).To(Succeed())

		Expect(fixture.reader.readCount()).To(Equal(1))
		Expect(fixture.reader.filenames()).To(Equal([]string{"G2.md"}))
		Expect(fixture.reader.calls()[0].pagesDir).To(Equal(refreshGoalsDir))
		Expect(fixture.reader.calls()[0].vaultPath).To(Equal(fixture.beta.path))
		Expect(fixture.lister.callCount()).To(Equal(0))

		served, err := fixture.index.ListPages(
			context.Background(), fixture.beta.path, refreshGoalsDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(served)).To(Equal([]string{"G1", "G2"}))
	})

	It("broadcasts an event for an unknown vault without touching the index", func() {
		fixture := newRefreshFixture()
		fixture.warm()

		event := ops.WatchEvent{
			Event: "modified", Name: "X", Vault: "gamma",
			Path: filepath.Join(refreshTasksDir, "X.md"), Type: "task",
		}
		Expect(fixture.handler(event)).To(Succeed())

		Expect(fixture.manager.BroadcastCallCount()).To(Equal(1))
		Expect(fixture.manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(event)))
		Expect(fixture.reader.readCount()).To(Equal(0))
		Expect(fixture.lister.callCount()).To(Equal(0))
	})

	It("falls back to the folder stat-diff for a path that is not a plain filename", func() {
		fixture := newRefreshFixture()
		fixture.content.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()
		fixture.content.setPages(
			fixture.alpha.path, refreshTasksDir, testPage("One"), testPage("Two"),
		)

		event := ops.WatchEvent{
			Event: "created", Name: "Two", Vault: fixture.alpha.name,
			Path: filepath.Join(refreshTasksDir, "sub", "Two.md"), Type: "task",
		}
		Expect(fixture.handler(event)).To(Succeed())

		// Evidence: the folder is listed once and no single file is read for the
		// event; the stat-diff re-reads only the file whose fingerprint changed.
		Expect(fixture.lister.callCount()).To(Equal(1))
		Expect(fixture.lister.callsFor(fixture.alpha.path, refreshTasksDir)).To(Equal(1))
		Expect(fixture.reader.filenames()).To(Equal([]string{"Two.md"}))

		served, err := fixture.index.ListPages(
			context.Background(), fixture.alpha.path, refreshTasksDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(served)).To(Equal([]string{"One", "Two"}))
	})

	It("broadcasts the frame and keeps the previous snapshot when the stat-diff fails", func() {
		fixture := newRefreshFixture()
		fixture.content.setPages(fixture.alpha.path, refreshTasksDir, testPage("One"))
		fixture.warm()
		fixture.content.setListError(
			fixture.alpha.path, refreshTasksDir, stderrors.New("folder unreadable"),
		)

		event := ops.WatchEvent{
			Event: "modified", Name: "One", Vault: fixture.alpha.name,
			Path: filepath.Join(refreshTasksDir, "sub", "One.md"), Type: "task",
		}
		Expect(fixture.handler(event)).To(Succeed())

		Expect(fixture.manager.BroadcastCallCount()).To(Equal(1))
		Expect(fixture.manager.BroadcastArgsForCall(0)).To(Equal(websocket.WatcherFrame(event)))

		served, err := fixture.index.ListPages(
			context.Background(), fixture.alpha.path, refreshTasksDir,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pageNames(served)).To(Equal([]string{"One"}))
	})
})

// findPage returns the named page of a snapshot.
func findPage(pages []*domain.Page, name string) *domain.Page {
	for _, page := range pages {
		if page.FileMetadata.Name == name {
			return page
		}
	}
	Fail("page not found: " + name)
	return nil
}
