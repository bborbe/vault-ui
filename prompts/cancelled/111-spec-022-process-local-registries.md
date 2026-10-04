---
status: cancelled
spec: [022-go-backend-private-logic]
created: "2026-10-03T23:45:00Z"
queued: "2026-10-03T23:36:14Z"
branch: dark-factory/go-backend-private-logic
cancelled: "2026-10-04T08:34:07Z"
---

# Add the launch registry and session-lock registry

<summary>
- The Go backend gains the two process-local registries the Python backend relies on: one tracking in-flight and finished launches, one serialising per-item session writes.
- The launch registry knows which `(vault, item)` launches are in flight and which have finished, and drives whether a card shows "Starting…".
- A finished launch record is dropped only when it is still finished — a relaunch that begins mid-eviction keeps its fresh record.
- A fresh launch clears any take-over mark left by the previous one.
- The session-lock registry hands out one lock per `(vault, item)` so unrelated items never serialise against each other.
- A lock entry is evicted only once nobody holds or awaits it, so a coroutine already waiting is never left on a deleted-and-replaced lock object.
- A cancelled waiter unwinds its count and does not wedge the entry — a later acquirer still gets the lock.
- Both registries are safe under concurrent access and are only sound because the server runs a single worker; that assumption is preserved and documented.
- The Python backend is untouched and keeps serving traffic.
</summary>

<objective>
Port the two process-local registries (`launch_registry.py`, `session_lock_registry.py`) into `pkg/launchregistry` and `pkg/sessionlock`, reproducing their exact state machines — begin/finish/evict-if-finished/take-over and per-key lock acquisition with holder-count eviction and cancellation unwind — with Ginkgo/Gomega tests.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` plus subpackages on a real split trigger.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry-point, `DescribeTable`/`Entry`, suite timeout.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — prefer `run.*` combinators over raw `go func()`; caller-owned channels.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — respect `ctx.Done()` in blocking waits.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the spec `specs/in-progress/022-go-backend-private-logic.md` (Desired Behavior 3; Acceptance Criteria 7, 8; Constraints — the single-process assumption; Failure Modes rows about a concurrent launch during eviction).

Read the Python source that is the contract — reproduce it exactly:
- `src/vault_ui/launch_registry.py` — `IN_FLIGHT`, `FINISHED`, `LaunchRegistry` (`begin`, `mark_taken_over`, `was_taken_over`, `finish`, `state`, `evict`, `evict_if_finished`, `finished`, `size`).
- `src/vault_ui/session_lock_registry.py` — `_LockEntry`, `SessionLockRegistry` (`session_lock`, `size`).
- The matching pytest suites are the regression lock: `tests/test_launch_registry.py`, `tests/test_session_lock_registry.py`.

Read the already-generated spec-021 prompts for module layout and prompt style.

The module is `github.com/bborbe/vault-ui` at the repo root (established by spec 021). `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` are already direct dependencies.

**Package-per-AC-name rule.** Ginkgo allows exactly ONE `RunSpecs` per test binary, so one Go package exposes exactly one suite entry-point. The spec pins `go test -run TestLaunchRegistry` and `go test -run TestSessionLock`, so they are two packages with suite entry-points named exactly `TestLaunchRegistry` and `TestSessionLock`. Do NOT merge them and do NOT rename the entry-points.
</context>

<requirements>

### 1. Create `pkg/launchregistry/` — the launch registry

Package name `launchregistry`. Files: `launchregistry.go`, `launchregistry_suite_test.go`, `launchregistry_test.go`.

Port `LaunchRegistry` from `src/vault_ui/launch_registry.py`. The key is `(vault, itemID)`; the record carries a state and a `kind` (`"task"` or `"goal"`). The registry is in-memory, never persisted.

Exported contract:

```go
package launchregistry

// State is the recorded launch state.
type State string

const (
    InFlight State = "in_flight"
    Finished State = "finished"
)

// FinishedRecord is one finished launch: the item id and its kind.
type FinishedRecord struct {
    ItemID string
    Kind   string
}

// Registry is the process-local launch registry. It is safe for concurrent use
// and is sound only under the single-process server assumption (see the package
// doc comment).
type Registry interface {
    Begin(vault, itemID, kind string)
    MarkTakenOver(vault, itemID string)
    WasTakenOver(vault, itemID string) bool
    Finish(vault, itemID string)
    State(vault, itemID string) (State, bool)
    Evict(vault, itemID string)
    EvictIfFinished(vault, itemID string) bool
    Finished(vault string) []FinishedRecord
    Size() int
}

func NewRegistry() Registry
```

Behavior notes to reproduce exactly:
- `Begin` overwrites any prior record for the key (a new launch supersedes an old one, so two racing begins leave one record) AND clears any take-over mark for the key.
- `Finish` on a key with no record is a no-op (never a panic).
- `EvictIfFinished` drops the record and returns true ONLY when it is still `Finished`; on an `InFlight` record or an absent key it returns false and leaves the map untouched. The check-and-delete is one synchronous step under the registry lock (no await between them).
- `Finished(vault)` returns `(itemID, kind)` for that vault's `Finished` records only.

### 2. Create `pkg/sessionlock/` — the per-item session lock registry

Package name `sessionlock`. Files: `sessionlock.go`, `sessionlock_suite_test.go`, `sessionlock_test.go`.

Port `SessionLockRegistry` from `src/vault_ui/session_lock_registry.py`. It hands out one lock per `(vault, itemID)` and tracks a holder count so an entry is evicted only when nobody holds or awaits it.

Exported contract:

```go
package sessionlock

import "context"

// Registry hands out one lock per (vault, itemID).
type Registry interface {
    // Lock acquires the lock for (vault, itemID) and returns a release func the
    // caller MUST invoke (typically deferred) to leave the critical section. A
    // cancelled ctx unwinds the waiter's holder count and returns ctx.Err().
    Lock(ctx context.Context, vault, itemID string) (release func(), err error)
    // Size is the number of tracked entries across all keys.
    Size() int
}

func NewRegistry() Registry
```

Behavior notes to reproduce exactly:
- All callers for the same key run their critical sections strictly one at a time; different keys do not contend.
- The holder count counts every caller inside the section OR queued waiting. The entry is evicted from the map only when the count returns to zero (the last holder or waiter has left).
- A waiter removed by context cancellation unwinds its count in the release path and does NOT leave the entry wedged — a later acquirer still obtains the lock.
- The entry is never deleted-and-replaced while a caller still holds or awaits it.

Implementation hint (not prescriptive): a per-entry buffered channel of size 1 (a counting semaphore) gives `select { case sem <- struct{}{}: ... case <-ctx.Done(): ... }`, which supports cancellation where a bare `sync.Mutex` cannot.

### 3. Tests — two Ginkgo suites

Each package's `<pkg>_suite_test.go` uses the standard suite body with the entry-point named exactly as the AC (`TestLaunchRegistry` / `TestSessionLock`), NOT `TestSuite`.

**`pkg/launchregistry` — suite entry `TestLaunchRegistry`.** Mirror `tests/test_launch_registry.py`. Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions):
- `evict-if-finished-drops-only-a-finished-record` — after `Begin` then `Finish`, `EvictIfFinished` returns true and drops the record; after only `Begin` (in-flight), `EvictIfFinished` returns false and the in-flight record survives.
Also cover: `Begin` records in-flight and grows `Size`; `Begin`+`Finish` yields finished and preserves the kind; `State` on an unknown key returns `(…, false)`; `Evict` drops a record; `EvictIfFinished` on an absent key returns false; `Finished` filters by vault and excludes in-flight; two `Begin`s for one key leave one record with the last kind; `Finish` with no record is a no-op; `Finish` after `Evict` does not panic; `Size` counts across vaults; `MarkTakenOver` then a fresh `Begin` clears the mark.

**`pkg/sessionlock` — suite entry `TestSessionLock`.** Mirror `tests/test_session_lock_registry.py` (adapt to Go concurrency with goroutines + channels or `Eventually`). Rows (exact entry descriptions):
- `evict-only-at-holder-count-zero` — the entry survives while a holder remains and is gone once the count returns to zero.
- `cancelled-waiter-does-not-wedge` — a waiter cancelled before acquiring unwinds its count; a later acquirer still obtains the lock and the entry evicts cleanly once the holder leaves.
Also cover: a second acquire for the same key waits for the first to release (ordering asserted); locks for different keys do not contend.

Do NOT use stdlib `func TestX(t *testing.T)` tables. Do NOT name any suite entry-point `TestSuite`. Tests must be deterministic (no real sleeping races that can flake) — coordinate with channels/`Eventually`.

### 4. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- feat: Port the process-local launch registry and session-lock registry to Go, preserving their state machines, holder-count eviction, and single-process assumption.`
Do NOT modify any existing section.

### 5. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.

</requirements>

<constraints>
- Copy of spec 022 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - **Behavior parity is the contract** — reproduce `src/vault_ui/launch_registry.py` and `src/vault_ui/session_lock_registry.py` exactly; the pytest suites are the reference. No behavior added, removed, or "improved".
  - **The Python backend keeps working until spec 3's cutover.** Do NOT change anything under `src/`.
  - **Single-process assumption (verbatim):** the launch registry and the session-lock registry are sound only because the server runs one worker. The Go port must preserve that assumption and must not introduce multi-worker sharing that would silently reintroduce the races they exist to close. Record this assumption in the package doc comment.
  - **Errors are wrapped and classified** (`go-error-wrapping-guide`).
  - Coding guides to follow (do not inline): `go-testing-guide`, `go-concurrency-patterns`, `go-context-cancellation-in-loops`, `go-package-layout-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT touch `src/`, `tests/`, or `Makefile`.
- Do NOT wire these registries into `pkg/factory` or `main.go` as process-global singletons — that is spec 3's job; export constructors only.
- Do NOT use a raw `sync.Mutex` for the session lock's critical section if it cannot honour context cancellation — the cancelled-waiter behavior is a spec requirement.
- The container masks `.git` (`hideGit: true`). If `go build`/`go test` fails with a VCS-stamping error, add `-buildvcs=false`. Do NOT change the Makefile.
- Existing Go tests from spec 021 and prompts 1-2 must still pass.

<!-- OPEN QUESTION for the human auditor: the Python `LaunchRegistry.finished(vault)` returns a list ordered by dict insertion; the Go port returns a slice whose order is unspecified. No AC asserts an order for `Finished`, so this is a deliberate, behavior-preserving relaxation. If order matters to a future consumer, it should be specified in spec 3. -->
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test -run TestLaunchRegistry ./pkg/launchregistry/` — must exit 0.
4. `go test -run TestSessionLock ./pkg/sessionlock/` — must exit 0.
5. `go test ./...` — must exit 0.
6. `go test -race ./...` — must exit 0 (exercises the registry and lock concurrency).
7. `gofmt -l .` — must print nothing.
8. `make test` — the existing pytest suite must still pass.
</verification>
