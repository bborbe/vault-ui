---
status: committing
summary: Downgraded missing Goals/ directory exception to debug log in cleanup.py; added test covering the new branch; updated CHANGELOG.md.
container: task-orchestrator-038-silence-missing-goals-dir
dark-factory-version: v0.156.1-1-g04f3863-dirty
created: "2026-05-08T12:30:00Z"
queued: "2026-05-08T12:41:25Z"
started: "2026-05-08T12:41:26Z"
---

<summary>
- The cleanup loop currently logs full stack traces every 300s for every vault that lacks a `Goals/` directory
- A missing `Goals/` directory is a normal config state (not every vault manages goals) — it should not surface as an exception
- This change downgrades that specific failure mode to a debug log, with no traceback
- Other `vault-cli goal list` failures (real errors: vault-cli broken, version mismatch, permission denied) keep their existing error-level logging with traceback
- Net effect: clean cleanup logs on machines where some vaults don't manage goals
</summary>

<objective>
Suppress noisy traceback logging when `vault-cli goal list` fails specifically because the configured `Goals/` directory does not exist. Keep the existing error-level logging for all other failure modes. No behavioral change to actual cleanup work.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files in full before making changes:
- `src/task_orchestrator/cleanup.py` — full file. The relevant block is the `except Exception as e:` that logs `"[Cleanup] Exception processing goals for vault %s: %s"` (added by spec 004 prompt 2). The error currently uses `logger.error(..., exc_info=True)` which prints the full traceback.
- `src/task_orchestrator/vault_cli_client.py` — `list_goals` method raises `RuntimeError(f"vault-cli goal list failed: {stderr.decode().strip()}")`. The stderr text contains the pattern `no such file or directory` when the goals directory is missing.
- `tests/test_cleanup.py` — existing goal cleanup tests, especially `test_goal_list_failure_does_not_abort_task_pass`. Add the new "missing directory" test alongside it.

The actual error message vault-cli emits (observed at runtime):
```
vault-cli goal list failed: Error: list pages: read directory /some/vault/Goals: open /some/vault/Goals: no such file or directory
```

The detection pattern: case-insensitive substring `no such file or directory` in the exception message.
</context>

<requirements>
### 1. Update the goal-cleanup `except` block in `src/task_orchestrator/cleanup.py`

Find the `except Exception as e:` that logs `"[Cleanup] Exception processing goals for vault %s: %s"` (this is the goal-cleanup wrapper added by spec 004, located inside the per-vault `try:` block, after the goal `for` loop).

Replace it with a branching except that detects the "missing directory" pattern:

```python
        except Exception as e:
            error_text = str(e).lower()
            if "no such file or directory" in error_text:
                logger.debug(
                    "[Cleanup] Skipping goals for vault %s: Goals directory not configured",
                    vault.name,
                )
            else:
                logger.error(
                    "[Cleanup] Exception processing goals for vault %s: %s",
                    vault.name,
                    e,
                    exc_info=True,
                )
```

Do NOT change any other except block. Do NOT change the task-side cleanup behavior.

### 2. Add a test in `tests/test_cleanup.py`

After the existing `test_goal_list_failure_does_not_abort_task_pass`, add:

```python
@pytest.mark.asyncio
async def test_goal_list_missing_directory_logs_debug_not_error(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A missing Goals directory is logged at DEBUG level (no traceback), not ERROR."""
    config = _make_config(current_user="alice")

    mock_client = AsyncMock()
    mock_client.list_tasks = AsyncMock(return_value=[])
    mock_client.list_goals = AsyncMock(
        side_effect=RuntimeError(
            "vault-cli goal list failed: Error: list pages: read directory "
            "/some/vault/Goals: open /some/vault/Goals: no such file or directory"
        )
    )

    with patch(
        "task_orchestrator.cleanup.VaultCLIClient", return_value=mock_client
    ):
        with caplog.at_level(logging.DEBUG, logger="task_orchestrator.cleanup"):
            cleared = await cleanup_stale_sessions(config)

    assert cleared == 0
    error_records = [r for r in caplog.records if r.levelname == "ERROR"]
    debug_records = [r for r in caplog.records if r.levelname == "DEBUG"]
    assert not any("Exception processing goals" in r.message for r in error_records), (
        "Missing-directory should not log at ERROR"
    )
    assert any(
        "Goals directory not configured" in r.message for r in debug_records
    ), "Missing-directory should log at DEBUG"
```

Confirm `import logging` exists at the top of the test file; if not, add it.

### 3. Verify other failure modes still log at ERROR

Re-read `test_goal_list_failure_does_not_abort_task_pass` — its `side_effect` is `RuntimeError("vault-cli goal list failed: unknown subcommand")`. Confirm this test still asserts the task pass continues. The new branch in step 1 will route this to the ERROR path (no "no such file or directory" in the text), preserving existing behavior. Do NOT change this test.

### 4. CHANGELOG entry

Prepend under `## Unreleased` in `CHANGELOG.md` (create the section if absent):

```markdown
## Unreleased

- fix: Suppress noisy traceback when a vault has no `Goals/` directory; downgraded to a debug log per cleanup cycle. Other `vault-cli goal list` failures still log at error level with traceback.
```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT change task-side cleanup behavior
- Do NOT change `vault_cli_client.list_goals` — the fix is in `cleanup.py` only
- Detection is by case-insensitive substring match on the exception message — do NOT introduce a new exception type
- The existing `test_goal_list_failure_does_not_abort_task_pass` must pass unchanged
- Existing task tests must still pass without modification
</constraints>

<verification>
Run `make precommit` — must pass.

Run the cleanup tests specifically:
```
python -m pytest tests/test_cleanup.py -v
```

Confirm:
- `test_goal_list_missing_directory_logs_debug_not_error` passes
- `test_goal_list_failure_does_not_abort_task_pass` still passes (unchanged)
- All previously-green tests remain green
</verification>
