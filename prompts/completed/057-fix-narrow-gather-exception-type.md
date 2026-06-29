---
status: completed
spec: [010-parallelize-vault-task-fanout]
summary: 'Narrowed asyncio.gather result re-raise from BaseException to RuntimeError in list_tasks, added assert isinstance(result, list) to satisfy mypy type narrowing, and appended CHANGELOG entry under ## Unreleased.'
container: vault-ui-parallel-vaults-exec-057-fix-narrow-gather-exception-type
dark-factory-version: v0.182.0
created: "2026-06-20T15:40:57Z"
queued: "2026-06-20T15:40:57Z"
started: "2026-06-20T15:40:59Z"
completed: "2026-06-20T15:42:15Z"
---
<summary>
- Narrows the `isinstance(result, BaseException)` re-raise check in `list_tasks` to `isinstance(result, RuntimeError)` so KeyboardInterrupt / SystemExit / CancelledError are not accidentally re-raised through the HTTP path.
- Addresses pr-reviewer NIT on PR #6 (`src/vault_ui/api/tasks.py:394`).
- Behavior change is intentional but minimal: only `RuntimeError` is expected from `_process_vault`; the previous broader catch was over-permissive.
</summary>

<objective>
In `src/vault_ui/api/tasks.py` inside `list_tasks`, change the `gather`-result loop's exception re-raise check from `isinstance(result, BaseException)` to `isinstance(result, RuntimeError)`. Update or add a test that asserts only RuntimeError still propagates as HTTP 500. Make `make precommit` exit 0.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `definition-of-done.md` — coverage and completion rules.

Read the full file before editing:
- `src/vault_ui/api/tasks.py` — focus on the `list_tasks` route handler's `asyncio.gather(..., return_exceptions=True)` block (~line 390-400). The current code path is roughly:
  ```python
  for result in results:
      if isinstance(result, ValueError):
          continue  # unknown vault, skip
      if isinstance(result, BaseException):
          raise result  # propagate; HTTP 500
      all_tasks.extend(result)
  ```
  The change narrows the second branch to `RuntimeError`.

- `tests/test_api.py` — has `test_list_tasks_concurrent_runtime_error_returns_500` (added by prompt 053) that asserts HTTP 500 on RuntimeError. That test must continue to pass.
</context>

<requirements>

### 1. Narrow the re-raise check

In `src/vault_ui/api/tasks.py`, in `list_tasks`, change:

```python
if isinstance(result, BaseException):
    raise result
```

to:

```python
if isinstance(result, RuntimeError):
    raise result
```

The behavior: only `RuntimeError` from `_process_vault` triggers HTTP 500; `ValueError` is already skipped above; any other exception type from `_process_vault` (none expected) is treated as "result-like" and would crash the `all_tasks.extend(result)` line — which is the desired failure mode (loud, not silently swallowed).

If there is a defensive case for other exception types in this codebase (e.g. `vault_cli_client.py` only raises RuntimeError or ValueError — confirm by reading it), no other branch is needed. If the agent finds another exception type explicitly raised by `client.list_tasks` or by `get_vault_cli_client_for_vault` that the current `BaseException` catch was implicitly handling, add a focused `isinstance(result, <that-type>)` branch — but do NOT keep `BaseException` as a fallback.

### 2. Keep the existing RuntimeError → 500 test green

`test_list_tasks_concurrent_runtime_error_returns_500` must continue to pass. No edits needed to that test; it asserts the response status, which is still 500 under the narrower check.

### 3. CHANGELOG entry

Append under `## Unreleased`:

```
- fix: Narrow asyncio.gather result re-raise from BaseException to RuntimeError so KeyboardInterrupt / SystemExit / CancelledError do not accidentally surface through GET /api/tasks
```

### 4. Verify

```bash
make precommit
```

Specifically:

```bash
uv run python -m pytest tests/test_api.py -v -k "concurrent_runtime_error_returns_500 or concurrent_skips_value_error_vault or concurrent_overlap"
```

All must pass.
</requirements>

<constraints>
- Response body for `GET /api/tasks` MUST remain byte-identical for all currently-tested inputs. The only behavior change is the exception type re-raised on `RuntimeError`-only (which already maps to HTTP 500).
- All existing tests must continue to pass with no behavioral assertion weakened.
- No new third-party dependency.
- No new query parameter or HTTP status change.
- `make precommit` must exit 0 with zero new ruff or mypy findings.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Confirm the narrow check is in place:
```bash
grep -n "isinstance(result, RuntimeError)" src/vault_ui/api/tasks.py
```
Expected: one match.

Confirm `BaseException` no longer appears in this loop:
```bash
grep -n "isinstance(result, BaseException)" src/vault_ui/api/tasks.py
```
Expected: no matches.

Run the existing RuntimeError-500 test:
```bash
uv run python -m pytest tests/test_api.py -v -k "concurrent_runtime_error_returns_500"
```
Expected: pass.

Full suite:
```bash
make precommit
```
Expected: exit 0.
</verification>
