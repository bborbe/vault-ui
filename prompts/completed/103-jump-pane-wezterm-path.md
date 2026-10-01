---
status: completed
summary: Prepended the WezTerm application bundle to the pane-resolution helper's PATH so the board resolves the terminal binary itself instead of depending on the launchd service's minimal PATH.
execution_id: vault-ui-jump-pathfix-exec-103-jump-pane-wezterm-path
dark-factory-version: v0.196.0
created: "2026-10-01T15:10:00Z"
queued: "2026-10-01T14:51:34Z"
started: "2026-10-01T14:52:16Z"
completed: "2026-10-01T14:54:21Z"
---

# Put the Terminal Binary on the PATH of the Pane-Resolution Subprocess

<summary>
- Live cards on the deployed board can reach their session again
- The board stops depending on the terminal app being on the service's PATH
- The change is confined to how the board launches the pane-resolution helper
- A machine without the terminal app behaves exactly as it does today
- Nothing else about the board changes
</summary>

<objective>
Make the jump feature actually work on the deployed board. The launchd service runs with a fixed, minimal PATH that does not include the WezTerm application bundle, so the pane-resolution helper cannot list panes and every live card silently receives no jump target — the control never renders, and no test catches it, because the difference is the environment rather than the code path.
</objective>

<context>
Read `docs/dod.md` for the definition of done the daemon validates against.

Read these before writing anything:

- `src/vault_ui/pane_resolver.py` — `resolve_pane_id` is the function to change. It already resolves the helper script through `_who_needs_me_path()` (a `Path.home()`-anchored function with a `CLAUDE_PLUGIN_ROOT` fallback) and spawns it with `asyncio.create_subprocess_exec`. Note the module's existing conventions and keep them: host-path helpers are **functions, not module constants**, so a test can monkeypatch them; failures return `None` rather than raising.
- `docs/launchd-service.md` — the service's `PATH` is fixed in the launchd plist and documented as needing to carry whatever the service shells out to. Read it before deciding how the environment is assembled, and note that the plist is **not** in this repo.

Facts about the host runtime, measured 2026-10-01. The container cannot observe these and must not try; they are why the change is needed, not something `<verification>` reproduces.

- The launchd service runs with `PATH=/Users/bborbe/.local/bin:/Users/bborbe/Documents/workspaces/go/bin:/opt/homebrew/bin:/usr/bin:/bin`.
- The WezTerm binary is at `/Applications/WezTerm.app/Contents/MacOS/wezterm`, and that directory is on none of the entries above.
- `who-needs-me.py --pane-for <8-char session prefix>` shells out to `wezterm cli list`. With a login shell's PATH it prints the pane id and exits `0`; with the service's PATH it prints `pane-for: WezTerm pane list unreadable` and exits `1`.
- Consequence, measured on the deployed board: **every** live task carried `jump_pane: null`, so no jump control rendered anywhere. The identical code run from a terminal resolved a pane for every live task. That is precisely why the unit suite is green — nothing in the code path differs, so there is no test to fail.
</context>

<requirements>
1. **`_wezterm_bin_dir() -> Path | None`** in `src/vault_ui/pane_resolver.py`. Returns the directory holding the `wezterm` executable, or `None` when it is not there. Check the macOS application bundle path `/Applications/WezTerm.app/Contents/MacOS` and return it only when `wezterm` actually exists inside it. A function, not a module constant — the same test seam `_jump_token_path` and `_who_needs_me_path` already provide.

2. **`_subprocess_env() -> dict[str, str]`**. Returns a **copy** of `os.environ` whose `PATH` is `str(_wezterm_bin_dir()) + os.pathsep + os.environ["PATH"]` when that directory is found, and the unchanged copy when it is not. Never mutate `os.environ` — the process serves every other request, and a mutated PATH would leak into the whole service.

3. **Pass `env=_subprocess_env()`** to the `asyncio.create_subprocess_exec` call in `resolve_pane_id`. This is the whole fix: the helper then inherits a PATH containing the terminal binary.

4. **Log the absence at `debug`.** When `_wezterm_bin_dir()` returns `None`, log that the binary was not located, naming the fact rather than the consequence. A later reader debugging an empty board needs to see it. Do not add logging to the success path — the module already logs the helper's stderr on a non-zero exit.

5. **Tests** in `tests/test_pane_resolver.py`, extending the existing group:

   - With `_wezterm_bin_dir` monkeypatched at a `tmp_path` that contains a dummy `wezterm` file, the env handed to the subprocess carries that directory as the **first** `PATH` entry, and the rest of PATH is preserved in order.
   - With `_wezterm_bin_dir` returning `None`, the env's `PATH` is byte-identical to `os.environ`'s.
   - `os.environ` is not mutated by either call — set a sentinel PATH value, call, and assert it is unchanged.
   - `_wezterm_bin_dir()` itself is exercised for **both** outcomes — monkeypatch the bundle path (or `Path.exists`) so a `wezterm` file is present, and assert it returns that directory; with nothing present, assert it returns `None`. Every other test above monkeypatches this function away, so without this one the function the entire fix rests on ships untested — the same silent-ship class this prompt exists to remove.

   **Capture the env from the spawn**, by monkeypatching the subprocess call and reading its kwargs, rather than re-deriving `_subprocess_env()`'s return value. The regression this guards against is a correct helper whose result never reaches `create_subprocess_exec`, which a helper-only assertion cannot see. The existing `_patch_subprocess` helper records **argv only** and swallows `**_kwargs`, and `test_resolve_pane_id_argv_is_exact` asserts on the list it returns — so extend it to also record the kwargs (a second list, or a sibling helper) rather than widening that return value, which would force the existing assertion to change.

6. **CHANGELOG.md**: the head is the released `## v0.74.0` and there is no `## Unreleased`. Add one directly above it, carrying a single `fix:` bullet. Follow the existing entries' style — the bullet explains the user-visible effect, not the mechanics.

7. **`docs/launchd-service.md`** — add a short note that WezTerm is now resolved by the application itself, so the service `PATH` no longer needs the WezTerm bundle. The doc's existing PATH guidance covers only `vault-cli`, and without this the next operator will re-add the bundle to the plist and conclude the code change was unnecessary.

8. **Self-check.** Before finishing, re-run every command in `<verification>` and confirm each passes; then walk each numbered requirement above against the change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- **Do not change `who-needs-me.py`.** It lives in the supervisor's own repository, not this one. The fix belongs where the subprocess is launched.
- **Do not edit the launchd plist.** It is not in this repo, and a fix that lives there means the next install silently ships the same dead feature. The point of this change is that the code carries its own dependency resolution.
- Do not widen the search beyond the application bundle path — no PATH scan, no `which`, no globbing the filesystem. One known location, checked for existence.
- Do not change `read_jump_token`, `perform_jump`, the jump route, the origin gate, `jump_pane`, or anything in `src/vault_ui/static/`. This prompt changes one spawn's environment.
- Existing tests must still pass and their assertions must not change.
- All paths in this prompt are repo-relative. The host paths above are runtime values the code computes, never file references to read.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass (format + test + lint + typecheck).

Then confirm the change is wired, not merely present:

- `grep -q '^def _wezterm_bin_dir' src/vault_ui/pane_resolver.py` -- must succeed. (Anchored at the line start so a docstring or comment naming the function cannot satisfy it.)
- `grep -q '^def _subprocess_env' src/vault_ui/pane_resolver.py` -- must succeed, anchored for the same reason.
- `grep -q 'env=_subprocess_env()' src/vault_ui/pane_resolver.py` -- must succeed, anchoring on the **call site** rather than the definition. A helper that exists but is never passed to the spawn is exactly the regression requirement 5 exists to catch, and a bare `_subprocess_env` pattern would match the `def` line and pass.
- `grep -q '^## Unreleased' CHANGELOG.md` -- must succeed.
</verification>
