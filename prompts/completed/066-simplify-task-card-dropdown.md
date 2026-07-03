---
status: completed
summary: Replaced task-card menu's 5 phase shortcuts with single Abort Task action — new PATCH /tasks/{id}/status endpoint, updated frontend menu, 5 backend tests + 3 frontend tests, CHANGELOG updated
execution_id: vault-ui-exec-066-simplify-task-card-dropdown
dark-factory-version: v0.191.0
created: "2026-07-02T00:00:00Z"
queued: "2026-07-02T21:52:48Z"
started: "2026-07-03T06:46:31Z"
completed: "2026-07-03T06:49:03Z"
---
<summary>
- The task-card three-dots menu drops its 5 redundant phase-shortcut entries (Error, Execution, AI Review, Human Review, Done) plus the now-empty "Move to" header — drag-and-drop between board columns already covers phase moves.
- "Complete Task" and "Defer Task" keep working exactly as before.
- A new "Abort Task" entry appears in the menu; clicking it sets the task's status to `aborted` and the board refreshes.
- Aborting is an instant frontmatter edit (no AI session, no loading modal) — same snappy feel as a drag-and-drop move.
- Drag-and-drop phase changes on the board are untouched (they use a separate code path and endpoint).
- Backend gains a task status endpoint mirroring the existing goal status endpoint, reusing the same validation that already permits `aborted`.
- Invalid statuses, injection-shaped task IDs, vault-cli failures, and vault-cli hangs are all rejected with clear HTTP errors.
- New backend and frontend tests cover the abort path end to end.
</summary>

<objective>
Simplify the task-card three-dots dropdown in the Vault UI: remove the 5 redundant "Move to" phase shortcuts, keep Complete/Defer intact, and add an "Abort Task" action that sets the task's `status` to `aborted` via a new backend endpoint mirroring the existing goal-status endpoint. Implements the vault task "Simplify Vault UI Task Card Dropdown Menu" (phase: execution).
</objective>

<context>
Read `CLAUDE.md` at the repo root for project conventions (Python 3.12 / FastAPI backend, static JS/HTML frontend — this is NOT a Go project).

Frontend — `src/vault_ui/static/app.js`:
- `showTaskMenu(event, taskId)` builds the dropdown. The `menuItems` array is assembled around lines 1505-1522. Today it pushes: `Clear Session` (conditional), `Complete Task` (action `complete_task`), `Defer Task` (action `defer_task`), then a `Move to` header (action `move`) followed by 5 phase shortcuts: `Error` (action `error`, `disabled: true`), `Execution` (action `execution`), `AI Review` (action `ai_review`), `Human Review` (action `human_review`), `Done` (action `done`).
- The forEach at lines 1524-1540 renders each item; items with `action !== 'move'` and `!disabled` get a click handler calling `handleMenuAction(taskId, item.action)`. The `Move to` item gets a `header` CSS class and no handler.
- `handleMenuAction(taskId, action)` (line 1600): dispatches `clear_session` → `clearTaskSession`; `complete_task`/`defer_task` → `executeSlashCommand`; and an `else` branch (lines 1614-1633) that PATCHes `/api/tasks/${taskId}/phase` for the phase-shortcut actions. Once the phase shortcuts are removed, this `else` branch is dead — replace it (see requirements).
- `executeSlashCommand(taskId, commandType)` (line 1674) is the Complete/Defer path — POSTs `/api/tasks/{id}/execute-command`, shows a loading modal, handles the vault-cli fast path. DO NOT change this function.
- `clearTaskSession(taskId)` (line 1754) is the reference pattern for a simple direct-fetch menu action: `fetch(PATCH/DELETE, ...)`, `if (!response.ok) throw new Error(await parseErrorResponse(response))`, then `await loadCurrentView()`, `catch` → `console.error` + `showToast(error.message, true)`. Mirror THIS shape for abort — not the heavier `executeSlashCommand` modal flow.
- NOTE: the board drag-and-drop drop handler (around line 800-820, "Failed to update task phase") also PATCHes `/api/tasks/{id}/phase` but is a completely separate code path from the menu. Leave it and the `/phase` endpoint fully intact.

Backend — `src/vault_ui/api/tasks.py`:
- `UpdateStatusRequest` (lines 196-205) already exists: `status: Literal["next", "in_progress", "backlog", "completed", "hold", "aborted"]`. It already permits `aborted`. Reuse it — do not add a new model.
- `update_goal_status` (line 961, `@router.patch("/goals/{goal_id}/status")`) is the exact template for the new task endpoint: leading-`-` guard (400), `asyncio.create_subprocess_exec(vault_config.vault_cli_path, "goal", "set", goal_id, "status", request.status, "--vault", vault_config.name.lower(), ...)`, `asyncio.wait_for(proc.communicate(), timeout=10.0)` with `TimeoutError` → 504, non-zero returncode → 500, then `_connection_manager.broadcast({"type": "task_updated", "task_id": ..., "item_kind": "task", "vault": vault})`, returning a success dict. Imports already present: `asyncio`, `suppress` from `contextlib`, `get_vault_config`, `HTTPException`.
- `update_task_phase` (line 874, `@router.patch("/tasks/{task_id}/phase")`) shows the task-flavored vault-cli invocation: `vault-cli task set <id> status <value> --vault <name>` — use this subcommand shape (`task set`, not `goal set`) for abort.

Tests — Python (`tests/test_api.py`):
- `test_update_goal_status_uses_vault_cli` (line 917), `test_update_goal_status_invalid_status_returns_422` (line 947), `test_update_goal_status_leading_dash_rejected` (line 959), `test_update_goal_status_vault_cli_failure_returns_500` (line 972) are the exact templates. They assert on `mock_exec.call_args.args` being the full vault-cli arg tuple. The `test_client` fixture is at line 175. For the timeout test, mock `mock_proc.communicate = AsyncMock(side_effect=TimeoutError())` so `asyncio.wait_for` surfaces the hang.

Tests — frontend content (`tests/test_cross_view_leak.py`, `tests/test_view_toggle.py`):
- Frontend is tested by reading `app.js` as text and asserting on substrings: `APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()`. Follow this pattern for the menu test.

Changelog: `CHANGELOG.md` uses `## vX.Y.Z` sections with `- <type>: <desc>` bullets. See `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.
</context>

<requirements>

## Backend — new task status endpoint (`src/vault_ui/api/tasks.py`)

1. Add a new endpoint `@router.patch("/tasks/{task_id}/status")` named `update_task_status(vault: str, task_id: str, request: UpdateStatusRequest) -> dict[str, str]`. Reuse the existing `UpdateStatusRequest` model (do NOT define a new request model). Model it directly on `update_goal_status` (line 961).
2. Reject task IDs starting with `-` with `HTTPException(status_code=400, detail="task_id must not start with '-'")` before any subprocess call, mirroring the goal-endpoint guard (prevents vault-cli argument injection).
3. Invoke vault-cli as `asyncio.create_subprocess_exec(vault_config.vault_cli_path, "task", "set", task_id, "status", request.status, "--vault", vault_config.name.lower(), stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)` where `vault_config = get_vault_config(vault)`. Note the `task set` subcommand (not `goal set`).
4. Wrap `proc.communicate()` in `asyncio.wait_for(..., timeout=10.0)`; on `TimeoutError` call `proc.kill()` under `with suppress(ProcessLookupError):` and raise `HTTPException(status_code=504, detail="vault-cli task set (status) timed out after 10s")`.
5. On non-zero `proc.returncode`, raise `HTTPException(status_code=500, detail=stderr.decode())`.
6. On success, if `_connection_manager` is set, `await _connection_manager.broadcast({"type": "task_updated", "task_id": task_id, "item_kind": "task", "vault": vault})`.
7. Return `{"status": "success", "task_id": task_id, "new_status": request.status}`.
8. Keep the same `except HTTPException: raise`, `except FileNotFoundError` → 404, `except ValueError` → 400 tail as `update_goal_status`.
9. Do NOT touch `update_task_phase` or the `/tasks/{id}/phase` endpoint — board drag-and-drop still depends on it.

## Frontend — remove phase shortcuts, add Abort (`src/vault_ui/static/app.js`)

10. In `showTaskMenu`, delete these 6 `menuItems.push(...)` lines (currently lines ~1517-1522): the `Move to` header entry AND all 5 phase shortcuts (`Error`, `Execution`, `AI Review`, `Human Review`, `Done`). The `Move to` header is removed too because it would otherwise sit above nothing.
11. Immediately after the `Defer Task` push (line ~1514), add: `menuItems.push({ label: 'Abort Task', action: 'abort_task', disabled: false });`
12. In the forEach that renders items (lines ~1524-1540), delete the two now-unreachable guards: the `if (item.label === 'Move to') { menuItem.classList.add('header'); }` block AND the `item.action !== 'move'` clause in the click-handler condition (change `if (!item.disabled && item.action !== 'move')` to `if (!item.disabled)`). After the menu changes there are no `move`/`header` items left, so these guards are dead — remove them (req 19's frontend test asserts `"label: 'Move to'"` no longer appears). Do NOT change any other rendering behavior.
13. In `handleMenuAction`, replace the dead `else` branch (lines ~1614-1633, the phase PATCH) with an explicit `else if (action === 'abort_task')` branch that performs a direct fetch mirroring `clearTaskSession`:
    ```javascript
    } else if (action === 'abort_task') {
        try {
            const response = await fetch(
                `/api/tasks/${encodeURIComponent(taskId)}/status?vault=${encodeURIComponent(task.vault)}`,
                {
                    method: 'PATCH',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ status: 'aborted' }),
                }
            );
            if (!response.ok) {
                throw new Error(await parseErrorResponse(response));
            }
            showToast('Task aborted');
            await loadCurrentView();
        } catch (error) {
            console.error('Failed to abort task:', error);
            showToast(error.message, true);
        }
    }
    ```
    There must be NO trailing generic `else` phase-PATCH branch after this — the phase shortcuts are gone, so any leftover `else` calling `/phase` is dead code and must be removed.
14. Do NOT add a confirmation dialog (mirrors Complete/Defer, which have none). See Assumptions.

## Tests

15. In `tests/test_api.py`, add `test_update_task_status_uses_vault_cli` modeled on `test_update_goal_status_uses_vault_cli` (line 917): PATCH `/api/tasks/Test%20Task/status?vault=TestVault` with `{"status": "aborted"}`, assert 200, assert body `{"status": "success", "task_id": "Test Task", "new_status": "aborted"}`, and assert `mock_exec.call_args.args == ("vault-cli", "task", "set", "Test Task", "status", "aborted", "--vault", "testvault")`. This test traverses the pydantic-validation + subprocess boundary with the real `aborted` value.
16. Add `test_update_task_status_invalid_status_returns_422` (model on line 947): PATCH with `{"status": "abortd"}` (typo) asserts 422 and `mock_exec.assert_not_called()`.
17. Add `test_update_task_status_leading_dash_rejected` (model on line 959): PATCH `/api/tasks/-help/status?vault=TestVault` asserts 400, `"task_id must not start with '-'"` in detail, `mock_exec.assert_not_called()`.
18. Add `test_update_task_status_vault_cli_failure_returns_500` (model on line 972): mock proc `returncode = 1`, assert 500.
19. Add `test_update_task_status_timeout_returns_504`: build `mock_proc = MagicMock()` with `mock_proc.communicate = AsyncMock(side_effect=TimeoutError())` (so `asyncio.wait_for` raises `TimeoutError`), give it a no-op `mock_proc.kill = MagicMock()`, patch `asyncio.create_subprocess_exec` to return it, PATCH `/api/tasks/Test%20Task/status?vault=TestVault` with `{"status": "aborted"}`, and assert `response.status_code == 504` and `"timed out"` in `response.json()["detail"]`. This closes the hang→504 loop advertised in req 4 and the summary.
20. Create `tests/test_task_menu.py` following the `APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()` pattern (see `tests/test_cross_view_leak.py` lines 1-17 for the `REPO_ROOT` / `read_text` boilerplate). Add tests asserting:
    - `"'Abort Task'"` and `"action: 'abort_task'"` appear in `APP_JS`.
    - The removed phase-shortcut wiring is gone: assert `"action: 'ai_review'"`, `"action: 'human_review'"`, and `"label: 'Move to'"` do NOT appear in `APP_JS`. (Do NOT assert on the bare word `'done'` or `'execution'` — those strings occur elsewhere, e.g. `formatPhase`. Anchor on the removed `menuItems.push` action/label literals only.)
    - `handleMenuAction` dispatches abort via the status endpoint: slice from `APP_JS.find("async function handleMenuAction")` forward ~1200 chars and assert both `"abort_task"` and `"/status?vault="` appear, and that `"status: 'aborted'"` appears.

## Changelog

21. Add a `CHANGELOG.md` entry under a new top version section (bump the patch or minor per the existing `## vX.Y.Z` convention; current top is `## v0.42.1`). One bullet, e.g.: `- feat(ui): Replace the task-card menu's 5 "Move to" phase shortcuts with a single "Abort Task" action — drag-and-drop already covers phase moves; abort sets task status to \`aborted\` via new \`PATCH /api/tasks/{id}/status\` (mirrors the goal status endpoint). Complete/Defer unchanged.` Follow `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

</requirements>

<constraints>
- This is a Python 3.12 / FastAPI + static JS project — no Go tooling applies.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Do NOT change `executeSlashCommand`, the Complete/Defer wiring, `clearTaskSession`, `update_task_phase`, or the `/tasks/{id}/phase` endpoint (board drag-and-drop depends on it).
- Reuse the existing `UpdateStatusRequest` model — do NOT add a new request model or a new status literal.
- No confirmation dialog on abort (mirrors Complete/Defer).
</constraints>

<verification>
Run `make precommit` — must pass (runs format, test, lint, typecheck).
Manually confirm in `tests/test_api.py` the new task-status tests assert the full vault-cli arg tuple including `"aborted"`, and that the 504 test asserts `response.status_code == 504`.
</verification>

<assumptions>
- **Endpoint choice**: implemented abort as a new dedicated `PATCH /tasks/{id}/status` endpoint mirroring the existing goal-status endpoint, rather than routing through `execute-command`. Rationale: abort is an instant frontmatter edit; `execute-command`'s slash-command semantics + loading modal are the wrong fit, and `UpdateStatusRequest` already permits `aborted`. Alternative (add an `abort-task` fast path to `execute-command`) was rejected as heavier and less idiomatic.
- **"Move to" header removed** along with the 5 shortcuts (it would otherwise head an empty section).
- **No confirmation dialog** on abort, mirroring Complete/Defer. If a confirm step is desired, flag it — it's a one-line `if (!confirm(...)) return;` addition.
- **Changelog version bump**: chose the next section per existing convention; adjust major/minor/patch to taste.
</assumptions>
