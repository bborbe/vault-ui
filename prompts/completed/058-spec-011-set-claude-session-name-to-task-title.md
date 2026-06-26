---
status: completed
spec: [011-set-claude-session-name-to-task-title]
summary: 'Extended _build_resume_command with keyword-only task_title parameter, wired both call sites in run_task and execute_slash_command, added 8 new tests (5 unit + 3 endpoint), and added CHANGELOG entry under ## Unreleased.'
container: task-orchestrator-session-name-exec-058-spec-011-set-claude-session-name-to-task-title
dark-factory-version: v0.183.0
created: "2026-06-26T08:15:00Z"
queued: "2026-06-26T08:33:34Z"
started: "2026-06-26T08:33:36Z"
completed: "2026-06-26T08:36:39Z"
---
<summary>
- Every resume command emitted by the task-orchestrator now embeds the task title as the launched Claude Code session's display name, so the title shows up in the prompt box, `/resume` picker, and terminal title from the first turn.
- The operator no longer needs to run `/rename <task title>` manually after starting a session from the orchestrator.
- Both the Start path (Run button → `POST /api/tasks/{id}/run`) and the slash-command path (`work-on-task` / `create-task`) inject the name.
- Fast-path commands that never launch Claude (`defer-task`, `complete-task`) are unchanged — they still shell out to vault-cli only.
- Titles with spaces, quotes, or shell-meta characters are quoted safely so the command string is paste-safe.
- When a task has no title (empty / None / whitespace-only), the command is byte-identical to today's output — graceful, opt-in-by-presence behaviour.
- No UI change, no API contract change, no vault-cli change.
</summary>

<objective>
Add an optional `task_title` keyword argument to `_build_resume_command` in `src/task_orchestrator/api/tasks.py` and wire its two callers (`run_task`, `execute_slash_command`) to pass `task.title` through, so the returned resume command embeds `-n <shlex.quote(task_title)>` after `--resume <session_id>` whenever the title is non-empty. Fast-path commands inside `execute_slash_command` (`defer-task`, `complete-task`) remain unchanged.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `changelog-guide.md` — changelog entry style and `## Unreleased` rules.
- `definition-of-done.md` — coverage and completion rules.

Read the spec at `specs/in-progress/011-set-claude-session-name-to-task-title.md` — source of truth for behaviour, constraints, failure modes, and acceptance criteria.

Read `src/task_orchestrator/api/tasks.py` in full before editing. Focus on:
- `_build_resume_command(vault_config: VaultConfig, session_id: str) -> str` (around lines 46–52) — the only function whose signature changes.
- `run_task` (around lines 407–454) — the Start-button endpoint. It already does `task = await client.show_task(task_id)` before calling `_build_resume_command`.
- `execute_slash_command` (around lines 457–560) — has TWO branches: (a) the `defer-task` / `complete-task` fast-path that returns early with `command_str = " ".join(vault_cli_args)` (unchanged by this spec), and (b) the `work-on-task` / `create-task` session path that calls `_build_resume_command(vault_config, session_id)` near line 544 (this call site must pass `task.title`). `task = await client.show_task(task_id)` is already in scope at both branches.

Read `tests/test_api.py` in full before adding tests:
- Existing `_build_resume_command` unit tests live at `test_build_resume_command_without_session_project_dir` / `_with_session_project_dir` / `_expands_tilde` (around lines 1051–1073) plus the `_make_vault_config` helper (around line 1037). New unit tests for the title behaviour must live next to them — same file, same helper.
- Existing endpoint tests for the Start path live at `test_run_task_endpoint_success` (around line 163) — reuse its `mock_proc` + `asyncio.create_subprocess_exec` patch shape. Note `_make_task` sets `title=task_id`, so a task created via `_make_task(task_id="Test Task")` has `title == "Test Task"`.
- Existing tests for the fast-path slash commands live at `test_execute_defer_task_uses_vault_cli` (around line 523) and `test_execute_complete_task_uses_vault_cli` (around line 560) — these prove the fast-path command shape; new tests must NOT change them and must follow their `mock_proc` patch pattern.
- Existing `test_client` fixture (around line 104) configures a single `TestVault` with `mock_vault_client`, exposes the standard sample task whose id/title is `"Test Task"`, and is what most endpoint tests use.

These imports are ALREADY present at the top of `tests/test_api.py` — do NOT re-import: `pytest`, `Path`, `AsyncMock`/`MagicMock`/`patch`, `TestClient`, `create_app`, `Config`/`VaultConfig`, `_build_resume_command`. The new tests need `shlex` — add `import shlex` at the top of the file with the other stdlib imports.

NOTE on the spec's evidence paths: the spec's Acceptance Criteria reference `tests/api/test_tasks.py` (mirroring the source layout `src/task_orchestrator/api/tasks.py`). That directory does NOT exist in this project — all API tests live in `tests/test_api.py`. Place every new test in `tests/test_api.py` next to the existing siblings. The acceptance criteria use `pytest -k <name>` so the evidence still resolves cleanly when run as `uv run pytest tests/test_api.py -k <name> -v`.

Read `CHANGELOG.md` — the current top section is `## v0.35.0`. There is NO `## Unreleased` section; create one above `## v0.35.0`.
</context>

<requirements>

### 1. Extend `_build_resume_command` in `src/task_orchestrator/api/tasks.py`

Add `shlex` to the existing stdlib imports at the top of the file (alphabetical position between `os` and `from contextlib import suppress` is fine).

Change the signature and body of `_build_resume_command` to:

```python
def _build_resume_command(
    vault_config: VaultConfig,
    session_id: str,
    *,
    task_title: str | None = None,
) -> str:
    """Build claude --resume command, prefixing with cd when session_project_dir is set.

    When ``task_title`` is non-empty (after stripping), the returned command also
    appends ``-n <shlex.quote(task_title)>`` so the launched Claude Code session
    shows the task title in its prompt box, /resume picker, and terminal title
    from the first turn. When ``task_title`` is ``None``, empty, or whitespace-only,
    the command is byte-identical to the pre-spec output.
    """
    script = vault_config.claude_script
    name_suffix = ""
    if task_title is not None and task_title.strip() != "":
        name_suffix = f" -n {shlex.quote(task_title)}"
    if vault_config.session_project_dir:
        cwd = vault_config.session_project_dir.replace("~", str(Path.home()))
        return f'cd "{cwd}" && {script} --resume {session_id}{name_suffix}'
    return f"{script} --resume {session_id}{name_suffix}"
```

Requirements honoured by this body:
- New parameter is keyword-only (`*,`) with default `None` — existing positional callers compile unchanged (backwards-compat constraint).
- `-n` lands AFTER `--resume <session_id>`, inside the same logical claude invocation (Desired Behaviour 2).
- `cd "<cwd>" && ` prefix is preserved when `session_project_dir` is set (Desired Behaviour 2).
- `shlex.quote` handles spaces, single/double quotes, and shell-meta characters (Failure Mode row 1).
- Empty / `None` / whitespace-only title omits `-n` entirely — byte-identical to current output (Desired Behaviour 3, Failure Mode row 2).
- Title passes through verbatim — no truncation, slugification, or normalisation (Constraint: title verbatim).

### 2. Wire `task.title` through `run_task` (same file)

In `run_task` (around line 438), find the line:

```python
command = _build_resume_command(vault_config, session_id)
```

Replace it with:

```python
command = _build_resume_command(vault_config, session_id, task_title=task.title)
```

`task` is already bound earlier in the function (`task = await client.show_task(task_id)` around line 431) and `task.title: str` is on the `Task` model. No new I/O, no new exception path — if `show_task` already failed, the existing `FileNotFoundError` → 404 / `Exception` → 500 handlers fire unchanged (Failure Mode row 5).

### 3. Wire `task.title` through `execute_slash_command` session path (same file)

In `execute_slash_command` (around line 544), inside the `work-on-task` / `create-task` branch (i.e. AFTER the early-return for `defer-task` / `complete-task`), find:

```python
command = _build_resume_command(vault_config, session_id)
```

Replace it with:

```python
command = _build_resume_command(vault_config, session_id, task_title=task.title)
```

`task` is already bound earlier in the function (`task = await client.show_task(task_id)` around line 482).

DO NOT touch the `defer-task` / `complete-task` fast-path block (around lines 490–534). It builds `command_str = " ".join(vault_cli_args)` from a vault-cli invocation; that path never calls `_build_resume_command` and must remain byte-identical (Constraint: fast-path unchanged; Acceptance Criterion 7).

### 4. Add `shlex` to the existing test-file imports

At the top of `tests/test_api.py`, add `import shlex` next to the other stdlib imports (`os`, `from pathlib import Path`, etc.). The new endpoint tests use `shlex.split` to tokenise the returned `command` field.

### 5. Add unit tests for `_build_resume_command` in `tests/test_api.py`

Append these four tests directly after the existing `test_build_resume_command_expands_tilde` (around line 1073). They reuse the existing `_make_vault_config` helper (around line 1037) — do NOT redefine it. The test names match the spec's Acceptance Criteria so `pytest -k <name>` resolves to a single test.

```python
def test_build_resume_command_includes_name() -> None:
    """Appends -n '<title>' when task_title is provided."""
    vault_config = _make_vault_config(session_project_dir="")
    result = _build_resume_command(vault_config, "abc-123", task_title="My Test Task")
    assert "-n 'My Test Task'" in result
    assert result.endswith(" -n 'My Test Task'")


def test_build_resume_command_without_name() -> None:
    """Omits -n entirely when task_title is empty string (graceful degradation)."""
    vault_config = _make_vault_config(session_project_dir="")
    result = _build_resume_command(vault_config, "abc-123", task_title="")
    assert " -n " not in result
    # Also byte-identical to the no-title call
    assert result == _build_resume_command(vault_config, "abc-123")


def test_build_resume_command_quotes_special_chars() -> None:
    """Title with apostrophes and spaces round-trips through shlex.split."""
    vault_config = _make_vault_config(session_project_dir="")
    title = "Title with 'apostrophe' and space"
    result = _build_resume_command(vault_config, "abc-123", task_title=title)
    tokens = shlex.split(result)
    # The token immediately following the literal "-n" is the verbatim title
    n_idx = tokens.index("-n")
    assert tokens[n_idx + 1] == title


def test_build_resume_command_keeps_cwd_prefix() -> None:
    """cd "<cwd>" && prefix stays when session_project_dir is set; -n lands AFTER --resume."""
    vault_config = _make_vault_config(
        session_project_dir="/home/user/Obsidian/Personal",
        claude_script="claude-personal.sh",
    )
    result = _build_resume_command(vault_config, "abc-123", task_title="Foo")
    assert result.startswith('cd "/home/user/Obsidian/Personal" && ')
    assert "claude-personal.sh --resume abc-123 -n 'Foo'" in result
    # -n must come AFTER --resume <id>, not before
    assert result.index("--resume abc-123") < result.index("-n 'Foo'")
```

Also cover the whitespace-only and `None` defaults — append this fifth defensive unit test (it satisfies Desired Behaviour 3 and Failure Mode row 2 explicitly):

```python
def test_build_resume_command_omits_name_for_whitespace_and_none() -> None:
    """task_title=None and task_title='   ' both omit -n entirely."""
    vault_config = _make_vault_config(session_project_dir="")
    baseline = _build_resume_command(vault_config, "abc-123")
    assert _build_resume_command(vault_config, "abc-123", task_title=None) == baseline
    assert _build_resume_command(vault_config, "abc-123", task_title="   ") == baseline
```

### 6. Add endpoint tests in `tests/test_api.py`

Append these three tests at the END of the file (after the last existing test). They reuse the existing `test_client` fixture (whose `mock_vault_client` has the sample task with id/title `"Test Task"`).

```python
def test_run_task_command_includes_task_title(test_client: TestClient) -> None:
    """POST /api/tasks/{id}/run returns a command whose -n token is followed by the task title."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b'{"session_id": "test-session-id"}', b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post("/api/tasks/Test%20Task/run?vault=TestVault")

    assert response.status_code == 200
    command = response.json()["command"]
    tokens = shlex.split(command)
    assert "-n" in tokens
    n_idx = tokens.index("-n")
    assert tokens[n_idx + 1] == "Test Task"


def test_execute_work_on_task_command_includes_task_title(test_client: TestClient) -> None:
    """POST /api/tasks/{id}/execute-command work-on-task returns a command whose -n token is followed by the task title."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b'{"session_id": "test-session-id"}', b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "work-on-task"},
        )

    assert response.status_code == 200
    command = response.json()["command"]
    tokens = shlex.split(command)
    assert "-n" in tokens
    n_idx = tokens.index("-n")
    assert tokens[n_idx + 1] == "Test Task"


def test_execute_defer_task_command_unchanged(test_client: TestClient) -> None:
    """Fast-path defer-task still uses vault-cli only — no -n token in the returned command."""
    mock_proc = MagicMock()
    mock_proc.returncode = 0
    mock_proc.communicate = AsyncMock(return_value=(b"deferred ok\n", b""))

    with patch("asyncio.create_subprocess_exec", AsyncMock(return_value=mock_proc)):
        response = test_client.post(
            "/api/tasks/Test%20Task/execute-command?vault=TestVault",
            json={"command": "defer-task"},
        )

    assert response.status_code == 200
    command = response.json()["command"]
    tokens = shlex.split(command)
    assert "-n" not in tokens
```

The endpoint tests mirror the patch pattern from `test_run_task_endpoint_success` and `test_execute_defer_task_uses_vault_cli` — the same `asyncio.create_subprocess_exec` patch is reused for both the vault-cli subprocess call and the `start_vault_cli_session` subprocess call (the mock returns the same `{"session_id": ...}` payload for both, which is what the existing `test_run_task_endpoint_success` already relies on).

### 7. CHANGELOG entry

Open `CHANGELOG.md`. The current top section is `## v0.35.0`. Insert a new `## Unreleased` section ABOVE it with the following bullet:

```
## Unreleased

- feat: Embed task title as `-n <title>` in the resume command emitted by the orchestrator, so the launched Claude Code session shows the task title in its prompt box, `/resume` picker, and terminal title from the first turn — eliminates the per-session manual `/rename`. Empty / missing titles omit the flag, leaving the command byte-identical to before. Affects both the Start button (`POST /api/tasks/{id}/run`) and the `work-on-task` / `create-task` slash commands; fast-path `defer-task` / `complete-task` are unchanged.
```

Verify with: `awk '/^## Unreleased/,/^## v/' CHANGELOG.md | grep -niE 'session.*name|claude.*-n|task title' | head -1` — must return at least one line.

</requirements>

<constraints>
- The new parameter on `_build_resume_command` MUST be keyword-only with default `None` — existing positional callers (and the existing unit tests `test_build_resume_command_without_session_project_dir`, `_with_session_project_dir`, `_expands_tilde`) must continue to compile and pass with no change to their call sites or assertions.
- Do NOT modify vault-cli, the headless session creation path (`start_vault_cli_session`), or the `SessionResponse` Pydantic model.
- Do NOT modify the modal UI, the `/api/tasks/{id}/run` endpoint shape, or the WebSocket broadcast payload.
- Do NOT change behaviour when `vault_config.session_project_dir` is set vs unset other than appending `-n …` to the claude invocation.
- Do NOT change the fast-path command-building for `defer-task` / `complete-task` — the existing tests `test_execute_defer_task_uses_vault_cli` and `test_execute_complete_task_uses_vault_cli` must still pass with their current assertions intact.
- Do NOT regress any existing test in `tests/`.
- Title is passed verbatim — no truncation, slugification, normalisation, or lowercasing. Use `shlex.quote` only; nothing else.
- `make precommit` MUST exit 0 (format + lint + typecheck + full test suite). The project's validation command and DoD prompt (`docs/dod.md`) are configured at the project level — do not redeclare them in this prompt's verification.
- No new third-party dependency. `shlex` is in the Python stdlib.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Record the test count before and after:
```bash
grep -c 'def test_' tests/test_api.py
```
The post-change count must equal the pre-change count plus 8 (5 unit + 3 endpoint).

Run each new test by its acceptance-criterion name:
```bash
uv run pytest tests/test_api.py -k build_resume_command_includes_name -v
uv run pytest tests/test_api.py -k build_resume_command_without_name -v
uv run pytest tests/test_api.py -k build_resume_command_quotes_special_chars -v
uv run pytest tests/test_api.py -k build_resume_command_keeps_cwd_prefix -v
uv run pytest tests/test_api.py -k build_resume_command_omits_name_for_whitespace_and_none -v
uv run pytest tests/test_api.py -k run_task_command_includes_task_title -v
uv run pytest tests/test_api.py -k execute_work_on_task_command_includes_task_title -v
uv run pytest tests/test_api.py -k execute_defer_task_command_unchanged -v
```
Each must report `PASSED`.

Confirm `_build_resume_command` now accepts the new keyword argument:
```bash
grep -n "task_title" src/task_orchestrator/api/tasks.py
```
Expected: at least three matches — one in the signature, two at the call sites in `run_task` and `execute_slash_command`.

Confirm the CHANGELOG bullet is present under `## Unreleased`:
```bash
awk '/^## Unreleased/,/^## v/' CHANGELOG.md | grep -niE 'session.*name|claude.*-n|task title' | head -1
```
Expected: at least one matching line.

Run `make precommit` — must exit 0.
</verification>
