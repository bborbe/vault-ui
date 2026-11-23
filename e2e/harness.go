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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
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
	// adminPort is the free port the binary's admin server binds, so the suite
	// never competes with the live board's :9090.
	adminPort int
	// stubServer is the fixture vault-api the binary reads, stubURL its origin,
	// and stubRoot the fixture vault the stub serves from.
	stubServer *http.Server
	stubURL    string
	stubRoot   string
)

// startStub runs a fixture vault-api serving the seeded fixture's tasks, goals
// and topics, so the board reads a hermetic source instead of the live one. Its
// vault name is "fixture", which the board's case-insensitive match accepts.
func startStub(ctx context.Context, root string) error {
	port, err := freePort(ctx)
	if err != nil {
		return err
	}
	stubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	stubRoot = root
	return listenStub(ctx)
}

// restartStub re-listens on the same port after stopStub, so a spec that
// simulated the outage does not strand the specs that run after it.
func restartStub(ctx context.Context) error {
	return listenStub(ctx)
}

func listenStub(ctx context.Context) error {
	addr := strings.TrimPrefix(stubURL, "http://")
	stubServer = &http.Server{Addr: addr, Handler: stubHandler()}
	go func() { _ = stubServer.ListenAndServe() }()
	return waitStub(ctx)
}

// stubHandler serves the seeded fixture as the vault-api list endpoints. It
// re-reads the fixture vault on every request rather than answering a fixed
// body, so a spec that writes a page into the vault sees it on the board's next
// poll — the propagation path the board has now that no page-index watcher
// exists.
func stubHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tasks", func(w http.ResponseWriter, _ *http.Request) {
		writeStubJSON(w, stubList("25 Tasks"))
	})
	mux.HandleFunc("/api/v1/goals", func(w http.ResponseWriter, _ *http.Request) {
		writeStubJSON(w, stubList("24 Goals"))
	})
	mux.HandleFunc("/api/v1/topics", func(w http.ResponseWriter, _ *http.Request) {
		writeStubJSON(w, stubList("23 Topics"))
	})
	return mux
}

// stubItem is one fixture page rendered as a vault-api list item. Only the
// fields the board reads are filled.
type stubItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Status   string `json:"status"`
	Phase    string `json:"phase,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Vault    string `json:"vault"`
}

// stubList renders every page in the fixture's dir as a list item.
func stubList(dir string) string {
	items := make([]stubItem, 0, 4)
	entries, err := os.ReadDir(filepath.Join(stubRoot, dir))
	if err != nil {
		glog.Warningf("stub list %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(stubRoot, dir, entry.Name())
		front := stubFrontmatter(path)
		title := strings.TrimSuffix(entry.Name(), ".md")
		items = append(items, stubItem{
			ID:       title,
			Title:    title,
			Path:     path,
			Status:   front["status"],
			Phase:    front["phase"],
			Assignee: front["assignee"],
			Vault:    "fixture",
		})
	}
	// Mirror the real envelope, including `warnings` as a NUMBER. The stub used
	// to send only `items`, so a client that declared the field as []string
	// passed the whole suite and then failed every live poll with "cannot
	// unmarshal number into Go struct field taskList.warnings".
	body, err := json.Marshal(map[string]any{
		"items":             items,
		"total":             len(items),
		"warnings":          0,
		"index_age_seconds": 0,
	})
	if err != nil {
		glog.Warningf("stub list %s marshal: %v", dir, err)
		return `{"items":[]}`
	}
	return string(body)
}

// stubFrontmatter reads a page's top-level frontmatter keys. Values are
// unquoted; nested structures are not needed by the fixture.
func stubFrontmatter(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		glog.Warningf("stub frontmatter %s: %v", path, err)
		return out
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return out
}

// waitStub polls until the stub serves its tasks route.
func waitStub(ctx context.Context) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx, ctx.Err(), "wait stub")
		case <-deadline:
			return errors.Errorf(ctx, "stub did not serve within 10s")
		case <-ticker.C:
			resp, err := client.Get(stubURL + "/api/v1/tasks")
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

// writeStubJSON writes a fixed JSON body.
func writeStubJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// stopStub closes the stub so the board's next poll fails — the outage state.
func stopStub() {
	if stubServer != nil {
		_ = stubServer.Close()
		stubServer = nil
	}
}

// buildBinary compiles the real binary.
func buildBinary(ctx context.Context, repoRoot, out string) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", out, ".")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	glog.V(2).Infof("go build -o %s . -> err=%v", out, err)
	if err != nil {
		return errors.Wrapf(ctx, err, "build binary: %s", tail(string(output), 2000))
	}
	return nil
}

// freePort reserves a port by binding it and closing the listener.
func freePort(ctx context.Context) (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.Wrap(ctx, err, "reserve port")
	}
	defer func() { _ = listener.Close() }()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.Errorf(ctx, "unexpected address type %T", listener.Addr())
	}
	return addr.Port, nil
}

// seedFixture writes a throwaway vault holding what the scenarios need: two
// tasks in different statuses (one sessionless), one goal and one topic.
func seedFixture(ctx context.Context, root string) error {
	for _, dir := range []string{"25 Tasks", "24 Goals", "23 Topics", "50 Knowledge Base"} {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx, ctx.Err(), "seed fixture")
		default:
		}
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			return errors.Wrapf(ctx, err, "create %s", dir)
		}
	}
	files := map[string]string{
		filepath.Join("25 Tasks", "Sessionless Fixture Task.md"):        taskBody("Sessionless Fixture Task", "in_progress", "execution", ""),
		filepath.Join("25 Tasks", "Held Fixture Task.md"):               taskBody("Held Fixture Task", "hold", "todo", ""),
		filepath.Join("25 Tasks", "Card Page Fixture.md"):               cardPageBody(),
		filepath.Join("25 Tasks", "Link Target Fixture.md"):             linkTargetBody(),
		filepath.Join("24 Goals", "Cross Folder Fixture.md"):            crossFolderGoalBody(),
		filepath.Join("50 Knowledge Base", "No Frontmatter Fixture.md"): noFrontmatterBody(),
		filepath.Join("24 Goals", "Fixture Goal.md"):                    pageBody("goal", "Fixture Goal", ""),
		filepath.Join("23 Topics", "Fixture Topic.md"):                  pageBody("topic", "Fixture Topic", ""),
		filepath.Join("23 Topics", "Assigned Fixture Topic.md"):         pageBody("topic", "Assigned Fixture Topic", "e2e"),
	}
	for rel, body := range files {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx, ctx.Err(), "seed fixture")
		default:
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			return errors.Wrapf(ctx, err, "write %s", rel)
		}
	}
	return nil
}

// taskBody renders a minimal task page the board can parse.
func taskBody(title, status, phase, assignee string) string {
	return fmt.Sprintf(`---
page_type: task
status: %s
phase: %s
priority: 2
created_date: "2026-11-13"%s
---
Tags: [[Task]]

---

%s, written by the e2e fixture.
`, status, phase, assigneeLine(assignee), title)
}

// cardPageBody renders a task whose page carries a table and a resolvable
// wikilink, so the page-view specs assert rendered HTML and in-app navigation
// instead of a URL match.
func cardPageBody() string {
	return `---
page_type: task
status: in_progress
phase: execution
priority: 2
created_date: "2026-11-13"
---
Tags: [[Task]]

---

## Fixture table

| Col A | Col B |
| --- | --- |
| alpha | beta |

See [[Link Target Fixture]] for more.
`
}

// linkTargetBody renders the wikilink target, carrying a body marker the
// navigation assertion looks for.
func linkTargetBody() string {
	return `---
page_type: task
status: in_progress
phase: execution
priority: 2
created_date: "2026-11-13"
---
Tags: [[Task]]

---

Link target body marker, written by the e2e fixture.
`
}

// crossFolderGoalBody renders a goal that links a page in another folder, so the
// cross-folder resolver and backlink specs have a target.
func crossFolderGoalBody() string {
	return `---
page_type: goal
status: in_progress
created_date: "2026-11-13"
---
Tags: [[Goal]]

---

# Cross Folder Fixture

See [[Card Page Fixture]] for the task.
`
}

// noFrontmatterBody renders a page with no frontmatter block, so the page route
// proves it renders such a page instead of erroring.
func noFrontmatterBody() string {
	return `# No Frontmatter Fixture

This page carries no frontmatter block.
`
}

// assigneeLine renders the assignee frontmatter key, or nothing when unset.
func assigneeLine(assignee string) string {
	if assignee == "" {
		return ""
	}
	return "\nassignee: " + assignee
}

// writeFixtureTask drops a new task into a fixture vault mid-run, which is how
// the "vault change reaches the open board" assertion gets its change.
func writeFixtureTask(ctx context.Context, vaultPath, title string) error {
	body := taskBody(title, "in_progress", "execution", "")
	path := filepath.Join(vaultPath, "25 Tasks", title+".md")
	return errors.Wrapf(ctx, os.WriteFile(path, []byte(body), 0o600), "write %s", title)
}

// pageBody renders a minimal goal or topic page.
func pageBody(kind, title, assignee string) string {
	return fmt.Sprintf(`---
page_type: %s
status: in_progress
created_date: "2026-11-13"%s
---
Tags: [[%s]]

---

%s, written by the e2e fixture.
`, kind, assigneeLine(assignee), kind, title)
}

// writeConfig writes both config files: vault-ui's own, and the vault-cli one
// its loader merges. Missing either half fails the load with a message that
// reads as a harness bug.
func writeConfig(ctx context.Context, homeDir, vaultPath string) error {
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
	reservedAdminPort, err := freePort(ctx)
	if err != nil {
		return errors.Wrap(ctx, err, "reserve admin port")
	}
	adminPort = reservedAdminPort
	cmd := exec.Command(binaryPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("VAULT_UI_LISTEN=127.0.0.1:%d", port),
		fmt.Sprintf("VAULT_UI_ADMIN_LISTEN=127.0.0.1:%d", adminPort),
		"HOME="+homeDir,
		"VAULT_API_URL="+stubURL,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return errors.Wrap(ctx, err, "start binary")
	}
	proc = cmd
	glog.V(2).Infof("started binary pid=%d port=%d", cmd.Process.Pid, port)

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitReady(ctx); err != nil {
		// Stop first, then read: os/exec copies the child's stderr in its own
		// goroutine and only Wait joins it. Reading before the stop races that
		// copy and drops whatever the binary wrote on the way out — which is
		// exactly the output a failed start needs.
		stopBinary()
		procStderr = stderr.String()
		return errors.Wrapf(ctx, err, "wait ready: %s", tail(procStderr, 2000))
	}
	return nil
}

// waitReady polls the admin port's /readiness, then proves the API port serves
// the merged config. Readiness alone only proves the vault-cli half loaded.
func waitReady(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx, ctx.Err(), "wait readiness")
		case <-deadline:
			return errors.Errorf(ctx, "readiness did not answer 200 within 30s")
		case <-ticker.C:
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/readiness", adminPort))
			if err != nil {
				continue
			}
			status := resp.StatusCode
			_ = resp.Body.Close()
			if status == http.StatusOK {
				glog.V(2).Infof("readiness answered 200")
				return proveAPIConfig(ctx, client)
			}
		}
	}
}

// proveAPIConfig checks the API port serves the merged config.
func proveAPIConfig(ctx context.Context, client *http.Client) error {
	resp, err := client.Get(baseURL + "/api/vaults")
	if err != nil {
		return errors.Wrap(ctx, err, "get /api/vaults")
	}
	defer func() { _ = resp.Body.Close() }()
	glog.V(2).Infof("GET /api/vaults -> %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf(ctx, "/api/vaults returned %d", resp.StatusCode)
	}
	return nil
}

// stopBinary terminates the running binary and waits for it, which is what lets
// the stderr copy finish before the buffer is read.
func stopBinary() {
	if proc == nil || proc.Process == nil {
		return
	}
	_ = proc.Process.Kill()
	_ = proc.Wait()
	proc = nil
}

// There is deliberately no "did the child exit early?" probe here. An unreaped
// child is a zombie, and a zombie still answers Signal(0), so every cheap
// liveness check reports alive; detecting the exit means calling Wait, which
// only one owner may do. A dead binary therefore costs the full 30s timeout —
// and the error carries the captured stderr, which is what actually diagnoses it.

// tail keeps the last n bytes of s, for failure messages.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
