---
status: completed
summary: 'Task cards now badge from the task page''s own jira: frontmatter via a new Go-only TaskResponse.Jira field carried through the snapshot and patch paths, with parity, README, CHANGELOG and tests updated'
execution_id: vault-ui-jira-badge-exec-172-jira-badge-field
dark-factory-version: v0.198.7
created: "2026-10-29T14:05:00Z"
queued: "2026-10-29T15:13:53Z"
started: "2026-10-29T15:13:56Z"
completed: "2026-10-29T15:20:46Z"
branch: dark-factory/172-jira-badge-field
---

# Task cards badge from the jira frontmatter field

<summary>
- A task card's Jira badge now comes from the task page's `jira:` frontmatter field
- A task whose `jira:` value differs from the key in its title badges the field's key, for the projects the card already maps to a Jira site
- A task with no `jira:` field keeps badging from the key in its title, exactly as today
- A task with neither shows no badge, exactly as today
- The task API carries the field so the frontend can read it
- The card's own heading is unchanged: it still shows the task title, never the ticket key
- Goal cards are untouched
- The comparison against the old backend still passes, on the same footing as the other fields only the new backend provides
</summary>

<objective>
Make a task card's Jira badge reflect the task's own `jira:` frontmatter instead of guessing
it from the card title. Today the badge is title-derived only, so a task carrying
`jira: BRO-22339` under a title reading `BRO-20080 …` badges the wrong ticket, and a task
whose title carries no key shows no badge at all even when its frontmatter names the issue.
</objective>

<context>
Read `README.md` for the API surface and the board's rendering conventions, and
`docs/dod.md` for this repo's Definition of Done (the daemon's `validationPrompt`).
Note: `CLAUDE.md` is gitignored in this repo and is not present in a worktree checkout.

Read these files before changing anything:

- `pkg/api/api.go` — the wire types. Read the package doc first: it states that JSON field
  names are the contract, that the Python backend is superseded and cannot emit Go-only
  additions, and that nullable fields use pointer types so `encoding/json` emits `null`
  rather than `[]` or a missing key. `TaskResponse.Location`, `JumpPane` and
  `OpenQuestions` are the existing Go-only additions and the pattern to follow.
- `pkg/board/tasks.go` — `taskRow`, the snapshot builder that populates it, `taskResponse`,
  and above all `openQuestionsByTask` with its doc comment. That helper is the precedent
  for this change: it takes **one** `ListPages` per vault, reusing the snapshot
  `opsProvider.List` already took, and its comment explains why a second listing per task
  would put a vault walk on the board's hottest path.
- `pkg/board/snapshot.go` — `taskSnapshotRow`, the precomputed row the request path renders,
  and `rowsEqual`, the `reflect.DeepEqual` comparison that decides whether a rebuild
  republished the same rows (so a new pointer field compares by pointee, not by address).
- `pkg/board/bench_internal_test.go` — the `open_questions` benchmark (~line 409) that calls
  the helper directly and pins the one-listing cost.
- `pkg/board/parity_test.go` — its header comment and the explicit sorted field list it
  pins. It fails on any field added to `api.TaskResponse` without a deliberate edit there.
- `scripts/parity/parity.sh` — the `normalize()` function and the comment block above it
  that records, per Go-only key, why the superseded Python backend cannot emit it.
- `src/vault_ui/static/app.js` — `extractJiraIssue`, `createTaskCard`, and `createGoalCard`.
  Note precisely what `extractJiraIssue` returns: it strips the matched key out of the
  `title` it hands back, and that returned `title` is what the card renders as its heading.
- `tests/test_card_render_unify.py` — `_slice`, `_function_body`, and
  `test_task_wrapper_keeps_urgency_and_jira`.
- `docs/task-list-rebuild-cost.md` — the measurement behind the board's cost-bounded rebuild
  path, i.e. the "why" under `openQuestionsByTask`'s one-listing-per-vault rule.
- `docs/go-cutover.md` — the operator-side Python→Go cutover record, i.e. why a Go-only field
  is stripped from both sides by the parity harness rather than compared.
- `prompts/completed/171-topic-card-state-and-live-manager.md` — the most recent prompt on
  this board that added a field to a response type and to a card. Follow its shape: the
  wire field, the board population, the parity list, the frontend read, and a CHANGELOG
  entry.
</context>

<requirements>
1. `pkg/api/api.go` — add a `Jira *string` field with the JSON tag `jira` to
   `TaskResponse`. Document it on the same terms as `Location` and `JumpPane`: it is
   Go-only, the superseded Python backend cannot emit it, it is always emitted, and it is
   `null` when the task page carries no `jira:` key. Place it after `JumpPane`, with the
   other Go-only documented fields.

2. `pkg/board/tasks.go` — carry the value from the task page to the response:
   - Add a `jira *string` field to `taskRow` (`pkg/board/tasks.go`) **and** to
     `taskSnapshotRow` (`pkg/board/snapshot.go`), and copy it across in the
     snapshot-row-to-`taskRow` carry block in `ListTasks` (the block that already copies
     `jumpPane`, `location` and `openQuestions`, ~line 100).
   - `patchTaskRows` (`pkg/board/tasks.go`, ~line 439; wired as the snapshot's `Patch` in
     `pkg/board/board.go`) rebuilds a row from an explicit `taskSnapshotRow` literal
     (~line 525) that carries `sessionState`, `location`, `jumpPane`, `activityDate` and
     `openQuestions` forward from the held row. Add `jira` there too, recomputed off the
     `page` the patch already holds (~line 513) exactly as `openQuestions` is — no new
     listing, so the one-listing rule still holds. Skipping it leaves every patched row's
     `jira` nil, so the badge silently reverts to the title-derived key after any page
     write, which is the exact symptom this change exists to fix.
   - Populate it from the **same single per-vault `ListPages` snapshot** the open-questions
     path already takes. Do not add a second listing, and do not take one listing per task:
     `openQuestionsByTask`'s doc comment states the invariant and why it exists. Extend that
     helper so one listing yields both the open questions and the `jira` value; keep a doc
     comment that still explains the one-listing-per-vault rule. Prefer returning a
     per-task struct (e.g. `taskPageFields` carrying the questions and the jira pointer)
     keyed by page name over a second accessor pair, so the name matches what it yields and
     the one-listing rule stays in one place. The helper is called directly from the
     `open_questions` benchmark in `pkg/board/bench_internal_test.go` (~line 414), so update
     that call and keep the benchmark measuring the same one-listing cost. Its accessor call
     (`openQuestionsFor(byName, item.Name)`, ~line 416) goes with it if the per-task struct
     replaces the accessor pair.
   - The value is `page.GetString("jira")` off the page already in hand, converted to a
     `*string` that is nil when the string is empty — the same nil-for-empty shape the
     existing pointer fields use. Carry the string **verbatim**: in the vaults the value is
     as often a full `https://…/browse/KEY` URL as a bare key, and the frontend's
     `extractJiraIssue` already finds the key in either, so never parse or normalise it on
     the Go side.
   - Set `Jira` on the response in `taskResponse`, beside the other row-derived fields.
   - A failure reading the pages must degrade exactly as the open-questions path does: log
     a warning and leave every row's `jira` nil. It must never fail the board read.

3. `pkg/board/parity_test.go` — add `"jira"` to the pinned sorted field list, in its sorted
   position between `"id"` and `"jump_pane"`. Do not reorder or reformat the rest of the
   list.

4. `scripts/parity/parity.sh` — add `.jira` to **both** `del(...)` arms inside `normalize()`
   (the array branch and the object branch), **last** in the argument list so each arm reads
   `del(.jump_pane, .open_questions, .location, .jira)`, and extend the comment block above it with a
   paragraph explaining that `jira` is Go-only on the same terms as `location`: the
   superseded Python backend cannot emit it, so it is removed from both sides rather than
   compared.

5. `src/vault_ui/static/app.js` — in `createTaskCard` only:
   - The card's **heading** must keep coming from `extractJiraIssue(task.title).title`.
     `extractJiraIssue` strips the matched key out of the `title` it returns, so feeding it
     the bare `jira` value would leave the heading empty or reduced to the ticket key. This
     is the trap in this change: a naive `extractJiraIssue(task.jira || task.title)` passes
     every badge check while blanking the card's title.
   - The **badge's** `issueKey` and `issueUrl` must come from the field-preferred source:
     `task.jira` when it is present and non-empty after trimming, otherwise `task.title`.
     Derive them by running the existing `extractJiraIssue` over that chosen source, so the
     project-domain mapping and the key regex are not duplicated.
   - A multi-key value such as `BRO-20080, IT-40369` therefore badges its **first** key.
     Do not render a second badge and do not split the string by hand.
   - `createGoalCard` and every goal response field stay exactly as they are.

6. Tests:
   - Go, in `pkg/board/`: a test proving the response carries the field. Assert a task page
     whose frontmatter carries `jira: BRO-22339` yields `Jira` pointing at `"BRO-22339"`, and
     that a page with no `jira:` key yields a nil `Jira`. Follow the existing board test
     helpers: build the board with `newHarness(item(...))` and feed pages through
     `h.pageIndex.ListPagesReturns(...)`. The existing `page(name, content)` helper hardcodes
     frontmatter `{"status": "todo"}`, so add a frontmatter-taking sibling (e.g.
     `pageWithFrontmatter`) beside it rather than changing `page`'s signature, which other
     tests call. Cover the three value shapes: a bare key, a full
     `https://seibertgroup.atlassian.net/browse/BRO-22339` URL (which must come back as that
     whole string, not a parsed key — this is what catches an agent that normalises on the
     Go side), and absent.
   - A second test over the **incremental** path, in `pkg/board/rowpatch_test.go` beside the
     existing `It("re-derives the Open Questions of a changed page from its own content")`:
     list once with `ListTasks`, then `h.index.setPages(...)` a page carrying
     `jira: BRO-22339`, call `h.index.markPages("Alpha.md")`, list again, and assert the
     patched row still yields `Jira` pointing at `"BRO-22339"`. That file already drives the
     patch path through the public `ListTasks`, so no internal test and no hand-written
     `IndexRevisions` fake is needed. The full-build test above cannot catch a field the
     patch literal drops.
   - `tests/test_card_render_unify.py` — in `test_task_wrapper_keeps_urgency_and_jira`,
     keep the existing `extractJiraIssue(task.title)` presence assertion and move it onto
     `_function_body("createTaskCard")` (use `_function_body`, not `_slice`: the fixed
     character window under-covers a builder longer than the window, which is exactly what
     this assertion must not do), then add a second assertion that the same body reads
     `task.jira`. **Both assertions stay**: the `extractJiraIssue(task.title)` one is what
     catches the naive `extractJiraIssue(task.jira || task.title)`, which satisfies a
     `task.jira`-only check while blanking the card's heading.

7. `CHANGELOG.md` — the file has no `## Unreleased` section; add one above the current
   `## v0.101.1` heading, separated by a blank line, with one bullet describing the change
   in this repo's established style (a lower-case type prefix, then what changed and why it
   was wrong).

8. `README.md` — the `### GET /api/tasks` section documents each Go-only task field beside
   its peers (`session_state`, `open_questions`; only `open_questions` closes with the
   "Go-only … the parity harness strips it from both sides" sentence). Add a paragraph after
   the `open_questions`
   paragraph stating that each task also carries `jira`: the task page's own `jira:`
   frontmatter value, `null` when the page carries none, used by the card for its badge.
   State that it is Go-only on the same terms as `open_questions` — the superseded Python
   backend does not emit it and the parity harness strips it from both sides. Do not
   restructure the existing paragraphs.
</requirements>

<constraints>
- Do NOT change goal cards, `GoalResponse`, or the goal board path. Goals need their own
  per-vault page listing and are deliberately out of scope.
- Do NOT change `extractJiraIssue`'s signature or behaviour, and do not move its
  `projectDomains` map.
- Do NOT add a second `ListPages` call per vault, and do not take a listing per task.
- Do NOT modify the Python backend under `src/vault_ui/api/` or
  `src/vault_ui/vault_cli_client.py`. It is superseded; `normalize()` removes `jira` from
  both sides, so the Python side needs no field.
- Do NOT add a Jira transition, status sync, or any mutation of a vault page.
- Do NOT commit — dark-factory handles git.
- Do NOT bump the version, edit `## v0.101.1`, or create a tag; the release is cut on
  master after merge.
- Existing tests must still pass.
- `tests/` assertions here are static source assertions over `app.js`; there is no JS test
  runner in this repo, so do not add one and do not claim a behavioural JS test.
</constraints>

<verification>
Run `make precommit` — must pass.

Then confirm the change's load-bearing invariants:

- `grep -q 'task.jira' src/vault_ui/static/app.js` — the card reads the new field.
- `grep -q 'extractJiraIssue(task.title)' src/vault_ui/static/app.js` — the heading still
  comes from the title, not from the field.
- `grep -q '"jira"' pkg/board/parity_test.go` — the pinned field list was updated, so the
  parity test can pass rather than failing on an unlisted field.
- `grep -cF 'del(.jump_pane, .open_questions, .location, .jira)' scripts/parity/parity.sh`
  prints `2` — both `normalize()` arms strip the new key. Nothing else exercises
  `normalize()` (`make parity-selftest` does not cover it, and its `jq … 2>/dev/null || cat`
  fallback would silently serve raw output on a malformed filter), so this count is the only
  proof the edit is complete.
- `make parity-selftest` still passes.
- `printf '{"id":"x","jira":"BRO-1"}' | jq -c 'del(.jump_pane, .open_questions, .location, .jira)'`
  prints `{"id":"x"}` — the extended filter is valid jq and actually strips the key.
- `grep -qi 'jira' README.md` — the API docs gained the field (README carries no `jira`
  mention today, so any hit is the new paragraph).
- `grep -q '^## Unreleased' CHANGELOG.md` — the entry sits under the section `docs/dod.md`
  requires.

Before you finish, re-run `make precommit` and confirm it passes, then walk each numbered
requirement above against the files you changed.
</verification>
