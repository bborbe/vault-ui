---
status: approved
spec: [025-lazy-pane-resolution-at-jump-time]
created: "2026-10-06T06:16:43Z"
queued: "2026-10-06T06:48:20Z"
---

# Offer the jump control on live session state alone

<summary>
- A live task card offers the jump control whenever its session is live, and nothing else is consulted.
- A live task whose terminal pane cannot currently be resolved still offers the control; the failure is reported when it is clicked, never by hiding the control.
- Clicking the control when no pane resolves shows a visible error on the card instead of doing nothing.
- A goal card never offers the control, because there is no jump route for a goal.
- The served frontend no longer reads or mentions the pane field anywhere.
- The frontend test that pinned the old pane-field gate is updated to pin the new rule instead.
</summary>

<objective>
Make the frontend offer a task's jump control from live session state alone, so the control's availability stops depending on a server-resolved pane id that no longer exists in the list response. A live session is what the control needs; whether a pane resolves is a fact the click discovers, not a reason to hide the affordance.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-lazy-pane-resolution-at-jump-time.md`. This prompt covers Desired Behavior 5 and Acceptance Criterion AC6.

Prerequisite: the previous prompt dropped `jump_pane` from the Go list response, so a task payload no longer carries the field at all. Read `src/vault_ui/static/app.js` and `tests/test_jump_control.py` before editing.

Read these regions of `src/vault_ui/static/app.js` (anchor by name, not line number):

- `function sessionButtonHtml(kind, item)` — called as `sessionButtonHtml('task', task)` and `sessionButtonHtml('goal', goal)`. Its `else if (item.session_state === 'live')` branch builds `liveBadge` and then, today, returns `liveBadge` alone when `!item.jump_pane` and `liveBadge + '<span class="jump-btn" ...>'` otherwise.
- `async function jumpToPane(kind, id)` — POSTs to `/api/${base}/${encodeURIComponent(id)}/jump?vault=...` where `base` is `goals` for a goal and `tasks` otherwise. On a non-2xx it calls `showToast(await parseErrorResponse(response), true)` and returns; on a transport failure it calls `showToast(error.message, true)`. Its non-2xx branch carries a comment that says "the payload said it resolves", which is no longer true.
- `function handleTaskUpdate(data)` — the frame dispatcher. A `kind: 'task'` frame with a type other than `deleted` calls `loadCurrentView()`; a `kind: 'goal'` frame calls `loadGoals()`. The 60-second fallback poll (`POLL_INTERVAL_MS`) is the only other refresh path.
- `src/vault_ui/static/style.css` — the `.jump-btn` rules, and the comment above them that describes the control as appearing when `jump_pane` is set. That comment is now wrong.

Read `tests/test_jump_control.py` in full. It slices `app.js` as text (no `package.json`, no node in this repo) and today asserts `"item.jump_pane" in body` inside `test_jump_control_rendered_inside_session_button_helper`, with a module docstring that names `jump_pane` as the contract the control renders against. `make precommit` runs `uv run pytest`, so this test file must be updated in this prompt or the build goes red.

Two other static tests slice `sessionButtonHtml` with a 4000-character window and assert `"runSession('${kind}'" in body` — `tests/test_card_render_unify.py` and `tests/test_goal_session_controls.py`. The `runSession('${kind}'` token sits only ~200 characters inside that window today, so keep the replacement comment in requirement 1 exactly as short as written there; a longer comment pushes the token out of the window and turns both tests red. Do not edit those two files.

`src/vault_ui/static/index.html` loads both assets with a cache-bust token (`style.css?v=2026-10-01-jump-control`, `app.js?v=2026-10-01-jump-control`). Requirement 4 bumps it.

The frontend is a plain embedded JS/CSS tree — there is no build step, no bundler and no `package.json`; the only tooling that touches it is `uv run ruff` (Python only) and the pytest assertions that read the files as text. No frontend coding guide applies.

The coding plugin docs are available in the container at `/home/node/.claude/plugins/marketplaces/coding/docs/`:

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
</context>

<requirements>

### 1. Gate the jump control on live session state, for tasks only

In `sessionButtonHtml`'s `item.session_state === 'live'` branch, replace the `if (!item.jump_pane) { return liveBadge; }` guard with a kind guard, so the control is emitted whenever the card's session is live and the card is a task:

```js
        // Jump control — a sibling of the badge, never a second handler on it: the
        // badge's click ENDS the session, so navigation must not ride on it. Offered
        // on live session state alone, and only for tasks: a goal card has no jump
        // route. Route: POST /tasks/{id}/jump.
        if (kind !== 'task') {
            return liveBadge;
        }
        return liveBadge + `<span class="jump-btn" role="button" tabindex="0" onclick="jumpToPane('${kind}', '${escapeJsAttr(item.id)}')" title="Jump to this session's terminal pane">↗</span>`;
```

The badge markup, its `onclick="takeOverSession(...)"`, and every other branch of `sessionButtonHtml` stay byte-identical.

### 2. Keep the failure visible

- In `jumpToPane`, replace the stale comment on the `if (!response.ok)` branch. The control stays in place because its availability follows live session state, not pane resolution — not because "the payload said it resolves". Keep the `showToast(await parseErrorResponse(response), true)` call exactly as it is: that toast is the visible error AC5 and AC6 ask for.
- Leave the 2xx path untouched: no body read, no navigate, no reload, no re-render.

### 3. Reconcile the stale comment in `style.css`

Update the comment above the `.jump-btn` rules so it no longer says the control appears when `jump_pane` is set. State the new rule: the control appears beside the Live badge on a live task card, and the pane is resolved when it is clicked. Change no CSS rule.

### 4. Bump the cache-bust tokens in `index.html`

Both `app.js` and `style.css` change, so bump both tokens in `src/vault_ui/static/index.html` to the same new value `2026-10-06-lazy-pane-jump`:

- `style.css?v=2026-10-01-jump-control` → `style.css?v=2026-10-06-lazy-pane-jump`
- `app.js?v=2026-10-01-jump-control` → `app.js?v=2026-10-06-lazy-pane-jump`

Change no other markup in `index.html`. The token is the only mechanism that stops a warm browser serving the pre-change `app.js`, whose live branch still gates on the removed field and would render no jump control at all.

### 5. Update `tests/test_jump_control.py`

- `test_jump_control_rendered_inside_session_button_helper`: keep `assert 'class="jump-btn"' in body`, and replace `assert "item.jump_pane" in body` with an absence assertion — the control is no longer gated on pane data. Assert the whole token is gone, not just the `item.`-prefixed form, so a reintroduced read under another name is caught by the grep in `<verification>` too:

  ```python
  assert "jump_pane" not in body  # the control is gated on live session state, not pane data
  assert "jump_pane" not in APP_JS  # whole file: keeps the go-cutover guard's -S baseline a fixed point
  ```

  Update the function's docstring: it currently says the control is "gated on the payload field". Say instead that it is emitted from `sessionButtonHtml` on live session state for a task card.
- Add one assertion pinning the goal guard, so a future edit cannot start offering the control on a goal card (which would POST to a route that does not exist):

  ```python
  assert "kind !== 'task'" in body  # a goal card has no jump route
  ```

- Update the module docstring, which names `jump_pane` as "the contract this renders against". The contract is now live session state plus a task kind.
- Update the `jump_server` fixture docstring, which names `jump_pane` as the reason the resolver is stubbed. Replace its line `and a stubbed pane resolver so the live task carries ``jump_pane``.` with `and a stubbed pane resolver so the Python list path stays hermetic; the control renders from live session state alone.` Leave the `_resolve_pane_map` monkeypatch in place — the Python reference still calls it.
- Leave `test_jump_control_does_not_replace_the_live_badge_handler`, `test_jump_control_styled`, `test_jump_handler_exists_and_posts_to_the_jump_route`, `test_jump_handler_does_not_read_the_success_body` and the Playwright integration case unchanged.

### 6. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes, then walk AC6 and name the command output that establishes it. Confirm the served `app.js` contains no occurrence of `jump_pane` at all — including comments.

</requirements>

<constraints>
- `src/vault_ui/static/` is embedded into the Go binary at build time AND served by the Python backend from the same files. Change only these four files — `app.js`, `style.css`, `index.html` (cache-bust tokens only) and `tests/test_jump_control.py` — and change no CSS rule, only the stale comment.
- A goal card must never render the jump control: there is no `/api/goals/{id}/jump` route, so a goal card offering it would send the operator to a 404.
- The jump control must stay a sibling of the Live badge, never a second handler on it — the badge's click ends the session.
- The jump handler must not read the success body, navigate, reload or re-render.
- The 60-second fallback poll and the WebSocket frame handling stay as they are.
- No new HTTP route, no new query parameter, no opt-out flag.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- All paths in this prompt and in the code are repo-relative.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0. This runs `go test -race ./...` (the Go binary re-embeds the changed static tree) and `uv run pytest`, which includes `tests/test_jump_control.py`.

```
uv run pytest tests/test_jump_control.py -q
```
Must pass.

```
! grep -q 'jump_pane' src/vault_ui/static/app.js
```
Must exit 0 — AC6's evidence that the served `app.js` contains no read of `jump_pane`. Use `! grep -q`, never `grep -c` (which exits 1 on a zero count).

```
grep -q 'class="jump-btn"' src/vault_ui/static/app.js
```
Must exit 0 — the control still exists.

```
grep -q 'class="jump-btn"' tests/test_jump_control.py
```
Must exit 0 — the updated test still pins the control's markup.

```
! grep -q 'jump_pane' src/vault_ui/static/style.css
```
Must exit 0 — the stale CSS comment no longer names the removed field.

```
grep -q 'app.js?v=2026-10-06-lazy-pane-jump' src/vault_ui/static/index.html && grep -q 'style.css?v=2026-10-06-lazy-pane-jump' src/vault_ui/static/index.html && ! grep -q '2026-10-01-jump-control' src/vault_ui/static/index.html
```
Must exit 0 — both cache-bust tokens bumped, the old one gone.
</verification>
