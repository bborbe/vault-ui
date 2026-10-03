---
status: approved
spec: [022-go-backend-private-logic]
created: "2026-10-03T23:45:00Z"
queued: "2026-10-03T23:06:15Z"
branch: dark-factory/go-backend-private-logic
---

# Add the process-termination guard and signal primitives

<summary>
- The Go backend can end a Claude session's process the same way the Python backend does, under the same named guards.
- A signal is sent only to a process resolved from a fresh (uncached) process scan taken immediately before the signal — never to a pid supplied by a caller.
- Only a real claude invocation carrying an exact session-pinning flag for the target id is ever a target; the launcher wrapper is never a target.
- The take-over path signals only a launch process (pinned with `--session-id`) and deliberately leaves an interactive resume (`--resume`) alone.
- When no process matches, nothing is signaled and the call reports that nothing was terminated.
- A process that exited between the scan and the signal, and a permission failure, are both non-fatal: the call reports "not terminated" without raising.
- A permission failure is logged at warning level naming the pid and the error; a process that simply vanished is not noisy.
- The one irreversible action in the whole port is isolated in its own reviewable package, separate from the liveness logic.
- The Python backend is untouched and keeps serving traffic.
</summary>

<objective>
Port the Python process-termination logic (`terminate_resumed_session`, `terminate_launch_process`, `item_has_live_launch`, and the `_sigterm_pid` primitive) into two Go packages — `pkg/terminate` for the guard logic and `pkg/sigterm` for the signal primitive and its failure paths — reproducing the exact guard conditions and non-fatal failure handling the Python modules and their pytest suites assert.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` plus subpackages on a real split trigger; each package below owns one cohesive domain.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry-point, `DescribeTable`/`Entry` (never stdlib `t.Run` tables), suite timeout.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; an expected absence (no matching process) is NOT an error.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md` — gosec; keep the signal path free of shell interpolation and unvalidated pids.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the spec `specs/in-progress/022-go-backend-private-logic.md` (Desired Behavior 2; Acceptance Criteria 5, 6; Failure Modes rows for termination; Security / Abuse Cases — the termination invariants).

Read the Python source that is the contract — reproduce it exactly:
- `src/vault_ui/activity.py` — `_sigterm_pid`, `terminate_resumed_session`, `terminate_launch_process`, `item_has_live_launch`, `_current_live_processes`, `_current_launch_maps`, and the `--session-id`-only launch matchers `_LAUNCH_ID_FLAG_RE`, `_parse_launch_processes`, `_parse_launch_names`.
- The matching pytest suite is the regression lock: `tests/test_activity.py` (the "ps process termination" and "launch-process resolution" sections).

Read the previous prompt `prompts/1-spec-022-session-liveness-and-resolution.md` — this prompt reuses `pkg/session`'s process-table parsing (`ParseLiveProcesses`, `ParseLaunchProcesses`, `ParseLaunchNames`, `ProcessScanner`). Do NOT duplicate that parsing.

The module is `github.com/bborbe/vault-ui` at the repo root (established by spec 021). `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` are already direct dependencies.

**Package-per-AC-name rule.** Ginkgo allows exactly ONE `RunSpecs` per test binary, so one Go package exposes exactly one suite entry-point. The spec pins `go test -run TestTerminateGuards` and `go test -run TestTerminateFailurePaths` as distinct behaviors, so they are two packages: `pkg/terminate` (suite entry `TestTerminateGuards`) and `pkg/sigterm` (suite entry `TestTerminateFailurePaths`). Do NOT merge them and do NOT rename the entry-points.
</context>

<requirements>

### 1. Create `pkg/sigterm/` — the SIGTERM primitive and its failure paths

Package name `sigterm`. Files: `sigterm.go`, `sigterm_suite_test.go`, `sigterm_test.go`.

Port `_sigterm_pid` from `src/vault_ui/activity.py`. This is the ONLY place a process signal is delivered; it must be trivially reviewable because it is the one irreversible action in the port.

Exported contract:

```go
package sigterm

import "context"

// Signaler delivers SIGTERM to a pid. It is injectable so tests can spy on the
// exact pid and simulate failures; production uses NewProcessSignaler.
type Signaler interface {
    Signal(pid int) error
}

// SigtermPID sends SIGTERM to pid via signaler. It returns true when the signal
// was delivered and false for every expected failure:
//   - the process is already gone (the error wraps os.ErrProcessDone) — a debug
//     log line only, never a warning and never a raised error;
//   - any other failure (e.g. permission denied, syscall.EACCES) — a WARNING log
//     line naming the pid, the sessionID, and the error.
// It never panics and never returns an error: a failed termination is reported as
// false, not raised.
func SigtermPID(ctx context.Context, signaler Signaler, pid int, sessionID string) bool

// NewProcessSignaler returns a Signaler backed by the real process table.
func NewProcessSignaler() Signaler
```

Behavior notes: `NewProcessSignaler().Signal(pid)` must use `os.FindProcess(pid)` then `Process.Signal(syscall.SIGTERM)`; on Unix `Signal` returns an error wrapping `os.ErrProcessDone` for a dead pid and `syscall.EACCES` for a foreign pid. `SigtermPID` classifies with `errors.Is(err, os.ErrProcessDone)`. The debug log line emitted on the process-gone path is a documented addition for observability, not Python parity (the Python path is silent there).

### 2. Create `pkg/terminate/` — the guard logic

Package name `terminate`. Files: `terminate.go`, `terminate_suite_test.go`, `terminate_test.go`. Imports `pkg/session` (for the parsing helpers) and `pkg/sigterm` (for the primitive).

Port `terminate_resumed_session`, `terminate_launch_process`, and `item_has_live_launch` from `src/vault_ui/activity.py`.

Exported contract:

```go
package terminate

import (
    "context"

    "github.com/bborbe/vault-ui/pkg/session"
    "github.com/bborbe/vault-ui/pkg/sigterm"
)

// TerminateResumedSession SIGTERMs the live claude process pinning sessionID
// (either --resume or --session-id). Returns true only when a matching process
// was found and signaled; false when no process matches.
func TerminateResumedSession(ctx context.Context, scan session.ProcessScanner, signaler sigterm.Signaler, sessionID string) bool

// TerminateLaunchProcess SIGTERMs the in-flight LAUNCH process of a Starting card
// and returns (resolvedSessionID, terminated). Resolution is two-step: the card's
// own id when a live process pins it, else the `-n <itemName>` launch row. Only a
// --session-id (launch) row is ever signaled; a --resume (interactive resume) row
// pinning the same id is left alone. When nothing matches it returns
// (sessionID, false).
func TerminateLaunchProcess(ctx context.Context, scan session.ProcessScanner, signaler sigterm.Signaler, sessionID, itemName string) (string, bool)

// ItemHasLiveLaunch reports whether a LAUNCH process (--session-id) for this item
// runs here. One fresh scan; interactive resumes never count.
func ItemHasLiveLaunch(ctx context.Context, scan session.ProcessScanner, sessionID, itemName string) bool
```

Behavior notes to reproduce exactly (from `activity.py`):
- `TerminateResumedSession` uses `session.ParseLiveProcesses` on a fresh scan (`ps -axww -o pid=,args=`) and signals the matched pid.
- `TerminateLaunchProcess` uses `session.ParseLaunchProcesses` + `session.ParseLaunchNames` on one fresh scan; `resolved := sessionID if sessionID in processes else names[itemName]`; if `resolved` is nil → `(sessionID, false)`; if `processes[resolved]` is missing → `(resolved, false)`; else `(resolved, sigterm.SigtermPID(...))`.
- Both functions call the injected `scan` for a FRESH process table — never a cached one (take-over must resolve the process as it is right now).

### 3. Tests — two Ginkgo suites

Each package's `<pkg>_suite_test.go` uses the standard suite body with the entry-point named exactly as the AC (`TestTerminateGuards` / `TestTerminateFailurePaths`), NOT `TestSuite`.

**`pkg/sigterm` — suite entry `TestTerminateFailurePaths`.** Inject a fake `Signaler`. Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions):
- `process-gone-between-scan-and-kill` — a signaler whose error wraps `os.ErrProcessDone` makes `SigtermPID` return false with no panic and no error raised.
- `permission-denied` — a signaler returning a non-`os.ErrProcessDone` error (use a `syscall.EACCES`-shaped error) makes `SigtermPID` return false AND emit a warning-level log line naming the pid and the error.
- `real-signaler-classifies-a-dead-pid` — `NewProcessSignaler().Signal(<pid of an already-reaped child>)` returns an error for which `errors.Is(err, os.ErrProcessDone)` is true (proves the production classification against the real `os` syscall path, not a synthetic error).
Also cover: a successful signal returns true and calls the signaler exactly once with the pid.

**`pkg/terminate` — suite entry `TestTerminateGuards`.** Inject a `ProcessScanner` returning a fixed `ps -axww -o pid=,args=` table (reuse the real rows quoted in `tests/test_activity.py`) and a spy `Signaler`. Rows (exact entry descriptions):
- `matching-row-signals-once` — the spy records exactly one signal call naming the matched pid.
- `no-match-signals-nothing` — zero signal calls and a false return.
- `resume-only-row-signals-nothing` — a `--resume`-only row produces zero signal calls (and `TerminateLaunchProcess` returns the card's own id with false).
Also cover: `TerminateResumedSession` kills a matched process and returns true; returns false with no call for no match; `TerminateLaunchProcess` prefers a live frontmatter id; resolves by `-n <title>` when the card's id has no process (including when the card id is nil/empty); returns the caller's id with false when nothing runs; `ItemHasLiveLaunch` counts a live launch and does NOT count an interactive resume.

Do NOT use stdlib `func TestX(t *testing.T)` tables. Do NOT name any suite entry-point `TestSuite`.

### 4. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- feat: Port the process-termination guards to Go, issuing SIGTERM only for a matched claude launch row and treating a vanished process and a permission failure as non-fatal.`
Do NOT modify any existing section.

### 5. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.

</requirements>

<constraints>
- Copy of spec 022 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - **Behavior parity is the contract** — reproduce `src/vault_ui/activity.py` exactly; the pytest suite `tests/test_activity.py` is the reference. No behavior added, removed, or "improved".
  - **The Python backend keeps working until spec 3's cutover.** Do NOT change anything under `src/`.
  - **Time is injectable** — any window/timeout is a parameter, not a wall-clock read inside the logic.
  - **Errors are wrapped and classified** so a caller can distinguish an expected absence (no matching process) from an unexpected failure.
  - **Security invariants (verbatim, from the spec):** the termination path cannot be aimed at an arbitrary pid — the pid comes from the matched `ps` row, never from the request; the request supplies a session id that must match an exact UUID already present in the host process table, and the row must be a claude invocation. A caller cannot name a pid, a process name, or a signal. `ps` content is parsed defensively (a row counts only when it matches the exact session-pinning-flag-plus-UUID pattern and contains the claude token). Subprocess arguments are passed as argv, never a shell.
  - Coding guides to follow (do not inline): `go-testing-guide`, `go-security-linting`, `go-error-wrapping-guide`, `go-package-layout-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: branch`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT touch `src/`, `tests/`, or `Makefile`.
- Do NOT wire these packages into `pkg/factory` or `main.go` — that is spec 3's job.
- Do NOT duplicate the process-table parsing in `pkg/terminate`; reuse `pkg/session`'s `Parse*` helpers and `ProcessScanner` (created by prompt 1).
- Do NOT signal a process from `pkg/sigterm` other than through the injected `Signaler`; the production `NewProcessSignaler` is the only real delivery path.
- The container masks `.git` (`hideGit: true`). If `go build`/`go test` fails with a VCS-stamping error, add `-buildvcs=false`. Do NOT change the Makefile.
- Existing Go tests from spec 021 and prompt 1 must still pass.

<!-- OPEN QUESTION for the human auditor: the spec's Failure Modes table describes termination as signaling a subprocess resolved from `ps`; this port keeps that exactly (an injected scanner + an injected signaler). The `pkg/sigterm` / `pkg/terminate` split exists solely because the spec pins two distinct `go test -run` names (TestTerminateGuards, TestTerminateFailurePaths) and Ginkgo permits one suite entry-point per package. -->
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test -run TestTerminateGuards ./pkg/terminate/` — must exit 0.
4. `go test -run TestTerminateFailurePaths ./pkg/sigterm/` — must exit 0.
5. `go test ./...` — must exit 0.
6. `go test -race ./...` — must exit 0.
7. `gofmt -l .` — must print nothing.
8. `! grep -rnE '\btime\.Now\(\)' pkg/terminate pkg/sigterm` — must succeed (no direct wall-clock read).
9. `! grep -rn 'regexp.MustCompile' pkg/terminate` — must succeed (no parser logic leaked into the guard package; parsing lives in `pkg/session`).
10. `go test -cover ./pkg/terminate ./pkg/sigterm` — must exit 0 and report non-zero coverage for each package.
11. `make test` — the existing pytest suite must still pass.
</verification>
