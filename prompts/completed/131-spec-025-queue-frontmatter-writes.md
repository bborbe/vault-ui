---
status: completed
spec: [025-optimistic-writes-through-a-per-vault-queue]
summary: 'Routed the nine frontmatter-writing routes through the per-vault queue: synchronous validation, 202 with today''s typed body, post-write side-effects moved into the queue consumer, a new write_failed frame on failure, plus router-level AC1/AC3/AC4 tests and a CHANGELOG Unreleased section.'
execution_id: vault-ui-write-queue-exec-131-spec-025-queue-frontmatter-writes
dark-factory-version: dev
created: "2026-10-06T06:49:18Z"
queued: "2026-10-06T07:16:00Z"
started: "2026-10-06T07:53:33Z"
completed: "2026-10-06T08:12:19Z"
branch: dark-factory/131-spec-025-queue-frontmatter-writes
---

# Route the nine frontmatter writes through the per-vault queue and answer 202

<summary>
- Moving a card, changing a status, toggling a flag, assigning, and setting or clearing a session now answer the browser immediately, before the vault file is touched.
- The answer carries the same body as today with the requested value in it; only the success status changes from 200 to 202 (Accepted).
- Bad input is still refused up front with exactly today's error responses (unknown vault, missing reason, dash-prefixed id, unknown task on assign, session-overwrite conflict, malformed body).
- A write the vault itself rejects (for example clearing the session of a task that does not exist) no longer answers with an error status; it answers 202 like any other write, and the failure arrives as the "write failed" message.
- The vault write then runs in that vault's background line, one write at a time, in the order the writes arrived.
- The "this item changed" live-update message, the page-index refresh mark and the status-cache refresh now happen only after the file has really been written — never before.
- A write that fails sends a new "write failed" live-update message naming the item, the vault and the reason, and does nothing else.
- Starting, taking over, running a lifecycle command, jumping, and the two reload actions behave exactly as before.
- The changelog gains an Unreleased section describing optimistic writes.
</summary>

<objective>
Make the nine frontmatter-writing routes validate synchronously, enqueue one write on the per-vault queue from prompt 1, and answer 202 with today's typed body; move each route's post-write side-effects (status-cache invalidate → page-index dirty mark → `task_updated`/`goal_updated` frame) into the queue consumer so they run only after the file is written; publish a new `write_failed` frame (item id, kind, vault, reason) on failure with none of the success side-effects. Wire the queue through the factory and `main.go`, prove AC1/AC3/AC4 with router-level tests, and create the CHANGELOG `## Unreleased` section.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-optimistic-writes-through-a-per-vault-queue.md`. This prompt covers Desired Behaviors 2, 4 and 5 and Acceptance Criteria AC1, AC3, AC4 and the changelog half of AC8.

Prerequisite from prompt 1 — read the real code first: `pkg/queue/queue.go` — `queue.Write{Vault string; ItemID string; Apply run.Func}`, `queue.Queue` with `Enqueue(ctx context.Context, write Write) error`, `Consume(ctx context.Context) error`, `Done(vault string) <-chan struct{}`, `queue.NewQueue() Queue`, `queue.ErrClosed`.

Current code (verified — read each file before editing):
- `pkg/handler/router.go` — `CreateHTTPRouter(b board.Board, m mutations.Service, staticFS fs.FS, readiness vaultui.Readiness, manager websocket.ConnectionManager) http.Handler`.
- `pkg/handler/api_mutations.go` — one `New*Handler(m mutations.Service) http.Handler` per route, each ending in `writeJSON(resp, http.StatusOK, result)`; `requireVault`, `decodeBody`, `validStatus`/`writeStatusEnumError` (the 422 paths), `writeMutationError`.
- `pkg/mutations/mutations.go` — `EventPublisher` (`PublishTaskUpdated(ctx, vault, taskID string)`, `PublishGoalUpdated(ctx, vault, goalID string)`, no counterfeiter directive yet although `mocks/event_publisher.go` exists), `IndexInvalidator` (with `//counterfeiter:generate -o ./mocks/index_invalidator.go --fake-name IndexInvalidator . IndexInvalidator`), `Deps`, `service`, `markVaultDirty(vault vaultconfig.Vault)`, `vaultByName`, `opsForVault`, `newHTTPError`, `requireSafeID`, `closeoutArgs`, `HTTPError{Status int; Detail string}`.
- `pkg/mutations/tasks.go` — `AssignTaskToMe`, `UpdateTaskPhase`, `UpdateTaskFlag`, `UpdateTaskStatus`, `ClearTaskSession`, `SetTaskSession` (queued by this prompt) and `RunTask`, `JumpTask`, `TakeOverTask`, `ExecuteTaskCommand`/`taskFastPath` (unchanged by this prompt).
- `pkg/mutations/goals.go` — `UpdateGoalStatus`, `AssignGoalToMe`, `ClearGoalSession` (queued) and `RunGoal`, `TakeOverGoal`, `ExecuteGoalCommand` (unchanged).
- `pkg/websocket/frames.go` — `TaskUpdatedFrame(vault, taskID string) []byte`, `GoalUpdatedFrame(vault, goalID string) []byte`, the unexported `watcherFrame` (whose id key is `task_id` for every item kind), `marshal`.
- `pkg/websocket/connection_manager.go` — `ConnectionManager.Broadcast(payload []byte)`; counterfeiter fake `pkg/websocket/mocks.WebsocketConnectionManager` (`BroadcastCallCount`, `BroadcastArgsForCall(i) []byte`).
- `pkg/factory/mutations.go` — `connectionEventPublisher{manager}` (the only `EventPublisher` implementation) and `CreateMutationService(loader config.Loader, configPath string, cache statuscache.Cache, launches launchregistry.Registry, locks sessionlock.Registry, homeDir string, publisher mutations.EventPublisher, index mutations.IndexInvalidator) mutations.Service`.
- `pkg/factory/api.go` — `CreateAPIHandler(loader, configPath, cache, paneCache, launches, homeDir, readiness, manager, pageIndex pageindex.PageIndex) http.Handler`, which calls `CreateMutationService(..., connectionEventPublisher{manager: manager}, pageIndex)`.
- `main.go` — builds the shared dependencies, calls `factory.CreateAPIHandler(...)`, and runs everything under `run.CancelOnFirstErrorWait(ctx, ...)`.
- Tests that will need updating: `pkg/mutations/service_test.go` (harness `newHarnessWith`, `newStarterHarnessWith`; describes for the nine methods; "Mutation service guard branches"; "Mutation service remaining branches"; "Mutation service page-index invalidation"), `pkg/handler/api_mutations_test.go` ("writes the flag body", "writes the assign-to-me body", "writes the session-clear body", and the `DescribeTable("200s the mutating routes", ...)` entries), `pkg/factory/api_test.go` (`newTestAPIHandlerWithManager`, "mutating-route publisher wiring"), `pkg/factory/pageindex_test.go` (`indexHandler`), `pkg/factory/mutations_index_test.go` (`newAC5Fixture` and the write-invalidation specs).
- `scripts/parity/mutations.txt` — the parity cases `unknown-task` (assign-to-me of a missing task → 404) and `set-task-session-conflict` (→ 409) compare bodies; their non-2xx bodies must stay byte-identical, which is why the two existing pre-write reads stay on the request path (requirement 4).
- `CHANGELOG.md` — top heading is `## v0.81.0`; there is NO `## Unreleased` section.

Verified vault-cli v0.159.0 APIs used by the new tests (module `github.com/bborbe/vault-cli`):
- `ops.FrontmatterSetOperation.Execute(ctx, vaultPath, taskName, key, value, reason, gateSuccessor, actor string, force bool) error`
- `ops.FrontmatterClearOperation.Execute(ctx, vaultPath, taskName, key string) error`
- `ops.EntitySetOperation.Execute(ctx, vaultPath, entityName, key, value, reason, gateSuccessor string) error`
- `ops.EntityClearOperation.Execute(ctx, vaultPath, entityName, key string) error`
- `ops.ShowOperation.Execute(ctx, vaultPath, vaultName, taskName string) (ops.TaskDetail, error)`; `ops.TaskDetail` has `Name`, `Status`, `Phase`, `ClaudeSessionID`.
- `ops.NewListOperation(storage.PageStorage)`; `Execute(ctx, vaultPath, vaultName, dir, nil, true, "", "") ([]ops.TaskListItem, error)`; `ops.TaskListItem` has `Name`, `Phase`, `Status`.
- Fakes in `github.com/bborbe/vault-cli/mocks` (import as `vcmocks`): `FrontmatterSetOperation`, `FrontmatterClearOperation`, `EntitySetOperation`, `EntityClearOperation`, `ShowOperation`.

Verified library APIs: `run.Func` / `run.CatchPanic(fn run.Func) run.Func` (`github.com/bborbe/run@v1.11.0`, converts a panic into an `errors.Errorf(ctx, "catch panic: %v", r)` error); `errors.Wrapf`, `errors.Errorf`, `errors.As` (`github.com/bborbe/errors@v1.6.1`); `sessionlock.Registry.Lock(ctx, vault, itemID string) (release func(), err error)`.

Coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. The `write_failed` frame and an exported publisher — `pkg/websocket`

1. In `pkg/websocket/frames.go` add:

   ```go
   // writeFailedFrame is broadcast when a queued vault write fails. Like the
   // watcher frame, the identifier key is task_id for every item kind.
   type writeFailedFrame struct {
   	Type     string `json:"type"`
   	TaskID   string `json:"task_id"`
   	ItemKind string `json:"item_kind"`
   	Vault    string `json:"vault"`
   	Reason   string `json:"reason"`
   }

   // WriteFailedFrame builds the frame for a failed queued write. itemKind is
   // "task" or "goal".
   func WriteFailedFrame(vault, itemKind, itemID, reason string) []byte {
   	return marshal(writeFailedFrame{
   		Type:     "write_failed",
   		TaskID:   itemID,
   		ItemKind: itemKind,
   		Vault:    vault,
   		Reason:   reason,
   	})
   }
   ```

   Do not change `TaskUpdatedFrame`, `GoalUpdatedFrame` or `WatcherFrame`.

2. Create `pkg/websocket/publisher.go` holding the publisher that today lives in `pkg/factory/mutations.go` as `connectionEventPublisher`, now exported so router-level tests can wire a real publisher over a fake manager:

   ```go
   // MutationPublisher announces vault-ui write outcomes to every connected
   // client. It satisfies mutations.EventPublisher structurally.
   type MutationPublisher interface {
   	PublishTaskUpdated(ctx context.Context, vault, taskID string)
   	PublishGoalUpdated(ctx context.Context, vault, goalID string)
   	PublishWriteFailed(ctx context.Context, vault, itemKind, itemID, reason string)
   }

   // NewMutationPublisher returns a MutationPublisher broadcasting through manager.
   func NewMutationPublisher(manager ConnectionManager) MutationPublisher
   ```

   The three methods call `manager.Broadcast(TaskUpdatedFrame(vault, taskID))`, `manager.Broadcast(GoalUpdatedFrame(vault, goalID))` and `manager.Broadcast(WriteFailedFrame(vault, itemKind, itemID, reason))` respectively. Delete `connectionEventPublisher` from `pkg/factory/mutations.go` and use `websocket.NewMutationPublisher(manager)` in its place (requirement 6).

3. Add tests in `pkg/websocket/frames_test.go` (and a new `publisher_test.go`) asserting the exact JSON of `WriteFailedFrame("personal", "task", "Task A", "permission denied")` — `{"type":"write_failed","task_id":"Task A","item_kind":"task","vault":"personal","reason":"permission denied"}` — and that each publisher method broadcasts exactly the corresponding frame bytes (use `mocks.WebsocketConnectionManager`).

### 2. Mutations dependencies — `pkg/mutations/mutations.go`

Update the package doc comment (its opening lines say the service mirrors the Python handlers' "status codes, bodies, guards"): the nine queued routes answer 202 (spec 025); every other status code, body and guard still mirrors the Python handlers.

1. Extend `EventPublisher` with `PublishWriteFailed(ctx context.Context, vault, itemKind, itemID, reason string)` and add the directive `//counterfeiter:generate -o ./mocks/event_publisher.go --fake-name EventPublisher . EventPublisher` above it. Update its doc comment (it currently says a no-op is wired — it is not).

2. Add the narrow consumer-side queue interface:

   ```go
   // WriteQueue accepts one vault write for asynchronous, per-vault FIFO
   // application. queue.Queue satisfies it.
   //
   //counterfeiter:generate -o ./mocks/write_queue.go --fake-name WriteQueue . WriteQueue
   type WriteQueue interface {
   	Enqueue(ctx context.Context, write queue.Write) error
   }
   ```

   and a field `Queue WriteQueue` on `Deps`.

3. Regenerate both mocks (`pkg/mutations/mocks/event_publisher.go`, new `pkg/mutations/mocks/write_queue.go`): from `pkg/mutations` run `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; prepend the copyright header used by the existing files in `pkg/mutations/mocks/`; do not add counterfeiter to `go.mod`; if the proxy is unreachable, hand-write them in counterfeiter's exact shape. If `-generate` also rewrites `index_invalidator.go`, keep it equivalent.

4. Add the enqueue helper and the success side-effect builders to `pkg/mutations/mutations.go` (adapt variable names freely, keep the behaviour exactly):

   ```go
   // enqueueWrite queues one frontmatter write on the vault's FIFO. write runs
   // the vault-cli calls; onSuccess runs the route's post-write side-effects.
   // Both run on the vault's queue consumer with the consumer's context — never
   // on the request goroutine and never with the request context. A failed (or
   // panicking) write publishes a write_failed frame and runs none of onSuccess.
   func (s *service) enqueueWrite(
   	ctx context.Context,
   	vault vaultconfig.Vault,
   	itemKind, itemID string,
   	write run.Func,
   	onSuccess func(ctx context.Context),
   ) error {
   	if err := s.deps.Queue.Enqueue(ctx, queue.Write{
   		Vault:  vault.Name,
   		ItemID: itemID,
   		Apply: func(ctx context.Context) error {
   			if err := run.CatchPanic(write)(ctx); err != nil {
   				s.deps.Publisher.PublishWriteFailed(ctx, vault.Name, itemKind, itemID, failureReason(err))
   				return errors.Wrapf(ctx, err, "apply %s write %s in vault %s", itemKind, itemID, vault.Name)
   			}
   			onSuccess(ctx)
   			return nil
   		},
   	}); err != nil {
   		return errors.Wrapf(ctx, err, "enqueue %s write %s in vault %s", itemKind, itemID, vault.Name)
   	}
   	return nil
   }

   // failureReason is the human-readable reason a write_failed frame carries:
   // an HTTPError's Detail (the text today's synchronous route would have
   // answered), otherwise the error text.
   func failureReason(err error) string {
   	var httpErr *HTTPError
   	if errors.As(err, &httpErr) {
   		return httpErr.Detail
   	}
   	return err.Error()
   }

   // taskWritten is the post-write side-effect of a publishing task route, in
   // today's order: status cache, page-index dirty mark, then the frame.
   func (s *service) taskWritten(vault vaultconfig.Vault, taskID string) func(context.Context) {
   	return func(ctx context.Context) {
   		s.deps.Cache.Invalidate(vault.Name, taskID)
   		s.markVaultDirty(vault)
   		s.deps.Publisher.PublishTaskUpdated(ctx, vault.Name, taskID)
   	}
   }

   // goalWritten is taskWritten for a publishing goal route.
   func (s *service) goalWritten(vault vaultconfig.Vault, goalID string) func(context.Context) { /* Invalidate, markVaultDirty, PublishGoalUpdated */ }

   // itemWrittenSilently is the post-write side-effect of the task session
   // routes, which publish no frame today: status cache, then dirty mark.
   func (s *service) itemWrittenSilently(vault vaultconfig.Vault, itemID string) func(context.Context) { /* Invalidate, markVaultDirty */ }
   ```

   `queue` is `github.com/bborbe/vault-ui/pkg/queue`; `run` is `github.com/bborbe/run`. An `Enqueue` error is returned from the route method as a plain wrapped error, which `writeMutationError` already maps to a 500 with the error text.

### 3. The nine queued routes — `pkg/mutations/tasks.go`, `pkg/mutations/goals.go`

For each method below: keep every check listed under "request path" exactly as it is today (same status code, same detail text, same order); remove the method's `defer s.markVaultDirty(resolved)` and its inline `Cache.Invalidate` / `markVaultDirty` / `Publish*Updated` calls (they move into `onSuccess`); move every vault-cli **write** — and every read that is part of the write — into the `write` closure; call `s.enqueueWrite(ctx, resolved, kind, id, write, onSuccess)`; return today's typed body built from the request (the requested value). Inside the closure call `s.opsForVault(resolved)` and use the closure's `ctx` parameter only. The closure returns `newHTTPError(<today's status>, <today's detail>)` exactly where today's code returns one, so `failureReason` carries today's text.

| Method | Request path (synchronous, unchanged) | Queued `write` closure | `onSuccess` | Body returned |
|---|---|---|---|---|
| `UpdateTaskPhase` | `vaultByName` (400 unknown vault) | `FrontmatterSet` `phase`; derive `newStatus` exactly as today (`done` → `completed`; else `Show` and skip when current status is `hold`, otherwise `in_progress`); `FrontmatterSet` `status` when non-empty | `taskWritten` | `PhaseUpdateResponse{Status: "success", TaskID: taskID, Phase: req.Phase}` |
| `UpdateTaskFlag` | `vaultByName` (404); `flag` default true when `req.Flag == nil` | today's set (`"flag", "true", "", "", "operator", false`, guarded by `selftestSkipFlagWrite()`) or `FrontmatterClear` `flag` | `taskWritten` | `FlagUpdateResponse{Status: "success", TaskID: taskID, Flag: flag}` |
| `UpdateTaskStatus` | `requireSafeID`; `closeoutArgs` for aborted/completed; `vaultByName` (400) | `FrontmatterSet` `status` with `reason`, `gate` | `taskWritten` | `StatusUpdateResponse{Status: "success", TaskID: taskID, NewStatus: req.Status}` |
| `AssignTaskToMe` | `Config.Load`; empty `CurrentUser` (400); `vaultByName` (404); **`Show` existence check (404 `taskNotFound`) stays here** | `FrontmatterSet` `assignee` = `cfg.CurrentUser` | `taskWritten` | `AssignResponse{Status: "success", TaskID: taskID, Assignee: cfg.CurrentUser}` |
| `SetTaskSession` | `vaultByName` (404); display-name resolution via `sessionresolver` (not a vault-cli call); **`Show` (500 on error) and the different-UUID conflict check (409, today's detail text) stay here, without the lock** | `Locks.Lock(ctx, resolved.Name, taskID)` with the consumer ctx (500 on error), `defer release()`; `Show` again and re-run the conflict check (409 → write_failed); `FrontmatterSet` `claude_session_id` | `itemWrittenSilently` | `SessionSetResponse{Status: "success", TaskID: taskID, ClaudeSessionID: storedValue}` |
| `ClearTaskSession` | `vaultByName` (500) | `FrontmatterClear` `claude_session_id` (error → `newHTTPError(404, taskNotFound(taskID))`); `FrontmatterClear` `claude_session_started` (error → 500) | `itemWrittenSilently` | `SessionClearResponse{Status: "success", TaskID: taskID}` |
| `UpdateGoalStatus` | `requireSafeID`; `closeoutArgs`; `vaultByName` (400) | `GoalSet` `status` with `reason`, `gate` | `goalWritten` | `StatusUpdateResponse{Status: "success", GoalID: goalID, NewStatus: req.Status}` |
| `AssignGoalToMe` | `requireSafeID`; `Config.Load`; empty `CurrentUser` (400); `vaultByName` (404) | `GoalSet` `assignee` | `goalWritten` | `AssignResponse{Status: "success", GoalID: goalID, Assignee: cfg.CurrentUser}` |
| `ClearGoalSession` | `requireSafeID`; `vaultByName` (400) | `GoalClear` `claude_session_id` (500); best-effort `GoalClear` `claude_session_started` (error ignored, as today, with today's comment) | `goalWritten` | `SessionClearResponse{Status: "success", GoalID: goalID}` |

Notes:
- The two pre-write reads kept on the request path (`AssignTaskToMe`'s existence check and `SetTaskSession`'s conflict check) exist so today's 404/409 bodies stay byte-identical (parity cases `unknown-task` and `set-task-session-conflict`). No vault-cli **write**, no `Cache.Invalidate`, no `MarkDirty` and no frame happens on any request path. Add a code comment at each kept read saying so.
- `kind` is `"task"` for the six task methods and `"goal"` for the three goal methods.
- On failure: no `Cache.Invalidate`, no `MarkDirty`, no `Publish*Updated` — only `PublishWriteFailed`. A write that fails after a partial file change is picked up by the file watcher, which refreshes the index on its own (state this in a comment in `enqueueWrite`).
- Do not touch `RunTask`, `JumpTask`, `TakeOverTask`, `ExecuteTaskCommand`, `taskFastPath`, `RunGoal`, `TakeOverGoal`, `ExecuteGoalCommand`, `ReloadCache`, `ReloadConfig`, `clearStartingMarker`, `bindSessionID`: they keep their synchronous behaviour, their `defer s.markVaultDirty(resolved)` and their inline publishes.
- The queue is keyed by `resolved.Name` (a configured vault), never by raw request input.

### 4. Handlers — `pkg/handler/api_mutations.go`

In exactly these nine handlers replace `writeJSON(resp, http.StatusOK, result)` with `writeJSON(resp, http.StatusAccepted, result)`: `NewAssignTaskHandler`, `NewTaskPhaseHandler`, `NewTaskFlagHandler`, `NewTaskStatusHandler`, `NewGoalStatusHandler`, `NewAssignGoalHandler`, `NewClearTaskSessionHandler`, `NewClearGoalSessionHandler`, `NewSetTaskSessionHandler`. Add a one-line doc note on each that the write is queued and the 202 body carries the requested value. Leave every other handler (run, jump, take-over, both execute-command routes, cache reload, config reload) byte-for-byte unchanged. Validation (422 for missing `vault`, malformed JSON, out-of-enum status) is unchanged and still happens before the service is called.

### 5. Wiring — `pkg/factory`, `main.go`

1. `pkg/factory/mutations.go`: delete `connectionEventPublisher`; add a trailing parameter `writeQueue mutations.WriteQueue` to `CreateMutationService` and set `Queue: writeQueue` in `mutations.Deps`. No other logic.
2. `pkg/factory/api.go`: add a trailing parameter `writeQueue queue.Queue` to `CreateAPIHandler` and pass `websocket.NewMutationPublisher(manager), pageIndex, writeQueue` to `CreateMutationService`. Add:

   ```go
   // CreateWriteQueue returns the process-wide per-vault write queue. Its
   // Consume must run in main's run group.
   func CreateWriteQueue() queue.Queue {
   	return queue.NewQueue()
   }
   ```

3. `main.go`: `writeQueue := factory.CreateWriteQueue()` next to the other shared dependencies; pass it as the new trailing argument of `factory.CreateAPIHandler`; add `writeQueue.Consume` to the `run.CancelOnFirstErrorWait(ctx, ...)` list (before `factory.CreateAPIServer(...)`).
4. Update every other `CreateAPIHandler` call site — `pkg/factory/api_test.go` (`newTestAPIHandlerWithManager`) and `pkg/factory/pageindex_test.go` (`indexHandler`, which gains a trailing `writeQueue queue.Queue` parameter that its callers create with `factory.CreateWriteQueue()`, so `newAC5Fixture` holds the queue it drains) — to create a queue with `factory.CreateWriteQueue()`, start `Consume` in a goroutine on a context cancelled via `DeferCleanup` (use `done := make(chan struct{}); go func() { defer GinkgoRecover(); defer close(done); _ = q.Consume(ctx) }(); DeferCleanup(func() { cancel(); <-done })`, so no in-flight write outlives the spec's temp dir), and pass it. Run `grep -rn 'CreateAPIHandler(\|CreateMutationService(' --include='*.go' .` before finishing and confirm every call site compiles with the new signatures.

### 6. Router-level acceptance tests — new `pkg/handler/queued_writes_test.go`

Package `handler_test`. Build the real router with `handler.CreateHTTPRouter(&fakeBoard{}, service, staticFS, readiness, manager)` (with `readiness := vaultui.NewReadiness(); readiness.SetReady()`, as in `newRouter`) (reuse `fakeBoard` from `api_test.go`, an `fstest.MapFS` like `newRouter`), where:
- `manager` is `&wsmocks.WebsocketConnectionManager{}` (`wsmocks "github.com/bborbe/vault-ui/pkg/websocket/mocks"`);
- `service` is `mutations.New(mutations.Deps{...})` with: a static `ConfigProvider` returning two vaults — `personal` (path = a `GinkgoT().TempDir()`, `TasksFolder: "24 Tasks"`, `GoalsFolder: "23 Goals"`) and `work` (its own temp dir) — and `CurrentUser: "tester"`; `Ops` returning an `vaultui.OpSet` built from `vcmocks` fakes (`FrontmatterSet`, `FrontmatterClear`, `Show`, `GoalSet`, `GoalClear`, and `List: ops.NewListOperation(storage.NewPageStorage(nil))`); `Cache` a recording wrapper that embeds `statuscache.NewCache()` and records every `Invalidate(vault, id)` into a shared, mutex-guarded event log; `Index` a `mocks.IndexInvalidator` whose `MarkDirtyStub` appends to the same event log; `Publisher` `websocket.NewMutationPublisher(manager)` with `manager.BroadcastStub` appending to the same event log; `Launch: launchregistry.NewRegistry()`, `Locks: sessionlock.NewRegistry()`, `Clock: libtime.NewCurrentDateTime()`, `HomeDir` a temp dir; `Queue` a real `queue.NewQueue()` whose `Consume` runs in a goroutine cancelled via `DeferCleanup`.
- Every fake write op records `(vault path, item, key)` into a mutex-guarded apply log and, when its key matches the test's block key, first waits on a release channel. `Show` returns `ops.TaskDetail{Name: <id>}` (no session id, status empty).

Specs:

1. **AC1 — 202 before the write.** A `DescribeTable` over the nine routes, each with method, target (`?vault=personal`), JSON body, block key, and expected body:

   | Route | Body sent | Block key (op) | Expected 202 body (compare as JSON) |
   |---|---|---|---|
   | `PATCH /api/tasks/TaskOne/phase` | `{"phase":"execution"}` | `phase` (FrontmatterSet) | `{"status":"success","task_id":"TaskOne","phase":"execution"}` |
   | `PATCH /api/tasks/TaskOne/status` | `{"status":"backlog"}` | `status` (FrontmatterSet) | `{"status":"success","task_id":"TaskOne","new_status":"backlog"}` |
   | `PATCH /api/tasks/TaskOne/flag` | `{}` | `flag` (FrontmatterSet) | `{"status":"success","task_id":"TaskOne","flag":true}` |
   | `PATCH /api/tasks/TaskOne/assign-to-me` | — | `assignee` (FrontmatterSet) | `{"status":"success","task_id":"TaskOne","assignee":"tester"}` |
   | `PATCH /api/tasks/TaskOne/session` | `{"claude_session_id":"33333333-3333-3333-3333-333333333333"}` | `claude_session_id` (FrontmatterSet) | `{"status":"success","task_id":"TaskOne","claude_session_id":"33333333-3333-3333-3333-333333333333"}` |
   | `DELETE /api/tasks/TaskOne/session` | — | `claude_session_id` (FrontmatterClear) | `{"status":"success","task_id":"TaskOne"}` |
   | `PATCH /api/goals/GoalOne/status` | `{"status":"hold"}` | `status` (GoalSet) | `{"status":"success","goal_id":"GoalOne","new_status":"hold"}` |
   | `PATCH /api/goals/GoalOne/assign-to-me` | — | `assignee` (GoalSet) | `{"status":"success","goal_id":"GoalOne","assignee":"tester"}` |
   | `DELETE /api/goals/GoalOne/session` | — | `claude_session_id` (GoalClear) | `{"status":"success","goal_id":"GoalOne"}` |

   Evidence per entry: the response code is `http.StatusAccepted` while the write is still blocked; the apply-log count for the block key is **0** at the moment the response is read; after closing the release channel, `Eventually` the count for the block key is exactly **1** and `Done("personal")` closes; the body equals the expected JSON.

2. **Per-vault ordering through the router.** Block `phase` writes; send `PATCH .../TaskOne/phase` `{"phase":"execution"}` then `{"phase":"done"}` to `personal`; send `PATCH /api/tasks/TaskOne/flag?vault=work` `{}` and assert `Eventually` its `flag` write is applied while the first `personal` write is still blocked; release; the `personal` apply log shows the `execution` phase write before the `done` phase write.

3. **AC3 — frame and dirty mark only after the write.** Block the `phase` write of `PATCH /api/tasks/TaskOne/phase?vault=personal`. While blocked: `manager.BroadcastCallCount()` is **0**, `index.MarkDirtyCallCount()` is **0**, and the cache recorder has **0** invalidations. Release. `Eventually` exactly one broadcast whose JSON equals `{"type":"task_updated","task_id":"TaskOne","item_kind":"task","vault":"personal"}`; `MarkDirtyArgsForCall(0)` equals `[]pageindex.Key{pageindex.NewKey(<personal dir>, "24 Tasks"), pageindex.NewKey(<personal dir>, "23 Goals")}`; the cache recorded `Invalidate("personal", "TaskOne")`; and the shared event log orders them invalidate → mark → broadcast. Add the same check for `PATCH /api/goals/GoalOne/status` expecting `{"type":"goal_updated","goal_id":"GoalOne","item_kind":"goal","vault":"personal"}`.

4. **AC4 — a failed write reverts and says so.** Write a real task file `<personal dir>/24 Tasks/TaskOne.md` with `---\nstatus: in_progress\nphase: planning\n---\n# Task One\n`. Make the `FrontmatterSet` fake return the stdlib `errors.New("permission denied")` (the handler test package already imports stdlib `errors`) for key `phase`. `PATCH /api/tasks/TaskOne/phase?vault=personal` `{"phase":"execution"}` → 202. `Eventually` exactly one broadcast, whose JSON equals `{"type":"write_failed","task_id":"TaskOne","item_kind":"task","vault":"personal","reason":"permission denied"}`; `Consistently` (short window) there are **0** `task_updated` broadcasts, **0** `MarkDirty` calls and **0** cache invalidations; the next list read of the vault — `ops.NewListOperation(storage.NewPageStorage(nil)).Execute(ctx, <personal dir>, "personal", "24 Tasks", nil, true, "", "")` — returns `TaskOne` with `Phase == "planning"`. Add a goal variant (`GoalSet` failing for `status`) expecting `item_kind` `goal`.

5. **A panicking write** (the `FrontmatterSet` fake panics) produces exactly one `write_failed` frame for the item, and a following write to the same vault is still applied.

6. **Validation still answers synchronously and enqueues nothing:** missing `vault` → 422; malformed JSON → 422; out-of-enum status → 422; dash-prefixed task id on `/status` → 400; unknown vault on `/phase` → 400; aborted status without reason → 400; assign-to-me with `Show` failing → 404 with detail `Task not found: TaskOne`; `PATCH .../session` when `Show` returns a different UUID → 409 with the "already holds session" detail. For each, the apply log stays empty and no broadcast happens.

### 7. Update existing tests

- `pkg/mutations/service_test.go`: in `newHarnessWith` and `newStarterHarnessWith`, add a real `queue.NewQueue()` as `Deps.Queue`, start `Consume` in a goroutine on a context cancelled via `DeferCleanup` (use `done := make(chan struct{}); go func() { defer GinkgoRecover(); defer close(done); _ = q.Consume(ctx) }(); DeferCleanup(func() { cancel(); <-done })`, so no in-flight write outlives the spec's temp dir), keep it on the harness (`newStarterHarnessWith` reuses `h.queue` — `Queue: h.queue` — rather than creating a second queue, so `drain()` always watches the queue the service writes to), and add a helper `func (h *harness) drain()` that does `Eventually(h.queue.Done("personal")).Should(BeClosed())`. Wherever a spec calls one of the nine queued methods and then asserts on files, publishes, cache or index marks, call `h.drain()` first. Specs that asserted a write failure as a returned error now assert a `nil` error, the 202 body, and — after `h.drain()` — exactly one `PublishWriteFailed` call with the expected vault, kind, id and reason and zero `Publish*Updated`/`MarkDirty` calls: this applies to "404s update-task-flag for an unknown task" (rename it accordingly) and any `ClearTaskSession`/`UpdateTaskPhase` missing-task case. `SetTaskSession`'s "409s when a different session is already held" and `AssignTaskToMe`'s "404s an unknown task" stay synchronous and unchanged. In "Mutation service page-index invalidation", drain before `expectVaultMarked` for the queued entries. The two "marks before Publish*Updated" specs currently call `ExpectWithOffset` inside the publisher stub; the stub now runs on the consumer goroutine, where a failed `Expect` cannot fail the spec. Instead, record the `MarkDirty` call count inside the stub into a mutex-guarded variable and assert it after `h.drain()`, on the spec goroutine. Add a spec per queued method proving that an `Enqueue` error (use `mocks.WriteQueue` with `EnqueueReturns(errors.New("closed"))`) returns an error from the method and writes nothing.
- `pkg/handler/api_mutations_test.go`: change `http.StatusOK` to `http.StatusAccepted` only in "writes the flag body", "writes the assign-to-me body", "writes the session-clear body", and in the `DescribeTable` entries for the queued routes (task phase, goal status, assign goal, clear goal session, set task session, and any other queued entry present); move the queued entries out into a new `DescribeTable("202s the queued routes", ...)` expecting `http.StatusAccepted`, leaving the original table's description and its run/take-over/execute-command entries byte-identical. Do not edit the entries or specs for run, take-over, execute-command, jump, cache reload or config reload — they are the AC1 negative control and must pass unedited.
- `pkg/factory/api_test.go` "mutating-route publisher wiring": expect `http.StatusAccepted`; the frame assertions stay unchanged (the frame now arrives after the queued write).
- `pkg/factory/mutations_index_test.go`: keep the fixture's queue reachable (`newAC5Fixture` creates it and starts `Consume`), expect `http.StatusAccepted` for the queued writes, and drain the queue (`Eventually(<queue>.Done("personal")).Should(BeClosed())`) after each write and before the next read or call-count assertion. The spec 024 assertions (exactly one rebuild per dirty read, concurrent readers share one rebuild, cache reload) must otherwise stay as they are.

### 8. CHANGELOG

`CHANGELOG.md` has no `## Unreleased` section. Insert one directly above `## v0.81.0` (keep the file's blank-line spacing) with this single bullet, reworded only if the implementation differs:

```
- feat: Apply the board's frontmatter writes optimistically through an in-memory per-vault queue (`pkg/queue`): the nine frontmatter-writing routes (task phase/status/flag/assign-to-me/session set/session clear, goal status/assign-to-me/session clear) validate synchronously, enqueue one write and answer 202 with today's body; each vault's consumer applies its writes one at a time in submission order without blocking other vaults, and only after the file is written invalidates the status cache, marks the page index dirty and publishes the `task_updated`/`goal_updated` frame; a failed write publishes a new `write_failed` frame (`task_id`, `item_kind`, `vault`, `reason`) and nothing else. The process-spawning, jump and reload routes are unchanged.
```

Prompts 3 and 4 of this spec do not edit `CHANGELOG.md`.

### 9. Self-check

Re-run every `<verification>` command and confirm each passes. Walk AC1 (including its negative control), AC3 and AC4 and name the spec that proves each. Confirm by reading the nine methods that none calls a vault-cli write, `Cache.Invalidate`, `markVaultDirty` or `Publish*` outside the queued closure.

</requirements>

<constraints>
- The nine queued routes are exactly: `PATCH /api/tasks/{id}/phase`, `PATCH /api/tasks/{id}/status`, `PATCH /api/tasks/{id}/flag`, `PATCH /api/tasks/{id}/assign-to-me`, `PATCH /api/tasks/{id}/session`, `DELETE /api/tasks/{id}/session`, `PATCH /api/goals/{id}/status`, `PATCH /api/goals/{id}/assign-to-me`, `DELETE /api/goals/{id}/session`.
- The nine routes' **body** shapes and their validation errors are frozen; only the success status changes from 200 to 202. The nine non-queued routes (`POST /api/tasks/{id}/run`, `/take-over`, `/execute-command`, `/jump`; `POST /api/goals/{id}/run`, `/take-over`, `/execute-command`; `POST /api/config/reload`; `POST /api/cache/reload`) are entirely unchanged, status codes included.
- No vault-cli write, no index mark and no frame happens on a queued route's request path (spec DB2: the only vault reads kept on the request path are assign-to-me's existence check and session-set's conflict check); after a write lands the consumer performs exactly the post-write side-effects the route performs today, in the same order (invalidate status cache → mark tasks and goals keys dirty → publish). Nothing is published and nothing is marked dirty before the file has been written.
- A failed write publishes an error frame carrying the item id, the vault and the reason, and performs none of the success side-effects.
- Reads never go through the queue. The page index's staleness bounds, frame-ordering rule and snapshot immutability (`docs/page-index.md`) are unchanged; the queue only moves the existing post-write side-effects from the request path to the consumer.
- `pkg/mutations` keeps its current dependency shape: the queue is injected as a narrow consumer-side interface (`WriteQueue`) and mocked with counterfeiter, like `IndexInvalidator` and `EventPublisher`.
- The queue is keyed by configured vault, never by request input.
- No durable queue, no `libqueue` dependency, no retry of a failed write, no change to vault-cli, no new external service, no new route or query parameter, no opt-out flag, no new metric.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests in external `_test` packages.
- `go test -race` must stay clean.
- Do NOT modify anything under `src/vault_ui/` (prompt 3) or `scripts/parity/` and `docs/` (prompt 4). `make parity` is expected to report mutation status mismatches until prompt 4 amends the harness — do not run or "fix" it here.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`. Do NOT run `go mod vendor`.
- Do NOT commit and do NOT run any git command — dark-factory handles git.
- Existing tests must still pass (updated only where requirement 7 says so).
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0 (includes `go test -race ./...`).

```
go test -race ./pkg/queue/... ./pkg/mutations/... ./pkg/handler/... ./pkg/factory/... ./pkg/websocket/...
```
Must pass.

```
test "$(grep -c 'writeJSON(resp, http.StatusAccepted, result)' pkg/handler/api_mutations.go)" -eq 9
```
Must exit 0.

```
test "$(grep -c 'writeJSON(resp, http.StatusOK, result)' pkg/handler/api_mutations.go)" -eq 8
```
Must exit 0 — the eight non-queued handlers that answer 200 today still do (AC1 negative control).

```
! grep -n 'connectionEventPublisher' pkg/factory/*.go
```
Must exit 0.

```
grep -n 'writeQueue.Consume' main.go
```
Must print one line.

```
grep -A10 '^## Unreleased' CHANGELOG.md | grep -q 'write_failed'
```
Must exit 0 (the section exists and carries the optimistic-writes bullet).

```
test "$(grep -c '^## Unreleased' CHANGELOG.md)" -eq 1
```
Must exit 0.
</verification>
