---
status: completed
summary: Updated README.md with Prerequisites section, correct make sync target, and config.yaml-based configuration documentation
container: vault-ui-014-h-fix-readme
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:32:47Z"
completed: "2026-03-11T21:33:30Z"
---
<summary>
- README install command matches actual Makefile target (sync, not install)
- Configuration section documents config.yaml instead of nonexistent environment variables
- All VaultConfig fields are documented including claude_script and vault_cli_path
- Prerequisites section helps new contributors set up without trial and error
- No changes to Features, Task Format, or License sections
</summary>

<objective>
README.md accurately reflects the project's actual setup: correct make targets, config.yaml-based configuration with all fields, and documented prerequisites.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `README.md` — the file to update.
Read `Makefile` — to verify target names (correct target is `sync`, not `install`).
Read `config.yaml.example` — the actual config format to document.
Read `src/vault_ui/config.py` — `VaultConfig` and `Config` dataclasses for field names.
</context>

<requirements>
1. Add a Prerequisites section before Installation:
   ```markdown
   ## Prerequisites

   - Python 3.12+
   - [uv](https://docs.astral.sh/uv/) package manager
   - [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) (`claude` command)
   - An Obsidian vault with tasks in frontmatter format
   ```

2. In the Development section (~line 36), change `make install` to `make sync`:
   ```markdown
   make sync        # Install dependencies
   ```

3. Replace the Configuration section (~lines 44-52). The app uses `config.yaml`, not environment variables:
   ```markdown
   ## Configuration

   Copy the example config and edit vault paths:
   ```bash
   cp config.yaml.example config.yaml
   ```

   **Top-level fields:**
   - `claude_cli` - Claude CLI command (default: `claude`)
   - `host` - Server host (default: `127.0.0.1`)
   - `port` - Server port (default: `8000`)

   **Per-vault fields** (under `vaults:`):
   - `name` - Display name for the vault
   - `vault_path` - Absolute path to the Obsidian vault
   - `vault_name` - Vault name for `obsidian://` URLs
   - `tasks_folder` - Folder containing task files (e.g., `"24 Tasks"`)
   - `claude_script` - Script to run Claude sessions (default: `claude`)
   - `vault_cli_path` - Path to vault-cli binary (default: `vault-cli`)
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Do NOT change the Features, Task Format, or License sections
- Keep the README concise — no more than ~90 lines total
</constraints>

<verification>
Run `grep -c "make install" README.md` — must return 0.
Run `grep -c "make sync" README.md` — must return at least 1.
Run `grep -c "config.yaml" README.md` — must return at least 1.
</verification>
