---
status: draft
---

# Topic cards stop contradicting their column and show a live manager

<summary>
- A topic card no longer prints a status that contradicts the phase column it sits in
- The column keeps following `phase`, which is the board's established grouping for every view
- The topic card stops rendering a status badge at all, exactly as task and goal cards do
- The topic's own status stays visible in the detail modal and still drives the status filter
- A topic whose manager session is live now shows a live indicator on its card
- A topic with no manager session, or a dead one, shows no indicator
- The live indicator is display-only: it is not a take-over control and is not clickable
- The API carries the topic's manager liveness on the same terms the goal response already does
- Both backends emit the same topic payload field set
</summary>

<objective>
Make the topic card self-consistent with the phase column it renders into, and surface
whether a live manager session is running for that topic. The card currently reads its
column from `phase` and its subtitle from `status`, so five topics carrying
`phase: todo` + `status: in_progress` sit in TODO while every card's subtitle says
`in_progress` — a card that contradicts its own column.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these files before changing anything:

- `src/vault_ui/static/app.js` — `createTopicCard` (~line 1233), `renderTopics` (~line 1208),
  `liveBadgeLabel` (~line 1552), `flagButtonHtml` (~line 1690), `displayPhase` (~line 1019).
- `pkg/board/goals.go` — `goalResponse` (~line 119) shows how a goal's manager session is
  classified, and `classifyGoalManagerSessionID` (~line 225) is the helper to reuse.
- `pkg/board/topics.go` — `ListTopics` (~line 35), the function to extend.
- `pkg/api/api.go` — `TopicResponse` (~line 115) and `GoalResponse.SessionState` (~line 104).
- `docs/liveness-classification.md` — the durable contract for the four outcomes and the
  multi-id strongest-outcome rule. A topic records only `manager_session_id` (no topic page
  carries `claude_session_id`), so its outcome is that one id's outcome.
- `tests/test_topics_view.py`, `tests/test_api.py`, `pkg/board/board_test.go` (`ListTopics`
  Describe block, ~line 1079).

Why the status badge goes rather than the column changing: `phase` grouping for goals and
topics was landed deliberately (`vX.98.0`), and `renderTopics` already buckets on
`displayPhase(topic.phase, 'unknown')`. The column is correct. The badge is the defect —
it renders a second, different axis (`status`) in the same affordance the column already
speaks to, so the two disagree whenever a page's `status` and `phase` differ. Task and goal
cards render no status badge for exactly this reason.
</context>

<requirements>
1. `pkg/api/api.go` — add a `SessionState *string` field to `TopicResponse`, with the JSON
   tag `session_state`, documented on the same terms as `GoalResponse.SessionState`: always
   emitted, null when the page records no manager id. Place it after `Phase` and before `Flag`.

2. `pkg/board/goals.go` — rename `classifyGoalManagerSessionID` to `classifyManagerSessionID`
   and update its doc comment: it now classifies the manager session id of a goal **or a
   topic** page. Update the one existing call site in `goalResponse`. Behaviour is unchanged.

3. `pkg/board/topics.go` — in `ListTopics`, populate `SessionState` from the row's
   `ManagerSessionID`:
   - Before the vault loop, read the same three signals `ListGoals` reads:
     `registryIDs := b.signals.RegistrySessionIDs(ctx)`,
     `liveIDsUnknown := !b.signals.LiveIDsKnown()`,
     `resumeIDs := b.signals.ResumeSessionIDs(ctx)`.
   - Inside the vault loop, derive `projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)`
     and `projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")`, and build one
     `session.ClassifyParams` for the vault, mirroring `goalResponse`'s construction
     (same `Now`, `LiveWindow`, `TranscriptMtime`, and the three id sets).
   - Per item, set `SessionState: sessionStatePtr(classifyManagerSessionID(ctx, item.ManagerSessionID, params))`.
   - Do not classify anything else on the topic: no `claude_session_started`, no
     `activity_date`, no `location`. A topic carries only the manager id.

4. `src/vault_ui/api/models.py` — the superseded Python backend must emit the same field set:
   - `Topic` dataclass gains `session_state: str | None = None` with a comment that the Go
     backend classifies it and the Python harness sets it directly so the frontend branch is
     exercisable.
   - `TopicResponse` gains `session_state: str | None = None`.

5. `src/vault_ui/api/tasks.py` — `_topic_to_response` passes `session_state=topic.session_state`
   through.

6. `src/vault_ui/static/app.js` — in `createTopicCard`:
   - Remove the `<span class="status-badge">…</span>` from the card footer entirely.
   - Add a display-only live indicator as the first child of `.card-footer`, rendered only
     when `topic.session_state === 'live'`, using the existing `liveBadgeLabel(null)` for the
     text. It carries no `onclick`, no `role="button"` and no `tabindex`: a topic has no
     take-over route, so the badge must not look like one.
   - Add a comment recording why the status badge is gone: the column is the topic's phase,
     the badge showed a different axis, and task and goal cards show no status badge either.
   - Leave `renderTopics`, `flagButtonHtml` and the detail button untouched.

7. `src/vault_ui/static/style.css` — delete the now-unused `.topic-card .status-badge` rule
   (keep `.topic-modal-header .status-badge` — the modal still shows the status). Add a
   `.topic-card .live-badge` rule that removes the pointer affordance the shared `.live-badge`
   carries (`cursor: default`) and neutralises its hover shade, because the topic badge is not
   clickable.

8. `src/vault_ui/static/index.html` — bump the `app.js?v=…` cache-buster token to
   `2026-10-29-topic-card-state`. An un-bumped token is how this repo has previously shipped a
   frontend fix that browsers never received.

9. Tests — update the ones this change invalidates and cover the new behaviour:
   - `tests/test_topics_view.py`: change the `_topic_cards` helper to report the card's
     `.live-badge` text instead of `.status-badge`. Rename
     `test_topics_render_each_own_status_and_phase_column` to
     `test_topic_cards_carry_no_status_badge_and_sit_in_their_phase_column`, asserting that no
     topic card carries a `.status-badge` and that each card sits in the column its `phase`
     names. Add a third fixture topic carrying `session_state="live"` and assert that exactly
     that card renders a `.live-badge` while the other two render none.
   - `tests/test_api.py`: add `"session_state"` to the topic key set in
     `test_list_topics_returns_topic_response_key_set`.
   - `pkg/board/board_test.go` in the `ListTopics` Describe block: add one case where a topic
     row carries `ManagerSessionID` and `h.signals.registry` lists that id — assert
     `SessionState` is non-nil and `"live"` (follow the goal case at ~line 1020); and one case
     where the row carries no `ManagerSessionID` — assert `SessionState` is nil.
10. `CHANGELOG.md` — add one `## Unreleased` bullet describing both halves: the topic card no
    longer renders a status badge that contradicts its phase column, and the topic response
    carries `session_state` classified from the page's `manager_session_id`, rendered as a
    display-only live indicator.
</requirements>

<constraints>
- Do NOT change `createTaskCard`, `createGoalCard`, or any task or goal response field. Task
  and goal cards must render exactly as they do today.
- Do NOT write to any vault `23 Topics/` page. This is a rendering and read-path change only;
  no backfill of `phase`, `assignee`, `priority` or `manager_session_id` onto topic pages.
- Do NOT commit — dark-factory handles git.
- Do NOT bump the version or tag; the release is cut by the operator.
- The live indicator is display-only. Do not add a start, resume, take-over or jump control to
  the topic card, and do not add any topic mutation route.
- `tests/test_topics_view.py` is marked `integration` and is deselected by the default pytest
  addopts — it runs host-side only (`make test-integration`) and is not part of
  `<verification>`. Update it correctly; do not try to run it in the container.
- Existing tests must still pass.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must pass.

Then confirm the two frontend invariants the change is about:

- `! grep -q 'status-badge' src/vault_ui/static/app.js` — the topic card no longer renders a
  status badge anywhere.
- `grep -q 'session_state' src/vault_ui/static/app.js` — the card reads the new field.

Before you finish, re-run `ROOTDIR=/workspace make precommit` and confirm it passes, then walk
each numbered requirement above against the diff.
</verification>
