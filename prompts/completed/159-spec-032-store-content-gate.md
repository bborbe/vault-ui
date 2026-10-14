---
status: completed
spec: [032-task-body-cache-invalidation]
summary: Gated the task-list snapshot store's published-rows signal on content, replacing the process-wide rebuild counter with a per-key signal that advances only when a rebuild's rows differ from the key's last published rows.
execution_id: vault-ui-cache-invalidation-exec-159-spec-032-store-content-gate
dark-factory-version: v0.198.7
created: "2026-10-13T22:16:50Z"
queued: "2026-10-13T22:54:54Z"
started: "2026-10-13T22:54:55Z"
completed: "2026-10-13T22:58:38Z"
branch: dark-factory/task-body-cache-invalidation
---

# Content gate: the task-list store advances a published-rows signal only when a key's rows actually change

<summary>
- The board's task-list snapshot store stops signalling "something changed" on every rebuild.
- A rebuild that reproduces the same rows as the key's last publication now leaves the key's published-rows signal untouched, whether the rebuild was a full build, a row patch or a session refresh.
- A rebuild that changes any rendered row advances that key's signal.
- The body cache therefore keeps a held body across an identical-row rebuild, and drops it when a rendered row really moves.
- Every rendered field of a row participates: changing any one of status, phase, defer date, completed date, priority, assignee, goals, blocked, blockers, session state, activity date, session-started marker, open questions or flag advances the signal.
- Each key gets its own signal, readable by key, so a later change can scope invalidation to the vaults a query selects.
- No document is edited and no production behaviour outside the store changes in this prompt.
</summary>

<objective>
Make the task-list snapshot store's published-rows signal content-gated: it advances only when a key's newly built rows differ from the rows that key last published, and each key exposes its own signal. This is the load-bearing half of the fix — without it, a per-query signal still moves on an identical-row rebuild and the bare `/api/tasks` body stays cold exactly as it is today.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it is absent in this checkout) and `docs/dod.md` (the Definition of Done the validation step checks).

Read the spec `specs/in-progress/032-task-body-cache-invalidation.md` in full. This prompt covers Desired Behavior 1 and Acceptance Criteria 1 and 2. It is prompt 1 of 3: prompt 2 scopes the body cache to the selected vaults' signals and caps the held set; prompt 3 owns the docs, the CHANGELOG and the byte-identity/failure-path rows. Do not do their work here.

Read these files fully before changing anything:
- `pkg/board/snapshot.go` — the store under change. `taskSnapshotStore.snapshotGeneration` (a process-wide rebuild counter) is incremented unconditionally at the success path of `runBuild` (`s.snapshotGeneration++`). `taskSnapshotEntry` holds the published `rows`, `hasSnapshot`, `pageRevision` and `sessionGeneration`. `SnapshotGeneration()` exports the counter. `List` decides the rebuild kind (`patchOnly`, `refreshOnly`, full) and `runBuild` runs it and publishes the result. There are three build kinds and all three must be content-gated.
- `pkg/board/taskbody.go` — the body cache. `taskBodyParams.Generations func() uint64` is the seam it reads; `Get` compares the read value to `c.generation` and drops every held entry when it moves. This prompt does not change `Get`; it changes what `Generations` returns (the board wires `Generations: b.snapshot.SnapshotGeneration` in `pkg/board/board.go`). `taskBodyKey`, the clock boundary (`taskBodyBoundary`, `expiresAt`) and the failure path stay exactly as they are.
- `pkg/board/tasks.go` — `ListTasks`, `taskSnapshotRows`, `ListTasksBody` and `taskResponse`. `ListTasksBody` is the production read order the test harness must mirror: it calls `b.taskSnapshotRows` (which calls `b.snapshot.List` for every selected vault, so the store rebuilds) **before** `b.bodies.Get`. `taskResponse` shows every rendered field and its row source: `item.Status`, `item.Phase`, `item.DeferDate`, `item.CompletedDate`, `item.Priority`, `item.Assignee`, `item.Goals`, `row.blocked`, `row.blockers`, `row.sessionState`, `activityDate`, `row.started`, `row.openQuestions`, `item.Flag`. All of these live on `taskSnapshotRow` (directly or through `item ops.TaskListItem`), which is why a whole-struct comparison is the content gate. `phaseOverride` is a request-time field on `taskRow`, not on `taskSnapshotRow` — do not add it to the snapshot row.
- `pkg/board/snapshot_internal_test.go` — the store's tests. `snapshotHarness`, `fakeRevisions` (`bump`, `markPages`, `markFolder`), `fakeGenerations`, `snapshotRow`, `numberedRows`, `rowsBuilder`, `statusPatch`, `allStatusRefresh`. The row `advances the snapshot generation on a successful rebuild only` is renamed by this prompt.
- `pkg/board/taskbody_internal_test.go` — the body cache's tests. `bodyHarness`, `newBodyHarness`, `bodyFixture`, `bodySnapshotRow`, `setGeneration`. The existing rows in this file must keep passing; this prompt renames none of them.
- `pkg/board/board.go` — the wiring in `New` (`b.snapshot = newTaskSnapshotStore(...)`, `b.bodies = newTaskBodyCache(taskBodyParams{... Generations: b.snapshot.SnapshotGeneration ...})`) and `Vault` (`Path`, `TasksFolder`).

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — coverage rules.
</context>

<requirements>

### 1. The store's content gate (`pkg/board/snapshot.go`)

Replace the process-wide rebuild counter with a content-gated published-rows signal.

- Rename the store field `snapshotGeneration uint64` to `signalSeq uint64`. It is a monotonic counter that advances **only** when a build's rows differ from the rows the key last published. Update its doc comment to say exactly that: it counts content-changing publications across every key, not rebuilds.
- Add a per-key field to `taskSnapshotEntry`: `signal uint64` — the key's published-rows signal, `0` until the key has published rows. Update `taskSnapshotEntry`'s doc comment to name it as the input the held bodies are gated on (alongside the existing `pageRevision`/`sessionGeneration`, which stay as the store's own rebuild triggers and are unchanged).
- In `runBuild`, on the **success path only**, compute whether the publication changed the key's rendered rows:
  ```go
  changed := !e.hasSnapshot || !rowsEqual(e.rows, rows)
  if changed {
      s.signalSeq++
      e.signal = s.signalSeq
  }
  e.rows = rows
  e.hasSnapshot = true
  e.pageRevision = b.startRevision
  e.sessionGeneration = b.startGeneration
  ```
  Remove the unconditional `s.snapshotGeneration++`. This single success path serves all three build kinds (`patchOnly`, `refreshOnly`, full), so a patch or a refresh that reproduces identical rows leaves the signal untouched and a patch or refresh that changes a row advances it. The failure path is unchanged (no signal move, entry stays dirty, previous rows keep serving).
- Add a package-level helper `rowsEqual`:
  ```go
  // rowsEqual reports whether two published row sets are identical in every
  // rendered field. The clock-derived fields (upcoming, recently_completed, the
  // phase override) are not stored on a row, so they cannot differ here; the
  // request path applies them.
  func rowsEqual(a, b []taskSnapshotRow) bool {
      return reflect.DeepEqual(a, b)
  }
  ```
  Import `"reflect"`. `reflect.DeepEqual` compares the `*string` / `*libtime.DateTime` pointer fields by pointed-to value and the slices element-wise, which is exactly the rendered-field comparison this needs. Do not hand-roll a per-field comparison.
- Add the per-key accessor:
  ```go
  // Signal returns the key's published-rows signal: the value of the store's
  // content-gated counter at the key's last content-changing publication, or 0
  // when the key has never published rows. It is the input a held body is gated
  // on for the vaults a query selects.
  func (s *taskSnapshotStore) Signal(key pageindex.Key) uint64 {
      s.mu.Lock()
      defer s.mu.Unlock()
      return s.entryLocked(key).signal
  }
  ```
- Change `SnapshotGeneration()` to return `s.signalSeq`. Keep the name and the signature `func (s *taskSnapshotStore) SnapshotGeneration() uint64` so `pkg/board/board.go`'s `Generations: b.snapshot.SnapshotGeneration` compiles unchanged (prompt 2 replaces that seam and removes this method). Rewrite its doc comment: it returns the number of **content-changing** publications so far, across every key — it advances by one only when a rebuild's rows differ from the rows the key last published, so a full build, a row patch or a session refresh that reproduces identical rows leaves it put.

Do **not** change `List`'s signature, `runBuild`'s signature, `taskSnapshotBuild`, `IndexRevisions`, `generationSource` or any of the rebuild-kind selection logic.

### 2. Rename the store row that asserts the counter (`pkg/board/snapshot_internal_test.go`)

Rename the row `advances the snapshot generation on a successful rebuild only` to `advances the published-rows signal only when a rebuild changes the rows`, and extend its body so it pins the content gate rather than a rebuild count. Assert against the **per-key** accessor `h.store.Signal(key)` where `key := pageindex.NewKey(snapshotVaultA.Path, snapshotVaultA.TasksFolder)`. Keep the existing failure-half (a failed build does not advance the signal) and add the identity half. The row must assert, in order:

1. First read of `snapshotVaultA` publishes rows → `Signal(key)` is `uint64(1)`.
2. A revision move (`h.revisions.bump()`) whose build returns **byte-identical rows** → `Signal(key)` is still `uint64(1)` (the content gate).
3. A second revision move whose build returns **different rows** → `Signal(key)` is `uint64(2)`.
4. A revision move whose build **fails** → `Signal(key)` is still `uint64(2)`.

Use a build func whose returned rows are keyed off the build ordinal and the revision, so steps 2–4 are deterministic. Do not change any other row in this file. The `fakeGenerations` type and its `bump()` are for the session-generation input and stay as they are; they are not the published-rows signal.

### 3. Add the content-gate rows over a real store and a real body cache (`pkg/board/taskbody_internal_test.go`)

Add a new `Describe("task-list body cache content gate", ...)` block. It must wire a **real** `taskSnapshotStore` (built with `newTaskSnapshotStore`, a `fakeRevisions` and a `fakeGenerations`) to a **real** `taskBodyCache` (built with `newTaskBodyCache`), so the row exercises the store's gate through the cache, not a settable counter. Add a small harness for it (a struct holding the store, the revisions, the body cache and a counting encoder) in this file.

The harness's read helper must mirror `ListTasksBody`'s production read order: **first** call `store.List(ctx, vault)` for every **configured** vault (this is what triggers a rebuild on a revision move, and it makes every key's signal live even for keys the query does not select), **then** call `cache.Get(ctx, query)`. Without the forced snapshot read a held body would never observe a rebuild, and every row below would pass trivially — the exact no-op the pair is meant to exclude.

Wire the body cache with:
- `Project` returning a fixed response per row (e.g. `api.TaskResponse{ID: row.item.Name}`);
- `Encode` as `encodeTaskList` wrapped by a counter;
- `Generations` as the store's `SnapshotGeneration`;
- `Clock` as a fixed `newFakeBodyClock(bodyBaseTime)` — `taskBodyParams.Clock` is required and `Get` dereferences `c.clock` on its first line, so a harness that omits it panics;
- `Rows` calling `store.List(ctx, vault)` for each selected vault (idempotent: the helper has already rebuilt the store, and `Get` needs the rows only for `taskBodyBoundary`).
- the store's `Build` returning a row set derived from a mutable fixture (so a row can be changed between builds); the store's `Revisions` is a `fakeRevisions`; a "page-index revision move" is `revisions.bump()`.

Add these rows, with these exact names:

- `It("a rebuild that reproduces identical rows keeps the held body")` — read once (encoder count 1), move the revision with the fixture **unchanged**, read again, and assert the encoder count is still **1** (the store published identical rows, so the signal did not move and the held body is served).
- `It("a rebuild that changes a rendered row discards the held body")` — read once (encoder count 1), move the revision with **one row's status changed** in the fixture, read again, and assert the encoder count is **2**.

The pair pins the gate: either row alone is satisfied by a no-op (never invalidate, or always invalidate); only the pair together proves the comparison.

- `DescribeTable("a change to any rendered row field discards the held body", ...)` with one `Entry` per rendered field, named exactly: `status`, `phase`, `defer_date`, `completed_date`, `priority`, `assignee`, `goals`, `blocked`, `blockers`, `session_state`, `activity_date`, `claude_session_started`, `open_questions`, `flag`. The table body takes a `mutate func(*taskSnapshotRow)`; it reads once (encoder count 1), moves the revision after applying `mutate` to exactly one row of the fixture, reads again, and asserts the encoder count is **2**. Map each Entry to the row field that backs the rendered field: `status`→`item.Status`, `phase`→`item.Phase`, `defer_date`→`item.DeferDate`, `completed_date`→`item.CompletedDate`, `priority`→`item.Priority`, `assignee`→`item.Assignee`, `goals`→`item.Goals`, `blocked`→`blocked`, `blockers`→`blockers`, `session_state`→`sessionState`, `activity_date`→`activityDate`, `claude_session_started`→`started`, `open_questions`→`openQuestions`, `flag`→`item.Flag`. Every Entry must set a value that **differs** from the fixture's current value (e.g. flip a bool, set a non-empty string where the fixture has an empty one).

Do not modify the existing `Describe("taskBodyCache", ...)` block or its rows in this file; they must keep passing unchanged. Do not modify `pkg/board/snapshot_internal_test.go` beyond the rename in requirement 2.

### 4. No production change outside the store

`pkg/board/taskbody.go`'s `Get`, `taskBodyKey`, `taskBodyBoundary` and the failure path are unchanged. `pkg/board/board.go` is unchanged. Do not touch `pkg/board/vaults.go`, `pkg/handler/`, `docs/` or `CHANGELOG.md` — prompts 2 and 3 do those. Confirm `pkg/board/vaults.go`'s `ListAssignees` still reads `b.snapshot.List`.

### 5. Self-check

Before finishing, re-run every `<verification>` command. Walk Desired Behavior 1 and Acceptance Criteria 1 and 2 against the tests and name the row that establishes each. Confirm `pkg/board` keeps `>= 80 %` statement coverage.
</requirements>

<constraints>
- The decoded JSON is unchanged: same fields, same order, same values, same escaping and trailing-newline trim. This prompt changes **when** a held body is discarded, never what it contains.
- The clock boundary and the failure path are unregressed. The existing rows `clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `boundary is the earliest transition`, `stores nothing and evicts nothing when a build fails` and `leaves the entry unheld when the generation moves during a build` keep their assertions and must still pass.
- The existing rows `serves a held body without re-encoding`, `cached and uncached bodies are byte-identical`, the `keys every body-changing field` table, `gzip-compresses the identity form losslessly`, `drops the held entries when the generation moves` and `rebuild discards held bodies` keep their assertions and must still pass (prompt 2 renames the last two, not this prompt).
- The store's rebuild model is unchanged: the same triggers, the same row patch, the same session refresh, the same atomic swap, the same failed-rebuild-keeps-serving rule. This prompt changes only what the signal counts.
- No new timer, no new configuration key, no change to `SessionRefreshInterval`. No new log line, metric or counter on the body-build path.
- `pkg/board/vaults.go`'s `ListAssignees` keeps reading parsed rows.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code.
- Do NOT change the `Board` interface, `ListTasks`, `ListAssignees`, `ListTasksBody`, `taskSnapshotStore.List` or `SnapshotGeneration` signatures.
- Do NOT modify `pkg/board/board.go`, `pkg/board/taskbody.go` (production), `pkg/handler/`, `docs/` or `CHANGELOG.md`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/... ./pkg/handler/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
grep -n 'func rowsEqual' pkg/board/snapshot.go
```
Must print one line.

```
grep -n 'func (s \*taskSnapshotStore) Signal(key pageindex.Key) uint64' pkg/board/snapshot.go
```
Must print one line.

```
! grep -n 'snapshotGeneration' pkg/board/snapshot.go
```
Must exit 0 (the rebuild counter is gone).

```
grep -n 'a rebuild that reproduces identical rows keeps the held body' pkg/board/taskbody_internal_test.go
grep -n 'a rebuild that changes a rendered row discards the held body' pkg/board/taskbody_internal_test.go
grep -n 'a change to any rendered row field discards the held body' pkg/board/taskbody_internal_test.go
grep -n 'advances the published-rows signal only when a rebuild changes the rows' pkg/board/snapshot_internal_test.go
```
Each must print at least one line.

```
grep -cE '^[[:space:]]*Entry\(' pkg/board/taskbody_internal_test.go
```
Must be at least 21 (the existing `keys every body-changing field` table's 7 entries plus the new 14-field table). The pattern is anchored to the start of the line on purpose: a bare `Entry(` also matches `hasEntry(`, which appears on three lines of this file, and would let 11 of the 14 required Entries pass.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
