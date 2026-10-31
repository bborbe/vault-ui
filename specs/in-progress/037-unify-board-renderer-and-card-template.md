---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-10-31T14:00:56Z"
generating: "2026-10-31T14:27:24Z"
prompted: "2026-10-31T14:27:24Z"
verifying: "2026-10-31T16:46:54Z"
branch: dark-factory/unify-board-renderer-and-card-template
---

## Summary

- The Vault UI renders three boards — tasks, goals, topics — through three forked renderers and three forked card builders. This spec collapses them onto one board renderer and one card template.
- Per-kind differences move out of markup and into two places only: a per-kind config map (which affordances a kind carries) and a per-kind data mapper (which wire fields a kind contributes). No card markup is written per kind.
- This reverses spec 017's Non-goal that forbade a single `createCard()` — the operator ruled on 2026-10-31 that a single config-driven builder is now the target.
- The topic card gains the parity it has never had: the same status filter, the same sort pipeline (flag-first, then alphabetical), and the same active → upcoming → hold bucketing the other two boards use.
- Topic affordances stay exactly what the API supports today: flag, open note, detail. The menu, session controls and status controls render from config and stay off for topics — no new backend routes.

## Problem

The board has three parallel render paths that were forked, not shared. Card render: `createTaskCard` (`src/vault_ui/static/app.js:1751`) is the only caller of `cardShellHtml` (`:1679`); `createGoalCard` (`:1844`) and `createTopicCard` (`:1252`) each build their own `innerHTML`. Today `grep -c 'card\.innerHTML ='` = 3, `grep -cE 'function create[A-Za-z]*Card\('` = 3, `grep -c 'cardShellHtml('` = 2 (definition plus one call site). Board render: `renderTasks` (`:1026`), `renderGoals` (`:1121`), `renderTopics` (`:1218`) each clear their own containers (`grep -c "container.innerHTML = ''"` = 3). The divergence is not cosmetic — only topics apply the status filter; only tasks and goals use `sortBoardItems` (topics sort alphabetically at `:1227`); only tasks and goals bucket active → upcoming → hold; unphased tasks fall back to `todo`, goals and topics to `unknown`.

Spec 017 deliberately stopped at shared helpers with thin kind wrappers and wrote that boundary into its Non-goals: no single monolithic `createCard()`. The cost of that boundary is now visible — the goal card duplicates the task card's shell, badges, menu button and session control inline, and the topic card is the thinnest of the three only because it never got the parity the other two received. The test harness encodes the fork as a rule: `tests/test_card_render_unify.py:45-51` allows the goal to keep inline HTML, and `:50` asserts `"function createCard(" not in APP_JS`. Every future card change is still written up to three times, and a topic card cannot inherit a card-level improvement without a fourth edit.

## Goal

The Vault UI renders all three boards through one board renderer and one card template. A kind supplies only two things: an entry in a per-kind config map (which affordances it carries, its unphased fallback column, whether its status filter applies) and a data mapper that maps its API response onto one common card view-model. The card template contains no per-kind markup and no per-kind branch. Tasks and goals render exactly as they do today; topics gain the shared status filter, the shared sort pipeline (flag-first, then alphabetical) and the shared active → upcoming → hold bucketing, while keeping only the affordances the API actually supports (flag, open note, detail, live indicator). A developer changing a card affordance edits the template once and all three boards inherit it.

## Non-goals

- **This spec reverses spec 017's Non-goal "Do NOT collapse card render into a single monolithic `createCard()`".** The operator ruled on 2026-10-31 that one config-driven builder is the target; spec 017's shared-helpers-plus-kind-wrappers boundary is superseded for card render. Its behavior-function collapse (run, menu, dispatch, clear-session) stays as it shipped.
- Do NOT add backend routes for topics. `GET /topics`, `GET /topics/{id}` and `PATCH /topics/{id}/flag` are the whole surface; there is no `/topics/{id}/run`, `/session`, `/status`, `/phase` or `/assign-to-me` and this spec adds none.
- Do NOT add new topic response fields, and do NOT backfill topic frontmatter. `TopicResponse` stays at its 8 keys; `Topic.session_state` stays always-null in the Python harness.
- Do NOT render counts, or defer / target / due dates, on any card. No card kind gains them.
- Do NOT introduce an objective or theme hierarchy, or a fourth board kind.
- Do NOT change the topic detail modal (`openTopicDetail`, `#topic-modal`) — its status line, tracked-work list and empty state stay as they are.
- Do NOT add a take-over, start, resume or jump control to a topic card; the topic live indicator stays display-only.
- Do NOT add a feature flag that lets a kind fall back to a per-kind renderer — the unification is the goal, and an escape hatch would re-fork it. If a future consumer demands per-kind markup, that is a separate spec.
- Do NOT merge the three boards into one stream; the Tasks / Goals / Topics toggle stays a strict kind selector.
- Do NOT change drag-and-drop column semantics: tasks stay draggable, goals and topics stay non-draggable.

## Acceptance Criteria

- [ ] **One card builder.** `grep -cE 'function create[A-Za-z]*Card\(' src/vault_ui/static/app.js` returns `1` (was 3) and `grep -c 'card\.innerHTML =' src/vault_ui/static/app.js` returns `1` (was 3), so a renamed-but-kept fork fails. A static test slices the survivor's whole body (`_function_body`, pattern at `tests/test_card_render_unify.py:17-28`) and asserts (a) the body contains none of `=== 'task'`, `=== 'goal'`, `=== 'topic'`, and (b) the body carries the file's only `'task-card'` class assignment — so a builder that delegates to a renamed per-kind body (`createTaskCardInner`, a builder lookup table) fails even when the grep counts and the kind-name check both pass. Evidence: grep counts + static test.
- [ ] **One board renderer.** `grep -c "container.innerHTML = ''" src/vault_ui/static/app.js` returns `1` (was 3), and a static test **slices every top-level function body in `app.js` and asserts the column-clearing assignment appears inside exactly one of them** — not merely that the literal occurs once, which a renamed helper elsewhere could satisfy. `syncUnknownColumn()` moves into the shared renderer (it toggles the single global `.kanban-column[data-phase="unknown"]`, `app.js:1163-1167`, so it has no per-kind state to preserve). Any surviving `renderTasks` / `renderGoals` / `renderTopics` entry point has a body that contains only a delegation call to the shared renderer — no `filter(`, no `.sort(`, no `displayPhase`, no `syncUnknownColumn` — and the renderer body itself contains no kind-name comparison (`=== 'task'` / `=== 'goal'` / `=== 'topic'`), so neither a renamed-but-kept forked renderer nor a renderer re-branched by kind passes. Evidence: grep count + static test.
- [ ] **Per-kind config map.** A static test asserts one object literal in `app.js` carries an entry for each of the three kinds, and that the entries encode the 2026-10-31 operator decision: the topic entry enables flag, note link, detail and the display-only live indicator, and disables menu, session controls, status controls and drag; the task and goal entries keep today's capabilities (menu, session Start/Resume, drag for tasks only, detail for none). The same static test asserts the card builder and the board renderer both **read specific fields off that map** — the builder reads at least one slot flag, the renderer reads `statusFilter` and `fallbackColumn` — so a config that is present but empty, decorative, or merely named (`void CARD_CONFIG;`) fails. Evidence: static test.
- [ ] **Topic field mapping.** A static test asserts the topic mapper reads exactly `id`, `title`, `status`, `phase`, `session_state`, `flag`, `vault`, `obsidian_url`, and produces a view-model in which the assignee, priority, activity, blocked and Jira slots are empty. Integration evidence: a rendered topic card carries no `.menu-btn`, no `.assignee-badge`, no `.priority-chip`, no `.activity-age`, no session button, and no status badge; it does carry the title link, the flag button, the live indicator when `session_state` is `live`, and the detail button. Evidence: Playwright test under `make test-integration` — **operator rung**, not container-verifiable.
- [ ] **Topic filter / sort / bucket parity.** A static test asserts the shared renderer applies the status filter and `sortBoardItems` unconditionally, with the per-kind toggles coming only from the config map (no inline alphabetical sort remains for topics). Integration evidence: a `hold` topic renders below an active topic in the same column; a flagged topic renders above an unflagged topic in the same column; a topic whose status is outside the active filter renders no card; and a task or goal whose status is outside the active filter still renders a card (the filter stays off for those kinds). Evidence: Playwright test under `make test-integration` — **operator rung**, not container-verifiable.
- [ ] **Task and goal cards unchanged.** Integration evidence (operator rung): a task card and a goal card each still carry flag, hold badge, assignee badge or `+ Assign to me`, priority chip, activity age, blocked badge, Jira badge, menu button and session Start/Resume, and neither carries a count, a defer date, a target date or a due date. Evidence: Playwright test under `make test-integration`, plus the six existing static tests passing **after re-targeting** — they bind to per-kind function names AC1 removes, so "unchanged" is impossible; each assertion must be carried over to the shared builder or renderer (see AC7).
- [ ] **Static harness inverted, every guard carried over.** `grep -c 'function createCard(' tests/test_card_render_unify.py` returns `0` — the spec-017 no-monolith assertion at `:50` is gone — and the goal-inline allowance at `:45-51` is replaced by an assertion that the goal renders through the shared builder. The six name-bound tests that would otherwise be deleted are re-targeted, each keeping its assertion: `test_task_wrapper_keeps_urgency_and_jira` (task urgency tier, Jira heading, shell call), `test_goal_wrapper_keeps_onhold_and_dataset` (goal `dataset.goalId`, `dataset.kind`, on-hold), `test_flag_control_present_on_all_card_kinds` (no inlined `class="flag-btn` on any kind), `test_topic_card_drops_status_badge_and_gates_the_live_indicator` (topic: no `status-badge`, `session_state === 'live'` gate, `topic-live-badge`) — all in `tests/test_card_render_unify.py` — and the two `syncUnknownColumn` assertions in `tests/test_unknown_column_hidden.py`, which **merge into one against the shared renderer body** rather than surviving as two per-kind checks. Evidence: test-file diff showing each assertion present against the new symbols, plus `uv run pytest tests/test_card_render_unify.py tests/test_unknown_column_hidden.py -q` exiting 0.
- [ ] **Cache-bust token bumped.** `git diff src/vault_ui/static/index.html` shows the `app.js?v=` value changed from `2026-10-30-live-session-filter`, and the static token test lists that value among the known-stale tokens (pattern `tests/test_card_render_unify.py:111-125`). Evidence: file diff + static test.
- [ ] **No wire-shape change, parity untouched.** `git diff --stat pkg/api/ src/vault_ui/api/models.py scripts/parity/cases.txt` is empty — this spec adds no Go response key, so no `topics-…` row is required in `scripts/parity/cases.txt`. (If an implementation does add a response key, a matching case row in `scripts/parity/cases.txt` is mandatory and this AC fails.) Evidence: negative diff.
- [ ] `make precommit` exits 0. Evidence: exit code.
- [ ] `make test-integration` passes on the host, including the rewritten topics view and the new card-parity cases. Evidence: exit code — **operator rung**, not container-verifiable.

**Scenario coverage — NO new scenario.** The behavior is reachable by the repo's existing static-assertion harness over `app.js` plus the Playwright lane that already drives all three boards in-process against a mocked vault-cli (`tests/test_topics_view.py`). No real deployment interaction is involved; the browser E2E is the top of this feature's pyramid and it already exists.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```bash
make precommit
uv run pytest tests/test_card_render_unify.py -q
grep -cE 'function create[A-Za-z]*Card\(' src/vault_ui/static/app.js        # expect 1
grep -c 'card\.innerHTML =' src/vault_ui/static/app.js                      # expect 1
grep -c "container.innerHTML = ''" src/vault_ui/static/app.js               # expect 1
grep -c 'function createCard(' tests/test_card_render_unify.py              # expect 0
```

Note: `make test` and `make precommit` deselect `integration`-marked tests (`addopts = "-m 'not integration'"`, `pyproject.toml:87`), so a green `make precommit` says nothing about the Playwright lane.

### Operator-executable (runs on the host after PR merge)

```bash
uv run playwright install chromium   # once
make test-integration
```

Expected: exit 0; the topics view, the task board and the goal board tests all pass.

⚠️ **production-touching:** the restart below replaces the running board process.

```bash
make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
```

Open `127.0.0.1:8000`, switch across Tasks / Goals / Topics, and confirm each board renders as before except for the topic sort/bucket change named above (a flagged topic now sits first, a held topic sinks).

## Desired Behavior

1. One card template builds every card. It takes a kind and a view-model, resolves the kind's config, and emits the whole card — header, title link, blocked row, Jira row, footer-left, actions — from those two inputs. No branch on kind name, and no second function that assembles card markup.
2. The per-kind config map is the single place a kind's affordances are declared: unphased fallback column, whether the status filter applies, whether drag is enabled, and which footer / action slots are populated (flag, hold, assignee or assign-to-me, priority, activity age, blocked badge, Jira badge, live indicator, session control, menu, detail). Topics declare flag, note link, detail and live indicator only.
3. Each kind has one data mapper from its API response onto the common view-model. The topic mapper contributes title (falling back to id), note URL, flag and session state, and leaves every slot the topic wire shape does not carry empty — so no badge renders for an absent field rather than rendering `undefined`.
4. One board renderer clears the phase columns once, filters by status where the kind's config says so, sorts through `sortBoardItems` for every kind, splits into active → upcoming → hold for every kind, and appends each card into `displayPhase(phase, config.fallbackColumn)`. Per-kind entry points delegate to it and carry no logic.
5. Topics inherit the shared pipeline: the status filter still applies, the flag marker now sorts first, the default order stays alphabetical by title (falling back to id), and a held topic sinks below active topics in its column. Topics have no `upcoming` field, so the upcoming bucket stays empty for them while the code path remains shared.
6. Task and goal cards render exactly the same fields as today, now produced by the shared template, the shared config and the existing helpers (`flagButtonHtml`, `blockedBadgeHtml`, `sessionButtonHtml`, `activityAgeHtml`, `titleWithIconHtml`).
7. The `app.js` cache-bust token is bumped so an already-open board fetches the new script.

## Assumptions

- The topic wire shape is exactly 8 keys and carries no `priority`, `assignee`, `activity_date`, `blocked` or Jira field — so a topic card cannot render those badges without a backend change, which this spec excludes. The unified template simply receives empty slots for a topic.
- `Topic.session_state` is `null` from the Python harness and classified only by the Go backend. The integration lane drives the mocked response directly, so the live-indicator assertion sets `session_state: "live"` on a fixture topic rather than depending on a real session.
- Topics carry `status` and `phase`, so the shared status filter and the hold bucket apply unchanged; they carry no `upcoming`, so their upcoming bucket is empty while the bucketing code path stays shared.
- The shared default sort's non-goal branch is task urgency (due/planned dates), which a topic has none of; the topic profile therefore orders by title with an id tiebreak, after the flag-first comparison, to keep the order deterministic.
- Static tests read `app.js` as text and slice a function body to the next top-level `function` declaration (`_function_body`, `tests/test_card_render_unify.py:17-28`); this is the repo's established lane for frontend assertions.
- Integration tests run the app in-process on a random port against a mocked vault-cli and are deselected by `make test`.

## Constraints

- **Frontend-only.** The change lives in `src/vault_ui/static/app.js`, its static test harness, `src/vault_ui/static/index.html` (token), `CHANGELOG.md`, and any doc whose described behavior changes. No Go handler, no Pydantic model, no new route.
- **REUSE, do not fork or reinvent:** `flagButtonHtml`, `blockedBadgeHtml`, `sessionButtonHtml`, `activityAgeHtml`, `titleWithIconHtml`, `displayPhase`, `sortBoardItems`, `normalizePriority`, `escapeHtml`, `escapeJsAttr`, `applyOptimisticWrite` / `overlayPendingWrites`, `syncUnknownColumn`. These are already shared or kind-agnostic.
- **Topic wire shape frozen at 8 keys** — `id`, `title`, `status`, `phase`, `session_state`, `flag`, `vault`, `obsidian_url`. `Topic.session_state` stays `null` in the Python harness (`src/vault_ui/api/models.py:85`).
- **Frozen behavior that must not regress:**
  - Tasks drag between phase columns; goals and topics stay non-draggable (a goal's column is owned by `vault-cli goal approve`).
  - `.live-badge` remains the take-over control only; the topic indicator keeps its own display-only class and carries no `onclick`, `role` or `tabindex` (`topic-live-badge`, `app.js:1267`, no handler — shipped with spec 036, whose spec file is stale but whose work is in tags `v0.101.0`+).
  - No card kind renders a `.status-badge` — the column is the phase.
  - The id arg-injection guard (ids beginning with `-` rejected before any fetch) stays on the merged session path.
  - Every interpolated value keeps its current escaping: DOM text through `escapeHtml`, values inside an `onclick` string through `escapeJsAttr`.
- **Integration tests are deselected by `make test`** — the rewritten topics-view and card-parity tests run only under `make test-integration`, so `make precommit` cannot catch a break in them. The operator rung is mandatory.
- **Document the contract, don't only spec it.** The config-map contract (slot names, capability flags, fallback column), the card slot inventory and the static-assertion lane move into `docs/frontend-card-render.md` in prompt 1 and are referenced from here — today that knowledge exists only in `app.js` comments and the three builders, which is exactly what this change removes.
- Follow `docs/dod.md`: `## Unreleased` CHANGELOG entry; ≥80% coverage on changed behavior.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---------|-------------------|----------|
| A mapper omits a slot a kind's config declares | The template renders an empty badge or the literal `undefined` on the card | Revert the prompt's commit and make the config declare only slots the mapper fills |
| A kind is missing from the config map | Its board renders no cards, or the renderer throws on the lookup | Revert the prompt's commit; add the missing entry and re-run the three-board integration test |
| The status filter leaks onto tasks or goals | Cards disappear from the Tasks or Goals board for statuses outside the active filter | Revert the prompt's commit; restore the config-gated filter and re-run the out-of-filter integration case |
| The topic sort keeps an inline alphabetical sort beside the shared one | Flag-first ordering does not apply to topics; the fork survives under a new name | Revert the prompt's commit; delete the per-kind sort and re-run the flagged-topic integration case |
| A per-kind markup branch survives under a renamed builder | Grep counts pass, behavior re-forks | Revert the prompt's commit; re-run `uv run pytest tests/test_card_render_unify.py -q` against the body-slice assertion |
| `app.js` changed but `?v=` not bumped | An already-open board runs the old forked script | Revert the prompt's commit; bump the token and re-run the stale-token test |
| Playwright lane never run | `make precommit` green while the browser behavior is broken | Do not merge; run `make test-integration` on the host first |
| An interpolated title or assignee loses its escaping during the merge | Stored markup executes in the board — an XSS regression | Each slot keeps its helper; review the template's interpolation sites; no raw `${item.title}` in an attribute |

## Security / Abuse Cases

- No new trust boundary: the same three read routes and the one flag write are used. Topic actions stay limited to flag, open note and detail — the menu, session and status controls are config-off because the routes behind them do not exist.
- Attacker-controllable input is vault frontmatter rendered as HTML: titles, assignees, blocker names, Jira keys, note URLs. The unified template must route each through the same helper it uses today — `escapeHtml` for DOM text, `escapeJsAttr` for values interpolated into an `onclick` string. A merge that interpolates a raw field is a stored-XSS regression and fails review.
- The id arg-injection guard on the session path must survive the merge once, covering every kind that can reach it.
- No new file paths, uploads or free-form user text reach any endpoint.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Per-kind config map, three data mappers, one card template/builder; rewrite the static harness (`test_card_render_unify.py`) to assert the single builder, the config entries and the topic view-model | 1, 2, 3 | 1, 3, 4 (static half), 7 | — |
| 2 | One board renderer; per-kind entry points delegate; topic filter/sort/bucket parity through the shared pipeline | 4, 5 | 2, 5 (static half) | prompt 1 |
| 3 | Playwright coverage: topic card slot set, topic sort/bucket/filter, task and goal card parity; cache-bust bump; CHANGELOG; `make precommit` + `make test-integration` | 6, 7 | 4 (integration half), 5 (integration half), 6, 8, 9, 10, 11 | prompts 1, 2 |

Rationale: prompt 1 establishes the template, the config and the mappers that everything else consumes; prompt 2 rewires the board onto them and delivers the topic parity; prompt 3 proves the whole surface in the browser and ships the token. The split keeps each prompt to one cohesive concern even though all three touch `app.js`.

## Alternatives Considered

- **Topic parity only, keeping spec 017's boundary.** Add the missing affordances to `createTopicCard` and leave three builders. Rejected: it is the cheapest path and the one that produced today's state — every future card change is still three edits, and the operator ruled on 2026-10-31 that a single reusable template is the target precisely because per-kind markup is where the differences come from.
- **One `createCard` with `if (kind === …)` branches inside the body.** Rejected: spec 017 rejected it for good reason and the rejection still holds — a branchy monolith is not a template. This spec takes the third shape the operator named: one template plus a per-kind config map and data mapper, with no per-kind markup anywhere.
- **Extend the topic API so topics can carry assignee, priority and dates.** Rejected for this spec: it is backend work with a wire-shape change, and it would let the card render fields the vault's topic pages do not carry. It stays a separate option if topic parity ever needs those badges.

## Do-Nothing Option

Do nothing and the three renderers keep drifting. The board works as today — the do-nothing cost is not a visible break, it is that every card-level change is written up to three times, the goal card keeps duplicating the task shell inline, and the topic card stays the thin outlier that cannot inherit a card improvement without a fourth edit. Spec 017 bought the behavior-function collapse and paid for it by freezing the render fork in a test assertion; leaving that assertion in place makes the fork look intentional while the operator has ruled it is not.
