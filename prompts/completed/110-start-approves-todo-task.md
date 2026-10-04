---
status: completed
execution_id: vault-ui-start-approve-exec-110-start-approves-todo-task
dark-factory-version: v0.196.0
created: "2026-10-03T23:35:27Z"
queued: "2026-10-03T23:49:18Z"
started: "2026-10-04T07:30:09Z"
completed: "2026-10-04T07:35:23Z"
branch: dark-factory/110-start-approves-todo-task
---

# Start Approves a Todo Task Before It Opens the Session

<summary>
- Pressing Start on a card in the board's todo column now opens a session instead of failing with a red banner.
- The Start click is treated as the operator's own approval of that task, which is exactly what the backend was refusing.
- Start first approves the task, then opens the session, in that order.
- A card that is already past the approval step starts exactly as it does today, with no extra approval write.
- A card whose phase is missing or unrecognised also starts exactly as it does today.
- If the approval itself fails, no session is opened and the failure surfaces as an error, as it does today.
- The approval step reuses the existing task-approval command, wrapped the same way as the board's other commands.
- Tests cover the todo path (approve runs first, then the launch) and the already-approved path (no approval call at all).
- The changelog records the fix.
</summary>

<objective>
`run_task` in the Python backend approves a task that is still in the `todo` phase before it launches the headless session, so the board's Start button works for cards in the todo column instead of failing with `vault-cli work-on failed: ... task is at phase "todo", not yet approved`. The Start click is the operator's own surface, so it is a legitimate approval source.
</objective>

<context>
Read `docs/dod.md` — the definition of done this prompt is validated against.

Read these before writing anything:

- `src/vault_ui/api/tasks.py` — `run_task` is the `POST /tasks/{task_id}/run` handler (the Start click). It reads the task with `client.show_task(task_id)`, then calls `get_launch_registry().begin(vault, task_id, "task")`, writes the `claude_session_started` marker via `client.set_field`, and launches via `start_vault_cli_session`. Note its exception mapping: `HTTPException` re-raised, `FileNotFoundError` → 404, anything else → 500.
- `src/vault_ui/vault_cli_client.py` — `VaultCLIClient`. `set_field` and `clear_field` are the exemplars for a subprocess wrapper: build the argv, `await asyncio.create_subprocess_exec(...)` with `stdout=PIPE, stderr=PIPE`, `await proc.communicate()`, and on a non-zero return code raise `RuntimeError(f"vault-cli task <verb> failed: {stderr.decode().strip()}")`. There is no `approve` method today.
- `tests/test_api.py` — `_make_vault_client` (the mock client used by the `test_client` fixture), `_make_task` (its `phase` parameter defaults to `"planning"`), `_make_streaming_proc`, `test_run_task_endpoint_success`, and `test_run_task_sets_started_flag_and_clears_on_success`. Follow their patterns.
- `tests/test_vault_cli_client.py` — `_make_proc(returncode, stdout, stderr)` and the argv-shape tests (`test_list_topics_always_passes_all`, `test_show_topic_builds_argv`). Follow their patterns.

The vault-cli contract this prompt relies on (do not change vault-cli; this repo only calls it):

- `vault-cli task approve <task-name> --vault <name>` moves a task at phase `todo` to phase `planning` and records `approved_by` and `approved_at` in the same write. The `--by` flag defaults to `operator`.
- It **refuses with a non-zero exit** when the task is not at phase `todo` — so the call must be gated on the phase, not attempted unconditionally.

`docs/starting-marker-lifecycle.md` describes the `claude_session_started` marker lifecycle. This change does not alter that lifecycle, so no documentation change is required.
</context>

<requirements>
1. Add `approve_task` to `VaultCLIClient` in `src/vault_ui/vault_cli_client.py`, following the `set_field`/`clear_field` idiom:
   - Signature: `async def approve_task(self, task_id: str) -> None:`
   - Argv: `[self._vault_cli_path, "task", "approve", task_id, "--vault", self._vault_name]`, run via `asyncio.create_subprocess_exec(..., stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)`.
   - `await proc.communicate()`; on a non-zero return code raise `RuntimeError(f"vault-cli task approve failed: {stderr.decode().strip()}")`; otherwise return `None`.
   - Do not pass `--output json` — the sibling wrappers do not, and the refusal text belongs in stderr.
   - Do not pass `--by` — the command's default `operator` is the correct attribution for the operator's own Start click.
   - Do not pass `--assignee` — owner resolution is vault-cli's own concern.
   - Add a short docstring stating that this is the `todo` → `planning` approval and that it refuses any task not at phase `todo`.

2. In `run_task` in `src/vault_ui/api/tasks.py`, insert the approval step immediately after `task = await client.show_task(task_id)` and **before** `get_launch_registry().begin(vault, task_id, "task")`:

   ```python
   # The Start click is the operator's own approval surface: a card still in the
   # approval inbox (phase "todo") is approved here before the launch, because
   # vault-cli work-on refuses a task that has not been approved. Any other phase
   # (including a missing one) is already past approval and takes the old path.
   if task.phase == "todo":
       await client.approve_task(task_id)
   ```

   Compare against the literal string `"todo"` — `task.phase` is `str | None`, so `None` and every other phase skip the call.

3. Placement and failure behaviour:
   - The approval runs before the launch registry `begin()` and before the `claude_session_started` marker write, so a refused or failed approval leaves no launch in flight — no marker to clear and no registry record to finish.
   - A failing approval propagates to the existing `except Exception` handler and answers 500, exactly as a launch failure does. Do not add a new HTTP status code and do not wrap the approval in its own `try`/`except`.

4. Change nothing else in the launch path: leave `start_vault_cli_session`, `start_vault_cli_goal_session`, `run_goal`, and the `execute_slash_command` launch (its `start_vault_cli_session` call site) untouched. Only `run_task` gains the approval step. `execute_slash_command` reaches `start_vault_cli_session` only for the `work-on-task` / `create-task` commands, which the board's frontend never sends (it maps only `complete_task` / `defer_task`), so its todo gate is unreachable; goals carry no `phase` field, so `run_goal` is unaffected.

5. Tests in `tests/test_api.py`:
   - Add `client.approve_task = AsyncMock()` to `_make_vault_client` so the shared mock client carries the new method.
   - **Todo path**: append a task with `phase="todo"` to `mock_vault_client._tasks` (use `_make_task`), then POST to `/api/tasks/<id>/run?vault=TestVault`. Record order with a shared list: give `approve_task` an `AsyncMock(side_effect=...)` that appends `"approve"`, and patch `asyncio.create_subprocess_exec` with an `AsyncMock(side_effect=...)` that appends `"work-on"` and returns `_make_streaming_proc(...)`. Assert the response is 200, the recorded order is exactly `["approve", "work-on"]`, and `approve_task` was awaited once with the task id.
   - **Already-approved path**: with the default sample task (`phase="planning"`), run the same POST with `asyncio.create_subprocess_exec` patched and assert the response is 200 with the same keys as today, and `mock_vault_client.approve_task.assert_not_awaited()`.

6. Tests in `tests/test_vault_cli_client.py` for `approve_task`, using the existing `_make_proc` helper:
   - **Argv shape** (the subprocess boundary contract): with return code 0, `await client.approve_task("Todo Task")` calls `asyncio.create_subprocess_exec` with exactly `["vault-cli", "task", "approve", "Todo Task", "--vault", "TestVault"]` — assert `list(mock_exec.call_args.args)`.
   - **Refusal**: return code 1 with stderr `b'refusing to approve "Todo Task": task is at phase "planning", not "todo"'` raises `RuntimeError` whose message contains that stderr text.
   - **Success**: return code 0 returns `None` without raising.

7. `CHANGELOG.md`: the file currently has no `## Unreleased` section — its first section heading is `## v0.75.0`. Add a `## Unreleased` section at the top (below the intro lines, above `## v0.75.0`) with one `- fix:` bullet: the board's Start button now approves a task still in the todo phase before opening its session, so starting a todo card no longer fails with `vault-cli work-on ... task is at phase "todo"`.

8. Type annotations on all new code (the `approve_task` signature and any new test helper).

9. Before finishing, re-run `<verification>` and confirm it passes, then walk each acceptance criterion above against the change.
</requirements>

<constraints>
- Do not run any `git` command.
- Do not commit — dark-factory handles git.
- Do not change `vault-cli task approve` or `vault-cli work-on` semantics, or any vault-cli code — this repo only invokes the CLI.
- Approve only from the Start click (`run_task`). Do not add approval to any other surface or route.
- Do not change behaviour for cards whose phase is not `todo` — no extra approval call, no extra writes.
- Do not touch the Go backend or the Go rewrite.
- Do not modify existing tests beyond adding `client.approve_task = AsyncMock()` to `_make_vault_client`.
- Existing tests must still pass.
- Do not add dependencies.
- No debug output — if logging is needed, use the module logger.
</constraints>

<verification>
set -eo pipefail
make precommit
grep -q 'async def approve_task' src/vault_ui/vault_cli_client.py
grep -q 'client.approve_task(task_id)' src/vault_ui/api/tasks.py
grep -q 'approve_task' tests/test_vault_cli_client.py
grep -q 'approve_task' tests/test_api.py
grep -q '^## Unreleased' CHANGELOG.md
</verification>
