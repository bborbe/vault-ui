---
status: completed
summary: 'Live sessions are now bound via the process table: _parse_live_session_ids/_parse_live_processes recognize both --resume and --session-id, a cached name→session-id map feeds resolve_session_id first (falling back to the transcript scan), and the board recognises sessions it started itself instead of offering duplicate Starts'
execution_id: vault-ui-exec-086-ps-session-binding
dark-factory-version: dev
created: "2026-09-09T09:20:00Z"
queued: "2026-09-09T07:49:23Z"
started: "2026-09-09T07:58:53Z"
completed: "2026-09-09T08:03:13Z"
---

# Bind live Claude sessions via the process table instead of transcript titles

<summary>
- The board can identify which Claude session belongs to a task even when several sessions share the same name
- Sessions the board itself starts are now recognised as running, instead of appearing idle and offering a duplicate start
- A running session the board newly recognises can also be taken over, not just observed
- Session identity is read from the operating system's list of running processes, which cannot confuse two sessions with the same name
- Reading names out of session history files stays as a fallback for sessions that have already ended
- Sessions that ended are still classified exactly as before — no change to that behaviour
- Scanning the process list stays throttled, so a board full of cards does not slow down
</summary>

<objective>
Make the running-process table the primary source for matching a task to its Claude session, so that tasks whose sessions share a name still bind to the right session — today they bind to none, the board offers "Start" for a task that already has a session running, and clicking it launches a duplicate.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `src/vault_ui/activity.py` — this is the file that changes most. Find:
- `_UUID_RE` — the bare UUID pattern (no anchors)
- `_parse_resume_session_ids(ps_output: str) -> set[str]` — matches `--resume <uuid>` only, filters lines to those containing `claude`
- `_current_resume_session_ids()` / `_cached_resume_session_ids(ttl)` — shell out to `ps -axww -o args=`, 30 s module-level cache in `_ps_cache`
- `_parse_resume_processes(ps_output: str) -> dict[str, int]` — same matcher over `ps -axww -o pid=,args=`, used by take-over
- `classify_session_state(...)` — returns `live` / `quiet` / `indeterminate` / `None`

Read `src/vault_ui/session_resolver.py` — `is_uuid(value)` and `resolve_session_id(display_name, project_dir)`. The resolver scans every `*.jsonl` transcript for the last `custom-title` line and returns the stem only when exactly one session currently carries the name; two or more is ambiguous and it returns `None`.

Read `tests/test_activity.py` — it already drives the ps matcher with fake `ps` output strings and injects `resume_session_ids` into `classify_session_state`. Follow that style exactly; do not shell out to `ps` in tests.

Read `tests/test_session_resolver.py` for the resolver's existing test style.

Background: `vault-cli` starts every session for a task with `-n "<task name>"`, so relaunching a task produces another transcript carrying the same title. Measured on the author's machine: 47 of 212 titles are shared by 2+ transcripts. A single `ps` row, by contrast, carries the name and the session id together and cannot collide.
</context>

<requirements>
1. In `src/vault_ui/activity.py`, generalise the session-id matcher so it recognises **both** `--resume <uuid>` and `--session-id <uuid>`. Keep the existing `claude`-in-line filter and the exact-UUID requirement — the launcher wrapper still must not count. Rename `_parse_resume_session_ids` to a name that reflects both flags (e.g. `_parse_live_session_ids`) and update all call sites; keep the public behaviour of `classify_session_state` unchanged for callers that inject `resume_session_ids`.

2. Apply the same both-flags widening to `_parse_resume_processes` (the session-id → PID map behind take-over), and rename it to match — e.g. `_parse_live_processes` — so no `_resume_*`-named function is left matching `--session-id`. Update its call sites (`_current_resume_processes`, and `terminate_resumed_session` in the same file). This is required, not optional: once requirement 1 makes a headless `--session-id` session classify as `live`, the board hides Resume and offers Take-Over instead — and take-over must be able to find and signal that process. Leaving this matcher on `--resume` only would make Take-Over silently return `False` for exactly the sessions this prompt newly reports as live.

3. In `src/vault_ui/activity.py`, add a function that extracts a **name → session-id mapping** from `ps` output: for each line containing `claude` that has both a `-n <name>` argument and a `--session-id <uuid>` argument, map the name to the uuid. Contract notes:
   - The name is unquoted in `ps` output and contains spaces, so it runs until the next flag. A lookahead terminator such as `(?=\s+-{1,2}[a-zA-Z])` is the shape that works; anchoring on whitespace alone will truncate the name at its first space.
   - A name mapping to two different uuids in one scan is ambiguous — omit it from the mapping rather than picking one.
   - Expose a cached accessor alongside the existing `_cached_resume_session_ids`. Restructure `_ps_cache` so the raw `ps` output (or both derived views) is cached once per `_PS_CACHE_TTL_SECONDS` window and both the id-set and the new name → uuid mapping are derived from that single cached scan — do not add a second independent `ps` shell-out per card.

4. In `src/vault_ui/session_resolver.py`, make `resolve_session_id(display_name, project_dir)` consult the live process mapping **first** and fall back to the existing transcript-title scan when the name is not currently running. Add an optional injectable parameter for the mapping (defaulting to the live cached one) so tests never shell out, mirroring how `classify_session_state` takes `resume_session_ids`. When the process table resolves the name, return that uuid without scanning transcripts. Keep the existing ambiguity refusal for the transcript fallback path — a name shared by two ended sessions must still return `None`.

5. Tests. Use the fake-`ps`-table style already in `tests/test_activity.py`; do not invoke `ps`, no subprocess, no network. Cover at minimum:
   - a `--session-id <uuid>` row classifies `live` (the case that fails today)
   - a `--resume <uuid>` row still classifies `live` (no regression)
   - a row with neither flag yields no id
   - the launcher-wrapper line (contains the uuid but is not a `claude` process) still does not count
   - `-n <name>` + `--session-id <uuid>` on one row produces the name → uuid mapping, with a multi-word name preserved in full
   - one name appearing with two different uuids is omitted as ambiguous
   - `resolve_session_id` returns the process-table uuid for a live name whose title is shared by 2+ transcripts (the defect this prompt fixes)
   - `resolve_session_id` still returns `None` for a name shared by 2+ transcripts when no matching process is running
   - take-over's process map finds a `--session-id` row

   Use these real observed `ps` rows as fixture material rather than inventing shapes (truncated for brevity; keep the flag order):
   ```
   64387 claude --settings {"theme":"custom:work-green"} --model x --print -n BRO-21903 Check Builds -p /vault-cli:work-on-task "/path/BRO-21903 Check Builds.md" --non-interactive --output-format json --session-id 0bc9bb57-7034-49b5-b73c-70fe0682e953
   40794 claude --settings {"theme":"custom:private-blue"} --model x --add-dir /tmp --resume cbe578a1-3338-4c7c-8fb6-f07cb34eda8d
   76493 claude --settings {"theme":"custom:private-blue"} --model x --add-dir /tmp --resume boss
   18880 claude --settings {"theme":"custom:private-blue"} --model x --add-dir /tmp
   ```
   Note the third row resumes by **name**, not uuid — it must not produce a uuid. The fourth has no session flag at all and is out of scope.

6. Add a `## Unreleased` section at the top of `CHANGELOG.md` (below the intro paragraph, above `## v0.63.2`) with a `- fix:` bullet describing the user-visible effect: the board now recognises sessions it started itself and binds tasks whose session names collide.

7. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Do NOT change how a session with no transcript and no process is classified — `indeterminate` stays.
- Do NOT change `LIVE_WINDOW` or the transcript-recency rule; this prompt only widens which processes count as proof of life.
- Do NOT add a second `ps` invocation per card — reuse the existing single cached scan per TTL window.
- Do NOT delete or rewrite the transcript-title resolution path; it remains the fallback for ended sessions.
- Bare interactive sessions (no `--resume` and no `--session-id`) are explicitly out of scope — their uuid is not recoverable from the process table, and they must keep today's behaviour.
- Existing tests must still pass.
- No real subprocess, network, or Claude API calls in tests — mock or inject.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the widened matcher is actually in place:
- `grep -c 'session-id' src/vault_ui/activity.py` -- must print a non-zero count
- `! grep -q 'def _parse_resume_session_ids' src/vault_ui/activity.py` -- the old single-flag name must be gone
- `grep -c 'Unreleased' CHANGELOG.md` -- must print a non-zero count
</verification>
