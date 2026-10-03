---
status: approved
spec: [022-go-backend-private-logic]
created: "2026-10-03T23:45:00Z"
queued: "2026-10-03T23:06:15Z"
branch: dark-factory/go-backend-private-logic
---

# Add the vault-cli watcher supervisor and the WezTerm pane resolver

<summary>
- The Go backend can run the vault file-change watcher: one subprocess covers every configured vault, and each parsed JSON event is dispatched to a callback.
- The supervisor restarts the watcher subprocess after every exit while no stop was requested; a stop during the restart window suppresses the restart, and an error naming the vaults and the exit code is logged only when the exit code is neither 0 nor SIGTERM.
- A line that is not valid JSON is logged and skipped; the event stream continues and no error is returned.
- Stopping the supervisor sends SIGTERM and escalates to kill after the stop timeout, so a wedged subprocess cannot linger.
- The Go backend can resolve a live session to its WezTerm pane and proxy a jump to it, reproducing the Python resolution order and its bounded timeouts.
- A wedged pane-resolution helper is killed and the call reports no pane, so the waiting HTTP request is never held open.
- The jump credential is read stripped and is never logged and never returned to a browser; only its path ever reaches a log line.
- The subprocess environment prepends the WezTerm bundle and selects the newest live GUI socket without mutating the service's own environment.
- A stale socket left by an exited GUI, and a socket name whose suffix is not ASCII digits, are skipped rather than handed to WezTerm.
- The jump percent-encodes both query values and reports a non-2xx status as an error.
</summary>

<objective>
Port the vault-cli watcher supervisor (`vault_cli_watcher.py`) and the WezTerm pane resolver (`pane_resolver.py`) into `pkg/watcher` and `pkg/pane`, reproducing the restart/stop lifecycle, the malformed-line tolerance, the pane-resolution order, the bounded helper, and the jump-credential handling the Python modules and their pytest suites assert.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` plus subpackages on a real split trigger.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry-point, `DescribeTable`/`Entry`, suite timeout, `Eventually`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — `run.*` combinators over raw `go func()`; caller-owned channels.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — respect `ctx.Done()` in the restart loop and the bounded helper wait.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; an expected absence (no pane, no token) is NOT an error.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md` — gosec: the token file is `0600`; never log the token; never shell-interpolate.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the spec `specs/in-progress/022-go-backend-private-logic.md` (Desired Behavior 6, 7; Acceptance Criteria 11, 12; Failure Modes rows for the watcher and pane resolution; Security / Abuse Cases — the jump credential, the WezTerm socket validation, subprocess args as argv).

Read the Python source that is the contract — reproduce it exactly:
- `src/vault_ui/vault_cli_watcher.py` — `_RESTART_DELAY_SECONDS`, `_STOP_TIMEOUT_SECONDS`, `VaultCLIWatcher` (`start`, `_run_subprocess`, `_handle_line`, `terminate`, `stop`).
- `src/vault_ui/pane_resolver.py` — `_RESOLVE_TIMEOUT_SECONDS`, `_JUMP_TIMEOUT_SECONDS`, `_JUMP_SERVER_URL`, `_WEZTERM_BUNDLE_DIR`, `_jump_token_path`, `_who_needs_me_path`, `_wezterm_bin_dir`, `_pid_alive`, `_wezterm_gui_socket`, `_subprocess_env`, `read_jump_token`, `resolve_pane_id`, `perform_jump`.
- The matching pytest suites are the regression lock: `tests/test_vault_cli_watcher.py`, `tests/test_pane_resolver.py`.

Read the previous prompt `prompts/4-spec-022-topology-and-status-cache.md` — the caller derives the watcher's inputs (the vault names and the `vault-cli` path) from `pkg/vaultconfig.Config` (`Vault.Name`, `Vault.VaultCLIPath`), exactly as the Python factory does; this prompt's watcher takes them as parameters so it stays testable in isolation.

The module is `github.com/bborbe/vault-ui` at the repo root (established by spec 021). `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` are already direct dependencies. Unlike vault operations (which go through vault-cli as a library), the watcher subprocess, the `ps` scan, and the pane helper are legitimate `os/exec` calls — this is the ONLY place in the port that spawns an external process.

**Package-per-AC-name rule.** Ginkgo allows exactly ONE `RunSpecs` per test binary, so one Go package exposes exactly one suite entry-point. The spec pins `go test -run TestWatcherSupervisor` and `go test -run TestPaneResolver`, so they are two packages with suite entry-points named exactly `TestWatcherSupervisor` and `TestPaneResolver`. Do NOT merge them and do NOT rename the entry-points.
</context>

<requirements>

### 1. Create `pkg/watcher/` — the vault-cli watcher supervisor

Package name `watcher`. Files: `watcher.go`, `watcher_suite_test.go`, `watcher_test.go`.

Port `VaultCLIWatcher` from `src/vault_ui/vault_cli_watcher.py`.

Exported contract:

```go
package watcher

import (
    "context"
    "time"
)

// Event is one parsed vault-cli watch event.
type Event struct {
    EventType string // the "event" field
    ItemID    string // the "name" field
    Vault     string // the "vault" field, defaulting to the first watched vault
    Kind      string // the "type" field (task/goal/theme/objective), or ""
}

// EventHandler receives every dispatched event.
type EventHandler func(Event)

// Supervisor runs one `vault-cli watch` subprocess for all vaults.
type Supervisor interface {
    // Run starts the subprocess and reads events until ctx is cancelled or Stop
    // is called. It restarts the subprocess after restartDelay on every exit
    // while no stop was requested.
    Run(ctx context.Context) error
    // Stop requests a stop and shuts the subprocess down with SIGTERM, escalating
    // to kill after stopTimeout.
    Stop(ctx context.Context) error
}

// NewSupervisor builds a supervisor for the given vaults. vaultNames are joined
// with commas into one --vault value; restartDelay and stopTimeout are injected.
func NewSupervisor(vaultCLIPath string, vaultNames []string, onEvent EventHandler, restartDelay, stopTimeout time.Duration) Supervisor

// Defaults for the injected durations, carried by value.
const (
    DefaultRestartDelay = 5 * time.Second
    DefaultStopTimeout  = 5 * time.Second
)
```

Behavior notes to reproduce exactly:
- The subprocess argv is exactly `[vaultCLIPath, "watch", "--vault", <comma-joined names>, "--types", "task,goal,theme,objective"]` — one `--vault` value regardless of how many vaults. Passed as argv, never through a shell.
- One subprocess covers every vault.
- `_handle_line`: parse JSON; on failure log at warning and skip (no event, no error). An event is dispatched only when both `event` and `name` are non-empty; `vault` falls back to the first watched vault; `kind` falls back to `""`.
- After the subprocess exits, restart it after `restartDelay` on every exit while no stop was requested. Log an error naming the vaults and the exit code only when the exit code is neither 0 nor `-SIGTERM`.
- A stop during the restart window sets the stop flag so no new subprocess is spawned.
- `Stop` sends SIGTERM, waits up to `stopTimeout`, then kills.

### 2. Create `pkg/pane/` — the WezTerm pane resolver

Package name `pane`. Files: `pane.go`, `pane_suite_test.go`, `pane_test.go`.

Port `pane_resolver.py`. The jump credential is a single stripped line in a `0600` file; it is never logged (not even truncated, not on the error path) and never returned to a browser. Route the pane package's logging through an injectable logger (a `logger`/`sink` parameter or field, or a logger configured to write to a buffer/stderr the test captures), so a test can prove the value never reaches a log line.

Exported contract:

```go
package pane

import (
    "context"
    "time"
)

// ReadJumpToken returns the stripped jump credential, or ("", false) when the
// file is missing, unreadable, or whitespace-only. The token VALUE is never
// logged and never returned to any caller that would expose it.
func ReadJumpToken(path string) (string, bool)

// WhoNeedsMePath resolves the pane-resolution script path: the pluginRoot prefix
// wins when non-empty, else the default marketplace location under homeDir.
func WhoNeedsMePath(pluginRoot, homeDir string) string

// WeztermBinDir returns the WezTerm bundle dir when the wezterm binary exists in
// it, else ("", false). Only that one known location is checked.
func WeztermBinDir(bundleDir string) (string, bool)

// PidAlive reports whether pid is alive and signalable (a permission error and a
// non-positive pid both count as not-ours-to-use).
func PidAlive(pid int) bool

// WeztermGuiSocket returns the newest live `gui-sock-<pid>` under dir, or
// ("", false). A candidate counts only when the pid is alive and the suffix is
// ASCII digits.
func WeztermGuiSocket(dir string, pidAlive func(int) bool) (string, bool)

// BuildSubprocessEnv returns a copy of env adjusted for the pane helper: the
// WezTerm bundle dir prepended to PATH when present, and WEZTERM_UNIX_SOCKET
// pointed at the newest live GUI socket when that variable is unset/empty. An
// explicitly set socket always wins. It NEVER mutates env.
func BuildSubprocessEnv(env []string, homeDir, bundleDir string, pidAlive func(int) bool) []string

// ResolvePaneID runs `interpreter whoNeedsMePath --pane-for <first 8 chars of
// sessionID>` with env, bounded by timeout. Returns ("", false) for every
// failure: no session id, a spawn failure, a non-zero exit, empty output, or a
// helper killed on timeout (the killed helper must not outlive the call).
func ResolvePaneID(ctx context.Context, interpreter, whoNeedsMePath, sessionID string, env []string, timeout time.Duration) (string, bool)

// PerformJump asks the fleet-jump server to activate paneID. Both query values
// are percent-encoded. Returns an error for a non-2xx status or a transport
// failure.
func PerformJump(ctx context.Context, jumpServerURL, paneID, token string, timeout time.Duration) error

// Defaults for the injected durations, carried by value.
const (
    DefaultResolveTimeout = 5 * time.Second
    DefaultJumpTimeout    = 5 * time.Second
)
```

Behavior notes to reproduce exactly:
- `ReadJumpToken` logs only the path and the exception, never the value.
- `ResolvePaneID` passes only the FIRST 8 characters of the session id to `--pane-for`, and uses `os/exec` argv (never a shell). On timeout it kills the process and reaps it.
- `BuildSubprocessEnv` copies the environment; a mutated PATH or socket must never leak into the service.
- `PerformJump` uses `net/http` with `url.QueryEscape` (or equivalent) on BOTH `pane` and `t`.
- The WezTerm socket directory is `~/.local/share/wezterm`; only `gui-sock-<pid>` names with an ASCII-digit suffix and a live pid are candidates; the newest by mtime wins.

### 3. Tests — two Ginkgo suites

Each package's `<pkg>_suite_test.go` uses the standard suite body with the entry-point named exactly as the AC, NOT `TestSuite`.

**`pkg/watcher` — suite entry `TestWatcherSupervisor`.** Mirror `tests/test_vault_cli_watcher.py`. Inject the subprocess by a seam (e.g. an unexported `execCommand` func field on the supervisor, or an exported `WithCommandRunner` option) so no real `vault-cli` runs. Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions):
- `restart-after-nonzero-exit` — a subprocess that exits non-zero is followed by a SECOND subprocess spawn after the restart delay.
- `malformed-json-line-is-skipped` — a non-JSON line makes the callback receive no event and `Run` return no error; a subsequent well-formed line is still dispatched.
Also cover: a valid event dispatches `(eventType, itemID, vault, kind)`; empty lines are skipped; an event with an empty name is skipped; the `vault` field defaults to the first watched vault; a missing `type` yields an empty kind; the argv is exactly the six-element tuple above with one `--vault`; a single vault still produces one `--vault`; `Stop` sends SIGTERM and waits; a stop during the restart window suppresses the restart; `Run` exits cleanly on context cancellation.

**`pkg/pane` — suite entry `TestPaneResolver`.** Mirror `tests/test_pane_resolver.py`. Point the token path, script path, bundle dir, socket dir, and subprocess call at tmp paths or fakes. Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions):
- `timeout-kills-helper` — a helper that hangs past its timeout is killed and `ResolvePaneID` returns `("", false)`.
- `jump-percent-encodes-both-query-values` — a pane id and a token containing `&`, `=`, space, `/`, `?` both survive intact in the request URL (decode the query and compare).
Also cover (negative evidence): the token value never appears in a log line — drive both the success and the error path of `ReadJumpToken` with a sentinel value through the injectable logger and assert it is absent from the captured output; the test must FAIL if the implementation logs the value. Also: stripped value returned; missing path returns false; whitespace-only returns false; `WhoNeedsMePath` prefers the plugin root; `WeztermBinDir` present/absent; `PidAlive` true for the current process and false for a non-positive pid; `WeztermGuiSocket` picks the newest live socket, skips a dead pid, ignores non-matching names and a non-ASCII-digit suffix; `BuildSubprocessEnv` prepends the bundle and never mutates the input env; `ResolvePaneID` argv is exactly `[interpreter, script, "--pane-for", <first 8 chars>]`; empty stdout and a non-zero exit return false; `PerformJump` returns an error on a non-2xx status.

Do NOT use stdlib `func TestX(t *testing.T)` tables. Do NOT name any suite entry-point `TestSuite`.

### 4. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- feat: Port the vault-cli watcher supervisor and the WezTerm pane resolver to Go, with the restart/stop lifecycle, the bounded helper, and the jump-credential handling preserved.`
Do NOT modify any existing section.

### 5. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.

</requirements>

<constraints>
- Copy of spec 022 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - **Behavior parity is the contract** — reproduce `src/vault_ui/vault_cli_watcher.py` and `src/vault_ui/pane_resolver.py` exactly; the pytest suites are the reference. No behavior added, removed, or "improved".
  - **The Python backend keeps working until spec 3's cutover.** Do NOT change anything under `src/`.
  - **Time is injectable** — the restart delay, stop timeout, resolve timeout, and jump timeout are parameters, not wall-clock reads inside the logic (`go-time-injection`).
  - **Errors are wrapped and classified** so a caller can distinguish an expected absence (no token, no pane, a non-zero helper exit) from an unexpected failure (`go-error-wrapping-guide`).
  - **Security invariants (verbatim, from the spec):** the jump credential never leaves the server — the token is read from a `0600` file, is never logged (not even truncated, not on the error path), and is never returned to the browser. Subprocess arguments are passed as argv, never a shell. The WezTerm socket is validated before use — a `gui-sock-<pid>` candidate counts only when the pid is alive and signalable and the suffix is ASCII digits. What can hang or retry forever is bounded: the pane-resolution helper is killed on timeout; the watcher subprocess restarts on a delay and is suppressed during shutdown. No path retries without a bound.
  - Coding guides to follow (do not inline): `go-testing-guide`, `go-concurrency-patterns`, `go-context-cancellation-in-loops`, `go-security-linting`, `go-error-wrapping-guide`, `go-package-layout-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: branch`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT touch `src/`, `tests/`, or `Makefile`.
- Do NOT wire these packages into `pkg/factory` or `main.go` — that is spec 3's job.
- Do NOT log, return, or embed the jump token value anywhere. Only the token PATH may appear in a log line.
- Do NOT mutate the process environment when building the helper's environment — always copy.
- The container masks `.git` (`hideGit: true`). If `go build`/`go test` fails with a VCS-stamping error, add `-buildvcs=false`. Do NOT change the Makefile.
- Existing Go tests from spec 021 and prompts 1-4 must still pass.

<!-- OPEN QUESTION for the human auditor: the Python `resolve_pane_id` spawns `sys.executable` (the current interpreter). The Go port cannot know that, so `ResolvePaneID` takes an injectable `interpreter` parameter (e.g. "python3"); the caller/spec 3 picks the value. This is a mechanical translation, not a behavior change. -->
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test -run TestWatcherSupervisor ./pkg/watcher/` — must exit 0.
4. `go test -run TestPaneResolver ./pkg/pane/` — must exit 0.
5. `go test ./...` — must exit 0.
6. `go test -race ./...` — must exit 0.
7. `gofmt -l .` — must print nothing.
8. `grep -rnE '5[[:space:]]*\*[[:space:]]*time\.(Second|Minute)' pkg/watcher pkg/pane` — must print at least one line (the restart/stop and resolve/jump timeouts are carried by value).
9. `! grep -rn 'time.Now()' pkg/watcher pkg/pane` — must succeed (no direct wall-clock read).
10. `make test` — the existing pytest suite must still pass.
</verification>
