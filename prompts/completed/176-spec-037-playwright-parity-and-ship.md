---
status: completed
spec: [037-unify-board-renderer-and-card-template]
summary: Added a 7-test Playwright card-parity suite proving the unified template across all three boards, bumped the app.js cache-bust token, added the stale-token guard, documented the browser lane, and recorded the unification in CHANGELOG
execution_id: vault-ui-card-unify-exec-176-spec-037-playwright-parity-and-ship
dark-factory-version: v0.198.7
created: "2026-10-31T14:14:00Z"
queued: "2026-10-31T14:27:21Z"
started: "2026-10-31T16:42:11Z"
completed: "2026-10-31T16:46:54Z"
pr-url: https://github.com/bborbe/vault-ui/pull/202
branch: dark-factory/176-spec-037-playwright-parity-and-ship
---

# Browser proof for card parity, plus the cache-bust bump and CHANGELOG

<summary>
- A browser test now drives all three boards at once and proves the topic card carries exactly the affordances the topic API supports
- The same test proves a held topic sinks below an active one in its column, and a flagged topic sorts above an unflagged one
- The status filter is proven to drop an out-of-filter topic while still rendering an out-of-filter task, which is the difference between the two kinds
- Task and goal cards are proven to keep every badge, the menu and the session control they have today, and to gain no counts or dates
- The browser lane is the only place these behaviours are observable, so it is written but run on the host, not in the container
- An already-open board fetches the new script because the cache-bust token moves forward
- The released changelog gains its unreleased entry describing the unification
- Nothing about the wire format or the backend changes
</summary>

<objective>
Prove the unified card template and board renderer in a real browser — the only lane that can see rendered markup — and ship the change to already-open boards by bumping the script's cache-bust token, with the CHANGELOG entry this repo's Definition of Done requires.
</objective>

<context>
Prompts 1 and 2 land before this one: `src/vault_ui/static/app.js` then has `CARD_CONFIG`, three data mappers, a single
`createCard(kind, item)` builder, and one `renderBoard(kind, items)` that all three entry points delegate to.

**The container cannot run this lane.** `make test` and `make precommit` deselect integration-marked tests
(`pyproject.toml` sets `addopts = "-m 'not integration'"`) and no Chromium is installed here. Your job is to
write the tests and make them collectable. Do not attempt `make test-integration` — the container has no
Chromium. The host run belongs to the spec's operator verification rung, not to this prompt.

Read these before writing anything.

- `tests/test_topics_view.py` — the in-process fixture to copy: `_serve()` starts the real FastAPI app on a free
  port with a mocked vault-cli, and pins **both** `vault_ui.factory._config` and `vault_ui.factory.load_config`
  (the app's periodic reload loop otherwise replaces the test config with the operator's real
  `~/.config/vault-ui/config.yaml` a few seconds in, and every vault name then reads "unknown"). It also shows
  the `wide_viewport` autouse fixture — the header controls hide below 1500px.
- `tests/test_blocked_badge_board.py` — the pattern for a blocked badge that is *derived* rather than hard-coded:
  `_seed_vault` writes real `status:` frontmatter, a real `StatusCache` is loaded from it, and the fixture patches
  `vault_ui.api.tasks.get_status_cache` alongside the vault-cli client. Copy that pattern for the blocked
  assertions; a hand-set `blocked=True` on the fixture object does not exercise the derivation.
- `tests/test_board_sort.py` — the sort-control browser suite; it shows how a task board is asserted card by card.
- `src/vault_ui/static/app.js` — read `createCard` and `renderBoard` as they now stand. The class names the tests
  assert are the ones the builder emits: `.flag-btn`, `.hold-badge`, `.assignee-badge`, `.assign-to-me-link`,
  `.priority-chip`, `.activity-age`, `.blocked-badge`, `.jira-badge`, `.menu-btn`, `.start-btn`, `.resume-btn`,
  `.live-badge`, `.topic-live-badge`, `.topic-title-link`, `.topic-detail-btn`. (`.status-badge` is asserted
  *absent* — no card kind emits it.)
- `src/vault_ui/static/index.html` — the `<script src="app.js?v=…">` tag at the bottom.
- `tests/test_card_render_unify.py` — `test_cachebust_token_bumped` holds the list of known-stale tokens.
- `CHANGELOG.md` — the file currently opens at `## v0.102.0`; there is no `## Unreleased` section.
- `docs/dod.md` — the Definition of Done: a `## Unreleased` CHANGELOG entry and tests covering the changed
  behaviour.
- `docs/frontend-card-render.md` — the contract prompt 1 wrote. Add the browser-lane pointer here.
</context>

<requirements>
1. **Create `tests/test_card_parity.py`**, a Playwright suite with `pytestmark = pytest.mark.integration` and a
   module docstring saying what it proves and why it lives in this lane (the static lane reads `app.js` as text
   and cannot see rendered markup). Follow `tests/test_topics_view.py` for the fixture: one `_client()` whose
   `list_tasks`, `list_goals`, `list_topics` and `show_topic` are `AsyncMock`s over fixed fixture lists, one
   `_serve(tmp_path, monkeypatch)` that builds a `Config` with a single `VaultConfig` carrying `name`,
   `vault_name="TestVault"` (`TopicResponse.obsidian_url` reads it), `vault_path=str(tmp_path)` (the status
   cache and goal-folder discovery both read the vault root),
   `tasks_folder="24 Tasks"` and `topics_folder="23 Topics"`, pins `vault_ui.factory._config` and
   `vault_ui.factory.load_config`, runs `create_app()` under `uvicorn.Server` on a free port, and a `live_server`
   fixture that patches `vault_ui.api.tasks.get_vault_cli_client_for_vault`. Keep the `wide_viewport` autouse
   fixture.

   **The mocked `_list_tasks` must ignore its `status_filter` argument and return every fixture row, and
   `_list_goals` returns every fixture row unconditionally** (`list_goals` takes only `show_all` —
   `src/vault_ui/vault_cli_client.py:246`). That is what makes the "the filter stays off for tasks and goals"
   assertion meaningful: the server-side filter is simulated as absent, so only a client-side filter could drop
   the card. Add a comment saying so.

   Fixture data, chosen so each assertion can fail:
   - Topics, all in vault `TestVault`: an active topic in `phase="execution"` with `status="in_progress"` whose
     title sorts *between* the held and the flagged one (so neither title order alone reproduces the expected
     DOM order); a **held** topic in the same `execution` column with `status="hold"` and a title that sorts
     *before* the active one; a **flagged** active topic in the same column whose title sorts *after* the
     unflagged ones (so flag-first, not the title sort, must put it on top); a topic with
     `session_state="live"`; and a topic with a status outside the filter (e.g. `status="abandoned"`) in its own
     column.
   - A task carrying every badge the card supports: `status="in_progress"`, `phase="execution"`, `flag=True`,
     `assignee`, `priority`, `due_date`, `planned_date`, `defer_date` set to distinctive sentinel strings,
     `modified_date` set to a recent datetime (the only source of `.activity-age` — `Task` has no
     `activity_date`), a title containing a Jira key (`"BRO-22339 …"`; only `BRO`/`TRADE` map to a URL, and the
     Python `Task` has no `jira` field, so the title is the only source), and `blocked_by=["[[Open Blocker]]"]`
     so the blocked badge is derived by the seeded status cache. Do NOT pass `target_date` to `Task(...)` —
     only `Goal` has that field.
   - A second task with `status="hold"` (the hold badge) and no assignee (so it renders `+ Assign to me`).
   - A third task with `status="backlog"` and no `phase` (so it lands in the `todo` fallback column) — the
     out-of-filter case.
     (Constructing `Task(...)` requires every field with no default — `project_path`, `content`, `description`,
     `category`, `recurring`, `claude_session_id` — see the fixture at `tests/test_blocked_badge_board.py:47-70`.)
   - A goal carrying `flag=True`, `assignee`, `priority`, a `defer_date` sentinel, `modified_date` set to a
     recent datetime, a title containing a Jira key (`"BRO-22339 …"` — goals derive the badge from the title
     only) and `blocked_by=["[[Open Goal Blocker]]"]`; plus the two blocker pages seeded through a local
     `_seed_vault` (copy the pattern from `tests/test_blocked_badge_board.py:263`).

   Write these seven tests:

   - `test_topic_card_slot_set` — on `?view=topics&vault=TestVault`, a topic card carries `.topic-title-link`,
     exactly one `.flag-btn` and exactly one `.topic-detail-btn`, and carries **no** `.menu-btn`,
     `.assignee-badge`, `.assign-to-me-link`, `.priority-chip`, `.activity-age`, `.start-btn`, `.resume-btn`,
     `.live-badge`, `.hold-badge` or `.status-badge`. Assert each absence separately with `to_have_count(0)` — a single
     "no badges" regex would pass on a card that silently lost its title link too.
   - `test_topic_live_indicator_is_per_card` — exactly the fixture topic with `session_state="live"` carries
     `.topic-live-badge`, and it carries no `onclick`, no `role` and no `tabindex` attribute. Assert the
     attribute *absence* on the badge element itself, not just its presence in the DOM: the indicator must stay
     display-only.
   - `test_topic_hold_sinks_below_active_in_its_column` — read the DOM order of `.topic-card` inside
     `#cards-execution` and assert the held topic comes after the active one, even though its title sorts first.
   - `test_flagged_topic_sorts_first_in_its_column` — the flagged topic precedes the unflagged ones in the same
     column, even though its title sorts last. This is the assertion that proves flag-first applies to topics at
     all. Also assert that the two unflagged topics in that column appear in title order after the flagged one,
     so the alphabetical default is proven alongside flag-first.
   - `test_topic_outside_status_filter_renders_no_card` — on `?view=topics&status=in_progress&vault=TestVault`
     the `abandoned` topic renders no card while the `in_progress` one does.
   - `test_task_outside_status_filter_still_renders` — on `?view=tasks&status=in_progress&vault=TestVault` the
     `backlog` task still renders a card. Together with the previous test this is the whole of the filter
     parity: same renderer, opposite outcomes, decided only by the kind's config.
   - `test_task_and_goal_cards_keep_every_slot` — on `?view=tasks&vault=TestVault` for the fully-loaded task
     card and on `?view=goals&vault=TestVault` for the fully-loaded goal card,
     assert `.flag-btn`, `.hold-badge` (on the hold fixtures), `.assignee-badge` (or `.assign-to-me-link` when
     unassigned), `.priority-chip`, `.activity-age`, `.blocked-badge`, `.jira-badge`, `.menu-btn` and a session
     control (`.start-btn` or `.resume-btn`) are each present, and that no card carries `.count-badge`,
     `.defer-date`, `.target-date`, `.due-date` or `.status-badge` (none of these classes exists in `app.js`
     today — cheap guards; the sentinel-string assertion below is the load-bearing one). Then assert each seeded date sentinel string
     is absent from the card's `innerHTML` — a distinctive value is the only way "renders no date" can fail.

2. **Bump the cache-bust token** in `src/vault_ui/static/index.html` from
   `app.js?v=2026-10-30-live-session-filter` to `app.js?v=2026-10-31-unified-card-template`. An un-bumped token
   is how this repo has previously shipped a frontend fix that browsers never received.

3. **Add the superseded token to the stale list** in `tests/test_card_render_unify.py::test_cachebust_token_bumped`
   — `assert "app.js?v=2026-10-30-live-session-filter" not in INDEX_HTML`, beside the three tokens already
   listed. Do not pin the new value: the test asserts the shape, and pinning it makes every future bump fail the
   test that is supposed to guard the bump.

4. **Add the `## Unreleased` section to `CHANGELOG.md`** — the file opens at `## v0.102.0`, so create the section
   above it. One bullet, in the file's existing voice (a single long sentence, present tense, naming the
   mechanism and the user-visible effect): the three board renderers and three card builders collapse into one
   config-driven card template and one board renderer, with each kind contributing only a config entry and a
   data mapper; the topic board gains the shared status filter, the flag-first sort and the active → upcoming →
   hold bucketing it never had, while keeping only the affordances its API supports (flag, open note, detail,
   display-only live indicator); task and goal cards are unchanged; the `app.js` cache-bust token is bumped.
   The bullet must contain the phrase **unified card template** — the verification greps for it.

5. **Point `docs/frontend-card-render.md` at the browser lane.** Add or extend a short section naming
   `tests/test_card_parity.py` as the integration proof, stating that it is integration-marked, deselected by
   `make test`, and run on the host with `uv run playwright install chromium` then `make test-integration`.
</requirements>

<constraints>
- **Frontend-only plus the changelog.** The change lives in `tests/test_card_parity.py`,
  `src/vault_ui/static/index.html`, `tests/test_card_render_unify.py`, `CHANGELOG.md` and
  `docs/frontend-card-render.md`. No Go handler, no Pydantic model, no new route, no change to
  `src/vault_ui/api/models.py`, `pkg/api/` or `scripts/parity/cases.txt` — this change adds no response key, so
  no new `topics-…` row is required in the parity cases.
- **Do NOT run `make test-integration`** — the container has no Chromium. Write the tests so they collect; the
  operator runs them.
- **Do NOT weaken the existing topic assertions.** `tests/test_topics_view.py` must keep passing unchanged on the
  host: the topic cards still carry `.topic-card`, `.topic-title-link`, `.topic-detail-btn`, no `.status-badge`,
  and sit in the column their `phase` names.
- **Do NOT change drag-and-drop semantics**, the topic detail modal, or add a take-over / start / resume control
  to a topic card.
- **Do NOT add new topic response fields.** `TopicResponse` stays at its 8 keys; `Topic.session_state` stays
  always-null in the Python harness, which is why the live-indicator test sets `session_state="live"` directly on
  its fixture.
- **Do NOT bump the version or tag** — the release is cut by the operator.
- New Python files must satisfy ruff (`line-length = 100`) or `make precommit` fails.
- Existing tests must still pass. Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run `make precommit` — must pass. It covers go-vet, the Go suite, the non-integration Python suite, ruff and mypy.

Then confirm the ship mechanics and that the new suite is collectable. Each command discriminates between the
unchanged and the updated tree.

- `grep -c 'app.js?v=2026-10-31-unified-card-template' src/vault_ui/static/index.html` — must print `1`.
- `! grep -q 'app.js?v=2026-10-30-live-session-filter' src/vault_ui/static/index.html`
- `grep -c 'app.js?v=2026-10-30-live-session-filter' tests/test_card_render_unify.py` — must print `1`.
- `test "$(awk '/^## /{sec=$0} /unified card template/{print sec; exit}' CHANGELOG.md)" = "## Unreleased"`
- `uv run pytest --collect-only -q -m integration tests/test_card_parity.py | grep -c '::test_'` — must print at
  least `7`. This imports the module and enumerates its tests without starting a browser, so it fails on a
  syntax error, a bad import or an unmarked test, which is the most the container can prove about this file.
- `grep -c 'test_card_parity.py' docs/frontend-card-render.md` — must print at least `1`.
- `uv run pytest tests/test_card_render_unify.py -q` — must exit 0.

Do not attempt `make test-integration`; it cannot run here. Before you finish, re-run the commands above and
confirm each prints what it says it must, then walk each numbered requirement against the diff. Do not report
success on a check you did not run.
</verification>
