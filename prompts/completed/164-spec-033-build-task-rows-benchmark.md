---
status: completed
spec: [033-measure-task-list-rebuild-path]
summary: Added pkg/board/bench_internal_test.go with BenchmarkBuildTaskRows — ten sub-benchmarks (eight rebuild sections plus a genuine end-to-end `whole` and a named `residual`) over a 2,500-item in-memory fixture — and recorded the raw -benchmem output in docs/task-list-rebuild-cost.md
execution_id: vault-ui-rebuild-fix-exec-164-spec-033-build-task-rows-benchmark
dark-factory-version: v0.198.7
created: "2026-10-14T12:29:16Z"
queued: "2026-10-14T13:02:28Z"
started: "2026-10-14T13:37:07Z"
completed: "2026-10-14T13:51:16Z"
branch: dark-factory/measure-task-list-rebuild-path
---

# Add `BenchmarkBuildTaskRows`, one sub-benchmark per section of the rebuild

<summary>
- A new benchmark in the board package measures the allocation of the task-list rebuild, section by section, on a fixture of at least 2,000 task items.
- The sections are the vault list, the row construction, the blockers, the session classification, the session-started marker, the registry location, the activity date, and the Open Questions section.
- The whole-rebuild figure is a genuine end-to-end `buildTaskRows` call, never composed from the section helpers, so the comparison cannot be true by construction.
- A named `residual` sub-benchmark reports the allocations the whole function makes that no section models, so the attribution is a split of the cost and not a list of names.
- The benchmark needs no real vault and runs inside the container.
- The raw `-benchmem` output, including the residual figure, is recorded in the rebuild-cost document as the benchmark's first run.
- No numeric tolerance is asserted, and no band is invented.
- No production code changes: this prompt adds one internal test file and appends to one document.
</summary>

<objective>
Add `BenchmarkBuildTaskRows` to `pkg/board`, one sub-benchmark per section of `buildTaskRows` plus a whole-rebuild sub-benchmark and a named `residual`, on a fixture of at least 2,000 task items, and record its raw `-benchmem` output in `docs/task-list-rebuild-cost.md`. This is prompt 3 of 4; the document landed in prompt 1 and the counters in prompt 2, and the regression fence follows in prompt 4. It touches no production code.
</objective>

<context>
Read the spec `specs/in-progress/033-measure-task-list-rebuild-path.md` in full before writing. This prompt covers Desired Behavior 3 and Acceptance Criterion 3. Acceptance Criteria 5 and 7 are the spec's operator rung and are **not** this prompt's scope.

Read these files fully before writing:
- `pkg/board/tasks.go` — `buildTaskRows` (the function the benchmark attributes), and the helpers it calls: `b.uncompletedBlockers`, `b.sessionStarted`, `b.openQuestionsByTask`, `openQuestionsFor`, `b.transcriptProbe`.
- `pkg/board/board.go` — `Deps` (every field the fixture must supply) and `New` (which returns a `Board` whose dynamic type is the unexported `*board`).
- `pkg/board/board_test.go` — the existing external-package fakes (`fakeVaults`, `fakeList`/`fakeOps`, `fakeCache`, `fakeSignals`, `fakeProbe`, `fakeIndex`, the `harness`) and the `item(...)` helper. This file is `package board_test`, so its fakes are **not** importable from the benchmark's package; write local equivalents in the benchmark file.
- `pkg/board/snapshot_internal_test.go` — the existing internal-package test file (`package board`), so the benchmark file follows the same package and can call `buildTaskRows` and the section helpers directly.
- `docs/task-list-rebuild-cost.md` — the document created by prompt 1; § *Benchmark attribution* is the placeholder this prompt fills.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-licensing-guide.md` (the new file starts with the copyright header)
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

Verified signatures you must use (read from the source above; do not invent):
- `func (b *board) buildTaskRows(ctx context.Context, vault Vault) ([]taskSnapshotRow, error)`
- `func (b *board) uncompletedBlockers(vaultName string, blockedBy []string) []string`
- `func (b *board) sessionStarted(vaultName, itemID string) *string`
- `func (b *board) openQuestionsByTask(ctx context.Context, vault Vault) map[string][]domain.OpenQuestion`
- `func openQuestionsFor(byName map[string][]domain.OpenQuestion, name string) []domain.OpenQuestion`
- `func (b *board) transcriptProbe() activity.TranscriptMtimeGetter`
- `session.ClassifySessionState(ctx context.Context, params session.ClassifyParams) session.SessionState`, with `session.ClassifyParams{SessionID, ProjectDir, ProjectsRoot, Now libtime.DateTime, LiveWindow time.Duration, ResumeSessionIDs, RegistrySessionIDs []string, LiveIDsUnknown bool, TranscriptMtime activity.TranscriptMtimeGetter}`
- `activity.ComputeActivityDateWith(ctx context.Context, probe activity.TranscriptMtimeGetter, modifiedDate *libtime.DateTime, sessionID, projectDir, projectsRoot string) *libtime.DateTime`
- `cleanup.DeriveClaudeProjectDir(homeDir, vaultPath, sessionProjectDir string) string`
- `parseDateTime(value string) *libtime.DateTime`
- `func New(deps Deps) Board` — the returned `Board`'s dynamic type is `*board`, so `New(Deps{...}).(*board)` is the way to reach `buildTaskRows`.
- The board's `OpsProvider` is `List(vault Vault) ops.ListOperation`, and `ops.ListOperation` is satisfied by a type with a single `Execute(ctx context.Context, vaultPath, vaultName, pagesDir string, statuses []string, showAll bool, sortBy, sortOrder string) ([]ops.TaskListItem, error)` method (proved by `fakeList` in `pkg/board/board_test.go`).
- `libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime)` satisfies `libtime.CurrentDateTimeGetter`.
- `pageindexmocks "github.com/bborbe/vault-ui/pkg/pageindex/mocks"` provides a generated `PageIndex` mock with `ListPagesReturns(pages, nil)`; use it for the Open Questions seam.
- `statuscache.Cache` has exactly `LoadVault`, `GetStatus`, `GetSessionStarted`, `Count`, `Invalidate`.
- `board.SessionSignals` has exactly `RegistrySessionIDs`, `RegistryLocation`, `LiveIDsKnown`, `ResumeSessionIDs`; `board.SessionProbe` has exactly `TranscriptMtime`, `Generation`.
</context>

<requirements>

### 1. Create `pkg/board/bench_internal_test.go` (`package board`)

Create the file with the copyright header. It holds the fixture, the local fakes the fixture needs, and the benchmark. Do **not** modify any existing file in `pkg/board` except the document (requirement 5).

### 2. The fixture and the local fakes

Build a fixture of **at least 2,000** `ops.TaskListItem`s, each carrying the fields the sections read, so every section does real work: a distinct `Name`, a `Status`, a `BlockedBy` list naming two other fixture items (one `completed`, one not), a `ClaudeSessionID`, and a `ModifiedDate`. The fixture must require **no real vault and no filesystem**: every dependency is an in-memory fake.

Write local fakes in the benchmark file mirroring the shapes in `pkg/board/board_test.go` (do not import that package's fakes):

- a `VaultsProvider` returning the single fixture `Vault` (path `/vault`, tasks folder `24 Tasks`);
- an `OpsProvider` whose `List` returns an `ops.ListOperation` whose `Execute` returns the fixture items (ignoring the pages dir);
- a `statuscache.Cache` whose `GetStatus` returns `"completed"` for the fixture's blocking item and `"todo"` otherwise, and whose `GetSessionStarted` returns a fixed marker for one item;
- a `board.SessionSignals` returning a fixed registry id set (matching one fixture item), a fixed location, `LiveIDsKnown() == true`, and a fixed resume set;
- a `board.SessionProbe` returning a fixed transcript mtime for the matching session id and a fixed generation;
- a `launchregistry.Registry` (`launchregistry.NewRegistry()`) — `sessionStarted` calls `b.launch.State(vaultName, itemID)`, so a nil `Launch` in `Deps` panics on the first `whole` iteration;
- a `board.IndexRevisions` (the store's input; unused by `buildTaskRows` but required by `Deps`) returning a fixed revision, no changed pages, and no pages;
- a `pageindexmocks.PageIndex` whose `ListPagesReturns(pages, nil)` returns a page per fixture item, each carrying a small `## Open Questions` section, so the Open Questions section does work;
- `HomeDir` set to a fixed non-existent path (no filesystem access happens; `cleanup.DeriveClaudeProjectDir` is pure string work), and a `libtime.CurrentDateTimeGetterFunc` returning a fixed instant.

Construct the board once per benchmark with `benchBoard(tb testing.TB) *board` returning `New(Deps{...}).(*board)`; the helper must not itself be timed.

### 3. `BenchmarkBuildTaskRows` — the sub-benchmarks

`BenchmarkBuildTaskRows(b *testing.B)` runs the sub-benchmarks below with `b.Run(name, ...)`. Every sub-benchmark calls `b.ReportAllocs()` and resets the timer after its fixture setup. The fixture (items, pages, the constructed `*board`, and the derived `projectDir`/`projectsRoot`) is built **once** outside the timed loop of each sub-benchmark.

Exactly these ten sub-benchmarks, with these names:

1. `vault_list` — the vault list walk: `b.ops.List(vault).Execute(ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "")` once per iteration, discarding the result.
2. `row_construction` — the per-row construction the loop performs outside the section helpers: allocate `make([]taskSnapshotRow, 0, len(items))` and append one `taskSnapshotRow{item: item, vault: vault}` per fixture item.
3. `blockers` — `b.uncompletedBlockers(vault.Name, item.BlockedBy)` for every fixture item.
4. `session_classification` — `session.ClassifySessionState(ctx, params)` for every fixture item, with `params` built exactly as `buildTaskRows` builds it (the same `ProjectDir`, `ProjectsRoot`, `Now`, `LiveWindow`, resume/registry ids, `LiveIDsUnknown`, and `TranscriptMtime: b.transcriptProbe()`).
5. `session_started` — `b.sessionStarted(vault.Name, item.Name)` for every fixture item.
6. `registry_location` — `b.signals.RegistryLocation(ctx, item.ClaudeSessionID)` for every fixture item.
7. `activity_date` — `activity.ComputeActivityDateWith(ctx, b.transcriptProbe(), parseDateTime(item.ModifiedDate), item.ClaudeSessionID, projectDir, projectsRoot)` for every fixture item.
8. `open_questions` — `openQuestionsFor(byName, item.Name)` for every fixture item, where `byName` is built **once per iteration** with `b.openQuestionsByTask(ctx, vault)` exactly as `buildTaskRows` builds it.
9. `whole` — a **genuine end-to-end `b.buildTaskRows(ctx, vault)` call** once per iteration, discarding the result. This figure must never be composed from the section helpers.
10. `residual` — the allocations the whole function makes that no section models: the per-call setup hoisted out of the per-task loop (`cleanup.DeriveClaudeProjectDir(...)`, `filepath.Join(b.homeDir, ".claude", "projects")`, and the `b.signals.RegistrySessionIDs`/`LiveIDsKnown`/`ResumeSessionIDs` reads), plus the row slice's own backing array (`make([]taskSnapshotRow, 0, len(items))`). Perform these once per iteration and discard the results.

The eight sections plus the residual are what account for the whole-rebuild figure; report all ten figures separately. Do not assert a numeric relationship between them anywhere.

### 4. What the benchmark must not do

- Do not assert a numeric tolerance, a band, or a sum-to-the-whole. Acceptance Criterion 3 deliberately asserts none of those: an isolated benchmark differs from the whole in shared allocations, escape and inlining, so a meaningful band is a number no correct implementation is known to produce. The falsifier is the residual figure, recorded side by side with the section figures. The one numeric property the criterion **does** assert is clause (i) below — the eight sections' `B/op` **sum is non-zero** on this fixture — which is a coarse non-zero test on a sum, not a tolerance and not a sum-to-the-whole.
- Do not require a real vault, a real page index, a process spawn, or the filesystem.
- Do not make the whole-rebuild figure a composition of the section helpers.
- Do not add a single top-level benchmark with no sub-benchmarks — that attributes nothing.
- Do not skip any of the eight sections; a set of empty shells does not satisfy the criterion.

### 5. Record the raw output in `docs/task-list-rebuild-cost.md`

Run the benchmark with `-benchmem` and paste its **raw output verbatim** (the `go test` benchmark lines, including `allocs/op` and `B/op` for every sub-benchmark and the `residual` figure) into § *Benchmark attribution*, replacing prompt 1's placeholder. Add one sentence recording that the fixture is 2,000+ task items, that the whole-rebuild figure is a genuine end-to-end `buildTaskRows` call, and that the sections plus the residual account for it. Do not edit any other section of the document, and in particular do not fill in § *Live reading and verdict*.
</requirements>

<constraints>
- This spec changes no behaviour: the benchmark and the document are the only additions. Do not change the invalidation, the rebuild's choice of path, the per-task work, the decoded JSON, the wire shape, the body cache, the page index's parse model or its store, or any endpoint.
- Do **not** trim or weaken the per-task work the benchmark measures; do not add a cache, a memo, a timer, a configuration key or a scenario.
- The benchmark file is `package board` (it calls the unexported `buildTaskRows` and section helpers). Do not export a helper to the external test package for it.
- The fixture must be at least 2,000 task items and must need no real vault.
- The whole-rebuild figure must be a genuine end-to-end `buildTaskRows` call, never composed from the section helpers.
- **No numeric tolerance is asserted**, and no band is invented; the band is established by this benchmark's first run and recorded in the document.
- Do not modify any existing test file in `pkg/board` or `pkg/pageindex`; do not change any existing `It(...)` body.
- Do not add an operator command (a deploy, a restart, a live metric read) to the document or to this prompt's `<verification>`.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. The new file carries the copyright header.
- Do not commit — dark-factory handles git.
- Existing tests must still pass, with their assertions unchanged.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/board -run=NONE -bench=BenchmarkBuildTaskRows -benchmem
```
Must run to completion and print one sub-benchmark line per name: `vault_list`, `row_construction`, `blockers`, `session_classification`, `session_started`, `registry_location`, `activity_date`, `open_questions`, `whole`, `residual`.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/board -run=NONE -bench=BenchmarkBuildTaskRows -benchmem \
  | awk '/^BenchmarkBuildTaskRows\/(vault_list|row_construction|blockers|session_classification|session_started|registry_location|activity_date|open_questions)/ {for(i=1;i<=NF;i++) if($i=="B/op") s+=$(i-1)} END{print "section B/op sum:", s+0; exit !(s>0)}'
```
Must print a non-zero section `B/op` sum and exit 0 (AC3 clause (i): a set of empty shells sums to about zero).

```
grep -n 'func BenchmarkBuildTaskRows' pkg/board/bench_internal_test.go
```
Must print one line.

```
grep -c 'b.Run(' pkg/board/bench_internal_test.go
```
Must print at least `1` — the ten sub-benchmarks are proved by the output lines in the check above, not by the source shape; a table-driven or helper-mediated `b.Run` is acceptable.

```
grep -n 'buildTaskRows' pkg/board/bench_internal_test.go
```
Must print at least one line (the whole-rebuild sub-benchmark makes the real call).

```
grep -n 'residual' docs/task-list-rebuild-cost.md
```
Must print at least one line (the raw output is recorded and names the residual).

```
grep -n 'residual' pkg/board/bench_internal_test.go
```
Must print at least one line.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/...
```
Must pass (the benchmark file must not break the suite).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
