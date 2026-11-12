// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package e2e drives the board in a real browser against a real binary.
//
// This file carries no build tag on purpose: everything here needs no browser,
// so `make precommit` compiles and tests it. Only the Ginkgo specs and the
// Playwright wiring carry `//go:build e2e`.
package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bborbe/errors"
)

var (
	// binaryPath is the built vault-ui binary under test.
	binaryPath string
	// baseURL is the API origin the specs drive.
	baseURL string
	// proc is the running binary, nil when stopped.
	proc *exec.Cmd
	// procStderr holds the binary's stderr for a failure message.
	procStderr string
	// homeDir is the temp HOME the binary reads its config from.
	homeDir string
)

// buildBinary compiles the real binary.
func buildBinary(ctx context.Context, repoRoot, out string) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", out, ".")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(ctx, err, "build binary: %s", tail(string(output), 2000))
	}
	return nil
}

// freePort reserves a port by binding it and closing the listener.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.Wrap(context.Background(), err, "reserve port")
	}
	defer func() { _ = listener.Close() }()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.Errorf(context.Background(), "unexpected address type %T", listener.Addr())
	}
	return addr.Port, nil
}

// seedFixture writes a throwaway vault holding what the scenarios need: two
// tasks in different statuses (one sessionless), one goal and one topic.
func seedFixture(root string) error {
	ctx := context.Background()
	for _, dir := range []string{"25 Tasks", "24 Goals", "23 Topics"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			return errors.Wrapf(ctx, err, "create %s", dir)
		}
	}
	files := map[string]string{
		filepath.Join("25 Tasks", "Sessionless Fixture Task.md"): taskBody("Sessionless Fixture Task", "in_progress", "execution"),
		filepath.Join("25 Tasks", "Held Fixture Task.md"):        taskBody("Held Fixture Task", "hold", "todo"),
		filepath.Join("24 Goals", "Fixture Goal.md"):             pageBody("goal", "Fixture Goal"),
		filepath.Join("23 Topics", "Fixture Topic.md"):           pageBody("topic", "Fixture Topic"),
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return errors.Wrapf(ctx, err, "write %s", rel)
		}
	}
	return nil
}

// taskBody renders a minimal task page the board can parse.
func taskBody(title, status, phase string) string {
	return fmt.Sprintf(`---
page_type: task
status: %s
phase: %s
priority: 2
created_date: "2026-11-13"
---
Tags: [[Task]]

---

%s, written by the e2e fixture.
`, status, phase, title)
}

// writeFixtureTask drops a new task into a fixture vault mid-run, which is how
// the "vault change reaches the open board" assertion gets its change.
func writeFixtureTask(vaultPath, title string) error {
	body := taskBody(title, "in_progress", "execution")
	path := filepath.Join(vaultPath, "25 Tasks", title+".md")
	return errors.Wrapf(context.Background(), os.WriteFile(path, []byte(body), 0o600), "write %s", title)
}

// pageBody renders a minimal goal or topic page.
func pageBody(kind, title string) string {
	return fmt.Sprintf(`---
page_type: %s
status: in_progress
created_date: "2026-11-13"
---
Tags: [[%s]]

---

%s, written by the e2e fixture.
`, kind, kind, title)
}

// writeConfig writes both config files: vault-ui's own, and the vault-cli one
// its loader merges. Missing either half fails the load with a message that
// reads as a harness bug.
func writeConfig(homeDir, vaultPath string) error {
	ctx := context.Background()

	vaultCLIDir := filepath.Join(homeDir, ".config", "vault-cli")
	if err := os.MkdirAll(vaultCLIDir, 0o750); err != nil {
		return errors.Wrap(ctx, err, "create vault-cli config dir")
	}
	vaultCLI := fmt.Sprintf(`current_user: e2e
vaults:
  fixture:
    name: fixture
    path: %s
    tasks_dir: 25 Tasks
    goals_dir: 24 Goals
    topics_dir: 23 Topics
`, vaultPath)
	if err := os.WriteFile(filepath.Join(vaultCLIDir, "config.yaml"), []byte(vaultCLI), 0o600); err != nil {
		return errors.Wrap(ctx, err, "write vault-cli config")
	}

	vaultUIDir := filepath.Join(homeDir, ".config", "vault-ui")
	if err := os.MkdirAll(vaultUIDir, 0o750); err != nil {
		return errors.Wrap(ctx, err, "create vault-ui config dir")
	}
	if err := os.WriteFile(filepath.Join(vaultUIDir, "config.yaml"), []byte("vaults:\n  fixture:\n"), 0o600); err != nil {
		return errors.Wrap(ctx, err, "write vault-ui config")
	}
	return nil
}

// startBinary launches the binary on the API port and waits until it serves.
// HOME is set on the child's environment only — never os.Setenv, which would
// repoint Playwright's driver cache out of the real home.
func startBinary(ctx context.Context, port int) error {
	cmd := exec.Command(binaryPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("VAULT_UI_LISTEN=127.0.0.1:%d", port),
		"HOME="+homeDir,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(ctx, err, "capture stderr")
	}
	if err := cmd.Start(); err != nil {
		return errors.Wrap(ctx, err, "start binary")
	}
	proc = cmd
	go func() {
		buf, _ := io.ReadAll(stderr)
		procStderr = string(buf)
	}()

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitReady(ctx); err != nil {
		stopBinary()
		return errors.Wrapf(ctx, err, "wait ready: %s", tail(procStderr, 2000))
	}
	return nil
}

// waitReady polls the admin port's /readiness, then proves the API port serves
// the merged config. Readiness alone only proves the vault-cli half loaded.
func waitReady(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if proc != nil && proc.ProcessState != nil {
			return errors.Errorf(ctx, "binary exited before readiness")
		}
		resp, err := client.Get("http://127.0.0.1:9090/readiness")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return proveAPIConfig(ctx, client)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.Errorf(ctx, "readiness did not answer 200 within 30s")
}

// proveAPIConfig checks the API port serves the merged config.
func proveAPIConfig(ctx context.Context, client *http.Client) error {
	resp, err := client.Get(baseURL + "/api/vaults")
	if err != nil {
		return errors.Wrap(ctx, err, "get /api/vaults")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf(ctx, "/api/vaults returned %d", resp.StatusCode)
	}
	return nil
}

// stopBinary terminates the running binary.
func stopBinary() {
	if proc == nil || proc.Process == nil {
		return
	}
	_ = proc.Process.Kill()
	_, _ = proc.Process.Wait()
	proc = nil
}

// tail keeps the last n bytes of s, for failure messages.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
