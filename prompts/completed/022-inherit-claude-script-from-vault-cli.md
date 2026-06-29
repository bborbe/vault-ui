---
status: completed
summary: Moved claude_script source from config.yaml overrides to vault-cli JSON output, updated config files and tests accordingly.
container: vault-ui-022-inherit-claude-script-from-vault-cli
dark-factory-version: v0.54.0
created: "2026-03-12T21:30:00Z"
queued: "2026-03-12T20:39:51Z"
started: "2026-03-12T20:39:52Z"
completed: "2026-03-12T20:40:41Z"
---

<summary>
- Vault-specific Claude script paths are read automatically from the central vault registry
- The project configuration file no longer needs per-vault script overrides
- Vault entries in the project config require only a name to be managed
- If the vault registry does not specify a script, the standard claude command is used
- Existing configurations with manually set script paths continue to work without error
</summary>

<objective>
Vault-specific Claude script paths are discovered automatically from vault-cli rather than repeated in vault-ui's config, reducing manual duplication and keeping the two systems in sync.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/config.py` — find `VaultConfig` dataclass and `load_config` function. In `load_config()`, find the vault construction loop where `claude_script` is set from `overrides.get("claude_script", "claude")`.
Read `config.yaml` — current format has per-vault `claude_script` overrides.
Read `config.yaml.example` — may also reference `claude_script`.
Read `tests/test_config.py` — find tests that reference `claude_script`, especially `test_load_config_reads_vaults`.
</context>

<requirements>
1. In `load_config()` in `src/vault_ui/config.py`, change the `claude_script` source in the `VaultConfig` constructor call:

```python
# Before
claude_script=overrides.get("claude_script", "claude"),
# After
claude_script=cli_vault.get("claude_script") or "claude",
```

The `or "claude"` handles both `None` (key absent) and `""` (empty string) from vault-cli JSON output.

2. Update `config.yaml` to remove `claude_script` from vault overrides. If a vault entry has no remaining overrides, it can be an empty dict or null. The vaults section is still needed to specify which vaults vault-ui should manage:

```yaml
vault_cli_path: vault-cli
host: 127.0.0.1
port: 8000
vaults:
  personal:
  brogrammers:
  family:
```

3. Update `config.yaml.example` to match — remove `claude_script` examples, add a comment that it's inherited from vault-cli.

4. Update `tests/test_config.py`:
   - In `test_load_config_reads_vaults`, move `claude_script` from the config YAML override into the mock vault-cli JSON output dict, and verify it's read from there
   - Tests that previously set `claude_script` via YAML overrides should verify it comes from the CLI output instead
   - Add a test that verifies fallback to "claude" when `claude_script` is absent from CLI output

5. In `load_config()`, the loop already handles empty/null overrides via `overrides = overrides or {}`. Ensure vault entries with no overrides (empty/null values) still create VaultConfig entries. No code change expected — just verify the existing guard in the `for vault_key, overrides` loop works.

6. The `overrides.get("claude_script")` call is simply removed — it is no longer read. If someone's config.yaml still has `claude_script` under a vault, it is harmlessly present as an unused YAML key (no error, no effect).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- All file paths are repo-relative
- `vault_name` override must still work (it's the only remaining per-vault override)
- The old `claude_script` key in config.yaml overrides is simply not read anymore — it is harmlessly ignored, not rejected
</constraints>

<verification>
Run `make precommit` — must pass.
</verification>
