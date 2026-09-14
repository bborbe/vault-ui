---
status: completed
summary: Collapsed vault-ui's per-vault watcher fan-out into a single vault-cli watch subprocess covering every configured vault, with per-event VaultConfig resolution, updated tests, docs and changelog
execution_id: vault-ui-watch-single-exec-093-single-watcher-for-all-vaults
dark-factory-version: dev
created: "2026-09-14T21:32:33Z"
queued: "2026-09-14T21:32:33Z"
started: "2026-09-14T21:32:57Z"
completed: "2026-09-14T21:35:29Z"
---

# watch all vaults in one subprocess

<summary>
- Vault UI runs one `vault-cli watch` subprocess per configured vault; it now runs exactly one, covering every vault.
- The process count no longer scales with the number of vaults the board displays.
- Every event already names its vault, so the UI still tells them apart.
- Task and goal changes in any configured vault still invalidate the right per-vault cache and reach the right browser clients.
- Session resolution still runs against the vault the event came from, not the first vault in the list.
- An event for a vault the UI does not display is ignored cleanly instead of raising.
- Reloading the config still reconciles watchers, and the diagnostic surface still reports which vaults are watched.
- No behaviour change visible on the board.

</summary>

<objective>
Collapse vault-ui's per-vault watcher fan-out into a single `vault-cli watch` subprocess so the number of watcher processes stops scaling with the number of displayed vaults — the engine and the CLI both already support it, and only vault-ui still spawns one process per vault.
</objective>

<context>
This repo has no `CLAUDE.md`. Project conventions live in the existing code, in `docs/dod.md` (the configured `validationPrompt`), and in the test suite.

`vault-cli watch` accepts a comma-separated vault list as of v0.132.0 (`--vault a,b,c`), running one process that watches exactly those vaults and emitting one newline-delimited JSON stream where each event carries its own `vault`. Vault UI was written before that existed and still starts one subprocess per vault.

Read these files before writing anything:

- `src/vault_ui/vault_cli_watcher.py` — the whole file. `VaultCLIWatcher.__init__` takes `vault_name: str` and `on_change: Callable[[str, str, str, str], None]`; `_run_subprocess` spawns `vault-cli watch --vault <name> --types task,goal,theme,objective`; `_handle_line` parses the JSON event and calls `on_change(event_type, item_id, vault, item_kind)` with `vault = event.get("vault", self._vault_name)`. Keep the restart loop, the SIGTERM/SIGKILL shutdown, and the event dispatch shape.
- `src/vault_ui/factory.py` — `start_task_watchers` (loops `for vault in config.vaults`, builds one watcher per vault via `make_callback(vault)`, stores them in the module-global `_watchers` dict keyed by vault name), `stop_task_watchers`, `watcher_vault_names` (returns `sorted(_watchers)`), `reload_config` (calls `stop_task_watchers()` then `start_task_watchers(...)`), and the module globals `_watchers: dict[str, VaultCLIWatcher]` and `_watcher_tasks: list[asyncio.Task[None]]`.
- `src/vault_ui/factory.py` `make_callback` — the closure this change is really about. It closes over a `VaultConfig` and uses `vault_cfg.vault_path`, `vault_cfg.session_project_dir` (via `derive_claude_project_dir`) and `vault_cfg.vault_cli_path` to dispatch `_try_resolve_task_session` / `_try_resolve_goal_session`. Read `_try_resolve_task_session` and `_try_resolve_goal_session` too.
- `src/vault_ui/config.py` — `VaultConfig` (`name`, `vault_path`, `tasks_folder`, `vault_name`, `claude_script`, `vault_cli_path`, `session_project_dir`) and `Config.vaults`.
- `tests/test_vault_cli_watcher.py` — the existing pytest-asyncio suite for this class (event parsing, default vault name, empty kind, terminate/stop semantics). These tests must keep passing, adapted only where the constructor signature changes.
- `tests/conftest.py` — existing fixtures.
- `docs/dod.md` — this repo's `validationPrompt`.

Read these coding-plugin docs (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-project-structure.md` — module layout and typing conventions.
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-factory-pattern.md` — `factory.py` is the composition root; the "zero business logic in factories" rule is why the per-event resolution belongs in a small module-level helper rather than inline in `start_task_watchers`.

<requirements>

## 1. Let `VaultCLIWatcher` watch several vaults

Change the constructor to take the vault names it should cover — a `list[str]` in place of the single `vault_name: str`. Keep the parameter order and the `on_change` callback signature unchanged.

`_run_subprocess` must pass the whole list as one comma-joined `--vault` value, keeping `--types task,goal,theme,objective`. With N names it must spawn exactly ONE process, never N.

`_handle_line` keeps reading `vault` from the event. The fallback it uses when the key is absent must stay meaningful for a multi-vault watcher — use the first name in the list, and keep the existing "default vault name when missing" test passing.

The restart loop, the `_stopped` flag, `terminate()`, and `stop()` are unchanged in behaviour.

## 2. One watcher in `start_task_watchers`

Build a single `VaultCLIWatcher` covering every vault name in `config.vaults`, and schedule one `start()` task. Emit exactly one startup log line for the whole set — `logger.info("[Factory] Started vault-cli watcher for vaults: %s", ", ".join(names))` — so the "Verify" line in `docs/launchd-service.md` describes the real output rather than a per-vault line that no longer exists. Replace the `_watchers` dict with a single module-global reference to that watcher (or `None`), and update `stop_task_watchers` and `reload_config` to match. Keep the module-global shape — this file is the composition root and the reload path depends on it.

`watcher_vault_names()` must keep returning the sorted vault names that are covered, sourced from the configured vaults rather than from a dict's keys. `reload_config` logs this, so it must not become empty.

## 3. Resolve the vault per event, not per watcher

This is the load-bearing part. The callback can no longer close over one `VaultConfig` — it receives events for every vault. Resolve the `VaultConfig` from the event's `vault` field against `config.vaults` on each event, and use that vault's `vault_path`, `session_project_dir`, `vault_cli_path` and `name` exactly as the old closure did.

`name` matters and is easy to get wrong: `VaultConfig` carries both `name` and a display-only `vault_name`, and the old closure passes `vault_cfg.name` (never `vault_name`) into both resolvers, because `VaultCLIClient` forwards it as `--vault` on every `vault-cli` call. Passing `vault_name` would break session resolution for every vault.

Prefer the existing `Config.get_vault(name)` helper over a hand-rolled loop over `config.vaults`.

Cache-invalidation and broadcast behaviour must not change: the status cache is invalidated for the event's vault, the per-vault task cache is popped for `task` events, the per-vault goal cache for `goal` events, and the WebSocket message keeps its `type` / `task_id` / `vault` / `item_kind` fields.

An event whose `vault` is not in `config.vaults` must be handled cleanly — log at debug level and return — never raise, and never invalidate or broadcast anything. A traceback here would kill the watcher's read loop for every vault at once, which is strictly worse than the old per-vault isolation; that asymmetry is the main risk this change introduces.

## 4. Tests

Extend `tests/test_vault_cli_watcher.py`:

- The argv contract: with three vault names, exactly one subprocess is spawned and its argument list contains one `--vault` whose value is the three names comma-joined in order.
- A single name still produces a single `--vault` with that name.
- An event carrying `vault` resolves to the matching `VaultConfig` — assert the resolver receives the event's own vault, using at least two different vault names across two events so a first-vault-wins implementation fails.
- An event for an unknown vault does not raise, and neither the cache nor the broadcaster is touched.
- The existing event-parsing, default-name, empty-kind, terminate and stop tests still pass.

Make the resolver a module-level function in `src/vault_ui/factory.py` — `def resolve_vault_for_event(config: Config, vault_name: str) -> VaultConfig | None:` — so it is importable and directly testable; a nested closure cannot be tested directly. Cover it directly as well as through the watcher — the boundary here is the mapping from an event's `vault` string to a `VaultConfig`, and a test that only exercises the happy path through the watcher would not catch a lookup that silently falls back to the first vault.

Also add a factory-level test that pins the central claim, which inspection alone does not protect against a regression: `start_task_watchers` builds exactly **one** `VaultCLIWatcher` for a multi-vault config and hands it every configured name. Patch `vault_ui.factory.VaultCLIWatcher` with a mock whose `start` is an `AsyncMock` (otherwise `loop.create_task` raises `TypeError` into the surrounding `except`), patch `vault_ui.factory._config` / `get_connection_manager` / `get_status_cache`, then assert one construction carrying all the names and one scheduled task.

Assert the **full** argument list of the spawned process — the binary path, `watch`, `--vault <comma-joined names>`, and `--types task,goal,theme,objective`. No existing test asserts the argv at all, so a dropped `--types` or a lost `watch` subcommand would ship uncaught.

Match vault names exactly against `config.vaults`. `vault-cli` canonicalises names to lowercase, while `VaultConfig.name` is the `config.yaml` key verbatim; today's config keys are all lowercase so exact matching works, but a mixed-case key would send every event for that vault into the unknown-vault path and silently stop its live updates. State the exact-match rule in the resolver's docstring so the constraint is visible rather than incidental.

## 5. Update the docs the change invalidates

`docs/launchd-service.md` describes the fan-out in two places, both of which become wrong:

- the startup-log line in its "Verify" section ("one `Started vault-cli watcher for vault: <name>` line per configured vault")
- the ↻ Refresh paragraph ("reconciles the per-vault watchers")

Three further places describe the same fan-out and become wrong for the same reason: the ↻ Refresh comment above `refreshBoard` in `src/vault_ui/static/app.js`, the `reload_config` docstring in `src/vault_ui/api/tasks.py` ("per-vault watchers are restarted over the new vault set"), and the `_build_callback` docstring in `tests/test_websocket_routing.py` ("built per-vault").

Update all five to describe the single watcher. Then add a `CHANGELOG.md` entry under `## Unreleased` — the section does not exist yet (the top section is `## v0.67.2`), so create it above that heading, as `docs/dod.md` requires.

</requirements>

<constraints>
- One subprocess per vault-ui instance, regardless of how many vaults are configured. That is the entire point — a build that still spawns one per vault fails the requirement even if every test passes.
- The event JSON schema is unchanged, and `vault-cli` is invoked exactly as before except for the comma-joined `--vault` value.
- No new config field, no new flag, no environment variable, and no change to `config.yaml`'s shape.
- The WebSocket message shape is frozen: `type`, `task_id`, `vault`, `item_kind`.
- Session resolution, cache invalidation and the broadcast path must behave exactly as they do today for every vault.
- Python 3.12+, type annotations on new and changed functions, `ruff` clean, `mypy` clean.
- Tests use the existing pytest-asyncio style; no real subprocesses, network or Claude API calls in tests.
- Do NOT commit — dark-factory handles git. This container's `.git` is masked: make no git calls at all, including in `<verification>`.
- Do NOT run `docker`, `kubectl`, `make run`, or any `dark-factory` command.
</constraints>

<verification>
Run everything from the repo root.

**1. The full gate:**

```
make precommit
```

Must exit 0 (sync + format + test + check).

**2. Exactly one watcher is constructed and it gets the whole list.** Read `src/vault_ui/factory.py` and confirm by inspection that `start_task_watchers` builds one `VaultCLIWatcher` and that no per-vault loop around watcher construction remains:

```
sed -n '/^def start_task_watchers/,/^def stop_task_watchers/p' src/vault_ui/factory.py
grep -n 'def make_callback' src/vault_ui/factory.py
```

Dump the whole function rather than a fixed context window: `grep -B 4` around the construction site stops four lines above it and never reaches the `for vault in config.vaults:` header, so the window alone cannot show whether a per-vault loop still encloses it. The dump must contain exactly one `VaultCLIWatcher(` line, outside any loop over vaults, receiving the whole vault-name list.

Note that `grep -c 'VaultCLIWatcher('` is **not** usable as evidence: it already prints `1` on the unmodified tree, because today's single construction sits inside the per-vault loop and executes N times.

The second must print nothing — rename any surviving helper, because this check pins the absence of the per-vault closure *by name*. The behavioural proof is the factory-level one-watcher test and the two-vault resolver test in requirement 4.

**3. The argv carries a comma-joined list, and no other call site spawns a watcher:**

```
grep -n '",".join' src/vault_ui/vault_cli_watcher.py
grep -rn '"watch"' src/
```

The first must print at least one line. The second must show `vault_cli_watcher.py` only — any other file spawning a `watch` subprocess means a second fan-out exists somewhere.

**4. Per-event resolution exists and is used:**

```
grep -n 'for vault in config.vaults' src/vault_ui/factory.py
```

Must not show a loop that constructs a watcher per vault. Line numbers alone cannot settle this — read the surrounding lines. (A bare `grep -c 'vault' src/vault_ui/factory.py` is useless as evidence: it prints `91` on the unmodified tree.)

**5. Tests:**

```
uv run pytest tests/test_vault_cli_watcher.py -q
```

Must pass, with the new multi-vault cases present. Confirm the new argv test would fail against the old single-vault code — a test that passes before and after proves nothing.

**6. Docs and changelog:**

```
grep -rn 'per-vault watcher\|built per-vault' src/ tests/ docs/
grep -n 'per configured vault' docs/launchd-service.md
grep -n '^## ' CHANGELOG.md | head -1
awk '/^## Unreleased/{f=1;next} /^## /{f=0} f' CHANGELOG.md | grep -c .
```

The first two must print nothing — every phrase describing the fan-out is gone. (The first prints four lines on the unmodified tree: `api/tasks.py`, `static/app.js`, `tests/test_websocket_routing.py` and `docs/launchd-service.md`.) The third must print `## Unreleased`. The fourth must print `>= 1` — the count of non-empty lines in the new section.

**7. Self-check.** Before you finish, re-run `make precommit` and confirm it passes; then walk each of requirements 1-5 against the change and state in your final message which requirement each verification step covers and which test pins it. Confirm by inspection that no path can spawn more than one `vault-cli watch` subprocess per vault-ui instance.
</verification>
