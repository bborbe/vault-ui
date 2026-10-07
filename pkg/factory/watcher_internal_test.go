// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// testPageIndex returns a real page index over empty storage, enough for the
// watcher tests that only assert frame delivery and lifecycle.
func testPageIndex() pageindex.PageIndex {
	return CreatePageIndex(
		pageindex.NewPageReader(storage.NewPageStorage(nil)),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
	)
}

// countingSeams counts single-file reads and folder listings while delegating
// to the real disk seams, so the watcher's per-file path is observable end to
// end without changing what it reads.
type countingSeams struct {
	pageindex.PageReader
	pageindex.DirectoryLister

	mu      sync.Mutex
	reads   []string
	listing int
}

func newCountingSeams() *countingSeams {
	return &countingSeams{
		PageReader:      pageindex.NewPageReader(storage.NewPageStorage(nil)),
		DirectoryLister: pageindex.NewDirectoryLister(),
	}
}

func (c *countingSeams) ListFiles(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	c.mu.Lock()
	c.listing++
	c.mu.Unlock()
	return c.DirectoryLister.ListFiles(ctx, vaultPath, pagesDir)
}

func (c *countingSeams) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*domain.Page, pageindex.FileFingerprint, error) {
	c.mu.Lock()
	c.reads = append(c.reads, filename)
	c.mu.Unlock()
	return c.PageReader.ReadPage(ctx, vaultPath, pagesDir, filename)
}

// reset clears the recorded work, so a test counts only its own change.
func (c *countingSeams) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = nil
	c.listing = 0
}

func (c *countingSeams) readFilenames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.reads...)
}

func (c *countingSeams) listCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listing
}

// captureConn records frames written by the manager's write pump.
type captureConn struct {
	mu     sync.Mutex
	frames [][]byte
	signal chan []byte
}

func newCaptureConn() *captureConn {
	return &captureConn{signal: make(chan []byte, 16)}
}

func (c *captureConn) WriteMessage(_ int, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	c.mu.Lock()
	c.frames = append(c.frames, cp)
	c.mu.Unlock()
	c.signal <- cp
	return nil
}

func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }
func (c *captureConn) Close() error                     { return nil }

func TestCreateWatcherBroadcastsChanges(t *testing.T) {
	vaultDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0o750); err != nil {
		t.Fatal(err)
	}
	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns([]*config.Vault{{
		Name: "personal", Path: vaultDir, TasksDir: "24 Tasks",
	}}, nil)

	manager := websocket.NewConnectionManager(websocket.NewMetrics())
	conn := newCaptureConn()
	client, err := manager.Connect(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	defer pumpCancel()
	go func() { _ = manager.Pump(pumpCtx, client) }()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		_ = CreateWatcher(loader, manager, testPageIndex(), ops.NewWatchOperation())(ctx)
	}()
	// Stop the watcher and wait for it before the test returns, so its
	// fsnotify goroutine cannot outlive the test and race later suites.
	defer func() {
		cancel()
		<-watcherDone
	}()

	// Give the watcher time to register the directory.
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(
		filepath.Join(vaultDir, "24 Tasks", "New.md"), []byte("---\nstatus: todo\n---\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	select {
	case frame := <-conn.signal:
		t.Logf("frame: %s", frame)
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not broadcast a frame")
	}
}

// TestCreateWatcherReadsOnlyTheEventFile drives the real watcher over a real
// temp directory: a single file written into a warm folder must cost exactly
// one single-file read of that file and no folder listing at all.
func TestCreateWatcherReadsOnlyTheEventFile(t *testing.T) {
	vaultDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(vaultDir, "24 Tasks", "Warm.md"),
		[]byte("---\nstatus: todo\n---\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns([]*config.Vault{{
		Name: "personal", Path: vaultDir, TasksDir: "24 Tasks",
	}}, nil)

	seams := newCountingSeams()
	index := CreatePageIndex(seams, seams, libtime.NewCurrentDateTime())
	if err := index.Build(context.Background(), []pageindex.Key{
		pageindex.NewKey(vaultDir, "24 Tasks"),
	}); err != nil {
		t.Fatal(err)
	}
	seams.reset()

	manager := websocket.NewConnectionManager(websocket.NewMetrics())
	conn := newCaptureConn()
	client, err := manager.Connect(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	defer pumpCancel()
	go func() { _ = manager.Pump(pumpCtx, client) }()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		_ = CreateWatcher(loader, manager, index, ops.NewWatchOperation())(ctx)
	}()
	// Stop the watcher and wait for it before the test returns, so its
	// fsnotify goroutine cannot outlive the test and race later suites.
	defer func() {
		cancel()
		<-watcherDone
	}()

	// Give the watcher time to register the directory.
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(
		filepath.Join(vaultDir, "24 Tasks", "New.md"),
		[]byte("---\nstatus: todo\n---\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	select {
	case frame := <-conn.signal:
		t.Logf("frame: %s", frame)
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not broadcast a frame")
	}

	reads := seams.readFilenames()
	if len(reads) == 0 {
		t.Fatal("expected at least one single-file read")
	}
	for _, name := range reads {
		if name != "New.md" {
			t.Fatalf("expected only New.md to be read, got %q", name)
		}
	}
	if listings := seams.listCount(); listings != 0 {
		t.Fatalf("expected 0 folder listings, got %d", listings)
	}
}

func TestBuildWatchTargets(t *testing.T) {
	targets := buildWatchTargets([]*config.Vault{{
		Name:          "personal",
		Path:          "/vault",
		TasksDir:      "24 Tasks",
		GoalsDir:      "23 Goals",
		ThemesDir:     "22 Themes",
		ObjectivesDir: "21 Objectives",
	}})
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	target := targets[0]
	if target.VaultPath != "/vault" || target.VaultName != "personal" {
		t.Fatalf("unexpected vault identity: %+v", target)
	}
	want := map[string]string{
		"24 Tasks":      "task",
		"23 Goals":      "goal",
		"22 Themes":     "theme",
		"21 Objectives": "objective",
	}
	if len(target.WatchDirs) != len(want) {
		t.Fatalf("expected %d watch dirs, got %d", len(want), len(target.WatchDirs))
	}
	for _, dir := range target.WatchDirs {
		if want[dir.Dir] != dir.Kind {
			t.Fatalf("unexpected watch dir %q kind %q", dir.Dir, dir.Kind)
		}
	}
}

func TestCreateConnectionManager(t *testing.T) {
	manager := CreateConnectionManager()
	if manager.Count() != 0 {
		t.Fatalf("expected a fresh manager to have no clients, got %d", manager.Count())
	}
}

func TestCreateWatcherSurfacesLoaderError(t *testing.T) {
	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns(nil, stderrors.New("config unavailable"))
	err := CreateWatcher(
		loader,
		websocket.NewConnectionManager(websocket.NewMetrics()),
		testPageIndex(),
		ops.NewWatchOperation(),
	)(context.Background())
	if err == nil {
		t.Fatal("expected the loader error to surface")
	}
}

func TestCreateWatcherStopsOnContextCancel(t *testing.T) {
	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CreateWatcher(
		loader,
		websocket.NewConnectionManager(websocket.NewMetrics()),
		testPageIndex(),
		ops.NewWatchOperation(),
	)(ctx); err != nil {
		t.Fatalf("expected nil on context cancel, got %v", err)
	}
}
