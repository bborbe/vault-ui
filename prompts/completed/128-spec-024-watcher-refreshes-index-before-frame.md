---
status: completed
spec: [024-serve-list-reads-from-page-index]
summary: Added pkg/watchrefresh so watcher task/goal events refresh only their own page-index folder and broadcast their frame only after a post-event rebuild has swapped in, with an injectable watch operation in factory.CreateWatcher wired from main.go, plus the ConnectionManager counterfeiter fake, Ginkgo tests for AC2/AC4 through the real callback and for the handler, docs and changelog.
execution_id: vault-ui-page-index-exec-128-spec-024-watcher-refreshes-index-before-frame
dark-factory-version: v0.196.0
created: "2026-10-05T19:39:53Z"
queued: "2026-10-05T20:18:36Z"
started: "2026-10-05T20:31:56Z"
completed: "2026-10-05T20:40:10Z"
branch: dark-factory/128-spec-024-watcher-refreshes-index-before-frame
---

# Refresh the page index on watcher events and send each task/goal frame only after the fresh swap

<summary>
- An edit made to a task or goal file outside vault-ui (Obsidian, an agent, a script) now refreshes the in-memory copy of that one folder, within about a second.
- Only the folder the change happened in is re-read; every other folder of every vault stays untouched.
- The live-update message the browser receives for that change is held back until the refreshed copy is in place, so a browser that re-fetches on the message sees the new data, never the old.
- If a change arrives while that folder is already being refreshed, its message waits for a refresh that started after the change, not the one already running.
- Theme and objective change messages go out exactly as before, without touching the index.
- A change for a vault name the service does not know is still announced, just without a refresh.
- The file-watching component is now injectable, so tests drive these rules with a fake watcher instead of real file-system timing.
- The watched directories and the message contents are unchanged; the parity harness still passes.
</summary>

<objective>
Make the callback that `factory.CreateWatcher` composes from a new `pkg/watchrefresh` handler route each task/goal `ops.WatchEvent` through `pageindex.PageIndex.Refresh` for that event's `(vault path, folder)` key and broadcast the event's frame only after `Refresh` returns, so external edits reach the index and a client re-fetching on a frame reads fresh data. Make the watch operation injectable so the ordering is provable in tests.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/024-serve-list-reads-from-page-index.md`. This prompt covers Desired Behavior 4, Acceptance Criteria AC4 and AC2 (through the real watcher callback), and keeps AC7. Read `docs/page-index.md` § Frame ordering and § Key derivation (written by prompt 1) — this prompt implements them; correct the doc if the code you write differs.

Prerequisites from earlier prompts — read the real code first:
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey(vaultPath, pagesDir string) Key`, and `PageIndex` with `Refresh(ctx context.Context, key Key) error` (blocks until a rebuild that started after the call has finished — swapped on success, logged on failure; returns an error only when `ctx` is cancelled first) and `ListPages`.
- `pkg/factory/pageindex.go` — `CreatePageIndex(pageStorage storage.PageStorage, currentDateTimeGetter libtime.CurrentDateTimeGetter) pageindex.PageIndex`, `CreatePageIndexWarmup(...)`.
- `main.go` — builds `pageIndex` and passes it to `CreateAPIHandler`; currently calls `factory.CreateWatcher(loader, manager)`.

Current watcher code (verified) in `pkg/factory/api.go`:
```go
func CreateWatcher(loader config.Loader, manager websocket.ConnectionManager) run.Func {
	return func(ctx context.Context) error {
		vaults, err := loader.GetAllVaults(ctx)
		if err != nil {
			return errors.Wrap(ctx, err, "load vaults for watcher")
		}
		targets := buildWatchTargets(vaults)
		glog.V(2).Infof("starting vault watcher for %d vaults", len(targets))
		return ops.NewWatchOperation().Execute(ctx, targets,
			func(event ops.WatchEvent) error {
				glog.V(3).Infof("watcher event %s %s/%s", event.Event, event.Vault, event.Name)
				manager.Broadcast(websocket.WatcherFrame(event))
				return nil
			},
		)
	}
}
```
`buildWatchTargets(vaults []*config.Vault) []ops.WatchTarget` sets `VaultPath: vault.Path`, `VaultName: vault.Name`, and watch dirs `GetTasksDir()`→`task`, `GetGoalsDir()`→`goal`, `GetThemesDir()`→`theme`, `GetObjectivesDir()`→`objective`. Do not change it.

vault-cli v0.159.0 (in-container module path `/home/node/go/pkg/mod/github.com/bborbe/vault-cli@v0.159.0`):
- `pkg/ops/watch.go` — `type WatchOperation interface { Execute(ctx context.Context, vaults []WatchTarget, handler func(WatchEvent) error) error }`; `NewWatchOperation() WatchOperation`; `WatchEvent{Event, Name, Vault, Path, Type string}` where `Vault` is the target's `VaultName` and `Type` the watch dir's `Kind`. The handler runs on a per-path debounce timer goroutine (`time.AfterFunc`, 100 ms), so blocking inside it delays only that event and never the fsnotify loop; its return value is ignored.
- `mocks/watch-operation.go` — `mocks.WatchOperation` (`ExecuteStub`, `ExecuteArgsForCall`) for driving the callback in tests.
- `pkg/config/config.go` — `(*Vault).GetTasksDir()` / `GetGoalsDir()`.

Other files:
- `pkg/websocket/connection_manager.go` — `type ConnectionManager interface { Connect; Disconnect; Send; Broadcast(payload []byte); Pump; Count }`; `pkg/websocket/frames.go` — `WatcherFrame(event ops.WatchEvent) []byte`.
- `pkg/websocket/metrics.go` — the counterfeiter directive form used in this repo.
- `pkg/factory/watcher_internal_test.go` — existing `testing.T` tests calling `CreateWatcher(loader, manager)` three times; they must be updated to the new signature and keep their assertions.
- `scripts/parity/ws_probe.py` — writes a fixture task file, then issues PATCH flag requests, comparing frame sequences with a 1.5 s settle window.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. Index-aware event handler (new package `pkg/watchrefresh`)

The event logic is business logic and must not live in `pkg/factory` (`docs/dod.md`: `Create*` factories contain no business logic). Create `pkg/watchrefresh/handler.go` with a package doc comment and the repo's copyright header (copy from `pkg/statuscache/statuscache.go`). Do NOT use or modify the existing legacy `pkg/watcher` package.

```go
// EventKey maps a watcher event to the page-index key it invalidates.
func EventKey(event ops.WatchEvent, vaultsByName map[string]*config.Vault) (pageindex.Key, bool)

// NewHandler returns the watch handler: it refreshes the event's key, then broadcasts its frame.
func NewHandler(
	ctx context.Context,
	vaults []*config.Vault,
	pageIndex pageindex.PageIndex,
	manager websocket.ConnectionManager,
) func(ops.WatchEvent) error
```

`NewHandler` builds the `map[string]*config.Vault` lookup by `vault.Name` once. The handler is invoked concurrently: vault-cli's debouncer deletes its pending entry before calling the handler and `Execute` does not wait for running handlers, so a new event for the same path while a handler is blocked runs a second handler at the same time. Keep the handler free of shared mutable state beyond the read-only lookup.

Handler behaviour for each `event`, in this order:
1. Log the existing `glog.V(3).Infof("watcher event %s %s/%s", ...)` line first.
2. Derive the key with `EventKey`:
   - `event.Type == "task"` → `pageindex.NewKey(vault.Path, vault.GetTasksDir())`
   - `event.Type == "goal"` → `pageindex.NewKey(vault.Path, vault.GetGoalsDir())`
   - any other type (`theme`, `objective`) → no key.
   - `event.Vault` not in the lookup → no key, and log `glog.V(2).Infof` naming the unknown vault (spec failure mode: config changed while running; the frame is still broadcast).
3. When there is a key, call `pageIndex.Refresh(ctx, key)` with the `ctx` passed to `NewHandler` (the run func's ctx). A failed rebuild does not surface here — `Refresh` logs it and returns nil, and the previous snapshot keeps serving. If `Refresh` returns an error, `ctx` is being cancelled (shutdown): log `glog.V(2).Infof("drop frame on shutdown for %s/%s", event.Vault, event.Name)` and return nil without broadcasting.
4. `manager.Broadcast(websocket.WatcherFrame(event))` — unchanged frame content — strictly after step 3 returned nil (or immediately when there is no key).
5. Return nil.

### 1b. Injectable watch operation (`pkg/factory/api.go`)

New signature (update the doc comment accordingly):

```go
func CreateWatcher(
	loader config.Loader,
	manager websocket.ConnectionManager,
	pageIndex pageindex.PageIndex,
	watchOperation ops.WatchOperation,
) run.Func
```

The closure keeps loading vaults and building targets exactly as today, then only composes: `watchOperation.Execute(ctx, targets, watchrefresh.NewHandler(ctx, vaults, pageIndex, manager))`. No key logic, switch, lookup, `Refresh` call or per-event logging lives in `pkg/factory`.

This keeps the read-side and event-side keys identical: the board reads `NewKey(board.Vault.Path, TasksFolder|GoalsFolder)`, and `vaultconfig.BuildVaultConfig` sets `TasksFolder = cliVault.TasksDir` (non-empty, so equal to `GetTasksDir()`) and `GoalsFolder = cliVault.GetGoalsDir()` from the same vault-cli entry.

### 2. Wire `main.go`

Replace `factory.CreateWatcher(loader, manager)` with `factory.CreateWatcher(loader, manager, pageIndex, ops.NewWatchOperation())` (import `github.com/bborbe/vault-cli/pkg/ops`). `CreateWatcher` itself no longer calls `ops.NewWatchOperation()`. No other `main.go` change.

### 3. Update existing watcher tests

In `pkg/factory/watcher_internal_test.go`, pass an index (`CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime())`) and `ops.NewWatchOperation()` to every `CreateWatcher` call so `TestCreateWatcherBroadcastsChanges`, `TestCreateWatcherSurfacesLoaderError` and `TestCreateWatcherStopsOnContextCancel` keep their current assertions (the real-fsnotify test still proves a real file write yields a frame end to end).

### 4. ConnectionManager fake

Add `//counterfeiter:generate -o ./mocks/websocket-connection-manager.go --fake-name WebsocketConnectionManager . ConnectionManager` above `ConnectionManager` in `pkg/websocket/connection_manager.go` and generate it (from `pkg/websocket`: `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; prepend the repo's copyright header like `pkg/websocket/mocks/websocket-metrics.go`; do not add counterfeiter to `go.mod`; if the proxy is unreachable, hand-write it in counterfeiter's exact shape). If running `-generate` also regenerates the existing metrics fake, re-prepend the copyright header to it so its content stays equivalent.

### 5. Tests through `CreateWatcher` (Ginkgo, external package `factory_test`, matching `factory_suite_test.go`; test 7 notes its package)

Fixture: a `mocks.Loader` returning two vault-cli vaults A and B (`Path`, `TasksDir`, `GoalsDir`, `ThemesDir`, `ObjectivesDir` set); the index from `factory.CreatePageIndex(fakeStorage, libtime.NewCurrentDateTime())` where `fakeStorage` is a `vault-cli mocks.PageStorage` whose stub serves per-key content (a test-controlled, mutex-guarded map from `(vaultPath, pagesDir)` to pages built with `domain.NewPage`) and can block on a channel; a `mocks.WatchOperation` whose `ExecuteStub` captures the handler, signals the test, and blocks until `ctx` is done; the `WebsocketConnectionManager` fake whose `BroadcastStub` records, for each frame, the frame bytes and the content `pageIndex.ListPages` returns for the frame's key at that instant — mapping the frame to a key by unmarshalling its `vault` and `item_kind` JSON fields (see `pkg/websocket/frames.go`) into `ops.WatchEvent{Vault: ..., Type: ...}` and calling `watchrefresh.EventKey` and skipping the read when there is no key (theme, objective, unknown vault), so the recorder never builds a never-built key. Two packages named `mocks` are imported (vault-cli's and `pkg/websocket/mocks`) — alias them on import. Start `factory.CreateWatcher(loader, fakeManager, pageIndex, fakeWatch)(ctx)` in a test goroutine, wait for the captured handler, and warm both vaults' task and goal keys with `pageIndex.Build`. Call the captured handler directly (from test goroutines where it must block).

Cover:
1. **AC2 through the callback** — one `task` event for vault A: exactly 1 new `ListPages(A.Path, A.TasksDir)` call and 0 new calls for A's goals and both of B's keys; the next `ListPages` for A's tasks returns the content the fake now serves.
2. **AC4(a)** — with the fake storage blocked, deliver a `task` event for A in a goroutine: `Consistently` (≥ 100 ms) no broadcast; release; `Eventually` exactly one broadcast whose recorded read-at-broadcast content is the new content (the swap happened before the frame), and whose bytes equal `websocket.WatcherFrame(event)`.
3. **AC4(b)** — the storage fake captures its content when each call STARTS (before blocking) and blocks each call on its own gate. Deliver event E1 (task `One`) and wait until the first storage call has entered (v1 captured); change the served content to v2; deliver event E2 (task `Two`) for the same key. Release only the first call: E1's frame is broadcast with v1, and E2's frame is NOT broadcast while the second call is still blocked (`Consistently` ≥ 100 ms). Release the second call: E2's frame follows, its recorded read-at-broadcast content is v2, and a read issued when E2's frame is observed returns v2. Exactly 2 `ListPages` calls for that key after warm-up.
4. **AC4(c)** — a `theme` and an `objective` event: each frame is broadcast immediately and unchanged, with 0 new `ListPages` calls.
5. **Goal events** — a `goal` event for B refreshes only B's goals key.
6. **Unknown vault** — an event whose `Vault` is not configured is broadcast with 0 `ListPages` calls.
7. **Shutdown** (in `pkg/watchrefresh`) — use the generated `pkg/pageindex/mocks` `PageIndex` fake with a `RefreshStub` that blocks until its `ctx` is done and then returns `ctx.Err()`. Call the handler from `NewHandler(ctx, ...)` for a `task` event in a goroutine, cancel `ctx`, and assert: the handler returns nil, `BroadcastCallCount()` stays 0 (`Consistently` ≥ 100 ms), and `RefreshArgsForCall(0)` received that ctx. (A handler running the real index's build does not stop on cancel by design — prompt 1 R5 — which is why this test uses the fake.)
8. **Rebuild fails, frame still sent** — the storage fake returns an error for A's tasks rebuild after a successful warm-up: the `task` event's frame is broadcast exactly once, and `ListPages` for that key still returns the warm-up snapshot (spec failure-mode row "rebuild errors").

Also add a Ginkgo suite in `pkg/watchrefresh` (external package `watchrefresh_test`, suite file shaped like `pkg/statuscache/statuscache_suite_test.go`) with a `DescribeTable` for `EventKey` covering task, goal, theme, objective and unknown vault, plus handler tests using the `pkg/pageindex/mocks` `PageIndex` fake: a task event calls `Refresh` once with the event's key before `Broadcast` (record call order), and a theme or unknown-vault event broadcasts with 0 `Refresh` calls. `pkg/watchrefresh` needs ≥ 80% statement coverage. These tests import both `pkg/pageindex/mocks` and `pkg/websocket/mocks` — alias both. Also name `pkg/watchrefresh` as the event side in `docs/page-index.md` § Key derivation.

### 6. CHANGELOG

Under `## Unreleased` add a `feat:` bullet: watcher task/goal events now refresh only the affected folder of the page index, and their WebSocket frames are sent after the refreshed snapshot is swapped in; theme/objective frames unchanged.

### 7. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk AC2, AC4 (a)(b)(c) and AC7 and name the test or command output establishing each. If `ws-parity` reports a divergent frame or order, find the cause in the new ordering (e.g. a frame dropped or delayed past the 1.5 s settle window); never weaken `scripts/parity/`.

</requirements>

<constraints>
- Response bodies, status codes, routes, query parameters, and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set is unchanged — do not modify `buildWatchTargets`.
- The index key must be derived the same way on the read side (`board.Vault.Path` + folder) and the event side (watcher vault name → configured vault + `GetTasksDir()`/`GetGoalsDir()`); a mismatch silently disables refresh.
- Each task/goal event's frame is broadcast only after a rebuild that started after that event has been swapped in; no cross-key ordering guarantee beyond today's. Theme/objective events are broadcast as today.
- vault-cli `ops.ListOperation` stays the only list logic; vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli subprocess is spawned for watching.
- Snapshots are never mutated after publication; `go test -race` must stay clean.
- No new HTTP route, query parameter, opt-out flag or configurable interval.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests.
- Do NOT modify anything under `src/vault_ui/` or weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`.
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
go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/factory/... ./pkg/websocket/...
```
Must pass.

```
make parity
```
Must exit 0 with `ws-parity: frames identical (<N> frames)` and every other parity line at full ratio. Run `make precommit` first so the container has a Linux `.venv`. An `Error 127` in `build_vault_cli` is an environment failure, not a pass.

```
! grep -n 'ops.NewWatchOperation()' pkg/factory/api.go && grep -n 'ops.NewWatchOperation()' main.go
```
Must exit 0 (the watch operation is injected from `main.go`).

```
! grep -nE 'Refresh\(|NewKey\(|EventKey\(' pkg/factory/api.go
```
Must exit 0 (event logic lives in `pkg/watchrefresh`, not the factory; `buildWatchTargets` legitimately keeps its `GetTasksDir()`/`GetGoalsDir()` calls).

```
go test -race -coverprofile=/tmp/watchrefresh.cover ./pkg/watchrefresh/ && go tool cover -func=/tmp/watchrefresh.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must exit 0 (≥ 80% on the new package).
</verification>
