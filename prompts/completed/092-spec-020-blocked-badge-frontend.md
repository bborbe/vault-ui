---
status: completed
spec: [020-blocked-by-show-dont-hide]
summary: Added a clickable blocked-by badge to blocked task and goal cards that names open blockers and navigates to (and marks) the blocker's own card, with static + Playwright integration tests and a CHANGELOG entry
execution_id: vault-ui-blocked-by-exec-092-spec-020-blocked-badge-frontend
dark-factory-version: dev
created: "2026-09-11T07:52:44Z"
queued: "2026-09-11T09:20:16Z"
started: "2026-09-11T09:29:29Z"
completed: "2026-09-11T09:35:27Z"
---

# Show a "blocked by" badge on task and goal cards that links to the blocker

<summary>
- Blocked task and goal cards carry a visible "blocked by X" badge naming their open blockers
- The badge is a click target: clicking it jumps to the blocker's card on the same board and marks it
- When the blocker is not on the board at all, the click says so instead of failing silently
- Cards whose blockers are all done, and cards with no blockers, render exactly as they do today
- The blocker name is rendered as plain text, never as markup, so a crafted task name cannot inject HTML
- Navigation stays inside the board's own data — no URLs, no selectors built from a task name
- Both the Tasks and the Goals board show the badge
- Browser caches pick up the new script and stylesheet automatically

<!-- OPEN QUESTION (for the human reviewer, not an instruction to the agent):
     Two points where the spec leaves room, both resolved here and both easy to flip:
     (1) The spec says the badge "lists all blocker names" but that clicking goes to "the
         first not-completed blocker". This prompt renders the names from the API's
         `blockers` list (the not-completed subset) so the visible badge and the click
         target can never disagree. Prompt 1 keeps the raw `blocked_by` list on the
         response if the reviewer prefers rendering that instead.
     (2) The spec says the badge "is a clickable link". It is rendered as a clickable
         span carrying the existing `.assignee-badge.clickable` pattern rather than an
         `<a href>`, because the navigation is in-page and an anchor would need a
         meaningless href. -->
</summary>

<objective>
Make the dependency graph visible. Prompt 1 stopped the board from deleting blocked tasks and made the API report, per task and per goal, whether it is blocked and which of its blockers are still open. This prompt renders that state: a "blocked by X" badge on the card, and a click on the badge that takes the operator to the blocker's own card on the same board. The badge is the only visual change — unblocked cards must look exactly as they do today.
</objective>

<context>
This repo has no `CLAUDE.md`. Project conventions live in the existing code, in `docs/dod.md` (the configured `validationPrompt`), and in the test suite.

Prompt 1 (already executed on this branch) added the data this prompt renders: `TaskResponse` and `GoalResponse` now carry `blocked: bool` and `blockers: list[str]` (the bracket-stripped names of the declared blockers that are **not** completed, in `blocked_by` order), and `GoalResponse` now carries `blocked_by` too. `blockers` is `[]` when nothing is blocking.

Read these source files before editing:

- `src/vault_ui/static/app.js` — the file that changes most.
  - `createTaskCard(task)` and `createGoalCard(goal)` are the two card renderers. Each builds a `footerLeft` template literal containing `${holdBadge}`; that is where the new badge belongs. `createTaskCard` delegates its shell to `cardShellHtml(kind, id, obsidianUrl, title, footerLeftHtml, startButtonHtml)`; `createGoalCard` writes its shell inline.
  - The existing badge patterns to copy: `holdBadge` (`task.status === 'hold' ? '<span class="hold-badge" ...>' : ''`) and `assigneeBadge` (`<span class="assignee-badge clickable ..." onclick="filterByAssignee('...')">`).
  - `escapeHtml(text)` (DOM-based text escaper) and `escapeJsAttr(value)` (escapes a value for a single-quoted JS string literal living inside a double-quoted HTML attribute — escapes backslash and apostrophe for JS plus `&`, `"`, `<`, `>` for HTML). Every inline `onclick` argument in this file goes through `escapeJsAttr`; the project shipped a bug fix (v0.63.7) for exactly the case where it did not.
  - `showToast(message, isError = false)` renders a transient `div.toast` (with `.error` when `isError`).
  - Cards are addressable: task cards carry `dataset.taskId` (`createTaskCard`), goal cards carry `dataset.goalId` and `dataset.kind = 'goal'` (`createGoalCard`). `renderTasks()` and `renderGoals()` rebuild cards into `#cards-<column>` containers.
- `src/vault_ui/static/style.css` — the `.hold-badge` rule (pill shape, `max-width: fit-content`, `white-space: nowrap`) is the visual reference for the new badge; `.task-card.on-hold` shows the existing "this card is out of the active flow" accent treatment.
- `src/vault_ui/static/index.html` — the two cache-busting tokens: `<link rel="stylesheet" href="style.css?v=...">` and `<script src="app.js?v=...">`. Both must be bumped whenever their file changes; the repo has previously shipped a fix browsers never received because a token was not bumped.
- `tests/test_board_sort.py` — the Playwright integration-test pattern to mirror: `pytestmark = pytest.mark.integration`, a `live_server` fixture that runs the real FastAPI app in-process on a free port (`_free_port`, `_wait_for_port`, `uvicorn.Server` on a daemon thread) with `vault_ui.api.tasks.get_vault_cli_client_for_vault` patched to a mock client, a `wide_viewport` autouse fixture, and a `_column_ids` DOM helper.
- `tests/test_card_render_unify.py` and `tests/test_goal_card_cleanup.py` — the static-assertion pattern for frontend changes: read `app.js` / `index.html` / `style.css` as text at module import, slice a function body out with a regex helper (`_slice_function` in `test_goal_card_cleanup.py`), and assert on the source.
- `tests/test_board_sort.py` also shows how the mock client is built from `Task(...)` / `Goal(...)` dataclasses.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `test-pyramid-triggers.md` — why the per-function behaviour is asserted statically while the browser test is marked integration.
- `changelog-guide.md` — `## Unreleased` rules and bullet style.
- `documentation-guide.md` — only if the change alters documented behaviour in `README.md`.
</context>

<requirements>

1. **Add a shared badge builder** to `src/vault_ui/static/app.js`, next to the other card helpers (e.g. beside `holdBadge`'s consumers, or immediately above `createTaskCard`):
   ```js
   function blockedBadgeHtml(kind, item) { ... }
   ```
   Contract:
   - Returns `''` when `item.blocked` is falsy — no markup of any kind, so an unblocked card's HTML is unchanged.
   - Otherwise returns a clickable badge span whose visible text starts with the literal `blocked by ` followed by every name in `item.blockers` joined with `, `.
   - The span carries the class `blocked-badge` plus `clickable` (matching the `.assignee-badge.clickable` convention).
   - Its inline `onclick` calls `navigateToBlocker('<kind>', '<first blocker>')`, where `<kind>` is the function's own `kind` argument (`'task'` / `'goal'`, passed by the caller — the same kind-first convention as `sessionButtonHtml(kind, item)`), and the first blocker is `item.blockers[0]` — the first still-open blocker, which is the navigation target the spec names.
   - Every name that reaches the DOM as text goes through `escapeHtml(...)`; the name interpolated into the inline `onclick` goes through `escapeJsAttr(...)` — not `escapeHtml` alone, which decodes `&#39;` back to a raw apostrophe before the JS parser runs.
   - Guard against an empty `item.blockers` even when `blocked` is set: render nothing rather than an empty name.

2. **Render the badge on both card types.** Add `${blockedBadgeHtml('task', task)}` to the `footerLeft` template literal in `createTaskCard` and `${blockedBadgeHtml('goal', goal)}` to the `footerLeft` template literal in `createGoalCard`, alongside the existing `${holdBadge}`. Change nothing else about either renderer: the badge is additive, and no card class, column assignment, drag handler, menu button, session button or Obsidian title link is touched.

3. **Add the navigation function** to `src/vault_ui/static/app.js`:
   ```js
   function navigateToBlocker(kind, name) { ... }
   ```
   Contract:
   - First clear the navigation marker from any card that currently carries it (`document.querySelectorAll('.blocked-target')` → `classList.remove('blocked-target')`), so only one card is ever marked.
   - Find the target card by comparing dataset attributes on the already-rendered cards — `card.dataset.taskId === name` for tasks, `card.dataset.goalId === name` for goals, selected by the `kind` argument. **Never build a CSS selector out of `name`** (no `[data-task-id="${name}"]` interpolation) — a task name may contain quotes, brackets and spaces, and interpolating it into a selector is both a correctness bug and the path-traversal-shaped input the spec's Security section rules out. No URL is constructed, no `fetch` is issued, no navigation away from the board.
   - When the card is found: add the `blocked-target` class to it and call `card.scrollIntoView({ block: 'center' })`. The marker stays until the next `navigateToBlocker` call or the next board re-render (do not schedule a removal timer — the marker is the observable result of the click).
   - When no card matches (the blocker is filtered off the board, deferred, or in the other view): call `showToast(...)` with an error toast naming the blocker, e.g. `Blocker not on the board: <name>`. Return without throwing. A silent no-op is not acceptable — the operator must be able to tell "the blocker is not shown right now" from "the click did nothing".

4. **Style the badge and the marker** in `src/vault_ui/static/style.css`, following the existing `.hold-badge` rule shape (inline-flex, small uppercase-ish pill, `max-width: fit-content`, `white-space: nowrap`, a distinct colour from the violet hold badge so the two are never confused):
   - `.blocked-badge` — the badge itself; give it `cursor: pointer` so the click affordance is visible.
   - `.blocked-target` — the marker applied to the blocker card after a click; a visible outline/accent that reads clearly on top of the existing card styles (including `.task-card.on-hold`, whose opacity is already reduced).
   Add no new framework, no build step, and no JavaScript beyond the two functions above.

5. **Bump both cache-busting tokens** in `src/vault_ui/static/index.html`: the `app.js?v=...` script token and the `style.css?v=...` link token. Use the repo's `YYYY-MM-DD-<slug>` convention (e.g. `2026-09-11-blocked-badge`). A changed file served under an unchanged token is how this repo has previously shipped a fix no browser ever loaded.

6. **Static tests** in a new `tests/test_blocked_badge.py`, following the `tests/test_goal_card_cleanup.py` / `tests/test_card_render_unify.py` pattern (read the three static files as text at module import; slice function bodies with the `_slice_function` regex helper). These run inside `make test`. Cover:
   - `blockedBadgeHtml` exists and both `createTaskCard` and `createGoalCard` call it kind-first (`blockedBadgeHtml('task', task)` / `blockedBadgeHtml('goal', goal)`).
   - The badge is gated on the derived flag (`item.blocked`) and its text contains the literal `blocked by `.
   - The badge escapes names for both contexts (`escapeHtml(` and `escapeJsAttr(` appear in the builder body).
   - `navigateToBlocker` exists, compares `dataset.taskId` and `dataset.goalId`, calls `scrollIntoView` and `showToast`, and applies `blocked-target`.
   - **Negative security assertion:** neither `[data-task-id="` nor `[data-goal-id="` appears in the `navigateToBlocker` body — the blocker name is never interpolated into a selector.
   - `.blocked-badge` and `.blocked-target` are defined in `style.css`.
   - Both cache-busting tokens are present and non-empty, and the two now-stale literals are gone: `app.js?v=2026-09-02-flagged-cards-top` and `style.css?v=2026-09-01-starting-marker-wins`. Assert the token *shape* (`re.search(r"app\.js\?v=\S+", INDEX_HTML)`), never a pinned new literal — pinning the literal makes every future legitimate bump fail this test.

7. **Playwright integration test** in a new `tests/test_blocked_badge_board.py`, mirroring `tests/test_board_sort.py` exactly in structure: `pytestmark = pytest.mark.integration` (so `make test` deselects it and `make test-integration` runs it), a `wide_viewport` autouse fixture, a `live_server` fixture that starts the real app in-process on a free port with `vault_ui.api.tasks.get_vault_cli_client_for_vault` patched to a mock client backed by fixed `Task` / `Goal` lists.
   The blocked state is derived by the real backend, so drive it through the real path:
   - give the fixture tasks and goals real `blocked_by` values (e.g. `blocked_by=["[[Open Blocker]]"]`, `blocked_by=["[[Done Blocker]]"]`, and one with `blocked_by=None`);
   - create the hierarchy folders under the vault root the fixture passes to `VaultConfig` (e.g. `<tmp>/24 Tasks/*.md`, `<tmp>/23 Goals/*.md` with `---\nstatus: <status>\n---` frontmatter) and patch `vault_ui.api.tasks.get_status_cache` with a real `StatusCache` populated via `cache.load_vault("TestVault", tmp_path, "24 Tasks")` — do not hand-set the cache's internals and do not hard-code `blocked` on the fixture objects.
   Cases to assert (use `expect(...)` locators scoped to the card, not global counts):
   - a task whose blocker is `in_progress` renders on `?status=in_progress&view=tasks` (it was filtered out before prompt 1) and its card contains a `.blocked-badge` naming the blocker, while the blocker's own card is also present;
   - clicking that badge leaves the blocker's card carrying `.blocked-target`;
   - a task whose blocker is `completed` renders with **no** `.blocked-badge` anywhere in its card;
   - a task with no `blocked_by` renders with no `.blocked-badge`;
   - on `?status=in_progress&view=goals`, a goal with a `blocked_by` renders the badge naming its blocker, and clicking it marks the blocker goal's card with `.blocked-target` — proving the goal path routes with `kind='goal'`;
   - the error path: a badge whose blocker is not on the board (blocker status outside the active `?status=` filter) produces a visible error toast (`.toast.error`) rather than a silent no-op;
   - the escaping boundary: a blocker whose name contains an apostrophe (e.g. `blocked_by=["[[Peer Machines' Session Bindings]]"]`) still navigates on click — the v0.63.7 regression class, where `escapeHtml` alone decodes `&#39;` back to a raw apostrophe before the JS parser runs.

8. **CHANGELOG.** Append one bullet to the existing `## Unreleased` section of `CHANGELOG.md` (created by prompt 1; do not modify or reorder the existing bullet) describing the user-visible effect: blocked task and goal cards now carry a "blocked by" badge naming their open blockers, and clicking it jumps to the blocker's card. The bullet MUST start with a recognised conventional prefix per `changelog-guide.md` — use `feat(ui):`; a prefix-less bullet breaks dark-factory's version-bump detection.

9. **Self-check.** Before finishing, re-run every command in `<verification>` and confirm each passes; then walk each numbered requirement above against the change and confirm each is satisfied.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- The badge and its navigation use the existing card component and event wiring in `app.js` / `style.css` — no new framework, no build step, no new dependency, no bundler.
- Blocked state is display-only: never write `blocked` or `blockers` to frontmatter, never mutate a card's `status` or `phase`, and never move a card between columns because it is blocked. The Hold column keeps its existing status-driven behaviour.
- Do NOT render a badge for a card whose blockers are all completed, or for a card with no `blocked_by` — those cards must render exactly as they do today (spec AC: no visual delta on unblocked cards).
- Blocker names are rendered as plain text through the existing escaping helpers; the badge must not introduce a new `innerHTML` sink beyond the ones the two card renderers already have.
- Navigation targets are vault-local entity names resolved against the board's own rendered cards — no external URLs, no path traversal, no `fetch`, no new network calls.
- Do NOT change `src/vault_ui/api/tasks.py`, `src/vault_ui/api/models.py`, `src/vault_ui/status_cache.py`, or `src/vault_ui/vault_cli_client.py` — prompt 1 owns the backend; if the data you need is missing, say so in a comment rather than editing the backend.
- Do NOT touch the Tasks/Goals view toggle, the status/assignee/vault filters, or `renderTasks` / `renderGoals` beyond what is required — the existing explicit `?status=` filter behaviour is unchanged and blocked items are subject to the same filters as any other card.
- Existing tests must still pass unchanged, including `tests/test_card_render_unify.py`, `tests/test_goal_card_cleanup.py`, and `tests/test_view_toggle.py` (these read `app.js`, `style.css` and `index.html` as text — keep the structures they assert on intact).
- The Playwright test requires a browser and cannot run inside the container; it must still be written, and it must be syntactically valid and importable there. Do not add it to `<verification>`.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the change is actually in place:
- `grep -c 'blockedBadgeHtml' src/vault_ui/static/app.js` -- must print at least `3` (one definition, two call sites)
- `grep -c 'navigateToBlocker' src/vault_ui/static/app.js` -- must print at least `2` (one definition, one call site)
- `grep -c 'blocked-badge' src/vault_ui/static/style.css` -- must print at least `1`
- `grep -c 'blocked-target' src/vault_ui/static/style.css` -- must print at least `1`
- `grep -c 'blocked-badge\|blocked-target' src/vault_ui/static/app.js` -- must print at least `2` (badge class + marker class present in app.js, not just in style.css)
- `uv run pytest tests/test_blocked_badge.py -q` -- must pass
- `uv run pytest tests/test_blocked_badge_board.py -m integration --collect-only -q` -- must exit 0 and list at least 6 tests (proves the Playwright module imports cleanly; the browser run itself is host-side and out of scope here)
- `uv run pytest -m 'not integration' -q` -- must pass
</verification>
