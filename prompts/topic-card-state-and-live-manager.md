---
spec: ["036-topic-card-state-and-topic-liveness"]
status: draft
created: "2026-10-29T00:00:00Z"
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
- A topic's live state reaches the board on the same terms a goal's already does
- Both backends emit the same topic payload field set (the Python harness always sends null)
</summary>

<objective>
Make the topic card self-consistent with the phase column it renders into, and surface
whether a live manager session is running for that topic. The card currently reads its
column from `phase` and its subtitle from `status`, so five topics carrying
`phase: todo` + `status: in_progress` sit in TODO while every card's subtitle says
`in_progress` — a card that contradicts its own column.
</objective>

<context>
Read `README.md` for the API surface and the board's rendering conventions, and
`docs/dod.md` for this repo's Definition of Done (the daemon's `validationPrompt`).
Note: `CLAUDE.md` is gitignored in this repo and is not present in a worktree checkout.

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
- `specs/in-progress/036-topic-card-state-and-topic-liveness.md` — this prompt's spec.
- `tests/test_topics_view.py`, `tests/test_api.py`, `pkg/board/board_test.go` (`ListTopics`
  Describe block, ~line 1079; the goal wire-shape case at ~line 945).

Why the status badge goes rather than the column changing: `phase` grouping for goals and
topics was landed deliberately (`v0.98.0`), and `renderTopics` already buckets on
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
   - This adds `path/filepath`, `pkg/cleanup` and `pkg/session` to `topics.go`'s imports.

4. `src/vault_ui/api/models.py` — the superseded Python backend must emit the same field set:
   - `Topic` dataclass gains `session_state: str | None = None`, with a comment recording the
     divergence: the Go backend classifies it from `manager_session_id`, while the Python
     harness sets it directly (its `_parse_topic` never reads that field) so the frontend
     branch stays exercisable. The Python backend therefore always sends `null`.
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
   frontend fix that browsers never received. Add the matching negative assertion to a Python
   test (pattern: `tests/test_card_render_unify.py:114`) that the old
   `2026-10-29-jump-pane-gating` token is gone.

9. Tests — update the ones this change invalidates and cover the new behaviour:
   - `tests/test_topics_view.py`: change the `_topic_cards` helper to report the card's
     `.live-badge` text instead of `.status-badge`. Rename
     `test_topics_render_each_own_status_and_phase_column` to
     `test_topic_cards_carry_no_status_badge_and_sit_in_their_phase_column`, asserting that no
     topic card carries a `.status-badge` and that each card sits in the column its `phase`
     names. Add a third fixture topic carrying `session_state="live"` and assert that exactly
     that card renders a `.live-badge` while the other two render none. Bump the card-count
     assertion at ~line 231 from `to_have_count(2)` to `to_have_count(3)`, and rewrite the
     docstring of `test_opening_a_topic_shows_its_own_status` (~line 260), which currently
     says "The card renders the status too" — the card no longer does.
   - `tests/test_api.py`: add `"session_state"` to the topic key set in
     `test_list_topics_returns_topic_response_key_set`.
   - `pkg/board/board_test.go` in the `ListTopics` Describe block: add a case where a topic
     row carries `ManagerSessionID` and `h.signals.registry` lists that id — assert
     `SessionState` is non-nil and `"live"` (follow the goal case at ~line 1020); a case where
     the row carries no `ManagerSessionID` — assert `SessionState` is nil and the marshalled
     response contains `"session_state":null` (pattern at ~line 945); and a case with
     `h.signals.unknown = true` (store unreachable) — assert `SessionState` is non-nil and
     `"indeterminate"`, never `"quiet"`.
   - `pkg/board/board_test.go`: pin the topic wire shape the way the task-list fence does in
     `pkg/board/parity_test.go` — `json.Marshal` a `ListTopics` result, decode the rows into
     `[]map[string]json.RawMessage`, and assert the field set is exactly
     `[flag id obsidian_url phase session_state status title vault]`, so a tag rename cannot
     ship silently.
10. `CHANGELOG.md` — add one `## Unreleased` bullet describing both halves: the topic card no
    longer renders a status badge that contradicts its phase column, and the topic response
    carries `session_state` classified from the page's `manager_session_id`, rendered as a
    display-only live indicator.
11. `README.md` — in the `GET /api/topics` section (~line 128), describe `session_state` on the
    same terms as the task paragraph at ~line 89 (`live`/`indeterminate`/`quiet`/`null`, null
    when the topic page records no `manager_session_id`), and add `phase`, `flag` and
    `session_state` to the example payload (the example currently omits `phase` and `flag` too).
12. `docs/liveness-classification.md` — add one sentence to § "Multiple session ids on a card":
    a topic page records only `manager_session_id`, so a topic's outcome is that single id's
    outcome.
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
- The four outcomes and their signal order in `docs/liveness-classification.md` are frozen. No
  fifth state, no reordering, no new window, no config knob.
- `tests/test_topics_view.py` is marked `integration` and is deselected by the default pytest
  addopts — it runs host-side only (`make test-integration`) and is not part of
  `<verification>`. Update it correctly; do not try to run it in the container.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass.

Then confirm the two frontend invariants the change is about:

- `! grep -q 'status-badge' src/vault_ui/static/app.js` — the topic card no longer renders a
  status badge anywhere.
- `grep -q 'session_state' src/vault_ui/static/app.js` — the card reads the new field.

Before you finish, re-run `make precommit` and confirm it passes, then walk each numbered
requirement above against the diff.
</verification>
