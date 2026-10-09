---
status: completed
summary: Wired the five-minute cleanup sweep into the running service via factory.CreateCleanupSweep, repaired the dead startup reconcile read, and closed the marker race by restoring claude_session_started after every await when the launch registry reports IN_FLIGHT
execution_id: vault-ui-clear-race-exec-154-wire-the-cleanup-sweep-race-closed
dark-factory-version: v0.196.0
created: "2026-10-09T11:40:00Z"
queued: "2026-10-09T13:45:30Z"
started: "2026-10-09T13:46:43Z"
completed: "2026-10-09T13:58:46Z"
branch: dark-factory/154-wire-the-cleanup-sweep-race-closed
---

# Wire the cleanup sweep into the service, with the marker race closed

<summary>
- The Go backend ships a complete, tested cleanup sweep that nothing ever runs.
- The Python service it replaced ran that sweep every five minutes; the cutover to Go left it unwired, so the running service does none of the work.
- What is lost is not cosmetic: stale session ids are never cleared, orphaned starting markers never expire, resurrected markers are never re-cleared, and an empty session id is never re-bound. Cards can offer Start for work that is already running.
- Wiring it is the point of this change. Closing the marker race is a requirement OF that wiring, not a separate fix — the sweep must not go live carrying a race.
- The race: the sweep decides what to clear, then waits on a command that edits the same file. A launch that begins during that wait has its fresh starting marker erased.
- One pass is also dead on arrival and is repaired here: the startup orphan reconciliation reads the marker from the listed item, a field the vault CLI does not emit, so its loop body never runs.
- Five more places in the periodic sweep have the same race, and are fixed with it.
- Every wait stays time-limited, and the clock is injected as it already is — no new setting to configure.
</summary>

<objective>
Make the running service perform the five-minute cleanup pass it was supposed to inherit, and make that pass safe against a relaunch beginning while it is awaiting a clear — so a card never offers Start for a launch that is in flight.
</objective>

<context>
Read `docs/dod.md` before writing code (the repo's Definition of Done: Go conventions, no swallowed errors, >= 80% coverage on new code, CHANGELOG entry). Do NOT try to read `CLAUDE.md` — it is gitignored and absent from this worktree.

Read first:
- `pkg/cleanup/cleanup.go` — the whole file. `SweepParams`, `NewSweep`, `Run`, `RunLoop`, `shouldClearUUID`, `clearItemSession`, `clearOrphanedTaskMarkers`, `clearOrphanedGoalMarkers`, `reclearResurrectedTaskMarkers`, `reclearResurrectedGoalMarkers`, `markerPresent`, and the `Default*` duration constants.
- `pkg/cleanup/reconcile.go` — the whole file.
- `pkg/cleanup/rebind.go` and `pkg/cleanup/derive.go` — the established style; `derive.go` is also what `pkg/board` and `pkg/mutations` already consume.
- `pkg/launchregistry/launchregistry.go` — `State` returning `(State, bool)` with the `InFlight` and `Finished` constants, `Finish`, `Evict`, `EvictIfFinished`, `Finished`.
- `pkg/statuscache/` — `GetSessionStarted`, the seam the TTL pass already uses.
- `pkg/factory/factory.go` and `pkg/factory/api.go` — the composition root: `CreateOpSet`, `CreateReadiness`, `CreateWatcher`, `CreateAPIServer`, and the other `Create*` constructors whose shape this change follows.
- `main.go` — the `run.CancelOnFirstErrorWait(ctx, ...)` set where the sweep is added.
- `docs/cleanup-retention-invariant.md` — the written contract for the retention rules this pass implements.
- `docs/starting-marker-lifecycle.md` — the frozen marker/registry/sweep contract.
- `src/vault_ui/factory.py` — the SUPERSEDED Python service's own wiring, kept only as the behavioural reference for what the sweep did at startup and on its interval. Read it for behaviour; do not port its code and do not modify anything under `src/`.
- `specs/completed/022-go-backend-private-logic.md` — the port that produced `pkg/cleanup` as a package, with the retention invariant this change must preserve. There is deliberately no spec for this change: it is a follow-up to the completed 022 (which built the package) and 023 (which cut the service over to Go and left the package unwired), so no `spec:` frontmatter is set — the absence is a decision, not an omission.
- The coding plugin's Go guides: `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` (Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry point), `go-concurrency-patterns.md`, `go-context-cancellation-in-loops.md` (bounded waits that respect `ctx.Done()`), `go-time-injection.md` (inject time; never `time.Now()` in production), `go-error-wrapping-guide.md` (`github.com/bborbe/errors`).

Why the sweep is dark today: `specs/completed/023` cut the service over to the Go binary, and the sweep is dead code — `NewSweep` and `RunLoop` have no production caller, appearing only in `pkg/cleanup/cleanup_test.go`; `main.go` and `pkg/factory/` reference neither `cleanup` nor `Sweep`. (`pkg/board` and `pkg/mutations` do import `pkg/cleanup`, but only for `DeriveClaudeProjectDir` — the sweep itself is never constructed.)

The race, in full: the sweep decides what to clear from a snapshot taken BEFORE it awaits anything, then awaits a vault operation that edits the same file. A relaunch for the same `(vault, item_id)` records itself in the launch registry and writes a fresh `claude_session_started` marker; the already-running clear then removes that fresh marker. The registry says the turn is running while the file says nothing is, and the API surfaces the marker only from the registry — so the card renders Start on a running turn.
</context>

<requirements>
1. **Repair the dead startup pass.** `ReconcileOrphanedMarkers` in `pkg/cleanup/reconcile.go` reads `marker := task.ClaudeSessionStarted` from `s.listTasks(ctx, ops)`. The CLI does not emit that field — the sweep's own comment on `clearOrphanedTaskMarkers` says so, and that pass reads the marker from `s.params.StatusCache.GetSessionStarted(vault.Name, task.ID)` instead. Change the reconcile loop to read the marker from the StatusCache the same way, so its body actually executes. Keep its guard order and its `OrphanGrace` / live-launch checks unchanged. Migrate the existing `pkg/cleanup/reconcile_test.go` rows to seed the marker through the same cache helper the other specs use — they currently set `Item.ClaudeSessionStarted`, which the production list path never populates, so they would stop clearing anything once the pass reads the cache.

2. **Add the restore helper** in `pkg/cleanup/cleanup.go`:

   `func (s *sweep) restoreMarkerIfInFlight(ctx context.Context, ops VaultOps, vaultName, itemID, kind string) bool`

   Contract: re-read `s.params.LaunchRegistry.State(vaultName, itemID)`. Restore **ONLY when the state is `launchregistry.InFlight`** — that is the case where a relaunch began during the await. Then write the marker back through `ops.SetTaskField` (`kind == "task"`) or `ops.SetGoalField` (`kind == "goal"`) under `s.bounded`, and return `true`. Return `false` without writing for every other case, **including a known `Finished` record**: at the two re-clear passes a `Finished` record is the normal pre-clear state, and restoring there would undo the clear the pass just performed. Log a warning and return `false` when the write fails; a failed restore must not abort the pass. The instant written is `s.params.Now.Now().UTC().Format(time.RFC3339Nano)` — the layout the launch path writes, in `sessionStartedMarker()` (`pkg/mutations/helpers.go`). That helper is unexported and reads the wall clock, so read the injected clock instead of calling it. (`SweepParams.Now` is a clock, not a `libtime.DateTime` — see requirement 4's clock clause.)

   Note that the pre-clear state at `clearItemSession` is not `Finished` but **any known record**: `shouldClearUUID` gates on `if _, known := s.params.LaunchRegistry.State(...); known`, which is true for `InFlight` too. So the helper will also restore at that site when the launch was already in flight before the await, not only when a relaunch slipped in. That is the wanted outcome — a launch really is running, so its marker belongs there — but do not describe the guard in comments as `Finished`-only.

3. **Call the helper at all six clear sites** — a partial fix is not a fix, because a relaunch racing any one of them still ends with its marker wiped:
   - `clearItemSession` (called by `clearStaleTaskSessions` and `clearStaleGoalSessions`) — the block that clears `claude_session_started` after the session-id clear. The goal branch is unconditional; the task branch is gated on `item.ClaudeSessionStarted != ""`. One call placed after that whole block covers both branches.

     Scope note, and a deliberate one: `item.ClaudeSessionStarted` is a field nothing in production ever writes (the CLI does not emit it — the same dead-read defect requirement 1 repairs in `reconcile.go`), so the task branch of that block clears nothing today. It is left alone here **on purpose**: where nothing is cleared there is nothing to wipe, so that branch carries no race, and the orphan-marker TTL pass remains the task marker's real clearer. Repairing that second dead read is a separate change — do not fold it into this one. Place the restore call so it covers the goal branch, and so it keeps covering the task branch if that read is ever repaired.
   - `clearOrphanedTaskMarkers` — after the clear returns nil.
   - `clearOrphanedGoalMarkers` — the mirror.
   - `reclearResurrectedTaskMarkers` — alongside the existing `EvictIfFinished`, which already returns false for a live record so the two compose without ordering.
   - `reclearResurrectedGoalMarkers` — the mirror.
   - `ReconcileOrphanedMarkers` — **this one has an ordering constraint**. Call the helper after the clear succeeds and BEFORE `s.params.LaunchRegistry.Finish(vault.Name, task.ID)`, and **skip that `Finish` call when the helper returned true**. Calling it after `Finish` makes the site a no-op; calling it before without skipping `Finish` restores the marker and then immediately marks the relaunch finished, which the next re-clear pass undoes. Comment the ordering at the call site.

   Do not increment the `cleared` counter at the reconcile site when the helper restored the marker — the marker was not left cleared.

4. **Wire the sweep into the running service.** Add `func CreateCleanupSweep(...) run.Func` to `pkg/factory` — follow the existing `Create*` shape, and return a `run.Func` that:
   - runs `ReconcileOrphanedMarkers` once at startup, logging its count, and continuing on error rather than aborting startup; then
   - runs `RunLoop(ctx)` until the context is cancelled.

   Build `cleanup.SweepParams` with the package's `Default*` constants for `MarkerTTL`, `OrphanGrace`, `SetFieldTimeout`, `LockAcquireTimeout` and `CleanupInterval` — do not introduce a flag, env var, or config key for any of them. Populate `Vaults` and `OpsFor` from the same config the API server already loads.

   ⚠️ **Three things this needs that do not exist yet.**

   First, `cleanup.VaultOps` has **no production implementation** — `SetTaskField` appears only as the interface method and in the sweep's own calls. `CreateOpSet` returns a `vaultui.OpSet`, a different type (a struct of vault-cli operations, `pkg/ops.go`), so write the adapter in `pkg/factory`: map `ListTasks`/`ListGoals` and `ShowTask` onto the `OpSet` reads, and `SetTaskField`/`ClearTaskField`/`SetGoalField`/`ClearGoalField` onto its frontmatter and goal set/clear operations, passing the vault's `Path`/`Name` to the ops as `pkg/board` does — the ops resolve the item's file themselves.

   Second, `SweepParams` also needs fields `main.go` does not thread to it today. **Two of them must be the API's own instances, or this change silently does nothing:**
   - `LaunchRegistry` **must be the same `launchregistry.Registry` instance the API uses** (`launches` in `main.go`). The entire race argument rests on the sweep observing the API's `Begin`; a private `launchregistry.NewRegistry()` compiles, passes every spec in this prompt, and leaves `restoreMarkerIfInFlight` never firing — the race stays open with every gate green.
   - `SessionLock` **must be the same `sessionlock.Registry` the API's `set_task_session` uses**: `rebindUnderLock` (`pkg/cleanup/rebind.go`) takes the per-`(vault, item)` lock through it, and `docs/cleanup-retention-invariant.md` requires the re-bind write to join the API's own critical section. A second, unshared registry compiles, passes tests, and silently destroys that exclusion. `CreateAPIHandler` builds `sessionlock.NewRegistry()` inline (`pkg/factory/api.go`) and hands it straight to `CreateMutationService`, which already receives it as its `locks sessionlock.Registry` parameter (`pkg/factory/mutations.go`) — so the lift is one level only: **give `CreateAPIHandler` a `sessionlock.Registry` parameter in place of the inline construction, create the registry once in `main.go`, and pass that same instance to both `CreateAPIHandler` and `CreateCleanupSweep`.**

   Also populate `StatusCache` — the same `cache` `CreateAPIHandler` already receives; a nil interface panics the first time `markerPresent` or either orphan pass reads it — and `HomeDir` (`os.UserHomeDir()`, already used in `main.go`; an empty value derives a wrong project dir and transcript lookups silently miss).

   Build the remainder from their existing seams: the process scanner as `session.NewPSScanner("-axww", "-o", "args=")` (`pkg/factory/mutations.go`), `LiveNames` from `session.NewProcessTable(...).LiveSessionNames` (`pkg/session/process.go`), `Now` from a `libtime.NewCurrentDateTime()` clock (see the clock clause below), `CurrentUser` from the merged config.

   ⚠️ **`SweepParams.Now` must become a clock, not a snapshot — without this, `RunLoop` is incorrect even once it is wired.** The field is currently a `libtime.DateTime` **value** (`pkg/cleanup/cleanup.go`) read by two consumers: `markerIsYoung` (`cleanup.go`) and the reconcile grace check (`reconcile.go`). A single `SweepParams` built at startup and handed to `RunLoop` freezes that instant, so a marker written *after* startup has a **negative** age, `markerIsYoung` returns true, and the marker-TTL pass never clears it — cards sit on "Starting…" until the process restarts, which is the very symptom this change exists to remove. It is also a parity break: the Python service the port must reproduce read the clock on every call (`src/vault_ui/cleanup.py`), and `docs/cleanup-retention-invariant.md` states the contract as a marker "older than the marker TTL".
   Change the field to `libtime.CurrentDateTimeGetter` and read it at each consumer as `s.params.Now.Now()` — the shape `pkg/board` already uses (`b.clock.Now().UTC()`, `pkg/board/tasks.go`). `libtime.NewCurrentDateTime()` returns exactly that interface. Update the `pkg/cleanup` fixture, which currently passes a fixed `libtime.DateTime`, to pass a clock and `SetNow(...)` for the cases that need a frozen instant.
   Do **not** instead hand-roll a loop that rebuilds `SweepParams` per pass. It would still do the work, but it routes around the seam instead of repairing it: `RunLoop` would keep a frozen `Now` and stay unused, and `go-time-injection.md` wants the clock injected and read at its point of use. Fix the field.

   Both signature changes (`CreateAPIHandler`, `CreateStatusCacheLoader`) break existing callers — update every one, production and test (`pkg/factory/api_test.go`, `pane_test.go`, `pageindex_test.go`).

   Third, **the startup reconcile must not run against an empty cache.** Requirement 1 makes `ReconcileOrphanedMarkers` read the marker from `StatusCache`, and that cache is populated by `factory.CreateStatusCacheLoader` — which sits in the *same* `run.CancelOnFirstErrorWait` group, and that group runs its funcs concurrently (`run.Run`). A reconcile that starts alongside the loader reads an empty cache, finds no markers, clears nothing, and reproduces the exact dead-pass symptom requirement 1 exists to fix — while requirement 5's cache-seeding unit test still passes. Sequence it: create `ready := make(chan struct{})` in `main.go`, have `CreateStatusCacheLoader` take that `chan struct{}` and close it once the load completes, pass it into `CreateCleanupSweep` as a `<-chan struct{}`, and have the startup reconcile wait on it — a `select` over it and `ctx.Done()` — before its first pass. Do not reorder the run group and do not move the loader out of it.

   Add `CreateCleanupSweep` to the `run.CancelOnFirstErrorWait` set in `main.go`.

5. **Tests in `pkg/cleanup/`**, in the existing Ginkgo/Gomega style. **Pin these spec names exactly** — `<verification>` greps them out of the test log, so a renamed or omitted spec fails the gate rather than passing it:
   - One regression test per clear site, **six in total, each named `It("restores the marker after <site> when a relaunch lands in the await", ...)`**, each written as a plain `It` block — **do not rewrite these six as `DescribeTable`/`Entry`**, because the gate greps their printed names and a table does not print them. Arrange the pre-await decision to pass, make the awaited vault operation record a launch in the registry before it returns (the test double's hook is how a relaunch lands inside the await), and assert that after the pass a `claude_session_started` write was recorded for that item — and, at the reconcile site, that its registry record is not finished.
     Assert on the recorded write, not on the cache: `fakeOps.SetTaskField`/`ClearTaskField` only *record* the call, while `markerPresent` reads the `StatusCache`. Assert the written value too — it must equal `s.params.Now.Now().UTC().Format(time.RFC3339Nano)`, which is the serialization boundary requirement 2 pins and a presence-only assertion never exercises.
     ⚠️ `fakeOps` exposes `onClearTask` and `onSetTask` only (`cleanup_test.go`). The two goal sites need `onClearGoal` and `onSetGoal` added alongside them, or those sites cannot be exercised and will be the two left untested.
   - A test named `It("does not restore when the record is finished", ...)` — the pre-clear state at three of the six sites. Without it, an implementation keyed on "a record is known" passes every other test while undoing those three clears.
   - A test that a genuine orphan is still cleared, so the fix cannot pass by disabling the sweep.
   - A test that a marker created **after** the sweep was constructed is still cleared once it passes the TTL — the regression test for the frozen clock. Run one pass, advance the clock past `MarkerTTL`, run a second pass, and assert the marker was cleared. A `Now` frozen at construction makes this fail, which is exactly what it is for.
   - A test that `ReconcileOrphanedMarkers` now acts on a marker the StatusCache holds — the case that was dead before this change.
6. **Tests in `pkg/factory/`** for the new constructor: it returns a runnable; the startup reconcile runs before the loop; and the reconcile does **not** run until the cache-ready channel from `CreateStatusCacheLoader` is closed (requirement 4's third clause) — a test that passes while the reconcile fires against an unloaded cache has not exercised the thing that was broken. Also test the `VaultOps` adapter (requirement 4's first clause) against a seeded vault: `ListTasks`/`ListGoals` return the on-disk items, and `SetTaskField`/`ClearTaskField`/`SetGoalField`/`ClearGoalField` each resolve to the correct item's file. That adapter is the only new code crossing into vault-cli ops and the filesystem, and a wrong path resolution writes the marker into the wrong file with every other gate still green. Finally, assert the shared-instance seam: `Begin` a launch for a seeded item through the registry you pass in, run the sweep, and assert its pass observes that launch (the restore path fires). Nothing else here distinguishes a sweep wired to the API's registry from one holding a private `launchregistry.NewRegistry()` — and that mistake compiles, passes every other gate, and leaves the race open.
7. Update `docs/starting-marker-lifecycle.md` with the marker-side guarantee this change adds, and `docs/cleanup-retention-invariant.md` with the fact that the pass now runs in the service. One or two sentences each; do not restructure either doc.
8. Add a `## Unreleased` CHANGELOG section — the file currently opens at `## v0.92.1`, so the heading must be created — with a bullet describing both the wiring and the race in behavioural terms. `docs/dod.md` requires an entry under `## Unreleased`.
9. Before finishing, re-run every command in `<verification>` and confirm each passes, then walk requirements 1-8 against your diff.
</requirements>

<constraints>
- Do NOT commit; dark-factory handles the commit.
- Do NOT modify anything under `src/` — the Python backend is superseded and is not changed by this prompt.
- Do NOT modify `src/vault_ui/static/` or any served asset.
- Do NOT add a config flag, env var, threshold, or opt-out for any sweep duration or for the sweep itself.
- Do NOT change the `(vault, item_id)` key shape of the launch registry.
- The launch registry stays process-local and in-memory; do not persist it.
- Do NOT serialise a clear behind a lock or hold a lock across the await — restore the invariant AFTER the await instead, so a slow vault operation never blocks a launch.
- Keep every awaited vault operation bounded, as the existing sweep already does.
- Existing behaviour must not regress: all currently passing tests must still pass, including the retention-invariant specs in `pkg/cleanup/cleanup_test.go`.
- Out of scope: the `vault-cli` side of the marker protocol. The vault CLI's own clear and set semantics are unchanged.
</constraints>

<verification>
First `export PATH=/usr/local/go/bin:$PATH` — `go` lives there in this container and is not always on `PATH`. Then run, in order, confirming each passes:

- `ROOTDIR=/workspace go build -buildvcs=false ./...` — the flag is required: `hideGit: true` makes `/workspace/.git` an empty tmpfs, so a bare `go build` dies with `error obtaining VCS status: exit status 128`. Every other build in this repo carries it (`Makefile`, `main_test.go`).
- `grep -c 'restoreMarkerIfInFlight(' pkg/cleanup/cleanup.go` — must print at least `6` (the definition plus one call at each of the five sites in this file)
- `grep -c 'restoreMarkerIfInFlight(' pkg/cleanup/reconcile.go` — must print at least `1`
- `grep -c 'CreateCleanupSweep' main.go` — must print at least `1` (the wiring line)
- `set -o pipefail; ROOTDIR=/workspace go test ./pkg/cleanup/... ./pkg/factory/... -v -args -ginkgo.v 2>&1 | tee /tmp/cleanup-tests.out` — must exit 0. `-args -ginkgo.v` is required, not cosmetic: Ginkgo v2 prints spec names only under its own verbose flag, so plain `go test -v` emits only `•` dots and the two greps below would match nothing — failing a correct implementation and, at the same time, letting an implementation with no race tests through.
- `grep -c 'when a relaunch lands in the await' /tmp/cleanup-tests.out` — must print at least `6`, one per clear site. `go test` exits 0 for a package whose new specs are absent or renamed, so without this line a diff that wires the helper but writes none of the race tests satisfies every other gate.
- `grep -c 'does not restore when the record is finished' /tmp/cleanup-tests.out` — must print at least `1` (the `Finished`-no-restore test ran, not merely compiled)
- `set -o pipefail; ROOTDIR=/workspace make precommit 2>&1 | tee /tmp/precommit.log` — must exit 0
- `test -s /tmp/precommit.log` — must exit 0 (the log exists, so the next line cannot pass vacuously)

The per-site tests in requirement 5 are the authoritative proof; the count greps and the `tee` greps are fast pre-filters over that same run, and the spec names are pinned in requirement 5 so they can be grepped at all.

Do not run any `git` command — the container has no usable `.git` (hideGit).
</verification>
