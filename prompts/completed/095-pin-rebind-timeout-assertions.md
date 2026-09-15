---
status: completed
summary: Pinned the three re-bind timeout tests to their own clause's lead phrase (adding a `Re-bind write` lead phrase to the write-clause warning) so a misattributed timeout fails the suite instead of passing
execution_id: vault-ui-rebind-exec-095-pin-rebind-timeout-assertions
dark-factory-version: v0.193.0
created: "2026-09-15T10:35:00Z"
queued: "2026-09-15T08:48:38Z"
started: "2026-09-15T08:48:48Z"
completed: "2026-09-15T08:51:14Z"
---

# Pin the re-bind timeout tests to the message that identifies the clause

<summary>
- Three timeout tests now fail if the wrong timeout is reported, instead of passing on any timeout at all
- No behaviour changes — this is a test-precision fix only
- The re-read timeout test can no longer be satisfied by a lock-acquisition message
- The lock-acquisition test can no longer be satisfied by any unrelated warning naming the same task
</summary>

<objective>
Three of the re-bind timeout tests match on substrings that more than one warning satisfies, so they would still pass if a timeout were attributed to the wrong clause — the exact misattribution the separate `TimeoutError` handlers exist to prevent. Pin each assertion to the phrase that identifies its clause.
</objective>

<context>
Read `README.md` and `docs/dod.md` for project conventions (this repo has no `CLAUDE.md`).

Read `src/vault_ui/cleanup.py` `_rebind_empty_session_ids`. It emits three distinct timeout warnings, and the tests below must each pin to exactly one:

- the lock-acquisition clause — message begins `Lock acquisition for task …`
- the re-read `wait_for` around `client.show_task`
- the write `wait_for` around `proc.communicate()`

Copy each assertion string from the actual `logger.warning(...)` format string in the source — do not invent wording. If two of those messages do not currently share a distinguishing prefix, that is part of what this change fixes: give each one a short unambiguous lead phrase (e.g. `Re-bind re-read …`, `Re-bind write …`) and pin the tests to it.

Read `tests/test_cleanup.py` — the three tests are `test_rebind_read_timeout...` (~line 2486), the subprocess-timeout test asserting `kill`/`wait` (~line 2378), and the lock-acquisition-timeout test (~line 2077).
</context>

<requirements>
1. **Re-read timeout test** (~`tests/test_cleanup.py:2486`): it currently asserts `any("slow-read" in r.message and "timed out" in r.message ...)`. Both substrings are also present in the lock-acquisition warning (`Lock acquisition for task %s … timed out after %ds`), so the test passes even when the re-read timeout is misattributed. Pin it to the re-read message's own lead phrase.

2. **Subprocess-timeout test** (~`tests/test_cleanup.py:2378`): keep the existing `kill.assert_called_once()` / `wait.assert_awaited_once()` assertions — they are the real contract — and additionally pin its warning assertion to the write clause's lead phrase.

3. **Lock-acquisition-timeout test** (~`tests/test_cleanup.py:2077`): it asserts only that a warning names the task and the vault, which any of the three warnings satisfies. Pin it to `Lock acquisition`.

4. If requirement 1-3 need distinguishing lead phrases that do not yet exist, add them to the corresponding `logger.warning(...)` calls in `src/vault_ui/cleanup.py`. Keep each message's existing arguments and meaning; only make the lead phrase unambiguous.

5. Do not change any control flow, any timeout value, or any handler ordering. This prompt changes log wording and test assertions only.

6. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Behaviour must not change: same clauses, same ordering (`TimeoutError` → `FileNotFoundError` → `Exception`), same timeouts, same kill-and-reap.
- Do NOT relax any existing assertion. Each test must end strictly more specific than it began.
- Existing tests must still pass.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm each test pins a distinct clause:
- `grep -q 'Lock acquisition' tests/test_cleanup.py` -- must exit 0 (the acquisition test names its clause)
- `grep -c 'Lock acquisition' src/vault_ui/cleanup.py` -- must print `1` (exactly one source message carries this lead phrase, so the test pins one clause and not a family)
</verification>
