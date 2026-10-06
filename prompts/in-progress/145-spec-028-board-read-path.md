---
status: approved
spec: [028-precompute-task-list-snapshot]
created: "2026-10-06T20:28:50Z"
queued: "2026-10-06T20:57:28Z"
branch: dark-factory/precompute-task-list-snapshot
---

# Serve `/api/tasks` from the precomputed task-list snapshot

<!-- REVIEWER NOTE (open questions / decisions)
- Decomposition: the spec's Suggested Decomposition names 5 prompts; this run produced 4 by merging the spec's prompt 3 (board read path) and prompt 4 (write-path invalidation + watcher ordering) into this one. The two are one coherent behavior — switching the read path without the invalidation wired would leave `/api/tasks` serving stale data after a write, failing AC4 — and the invalidation mechanism itself lives in prompt 2's revision. Splitting them would have left a broken intermediate state or a test-only prompt.
- The accepted extra rebuild per write (see prompt 2's reviewer note) is deliberate; do not "fix" it with an invalidation counter — recording the revision at build start is what guarantees a racing invalidation is never lost.
-->

<summary>
- A `/api/tasks` request no longer lists the vault, probes a transcript or spawns a process; it reads the already-built row list and applies the query filters and the time-dependent visibility rules in memory.
- The row list is built once per page-index change and once per session refresh, not once per request, so the board's work tracks how much changed rather than how often browsers re-fetch.
- A write made through vault-ui is still visible to the next read: the write marks the page index, which moves the key's revision, which makes the next read rebuild before serving.
- A watcher frame still implies fresh data: the event's page read moves the revision, so the next read rebuilds.
- The JSON a browser renders is unchanged: same fields, same order, same filtering and sorting semantics; the response builder is the same code, fed precomputed values.
- A warm request that triggers no rebuild performs no page-storage call and no `ps` spawn, asserted by the factory's counting seams.
- The board's session-state classification and activity date now come from the cached session snapshot, so the request path never touches the filesystem for them.
- `/api/goals` keeps its own list cost and is not routed through this store.
</summary>

<objective>
Build the board's precomputed task rows from the page index and the session snapshot, and rewrite `ListTasks` to serve every `/api/tasks` request from `taskSnapshotStore`, applying the query filters, the visibility rules and the response builder at read time so the JSON contract is unchanged.
</objective>

<context>
Read `docs/dod.md` and, if present, `CLAUDE.md` at the repo root.

Read the spec `specs/in-progress/028-precompute-task-list-snapshot.md` in full. This prompt covers Desired Behaviors 1, 3 and 7 and Acceptance Criteria AC1, AC2, AC3, AC4 and AC7. It is prompt 3 of 4; prompt 1 added the session snapshot, prompt 2 added `pkg/board`'s `taskSnapshotStore` and `pkg/pageindex`'s `Revision`, prompt 4 writes the docs.

Read these files before changing anything:
- `pkg/board/board.go` — `Deps`, `SessionSignals`, `SessionProbe`, `Board`, `board`, `New`, `TaskQuery`.
- `pkg/board/tasks.go` — `ListTasks`, `tasksForVault`, `visibleRow`, `taskResponse`, `taskRow`, `uncompletedBlockers`, `sessionStarted`, `sessionStatePtr`, the filter helpers.
- `pkg/board/snapshot.go` (added by prompt 2) — `taskSnapshotRow`, `taskSnapshotStore`, `newTaskSnapshotStore`, `taskSnapshotParams`, `revisionSource`, `generationSource`, `taskSnapshotBuildFunc`.
- `pkg/board/goals.go` — `ListGoals`/`goalsForVault` (left on the direct path).
- `pkg/board/helpers.go` — `flattenFilter`, `flattenAssigneeFilter`, `parseDateTime`, `hasString`.
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey`, `Revision`.
- `pkg/sessionsnapshot/snapshot.go` (added by prompt 1) — `Snapshot`, `TranscriptMtime`, `Generation`.
- `pkg/handler/api_tasks.go` and `pkg/handler/router.go` — the `/api/tasks` handler and route (unchanged by this prompt).
- `pkg/factory/api.go` — the `board.New(board.Deps{...})` call.
- `pkg/factory/pageindex_test.go` — `countingSeams` and the "serves warm list reads without touching page storage" assertions.
- `pkg/board/board_test.go` — the three `board.New(board.Deps{...})` literals and the `fakeSignals` fake.
- `docs/page-index.md`, `docs/optimistic-writes.md` — the staleness and write-visibility rules the read path must preserve.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
</context>

<requirements>

### 1. The board's seams

`pkg/board/board.go`:
- Add `Generation() uint64` to `SessionProbe` and update its doc comment: the store reads it to detect that a new session snapshot is available. `sessionsnapshot.Snapshot` already satisfies it.
- Add a `Deps` field for the page-index revision seam defined in prompt 2
  (`pkg/board/snapshot.go`, `IndexRevisions`); `pageindex.PageIndex` satisfies it and a test supplies a tiny fake:

```go
// Deps gains:
	// Index reports the page-index revision the store rebuilds on.
	Index IndexRevisions
```

- Add `snapshot *taskSnapshotStore` to the `board` struct.

`pkg/board/board.go` `New`:
```go
func New(deps Deps) Board {
	b := &board{
		vaults:  deps.Vaults,
		ops:     deps.Ops,
		cache:   deps.Cache,
		launch:  deps.Launch,
		clock:   deps.Clock,
		signals: deps.Signals,
		sessions: deps.Sessions,
		homeDir: deps.HomeDir,
	}
	b.snapshot = newTaskSnapshotStore(taskSnapshotParams{
		Build:       b.buildTaskRows,
		Revisions:   deps.Index,
		Generations: deps.Sessions,
	})
	return b
}
```

Both `deps.Index` and `deps.Sessions` are required in production and in tests; a nil value is a wiring bug, so do not add nil-guards that silently disable the store.

### 2. The row builder

Replace `tasksForVault` with `buildTaskRows`, which lists the vault and precomputes every field that needs I/O, applying **no** request-time filter:

```go
// buildTaskRows lists the vault's tasks and precomputes every field that needs
// I/O: the uncompleted blockers, the blocked flag, the session-started marker,
// the classified session state and the activity date. It applies none of the
// request-time filters, so one build serves every query.
func (b *board) buildTaskRows(ctx context.Context, vault Vault) ([]taskSnapshotRow, error)
```

Body:
1. `items, err := b.ops.List(vault).Execute(ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "")`; on error `errors.Wrapf(ctx, err, "list tasks for vault %s", vault.Name)`. This is the vault-cli list walk, and it runs at build time, not per request. It goes through the page index, so it resolves any pending write mark before returning (read-your-writes).
2. `projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)`; `projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")`.
3. `registryIDs := b.signals.RegistrySessionIDs(ctx)`; `resumeIDs := b.signals.ResumeSessionIDs(ctx)` — read once per build, from the session snapshot, never per task.
4. For every item, build a `taskSnapshotRow`:
   - `blockers := b.uncompletedBlockers(vault.Name, item.BlockedBy)`; `blocked := len(blockers) > 0`; when `blockers` is nil keep it nil (the response builder turns nil into `[]`).
   - `started := b.sessionStarted(vault.Name, item.Name)`.
   - `state := session.ClassifySessionState(ctx, session.ClassifyParams{SessionID: item.ClaudeSessionID, ProjectDir: projectDir, ProjectsRoot: projectsRoot, Now: b.clock.Now().UTC(), LiveWindow: session.DefaultLiveWindow, ResumeSessionIDs: resumeIDs, RegistrySessionIDs: registryIDs, TranscriptMtime: b.transcriptProbe()})`; `sessionState := sessionStatePtr(state)`.
   - `activityDate := activity.ComputeActivityDateWith(ctx, b.transcriptProbe(), parseDateTime(item.ModifiedDate), item.ClaudeSessionID, projectDir, projectsRoot)`.
5. Return the rows in the vault-cli list order (the store preserves the order and the read path concatenates vaults in query order, so the response order is unchanged).

Delete `tasksForVault`. Its filter and visibility logic moves into `ListTasks` (step 3).

### 3. `ListTasks` serves from the store

Rewrite `ListTasks` so it performs no per-request vault read, transcript probe or process spawn:

1. `all, err := b.vaults.Vaults(ctx)`; `selected := b.selectVaults(all, query.Vaults)`.
2. `statusFilter := flattenFilter(query.Statuses)`; `effectiveStatus := statusFilter; if effectiveStatus == nil { effectiveStatus = defaultStatuses }`; `phaseFilter := flattenFilter(query.Phases)`; `assigneeFilter := flattenAssigneeFilter(query.Assignees)`; `goalFilter := flattenFilter(query.Goals)`.
3. `now := b.clock.Now().UTC().Time()`; `cutoff := now.Add(time.Duration(query.UpcomingHours) * time.Hour)`; `lookback := now.Add(-LookbackHours * time.Hour)`.
4. For each selected vault, in order:
   - `rows, err := b.snapshot.List(ctx, vault)`; on error return it.
   - `projectDir`/`projectsRoot` are no longer needed for the response (the activity date is precomputed); do not derive them.
   - For each `row` in `rows`, apply the same filters and visibility in the same order as before: `hasString(effectiveStatus, row.item.Status)`, `phaseMatches(row.item.Phase, phaseFilter)`, `assigneeMatches(row.item.Assignee, assigneeFilter)`, `goalMatches(goalsValue(row.item.Goals), goalFilter)`; then `visible, visibleErr := b.visibleRow(ctx, row.item, now, cutoff, lookback)`; on `visibleErr != nil` return `errors.Wrapf(ctx, visibleErr, "resolve visibility for %s", row.item.Name)`, and skip the row when not visible.
   - For a visible row, set the precomputed fields onto the `taskRow` `visibleRow` returned (`row.blockers`, `row.blocked`, `row.started`, `row.sessionState`) and append `b.taskResponse(vault, taskRow, row.activityDate)`.
5. Apply the `query.SessionLive` filter exactly as today (keep only `SessionState != nil && *SessionState == string(session.SessionStateLive)`).
6. The response order (vault order × vault-cli list order) and every field are unchanged. `goalsValue`, `priorityValue`, `dateTimeString`, `strPtr` and `obsidianURL` are unchanged.

`visibleRow` keeps its current signature and behaviour. `taskResponse` changes signature to take the precomputed activity date and drop the now-unused parameters:

```go
// taskResponse renders one visible row. activityDate is the precomputed
// transcript-aware activity date, so the request path never probes.
func (b *board) taskResponse(vault Vault, row taskRow, activityDate *libtime.DateTime) api.TaskResponse
```

Its body is today's, with the `activity.ComputeActivityDate(...)` expression replaced by `activityDate` and the now-unused `ctx`, `projectDir` and `projectsRoot` parameters removed.

### 4. Wire the factory

`pkg/factory/api.go` — in the `board.New(board.Deps{...})` literal set `Index: pageIndex` and `Sessions: sessionSnapshot` (`pageIndex` and `sessionSnapshot` are already parameters of `CreateAPIHandler`; no signature change). `pageIndex` is the same instance already passed to `opsProvider`, so the store's revision reads observe every mark and publication the write paths and the watcher cause.

`pkg/handler/api_tasks.go` and `pkg/handler/router.go` are unchanged.

### 5. Why a write is still visible (DB7)

No new invalidation call is added to `pkg/mutations` or `pkg/watchrefresh`. The mechanism is:
- The synchronous and queued write paths already call `Index.MarkDirty`/`MarkFileDirty` before their `Publish*Updated` frame (`pkg/mutations/mutations.go`: `markVaultDirty`, `taskWritten`, `goalWritten`, `itemWrittenSilently`). Those marks advance the page-index revision (prompt 2), which is the task-list snapshot's invalidation.
- A watcher event already calls `pageIndex.RefreshFile`/`Refresh` before broadcasting (`pkg/watchrefresh/handler.go`), and a publication advances the revision.
- So the next `/api/tasks` read after either rebuilds, and that rebuild's vault-cli list walk resolves the pending write mark before reading, so the reader sees the post-write content.
Add a test that proves this end to end (step 6), and do NOT change `pkg/mutations` or `pkg/watchrefresh` — AC7 requires the existing `pkg/watchrefresh` tests to exit 0 unchanged.

### 6. Tests

`pkg/board/board_test.go` — update the three `board.New(board.Deps{...})` literals: add `Index: <fake revision source>` (a tiny type with a settable `uint64` behind a mutex) and `Sessions: <fake session probe>` (extend `fakeSignals` with `TranscriptMtime` returning a configurable value and `Generation` returning a settable counter). Add tests:
1. **DB1 / warm read** — a harness with a counting `OpsProvider.List`: the first `ListTasks` triggers exactly one list call (the build); a second identical `ListTasks` triggers zero additional list calls and returns an equal response.
2. **DB7 / read-your-writes** — after the first `ListTasks`, mutate the fake list's items and bump the fake index revision (the effect of a write's `MarkFileDirty`), then `ListTasks` again: it rebuilds and the response reflects the mutated items.
3. **AC7 / watcher ordering** — bumping the revision (the effect of a watcher's `RefreshFile`) makes the next `ListTasks` rebuild; a read that does not bump the revision does not.
4. **Session refresh** — bumping the fake session `Generation` makes the next `ListTasks` rebuild, and the rebuilt response carries the fake probe's new session state (this is the AC5 behaviour seen from the board).
5. **Filters and visibility unchanged** — the existing `ListTasks` specs (status/phase/assignee/goal filters, upcoming/recently-completed, `session_live`, blocked/blockers, started marker, response fields) must pass unchanged against the store-served path; update only their fixture wiring, never their expectations.

`pkg/factory/pageindex_test.go` — the existing "serves warm list reads without touching page storage" assertion must hold **unchanged**: the page index is already warm, so the first (cold-store) `/api/tasks` request rebuilds from the warm page-index snapshot and still adds zero `ListFiles`/`ReadPage` calls, exactly as the other warm requests do. Do not adjust or relax `Expect(seams.totalListCalls()).To(Equal(warmCalls))`.

`pkg/handler/api_test.go` and the parity fixtures must pass unchanged: the response bodies are the frozen contract (AC4).

No new timer, no new goroutine in production code.

### 7. CHANGELOG

Do NOT edit `CHANGELOG.md` in this prompt — prompt 4 adds the single entry for the spec.

</requirements>

<constraints>
- The JSON contract of `/api/tasks` is frozen: same fields, same order, same filtering and sorting semantics. This is enforced by the existing handler and parity tests, not by convention.
- A warm `/api/tasks` request performs no page-storage call, no transcript probe and no process spawn; it reads the published rows and filters in memory.
- The task-list snapshot is rebuilt only when its inputs change: a page-index revision move for its key, or a new session snapshot. The store owns no timer.
- A published snapshot is never mutated; `go test -race ./pkg/...` must stay clean.
- Accepted and deliberate: because a rebuild's own page read resolves a pending write mark (advancing the revision), a write can cost one extra rebuild on the next read; the extra build lists an unchanged folder and classifies from the cached session snapshot, so it is cheap and bounded. Do not add an invalidation counter to suppress it — recording the revision at build start is what guarantees a racing invalidation is never lost.
- `pkg/mutations` and `pkg/watchrefresh` are NOT changed by this prompt; the existing `pkg/watchrefresh` tests must exit 0 unchanged (AC7).
- `/api/goals` is not routed through the store and gets no acceptance criterion; it inherits only the session snapshot from prompt 1.
- Response bodies, status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract). No route is added to `:8000` or `:9090`.
- `/api/tasks` stays read-only; the snapshot adds no write surface.
- No config knob, opt-out flag or tunable interval is added.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, Ginkgo/Gomega tests, ≥ 80 % coverage on the changed packages.
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
go test -race ./pkg/...
```
Must pass (AC6: the race detector is clean).

```
go test -race ./pkg/board/... ./pkg/factory/... ./pkg/handler/... ./pkg/watchrefresh/...
```
Must pass (AC7: the existing watcher tests exit 0 unchanged).

```
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80 %).

```
grep -q 'Index: pageIndex' pkg/factory/api.go && grep -q 'Sessions: sessionSnapshot' pkg/factory/api.go
```
Must exit 0 (the store's inputs are wired).

```
! grep -n 'tasksForVault' pkg/board/*.go
```
Must exit 0 (the per-request vault list walk is gone).

```
! grep -rnE 'NewTicker|time\.AfterFunc' pkg/pageindex pkg/board
```
Must exit 0 (no sub-60 s ticker in the snapshot packages; the store adds no timer).

Before finishing, walk Desired Behaviors 1, 3 and 7 and AC1, AC2, AC3, AC4 and AC7 against the tests you wrote and name the test that establishes each. AC1, AC2 and AC3 are Post-Deploy (Rung-2) operator criteria — the container-side proxy is the warm-read assertion that a second `/api/tasks` request adds zero page-storage calls and zero `ps` spawns.
</verification>
