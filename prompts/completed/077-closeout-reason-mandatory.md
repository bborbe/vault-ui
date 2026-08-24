---
status: completed
summary: 'Made close-out reason mandatory in vault-ui: backend request models + _closeout_extra_args gate wired into all five write endpoints (passing --reason/--gate-successor to vault-cli), frontend reason modal wired into every close-out entry point (Abort/Complete menu actions and drag-to-Done), cache-buster tokens bumped, 26 backend + 15 static-source frontend tests added/updated, CHANGELOG updated under Unreleased'
execution_id: vault-ui-exec-077-closeout-reason-mandatory
dark-factory-version: dev
created: "2026-08-24T12:00:00Z"
queued: "2026-08-24T20:50:57Z"
started: "2026-08-24T20:51:11Z"
completed: "2026-08-24T21:02:34Z"
---

# Make close-out reason mandatory in vault-ui

<summary>
- Aborting or completing a task or goal now requires the operator to type a reason before the UI sends the change; the API independently rejects close-out requests that carry no reason.
- A gate-successor prompt asks where any trigger / gate / threshold / recurring check the item owns moves; a blank answer is recorded as the literal value `none`.
- The reason requirement applies to every close-out entry point: the card-menu Abort and Complete actions (tasks and goals) and dragging a card into the Done column — all send `reason` + `gate_successor` in the request body.
- The API passes both values to vault-cli as `--reason` and `--gate-successor`, so close-out writes succeed against vault-cli v0.116.0+, which rejects aborted/completed transitions without those frontmatter fields.
- Non-close-out actions (Hold, Resume, defer, phase moves other than `done`) are unchanged: no modal, no reason, no extra vault-cli flags.
- A blank or whitespace-only reason is blocked in the browser (Confirm disabled while empty) and by the API (HTTP 400 naming the missing field) — a close-out can never be sent without a reason.
- Missing gate-successor defaults to `none` on both the frontend (blank input) and the backend (absent field), so the API stays usable when the operator chooses not to name a successor.
- Existing close-out actions that are currently broken against vault-cli v0.116.0 (they would be rejected with a "missing close-out field(s)" error) work again.
- Backend tests pin the reason requirement and the exact vault-cli argv for every close-out path; a static-source frontend test guards the modal wiring so the reason prompt cannot silently disappear in a future edit.
</summary>

<objective>
Make every close-out write in vault-ui (Abort/Complete via the card menu, and drag-to-Done) require a free-text reason and pass both that reason and a gate-successor to vault-cli, so the UI keeps working against vault-cli v0.116.0+, which rejects aborted/completed transitions without `aborted_reason` and `gate_successor` frontmatter. The operator sees a modal before any close-out; non-close-out actions are untouched.
</objective>

<context>
Read `CLAUDE.md` at the repo root for project conventions (vanilla-JS frontend, FastAPI backend, `uv`-managed Python, pytest; code changes ship only via dark-factory prompts).

Read these files in full before editing:

- `src/vault_ui/api/tasks.py` — all backend changes live here. Key anchors:
  - Request models `UpdatePhaseRequest`, `UpdateStatusRequest`, `ExecuteCommandRequest` (the model block just above `list_vaults`, ~lines 342-369). `UpdateStatusRequest.status` is `Literal["next", "in_progress", "backlog", "completed", "hold", "aborted"]`; `ExecuteCommandRequest.command` is a plain `str` allowlisted per-endpoint. `UpdateStatusRequest` is shared by BOTH `update_task_status` and `update_goal_status`; `ExecuteCommandRequest` is shared by `execute_slash_command` AND `execute_goal_command`.
  - The five write endpoints and their subprocess pattern: `update_task_phase` (~1195-1291, phase `done` → second `task set status completed` subprocess at ~1256-1267), `update_goal_status` (~1294-1366, `goal set status`), `execute_goal_command` (~1369-1458, `goal complete` fast path at ~1415-1422), `update_task_status` (~1461-1531, `task set status`), `execute_slash_command` (~1026-1136, `task complete` fast path at ~1072-1079; note this endpoint pre-reads the task via `client.show_task(task_id)` at ~1051 before the fast-path branch).
  - The arg-injection guard pattern to mirror: `if task_id.startswith("-"): raise HTTPException(status_code=400, detail="task_id must not start with '-'")` — placed BEFORE the subprocess, fail-fast, HTTP 400. The command-allowlist pattern in `execute_goal_command`: `if request.command not in ("complete-goal", "defer-goal"): raise HTTPException(status_code=400, ...)`.
  - The 10s-timeout pattern: `await asyncio.wait_for(proc.communicate(), timeout=10.0)` → `except TimeoutError` → kill → HTTP 504. Every endpoint ends its `try` with `except HTTPException: raise` first.
  - vault-cli is treated as immutable from the UI's side (the UI shells out to it and never patches it): the close-out gate must be enforced in vault-ui's own request models + subprocess args, NOT by patching vault-cli.
- `src/vault_ui/static/app.js` — the frontend. Key anchors:
  - `patchStatus(kind, id, vault, status, successMsg)` (~1832-1852) — the shared PATCH /status helper used by Abort/Hold/Resume for both kinds. Its body builds `body: JSON.stringify({ status })`.
  - `dispatchMenuAction(kind, id, action)` (~1772-1828) — the card-menu dispatcher. `abort_goal` calls `patchStatus('goal', id, item.vault, 'aborted', 'Goal aborted')` (~1808-1809); the goal `complete_goal` branch has its own inline fetch to `/goals/{id}/execute-command` with `body: JSON.stringify({ command })` (~1791-1798); `complete_task`/`defer_task` call `executeSlashCommand(id, action)` (~1819-1820); `abort_task` calls `patchStatus('task', id, item.vault, 'aborted', 'Task aborted')` (~1821-1822).
  - `executeSlashCommand(taskId, commandType)` (~1924-2002) — task complete/defer fast path; POST body `JSON.stringify({ command: slashCommand })` at ~1961.
  - `handleDrop(e)` (~831-877) — drag-and-drop. Task drop → `PATCH /tasks/{id}/phase` with `body: JSON.stringify({ phase: targetKey })` (~846-850); goal drop → `PATCH /goals/{id}/status` with `body: JSON.stringify({ status: targetKey })` (~862-866). `targetKey` is the column id with `cards-` stripped (task phase `done` for the Done column; goal status `completed`).
  - `showModal(...)` / `closeModal()` (~1424-1463) — the existing imperative modal helpers, plus the loading/session modal element accessors (`document.getElementById('loading-modal')`, `.classList.remove('hidden')` / `.add('hidden')`) used throughout — the modal-show/hide idiom this prompt's modal must follow.
- `src/vault_ui/static/index.html` — the modal exemplars to copy structurally: `#loading-modal` (~86-95) and `#session-modal` (~97-112), both `class="modal hidden"` containing `.modal-content` with a `.modal-buttons` row. The script include at line 114 (`app.js?v=2026-08-19-board-sort`) and the stylesheet include at line 7 (`style.css?v=2026-08-19-board-sort`) are the cache-buster tokens to bump.
- `src/vault_ui/static/style.css` — the `.modal`, `.modal.hidden`, `.modal-content`, `.modal-content h2/p/code`, `.modal-buttons`, `.modal-buttons button` rules (~731-806). Reuse these classes for the new modal; add only the minimal new rules the new form controls need, in the same dark theme (`#2a2a2a` content background, `#3a3a3a` borders, `#9ca3af` muted text).
- `tests/test_api.py` — the backend test exemplars: mocked-subprocess style (`patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec` then `assert mock_exec.call_args.args == (...)`), the `test_client` / `mock_vault_client` fixtures, and the specific tests that MUST be updated because they currently perform close-outs without a reason (see requirement 5 for the list).
- `tests/test_task_menu.py` and `tests/test_cross_view_leak.py` — the sanctioned static-source frontend test style: pure Python, read `app.js` / `index.html` via `pathlib`, string assertions and/or brace-walking a named function body. No JS runtime. This is the ONLY automated frontend test shape available in this repo.
- `docs/dod.md` — the repo's Definition of Done (new code needs tests, CHANGELOG entry under `## Unreleased`, no debug output).

Relevant coding-plugin guides (in-container paths — the agent runs in the YOLO container, not the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `feat:` bullet format under `## Unreleased`, preamble-frozen rule (never insert above the `# Changelog` title or inside the SemVer preamble).
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-pydantic-guide.md` — Pydantic v2 optional-field conventions. This prompt deliberately uses plain `str | None = None` fields rather than a `model_validator` (see requirement 1).

**Cross-repo contract (vault-cli v0.116.0+ — verified in the vault-cli source; the vault-cli repo is NOT mounted in the container, so this contract is inlined here and MUST NOT be re-investigated):**
- Close-out transitions — `task set status aborted|completed`, `task complete`, `goal set status aborted|completed`, `goal complete` — are REJECTED unless the frontmatter already holds a non-empty `aborted_reason` AND a non-empty `gate_successor`. The CLI writes both fields from flags in the same write: `--reason "<text>"` and `--gate-successor "<successor|none>"`. The literal string `none` is the documented "nothing is inherited" value.
- The rejection names the missing fields and suggests the flag form, e.g. `cannot set status "aborted": missing close-out field(s) aborted_reason, gate_successor; ... Try: vault-cli task set "<id>" status aborted --reason "<text>" --gate-successor "<successor|none>"`.
- Non-close-out targets (statuses `next`/`in_progress`/`backlog`/`hold`, `defer-task`, `defer-goal`, and phase-field writes) do NOT require the flags. Setting the `phase` field to `done` does NOT trigger the close-out guard — only the separate `status` write does. Therefore in `update_task_phase` the `--reason`/`--gate-successor` flags belong on the STATUS subprocess only, never on the phase subprocess.

**Flow note:** this change is full-stack (backend validation + subprocess args + frontend modal), so it ships as one prompt. Per `vault-ui/CLAUDE.md` § Choosing a Flow, frontend-only changes whose real verification is browser E2E normally go direct with a host-side Playwright test — the container cannot run a browser. The existing Playwright suite `tests/test_board_sort.py` (integration-marked, deselected by `make test`) is EXACTLY that host-side harness. This prompt deliberately does NOT add Playwright coverage for the modal: the modal's interactive behavior is verified by the operator on the host (`make run` + manual check), and `make test-integration` is not container-executable. Do not extend `test_board_sort.py` here.
</context>

<requirements>

### 1. Add `reason` and `gate_successor` optional fields to the three shared request models

In `src/vault_ui/api/tasks.py`, add two optional fields to each of `UpdateStatusRequest`, `UpdatePhaseRequest`, and `ExecuteCommandRequest`:

```python
class UpdateStatusRequest(BaseModel):
    status: Literal["next", "in_progress", "backlog", "completed", "hold", "aborted"]
    reason: str | None = None
    gate_successor: str | None = None
```

Same two fields (`reason: str | None = None`, `gate_successor: str | None = None`) on `UpdatePhaseRequest` (which keeps `phase: str`) and `ExecuteCommandRequest` (which keeps `command: str`). Field name in the API payload is exactly `gate_successor` (underscore), matching the frontend payload and the vault-cli flag. No new imports needed (Pydantic `BaseModel` with `str | None = None`).

These are plain optional fields — do NOT add a Pydantic `model_validator`. The conditional requirement ("reason needed only for close-out targets") is endpoint logic, implemented in requirement 3 with a shared helper, so the 400 response carries a clean string `detail` that the frontend `parseErrorResponse` surfaces directly in the toast (a Pydantic 422 would return `detail` as a list, which the frontend does not render cleanly).

### 2. Add the shared close-out helper

Add this module-level function to `src/vault_ui/api/tasks.py` (near the other request models, after `ExecuteCommandRequest`):

```python
def _closeout_extra_args(reason: str | None, gate_successor: str | None) -> list[str]:
    """Return vault-cli --reason/--gate-successor flags for a close-out write.

    Raises HTTPException(400) naming `reason` when it is empty or whitespace-only
    (a close-out must record why the work is being closed out). gate_successor
    defaults to the literal string "none" when not supplied — the documented
    no-inheritance value vault-cli accepts.
    """
    if not (reason or "").strip():
        raise HTTPException(
            status_code=400,
            detail="reason is required to close out a task or goal (aborted/completed)",
        )
    return [
        "--reason",
        reason.strip(),
        "--gate-successor",
        (gate_successor or "").strip() or "none",
    ]
```

Contract: returns a 4-element list `["--reason", <trimmed reason>, "--gate-successor", <successor or "none">]`; raises `HTTPException(400)` with `detail` naming `reason` when the reason is missing/blank/whitespace-only. This helper is the single source of the 400 contract and the flag spelling.

### 3. Wire the helper into the five write endpoints (fail-fast before the close-out write)

For each endpoint below, when the write is a close-out: (a) call `_closeout_extra_args(request.reason, request.gate_successor)` BEFORE the close-out write subprocess starts and splice its 4 items into the vault-cli args immediately BEFORE the `"--vault", vault_config.name.lower()` tail; (b) when the write is NOT a close-out, do not call the helper, do not require reason, and do not add the flags — the existing argv is unchanged. A missing reason must mean the close-out write subprocess is NEVER started (fail-fast). For `execute_slash_command`, which pre-reads the task via `show_task` before the fast-path branch, the check may sit before or after that read, but MUST precede the `task complete` write. The 400 raised by the helper must propagate (every endpoint already has `except HTTPException: raise` first).

- `update_task_status` (PATCH /tasks/{id}/status): close-out when `request.status in ("aborted", "completed")`. Target argv for close-out:
  `(vault_cli_path, "task", "set", task_id, "status", <status>, "--reason", <reason>, "--gate-successor", <successor>, "--vault", vault_config.name.lower())`
- `update_goal_status` (PATCH /goals/{id}/status): close-out when `request.status in ("aborted", "completed")`. Same splice on the `goal set` args.
- `update_task_phase` (PATCH /tasks/{id}/phase): close-out when `request.phase == "done"` (this branch sets `new_status = "completed"`). Splice the flags onto the STATUS subprocess args ONLY (the `task set ... status completed` subprocess in the `if new_status is not None:` block). The PHASE subprocess (`task set ... phase done`) stays exactly as-is — vault-cli's phase-field write does not enforce close-out, and the flags are not defined there.
- `execute_slash_command` (POST /tasks/{id}/execute-command): close-out when `request.command == "complete-task"` (the `else` branch at ~1072-1079). Splice onto the `task complete` args. `defer-task`, `work-on-task`, `create-task` unchanged.
- `execute_goal_command` (POST /goals/{id}/execute-command): close-out when `request.command == "complete-goal"` (the `else` branch at ~1415-1422). Splice onto the `goal complete` args. `defer-goal` unchanged. Keep the existing `goal_id.startswith("-")` guard and the command allowlist exactly as they are.

### 4. Frontend: close-out reason modal

The modal is the one novel structure in this change — shape its internals from the existing modal exemplars (`#loading-modal` / `#session-modal` in `index.html` and the `.modal*` rules in `style.css`); do not introduce a modal framework.

4a. In `src/vault_ui/static/index.html`, add a new modal element next to the existing `#session-modal` (before the `<script>` include at line 114), reusing `class="modal hidden"` + `.modal-content` + `.modal-buttons`:

- container `id="reason-modal"` with class `modal hidden`
- an `h2` title (id it, e.g. `id="reason-title"`)
- a reason text input/textarea with `id="reason-input"` (free-text; a multi-line `textarea` is fine)
- a gate-successor text input with `id="gate-successor-input"`
- a risk-prompt paragraph whose text is (parameterized by kind): `Does this task own a trigger, gate, threshold or recurring check? If so, name where it moves (gate successor), or 'none'.` (for goals use `Does this goal own ...`). This exact string must appear verbatim so the static-source test can assert it.
- a `.modal-buttons` row with a Cancel button `id="reason-cancel-btn"` and a Confirm button `id="reason-confirm-btn"`

4b. In `src/vault_ui/static/style.css`, add only the minimal rules needed for the new form controls (label, textarea/input, risk-prompt styling) in the existing dark theme. Reuse `.modal-content`, `.modal-buttons`, `.modal-buttons button` unchanged.

4c. In `src/vault_ui/static/app.js`, add a promise-based helper (follow the existing modal show/hide idiom — `document.getElementById('reason-modal').classList.remove('hidden')` / `.add('hidden')`):

```javascript
// Open the close-out reason modal; resolve with { reason, gate_successor } on
// Confirm, or null when the operator cancels. Confirm is disabled while the
// reason is empty/whitespace-only, so a blank reason can never be submitted.
async function askCloseOut(kind, verb) { ... }
```

Contract:
- Sets the modal title from kind+verb (e.g. `Abort Task`, `Complete Goal`; the drop path may use e.g. `Move to Done`).
- Returns `{ reason: <trimmed>, gate_successor: <trimmed or 'none'> }` on Confirm, `null` on Cancel.
- Blocked empty-submit: the Confirm button is disabled whenever the reason is empty/whitespace-only (listen to the reason input to toggle it), and the confirm handler also refuses a blank reason as a guard. Cancel/Confirm both hide the modal and detach any listeners.
- `gate_successor` is the trimmed input or the literal string `'none'` when blank.

4d. Wire the four card-menu close-out actions in `dispatchMenuAction` (kind `task` branch ~1818-1827, kind `goal` branch ~1787-1815). For each close-out action, first `const closeOut = await askCloseOut(kind, <verb>); if (closeOut === null) return;` then proceed with the existing dispatch carrying `closeOut`. Concretely:
- `abort_task` → `askCloseOut('task', 'abort')` → `patchStatus('task', id, item.vault, 'aborted', 'Task aborted', closeOut)`
- `abort_goal` → `askCloseOut('goal', 'abort')` → `patchStatus('goal', id, item.vault, 'aborted', 'Goal aborted', closeOut)`
- `complete_task` → `askCloseOut('task', 'complete')` → `executeSlashCommand(id, action, closeOut)`
- `complete_goal` → `askCloseOut('goal', 'complete')` → include `closeOut` in the inline `/goals/{id}/execute-command` POST body
- Hold/Resume/defer actions must NOT open the modal and must pass no close-out.

4e. Extend `patchStatus(kind, id, vault, status, successMsg, closeOut = null)` to include the close-out fields in the request body when present:

```javascript
const body = { status };
if (closeOut) {
    body.reason = closeOut.reason;
    body.gate_successor = closeOut.gate_successor;
}
```

4f. Extend `executeSlashCommand(taskId, commandType, closeOut = null)` to include `reason`/`gate_successor` in the POST body when `closeOut` is present. Non-close-out calls (defer) pass no body fields.

4g. Extend `handleDrop` so dragging a card into a close-out column prompts first and aborts the drop on cancel:
- Task branch (phase `done`): when `targetKey === 'done'`, `const closeOut = await askCloseOut('task', 'complete'); if (closeOut === null) return;` then include `reason`/`gate_successor` in the PATCH `/tasks/{id}/phase` body. Other phase drops unchanged.
- Goal branch (status `completed`): when `targetKey === 'completed'`, same prompt, then include the fields in the PATCH `/goals/{id}/status` body. Other status drops unchanged.

### 5. Backend tests (`tests/test_api.py`)

5a. UPDATE these existing tests, which currently perform close-outs without a reason and would now hit the 400 guard instead of their intended subprocess path. Add `"reason": "closing out"` (and, where the test asserts full argv, `"gate_successor": "none"` or an explicit successor) to the request JSON, and extend the `assert mock_exec.call_args.args == (...)` expectations to include `"--reason"`, the reason, `"--gate-successor"`, the successor, before `"--vault"`:

- `test_execute_complete_task_uses_vault_cli` (~784) — also assert the new argv shape exactly: `("vault-cli", "task", "complete", "Test Task", "--reason", ..., "--gate-successor", ..., "--vault", "testvault")`
- `test_execute_vault_cli_failure_returns_500` (~817)
- `test_execute_vault_cli_uses_configured_path` (~836)
- `test_update_task_status_uses_vault_cli` (~1083) — status `"aborted"`; extend the exact argv assertion with the two flag pairs
- `test_update_task_status_vault_cli_failure_returns_500` (~1138)
- `test_update_task_status_timeout_returns_504` (~1154)
- `test_execute_goal_command_complete_uses_vault_cli` (~1206) — extend the exact argv assertion with the two flag pairs
- `test_update_phase_done_writes_completed_status` (~2941) — add `reason`/`gate_successor` to the phase request; keep asserting the phase subprocess is flag-free and extend the status-argv assertion with the flag pairs

Do NOT touch the leading-dash tests (`test_update_task_status_leading_dash_rejected`, `test_execute_goal_command_leading_dash_rejected`), the unknown-command test (`test_execute_goal_command_unknown_returns_400`), or the invalid-status 422 tests — their guards fire before the reason check and they pass unchanged.

5b. ADD new tests (same mocked-subprocess style, `patch("asyncio.create_subprocess_exec", AsyncMock())` and assert `mock_exec.assert_not_called()` for the rejections). Cover at minimum:

- Task status `aborted` without reason → 400, `detail` names `reason`, subprocess not called.
- Task status `completed` without reason → 400.
- Goal status `aborted` without reason → 400.
- `complete-task` without reason → 400.
- `complete-goal` without reason → 400.
- Phase `done` without reason → 400.
- Whitespace-only reason (`"   "`) → 400 for a close-out.
- Close-out with reason but no `gate_successor` → 200 and argv contains `"--gate-successor"`, `"none"` (the default).
- Close-out with reason + explicit `gate_successor` → 200 and argv contains the explicit successor verbatim (round-trips through the boundary unchanged).
- Non-close-out status (`"hold"`) with no reason → 200 and argv contains NO `--reason` (guard: non-close-out writes stay flag-free).
- Task status `aborted` with reason → 200, exact argv `("vault-cli", "task", "set", "Test Task", "status", "aborted", "--reason", <reason>, "--gate-successor", <successor>, "--vault", "testvault")`.

### 6. Frontend static-source tests

Create `tests/test_closeout_reason_modal.py` in the pure-Python static-source style of `tests/test_task_menu.py` / `tests/test_cross_view_leak.py` (read `src/vault_ui/static/app.js` and `src/vault_ui/static/index.html` via `pathlib`, string assertions, brace-walk named function bodies where you need body-level precision). Assert at least:

- `index.html` contains `id="reason-modal"`, `id="reason-input"`, `id="gate-successor-input"`, `id="reason-confirm-btn"`, `id="reason-cancel-btn"`.
- `index.html` loads `app.js` with the NEW cache-buster token from requirement 7 and `style.css` with the NEW token.
- `app.js` defines `function askCloseOut` (or `async function askCloseOut`).
- The risk-prompt sentence `Does this task own a trigger, gate, threshold or recurring check?` appears verbatim in `app.js` (the title/risk line is populated from JS) OR in `index.html` (if the text lives in the static HTML) — assert against whichever file actually holds it.
- `patchStatus`'s body JSON construction includes `reason` and `gate_successor` keys (brace-walk `async function patchStatus`, assert `'reason'` and `'gate_successor'` appear inside its body).
- `dispatchMenuAction` calls `askCloseOut` (brace-walk `async function dispatchMenuAction`, assert the call appears and the `abort_task` / `complete_task` / `abort_goal` / `complete_goal` branches are gated on a non-null result).
- `handleDrop` calls `askCloseOut` and the call is guarded by `'done'` (task) / `'completed'` (goal) target checks (brace-walk `async function handleDrop`).

These are the sanctioned in-container guards for frontend behavior in this repo. Do NOT add Playwright/jsdom/node/any new dependency.

### 7. Bump the cache-buster tokens

In `src/vault_ui/static/index.html`, bump BOTH tokens (the repo convention bumps every changed asset — see prior CHANGELOG entries): `app.js?v=2026-08-19-board-sort` → `app.js?v=2026-08-24-closeout-reason` and `style.css?v=2026-08-19-board-sort` → `style.css?v=2026-08-24-closeout-reason` (style.css changes in requirement 4b).

### 8. CHANGELOG

In `CHANGELOG.md`, add a `feat(ui):` bullet under the EXISTING `## Unreleased` section (do not create a new version section; do not touch any `## vX.Y.Z` section). Follow `changelog-guide.md` conventions. The bullet should state, in plain terms: closing out a task or goal (Abort/Complete menu actions and drag-to-Done) now requires a free-text reason and asks where any owned trigger/gate/threshold/recurring check moves (gate successor, `none` if nothing is inherited); both are passed to vault-cli (`--reason` / `--gate-successor`), restoring close-outs against vault-cli v0.116.0+, which rejects aborted/completed writes without `aborted_reason` and `gate_successor`; blank reason is blocked in the UI and by the API (HTTP 400); Hold/Resume/defer are unchanged; bumps the `app.js` and `style.css` cache-bust tokens.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- This repo's `.dark-factory.yaml` sets `hideGit: true` — never run a bare `git` command inside this prompt's verification (it dies with `fatal: not a git repository` in the container and would falsely pass). Use filesystem checks (`grep`, `find`) and `uv run pytest` instead.
- vault-cli is frozen from the UI's side — do not patch vault-cli, do not add workarounds that swallow its rejection. The close-out gate is enforced in vault-ui's request handling and subprocess args only.
- Do NOT add a Pydantic `model_validator` to the request models — the reason requirement is endpoint-level via `_closeout_extra_args` so the 400 detail is a clean string.
- Non-close-out writes (statuses other than `aborted`/`completed`, phase other than `done`, `defer-task`, `defer-goal`, `work-on-task`, `create-task`) MUST behave exactly as before: no modal, no reason requirement, no extra vault-cli flags.
- In `update_task_phase`, the `--reason`/`--gate-successor` flags go ONLY on the status subprocess, never the phase subprocess.
- The exact argv shapes in requirement 3 are load-bearing — tests assert them as tuples; do not reorder, and the flags must sit before `--vault`.
- Frontend is vanilla JS — no framework, no new dependencies. The modal reuses the existing `.modal` / `.modal-content` / `.modal-buttons` classes.
- Do NOT add Playwright / jsdom / node coverage, do NOT extend `tests/test_board_sort.py`, and do NOT run `make test-integration` (needs a host browser + `uv run playwright install chromium`; the container has neither). The modal's interactive behavior is verified by the operator on the host with `make run` — the static-source test in requirement 6 is the automated guard.
- New/updated code follows `docs/dod.md`: type annotations, no debug output, tests for changed behavior, CHANGELOG entry under `## Unreleased`.
- `make precommit` MUST stay green.
</constraints>

<verification>
```bash
# Full pre-commit gate (format + test + lint + typecheck). `make test` deselects
# the integration suite via pytest addopts -m 'not integration', so this runs
# every test this prompt adds or changes. Must exit 0.
make precommit

# Backend close-out behavior explicitly
uv run pytest tests/test_api.py -v
# Expected: all pass, including the new reason-required / argv tests.

# Frontend static-source guard
uv run pytest tests/test_closeout_reason_modal.py -v
# Expected: all pass.

# Confirm the close-out flags reach every vault-cli write site
grep -n "_closeout_extra_args\|--gate-successor" src/vault_ui/api/tasks.py
# Expected: the helper definition plus a call in each of the five endpoints
# (update_task_status, update_goal_status, update_task_phase, execute_slash_command,
# execute_goal_command), and the flags only spliced on close-out paths.

# Confirm the modal exists and the cache-buster tokens are bumped
grep -n 'reason-modal\|app.js?v=\|style.css?v=' src/vault_ui/static/index.html
# Expected: the modal element ids, and app.js + style.css loaded with the
# 2026-08-24-closeout-reason token (no stale 2026-08-19-board-sort reference).

# Confirm the CHANGELOG Unreleased entry exists
grep -n 'Unreleased' CHANGELOG.md
```

Do NOT run `make test-integration`, `git ...`, or any `docker`/`kubectl`/`gh` command — none are executable in the container and all would produce a false-positive verification pass.
</verification>
