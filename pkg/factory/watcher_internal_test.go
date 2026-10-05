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
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// testPageIndex returns a real page index over empty storage, enough for the
// watcher tests that only assert frame delivery and lifecycle.
func testPageIndex() pageindex.PageIndex {
	return CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime())
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
	go func() { _ = CreateWatcher(loader, manager, testPageIndex(), ops.NewWatchOperation())(ctx) }()

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
