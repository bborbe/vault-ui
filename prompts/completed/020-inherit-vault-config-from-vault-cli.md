---
status: completed
summary: Replaced vault config discovery with vault-cli subprocess call in load_config(), updated config.yaml/config.yaml.example to slim dict format, removed dead claude_cli field, rewrote test_config.py for new format
container: vault-ui-020-inherit-vault-config-from-vault-cli
dark-factory-version: v0.54.0
created: "2026-03-12T17:00:00Z"
queued: "2026-03-12T16:59:28Z"
started: "2026-03-12T17:42:41Z"
completed: "2026-03-12T17:46:47Z"
---
<summary>
- Task-orch discovers vault names, paths, and tasks_folder from vault-cli at startup
- Calls `vault-cli config list --output json` subprocess to get vault configs
- Task-orch config.yaml shrinks to only orch-specific overrides (claude_script, vault_name for obsidian:// URLs)
- Shared fields (name, vault_path, tasks_folder) are no longer duplicated in task-orch config
- Startup fails with clear error if vault-cli is not available or returns no vaults
</summary>

<objective>
Replace duplicated vault configuration in task-orch with discovery from `vault-cli config list --output json`, keeping only orch-specific settings (claude_script, vault_name) in the task-orch config.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read these files before making changes:

- `src/vault_ui/config.py` — current `VaultConfig` and `Config` dataclasses, `load_config()` function
- `src/vault_ui/factory.py` — how config is used at startup (lifespan, watchers, cache)
- `src/vault_ui/api/tasks.py` — how VaultConfig fields are accessed (vault_path, claude_script, vault_name, vault_cli_path, tasks_folder)
- `src/vault_ui/cleanup.py` — how VaultConfig fields are accessed (vault_path, vault_cli_path, name, tasks_folder)
- `config.yaml` — current config format
- `config.yaml.example` — example config

Current task-orch `config.yaml`:
```yaml
vaults:
  - name: Personal
    vault_path: /Users/bborbe/Documents/Obsidian/Personal
    vault_name: Personal
    tasks_folder: "24 Tasks"
    claude_script: claude-obsidian-personal.sh
  - name: Brogrammers
    vault_path: /Users/bborbe/Documents/Obsidian/Brogrammers
    vault_name: Brogrammers
    tasks_folder: "40 Tasks"
    claude_script: claude-obsidian-brogrammers.sh
  - name: Family
    vault_path: /Users/bborbe/Documents/Obsidian/Family
    vault_name: Family
    tasks_folder: "24 Tasks"
    claude_script: claude
```

vault-cli `config list --output json` will return (once implemented):
```json
[
  {
    "name": "personal",
    "path": "/Users/bborbe/Documents/Obsidian/Personal",
    "tasks_dir": "24 Tasks",
    "goals_dir": "23 Goals",
    "daily_dir": "60 Periodic Notes/Daily"
  },
  {
    "name": "brogrammers",
    "path": "/Users/bborbe/Documents/Obsidian/Brogrammers",
    "tasks_dir": "40 Tasks",
    "daily_dir": "60 Periodic Notes/Daily"
  },
  {
    "name": "family",
    "path": "/Users/bborbe/Documents/Obsidian/Family",
    "tasks_dir": "24 Tasks",
    "daily_dir": "60 Periodic Notes/Daily"
  }
]
```

Field mapping from vault-cli JSON to VaultConfig:
- `name` → `VaultConfig.name` (note: vault-cli uses lowercase, task-orch currently uses capitalized)
- `path` → `VaultConfig.vault_path`
- `tasks_dir` → `VaultConfig.tasks_folder`

Orch-specific fields NOT in vault-cli (remain in task-orch config):
- `claude_script` — per-vault Claude wrapper script
- `vault_name` — for obsidian:// URL construction (defaults to capitalized vault name if not set)
- `vault_cli_path` — path to vault-cli binary (global, not per-vault)
</context>

<requirements>
1. Modify `src/vault_ui/config.py`:

   a. Add a function to discover vaults from vault-cli:
   ```python
   def discover_vaults_from_cli(vault_cli_path: str) -> list[dict]:
       """Call vault-cli config list --output json and return parsed vault list."""
       import subprocess
       result = subprocess.run(
           [vault_cli_path, "config", "list", "--output", "json"],
           capture_output=True, text=True, timeout=10,
       )
       if result.returncode != 0:
           raise RuntimeError(f"vault-cli config list failed: {result.stderr.strip()}")
       import json
       vaults = json.loads(result.stdout)
       if not isinstance(vaults, list):
           raise RuntimeError("vault-cli config list returned non-list")
       return vaults
   ```

   b. Change `VaultConfig` — remove default for `vault_path` and `tasks_folder` (they come from vault-cli now), but keep them as fields:
   ```python
   @dataclass
   class VaultConfig:
       name: str
       vault_path: str
       tasks_folder: str
       vault_name: str = ""  # For obsidian:// URLs, defaults to name.title()
       claude_script: str = "claude"
       vault_cli_path: str = "vault-cli"
   ```

   c. Change the config YAML schema. The new format for `config.yaml`:
   ```yaml
   vault_cli_path: vault-cli  # optional, defaults to "vault-cli"
   host: 127.0.0.1
   port: 8000
   vaults:
     personal:
       claude_script: claude-obsidian-personal.sh
     brogrammers:
       claude_script: claude-obsidian-brogrammers.sh
     family:
       claude_script: claude
   ```

   Note: vault keys in config.yaml now match vault-cli names (lowercase). Vaults listed in config.yaml are the ONLY ones task-orch will manage (not all vault-cli vaults).

   d. Update `load_config()`:
   - Parse the YAML to get orch-specific overrides per vault and global settings
   - Read `vault_cli_path` from YAML (default: `"vault-cli"`)
   - Call `discover_vaults_from_cli(vault_cli_path)` to get vault paths/dirs
   - For each vault in config.yaml's `vaults` dict:
     - Find matching vault from vault-cli output by name (case-insensitive)
     - Merge: take `path` → `vault_path`, `tasks_dir` → `tasks_folder` from vault-cli
     - Take `claude_script`, `vault_name` from config.yaml overrides
     - If `vault_name` is empty/missing, default to `name.title()` (e.g. "personal" → "Personal")
     - **Set `vault_cli_path` on every VaultConfig** from the global `vault_cli_path` value (VaultConfig has it per-instance, but the value comes from the global config)
   - If a vault in config.yaml is NOT found in vault-cli output, log a warning and skip it
   - **If the resulting vault list is empty, raise an error** — do not silently start with zero vaults
   - Return the merged Config

   e. Remove the `claude_cli` field from the `Config` dataclass — it is dead code (defined but never read anywhere in the codebase). Also remove the `claude_cli=data.get(...)` line from `load_config()`.

2. Update `config.yaml.example` to show the new slim format.

3. Update `config.yaml` to the new format:
   ```yaml
   vault_cli_path: vault-cli
   host: 127.0.0.1
   port: 8000
   vaults:
     personal:
       claude_script: claude-obsidian-personal.sh
     brogrammers:
       claude_script: claude-obsidian-brogrammers.sh
     family:
       claude_script: claude
   ```

4. The rest of the codebase (factory.py, tasks.py, cleanup.py) should NOT change — they use `VaultConfig` fields which remain the same. The merge happens entirely in `load_config()`.

5. Update CHANGELOG.md with entry under Unreleased or current section.

6. Add a unit test in `tests/test_config.py` for `discover_vaults_from_cli()`:
   - Test success case: mock `subprocess.run` returning valid JSON, verify parsed output
   - Test failure case: mock `subprocess.run` returning non-zero exit code, verify `RuntimeError` raised
   - Test timeout case: mock `subprocess.run` raising `subprocess.TimeoutExpired`, verify it propagates
   - Test invalid JSON: mock `subprocess.run` returning non-JSON stdout, verify `RuntimeError` or `json.JSONDecodeError`

7. Note on `VaultConfig.name` casing: after this change, `VaultConfig.name` will be **lowercase** (matching vault-cli: "personal", "brogrammers", "family"). Code that passes `vault.name` to `vault-cli --vault` already works because vault-cli expects lowercase. Code that uses `vault.vault_name` for obsidian:// URLs gets the title-cased version via the default (`name.title()`).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- VaultConfig interface (field names and types) must NOT change — only how it's populated
- Existing tests must still pass
- If vault-cli is not installed or fails, startup must fail with a clear error message
- The subprocess call must have a timeout (10 seconds)
- vault keys in config.yaml must match vault-cli names (lowercase)
- Empty vault list after merge must raise an error, not start silently
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
