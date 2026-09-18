---
status: completed
summary: vault-ui now degrades with a warning instead of HTTP 500 when a configured vault was renamed in vault-cli, and re-reads the vault set every 30 seconds so the rename is picked up without a restart.
execution_id: vault-ui-stale-vault-name-exec-096-stale-vault-name-degrade-and-reload
dark-factory-version: dev
created: "2026-09-18T11:38:02Z"
queued: "2026-09-18T11:38:02Z"
started: "2026-09-18T12:36:56Z"
completed: "2026-09-18T12:42:16Z"
---

# Degrade on a stale vault name and reload the vault list periodically

<summary>
- Renaming a vault in vault-cli stops being a hard outage of the board
- A vault that vault-cli no longer knows is skipped with a warning, and the remaining vaults still load
- A genuine vault-cli failure still fails loudly instead of being silently swallowed
- The vault list is re-read on a timer, so a rename is picked up without restarting vault-ui
- The watcher is only restarted when the vault set actually changed, so live updates keep flowing
- A reload that cannot read the config leaves the running server exactly as it was
- The board renders the vaults that still exist instead of a blank page
- The assignee dropdown and the goals view survive a stale vault too, not just the task board
- The manual ↻ Refresh button keeps working exactly as it does today
</summary>

<objective>
Make vault-ui survive a vault rename in vault-cli while it is running: it must pick up the new vault set within 60 seconds without a restart, and it must degrade with a warning instead of returning HTTP 500 when a configured vault no longer exists. Today a rename is a hard outage — every board request 500s until a human restarts the server, and the failure is invisible until someone opens the board.
</objective>

<context>
Project conventions: Python 3.12+ managed by `uv`, FastAPI, `ruff` for format/lint, `mypy` for type checking, `pytest` for tests. `make precommit` runs format + test + check. Tests must not make real subprocess, network, or Claude API calls — mock external dependencies. Read `docs/dod.md` for the project's Definition of Done.

Reproduction, verified 2026-09-18 against a *copy* of vault-cli's config (no live config was modified — vault-cli was invoked through a wrapper passing `--config <copy>`, and vault-ui ran under an isolated `HOME`):

- vault-ui started with vault `brogrammers` configured: `GET /api/tasks` → **200**, 279 tasks.
- The vault's key and `name:` field were renamed in the vault-cli config copy while vault-ui kept running. vault-cli now answers `task list --vault brogrammers` with rc=1 and stderr `Error: get vaults: vault not found: brogrammers`.
- After the task-list cache TTL expired (30s), `GET /api/tasks` → **500** `Internal Server Error`, and the server log carried exactly the production failure:

  ```
  RuntimeError: vault-cli task list failed: Error: get vaults: vault not found: brogrammers
  Error: get vaults: vault not found: brogrammers
  ```

- The traceback ends at `raise result  # RuntimeError from vault-cli -> propagates -> HTTP 500` in `list_tasks`.
- `POST /api/config/reload` in that same state also returned **500**, with `{"detail":"No vaults configured after merging with vault-cli output. ..."}` — the one configured key was skipped by the load-time guard, leaving zero vaults, which trips the `if not vaults` check in `load_config`.
- After the vault-ui config key was renamed to match, `POST /api/config/reload` → **200** `{"vaults":["brogrammers_renamed"],"watchers":["brogrammers_renamed"]}`, and `GET /api/tasks` → **200** with all 279 tasks now under the new vault name.

The last two observations are the design constraint: **`reload_config()` already does everything needed** — it re-reads the config and `vault-cli config list`, swaps the vault set, and restarts the watcher. What is missing is only a periodic trigger; today it fires only when a human presses ↻ or POSTs `/api/config/reload`.

Frontend note: `src/vault_ui/static/app.js` `loadTasks` throws on a non-ok response and never reaches `renderTasks()`, so a 500 leaves the board blank. Returning 200 from the endpoint is what makes the board render — no frontend change is required.

Files to read before writing:
- `src/vault_ui/config.py` — `load_config` (the load-time warn-and-skip guard, and the `if not vaults` raise), `discover_vaults_from_cli`
- `src/vault_ui/factory.py` — `reload_config`, `start_task_watchers`, `lifespan`
- `src/vault_ui/cleanup.py` — `run_cleanup_loop`, the existing periodic-loop shape to model on
- `src/vault_ui/api/tasks.py` — `list_tasks`, `list_goals` and `list_assignees`, the three per-vault fan-out paths that turn a per-vault `RuntimeError` into HTTP 500
- `src/vault_ui/vault_cli_client.py` — `list_tasks` and `list_goals`, where the subprocess failure is raised
- `tests/test_config.py` — existing `reload_config` tests, for style
- `tests/test_api.py` — existing endpoint tests, for style
- `tests/test_task_reader.py` — the only place `VaultCLIClient` is driven against a faked subprocess; read it before tightening its failure test
</context>

<requirements>
1. **Add a distinguishable error type for a stale vault name.**
   In `src/vault_ui/vault_cli_client.py`, define `class VaultNotFoundError(RuntimeError)`.
   Raise it instead of a plain `RuntimeError` from `VaultCLIClient.list_tasks` and `VaultCLIClient.list_goals` when vault-cli reports the vault is unknown.
   vault-cli's marker is `vault not found` (observed stderr: `Error: get vaults: vault not found: <name>`). Match on that marker only — every other non-zero exit (timeout, disk error, non-JSON output) keeps raising plain `RuntimeError`.
   Keep the message prefix unchanged (`vault-cli task list failed: <stderr>` / `vault-cli goal list failed: <stderr>`), so the existing `tests/test_task_reader.py::test_list_tasks_failure_raises` assertion still matches.

2. **Degrade the runtime path instead of returning 500.**
   In `src/vault_ui/api/tasks.py`, both `list_tasks` and `list_goals` inspect the `asyncio.gather(..., return_exceptions=True)` results and currently do `if isinstance(result, RuntimeError): raise result`, which becomes HTTP 500.
   Change both to skip a stale vault: check `isinstance(result, VaultNotFoundError)` **first** (it is a subclass of `RuntimeError`) and `continue`, mirroring the existing `isinstance(result, ValueError): continue` branch's skip semantics.
   Keep `isinstance(result, RuntimeError): raise result` after it, so a genuine vault-cli failure still surfaces as HTTP 500.
   Log a `logger.warning` naming the vault that was skipped, so the degraded state is diagnosable from the log alone. Match the wording style of the existing load-time guard in `config.py` (`logger.warning("Vault '%s' not found in vault-cli output, skipping", vault_key)`).
   Do not let one stale vault suppress the other vaults' tasks: the endpoint must return 200 with the surviving vaults' tasks.
   A **third** fan-out in the same module carries the identical defect and must be fixed in the same step: `list_assignees` gathers `_fetch_assignees_for_vault` at ~line 517 with **no** `return_exceptions=True` and no exception guard, and `_fetch_assignees_for_vault` calls `client.list_tasks(show_all=True)`. A stale vault therefore raises `VaultNotFoundError` straight out of that endpoint. Add `return_exceptions=True` to that gather and skip `VaultNotFoundError` results the same way, keeping the existing `except ValueError` unknown-vault skip inside `_fetch_assignees_for_vault`, so `/api/assignees` returns the surviving vaults' assignees instead of 500.
   Order matters there: with `return_exceptions=True` the exception object lands in `results`, where the existing loop does `for r_named, r_unassigned in results:` — an exception instance is not iterable, so the skip must come **before** the unpacking or the endpoint raises `TypeError` instead of degrading.
   Add `VaultNotFoundError` to the existing `from vault_ui.vault_cli_client import VaultCLIClient` import at the top of `src/vault_ui/api/tasks.py`.

3. **Re-read the vault list on a timer.**
   In `src/vault_ui/factory.py`, add a module-level `_CONFIG_RELOAD_INTERVAL_SECONDS = 30` (the requirement is ≤60) and an async `run_config_reload_loop(vault_task_cache, vault_goal_cache)`, modelled on `run_cleanup_loop` in `src/vault_ui/cleanup.py` (`while True:` → work → `await asyncio.sleep(interval)`).
   Each tick must:
   - Call `load_config()` off the event loop — `await asyncio.to_thread(load_config)` — inside a `try/except Exception`; on failure log a `logger.warning` and continue to the next tick. `load_config()` is synchronous and makes two blocking `subprocess.run(..., timeout=10)` calls (`discover_current_user`, `discover_vaults_from_cli`), so calling it inline would stall every in-flight request for up to ~20s on every tick. The loop must never die. This matters: with every configured key stale, `load_config()` raises `RuntimeError("No vaults configured after merging with vault-cli output")` — an unguarded loop would die on the first such tick and never recover, exactly when recovery matters most.
     (There is no existing `asyncio.to_thread` precedent in `src/` — the manual `/api/config/reload` endpoint calls `load_config()` inline. Introduce it here; a recurring stall is a different problem from a one-off manual click.)
   - Call `reload_config(...)` **only when the vault set actually changed** — compare the freshly loaded vault-name list against the currently running `get_config().vaults` names, and return without touching anything when they are equal. `reload_config` unconditionally stops and restarts the `vault-cli watch` subprocess; doing that every tick would churn the watcher and drop live-update events.
     Note this reads the config twice per *changed* tick — once for the comparison, once inside `reload_config` — which is accepted here: a changed vault set is rare, and keeping `reload_config`'s signature untouched keeps the manual ↻ Refresh path identical. Say so in a comment rather than threading a pre-loaded config through.
   Start the loop in `lifespan` as a module-level task (a distinct global — `reload_config` already cancels and recreates `_cleanup_task`, so do not reuse that name), and cancel it in the `finally` block alongside `_cleanup_task`.

4. **Keep the manual refresh path working.** `POST /api/config/reload` and its response shape (`{"vaults": [...], "watchers": [...]}`) stay as they are.

5. **Tests.**
   - `tests/test_api.py`: one vault raising `VaultNotFoundError` while another returns tasks → the endpoint responds 200 and the body contains only the healthy vault's tasks. This test must fail against the current behaviour (which returns 500).
   - `tests/test_api.py`: a plain `RuntimeError` from a vault still produces HTTP 500 — the degrade path must not swallow real failures.
   - `tests/test_api.py`: the `/api/goals` equivalent of the first bullet — `list_goals` is modified the same way and is otherwise untested — plus an assertion that the `logger.warning` from requirement 2 names the skipped vault.
   - `tests/test_api.py`: `/api/assignees` returns the surviving vaults' assignees (200) when one vault raises `VaultNotFoundError`.
   - `tests/test_task_reader.py` — the only place `VaultCLIClient` is driven against a faked subprocess, and therefore the only test that can prove requirement 1's marker matching actually works: stderr containing `vault not found` → `VaultNotFoundError`; a different non-zero stderr (e.g. `b"timeout"`) → `RuntimeError` and **not** `VaultNotFoundError`. The existing `test_list_tasks_failure_raises` already feeds `b"vault not found"` but asserts only `RuntimeError`, so it cannot tell the subclass apart — tighten it or add a sibling. Without this, the marker match is dead code in the suite and a wrong marker string would still ship green.
   - `tests/test_config.py` (which already covers `reload_config`): the reload loop calls `reload_config` when the vault set changed, does not call it when the set is unchanged, and survives a `load_config()` that raises without the loop task exiting.
   - Follow the existing pytest style (`tmp_path`, `unittest.mock.patch`, `AsyncMock`); no real subprocess, network, or Claude API calls.

6. **Satisfy the repo's Definition of Done obligations.** `docs/dod.md` is the configured validation prompt and demands both of the following; report any unmet criterion as a blocker.
   - **CHANGELOG:** `CHANGELOG.md` currently has no `## Unreleased` section — its top section is `## v0.67.4`. Add a `## Unreleased` section above it with a single `- fix:` bullet describing this change, in the style of the existing entries (one paragraph, plain language, stating what the user-visible behaviour becomes). This is the section the release watcher renames when it cuts a version — every released section in this file was written from it — and `docs/dod.md` (the configured validation prompt) requires it.
   - **Docs:** `docs/launchd-service.md` presents `↻ Refresh` as the way a `config.yaml` vault change is picked up — see the "Restart" note (~line 84-86, "a `config.yaml` vault change is picked up by `↻ Refresh` on the board instead") and the Troubleshooting entry "Changed `config.yaml` but vaults didn't update" (~line 183-190), which describes the reload and calls a restart the fallback. After this change the vault set is re-read automatically every 30 seconds, so update both places: `↻ Refresh` remains the manual trigger, but it is no longer the only one.

7. **Self-check before finishing.** Re-run `make precommit` and confirm it passes. Then walk each numbered requirement above against the change and confirm the two negative cases hold: a genuine vault-cli failure still yields 500, and an unchanged vault set does not restart the watcher.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- No new dependencies
- Repo-relative paths only — no absolute or home-relative paths
- Out of scope: making the config.yaml `vaults:` block optional; repairing a rename that was already applied; any other vault-ui failure mode surfaced by a vault-cli change (task schema, status values)
</constraints>

<verification>
Run everything from the repo root.

**1. The full gate:**

```
make precommit
```

Must exit 0 (sync + format + test + check).

**2. The DoD deliverables from requirement 6 exist.** `make precommit` does not read either file, so a missing section or an un-updated doc would still show green above — check them directly:

```
grep -n '^## ' CHANGELOG.md | head -1
awk '/^## Unreleased/{f=1;next} /^## /{f=0} f' CHANGELOG.md | grep -c .
grep -n 'every 30 seconds\|automatically' docs/launchd-service.md
```

The first must print `## Unreleased`. The second must print `>= 1` — the count of non-empty lines in the new section. The third must print at least one line, in each of the two places requirement 6 names.

**3. The negative cases hold.** Read `src/vault_ui/api/tasks.py` and confirm by inspection that in all three fan-outs the `VaultNotFoundError` skip precedes the `RuntimeError` re-raise, and that in `list_assignees` it also precedes the `for r_named, r_unassigned in results:` unpacking. Confirm `VaultCLIClient` still raises a plain `RuntimeError` — not `VaultNotFoundError` — when the non-zero exit is not a `vault not found` marker.

**4. Self-check.** Before you finish, re-run `make precommit` and confirm it passes; then walk each of requirements 1-6 against the change and state in your final message which requirement each verification step covers and which test pins it.
</verification>
