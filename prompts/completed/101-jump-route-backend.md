---
status: completed
summary: Added a server-side jump route (POST /api/tasks/{task_id}/jump) plus a pane_resolver module that reads the fleet-jump credential on the server, resolves a live session's WezTerm pane via who-needs-me.py, activates it over urllib, and exposes a derived jump_pane field on TaskResponse resolved concurrently once per request behind a same-origin gate.
execution_id: vault-ui-jump-to-pane-exec-101-jump-route-backend
dark-factory-version: v0.196.0
created: "2026-10-01T14:10:00Z"
queued: "2026-10-01T10:35:03Z"
started: "2026-10-01T13:37:51Z"
completed: "2026-10-01T13:45:27Z"
cancelled: "2026-10-01T13:25:14Z"
---

# Serve a Board-Local Jump Route That Activates a Live Session's Pane

<summary>
- The board can hand the operator from a live card straight to the session it belongs to
- The hand-off is performed by the server, not by the browser
- The shared credential the hand-off needs is read on the server and never leaves it
- The destination is looked up fresh on every request, so a tab that moved is still followed
- A session with no reachable destination produces a readable refusal instead of a dead link
- The board knows which live sessions are reachable before it draws the control, so a card never offers a hand-off that would fail
- Only a request that came from the board itself can trigger the hand-off
- Nothing about the existing board changes: the live indicator, the filters and every other endpoint behave exactly as before
</summary>

<objective>
Give the vault-ui server a board-local route that activates the WezTerm pane a live session runs in, so the board can hand the operator to running work without the shared jump credential ever reaching the browser. The board already tells the operator a session is live but gives them no way to reach it; the credential that authorises the jump cannot be published in the served document, which is why the jump is proxied server-side rather than linked directly.
</objective>

<context>
Read `docs/dod.md` for the definition of done the daemon validates against. (This repo carries no `CLAUDE.md`: it is gitignored, so it is absent from the worktree and from the container. Project conventions live in `README.md` and `docs/`.)

Read these before writing anything — each is the pattern the new code follows:

- `src/vault_ui/activity.py` — `_claude_projects_root()` is the shape to mirror for every host-path helper in this change: a `Path.home()`-anchored function with no module-level constant, so a test can point it elsewhere by monkeypatching the function. The module's `logger.debug("[Activity] ...", e)` calls are the logging style to match.
- `src/vault_ui/vault_cli_client.py` — the vault-cli subprocess wrapper. `asyncio.create_subprocess_exec` with `stdout=PIPE, stderr=PIPE`, then decode and strip. (`activity.py`, `cleanup.py` and `api/tasks.py` also spawn subprocesses in the same argv-list style; this module is the cleanest example of it.) Follow its argument-passing style exactly: an argv list, never a shell string.
- `src/vault_ui/api/tasks.py` — `take_over_task` is the structural model for the new endpoint: the `@router.post("/tasks/{task_id}/...")` decorator, the `(vault: str, task_id: str)` signature, the long docstring that states the access model, and the `Args:` footer. Read its docstring paragraph beginning "Access model:" — requirement 4 is a deliberate and narrower exception to the position stated there, and the reason is given in full.
- `src/vault_ui/session_resolver.py` — a small, single-purpose resolver module with a module logger, a `None`-on-failure contract, and no exception escaping. This is the module shape `pane_resolver.py` should match.
- `src/vault_ui/api/models.py` — `TaskResponse` already carries derived, server-computed fields (`blocked`, `blockers`, `upcoming`, `recently_completed`), each annotated with a `# Derived:` comment. The new `jump_pane` field follows that exact shape; read those four before adding a fifth.
- `tests/test_activity.py` — the `test_classify_*` group is the model for hermetic tests: monkeypatch the root helper at a `tmp_path` rather than reaching the real home directory.

Facts about the host runtime. The container cannot observe any of these and must not try; they are context for what the code must do, not something `<verification>` reproduces.

- A separate fleet-jump server listens on `127.0.0.1:1337` and accepts `GET /jump?pane=<N>&t=<token>`. Following it activates that WezTerm pane.
- The shared credential is a single line in `~/.claude/secrets/jump-token`, mode `0600`, owned by the user the service runs as, so no privilege change is needed to read it.
- Pane resolution is delegated to the supervisor's own script: `who-needs-me.py --pane-for <8-char session-id prefix>` prints the pane id on stdout and exits `0`, or prints a reason on stderr and exits non-zero. Do not re-implement its logic. Its resolution order — a recorded pane validated against the live WezTerm list by existence *and* title, else the registry's current session name matched against pane titles, else refuse — encodes a recycled-pane-id trap that a naive lookup gets wrong in the direction that hands over a confident link to the wrong tab.
- The service binds loopback for a single operator, so there is no per-user auth; every vault-scoped endpoint takes the same unauthenticated `vault` query parameter.
</context>

<requirements>
1. **New module** `src/vault_ui/pane_resolver.py`, matching `session_resolver.py`'s shape: a module logger, no import-time side effects, and a contract of "return `None` or raise a plain exception — never a bare crash".

   Four functions:

   - `_jump_token_path() -> Path` — returns `Path.home() / ".claude" / "secrets" / "jump-token"`. Mirrors `_claude_projects_root()`: a function, not a module constant, so a test can monkeypatch it.
   - `_who_needs_me_path() -> Path` — returns the supervisor script. Resolve `CLAUDE_PLUGIN_ROOT` from the environment first and use `<that>/scripts/who-needs-me.py`; otherwise fall back to `Path.home() / ".claude" / "plugins" / "marketplaces" / "claude-supervisor" / "scripts" / "who-needs-me.py"`. Same function-not-constant rule.
   - `read_jump_token(path: Path | None = None) -> str | None` — returns the file's contents stripped of surrounding whitespace, or `None` when the file is missing, unreadable, or empty. Catch `OSError` at the narrowest scope and log at `debug`. **Never log the token's value**, not even truncated, and not in the error path — log the path and the exception only.
   - `async def resolve_pane_id(session_id: str) -> str | None` — when `session_id` is falsy, return `None` immediately. Otherwise run `who-needs-me.py --pane-for <session_id[:8]>` with `asyncio.create_subprocess_exec`, passing `sys.executable` as argv[0] and the script path as argv[1] so the script's own shebang and import path are not relied on. Return `stdout.decode().strip()` when the exit code is `0` and the result is non-empty; return `None` on any non-zero exit, logging the stripped stderr at `debug`. Catch `OSError` (a missing script) and return `None`. **Bound the call**: wrap `proc.communicate()` in `asyncio.wait_for(..., timeout=5)` and treat a timeout exactly like a non-zero exit — return `None`. An unbounded subprocess would hang the request that is waiting on it.
   - `async def perform_jump(pane_id: str, token: str) -> None` — issue `GET http://127.0.0.1:1337/jump?pane=<pane_id>&t=<token>` with both values percent-encoded. Use `urllib.request` from the standard library: `httpx` is a **dev-only** dependency in `pyproject.toml` and must not become a runtime one. Because `urllib.request.urlopen` blocks, run it through `asyncio.to_thread` so the event loop is not stalled. Pass a bounded `timeout=5` to `urlopen` — the jump server is a local process, so a hang means it is wedged and the caller should see the `502` rather than wait. Raise on a non-2xx status so the caller can map it.

2. **New endpoint** in `src/vault_ui/api/tasks.py`: `POST /tasks/{task_id}/jump`, declared `@router.post("/tasks/{task_id}/jump", status_code=204)`, signature `async def jump_to_task(vault: str, task_id: str) -> Response`. Add `Response` to the existing `from fastapi import ...` line — it is not imported there today. Model the docstring on `take_over_task`'s, including an `Args:` footer.

   Sequence, in this order:

   - Reject the request unless it passes requirement 3's origin gate. Do this **first** — before any vault or task lookup, so a cross-origin probe cannot use the endpoint's timing or status codes to enumerate task ids.
   - Load the task through the same path `take_over_task` uses. A task that does not resolve is a `404`.
   - Read the task's `claude_session_id`. When it is absent or empty, refuse with `409` and the reason "no session to jump to".
   - `await resolve_pane_id(session_id)`. When it returns `None`, refuse with `409` and the reason "no pane resolves for this session" — the same absence rule the board applies, so a card and this endpoint never disagree about whether a session is reachable.
   - `read_jump_token()`. When it returns `None`, refuse with `503` and the reason "jump credential unreadable".
   - `await perform_jump(pane_id, token)`. A raised failure becomes a `502` with the reason "jump server unreachable".

   Return `204` with **no body** on success. On every path — success, refusal, and error — the response must carry no body containing the token, and no header carrying it.

3. **`jump_pane` on `TaskResponse`** in `src/vault_ui/api/models.py` — add `jump_pane: str | None = None`, carrying a `# Derived:` comment in the style of the neighbouring `blocked` / `blockers` fields. It holds the pane id the session resolves to, or `None`.

   Populate it in `_task_to_response` in `src/vault_ui/api/tasks.py`, **only** for a task whose `session_state` is `"live"`. Every other task keeps `None` and must not trigger a lookup at all — resolving a pane for a quiet or sessionless card would spend a subprocess on something that can never carry the control.

   Resolve once per request, never per card. `_task_to_response` runs inside a per-request loop, so `list_tasks` must resolve every live task **concurrently**: collect the live tasks' session ids, `asyncio.gather` the `resolve_pane_id` calls, and thread the resulting `{session_id: pane_id}` map into `_task_to_response` — exactly the way `registry_session_ids` is already threaded for the liveness classification. A per-card call would spawn one subprocess per live card and serialize them.

   This field is the contract the card renders against: the frontend shows a jump control if and only if `jump_pane` is non-null. It exists because the alternative — drawing the control on every live card and discovering at click time that no pane resolves — cannot satisfy the requirement that a pane-less live card shows no control at all.

4. **Same-origin gate**, a small helper in `src/vault_ui/api/tasks.py`. The request is accepted only when it carries no `Origin` and no `Referer`, or when the `Origin` (or, absent that, the `Referer`'s scheme-and-host) equals the request's own `Host`. A request whose origin is present and differs is rejected with `403`.

   This is load-bearing, not ceremony, and it is the one place this endpoint deliberately diverges from `take_over_task`'s "auth belongs at the service boundary" position. The shared token exists precisely so that a visited web page cannot fire `<img src="http://127.0.0.1:1337/jump?pane=X">`. A board route on the same port without this check re-opens that hole exactly: any page the operator visits could POST to it and move their focus. The token is not a substitute, because the server adds it — the browser never has to know it. State this reasoning in the helper's docstring so a later reader does not "simplify" it away.

5. **Tests.** Unit tests for the resolver in `tests/test_pane_resolver.py`, covering at minimum:
   - `read_jump_token` against a `tmp_path` file returns the stripped value; against a missing path returns `None` without raising; against a file containing only whitespace returns `None`.
   - `resolve_pane_id` with an empty session id returns `None` **without spawning a subprocess** — assert this by patching the subprocess call to raise, so a regression that shells out on an empty id fails loudly rather than silently succeeding.
   - `resolve_pane_id` returns the stripped pane id on exit `0`, and `None` on a non-zero exit.
   - `perform_jump` percent-encodes a pane id and token containing URL-significant characters. Drive this through a monkeypatched `urllib.request.urlopen` that records the URL it was handed, and assert the recorded URL's query values decode back to the originals — a token containing `&` or `=` that reaches the server raw would silently truncate the query.
   - `resolve_pane_id` builds its argv **exactly** as `[sys.executable, <script path>, "--pane-for", <first 8 characters of the session id>]`. Capture the argv by monkeypatching the subprocess call and assert it element by element. This is the one boundary in the change whose failure is silent: a wrong flag name, or the full session id where the script expects the 8-character prefix, exits non-zero at run time with no test that would have caught it. Pass a session id longer than 8 characters so a regression that forwards the whole id is distinguishable.

   Endpoint tests in `tests/test_api.py`, alongside the existing endpoint tests, driving the route through the app with the resolver and token reader monkeypatched — no real subprocess, no real network, no real `~/.claude/`:
   - a task with a live session and a resolvable pane returns `204`;
   - a task with no `claude_session_id` returns `409`;
   - a task whose pane does not resolve returns `409`, and the reason does not name the token path;
   - an unreadable token returns `503`;
   - a request carrying a foreign `Origin` returns `403` and **never reaches the resolver** — assert the resolver was not called, so the gate is proven to run first rather than merely to exist.

   List-endpoint tests for `jump_pane`, in the same file: a live task whose pane resolves carries `jump_pane` set to the pane id; a live task whose pane does not resolve carries `jump_pane: null`; and a non-live task carries `jump_pane: null` **with the resolver never called for it**. Pin the call count — assert the resolver was invoked exactly once per live task and zero times for quiet and sessionless ones. A per-card implementation would still pass a value-only assertion while spawning one subprocess per live card, so the count is the assertion that catches it.

6. **CHANGELOG.md**: add a `## Unreleased` section directly above the current head section, carrying a single `feat:` bullet. Follow the existing entries' style — the bullet explains the user-visible effect, not the mechanics.

7. **Self-check.** Before finishing, re-run every command in `<verification>` and confirm each passes; then walk each numbered requirement above against the change and confirm each is satisfied.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git. This project sets `hideGit: true`, so no `git` command will work inside the container; do not attempt one.
- **This prompt is backend-only.** Do not touch `src/vault_ui/static/` — the jump control on the card is a separate prompt, because the container has no browser and cannot verify a rendered control. Requirement 3's `jump_pane` field is the entire contract that prompt renders against — do not add any further field to `TaskResponse` for it.
- Do not add a runtime dependency. `httpx` is in `pyproject.toml`'s `dev` extra only; the runtime path uses `urllib.request` from the standard library.
- Do not cache the token or the pane id, and do not add a module-level global for either. The pane must be resolved per request: a pane id is recycled across tab moves and WezTerm restarts, so a cached one keeps pointing at a pane that has since become another session's.
- Never log the token value, and never place it in a response body or header on any path.
- Do not change `classify_session_state`, `session_state`, the `● Live` badge, or any existing endpoint's behaviour or signature. `take_over_task` in particular keeps its current shape — this prompt adds a sibling, it does not refactor the existing one.
- Do not re-implement pane resolution. Shell out to the supervisor script per requirement 1; a local reimplementation would duplicate a resolution order that already handles the recycled-pane-id case.
- Existing tests must still pass and their assertions must not change.
- All paths in this prompt are repo-relative. The `~/.claude/...` paths above are host-runtime values the code computes at run time, never file references to read.
</constraints>

<verification>
Run `set -o pipefail; make precommit` -- must pass (format + test + lint + typecheck).

Then confirm the change is wired, not merely present:

- `grep -q 'def read_jump_token' src/vault_ui/pane_resolver.py` -- must succeed.
- `grep -q 'def resolve_pane_id' src/vault_ui/pane_resolver.py` -- must succeed.
- `grep -q 'urllib.request' src/vault_ui/pane_resolver.py` -- must succeed, proving the standard library carries the HTTP call rather than a new runtime dependency. (The `.request` suffix is required: a bare `urllib` pattern matches a comment or an unrelated import.)
- `grep -q '@router.post("/tasks/{task_id}/jump"' src/vault_ui/api/tasks.py` -- must succeed, anchoring on the route **declaration** rather than a bare mention (a docstring or comment quoting the path would satisfy the bare form).
- `grep -q 'jump_pane' src/vault_ui/api/models.py` -- must succeed, proving the field is declared.
- `grep -q 'jump_pane' src/vault_ui/api/tasks.py` -- must succeed, proving the field is populated rather than only declared.
- `! grep -rq 'jump-token\|jump_token' src/vault_ui/static/` -- no frontend file carries the credential's path, so the backend-only scope held. (`-r` is required: without it grep exits 2 on a directory operand and `!` turns that into an unconditional pass.)
- `! grep -rwq 'jump' src/vault_ui/static/` -- the frontend is untouched by this prompt. (`-w` is required, not decoration: `src/vault_ui/static/style.css:1125` contains the word "jumps" in a CSS comment, so a bare `jump` pattern matches the untouched tree and the check could never pass without editing a file the constraints forbid touching.)
- `grep -q '^## Unreleased' CHANGELOG.md` -- must succeed.

`make test-integration` is deliberately not run here: the container has no browser and the Playwright cases are host-side. `make precommit` runs the unit suite, which deselects them via the repo's `-m 'not integration'` addopts.
</verification>
