// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// TestFreePortReturnsABindablePort proves freePort hands back a port the caller
// can actually bind — the whole harness depends on it and a stale or reused
// value fails as a confusing bind error much later.
func TestFreePortReturnsABindablePort(t *testing.T) {
	port, err := freePort()
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
	if err := seedFixture(root); err != nil {
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

// TestWriteConfigProducesAConfigTheLoaderAccepts is the only guard on the two
// hand-written YAML files: `make e2e` cannot run in a container, so a wrong
// config path or a missing current_user would ship silently.
func TestWriteConfigProducesAConfigTheLoaderAccepts(t *testing.T) {
	homeDir := t.TempDir()
	vaultPath := filepath.Join(homeDir, "fixture-vault")
	if err := seedFixture(vaultPath); err != nil {
		t.Fatalf("seedFixture: %v", err)
	}
	if err := writeConfig(homeDir, vaultPath); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	t.Setenv("HOME", homeDir)

	path := filepath.Join(homeDir, ".config", "vault-ui", "config.yaml")
	config, err := vaultconfig.Load(context.Background(), factory.CreateConfigLoader(""), path)
	if err != nil {
		t.Fatalf("vaultconfig.Load: %v", err)
	}
	if len(config.Vaults) != 1 {
		t.Fatalf("expected exactly one vault, got %d", len(config.Vaults))
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
}
