---
status: completed
summary: Implemented XDG-first config resolution for vault-ui, checking ~/.config/vault-ui/config.yaml before the legacy repo-root fallback
execution_id: vault-ui-config-xdg-exec-067-config-xdg-first
dark-factory-version: v0.191.0
created: "2026-07-03T13:41:20Z"
queued: "2026-07-03T13:41:20Z"
started: "2026-07-03T13:43:25Z"
completed: "2026-07-03T13:45:53Z"
---

<summary>
- vault-ui now looks for its config in the XDG-standard location first
- `~/.config/vault-ui/config.yaml` is the new preferred config path
- The old repo-root `config.yaml` still works as a fallback for existing setups
- No existing installs break — nothing is migrated or deleted
- New installs are pointed at the XDG path in the README and example config
- Resolution mirrors the same XDG-first pattern vault-cli already uses
</summary>

<objective>
Make vault-ui resolve its config file XDG-first (`~/.config/vault-ui/config.yaml`), falling back to the legacy repo-root `config.yaml` when the XDG file does not exist, so vault-ui's config location matches the convention already established by vault-cli and stops requiring the config file to live inside the cloned repo.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read `src/vault_ui/config.py` — current state:
- `_CONFIG_PATH = Path(__file__).parent.parent.parent / "config.yaml"` (module-level constant, repo-root only)
- `def load_config(config_path: Path = _CONFIG_PATH) -> Config:` — raises `FileNotFoundError` with a helpful message when `config_path` does not exist (message references `cp config.yaml.example config.yaml`)

Read `src/vault_ui/factory.py` — `load_config()` is called with no arguments at line 37 (relies on the default), so the resolution logic must live behind the default, not require callers to pass a path.

Pattern this mirrors conceptually (not a file read — it's a sibling Go repo, not mounted in this container): vault-cli's `FindConfigDir` — XDG dir first, then legacy dir, then XDG dir as the default for new installs; never creates directories. Full logic is spelled out in `<requirements>` below, no need to read the Go source.

Read `tests/test_config.py` — every existing test calls `load_config(config_file)` with an explicit `tmp_path`-based path, so none of them exercise the default-resolution branch. `resolve_default_config_path` must accept injectable path arguments so tests can pass `tmp_path`-based fakes for BOTH the XDG and legacy candidates — the real repo-root `config.yaml` is gitignored and absent in this container, so nothing can rely on it existing on disk during tests.
</context>

<requirements>
1. In `src/vault_ui/config.py`, rename the module-level constant `_CONFIG_PATH` to `_LEGACY_CONFIG_PATH` (same value, same expression: `Path(__file__).parent.parent.parent / "config.yaml"`). Add a helper function (not a frozen constant — computed at call time so the no-arg default reflects the real home directory at call time, not at import time):
   ```python
   def _xdg_config_path() -> Path:
       return Path.home() / ".config" / "vault-ui" / "config.yaml"
   ```

2. Add a new function with injectable path arguments (defaults resolve to the real candidates, but tests can override both):
   ```python
   def resolve_default_config_path(
       xdg_path: Path | None = None,
       legacy_path: Path | None = None,
   ) -> Path:
       """Resolve the default config.yaml path, XDG-first.

       Checks the XDG path (`~/.config/vault-ui/config.yaml` by default)
       first. Falls back to the legacy path (repo-root `config.yaml` by
       default) if the XDG path does not exist but the legacy path does.
       Otherwise returns the XDG path as the default for new installs.
       Never creates directories or files.
       """
       if xdg_path is None:
           xdg_path = _xdg_config_path()
       if legacy_path is None:
           legacy_path = _LEGACY_CONFIG_PATH
       if xdg_path.exists():
           return xdg_path
       if legacy_path.exists():
           return legacy_path
       return xdg_path
   ```

3. Change `load_config`'s signature from `def load_config(config_path: Path = _CONFIG_PATH) -> Config:` to `def load_config(config_path: Path | None = None) -> Config:`. At the top of the function body, add:
   ```python
   if config_path is None:
       config_path = resolve_default_config_path()
   ```
   Keep the rest of `load_config` (the `FileNotFoundError` check and everything after) unchanged in control flow, but update the `FileNotFoundError` message text (see requirement 5) since it currently only tells the user to create the legacy repo-root file.

4. `src/vault_ui/factory.py` requires no change — `load_config()` called with no arguments continues to work because the new default is `None`, resolved inside the function.

5. Update the `FileNotFoundError` message raised in `load_config` when `config_path` does not exist. It currently reads:
   ```
   f"config.yaml not found at {config_path}\n"
   "\nCreate it by copying the example:\n"
   "  cp config.yaml.example config.yaml\n"
   "\nThen edit vault paths to match your system."
   ```
   Update it to also mention the preferred XDG location, e.g.:
   ```
   f"config.yaml not found at {config_path}\n"
   "\nCreate it by copying the example (preferred, XDG location):\n"
   "  mkdir -p ~/.config/vault-ui\n"
   "  cp config.yaml.example ~/.config/vault-ui/config.yaml\n"
   "\nOr, for the legacy repo-root location:\n"
   "  cp config.yaml.example config.yaml\n"
   "\nThen edit vault paths to match your system."
   ```

6. Update `config.yaml.example` (repo root): add a comment block near the top explaining the new resolution order, e.g.:
   ```yaml
   # vault-ui looks for its config in this order:
   #   1. ~/.config/vault-ui/config.yaml  (preferred — copy this file there)
   #   2. ./config.yaml                    (legacy, repo-root fallback)
   # Copy this example to ~/.config/vault-ui/config.yaml for new installs.
   ```
   Keep the rest of the example file's content unchanged.

7. Update `README.md` § "Configuration" (the section starting `## Configuration`, currently instructing `cp config.yaml.example config.yaml`). Replace the setup instructions to document the XDG path as canonical, e.g.:
   ```markdown
   ## Configuration

   Copy the example config to the XDG config directory (preferred):
   ```bash
   mkdir -p ~/.config/vault-ui
   cp config.yaml.example ~/.config/vault-ui/config.yaml
   ```

   vault-ui looks for config in this order:
   1. `~/.config/vault-ui/config.yaml` (preferred)
   2. `./config.yaml` in the repo root (legacy fallback — still supported for existing installs)
   ```
   Keep the "Top-level fields" and "Per-vault fields" bullet lists that follow unchanged.

8. In `tests/test_config.py`, add tests for `resolve_default_config_path` (import it alongside `load_config`). Build both candidate paths from `tmp_path` and pass them explicitly via the `xdg_path=` / `legacy_path=` arguments — do not rely on `Path.home()` patching, since the function now accepts both paths as injectable arguments. Cover:
   - XDG file exists (write `(tmp_path / "xdg" / "config.yaml")`, ensure the parent dir exists via `mkdir(parents=True)` in the test, then write the file) → returns the XDG path
   - XDG file absent, legacy file exists (write `(tmp_path / "legacy" / "config.yaml")`) → returns the legacy path
   - neither exists → returns the XDG path (as the new-install default)
   - XDG file exists AND legacy file exists → returns the XDG path (XDG wins)
   - `resolve_default_config_path()` called with no arguments still returns a `Path` under `.config/vault-ui/config.yaml` when nothing is patched (sanity check that the real defaults resolve to a sane XDG-shaped path — do not assert existence, only shape, since the real home directory's state is untestable)

9. Update `docs/launchd-service.md`:
   - Line ~16 (`- A populated `config.yaml` in the repo root (`cp config.yaml.example config.yaml`)`) — mention the XDG path is now preferred, repo-root remains a working fallback.
   - Line ~68 (`uv run --directory <repo>` is required because `config.yaml` is loaded relative to the source tree...`) — this statement is now only true for the legacy fallback path; add a note that `~/.config/vault-ui/config.yaml` is checked first regardless of working directory, so a `uv tool install`-based bare `vault-ui` invocation now works IF the XDG file exists (the `uv run --directory` requirement only still applies when relying on the legacy repo-root fallback).
   - Do not change the systemd/launchd plist mechanics, restart instructions, or troubleshooting sections beyond the config-path wording.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests in `tests/test_config.py` must still pass unchanged (all pass an explicit `config_path`, so behavior is unaffected)
- Never create directories or files as a side effect of config resolution — `resolve_default_config_path` only calls `Path.exists()`, never `mkdir` or `touch`
- Keep the repo-root legacy config.yaml fallback working — do not migrate, delete, or warn-deprecate it
- Do not change `VaultConfig`, `Config`, `discover_current_user`, `discover_vaults_from_cli`, or any function signature other than `load_config`'s default parameter
- `src/vault_ui/factory.py` must not need any code change
</constraints>

<verification>
Run `make precommit` -- must pass (format + test + lint + typecheck).
</verification>
