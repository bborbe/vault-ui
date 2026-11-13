---
status: completed
spec: [039-mode-pill]
created: "2026-11-12T12:15:00+01:00"
queued: "2026-11-12T11:30:28Z"
---

# Serve the page's mode on the task, goal and topic payloads

<summary>

- Every board payload can now say whether a row starts a headless worker or an interactive session
- The value is read from the page's own mode field, never from a default
- A page that declares no mode serves null, so the key is always present but carries no value
- Goal and topic payloads carry the same key, read from their own pages
- Goals gain a page read they never had before, one listing per vault rather than one per row
- An empty mode value counts as absent, so it never reaches the wire as a blank string
- Two pinned payload key lists move with the change, so a key added without them fails a test instead of shipping silently
- Nothing else the board serves changes: every existing key keeps its name, its meaning and its value
- No frontend, documentation, fixture or changelog change in this prompt — the card that renders the value is the next step

</summary>

<objective>

Make the per-row execution mode the operator already writes on disk reachable on the wire, so the card can show it. Today the choice is recorded in a page's `mode:` frontmatter at creation and is unreadable from the API — the board parser ignores it and no payload carries it. This prompt ends with `mode` on all three list payloads, taken from each page's own value and null where the page declares none.

</objective>

<context>

Read these before writing code.

`pkg/board/tasks.go` — the exemplars this change copies:
- `pageJira` reads one page frontmatter key as a nullable string, nil for empty. `pageMode` is its sibling and must be shaped identically.
- `taskPageFields` is the per-task page state (`openQuestions`, `jira`); `taskPageFieldsByTask` fills it from one `pageIndex.ListPages` per vault, degrades to an empty map when `b.pageIndex` is nil or the listing fails, and stops on `ctx.Done()` inside the page loop; `taskPageFieldsFor` returns the zero value for a page that is not in the map.
- `taskRow` is the per-request row; `ListTasks` copies the snapshot row's page-derived fields onto it (`visibleTask.jira = row.jira` and friends); `taskResponse` renders the wire struct from that row.
- `buildTaskRows` attaches the page fields to each `taskSnapshotRow`; `patchTaskRows` re-derives a changed page's row directly from the page (`jira: pageJira(page)`).

`pkg/board/snapshot.go` — `taskSnapshotRow` is the immutable published row. Adding a field here is what makes the value survive a cached read without touching a page.

`pkg/board/topics.go` — `topicPageFieldsByTopic` currently returns `map[string]*string` (jira only); `ListTopics` reads it as `pageJiraByTopic[item.Name]`.

`pkg/board/goals.go` — `goalsForVault` lists a vault's goals and filters them; `goalResponse` renders one row from `ops.TaskListItem` and has exactly one caller. Goals read no page today.

`pkg/api/api.go` — the `*string` plus plain `json:"<key>"` convention with a doc comment, and the explicit "always emitted (no omitempty)" wording: `TaskResponse.Jira`, `TaskResponse.Location`, `GoalResponse.Flag`, `TopicResponse.Phase`, `TopicResponse.Jira`.

`pkg/board/board_test.go` — the `harness` (`newHarness`, `h.list.items["23 Goals"]`, `h.pageIndex.ListPagesReturns`), the `vault` fixture (tasks folder `24 Tasks`, goals folder `23 Goals`, topics folder `23 Topics`), `pageWithFrontmatter`, and the existing "serves the topic payload field set" example, which marshals the responses and compares a sorted key slice.

`pkg/board/parity_test.go` — the pinned task key list; the test decodes `ListTasksBody`'s identity bytes into `[]map[string]json.RawMessage` and compares the sorted key slice exactly.

`docs/page-index.md` — the one-snapshot-per-key read model the per-vault `ListPages` call relies on; a page-derived field costs no extra vault walk.

`docs/frontend-card-render.md` — read only, to see what the next prompt will consume. Do not edit it here.

Conventions: `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` for the `errors.Wrapf` style the helpers use, and `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` for the Ginkgo/Gomega shape the board tests use.

</context>

<requirements>

1. Add `pageMode` in `pkg/board/tasks.go`, directly beside `pageJira`, shaped exactly like it — a page's `mode:` frontmatter value as a nullable string, verbatim and unparsed, nil for empty:

```go
// pageMode returns a page's `mode:` frontmatter value as a nullable string,
// verbatim and unparsed: in the vaults the field is free-form, and an empty
// value reads as absent — the same nil-for-empty shape pageJira uses.
func pageMode(page *domain.Page) *string {
	value := page.GetString("mode")
	if value == "" {
		return nil
	}
	return &value
}
```

The empty-to-nil rule is load-bearing: a page carrying `mode:` with no value must serve `null`, never `""`.

2. Add `Mode *string` with the tag `json:"mode"` and **no** `omitempty` to all three wire structs in `pkg/api/api.go` — `TaskResponse`, `GoalResponse`, `TopicResponse`. Append it as the struct's last field with a doc comment in the same voice as the neighbouring ones:

```go
	// Mode is the page's own `mode:` frontmatter value — the execution mode the
	// row opens with. It is carried verbatim because the field is free-form on
	// disk, and the key is always emitted (no omitempty), so a page declaring no
	// mode reads back as "mode": null rather than vanishing from the payload.
	Mode *string `json:"mode"`
```

A plain `string` field, a `string` with a default, or a tag carrying `omitempty` all break the null half of the contract.

3. Thread the task path end to end:
   - `taskPageFields` (tasks.go) gains `mode *string`; `taskPageFieldsByTask` sets `mode: pageMode(page)` in the same `byName[...]` write that sets `jira`, off the same single listing. `taskPageFieldsFor` is unchanged.
   - `taskSnapshotRow` (snapshot.go) gains `mode *string`; `buildTaskRows` sets `mode: fields.mode` in the row literal.
   - `patchTaskRows` sets `mode: pageMode(page)` in the patched row literal, beside `jira: pageJira(page)`. The patch path re-derives from the page it is handed, so a page edit that changes `mode:` must show on the next read rather than waiting for a full rebuild.
   - `taskRow` (tasks.go) gains `mode *string`; `ListTasks` copies `visibleTask.mode = row.mode` beside `visibleTask.jira = row.jira`.
   - `taskResponse` sets `Mode: row.mode`.
   - Add `Entry("mode", func(row *taskSnapshotRow) { row.mode = strPtr("headless") })` to the rendered-row `DescribeTable` in `pkg/board/taskbody_internal_test.go` ("a change to any rendered row field discards the held body"). `mode` is now a rendered row field, so without an entry that table's own claim stops being true.

4. Widen the topic page read. Replace the bare `map[string]*string` return of `topicPageFieldsByTopic` with a struct that carries both keys, so one listing serves both:

```go
// pageKeyFields is the per-page state the topic and goal boards derive from a page
// that their list rows do not carry: the page's `jira:` and `mode:` frontmatter
// values. The goal board reads only `mode` — a goal payload carries no jira key.
// Named pageKeyFields, not pageFields: tasks.go:244 already declares a local
// `pageFields` variable, and a package-scope type of that name would shadow it.
type pageKeyFields struct {
	jira *string
	mode *string
}
```

`topicPageFieldsByTopic` returns `map[string]pageKeyFields`, filled with `pageJira(page)` and `pageMode(page)` from the same listing, keeping its nil-`pageIndex` guard, its warning-and-degrade on a listing error and its `ctx.Done()` check inside the loop. `ListTopics` reads the entry once (`fields := fieldsByTopic[item.Name]`) and serves `Jira: fields.jira` and `Mode: fields.mode`.

5. Give the goal board its own page read. Add `func (b *board) goalPageFieldsByGoal(ctx context.Context, vault Vault) map[string]pageKeyFields` — one `b.pageIndex.ListPages(ctx, vault.Path, vault.GoalsFolder)` per vault, with the same nil-guard, warning-and-degrade and `ctx.Done()` handling as `topicPageFieldsByTopic`. Do not reuse the topic helper: it lists the topics folder. Do not list pages per row. It is a method on `*board` because it needs `b.pageIndex`, like both siblings.

`goalsForVault` calls it once per vault, before its response loop, and passes the row's mode into `goalResponse`. `goalResponse` gains a `mode *string` parameter (its only caller is `goalsForVault`) and sets `Mode: mode`. A goal page that is absent from the map carries nil, which serialises as `null`.

6. Task payload test (AC1) — extend `Describe("ListTasks")` in `pkg/board/board_test.go`:
   - Register two rows, e.g. `item("WithMode")` and `item("NoMode")`, and return two pages from `h.pageIndex.ListPagesReturns`: `pageWithFrontmatter("WithMode", map[string]any{"mode": "headless"}, "")` and a page named `NoMode` whose frontmatter carries no `mode` key.
   - Call `ListTasks`, `json.Marshal` the responses, decode into `[]map[string]json.RawMessage`, and assert per row that the `mode` key **is present** in the map and that its raw value is `"\"headless\""` for `WithMode` and `"null"` for `NoMode`.
   - Decode into the raw map rather than asserting on `responses[0].Mode`: the criterion is the key always present and null when undeclared, and a struct assertion cannot tell a missing key from a null one.
   - Add a second case for a page carrying `mode: ""` serving `null`, so the empty-reads-as-absent rule (requirement 1) is locked rather than assumed.
   - **Add a patch-path case**, beside `rowpatch_test.go`'s "re-derives the Jira value of a changed page from its own content": `h.index.setPages(pageWithFrontmatter("Alpha", map[string]any{"status": "todo", "mode": "headless"}, ""))`, then `h.index.markPages("Alpha.md")`, then assert the patched response's `Mode` is `"headless"`. Requirement 3 changes `patchTaskRows`, and the full-build cases above never enter that path — a regression that omits `mode` from the patched row literal would pass every other test here.

7. Goal and topic payload tests (AC2) — same file, same marshal-and-decode shape, asserting the **value**, not merely the key:
   - Goals: `h.list.items["23 Goals"]` holds two goals, one page carries `mode: headless` and the other carries none; `ListGoals` serves `"mode": "headless"` and `"mode": null`. Also assert `h.pageIndex.ListPagesCallCount()` is 1 for that read with two rows, so a per-row page read fails here instead of in production.
   - Topics: `h.list.items["23 Topics"]` holds two topics, one page carries `mode: headless` and the other carries none; `ListTopics` serves `"mode": "headless"` and `"mode": null`.
   - Keep the existing "round-trips assignee, priority, jira and the open blockers" topic example green — its page carries only `jira`, so it must now serve `"mode": null` without the assertion changing.

8. Move both pinned key lists (AC3), each asserted exactly — add the one key, delete or rename nothing:
   - `pkg/board/parity_test.go`, the sorted task slice: insert `"mode"` between `"location"` and `"modified_date"` (the list grows from 32 to 33 entries).
   - `pkg/board/board_test.go`, the "serves the topic payload field set" sorted slice: insert `"mode"` between `"jira"` and `"obsidian_url"` (14 to 15 entries).
   Both lists are alphabetical; a key appended at the end fails on sort order.

9. Do not add a goal equivalent of those lists. AC2's goal test is the goal lock.

10. Before finishing, re-run `<verification>` and confirm it exits 0; then walk requirements 1–9 and the spec's AC1, AC2 and AC3 against the change, naming for each the test that proves it.

</requirements>

<constraints>

- `Mode` is `*string` with `json:"mode"` and **no** `omitempty` on all three structs — the key is always emitted, `null` when the page declares none. A plain `string` with a default would print a value on every row and break AC1.
- The other 32 task keys and 14 topic keys are unchanged, and the goal payload gains exactly one key.
- Page-derived fields come from **one `pageIndex.ListPages` per vault**, never one per row. Goals use the goals folder, topics the topics folder, tasks the tasks folder.
- The mode value is free-form: do not validate it, normalise it, enumerate it, lowercase it, or map it to a default. Do not add an enum or a constant list.
- Do not add `mode:` to any goal or topic page, and write no vault content — this change reads pages only.
- Do not modify `src/vault_ui/static/app.js`, `pkg/board/testdata/card-vectors-pre-change.json`, anything under `pkg/board/jsasset/`, `docs/`, `README.md` or `CHANGELOG.md`. The card slot, the fixture extension and the documentation belong to the next prompt in this spec's decomposition, and the fixture must move in the same change as the card config — not here.
- No new endpoint, query parameter, CLI flag or write path; no new dependency.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`. Run every command below with `export PATH=/usr/local/go/bin:$PATH` first. (`ROOTDIR` is not read by this repo's Makefile, so a `ROOTDIR=/workspace` prefix is inert.)

`make precommit` — must exit 0. It runs `go fmt ./...`, `go vet ./...` and `go test -race ./...`, which covers the task, goal and topic payload tests and both pinned key lists.

</verification>
