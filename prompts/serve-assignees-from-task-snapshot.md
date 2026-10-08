---
status: draft
created: "2026-10-08T12:37:15Z"
---

# Serve `/api/assignees` from the precomputed task-list snapshot

<summary>
- An `/api/assignees` request no longer lists the vault; it projects the distinct assignee names from the row list that is already built.
- The endpoint therefore costs about what a warm `/api/tasks` read costs, instead of a full vault walk per selected vault.
- The JSON a browser renders is unchanged: the same names, the same case-insensitive sort, the same unassigned flag.
- `/api/assignees` and `/api/tasks` now share one row build per vault, so whichever is asked first pays for the build and the other reuses it.
- A write or a watcher event that makes `/api/tasks` rebuild makes `/api/assignees` see the new rows too, because both read the same published snapshot.
- A slow vault read is bounded by the store's existing build timeout rather than hanging the request indefinitely.
- No new cache, no new store, no new timer, no new interface and no new configuration knob is introduced.
</summary>

<objective>
Serve `GET /api/assignees` from the task-list snapshot that `GET /api/tasks` already reads, so the endpoint stops walking the vault on every request while returning exactly the JSON it returns today.
</objective>

<context>
Read `docs/dod.md` and `CLAUDE.md` at the repo root.

Read these files before changing anything:
- `pkg/board/vaults.go` — `ListVaults`, `ListAssignees`, `selectVaults`, `findVault`.
- `pkg/board/tasks.go` — `ListTasks` (the target shape) and `buildTaskRows` (the builder).
- `pkg/board/snapshot.go` — `taskSnapshotStore`, `taskSnapshotRow`, `taskSnapshotStore.List`.
- `pkg/board/board.go` — `Board`, `board`, `Deps`, `New`.
- `pkg/board/board_test.go` — `newHarness`, `harness.build`, `fakeList`, `listCounter`, `fakeIndex`, `fakeProbe`, and the existing `ListAssignees` spec.
- `pkg/handler/api_vaults.go` — `NewAssigneesHandler` (unchanged by this prompt).
- `pkg/api/api.go` — `AssigneesResponse`.
- `docs/page-index.md`, `docs/optimistic-writes.md` — the task-list snapshot's rebuild triggers, staleness bounds and write-visibility chain, which this read path inherits.

Pattern reference — this change is the same shape as the one that moved `/api/tasks` onto the store:
- `prompts/completed/145-spec-028-board-read-path.md`, `<requirements>` § 3 "`ListTasks` serves from the store".

This prompt carries no `spec:` frontmatter deliberately: it is a standalone follow-up rather than part of spec 028, whose Goal and Non-goals are scoped to `/api/tasks` alone. The spec's Failure Modes and Security tables still describe the mechanism this change inherits, which is why requirement 3 tests 3 and 4 mirror them.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
</context>

<requirements>

### 1. Confirm the two call sites list the same rows

`pkg/board/vaults.go` `ListAssignees` calls, once per selected vault:

```go
b.ops.List(vault).Execute(ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "")
```

`pkg/board/tasks.go` `buildTaskRows` calls `b.ops.List(vault).Execute` with the **same eight arguments**. Read both call sites and confirm that they are identical before changing anything — the change below is only correct because the snapshot therefore publishes exactly the rows this walk returns. If they are not identical, stop and report the difference instead of proceeding.

### 2. `ListAssignees` reads the snapshot

In `pkg/board/vaults.go`, replace the per-vault walk with the published rows. Change only the per-vault fetch and its inner loop; keep `b.vaults.Vaults(ctx)`, `b.selectVaults`, the `namedSet`/`hasUnassigned` accumulation, the case-insensitive `sort.Slice` and the returned `api.AssigneesResponse` exactly as they are.

```go
	for _, vault := range selected {
		rows, snapshotErr := b.snapshot.List(ctx, vault)
		if snapshotErr != nil {
			return api.AssigneesResponse{}, snapshotErr
		}
		for _, row := range rows {
			if strings.TrimSpace(row.item.Assignee) != "" {
				namedSet[row.item.Assignee] = true
			} else {
				hasUnassigned = true
			}
		}
	}
```

Notes on the contract:
- `row.item` is the `ops.TaskListItem` the builder stored; `Assignee` is the same field the walk read, so the name set and the `hasUnassigned` flag are identical by construction.
- Return the store's error directly, as `ListTasks` does at `pkg/board/tasks.go`. The previous `errors.Wrapf(ctx, listErr, "list assignees for vault %s", vault.Name)` is replaced — the store's own error already names the vault. Nothing asserts the old message; confirm that with a grep before deleting it.
- The failure path changes shape, deliberately and in `/api/tasks`'s favour: when a rebuild fails and a snapshot was already published, `taskSnapshotStore.List` returns the previous rows and a nil error, so `/api/assignees` serves the stale-but-valid list with a 200 instead of the 500 the vault walk produced. A cold key with nothing published still returns the store's error. This is the contract `/api/tasks` already has; do not add an error path that diverges from it.
- Do **not** add a nil-guard for `b.snapshot`: `New` always constructs it, and a nil value is a wiring bug, not a state to tolerate.
- Do **not** add a second store, a second `List` interface, a cache, a timer, or a fallback that walks the vault when the snapshot is cold. A cold key builds through the same path `/api/tasks` uses.

`pkg/board/vaults.go` imports `context`, `sort`, `strings`, `errors` and `pkg/api`; `errors` stays because `ListVaults` uses it. Add no import.

### 3. Tests

`pkg/board/board_test.go` — `newHarness` already wires `index: &fakeIndex{}` and `sessions: &fakeProbe{}`, so the snapshot is live in the existing harness. Reuse it; do not build a second harness.

1. **The existing spec passes unchanged.** `Describe("ListAssignees")` asserts `Named` equals `[]string{"Alice", "bob"}` and `HasUnassigned` is true. Do not edit its expectations — it is the frozen contract for the name set, the sort and the flag.
2. **Warm read adds no list call.** Using `newHarness` and `h.counter`: call `ListAssignees` once, record the count; call it a second time and assert the count did not advance. This is the whole point of the change, so it must be asserted on the counter, not on the returned value.
3. **The two endpoints share one build.** On a fresh harness, call `ListTasks` then `ListAssignees` (and separately `ListAssignees` then `ListTasks`) and assert the total list-call count is 1, not 2. This is the sharing claim in `<summary>` and it fails today.
4. **A rebuild is visible to the assignees read.** After the first `ListAssignees`, mutate the harness's `fakeList` items and bump `fakeIndex` (the effect of a write's `MarkFileDirty`), then call `ListAssignees` again and assert the new assignee appears. Mirror how the existing `ListTasks` read-your-writes spec drives `fakeIndex`.
5. **The unassigned flag still comes from the rows.** With every item assigned, `HasUnassigned` is false; with one unassigned item, it is true. Assert both directions, so a projection that drops the flag cannot pass on a single case.

### 4. Docs and CHANGELOG

`docs/page-index.md` describes the task-list snapshot as the thing `GET /api/tasks` serves — line 23 reads "`GET /api/tasks` serves it." and line 49 reads "A warm `/api/tasks` read likewise returns the published task-list rows…". `/api/assignees` now serves that same snapshot, so widen both statements to name both endpoints. A third `/api/tasks` reference later in the file, in the watcher-ordering section, stays as-is: it describes the rebuild trigger, not which endpoint serves the snapshot. Change nothing else in the file.

`CHANGELOG.md` has no `## Unreleased` section today (its head is `## v0.87.3`), so add the `## Unreleased` heading if it is absent, then add one `- feat:` bullet recording that `/api/assignees` is now served from the precomputed task-list snapshot. Match the file's existing bullet style. Add nothing else to the file.

</requirements>

<constraints>
- The JSON contract of `/api/assignees` is frozen: same `named` array, same case-insensitive sort with the same tie-break, same `has_unassigned` boolean. Held by three separate guards — `pkg/board/board_test.go`'s existing spec asserts the Go fields, `pkg/handler/api_test.go` asserts the JSON keys, and `scripts/parity/cases.txt` pins the endpoint's body. Do not weaken any of them.
- The row set is not filtered, paged or reordered. `buildTaskRows` applies no request-time filter, and `ListAssignees` must keep consuming the full row list.
- No new store, no new timer, no new goroutine, no new `Deps` field, no new config knob. The snapshot's build is bounded by the store's existing `snapshotBuildTimeout`.
- A published snapshot is never mutated; `go test -race ./pkg/...` must stay clean.
- Response bodies and success-path status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract). The failure path inherits `/api/tasks`'s stale-on-error 200, as requirement 2 states — that is not a parity break, because no `/api/assignees` error case is pinned in `scripts/parity/`. No route is added to `:8000` or `:9090`. `pkg/handler` is not changed by this prompt.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, Ginkgo/Gomega tests, ≥ 80 % coverage on the changed package.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`; do not use `-mod=vendor`.
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
go test -race -count=1 ./pkg/...
```
Must pass.

```
go test -race -count=1 ./pkg/board/... ./pkg/factory/... ./pkg/handler/...
```
Must pass — the factory's warm-read no-page-storage assertions and the handler parity tests are the frozen contract.

```
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80 %).

```
grep -q 'rows, snapshotErr := b.snapshot.List(ctx, vault)' pkg/board/vaults.go
```
Must exit 0 — the assignees path reads the snapshot.

```
! grep -q 'b.ops.List' pkg/board/vaults.go
```
Must exit 0 — the per-request vault walk is gone from this file.

```
! grep -rq 'list assignees for vault' pkg/
```
Must exit 0 — the replaced error wrap is gone.

```
! grep -rnE 'NewTicker|time\.AfterFunc' pkg/board
```
Must exit 0 — no timer was introduced. This passes on the unmodified tree as well, so it is a regression guard against a polling refresher, not evidence that this change happened.

```
grep -q '/api/assignees' docs/page-index.md && grep -q '^## Unreleased' CHANGELOG.md
```
Must exit 0 — requirement 4 landed: the doc names both endpoints, and the changelog carries the `## Unreleased` heading. Without this, requirement 4 is unverified: no other command in this block reads `docs/page-index.md` or `CHANGELOG.md`.

```
grep -c 'It(' pkg/board/board_test.go
```
Must print a count at least 3 higher than the count on the unmodified file (4 if every new case is written as an `It(`) — record both numbers. Together with the doc grep above, this pair covers requirement 3's new tests and requirement 4, while the three code greps above detect requirement 2's change. `make precommit`, the test runs, the coverage floor and the timer grep all pass on the unmodified tree — those are regression gates, not evidence that this change happened. This count is a shape heuristic in both directions: it also passes on unrelated `It(` lines, and it reads low if you write the new cases as `DescribeTable`/`Entry`. The real behavioural evidence is the counter assertions in requirement 3 tests 2 and 3, which no command in this block can confirm for you.

Before finishing, re-run every command above and confirm it passes, then walk `<summary>` bullet by bullet against the tests you wrote and name the test that establishes each. Bullets 1 and 2 are established by requirement 3 test 2 (the counter does not advance on a second read); bullet 3 by test 1 (the existing spec's exact `Named`/`HasUnassigned` values) and test 5 (the flag in both directions); bullet 4 by test 3 (one build serves both endpoints); bullet 5 by test 4 (a revision bump is visible to the assignees read). Bullet 6 is inherited unchanged from the store's `snapshotBuildTimeout` and needs no new test. Bullet 7's no-timer clause is established by the timer grep above; its no-cache, no-store, no-interface and no-knob clauses are established by the diff itself, which adds no such declaration.
</verification>
