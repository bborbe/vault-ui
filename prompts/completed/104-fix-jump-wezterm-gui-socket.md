---
status: completed
summary: The pane-resolution helper is now spawned with the newest live WezTerm GUI socket when WEZTERM_UNIX_SOCKET is unset, restoring the `↗` jump under launchd.
execution_id: vault-ui-wezterm-socket-exec-104-fix-jump-wezterm-gui-socket
dark-factory-version: dev
created: "2026-10-03T14:00:00Z"
queued: "2026-10-03T13:39:49Z"
started: "2026-10-03T13:40:31Z"
completed: "2026-10-03T13:42:15Z"
---

# Point the Pane-Resolution Subprocess at the Live WezTerm GUI Socket

<summary>
- The `↗` jump on a live task card reaches the running session again when the board runs as a background service
- The board finds the terminal app's running GUI on its own, so a terminal restart needs no service reconfiguration
- A socket left behind by a terminal that has since exited is ignored, never chosen
- An operator who sets the terminal socket explicitly keeps that choice; the board never overrides it
- A machine with no running terminal GUI behaves exactly as it does today
- The board's own process environment stays untouched; only the helper it launches changes
- Nothing else about the jump route, the cards, or the board changes
</summary>

<objective>
Make `POST /api/tasks/{id}/jump` resolve the pane of a live session when vault-ui runs under launchd. The pane-resolution helper is spawned without `WEZTERM_UNIX_SOCKET`, so `wezterm cli` talks to a separate mux server that holds almost none of the operator's panes and the route answers 409 "no pane resolves for this session". After this change the helper's environment names the newest WezTerm GUI socket whose process is still alive, unless the variable was already set.
</objective>

<context>
Read `docs/dod.md` for the definition of done the daemon validates against.

Read these before writing anything:

- `src/vault_ui/pane_resolver.py` — `_subprocess_env()` is the function to change. Today it returns `dict(os.environ)` with the WezTerm bundle dir (from `_wezterm_bin_dir()`) prepended to `PATH`, and **returns early** when `_wezterm_bin_dir()` is `None`. `resolve_pane_id()` passes `env=_subprocess_env()` to `asyncio.create_subprocess_exec`. Keep the module's conventions: host-path helpers are **functions, not module constants**, so tests can monkeypatch them; expected absences return `None` and log at `debug`, never raise; `os.environ` is never mutated.
- `tests/test_pane_resolver.py` — the `_subprocess_env` group (`test_subprocess_env_prepends_bundle_and_preserves_rest_of_path`, `test_subprocess_env_is_unchanged_when_bundle_absent`, `test_subprocess_env_never_mutates_os_environ`) and the spawn-env tests (`test_resolve_pane_id_spawn_env_prepends_wezterm_dir`, `test_resolve_pane_id_spawn_env_unchanged_without_wezterm`) using `_patch_subprocess(monkeypatch, proc, kwargs_record)`. Follow these patterns.
- `docs/launchd-service.md` — the "**Important:**" bullet list, specifically the bullet beginning "WezTerm does **not** need to be on the `PATH`".

Host facts, measured 2026-10-03. The container cannot observe these and must not try; they explain the change, they are not something `<verification>` reproduces.

- launchd starts the service with no `WEZTERM_UNIX_SOCKET`. `wezterm cli` then falls back to `~/.local/share/wezterm/sock`, which belongs to a separate `wezterm-mux-server` holding almost no panes.
- The WezTerm GUI listens on `~/.local/share/wezterm/gui-sock-<pid>`, where `<pid>` is the GUI process id. The name changes on every WezTerm restart, so it cannot be pinned in the plist; stale `gui-sock-<pid>` files from exited GUIs stay on disk.
- With `WEZTERM_UNIX_SOCKET=~/.local/share/wezterm/gui-sock-33263` the helper returned pane `871` in 2.3s; with `.../sock` it reported "no pane resolves".
</context>

<requirements>
1. **`_pid_alive(pid: int) -> bool`** in `src/vault_ui/pane_resolver.py`. Returns `True` only when `os.kill(pid, 0)` returns without raising; any `OSError` (including `ProcessLookupError` and `PermissionError`) or `OverflowError` returns `False`; a `pid <= 0` returns `False` without calling `os.kill`. A function, so tests can monkeypatch liveness instead of depending on real pids.

2. **`_wezterm_gui_socket() -> Path | None`** in `src/vault_ui/pane_resolver.py`.
   - Directory: `Path.home() / ".local" / "share" / "wezterm"`, computed inside the function at call time (not a module constant), so tests can redirect `HOME`.
   - Candidates: entries of that one directory whose name is `gui-sock-<pid>` with `<pid>` parsing as a positive `int` (`str.isdigit()` on the suffix). Skip every other name, including the plain `sock`. Do not check the file type — tests use regular files as stand-ins.
   - Keep only candidates with `_pid_alive(pid)` true. Return the one with the greatest `stat().st_mtime`; return `None` when none qualifies.
   - Failure handling: a missing or unreadable directory (`OSError` from listing) returns `None`; a candidate whose `stat()` raises `OSError` (vanished between listing and stat) is skipped. Never raise.
   - Scan only this one directory — no recursion, no PATH scan, no other locations.

3. **Rework `_subprocess_env() -> dict[str, str]`**:
   - Start from `env = dict(os.environ)`. Keep the existing `PATH` behaviour exactly as it is (prepend `_wezterm_bin_dir()` when found; debug-log and leave `PATH` alone when not), but remove the early `return` so the socket step below runs in both branches.
   - When `os.environ.get("WEZTERM_UNIX_SOCKET")` is falsy (absent or empty), call `_wezterm_gui_socket()`; when it returns a path, set `env["WEZTERM_UNIX_SOCKET"] = str(path)`; when it returns `None`, leave the key out of `env` exactly as `os.environ` has it and log at `debug` that no live WezTerm GUI socket was found.
   - When `WEZTERM_UNIX_SOCKET` is set non-empty in `os.environ`, it is copied through unchanged and `_wezterm_gui_socket()` is not consulted.
   - Never mutate `os.environ`. Update the docstring to describe both adjustments and why (launchd provides neither the bundle on `PATH` nor the GUI socket).

4. **Tests** in `tests/test_pane_resolver.py`:
   - Add an **autouse fixture** (module-level, `@pytest.fixture(autouse=True)`) that runs `monkeypatch.setenv("HOME", str(tmp_path))` and `monkeypatch.delenv("WEZTERM_UNIX_SOCKET", raising=False)`, so every existing test is hermetic against the developer's real `~/.local/share/wezterm/` and existing assertions keep holding without being edited. Confirm `test_jump_token_path_is_under_home_secrets` and `test_who_needs_me_path_falls_back_to_marketplace` still pass (they compare against `Path.home()`, which follows `HOME`).
   - Add a small helper that creates `tmp_path / ".local/share/wezterm/gui-sock-<pid>"` files and sets their mtime with `os.utime`.
   - New tests (monkeypatch `vault_ui.pane_resolver._pid_alive` with a set-membership lambda):
     - newest live socket chosen: three live sockets with distinct mtimes → `_subprocess_env()["WEZTERM_UNIX_SOCKET"]` is the newest one's path.
     - dead-pid sockets skipped: the newest-by-mtime socket has a dead pid, an older one is live → the older live one is chosen; all dead → key absent.
     - non-matching names ignored: `sock`, `gui-sock-abc`, and `gui-sock-` present alongside no valid live socket → key absent.
     - existing env var preserved: `monkeypatch.setenv("WEZTERM_UNIX_SOCKET", "/explicit/sock")` with a live gui-sock present → value is `"/explicit/sock"`.
     - no sockets (directory missing entirely) → `"WEZTERM_UNIX_SOCKET" not in _subprocess_env()`.
     - `os.environ` unchanged: with a live socket present and the var unset, call `_subprocess_env()` and assert `"WEZTERM_UNIX_SOCKET" not in os.environ` and `os.environ` equals a snapshot taken before the call.
     - `_pid_alive` itself: `_pid_alive(os.getpid())` is `True`; with `os.kill` monkeypatched to raise `ProcessLookupError`, it is `False`.
   - **Spawn-boundary test**: with a live socket present, call `resolve_pane_id(...)` through `_patch_subprocess(..., kwargs_record)` and assert `kwargs_record[0]["env"]["WEZTERM_UNIX_SOCKET"]` equals that socket's path. A correct helper whose result never reaches `create_subprocess_exec` is the silent regression this guards.

5. **`docs/launchd-service.md`**: extend the bullet "WezTerm does **not** need to be on the `PATH`" (or add a sibling bullet right after it) stating that `WEZTERM_UNIX_SOCKET` must not be set in the plist either: the board picks the newest `~/.local/share/wezterm/gui-sock-<pid>` whose process is alive, because the GUI socket name changes on every WezTerm restart; without it `wezterm cli` reaches the separate mux server and the jump answers 409. An explicitly set value still wins.

6. **CHANGELOG.md**: there is no `## Unreleased` section (the head is `## v0.74.1`). Add `## Unreleased` directly above the first `## v` heading, with a single `- fix: ...` bullet in the style of the existing entries — the user-visible effect first (the `↗` jump on a live card no longer answers 409 under launchd), then the cause (launchd provides no `WEZTERM_UNIX_SOCKET`, so `wezterm cli` reached the mux-server socket instead of the GUI) and the fix (the helper is spawned with the newest live GUI socket; an explicit value wins).

7. **Self-check.** Re-run every command in `<verification>` and confirm each passes; then walk each numbered requirement above against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command works inside the container; do not attempt one.
- `os.environ` must never be mutated — the service process serves every other request.
- An explicitly set (non-empty) `WEZTERM_UNIX_SOCKET` always wins over discovery.
- When no live GUI socket exists, `WEZTERM_UNIX_SOCKET` stays absent from the returned env — never fall back to `sock` or to a dead-pid socket.
- Home directory is resolved via `Path.home()` at call time, never cached at import.
- Do not change `who-needs-me.py` (it lives in the supervisor repo) and do not edit the launchd plist (not in this repo).
- Do not change `read_jump_token`, `perform_jump`, `_who_needs_me_path`, `_wezterm_bin_dir`, the jump route, `jump_pane`, or anything under `src/vault_ui/static/`.
- Existing tests must still pass and their assertions must not change; the autouse fixture is the only change that touches them.
- All paths in this prompt are repo-relative; the `~/.local/share/wezterm/...` paths are runtime values the code computes, never files to read in the container.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass (sync + format + test + lint + typecheck).

Then confirm the change is wired, not merely present:

- `grep -q '^def _wezterm_gui_socket' src/vault_ui/pane_resolver.py` -- must succeed.
- `grep -q '^def _pid_alive' src/vault_ui/pane_resolver.py` -- must succeed.
- `grep -q 'env=_subprocess_env()' src/vault_ui/pane_resolver.py` -- must succeed (the spawn call site still uses the helper).
- `grep -q 'WEZTERM_UNIX_SOCKET' docs/launchd-service.md` -- must succeed.
- `awk '/^## /{sec=$0} /^- fix:/ && sec=="## Unreleased"{n++} END{exit !(n>=1)}' CHANGELOG.md` -- must exit 0 (a `fix:` bullet sits under `## Unreleased`).
</verification>
