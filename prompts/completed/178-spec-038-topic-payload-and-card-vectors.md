---
status: completed
spec: [038-topic-card-data-parity]
summary: GET /api/topics now serves assignee, priority, activity_date, jira, blockers and blocked from each topic page's own data, with a structural jsasset helper and a captured pre-change card-vector fixture
execution_id: vault-ui-topic-card-parity-exec-178-spec-038-topic-payload-and-card-vectors
dark-factory-version: v0.198.17
created: "2026-11-10T07:10:00+01:00"
queued: "2026-11-11T07:04:38Z"
started: "2026-11-11T07:11:42Z"
completed: "2026-11-11T07:17:32Z"
branch: dark-factory/178-spec-038-topic-payload-and-card-vectors
---

# Serve the six data fields on the topic payload and capture the pre-change card vectors

<summary>

- The topic list endpoint gains assignee, priority, activity date, jira, blockers and a blocked flag
- Those values are read from the topic page's own frontmatter, not invented
- A topic with no assignee reports null rather than a placeholder
- The activity date is derived from the page and its session, exactly as a task's is
- The task and goal payloads are untouched
- A fixture of the current card vectors is captured before any frontend change, so the later non-regression check has a pre-change baseline

</summary>

<objective>

Make `GET /api/topics` carry the six data fields the topic card needs, so the frontend can render badges from real page data instead of empty values. Capture the pre-change card vectors in the same prompt, while the frontend asset is still untouched, so the non-regression assertion has a baseline that predates the change.

</objective>

<context>

Read `pkg/api/api.go` — find `TopicResponse` and compare it with `TaskResponse` and `GoalResponse`, which already carry the field shapes this prompt needs.

Read `pkg/board/topics.go` — `ListTopics` builds each `TopicResponse` from an `ops.TaskListItem`.

Read `pkg/board/tasks.go` — `uncompletedBlockers`, `taskPageFieldsByTask`, `pageJira`, `priorityValue`, `dateTimeString` and the `activity.ComputeActivityDateWith` call site show how a task derives blockers, jira, priority and its activity date. Follow those patterns rather than inventing new ones.

Read `src/vault_ui/static/app.js` — `const CARD_CONFIG` and the `mapTaskCard` / `mapGoalCard` / `mapTopicCard` functions. Do not modify this file in this prompt.

Read `pkg/board/board_test.go` — the `Describe("ListTopics")` block already registers topic rows via `h.list.items["23 Topics"]` and asserts the wire shape with `json.Marshal`; extend it rather than starting a new suite. It also carries an "ignores the retired manager_session_id on a topic" example, which reinforces requirement 2's warning.

Read `pkg/board/parity_test.go` — the explicit field-set assertion pattern requirement 6.1 should follow.

</context>

<requirements>

1. Extend `TopicResponse` in `pkg/api/api.go` with `Assignee *string`, `Priority any`, `ActivityDate *string`, `Jira *string`, `Blockers []string` and `Blocked bool`, using the JSON key names `assignee`, `priority`, `activity_date`, `jira`, `blockers`, `blocked`. `Priority` is `any`, exactly as `TaskResponse.Priority` and `GoalResponse.Priority` are, populated with `priorityValue(item.Priority)` (`pkg/board/tasks.go`) — nil for the zero value, the int otherwise. Do not declare it `*int` and do not add a second int-to-pointer helper. `Assignee` uses `strPtr(item.Assignee)` (`pkg/board/helpers.go`), which is nil for the empty string. The existing eight keys keep their names and meanings.

2. Populate the new fields in `ListTopics` (`pkg/board/topics.go`):
   - `assignee` and `priority` from the list item — it already carries both.
   - `blockers` from the item's blocked-by list, filtered to the entries that are still open, using the same helper the task path uses.
   - `blocked` is true only when that filtered list is non-empty.
   - `activity_date` from `activity.ComputeActivityDateWith`, keyed on the item's `ClaudeSessionID` and wrapped in `dateTimeString` — the same call the task path makes. `manager_session_id` is retired in this repo — do not read it.
   - `jira` from the page's frontmatter. Add `topicPageFieldsByTopic` alongside the task equivalent: one `ListPages(ctx, vault.Path, vault.TopicsFolder)` per vault, degrading to nil on error exactly as the task helper does, reading only `pageJira(page)`. Do not reuse the task helper — it lists the tasks folder and parses Open Questions, neither of which applies to a topic.

3. Do not fabricate a default. A page carrying no assignee yields a null `assignee`; no priority yields a null `priority`. The keys are always present; their values are null.

4. Add a shared helper package at `pkg/board/jsasset` — this exact path; the next two prompts import it — that loads the embedded frontend asset through `fs.Sub(staticui.FS, "static")` and locates a **named JavaScript construct**: an object literal (`CARD_CONFIG` and each mapper's returned literal), a function body (`createCard`, `navigateToBlocker`), and a branch inside a function body (the assign-control branch in `createCard`). For an object literal it returns the key-to-source-expression map; for a body or branch it returns the construct's source text. It must parse the JavaScript structure — keys, nesting, quotes, template literals, braces — not grep for substrings, so a name appearing in a comment cannot satisfy a later assertion. The function-body and branch locators are required now even though this prompt asserts only slot maps and mapper literals: the next prompt's assertions over `createCard` and `navigateToBlocker` must reuse this helper rather than add their own regexes.

5. Using that helper, capture the current card vectors into `pkg/board/testdata/card-vectors-pre-change.json` (create the directory). The fixture records, for the `task` and `goal` entries, the enabled slot set and each mapper's key-to-source-expression map. Store each expression with runs of whitespace collapsed to a single space and the map's keys in a fixed sorted order, so the fixture stays stable under reformatting. Do this **before** any change to `src/vault_ui/static/app.js` — this prompt does not touch that file, so reading it now captures the pre-change state.

6. Add Go tests for the payload:
   - the key set of a decoded topic list row is exactly the existing eight plus the six new keys;
   - a page carrying assignee, priority, jira and blocked-by round-trips each value;
   - a page carrying three `blocked_by:` entries, one of them `completed` (populate the harness status cache first, as the existing board tests do), serves a `blockers` list holding the two open names, excludes the completed one, and serves `blocked: true`; a page whose only blocker is completed serves an empty list and `blocked: false`;
   - a page carrying none of them serves null for assignee and priority, with the keys present;
   - a page whose transcript is newer than its file returns the transcript timestamp, and a page with no session returns the file mtime.

7. Before finishing, re-run `make precommit` and confirm it passes; walk each requirement above against the change.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- The topic payload's existing eight keys keep their names and meanings. This change is additive.
- The task and goal payloads are unchanged.
- No new dependency and no change to the vault-cli module; every field is derived from page data the board already reads.
- `blocked_by` is deliberately not exposed on the topic payload — only the derived `blockers` list and the `blocked` flag.
- The frontend asset `src/vault_ui/static/app.js` is NOT modified by this prompt.
- Add an entry under `## Unreleased` in `CHANGELOG.md`; create that heading if it is absent.

</constraints>

<verification>

`make precommit` — must pass.

</verification>
