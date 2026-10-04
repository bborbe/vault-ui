---
status: approved
spec: [023-go-backend-api-and-cutover]
created: "2026-10-04T00:00:00Z"
queued: "2026-10-04T13:10:34Z"
---

# Serve the eighteen write routes through vault-cli's Go library and prove write-side parity

<summary>
- Every action a card or goal exposes on the board — run, take over, jump, execute a command, assign to me, change phase, set a flag, change status, and clear or set a session — is now answered by the Go backend at the same path the frozen frontend already calls.
- The two maintenance routes the board uses to pick up external changes — reload the cache and reload the config — are answered too.
- For every write route the same request against a freshly reset fixture vault leaves the same bytes behind in the vault files as the Python backend leaves, not just the same HTTP response.
- The request bodies are validated the same way: a bad status value is refused with the same status code, and the same guard rejects an identifier that begins with a dash.
- The routes that start or take over a Claude session behave identically when the session cannot start, including the error status and message the board shows.
- Nothing shells out to vault-cli: every vault read and write goes through its in-process Go library.
- The mutations announce themselves to the board through an injected publisher, so the WebSocket work in the next prompt can plug in without touching these handlers.
- The parity harness now covers the write side: it resets the fixture, runs the request against one backend, resets again, runs it against the other, and diffs both the responses and the resulting files.
</summary>

<objective>
Implement the eighteen mutating REST routes on the Go `:8000` surface — task and goal session lifecycle, frontmatter writes, command execution, cache and config reload — reproducing the Python backend's status codes, bodies, error guards, and vault-file side effects exactly, and extend the parity harness to compare the write path.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read the spec `specs/in-progress/023-go-backend-api-and-cutover.md` in full. This prompt covers Acceptance Criteria 1 (routes 7–24), 5 (write-side parity), and the failure-mode rows for vault-cli failure, session spawn failure, and in-flight launches. Prompt 1 (`prompts/1-spec-023-http-router-read-routes-and-parity-harness.md`) already shipped the router, `pkg/api` models, static serving, the error contract, and the harness scaffold — read it before you start and build on it rather than re-creating it.

Read these coding guides in the container — they are the contract for this change:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — handlers in `pkg/handler/`, factories in `pkg/factory/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md` — compose via injected interfaces; business logic never calls package functions directly.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `DescribeTable`/`Entry`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Read the Python source that is the behavior contract — reproduce it exactly, do not "improve" it. `src/vault_ui/api/tasks.py` is ~131 KB: `grep -n` for each route decorator and read 100–250 line windows around the matches. Do NOT read the whole file.
- Route decorators: `/tasks/{task_id}/run` (~1423), `/tasks/{task_id}/jump` (~1731), `/tasks/{task_id}/take-over` (~1809), `/goals/{goal_id}/run` (~1927), `/goals/{goal_id}/take-over` (~2058), `/tasks/{task_id}/execute-command` (~2145), `/tasks/{task_id}/assign-to-me` (~2265), `/tasks/{task_id}/phase` (~2321), `/tasks/{task_id}/flag` (~2435), `/goals/{goal_id}/status` (~2487), `/goals/{goal_id}/execute-command` (~2574), `/tasks/{task_id}/status` (~2673), `/goals/{goal_id}/assign-to-me` (~2758), `DELETE /tasks/{task_id}/session` (~2817), `DELETE /goals/{goal_id}/session` (~2847), `PATCH /tasks/{task_id}/session` (~2950), `/cache/reload` (~3032), `/config/reload` (~3072).
- Request models: `UpdateFlagRequest` (~390), `UpdatePhaseRequest` (~396), `UpdateStatusRequest` (~410), `UpdateSessionRequest` (~426), `ExecuteCommandRequest` (~432).
- Response model `SessionResponse` — `src/vault_ui/api/models.py` (~194).
- The close-out reason gate (~455) and the session overwrite conflict guard (~3013).

Read the vault-cli v0.159.0 library API you will call (module source at `/home/node/go/pkg/mod/github.com/bborbe/vault-cli@v0.159.0`):
- `pkg/ops/workon.go` — `WorkOnOperation.Execute(ctx, vaultPath, taskName, assignee, vaultName, isInteractive, sessionDir, *config.Vault) (MutationResult, error)`; `goal_workon.go` for the goal variant.
- `pkg/ops/frontmatter.go` — `FrontmatterSetOperation.Execute(ctx, vaultPath, taskName, key, value, reason, gateSuccessor, actor, force)` and `FrontmatterClearOperation.Execute(ctx, vaultPath, taskName, key)`.
- `pkg/ops/frontmatter_entity.go` — `EntitySetOperation.Execute(ctx, vaultPath, entityName, key, value, reason, gateSuccessor)` / `EntityClearOperation.Execute(ctx, vaultPath, entityName, key)` (goal and topic variants).
- `pkg/ops/complete.go` — `MutationResult` (the json-tagged result shape).
- `pkg/ops/errors.go` — `ErrStarterUnavailable`, `ErrSessionBusy`.
- `pkg/ops/claude_session.go`, `pkg/ops/session_lock.go` — the starter and the locker.
- `pkg/domain/task_frontmatter.go`, `pkg/domain/goal_frontmatter.go` — `SetClaudeSessionID` / `ClearClaudeSessionID`.
- `pkg/storage/errors.go:15` — `ErrNotFound` and the per-entity storages.
- `pkg/config/config.go` — `Loader` (`GetVault`, `GetAllVaults`, `GetCurrentUser`), `Vault.GetClaudeScript()`.

Read the spec-022 packages this prompt consumes (`pkg/session`, `pkg/activity`, `pkg/sessionresolver`) — if a symbol you need is missing, STOP and report the prompt as failed rather than inventing it.

Read `pkg/ops.go` (`OpSet`) and `pkg/factory/factory.go` (`CreateOpSet`) — extend `OpSet` with any operation this prompt needs (e.g. the topic lifecycle ops), following the existing wiring style.
</context>

<requirements>

### 1. Extend `pkg/api` with the write-side request and response models

Add the request models, with JSON field names and defaults matching the Python models exactly:

- `UpdateFlagRequest` — `flag` (bool, default `true`).
- `UpdatePhaseRequest` — `phase` (string), `reason` (optional string), `gate_successor` (optional string).
- `UpdateStatusRequest` — `status` (one of `next`, `in_progress`, `backlog`, `completed`, `hold`, `aborted` — **enum-validated**), `reason` (optional), `gate_successor` (optional).
- `UpdateSessionRequest` — `claude_session_id` (string).
- `ExecuteCommandRequest` — `command` (string), `reason` (optional), `gate_successor` (optional).

Add `SessionResponse` — `session_id`, `command`, `working_dir`, `task_title`, `executed_command`, `success`, `error`, `response`, `terminated` — mirroring `src/vault_ui/api/models.py` nullability (the last five are nullable).

### 2. Implement the eighteen routes

Add handlers under `pkg/handler/` (split by resource family, e.g. `api_task_mutations.go`, `api_goal_mutations.go`, `api_maintenance.go`), mirroring the `New*Handler(deps...) http.Handler` shape from prompt 1.

Every task/goal route takes a **required** query parameter `vault`; a request without it must produce the same `422` validation body the Python backend produces.

| Method | Path | Body | Success response |
|---|---|---|---|
| POST | `/api/tasks/{task_id}/run` | — | `SessionResponse` |
| POST | `/api/tasks/{task_id}/jump` | — | `204`, empty body |
| POST | `/api/tasks/{task_id}/take-over` | — | `SessionResponse` (sets `terminated`) |
| POST | `/api/goals/{goal_id}/run` | — | `SessionResponse` |
| POST | `/api/goals/{goal_id}/take-over` | — | `SessionResponse` (sets `terminated`) |
| POST | `/api/tasks/{task_id}/execute-command` | `ExecuteCommandRequest` | `SessionResponse` |
| PATCH | `/api/tasks/{task_id}/assign-to-me` | — | `{"status":"success","task_id":…,"assignee":…}` |
| PATCH | `/api/tasks/{task_id}/phase` | `UpdatePhaseRequest` | `{"status":"success","task_id":…,"phase":…}` |
| PATCH | `/api/tasks/{task_id}/flag` | `UpdateFlagRequest` | `{"status":"success","task_id":…,"flag":…}` |
| PATCH | `/api/goals/{goal_id}/status` | `UpdateStatusRequest` | `{"status":"success","goal_id":…,"new_status":…}` |
| POST | `/api/goals/{goal_id}/execute-command` | `ExecuteCommandRequest` | `{"status":"success","goal_id":…,"command":…}` |
| PATCH | `/api/tasks/{task_id}/status` | `UpdateStatusRequest` | `{"status":"success","task_id":…,"new_status":…}` |
| PATCH | `/api/goals/{goal_id}/assign-to-me` | — | `{"status":"success","goal_id":…,"assignee":…}` |
| DELETE | `/api/tasks/{task_id}/session` | — | `{"status":"success","task_id":…}` |
| DELETE | `/api/goals/{goal_id}/session` | — | `{"status":"success","goal_id":…}` |
| PATCH | `/api/tasks/{task_id}/session` | `UpdateSessionRequest` | `{"status":"success","task_id":…,"claude_session_id":…}` |
| POST | `/api/cache/reload` | — | `{"reloaded":[…],"counts":{…}}` |
| POST | `/api/config/reload` | — | `{"vaults":[…],"watchers":[…]}` |

Notes that are easy to get wrong:

- The success bodies above are **bare objects** with those exact keys — read the Python for the exact value expressions (e.g. `assign-to-me` returns the configured current user, and `PATCH session` returns the stored value, which is not necessarily the requested one).
- `jump` returns `204` with an empty body on success.
- The session routes return `SessionResponse`; `run` omits `success`/`response`, the execute-command fast path sets `response` to the captured stdout and `session_id` to `""`, and take-over sets `terminated`. Read the Python for the exact per-route population.

### 3. Call vault-cli as a library — never a subprocess

Every route resolves the vault through the injected `config.Loader` and performs its work through the wired `OpSet` / vault-cli `pkg/ops` + `pkg/storage` library calls. Do not spawn a `vault-cli` process, and do not construct a command line. The Python backend shells out; the Go backend must not — the observable behavior is what must match, not the mechanism.

Extend `pkg/ops.go`'s `OpSet` and `pkg/factory`'s `CreateOpSet` if you need operations that are not wired yet (e.g. the topic lifecycle ops). Keep `CreateOpSet` free of business logic.

### 4. Reproduce the guards and error contract exactly

Read the Python for each and mirror the status code and the `{"detail": …}` body:

- **Argument-injection guard** — a `task_id`/`goal_id` beginning with `-` is rejected with `400` and the Python message, before any vault operation runs. Applies to every route that writes.
- **Close-out reason gate** — moving a task or goal to `aborted`/`completed` without a `reason` is rejected with `400` and the Python message.
- **Unknown command** — an unrecognized `command` value is rejected with `400` and the Python message.
- **Enum validation** — an out-of-enum `status` is rejected with `422` and the framework validation body shape.
- **Unknown task/goal** — `404` with the Python `{"detail": …}` message.
- **Unknown vault** — `404` with `{"detail": "Unknown vault: <vault>"}`.
- **Session start unavailable** — when the Claude session cannot start, reproduce the Python status and message (see the `ErrStarterUnavailable` path and the Python's handling).
- **Session overwrite conflict** — `PATCH /api/tasks/{task_id}/session` on a task that already holds a different session is rejected with `409` and the Python message; `DELETE` releases it first.
- **Start cap** — reproduce the Python `429` when too many sessions are starting.
- **Take-over ended launch** — reproduce the Python `409`.
- **Timeouts** — where the Python returns `504` for a timed-out vault operation, reproduce the status and message.
- **Jump** — reproduce the Python's `403` (cross-origin), `409` (no session / no pane), `503` (credential unreadable), and `502` (jump server unreachable) branches. Do not add a live jump server to the fixture; the harness exercises the deterministic branches.

Do not invent error fields the Python backend does not emit.

### 5. Inject the board-event publisher

The Python mutating routes broadcast a board event after a successful mutation (task events carry `task_id`, goal events carry `goal_id`). Prompt 3 owns the WebSocket and the connection manager.

Define a small `EventPublisher` interface in `pkg` (e.g. `PublishTaskUpdated(ctx, vault, taskID string)` / `PublishGoalUpdated(ctx, vault, goalID string)`) and inject it into the mutating handlers via the factory, following `go-composition.md`. In this prompt, wire a **no-op** implementation in `pkg/factory`. Prompt 3 replaces the no-op with the connection-manager-backed implementation — do not have prompt 3 edit these handlers.

Publish after exactly the mutations the Python backend broadcasts after (read the Python to confirm which; `run` and `take-over` do not broadcast directly, while `execute-command`'s fast path, `assign-to-me`, `phase`, `flag`, task/goal `status`, goal `execute-command`, and clear-goal-session do).

### 6. Maintenance routes

- `POST /api/cache/reload` — takes an optional `vault` query parameter; reloads the status cache for that vault (or all) and returns `{"reloaded":[…],"counts":{…}}`. Reproduce the Python's per-vault selection and count semantics.
- `POST /api/config/reload` — reloads the vault-cli config and returns `{"vaults":[…],"watchers":[…]}` with the vault and watcher names the Python reports.

If the Python's watcher set is driven by a subprocess watcher that Go replaces with `ops.WatchOperation` (prompt 3), return the names from the same source the Go service will use, and note the seam in a comment so prompt 3 keeps them consistent.

### 7. Extend the parity harness for the write path

Extend `scripts/parity/` (created in prompt 1) so the `mutation-parity` line is real:

1. For each write route, reset the fixture vault to its pristine state.
2. Issue the request to the Python backend; capture the status, the normalized body, and the resulting vault-file bytes.
3. Reset the fixture again.
4. Issue the identical request to the Go backend; capture the same three artifacts.
5. `diff` the two file trees and normalize-compare the two responses.

Print `mutation-parity: <N>/<N>` where N counts the cases whose status, normalized body, **and** file diff all matched, and on failure print `mutation-parity: <M>/<N> mismatched — <case> (status|body|files)` naming the case and which artifact diverged. Exit non-zero when M>0.

Make the fixture deterministic for the session-spawning routes: point the fixture's `claude_script` at a stub on `PATH` that writes a fixed transcript and exits 0, and pin `TZ`, so `run` and `take-over` produce reproducible bodies rather than depending on a real Claude binary. Also add a fixture case where the session cannot start, to exercise that error path in both backends.

Add a third injected-divergence build tag to the harness self-test (prompt 1 built the first two): a throwaway binary that skips one vault-file write, asserting `make parity` exits non-zero on a `mutation-parity` mismatch. Update `make parity-selftest` to run all three.

### 8. Tests

Add Ginkgo/Gomega tests (external `package X_test`) covering, for each route:

- The happy path against a fixture vault, asserting the exact status and body.
- The vault-file side effect — the frontmatter key written or cleared.
- The guards: dash-prefixed id (`400`), missing `reason` on close-out (`400`), unknown command (`400`), out-of-enum status (`422`), unknown task/goal (`404`), missing `vault` (`422`).
- The session conflict (`409`) and the starter-unavailable path.
- That the injected `EventPublisher` is called exactly for the mutations that should publish, and not for the ones that should not.

Use Counterfeiter fakes for the injected interfaces (`EventPublisher`, the session starter/resumer, the loader) — never hand-written mocks.

New code must reach ≥80% statement coverage. Check with `go test -coverprofile=/tmp/cover.out ./pkg/... && go tool cover -func=/tmp/cover.out`.

### 9. Update the changelog

Add an `## Unreleased` entry describing the delivered write surface, following `changelog-guide.md` (prefix + specific, no file paths).

### Self-check before finishing

Re-run the `<verification>` commands and confirm they pass. Walk the spec's Acceptance Criteria 1 (routes 7–24) and 5 against this change.

</requirements>

<constraints>
- **Never code directly** — this repo's `CLAUDE.md` mandates the dark-factory pipeline (spec → prompts → daemon) for all code changes. This prompt is the only path to this code.
- **Frozen path shape** — the 24 REST routes live under `/api/`, the WebSocket is `/ws`, static is `/`. `src/vault_ui/static/app.js` calls these paths and is not modified to accommodate the Go backend.
- **Frozen static tree** — `src/vault_ui/static/` (index.html, app.js, style.css) is out of scope and must not change.
- **Frozen vault-cli pin** — `vault-cli v0.159.0` via `require`, never `replace`. Call its exported Go API in-process; never spawn a subprocess and never pass input through a shell.
- **No new routes on `:8000`** — exactly the 25 in the spec's frozen table. The `:9090` admin block is unchanged.
- **Local-only bind** — `127.0.0.1`, never `0.0.0.0`. No authentication; this is a local tool.
- **Request-body validation happens before any vault operation runs** — malformed JSON and out-of-enum values are rejected first.
- **Do NOT commit** — dark-factory handles git.
- **Do NOT change vault-cli semantics or the pinned version.**
- **Do NOT deploy to a cluster** — single host; no dev/prod cluster exists or is invented.
- **Do NOT add automatic rollback tooling, a `backend: go|python` switch flag, or a runtime fallback to Python.** The cutover is a manual operator step.
- **Do NOT delete the Python source** — it is marked superseded (prompt 4) and its removal is filed as a follow-up.
- **The cutover runbook is a direct doc change** — authored outside the dark-factory pipeline; nothing here authors it, references its commands, or performs the service switch.
- **Restart side effect** — a service restart kills in-flight launches; this prompt does not restart the service.
- **Existing tests must still pass** — the retained Python suite stays green while the backend is superseded.
</constraints>

<verification>
Run inside the container:

```
make precommit
```

Expected: exit 0 — Go format/vet/lint clean, unit tests pass, the retained Python suite still green.

```
make parity-selftest
```

Expected: exit 0 — the harness rejects all three injected divergences, including the skipped-write mutation divergence.

`make parity` is still expected to be non-zero after this prompt: the `/ws` route (route 25) does not exist until prompt 3, so `routes` and `ws-parity` remain incomplete. The `mutation-parity` line, however, must now be real and must be at full ratio for the routes that exist. Do not weaken the harness to make the overall run green early.
</verification>
