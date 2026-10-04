---
status: completed
spec: [023-go-backend-api-and-cutover]
summary: Added the Go :8000 read-only API surface (six routes), byte-identical embedded static serving with traversal refusal, the FastAPI-shaped error contract, a retargeted make build, and a loud parity harness plus self-test proven against injected divergences
execution_id: vault-ui-exec-118-spec-023-http-router-read-routes-and-parity-harness
dark-factory-version: v0.196.0
created: "2026-10-04T00:00:00Z"
queued: "2026-10-04T13:10:34Z"
started: "2026-10-04T13:11:11Z"
completed: "2026-10-04T13:34:27Z"
branch: dark-factory/118-spec-023-http-router-read-routes-and-parity-harness
---

# Serve the read-only API surface on `:8000`, serve the frozen frontend, and stand up the parity harness

<summary>
- The Go service now answers the six read routes the board uses on every page load — the vault list, the assignee list, and the task, goal, topic, and topic-detail listings — at the exact paths and query-parameter names the frozen frontend already calls.
- Every read response carries the same field names and the same derived values (blocked flags, upcoming flag, recently-completed flag, session state, activity date, Obsidian deep link) that the Python backend returns today, so the board renders identically.
- The unchanged frontend is served from the frozen static tree at `/`, byte-for-byte, and a request that tries to climb out of that directory is refused instead of served.
- A path the service does not route answers exactly as the Python backend answers it, so a typo in a URL fails the same way in both backends.
- `make build` now writes the binary to the path the service is started from, and a new `make parity` boots the Go and Python backends side by side against a disposable fixture vault and compares them.
- The parity harness fails loudly and is proven to actually compare: a self-test builds deliberately broken binaries and asserts the harness rejects them.
- The vault-cli library stays pinned at its required version with no replacement, and is called in-process — no vault-cli subprocess is spawned for any of these routes.
- Everything is covered by Ginkgo/Gomega tests that pin the response shapes and the routing, so the surface cannot drift silently while the later prompts add the write and WebSocket routes.
</summary>

<objective>
Stand up the `:8000` HTTP surface in Go for the six read-only routes plus byte-identical static serving, and build the parity harness that will measure every later route against the Python backend. This prompt establishes the router, the wire models, the error contract, the build target, and the harness — everything prompts 2 and 3 are measured against.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read the spec `specs/in-progress/023-go-backend-api-and-cutover.md` in full. This prompt covers its Acceptance Criteria 1 (partially — routes 1–6 only), 2, 3, 4, 7, 8, 9 and 15; the remaining routes land in prompts 2 and 3.

Read these coding guides in the container — they are the contract for this change:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-service-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — handlers live in `pkg/handler/`, factories in `pkg/factory/`, no inline handlers in `main.go`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md` — JSON error responses and factory integration.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md` — for whatever request/log metrics the service emits. Do NOT add a new metrics endpoint; the `:9090` admin block already owns `/metrics`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package (`package X_test`), `<pkg>_suite_test.go` entry point, `DescribeTable`/`Entry` (never stdlib `t.Run` tables).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.

Read the existing Go tree, which this prompt extends and must stay consistent with:
- `main.go` — starts the `:9090` admin server via `factory.CreateHTTPServer`; `adminListen` is a `const` (spec 021 Non-goals forbid an admin-port knob).
- `pkg/factory/factory.go` — `Create*` factories; note `CreateHTTPServer` builds a `mux.Router` with the admin routes. Follow this construction style for the new router. `CreateConfigLoader("")` builds a vault-cli `config.Loader`; an empty path means vault-cli's default config location (resolved from `$HOME`).
- `pkg/handler/healthz.go`, `pkg/handler/readiness.go` — the handler + `New*Handler` shape to mirror.
- `pkg/ops.go` — `OpSet`, the wired vault-cli operation set (`List`, `Show`, `TopicShow`, …). `pkg/factory/factory.go`'s `CreateOpSet` builds it from a single `*config.Vault`.
- `pkg/discovery.go` — `DiscoverVaults`; the only place vault paths are resolved.

Read the spec-022 packages that this prompt consumes. They are built by the sibling spec `specs/in-progress/022-go-backend-private-logic.md`, which ships ahead of this one in the queue — if a symbol below is missing, STOP and report the prompt as failed rather than inventing it:
- `pkg/session` — `ClassifySessionState`, `SessionState` (values `""`, `live`, `quiet`, `indeterminate`).
- `pkg/activity` — `ComputeActivityDate`, registry/transcript readers.
- `pkg/sessionresolver` — display-name resolution for `jump_pane`.

Read the Python source that is the behavior contract — reproduce it exactly, do not "improve" it:
- `src/vault_ui/api/tasks.py` — the six read handlers: `/vaults` (line 467), `/assignees` (486), `/tasks` (834), `/goals` (1100), `/topics` (1267), `/topics/{topic_id}` (1306). Do NOT read the whole file — `grep -n` for each decorator and read 100–250 line windows around the matches, plus the helpers `_flatten_filter` (567), `_flatten_assignee_filter` (575), the default status set (668), the phase normalization (676/691), and the `TaskResponse` construction.
- `src/vault_ui/api/models.py` — the response models (`TaskResponse`, `GoalResponse`, `TopicResponse`, `TopicDetailResponse`, `AssigneesResponse`). `VaultResponse` lives in `src/vault_ui/api/tasks.py` (line 381), not in `models.py`.
- `src/vault_ui/factory.py` — how routers are mounted and static is served (`create_app`, near the end of the file); `create_app` is the ASGI factory. `app.include_router(tasks_router, prefix="/api")`, `app.include_router(ws_router)` (mounted at `/ws`), then `app.mount("/", StaticFiles(directory=..., html=True))`.
- `src/vault_ui/__main__.py` — `app = create_app()` at module level (line 15) is the ASGI entry point the harness boots.
- `src/vault_ui/config.py` — `resolve_default_config_path` (`$HOME/.config/vault-ui/config.yaml`, XDG-first) and `load_config`; the backend calls `vault-cli config list --output json` (via `vault_cli_path`) to resolve vault paths. The parity harness must reproduce this config environment (requirement 8).

For prompt style, read `prompts/completed/109-spec-021-makefile-and-dod.md`.

Tooling verified present in the container: `go` 1.27.1, `uv`, `python3`, `jq`, `shellcheck`. `vault-cli` is NOT on `PATH` — the parity harness builds it (requirement 8).
</context>

<requirements>

### 1. Retarget `make build` to the frozen binary path

The Makefile's `build:` target today has the recipe `go build -o bin/vault-ui .` (bare `go` — there is no `$(GO)` variable). Change it so `make build` writes the binary to `$(HOME)/Documents/workspaces/go/bin/vault-ui` — the frozen cutover path named in the spec's Constraints. Do NOT add a second target (`make install` is explicitly rejected by the spec); retarget the existing one. Keep `bin/` out of it, and make sure the target creates the directory if absent (`mkdir -p`).

Leave the rest of the Makefile targets untouched.

### 2. Add the `make parity` and `make parity-selftest` targets

Add two targets that call the harness scripts created in requirement 8:

- `parity` — runs the full comparison against both backends; non-zero exit on any mismatch.
- `parity-selftest` — runs the harness's own self-test (requirement 9); non-zero exit if the harness fails to reject an injected divergence.

Do NOT add `parity` to `precommit` — `make precommit` must stay exactly as green as it is today. Full `make parity` is expected to be **red until prompt 3 lands** (routes 7–25 do not exist yet); that is correct and must not be papered over.

### 3. Create `pkg/api` — the wire models

New package `pkg/api` holding the request and response wire types. The JSON field names are the contract and must match the Python models **exactly** — the parity harness normalizes object-key *order* but not key *names*.

Response types (field names taken from `src/vault_ui/api/models.py`; read the Python for types, nullability, and defaults):

- `TaskResponse` — `id`, `title`, `status`, `phase`, `project_path`, `description`, `modified_date`, `completed_date`, `obsidian_url`, `defer_date`, `planned_date`, `due_date`, `priority`, `category`, `recurring`, `claude_session_id`, `claude_session_started`, `assignee`, `blocked_by`, `blocked`, `blockers`, `upcoming`, `recently_completed`, `vault`, `goals`, `flag`, `activity_date`, `session_state`, `jump_pane`.
- `GoalResponse` — `id`, `title`, `status`, `priority`, `obsidian_url`, `defer_date`, `target_date`, `completed_date`, `vault`, `claude_session_id`, `claude_session_started`, `assignee`, `blocked_by`, `blocked`, `blockers`, `upcoming`, `activity_date`, `session_state`.
- `TopicResponse` — `id`, `title`, `status`, `vault`, `obsidian_url`.
- `TopicDetailResponse` — `id`, `title`, `status`, `vault`, `obsidian_url`, `goals`, `tasks`, `unresolved`.
- `VaultResponse` — `name`, `vault_path`, `tasks_folder`, `claude_script` (defined in `src/vault_ui/api/tasks.py` line 381, not `models.py`).
- `AssigneesResponse` — `named`, `has_unassigned`.

Two contracts are load-bearing and easy to get wrong:

- **Nullability.** A field that is `None` in Python must serialize as JSON `null`, not as an omitted key or a Go zero value. Use pointer types (`*string`, `*int`, `*bool`) or `json.RawMessage` where Python emits `null`; verify against a real Python response in the harness rather than guessing.
- **Derived fields.** `blocked`/`blockers` are resolved from the frontmatter `blocked_by` list against the other tasks in the same vault; `upcoming` is derived from `defer_date ∈ (now, now+upcoming_hours]`; `recently_completed` forces `phase="done"`; `session_state` and `jump_pane` come from the spec-022 packages; `obsidian_url` is `obsidian://open?vault=<urlquote(vault_name)>&file=<urlquote(tasks_folder/id.md)>`. Reproduce the Python derivations exactly — the harness is the oracle.

Also add the `Task`/`Goal`/`Topic`/`TopicDetail` internal dataclasses' Go equivalents if the handlers need them; keep them unexported or in `pkg/api` as you prefer, but do not leak wire concerns into `pkg/handler`.

### 4. Create the `:8000` router in `pkg/handler`

Add `pkg/handler/router.go` with a `CreateHTTPRouter(...) http.Handler` (or equivalent) that builds a `mux.Router` serving, in this precedence order:

1. `/api/*` — the REST routes.
2. `/ws` — reserved now; prompt 3 adds the handler. Do not route it yet.
3. `/` — the static tree (requirement 6).

Mirror the `mux.Router` construction in `pkg/factory/factory.go`'s `CreateHTTPServer`. Keep the admin `:9090` router untouched and separate.

Add a `pkg/factory` constructor (e.g. `CreateAPIHandler(...)`) that wires the router from the loader and the `OpSet`, following the `Create*` naming and zero-business-logic rules in `go-factory-pattern.md`.

Add a `CreateAPIServer(listen string, …) run.Func` to `pkg/factory` (mirroring `CreateHTTPServer`) and add it to the `run.CancelOnFirstErrorWait` list in `main.go` (alongside `CreateHTTPServer(adminListen, readiness)`), so the binary serves the `:8000` surface the parity harness boots.

The API listen address must be settable by the harness (which boots the binary on an ephemeral port): `main.go` sources it from the environment variable `VAULT_UI_LISTEN`, defaulting to `:8000`. Do NOT hardcode `:8000` in a way the harness cannot override. The admin `:9090` stays a fixed `const` — only the API port is settable.

### 5. Implement the six read routes

Add handlers under `pkg/handler/` (one file per resource family is fine: `api_vaults.go`, `api_tasks.go`, `api_goals.go`, `api_topics.go`). Each handler is a `New*Handler(deps...) http.Handler` returning a `http.HandlerFunc`, mirroring `pkg/handler/healthz.go`.

| Method | Path | Query params | Returns |
|---|---|---|---|
| GET | `/api/vaults` | — | `[]VaultResponse` |
| GET | `/api/assignees` | `vault[]` (optional) | `AssigneesResponse` |
| GET | `/api/tasks` | `vault[]`, `status[]`, `phase[]`, `assignee[]`, `goal[]` (all optional), `upcoming_hours` (int, default 8, range 0–168), `session_live` (bool, default false) | `[]TaskResponse` |
| GET | `/api/goals` | `vault[]`, `status[]`, `assignee[]` (optional), `upcoming_hours` (int, default 8, range 0–168) | `[]GoalResponse` |
| GET | `/api/topics` | `vault[]` (optional) | `[]TopicResponse` |
| GET | `/api/topics/{topic_id}` | `vault` (**required**) | `TopicDetailResponse` |

Reproduce the Python query-parameter handling exactly:

- `_flatten_filter` — each repeated value is additionally comma-split and empty tokens are dropped. Applies to `vault[]`, `status[]`, `phase[]`, `goal[]`.
- `_flatten_assignee_filter` — comma-split but empty tokens are **kept** (an empty token matches unassigned). Applies to `assignee[]`.
- Default status set when `status[]` is omitted: `todo`, `next`, `in_progress`, `hold`, `completed`.
- Valid phases: `todo`, `planning`, `in_progress`, `execution`, `ai_review`, `human_review`, `done`; an unrecognized phase is treated as `todo`, not rejected.
- List endpoints return a **bare JSON array**, never a wrapper object. An empty result is `[]`, never `null`.

Data access goes through the wired `OpSet` (`List`, `Show`, `TopicShow`) and the vault-cli `pkg/storage` / `pkg/config` library API. Multi-vault selection iterates the configured vaults; a request naming an unknown vault follows the Python status/body (see requirement 7).

### 6. Serve the frozen static tree at `/`

Serve `src/vault_ui/static/` (`index.html`, `app.js`, `style.css`) at `/`, byte-for-byte, with `/` resolving to `index.html`.

- The served bytes must be identical to the Python backend's — do not reformat, minify, or rewrite anything. `sha256sum` of all three files must match the Python-served hashes.
- Query strings must be ignored for path resolution: `index.html` loads `app.js?v=…` and `style.css?v=…`, and those requests must return the same bytes and hashes as the unversioned path.
- **Path traversal must be refused.** Canonicalize the resolved path (e.g. `filepath.Clean` + an `os.Root`/`filepath.Rel` containment check) and return the same non-200 status the Python backend returns for `/../config.yaml` and its encoded variants, with no file contents in the body. Do not rely on `http.Dir` alone; assert the containment explicitly.
- Mount the static handler last so `/api/*` and `/ws` win.

Choose embedded (`//go:embed`) or on-disk serving at implementation time; both satisfy the spec. If you embed, the embed source must be `src/vault_ui/static/` and the tree must remain untouched.

### 7. Match the error contract

There are no custom exception handlers in the Python backend, so errors use framework defaults. Reproduce them:

- Unknown route (e.g. `/api/nope`) — same status and body as Python.
- Unknown vault — `404` with `{"detail": "Unknown vault: <vault>"}`.
- Missing required query param (`vault` on `/api/topics/{id}`) — `422` with the framework validation body shape.
- vault-cli/storage failure — `500` with `{"detail": <message>}`.

Because `bborbe/http` and the guide may offer a JSON error writer, follow `go-json-error-handler-guide.md`; the *shape* `{"detail": ...}` is the contract, not the mechanism. Do not add error fields the Python backend does not emit.

### 8. Build the parity harness

Create `scripts/parity/` (bash + `jq`, both present in the container) with a driver `scripts/parity/parity.sh` invoked by `make parity`. It must:

1. **Create the fixture environment both backends read from.** vault-ui resolves its config at `$HOME/.config/vault-ui/config.yaml` and vault-cli resolves its config at `$HOME/.config/vault-cli/config.yaml`, both via `$HOME`. Create a temp dir per run and launch every harness subprocess with `HOME=<fixture>` so nothing touches the operator's real config. Under the fixture, write:
   - `<fixture>/.config/vault-cli/config.yaml` — the disposable fixture vault (task, goal, topic, and daily-note files with frontmatter shapes matching the live vault, including enum- and date-derived fields; `tasks_dir` must name a folder that exists on disk). Reset the fixture to this pristine state before each backend's run so a mutation from an earlier case cannot leak forward.
   - `<fixture>/.config/vault-ui/config.yaml` — `vault_cli_path` pointing at the binary built in step 2, `host: 127.0.0.1`, and a `port`.
2. **Build a `vault-cli` binary for the Python backend.** The Python backend shells out to a `vault-cli` executable and none is on `PATH` in the container. Build one from the pinned module into the fixture — `go build -o <fixture>/bin/vault-cli github.com/bborbe/vault-cli` (already a `go.mod` dependency at v0.159.0; the root package is `main`) — and point the fixture's `vault_cli_path` at it. The Go backend does NOT use this binary (it consumes vault-cli as a library), but the Python backend needs it for every route.
3. **Boot both backends on two ephemeral localhost ports**, both with `HOME=<fixture>` and a pinned `TZ` (pin the timezone so date-derived fields compare deterministically): the Go binary (with `VAULT_UI_LISTEN` set to its ephemeral port) and the Python ASGI app (`uv run python -m uvicorn vault_ui.__main__:app`).
4. Issue the same request to both backends for every route in `scripts/parity/routes.txt` — the frozen 25-route table from the spec (method, path, query, optional body, and the fixture files it touches). Routes 7–25 will not exist in the Go backend yet; the harness must report them as unmatched, not skip them.
5. Compare: normalize bodies with `jq -S .` and `diff`; compare status codes; for `/`, `/app.js`, `/style.css` compare `sha256sum`; compare the traversal probe's status.
6. Print exactly these summary lines, each with a real computed ratio (never a constant):

```
routes: <M>/<N> matched
body-parity: <N>/<N>
error-parity: <N>/<N>
mutation-parity: <N>/<N>
ws-parity: frames identical (<N> frames)
static-parity: 3/3 byte-identical
```

In this prompt, implement the `routes`, `body-parity`, `error-parity`, `static-parity` lines for the routes that exist. `mutation-parity` and `ws-parity` are implemented in prompts 2 and 3 — print them as `0/0` and `0 frames` respectively here, and make prompt 2/3 replace those stubs.

7. **Fail loudly.** Any mismatch — a route absent, a status difference, a non-empty normalized diff, a static-hash difference, a traversal probe returning 200 — makes the script exit non-zero and print a diagnostic naming the mismatched route (or case). It must never print a fixed success line regardless of outcome.

Add `make parity` calling `scripts/parity/parity.sh`. Keep the script shellcheck-clean — `shellcheck` is available in the container (run it on the script before finishing). The repo does not currently run `shellcheck` in `make precommit`, so this is a self-check, not a gating step.

### 9. Prove the harness compares — the self-test

Add `scripts/parity/selftest.sh`, invoked by `make parity-selftest`. It must:

1. Build a throwaway Go binary with **one route renamed** (e.g. via a Go build tag such as `parity_selftest_rename_route` that the router consults to serve a deliberately wrong path for one route) and run the harness against it, asserting `make parity` exits **non-zero** and that the diagnostic names the renamed route.
2. Build a second throwaway binary with **one response field renamed** (or one status code changed) — a second build tag such as `parity_selftest_body_diverge` — and run the harness against it, asserting exit **non-zero** and that the diagnostic names the divergent case.

The self-test passes only when **both** injected-divergence runs exit non-zero. A harness that still prints `routes: 25/25 matched` against the renamed-route build, or a full `body-parity: N/N` against the body-divergent build, fails the self-test. Keep the build-tag hooks minimal and confined to the router/model layer so they cannot affect a normal build.

### 10. Tests

Add Ginkgo/Gomega tests (external `package X_test`, with `<pkg>_suite_test.go`) covering:

- Each of the six read routes: happy path with a fixture vault, the query-parameter flattening rules (comma-split, empty-token retention for `assignee[]`), the default status set, and unknown-phase normalization.
- The error paths: unknown vault, missing required `vault`, unknown route, and a storage failure — asserting the exact status and `{"detail": ...}` body.
- Static serving: byte-identity, query-string insensitivity, and the traversal refusal.
- The derived fields (`blocked`, `upcoming`, `recently_completed`, `obsidian_url`) against hand-built fixtures.

New code must reach ≥80% statement coverage. Check with `go test -coverprofile=/tmp/cover.out ./pkg/... && go tool cover -func=/tmp/cover.out`.

### 11. Assert the vault-cli pin

Do not change the pin. Confirm `go.mod` has `github.com/bborbe/vault-cli v0.159.0` inside a `require` block and no `replace` directive for it. Add a short test or script assertion if that keeps the check durable.

### Self-check before finishing

Re-run the `<verification>` commands and confirm they pass. Walk each of the spec's Acceptance Criteria 1–4, 7–9 and 15 against this change and note in your completion report which are now satisfied and which are deferred to prompts 2 and 3.

</requirements>

<constraints>
- **Never code directly** — this repo's `CLAUDE.md` mandates the dark-factory pipeline (spec → prompts → daemon) for all code changes. This prompt is the only path to this code.
- **Frozen path shape** — the 24 REST routes live under `/api/`, the WebSocket is `/ws`, static is `/`. `src/vault_ui/static/app.js` calls these paths and is not modified to accommodate the Go backend.
- **Frozen static tree** — `src/vault_ui/static/` (index.html, app.js, style.css) is out of scope and must not change; the Go backend serves it byte-identically (embedded or from disk — decide at impl time).
- **Frozen vault-cli pin** — `vault-cli v0.159.0` via `require`, never `replace`.
- **No new routes on `:8000`** — exactly the 25 in the spec's frozen table, no more. The `:9090` admin block is unchanged.
- **vault-cli is a library, not a subprocess** — call its exported Go API in-process. Do not spawn `vault-cli` and do not pass any value through a shell.
- **Local-only bind** — the service listens on `127.0.0.1`, never `0.0.0.0`. It has no authentication and is a local tool.
- **Do NOT commit** — dark-factory handles git.
- **Do NOT change vault-cli semantics or the pinned version.**
- **Do NOT deploy to a cluster** — this is a single-host service; no dev/prod cluster exists or is invented.
- **Do NOT add automatic rollback tooling, a `backend: go|python` switch flag, or a runtime fallback to Python.** The cutover is a manual operator step; the fallback is documented in the operator runbook.
- **Do NOT delete the Python source** — it is marked superseded (prompt 4) and its removal is filed as a follow-up.
- **The cutover runbook is a direct doc change** — it is authored outside the dark-factory pipeline, so nothing in this prompt or any generated prompt authors it, references its commands, or performs the service switch.
- **Ports** — production `:8000`; the documented dev convention is `:8001` for a worktree. The API server takes a settable listen address (env `VAULT_UI_LISTEN`, default `:8000`) so the harness can use ephemeral ports.
- **Production-touching, operator-gated cutover** — the cutover stops and replaces a running service and must be an explicit operator step, never an unattended prompt action.
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

Expected: exit 0 — the harness rejects both injected divergences (renamed route, divergent body/status).

`make parity` is deliberately **not** part of this prompt's pass criteria: routes 7–25 do not exist until prompts 2 and 3, so full parity is expected to be non-zero here. Do not weaken the harness to make it green early.
</verification>
