---
status: approved
spec: [018-closeout-reason-abort-only]
created: "2026-08-25T09:46:00Z"
queued: "2026-08-25T08:09:39Z"
branch: dark-factory/closeout-reason-abort-only
---

# Stop prompting for a reason on Complete (frontend)

<summary>
- The reason modal now opens only for Abort: the Complete menu action (task and goal) and dragging a card into Done / Completed close out directly with no prompt, and their request bodies carry no `reason` / `gate_successor` field.
- Abort is unchanged: the modal still shows the reason input, gate-successor input, risk-prompt sentence, blank-reason disabled-Confirm guard, and cancel support, and a confirmed abort still sends both fields through `patchStatus`.
- The `executeSlashCommand` helper drops its now-unused close-out parameter so the complete-task request body is provably field-free.
- The static-source frontend tests are rewired to the new contract: zero `'complete'`-verb `askCloseOut` calls in `dispatchMenuAction`/`handleDrop`, the abort prompt assertions retained, and the cache-buster guard updated.
- The `app.js` cache-buster token is bumped so already-open boards fetch the new script; the CHANGELOG gains a `## Unreleased` entry describing the complete feature (backend + frontend).
- This prompt depends on the backend prompt in the same spec having shipped (the API now accepts a reason-free completion and passes no close-out flags).
</summary>

<objective>
Stop the vault-ui frontend from prompting for, and sending, close-out reason fields on any `completed` close-out (Complete menu actions and drag-into-Done/Completed), while keeping the reason modal and its fields for `aborted` — aligning the UI with the abort-only backend contract from the sibling prompt.
</objective>

<context>
Read `README.md` for project conventions (vanilla-JS frontend in `src/vault_ui/static/`, no JS runtime — frontend behavior is pinned by pure-Python static-source tests that read `app.js` / `index.html` via `pathlib` and string-assert / brace-walk named function bodies).

Read these files in full before editing:

- `src/vault_ui/static/app.js` — the frontend. Key anchors (all still present from spec 077):
  - `dispatchMenuAction` (~line 1855) — the card-menu dispatcher. The `complete_goal`/`defer_goal` branch builds `const body = { command };` then conditionally adds `body.reason`/`body.gate_successor` when a `closeOut` came from `askCloseOut('goal', 'complete')`; the `complete_task`/`defer_task` branch calls `executeSlashCommand(id, action, closeOut)` after `askCloseOut('task', 'complete')`; the `abort_task` / `abort_goal` branches call `askCloseOut('task'|'goal', 'abort')` then `patchStatus(..., closeOut)`.
  - `handleDrop` (~line 831) — drag-and-drop. Task branch prompts with `askCloseOut('task', 'complete')` when `targetKey === 'done'` and extends `const body = { phase: targetKey };` with `body.reason`/`body.gate_successor`; goal branch prompts with `askCloseOut('goal', 'complete')` when `targetKey === 'completed'` and extends `const body = { status: targetKey };` the same way.
  - `executeSlashCommand` (~line 2029) — `async function executeSlashCommand(taskId, commandType, closeOut = null)`; its POST body is `const body = { command: slashCommand };` followed by `if (closeOut) { body.reason = ...; body.gate_successor = ...; }`. The only caller is the `complete_task`/`defer_task` branch of `dispatchMenuAction`.
  - `patchStatus` (~line 1932) — `async function patchStatus(kind, id, vault, status, successMsg, closeOut = null)`. The `if (closeOut) { body.reason = ...; body.gate_successor = ...; }` extension STAYS — abort still passes `closeOut`. Only its header comment ("set for close-out statuses (aborted/completed)") drifts.
  - `askCloseOut` (~line 1491) — the modal helper. UNCHANGED — abort still uses it.
- `src/vault_ui/static/index.html` — the `<script src="app.js?v=2026-08-24-closeout-reason">` include at line 132 is the cache-buster token to bump. The `#reason-modal` markup (lines 115-130) is UNCHANGED. `style.css` is NOT changed by this spec — its token stays.
- `tests/test_closeout_reason_modal.py` — the static-source frontend suite for the modal wiring; `NEW_TOKEN = "2026-08-24-closeout-reason"` at the top and the `_function_body` / `_if_block` brace-walk helpers. Requirements 5 rewires it.
- `tests/test_card_unify_behavior.py` — line 62 asserts `"executeSlashCommand(id, action, closeOut)" in body`; this call-string changes in requirement 1, so the assertion must change too.
- `CHANGELOG.md` — has NO `## Unreleased` section yet (last release `v0.53.0`); requirement 8 creates one directly above `## v0.53.0`.
- `docs/dod.md` — CHANGELOG entry under `## Unreleased` required; coverage on changed behavior.

Relevant coding-plugin guides (in-container paths — the agent runs in the YOLO container, not the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement (immediately after the frozen preamble, directly above the newest `## vX.Y.Z`), conventional `feat:` prefix, preamble-frozen rule.
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-makefile-commands.md` — `make precommit` is the full gate.

**Static-test shape note:** these tests assert exact substrings and brace-walked bodies. Where a requirement says a body "carries no close-out fields", the assertion is on absence (`"body.reason" not in <body>`); keep the assertions aligned with the literal code you write (e.g. `const body = { phase: targetKey };`).
</context>

<requirements>

### 1. Remove the Complete prompts from `dispatchMenuAction` (keep Abort)

In `src/vault_ui/static/app.js`:

1a. Goal branch — the `complete_goal`/`defer_goal` path. Remove the prompt lines and the conditional body extension. Old → new:

```javascript
            const command = action === 'complete_goal' ? 'complete-goal' : 'defer-goal';
            // Close-out (complete_goal) prompts for a reason + gate successor first.
            const closeOut = action === 'complete_goal' ? await askCloseOut('goal', 'complete') : null;
            if (action === 'complete_goal' && closeOut === null) return;
            try {
                const body = { command };
                if (closeOut) {
                    body.reason = closeOut.reason;
                    body.gate_successor = closeOut.gate_successor;
                }
```
becomes:
```javascript
            const command = action === 'complete_goal' ? 'complete-goal' : 'defer-goal';
            // complete_goal is a completed-targeting close-out — reason-free
            // (abort-only contract); the POST body carries no close-out fields.
            try {
                const body = { command };
```

1b. Task branch — the `complete_task`/`defer_task` path. Old → new:

```javascript
        // Close-out (complete_task) prompts for a reason + gate successor first.
        const closeOut = action === 'complete_task' ? await askCloseOut('task', 'complete') : null;
        if (action === 'complete_task' && closeOut === null) return;
        await executeSlashCommand(id, action, closeOut);
```
becomes:
```javascript
        // complete_task is a completed-targeting close-out — reason-free
        // (abort-only contract); executeSlashCommand sends no close-out fields.
        await executeSlashCommand(id, action);
```

1c. UNCHANGED: the `abort_task` / `abort_goal` branches still do `const closeOut = await askCloseOut('task'|'goal', 'abort'); if (closeOut === null) return; await patchStatus(..., 'aborted', ..., closeOut);`. Hold/Resume branches unchanged (no prompt, no closeOut).

### 2. Remove the Complete prompts from `handleDrop`

In `src/vault_ui/static/app.js`:

2a. Task branch (Done column drop). Old → new:

```javascript
        // Dropping into the Done column is a close-out (status auto-writes
        // completed) — require a reason + gate successor first; cancel aborts.
        let closeOut = null;
        if (targetKey === 'done') {
            closeOut = await askCloseOut('task', 'complete');
            if (closeOut === null) return;
        }
        try {
            const body = { phase: targetKey };
            if (closeOut) {
                body.reason = closeOut.reason;
                body.gate_successor = closeOut.gate_successor;
            }
```
becomes:
```javascript
        // Dropping into the Done column is a completed-targeting close-out —
        // reason-free (abort-only contract); the PATCH body carries no
        // close-out fields.
        try {
            const body = { phase: targetKey };
```

2b. Goal branch (Completed column drop). Old → new:

```javascript
        // Dropping into the Completed column is a close-out — same prompt/cancel.
        let closeOut = null;
        if (targetKey === 'completed') {
            closeOut = await askCloseOut('goal', 'complete');
            if (closeOut === null) return;
        }
        try {
            const body = { status: targetKey };
            if (closeOut) {
                body.reason = closeOut.reason;
                body.gate_successor = closeOut.gate_successor;
            }
```
becomes:
```javascript
        // Dropping into the Completed column is a completed-targeting close-out —
        // reason-free (abort-only contract); the PATCH body carries no
        // close-out fields.
        try {
            const body = { status: targetKey };
```

### 3. Drop the close-out parameter from `executeSlashCommand`

The only caller (requirement 1b) no longer passes a `closeOut`. Remove the dead path so the complete-task body is provably field-free:

```javascript
async function executeSlashCommand(taskId, commandType, closeOut = null) {
```
becomes
```javascript
async function executeSlashCommand(taskId, commandType) {
```
and inside the `try`, old → new:

```javascript
        // Call backend endpoint. Close-out commands (complete-task) carry the
        // reason + gate successor; defer passes no extra body fields.
        const body = { command: slashCommand };
        if (closeOut) {
            body.reason = closeOut.reason;
            body.gate_successor = closeOut.gate_successor;
        }
```
becomes
```javascript
        // Call backend endpoint. Neither complete-task nor defer-task carries
        // close-out fields (completion is reason-free; defer never had any).
        const body = { command: slashCommand };
```

### 4. Keep `patchStatus` close-out handling (abort), fix its stale comment

`patchStatus(kind, id, vault, status, successMsg, closeOut = null)` and its `if (closeOut) { body.reason = ...; body.gate_successor = ...; }` extension are UNCHANGED — abort still passes `closeOut`. Only the comment directly above the function that reads `// closeOut ({ reason, gate_successor } | null) is set for close-out statuses (aborted/completed) and added to the request body when present.` must be corrected to abort-only framing, e.g. `// closeOut ({ reason, gate_successor } | null) is set for the abort close-out status and added to the request body when present (completed is reason-free).`

### 5. Rewire `tests/test_closeout_reason_modal.py` to the abort-only contract

5a. Update the module docstring: it currently says every close-out write (Abort/Complete, drag-to-Done) prompts; restate that ONLY Abort prompts and Complete is reason-free.

5b. Change `NEW_TOKEN = "2026-08-24-closeout-reason"` to `NEW_TOKEN = "2026-08-25-closeout-abort-only"`.

5c. Update `test_cache_busters_bumped` — `style.css` is NOT changed by this spec, so its token stays `2026-08-24-closeout-reason`:
```python
def test_cache_busters_bumped() -> None:
    """index.html loads app.js with the NEW abort-only token; style.css is unchanged."""
    assert f"app.js?v={NEW_TOKEN}" in INDEX_HTML
    assert "style.css?v=2026-08-24-closeout-reason" in INDEX_HTML
    assert "2026-08-19-board-sort" not in INDEX_HTML
```

5d. REPLACE `test_dispatch_menu_action_calls_ask_close_out` with an abort-only guard:
```python
def test_dispatch_menu_action_asks_close_out_only_for_abort() -> None:
    """dispatchMenuAction prompts via askCloseOut for abort only — zero 'complete' verbs."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('task', 'abort')" in body
    assert "askCloseOut('goal', 'abort')" in body
    assert "askCloseOut('task', 'complete')" not in body
    assert "askCloseOut('goal', 'complete')" not in body
```

5e. REPLACE `test_dispatch_menu_action_complete_task_gated` with a reason-free guard:
```python
def test_dispatch_menu_action_complete_task_reason_free() -> None:
    """complete_task dispatches directly with no close-out prompt or fields."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('task', 'complete')" not in body
    assert "executeSlashCommand(id, action)" in body
```

5f. REPLACE `test_dispatch_menu_action_complete_goal_gated` with a reason-free guard:
```python
def test_dispatch_menu_action_complete_goal_reason_free() -> None:
    """complete_goal posts { command } with no close-out fields on the body."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('goal', 'complete')" not in body
    assert "body.reason" not in body
    assert "body.gate_successor" not in body
```

5g. KEEP `test_dispatch_menu_action_abort_task_gated` and `test_dispatch_menu_action_abort_goal_gated` unchanged (abort contract: `askCloseOut('task'|'goal', 'abort')` + `patchStatus(..., 'aborted', ..., closeOut)`).

5h. REPLACE `test_dispatch_menu_action_defer_not_gated` — its two ternary-string assertions no longer exist. Replace with a guard that defer dispatches without any askCloseOut:
```python
def test_dispatch_menu_action_defer_not_gated() -> None:
    """defer_goal / defer_task dispatch without any askCloseOut prompt."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('task', 'complete')" not in body
    assert "askCloseOut('goal', 'complete')" not in body
```

5i. REPLACE `test_handle_drop_task_done_guards_ask_close_out` with a reason-free guard:
```python
def test_handle_drop_task_done_reason_free() -> None:
    """Task drops into the 'done' column are reason-free — no prompt, no close-out fields."""
    body = _function_body(APP_JS, "handleDrop")
    assert "askCloseOut('task', 'complete')" not in body
    assert "const body = { phase: targetKey };" in body
    assert "body.reason" not in body
```

5j. REPLACE `test_handle_drop_goal_completed_guards_ask_close_out` with a reason-free guard:
```python
def test_handle_drop_goal_completed_reason_free() -> None:
    """Goal drops into the 'completed' column are reason-free — no prompt, no close-out fields."""
    body = _function_body(APP_JS, "handleDrop")
    assert "askCloseOut('goal', 'complete')" not in body
    assert "const body = { status: targetKey };" in body
    assert "body.reason" not in body
```

5k. KEEP unchanged (modal contract retained for abort): `test_reason_modal_markup_present`, `test_modal_reuses_existing_modal_classes`, `test_ask_close_out_defined`, `test_ask_close_out_contract`, `test_risk_prompt_sentence_present`, `test_patch_status_builds_close_out_body`.

5l. The `_if_block` helper may become unused after 5i/5j. That is fine (ruff does not flag unused module-level functions) — leave it, or remove it if you prefer.

### 6. Update `tests/test_card_unify_behavior.py`

Line 62 (`test_dispatch_routes_lifecycle_and_clear`) asserts `"executeSlashCommand(id, action, closeOut)" in body` — the call-site string changed in requirement 1b. Change the assertion to `assert "executeSlashCommand(id, action)" in body` and update the trailing comment to `# task complete/defer preserved (reason-free)`. No other change to this file.

### 7. Bump the `app.js` cache-buster token

In `src/vault_ui/static/index.html`, change the script include at line 132 from `app.js?v=2026-08-24-closeout-reason` to `app.js?v=2026-08-25-closeout-abort-only`. Do NOT change the `style.css` token (style.css is untouched by this spec) and do NOT modify the `#reason-modal` markup.

### 8. CHANGELOG entry

`CHANGELOG.md` currently has NO `## Unreleased` section. Create one directly above `## v0.53.0` (immediately after the frozen SemVer preamble — never above or inside the `# Changelog` / preamble header), with a single `feat(ui):` bullet per `changelog-guide.md` conventions. The bullet must state, in plain terms: completing a task or goal (Complete menu action and drag into Done/Completed) no longer requires a reason — the UI never prompts on Complete, the API accepts a reason-free completion and drops any supplied `reason`/`gate_successor`, and no `--reason`/`--gate-successor` flag reaches vault-cli, so completed task/goal files stay free of `aborted_reason`/`gate_successor`; Abort is unchanged (reason + gate-successor mandatory, HTTP 400 on blank); matches the sibling vault-cli fix; bumps the `app.js` cache-buster token.

### 9. Self-check before finishing

Re-run the `<verification>` commands and confirm they pass. Then walk the spec's acceptance criteria AC4, AC5, AC6 against the change: `dispatchMenuAction` and `handleDrop` contain zero `'complete'`-verb `askCloseOut` calls and their complete branches build bodies without `reason`/`gate_successor`; the abort branches still call `askCloseOut(..., 'abort')` and pass `closeOut.reason`/`closeOut.gate_successor` through `patchStatus`; the modal keeps its inputs, guard, and risk-prompt sentence; `index.html` serves `app.js` with the new token.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- This repo's `.dark-factory.yaml` sets `hideGit: true` — never run a bare `git` command in this prompt's verification (it dies with `fatal: not a git repository` in the container and would falsely pass). Use `make precommit`, `uv run pytest`, `grep`, and `find` instead.
- Frozen field names (spec 037): `aborted_reason` and `gate_successor`. No new field, no rename, no backfill of existing completed files.
- Frozen `aborted` contract: abort still opens the reason modal with the reason input, the gate-successor input, the risk-prompt sentence, the blank-reason disabled-Confirm guard, and cancel; a confirmed abort still sends `reason` + `gate_successor` (defaulting to `none`) to the backend via `patchStatus`.
- `completed` contract: Complete (menu task and goal) and drag into Done/Completed close out directly with no prompt, and their request bodies carry no `reason` / `gate_successor` fields.
- Do NOT add an optional completion-reason or gate-successor capture on Complete; do NOT change the `#reason-modal` markup or `style.css`.
- Do NOT add Playwright / jsdom / node coverage or any new dependency; do NOT extend `tests/test_board_sort.py`; do NOT run `make test-integration` (needs a host browser + `uv run playwright install chromium`; the container has neither). The static-source tests in requirements 5 and 6 are the automated guard; the browser click-through is the operator rung on the spec, not this prompt.
- Existing tests keep passing EXCEPT the ones this prompt intentionally rewires (`tests/test_closeout_reason_modal.py` per requirement 5 and `tests/test_card_unify_behavior.py` line 62).
- Per `docs/dod.md`: CHANGELOG entry under `## Unreleased`; no debug output; coverage on changed behavior ≥ 80%.
- This prompt is frontend-only. Do NOT touch `src/vault_ui/api/tasks.py` or `tests/test_api.py` — the backend prompt in the same spec handled those.
- `make precommit` MUST stay green.
</constraints>

<verification>
```bash
# Full pre-commit gate (format + test + lint + typecheck). `make test` deselects
# the integration suite via pytest addopts -m 'not integration', so this runs
# every test this prompt changes. Must exit 0.
make precommit

# Frontend static-source guards explicitly
uv run pytest tests/test_closeout_reason_modal.py tests/test_card_unify_behavior.py -v
# Expected: all pass — zero 'complete'-verb askCloseOut calls in dispatchMenuAction
# and handleDrop, the abort prompt assertions green, the new cache-buster token
# asserted, and the executeSlashCommand call shape updated.

# Confirm the cache-buster token is bumped and the modal markup is retained
grep -n 'app.js?v=\|style.css?v=\|id="reason-modal"' src/vault_ui/static/index.html
# Expected: app.js?v=2026-08-25-closeout-abort-only, style.css unchanged, the
# reason-modal element still present.

# Confirm no 'complete'-verb askCloseOut remains in app.js
grep -n "askCloseOut('task', 'complete')\|askCloseOut('goal', 'complete')" src/vault_ui/static/app.js || echo "no complete-verb askCloseOut calls remain"

# Confirm the CHANGELOG Unreleased entry exists
grep -n 'Unreleased' CHANGELOG.md
```

Do NOT run `make test-integration`, `git ...`, or any `docker`/`kubectl`/`gh` command — none are executable in the container and all would produce a false-positive verification pass.
</verification>
