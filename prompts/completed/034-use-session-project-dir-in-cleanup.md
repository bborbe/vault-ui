---
status: completed
summary: Added session_project_dir field to VaultConfig, populated it from vault-cli JSON, and updated all three derive_claude_project_dir call sites to pass the override; added tests for the new behavior.
container: vault-ui-034-use-session-project-dir-in-cleanup
dark-factory-version: v0.57.5
created: "2026-03-19T10:52:03Z"
queued: "2026-03-19T11:03:07Z"
started: "2026-03-19T11:05:11Z"
completed: "2026-03-19T11:06:59Z"
---

<summary>
- Cleanup uses the configured session project directory (if set) when looking for Claude session files, falling back to the vault path when not set
- A new optional field in vault config captures the override directory from vault-cli config
- When the override is set, cleanup, watcher, and API session resolution all use it instead of deriving the directory from vault path
- Vaults without the override continue to work unchanged
- Works for all vaults regardless of whether a custom Claude script is used
</summary>

<objective>
Fix stale-session cleanup for vaults whose Claude sessions land in a different project directory than what `derive_claude_project_dir(vault_path)` computes. vault-cli now exposes `session_project_dir` in its config JSON output; when set, all code that resolves session files must use it instead of the vault_path-derived directory.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Files to read before making changes:
- `src/vault_ui/config.py` — `VaultConfig` dataclass and `load_config` function (especially the `vaults.append(VaultConfig(...))` block around line 109)
- `src/vault_ui/cleanup.py` — `derive_claude_project_dir` function (line 16) and its call site in `cleanup_stale_sessions` (line 34)
- `src/vault_ui/factory.py` — `make_callback` closure in `start_task_watchers` (line 130) where `derive_claude_project_dir` is called
- `src/vault_ui/api/tasks.py` — `set_task_session` endpoint (around line 555) where `derive_claude_project_dir` is called
- `tests/test_cleanup.py` — existing cleanup tests and `_make_config` helper
</context>

<requirements>
### 1. Add `session_project_dir` field to `VaultConfig` in `src/vault_ui/config.py`

In the `VaultConfig` dataclass, add a new field after `vault_cli_path`:

Old:
```python
@dataclass
class VaultConfig:
    """Configuration for a single Obsidian vault."""

    name: str
    vault_path: str
    tasks_folder: str
    vault_name: str = ""  # For obsidian:// URLs, defaults to name.title()
    claude_script: str = "claude"  # Script to run Claude sessions (default: "claude")
    vault_cli_path: str = "vault-cli"  # Path to vault-cli binary
```

New:
```python
@dataclass
class VaultConfig:
    """Configuration for a single Obsidian vault."""

    name: str
    vault_path: str
    tasks_folder: str
    vault_name: str = ""  # For obsidian:// URLs, defaults to name.title()
    claude_script: str = "claude"  # Script to run Claude sessions (default: "claude")
    vault_cli_path: str = "vault-cli"  # Path to vault-cli binary
    session_project_dir: str = ""  # Override Claude project dir for session file lookup
```

### 2. Populate `session_project_dir` from vault-cli JSON in `load_config` in `src/vault_ui/config.py`

In the `load_config` function, find the `VaultConfig(...)` constructor call (around line 109-117). Add `session_project_dir` from the CLI vault JSON:

Old:
```python
        vaults.append(
            VaultConfig(
                name=vault_key,
                vault_path=cli_vault["path"],
                tasks_folder=cli_vault["tasks_dir"],
                vault_name=vault_name,
                claude_script=cli_vault.get("claude_script") or "claude",
                vault_cli_path=vault_cli_path,
            )
        )
```

New:
```python
        vaults.append(
            VaultConfig(
                name=vault_key,
                vault_path=cli_vault["path"],
                tasks_folder=cli_vault["tasks_dir"],
                vault_name=vault_name,
                claude_script=cli_vault.get("claude_script") or "claude",
                vault_cli_path=vault_cli_path,
                session_project_dir=cli_vault.get("session_project_dir") or "",
            )
        )
```

### 3. Update `derive_claude_project_dir` in `src/vault_ui/cleanup.py`

Change the function signature to accept an optional `session_project_dir` parameter. When it is non-empty, use it directly instead of deriving from `vault_path`.

Old:
```python
def derive_claude_project_dir(vault_path: str) -> Path:
    """Convert vault_path to ~/.claude/projects/<derived> directory."""
    derived = vault_path.replace("/", "-")
    return Path.home() / ".claude" / "projects" / derived
```

New:
```python
def derive_claude_project_dir(vault_path: str, session_project_dir: str = "") -> Path:
    """Return the Claude project directory for session file lookup.

    If session_project_dir is provided and non-empty, use it directly.
    Otherwise derive from vault_path using the standard convention.
    """
    if session_project_dir:
        return Path(session_project_dir).expanduser()
    derived = vault_path.replace("/", "-")
    return Path.home() / ".claude" / "projects" / derived
```

### 4. Update the call site in `cleanup_stale_sessions` in `src/vault_ui/cleanup.py`

In the `cleanup_stale_sessions` function, find the line that calls `derive_claude_project_dir` (line 34):

Old:
```python
            project_dir = derive_claude_project_dir(vault.vault_path)
```

New:
```python
            project_dir = derive_claude_project_dir(vault.vault_path, vault.session_project_dir)
```

### 5. Update the call site in `start_task_watchers` in `src/vault_ui/factory.py`

In the `make_callback` closure inside `start_task_watchers`, find the `derive_claude_project_dir` call (line 130):

Old:
```python
                project_dir = derive_claude_project_dir(vault_cfg.vault_path)
```

New:
```python
                project_dir = derive_claude_project_dir(vault_cfg.vault_path, vault_cfg.session_project_dir)
```

### 6. Update the call site in `set_task_session` endpoint in `src/vault_ui/api/tasks.py`

Find the `derive_claude_project_dir` call (around line 555):

Old:
```python
            resolved = resolve_session_id(
                request.claude_session_id,
                derive_claude_project_dir(vault_config.vault_path),
            )
```

New:
```python
            resolved = resolve_session_id(
                request.claude_session_id,
                derive_claude_project_dir(vault_config.vault_path, vault_config.session_project_dir),
            )
```

### 7. Update tests in `tests/test_cleanup.py`

a. Update `_make_config` to include `session_project_dir`:

Old:
```python
def _make_config(current_user: str = "alice") -> Config:
    vault = VaultConfig(
        name="testvault",
        vault_path="/vault",
        tasks_folder="Tasks",
        vault_cli_path="vault-cli",
    )
    return Config(vaults=[vault], current_user=current_user)
```

New:
```python
def _make_config(current_user: str = "alice", session_project_dir: str = "") -> Config:
    vault = VaultConfig(
        name="testvault",
        vault_path="/vault",
        tasks_folder="Tasks",
        vault_cli_path="vault-cli",
        session_project_dir=session_project_dir,
    )
    return Config(vaults=[vault], current_user=current_user)
```

b. Add a unit test for `derive_claude_project_dir` with and without `session_project_dir`:

```python
from vault_ui.cleanup import derive_claude_project_dir

def test_derive_claude_project_dir_default() -> None:
    """Without session_project_dir, derives from vault_path."""
    result = derive_claude_project_dir("/Users/me/vault")
    assert result == Path.home() / ".claude" / "projects" / "-Users-me-vault"

def test_derive_claude_project_dir_with_session_override() -> None:
    """With session_project_dir set, uses it directly instead of deriving."""
    result = derive_claude_project_dir(
        "/Users/me/vault",
        session_project_dir="/Users/me/.claude/projects/-Users-me-other",
    )
    assert str(result) == "/Users/me/.claude/projects/-Users-me-other"

def test_derive_claude_project_dir_empty_session_falls_back() -> None:
    """Empty session_project_dir falls back to vault_path derivation."""
    result = derive_claude_project_dir("/Users/me/vault", session_project_dir="")
    assert result == Path.home() / ".claude" / "projects" / "-Users-me-vault"
```

### 8. Update `tests/test_config.py` if needed

Read `tests/test_config.py` first. If there are tests that assert on `VaultConfig` fields or mock `vault-cli config list` JSON output, add `session_project_dir` to the expected fields. The JSON output from vault-cli may or may not include the key — the code must handle both cases (via `.get("session_project_dir") or ""`).
</requirements>

<constraints>
- Do NOT commit -- dark-factory handles git
- Existing tests must still pass after changes
- No real subprocess, network, or Claude API calls in tests -- mock external dependencies
- vault-cli is the sole interface for vault operations -- never read vault files directly
- `derive_claude_project_dir` must remain backward compatible -- the new parameter has a default value of `""` so all existing callers without the argument continue to work
- Do NOT change the function name `derive_claude_project_dir` -- it is imported by `factory.py` and `api/tasks.py`
- The `session_project_dir` value from vault-cli JSON may be absent or empty string -- both must be treated as "not set" (fall back to vault_path derivation)
</constraints>

<verification>
Run `make precommit` from the repo root -- must pass (format + test + lint + typecheck).

Additionally confirm:
1. `derive_claude_project_dir("/some/path")` still works without the second argument (backward compat)
2. `derive_claude_project_dir("/some/path", "/override/dir")` returns `Path("/override/dir")`
3. `derive_claude_project_dir("/some/path", "")` falls back to vault_path derivation
4. `VaultConfig` can be constructed without `session_project_dir` (default `""`)
5. All 3 call sites (cleanup.py, factory.py, api/tasks.py) pass `session_project_dir`
</verification>
