---
status: approved
spec: [033-measure-task-list-rebuild-path]
created: "2026-10-14T12:29:16Z"
queued: "2026-10-14T13:02:28Z"
branch: dark-factory/measure-task-list-rebuild-path
---

# Record the change and fence the served payload

<summary>
- The CHANGELOG gains one `feat:` entry under a new `## Unreleased` heading, naming the new counters.
- A container-runnable parity probe asserts the decoded `GET /api/tasks` payload still carries exactly the same fields — nothing removed, nothing added beyond the existing `location` and `open_questions`.
- The probe is the container-side half of the spec's payload-parity check; the live half stays on the operator rung.
- The prompt confirms, and fences, that no existing test row changed its assertions.
- Nothing about how the board serves a request changes: the decoded JSON, the wire shape and every endpoint stay as they are.
- No production code changes: this prompt adds one test file and one CHANGELOG entry.
</summary>

<objective>
Record the instrumentation in the CHANGELOG and add the container-runnable regression fence that proves the served payload did not change. This is prompt 4 of 4; the document landed in prompt 1, the counters in prompt 2 and the benchmark in prompt 3. It covers Desired Behavior 5 and Acceptance Criterion 6, and adds the container-side parity probe for the payload fence.
</objective>

<context>
Read the spec `specs/in-progress/033-measure-task-list-rebuild-path.md` in full before writing. This prompt covers Desired Behavior 5 and Acceptance Criterion 6. Acceptance Criteria 5 and 7 are the spec's operator rung (a deploy, a live metric reading, and a live payload parity check against a recorded JSON) and are **not** this prompt's scope — do not write a deploy step, a `curl` to a running board, a `launchctl` command, or a live-reading row into this prompt.

Read these files fully before writing:
- `CHANGELOG.md` — the newest heading is `## v0.96.1` and there is **no** `## Unreleased` section; one must be added directly above `## v0.96.1`.
- `pkg/api/api.go` — `TaskResponse` (the wire shape of one `/api/tasks` row) and its json tags.
- `pkg/board/board_test.go` — the external-package harness (`newHarness(entries ...ops.TaskListItem) *harness`, `h.board`, and the `item(name, mutate...)` helper) the parity row reuses. This file is `package board_test`.
- `pkg/board/taskbody.go` — `TaskListBody` (`Identity []byte`, `Gzipped []byte`) and `encodeTaskList`.
- `pkg/board/tasks.go` — `ListTasksBody` (the entry point that returns the rendered body).
- `docs/page-index.md` § *Task-list snapshot* — read only, to confirm the document's claim that the wire shape is unchanged.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format, prefix, anti-patterns.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

Verified facts you may rely on (read from the source above; do not re-derive):
- `board.ListTasksBody(ctx, query)` returns `board.TaskListBody` whose `Identity` field is the JSON encoding of `[]api.TaskResponse` (an array), produced by `encodeTaskList` with `SetEscapeHTML(false)` and a trimmed trailing newline.
- `api.TaskResponse` carries exactly these thirty json fields: `id`, `title`, `status`, `phase`, `project_path`, `description`, `modified_date`, `completed_date`, `obsidian_url`, `defer_date`, `planned_date`, `due_date`, `priority`, `category`, `recurring`, `claude_session_id`, `claude_session_started`, `assignee`, `blocked_by`, `blocked`, `blockers`, `upcoming`, `recently_completed`, `vault`, `goals`, `flag`, `activity_date`, `session_state`, `location`, `open_questions`.
- `newHarness(entries ...ops.TaskListItem)` is defined in `pkg/board/board_test.go` and is usable from any `package board_test` file.
</context>

<requirements>

### 1. Add the CHANGELOG entry

Add a `## Unreleased` section directly above `## v0.96.1` in `CHANGELOG.md`, with exactly one `feat:` bullet, following `changelog-guide.md`: `- feat: <what> [context]`, one bullet per logical change, specific (name the counters and what they make visible). Do not copy shell comments from this prompt's `<verification>` section. Do not use the prompt filename as the entry. Do not edit or move the `# Changelog` preamble or any released section.

Suggested shape:

```
## Unreleased

- feat: Instrument the task-list rebuild path so the board can say what kind of rebuild ran and why — two new labelled counters on the admin metrics endpoint, `vault_ui_board_task_snapshot_rebuilds_total{kind}` (`full`, `patch`, `refresh`) and `vault_ui_board_task_snapshot_full_rebuilds_total{reason}` (`cold_key`, `both_moved`, `patch_not_wired`, `changed_set_unbounded`, `new_page_no_held_row`, `patch_page_not_listed`), classify every rebuild by the work it performed and attribute every full rebuild to its cause — including a full rebuild that fell back from inside the patch path, which is counted as a full rebuild and never as a patch; the page index gains `vault_ui_page_index_changed_pages_unbounded_total{reason}`, which reports why a changed set could not be bounded (one reason per refused bound, the earliest unbounded advance in the window) and separates a folder-level mark a write route issued outright from a `MarkFileDirty` widening and its four sub-causes; every label is pre-initialised at boot, each counter is incremented once per rebuild and never per row, and no behaviour changes.
```

### 2. Add the payload-parity probe row

Create `pkg/board/parity_test.go` in `package board_test` (with the copyright header), holding one Ginkgo `Describe` block and one row. This is the container-runnable half of the spec's payload-parity check: it proves the decoded `GET /api/tasks` row shape is unchanged, so an accidental field addition or removal on the wire fails a row.

The row must:

1. Build a board with the existing harness: `h := newHarness(item("Parity"))` (add a `ClaudeSessionID` via the `item` mutate function if the row needs a live shape; a plain item is sufficient).
2. Call `h.board.ListTasksBody(context.Background(), board.TaskQuery{UpcomingHours: 8})` and fail the spec if it errors.
3. Unmarshal `body.Identity` into `[]map[string]json.RawMessage` and fail the spec if the slice is empty.
4. Collect the key set of the first element, sort it, and assert it `Equal`s this explicit, sorted expected list:

```
activity_date, assignee, blocked, blocked_by, blockers, category,
claude_session_id, claude_session_started, completed_date,
defer_date, description, due_date, flag, goals, id, location,
modified_date, obsidian_url, open_questions, phase, planned_date,
priority, project_path, recently_completed, recurring, session_state,
status, title, upcoming, vault
```

The comparison is against the explicit list, not against `reflect`-derived tags, so that adding or removing a field on `api.TaskResponse` without updating this list fails the row.

5. Name the row exactly `It("serves the unchanged task-list payload field set")` — the `<verification>` grep below matches this string literally, so the name is required rather than suggested.

Do **not** assert the payload's bytes against a golden file, do not add a `testdata` fixture, and do not assert counter values: this row is the payload fence only.

### 3. Fence the existing rows — do not change any assertion

Every existing `make test` row in `pkg/board` and `pkg/pageindex` must keep its assertions. Do **not** modify `pkg/board/snapshot_internal_test.go`, `pkg/board/rowpatch_test.go`, `pkg/board/taskbody_internal_test.go`, `pkg/board/board_test.go`, or any `pkg/pageindex` test file, and do **not** change any existing `It(...)` body. If an existing row does not pass, report it as a blocker and stop — do **not** weaken a row to make it pass, and do **not** change production code in this prompt (the constraints forbid it, and the files involved belong to prompts 2 and 3).

The rows this fence leans on, which must pass unchanged: `builds once and serves the identical slice on a warm read`, `rebuilds when the page-index revision moves`, `rebuilds when the session generation moves`, `keeps the previous rows when a rebuild fails and retries on the next read`, `advances the published-rows signal only when a rebuild changes the rows`, `does not clear an invalidation recorded while a build was in flight`, `never mixes two published row sets under a concurrent reader`, `bounds a hung build by the timeout`, `keeps different keys independent`, `re-derives only the changed rows and never rebuilds`, `rebuilds in full for a folder-level mark`, `takes the session-only refresh when only the generation moves`, `rebuilds in full when the page revision and the generation both move`, `keeps the previous rows when a patch fails and retries the next read`, `serves only the changed pages it was given`, `re-derives only the changed row without touching the vault seams`, `carries the held session fields onto the re-derived row`, `drops the row of a changed page that is gone`, `falls back to the full build for a changed page with no held row`, `recomputes a dependent row's blockers when its blocker's page changes`, and `recomputes the session-started marker of a changed row`.

### 4. Self-check

Before finishing, re-run every `<verification>` command. Walk Desired Behavior 5 and Acceptance Criterion 6 against the row and the CHANGELOG and name what establishes each. Confirm no production `.go` file changed in this prompt and that the only new file is `pkg/board/parity_test.go`.
</requirements>

<constraints>
- This spec changes no behaviour: the decoded JSON, the wire shape, the body cache, the invalidation, the rebuild's choice of path, the per-task work, the page index's parse model and its store, and every endpoint stay as they are. This prompt adds a CHANGELOG entry and one test file only.
- Do **not** change any production `.go` file, and do **not** change any existing `It(...)` body in `pkg/board` or `pkg/pageindex`.
- Do **not** add an operator command, a `curl` to a running board, a `launchctl` command, or a "run this on the host" note to this prompt's `<verification>` or `<constraints>`. Acceptance Criteria 5 and 7 (the live reading and the live payload parity check) are the spec's Post-Deploy evidence and stay on the spec's Verification § *Operator-executable* rung. Nothing in this prompt runs against a live board.
- The parity row compares against an explicit expected field list, not a `reflect`-derived set; do not add a golden file or a `testdata` fixture.
- Do not assert counter values anywhere; Acceptance Criteria 1 and 2 deliberately assert no per-label delta.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. The new file carries the copyright header.
- Do not commit — dark-factory handles git.
- Existing tests must still pass, with their assertions unchanged.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/... ./pkg/pageindex/...
```
Must pass.

```
grep -n 'func Test\|^var _ = Describe' pkg/board/parity_test.go
```
Must print at least one `Describe` line.

```
grep -n 'serves the unchanged task-list payload field set' pkg/board/parity_test.go
```
Must print one line.

```
grep -n 'open_questions' pkg/board/parity_test.go
```
Must print at least one line (the explicit expected field list is present).

```
grep -A4 '^## Unreleased' CHANGELOG.md
```
Must show one `feat:` bullet naming `vault_ui_board_task_snapshot_rebuilds_total` and `vault_ui_page_index_changed_pages_unbounded_total`.

```
grep -c '^## v0.96.1' CHANGELOG.md
```
Must print `1` (the released heading is still present exactly once, below `## Unreleased`).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
