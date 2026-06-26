---
status: verifying
approved: "2026-06-26T13:19:52Z"
generating: "2026-06-26T13:28:52Z"
prompted: "2026-06-26T13:28:52Z"
verifying: "2026-06-26T13:55:06Z"
branch: dark-factory/add-log-level-env-var-and-stream-vault-cli-subprocess
---

## Summary

- Add a `LOG_LEVEL` env var to task-orchestrator (DEBUG / INFO / WARNING / ERROR). Read at startup, applied to both Python's root logger AND uvicorn's `log_level=`. Default `INFO` (current behaviour byte-identical when unset).
- For the long-running `start_vault_cli_session` subprocess call (the 60–120s+ `vault-cli task work-on --mode headless` path that runs the entire `/vault-cli:work-on-task` Claude skill), stream stdout/stderr line-by-line as they arrive — at any log level — instead of buffering in `communicate()`. Stdout still needs to be captured for JSON parsing at end; the streaming is an additive log feed.
- Other shorter vault-cli subprocess calls (`task list`, `task show`, `task set`, cleanup loops) keep their existing `communicate()` semantics — they're fast and don't need streaming.
- Document the env var in the launchd plist with a commented-out `LOG_LEVEL` entry showing valid values.

## Problem

Today task-orchestrator's `start_vault_cli_session` (`api/tasks.py:55-80`) spawns a subprocess via `asyncio.create_subprocess_exec(..., stdout=PIPE, stderr=PIPE)` and blocks on `proc.communicate()`. The subprocess runs `vault-cli task work-on --mode headless` which in turn runs `claude --print -p /vault-cli:work-on-task <task.md>` — the full work-on-task skill non-interactively. Wall time: 60–120s typical, 3+ min observed today.

`proc.communicate()` buffers stdout/stderr until the subprocess exits. So for the entire ~2-3 min wait:
- The frontend modal (`app.js:1031`) shows `⏳ Starting...` then times out at ~2min, button resets to `▶ Start`.
- task-orchestrator's log emits exactly two lines: `run_task called` (start) and `Returning session response` (after subprocess exits). Nothing in between.
- When the headless claude hangs (real today: PID 35294 ran for ~5min before manual kill), the only diagnostic is `ps aux | grep claude`, then reading the claude session jsonl directly to see which tool call is stuck.

Today's verification gate on the [[Automatically Name Claude Sessions Started by Task Orchestrator]] goal sits stuck for exactly this reason: clicked Start, modal closed, button reset, no log signal — we cannot tell if vault-cli's headless `-n` plumbing is actually firing without manual archaeology.

Python's `logging.basicConfig(level=logging.INFO)` is hard-coded in `__main__.py:23-27`; no env var lookup. uvicorn's `log_level="info"` is hard-coded at `__main__.py:34`. No way to crank verbosity without code edit + restart.

## Goal

After this work, two diagnostics are routinely available without code changes or archaeology:

1. **`LOG_LEVEL=DEBUG` env var** lets the operator restart task-orchestrator (`launchctl kickstart`) and immediately see every router log + uvicorn request-trace + subprocess-stream line in `/tmp/task-orchestrator.log`. At default `INFO` the verbosity is byte-identical to today.
2. **Streaming subprocess output** for the long-running headless `task work-on` subprocess — operator sees the headless claude's tool calls arrive live (e.g. "now calling `mcp__semantic-search`", "now calling `mcp__atlassian__getJiraIssue`") instead of a 2-3 min black box. Works at any log level (the stream itself logs at DEBUG; at INFO and above the line-arrival is unlogged but the stream still flushes promptly so the subprocess doesn't block on a full pipe buffer).

This unblocks the verification gate on the naming goal AND gives a durable diagnostic for any future "Start should work but doesn't" failure mode.

## Non-goals

- No change to other (short-lived) vault-cli subprocess call sites: `task list`, `task show`, `task set field`, `task clear field`, `cleanup` polls. They finish in <1s and don't need streaming. Refactoring them adds risk for no diagnostic value.
- No structured JSON logs (today's plain-text format stays).
- No log shipping (file → loki / etc.).
- No per-vault log-level overrides (e.g. `LOG_LEVEL_PERSONAL=DEBUG` + `LOG_LEVEL_TRADING=INFO`). Single global level only.
- No Prometheus metrics on `/api/tasks/{id}/run` latency. Useful follow-up, separate task.
- No replacement of the vault-cli precreate with `claude --session-id <uuid>` (the architectural fix — explicitly out of scope per the parent goal).
- No change to the JSON-parse step at end of `start_vault_cli_session` — the subprocess's stdout is still captured for `json.loads(stdout)`. Streaming is *additive*: lines are tee'd to the log AND accumulated into the captured stdout for the JSON parse.

## Acceptance Criteria

- [ ] `LOG_LEVEL=DEBUG uv run task-orchestrator` (or any unset case) produces Python root-logger DEBUG output AND uvicorn DEBUG output. Evidence: `LOG_LEVEL=DEBUG uv run task-orchestrator &  sleep 3  &&  curl -s http://127.0.0.1:8000/  &&  grep -cE 'DEBUG|debug' /tmp/test-orch.log` returns ≥1 (paired with a pytest that asserts `logging.getLogger().getEffectiveLevel() == logging.DEBUG` after parsing env).
- [ ] `LOG_LEVEL` unset → root logger level is `INFO` (default unchanged). Evidence: `uv run pytest tests/test_main.py -k test_log_level_default_info -v` reports `PASSED`.
- [ ] `LOG_LEVEL=DEBUG | INFO | WARNING | ERROR` parses correctly (case-insensitive). Invalid values fall back to `INFO` with a one-line WARN. Evidence: `uv run pytest tests/test_main.py -k test_log_level -v` reports all parametrized cases `PASSED`.
- [ ] `start_vault_cli_session` streams subprocess stdout/stderr line-by-line. Verified via a unit test that runs a fake subprocess emitting 5 lines with 100ms sleeps between them; the test asserts BOTH (a) caplog captures DEBUG records for each line, AND (b) the timestamp delta between successive records is ≥ 80ms (proving non-buffered behaviour — a `communicate()` regression would emit all records at exit with sub-millisecond gaps). Evidence: `uv run pytest tests/api/test_tasks.py -k test_start_vault_cli_session_streams_output -v` reports `PASSED`.
- [ ] Other call sites in `vault_cli_client.py` (`list`, `show`, `set_field`, `clear_field`, watch) are untouched — file is byte-identical to master. Evidence: `git diff origin/master...HEAD -- src/task_orchestrator/vault_cli_client.py` produces empty output.
- [ ] launchd plist (`docs/launchd-service.md` or equivalent) documents the `LOG_LEVEL` env var with example values. Evidence: `grep -n "LOG_LEVEL" docs/launchd-service.md` returns ≥1 line.
- [ ] `make precommit` exits 0 (format + lint + mypy + full test suite).
- [ ] `CHANGELOG.md` has a new bullet under `## Unreleased`. Evidence: `awk '/^## Unreleased/,/^## v/' CHANGELOG.md | grep -niE 'LOG_LEVEL|streaming.*subprocess|debug.*logging' | head -1` returns ≥1 line.

## Verification

```
make precommit
```

Plus a live smoke (out-of-spec but documented in the Goal DoD):

```
launchctl setenv LOG_LEVEL DEBUG
launchctl kickstart -k gui/$UID/com.github.bborbe.task-orchestrator
# click Start in UI → tail /tmp/task-orchestrator.log → see streaming output
launchctl unsetenv LOG_LEVEL
launchctl kickstart -k gui/$UID/com.github.bborbe.task-orchestrator
```

## Desired Behavior

1. `src/task_orchestrator/__main__.py` reads `LOG_LEVEL` from `os.environ` at the top of `main()`. Recognised values: `DEBUG | INFO | WARNING | ERROR` (case-insensitive). Unrecognised values produce a one-line WARN and default to `INFO`. Unset defaults to `INFO`.
2. The parsed level is passed to `logging.basicConfig(level=...)` AND used as `uvicorn.run(..., log_level=<lowercase string>)`. Both Python's root logger and uvicorn's logger respect the same value.
3. `src/task_orchestrator/api/tasks.py::start_vault_cli_session` is refactored: instead of `await proc.communicate()`, spawn two concurrent tasks via `asyncio.gather` — one that `await proc.stdout.readline()` in a loop, logging each line at DEBUG and accumulating bytes; another that does the same for `proc.stderr` (logging at DEBUG, accumulating). Subprocess exit is awaited; final accumulated stdout is JSON-parsed exactly as today.
4. The streaming wrapper logs each line via `logger.debug("vault-cli stdout: %s", line.rstrip())` (and `stderr: %s` for the err pipe). At default INFO level these are filtered out by the standard handler; at DEBUG they appear in real time.
5. The line-arrival timing is observable in tests: a fake subprocess emitting "A\n", sleep 100ms, "B\n", sleep 100ms, exit MUST produce log records for "A" before the 100ms elapse — proving non-buffered behaviour.
6. Other call sites in `vault_cli_client.py` (`task list`, `task show`, `set_field`, `clear_field`, watch, cleanup) keep their existing `proc.communicate()` calls untouched.
7. `docs/launchd-service.md` adds a section "Log Verbosity" documenting the `LOG_LEVEL` env var, its valid values, and how to set it via `launchctl setenv` + plist `EnvironmentVariables`.

## Constraints

- Must not change the public HTTP API shape (`SessionResponse`, endpoint paths, status codes).
- Must not change the JSON-parse semantics at end of `start_vault_cli_session` — the captured stdout bytes round-trip through the new accumulator to `json.loads` byte-for-byte.
- Must not change `vault_cli_client.py` short-running calls. Streaming is opt-in per call site, applied only to `start_vault_cli_session`.
- Must not introduce a third-party log library (stick with stdlib `logging`).
- Must not regress any existing test in `tests/`.
- Default behaviour (no env var) must be byte-identical to today's logs at INFO level — same line format, same content.
- launchd plist file itself is NOT edited by the prompt (the plist lives outside the repo). Only `docs/launchd-service.md` is updated with the documentation; operator applies the plist change separately.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `LOG_LEVEL` unset | Default to INFO; byte-identical to today | None |
| `LOG_LEVEL=foo` (invalid) | One-line WARN at startup; fall back to INFO | Operator fixes env var |
| `LOG_LEVEL=debug` (lowercase) | Case-insensitive parse → DEBUG | None |
| Subprocess stdout closes early (process died) | `await readline()` returns `b""`; the loop exits cleanly; accumulator has whatever was sent; final await on `proc.wait()` yields the non-zero exit code; existing error path fires (`RuntimeError("vault-cli work-on failed")`) | Operator inspects logged stderr stream |
| Subprocess emits very long single line (>64KB asyncio default) | Pre-empt by passing `limit=1 << 20` (1 MiB) to `asyncio.create_subprocess_exec`'s StreamReader. If a single line still exceeds that limit, `asyncio.LimitOverrunError` is raised, logged as WARN with the function name, and the stream-drain loop exits — falling back to a final `await proc.communicate()` for the remainder so the JSON-parse step still gets the full payload. No mid-line chunked recovery. | None — defensive |
| Subprocess emits non-UTF8 bytes | Decode with `errors='replace'` when logging; preserve raw bytes in the accumulator for JSON parse | None |
| Two concurrent Start clicks → two concurrent streaming subprocesses | Each gets its own coroutine pair; logger is thread/async-safe; lines interleave in the log but are tagged with subprocess identifier (e.g. session task_id) so they're disambiguable | None |

## Do-Nothing Option

Without this work, the [[Automatically Name Claude Sessions Started by Task Orchestrator]] verification gate stays stuck — we can't tell whether the naming code is firing or whether vault-cli is silently hanging. Every future "Start should work but doesn't" failure mode requires the same `ps aux` + claude-jsonl archaeology drill that ate ~30min of today's session.

The fix is small: one env-var read + two-line uvicorn change + one subprocess refactor in one function + ~6 tests + one doc section + CHANGELOG bullet. Under-hour container time. Unblocks the parent goal's verification.
