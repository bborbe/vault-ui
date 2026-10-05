---
spec: ["024-serve-list-reads-from-page-index"]
status: draft
created: "2026-10-05T19:39:53Z"
---

# Mark the page index dirty on every vault-ui write and on cache reload

<summary>
- Any change made through the board — moving a card, flagging, assigning, completing, deferring, starting or taking over a session, setting or clearing a session id — is visible on the very next list read, without waiting for the file watcher.
- The change is recorded as "this vault's task and goal lists are stale" before the request returns and before any live-update message is sent, so a browser reacting to the message never re-fetches old data.
- The stale lists are re-read once, by the first request that needs them; any requests arriving at the same moment share that one re-read.
- The manual cache-reload action now also forces every list to be re-read on next use.
- Read-only actions (jumping to a pane, reloading config) do not trigger any re-read.
- How writes behave is otherwise unchanged; the write side keeps reading files directly, and the parity harness still passes.
- The changelog and the page-index doc describe the finished behaviour.
</summary>

<objective>
Give the mutation service a narrow index-invalidation dependency and call it on every successful (or partially successful) vault write, before any `Publish*Updated` frame and before the method returns, plus a mark-everything call in `ReloadCache`, so vault-ui guarantees read-your-writes while list reads are served from the in-memory page index.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/024-serve-list-reads-from-page-index.md`. This prompt covers Desired Behavior 5 and Acceptance Criteria AC5, AC7 and AC8.

Prerequisites from earlier prompts — read the real code first:
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey(vaultPath, pagesDir string) Key`, `PageIndex` with `MarkDirty(keys ...Key)` (no storage call; the next read of a dirty key performs one shared rebuild) and `MarkAllDirty()`.
- `pkg/factory/pageindex.go` — `CreatePageIndex(pageStorage storage.PageStorage, currentDateTimeGetter libtime.CurrentDateTimeGetter) pageindex.PageIndex`, `CreatePageIndexWarmup(loader, configPath, pageIndex) run.Func`.
- `pkg/factory/api.go` — `CreateAPIHandler(..., manager websocket.ConnectionManager, pageIndex pageindex.PageIndex) http.Handler` (trailing `pageIndex` added by prompt 2), which calls `CreateMutationService(loader, configPath, cache, launches, sessionlock.NewRegistry(), homeDir, connectionEventPublisher{manager: manager})`.
- `docs/page-index.md` — § Staleness bounds; update it if what you build differs.

Current mutation code (verified):
- `pkg/mutations/mutations.go` — `EventPublisher` interface (`PublishTaskUpdated(ctx, vault, taskID string)`, `PublishGoalUpdated(ctx, vault, goalID string)`); `Deps` struct (`Config, Ops, Cache, Launch, Locks, Publisher, Clock, Scanner, Signaler, Pane, Jump, HomeDir, WatcherNames`); `Service` interface; `service.vaultByName(ctx, name) (vaultconfig.Vault, bool, error)` resolves the vault (fields `Path`, `TasksFolder`, `GoalsFolder`).
- `pkg/mutations/tasks.go` — vault-writing methods: `RunTask`, `TakeOverTask` (writes via `clearStartingMarker` / `bindSessionID`), `ExecuteTaskCommand` (work-on path and `taskFastPath`, which publishes), `AssignTaskToMe`, `UpdateTaskPhase`, `UpdateTaskFlag`, `UpdateTaskStatus`, `ClearTaskSession`, `SetTaskSession`. `JumpTask` writes nothing.
- `pkg/mutations/goals.go` — vault-writing methods: `RunGoal`, `TakeOverGoal`, `UpdateGoalStatus`, `ExecuteGoalCommand`, `AssignGoalToMe`, `ClearGoalSession`. Note `GoalComplete` also writes tasks, so a goal write must dirty the tasks key too.
- `pkg/mutations/maintenance.go` — `ReloadCache(ctx, vault string)` (resolves one vault or all, calls `Cache.LoadVault`); `ReloadConfig` writes nothing.
- Several write paths write and then fail (e.g. `RunTask` sets `claude_session_started`, then `WorkOn` fails and it clears the marker), so a mark must cover error returns that follow a write, not only the success return.
- `pkg/mutations/mocks/event_publisher.go` — generated mock with the repo copyright header; `pkg/mutations/service_test.go` — `newHarnessWith` and two more `mutations.New(mutations.Deps{...})` constructions (the 429 launch-cap test and the faked-starter `BeforeEach` near the end of the file), `opsFactoryWithStarter`.
- `pkg/factory/mutations.go` — `CreateMutationService(loader config.Loader, configPath string, cache statuscache.Cache, launches launchregistry.Registry, locks sessionlock.Registry, homeDir string, publisher mutations.EventPublisher) mutations.Service`.
- `pkg/handler/router.go` — routes: `PATCH /api/tasks/{task_id}/phase` (body `{"phase": ...}`), `PATCH /api/tasks/{task_id}/session` (body `{"claude_session_id": ...}`, `SetTaskSession`, non-publishing), `POST /api/cache/reload`.
- vault-cli mocks `github.com/bborbe/vault-cli/mocks`: `mocks.PageStorage`, `mocks.Loader`.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md` — consumer-side narrow interfaces.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. Narrow invalidation dependency (`pkg/mutations/mutations.go`)

Add, next to `EventPublisher`:

```go
// IndexInvalidator marks the read-side page index stale after a vault-ui write,
// so the next list read re-reads the affected folders. It deliberately exposes
// no read method: mutations keep reading vault files directly.
//
//counterfeiter:generate -o ./mocks/index_invalidator.go --fake-name IndexInvalidator . IndexInvalidator
type IndexInvalidator interface {
	MarkDirty(keys ...pageindex.Key)
	MarkAllDirty()
}
```

Add `Index IndexInvalidator` to `Deps`. `pageindex.PageIndex` satisfies it. Generate `pkg/mutations/mocks/index_invalidator.go` (from `pkg/mutations`: `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; prepend the copyright header used by `pkg/mutations/mocks/event_publisher.go`; do not add counterfeiter to `go.mod`; if the proxy is unreachable, hand-write it in counterfeiter's exact shape). If `-generate` also rewrites `event_publisher.go`, keep it equivalent.

Add an unexported helper on `service`:
```go
// markVaultDirty marks the vault's tasks and goals keys dirty in the page index.
func (s *service) markVaultDirty(vault vaultconfig.Vault)
```
It calls `s.deps.Index.MarkDirty(pageindex.NewKey(vault.Path, vault.TasksFolder), pageindex.NewKey(vault.Path, vault.GoalsFolder))` — the same derivation the board uses on the read side.

### 2. Call sites

- In every vault-writing method listed in `<context>` (the nine task methods excluding `JumpTask`, and the six goal methods), register `defer s.markVaultDirty(resolved)` immediately after the vault has been resolved successfully (the point where `resolved` is known and before the first vault write op). Every return path that can follow a write — success or error — then marks before the method returns to the handler. Marking on an early error return that wrote nothing is harmless (one extra rebuild) and acceptable.
- Immediately before every `s.deps.Publisher.PublishTaskUpdated(...)` / `PublishGoalUpdated(...)` call, also call `s.markVaultDirty(resolved)` so the frame can never reach a client before the mark (the deferred call runs too late for that). Use the method's actual resolved-vault variable name. Publishing methods therefore mark twice (pre-publish + deferred); keep both — the deferred mark covers error returns after a partial write, and the extra mark costs at most one extra rebuild. Add a one-line comment saying so.
- `ReloadCache`: call `s.deps.Index.MarkAllDirty()` once the request is resolved (after the config load and, for a named vault, after it was found — not on the 404/config-error paths), before the `Cache.LoadVault` calls. Spec: `/api/cache/reload` marks every key dirty regardless of the `vault` parameter.
- `JumpTask` and `ReloadConfig` make no mark.

Do not change any status code, response body, write, guard, or publish call otherwise.

### 3. Factory wiring

- `CreateMutationService` gains a trailing parameter `index mutations.IndexInvalidator`, set as `Deps.Index`.
- `CreateAPIHandler` passes its `pageIndex` as that argument. No `main.go` change is needed (it already passes `pageIndex` to `CreateAPIHandler`); confirm by reading it.

### 4. Keep existing tests compiling

Every `mutations.New(mutations.Deps{...})` in `pkg/mutations/service_test.go` sets `Index` to a `mocks.IndexInvalidator` (keep it on the harness so tests can assert on it). Existing assertions must not change.

### 5. Mutation-level tests (`pkg/mutations/service_test.go`)

1. Table test over every vault-writing method on a path that writes successfully (reuse the harness; use the faked-starter service for `RunTask`/`RunGoal`; give `TakeOverTask`/`TakeOverGoal` an item whose starting marker is present so the write branch runs — reuse the existing fixtures `taskThreeID`/`taskFourID`/`goalThreeID` in `pkg/mutations/service_test.go`; note `TakeOverGoal`'s starting branch only fires through the status cache, which the harness loads from the tasks folder): after the call, `MarkDirtyCallCount() >= 1` and some call's args equal exactly `[NewKey(dir, "24 Tasks"), NewKey(dir, "23 Goals")]`.
2. Ordering: for `UpdateTaskPhase` and `UpdateGoalStatus`, the publisher fake's `Publish*UpdatedStub` asserts the invalidator's `MarkDirtyCallCount()` is already ≥ 1 when the frame is published.
3. A write that fails after writing (`RunTask` with a starter that errors, after `claude_session_started` was set) still marks.
4. `JumpTask` and `ReloadConfig` → 0 `MarkDirty` and 0 `MarkAllDirty` calls.
5. `ReloadCache` with and without a `vault` → exactly 1 `MarkAllDirty`; with an unknown vault (404) → 0.

### 6. AC5 end-to-end through the factory (Ginkgo, `pkg/factory`)

Build the handler with `factory.CreateAPIHandler(..., pageIndex)` over a real temp vault (tasks + goals folders with real files, `apiFixture`-style loader/config), where `pageIndex := factory.CreatePageIndex(fake, libtime.NewCurrentDateTime())` and `fake` is a `vault-cli mocks.PageStorage` whose `ListPagesStub` delegates to `storage.NewPageStorage(nil).ListPages` and counts calls per `(vaultPath, pagesDir)`. Warm the index first (one `GET /api/tasks` or `CreatePageIndexWarmup`). No watcher runs in these tests, so only dirty marks can refresh the index.

- **(i) publishing write** — `PATCH /api/tasks/<id>/phase?vault=<name>` with a new phase → 200; record the tasks-key call count; one `GET /api/tasks` returns the task with the new phase and caused exactly 1 new call for the tasks key. Repeat with a second write, then two concurrent `GET /api/tasks` → exactly 1 new call between them, both bodies showing the new value.
- **(ii) non-publishing write** — `PATCH /api/tasks/<id>/session?vault=<name>` with `{"claude_session_id": "<uuid>"}` (`SetTaskSession`, which publishes nothing) → 200; the next `GET /api/tasks` shows that `claude_session_id` and caused exactly 1 new call for the tasks key; two concurrent reads after a further session write → exactly 1 call. (`RunTask` and take-over are covered at the mutation level in step 5: through the factory, `RunTask` would spawn the real configured claude script, and take-over's visible effect lives in the status cache, not in the list.)
- **(iii) cache reload** — edit a task file's `priority` (a page-sourced field, not a status-cache field) directly on disk; `GET /api/tasks` still shows the old value (proves the index serves from memory); `POST /api/cache/reload` → 200; the next `GET /api/tasks` shows the new value.

### 7. CHANGELOG and docs

- Under `## Unreleased` add a `feat:` bullet: every vault-ui write and `POST /api/cache/reload` mark the page index dirty so the next list read reflects the write. Make sure the bullet prompt 2 added (serving list reads from the in-memory page index) is still under `## Unreleased`; add it if missing. Remove any "not yet wired into the service" wording left by prompt 1's bullet, since the index is now fully wired.
- Ensure `docs/page-index.md` § Staleness bounds states the read-your-writes rule as built (which writes mark, when, and that reload marks every key).

### 8. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk AC5 (i)(ii)(iii), AC7 and AC8 and name the test or command output establishing each.

</requirements>

<constraints>
- No change to write semantics or ordering, and no write queue — writes only gain an index dirty-mark.
- Every successful vault-ui write — publishing mutations, the non-publishing `Run*`/`TakeOver*`/session writes, and `POST /api/cache/reload` — marks the affected keys dirty before it returns and before any `Publish*Updated` frame. `/api/cache/reload` marks every key dirty.
- `pkg/mutations` reads and `pkg/cleanup` keep reading disk directly; the mutation service must not read through the index.
- Response bodies, status codes, routes, query parameters, and WebSocket frame content are frozen (spec 023 parity contract).
- The index key must be derived the same way on the read side (`Path` + `TasksFolder`/`GoalsFolder`) and the write side.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli subprocess.
- `go test -race` must stay clean.
- No new HTTP route, query parameter, opt-out flag, or configurable interval.
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
Must exit 0 (includes `go test -race ./...`).

```
go test -race ./pkg/pageindex/... ./pkg/board/... ./pkg/factory/... ./pkg/mutations/...
```
Must pass.

```
make parity
```
Must exit 0 with every parity line at full ratio (mutation-parity included). Run `make precommit` first so the container has a Linux `.venv`. An `Error 127` in `build_vault_cli` is an environment failure, not a pass.

```
test "$(awk '/^## /{sec=$0} /page index/{print sec}' CHANGELOG.md | sort -u)" = "## Unreleased"
```
Must exit 0 (every page-index bullet sits under `## Unreleased`, and at least one exists).

```
grep -q '^## Staleness bounds' docs/page-index.md && grep -q '^## Frame ordering' docs/page-index.md && grep -q '^## Key derivation' docs/page-index.md
```
Must exit 0.

```
! grep -n 'NewPageStorage' pkg/factory/api.go
```
Must exit 0.
</verification>
