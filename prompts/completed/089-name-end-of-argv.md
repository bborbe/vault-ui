---
status: completed
summary: 'Widened _SESSION_NAME_RE in src/vault_ui/activity.py so a -n <name> in final argv position binds to its session id, added launcher-shape fixtures and tests to tests/test_activity.py, and documented the fix in CHANGELOG.md under ## Unreleased'
execution_id: vault-ui-exec-089-name-end-of-argv
dark-factory-version: dev
created: "2026-09-09T15:30:00Z"
queued: "2026-09-09T13:31:47Z"
started: "2026-09-09T13:34:09Z"
completed: "2026-09-09T13:36:15Z"
---

# Match the session name when `-n <name>` is the final argument

<summary>
- A session launched by the `cc-*` launcher scripts — where `-n "<task name>"` is the LAST argument, after `--resume <uuid>` — now binds its name to its session id
- Names in the middle of the command line still bind exactly as before, with multi-word names preserved in full
- A `-n` with no name after it still produces no mapping entry
- The wall can now resolve tasks like `Check Failed Builds Watcher` to their session UUID instead of leaving the display name unrepaired forever
- A `-n` followed by no name at all (or only whitespace) still produces no session mapping
- Sessions started with the name before other flags keep binding exactly as before, multi-word names intact
</summary>

<objective>
A session launched by the `cc-*` launcher scripts — where `-n "<task name>"` is the LAST argument, after `--resume <uuid>` — never binds its name to its session id, so the board keeps the task's display-name `claude_session_id`, the card can never resolve to its UUID, and it cannot show Live. The name matcher's terminator requires a flag AFTER the name; at end-of-argv there is none, so the match fails outright.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read `src/vault_ui/activity.py`:
- `_SESSION_NAME_RE` (~line 47) — `(?<!\w)-n\s+(.+?)(?=\s+-{1,2}[a-zA-Z])`. The name is unquoted in `ps` output and may contain spaces, so it runs until the next flag (`-p /vault-cli:...`) rather than the first space. The trailing lookahead `(?=\s+-{1,2}[a-zA-Z])` is the defect: it demands a following flag, so a name in final position never matches.
- `_parse_live_session_names` (~line 147) — iterates lines containing `claude`, requires both `_SESSION_ID_FLAG_RE` (either `--resume` or `--session-id`) and `_SESSION_NAME_RE` to match, maps name→uuid, omits names bound to two different uuids.

Read `tests/test_activity.py` — the fake-`ps`-table style is established. The current fixtures (`PS_HEADLESS`, `PS_RESUME`, `PS_RESUME_BY_NAME`, `PS_NO_FLAG`) all carry `-n` mid-argv or not at all; none has `-n` as the final argument. That is why the defect shipped: the fixtures were real captured rows, but sampled when only headless `--print` launches carried `-n`.

The real launcher-shaped row (captured live, PID 42875, task `Check Failed Builds Watcher` in the Brogrammers vault) — flag order preserved, middle truncated for brevity:
```
claude --settings {"theme":"custom:work-green"} --model x --add-dir /tmp --resume ebd4c030-c912-47ef-96f2-5bd4da80d206 -n Check Failed Builds Watcher
```
Here the name is genuinely the last argument: `--resume <uuid>` comes before `-n <name>`, and nothing follows the name. This is the exact shape this prompt must make match.
</context>

<requirements>
1. In `src/vault_ui/activity.py`, widen `_SESSION_NAME_RE` so the name may terminate at the end of the line as well as at the next flag. The terminator must accept both a following flag AND end-of-string — with any trailing whitespace tolerated, since `ps` lines may carry a trailing space. The form that satisfies all contract cases is:
   ```
   (?<!\w)-n\s+(?!-{1,2}[a-zA-Z])(.+?)(?=\s+-{1,2}[a-zA-Z]|\s*$)
   ```
   (The `\s*$` alternative is what makes end-of-argv match; `\s*` keeps a trailing-space name from leaking the spaces into the captured value.) Update the comment above the regex to state that the name runs until the next flag or the end of the line. A name that itself starts with `-` would no longer match — pathological and consistent with the no-junk-as-name intent.

2. Preserve existing behaviour exactly for non-end-of-argv shapes:
   - a name followed by another flag still terminates at that flag, multi-word name intact (`-n BRO-21903 Check Builds -p /vault-cli:work-on-task …`)
   - a name followed by a `--session-id` still binds (headless `--print` shape unchanged)
   - a `-n` with no value after it (bare `-n` or `-n ` at end of line) produces NO mapping entry — the widened matcher must not capture trailing junk as a name

3. Tests. In `tests/test_activity.py`, add fixtures and cases in the existing style (fake `ps` strings, no subprocess). Cover at minimum:
   - the launcher shape: a row ending in `--resume ebd4c030-c912-47ef-96f2-5bd4da80d206 -n Check Failed Builds Watcher` maps `{"Check Failed Builds Watcher": "ebd4c030-c912-47ef-96f2-5bd4da80d206"}` — use the real row quoted in `<context>` as the fixture (this is the regression this prompt fixes; the task's Success Criteria require the fixture to be that real captured row, not a hand-invented shape)
   - the same launcher row with trailing whitespace after the name still maps to the name without the trailing spaces
   - the existing headless mid-argv shape still maps (no regression) — the existing `test_parse_live_session_names_maps_name_to_session_id` already covers this and must keep passing unmodified
   - a row whose final argument is a bare `-n` (no name) produces no entry
   - a row whose final argument is `-n` directly followed by a flag (`-n -p /vault-cli:work-on-task`) produces no entry
   - a row whose final argument is `-n` directly followed by a flag (e.g. `-n -p /vault-cli:work-on-task`) produces no entry — the widened matcher must not capture the flag itself as a name
   - the existing ambiguity test (one name, two uuids) still yields `{}`

4. Add a bullet to the existing `## Unreleased` section of `CHANGELOG.md` (create it below the intro paragraph and above the most recent released heading if it is absent). Describe the user-visible effect: a session whose `-n <name>` is the final command-line argument now binds to its session id, so launcher-started sessions resolve instead of keeping a display name forever.

5. Before you finish, re-run the `<verification>` command and confirm it passes; then walk each requirement above against your change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- Do NOT change `_SESSION_ID_FLAG_RE` (the `--resume` / `--session-id` matcher) — it already covers both flags and is not part of this defect.
- Do NOT change `_parse_live_session_ids`, `_parse_live_processes`, `resolve_session_id`, `session_resolver.py`, or `cleanup.py` — out of scope for this prompt.
- Do NOT alter the both-flags requirement in `_parse_live_session_names` (a name without a session-id flag must still not map) and do NOT weaken the ambiguity rule (one name bound to two uuids stays omitted).
- Do NOT change the fixture rows already present in `tests/test_activity.py` — only add to them.
- Existing tests must still pass.
- No real subprocess, network, or Claude API calls in tests — the fake-`ps`-string style only.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass.

Then confirm the end-of-argv behaviour is actually in place:
- `grep -cF '\s*$' src/vault_ui/activity.py` -- must print a non-zero count (the new end-of-line terminator alternative)
- `grep -c 'ebd4c030-c912-47ef-96f2-5bd4da80d206 -n Check Failed Builds Watcher' tests/test_activity.py` -- must print `1` (the real launcher row is a fixture)
- `uv run python -c "import sys; sys.path.insert(0,'src'); from vault_ui.activity import _parse_live_session_names; row='claude --settings {} --model x --add-dir /tmp --resume ebd4c030-c912-47ef-96f2-5bd4da80d206 -n Check Failed Builds Watcher'; r=_parse_live_session_names(row); assert r=={'Check Failed Builds Watcher':'ebd4c030-c912-47ef-96f2-5bd4da80d206'}, r; print('OK')"` -- must print `OK`
</verification>
