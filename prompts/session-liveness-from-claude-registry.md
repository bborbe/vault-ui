---
status: draft
created: "2026-09-30T20:50:00Z"
---

# Mark a Session Live From the Claude Registry, Not Only From Transcript Recency

<summary>
- The board stops calling an alive-but-idle worker "quiet"
- Liveness consults Claude Code's own session registry, in addition to transcript recency and the process scan
- A task whose session the registry lists renders live even when its transcript has gone stale
- The task list can be filtered to just those tasks over the API
- The registry is read fresh on every request, so a worker that exits flips to quiet without a server restart
- A missing or unreadable registry degrades to today's behaviour rather than erroring
- No second liveness field is introduced; existing states, the Live badge and every other endpoint are unchanged
</summary>

<objective>
Make the board able to tell the operator which open tasks have a running Claude session. Today `session_state` derives liveness from transcript recency (a five-minute window) plus a `--resume`/`--session-id` process scan, so a worker that is alive but idle — no transcript write in five minutes, and not launched with a resume flag — renders `quiet`, identical to an orphan whose session exited. Measured against the deployed board on 2026-09-30: of 147 open tasks carrying a `claude_session_id`, 14 were present in `~/.claude/sessions/`, and only 6 rendered `live`. The registry is the harness's own record that a session is running; merging it closes that gap without adding a second, contradicting indicator.
</objective>

<context>
Read `CLAUDE.md` for project conventions and `docs/dod.md` for the definition of done the daemon validates against.

The classifier and its call sites — read these before writing anything:

- `src/vault_ui/activity.py` — `classify_session_state` is the function to extend. `LIVE_WINDOW` (5 minutes) and `_SESSION_ID_FLAG_RE` (`(?:--resume|--session-id)`) are its two existing signals, and `_cached_live_session_ids` wraps the 30-second `ps` cache. `_claude_projects_root()` is the shape to mirror for the new registry root: a `Path.home()`-anchored helper, so a test can point it elsewhere. Note the signature already takes an injectable `resume_session_ids: set[str] | None` — that is the seam the new signal follows.
- `src/vault_ui/api/tasks.py` — `_task_to_response` and `_goal_to_response` each build one card, and each already calls `classify_session_state`. Both run inside per-request loops, so the registry must be read **once per request** and threaded in, never per card. `list_tasks` owns the `GET /api/tasks` query parameters; `_process_vault` and `_process_goal_vault` are the per-vault helpers that call the two response builders.
- `src/vault_ui/api/models.py` — `session_state` is **already** a field on both `TaskResponse` and `GoalResponse`. Do not add a second liveness field.
- `tests/test_activity.py` — the existing `test_classify_*` cases are the home for the new unit tests.
- `tests/test_session_live_gating.py` — already pins a hermetic root via `monkeypatch.setattr("vault_ui.activity._claude_projects_root", lambda: projects_root)`. Requirement 6's registry root should follow that pattern, against `vault_ui.activity._claude_sessions_root`.

Facts verified against the live system on 2026-09-30 — do not re-derive them:

- `~/.claude/sessions/` holds one `<pid>.json` per live Claude Code session (31 at the time of writing). Each carries `pid`, `sessionId` (a UUID), `cwd`, `status`, `name`, `startedAt`. **`sessionId` is the key that matches a task's `claude_session_id`.** The `status` field is deliberately **not** used by this change — presence is the signal.
- The directory is mode `0700` and the files `0600`/`0644`, all owned by the user the service runs as, so no privilege change is needed to read them.
- No reader for `~/.claude/sessions/` exists anywhere in `src/` today; `grep -rn "claude/sessions" src/` exits 1. This signal is genuinely new.
- `src/vault_ui/launch_registry.py` and `src/vault_ui/session_lock_registry.py` are **in-memory and process-local**. They are unrelated to the on-disk `~/.claude/sessions/` directory — do not extend or reuse them.
- These are **host-runtime facts about the deployed service**. The container's `Path.home()` is not the host's and the container never reads this directory, so the measurement above (147 open tasks, 14 in the registry, 6 rendering `live`) is context for the change — not something `<verification>` reproduces.
</context>

<requirements>
1. **Registry reader** in `src/vault_ui/activity.py`: add a `_claude_sessions_root() -> Path` helper returning `Path.home() / ".claude" / "sessions"`, mirroring `_claude_projects_root()`, and a public `read_registry_session_ids(root: Path | None = None) -> set[str]` that returns the `sessionId` of every `*.json` in that directory.

   It must **never raise**. A missing directory, an unreadable directory (`PermissionError`), an unreadable file, and a file whose contents are not valid JSON each contribute nothing — return the ids that were readable, and return an empty set when none were. Catch `OSError` and `json.JSONDecodeError` at the narrowest scope; log at `debug` through the module logger, matching the existing `logger.debug("[Activity] Cannot run ps: %s", e)` style. A file whose JSON parses but carries no `sessionId` is skipped. The `root` parameter exists so tests can point it at a temp directory; the default is the helper's value.

   Do **not** cache this. It is read once per request by requirement 3, and a cache would break requirement 6's no-restart transition. Do not add a module-level global for it the way `_ps_cache` caches the process table.

2. **Merge into the classifier**: add `registry_session_ids: set[str] | None = None` to `classify_session_state`'s signature, alongside the existing `resume_session_ids`. Resolve it via `read_registry_session_ids()` when it is `None`, then check it **before** the transcript lookup:

   ```
   if not session_id:
       return None
   if registry_session_ids is None:
       registry_session_ids = read_registry_session_ids()
   if session_id in registry_session_ids:
       return "live"
   mtime = transcript_mtime(session_id, project_dir, projects_root)
   ...
   ```

   The ordering is load-bearing and is the whole point of the change. The registry is authoritative: the harness itself knows the session is running. Placing the check after `transcript_mtime` would leave the `mtime is None → "indeterminate"` branch ahead of it, so a registry-live session with no transcript on this host would still render `indeterminate` rather than `live`.

   Extend the docstring's state list to name the new signal, and keep the existing wording for `live` / `quiet` / `indeterminate` / `None` — the vocabulary does not change. Update the paragraph that explains the `ps` cross-check so it reads as one of three signals rather than the second of two.

3. **Read once per request**: thread the set from the request into both response builders. Read it once in `list_tasks` (before the per-vault `asyncio.gather`) and once in `list_goals`, pass it through `_process_vault` / `_process_goal_vault` into `_task_to_response` / `_goal_to_response`, and pass it to `classify_session_state` as `registry_session_ids`. Do not let the per-card call fall back to its own default — that would re-read 31 files for every card on a 600-card board.

4. **`session_live` query parameter** on `GET /api/tasks`: add `session_live: Annotated[bool, Query()] = False` to `list_tasks`, after `upcoming_hours`. When true, restrict the returned list to tasks whose `session_state` is `"live"`. Apply the filter **after** the per-vault gather, over the assembled `all_tasks` list, so it composes with the existing `status` / `phase` / `assignee` / `goal` filters rather than replacing them. Default `False` keeps every existing caller and the board's current request unchanged. Document the parameter in the endpoint's docstring alongside the existing ones — no existing parameter in `list_tasks` carries a `Query(description=...)`, so do not add one.

   **Tasks only.** Do not add a `session_live` parameter to `GET /api/goals`. The summary scopes this to the task list, the board's filter is a tasks-view control, and requirement 6 tests only `/api/tasks` — a goals path would ship as an untested code path.

5. **Tests** in `tests/test_activity.py`, extending the existing `test_classify_*` group. Point `read_registry_session_ids` at a `tmp_path` root rather than the real `~/.claude/sessions/`. Cover, at minimum:
   - a session id present in the registry **and** whose transcript is older than `LIVE_WINDOW` **and** which matches no `--resume` process → `"live"` (this is the regression case; before the change it is `"quiet"`);
   - a session id in the registry with **no** transcript on disk → `"live"`, not `"indeterminate"`;
   - a session id absent from the registry, with a stale transcript and no process → `"quiet"` (the merge must not make everything live);
   - an empty session id → `None`, regardless of the registry;
   - a nonexistent registry directory → empty set, no exception, and classification equal to what the transcript/`ps` model alone returns;
   - an unreadable registry directory → empty set, no exception. `chmod 000` does **not** raise for a root runner (the pinned container image runs as uid 0), so cover this branch by patching the directory read to raise `PermissionError` — do not rely on `chmod` plus `pytest.skip`, which would leave requirement 1's `PermissionError` handling covered by nothing in this container;
   - a directory containing one malformed `.json` and one valid entry → the valid id is returned, the malformed file contributes nothing.

   Write the fixtures as realistic `<pid>.json` payloads carrying `sessionId`, `pid` and `cwd` — not bare `{"sessionId": ...}` stubs — so an upstream rename of that key fails a test instead of silently degrading every row to `quiet`.

   Mock every subprocess; the suite makes no real subprocess or network calls. If a test must reach the real `~/.claude/`, it is wrong — the root parameter exists precisely to avoid that.

6. **Tests** for the endpoint, in `tests/test_api.py` alongside the existing endpoint tests: `GET /api/tasks?vault=<v>&session_live=true` returns only rows whose `session_state` is `"live"`, and the same request with `session_live` omitted is unchanged from today. Drive this with a monkeypatched registry root and the existing mocked vault-cli; assert the serialized key set is unchanged (the new parameter adds no field to `TaskResponse`).

7. **CHANGELOG.md**: the file's head is `## v0.69.1` and there is no `## Unreleased` section. Add one directly above `## v0.69.1`, carrying a single `feat:` bullet. Follow the existing entries' style — the bullet explains the user-visible effect and why the previous behaviour was wrong, not the mechanics.

8. **README.md**: the `## API` section documents the topic endpoints and states that task and goal endpoints exist alongside them, without documenting the task endpoint itself. Add a `### GET /api/tasks` subsection in the same table style as `### GET /api/topics`, listing `vault`, `status`, `phase`, `assignee`, `goal`, `upcoming_hours` and the new `session_live`. One line per parameter; no example payload.

9. Before finishing, re-run `<verification>`, confirm it passes, and walk each numbered requirement above against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- **This prompt is backend-only.** Do not touch `src/vault_ui/static/` — the session column and the board's filter control are a separate, host-side change, because the container has no browser and cannot verify a rendered control.
- Do **not** add a second liveness field. `session_state` on `TaskResponse` / `GoalResponse` already exists; this change adds a signal to it, not a field beside it.
- The four existing states keep their names and meanings: `live`, `quiet`, `indeterminate`, and `None` for a task with no session id. Do not introduce `dead` or `unknown`.
- Do not add a TTL cache or module-level global for the registry read — requirement 6's no-restart transition depends on it being read per request.
- Do not change `_cached_live_session_ids`, `_PS_CACHE_TTL_SECONDS`, or the `ps` scan. The 30-second process-table cache is unrelated and stays.
- Existing tests must still pass and their assertions must not change — but they **may** gain an explicit `registry_session_ids=set()` argument, or an autouse conftest fixture pinning `vault_ui.activity._claude_sessions_root` at a tmp dir, so that no test reaches the real `~/.claude/sessions/`. Requirement 5's hermeticity rule applies to the whole suite, not only the new cases: `test_classify_stale_transcript_without_process_is_quiet` and `test_classify_liveness_is_transcript_only` each assert a fixed outcome for a hardcoded UUID, and would flip to `live` on a host whose registry happens to carry it.
- All paths in this prompt are repo-relative.
</constraints>

<verification>
Run `make precommit` -- must pass (format + test + lint + typecheck).

Then confirm the merge is wired, not merely present:

- `grep -q 'read_registry_session_ids' src/vault_ui/activity.py` -- must succeed.
- `grep -q 'session_live: Annotated\[bool, Query()\]' src/vault_ui/api/tasks.py` -- must succeed, anchoring on the parameter declaration rather than a bare mention (a comment or docstring would satisfy the bare form).
- `grep -q 'registry_session_ids' src/vault_ui/api/tasks.py` -- must succeed, proving the set is threaded from the request rather than defaulted per card.
- `grep -q '^## Unreleased' CHANGELOG.md` -- must succeed.
- `! grep -rq 'session_live' src/vault_ui/static/` -- no frontend file carries the new API parameter, so the backend-only scope held. (Note `-r`: without it, grep exits 2 on a directory operand and `!` turns that into an unconditional pass.)

`make test-integration` is deliberately **not** run here: the container has no browser, and the Playwright cases are host-side. `make precommit` runs the unit suite, which deselects them via the repo's `-m 'not integration'` addopts.
</verification>
