---
status: completed
summary: 'Made the vaults: block optional — an absent or empty block now serves every vault-cli vault with an existing tasks folder, task-less or path-less vaults are skipped with a naming warning on both paths instead of raising, the explicit block still filters and overrides vault_name, and the no-vaults guard is preserved with a reworded message'
execution_id: vault-ui-derive-vault-list-exec-097-derive-vault-list-from-vault-cli
dark-factory-version: v0.193.0
created: "2026-09-18T15:43:57Z"
queued: "2026-09-18T15:43:57Z"
started: "2026-09-18T16:39:29Z"
completed: "2026-09-18T16:41:13Z"
branch: dark-factory/097-derive-vault-list-from-vault-cli
---

# Derive the vault list from vault-cli when no `vaults:` block is configured

<summary>
- An absent or empty `vaults:` block stops being a startup failure
- The board serves every vault-cli vault that actually has a tasks folder
- A vault-cli vault with no tasks folder is skipped with a warning instead of crashing startup
- A vault whose tasks folder is missing on disk is skipped with a warning too
- An explicit `vaults:` block keeps working exactly as today: it still filters, and still overrides the display name
- Removing the block surfaces vaults that were never listed in it, rather than silently narrowing the board
- The existing "no vaults at all" startup failure is preserved for the genuine misconfiguration it was written for
- The README and the example config no longer read as if the block were mandatory
</summary>

<objective>
Make vault-ui's `vaults:` block optional. Today the block is a second, hand-maintained copy of vault-cli's vault list, and it is mandatory: an absent or empty block fails startup outright, so it cannot simply be deleted. After this change an absent or empty block means "every vault-cli vault that has a tasks folder" — where *has a tasks folder* means the `tasks_dir` field is set **and** that folder exists on disk, so both conditions are in scope — while an explicit block keeps its current filtering role. This removes the drift hazard that took the board down on 2026-09-18, when vault-cli's config was cleaned up and vault-ui's stale copy still named the removed vaults.
</objective>

<context>
Project conventions: Python 3.12+ managed by `uv`, FastAPI, `ruff` for format/lint, `mypy` for type checking, `pytest` for tests. `make precommit` runs sync + format + test + check. Tests must not make real subprocess, network, or Claude API calls — mock external dependencies. Read `docs/dod.md` for the project's Definition of Done.

The 2026-09-18 incident this change removes the cause of: another session correctly removed `tasks_dir`/`goals_dir` from the `trading`, `data` and `gaming` vault blocks in vault-cli's config (none of those vaults has a tasks or goals folder on disk). vault-ui's own `config.yaml` still listed those three names, and `load_config` indexes `cli_vault["tasks_dir"]` directly at `src/vault_ui/config.py:158`. Result: `KeyError: 'tasks_dir'` at startup, crash-looping the launchd service until a human removed the three names by hand.

Measured state of `vault-cli config list` on 2026-09-18 — 13 vaults, 8 with a `tasks_dir`, 5 without:

- **8 with a `tasks_dir`, and every one of those folders exists on disk** (so this change drops nothing today): `personal`, `brogrammers`, `family`, `openclaw`, `octopusagent`, `dataassistant`, `boss`, `starcitzen`.
- **5 without a `tasks_dir`**: `trading`, `data`, `gaming`, `brogrammersassistant`, `personalassistant`. These are the vaults the skip path must handle — it must skip all five, not three.

The operator's live XDG config — host-side `~/.config/vault-ui/config.yaml`, **not mounted in this container**, so treat the list below as given and do not try to read it — names only 6 vaults: `personal`, `brogrammers`, `family`, `openclaw`, `octopusagent`, `dataassistant`. `boss` and `starcitzen` are in vault-cli, have tasks folders, and were simply never added. So the block is a **filtered subset**, not a faithful copy, and removing it **widens** the board by those two. That widening is intended, not a regression.

(The repo-root `config.yaml` in this checkout is a *different, legacy* file with its own vault list. It is not the file the 6-vault figure describes and not the file this change targets — do not read it to check the claim above.)

Note the distinction the change must preserve: the block is `data.get("vaults", {}) or {}` — absent and empty are the same value after that expression, and both must mean "all vault-cli vaults". Only a block with at least one key filters.

Files to read before writing:
- `src/vault_ui/config.py` — `load_config` (the `for vault_key, overrides in vault_overrides.items():` loop at ~lines 143-164, the direct `cli_vault["tasks_dir"]` index at ~line 158, the `if not vaults` raise at ~lines 166-170), `discover_vaults_from_cli`, and the `VaultConfig` dataclass at ~lines 14-25
- `tests/test_config.py` — the existing `load_config` tests and their `_make_side_effect` helper, for style
- Note: `CLAUDE.md` is **gitignored** in this repo (`.gitignore:14` — `/CLAUDE.md`), so it is not present in this container and must not be edited or created. It carries the Key Design Decision "Config from vault-cli" that motivates this change; its update is handled outside dark-factory.
- `prompts/completed/020-inherit-vault-config-from-vault-cli.md` — the prompt that made the `vaults:` block a **filter** ("Vaults listed in config.yaml are the ONLY ones task-orch will manage"). This change relaxes that decision rather than reversing it, so read it before touching the loop — it is the reason requirement 4 insists the filter keeps working.
- `docs/launchd-service.md` — deliberately left alone by this change: it never describes the `vaults:` block as required (its three `vaults` occurrences are the watcher log-line example at line 102, the reload-troubleshooting heading at line 182, and the prose at line 196), so it needs no edit. Do not edit it.
</context>

<requirements>
1. **An absent or empty `vaults:` block resolves to every vault-cli vault that has a tasks folder.**
   In `load_config` (`src/vault_ui/config.py`), when `vault_overrides` is empty, iterate the vault-cli vaults instead of the overrides. Build the same `VaultConfig` objects as today, from the same **six** fields — `name=vault_key`, `vault_path=cli_vault["path"]`, `tasks_folder=cli_vault["tasks_dir"]`, `vault_name`, `claude_script=cli_vault.get("claude_script") or "claude"`, and `vault_cli_path=vault_cli_path` (plus `session_project_dir`, as today) — with `vault_name` defaulting to the vault key title-cased, the same default the explicit path already applies at lines 150-152. `vault_cli_path` in particular must carry the configured value through: defaulting it would silently revert a non-default install to `"vault-cli"`.
   On the fallback path there is no block key, so `name` comes from `cli_vault["name"]` — vault-cli's own spelling, which is also the spelling a block key uses. Write the requirement 5 parity test with that same casing: the explicit path derives `name` from the block key, so the two are equal only when the casing matches, and a test written with mismatched casing would pass while the real-world "indistinguishable" claim stays false.
   Keep the two paths behaviourally identical: a vault reached through the fallback must be indistinguishable in the resulting `Config` from the same vault named explicitly in a block with no overrides. Requirement 5 pins this with a test rather than leaving it as a claim.
   The one deliberate difference is **list order** — the explicit path follows YAML insertion order, the fallback follows vault-cli's list order. State it in your final message: `factory.run_config_reload_loop` compares vault name lists in order and `factory.py` reads `config.vaults[0].vault_cli_path`, so order is observable even though each vault's `VaultConfig` is identical.
   Do not duplicate the `VaultConfig` construction into two divergent copies — factor the per-vault construction so both paths call it, so a future field addition cannot land in one path only.

2. **A vault-cli vault with no `tasks_dir`, or whose tasks folder is missing on disk, is skipped with a warning instead of raising `KeyError`.**
   Two conditions, both producing a skip plus a `logger.warning` that **names the vault**:
   - `tasks_dir` absent or empty in the vault-cli entry — today this is the `KeyError: 'tasks_dir'` at line 158. Read it with `.get()` and treat a missing or empty value as "skip".
   - the tasks folder does not exist on disk — resolve `Path(vault_path) / tasks_dir` and skip when it is not a directory. Use `pathlib`, matching the existing house style (`config.py` already imports `Path`; `src/vault_ui/api/tasks.py:625` resolves the same pair the same way) — do not introduce an `os` import for this.
   Match the existing load-time warning style (`logger.warning("Vault '%s' not found in vault-cli output, skipping", vault_key)`) and make the two reasons distinguishable in the message, so a reader can tell a config-shape skip from an on-disk skip without guessing.
   This applies to **both** paths — a vault named explicitly in a `vaults:` block must be skipped the same way, not only a vault reached through the fallback. A block naming a task-less vault must degrade, not crash.
   The skip must not raise and must not abort the loop: the remaining vaults still load.
   The same "do not KeyError on a vault-cli entry" rule applies to `cli_vault["path"]` (~line 157). It is a direct index today, and this change widens the loop that runs it: before, it ran only for vaults named in a block; now it runs for **every** vault-cli vault, so a `path`-less entry the block used to filter out would crash startup — the same failure class this prompt removes. Read it defensively too; this is mandatory, not optional. A `path`-less entry crashing startup is exactly what this change exists to prevent, and an escape hatch here would let an untested branch ship. Requirement 5 adds the matching test.

3. **Keep the `if not vaults` guard for the misconfiguration it was written for.**
   Leave the `RuntimeError("No vaults configured after merging with vault-cli output. …")` raise at lines 166-170 in place and reachable. It now means what it says — no vault survived the merge, because vault-cli returned nothing usable or every vault was skipped — which is still a real misconfiguration worth failing loudly on. Do not delete it, and do not weaken it into a warning.
   Its second sentence — "Check config.yaml vaults section and that vault-cli is available." — is now half-stale: an absent block is a legitimate configuration, and the new skip reasons are a vault-cli entry with no `tasks_dir` and a tasks folder missing on disk. Reword it to name those two, or the loud failure points the operator at the wrong cause.
   An absent block against a healthy vault-cli must therefore **not** reach it.

4. **An explicit `vaults:` block keeps its current role: it filters, and it overrides the display name.**
   When `vault_overrides` is non-empty, the loop must still iterate the overrides, so a vault-cli vault not named in the block stays off the board — that is the block's remaining legitimate purpose. The `vault_name` override (`overrides.get("vault_name")`) must still take effect, falling back to the title-cased key when unset.
   A block key that vault-cli does not know must keep the existing skip-with-warning at lines 146-148.

5. **Tests.** Add to `tests/test_config.py`, following the existing `tmp_path` + `patch("subprocess.run", side_effect=_make_side_effect(...))` style. Each test that pins a **new** behaviour below must **fail against the current code**, not merely pass against the new code. Two of them instead guard behaviour this change must **preserve** and therefore pass today — the 2-entry filter test and the `vault_name` override test. State in your final message, per test, which behaviour it pins and whether it is a regression test (fails today) or a preserved-behaviour guard (passes today).
   - an **absent** `vaults:` block against a vault-cli list of several vaults → `load_config` returns all of them, and does not raise. This is the test that fails today with `RuntimeError`.
   - an **empty** `vaults:` block (the `vaults:` key present, no entries) → same outcome as absent.
   - a vault-cli entry with **no `tasks_dir`** → skipped, not raised, and the other vaults still load. Drive this through an **explicit** block that names the task-less vault alongside a healthy one (`vaults:\n  personal:\n  trading:\n`) — that is the shape the 2026-09-18 incident actually hit, and it is the one that fails today with `KeyError`. (With an absent block the same fixture would instead trip the `if not vaults` guard, so the test would pin the wrong line.)
   - a vault whose `tasks_dir` is set but whose **folder does not exist on disk** → skipped with a warning, others still load.
   - a vault-cli entry with **no `path`** → skipped with a warning, others still load. This pins requirement 2's mandatory defensive read: the fallback now runs `cli_vault["path"]` for every vault-cli vault, so without this test that branch is unexercised and a `path`-less entry would still crash startup.
   - an explicit **2-entry** `vaults:` block against a vault-cli list of more vaults → exactly those 2 load, proving the filter still filters.
   - an explicit block carrying a `vault_name` override → the resulting `VaultConfig.vault_name` is the override value, not the title-cased key.
   - **two-path parity** — build the *same* vault twice, once through an absent block and once through an explicit block naming it with no overrides, and assert the resulting `VaultConfig` objects are **equal**. This is the contract test for requirement 1's central claim; without it, "indistinguishable" is an assertion with nothing pinning it, and a field that lands on only one path (the `vault_cli_path` trap requirement 1 names) ships green.
   - assert the warning names the skipped vault (e.g. via `caplog`), so requirement 2's observability is pinned rather than assumed.
   - **Existing tests that this change breaks are part of this requirement.** `tests/test_config.py` builds configs whose `vault_path` values are fake (`/some/path/Personal`) and whose tasks folders therefore do not exist on disk. With requirement 2's on-disk check in place those vaults are skipped and those tests fail. Update them to use real directories under `tmp_path` (create the tasks folder in the test) rather than weakening the on-disk check — the check is the requirement, the fake paths are the stale part.
     The blast radius is every `load_config` test in the file that feeds a synthetic `path` through `_make_side_effect` — roughly a dozen, from `test_load_config_reads_vaults` through `test_get_vault_returns_correct_vault`. Several of those assert only on `host`/`port`, so they will fail with `RuntimeError` rather than a vault-count mismatch and read as unrelated breakage. `test_load_config_reads_vaults` also asserts `vault.vault_path == "/some/path/Personal"`; that assertion changes with the fixture. Factor one helper — e.g. `_cli_vault(tmp_path, name)` returning a cli entry whose `path` is a real `tmp_path` directory with its tasks folder created — rather than repeating the mkdir in each test.
     Re-run the whole suite and confirm every previously-passing test still passes.
   - no real subprocess, network, or Claude API calls.

6. **Satisfy the repo's Definition of Done obligations.** `docs/dod.md` is the configured validation prompt; report any unmet criterion as a blocker.
   - **CHANGELOG:** `CHANGELOG.md`'s top section is `## v0.67.5` with no `## Unreleased`. Add a `## Unreleased` section above it with a single `- fix:` bullet describing this change, in the style of the existing entries (one paragraph, plain language, stating what the user-visible behaviour becomes). This is the section the release watcher renames when it cuts a version — every released section in this file was written from it — and `docs/dod.md` requires it.
   - **Docs:** `README.md`'s Configuration section documents the per-vault fields "under `vaults:`" (~line 108) without saying the block is optional, and `config.yaml.example`'s comment above the block ("List vaults vault-ui should manage.") reads as a requirement. Update both: state that the `vaults:` block is optional, that an absent or empty block serves every vault-cli vault that has a tasks folder, and that an explicit non-empty block still filters and still overrides `vault_name`. `docs/dod.md` requires the README update for a configuration change. Do **not** edit `docs/launchd-service.md` for this — it never describes the block as required; its three `vaults` lines (the watcher log example at line 102, the reload-troubleshooting heading at line 182, and the prose at line 196) all match whether or not any doc was updated.

7. **Self-check before finishing.** Re-run `make precommit` and confirm it passes. Then walk each numbered requirement above against the change and confirm the three negative cases hold: an explicit block still filters, a genuine "no vaults at all" configuration still raises, and a task-less vault named explicitly in a block is skipped rather than crashing.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass (updating their fake paths per requirement 5 is expected; deleting or skipping them is not)
- No new dependencies
- Repo-relative paths only — no absolute or home-relative paths
- Out of scope: changing `vault-cli config list`'s output shape or adding new vault-cli fields; any change to the rename/degrade reload behaviour shipped in v0.67.5; re-adding `tasks_dir` to vault-cli's config for the task-less vaults; the stale brew cask
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
grep -n -i 'optional' README.md
grep -n -i 'vaults' config.yaml.example
```

The first must print `## Unreleased`. The second must print `>= 1` — the count of non-empty lines in the new section. The third must print at least one hit **adjacent to the `vaults:` block** — README.md has zero `optional` hits today, so this one fails before the edit and passes after, which is what makes it a check rather than a formality. The fourth only locates the block in `config.yaml.example`; that file already contains three unrelated `optional` hits (lines 5, 13, 16), so it proves nothing on its own — read the lines around the block and confirm the comment above it no longer reads as a requirement. Do **not** grep `docs/launchd-service.md` for this — its only `vaults` line is the watcher log example at line 102, which matches whether or not any doc was updated.

**3. The new tests actually fail against the old behaviour.** For each of the two central tests — absent block, and missing `tasks_dir` — confirm by inspection that it exercises the exact line the old code raised on (`if not vaults` and `cli_vault["tasks_dir"]` respectively), so it is a real regression test rather than a test that would have passed before.

**4. The negative cases hold.** Read `src/vault_ui/config.py` and confirm by inspection that: an explicit non-empty block still iterates the overrides and still filters; the `if not vaults` raise is still present and still reachable; and the task-less skip applies on the explicit path too, not only the fallback.

**5. Self-check.** Before you finish, re-run `make precommit` and confirm it passes; then walk each of requirements 1-6 against the change and state in your final message which requirement each verification step covers and which test pins it.
</verification>
