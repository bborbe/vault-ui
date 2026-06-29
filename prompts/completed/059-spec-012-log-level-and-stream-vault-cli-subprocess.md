---
status: completed
spec: [012-add-log-level-env-var-and-stream-vault-cli-subprocess]
summary: Added LOG_LEVEL env var parsing to __main__.py and streaming refactor of start_vault_cli_session in api/tasks.py, with tests in tests/test_main.py and tests/test_api.py, docs in docs/launchd-service.md, and CHANGELOG entry.
execution_id: vault-ui-observability-exec-059-spec-012-log-level-and-stream-vault-cli-subprocess
dark-factory-version: v0.183.0
created: "2026-06-26T14:00:00Z"
queued: "2026-06-26T13:46:45Z"
started: "2026-06-26T13:46:47Z"
completed: "2026-06-26T13:55:06Z"
---
<summary>
- The operator can set `LOG_LEVEL=DEBUG|INFO|WARNING|ERROR` (case-insensitive) in the environment to crank Python root-logger AND uvicorn verbosity in one shot — no code edit, no rebuild.
- Unset / unrecognised `LOG_LEVEL` falls back to `INFO`, the byte-identical pre-spec default; invalid values get a one-line WARN at startup.
- The long-running `start_vault_cli_session` subprocess (the headless `vault-cli task work-on` that drives the full `/vault-cli:work-on-task` Claude skill) now streams stdout/stderr line-by-line to the logger as bytes arrive — instead of going dark for 60–180s until `communicate()` returns.
- Stream lines log at DEBUG level and are tagged with the task id so concurrent Start clicks are disambiguable.
- The final captured stdout is still JSON-parsed byte-for-byte exactly as today — streaming is additive, never lossy.
- 14 other vault-cli subprocess sites (`task list`, `task show`, `task set`, `task clear`, `task defer`, `task complete`, cleanup, watcher) keep their existing `communicate()` semantics — fast calls don't need streaming.
- `docs/launchd-service.md` documents the env var with example values for both inline plist `EnvironmentVariables` and `launchctl setenv`.
- `CHANGELOG.md` gains an `## Unreleased` entry describing the new env var and the streaming behaviour.
</summary>

<objective>
Land two diagnostics in vault-ui: (1) a `LOG_LEVEL` env var read at startup that drives both `logging.basicConfig(level=...)` and `uvicorn.run(..., log_level=...)`, defaulting to `INFO` with byte-identical pre-spec output when unset; and (2) a streaming refactor of `start_vault_cli_session` in `src/vault_ui/api/tasks.py` so subprocess stdout/stderr are tee'd to the logger line-by-line at DEBUG while still being accumulated for the final `json.loads` parse. The 14 other `communicate()` call sites stay untouched.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `changelog-guide.md` — entry style and `## Unreleased` rules.
- `python-logging-guide.md` — stdlib `logging` patterns used in this codebase.
- `definition-of-done.md` — coverage and completion rules.

Read the spec at `specs/in-progress/012-add-log-level-env-var-and-stream-vault-cli-subprocess.md` — source of truth for behaviour, constraints, failure modes, and acceptance criteria. The Failure Modes table is load-bearing: every row maps to a requirement step below.

Read these source files in full before editing:
- `src/vault_ui/__main__.py` — entry point. `main()` body is short. Two hard-coded levels today: `logging.basicConfig(level=logging.INFO, ...)` at lines 23–27 and `uvicorn.run(..., log_level="info")` at line 41. The module-level `app = create_app()` at line 17 must stay (it backs `make watch` / `uvicorn --reload`).
- `src/vault_ui/api/tasks.py` — the only file whose `start_vault_cli_session` (lines 71–96) needs the streaming refactor. Note: there are TWO other `await proc.communicate()` calls in this same file — `execute_slash_command` fast path around line 533 and `update_task_phase` around lines 667 and 686. Those MUST stay untouched (spec Constraint + Non-goal: only `start_vault_cli_session` streams).
- `src/vault_ui/vault_cli_client.py` — list it but DO NOT modify. The 14-ish short-running `communicate()` call sites here are explicitly out-of-scope (spec Non-goal #1, Acceptance Criterion `git diff` line).
- `src/vault_ui/cleanup.py` — list it but DO NOT modify (same Non-goal).

Read these test files in full before adding tests:
- `tests/test_api.py` — particularly `test_run_task_endpoint_success` (around line 164), the `mock_vault_client` fixture (line 99), and the `test_client` fixture (line 105). The streaming test must follow the `MagicMock` + `AsyncMock` + `patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc))` pattern these use. `_make_task` (line 21) and `_make_sample_task` (line 60) give pre-built tasks; the sample task's id/title is `"Test Task"`.
- `tests/conftest.py` — shared fixtures (`tmp_vault`, `sample_task_file`). The new tests do not need these but knowing they exist prevents accidental fixture duplication.

NOTE on the spec's evidence paths: the spec's Acceptance Criteria reference `tests/api/test_tasks.py` (mirroring the source layout `src/vault_ui/api/tasks.py`). That directory does NOT exist in this project — all API tests live in `tests/test_api.py`. Place the streaming test in `tests/test_api.py` next to `test_run_task_endpoint_success` (around line 164). The spec uses `pytest -k <name>` selectors so evidence still resolves cleanly when run as `uv run pytest tests/test_api.py -k test_start_vault_cli_session_streams_output -v`.

The spec's Acceptance Criteria reference `tests/test_main.py` for the LOG_LEVEL tests. That file does NOT exist yet — create it. Put the LOG_LEVEL parsing tests there (they test a `__main__.py` helper).

Read `CHANGELOG.md` — the current top section is `## v0.36.0`. There is NO `## Unreleased` section; create one above `## v0.36.0`.

Read `docs/launchd-service.md` in full — the new "Log Verbosity" section will sit between section "## 4. Upgrade flow" (line 102) and "## Troubleshooting" (line 115) so the operational lifecycle reads top-to-bottom: install → manage → verify → upgrade → tune verbosity → troubleshoot.
</context>

<requirements>

### 1. Add LOG_LEVEL parsing helper to `src/vault_ui/__main__.py`

Add `import os` at the top of the file alongside the existing stdlib imports.

Add a module-level helper above `main()`:

```python
_VALID_LOG_LEVELS = {"DEBUG", "INFO", "WARNING", "ERROR"}


def _parse_log_level(raw: str | None) -> tuple[int, str, str | None]:
    """Parse the LOG_LEVEL env var.

    Returns ``(numeric_level, lowercase_name, warning_message)``.

    - ``raw`` is ``None`` or empty → returns ``(logging.INFO, "info", None)`` — byte-identical default.
    - ``raw`` matches one of ``DEBUG | INFO | WARNING | ERROR`` (case-insensitive) → returns the
      corresponding ``(logging.<LEVEL>, lowercase_name, None)``.
    - ``raw`` is anything else → returns ``(logging.INFO, "info", "<one-line WARN message>")`` and
      the caller emits the WARN AFTER ``basicConfig`` is configured at INFO so the message lands in the log.

    The ``lowercase_name`` is what gets passed to ``uvicorn.run(..., log_level=...)``.
    """
    if raw is None or raw.strip() == "":
        return logging.INFO, "info", None
    normalized = raw.strip().upper()
    if normalized in _VALID_LOG_LEVELS:
        return getattr(logging, normalized), normalized.lower(), None
    fallback_warning = (
        f"Invalid LOG_LEVEL={raw!r}; expected one of "
        f"{sorted(_VALID_LOG_LEVELS)} (case-insensitive). Falling back to INFO."
    )
    return logging.INFO, "info", fallback_warning
```

Wire it into `main()`:

```python
def main() -> int:
    """Run the application."""
    level, uvicorn_level, fallback_warning = _parse_log_level(os.environ.get("LOG_LEVEL"))

    logging.basicConfig(
        level=level,
        format="%(asctime)s %(levelname)-8s [%(name)s:%(lineno)d] %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    if fallback_warning is not None:
        logging.getLogger(__name__).warning(fallback_warning)

    try:
        set_connection_manager(get_connection_manager())
        tasks_set_connection_manager(get_connection_manager())

        config = get_config()
        uvicorn.run(
            app,
            host=config.host,
            port=config.port,
            log_level=uvicorn_level,
        )
    except FileNotFoundError as e:
        print(str(e), file=sys.stderr)
        return 1

    return 0
```

Requirements honoured:
- Unset / empty `LOG_LEVEL` → `(logging.INFO, "info", None)` — byte-identical pre-spec output (Constraint: default behaviour byte-identical; Failure Mode row 1).
- Case-insensitive parse via `.upper()` covers `debug` / `Debug` / `DEBUG` (Failure Mode row 3).
- Invalid value → one-line WARN AFTER `basicConfig` so the message is captured in the configured log (Failure Mode row 2).
- Same parsed level drives BOTH Python's root logger AND uvicorn (Desired Behaviour 2). Uvicorn requires the lowercase string form (`"debug"` / `"info"` / `"warning"` / `"error"`), so the helper returns it pre-lowercased.
- `getattr(logging, "DEBUG")` is a safe attribute lookup — only used for names already validated against `_VALID_LOG_LEVELS`.

### 2. Refactor `start_vault_cli_session` in `src/vault_ui/api/tasks.py` to stream stdout/stderr

The function lives at lines 71–96 today. Replace its body with the streaming implementation below. Keep the signature, docstring purpose, return type, and the final `json.loads` + `session_id` validation behaviour unchanged.

Add this helper above `start_vault_cli_session`:

```python
async def _drain_stream(
    stream: asyncio.StreamReader,
    label: str,
    task_id: str,
    buffer: bytearray,
) -> None:
    """Tee a subprocess pipe to the logger line-by-line while accumulating raw bytes.

    - Logs each line at DEBUG with the task_id prefix so concurrent Start clicks
      are disambiguable in interleaved log output (Failure Mode row 6).
    - Decodes for logging with ``errors='replace'`` so non-UTF8 bytes do not crash
      the drain loop; the raw bytes are preserved verbatim in ``buffer`` so the
      final ``json.loads`` sees byte-identical input (Failure Mode row 5).
    - On an oversized single line (``asyncio.LimitOverrunError`` from the
      ``StreamReader``'s 1 MiB buffer), logs a WARN and breaks out of the loop
      so the caller can fall through to ``proc.communicate()`` for the remainder
      (Failure Mode row 4).
    - End-of-stream (process exit or pipe closed) → ``readline()`` returns ``b""``
      and the loop exits cleanly (Failure Mode row 4).
    """
    while True:
        try:
            line = await stream.readline()
        except asyncio.LimitOverrunError:
            logger.warning(
                "vault-cli %s line exceeded buffer limit for task %s; "
                "stopping line-streaming and falling back to bulk drain",
                label,
                task_id,
            )
            # Drain remaining bytes from the StreamReader without line-splitting,
            # so the JSON-parse step downstream still gets the full payload.
            # Read in chunks until EOF (b"") — bypasses readline's 1 MiB
            # line-limit by switching to raw byte reads.
            while True:
                chunk = await stream.read(64 * 1024)
                if not chunk:
                    return
                buffer.extend(chunk)
        if not line:
            return
        buffer.extend(line)
        logger.debug(
            "vault-cli %s [%s]: %s",
            label,
            task_id,
            line.decode("utf-8", errors="replace").rstrip(),
        )
```

Replace the body of `start_vault_cli_session` with:

```python
async def start_vault_cli_session(vault_config: VaultConfig, task_id: str) -> str:
    """Start a Claude session via vault-cli, returns session_id.

    Streams subprocess stdout/stderr to the logger line-by-line at DEBUG while
    accumulating raw bytes for the final JSON parse. Other (short-lived) vault-cli
    subprocess sites in this codebase keep their ``communicate()`` semantics; only
    this long-running ``task work-on --mode headless`` call streams (spec 012).
    """
    proc = await asyncio.create_subprocess_exec(
        vault_config.vault_cli_path,
        "task",
        "work-on",
        task_id,
        "--mode",
        "headless",
        "--vault",
        vault_config.name,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        limit=1 << 20,  # 1 MiB per-line buffer (default 64 KiB is too small for claude jsonl output)
    )
    assert proc.stdout is not None  # PIPE always yields a StreamReader
    assert proc.stderr is not None

    stdout_buf = bytearray()
    stderr_buf = bytearray()

    await asyncio.gather(
        _drain_stream(proc.stdout, "stdout", task_id, stdout_buf),
        _drain_stream(proc.stderr, "stderr", task_id, stderr_buf),
    )

    returncode = await proc.wait()

    if returncode != 0:
        raise RuntimeError(
            f"vault-cli work-on failed: {bytes(stderr_buf).decode(errors='replace').strip()}"
        )

    result: dict[str, Any] = json.loads(bytes(stdout_buf).decode())
    session_id: str = result.get("session_id") or ""
    if not session_id:
        warnings: list[str] = result.get("warnings") or []
        detail = "; ".join(warnings) if warnings else "no warnings reported"
        raise RuntimeError(f"vault-cli work-on did not start a claude session: {detail}")
    return session_id
```

Requirements honoured:
- Two concurrent drain coroutines via `asyncio.gather` — stdout and stderr stream in parallel (Desired Behaviour 3). Both must complete before `proc.wait()` so the buffers are full when the JSON parse runs.
- `limit=1 << 20` raises the per-line `StreamReader` buffer from the asyncio default 64 KiB to 1 MiB (Failure Mode row 4).
- `bytes(stdout_buf)` round-trips the accumulated raw bytes to `json.loads(...)` unchanged — byte-identical to today's `stdout.decode()` for valid UTF-8 input (Constraint: JSON parse semantics unchanged).
- Non-UTF8 bytes are tolerated for LOGGING via `errors="replace"`; the JSON-parse path still uses strict decoding which today already crashes on invalid UTF-8 (preserving today's behaviour — spec says no change to JSON-parse semantics).
- The error-path `RuntimeError("vault-cli work-on failed: ...")` is preserved (Constraint: existing error path fires).
- `task_id` is included in every DEBUG log line so concurrent Start clicks produce disambiguable interleaved output (Failure Mode row 6).

DO NOT touch `execute_slash_command` (lines 473–576) or `update_task_phase` (lines 633–700) in this same file — both still use `await proc.communicate()` and that is correct (Non-goal #1).

### 3. New tests in `tests/test_main.py` (CREATE this file)

Create `tests/test_main.py` with:

```python
"""Tests for src/vault_ui/__main__.py."""

import logging

import pytest

from vault_ui.__main__ import _parse_log_level


def test_log_level_default_info() -> None:
    """Unset env → INFO, no warning, uvicorn gets 'info'."""
    level, uvicorn_level, warning = _parse_log_level(None)
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is None


def test_log_level_empty_string_defaults_info() -> None:
    """Empty string is treated as unset."""
    level, uvicorn_level, warning = _parse_log_level("")
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is None


def test_log_level_whitespace_only_defaults_info() -> None:
    """Whitespace-only is treated as unset."""
    level, uvicorn_level, warning = _parse_log_level("   ")
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is None


@pytest.mark.parametrize(
    "raw,expected_level,expected_uvicorn",
    [
        ("DEBUG", logging.DEBUG, "debug"),
        ("INFO", logging.INFO, "info"),
        ("WARNING", logging.WARNING, "warning"),
        ("ERROR", logging.ERROR, "error"),
        ("debug", logging.DEBUG, "debug"),
        ("Debug", logging.DEBUG, "debug"),
        ("  warning  ", logging.WARNING, "warning"),
    ],
)
def test_log_level_valid_values(raw: str, expected_level: int, expected_uvicorn: str) -> None:
    """Case-insensitive parse of all four levels + surrounding whitespace."""
    level, uvicorn_level, warning = _parse_log_level(raw)
    assert level == expected_level
    assert uvicorn_level == expected_uvicorn
    assert warning is None


@pytest.mark.parametrize("raw", ["foo", "TRACE", "verbose", "1", "true", "DEBUG,INFO"])
def test_log_level_invalid_value_warns_and_falls_back(raw: str) -> None:
    """Invalid → falls back to INFO and surfaces a one-line warning."""
    level, uvicorn_level, warning = _parse_log_level(raw)
    assert level == logging.INFO
    assert uvicorn_level == "info"
    assert warning is not None
    assert raw in warning
    assert "INFO" in warning
```

The acceptance criterion `pytest tests/test_main.py -k test_log_level_default_info` resolves to `test_log_level_default_info`. The acceptance criterion `pytest tests/test_main.py -k test_log_level` resolves to ALL the `test_log_level_*` parametrized cases above.

### 4. New streaming test in `tests/test_api.py`

Add a single test next to `test_run_task_endpoint_success` (around line 164). FIRST, check the existing imports at the top of `tests/test_api.py`. The file imports `AsyncMock`, `MagicMock`, `patch`, `pytest` at the top, but `asyncio` and `logging` are only imported inside individual test bodies, not module-scope. ADD `import asyncio`, `import logging`, and `import time` to the stdlib import block at the top of the file if not already present at module scope (the new test uses `asyncio.sleep`, `logging.DEBUG`, and `time.monotonic` directly):

```python
@pytest.mark.asyncio
async def test_start_vault_cli_session_streams_output(caplog: pytest.LogCaptureFixture) -> None:
    """Subprocess stdout is logged at DEBUG line-by-line as it arrives, not buffered at exit.

    Fake subprocess emits 3 lines with 100ms sleeps between them, then a JSON envelope.
    The test asserts BOTH:
      (a) every emitted line shows up as a DEBUG record tagged with the task_id, AND
      (b) the wall-clock delta between successive log records is >= 80ms, proving
          the stream is drained as bytes arrive — a regression to `communicate()`
          would emit all records back-to-back at process exit with sub-millisecond
          gaps between them.
    """
    from vault_ui.api.tasks import start_vault_cli_session
    from vault_ui.config import VaultConfig

    vault_config = VaultConfig(
        name="TestVault",
        vault_path="/tmp/vault",
        tasks_folder="24 Tasks",
        claude_script="claude",
        vault_cli_path="vault-cli",
    )

    # Build a fake StreamReader that emits lines with sleeps between them.
    async def _fake_stdout_lines() -> list[bytes]:
        return [
            b"line-A\n",
            b"line-B\n",
            b"line-C\n",
            b'{"session_id": "abc-123"}\n',
        ]

    class _FakeStream:
        def __init__(self, lines: list[bytes]) -> None:
            self._lines = list(lines)

        async def readline(self) -> bytes:
            if not self._lines:
                return b""
            await asyncio.sleep(0.1)  # 100ms inter-line gap
            return self._lines.pop(0)

    lines = await _fake_stdout_lines()
    fake_stdout = _FakeStream(lines)
    fake_stderr = _FakeStream([])

    fake_proc = MagicMock()
    fake_proc.stdout = fake_stdout
    fake_proc.stderr = fake_stderr
    fake_proc.wait = AsyncMock(return_value=0)

    caplog.set_level(logging.DEBUG, logger="vault_ui.api.tasks")

    with patch(
        "asyncio.create_subprocess_exec", AsyncMock(return_value=fake_proc)
    ):
        session_id = await start_vault_cli_session(vault_config, "Test Task")

    assert session_id == "abc-123"

    # (a) every emitted line shows up as a DEBUG record tagged with the task_id
    stream_records = [
        r for r in caplog.records
        if r.levelno == logging.DEBUG and "vault-cli stdout" in r.getMessage()
    ]
    assert len(stream_records) == 4, [r.getMessage() for r in stream_records]
    messages = [r.getMessage() for r in stream_records]
    assert all("Test Task" in m for m in messages), messages
    assert any("line-A" in m for m in messages)
    assert any("line-B" in m for m in messages)
    assert any("line-C" in m for m in messages)

    # (b) timestamp deltas between successive records >= 80ms (proving streaming,
    # not buffered-at-exit). 80ms gives 20ms slack under the 100ms sleeps so a
    # busy CI does not flake. A `communicate()` regression would produce <1ms gaps.
    timestamps = [r.created for r in stream_records]
    deltas = [b - a for a, b in zip(timestamps, timestamps[1:])]
    assert all(d >= 0.08 for d in deltas), deltas
```

Add `import logging` to the test file's imports if not already present.

Add the `pytest-asyncio` marker check: `tests/test_api.py` already runs async tests via the existing pytest config (verify by looking for `@pytest.mark.asyncio` use in the file; if absent, use `asyncio.run(...)` to invoke `start_vault_cli_session` from a sync test instead). Decision rule: if a `@pytest.mark.asyncio` decorator already appears anywhere in `tests/test_api.py`, use the decorated form above; otherwise convert the test to a sync function that wraps the body in `asyncio.run(_inner())` and drop the decorator.

The acceptance criterion `pytest tests/api/test_tasks.py -k test_start_vault_cli_session_streams_output` resolves cleanly under `uv run pytest tests/test_api.py -k test_start_vault_cli_session_streams_output -v` since `-k` does name-substring matching, not path matching.

### 5. Update `docs/launchd-service.md` — new "Log Verbosity" section

Insert a new section between section "## 4. Upgrade flow" (ends at line 113 today) and "## Troubleshooting" (line 115). Use exactly this content:

```markdown
## 5. Log verbosity

vault-ui reads `LOG_LEVEL` from the environment at startup. Valid values (case-insensitive): `DEBUG`, `INFO`, `WARNING`, `ERROR`. Unset → `INFO` (default; same as before this knob existed). Invalid → falls back to `INFO` and logs a one-line WARN at startup.

The same level drives both Python's root logger and uvicorn's logger — bumping to `DEBUG` surfaces router internals, every HTTP request, AND the per-line streaming output of the long-running `vault-cli task work-on` subprocess (so you can watch the headless claude's tool calls arrive live instead of waiting 60–180s for the buffered exit).

**Set persistently via the plist** (preferred — survives `launchctl kickstart`):

```xml
<key>EnvironmentVariables</key>
<dict>
    <key>PATH</key>
    <string>/Users/YOUR_USER/.local/bin:/Users/YOUR_USER/Documents/workspaces/go/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    <!-- Uncomment to enable verbose logging:
    <key>LOG_LEVEL</key>
    <string>DEBUG</string>
    -->
</dict>
```

Apply the plist edit by restarting the service (section 2).

**Set transiently for a single restart** (clears on reboot or `launchctl unsetenv`):

```bash
launchctl setenv LOG_LEVEL DEBUG
launchctl kickstart -k gui/$UID/com.github.bborbe.vault-ui
# ... investigate ...
launchctl unsetenv LOG_LEVEL
launchctl kickstart -k gui/$UID/com.github.bborbe.vault-ui
```

Verify the level took effect:

```bash
tail -f /tmp/vault-ui.log
# At DEBUG you should see per-request lines and "vault-cli stdout [<task_id>]: ..." lines while a Start is in flight.
```
```

The acceptance criterion `grep -n "LOG_LEVEL" docs/launchd-service.md` will return ≥1 line after this edit.

### 6. CHANGELOG.md

Open `CHANGELOG.md`. The top entry today is `## v0.36.0`. Insert a new section `## Unreleased` ABOVE `## v0.36.0` with a single bullet:

```markdown
## Unreleased

- feat: Add `LOG_LEVEL` env var (`DEBUG | INFO | WARNING | ERROR`, case-insensitive; default `INFO`) read at startup and applied to both Python's root logger and uvicorn — bump to `DEBUG` to trace HTTP requests and the long-running headless `vault-cli task work-on` subprocess live. Stream the headless subprocess's stdout/stderr line-by-line at DEBUG (1 MiB per-line buffer, non-UTF8 tolerated) instead of buffering in `communicate()` — operator no longer waits 60–180s in the dark when starting a session from the UI. Other short-running vault-cli call sites are unchanged.
```

The acceptance criterion `awk '/^## Unreleased/,/^## v/' CHANGELOG.md | grep -niE 'LOG_LEVEL|streaming.*subprocess|debug.*logging' | head -1` resolves on the `LOG_LEVEL` token in this bullet.

### 7. Constraint validation pass (run before declaring done)

Confirm by `grep`/`git diff` that ALL of the following hold — these are explicit Acceptance Criteria from the spec:

- `git diff origin/master...HEAD -- src/vault_ui/vault_cli_client.py` produces empty output (no edits there).
- `git diff origin/master...HEAD -- src/vault_ui/cleanup.py` produces empty output (no edits there).
- In `src/vault_ui/api/tasks.py`, exactly ONE function body was rewritten (`start_vault_cli_session`); the function `_drain_stream` was added. `execute_slash_command` and `update_task_phase` still call `await proc.communicate()` unchanged. Confirm with `grep -n "await proc.communicate" src/vault_ui/api/tasks.py` — must return at least 3 occurrences (1 in `execute_slash_command`, 2 in `update_task_phase`) and must NOT include any line inside `start_vault_cli_session`.
- `_parse_log_level` lives at module scope in `__main__.py` so the test can import it via `from vault_ui.__main__ import _parse_log_level`.

</requirements>

<constraints>
- Must not change the public HTTP API shape (`SessionResponse`, endpoint paths, status codes).
- Must not change the JSON-parse semantics at end of `start_vault_cli_session` — captured stdout bytes round-trip through the new accumulator to `json.loads` byte-for-byte for valid UTF-8 input.
- Must not change `src/vault_ui/vault_cli_client.py` or `src/vault_ui/cleanup.py`. Streaming is opt-in per call site, applied only to `start_vault_cli_session`. The `git diff` against master for those two files MUST be empty.
- Must not introduce a third-party logging library (stick with stdlib `logging`).
- Must not regress any existing test in `tests/`.
- Default behaviour (no `LOG_LEVEL` env var) must be byte-identical to today's logs at INFO level — same line format, same content.
- The launchd plist file itself is NOT edited by this prompt (the plist lives outside the repo). Only `docs/launchd-service.md` is updated with the documentation snippet.
- Do NOT commit — dark-factory handles git. Existing tests must still pass.
- Do NOT touch `execute_slash_command` or `update_task_phase` in `api/tasks.py` beyond what is required for the streaming refactor of `start_vault_cli_session`.
</constraints>

<verification>
Run `make precommit` — must pass (format + lint + mypy + full test suite).

Additionally, confirm the spec-level acceptance evidence resolves:

```bash
uv run pytest tests/test_main.py -k test_log_level_default_info -v
uv run pytest tests/test_main.py -k test_log_level -v
uv run pytest tests/test_api.py -k test_start_vault_cli_session_streams_output -v
git diff origin/master...HEAD -- src/vault_ui/vault_cli_client.py   # empty
git diff origin/master...HEAD -- src/vault_ui/cleanup.py            # empty
grep -n "LOG_LEVEL" docs/launchd-service.md                                  # >= 1 line
awk '/^## Unreleased/,/^## v/' CHANGELOG.md | grep -niE 'LOG_LEVEL|streaming.*subprocess|debug.*logging' | head -1   # >= 1 line
grep -cE 'await (proc|status_proc)\.communicate' src/vault_ui/api/tasks.py  # == 3 (execute_slash_command + update_task_phase x2). start_vault_cli_session must NOT match.
```
</verification>
