---
status: completed
summary: Vault UI's flag toggle now passes --by operator to vault-cli task set, recording the operator as the flag's writer
execution_id: vault-ui-flag-operator-exec-098-flag-by-operator
dark-factory-version: v0.196.0
created: "2026-09-30T09:13:34Z"
queued: "2026-09-30T09:13:34Z"
started: "2026-09-30T09:16:31Z"
completed: "2026-09-30T09:18:00Z"
---

# Pass --by operator from the Vault UI's flag toggle

<summary>
- A flag now records who set it, so a flag carries its writer instead of reading as anonymous
- The Vault UI's flag toggle is the operator's own surface, but it writes the flag without declaring anyone
- This change makes the toggle declare the operator, so a flag set through the UI is recorded as the operator's rather than as unattributed
- Clearing the flag is unchanged
- No other Vault UI write is affected
</summary>

<objective>
Make the Vault UI's flag toggle pass `--by operator` to `vault-cli`, so a flag the operator sets through the UI is recorded with its writer (`flag_set_by: operator`) rather than as an unattributed write.
</objective>

<context>
Project conventions: Python managed by `uv`, FastAPI, `ruff` for format and lint, `mypy` for type checking, `pytest` for tests. `make precommit` runs sync + format + test + check. Read `docs/dod.md` for the project's Definition of Done.

Read these before writing:

- `src/vault_ui/vault_cli_client.py` — `set_field`, which builds the `vault-cli task set` argv, and `clear_field`.
- `src/vault_ui/api/tasks.py` — `update_task_flag`, the `PATCH /tasks/{task_id}/flag` handler: it calls `set_field` for the set path and `clear_field` for the clear path.
- `tests/test_task_reader.py` — `test_set_field_success`, which asserts the argv the client builds.
- `tests/test_api.py` — `test_update_task_flag_sets_field` and `test_update_task_flag_clears_field`, which assert how the handler calls the client.

**The contract this serves** (it lives in the `vault-cli` repository and is not readable from here, so it is stated inline): `vault-cli task set <name> flag true` accepts `--by <actor>` and records it as `flag_set_by`. A write that declares no actor records `flag_set_by: unknown` — in `vault-cli`'s own wording, an operator-set flag is *"recorded as unattributed rather than as an approval"*. The field is the durable record of that distinction. The consumer that would enforce it is **not** in this repository, and the currently installed `/supervisor:open --flagged` still selects on `flag: true` alone — so do **not** expect an unattributed flag to be refused today; this change writes the record, it does not activate the enforcement. `--by` is accepted only for the `flag` key; `vault-cli` rejects it for any other key.
</context>

<requirements>
1. In `src/vault_ui/vault_cli_client.py`, give `set_field` an optional keyword parameter `by: str | None = None`. When `by` is not `None`, append `"--by", by` to the argv — after the value and before `--vault`; the rest of the argv order is unchanged.
   - When `by` is `None`, the argv must be byte-identical to today's, so every existing caller is unaffected.
   - `clear_field` is unchanged: `vault-cli task clear` takes no `--by`.

2. In `src/vault_ui/api/tasks.py`, `update_task_flag` passes `by="operator"` on the set path only:
   `await client.set_field(task_id, "flag", "true", by="operator")`.
   The clear path (`clear_field`) is unchanged. Update the docstring to say why: the toggle is the operator's own surface, and `--by operator` is what records the flag as the operator's rather than as unattributed.

3. Tests:
   - `tests/test_api.py` — update `test_update_task_flag_sets_field`'s assertion to include the new keyword, so it reads `assert_awaited_once_with("Test Task", "flag", "true", by="operator")`. `test_update_task_flag_clears_field` is unchanged.
   - `tests/test_task_reader.py` — strengthen `test_set_field_success` to assert the exact argv (`args == ("vault-cli", "task", "set", "task-1", "phase", "in_progress", "--vault", "TestVault")`), which pins the no-`by` case as byte-identical rather than merely `--by`-free, and add a sibling test that `set_field(..., by="operator")` asserts the index relationship — `"--by"` and `"operator"` immediately after the value and before `--vault` — not merely membership, since requirement 1 mandates that position and nothing else pins it.

4. Add a bullet to `CHANGELOG.md`'s `## Unreleased` section — create that section below the intro paragraph and above the top version heading if it is absent. Follow the repo's existing entries and the coding plugin's `changelog-guide.md`: a conventional prefix (`feat:` fits — the flag gains a written field) and `## Unreleased` first. Describe the user-visible effect: a flag set from the board now records the operator as its writer, so the flag carries its provenance instead of reading as unattributed. `docs/dod.md` (the repo's configured validation prompt) requires this.

5. Add `vault-cli >= 0.156.0` to `README.md`'s `## Prerequisites`, which today lists Python, uv, the Claude CLI and the vault but not `vault-cli` at all. The floor is load-bearing: below it the flag toggle returns HTTP 500 on `unknown flag: --by`.

6. Before you finish, re-run `<verification>` and confirm it passes, then walk each requirement against the change: confirm the no-`by` argv is unchanged, that the handler passes `by="operator"` on the set path only, that both test files were updated, that the changelog entry exists, and that the README prerequisite was added.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Do NOT change any other caller of `set_field`. `phase`, `claude_session_id`, `assignee` and the rest must keep writing without `--by` — `vault-cli` rejects `--by` for any key other than `flag`.
- Do NOT change the flag's rendering, its sorting, or the board's behaviour. This change is the argv only.
- Follow the project's existing async / subprocess patterns; do not introduce a second way to build the argv.
- `--by` exists only in `vault-cli` >= 0.156.0, and this repo does not pin a `vault-cli` version (`vault_cli_path` is the bare `vault-cli` binary). State the requirement in your final message: the deployed `vault-cli` must be >= 0.156.0, or the flag toggle returns HTTP 500 on `unknown flag: --by`.
- Out of scope: `vault-cli`'s `/plan-day` writer (`commands/session-close.md` § Phase 4.5), the other `--by operator` writer named in the `vault-cli` v0.156.0 release notes. It lives in that repository and ships in its own release.
</constraints>

<verification>
Run `make precommit` -- must pass.

`make precommit` does not read the changelog, so check it directly: `grep '^## ' CHANGELOG.md | head -1` must print `## Unreleased`. Before the change it prints the top version heading, so this is a real check, not a formality.
</verification>
