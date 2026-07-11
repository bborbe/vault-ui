---
status: completed
spec: [015-goal-start-resume-session]
execution_id: vault-ui-exec-069-spec-015-backend-goal-session-endpoints
dark-factory-version: v0.191.0
created: "2026-07-11T09:56:44Z"
queued: "2026-07-11T10:08:01Z"
started: "2026-07-11T10:13:00Z"
completed: "2026-07-11T10:17:58Z"
---

<summary>
- Adds a backend endpoint that launches a goal into a real Claude session, mirroring the existing task Start flow.
- The launch shells out to `vault-cli goal work-on`, stores the returned session id on the goal, and returns a ready-to-run resume command.
- Adds a second backend endpoint that clears a goal's stored session so its card can revert to Start.
- Both endpoints reject goal ids that begin with `-` before touching vault-cli (argument-injection guard).
- Launch failures (goal not found, vault-cli non-zero exit, non-JSON output, no session minted, clear hang) surface as clean HTTP error codes, never a silent success.
- Adds a regression test proving the goals list still exposes each goal's session id so the frontend can choose Start vs Resume.
- Confirms the already-shipped stale-session cleanup for goals still works (existing test), and adds no cleanup logic.
- Frontend is untouched here — that is the sibling prompt's job.
</summary>

<objective>
Add two goal-session endpoints to the Task Orchestrator backend, mirroring the task equivalents: `POST /api/goals/{goal_id}/run` (mint a Claude session via `vault-cli goal work-on`, store `claude_session_id` on the goal, return a `SessionResponse` with a resume command) and `DELETE /api/goals/{goal_id}/session` (clear `claude_session_id`). Both reject goal ids starting with `-`. Failures surface as HTTP 404 / 500 / 504 exactly as the spec's Failure Modes table requires. No frontend changes and no new cleanup logic.
</objective>

<context>
Read `/workspace/CLAUDE.md` for project conventions (Python 3.12, FastAPI, uv, pytest).

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `python-pydantic-guide.md` — Pydantic v2 patterns used by the response models.
- `changelog-guide.md` — bullet style (CHANGELOG is added in the sibling frontend prompt, not here).
- `python-logging-guide.md` — the `logging.getLogger(__name__)` + `logger.exception` idiom already used in `tasks.py`.

Also read the project DoD at `/workspace/docs/dod.md` — CHANGELOG goes under `## Unreleased`; new code targets ≥80% coverage; tests must mock the vault-cli subprocess and network (no real vault-cli / Claude / filesystem calls).

Read these source files in full before editing (paths are container-side under `/workspace`):
- `src/vault_ui/api/tasks.py` — the file you are extending. Study these exact anchors:
  - `start_vault_cli_session(vault_config: VaultConfig, task_id: str) -> str` (the streaming mint you will parallel). It runs `vault-cli task work-on <task_id> --mode headless --vault <vault_config.name> --output json`, streams stdout/stderr via `_drain_stream`, parses the last JSON value via `_last_json_value`, and raises `RuntimeError` on non-zero exit / non-JSON / non-dict / no `session_id`.
  - `run_task(vault: str, task_id: str) -> SessionResponse` — the happy path you mirror: read the item, mint, `_build_resume_command`, return `SessionResponse`. Note it wraps everything in `try/except FileNotFoundError -> 404` / `except Exception -> 500`.
  - `_build_resume_command(vault_config, session_id, *, task_title=None) -> str` — title-source-agnostic; pass the goal title through `task_title=`.
  - `update_goal_status(...)` — the inline-subprocess pattern you copy for the clear endpoint: `asyncio.create_subprocess_exec(...)` with `--vault vault_config.name.lower()`, then `await asyncio.wait_for(proc.communicate(), timeout=10.0)`, `except TimeoutError` → `proc.kill()` (guarded by `with suppress(ProcessLookupError)`) → HTTP 504, non-zero `returncode` → HTTP 500 with `stderr.decode()`, and `http_request.app.state.vault_goal_cache.pop(vault, None)` before returning.
  - `clear_task_session(vault: str, task_id: str)` — the task DELETE-session mirror (it uses the client, no timeout; you use the inline-subprocess pattern instead because the spec Failure Mode requires a 504 + killed process on a clear hang).
  - The `goal_id.startswith("-")` guard used at the top of `update_goal_status`, `execute_goal_command`, and `assign_goal_to_me` (raises `HTTPException(status_code=400, detail="goal_id must not start with '-'")`).
- `src/vault_ui/vault_cli_client.py` — `list_goals(self, show_all: bool = False) -> list[Goal]`, `set_goal_field(self, goal_id, key, value) -> None`, `clear_goal_field(self, goal_id, key) -> None`. These already exist; do NOT modify them.
- `src/vault_ui/api/models.py` — `SessionResponse` (fields: `session_id: str`, `command: str`, `working_dir: str`, `task_title: str`, plus optional `executed_command`/`success`/`error`/`response`) and `GoalResponse` (already carries `claude_session_id: str | None = None` — regression invariant, do NOT change).
- `src/vault_ui/config.py` — `VaultConfig` has `name: str`, `vault_path: str`, `vault_cli_path: str = "vault-cli"`, `claude_script`, `session_project_dir`.
- `src/vault_ui/cleanup.py` — the goal sweep already clears stale `claude_session_id` from goals (independent try/except around `list_goals` + `goal clear`). Do NOT touch this file.
- `tests/test_api.py` — study these test helpers you will reuse:
  - `_make_goal(...)` (creates a `Goal`), `_make_goal_client(...)`.
  - `mock_vault_client_with_goals` fixture — a `MagicMock` client whose `list_goals` is an `AsyncMock` closing over `client._goals` (a mutable list), and which already has `set_goal_field = AsyncMock()`, `clear_field = AsyncMock()`, `set_field = AsyncMock()` (from `_make_vault_client`). It does NOT set `clear_goal_field` — but the clear endpoint uses an inline subprocess, not the client, so you won't need it.
  - `test_client_with_goals` fixture — a `TestClient` with `get_vault_cli_client_for_vault` patched to return `mock_vault_client_with_goals`; the test vault is `TestVault` with `vault_path=tmp_vault`.
  - `_make_streaming_proc(response_json: bytes) -> MagicMock` — a fake subprocess compatible with the streaming mint (`.stdout`/`.stderr` fake streams with `readline()`, `.wait = AsyncMock(return_value=0)`). Reuse it for the run tests.
  - `test_run_task_endpoint_success` (line ~315) — the exact `with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)): ...` shape you mirror.
- `tests/test_cleanup.py` — `test_goal_uuid_cleared_on_missing_file` already exercises the goal stale-session sweep (a goal with a UUID session id whose `.jsonl` is absent gets `goal clear`ed). This IS the spec AC#7 regression guard — it must keep passing; do NOT add a duplicate cleanup test.

Out of scope for this prompt (do NOT do): any `app.js` / `index.html` / CSS change; the CHANGELOG entry; the `?v=` cache-bust bump — all belong to prompt 2. Do NOT add a durable "Starting…" flag for goals (spec Non-goal). Do NOT modify `start_vault_cli_session`, `run_task`, `clear_task_session`, or any task endpoint (spec Non-goal — task-card session behavior is unchanged). Do NOT modify `vault_cli_client.py` goal methods or `cleanup.py`.
</context>

<requirements>

### 1. Add `start_vault_cli_goal_session` in `src/vault_ui/api/tasks.py`

Add a new async function directly after `start_vault_cli_session` (do NOT change `start_vault_cli_session` — it has other callers). It is a goal-targeted parallel that reuses the shared `_drain_stream` and `_last_json_value` helpers:

```python
async def start_vault_cli_goal_session(vault_config: VaultConfig, goal_id: str) -> str:
    """Start a Claude session for a goal via ``vault-cli goal work-on``, returns session_id.

    Parallels ``start_vault_cli_session`` (which targets tasks) but runs
    ``vault-cli goal work-on <goal_id> --mode headless --vault <name> --output json``.
    Streams stdout/stderr line-by-line to the logger via ``_drain_stream`` while
    accumulating raw bytes for the final JSON parse. Every diagnostic RuntimeError
    names the goal id and vault so a failure is diagnosable from the toast + log
    (spec Failure Mode rows 2 and 3).
    """
    proc = await asyncio.create_subprocess_exec(
        vault_config.vault_cli_path,
        "goal",
        "work-on",
        goal_id,
        "--mode",
        "headless",
        "--vault",
        vault_config.name,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        limit=1 << 20,  # 1 MiB per-line buffer (matches start_vault_cli_session)
    )
    assert proc.stdout is not None
    assert proc.stderr is not None

    stdout_buf = bytearray()
    stderr_buf = bytearray()

    await asyncio.gather(
        _drain_stream(proc.stdout, "stdout", goal_id, stdout_buf),
        _drain_stream(proc.stderr, "stderr", goal_id, stderr_buf),
    )

    returncode = await proc.wait()

    if returncode != 0:
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli goal work-on failed (rc={returncode}) for goal {goal_id!r} "
            f"in vault {vault_config.name!r}: {stderr_text}"
        )

    stdout_text = bytes(stdout_buf).decode()
    try:
        parsed = _last_json_value(stdout_text)
    except json.JSONDecodeError as e:
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli goal work-on returned non-JSON output (rc={returncode}) for "
            f"goal {goal_id!r} in vault {vault_config.name!r}: {e}. "
            f"stdout ({len(stdout_text)} chars)={stdout_text[:500]!r}; "
            f"stderr={stderr_text!r}"
        ) from e
    if not isinstance(parsed, dict):
        stderr_text = bytes(stderr_buf).decode(errors="replace").strip()
        raise RuntimeError(
            f"vault-cli goal work-on returned {type(parsed).__name__} (expected JSON object, "
            f"rc={returncode}) for goal {goal_id!r} in vault {vault_config.name!r}. "
            f"stdout ({len(stdout_text)} chars)={stdout_text[:500]!r}; "
            f"stderr={stderr_text!r}"
        )
    result: dict[str, Any] = parsed
    session_id: str = result.get("session_id") or ""
    if not session_id:
        warnings: list[str] = result.get("warnings") or []
        detail = "; ".join(warnings) if warnings else "no warnings reported"
        raise RuntimeError(
            f"vault-cli goal work-on did not start a claude session for goal {goal_id!r} "
            f"in vault {vault_config.name!r}: {detail}"
        )
    return session_id
```

`asyncio`, `json`, `Any`, `VaultConfig`, `_drain_stream`, and `_last_json_value` are all already imported/defined in `tasks.py` — do not re-import.

### 2. Add `POST /api/goals/{goal_id}/run` in `src/vault_ui/api/tasks.py`

Place it next to the other goal endpoints (after `execute_goal_command`, or after `run_task` — pick one adjacency and keep it). vault-cli has no `goal show`, so resolve the goal's title and existence through the existing `list_goals(show_all=True)` surface: a missing goal yields HTTP 404 (spec Failure Mode row 1) before any session is minted.

```python
@router.post("/goals/{goal_id}/run", response_model=SessionResponse)
async def run_goal(
    vault: str,
    goal_id: str,
) -> SessionResponse:
    """Create a Claude Code session for the given goal.

    Mirrors ``run_task`` for goals: mint a session via ``vault-cli goal work-on``,
    store the returned ``claude_session_id`` on the goal frontmatter via vault-cli,
    and return a ``SessionResponse`` with a ready-to-run resume command.

    Raises:
        HTTPException 400: goal_id starts with '-'
        HTTPException 404: goal not found in the vault
        HTTPException 500: vault-cli non-zero exit / non-JSON / no session minted
    """
    logger.info(f"run_goal called: vault={vault}, goal_id={goal_id}")

    # Reject goal IDs starting with `-` before any subprocess (arg-injection guard,
    # same as update_goal_status).
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    try:
        client = get_vault_cli_client_for_vault(vault)
        vault_config = get_vault_config(vault)

        # vault-cli has no `goal show`; list_goals(show_all=True) is the existing
        # surface. Resolving here yields the title for the resume command AND a
        # clean 404 when the goal does not exist (spec Failure Mode row 1).
        goals = await client.list_goals(show_all=True)
        goal = next((g for g in goals if g.id == goal_id), None)
        if goal is None:
            raise HTTPException(status_code=404, detail=f"Goal not found: {goal_id}")

        logger.info(f"Starting vault-cli goal session for goal {goal_id}")
        session_id = await start_vault_cli_goal_session(vault_config, goal_id)
        logger.info(f"Goal session {session_id} created")

        # Store the minted session id on the goal via vault-cli (not a direct file
        # write) so /api/goals surfaces it and the card flips to Resume.
        await client.set_goal_field(goal_id, "claude_session_id", session_id)

        command = _build_resume_command(vault_config, session_id, task_title=goal.title)

        return SessionResponse(
            session_id=session_id,
            command=command,
            working_dir=vault_config.vault_path,
            task_title=goal.title,
        )

    except HTTPException:
        raise
    except FileNotFoundError as e:
        logger.error(f"Goal not found: {e}")
        raise HTTPException(status_code=404, detail=str(e)) from e
    except Exception as e:
        logger.exception(f"Error creating goal session: {e}")
        raise HTTPException(status_code=500, detail=str(e)) from e
```

`SessionResponse`, `HTTPException`, `get_vault_cli_client_for_vault`, `get_vault_config`, `_build_resume_command`, and `logger` are already imported/defined in `tasks.py`.

### 3. Add `DELETE /api/goals/{goal_id}/session` in `src/vault_ui/api/tasks.py`

Use the inline-subprocess pattern copied from `update_goal_status` (NOT `client.clear_goal_field`) because the spec Failure Mode requires a bounded 10s timeout that returns HTTP 504 and kills the wedged process — behavior the client method cannot provide. Run `vault-cli goal clear <goal_id> claude_session_id --vault <name.lower()>`.

```python
@router.delete("/goals/{goal_id}/session")
async def clear_goal_session(
    http_request: Request,
    vault: str,
    goal_id: str,
) -> dict[str, str]:
    """Clear ``claude_session_id`` from goal frontmatter (Reset Session).

    Mirrors ``clear_task_session`` but uses the inline-subprocess pattern with a
    bounded 10s timeout (spec Failure Mode: a wedged ``goal clear`` surfaces as
    HTTP 504 with the process killed, not a hung request).

    Raises:
        HTTPException 400: goal_id starts with '-'
        HTTPException 404: vault-cli BINARY not found (FileNotFoundError only)
        HTTPException 500: vault-cli non-zero exit — INCLUDING a missing goal (vault-cli
            exits non-zero; this matches update_goal_status and every other goal write
            endpoint. Do NOT add a list_goals pre-check to the clear path just to turn
            goal-not-found into 404 — the run path pre-fetches only because it needs the
            title; clear does not. The error still surfaces cleanly as a toast.)
        HTTPException 504: vault-cli goal clear timed out (process killed)
    """
    if goal_id.startswith("-"):
        raise HTTPException(status_code=400, detail="goal_id must not start with '-'")

    try:
        vault_config = get_vault_config(vault)

        proc = await asyncio.create_subprocess_exec(
            vault_config.vault_cli_path,
            "goal",
            "clear",
            goal_id,
            "claude_session_id",
            "--vault",
            vault_config.name.lower(),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        # 10s timeout — vault-cli `goal clear` is a single-file frontmatter edit;
        # anything beyond this is a hang we surface as HTTP 504.
        try:
            _stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=10.0)
        except TimeoutError as e:
            with suppress(ProcessLookupError):
                proc.kill()
            raise HTTPException(
                status_code=504, detail="vault-cli goal clear (session) timed out after 10s"
            ) from e

        if proc.returncode != 0:
            raise HTTPException(status_code=500, detail=stderr.decode())

        # Invalidate the per-vault goal cache synchronously so the card's
        # Start/Resume state updates without waiting for the async watcher
        # (same rationale as update_goal_status).
        http_request.app.state.vault_goal_cache.pop(vault, None)

        if _connection_manager:
            await _connection_manager.broadcast(
                {"type": "goal_updated", "goal_id": goal_id, "item_kind": "goal", "vault": vault}
            )

        return {"status": "success", "goal_id": goal_id}
    except HTTPException:
        raise
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
```

`Request`, `suppress`, `ProcessLookupError` (builtin), and `_connection_manager` are already imported/defined in `tasks.py`.

### 4. Tests in `tests/test_api.py`

Append these tests (use the existing `test_client_with_goals` / `mock_vault_client_with_goals` fixtures, `_make_goal`, and `_make_streaming_proc`). All mock the subprocess and the client — no real vault-cli / network / filesystem.

**4a. Run happy path** — asserts HTTP 200, the mocked `session_id` in the body, a `_build_resume_command`-built resume command, and that `set_goal_field` stored the minted id:
```python
def test_run_goal_endpoint_success(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """POST /api/goals/{id}/run mints a session, stores it, returns a resume command."""
    mock_proc = _make_streaming_proc(b'{"session_id": "goal-session-id"}')
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)) as mock_exec:
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")
    assert response.status_code == 200
    data = response.json()
    assert data["session_id"] == "goal-session-id"
    assert "claude --resume goal-session-id" in data["command"]
    assert data["task_title"] == "Test Goal"
    # Boundary: assert the exact vault-cli argv (mirror the clear test's argv check).
    # Guards a "task" vs "goal" or wrong-flag typo that would still return the mocked
    # session_id and pass every other assertion above.
    argv = mock_exec.call_args.args
    for tok in ("goal", "work-on", "Test Goal", "--mode", "headless", "--output", "json"):
        assert tok in argv, f"missing {tok!r} in vault-cli argv {argv}"
    mock_vault_client_with_goals.set_goal_field.assert_awaited_once_with(
        "Test Goal", "claude_session_id", "goal-session-id"
    )
```

**4b. Goal not found → 404, no subprocess** (the default mock has only "Test Goal"):
```python
def test_run_goal_endpoint_not_found(test_client_with_goals: TestClient) -> None:
    """A goal_id not present in the vault returns HTTP 404 before minting."""
    response = test_client_with_goals.post("/api/goals/NoSuchGoal/run?vault=TestVault")
    assert response.status_code == 404
```

**4c. Dash-prefixed goal id → 400, no subprocess spawned** (AC#4):
```python
def test_run_goal_dash_prefix_rejected(test_client_with_goals: TestClient) -> None:
    """A goal_id starting with '-' is rejected with HTTP 400 and no subprocess."""
    mock_exec = AsyncMock()
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.post("/api/goals/-evil/run?vault=TestVault")
    assert response.status_code == 400
    mock_exec.assert_not_called()
```

**4d. Three diagnosable-500 cases** (AC#3) — non-zero exit, non-JSON, no session — each asserts HTTP 500 and that the detail names the goal id and vault:
```python
@pytest.mark.parametrize(
    "proc_factory",
    [
        pytest.param("nonzero", id="nonzero_exit"),
        pytest.param("nonjson", id="non_json"),
        pytest.param("nosession", id="no_session"),
    ],
)
def test_run_goal_failures_return_diagnosable_500(
    test_client_with_goals: TestClient, proc_factory: str
) -> None:
    if proc_factory == "nonzero":
        proc = _make_streaming_proc(b"")
        proc.wait = AsyncMock(return_value=1)
    elif proc_factory == "nonjson":
        proc = _make_streaming_proc(b"not json at all")
    else:  # nosession
        proc = _make_streaming_proc(b'{"foo": "bar"}')
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        response = test_client_with_goals.post("/api/goals/Test%20Goal/run?vault=TestVault")
    assert response.status_code == 500
    detail = response.json()["detail"]
    assert "Test Goal" in detail
    assert "TestVault" in detail
```

**4e. Clear session happy path** (AC#5) — asserts HTTP 200 and that the vault-cli `goal clear ... claude_session_id` path was invoked with the goal id:
```python
def test_clear_goal_session_success(test_client_with_goals: TestClient) -> None:
    """DELETE /api/goals/{id}/session clears claude_session_id via vault-cli."""
    proc = MagicMock()
    proc.communicate = AsyncMock(return_value=(b"", b""))
    proc.returncode = 0
    mock_exec = AsyncMock(return_value=proc)
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.delete(
            "/api/goals/Test%20Goal/session?vault=TestVault"
        )
    assert response.status_code == 200
    assert response.json()["goal_id"] == "Test Goal"
    args = mock_exec.call_args.args
    assert "goal" in args
    assert "clear" in args
    assert "claude_session_id" in args
    assert "Test Goal" in args
```

**4f. Clear session dash-prefix rejected**:
```python
def test_clear_goal_session_dash_prefix_rejected(test_client_with_goals: TestClient) -> None:
    mock_exec = AsyncMock()
    with patch("asyncio.create_subprocess_exec", mock_exec):
        response = test_client_with_goals.delete("/api/goals/-evil/session?vault=TestVault")
    assert response.status_code == 400
    mock_exec.assert_not_called()
```

**4g. Clear session timeout → 504 + process killed** (Failure Mode row 4):
```python
def test_clear_goal_session_timeout_returns_504(test_client_with_goals: TestClient) -> None:
    proc = MagicMock()
    proc.communicate = AsyncMock(side_effect=TimeoutError())
    proc.kill = MagicMock()
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        response = test_client_with_goals.delete(
            "/api/goals/Test%20Goal/session?vault=TestVault"
        )
    assert response.status_code == 504
    proc.kill.assert_called_once()
```

**4h. Clear session vault-cli non-zero → 500**:
```python
def test_clear_goal_session_nonzero_returns_500(test_client_with_goals: TestClient) -> None:
    proc = MagicMock()
    proc.communicate = AsyncMock(return_value=(b"", b"boom"))
    proc.returncode = 1
    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=proc)):
        response = test_client_with_goals.delete(
            "/api/goals/Test%20Goal/session?vault=TestVault"
        )
    assert response.status_code == 500
```

**4i. Regression: `/api/goals` surfaces `claude_session_id`** (AC#6) — seed a goal with a session id, assert the value round-trips in the payload:
```python
def test_list_goals_surfaces_claude_session_id(
    test_client_with_goals: TestClient, mock_vault_client_with_goals: MagicMock
) -> None:
    """/api/goals continues to include each goal's claude_session_id (regression guard)."""
    mock_vault_client_with_goals._goals.clear()
    mock_vault_client_with_goals._goals.append(
        _make_goal(goal_id="Sessioned Goal", claude_session_id="sess-123")
    )
    response = test_client_with_goals.get("/api/goals?vault=TestVault")
    assert response.status_code == 200
    goal = response.json()[0]
    assert goal["claude_session_id"] == "sess-123"
```

`pytest`, `MagicMock`, `AsyncMock`, `patch`, and `TestClient` are already imported at the top of `tests/test_api.py` — confirm before adding; do not duplicate imports.

Do NOT add a cleanup test — `tests/test_cleanup.py::test_goal_uuid_cleared_on_missing_file` already serves the spec AC#7 regression guard and must keep passing unchanged.
</requirements>

<constraints>
- vault-cli is invoked with separate arguments (no shell). Goal ids beginning with `-` are rejected before any subprocess (arg-injection guard), matching `update_goal_status` / `execute_goal_command` / `assign_goal_to_me`.
- Do NOT modify `start_vault_cli_session`, `run_task`, `clear_task_session`, or any task endpoint — task-card session behavior is unchanged (spec Non-goal). Add the goal function alongside, never by changing the task function's signature.
- Do NOT add a durable `claude_session_started` "Starting…" flag for goals (spec Non-goal).
- Do NOT add or modify cleanup logic — goal stale-session sweep already ships in `cleanup.py`; this prompt only guards it against regression via the existing test.
- Do NOT modify `vault_cli_client.py` goal methods, `cleanup.py`, `models.py` (`GoalResponse` already carries `claude_session_id`), or `factory.py`.
- Do NOT touch `app.js`, `index.html`, CSS, the CHANGELOG, or the `?v=` cache-bust token — those belong to prompt 2.
- The clear endpoint MUST carry a bounded 10s timeout that returns HTTP 504 and kills the process on hang (spec Failure Mode row 4 + Security section).
- Every diagnostic `RuntimeError` in `start_vault_cli_goal_session` MUST name the goal id and the vault (spec Failure Mode rows 2/3; AC#3 asserts the detail names both).
- Tests MUST mock the vault-cli subprocess (`asyncio.create_subprocess_exec`) and the client — no real vault-cli, Claude, network, or filesystem calls (project DoD).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass with ≥80% coverage on the changed behavior.

Fast-loop checks:
```bash
uv run pytest tests/test_api.py -k "run_goal or clear_goal_session or list_goals_surfaces" -v
uv run pytest tests/test_cleanup.py -k "goal_uuid_cleared_on_missing_file" -v
uv run pytest tests/test_api.py -k "run_task or clear_task_session" -v   # task path unchanged
```

Confirm no task-session regression:
```bash
git diff -- src/vault_ui/api/tasks.py | grep -E "^[+-].*(def run_task|def start_vault_cli_session|def clear_task_session)" | grep -v '^[+-]\{3\}'
# Expected: empty (those functions are untouched)
```
</verification>
