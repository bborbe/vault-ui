---
status: completed
summary: Wired the board's Live session checkbox into the Goals and Topics views via a session_live query parameter on GET /api/goals and GET /api/topics, with board, handler, frontend, docs, and tests updated
execution_id: vault-ui-live-session-filter-exec-173-live-session-filter-goals-and-topics
dark-factory-version: v0.198.7
created: "2026-10-30T15:45:00Z"
queued: "2026-10-30T15:24:58Z"
started: "2026-10-30T15:25:19Z"
completed: "2026-10-30T15:28:49Z"
branch: dark-factory/173-live-session-filter-goals-and-topics
---

# Filter the Goals and Topics boards by live session

<summary>
- The "Live session" checkbox in the board toolbar now filters all three views, not just Tasks
- Ticking it on the Goals board shows only goals whose session is running right now
- Ticking it on the Topics board shows only topics whose manager session is running right now
- Unticking restores the full set on both views, with no stuck state
- The filter survives a page reload on both views, with the checkbox rendered checked
- Both endpoints accept the same parameter the task endpoint already accepts
- The three views agree on what "live" means because all three use the one existing classifier
- Nothing about how liveness is decided changes — this is plumbing, not a new definition
- The control's own label and the Goals board's documentation stop claiming it is tasks-only
</summary>

<objective>
Wire the board's existing "Live session" checkbox into the Goals and Topics views so one control
filters all three boards. Today the checkbox sets a browser flag that only the task fetch sends,
so on the Goals and Topics views the board refetches an unfiltered query and nothing changes on
screen — the control is inert on two of its three views.
</objective>

<context>
Read `README.md` for the API surface and the board's rendering conventions, and `docs/dod.md`
for this repo's Definition of Done (the daemon's `validationPrompt`). `CLAUDE.md` is gitignored
in this repo and is not present in a worktree checkout.

Read these files before changing anything:

- `pkg/board/tasks.go` — `ListTasks` (~line 55) and the `if query.SessionLive` block (~line 113)
  is the filter to mirror, including its `make([]api.TaskResponse, 0, len(responses))`
  allocation. `TaskQuery.SessionLive` in `pkg/board/board.go` is the field to copy.
- `pkg/board/goals.go` — `ListGoals` (~line 28) and `goalsForVault` (~line 64), where responses
  are accumulated. `strongestSessionState` (~line 197) computes each goal's state from the goal
  page's own `claude_session_id` and `manager_session_id`; `goalResponse` assigns it (~line 189).
- `pkg/board/topics.go` — `ListTopics` (~line 38), which takes `vaults []string` today and sets
  `SessionState` per topic at ~line 80.
- `pkg/handler/api_tasks.go` — the `SessionLive:` line (~line 30) inside `NewTasksHandler` is the
  parse idiom to copy; `first` (~line 75) and `parseBool` (`pkg/handler/api_common.go:132`) are
  the helpers it uses.
- `pkg/handler/api_goals.go` and `pkg/handler/api_topics.go` — the two handlers to extend.
- `pkg/handler/api_test.go` — `fakeBoard.ListTopics` (~line 73) currently records only the vault
  slice it was handed, into `gotVaults` (a field shared with `ListAssignees`); no handler test
  asserts topics forwarding today. The `ListTasks` forwarding case (~line 189) is the template.
- `pkg/board/board_test.go` — the `ListGoals` (~line 812) and `ListTopics` (~line 1093) Describe
  blocks. The `goal session-id combination` block (~line 887) shows how the board *derives*
  session state from `h.signals.registry` and `h.sessions`; nothing sets it on a fixture.
- `src/vault_ui/static/app.js` — `loadGoals` (~line 1076) and `loadTopics` (~line 1168) build
  their query strings; the task fetch (~line 978) shows the two lines to add. `updateURL`
  (~line 840, with the `session_live` write at ~863) already persists it, and the checkbox
  handler (~line 234) already calls `loadCurrentView()` — both are correct and must not change.
- `tests/test_goal_defer_filter.py` — this repo's convention for "loader X sends parameter Y":
  slice a function body out of `app.js` and assert on it statically. Requirement 8 follows it.
- `tests/test_card_render_unify.py` (~line 111) — the cache-buster assertion pattern: a non-empty
  `app.js?v=` token must be present and three pinned superseded tokens must be absent. It asserts
  the *shape*, not the current value, so it will NOT catch an un-bumped token — see
  `<verification>`.
- `docs/liveness-classification.md` — the frozen contract for the four outcomes. This change does
  not alter it.
- `src/vault_ui/api/tasks.py` — the superseded Python backend. Do not extend it (see constraints).
</context>

<requirements>
1. `pkg/board/board.go` — add `SessionLive bool` to `GoalQuery`, placed after `UpcomingHours`,
   with a short comment saying what `true` keeps. Mirror the `TaskQuery` struct's own comment
   style; the `TaskQuery.SessionLive` field itself carries no per-field comment to copy.

2. `pkg/board/board.go` — add a `TopicQuery` struct holding `Vaults []string` and
   `SessionLive bool`, and change the `Board` interface's `ListTopics` method to
   `ListTopics(ctx context.Context, query TopicQuery) ([]api.TopicResponse, error)`.

3. `pkg/board/goals.go` — in `ListGoals`, after the vault loop has accumulated `responses` and
   before returning, apply the same filter `ListTasks` applies at `pkg/board/tasks.go:113-120`:
   when `query.SessionLive` is set, keep only responses whose `SessionState` is non-nil and
   equal to `string(session.SessionStateLive)`. Allocate the filtered slice with
   `make([]api.GoalResponse, 0, len(responses))`, matching the tasks block. Do not change how
   any goal's `SessionState` is computed.

4. `pkg/board/topics.go` — change `ListTopics` to take `query TopicQuery` and select vaults from
   `query.Vaults` instead of the `vaults` parameter. Apply the same live filter over the
   accumulated `responses` before returning, with the same shape as requirement 3. Do not change
   how any topic's `SessionState` is computed.

5. `pkg/handler/api_goals.go` — in `NewGoalsHandler`, read `session_live` from the request query
   and set it on the `board.GoalQuery` it builds, using `parseBool(first(query["session_live"]))`
   exactly as `NewTasksHandler` does. The existing `upcoming_hours` validation and its early
   return stay where they are.

6. `pkg/handler/api_topics.go` — in `NewTopicsHandler`, build a `board.TopicQuery` from the
   request query: `Vaults` from `req.URL.Query()["vault"]` and `SessionLive` from
   `parseBool(first(query["session_live"]))`. Leave `NewTopicDetailHandler` untouched.

7. `src/vault_ui/static/app.js` — send the parameter from both loaders:
   - In `loadGoals`, after the `upcoming_hours` line, add `if (currentLiveOnly) {
     params.set('session_live', 'true'); }` with a comment pointing at the task fetch.
   - In `loadTopics`, add the same two lines after the vault parameters.
   - Do not change `loadTasks`, the checkbox handler, or `updateURL` — they already work.

8. Tests — Python, in a new `tests/test_live_session_filter_views.py`: two static-assert cases in
   the style of `tests/test_goal_defer_filter.py` — slice the `loadGoals` and `loadTopics`
   function bodies out of `src/vault_ui/static/app.js` and assert each contains
   `params.set('session_live'`. This is the only automated proof of the client half: the Go
   handler tests prove the server parses the parameter, not that the browser sends it, and
   `app.js` already contains six other `session_live` occurrences that a file-wide grep would
   match on the unmodified file. Runs in `make precommit` (it is not `integration`-marked).

9. Tests — Go, in `pkg/board/board_test.go`:
   - A `ListGoals` case where the board **derives** the three states: one goal carrying a
     `ClaudeSessionID` that `h.signals.registry` lists (live), one whose transcript in
     `h.sessions` is stale (quiet), and one with no session id at all. Assert that
     `GoalQuery{SessionLive: true}` returns exactly the live one, and that `SessionLive: false`
     returns all three. `SessionState` is computed inside `goalResponse` and cannot be set on a
     fixture — follow the `goal session-id combination` Describe block (~line 887). Two details
     of that block are load-bearing: it swaps `h.signals.registry` / `h.sessions` and then calls
     `h.build()` (the fakes are copied into the board at construction, so a mutation is invisible
     until the board is rebuilt), and its ids are valid UUIDs, which they must be —
     `classifyManagerSessionID` (`pkg/board/goals.go:229`) returns `indeterminate` for a non-UUID
     id, so an `"own-live"`-style id lands in the wrong bucket.
   - The same three-way case for `ListTopics`, which derives from `ManagerSessionID` the same way
     (~line 1130). Assert that `TopicQuery{SessionLive: true}` returns exactly the live one and
     that `SessionLive: false` returns all three. No topic on this machine classifies `live`, so
     this manufactured case is the only proof that the inclusion direction works — a filter that
     returns nothing passes a careless exclusion check — and the `false` assertion is the only
     proof the filter is not inverted.
   - Update the eight existing `ListTopics` call sites in this file — lines 1099, 1120, 1139,
     1158, 1170, 1193, 1205, 1246, all currently `ListTopics(context.Background(), nil)` — to
     `ListTopics(context.Background(), board.TopicQuery{})`.

10. Tests — Go, in `pkg/handler/api_test.go`:
    - Update `fakeBoard.ListTopics` to the new signature and record the received `TopicQuery` in
      a new `gotTopicQuery` field alongside the existing `gotGoalQuery`. Do not reuse `gotVaults`:
      `ListAssignees` writes it (`pkg/handler/api_test.go:49`), so a shared field cannot tell the
      two routes apart.
    - Add a handler-level case asserting `GET /api/topics?session_live=true` forwards
      `SessionLive: true` to the board, and the same for `GET /api/goals?session_live=true`,
      following the `ListTasks` case at ~line 189. Asserting at the handler is what proves the
      parameter is parsed — a board-level test alone leaves a handler that ignores the query
      string passing every check.
    - In that same topics case, drive `GET /api/topics?vault=personal&session_live=true` and also
      assert `Vaults: []string{"personal"}`. `ListTopics` loses its `vaults` parameter in
      requirement 2, so vault selection now travels through a brand-new struct field: a handler
      that omits `Vaults: query["vault"]` compiles, passes every other test, and silently returns
      every configured vault's topics for a scoped request. No existing test passes a non-empty
      `TopicQuery.Vaults`, so this assertion is the only thing between the refactor and that
      regression.

11. `README.md` — the API section has no `GET /api/goals` subsection at all; the intro at
    `README.md:71-72` only says "the goal endpoints exist alongside it". Author one, placed
    between `GET /api/tasks` and `PATCH /api/tasks/{id}/phase`, with a parameter table covering
    `vault`, `status`, `assignee`, `upcoming_hours` and `session_live`, and a paragraph on
    `session_state` stating that a goal's state is the strongest of its own `claude_session_id`
    and its `manager_session_id`, reusing the framing of the topics section's paragraph at
    ~line 149. Do not reuse the task paragraph at ~line 89 verbatim: it says null for a task with
    no `claude_session_id`, which is false for a goal that records only a manager id. Then add a
    `session_live` row to the existing `GET /api/topics` parameter table (~line 136), which
    currently lists only `vault`, mirroring the tasks row at ~line 87. Update the Goals-view
    bullet at `README.md:59` — "Vault, status, and assignee filters apply to both views." — to
    name the live-session filter alongside them. Finally, rewrite the API intro at
    `README.md:71-72`: once a `GET /api/goals` section exists, "the goal endpoints exist alongside
    it" is no longer true, so name all three documented endpoint groups.

12. `src/vault_ui/static/index.html` — two edits:
    - Retitle the toolbar checkbox label at ~line 53: its `title` currently reads "Show only
      tasks whose Claude session is running right now", which becomes wrong once the control
      filters all three views. Make it name items, not tasks.
    - Bump the `app.js?v=…` cache-buster token at ~line 175 to `2026-10-30-live-session-filter`.
      An un-bumped token is how this repo has previously shipped a frontend fix that browsers
      never received.

13. `CHANGELOG.md` — the file currently opens at `## v0.101.2`; there is no `## Unreleased`
    section. Create one at the top with a single bullet: the "Live session" toolbar filter now
    applies to the Goals and Topics views as well as Tasks, via a `session_live` query parameter
    on `GET /api/goals` and `GET /api/topics`.
</requirements>

<constraints>
- Do NOT change `ListTasks`, `TaskQuery`, or the task board's filter. It works and is the
  exemplar, not the subject.
- Do NOT change liveness classification. `pkg/session` is frozen: no new outcome, no reordered
  signal, no new window, no config knob. Filter on `session.SessionStateLive`, never on a
  `"live"` string literal.
- Do NOT add any filter dimension beyond live-session. No new query parameter, no new toolbar
  control.
- Do NOT extend the superseded Python backend (`src/vault_ui/api/tasks.py`, `models.py`). The
  live board is the Go binary; the Python harness serves nothing and adding a parameter there is
  dead work. `tests/test_api.py` must still pass unchanged.
- Do NOT write to any vault page. This is a read-path and rendering change only.
- Do NOT commit — dark-factory handles git.
- Do NOT bump the version or tag; the release is cut by the operator.
- `tests/test_topics_view.py` and anything else marked `integration` is deselected by the default
  pytest addopts and runs host-side only (`make test-integration`). It is not part of
  `<verification>`. Do not try to run it in the container.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass. It covers go-test, pytest, lint and typecheck.

Then confirm the parameter reached each layer. Each command below exits non-zero on the
unmodified tree and zero after the change, so each one actually discriminates:

- `grep -q 'session_live' pkg/handler/api_goals.go` — the goals handler parses it.
- `grep -q 'session_live' pkg/handler/api_topics.go` — the topics handler parses it.
- `awk '/^async function loadGoals/,/^}/' src/vault_ui/static/app.js | grep -q "params.set('session_live'"` — the goals loader sends it.
- `awk '/^async function loadTopics/,/^}/' src/vault_ui/static/app.js | grep -q "params.set('session_live'"` — the topics loader sends it.
- `grep -q 'GET /api/goals' README.md` — the goals endpoint is now documented.
- `awk '/^### `GET \/api\/topics`$/,0' README.md | grep -q 'session_live'` — the topics table gained its row.
- `grep -q 'app.js?v=2026-10-30-live-session-filter' src/vault_ui/static/index.html` — the cache-buster is bumped.
- `! grep -q 'Show only tasks whose' src/vault_ui/static/index.html` — the toolbar label no longer claims tasks-only.

Do NOT verify the client half with a file-wide `grep 'session_live' src/vault_ui/static/app.js` —
that file already contains six such matches before the change and the check would pass on an
unmodified tree.

Before you finish, re-run `make precommit` and confirm it passes, then walk each numbered
requirement above against the diff.
</verification>
