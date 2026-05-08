---
status: completed
spec: [003-cleanup-resolve-renamed-sessions]
summary: Created session_resolver.py with is_uuid() and resolve_session_id() pure functions, plus full test suite covering all specified cases
container: task-orchestrator-027-spec-003-session-resolver
dark-factory-version: v0.57.5
created: "2026-03-17T00:00:00Z"
queued: "2026-03-17T13:10:15Z"
started: "2026-03-17T13:10:16Z"
completed: "2026-03-17T13:11:55Z"
---

<summary>
- A new module provides two pure functions: one to detect UUID-formatted session IDs, one to scan for a matching session file
- UUID detection uses the standard 8-4-4-4-12 hexadecimal pattern; everything else is treated as a display name
- The scanner reads .jsonl files in a given project directory looking for a `custom-title` JSON line whose `customTitle` field matches the display name
- When a match is found, the UUID comes from the filename stem — never from inside the JSON
- Malformed JSON lines are skipped individually; a bad line does not abort scanning of subsequent lines or other files
- Files that cannot be read are skipped gracefully with a warning
- Lines longer than 4096 bytes are skipped to bound memory usage
- A `customTitle` value that looks like a path (e.g. `../../etc/passwd`) is only used for string comparison — never used to construct file paths
- When two .jsonl files claim the same custom title, first match wins and a log message is emitted
- Unit tests cover all failure modes: no match, malformed JSON, unreadable files, path traversal strings, duplicate titles, and UUID-formatted inputs
</summary>

<objective>
Create `src/task_orchestrator/session_resolver.py` — a pure, self-contained module with `is_uuid()` and `resolve_session_id()` that later prompts will wire into the API, watcher, and cleanup. Nothing else changes in this prompt.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before making any changes:
- `src/task_orchestrator/cleanup.py` — note `derive_claude_project_dir(vault_path)` (line 15) and understand the project directory layout (`~/.claude/projects/<derived>/`). The resolver does NOT call this function — callers are responsible for constructing the `Path` and passing it in.
- `src/task_orchestrator/api/models.py` — confirm `Task.claude_session_id: str | None`.

The `.jsonl` file format is owned by Claude Code. Each line is an independent JSON object. A `custom-title` line has at minimum `{"type": "custom-title", "customTitle": "<name>"}` but may contain additional fields. The UUID that identifies the session comes from the `.jsonl` filename stem (e.g. `abc123.jsonl` → UUID `abc123`), not from inside the JSON.
</context>

<requirements>
1. Create `src/task_orchestrator/session_resolver.py` with this exact public interface:

```python
"""Resolve Claude session display names to their real UUIDs."""

import json
import logging
import re
from pathlib import Path

logger = logging.getLogger(__name__)

_UUID_RE = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
    re.IGNORECASE,
)

_MAX_LINE_BYTES = 4096


def is_uuid(value: str) -> bool:
    """Return True if value matches the UUID format (8-4-4-4-12 hex)."""
    return bool(_UUID_RE.match(value))


def resolve_session_id(display_name: str, project_dir: Path) -> str | None:
    """Scan .jsonl files in project_dir for a custom-title entry matching display_name.

    Returns the UUID (filename stem) of the first matching file, or None if no match found.

    Args:
        display_name: The non-UUID session ID to resolve (e.g. "trading-alerts")
        project_dir: Directory containing .jsonl session files (e.g. ~/.claude/projects/...)
    """
    ...
```

2. Implement `resolve_session_id` with the following behavior:

   a. If `project_dir` does not exist, log a debug message and return `None` immediately.

   b. Iterate over all `*.jsonl` files in `project_dir` (non-recursive, just the top level). The filename stem is the candidate UUID.

   c. For each file, attempt to open and read it. If the file is not readable (any `OSError`), log a warning and continue to the next file:
      ```
      logger.warning("[SessionResolver] Cannot read %s: %s", path, e)
      ```

   d. Read the file line by line. For each line:
      - Skip empty lines.
      - Skip lines whose byte length exceeds `_MAX_LINE_BYTES` (log at debug level).
      - Parse the line as JSON. If `json.JSONDecodeError` is raised, log a warning and continue:
        ```
        logger.warning("[SessionResolver] Malformed JSON in %s, skipping line", path)
        ```
      - If `parsed.get("type") == "custom-title"` and `parsed.get("customTitle") == display_name`:
        - This file matches. Log at info level:
          ```
          logger.info("[SessionResolver] Resolved '%s' -> '%s'", display_name, path.stem)
          ```
        - Check for duplicates: if a match was already found from a different file, log:
          ```
          logger.info("[SessionResolver] Duplicate custom-title '%s' in %s (keeping first match)", display_name, path)
          ```
          and break out of the line loop (do NOT replace the first match).
        - Otherwise, record this file's stem as the resolved UUID and break out of the line loop.

   e. After iterating all files: return the resolved UUID string if found, else `None`.

   f. **Security**: `display_name` is only used as a string equality comparison against `parsed.get("customTitle")`. It MUST NOT be used to construct any file path or perform any file system operation. The UUID returned is always the `.stem` of the `.jsonl` file.

3. Create `tests/test_session_resolver.py` with the following test cases (use `pytest`, `tmp_path` fixture, no mocking of the filesystem — write actual temp files):

   a. `test_is_uuid_valid` — canonical UUID strings return True.
   b. `test_is_uuid_invalid` — display names, UUIDs with wrong length/format, empty string return False.
   c. `test_resolve_exact_match` — a .jsonl file contains a `custom-title` line for "trading-alerts"; function returns the file's stem.
   d. `test_resolve_no_match` — no file contains a matching entry; function returns `None`.
   e. `test_resolve_project_dir_missing` — `project_dir` does not exist; function returns `None` without raising.
   f. `test_resolve_malformed_json_skipped` — .jsonl file with a malformed line followed by a valid custom-title line; function still resolves correctly (malformed line is skipped).
   g. `test_resolve_unreadable_file_skipped` — one file that cannot be read (use `chmod 000` on Linux, or monkeypatch `open` to raise `OSError`); function skips it and resolves from a second file that is readable.
   h. `test_resolve_path_traversal_in_custom_title` — a .jsonl file contains `{"type": "custom-title", "customTitle": "../../etc/passwd"}`; searching for `"../../etc/passwd"` returns the file stem (string match only) — no file system access using that string.
   i. `test_resolve_duplicate_titles` — two .jsonl files both claim the same custom title; function returns the stem of the first file encountered without crashing.
   j. `test_resolve_line_too_long` — a line longer than `_MAX_LINE_BYTES` is skipped; a subsequent valid line in the same file is still parsed.
   k. `test_resolve_extra_fields_in_json` — a `custom-title` line with extra fields still matches.
   l. `test_resolve_custom_title_missing_field` — a line with `{"type": "custom-title"}` (no `customTitle`) does not match and does not crash.
   m. `test_resolve_uuid_input` — calling `resolve_session_id` with a value that is already a UUID still works (no special case needed in the resolver; callers are responsible for calling `is_uuid` first).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- `session_resolver.py` must have NO imports from `task_orchestrator.*` — it is a pure utility module depending only on stdlib
- The resolver accepts `project_dir: Path` directly; it does NOT call `derive_claude_project_dir` or import from `cleanup.py`
- Do NOT modify `cleanup.py`, `api/tasks.py`, `factory.py`, or any other existing module in this prompt — only create the new module and its tests
- `is_uuid` must use the compiled regex `_UUID_RE`, not `uuid.UUID()` — parsing may raise on malformed input, regex is safer here
- The `customTitle` field from JSON is used only for string comparison — never as a path component
- Lines longer than `_MAX_LINE_BYTES` bytes are skipped silently (log at debug level only)
- Each `.jsonl` file is iterated line by line — do NOT read the entire file into memory at once
</constraints>

<verification>
Run `make precommit` — must pass.

Confirm the test file exists and all tests pass:
```
python -m pytest tests/test_session_resolver.py -v
```
</verification>
