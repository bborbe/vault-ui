---
status: completed
summary: Rewrote resolve_session_id to match a display name only against each transcript's current title and refuse ambiguous ties with a warning listing all candidate ids, with TDD regression tests and a CHANGELOG Unreleased entry.
execution_id: vault-ui-exec-080-fix-session-resolver-current-title-and-ambiguity
dark-factory-version: dev
created: "2026-08-31T21:48:35Z"
queued: "2026-08-31T21:58:51Z"
started: "2026-08-31T22:01:30Z"
completed: "2026-08-31T22:04:44Z"
---

# Fix session resolver: match only the current title, refuse ambiguous matches

<summary>
- Resolving a session name to a session ID now only considers each session's CURRENT name, never a name it used to have
- A session that was renamed away from a task no longer answers to that task's old name
- When two or more sessions currently share the same name, resolution refuses to guess and returns nothing
- The refusal is logged with the list of session ids that tied, so a human can pick the right one
- The old behavior — silently returning whichever file the filesystem happened to list first — is gone
- Callers already treat "no resolution" as "leave the name in place", so the outcome of an ambiguous name is a visible unresolved name instead of a wrong session binding
- Regression tests cover renamed-away sessions, tied current names, and the single-match happy path
- Existing tolerance for oversized lines, malformed JSON, unreadable files, and a missing directory is preserved
</summary>

<objective>
Make `resolve_session_id` in `src/vault_ui/session_resolver.py` deterministic and correct: match a display name only against each transcript's CURRENT title (the last `custom-title` entry in the file), and return a UUID only when exactly one session matches. This matters because the resolved value is written straight into a task's or goal's `claude_session_id` frontmatter by the watcher, so a wrong-but-real UUID silently points the operator's Resume button at a conversation that was working a different task — 16 such corrupted bindings were found and hand-corrected across two vaults.
</objective>

<context>
Read `CLAUDE.md` for project conventions (dark-factory workflow, test conventions, `make precommit`).

Read these coding guides before writing code:
- `/home/node/.claude/plugins/marketplaces/coding/docs/tdd-guide.md` — write the failing tests first, then the fix
- `/home/node/.claude/plugins/marketplaces/coding/docs/test-pyramid-triggers.md` — the filesystem is a real boundary; one realistic multi-file test belongs at the integration level
- `/home/node/.claude/plugins/marketplaces/coding/docs/python-architecture-patterns.md` — deterministic resolution, no dependence on glob/filesystem ordering

Files to read before making changes:
- `src/vault_ui/session_resolver.py` — the only file whose logic changes; contains `is_uuid` and `resolve_session_id`
- `tests/test_session_resolver.py` — the existing test module to EXTEND (do not create a parallel module); note the `_write_jsonl` helper and the existing `test_resolve_duplicate_titles`
- `src/vault_ui/factory.py` — `_try_resolve_task_session` / `_try_resolve_goal_session`, the watcher callers that write the result via `client.set_field(task_id, "claude_session_id", resolved)`; both already `return` early when the result is `None`
- `src/vault_ui/cleanup.py` — around the `if not is_uuid(session_id):` branch in the goal pass; already only writes when `resolved is not None`
- `src/vault_ui/api/tasks.py` — the `resolve_session_id(...)` call; already falls back to the original display name when the result is `None`
- `CHANGELOG.md` — add an entry under `## Unreleased` (create the section if absent; the top section is currently a released version)
</context>

<requirements>
Follow TDD: write the new/changed tests FIRST, run them and see them fail for the right reason, then change `resolve_session_id`, then run them green.

## 1. Tests first — `tests/test_session_resolver.py`

Extend the existing module. Reuse the existing `_write_jsonl` helper. Do NOT add `@pytest.mark.integration` to any of these tests: that marker is reserved for host-side Playwright tests and is deselected by `addopts = "-m 'not integration'"` in `pyproject.toml`, so a marked test would never run in `make test` / `make precommit`.

1. **Renamed-away session must not match its old title.** One `.jsonl` file whose lines contain, in order, a `custom-title` entry with title `"Old Task Name"` and later a `custom-title` entry with title `"New Task Name"` (interleave a couple of unrelated entries such as `{"type": "summary", ...}` and a `user` entry so the file resembles a real transcript). Assert:
   - `resolve_session_id("Old Task Name", tmp_path) is None`
   - `resolve_session_id("New Task Name", tmp_path) == stem`

2. **Many historical entries, one current title.** Same shape as (1) but with the old title repeated ~5 times and the new title repeated ~3 times, mirroring the real transcript that carried 132 entries under one name and 14 under its successor. Assert the old name resolves to `None` and the new name resolves to the stem.

2b. **A trailing `custom-title` line with no `customTitle` key does not erase the current title.** One file whose lines are: `{"type": "custom-title", "customTitle": "Real Title"}` followed by `{"type": "custom-title"}` (no `customTitle` key). Assert `resolve_session_id("Real Title", tmp_path) == stem` — the keyless entry is skipped, not treated as the current title.

3. **Two files whose CURRENT title is identical resolve to `None`.** Rewrite the existing `test_resolve_duplicate_titles` (it currently asserts `result in (stem_a, stem_b)` — that assertion encodes the bug and must go). New assertion: `resolve_session_id("shared-title", tmp_path) is None`. Use `caplog` (at `logging.WARNING`) to assert the ambiguity is logged and that BOTH candidate stems appear in the emitted log text.

4. **Ambiguity is judged on current titles only.** File A: `custom-title` `"shared-title"` then `custom-title` `"renamed-away"`. File B: `custom-title` `"shared-title"` only. Assert `resolve_session_id("shared-title", tmp_path) == stem_b` — file A must not count as a candidate, so this is a single match, not an ambiguity.

5. **Exactly one match still resolves (no regression).** Keep/confirm `test_resolve_exact_match` passes unchanged.

6. **Preserve the existing edge-case coverage** — these tests must remain and must still pass, adjusted only if the new contract genuinely changes their expected value:
   - `test_resolve_no_match`
   - `test_resolve_project_dir_missing`
   - `test_resolve_malformed_json_skipped`
   - `test_resolve_line_too_long`
   - `test_resolve_unreadable_file_skipped`
   - `test_resolve_extra_fields_in_json`
   - `test_resolve_custom_title_missing_field`
   - `test_resolve_path_traversal_in_custom_title`
   - `test_resolve_uuid_input`

7. **Add one filesystem-integration test.** A single test that builds a realistic project directory in `tmp_path` with five `.jsonl` files at once and asserts the whole resolution contract in one pass through the real glob + real file I/O:
   - file 1: renamed away (current title `"Check Prometheus Alerts - 2026-08-31"`, earlier title `"Agent Gate Failure"`)
   - file 2: current title `"Check Prometheus Alerts - 2026-08-31"` only
   - file 3 and file 4: both currently titled `"Cleanup OmniFocus Inbox - 2026-08-31"`
   - file 5: current title `"Audit Prompt - 2026-08-31"` only — the unique-match control
   - Assert: `"Agent Gate Failure"` → `None` (historical only); `"Check Prometheus Alerts - 2026-08-31"` → `None` (two current matches: files 1 and 2); `"Cleanup OmniFocus Inbox - 2026-08-31"` → `None` (two current matches: files 3 and 4); `"Audit Prompt - 2026-08-31"` → file 5's stem (the only unique current title in the directory).
   - Include at least one oversized (>4096-byte) line and one malformed-JSON line somewhere in these files so the integration test also proves the tolerant paths survive the rewrite. `_write_jsonl` cannot emit those lines — write that one file with a raw `path.open("w")` block, matching the style already used by `test_resolve_line_too_long` and `test_resolve_malformed_json_skipped`. Place them in files 3/4 so the unique-match control (file 5) stays clean.

## 2. The fix — `src/vault_ui/session_resolver.py`

Change only `resolve_session_id`. Leave `is_uuid`, `_UUID_RE`, and `_MAX_LINE_BYTES` exactly as they are. Keep the signature `def resolve_session_id(display_name: str, project_dir: Path) -> str | None:`.

8. **Per file, compute the CURRENT title, not a first hit.** Scan every line of the file and keep the `customTitle` value of the LAST line whose `type == "custom-title"` and which carries a `customTitle` key. The early `break` on the first matching entry is the root cause of defect 1 and must be removed — the whole file has to be scanned to know which title is current. A file with no `custom-title` entry has no current title and is never a candidate. Ordering is file order (transcripts are append-only, so the last entry is the newest); do NOT sort by a `timestamp` field or by file mtime.

9. **Collect all candidates, then decide.** Build a list of stems whose current title equals `display_name`. Then:
   - exactly one candidate → return it, keeping the existing `logger.info("[SessionResolver] Resolved '%s' -> '%s'", ...)` success log
   - zero candidates → return `None` (keep the existing debug-level quiet behavior; no warning)
   - two or more candidates → return `None` and emit a `logger.warning` that names the display name and lists ALL candidate stems (e.g. `"[SessionResolver] Ambiguous title '%s' matches %d sessions: %s — refusing to resolve"` with the sorted candidate list). Sort the candidate list before logging so the message is deterministic.
   - Never return an arbitrary pick. Delete the `"(keeping first match)"` log line and the `resolved is not None` first-wins branch entirely.
   - Expected and accepted: because the cleanup loop (every 5 min) and the watcher callback both re-attempt resolution, a permanently-ambiguous name re-emits this warning on every attempt. Do NOT add dedupe state, a cache, or a rate limiter — the repetition is the operator's cue to rename one of the tied sessions.

10. **Preserve every tolerant path unchanged:**
    - `project_dir.exists()` is checked first; missing directory → debug log + `None`
    - a raw line longer than `_MAX_LINE_BYTES` is skipped (`continue`) without being parsed
    - empty lines are skipped
    - `json.JSONDecodeError` on a line → existing `logger.warning` + skip that line, keep scanning the file
    - `OSError` opening/reading a file → existing `logger.warning` + skip that file, keep scanning the remaining files
    - decoding stays `raw_line.decode("utf-8", errors="replace")`
    - `display_name` is compared as an opaque string; it is never used to build a path

11. **Update the docstring** of `resolve_session_id` to state the new contract: matches a session's current title (the last `custom-title` entry in its transcript), returns the UUID only when exactly one session currently carries that title, and returns `None` for zero matches or for an ambiguous tie (which is logged with the candidate ids).

## 3. Callers and changelog

12. **Do not change any caller.** `factory._try_resolve_task_session`, `factory._try_resolve_goal_session`, the goal pass in `cleanup.py`, and the resolve call in `api/tasks.py` all already handle `None` correctly (no-op / skip / fall back to the display name). Returning `None` more often is exactly the desired downstream behavior: the display name stays in the frontmatter for a human to resolve, rather than a wrong UUID being written.

13. **Add a `## Unreleased` entry to `CHANGELOG.md`** at the top of the version list, describing the user-visible effect: session names now resolve only against a session's current title and an ambiguous name resolves to nothing instead of an arbitrary session, so Resume can no longer be pointed at a conversation that worked a different task.

## 4. Self-check

14. Before you finish, re-run the `<verification>` commands and confirm they pass. Then walk each requirement above against your change: renamed-away name → `None`; tied current names → `None` plus a warning listing all candidate ids; single current match → the stem; all pre-existing tolerance tests still green.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass, with one deliberate exception: `test_resolve_duplicate_titles` asserts the buggy first-wins behavior and must be rewritten to assert `None` + the ambiguity log
- Do NOT change the signature of `resolve_session_id`, and do NOT change `is_uuid`, `_UUID_RE`, or `_MAX_LINE_BYTES`
- Do NOT modify any caller (`factory.py`, `cleanup.py`, `api/tasks.py`) — they already handle `None` correctly
- Do NOT return an arbitrary or "best guess" candidate on a tie, and do NOT add a tie-breaker (mtime, timestamp, sort order, "most recent") — refusing is the required behavior
- Do NOT add configuration, feature flags, or an opt-out for the new strictness
- No real subprocess, network, or Claude API calls in tests — real `tmp_path` files are expected and correct here
- Do NOT add `@pytest.mark.integration` to any new test — that marker is deselected by default `addopts` and the test would silently never run
- Extend `tests/test_session_resolver.py`; do NOT create a parallel session-resolver test module
- Type annotations are required on all new functions (mypy runs in strict mode via `make check`)
- Use module-level `logger` calls with `%s` lazy formatting, matching the existing style in this file; no `print`
</constraints>

<verification>
Run `make precommit` from the repo root — must pass (sync + format + test + lint + typecheck).

Also run and confirm green:
- `uv run pytest tests/test_session_resolver.py -v`
- `uv run pytest tests/test_cleanup.py tests/test_api.py -v` — the three call sites' tests must be unaffected

Then confirm by inspection:
- `! grep -q "keeping first match" src/vault_ui/session_resolver.py` — succeeds only when the first-wins branch is gone
- `grep -n "Unreleased" CHANGELOG.md` prints a match
</verification>
