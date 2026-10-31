---
status: approved
spec: [037-unify-board-renderer-and-card-template]
created: "2026-10-31T14:12:00Z"
queued: "2026-10-31T14:27:18Z"
---

# One board renderer with per-kind entry points

<summary>
- All three boards — tasks, goals and topics — now populate their columns through one shared renderer
- The per-kind entry points shrink to a single delegation call each, so no board can drift from the others again
- Topics gain the same status filter, the same flag-first sorting and the same active / upcoming / hold ordering the other two boards already had
- A held topic now sinks below the active topics in its column instead of sitting wherever the title sort put it
- A flagged topic now sorts above an unflagged one in the same column
- Topics keep ordering by title within the flag group, and the default board order does not change for tasks or goals
- The empty "—" column is still hidden when it holds nothing, now from one place instead of two
- A test now proves only one function on the page clears the columns, so a second renderer cannot be reintroduced unnoticed
</summary>

<objective>
Replace the three forked board renderers with one shared renderer that every kind delegates to, so the topic board inherits the status filter, the shared sort pipeline and the active → upcoming → hold bucketing the other two boards already use — while a per-kind entry point stays as thin as a single call.
</objective>

<context>
Prompt 1 has already landed: `src/vault_ui/static/app.js` now has a `CARD_CONFIG` map with a `fallbackColumn`,
`statusFilter` and `draggable` field per kind, three data mappers, and a single `createCard(kind, item)` builder.
This prompt consumes that config; it adds no new card markup.

Read these before changing anything. Anchors are function names, never line numbers.

- `src/vault_ui/static/app.js`
  - `renderTasks` — clears six phase containers, sorts with `sortBoardItems(a, b, 'task')`, splits into
    recently-completed / hold / upcoming / active, appends each card at `displayPhase(task.phase, 'todo')`, and
    pins recently-completed cards to the bottom of the Done lane. Its bucketing comments are the contract for the
    shared renderer.
  - `renderGoals` — clears seven containers (including `unknown`), sorts with `'goal'`, splits into
    hold / upcoming / active, and calls `syncUnknownColumn()` at the end.
  - `renderTopics` — clears seven containers, applies `currentStatuses.includes(t.status || 'in_progress')`,
    sorts **inline** with `localeCompare` (the fork this prompt removes), and calls `syncUnknownColumn()`.
  - `syncUnknownColumn` — toggles `hidden` on the single global `.kanban-column[data-phase="unknown"]`, with a
    null guard. Keep the function; only its call sites change.
  - `displayPhase(phase, fallback)`, `sortBoardItems(a, b, kind)`, `defaultSortCompare(a, b, kind)` — the sort
    pipeline. `defaultSortCompare` already branches on `kind` for goals; the topic profile joins it there.
  - `CARD_CONFIG` — added by prompt 1. The renderer reads `statusFilter` and `fallbackColumn` from it.
  - `docs/frontend-card-render.md` — written by prompt 1: the `CARD_CONFIG` field table (slot names, capability
    flags, fallback column) and the static-assertion lane. Read it before adding the renderer; it is the written
    contract this prompt consumes.
  - `docs/dod.md` — the repo's definition of done.
- `tests/test_card_render_unify.py` — the harness prompt 1 rewrote. Add the renderer assertions here; it already
  has `_function_body` and `_slice_function_body`.
- `tests/test_unknown_column_hidden.py` — `test_render_goals_syncs_after_populating` and
  `test_render_topics_syncs_after_populating` bind to the two entry points this prompt empties. They merge into
  one assertion against the shared renderer. The `renderColumnHeaders` and `removeGoalCard` tests in the same
  file stay untouched — those two call sites do not move.
- `tests/test_goal_defer_filter.py::test_load_goals_buckets_upcoming_to_bottom` — asserts the literal bucket
  variable names inside `renderGoals`. Re-target it to the shared renderer.
- `tests/test_board_sort.py` — the browser suite for the sort control. It is integration-marked and runs only on
  the host; do not run it, and do not change it.
</context>

<requirements>
1. **Add `renderBoard(kind, items)`** to `src/vault_ui/static/app.js` as a top-level `function` declaration,
   placed where `renderTasks` is today. It takes the kind and the array of API items and does all of the work:

```js
function renderBoard(kind, items) {
    const config = CARD_CONFIG[kind];

    // Clear every phase container, including the "—" one goals and topics
    // without a phase land in. The Tasks view has no such column; the
    // getElementById null-guard covers it.
    ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done', 'unknown'].forEach(id => {
        const container = document.getElementById(`cards-${id}`);
        if (container) container.innerHTML = '';
    });

    // The topics endpoint returns every topic by design, so its status filter
    // is applied here; the task and goal endpoints filter server-side, so
    // their config leaves this off. slice() keeps the caller's array intact.
    const visible = config.statusFilter
        ? items.filter(item => currentStatuses.includes(item.status || 'in_progress'))
        : items.slice();

    // Every kind goes through the one sort pipeline: flag-first, then the
    // kind's own profile (see defaultSortCompare).
    visible.sort((a, b) => sortBoardItems(a, b, kind));

    // recently_completed is a task-only field; for goals and topics the
    // filter is empty and the bucketing below sees every item.
    const pinned = visible.filter(item => item.recently_completed);
    const rest = visible.filter(item => !item.recently_completed);

    // Hold takes precedence over upcoming so a held item with a future
    // defer_date still sinks to the bottom (parked, not actionable now).
    const holdItems = rest.filter(item => item.status === 'hold');
    const upcomingItems = rest.filter(item => item.status !== 'hold' && item.upcoming);
    const activeItems = rest.filter(item => item.status !== 'hold' && !item.upcoming);

    [...activeItems, ...upcomingItems, ...holdItems].forEach(item => {
        const container = document.getElementById(
            `cards-${displayPhase(item.phase, config.fallbackColumn)}`
        );
        if (container) container.appendChild(createCard(kind, item));
    });

    // Recently completed always go to the done lane, at the very bottom.
    if (pinned.length > 0) {
        const doneContainer = document.getElementById('cards-done');
        if (doneContainer) pinned.forEach(item => doneContainer.appendChild(createCard(kind, item)));
    }

    syncUnknownColumn();
}
```

   The body must contain **no** comparison against a kind name — not `=== 'task'`, `=== 'goal'` or `=== 'topic'`.
   Every kind difference comes from `config`. Keep the four explanatory comments: they are the only written
   record of why the bucketing order is what it is.

2. **Reduce the three entry points to delegation.** Each keeps its name and its existing call sites —
   `setupSortControl`, the `loadTasks` / `loadGoals` / `loadTopics` loaders, `applyOptimisticWrite` and the
   `runSession` loading-modal close handler all call them by name — and its body contains only the delegation
   call: no `filter(`, no `.sort(`, no `displayPhase`, no `syncUnknownColumn`, no `innerHTML`.

```js
// Render the board from the tasks cache, ordered by the active sort key.
// Split out of loadTasks so a sort change can re-render without refetching.
function renderTasks() {
    renderBoard('task', Object.values(tasksCache));
}

// Render the goals board from the goals cache, ordered by the active sort key.
// Split out of loadGoals so a sort change can re-render without refetching.
function renderGoals() {
    renderBoard('goal', Object.values(goalsCache));
}

// Render the topics board from the topics cache. The status filter lives in
// the shared renderer, gated on the kind's config (see loadTopics).
function renderTopics() {
    renderBoard('topic', Object.values(topicsCache));
}
```

   Do not change the loaders (`loadTasks`, `loadGoals`, `loadTopics`), `syncUnknownColumn` itself,
   `renderColumnHeaders` or `removeGoalCard` — their `syncUnknownColumn()` calls stay where they are.

3. **Give the topic kind its sort profile.** In `defaultSortCompare(a, b, kind)`, add a topic branch beside the
   existing goal branch, before the task fall-through: flag-first is already handled by `sortBoardItems`, so the
   profile is title, then id for a deterministic tiebreak.

```js
    if (kind === 'topic') {
        // Topics carry no dates and no priority, so there is no urgency tier
        // and no priority comparison: title first, id for a stable tiebreak.
        return (a.title || a.id || '').localeCompare(b.title || b.id || '');
    }
```

   Do not change the goal branch or the task branch. Do not add a sort parameter, a new sort mode, or a
   `statusFilter`-style toggle for sorting.

4. **Delete the inline alphabetical sort** from `renderTopics` — it is gone with the body (requirement 2). No
   `localeCompare` may remain inside `renderTopics` or `renderBoard`; the only `localeCompare` calls in the file
   live in `defaultSortCompare`.

5. **Add the renderer assertions to `tests/test_card_render_unify.py`.** The suite already has `_function_body`
   and `_slice_function_body`; add a helper `_all_function_bodies()` that finds every top-level function
   declaration with `re.compile(r"^(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(", re.MULTILINE)` and returns
   `{name: body}` using the brace walker. Then add:

   - `test_one_board_renderer` — the set of function names whose body contains `container.innerHTML = ''` is
     exactly `{"renderBoard"}`. This is deliberately not a count of the literal in the file: a renamed helper
     elsewhere would satisfy a count while leaving the fork in place.
   - `test_board_entry_points_only_delegate` — for each of `("renderTasks", "task", "tasksCache")`,
     `("renderGoals", "goal", "goalsCache")`, `("renderTopics", "topic", "topicsCache")`, the body contains
     `renderBoard('<kind>', Object.values(<cache>))` and contains none of `filter(`, `.sort(`, `displayPhase`,
     `syncUnknownColumn`, `innerHTML`.
   - `test_renderer_reads_the_config_map` — the `renderBoard` body contains `config.statusFilter`,
     `config.fallbackColumn`, `sortBoardItems` and `createCard(kind, item)`, and contains no `localeCompare`.
   - `test_renderer_has_no_kind_branch` — the `renderBoard` body contains none of `=== 'task'`, `=== 'goal'`,
     `=== 'topic'`.

6. **Merge the two per-kind sync assertions** in `tests/test_unknown_column_hidden.py` into one. Replace
   `test_render_goals_syncs_after_populating` and `test_render_topics_syncs_after_populating` with a single
   `test_render_board_syncs_after_populating` that slices `renderBoard` and asserts `syncUnknownColumn();`. Update
   the module docstring: it currently says "the two renderers"; it must now name the shared renderer plus the
   header rebuild and the goal deletion path, which are the only remaining call sites. Leave
   `test_render_column_headers_syncs_after_building_the_lane`, `test_goal_deletion_syncs_without_a_full_rerender`,
   the two `syncUnknownColumn` body tests and the CSS test untouched.

7. **Re-target `tests/test_goal_defer_filter.py::test_load_goals_buckets_upcoming_to_bottom`** onto the shared
   renderer. Its docstring keeps the intent — deferred (upcoming) goals sink below active ones, mirroring tasks —
   and the assertions become the bucket declarations and the spread order inside `renderBoard`:
   `const holdItems = rest.filter(item => item.status === 'hold')`,
   `const upcomingItems = rest.filter(item => item.status !== 'hold' && item.upcoming)`,
   `const activeItems = rest.filter(item => item.status !== 'hold' && !item.upcoming)` and
   `[...activeItems, ...upcomingItems, ...holdItems].forEach(`. Assert against
   `_slice_function_body(APP_JS, "renderBoard")`, not the whole file — a whole-file `in` check also passes
   against a renamed copy elsewhere. `test_load_goals_sends_upcoming_hours` and
   `test_create_goal_card_greys_upcoming` stay as prompt 1 left them.
</requirements>

<constraints>
- **Frontend-only.** The change lives in `src/vault_ui/static/app.js` and the two static test files. No Go
  handler, no Pydantic model, no new route, no change to `src/vault_ui/api/models.py` or
  `scripts/parity/cases.txt`.
- **Do NOT merge the three boards into one stream.** The Tasks / Goals / Topics toggle stays a strict kind
  selector; the three entry points stay separate and each stays bound to its own cache.
- **Do NOT change drag-and-drop column semantics.** Tasks stay draggable, goals and topics stay non-draggable;
  `handleDrop` is untouched.
- **Do NOT add a feature flag** that lets a kind fall back to a per-kind renderer.
- **Do NOT add new topic response fields.** `TopicResponse` stays at its 8 keys; `Topic.session_state` stays
  always-null in the Python harness. Topics have no `upcoming` field, so their upcoming bucket stays empty while
  the code path remains shared.
- **Do NOT render counts, or defer / target / due dates, on any card.**
- **REUSE, do not fork or reinvent:** `displayPhase`, `sortBoardItems`, `normalizePriority`, `syncUnknownColumn`,
  `createCard`, `CARD_CONFIG`. Do not introduce a second clearing helper, a second bucketing helper, or a
  per-kind `sortBoardItems` call with a hardcoded kind.
- **The websocket handler's own `kind === 'topic'` branch is untouched** — it dispatches refetches per view and
  has nothing to do with rendering.
- **Escaping discipline is unchanged** — this prompt adds no markup.
- Integration-marked tests are deselected by `make test`. Do not add any, and do not run `make test-integration`
  in the container.
- **This prompt deliberately does not bump the `app.js?v=` token in `src/vault_ui/static/index.html`** — prompt 3
  does, together with the CHANGELOG. Until prompt 3 lands, an already-open board runs the old forked script
  (spec Failure Modes: "`app.js` changed but `?v=` not bumped").
- Existing tests must still pass. Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run `make precommit` — must pass. It covers go-vet, the Go suite, the Python suite, ruff and mypy.

Nothing in this container executes `app.js` — `make precommit` lints Python and Go only, and the static tests
read the file as text. A syntax error in `renderBoard` would pass every check below. The runtime proof is
prompt 3's Playwright lane (`make test-integration`, operator rung); do not claim the board renders until that
has run on the host.

Then confirm the shape mechanically. Each command discriminates between the forked and the unified tree.

- `grep -c "container.innerHTML = ''" src/vault_ui/static/app.js` — must print `1` (it prints `3` before).
- `grep -c 'function renderBoard(' src/vault_ui/static/app.js` — must print `1`.
- `awk '/^function renderTasks/,/^}/' src/vault_ui/static/app.js | grep -c 'renderBoard'` — must print `1`.
- `awk '/^function renderGoals/,/^}/' src/vault_ui/static/app.js | grep -c 'renderBoard'` — must print `1`.
- `awk '/^function renderTopics/,/^}/' src/vault_ui/static/app.js | grep -c 'renderBoard'` — must print `1`.
- `! awk '/^function renderTopics/,/^}/' src/vault_ui/static/app.js | grep -q localeCompare`
- `! awk '/^function renderBoard/,/^}/' src/vault_ui/static/app.js | grep -q localeCompare`
- `! awk '/^function renderBoard/,/^}/' src/vault_ui/static/app.js | grep -qE "=== '(task|goal|topic)'"`
- `awk '/^function defaultSortCompare/,/^}/' src/vault_ui/static/app.js | grep -c "kind === 'topic'"` — must
  print `1`. Scope the check to that one function: the file already carries five other `kind === 'topic'`
  comparisons (`app.js` ~:1395, ~:1398, ~:1423 optimistic-write keying, ~:1829 flag route, ~:3019 websocket
  dispatch), and a file-wide count would report `6` on a correct tree.
- `uv run pytest tests/test_card_render_unify.py tests/test_unknown_column_hidden.py tests/test_goal_defer_filter.py -q`
  — must exit 0.

Before you finish, re-run the commands above and confirm each prints what it says it must, then walk each
numbered requirement against the diff. Do not report success on a check you did not run.
</verification>
