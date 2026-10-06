---
status: failed
spec: [028-precompute-task-list-snapshot]
created: "2026-10-06T20:28:50Z"
queued: "2026-10-06T20:57:28Z"
completed: "2026-10-06T20:57:30Z"
branch: dark-factory/precompute-task-list-snapshot
lastFailReason: 'setup workflow: working tree is not clean; cannot switch to branch "dark-factory/precompute-task-list-snapshot"; uncommitted changes: specs/in-progress/028-precompute-task-list-snapshot.md'
---

# Add the process-wide session snapshot and read session-derived fields from it

<!-- REVIEWER NOTE (open questions / decisions)
- The session snapshot's transcript probe is triggered by the first task-list build after each refresh, not proactively by the timer: the snapshot cannot enumerate the task session ids without the task list. The observable contract ("at most once per task per refresh, never once per request") holds; the probe is lazy per refresh window.
- Registry ids are read from the existing session-state store on the refresh timer, so a registry change can take up to one refresh interval to reach the snapshot. The session-state watcher's own frame does not force a task-list rebuild; the next refresh does. This is inside the spec's stated bound (session-derived fields refresh on an interval of at least 60 s) and is what AC5 measures.
- `RefreshOnce` is on the `Snapshot` interface so main/tests can seed it; `Run` is the production driver.
-->

<summary>
- A new process-wide component holds the session-liveness inputs in memory: the live session-registry ids, the live `--resume`/`--session-id` ids, and a per-refresh cache of session transcript modification times.
- The `ps -axww` scan the board used to spawn on every `/api/tasks` and `/api/goals` request now runs once per refresh on a fixed interval, never on the request path.
- The transcript probe that used to stat (and sometimes glob) a path per task per request now runs at most once per task per refresh, served from the cache in between.
- Session classification and the activity date are unchanged in outcome: the same four outcomes, the same fixed signal order, the same five-minute window; only where the two inputs come from changes.
- The refresh interval is at least 60 seconds; it is the only timer this spec adds, and a refresh that fails keeps the previous values instead of blanking them.
- The board reads the session-derived fields through an injected seam, so a board test can supply fixed session values without touching the filesystem.
- The change is wired through the API factory and started in the process run group, so a restart seeds the snapshot before the first request.
- This prompt does not yet precompute the task list; it only moves the session inputs off the request path.
</summary>

<objective>
Add `pkg/sessionsnapshot`: one process-wide, timer-refreshed cache of the session-liveness inputs (registry ids, live `--resume`/`--session-id` ids, and per-refresh transcript mtimes) that the board reads session-derived fields from, so no request spawns `ps` or probes a transcript more than once per refresh. Wire it through `pkg/factory/api.go` and `main.go`, and inject an optional transcript probe into `pkg/session` and `pkg/activity` so the board can pass the cached probe.
</objective>

<context>
Read `docs/dod.md` and, if present, `CLAUDE.md` at the repo root.

Read the spec `specs/in-progress/028-precompute-task-list-snapshot.md` in full. This prompt covers Desired Behavior 4 and Acceptance Criteria AC2, AC5 and AC8. AC2, AC5 and AC3 are rung-2 (post-deploy) criteria: this prompt establishes their mechanisms with container-executable tests and leaves the deployed measurement to the spec's verification ladder. It is prompt 1 of 4; prompts 2 and 3 add the task-list snapshot that consumes this snapshot, and prompt 4 writes the docs.

Read these files before changing anything:
- `pkg/activity/activity.go` — `TranscriptMtime`, `ComputeActivityDate`, `ReadRegistrySessionIDs`, `DefaultProjectsRoot`, `DefaultRegistryRoot`.
- `pkg/session/classify.go` — `ClassifyParams`, `ClassifySessionState` (it calls `activity.TranscriptMtime` internally today).
- `pkg/session/process.go` — `ProcessScanner`, `NewPSScanner`, `ParseLiveSessionIDs`, `ProcessTable`.
- `pkg/session/session.go` — `SessionState` values, `DefaultLiveWindow`.
- `pkg/sessionstate/sessionstate.go` — `State` (`RegistrySessionIDs`, `Replace`), `DefaultRescanInterval` (60 s).
- `pkg/board/board.go` — `Deps`, `SessionSignals`, `board` struct.
- `pkg/board/tasks.go` — `ListTasks`, `tasksForVault` (the `registryIDs`/`resumeIDs` reads and the `ClassifyParams` call), `taskResponse` (the `activity.ComputeActivityDate` call).
- `pkg/board/goals.go` — `ListGoals`, `goalsForVault`, `goalResponse` (same two calls).
- `pkg/factory/api.go` — `sessionSignals` (the per-request `ps` spawn), `CreateAPIHandler`, `CreateSessionState`, `CreateSessionStateWatcher`.
- `main.go` — the `run.CancelOnFirstErrorWait` run group and the `CreateAPIHandler` call.
- `docs/liveness-classification.md` — the authoritative classification contract; it must keep holding.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
</context>

<requirements>

### 1. New package `pkg/sessionsnapshot`

Create `pkg/sessionsnapshot/snapshot.go` and `pkg/sessionsnapshot/refresh.go`.

`SessionRefreshInterval` is the refresh period and MUST be at least 60 seconds:

```go
// SessionRefreshInterval is how often the session snapshot re-reads the session
// inputs. It is deliberately at least 60 s: this is the only timer the spec
// adds, and the goal forbids a tight refresher.
const SessionRefreshInterval = 60 * time.Second
```

`Snapshot` is the board's cached view of the session-liveness inputs:

```go
//counterfeiter:generate -o ./mocks/session-snapshot.go --fake-name Snapshot . Snapshot

// Snapshot is the process-wide, timer-refreshed view of the session-liveness
// inputs. A read never spawns a process and never reads the registry directory.
type Snapshot interface {
	// RegistrySessionIDs returns the live registry session ids from the last
	// successful refresh. Never nil.
	RegistrySessionIDs(ctx context.Context) []string
	// ResumeSessionIDs returns the live --resume/--session-id ids from the last
	// successful refresh. Never nil.
	ResumeSessionIDs(ctx context.Context) []string
	// TranscriptMtime returns the cached transcript mtime for the session,
	// probing at most once per session per refresh window. It returns nil for a
	// missing or blank session id and for a transcript that cannot be found,
	// exactly as activity.TranscriptMtime does.
	TranscriptMtime(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime
	// Generation advances by one on every successful refresh; it is how a
	// consumer detects that a new session snapshot is available.
	Generation() uint64
	// RefreshOnce performs one refresh and returns the first error. A failed
	// refresh keeps the previous values.
	RefreshOnce(ctx context.Context) error
	// Run refreshes once, then once per Interval until ctx is done. It returns
	// nil on cancellation.
	Run(ctx context.Context) error
}
```

`NewSnapshot` and `Params`:

```go
// Params carries the snapshot's injectable dependencies.
type Params struct {
	// Registry returns the live registry session ids. It never errors.
	Registry func(ctx context.Context) []string
	// Scanner produces fresh `ps` output; one scan runs per refresh.
	Scanner session.ProcessScanner
	// Probe returns a session transcript's mtime. Nil means
	// activity.TranscriptMtime.
	Probe activity.TranscriptMtimeGetter
	// Clock is the injected clock; never read the wall clock.
	Clock libtime.CurrentDateTimeGetter
	// Waiter is the injected sleeper, so a test drives the interval. There is
	// no interval knob: SessionRefreshInterval is the only period.
	Waiter libtime.WaiterDuration
}

// NewSnapshot creates an empty snapshot. Call RefreshOnce or Run before serving.
func NewSnapshot(params Params) Snapshot
```

Refresh semantics (one call to `RefreshOnce`):
1. Run `Scanner(ctx)` once. On error, log at `ERROR` naming the interval and the cause, and return the wrapped error WITHOUT swapping the state — the previous values stay served. On success, `session.ParseLiveSessionIDs(output)` gives the resume ids.
2. Read `Registry(ctx)` once for the registry ids.
3. Under the mutex, swap in the new registry ids, resume ids and `refreshedAt` timestamp (`Clock.Now()`) atomically, and increment `Generation`. `Generation` advances only on a successful refresh. The transcript cache is NOT cleared; its entries are timestamped, so an entry probed before `refreshedAt` is stale.
4. The stored slices are never mutated after publication; the getters return copies that never alias the internal slices and are never nil.

`TranscriptMtime` semantics (the timestamped cache the spec calls for):
- Under the mutex, if an entry for `(sessionID, projectDir, projectsRoot)` exists and its `probedAt` is not before the snapshot's `refreshedAt`, return its value — the cached verdict is still inside its window.
- Otherwise call `Probe(ctx, sessionID, projectDir, projectsRoot)` (or `activity.TranscriptMtime` when `Probe` is nil), then under the mutex cache the result with `probedAt = Clock.Now()` and return it. A nil result is cached too, so an absent transcript is probed once per window, not once per call.
- The mutex MUST NOT be held across the `Probe` call. Two concurrent first lookups for the same session in one window may both probe; that is harmless and bounded.

`Run(ctx)`:
- Call `RefreshOnce(ctx)` once at start; a failure is logged and the loop continues (the next tick retries).
- Then loop with `params.Waiter.Wait(ctx, libtime.Duration(SessionRefreshInterval))`: on a wait error (ctx cancelled) return nil; otherwise `RefreshOnce` again.
- Do not use a raw `time.NewTicker`/`time.AfterFunc`; use the injected `libtime.WaiterDuration`, mirroring `pkg/pageindex`'s `Rescan`.

### 2. Inject the transcript probe into `pkg/activity` and `pkg/session`

`pkg/activity/activity.go` — add an exported probe type and a probe-taking variant, and make the existing function delegate so no caller changes behaviour:

```go
// TranscriptMtimeGetter returns a session transcript's mtime, or nil when the
// transcript is absent. activity.TranscriptMtime is the default implementation.
type TranscriptMtimeGetter func(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime

// ComputeActivityDateWith is ComputeActivityDate with an injected transcript
// probe, so a caller can supply a cached probe instead of touching the
// filesystem. A nil probe means TranscriptMtime.
func ComputeActivityDateWith(
	ctx context.Context,
	probe TranscriptMtimeGetter,
	modifiedDate *libtime.DateTime,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime
```

`ComputeActivityDate` keeps its signature and delegates to `ComputeActivityDateWith(ctx, TranscriptMtime, ...)`. The result for the same inputs must be unchanged.

`pkg/session/classify.go` — add one field to `ClassifyParams`:

```go
	// TranscriptMtime probes the session transcript; nil means
	// activity.TranscriptMtime. The board passes its cached probe so a request
	// never touches the filesystem.
	TranscriptMtime activity.TranscriptMtimeGetter
```

`ClassifySessionState` uses `params.TranscriptMtime` when non-nil and `activity.TranscriptMtime` when nil. The signal order, the four outcomes and the five-minute window are unchanged (`docs/liveness-classification.md` stays authoritative).

### 3. The board reads session-derived fields from the snapshot

In `pkg/board/board.go`, add to `Deps`:

```go
	// Sessions supplies the session-derived fields the board renders. A nil
	// value means the board probes directly (tests only).
	Sessions SessionProbe
```

and define the narrow seam next to `SessionSignals`:

```go
// SessionProbe supplies the cached session-derived fields the board renders.
// pkg/sessionsnapshot.Snapshot satisfies it.
type SessionProbe interface {
	TranscriptMtime(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime
}
```

Store it on `board` as `sessions SessionProbe` in `New`. Keep `Deps.Signals` unchanged (the snapshot satisfies `SessionSignals` too, so production passes the same instance for both).

In `pkg/board/tasks.go` and `pkg/board/goals.go`, replace the direct transcript probe with the injected one:
- In the `session.ClassifySessionState(ctx, session.ClassifyParams{...})` call, set `TranscriptMtime: b.transcriptProbe()`.
- In the `activity.ComputeActivityDate(...)` call, use `activity.ComputeActivityDateWith(ctx, b.transcriptProbe(), ...)`.
- Add a small unexported helper on `board`, e.g. `func (b *board) transcriptProbe() activity.TranscriptMtimeGetter`, that returns `b.sessions.TranscriptMtime` when `b.sessions != nil` and `activity.TranscriptMtime` when nil. Use it at all four call sites (two in `tasks.go`, two in `goals.go`). A nil `Sessions` MUST keep today's direct-probe behaviour so the existing board tests compile and pass.

### 4. Wire through the factory and main

`pkg/factory/api.go`:
- Add:

```go
// CreateSessionSnapshot returns the process-wide session snapshot the board
// reads session-derived fields from. Its Run must run in main's run group.
func CreateSessionSnapshot(state sessionstate.State) sessionsnapshot.Snapshot {
	return sessionsnapshot.NewSnapshot(sessionsnapshot.Params{
		Registry: state.RegistrySessionIDs,
		Scanner:  session.NewPSScanner("-axww", "-o", "args="),
		Probe:    activity.TranscriptMtime,
		Clock:    libtime.NewCurrentDateTime(),
		Waiter:   libtime.NewWaiterDuration(),
	})
}
```

- Change `CreateAPIHandler`'s `sessionState sessionstate.State` parameter into `sessionSnapshot sessionsnapshot.Snapshot` (same position, same arity, so only the argument changes at each call site). In the `board.New(board.Deps{...})` literal set `Signals: sessionSnapshot` and `Sessions: sessionSnapshot`, and delete the now-unused `sessionSignals` type (it has exactly one caller). `CreateSessionStateWatcher` is untouched; it is built in `main.go` from the `sessionstate.State`, not inside `CreateAPIHandler`.

`main.go`:
- Create the snapshot after `sessionState := factory.CreateSessionState()`:
  `sessionSnapshot := factory.CreateSessionSnapshot(sessionState)`.
- Pass it to `CreateAPIHandler` where the `sessionState` argument is today.
- Keep `sessionState` for `factory.CreateSessionStateWatcher(...)`, and add `sessionSnapshot.Run` to the `run.CancelOnFirstErrorWait` run group next to the watcher.

Update every `CreateAPIHandler` call site — there are exactly four (grep `CreateAPIHandler` to confirm none is missed):
- `main.go` (the production call) — pass the `sessionSnapshot` built from `sessionState`.
- `pkg/factory/api_test.go` (`newTestAPIHandlerWithManager`) — pass `factory.CreateSessionSnapshot(factory.CreateSessionState())`.
- `pkg/factory/pageindex_test.go` (`indexHandler`) — same.
- `pkg/factory/pane_test.go` (`paneHandler`) — this helper seeds `sessionState` with `Replace(...)` and expects the board to read the live set, so build the snapshot FROM that seeded state and call `sessionSnapshot.RefreshOnce(context.Background())`, asserting it succeeds (`Expect(err).NotTo(HaveOccurred())`) before passing it; the snapshot's `Registry` reads `sessionState.RegistrySessionIDs`, so the seeded ids land in the snapshot's first refresh.

None of the three test helpers runs `Run`; `RefreshOnce` is enough to seed the snapshot for a test.

### 5. Regenerate the counterfeiter mock

`pkg/sessionsnapshot/mocks/session-snapshot.go` must exist and satisfy `Snapshot`. Run `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; do not add counterfeiter to `go.mod`. If `go run` cannot reach the module proxy, stop and report the blocker rather than hand-writing the fake; a hand-written mock that drifts from the interface is worse than a failed generation.

### 6. Tests

Add Ginkgo/Gomega tests (`pkg/sessionsnapshot/snapshot_test.go`, suite file `snapshot_suite_test.go`, external `package sessionsnapshot_test` where practical; use the `export_test.go` pattern only if an unexported seam needs reaching). Cover:
1. **Refresh reads once, getters return the values** — a fake `Scanner` counting calls and a fake `Registry`; after `RefreshOnce` the getters return the parsed resume ids and the registry ids.
2. **AC2 — the request path spawns no `ps`** — with a scanner that counts calls, `N` calls to `ResumeSessionIDs` between refreshes spawn exactly 0 additional scans; the count rises only across `RefreshOnce`.
3. **AC8 — the interval is at least 60 s** — `Expect(sessionsnapshot.SessionRefreshInterval).To(BeNumerically(">=", 60*time.Second))`, and `Run` with a fake `libtime.WaiterDuration` advances exactly one refresh per `SessionRefreshInterval`.
4. **Refresh failure keeps the previous values** — seed a good refresh, then make the scanner return an error; `RefreshOnce` returns an error, the getters still return the previous ids, and `Generation` does not advance. Assert both: the returned error wraps the cause, and the failure is logged at error level naming the interval and the cause (capture `glog`).
5. **DB4 — the transcript probe runs at most once per task per refresh** — a counting probe: two `TranscriptMtime` calls for the same session in one window probe once; after `RefreshOnce` the next call probes again. A nil result is also cached for the window.
6. **AC5 (container proxy) — a refresh re-reads the live set** — a fake `Scanner`/`Registry` whose output changes between refreshes: after a second `RefreshOnce` the getters return the NEW registry and resume ids and `Generation` has advanced twice; a board built on that snapshot classifies a session that has left both the registry and the process table as no longer `live`. AC5's own measurement (end a session on the deployed board, re-fetch) is rung-2/operator-side and is not run here.
7. **Generation advances only on success** and advances by one per successful refresh.
8. **No raw goroutine** in non-test `pkg/sessionsnapshot`.

Extend `pkg/session/classify_test.go` and `pkg/activity/activity_test.go`:
- `ClassifyParams.TranscriptMtime` non-nil is used instead of `activity.TranscriptMtime` (a probe returning a fixed mtime drives the live/quiet outcome without a real file).
- `ClassifyParams.TranscriptMtime` nil keeps today's behaviour.
- `ComputeActivityDateWith` with a probe returns the newer of the task mtime and the probed mtime; `ComputeActivityDate` still equals `ComputeActivityDateWith(..., activity.TranscriptMtime, ...)` for the same inputs.

`pkg/board/board_test.go`: add `Sessions: h.sessions` to the three `board.New(board.Deps{...})` literals (a small fake implementing `TranscriptMtime`; extend the existing `fakeSignals` or add a sibling fake). Add one test asserting the board classifies from the injected probe's mtime, not the filesystem.

`pkg/sessionsnapshot` needs ≥ 80 % statement coverage.

### 7. CHANGELOG

Append under `## Unreleased` in `CHANGELOG.md` a `feat:` bullet: the board reads session-derived fields (session state and activity date) from a process-wide session snapshot refreshed at least once per 60 s, so no `/api/tasks` or `/api/goals` request spawns `ps` or probes a transcript more than once per refresh. Follow `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

</requirements>

<constraints>
- The classification contract is frozen: the four outcomes, the fixed signal order (registry, transcript recency, live process), and the five-minute window (`session.DefaultLiveWindow`) are unchanged; `docs/liveness-classification.md` stays authoritative and the `ps` scan stays signal #4. Only the source of the inputs changes.
- The `ps` scan runs once per refresh on the timer, never on the request path; its result is cached with a timestamp so a cached verdict never outlives its window.
- The refresh interval is at least 60 s and is the only timer added. No sub-second refresher, no ticker, no `time.AfterFunc`, no configurable interval knob.
- A failed refresh keeps the previous values and logs the error with the interval and the cause; the next refresh retries.
- The snapshot's stored slices are never mutated after publication; `go test -race` must stay clean.
- Response bodies, status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract). `/api/goals` inherits the snapshot and stops spawning `ps` per request as an incidental consequence only; its own list cost is out of scope and gets no acceptance criterion.
- `pkg/pageindex`'s published-snapshot invariant is untouched by this prompt.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` and `libtime.WaiterDuration` injected, ≥ 80 % coverage on the new package.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli change.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/sessionsnapshot/... ./pkg/session/... ./pkg/activity/... ./pkg/board/... ./pkg/factory/...
```
Must pass.

```
go test -race -coverprofile=/tmp/sessionsnapshot.cover ./pkg/sessionsnapshot/ && go tool cover -func=/tmp/sessionsnapshot.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80 %).

```
grep -rn 'SessionRefreshInterval' pkg/
```
Must print the declaration and its configuration site.

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/sessionsnapshot/
```
Must exit 0 (no raw goroutines in non-test code).

```
! grep -rnE 'NewTicker|time\.AfterFunc' pkg/sessionsnapshot/
```
Must exit 0 (no ticker; the loop uses the injected waiter).

```
grep -q 'sessionsnapshot' pkg/factory/api.go && grep -q 'sessionSnapshot.Run' main.go
```
Must exit 0 (the snapshot is wired and its Run is in the run group).

Before finishing, walk Desired Behavior 4 and AC2/AC5/AC8 against the tests you wrote and name the test that establishes each.
</verification>
