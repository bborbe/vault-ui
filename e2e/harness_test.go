// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// These are plain `testing` tests, not Ginkgo specs, and that is deliberate:
// the package already owns exactly one RunSpecs in e2e_suite_test.go, and a
// second Ginkgo suite here would panic in the tagged build. Keeping the
// browser-free half on stdlib testing is what lets `make precommit` run it.
package e2e

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// TestFreePortReturnsABindablePort proves freePort hands back a port the caller
// can actually bind — the whole harness depends on it and a stale or reused
// value fails as a confusing bind error much later.
func TestFreePortReturnsABindablePort(t *testing.T) {
	port, err := freePort(context.Background())
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("freePort returned out-of-range port %d", port)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("port %d is not bindable: %v", port, err)
	}
	_ = listener.Close()
}

// TestSeedFixtureWritesTheExpectedTree proves the fixture carries what the
// scenarios need: a sessionless task, a live task, a goal and a topic, under
// the folder names the config declares.
func TestSeedFixtureWritesTheExpectedTree(t *testing.T) {
	root := t.TempDir()
	if err := seedFixture(context.Background(), root); err != nil {
		t.Fatalf("seedFixture: %v", err)
	}
	for _, dir := range []string{"25 Tasks", "24 Goals", "23 Topics"} {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("expected %s to exist: %v", dir, err)
		}
	}
	tasks, err := filepath.Glob(filepath.Join(root, "25 Tasks", "*.md"))
	if err != nil {
		t.Fatalf("glob tasks: %v", err)
	}
	if len(tasks) < 2 {
		t.Fatalf("expected at least two tasks, got %d", len(tasks))
	}
	sessionless, err := os.ReadFile(filepath.Join(root, "25 Tasks", "Sessionless Fixture Task.md"))
	if err != nil {
		t.Fatalf("read sessionless task: %v", err)
	}
	if strings.Contains(string(sessionless), "claude_session_id:") {
		t.Fatalf("sessionless fixture task must carry no claude_session_id")
	}
}

// TestSeedFixtureParsesThroughTheRealLoader is the fixture's only real proof.
// The board's specs cannot run in a container, so a fixture whose markdown the
// loader rejects would fail only in the board-down window. ListTasksStrict is
// used deliberately: it fails on an unreadable file instead of silently listing
// a smaller set, which is exactly the failure a plain ListTasks would hide.
func TestSeedFixtureParsesThroughTheRealLoader(t *testing.T) {
	root := t.TempDir()
	if err := seedFixture(context.Background(), root); err != nil {
		t.Fatalf("seedFixture: %v", err)
	}
	storageConfig := &storage.Config{
		TasksDir:  "25 Tasks",
		GoalsDir:  "24 Goals",
		TopicsDir: "23 Topics",
	}

	tasks, err := storage.NewTaskStorage(storageConfig).ListTasksStrict(context.Background(), root)
	if err != nil {
		t.Fatalf("ListTasksStrict: %v", err)
	}
	if len(tasks) != 4+len(deferredFixtures(time.Now().UTC())) {
		t.Fatalf("expected %d fixture tasks, got %d", 4+len(deferredFixtures(time.Now().UTC())), len(tasks))
	}
	for _, task := range tasks {
		if task.Name == "Sessionless Fixture Task" && task.ClaudeSessionID() != "" {
			t.Fatalf("the sessionless fixture task must carry no claude_session_id")
		}
	}

	if _, err := storage.NewGoalStorage(storageConfig).
		FindGoalByName(context.Background(), root, "Fixture Goal"); err != nil {
		t.Fatalf("fixture goal does not parse: %v", err)
	}
}

// TestWriteConfigProducesAConfigTheLoaderAccepts is the only guard on the two
// hand-written YAML files: `make e2e` cannot run in a container, so a wrong
// config path or a missing current_user would ship silently.
func TestWriteConfigProducesAConfigTheLoaderAccepts(t *testing.T) {
	homeDir := t.TempDir()
	vaultPath := filepath.Join(homeDir, "fixture-vault")
	if err := seedFixture(context.Background(), vaultPath); err != nil {
		t.Fatalf("seedFixture: %v", err)
	}
	if err := writeConfig(context.Background(), homeDir, vaultPath); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	t.Setenv("HOME", homeDir)

	path := filepath.Join(homeDir, ".config", "vault-ui", "config.yaml")
	config, err := vaultconfig.Load(context.Background(), factory.CreateConfigLoader(""), path)
	if err != nil {
		t.Fatalf("vaultconfig.Load: %v", err)
	}
	if len(config.Vaults) != 2 {
		t.Fatalf("expected two vaults, got %d", len(config.Vaults))
	}
	vault := config.Vaults[0]
	if vault.Name != "fixture" {
		t.Fatalf("expected vault Name %q, got %q", "fixture", vault.Name)
	}
	if vault.TasksFolder != "25 Tasks" {
		t.Fatalf("expected TasksFolder %q, got %q", "25 Tasks", vault.TasksFolder)
	}
	if vault.GoalsFolder != "24 Goals" {
		t.Fatalf("expected GoalsFolder %q, got %q", "24 Goals", vault.GoalsFolder)
	}
	if vault.TopicsFolder != "23 Topics" {
		t.Fatalf("expected TopicsFolder %q, got %q", "23 Topics", vault.TopicsFolder)
	}
	// The second vault is the one the stub never serves. It must still survive
	// the merge, so the board can list it and mark it no-source.
	if config.Vaults[1].Name != "unserved" {
		t.Fatalf("expected second vault Name %q, got %q", "unserved", config.Vaults[1].Name)
	}
	if config.Vaults[1].TasksFolder != "25 Tasks" {
		t.Fatalf("expected unserved TasksFolder %q, got %q", "25 Tasks", config.Vaults[1].TasksFolder)
	}
}

// TestDeferredFixturesAreRemovable proves the seeded deferred pages can be
// removed, and that the probe can tell the difference — a cleanup assertion
// that also passes with nothing seeded proves nothing. It runs without a
// browser, on its own temp vault, so it cannot disturb the browser specs that
// assert against the shared fixture vault.
func TestDeferredFixturesAreRemovable(t *testing.T) {
	root := t.TempDir()
	if err := seedFixture(context.Background(), root); err != nil {
		t.Fatalf("seedFixture: %v", err)
	}

	want := 4 * len(deferredFixtureDirs)
	if got := countDeferredFixtures(root); got != want {
		t.Fatalf("positive control: expected %d seeded deferred fixtures, got %d", want, got)
	}

	remaining, err := removeDeferredFixtures(root)
	if err != nil {
		t.Fatalf("removeDeferredFixtures: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected 0 deferred fixtures after teardown, got %d", remaining)
	}

	storageConfig := &storage.Config{
		TasksDir:  "25 Tasks",
		GoalsDir:  "24 Goals",
		TopicsDir: "23 Topics",
	}
	tasks, err := storage.NewTaskStorage(storageConfig).ListTasksStrict(context.Background(), root)
	if err != nil {
		t.Fatalf("ListTasksStrict: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("expected the 4 base fixture tasks to survive the cleanup, got %d", len(tasks))
	}
}
