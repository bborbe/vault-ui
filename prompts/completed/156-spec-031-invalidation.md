---
status: completed
spec: [031-task-list-body-cache]
summary: Verified the clock-boundary body-cache invalidation (Clock/Rows seams, expiresAt hit check, taskBodyBoundary over pre-filter rows, taskSnapshotRows) is complete; go test -race ./pkg/board/..., 90.2% coverage, all grep gates, and ROOTDIR=/workspace make precommit exit 0.
execution_id: vault-ui-exec-156-spec-031-invalidation
dark-factory-version: v0.196.0
created: "2026-10-09T20:51:14Z"
queued: "2026-10-09T21:18:12Z"
started: "2026-10-09T22:26:31Z"
completed: "2026-10-09T22:29:28Z"
branch: dark-factory/task-list-body-cache
---

# Discard a held body when the clock crosses the boundary that changes it

<summary>
- A held body is dropped when the clock crosses the instant at which one of its clock-derived fields changes.
- The boundary is computed from the request's clock reading and its upcoming-hours window, not from a fixed timer.
- A body whose boundary has not been crossed keeps serving; a body whose boundary has been crossed is rebuilt.
- The boundary is the earliest transition among the built rows, so the cache never serves a body the clock has made stale.
- A snapshot generation move already drops the held bodies; this step adds the regression test that proves it and the test for the clock boundary.
- No time-to-live, no ticker, no background sweep — the boundary is derived per build and checked on read.
- The decoded body is unchanged: only when it is rebuilt changes.
</summary>

<objective>
Add the second invalidation axis to the body cache: a held entry expires when the clock crosses the next instant at which any clock-derived field of that body can change, derived from the request's clock reading and its upcoming-hours window. This completes the spec's invalidation model and covers the spec's Suggested Decomposition row 2 (Desired Behavior 6; Acceptance Criteria 5 and 6). It runs after prompt 1, which built the cache and its generation keying.
</objective>

<context>
Read the spec `specs/in-progress/031-task-list-body-cache.md` in full, especially Desired Behavior 6, the Constraints on the clock boundary, the Failure Modes rows for a clock crossing mid-request and for unbounded query cardinality, and Acceptance Criteria 5 and 6.

Read `docs/page-index.md` § *Task-list snapshot* — its *Rebuild triggers* and *Failed rebuild* bullets are the model this cache mirrors. This spec does not restate or change that model.

Read the code prompt 1 added and the code it reads:
- `pkg/board/taskbody.go` — `TaskListBody`, `taskBodyParams`, `taskBodyEntry`, `taskBodyCache`, `newTaskBodyCache`, `Get`, `taskBodyKey`, `encodeTaskList`, `gzipBody`. Prompt 1 added all of these.
- `pkg/board/snapshot.go` — `taskSnapshotStore.SnapshotGeneration` (prompt 1) and `runBuild`'s success path that increments it.
- `pkg/board/tasks.go` — `ListTasks`, `visibleRow` (the `upcoming`, `recently_completed` and `phaseOverride` rules).
- `pkg/board/board.go` — `New` (where `b.bodies` is built), `Deps.Clock`, `TaskQuery`, `LookbackHours`.
- `pkg/board/helpers.go` — `parseDateTime`, `parseDeferDate`.
- `pkg/board/tasks.go` — `parseFlexibleDate` (`pkg/board/tasks.go:758`), the completed-date parser the boundary's completed candidate uses.
- `pkg/board/taskbody_internal_test.go` — the counting seams and tests prompt 1 added. Extend them; do not duplicate them.
- `pkg/board/board_test.go` — `newHarness`, `harness`, `item`, and `libtime.CurrentDateTimeGetterFunc`, used for the clock in the board tests.

The board's clock is `libtime.CurrentDateTimeGetter` (`b.clock`); `b.clock.Now().UTC().Time()` is the `time.Time` the read path uses. `visibleRow` derives `upcoming` from `DeferDate` and the window `[now, now+upcoming_hours]`, `recently_completed` and `phaseOverride` from the completed date and the `[now-LookbackHours, now]` window.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

`.dark-factory.yaml` sets `hideGit: true`, so the container's `.git` is masked. Never rely on a `git` command in this prompt. All verification below is non-git.

Decision (spec wording). The spec says the boundary is "the next `upcoming_hours` boundary that can change `upcoming`, `recently_completed` or `phaseOverride`" and is "derived from the request's `now` and `upcoming_hours`, not from a fixed timer". Read literally, `now + upcoming_hours` is not sufficient: a deferred task at `now + 0.5h` flips from upcoming to visible at `now + 0.5h`, before that. The boundary is therefore the earliest instant, strictly after `now`, at which any of the three fields changes, computed over the snapshot's pre-filter rows from the request's `upcoming_hours` and `LookbackHours`. This is the only reading that never serves a stale body.
</context>

<requirements>

### 1. Give the cache a clock and an expiry

In `pkg/board/taskbody.go`:

- Add a `Clock libtime.CurrentDateTimeGetter` field to `taskBodyParams` with the doc comment "Clock reads the request time the clock boundary is derived from. Production: the board's clock." Add the `time` and `libtime "github.com/bborbe/time"` imports to `pkg/board/taskbody.go` — prompt 1 deliberately left them out, because neither was used until this step.
- Add a `clock libtime.CurrentDateTimeGetter` field to `taskBodyCache` and copy it in `newTaskBodyCache`.
- Add an `expiresAt time.Time` field to `taskBodyEntry` with the doc comment "expiresAt is the first instant, strictly after the entry was built, at which a clock-derived field of the body can change. The zero time means the body has no clock-derived rows and never expires within its generation."

In `pkg/board/board.go`'s `New`, pass the board clock into the cache:

```go
	b.bodies = newTaskBodyCache(taskBodyParams{
		Project:     b.ListTasks,
		Rows:        b.taskSnapshotRows,
		Encode:      encodeTaskList,
		Generations: b.snapshot.SnapshotGeneration,
		Clock:       b.clock,
	})
```

### 2. Check the boundary on read and compute it on build

In `taskBodyCache.Get` (prompt 1's implementation), change two points:

- Capture the request time once, before the hit check: `now := c.clock.Now().UTC().Time()`. Use this single reading for both the hit test and the build.
- A hit is served only while it is unexpired. Replace the plain map lookup with:

```go
		if entry, ok := c.entries[key]; ok {
			if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
				body := entry.body
				c.mu.Unlock()
				return body, nil
			}
			delete(c.entries, key)
		}
```

So a crossed boundary discards the entry, and a discarded entry is not served again — the read falls through to the build.

- After the build produces `body`, read the snapshot's pre-filter rows through a new `Rows func(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error)` seam on `taskBodyParams` and a `rows` field on `taskBodyCache` — production: the board reads `b.snapshot.List(ctx, vault)` for each of the query's selected vaults and concatenates the rows, before the visibility filter drops any — and compute `expiresAt := taskBodyBoundary(rows, now, query.UpcomingHours)` over them. Store `&taskBodyEntry{body: body, expiresAt: expiresAt}` at step 7. Do not compute the boundary anywhere else, and do not re-read the clock during the build. The `now` the boundary uses is read slightly earlier than the one `b.ListTasks` reads internally, so the only possible effect is a conservative (early) rebuild, never a stale serve.

Add `taskSnapshotRows(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error)` to `pkg/board/tasks.go`: it resolves `b.vaults.Vaults(ctx)` through `b.selectVaults(all, query.Vaults)` (the same selection `ListTasks` makes), calls `b.snapshot.List(ctx, vault)` per selected vault, concatenates the rows, and returns them **before** the status filter and `visibleRow` drop any.

The generation-capture rule from prompt 1 is unchanged: capture `gen` at the top under the mutex, re-read it at the store step, and leave the entry unheld when it moved.

### 3. The boundary function

Add:

```go
// taskBodyBoundary returns the earliest instant strictly after now at which a
// clock-derived field of the built body can change, or the zero time when none
// can. The clock-derived fields are upcoming, recently_completed and the
// recently-completed phase override; they are the only places the request's now
// enters the response.
//
// It is computed over the snapshot's pre-filter rows — the rows the visibility
// filter has not yet dropped — because a deferred row beyond now+upcoming_hours
// is dropped by visibleRow and is precisely the row whose entry instant matters.
// Each row's dates and status are read from its item.
//
// It is derived from those rows and the request's windows — never from a fixed
// timer — so it moves with the data and with the request's upcoming_hours.
func taskBodyBoundary(rows []taskSnapshotRow, now time.Time, upcomingHours int) time.Time
```

Compute it as the minimum over every row of the candidate instants below, considering a candidate only when it is strictly after `now`, and returning the zero `time.Time` when no candidate qualifies:

- For a row with a parseable `DeferDate` `d` (use `parseDeferDate`, which accepts both the date-only and RFC3339 forms and is already in `pkg/board/helpers.go`):
  - `d` — the instant the task flips from upcoming to visible;
  - `d.Add(-time.Duration(upcomingHours) * time.Hour)` — the instant a task that is hidden now enters the window and becomes upcoming.
- For a row whose `Status` is `"completed"`, let `r` be `parseFlexibleDate(CompletedDate)` when that parses, else `parseDateTime(ModifiedDate).Time()` when that parses; add `r.Add(LookbackHours * time.Hour)` — the instant the task leaves the recently-completed window (and loses its phase override). Skip the row when neither parses.

Do not consider a row whose `Status` is not `"completed"` for the completed candidate, and do not consider `d`-based candidates for a completed row.

The candidate set is deliberately small and in-memory: it walks the snapshot's pre-filter rows, parses a date per row and takes a minimum. It performs no vault read, no page scan and no process spawn.

### 4. Tests (`pkg/board/taskbody_internal_test.go`, package `board`)

Extend the file prompt 1 added. Reuse its counting projection, counting encoder and its `Generations func() uint64` generation seam (a per-test counter behind the func, not a package-level variable); add a settable clock behind the existing seams:

```go
type fakeBodyClock struct {
	mu  sync.Mutex
	now time.Time
}
func (c *fakeBodyClock) Now() libtime.DateTime { ... }
```

Provide it as `Clock` to `newTaskBodyCache`. Prompt 1's tests construct the cache without a clock; extend whatever helper they use (or each construction) so every `newTaskBodyCache` call passes a non-nil `Clock` and a non-nil `Rows`, or `Get` will dereference a nil seam (the clock in the hit test, `Rows` in the boundary computation). The test's projection must return rows whose `DeferDate`/`CompletedDate` are set from the fake clock, so the boundary is exercised with real row data. Name the Ginkgo `It` rows exactly as the spec's evidence strings: `rebuild discards held bodies` (item 2) and `clock boundary discards held bodies` (item 1). Cover at least:

1. **AC6 — clock boundary discards held bodies.** A row with `DeferDate` at `now+2h`, `upcoming_hours` 8, clock fixed at `now`. `Get` once (encoder count 1). Advance the clock to `now+1h`; `Get` again — still a hit (encoder count stays 1), because the boundary is `now+2h`. Advance the clock past the boundary to `now+3h`; `Get` again — assert the encoder count is now 2 (a re-encode happened) and the rebuilt body reflects the post-boundary state.
2. **AC5 — rebuild discards held bodies.** `Get` once (encoder count 1); set the generation func to a new value; `Get` again — assert the encoder count is 2 and the cache's `entries` map holds exactly one entry. This is the regression test for the generation-discard path prompt 1 built. AC5 is re-asserted here deliberately — the spec's decomposition assigns it to row 2 — rather than silently duplicating prompt 1's requirement 9 test 4.
3. **A body with no clock-derived rows never expires.** A fixture whose rows have no `DeferDate` and are not completed: assert `taskBodyBoundary` returns the zero time, and two `Get` calls at clocks a day apart perform exactly one encode.
4. **The boundary is the earliest transition.** A fixture with a deferred row and a completed row whose candidate instants differ: assert `taskBodyBoundary` returns the smaller one, and that advancing just past it rebuilds while advancing to just before it does not.
5. **A boundary already in the past is not a candidate.** A deferred row whose `d` and `d-upcoming_hours` are both `<= now`: assert those instants are excluded from the minimum.
6. **The window entry is invalidated when a hidden row becomes visible.** A row whose `defer_date` is beyond `now + upcoming_hours`, so `visibleRow` drops it and it is absent from the served body, and whose `defer_date - upcoming_hours` is the minimum candidate among all rows: `Get` once (encoder count 1); advance the clock across that instant; `Get` again — assert the encoder count is 2 (a re-encode happened) and the newly-visible row now appears in the served body. This is the case the projected responses can never cover, because the dropped row is absent from them.

Also assert `LookbackHours` is still `8` and `DefaultUpcomingHours` is still `8`.

### 5. No timer, and nothing else changes

- Do NOT add a `time.Ticker`, `time.AfterFunc`, `time.Tick`, a background sweep, a flush loop or a configurable interval anywhere in `pkg/board`.
- The boundary is derived from the request's clock reading and `upcoming_hours`, never from a fixed duration.
- The boundary must be computed over the snapshot's pre-filter rows, not the projected responses, precisely because the row whose entry instant matters is the one `visibleRow` drops — it is absent from the responses and so can never contribute a candidate there.
- `ListTasks`, `ListAssignees`, `ListTasksBody` and `taskSnapshotStore.List` keep their signatures and behaviour.
- The identity and gzip bytes are unchanged; only when they are rebuilt changes.

### 6. Coverage and self-check

`pkg/board` must keep `>= 80 %` statement coverage. Before finishing, re-run every `<verification>` command. Walk Desired Behavior 6 and Acceptance Criteria 5 and 6 against the tests and name what establishes each. Confirm no non-test file in `pkg/board` adds a timer or a goroutine, and confirm the decoded body is unchanged.

</requirements>

<constraints>
- The clock boundary is derived from the request's `now` and `upcoming_hours` (and the built rows' dates), not from a fixed timer, and not from a background sweep.
- A discarded entry is not served again; the read falls through to a rebuild.
- A failed build still stores nothing and evicts nothing — the failure-mode contract from prompt 1 is unchanged.
- The cache still holds at most one snapshot generation's entries; a superseded generation's entries are dropped.
- The decoded JSON is unchanged: same fields, same order, same values, same `SetEscapeHTML(false)` behaviour.
- No configuration, no opt-out flag, no size limit, no time-to-live knob, no new timer. `RescanInterval` and `SessionRefreshInterval` are unchanged.
- Do NOT change the `Board` interface, `ListTasks`, `ListAssignees`, `ListTasksBody` or `taskSnapshotStore.List` signatures.
- Do NOT touch `pkg/handler/`, `docs/` or `CHANGELOG.md`; prompts 3 and 4 do those.
- Do NOT modify `pkg/board/snapshot_internal_test.go`, `pkg/board/board_test.go` or `pkg/board/rowpatch_test.go`.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
grep -n 'func taskBodyBoundary' pkg/board/taskbody.go
```
Must print one line.

```
grep -n 'expiresAt' pkg/board/taskbody.go
```
Must print at least three lines (the field, the hit check, the store).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'time.NewTicker\|time.AfterFunc\|time.Tick\|go func' pkg/board/
```
Must exit 0 (no timer and no raw goroutine in non-test board code).

```
grep -n 'LookbackHours = ' pkg/board/board.go
```
Must print `const LookbackHours = 8`.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
