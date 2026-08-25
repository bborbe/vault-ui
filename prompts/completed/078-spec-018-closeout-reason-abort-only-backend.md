---
status: completed
spec: [018-closeout-reason-abort-only]
summary: 'Made the close-out guard abort-only: _closeout_extra_args is now status-aware returning [] for completed, all five completed-targeting API paths accept a missing reason and emit no --reason/--gate-successor flags, six completed-path tests flipped/added, three exact-argv tests updated, abort regressions untouched, CHANGELOG updated'
execution_id: vault-ui-aborted-reason-exec-078-spec-018-closeout-reason-abort-only-backend
dark-factory-version: dev
created: "2026-08-25T09:45:00Z"
queued: "2026-08-25T08:09:39Z"
started: "2026-08-25T08:09:41Z"
completed: "2026-08-25T08:13:24Z"
branch: dark-factory/closeout-reason-abort-only
---

# Make the close-out guard abort-only on the backend

<summary>
- Closing out a task or goal as `completed` no longer requires a reason: all five completed-targeting API paths accept a missing, empty, or whitespace-only reason and return 200.
- A `completed` close-out passes no `--reason` / `--gate-successor` flags to the vault-cli subprocess, even when the request body still carries those fields — they are dropped deterministically.
- The `aborted` path is byte-for-byte unchanged: a non-empty reason plus gate-successor remain mandatory, a blank/whitespace reason is still rejected with HTTP 400 naming `reason` before any subprocess starts, and the abort argv still carries both flag pairs.
- The four existing completed-path `*_without_reason_returns_400` tests are flipped to assert 200 + flag-free argv, one new goal-status completed test is added, and the completed round-trip test becomes a "supplied close-out fields are dropped" test.
- The abort regression tests are left unmodified so the abort-only contract is pinned.
- This is the backend half of spec 018; the frontend half (stop prompting on Complete, bump the cache-buster, CHANGELOG) ships in the next prompt.
</summary>

<objective>
Make the close-out guard in the vault-ui API status-aware so only `aborted` close-outs demand a reason and emit `--reason` / `--gate-successor`, while every `completed` close-out proceeds reason-free and flag-free — matching the sibling vault-cli fix (branch `fix/aborted-reason-completed`) that requires neither `aborted_reason` nor `gate_successor` for completion.
</objective>

<context>
Read `README.md` for project conventions (FastAPI backend in `src/vault_ui/api/`, `uv`-managed Python, pytest via `make test` which deselects the `integration` marker).

Read these files in full before editing:

- `src/vault_ui/api/tasks.py` — all backend changes live here. Key anchors:
  - `_closeout_extra_args` (module-level, just after the `ExecuteCommandRequest` model) — the single source of the close-out 400 contract and the flag spelling. Current signature `def _closeout_extra_args(reason: str | None, gate_successor: str | None) -> list[str]`. It raises `HTTPException(status_code=400, detail="reason is required to close out a task or goal (aborted/completed)")` when the reason is missing/blank/whitespace-only and returns the 4-element list `["--reason", <trimmed>, "--gate-successor", <successor or "none">]`.
  - The five write endpoints that call it: `execute_slash_command` (`complete-task` branch), `update_task_phase` (`phase == "done"` branch), `update_goal_status` (`request.status in ("aborted", "completed")` branch), `execute_goal_command` (`complete-goal` branch), `update_task_status` (`request.status in ("aborted", "completed")` branch). All use the `vault_cli_args[N:N] = ...` splice idiom to insert the flags immediately before the `"--vault", vault_config.name.lower()` tail.
  - The request models `UpdateStatusRequest`, `UpdatePhaseRequest`, `ExecuteCommandRequest` — the `reason: str | None = None` / `gate_successor: str | None = None` fields are NOT changing; only the comments above them drift (requirement 3).
  - The endpoint error-handling idiom: every endpoint ends its `try` with `except HTTPException: raise` first, so the 400 raised by `_closeout_extra_args` propagates cleanly — unchanged by this prompt.
- `tests/test_api.py` — the close-out gate test block (the `# --- close-out reason gate (spec 077) ---` comment, roughly lines 1294-1505) plus the exact-argv tests named in requirement 4. Note the `test_client` fixture already wires `get_vault_cli_client_for_vault` to a MagicMock whose `show_task` works, so the flipped tests reach the subprocess; give `asyncio.create_subprocess_exec` a success mock (`AsyncMock(return_value=mock_proc)` with `mock_proc.returncode = 0`) to get 200.

Relevant coding-plugin guides (in-container paths — the agent runs in the YOLO container, not the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-pydantic-guide.md` — the request models stay plain optional `str | None` fields; do not add a `model_validator` (the 400 detail must remain a clean string, not a Pydantic 422 list).
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-makefile-commands.md` — `make precommit` is the full gate (format + test + lint + typecheck); `make test` runs the unit suite.

**Cross-repo contract (inline, do not re-investigate):** the sibling vault-cli fix (`bborbe/vault-cli` branch `fix/aborted-reason-completed`) makes `completed` require neither `aborted_reason` nor `gate_successor`; `aborted` still requires both. Passing `--gate-successor none` on a `completed` write would write a meaningless `gate_successor: none` into completed frontmatter, so the UI must not pass any close-out flag on completed-targeting paths.
</context>

<requirements>

### 1. Make `_closeout_extra_args` status-aware (abort-only)

In `src/vault_ui/api/tasks.py`, change the signature of `_closeout_extra_args` to take the target status as the first argument and return an empty list for any non-`aborted` target:

```python
def _closeout_extra_args(
    status: str, reason: str | None, gate_successor: str | None
) -> list[str]:
    """Return vault-cli --reason/--gate-successor flags for an ABORTED close-out.

    Only `aborted` demands a non-empty reason — a missing/empty/whitespace-only
    reason raises HTTPException(400) naming `reason` before any write starts.
    `completed` close-outs require neither field (sibling vault-cli fix) and pass
    no close-out flags, so this returns [] for any non-aborted target.
    gate_successor defaults to the literal string "none" on the abort path.
    """
    if status != "aborted":
        return []
    reason_trimmed = (reason or "").strip()
    if not reason_trimmed:
        raise HTTPException(
            status_code=400,
            detail="reason is required to close out a task or goal (aborted/completed)",
        )
    return [
        "--reason",
        reason_trimmed,
        "--gate-successor",
        (gate_successor or "").strip() or "none",
    ]
```

Contract to preserve: the existing 400 `detail` string is kept verbatim (only `"reason" in detail` is load-bearing — every abort test asserts that substring); the abort flag spelling `["--reason", ..., "--gate-successor", ...]` is unchanged; `status != "aborted"` returns `[]` so the splice at every call site becomes a no-op on completed paths.

### 2. Update the five call sites to pass the target status

Keep the existing control-flow guard at each site (the helper is still called on the close-out path only). Pass the target status as the first argument:

- `execute_slash_command` — `complete-task` branch (`vault_cli_args[4:4] = _closeout_extra_args(...)`): pass the literal `"completed"` — `_closeout_extra_args("completed", request.reason, request.gate_successor)`. `task complete` targets `completed`, so this splice inserts nothing and never raises.
- `execute_goal_command` — `complete-goal` branch (`vault_cli_args[4:4] = _closeout_extra_args(...)`): pass the literal `"completed"` — `_closeout_extra_args("completed", request.reason, request.gate_successor)`.
- `update_task_phase` — the `phase == "done"` branch assigns `closeout_flags = (_closeout_extra_args(request.reason, request.gate_successor) if request.phase == "done" else [])` and later splices `status_args[6:6] = closeout_flags`. Pass the literal `"completed"` (phase done auto-writes status `completed`): `_closeout_extra_args("completed", request.reason, request.gate_successor)`. The PHASE subprocess stays exactly as-is (flag-free, as today). Since the result is now always `[]`, the later `status_args[6:6] = closeout_flags` splice is a no-op — keep the splice line (it is harmless and keeps the structure uniform) but do NOT reorder anything.
- `update_goal_status` — `if request.status in ("aborted", "completed"):` branch (`vault_cli_args[6:6] = _closeout_extra_args(...)`): pass the request status — `_closeout_extra_args(request.status, request.reason, request.gate_successor)`. For `aborted` this splices the 4 flags; for `completed` it splices `[]` (no-op).
- `update_task_status` — `if request.status in ("aborted", "completed"):` branch (`vault_cli_args[6:6] = _closeout_extra_args(...)`): pass the request status — `_closeout_extra_args(request.status, request.reason, request.gate_successor)`.

Do NOT change the splice indices, the `"--vault"` tail, the `goal_id`/`task_id` leading-dash guards, the command allowlists, the 10s timeouts, or the `except HTTPException: raise` ordering in any endpoint.

### 3. Update the now-stale comments to abort-only framing

Comments that claim vault-cli rejects `aborted/completed` writes without the close-out fields now describe behavior that no longer exists for `completed`. Update them to abort-only framing (plain comment edits, no code movement):

- The comment above each `reason`/`gate_successor` field in the three request models (`UpdateStatusRequest`, `UpdatePhaseRequest`, `ExecuteCommandRequest`) that reads "vault-cli v0.116.0+ rejects aborted/completed writes without a non-empty `aborted_reason` and `gate_successor`" → restate that only `aborted` close-outs require the fields, `completed` requires neither (sibling vault-cli fix).
- The inline comments at the five call sites that say "vault-cli v0.116.0+ rejects `...` without aborted_reason and gate_successor" / "Close-out gate" → restate the abort-only contract: `completed` targets pass no close-out flags and never demand a reason; `aborted` still raises fail-fast.

These are documentation-only edits — do not rename `aborted_reason`, do not add fields, do not touch the `status`/`phase`/`command` model fields.

### 4. Update `tests/test_api.py` to the new contract

All assertions use the existing mocked-subprocess style: `with patch("asyncio.create_subprocess_exec", AsyncMock()) as mock_exec:` and, for 200-path tests, `AsyncMock(return_value=mock_proc)` with `mock_proc = MagicMock(); mock_proc.returncode = 0; mock_proc.communicate = AsyncMock(return_value=(b"", b""))`. "Flag-free" means BOTH `"--reason" not in mock_exec.call_args.args` AND `"--gate-successor" not in mock_exec.call_args.args`.

4a. FLIP these four completed-path tests (they currently assert 400 + subprocess not called; the new contract makes them 200 + flag-free). Rename each to reflect the new behavior and change the body to the success-mock pattern:

- `test_update_task_status_completed_without_reason_returns_400` → `test_update_task_status_completed_without_reason_succeeds_flag_free`: `PATCH /api/tasks/Test%20Task/status?vault=TestVault` with `{"status": "completed"}` (no reason); assert `status_code == 200` and flag-free argv.
- `test_execute_complete_task_without_reason_returns_400` → `test_execute_complete_task_without_reason_succeeds_flag_free`: `POST /api/tasks/Test%20Task/execute-command?vault=TestVault` with `{"command": "complete-task"}` (no reason); assert `status_code == 200` and flag-free argv.
- `test_execute_complete_goal_without_reason_returns_400` → `test_execute_complete_goal_without_reason_succeeds_flag_free`: `POST /api/goals/Some%20Goal/execute-command?vault=TestVault` with `{"command": "complete-goal"}` (no reason); assert `status_code == 200` and flag-free argv.
- `test_update_phase_done_without_reason_returns_400` → `test_update_phase_done_without_reason_succeeds_flag_free`: `PATCH /api/tasks/Test%20Task/phase?vault=TestVault` with `{"phase": "done"}` (no reason); assert `status_code == 200` and BOTH subprocess calls (phase then status) are flag-free: `all("--reason" not in call[0] and "--gate-successor" not in call[0] for call in mock_exec.call_args_list)`.

4b. ADD one new test (the AC1(e) goal-status flip — no equivalent 400 test exists to flip): `test_update_goal_status_completed_without_reason_succeeds_flag_free`: `PATCH /api/goals/Some%20Goal/status?vault=TestVault` with `{"status": "completed"}` (no reason); assert `status_code == 200` and flag-free argv.

4c. REPLACE `test_closeout_round_trips_explicit_gate_successor` (AC2 — currently asserts the flags round-trip on a completed goal) with `test_completed_drops_supplied_closeout_fields`: `PATCH /api/goals/Some%20Goal/status?vault=TestVault` with `{"status": "completed", "reason": "x", "gate_successor": "y"}`; assert `status_code == 200` and flag-free argv (the supplied close-out fields are dropped, they do NOT reach vault-cli).

4d. UPDATE the three exact-argv tests that assert close-out flags on completed-targeting paths (these are not in the spec's explicit flip list but WILL fail precommit if left — the new contract removes the flags from these argv shapes):

- `test_execute_complete_task_uses_vault_cli` — remove `"--reason", "closing out", "--gate-successor", "none"` from the expected argv tuple; it becomes `("vault-cli", "task", "complete", "Test Task", "--vault", "testvault")`. The request body may keep or drop the reason fields; the argv assertion is what matters.
- `test_execute_goal_command_complete_uses_vault_cli` — same: expected argv becomes `("vault-cli", "goal", "complete", "Some Goal", "--vault", "testvault")`.
- `test_update_phase_done_writes_completed_status` — the status subprocess (`second_call_args`) must now be flag-free: replace the two `_argv_has_pair(second_call_args, "--reason", ...)` / `_argv_has_pair(second_call_args, "--gate-successor", ...)` assertions with absence assertions (`"--reason" not in second_call_args` and `"--gate-successor" not in second_call_args`). Keep the phase subprocess flag-free assertion (`"--reason" not in first_call_args`) and the `("status", "completed")` pair assertion.

4e. LEAVE UNMODIFIED (abort regression lock — AC3): `test_update_task_status_aborted_without_reason_returns_400`, `test_update_goal_status_aborted_without_reason_returns_400`, `test_update_task_status_whitespace_reason_returns_400`, `test_closeout_defaults_gate_successor_to_none`, `test_update_task_status_aborted_with_reason_exact_argv`, `test_non_closeout_status_stays_flag_free`. Do NOT touch the leading-dash, unknown-command, invalid-status-422, or 500/504 abort tests.

### 5. Self-check before finishing

Re-run the `<verification>` commands and confirm they pass. Then walk the spec's acceptance criteria AC1, AC2, AC3 against the change: each of the five completed paths accepts a missing reason with no close-out flag in argv; a completed request carrying reason/gate_successor still emits no flags; the abort tests above are untouched and green.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- This repo's `.dark-factory.yaml` sets `hideGit: true` — never run a bare `git` command in this prompt's verification (it dies with `fatal: not a git repository` in the container and would falsely pass). Use `make precommit`, `uv run pytest`, `grep`, and `find` instead.
- Frozen field names (spec 037): `aborted_reason` and `gate_successor`. No new field, no rename, no backfill of existing completed files.
- Frozen `aborted` contract (spec 037 invariants preserved): non-empty reason + gate-successor mandatory; whitespace-only reason treated as missing; 400 raised fail-fast before any vault-cli subprocess starts. The `aborted` argv must still carry `--reason <text>` and `--gate-successor <name|none>`.
- `completed` contract: no close-out flag (`--reason`, `--gate-successor`) is passed to vault-cli on any completed-targeting path — deterministically, even when the request body carries the fields.
- Do NOT change vault-cli semantics; do NOT add an optional completion-reason/gate-successor capture on Complete; do NOT add a Pydantic `model_validator` to the request models.
- The id arg-injection guards (task/goal ids beginning with `-` rejected with 400) are untouched and still enforced on every status/phase/execute-command path.
- Existing tests keep passing EXCEPT the completed-path flips/updates enumerated in requirement 4. The exact argv shapes and splice indices in requirement 2 are load-bearing — tests assert them.
- Per `docs/dod.md`: changed behavior is covered by tests, no debug output, type annotations present, coverage on changed behavior ≥ 80%.
- This prompt is backend-only. Do NOT touch `src/vault_ui/static/app.js`, `index.html`, `tests/test_closeout_reason_modal.py`, or `CHANGELOG.md` — the frontend prompt in the same spec handles those.
- `make precommit` MUST stay green.
</constraints>

<verification>
```bash
# Full pre-commit gate (format + test + lint + typecheck). `make test` deselects
# the integration suite via pytest addopts -m 'not integration', so this runs
# every test this prompt changes. Must exit 0.
make precommit

# Backend close-out behavior explicitly
uv run pytest tests/test_api.py -v
# Expected: the four flipped completed-path tests + the new goal-status flip
# assert 200 with no --reason/--gate-successor; the abort 400 tests assert 400;
# test_closeout_defaults_gate_successor_to_none still asserts --reason +
# --gate-successor none; test_completed_drops_supplied_closeout_fields asserts
# the supplied fields never reach the argv.

# Confirm every close-out site passes the target status to the guard
grep -n "_closeout_extra_args(" src/vault_ui/api/tasks.py
# Expected: the def line plus five call sites, each with a status argument
# (request.status or the literal "completed").

# Confirm no completed-path test still asserts a close-out flag in argv
grep -n '"completed"' tests/test_api.py | grep -n -- '--reason\|--gate-successor' || echo "no completed-path flag assertions remain"
```

Do NOT run `make test-integration`, `git ...`, or any `docker`/`kubectl`/`gh` command — none are executable in the container and all would produce a false-positive verification pass.
</verification>
