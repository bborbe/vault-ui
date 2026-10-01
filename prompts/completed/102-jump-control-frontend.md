---
status: completed
summary: Added a jump control beside the live badge on live cards whose payload resolves a pane, gated on jump_pane, with a non-navigating jumpToPane handler, styling, bumped cachebust tokens, tests, and a CHANGELOG entry
execution_id: vault-ui-jump-to-pane-exec-102-jump-control-frontend
dark-factory-version: v0.196.0
created: "2026-10-01T14:40:00Z"
queued: "2026-10-01T13:45:58Z"
started: "2026-10-01T13:46:41Z"
completed: "2026-10-01T13:50:00Z"
---

# Draw a Jump Control Beside the Live Badge on Cards That Resolve a Pane

<summary>
- A live card carries a control that hands the operator to that session's terminal pane
- The control sits beside the live indicator and never replaces it
- Clicking it moves the operator's terminal and leaves the board exactly where it was
- A live card whose session has no reachable pane shows no control at all
- Clicking the live indicator itself still takes the session over, exactly as it does today
- Nothing else on the card, or in the filters, changes
</summary>

<objective>
Draw a jump control on every live card whose payload carries a resolvable pane, so the operator can move from the board to the running session in one click. The control is deliberately a separate element from the live indicator: that indicator is already a destructive affordance, and folding a second action into it would mean a click aimed at jumping ends the session instead.
</objective>

<context>
Read `docs/dod.md` for the definition of done the daemon validates against. (This repo carries no `CLAUDE.md`: it is gitignored, so it is absent from the worktree and from the container. Project conventions live in `README.md` and `docs/`.)

The backend half of this feature lands on this branch immediately **before** this prompt — it is `prompts/in-progress/101-jump-route-backend.md`, approved and run to completion first. The order is not incidental: this prompt changes static assets, and the backend prompt's own `<verification>` asserts they are untouched, so the two must not run concurrently in the same worktree. It added:

- `jump_pane: str | None` on `TaskResponse` — the pane id the session resolves to, or `null`. **This is the only signal the control renders against.** A card shows the control if and only if `jump_pane` is non-null.
- `POST /api/tasks/{task_id}/jump?vault=X` — performs the hand-off server-side and answers `204` with no body. It refuses with `409` when no pane resolves, `503` when the credential is unreadable, and `502` when the jump server is unreachable. The shared credential is read on the server and is never present in any response.

Read these before writing anything:

- `src/vault_ui/static/app.js` — `sessionButtonHtml(kind, item)` is the function that renders the card's action area. (`hasSession` is a local const inside it, derived from `item.claude_session_id` — not a parameter.) It returns the `● Live` badge (`<span class="live-badge" ... onclick="takeOverSession('${kind}', '${escapeJsAttr(item.id)}')" ...>● Live</span>`), the `⏳ Starting...` badge, or the Start/Resume button. The jump control is added here.
- `src/vault_ui/static/app.js` — `takeOverSession(kind, id)` is what the live badge calls. Read it to see the shape a click handler takes in this file (`showToast`, `parseErrorResponse`, the `item.vault` query parameter). Do not modify it.
- `src/vault_ui/static/app.js` — `runSession(kind, id)` is the other card action and shows the same fetch-and-toast idiom.
- `src/vault_ui/static/style.css` — `.live-badge` is the style to sit beside; match its visual weight rather than competing with it.
- `src/vault_ui/api/models.py` — `jump_pane` is the field this renders against. Read it rather than assuming its name.

The board's DOM contract, as documented for driving it: a card is `.task-card[data-task-id="<task title>"]`, the live indicator is `.live-badge`, and `data-task-id` is the **task title, not a UUID** — it is prefixed with the Jira issue key for Jira-backed tasks. Assertions must match on element *and* text, never on class alone: `.start-btn` and `.resume-btn` share classes, and `.live-badge` and `.starting-badge` are different elements with different meanings.
</context>

<requirements>
1. **Render the control** in `sessionButtonHtml` in `src/vault_ui/static/app.js`. When the item's `session_state` is `"live"` **and** `item.jump_pane` is truthy, the returned markup carries the existing `● Live` badge **plus** a jump control as a sibling element.

   The badge's markup and its `onclick="takeOverSession(...)"` must be reproduced **byte-identical** to today. The jump control is an addition beside it, never a wrapper around it, never a replacement for it, and never a second handler on it.

   When `session_state` is `"live"` but `jump_pane` is null or absent, the return value is exactly what it is today — the badge alone, with no control and no placeholder, no disabled button, and no empty element.

   Keep the live-branch condition literal `} else if (item.session_state === 'live')` unchanged, and add the `item.jump_pane` test **inside** the branch rather than folding `&& item.jump_pane` into the condition: `tests/test_activity.py::test_starting_badge_wired_into_starting_branch` splits the function on that exact string, and folding the condition makes its `find` return `-1`, turning an unrelated assertion into a failure. Keep the addition compact — four existing tests slice this function with a fixed 4000-character window, and the `runSession('${kind}'` literal that one of them asserts currently sits at offset 3196.

   Every non-live branch (`⏳ Starting...`, Start, Resume, indeterminate Resume) returns exactly what it returns today.

2. **The control's markup**: a `<span>` carrying the class `jump-btn`, `role="button"`, `tabindex="0"`, an `onclick="jumpToPane('${kind}', '${escapeJsAttr(item.id)}')"`, and a visible label. Use a text glyph and a `title` attribute naming the destination, in the same style the badges already use — the repo has no icon set and no build step, so do not introduce one.

   Escape the id through `escapeJsAttr` exactly as the neighbouring handlers do. Do not interpolate `item.jump_pane` into the markup: the client does not need the pane id, and putting it in the DOM would spread a value the server owns.

3. **The click handler** `async function jumpToPane(kind, id)`, in `src/vault_ui/static/app.js` beside `takeOverSession`. It mirrors that function's argument-injection guard and cache lookup, then:

   - `POST`s to `/api/${base}/${encodeURIComponent(id)}/jump?vault=${encodeURIComponent(item.vault)}` where `base` is `'goals'` or `'tasks'` as `takeOverSession` computes it.
   - On a `2xx`: **do not navigate, do not reload, do not re-render, and do not read the response body.** The server answers `204` with no body, so the `await response.json()` that `takeOverSession` performs would throw on the *success* path and surface a spurious error toast. The operator's focus has already moved; the board must stay exactly as it is, and a reload here would scroll it and undo the point of the feature.
   - On a non-`2xx`: `showToast(await parseErrorResponse(response), true)`, matching the sibling handlers' error path. Do not clear the control on failure — the payload said the pane resolves, and a transient failure is not evidence that it does not.
   - On a thrown fetch error: `showToast(error.message, true)` and `console.error`, as the siblings do.

4. **Style** the control in `src/vault_ui/static/style.css`, next to the `.live-badge` rule. It must be visually subordinate to the badge — the badge states a fact, the control offers an action — and it must not change the badge's own box, colour or position. Give it a `:hover` state and a `cursor: pointer`, matching how the badges signal interactivity.

5. **Tests.** This repo has no JavaScript test runner — there is no `package.json` and no node. Every `app.js` test reads the file as a string and slices it with the `_slice(marker, length)` helper in `tests/test_card_render_unify.py`. Write the static assertions in a new `tests/test_jump_control.py`, slicing `sessionButtonHtml` the same way:

   - The `sessionButtonHtml` slice contains the literal `class="jump-btn"` **and** the guard substring `item.jump_pane` — proving the control is rendered inside that function and gated on the payload field, rather than merely appearing somewhere in the file.
   - The slice still contains the literal `onclick="takeOverSession('${kind}', '${escapeJsAttr(item.id)}')"`. The regression this guards against is a control that quietly took the badge's click.
   - A `.jump-btn` rule exists in `src/vault_ui/static/style.css`.

   ⚠️ **Do not write substring assertions that pretend to prove conditional behaviour.** A text assertion cannot show that the control is absent when `jump_pane` is null, nor that the Start / Resume / `⏳ Starting...` branches emit none — those are runtime properties of a single function body. `assert 'jump-btn' not in slice` is unsatisfiable, and `assert 'jump-btn' in slice` passes unconditionally because the literal sits in the body whichever branch emits it. Both are false-positive tests, which is worse than no test. That conditional behaviour is covered only by the Playwright case below, which the container cannot run.

   The path literal `/jump` and the field name `jump_pane` must match `prompts/in-progress/101-jump-route-backend.md`'s route declaration and model field exactly. Put a comment beside each naming the backend line it mirrors, so a rename on either side is visible rather than silent.

   Write a Playwright case in the repo's integration suite that loads a board with a stubbed live card carrying a `jump_pane`, clicks `.jump-btn`, and asserts the request went to the jump route and the page did not navigate. ⚠️ **The container has no browser and cannot run this.** It must still be written and must be syntactically valid and importable there; do not add it to `<verification>`.

6. **Cachebust tokens** in `src/vault_ui/static/index.html`: bump **both** query values — `style.css?v=` and `app.js?v=` — to a new token naming this change (e.g. `2026-10-01-jump-control`). They currently read `2026-09-30-topic-card-affordance` and `2026-09-30-no-session-chip`.

   This is not ceremony. The token is what a browser keys its cache on, and this repo has shipped an un-bumped token four times — the v0.71.0 through v0.71.3 entries in `CHANGELOG.md` are all this same failure, and v0.71.3 records that nothing enforces the bump. The four `test_cachebust_*` tests assert only the token's *shape* plus a list of historical stale values, so a missed bump passes `make precommit` green while a browser holding the board open across the deploy keeps serving the old `app.js` and `style.css` — and the operator never sees the control.

7. **CHANGELOG.md**: add a `feat:` bullet for the visible control to the `## Unreleased` section the previous prompt created. Never create a second `## Unreleased` heading.

8. **Self-check.** Before finishing, re-run every command in `<verification>` and confirm each passes; then walk each numbered requirement above against the change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- **Do not change the `● Live` badge's behaviour.** Its click takes the session over: it opens a confirm dialog and then terminates the running process, losing in-flight work. Adding a jump action to that element would turn a click meant to navigate into one that kills the session. The badge keeps its `onclick="takeOverSession(...)"` and its markup verbatim.
- **Do not change the backend.** `src/vault_ui/api/` is owned by the previous prompt on this branch. If the payload lacks something the control needs, that is a finding to state in a comment — not an edit here.
- Do not introduce a framework, a build step, a bundler, or a dependency. This is a plain static file served as-is.
- Do not write `jump_pane`, the pane id, or the credential into the DOM, a `data-` attribute, or a log line. The control renders on the field's presence, never on its value.
- Do not add a control to a card that is not live, and do not add one to goal cards. Goals **do** carry `session_state` and a live goal renders the `● Live` badge, so the shared helper reaches the goal call site too; goals are excluded only because the previous prompt populates `jump_pane` for tasks alone and its route is tasks-only. Gate on `item.jump_pane` — never on `kind` — and leave the live badge rendering unchanged for goals.
- Existing tests must still pass unchanged, including `tests/test_card_render_unify.py`, `tests/test_goal_card_cleanup.py`, and `tests/test_view_toggle.py`. They read `app.js`, `style.css` and `index.html` as text, so keep the structures they assert on intact.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
- All paths in this prompt are repo-relative.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass (format + test + lint + typecheck).

Then confirm the change is wired, not merely present:

- `grep -q 'function jumpToPane' src/vault_ui/static/app.js` -- must succeed, proving the handler exists rather than only being referenced from markup.
- `grep -q 'jump-btn' src/vault_ui/static/app.js` -- must succeed, proving the control is rendered.
- `grep -q 'jump-btn' src/vault_ui/static/style.css` -- must succeed, proving it is styled rather than left unstyled.
- `grep -q 'jump_pane' src/vault_ui/static/app.js` -- must succeed, proving the control is gated on the payload field.
- `grep -q "onclick=\"takeOverSession(" src/vault_ui/static/app.js` -- must succeed, proving the live badge's take-over handler survived the change.
- `! grep -rq 'jump-token\|jump_token' src/vault_ui/static/` -- no frontend file carries the credential's path. (`-r` is required: without it grep exits 2 on a directory operand and `!` turns that into an unconditional pass.)
- `grep -qE 'app\.js\?v=2026-10-01' src/vault_ui/static/index.html` -- must succeed, proving the `app.js` cachebust token was bumped. A shape-only check cannot see a missed bump, which is why this asserts the new value.
- `grep -qE 'style\.css\?v=2026-10-01' src/vault_ui/static/index.html` -- must succeed, proving the `style.css` token was bumped too. Bumping only one leaves half the change cached.
- `grep -q '^## Unreleased' CHANGELOG.md` -- must succeed, and the section must carry both bullets rather than a second heading.

`make test-integration` is deliberately not run here: the container has no browser. `make precommit` runs the unit suite, which deselects the Playwright cases via the repo's `-m 'not integration'` addopts.
</verification>
