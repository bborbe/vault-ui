---
status: cancelled
spec: [022-go-backend-private-logic]
created: "2026-10-03T23:45:00Z"
queued: "2026-10-03T23:36:15Z"
branch: dark-factory/go-backend-private-logic
cancelled: "2026-10-04T08:34:07Z"
---

# Add the cleanup sweep policy and the port parity lock

<summary>
- The Go backend can run the five-minute cleanup sweep that keeps stale Claude session ids from stranding a card.
- A valid session id is cleared only when this instance launched it (the launch registry has a record) and its transcript is gone — a dead local session.
- A session id assigned to someone else, and one whose transcript exists, are always retained, so a peer machine's work is never published as deleted.
- A non-UUID display name is repaired to its resolved id when one is found and otherwise left untouched; a goal's unresolvable display name is cleared (the one deliberate divergence).
- An empty session id is re-bound from the task title only when exactly one live session carries that title AND its transcript lives in this vault's own project directory.
- A `claude_session_started` marker older than the 45-minute TTL with no registry record is cleared, so a card stuck on "Starting…" after a restart returns to Start.
- A finished launch's marker that a concurrent writer restored is re-cleared from disk, and the registry record is evicted only while it is still finished.
- Every awaited vault operation is bounded, so one stuck helper cannot freeze the pass.
- The retention invariant is captured in a durable document beside the existing marker-lifecycle doc, so the contract survives the Python backend's removal.
- The whole port is locked: the module builds, its full test suite passes, and the existing Python backend is untouched and green.
</summary>

<objective>
Port the cleanup sweep policy (`cleanup.py`) into `pkg/cleanup` — the retention invariant plus the empty-id re-bind, the orphaned-marker TTL, and the resurrected-marker re-clear passes for tasks and goals — and land the cross-cutting parity lock: the whole Go module builds and its full suite passes, and the Python backend is untouched and green.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` plus subpackages on a real split trigger.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry-point, `DescribeTable`/`Entry`, suite timeout.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` and `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — bounded waits, respect `ctx.Done()`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md` — inject time; never `time.Now()` in production.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; an expected absence is not an error.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the spec `specs/in-progress/022-go-backend-private-logic.md` (Desired Behavior 8; Acceptance Criteria 1, 13, 14; Failure Modes rows for the cleanup sweep; Security / Abuse Cases; Constraints — the marker lifecycle doc is the frozen contract).

Read the Python source that is the contract — reproduce it exactly:
- `src/vault_ui/cleanup.py` — `_CLEANUP_INTERVAL_SECONDS`, `_SET_FIELD_TIMEOUT_SECONDS`, `_LOCK_ACQUIRE_TIMEOUT_SECONDS`, `_STARTING_MARKER_TTL_SECONDS`, `_ORPHAN_GRACE_SECONDS`, `derive_claude_project_dir`, `_marker_age_seconds`, `_rebind_empty_session_ids`, `cleanup_stale_sessions`, `run_cleanup_loop`, `reconcile_orphaned_markers`.
- The matching pytest suite is the regression lock: `tests/test_cleanup.py` (2531 lines — read it in chunks).
- `docs/starting-marker-lifecycle.md` — the frozen marker-lifecycle contract the sweep must honor.

Read the earlier prompts in this spec so you know the packages the sweep composes: `prompts/1-spec-022-session-liveness-and-resolution.md` (`pkg/session`, `pkg/activity`, `pkg/sessionresolver`), `prompts/3-spec-022-process-local-registries.md` (`pkg/launchregistry`, `pkg/sessionlock`), `prompts/4-spec-022-topology-and-status-cache.md` (`pkg/statuscache`, `pkg/hierarchy`, `pkg/vaultconfig`).

The module is `github.com/bborbe/vault-ui` at the repo root (established by spec 021). `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` are already direct dependencies.

**Package-per-AC-name rule.** The spec pins `go test -run TestCleanupSweep`, so `pkg/cleanup`'s suite entry-point is named exactly `TestCleanupSweep` (not `TestSuite`).

**Vault operations are injected, not shelled out.** The Python sweep shells out to `vault-cli task set/clear` and `vault-cli goal set/clear`. The Go backend uses vault-cli as a LIBRARY and performs no direct vault file I/O, so `pkg/cleanup` takes an injected `VaultOps` interface (one instance per vault) that spec 3 will implement over vault-cli's `ops.*` constructors; this prompt's tests inject a fake that records calls. The "bounded awaited subprocess" from the Python becomes a bounded context timeout around each `VaultOps` call.
</context>

<requirements>

### 1. Create `pkg/cleanup/` — the sweep policy

Package name `cleanup`. Files: `cleanup.go` (types + `Sweep` + main pass), `rebind.go` (empty-id re-bind), `reconcile.go` (`ReconcileOrphanedMarkers` + orphan grace), `derive.go` (`DeriveClaudeProjectDir`), `cleanup_suite_test.go`, `cleanup_test.go`, `rebind_test.go`, `reconcile_test.go`.

Port `cleanup.py` exactly. Reproduce the retention invariant and the three secondary passes.

Exported contract:

```go
package cleanup

import (
    "context"
    "time"

    libtime "github.com/bborbe/time"

    "github.com/bborbe/vault-ui/pkg/launchregistry"
    "github.com/bborbe/vault-ui/pkg/sessionlock"
    "github.com/bborbe/vault-ui/pkg/statuscache"
)

// Item is the task/goal view the sweep operates on.
type Item struct {
    ID                   string
    Title                string
    Status               string
    Assignee             string
    ClaudeSessionID      string
    ClaudeSessionStarted string
}

// VaultOps is the vault-cli operation surface the sweep needs, one per vault.
// Implementations wrap vault-cli's ops constructors; the sweep never reads or
// writes a vault file itself.
type VaultOps interface {
    ListTasks(ctx context.Context) ([]Item, error)
    ListGoals(ctx context.Context) ([]Item, error)
    ShowTask(ctx context.Context, itemID string) (Item, error)
    SetTaskField(ctx context.Context, itemID, key, value string) error
    ClearTaskField(ctx context.Context, itemID, key string) error
    SetGoalField(ctx context.Context, itemID, key, value string) error
    ClearGoalField(ctx context.Context, itemID, key string) error
}

// VaultOpsFactory builds the ops for one vault.
type VaultOpsFactory func(vault Vault) VaultOps

// Vault is the per-vault config the sweep iterates.
type Vault struct {
    Name              string
    Path              string
    TasksFolder       string
    GoalsFolder       string
    SessionProjectDir string
}

// SweepParams carries every injectable dependency and every frozen value. None of
// the durations is read from a wall clock inside the logic.
type SweepParams struct {
    Vaults             []Vault
    OpsFor             VaultOpsFactory
    HomeDir            string
    CurrentUser        string
    LaunchRegistry     launchregistry.Registry
    SessionLock        sessionlock.Registry
    StatusCache        statuscache.Cache
    LiveNames          func(ctx context.Context) map[string]string
    Now                libtime.DateTime
    MarkerTTL          time.Duration
    OrphanGrace        time.Duration
    SetFieldTimeout    time.Duration
    LockAcquireTimeout time.Duration
    CleanupInterval    time.Duration
}

// Frozen values (carried by value, not invented).
const (
    DefaultMarkerTTL          = 45 * time.Minute
    DefaultOrphanGrace        = 120 * time.Second
    DefaultSetFieldTimeout    = 10 * time.Second
    DefaultLockAcquireTimeout = 10 * time.Second
    DefaultCleanupInterval    = 5 * time.Minute
)

// Sweep is the five-minute cleanup pass.
type Sweep interface {
    // Run executes one cleanup pass and returns the number of session ids/markers
    // cleared. A re-bind is not a clear and does not count.
    Run(ctx context.Context) (int, error)
    // ReconcileOrphanedMarkers clears claude_session_started markers whose launch
    // this host no longer has (startup reconciliation) and returns the count.
    ReconcileOrphanedMarkers(ctx context.Context) (int, error)
}

func NewSweep(params SweepParams) Sweep

// DeriveClaudeProjectDir returns the Claude project dir for a vault:
// homeDir/.claude/projects/<encoded>, where <encoded> is sessionProjectDir (or
// vaultPath when empty), tilde-expanded, with "/" replaced by "-".
func DeriveClaudeProjectDir(homeDir, vaultPath, sessionProjectDir string) string
```

Behavior to reproduce exactly (from `cleanup.py`; read it fully):
- **Main task pass** (per vault): list tasks (show-all); for each task with a non-empty `ClaudeSessionID`:
  - a session id containing `/` or `\` is skipped with a warning;
  - a non-UUID id is repaired via `sessionresolver.ResolveSessionID`: a resolved id is written with `SetTaskField(..., "claude_session_id", resolved)` and never falls through to the clear block; an unresolvable id is retained (never cleared);
  - a UUID id: a foreign assignee (non-empty and `!= CurrentUser`) is retained FIRST; else a present transcript (`projectDir/<id>.jsonl` exists) is retained; else if `LaunchRegistry.State(vault, item) != (…, false)` (this instance launched it and the transcript is gone) it falls through to the clear block; else it is retained as non-local;
  - the clear block calls `ClearTaskField(..., "claude_session_id")` and, when the marker was set, `ClearTaskField(..., "claude_session_started")`; a successful clear increments the count.
- **Re-bind pass** (`rebind.go`): for each task with an EMPTY `ClaudeSessionID`, skip when: the assignee is set and foreign; the launch registry has ANY record (in-flight OR finished); the title is empty; the status is `completed`/`aborted`; or no live session carries the title. When exactly one live session carries the title (`LiveNames`), the resolved id is read straight from the live map; the write happens only when `projectDir/<resolved>.jsonl` exists. Under the per-item `SessionLock`, re-read the task (`ShowTask`) and abandon the write when it now holds a session id or is now foreign-assigned. `LockAcquireTimeout` bounds the lock ACQUISITION; `SetFieldTimeout` bounds the `ShowTask` re-read and the `SetTaskField` write. A timed-out helper leaves the field untouched and the next sweep retries.
- **Marker TTL pass** (tasks and goals): the marker comes from the `StatusCache` (`GetSessionStarted`), NOT from the listed item (the CLI does not emit it). Skip when the launch registry has any record; skip an id-bearing item whose transcript does NOT exist (already cleared in lockstep); skip a marker younger than `MarkerTTL`; otherwise clear `claude_session_started` and increment. An unparseable marker (legacy `"true"`) has an unknown age and is treated as expired.
- **Resurrected-marker re-clear** (tasks and goals): for each FINISHED registry record of the matching kind, clear `claude_session_started` when the cache still holds the marker; on success increment and `EvictIfFinished` (a concurrent re-begin must survive); when the marker is already gone, `Evict` the record.
- **Goal pass** mirrors the task pass, including the one deliberate divergence: a goal's UNRESOLVABLE display name IS cleared (fall through to clear).
- `ReconcileOrphanedMarkers`: for each task with a marker, skip when the registry has any record, when the age is below `OrphanGrace`, or when a live launch for the item exists (`terminate.ItemHasLiveLaunch`); otherwise clear the marker, `Finish` the registry record, and increment.
- A vault-level or goal-level failure is caught and logged; it must not abort the remaining vaults or the remaining passes.

### 2. Tests — `pkg/cleanup` suite entry `TestCleanupSweep`

Mirror `tests/test_cleanup.py`. Use a fake `VaultOps` that records calls, a real `launchregistry.Registry` / `sessionlock.Registry` / `statuscache.Cache`, and a temp `HomeDir` so the transcript-existence checks are real filesystem checks.

Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions):
- `clear-requires-registry-record-and-absent-transcript` — a valid UUID with a launch-registry record but a PRESENT transcript is retained (not cleared); the same id with the transcript absent and the record present is cleared.
- `foreign-assignee-retained` — a foreign-assignee session id survives even when the registry holds the launch and the transcript is absent.

Also cover: a current-user id with a present transcript is not cleared; a missing transcript with NO registry record is retained; an unresolvable display name is retained with zero clear calls; a resolvable display name is repaired with `SetTaskField` and never cleared; the goal pass clears an unresolvable display name; a foreign-assignee goal is retained; the empty-id re-bind writes only under a live unique title with a transcript in this vault's project dir, and skips a completed/aborted/foreign/mid-launch/no-title task and an ambiguous or absent title; the re-bind abandons the write when the under-lock re-read now holds a session id or a foreign assignee; a marker older than `MarkerTTL` with no registry record is cleared while a younger one is left; an IN_FLIGHT record is never cleared; a FINISHED record's resurrected marker is re-cleared exactly once and the record is evicted only while still finished; a failed re-clear keeps the record; `ReconcileOrphanedMarkers` clears when the launch process is gone and keeps the marker while a launch runs or the marker is young; `DeriveClaudeProjectDir` default/override/tilde/empty cases.

Do NOT use stdlib `func TestX(t *testing.T)` tables. Do NOT name the suite entry-point `TestSuite`.

### 3. `docs/cleanup-retention-invariant.md` — the durable retention contract

Create `docs/cleanup-retention-invariant.md`, a sibling of the frozen `docs/starting-marker-lifecycle.md`. It MUST capture the retention invariant: a valid session id is cleared only when this instance launched it (a launch-registry record) AND its transcript is absent; it is never cleared for an assignee mismatch or a missing transcript alone; a non-UUID display name is repaired or retained (never cleared) on the task path, with the goal path's one deliberate divergence; an empty id is re-bound only from a live unique title whose transcript is in this vault's project directory; and the marker TTL / resurrected-marker passes. State the frozen values (45-minute marker TTL, 120-second orphan grace). Reference `docs/starting-marker-lifecycle.md` and the liveness doc `docs/liveness-classification.md`. Do NOT invent new behavior — describe the ported contract only.

### 4. Parity lock — module build/test and the untouched Python backend

This prompt carries AC 1 and AC 14:
- The whole module builds and its FULL test suite passes: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...`, `gofmt -l .` all succeed.
- The Python backend is untouched: `make test` (the pytest suite) passes, and no file under `src/` or `tests/` was modified by this spec.

Do NOT modify any file under `src/` or `tests/`. The `git diff --stat -- src/vault_ui/` check from the spec's Acceptance Criterion 14 is operator-executable (the container masks `.git`) and lives on the spec's Verification ladder, not in this prompt's `<verification>`.

### 5. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- feat: Port the cleanup sweep policy to Go — the session-id retention invariant plus the empty-id re-bind, orphaned-marker TTL, and resurrected-marker re-clear passes — and capture the retention invariant in docs/.`
Do NOT modify any existing section.

### 6. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective (ACs 1, 13, 14) against the change.

</requirements>

<constraints>
- Copy of spec 022 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - **Behavior parity is the contract** — reproduce `src/vault_ui/cleanup.py` exactly; the pytest suite `tests/test_cleanup.py` is the reference. No behavior added, removed, or "improved".
  - **The Python backend keeps working until spec 3's cutover.** Do NOT change anything under `src/`; the pytest suite stays green.
  - **Single-process assumption** — the launch registry and the session-lock registry are sound only under the single-worker server; preserve it.
  - **Time is injectable** — every window, TTL, grace period, and timeout is a parameter, not a wall-clock read inside the logic (`go-time-injection`).
  - **Constants carry values, not Python names.** The 45-minute starting-marker TTL and the 120-second orphan grace are frozen values; the Go identifiers may be idiomatic, so the port is verified by value, never by a name-grep.
  - `docs/starting-marker-lifecycle.md` is the frozen contract for the sweep's marker handling — honor it.
  - **Security invariants:** the sweep's session id must be validated (a UUID, or a resolvable display name, and must not contain a path separator before any file lookup); vault operations go through the injected `VaultOps` (vault-cli as a library), never a subprocess and never direct vault file I/O; each awaited vault operation is bounded so one stuck helper cannot freeze the pass.
  - Coding guides to follow (do not inline): `go-testing-guide`, `go-concurrency-patterns`, `go-context-cancellation-in-loops`, `go-time-injection`, `go-error-wrapping-guide`, `go-package-layout-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT touch `src/`, `tests/`, or `Makefile`.
- Do NOT spawn `vault-cli` as a subprocess for the sweep's set/clear operations — use the injected `VaultOps` interface.
- Do NOT emit a `git` command in `<verification>` — the container masks `.git` (`hideGit: true`); a git command would silently no-op and produce a false pass. The `git diff --stat -- src/vault_ui/` check is operator-side (spec Verification ladder).
- The container masks `.git`. If `go build`/`go test` fails with a VCS-stamping error, add `-buildvcs=false`. Do NOT change the Makefile.
- Existing Go tests from spec 021 and prompts 1-5 must still pass.

<!-- OPEN QUESTION for the human auditor: the Python cleanup's vault operations are subprocesses; the Go port routes them through an injected VaultOps interface (vault-cli as a library), so the "stuck helper" failure mode becomes a bounded context timeout rather than a kill-and-reap. The AC rows for this prompt (clear-requires-registry-record-and-absent-transcript, foreign-assignee-retained) are policy assertions and are unaffected; the timeout mechanics are covered by the SetFieldTimeout/LockAcquireTimeout specs. If the reviewer wants the kill-and-reap semantics preserved literally, that belongs on the VaultOps implementation (spec 3), not this package. -->
<!-- OPEN QUESTION for the human auditor: the spec constraint "reference them from the spec" for the new durable docs cannot be satisfied from inside this prompt (the spec file is approved and will be archived). The operator should add links to `docs/cleanup-retention-invariant.md` and `docs/liveness-classification.md` from `specs/in-progress/022-go-backend-private-logic.md`. -->
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0 (AC 1).
2. `go vet ./...` — must exit 0.
3. `go test -run TestCleanupSweep ./pkg/cleanup/` — must exit 0 (AC 13).
4. `go test ./...` — must exit 0 (AC 1; runs every package's suite from prompts 1-6).
5. `go test -race ./...` — must exit 0.
6. `gofmt -l .` — must print nothing.
7. `grep -rnE '45[[:space:]]*\*[[:space:]]*time\.Minute' pkg/cleanup/` — must print at least one line (the 45-minute marker TTL is carried by value).
8. `grep -rnE '120[[:space:]]*\*[[:space:]]*time\.Second' pkg/cleanup/` — must print at least one line (the 120-second orphan grace is carried by value).
9. `! grep -rn 'time.Now()' pkg/cleanup` — must succeed (no direct wall-clock read).
10. `test -f docs/cleanup-retention-invariant.md` — must exit 0.
11. `make test` — the existing pytest suite must still pass (the Python backend stays untouched and green — the container-executable half of AC 14; the operator-side source-diff half is on the spec's Verification ladder).
</verification>
