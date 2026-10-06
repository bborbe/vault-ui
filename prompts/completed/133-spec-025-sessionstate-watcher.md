---
status: completed
spec: [025-lazy-pane-resolution-at-jump-time]
summary: Added a new pkg/sessionstate package that keeps the harness session registry's live ids in memory from an initial read, fsnotify file events and a 60 s rescan, wired it through factory.CreateSessionState/CreateSessionStateWatcher and main.go so the board's Live badge and jump availability read from it and a live-set change pushes the existing watcher frame (task + goal per vault), promoted fsnotify to a direct dependency, and documented it in docs/pane-resolution.md with a CHANGELOG entry.
execution_id: vault-ui-lazy-pane-exec-133-spec-025-sessionstate-watcher
dark-factory-version: dev
created: "2026-10-06T06:16:43Z"
queued: "2026-10-06T06:48:20Z"
started: "2026-10-06T07:56:47Z"
completed: "2026-10-06T08:09:29Z"
branch: dark-factory/133-spec-025-sessionstate-watcher
---

# Keep live session state current from registry file events

<summary>
- A new in-memory session-state store holds the live session ids and is kept current by file events on the harness session registry, not by a timer.
- The store is read once at startup, re-read on every registry change, and re-read every 60 seconds as the safety net for a missed event.
- The board's Live badge reads from that store instead of opening the registry directory on every request.
- When the live set actually changes, the board pushes a refresh to connected browsers, so an idle board's badge updates within a couple of seconds instead of waiting for the 60-second fallback poll.
- The liveness classification itself is untouched: the same four outcomes, the same signal order, the same five-minute window.
- The per-request process scan stays exactly where it is.
- A new document records when a pane is resolved and how fresh session state is.
</summary>

<objective>
Make session state event-driven: a new `pkg/sessionstate` package keeps the live session registry ids in memory, updated from file events on `~/.claude/sessions/*.json` with a 60-second rescan as the safety net, and the board's Live badge and the jump control's availability read from it. This is the second half of the architectural rule this spec implements — background work is event-driven or computed on demand, never a fixed short-interval ticker.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-lazy-pane-resolution-at-jump-time.md`. This prompt covers Desired Behaviors 6, 7 and the documentation half of 8, and Acceptance Criteria AC7, AC8 and the `docs/pane-resolution.md` half of AC9.

Prerequisites: prompt `1-spec-025-go-pane-resolver.md` added `pkg/pane/resolver.go` and `factory.CreatePaneResolver`; prompt `2-spec-025-drop-pane-state-and-delete-panecache.md` deleted `pkg/panecache`, removed the board's pane dependency, and gave `CreateAPIHandler` a `paneResolver mutations.PaneResolver` fourth parameter. Read `pkg/pane/resolver.go`, `pkg/factory/api.go` and `pkg/factory/mutations.go` first and use the real signatures you find there.

Read these files before writing (current shapes verified):

- `pkg/activity/activity.go` — `ReadRegistrySessionIDs(ctx context.Context, root string) []string`: the existing registry reader. It lists `root` with `os.ReadDir` and keeps `*.json` entries, skips unreadable entries with a `glog.V(4)` line, and returns an empty slice when the directory is missing. This is the read this watcher re-runs; do not write a second reader.
- `pkg/factory/api.go` — `type sessionSignals struct{ homeDir string }` with `RegistrySessionIDs` (today: `activity.ReadRegistrySessionIDs(ctx, filepath.Join(s.homeDir, ".claude", "sessions"))`) and `ResumeSessionIDs` (today: `session.NewPSScanner("-axww", "-o", "args=")(ctx)` → `session.ParseLiveSessionIDs`). It implements `board.SessionSignals`. Also `CreateWatcher`, the sibling watcher `run.Func`, and `CreateStatusCacheLoader`.
- `pkg/pageindex/pageindex.go` — the pattern to follow: `RescanInterval = 50 * time.Second` with a 1-second poll granularity, a documented staleness bound, and a `Rescan(ctx) error` loop. `docs/page-index.md` shows the documented shape this prompt's `docs/pane-resolution.md` should mirror.
- `pkg/watchrefresh/handler.go` — how a file event is turned into a WebSocket broadcast today (`manager.Broadcast(websocket.WatcherFrame(event))` after the index is refreshed).
- `pkg/websocket/frames.go` — `WatcherFrame(event ops.WatchEvent) []byte`, marshalling `{"type": <event.Event>, "task_id": <event.Name>, "vault": <event.Vault>, "item_kind": <event.Type>}`.
- `src/vault_ui/static/app.js` — `handleTaskUpdate(data)`: a frame with `item_kind: "task"` and a type other than `deleted` calls `loadCurrentView()`; a frame with `item_kind: "goal"` calls `loadGoals()`. The only other refresh path is the 60-second fallback poll (`POLL_INTERVAL_MS = 60000`), which is why a pushed frame is required for a 2-second badge update.
- `pkg/session/classify.go` and `docs/liveness-classification.md` — the FROZEN classification contract. Do not change either. `ClassifySessionState` takes the registry ids as a parameter, so moving where they come from changes nothing about the classification.
- `main.go` — the `run.CancelOnFirstErrorWait` list, where every background `run.Func` is launched.

The harness writes one `<pid>.json` per live session under `~/.claude/sessions/`, as documented in `docs/liveness-classification.md`. An entry is deleted when its session exits, so presence means live.

The coding plugin docs are available in the container at `/home/node/.claude/plugins/marketplaces/coding/docs/` — read `go-concurrency-patterns.md` and `go-context-cancellation-in-loops.md` before writing the fan-out and the loop, plus:

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
</context>

<requirements>

### 1. New package `pkg/sessionstate` (`pkg/sessionstate/sessionstate.go`)

```go
// DefaultRescanInterval is the safety-net rescan period for a missed file event.
const DefaultRescanInterval = 60 * time.Second

// State is the in-memory set of live registry session ids.
type State interface {
	// RegistrySessionIDs returns the live session ids currently known.
	RegistrySessionIDs(ctx context.Context) []string
	// Replace atomically swaps the whole set and reports whether it changed.
	Replace(ids []string) bool
}

// NewState creates an empty State.
func NewState() State

// Source signals that a watched directory may have changed.
type Source interface {
	// Watch calls changed once per change under dir, until ctx is cancelled.
	// It returns nil on cancellation and an error otherwise.
	Watch(ctx context.Context, dir string, changed func()) error
}

// NewFSNotifySource returns the fsnotify-backed Source.
func NewFSNotifySource() Source

// WatchParams carries the watcher's injectable dependencies.
type WatchParams struct {
	Dir      string
	State    State
	Source   Source
	Read     func(ctx context.Context, dir string) []string
	Changed  func()
	Interval time.Duration
}

// Watcher keeps State current from the registry directory.
type Watcher interface {
	Run(ctx context.Context) error
}

// NewWatcher creates a Watcher. A non-positive Interval falls back to
// DefaultRescanInterval.
func NewWatcher(params WatchParams) Watcher
```

`State` is backed by a mutex-guarded `[]string` (or a set) and must be safe for concurrent readers. `RegistrySessionIDs` returns a copy, never the internal slice, and never nil (an empty state returns an empty, non-nil slice — `sessionStatePtr` in `pkg/board/tasks.go` and `slices.Contains` in `ClassifySessionState` both handle either, but a stable non-nil shape keeps the JSON and the tests honest).

`Replace` compares the incoming set with the stored one **order-insensitively** and returns `true` only when the set differs. It must dedupe and drop empty ids.

### 2. The watcher's `Run`

1. **Initial read**: `state.Replace(read(ctx, dir))`. Do not call `Changed` for the initial read.
2. Then run two functions in parallel through `github.com/bborbe/run` — `run.CancelOnFirstErrorWait(ctx, run.SkipErrors(run.CatchPanic(watchFunc)), rescanFunc)`.
   - `watchFunc`: `source.Watch(ctx, dir, func() { apply(ctx) })`. A `Source` error must NOT take the board down. Log it at `glog.V(2)` naming the directory and the error, then return nil, so the rescan keeps the state current and the board keeps serving — this is the spec's failure mode "the watcher goroutine exits or crashes → badge stops updating from events and falls back to the 60 s rescan only; the board keeps serving", whose detection is that V(2) line. `run.CatchPanic` turns a panic in the source into an error, which `run.SkipErrors` logs and swallows. Do NOT let the watch error reach `CancelOnFirstErrorWait`: that would cancel the rescan loop and, at the top level, take the whole service down with it.
   - `rescanFunc`: a loop that waits `Interval` on a `time.Timer` with a `select` on `ctx.Done()`, calls `apply(ctx)` on each tick, `timer.Stop()`s and returns nil when the context is cancelled.
   - `apply(ctx)`: holding a watcher-owned `sync.Mutex` for the whole step, re-read the directory, `state.Replace(...)`, and call `Changed()` only when `Replace` reported a change, so a no-op rescan does not broadcast. The lock is required: `watchFunc` and `rescanFunc` both call `apply`, and without it a rescan whose read began before a file event can `Replace` after the event's fresher read and revert the state to a stale set for up to `Interval` — a logic race `-race` does not detect.
3. Return nil on context cancellation.

Concurrency must go through `github.com/bborbe/run` — a bare `go func()` is a violation. Honour `ctx.Done()` in every loop.

### 3. The fsnotify source

`NewFSNotifySource` returns a `Source` over `github.com/fsnotify/fsnotify` (already in `go.sum` as an indirect dependency; the direct import promotes it — **write the importing code first, then run `go mod tidy`**, never the other way round; until the import exists, tidy keeps it `// indirect` because vault-cli's watcher needs it).

- `fsnotify.NewWatcher()`, then `watcher.Add(dir)`. A failed `Add` (the directory is missing) returns the wrapped error — `watchFunc` logs it at `glog.V(2)` and returns nil, and the rescan keeps the state current. A directory that reappears later is picked up by the rescan's re-read, which is the spec's stated recovery.
- `select` on `ctx.Done()` (return nil, closing the watcher via `defer`), `watcher.Errors`, and `watcher.Events`. Call `changed` on an event; log an error channel value at `glog.V(2)` and keep going; a closed channel returns nil.
- Mirror `github.com/bborbe/vault-cli/pkg/ops/watch.go`'s `Execute` shape for the select loop.

### 4. Wire it in `pkg/factory`

- `sessionSignals` becomes `struct{ state sessionstate.State }`; `RegistrySessionIDs` returns `s.state.RegistrySessionIDs(ctx)`. `ResumeSessionIDs` is **unchanged** — the per-request `ps` scan stays on the request path (spec Non-goal).
- `CreateAPIHandler` gains a trailing `sessionState sessionstate.State` parameter and passes `sessionSignals{state: sessionState}` as `board.Deps.Signals`. Keep the existing parameters and their order.
- Add two composition helpers:

  ```go
  // CreateSessionState returns the process-wide live-session state the board reads.
  func CreateSessionState() sessionstate.State {
  	return sessionstate.NewState()
  }

  // CreateSessionStateWatcher returns a run.Func that keeps state current from the
  // harness session registry and pushes a board refresh whenever the live set changes.
  func CreateSessionStateWatcher(
  	loader config.Loader,
  	manager websocket.ConnectionManager,
  	state sessionstate.State,
  	homeDir string,
  ) run.Func
  ```

  The watcher closure loads the vaults once with `loader.GetAllVaults(ctx)` (wrap and return its error, like `CreateWatcher` does), collects their names, and builds `sessionstate.NewWatcher(sessionstate.WatchParams{...})` with `Dir: filepath.Join(homeDir, ".claude", "sessions")`, `Source: sessionstate.NewFSNotifySource()`, `Read: activity.ReadRegistrySessionIDs`, `Interval: sessionstate.DefaultRescanInterval`, and a `Changed` callback that broadcasts one refresh frame per configured vault **per entity kind**:

  ```go
  manager.Broadcast(websocket.WatcherFrame(ops.WatchEvent{Event: "modified", Vault: name, Type: "task"}))
  manager.Broadcast(websocket.WatcherFrame(ops.WatchEvent{Event: "modified", Vault: name, Type: "goal"}))
  ```

  Both kinds are needed because the frontend dispatches a frame by `item_kind` and ignores the other kind: with only a task frame, a session that goes live while the operator is on the goals view would never refresh its badge. The frame SHAPE is the existing `WatcherFrame` shape, so no new protocol is introduced. This frame is a Go-only emission the Python reference never produces; the parity harness does not see it because `parity.sh` runs the Go binary with `HOME=$FIXTURE`, which has no `.claude/sessions`, so `Replace` never reports a change. Do not edit `scripts/parity/`. Keep the closure thin — assembling vault names and the per-vault broadcast fan-out both belong in package-level helpers (e.g. `sessionRefreshBroadcaster(manager, names) func()`), not in the `Create*` function. Import the doubles as `github.com/bborbe/vault-cli/mocks` and `websocketmocks "github.com/bborbe/vault-ui/pkg/websocket/mocks"`, matching `pkg/factory/watcher_refresh_test.go`.

### 5. Keep the existing call sites compiling

Update every `CreateAPIHandler` call site for the new trailing parameter, passing `factory.CreateSessionState()`:

- `pkg/factory/api_test.go` — `newTestAPIHandlerWithManager` (and `newTestAPIHandler` through it).
- `pkg/factory/pageindex_test.go` — `indexHandler`.

Existing assertions must not change.

### 6. Wire `main.go`

- `sessionState := factory.CreateSessionState()`.
- Pass it as the trailing argument of `factory.CreateAPIHandler`.
- Add `factory.CreateSessionStateWatcher(loader, manager, sessionState, homeDir)` to the `run.CancelOnFirstErrorWait` list. Keep every existing entry and its order.

### 7. Tests

Hand-write the doubles (a struct of function fields; a nil field panics on an unexpected call). Do NOT add a `//counterfeiter:generate` directive — this repo has no `make generate` target and counterfeiter is not a module dependency (the documented deviation in `prompts/completed/126-background-pane-resolution.md`).

- `pkg/sessionstate` unit tests:
  - `Replace` returns true on a change, false when the same set arrives in a different order, and drops duplicates and empty ids.
  - `RegistrySessionIDs` returns a copy (mutating the returned slice does not change the state).
  - `Run` performs the initial read before it starts the watch and the rescan, so the state is populated as soon as `Run` is entered.
  - A source event re-reads the directory and updates the state; the `Changed` callback fires exactly once for a real change and not at all for an unchanged re-read.
  - **Negative control (AC7):** with a `fakeSource` that never fires and `Interval: 500*time.Millisecond`, switch the `Read` double's return to the fresh set only after the initial read is observed (`Eventually` on the state), then `Consistently(..., 200*time.Millisecond)` asserts the state is still stale, then `Eventually(..., 3*time.Second)` asserts it is fresh. Assert both readings, so the spec proves the rescan — not a ticker, and not the event path.
  - `Run` returns nil when its context is cancelled.
  - A read that returns nothing (missing directory) leaves the state empty and `Run` keeps running.
  - **Serialised apply (C1 regression lock):** `Read` double — call 1 (the initial read) returns `{}`; call 2 blocks on a `release` channel then returns `{"old"}`; every later call returns `{"new"}`. Use `Interval: 50*time.Millisecond` so the rescan makes call 2, wait until call 2 has been entered, then fire one `fakeSource` event, wait ~100 ms, close `release`. Wrap `State` in a recording double that logs every `Replace` argument and assert: once `{"new"}` has been stored, `{"old"}` is never stored after it. Do not assert only the final state — a later rescan returns `{"new"}` and would mask the race.
  - A `fakeSource` whose `Watch` returns an error immediately, and one whose `Watch` panics: `Run` keeps running (does not return before cancel), and the rescan still updates the state on the next `Interval`.
  - `NewFSNotifySource().Watch` on a temp dir: (re)writing a file inside the `Eventually` poll until `changed` has been called at least once (a single write can precede `watcher.Add`); a missing dir returns a non-nil error; cancelling ctx returns nil. (This keeps the fsnotify source inside `pkg/sessionstate`'s own coverage.)
  - Tests may start `Run` with `go func() { defer GinkgoRecover(); ... }()` as the existing factory tests do; the `run`-only rule covers production code.
  - `pkg/sessionstate` must reach at least 80% statement coverage.
- `pkg/factory` test: build `CreateSessionStateWatcher` over a `mocks.Loader` whose `GetAllVaultsReturns` lists one vault named `personal`, a `mocks.WebsocketConnectionManager` (`pkg/websocket/mocks`) and a `factory.CreateSessionState()` state, with a temp dir as `homeDir` and `mkdir -p <homeDir>/.claude/sessions` first, then seed one entry `seed.json` with `{"sessionId":"seed"}`. Run it on a cancellable context in the background, then `Eventually(state.RegistrySessionIDs(ctx)).Should(ConsistOf("seed"))` with `manager.BroadcastCallCount()` still 0 — this proves the initial read has run (and fired no frame) before any further write, so the next write cannot be absorbed by the initial read. Then:
  - write a second registry entry (a different `sessionId` and `.json` name; `<pid>.json` with a `sessionId`) into that directory **inside the `Eventually` poll function** — rewrite the same bytes on every poll, then return `manager.BroadcastCallCount()` — and assert it reaches exactly 2. Rewriting is required because the test cannot observe when the fsnotify watch is armed: a single write that lands before the watch is added is missed and only the 60 s rescan would repair it. Rewriting identical content leaves the set unchanged, so later polls cannot inflate the count. Then unmarshal both frames and assert `type` is `modified`, `task_id` is `""`, `vault` is `personal`, and `item_kind` is `task` for one and `goal` for the other;
  - rewrite the same file with the same `sessionId` and `Consistently` assert the broadcast count does not grow — an event that changes nothing must not push a frame;
  - add a third entry with a different `sessionId` and `Eventually` assert the count grows by 2 again.
  This is a real fsnotify watch on a temp directory, which is allowed: the ban is on spawning a real subprocess, a real `wezterm`, or a real network call.

### 8. `docs/pane-resolution.md` (new file)

Write the document this spec's Goal names. It must contain, at minimum, these two headings:

- `## When a pane is resolved`
- `## Session state freshness`

`## When a pane is resolved` must record:
- A pane is resolved only when the operator clicks a card's jump control — never on a list read, never on a timer. `/api/tasks` and every other list response carry no pane id at all.
- The resolver's seam signature, written literally so it is greppable: `Resolve(ctx context.Context, sessionID string) (string, bool)`.
- How the resolution works: the harness session registry (`~/.claude/sessions/*.json`) supplies the session's current name; `wezterm cli list --format json` supplies the panes; the registry name (with its leading status glyph stripped) is matched against the pane titles; exactly one match resolves, zero or several do not.
- Where the composition root wires it: `factory.CreatePaneResolver` in `pkg/factory`, handed to `CreateAPIHandler` and passed on to `CreateMutationService` as the mutation service's `Pane`.
- The failure contract: no resolvable pane answers non-2xx with a body naming the failure; the jump control stays offered because it follows live session state, and the failure is reported on the click.

`## Session state freshness` must record:
- The state is held in memory in `pkg/sessionstate`, read once at startup, re-read on every file event on the registry directory, and re-read every 60 seconds as the safety net for a missed event.
- The staleness bound: a change is visible after the debounce-free event path (sub-second), and a missed event is repaired within `DefaultRescanInterval` (60 s).
- The Live badge and the jump control's availability both read from this state, and a change pushes a refresh frame to connected browsers — two frames per configured vault (`task` and `goal`), each of which triggers one list reload in a browser viewing that kind.
- If the registry directory is deleted and recreated while the service runs, the fsnotify watch is lost and freshness falls back to the 60 s rescan until restart.
- The classification contract is unchanged and points at `docs/liveness-classification.md`: the same four outcomes, the same signal order, the same five-minute window. Only where the registry ids are read from moved.

Start the file with `# Pane resolution` as its title heading, matching `docs/page-index.md`'s shape. (The vault's no-H1 rule applies to Obsidian notes, not to repo docs.)

### 9. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes, then walk AC7, AC8 and the documentation half of AC9 and name the test or command output that establishes each. AC8's `git diff` half cannot run here (`hideGit: true`) — it stays on the operator rung; the container evidence is `go test ./pkg/session/...` passing with the classification cases present by name.

</requirements>

<constraints>
- `docs/liveness-classification.md` is a frozen contract: the four outcomes, the signal order and the five-minute window do not change. Do NOT touch `pkg/session/classify.go` or `pkg/session/session.go`.
- The per-request `ps` scan (`sessionSignals.ResumeSessionIDs` via `session.NewPSScanner("-axww", "-o", "args=")`) stays on the request path. Moving it off is the sibling task's job, not this one.
- Every route keeps its response body, status codes, query parameters and WebSocket frame content byte-identical to the Python reference (spec 023 parity contract). The refresh frames this prompt adds reuse the existing `WatcherFrame` shape — do not invent a new frame type, a new key, or a new route.
- No persistence of session state to disk.
- No new external service dependency, no new HTTP route, no new query parameter, no opt-out flag, no configurable rescan interval exposed as configuration. `WatchParams.Interval` exists so a test can drive the rescan without waiting a minute; the production value is the `DefaultRescanInterval` constant.
- No Prometheus metric is added: the observability this spec asks for is log lines.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no `fmt.Errorf`, `Create*` factories carry no business logic, Ginkgo/Gomega tests, `glog` for logging, ≥80 % coverage on new packages.
- Concurrency goes through `github.com/bborbe/run`; a bare `go func()` is a violation. Loop and watcher code honours `ctx.Done()`.
- Hand-write test doubles; do not add a counterfeiter directive.
- Write the fsnotify import before running `go mod tidy`, never after.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- All paths in the code are repo-relative; the only absolute paths in this prompt are the mounted coding docs under `/home/node/.claude/`.
- `make parity` stays red until prompt 5 re-baselines `/api/tasks`; do not run it as a gate here and do not edit `scripts/parity/`.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/sessionstate/... ./pkg/session/... ./pkg/board/... ./pkg/mutations/... ./pkg/factory/...
```
Must pass. The `./pkg/session/...` entry is AC8's evidence that the classification tests still pass unchanged — this prompt must not have edited them.

```
grep -q '^## When a pane is resolved' docs/pane-resolution.md
```
Must exit 0.

```
grep -q '^## Session state freshness' docs/pane-resolution.md
```
Must exit 0.

```
grep -n 'Resolve(ctx' docs/pane-resolution.md
```
Must print at least one line — the resolver's seam signature is recorded, not just the headings (a headings-only file fails AC9).

```
grep -Eq '^\s+github.com/fsnotify/fsnotify v[0-9.]+$' go.mod
```
Must exit 0 — the line carries no `// indirect` marker, so the direct import was promoted by `go mod tidy` (the dependency is already listed as indirect today, so a bare `grep fsnotify` would pass before any change).

```
! grep -rn 'vault-ui/pkg/session"' pkg/sessionstate
```
Must exit 0 — this package neither imports nor re-implements the classification; it only produces the registry ids that `ClassifySessionState` consumes.
</verification>
