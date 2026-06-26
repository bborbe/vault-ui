---
status: prompted
approved: "2026-06-26T08:02:38Z"
generating: "2026-06-26T08:09:02Z"
prompted: "2026-06-26T08:09:02Z"
branch: dark-factory/set-claude-session-name-to-task-title
---

## Summary

- When the user clicks Start (or runs work-on-task / create-task) on a task in the Kanban UI, the resume command emitted in the modal embeds `-n <task-title>` so the launched Claude Code session is named with the task title.
- The display name appears in the prompt box, `/resume` picker, and terminal title from the first turn, with no operator action.
- Eliminates the per-session manual `/rename <task title>` the user runs every time they start a session from the orchestrator.
- Pure backend change in `src/task_orchestrator/api/tasks.py::_build_resume_command`; no UI change, no API contract change, no vault-cli change.

## Problem

The task-orchestrator's Start button hands the user a resume command of the form `cd <vault> && claude --resume <session_id>`. The resulting interactive session shows up in the `/resume` picker and the terminal title as Claude Code's auto-generated `ai-title` (or untitled) — not as the task the operator is working on. To keep sessions identifiable across multiple parallel tasks, the operator manually runs `/rename <task title>` at the start of every session. The rename is identical every time, trivially derivable from the task already known to the orchestrator, and pure friction.

Claude Code (v2.1.187+) supports `-n <name>` on `--resume` and writes the name to the session's `custom-title` + `agent-name` records on the first turn — verified empirically: `claude --resume <id> -n "RENAMED" -p "noop"` overwrites the prior `custom-title`. So a single append to the resume command string fully eliminates the manual rename.

## Goal

After this work, every resume command emitted by task-orchestrator includes `-n <shell-quoted task title>` whenever the task has a non-empty title. The launched Claude Code session displays the task title in its prompt box, `/resume` picker, and terminal title from the first turn. Both the Start path (`run_task` → `POST /api/tasks/{id}/run`) and the slash-command path (`execute_slash_command` for `work-on-task` / `create-task`) include the flag. Fast-path commands (defer-task, complete-task) that do not launch claude are unchanged.

## Non-goals

- No change to vault-cli or to the headless `claude` session creation — the headless session has no UI surface where a name would be observed.
- No change to the fast-path commands (`defer-task`, `complete-task`) — they run vault-cli only, no claude.
- No truncation, slugification, or transformation of the task title — pass it through verbatim. Claude Code's `-n` accepts long names; the picker handles display truncation.
- No frontend-side change — the modal already shows the task title separately. This spec is about the resume command string only.
- No persistence of the chosen name in task frontmatter — the name is derived from `task.title` at command-build time.

## Acceptance Criteria

- [ ] `_build_resume_command(vault_config, "abc-123", task_title="My Test Task")` returns a string containing the substring `-n 'My Test Task'` (or equivalent `shlex.quote` form). Evidence: `uv run pytest tests/api/test_tasks.py -k build_resume_command_includes_name -v` reports `PASSED`.
- [ ] `_build_resume_command(vault_config, "abc-123", task_title="")` returns a string that does NOT contain ` -n ` (substring search). Evidence: `uv run pytest tests/api/test_tasks.py -k build_resume_command_without_name -v` reports `PASSED`.
- [ ] `_build_resume_command(vault_config, "abc-123", task_title="Title with 'apostrophe' and space")` returns a string whose tokens (via `shlex.split`) include the literal value `"Title with 'apostrophe' and space"` immediately after a `-n` token. Evidence: `uv run pytest tests/api/test_tasks.py -k build_resume_command_quotes_special_chars -v` reports `PASSED`.
- [ ] `_build_resume_command(vault_config, "abc-123", task_title="Foo")` preserves the `cd "<cwd>" && ` prefix when `vault_config.session_project_dir` is set, with `-n 'Foo'` placed AFTER `--resume <id>`. Evidence: `uv run pytest tests/api/test_tasks.py -k build_resume_command_keeps_cwd_prefix -v` reports `PASSED`.
- [ ] `POST /api/tasks/{id}/run` returns a `SessionResponse` whose `command` field, when tokenised via `shlex.split`, contains a `-n` token immediately followed by the task's title as a literal token. Evidence: `uv run pytest tests/api/test_tasks.py -k run_task_command_includes_task_title -v` reports `PASSED`.
- [ ] `POST /api/tasks/{id}/execute-command` with `command="work-on-task"` returns a `SessionResponse` whose `command` field, when tokenised via `shlex.split`, contains a `-n` token immediately followed by the task's title as a literal token. Evidence: `uv run pytest tests/api/test_tasks.py -k execute_work_on_task_command_includes_task_title -v` reports `PASSED`.
- [ ] `POST /api/tasks/{id}/execute-command` with `command="defer-task"` returns a `SessionResponse` whose `command` field, when tokenised via `shlex.split`, contains NO `-n` token (fast-path unchanged). Evidence: `uv run pytest tests/api/test_tasks.py -k execute_defer_task_command_unchanged -v` reports `PASSED`.
- [ ] `make precommit` exits 0 (format + lint + typecheck + full test suite).
- [ ] `CHANGELOG.md` has a new bullet under the `## Unreleased` section mentioning the session-name behaviour. Evidence: `awk '/^## Unreleased/,/^## v/' CHANGELOG.md | grep -niE 'session.*name|claude.*-n' | head -1` returns at least one line.

## Verification

```
make precommit
```

## Desired Behavior

1. `_build_resume_command` accepts a new optional keyword argument `task_title: str | None = None` and, when truthy, appends ` -n <shlex.quote(task_title)>` to the returned command string.
2. The `-n` flag is positioned AFTER `--resume <session_id>` and BEFORE the end of the command string. The `cd "<cwd>" && ` prefix (when `session_project_dir` is set) stays at the start; `-n` lands inside the same logical claude invocation.
3. When `task_title` is empty, `None`, or whitespace-only, the command string omits `-n` entirely and is byte-identical to today's output.
4. `task_title` is quoted with `shlex.quote`; any title containing spaces, single/double quotes, or shell-meta characters round-trips through `shlex.split` back to its original value.
5. `run_task` (`POST /api/tasks/{id}/run`) passes `task.title` through to `_build_resume_command` (it already reads `task = await client.show_task(task_id)` earlier in the function).
6. `execute_slash_command`'s session path (`work-on-task` / `create-task`) passes `task.title` through to `_build_resume_command` the same way.
7. The fast-path branch of `execute_slash_command` (`defer-task` / `complete-task`) is unchanged — these commands shell out to `vault-cli` and never invoke claude, so `-n` is not applicable.

## Constraints

- Must not change the external API contract of `_build_resume_command` for existing callers — the new parameter is keyword-only with a backward-compatible default (`None`).
- Must not modify vault-cli, the headless session creation path, or the `SessionResponse` Pydantic model.
- Must not change the modal UI, the `/api/tasks/{id}/run` endpoint shape, or the WebSocket broadcast payload.
- Must not change behavior when `vault_config.session_project_dir` is set vs unset other than appending `-n …` to the claude invocation.
- Must not change the fast-path command-building for `defer-task` / `complete-task`.
- Must not regress any existing test in `tests/`.
- Title is passed verbatim — no truncation, slugification, normalization, or lowercasing.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Task title contains apostrophes / double quotes / shell-meta characters | `shlex.quote` wraps the title in single quotes (escaping inner single quotes via the standard `'\''` idiom); the command string is paste-safe | None — handled by `shlex.quote` |
| Task title is empty string in frontmatter | `_build_resume_command` omits `-n`; command matches today's output | None — graceful degradation, identical to pre-spec |
| Task title contains a newline character | `shlex.quote` quotes the newline; claude's `-n` parser accepts it as part of the name. Display behavior is whatever claude does (likely truncated at first line) | None — out of scope; vault titles should not contain newlines per task-writing conventions |
| Operator's `claude` binary is older than v2.1.187 and does not recognize `-n` | `claude` exits with an unknown-flag error; the session is not launched | Operator upgrades `claude`. The orchestrator already assumes a recent claude (it embeds `--resume <uuid>`). |
| `task.title` lookup fails (task file unreadable) | Existing exception path fires (`HTTPException 404` / `500`) — unchanged | None — pre-existing behavior |

## Do-Nothing Option

The user keeps running `/rename <task title>` at the start of every orchestrator-launched session. At ~5 seconds per session and growing context-switch cost as parallel sessions accumulate in the picker (currently 100+ recent sessions per project), the friction compounds. The fix is a single keyword argument + two call-site updates + 5–7 unit tests + a CHANGELOG bullet. Defer cost: every session forever; ship cost: < 1 hour container time. No upside to deferring.
