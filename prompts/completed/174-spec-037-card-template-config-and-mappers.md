---
status: completed
spec: [037-unify-board-renderer-and-card-template]
summary: Collapsed the three forked card renderers into one CARD_CONFIG-driven createCard builder with per-kind data mappers, re-targeted the static suites, and documented the card contract.
execution_id: vault-ui-card-unify-exec-174-spec-037-card-template-config-and-mappers
dark-factory-version: v0.198.7
created: "2026-10-31T14:10:00Z"
queued: "2026-10-31T14:27:15Z"
started: "2026-10-31T16:33:35Z"
completed: "2026-10-31T16:39:12Z"
branch: dark-factory/174-spec-037-card-template-config-and-mappers
---

# One card template, one config map, three data mappers

<summary>
- Every card on the board — task, goal and topic — is now produced by a single builder instead of three forked ones
- Which affordances a card kind carries is declared in one table rather than written into three markup blocks
- Each kind maps its own API response onto one common card description, so the template never needs to know which kind it is rendering
- A topic card gets the fields its API actually supports and nothing else — no empty badges, no literal "undefined"
- Task and goal cards look and behave exactly as before; the topic card gains the same title-with-arrow treatment the other two have
- The topic card keeps only the flag button, the note link, the detail button and the display-only live indicator
- The old per-kind renderer assertions are replaced by assertions on the single builder, so the fork cannot come back under a new name
- The card contract — slot names, capability flags, fallback column, and the test lane that guards them — is written down for the first time
- Nothing about the wire format, the backend or the board grouping changes
</summary>

<objective>
Collapse the three forked card renderers into one config-driven template so a card-level change is written once and all three boards inherit it. Each kind contributes only data: an entry in a per-kind config map and a mapper from its API response onto one common card view-model.
</objective>

<context>
Read `docs/dod.md` for this repo's Definition of Done (the daemon's `validationPrompt`). `CLAUDE.md` is
gitignored in this repo and is not present in a worktree checkout; `README.md` has no rendering-conventions
section, only two passing mentions (`README.md:58`, `:105`) — the board's behaviour is documented in `app.js`
and in `docs/optimistic-writes.md` / `docs/liveness-classification.md`.

Read these before changing anything. Every anchor below is a function name, not a line number — the file is
3,173 lines and shifts with every edit.

- `src/vault_ui/static/app.js`
  - `cardShellHtml` — the shared shell only the task card calls today. This function is deleted by this prompt.
  - `createTaskCard`, `createGoalCard`, `createTopicCard` — the three forks. All three are deleted by this prompt.
    Read all three completely: they are the source of truth for every slot the template must reproduce.
  - `flagButtonHtml`, `blockedBadgeHtml`, `sessionButtonHtml`, `activityAgeHtml`, `titleWithIconHtml`,
    `liveBadgeLabel`, `displayPhase`, `sortBoardItems`, `normalizePriority`, `escapeHtml`, `escapeJsAttr`,
    `getUrgencyTier`, `extractJiraIssue`, `openTopicDetail` — all stay exactly as they are. Reuse them; do not
    re-implement any of them.
  - `renderTasks`, `renderGoals`, `renderTopics` — the three renderers. This prompt changes only their card
    construction call sites; their bodies are rewritten by prompt 2.
- `tests/test_card_render_unify.py` — the harness this prompt rewrites. Read `_function_body` first: it slices a
  top-level function body up to the next `\nfunction ` declaration, and it is the lane every new assertion uses.
- `tests/test_unknown_column_hidden.py` — shows the repo's brace-walking function-body slicer
  (`_slice_function_body`). Copy that helper into the new harness rather than importing across test modules.
- `tests/test_goal_card_cleanup.py`, `tests/test_goal_defer_filter.py`, `tests/test_goal_session_controls.py`,
  `tests/test_view_toggle.py`, `tests/test_card_unify_behavior.py`, `tests/test_activity.py`,
  `tests/test_blocked_badge.py`, `tests/test_live_badge_location.py`, `tests/test_task_menu.py` — nine static
  suites carry assertions that bind to the per-kind function names, per-kind helper calls, or card markup
  literals this prompt removes. Each must be re-targeted to the shared builder or a mapper in this prompt, or
  `make precommit` fails.
- `docs/optimistic-writes.md` and `docs/liveness-classification.md` — describe behaviour the cards display.
  Neither changes.
</context>

<requirements>
1. **Add the per-kind config map** to `src/vault_ui/static/app.js`, in the position `cardShellHtml` occupies
   today, as a single object literal named `CARD_CONFIG` with exactly these entries and field names. The field
   names are load-bearing: the static harness and prompt 2 both read them by name.

```js
// Which affordances a card kind carries and how its board places it. The card
// template reads this map and never branches on the kind name; a kind's
// differences live here and in its data mapper, nowhere else. See
// docs/frontend-card-render.md.
const CARD_CONFIG = {
    task: {
        map: mapTaskCard,
        fallbackColumn: 'todo',
        statusFilter: false,
        draggable: true,
        idDataset: 'taskId',
        kindDataset: null,
        vaultDataset: false,
        extraClass: '',
        titleLinkExtraClass: '',
        assignCall: 'assignToMe',
        assigneeClickable: true,
        holdTitle: 'On hold — blocked, not actively worked',
        slots: {
            noteLink: true,
            flag: true,
            hold: true,
            assignee: true,
            priority: true,
            activityAge: true,
            blocked: true,
            jira: true,
            live: false,
            session: true,
            menu: true,
            detail: false,
        },
    },
    goal: {
        map: mapGoalCard,
        fallbackColumn: 'unknown',
        statusFilter: false,
        draggable: false,
        idDataset: 'goalId',
        kindDataset: 'goal',
        vaultDataset: false,
        extraClass: 'goal-card',
        titleLinkExtraClass: '',
        assignCall: 'assignGoalToMe',
        assigneeClickable: false,
        holdTitle: 'On hold — paused, not actively worked',
        slots: {
            noteLink: true,
            flag: true,
            hold: true,
            assignee: true,
            priority: true,
            activityAge: true,
            blocked: true,
            jira: true,
            live: false,
            session: true,
            menu: true,
            detail: false,
        },
    },
    topic: {
        map: mapTopicCard,
        fallbackColumn: 'unknown',
        statusFilter: true,
        draggable: false,
        idDataset: 'topicId',
        kindDataset: null,
        vaultDataset: true,
        extraClass: 'topic-card',
        titleLinkExtraClass: 'topic-title-link',
        assignCall: null,
        assigneeClickable: false,
        holdTitle: 'On hold — paused, not actively worked',
        slots: {
            noteLink: true,
            flag: true,
            hold: false,
            assignee: false,
            priority: false,
            activityAge: false,
            blocked: false,
            jira: false,
            live: true,
            session: false,
            menu: false,
            detail: true,
        },
    },
};
```

   `assignCall` is read only inside the assignee slot, which no topic enables — keep the field present so every
   entry has the same shape. The three `map*` names the literal references are `function` declarations added by
   requirement 2; function declarations hoist, so the literal may name them even though they are written below it.

2. **Add three data mappers**, each a top-level `function` declaration (not a `const`, not an arrow assigned to a
   variable — the hoisting above and the static harness both depend on it), each returning the same view-model
   shape and **containing no markup**: no `innerHTML`, no `<div`, no `class=`. They are data, not renderers — a
   mapper that emits markup is the fork coming back under a new name.

   `mapTaskCard(task)` — read `createTaskCard` for the exact current behaviour and reproduce it as data. It must
   keep both halves of the Jira logic verbatim: the heading comes from `extractJiraIssue(task.title).title`, while
   the badge source prefers `task.jira` when it is a non-empty string and otherwise falls back to `task.title`.
   Keep `getUrgencyTier(task)`.

   `mapGoalCard(goal)` — read `createGoalCard`. Same fields minus the task-only ones; Jira comes from
   `extractJiraIssue(goal.title)` alone, because a goal response carries no `jira` field.

   Both `mapTaskCard` and `mapGoalCard` set `live: false` (neither kind has a liveness signal of its own — the
   live badge is a topic concept), `assignee: item.assignee || ''`, `blockers: item.blockers || []` and
   `activityDate: item.activity_date || null`. Spell those four out in each mapper rather than leaving them to
   inference: the builder's `view.blockers.length` gate depends on `blockers` always being an array.

   `mapTopicCard(topic)` — must read **exactly** these eight wire keys and no others: `id`, `title`, `status`,
   `phase`, `session_state`, `flag`, `vault`, `obsidian_url`. The topic response carries no assignee, priority,
   activity, blocked or Jira field, so those slots are returned empty (`''`, `null`, `[]`) — an absent field
   must render no badge, never the literal `undefined`.

   Every mapper returns this object, with these exact key names:

```
{
    id,                // string
    vault,             // string
    title,             // string — heading text; task/goal already Jira-stripped; topic falls back to id
    obsidianUrl,       // string
    status,            // string | null
    phase,             // string | null
    flag,              // boolean — item.flag === true
    live,              // boolean — the kind's own liveness signal
    hold,              // boolean — status === 'hold'
    upcoming,          // boolean
    recentlyCompleted, // boolean
    urgencyTier,       // number 0..3 for tasks, null for goal and topic
    assignee,          // string — '' when none
    priority,          // number | string | null
    activityDate,      // string | null
    blockers,          // string[] — [] when not blocked
    jiraKey,           // string — '' when none
    jiraUrl,           // string — '' when none
}
```

   `mapTopicCard` sets `live: topic.session_state === 'live'`, `upcoming: false`, `recentlyCompleted: false`,
   `urgencyTier: null`, `assignee: ''`, `priority: null`, `activityDate: null`, `blockers: []`, `jiraKey: ''`,
   `jiraUrl: ''`.

3. **Replace `cardShellHtml` and all three `create*Card` functions with one builder**, a top-level
   `function createCard(kind, item) { … }`. Delete `cardShellHtml`, `createTaskCard`, `createGoalCard` and
   `createTopicCard` entirely — no wrapper, no lookup table of markup functions, no `createTaskCardInner`.

   The body resolves `const config = CARD_CONFIG[kind];` and `const view = config.map(item);` and then contains
   **no** comparison against a kind name: the strings `=== 'task'`, `=== 'goal'` and `=== 'topic'` must not appear
   in it. Everything kind-specific comes from `config` and `view`.

   Behaviour the body must reproduce, all gated on config fields rather than on the kind:
   - `card.className = 'task-card'` plus `' ' + config.extraClass` when that is non-empty. This is the file's only
     `card.className = 'task-card…` assignment.
   - `card.draggable = config.draggable`; attach the `dragstart`/`dragend` listeners only when it is true.
   - `card.dataset[config.idDataset] = view.id`; `card.dataset.kind = config.kindDataset` when non-null;
     `card.dataset.vault = view.vault` when `config.vaultDataset` is true.
   - Class additions in this order: `urgency-overdue` / `urgency-today` / `urgency-scheduled` from
     `view.urgencyTier` (0/1/2; 3 adds nothing), `upcoming`, `recently-completed`, `on-hold` from `view.hold`,
     `flagged` from `view.flag`.
   - One `card.innerHTML = ` assignment holding the whole card, in the shape `cardShellHtml` emits today:
     the menu button (when `config.slots.menu`), `card-content` with the `task-title` heading whose anchor carries
     `class="task-title-link"` plus `config.titleLinkExtraClass`, `href="${escapeHtml(view.obsidianUrl)}"`,
     `title="Open in Obsidian"` and `titleWithIconHtml(view.title)` as its text; a `card-blocked` row when
     `config.slots.blocked` and `view.blockers.length`; a `card-jira` row when `config.slots.jira` and both
     `view.jiraKey` and `view.jiraUrl`; then `card-footer` holding `card-footer-left` and `card-actions`.
   - `card-footer-left`, in this order: the display-only live indicator when `config.slots.live && view.live`
     (`<span class="topic-live-badge">${liveBadgeLabel(null)}</span>` — no `onclick`, no `role`, no `tabindex`);
     `flagButtonHtml(kind, item)` when `config.slots.flag`; the hold badge when `config.slots.hold && view.hold`,
     using `config.holdTitle` as its `title`; the assignee block when `config.slots.assignee`; the priority chip
     when `config.slots.priority && view.priority`; the detail button when `config.slots.detail`.
   - The assignee block keeps today's two shapes: with an assignee, a `span.assignee-badge` carrying the
     `assignee-icon` and the escaped name — plus, only when `config.assigneeClickable`, the `clickable` and
     `active` classes, the `filterByAssignee(...)` onclick and its `title`; without one, an
     `a.assign-to-me-link` calling `config.assignCall`. The `active` class comes from
     `const isActiveFilter = currentAssignees.includes(view.assignee);` inside the builder — today it lives in
     `createTaskCard` (`app.js:1806`) and must not be dropped.
   - `card-actions` holds `activityAgeHtml(view.activityDate)` when `config.slots.activityAge`, then
     `sessionButtonHtml(kind, item)` when `config.slots.session`.
   - After setting `innerHTML`, when `config.slots.detail`, attach a `click` listener on `.topic-detail-btn` that
     calls `openTopicDetail(item)` — the raw item, not the view-model.
   - Escaping discipline is unchanged: DOM text through `escapeHtml`, anything interpolated into an `onclick`
     string through `escapeJsAttr`. No raw `${view.title}` or `${view.assignee}` reaches an attribute. One
     deliberate hardening: both the goal card's title href (`${goal.obsidian_url}`) and the task card's (via
     `cardShellHtml`'s unescaped `${obsidianUrl}`) interpolate the URL raw today; the shared builder escapes it
     as `${escapeHtml(view.obsidianUrl)}`. Record it in `docs/frontend-card-render.md` — prompt 3 owns the
     CHANGELOG entry, and this prompt's constraint set excludes `CHANGELOG.md`.
   - The topic card's DOM wrappers change: today it nests `.card-header > .card-title` and its footer is a bare
     `.card-footer`; the shared builder emits `.card-content > h3.task-title` and splits the footer into
     `.card-footer-left` / `.card-actions`. No static test depends on the old wrappers (verified), but
     `.card-actions { margin-left: auto }` (`style.css:710`) will push the detail button to the right edge —
     record it so the reviewer is not surprised.
   - Return the card element.

   The builder must interpolate exactly these `escapeJsAttr` arguments, once each unless stated: `view.id`
   (twice — the menu button and the assign-to-me link), `view.vault` (once, the assign-to-me link), and
   `view.assignee` (once, the clickable assignee badge). `flagButtonHtml`, `blockedBadgeHtml` and
   `sessionButtonHtml` keep their own `escapeJsAttr(item.id)` / `escapeJsAttr(item.vault)` /
   `escapeJsAttr(item.blockers[0])` calls untouched.

4. **Update the four card-construction call sites** so the boards keep rendering: in `renderTasks` replace
   `createTaskCard(task)` with `createCard('task', task)` (two call sites — the phase loop and the
   recently-completed loop); in `renderGoals` replace `createGoalCard(goal)` with `createCard('goal', goal)`;
   in `renderTopics` replace `createTopicCard(topic)` with `createCard('topic', topic)`. Do not otherwise change
   those three functions — prompt 2 rewrites them.

5. **Re-target every static suite that binds to the removed function names, per-kind helper calls, or card
   markup literals.** Each assertion must survive in meaning, moved onto the shared builder or a mapper —
   deleting an assertion is not acceptable.
   - `tests/test_goal_card_cleanup.py`: three tests slice `createGoalCard` today —
     `test_goal_card_has_only_one_obsidian_anchor`, `test_goal_card_title_link_preserves_obsidian_url` and
     `test_no_new_innerhtml_write_in_goal_card`, the last of which asserts exactly one `card.innerHTML =` write
     site. Slice `createCard` instead and
     assert exactly one anchor interpolating `obsidianUrl` (the title link); keep the `"Open in Obsidian →"`
     and `open-in-obsidian` absence assertions against the same body. In
     `test_goal_card_title_link_preserves_obsidian_url`, assert `obsidianUrl` and `task-title-link` in the builder
     body and `goal.obsidian_url` in the `mapGoalCard` body.
   - `tests/test_goal_defer_filter.py::test_create_goal_card_greys_upcoming`: assert `goal.upcoming` in the
     `mapGoalCard` body and `if (view.upcoming) card.classList.add('upcoming')` in the `createCard` body.
   - `tests/test_goal_session_controls.py::test_goal_card_renders_start_and_resume_gated_on_session`: replace
     `sessionButtonHtml('goal', goal)` with `sessionButtonHtml(kind, item)`.
   - `tests/test_view_toggle.py`: replace `"function createGoalCard" in APP_JS` with the shared builder's
     signature, and replace `"task-card goal-card" in APP_JS` with an assertion that the `goal` entry of
     `CARD_CONFIG` carries `extraClass: 'goal-card'` while the builder composes the base `'task-card'` class.
     The `goal.obsidian_url` / `task.obsidian_url` assertions in the neighbouring test keep passing unchanged —
     both mappers read those fields.
   - `tests/test_activity.py`: `test_task_card_renders_activity_age` asserts
     `activityAgeHtml(task.activity_date)` and `test_goal_card_renders_activity_age` asserts
     `activityAgeHtml(goal.activity_date)`; both become `activityAgeHtml(view.activityDate)` in the builder body,
     and each test additionally asserts its mapper fills `activityDate` from the raw field.
   - `tests/test_blocked_badge.py::test_blocked_badge_builder_exists_and_both_cards_call_it_kind_first`:
     `blockedBadgeHtml('task', task)` / `blockedBadgeHtml('goal', goal)` become the single
     `blockedBadgeHtml(kind, item)` call in the builder body.
   - `tests/test_live_badge_location.py::test_session_button_serves_both_card_kinds`:
     `sessionButtonHtml('task', task)` / `sessionButtonHtml('goal', goal)` become `sessionButtonHtml(kind, item)`.
   - `tests/test_task_menu.py`: `test_goal_card_has_menu` asserts `showMenu(event, \'goal\'` — the builder emits
     `showMenu(event, \'${kind}\'`; `test_priority_chip_replaces_goal_meta_line` asserts
     `APP_JS.count("priority-chip") >= 2` and `task.priority ?` / `goal.priority ?` — the builder emits the chip
     once, gated on `view.priority ?`.
   - `tests/test_card_unify_behavior.py::test_onclick_js_string_args_escape_titles`: re-derive the counts for the
     unified builder. After this prompt `APP_JS.count("escapeJsAttr(item.id)")` is still `5` (the four session
     helper call sites plus `flagButtonHtml`), and the new counts are
     `escapeJsAttr(view.id)` = 2, `escapeJsAttr(view.vault)` = 1, `escapeJsAttr(view.assignee)` = 1. Two existing
     count assertions must be deleted rather than re-derived: `APP_JS.count("escapeJsAttr(id)") == 1` (it counted
     `cardShellHtml`'s menu button) and `APP_JS.count("escapeJsAttr(goal.id)") == 2` (both per-kind call sites
     are gone); the `view.id` count above replaces both. Replace the
     `escapeJsAttr(task.id)` / `task.vault` / `goal.id` / `goal.vault` / `task.assignee` entries in the wrapped
     and leaked lists with their `view.*` equivalents, and change the trailing
     `"Filter by ' + escapeHtml(task.assignee)"` assertion to `view.assignee`. Keep every negative
     (`runTask('`, `showTaskMenu(event,` …) as it is.

6. **Rewrite `tests/test_card_render_unify.py`** so it guards the new shape. Keep the module docstring's
   convention, keep the `APP_JS` / `INDEX_HTML` module-level reads, keep `_function_body`, and add the
   brace-walking `_slice_function_body` helper from `tests/test_unknown_column_hidden.py` plus an
   `_object_body(source, key)` helper that finds `key + ": {"` and walks braces to the matching close. The suite
   must contain these tests:

   - `test_one_card_builder` — `re.findall(r"^function create[A-Za-z]*Card\(", APP_JS, re.MULTILINE)` has length
     1; `APP_JS.count("card.innerHTML =")` is 1; the survivor's body contains none of `=== 'task'`,
     `=== 'goal'`, `=== 'topic'`; and `APP_JS.count("card.className = 'task-card")` is 1 with that assignment
     inside the survivor's body.
   - `test_no_per_kind_markup_survives` — for each of `mapTaskCard`, `mapGoalCard`, `mapTopicCard`, the body
     contains no `innerHTML`, no `<div` and no `class=`; and none of the three removed declarations exists in
     `APP_JS` (build each name from parts, e.g. `"function create" + "TaskCard("`, so the test file itself never
     contains the literal a future grep might count).
   - `test_card_builder_reads_the_config_map` — the builder body contains `CARD_CONFIG[kind]`, `config.map(`
     and at least one `config.slots.` read, plus `config.idDataset`, `config.kindDataset`,
     `config.vaultDataset`, `config.draggable`, `config.extraClass`, `config.titleLinkExtraClass`,
     `config.holdTitle` and `config.assignCall` — every field the config map declares is read somewhere.
   - `test_boards_call_the_builder` — `APP_JS.count("createCard('task', task)")` is 2 (the phase loop and the
     recently-completed loop), `APP_JS.count("createCard('goal', goal)")` is 1 and
     `APP_JS.count("createCard('topic', topic)")` is 1; and no `createTaskCard(` / `createGoalCard(` /
     `createTopicCard(` call survives (build each name from parts).
   - `test_config_map_declares_every_kind` — the `CARD_CONFIG` literal has one entry per kind (`task: {`,
     `goal: {`, `topic: {`). Slice the outer literal by searching for `"const CARD_CONFIG = {"` and walking
     braces; `_object_body` finds `key + ": {"`, so it handles the per-kind entries only, not the outer map.
   - `test_topic_config_matches_the_operator_decision` — the topic entry has `noteLink: true`, `flag: true`,
     `detail: true`, `live: true`, `statusFilter: true`, `fallbackColumn: 'unknown'`, and `menu: false`,
     `session: false`, `draggable: false`. A comment in the test must record why `menu: false` is also what turns
     the status controls off: the status actions live in the card menu (`showMenu`), so there is no separate
     status slot to disable. The task and goal entries assert `menu: true`, `session: true`, `detail: false`,
     `statusFilter: false`, with `draggable` true for task and false for goal, and `fallbackColumn` `'todo'` and
     `'unknown'` respectively.
   - `test_topic_mapper_reads_exactly_the_wire_keys` — `set(re.findall(r"\btopic\.([a-z_]+)", body))` equals
     exactly `{"id", "title", "status", "phase", "session_state", "flag", "vault", "obsidian_url"}`.
   - `test_topic_mapper_leaves_unsupported_slots_empty` — the mapper body carries `assignee: ''`,
     `priority: null`, `activityDate: null`, `blockers: []`, `jiraKey: ''`, `jiraUrl: ''`.
   - `test_session_button_helper_shared` — keep the existing helper assertions, and assert
     `sessionButtonHtml(kind, item)` in `APP_JS` instead of the two per-kind calls.
   - `test_flag_control_present_on_all_card_kinds` — keep the helper and `toggleFlag(` assertions; replace the
     per-builder loop with: the builder body contains no `class="flag-btn`, and all three `CARD_CONFIG` entries
     enable the flag slot. Also drop the per-kind call list (`flagButtonHtml('task', task)` / `('goal', goal)` /
     `('topic', topic)`) — the builder calls `flagButtonHtml(kind, item)` once.
   - `test_task_card_keeps_urgency_and_jira` — `mapTaskCard` contains `getUrgencyTier(`, `extractJiraIssue(task.title)`
     and `task.jira`; the builder body contains `urgency-overdue`.
   - `test_goal_card_keeps_onhold_and_dataset` — the goal entry carries `idDataset: 'goalId'` and
     `kindDataset: 'goal'`; `mapGoalCard` contains `goal.status === 'hold'`; the builder adds `on-hold` from
     `view.hold`.
   - `test_topic_card_drops_status_badge_and_gates_the_live_indicator` — the builder body contains
     `status-badge` nowhere, gates the indicator on `view.live`, and emits `topic-live-badge`;
     `mapTopicCard` contains `session_state === 'live'`.
   - `test_shared_card_shell_used_by_both_kinds` is deleted outright: its
     `function cardShellHtml` and `cardShellHtml('task'` assertions name a function this prompt removes, and its
     `not in APP_JS` line is the spec-017 assertion being reversed.
   - `test_flag_check_leads_every_sort_mode` and `test_cachebust_token_bumped` — keep as they are.
   - The spec-017 assertion that forbade a single monolithic builder must be gone. `tests/test_card_render_unify.py`
     must contain **zero** occurrences of the literal `function createCard(` — that is the point of the reversal,
     and the string is what the acceptance check greps for. Reference the builder through a module-level name
     (e.g. `BUILDER = "createCard"`) and `_function_body(BUILDER)`, never by pasting the literal declaration.

7. **Write `docs/frontend-card-render.md`** — the contract this prompt establishes, so the next card change does
   not have to reverse-engineer it:
   - The two extension points: the per-kind entry in `CARD_CONFIG` and the per-kind data mapper. State plainly
     that the template itself carries no per-kind markup and no kind-name branch, and that spec 017's
     "no monolithic card builder" non-goal was reversed by the operator on 2026-10-31.
   - A table of every `CARD_CONFIG` field with its meaning, and a table of every `slots.*` name with the markup
     it gates, marking which kinds enable it.
   - The view-model shape from requirement 2, noting that a mapper returns data only.
   - The static-assertion lane: the tests read `app.js` as text and slice function bodies with `_function_body` /
     `_slice_function_body`, so a helper must stay a top-level `function` declaration to remain assertable.
   - A short "adding an affordance" recipe: edit the template once, add the slot to the config entries that want
     it, fill the mapper, extend the static test.
   - The browser lane: `tests/test_card_parity.py` (added by prompt 3) is the integration proof; the static lane
     cannot see rendering.
</requirements>

<constraints>
- **Frontend-only.** The change lives in `src/vault_ui/static/app.js`, its static test harness and
  `docs/frontend-card-render.md`. No Go handler, no Pydantic model, no new route, no change to
  `src/vault_ui/api/models.py` or `scripts/parity/cases.txt`.
- **Do NOT add backend routes for topics.** `GET /topics`, `GET /topics/{id}` and `PATCH /topics/{id}/flag` are
  the whole surface; there is no `/topics/{id}/run`, `/session`, `/status`, `/phase` or `/assign-to-me` and this
  change adds none.
- **Do NOT add new topic response fields and do NOT backfill topic frontmatter.** `TopicResponse` stays at its
  8 keys; `Topic.session_state` stays always-null in the Python harness.
- **Do NOT render counts, or defer / target / due dates, on any card.** No card kind gains them.
- **Do NOT add a feature flag that lets a kind fall back to a per-kind renderer** — the unification is the goal
  and an escape hatch would re-fork it.
- **Do NOT change the topic detail modal** (`openTopicDetail`, `#topic-modal`) — its status line, tracked-work
  list and empty state stay as they are.
- **Do NOT add a take-over, start, resume or jump control to a topic card**; the topic live indicator stays
  display-only, with its own `topic-live-badge` class and no `onclick`, `role` or `tabindex`.
- **No card kind renders a `.status-badge`** — the column is the phase.
- **REUSE, do not fork or reinvent:** `flagButtonHtml`, `blockedBadgeHtml`, `sessionButtonHtml`,
  `activityAgeHtml`, `titleWithIconHtml`, `displayPhase`, `sortBoardItems`, `normalizePriority`, `escapeHtml`,
  `escapeJsAttr`, `applyOptimisticWrite` / `overlayPendingWrites`, `syncUnknownColumn`.
- **Escaping discipline is unchanged:** DOM text through `escapeHtml`, values inside an `onclick` string through
  `escapeJsAttr`. A raw `${view.title}` or `${view.assignee}` in an attribute is a stored-XSS regression.
- **The id arg-injection guard** on the session path stays exactly as it is in `runSession`.
- Integration-marked tests are deselected by `make test` (`addopts = "-m 'not integration'"`). Do not add any.
- **This prompt deliberately does not bump the `app.js?v=` token in `src/vault_ui/static/index.html`** — prompt 3
  does, together with the CHANGELOG. Bumping it here would break prompt 3's stale-token assertion. For the same
  reason, do **NOT** add a `## Unreleased` entry to `CHANGELOG.md` — prompt 3 creates that section;
  `docs/dod.md` line 21 is satisfied there, not here.
- **Nothing in this container executes `app.js`** — `make precommit` lints Python and Go only and the static
  tests read the file as text. A syntax error in the builder passes every check here; the runtime proof is
  prompt 3's Playwright lane on the host. Do not attempt a browser run.
- Existing tests must still pass. Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run `make precommit` — must pass. It covers go-vet, the Go suite, the Python suite, ruff and mypy.

Then confirm the shape mechanically. Each command below discriminates: it reports the wrong thing on the
unmodified tree and the right thing after the change.

- `grep -cE 'function create[A-Za-z]*Card\(' src/vault_ui/static/app.js` — must print `1`.
- `grep -c 'card\.innerHTML =' src/vault_ui/static/app.js` — must print `1`.
- `! grep -q 'function createTaskCard(' src/vault_ui/static/app.js`
- `! grep -q 'function createGoalCard(' src/vault_ui/static/app.js`
- `! grep -q 'function createTopicCard(' src/vault_ui/static/app.js`
- `! grep -q 'function cardShellHtml(' src/vault_ui/static/app.js`
- `! grep -q 'function createCard(' tests/test_card_render_unify.py` — the spec-017 no-monolith assertion is gone.
- `grep -c 'CARD_CONFIG\[kind\]' src/vault_ui/static/app.js` — must print at least `1`.
- `uv run pytest tests/test_card_render_unify.py tests/test_unknown_column_hidden.py -q` — must exit 0.
- `uv run pytest tests/test_goal_card_cleanup.py tests/test_goal_defer_filter.py tests/test_goal_session_controls.py tests/test_view_toggle.py tests/test_card_unify_behavior.py -q`
  — must exit 0; these are the re-targeted suites.
- `test -f docs/frontend-card-render.md`
- `command -v node >/dev/null && node --check src/vault_ui/static/app.js` — must exit 0 when node is present
  (the only in-container check that would catch a syntax error in the new builder)

Before you finish, re-run the commands above and confirm each prints what it says it must, then walk each
numbered requirement against the diff. Do not report success on a check you did not run.
</verification>

<!--
Open question, resolved inside this prompt rather than left for the agent:

1. The spec's acceptance criterion names four capabilities the topic entry must disable — "menu, session
   controls, status controls and drag" — but the card carries no separate status-control surface: every status
   action (complete, hold, abort, defer) lives in the card menu built by `showMenu`. A `statusControl` flag would
   therefore be dead configuration that nothing reads. The prompt resolves this as `menu: false`, documents the
   equivalence in `docs/frontend-card-render.md`, and records it in the test's own comment so the mapping is
   auditable. If the operator later wants a status affordance outside the menu, it becomes its own slot then.
2. The spec does not say whether a held topic shows the hold badge. Its acceptance criterion for the topic card
   lists the affordances the API supports (flag, note link, detail, live indicator) and names no hold badge, so
   the topic entry disables the hold slot. Held topics still sink below active ones in their column — that is the
   renderer's bucketing, which reads `status`, not the badge slot.
-->
