---
status: completed
approved: "2026-10-03T20:58:01Z"
generating: "2026-10-03T21:55:28Z"
prompted: "2026-10-03T23:20:58Z"
verifying: "2026-10-04T19:25:13Z"
completed: "2026-10-05T07:17:45Z"
branch: dark-factory/go-backend-api-and-cutover
---

## Summary

- The Go rewrite of vault-ui reaches its final stage: the HTTP API surface, the WebSocket channel, and the switch from the Python service to the Go one on the operator's machine.
- All 24 REST routes plus the `/ws` WebSocket are reimplemented at the exact same paths the current Python backend serves, because the unchanged frontend (`src/vault_ui/static/app.js`, 119 KB of vanilla JS) calls them by those paths.
- The acceptance bar is **parity**, not feature improvement: for every route the same request against the Go and Python backends returns the same status code and a byte-identical body after JSON object-key reordering only; for the WebSocket, the same frames in the same order for the same session.
- The cutover is a production-touching, explicitly operator-gated step — it stops and replaces a running service. No dark-factory prompt performs it.
- The Python backend is marked superseded (kept in-tree for one rollback window), with its removal filed as a separate follow-up.

## Problem

The Python service on `127.0.0.1:8000` is the board the operator uses daily, and it is the last piece still running the old stack. The vault-cli delegation layer, config, cache, watchers, and session lifecycle have been rebuilt in Go (specs 1 and 2 of this migration); the HTTP API, the WebSocket, and the actual switchover are what remain. Until the Go service answers the exact same requests the frontend already makes — and until the launchd service is repointed from the uv-installed Python tool to the Go binary — the rewrite delivers nothing the operator can see. The risk in this final stage is entirely on the seam: a single divergent path, status code, body shape, or WebSocket frame silently breaks a board whose frontend will not be touched to accommodate it, and a careless cutover can leave the machine with no working board at all.

## Goal

After this work, the Go binary is the service behind `http://127.0.0.1:8000`: it serves the identical HTTP and WebSocket surface as the Python backend, serves the frontend byte-identically from the unchanged `src/vault_ui/static/`, and is what the launchd LaunchAgent runs. Parity is proven mechanically against the Python reference before the cutover, and the cutover itself is a single, reversible, operator-run procedure documented in `docs/`. The Python backend remains in the tree, marked superseded, until its removal is filed and executed as its own follow-up.

## Non-goals

- Do NOT change anything under `src/vault_ui/static/` — the frontend is frozen and must be served byte-identically. No route may be renamed, no response shape adjusted, to make the Go backend easier.
- Do NOT add routes beyond the 25 in the frozen table below — no additional routes on the `:8000` API surface; the `:9090` admin block from the foundation spec (`/healthz`, `/readiness`, `/metrics`, `/setloglevel/{level}`, `/gc`) is unchanged. An extra `:8000` route is a divergence surface; if a future consumer needs one, that is a separate spec.
- Do NOT change vault-cli semantics or the pinned version — this spec consumes vault-cli at v0.159.0 exactly as specs 1 and 2 established.
- Do NOT deploy to a cluster — this is a single-host macOS launchd service; there is no Rung-3 (no dev/prod cluster) and none is invented.
- Do NOT add an automatic rollback tooling, a `backend: go|python` switch flag, or a runtime fallback to Python. The cutover is a manual operator step; the fallback is "repoint the plist back", documented in the runbook.
- Do NOT delete the Python source in this spec — it is marked superseded and its removal is filed as a follow-up so one rollback window exists.
- Do NOT author the cutover runbook through a prompt — `docs/go-cutover.md` is a **direct doc change** (this repo's `CLAUDE.md` flow table routes "Doc / config / yaml — no code" to Direct), so it never enters `prompts/` and no generated prompt carries the service-restart, plist-edit, or tool-reinstall commands the runbook necessarily contains. (Deliberately token-free: this bullet is copied into every generated prompt, so naming a command here would make the negative acceptance criterion match the prompts' own text.)
- Scenario coverage: NO new dark-factory scenario. The parity harness (container-executable) plus the Go test suite reach this behavior directly; the cutover is an operator rung, not an automated E2E.

## Acceptance Criteria

The frozen route table — every route below must exist in the Go service at exactly this method and path (the 24 REST routes are mounted under the `/api` prefix; the WebSocket is `/ws`, **not** `/api/ws`; the frontend is served at `/`):

| # | Method | Path |
|---|---|---|
| 1 | GET | `/api/vaults` |
| 2 | GET | `/api/assignees` |
| 3 | GET | `/api/tasks` |
| 4 | GET | `/api/goals` |
| 5 | GET | `/api/topics` |
| 6 | GET | `/api/topics/{topic_id}` |
| 7 | POST | `/api/tasks/{task_id}/run` |
| 8 | POST | `/api/tasks/{task_id}/jump` |
| 9 | POST | `/api/tasks/{task_id}/take-over` |
| 10 | POST | `/api/goals/{goal_id}/run` |
| 11 | POST | `/api/goals/{goal_id}/take-over` |
| 12 | POST | `/api/tasks/{task_id}/execute-command` |
| 13 | PATCH | `/api/tasks/{task_id}/assign-to-me` |
| 14 | PATCH | `/api/tasks/{task_id}/phase` |
| 15 | PATCH | `/api/tasks/{task_id}/flag` |
| 16 | PATCH | `/api/goals/{goal_id}/status` |
| 17 | POST | `/api/goals/{goal_id}/execute-command` |
| 18 | PATCH | `/api/tasks/{task_id}/status` |
| 19 | PATCH | `/api/goals/{goal_id}/assign-to-me` |
| 20 | DELETE | `/api/tasks/{task_id}/session` |
| 21 | DELETE | `/api/goals/{goal_id}/session` |
| 22 | PATCH | `/api/tasks/{task_id}/session` |
| 23 | POST | `/api/cache/reload` |
| 24 | POST | `/api/config/reload` |
| 25 | WS | `/ws` |

- [ ] All 25 routes in the table above are served by the Go backend at the exact method and path — evidence: the parity harness's route enumeration prints `routes: 25/25 matched` and exits 0; a request to an unrouted path (e.g. `/api/nope`) returns the same status and body as the Python backend.
- [ ] Path shape is preserved exactly: the 24 REST routes are under `/api/`, the WebSocket is at `/ws` (not `/api/ws`), and static assets are served at `/` — evidence: `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:<port>/api/tasks` returns 200 while `.../tasks` (no prefix) returns 404, identical to the Python backend on the same request.
- [ ] Read-route body parity: for every GET route, the Go and Python responses are byte-identical after JSON object-key reordering only (array order preserved, scalars byte-identical) — evidence: the harness prints `body-parity: <N>/<N>` where N is the count of routes whose `diff <(jq -S . python.json) <(jq -S . go.json)` was **empty**, and on a failing run prints `body-parity: <M>/<N> mismatched — <route list>` naming each route whose normalized diff was non-empty; exit 0 requires M=N, so the printed ratio is the count of real comparisons, not a constant.
- [ ] Error-shape parity: a request that the Python backend answers with 400/404/422/500 (missing query param, unknown task id, malformed JSON body, vault-cli failure) is answered by the Go backend with the same status code and the same body after key reordering — evidence: the harness prints `error-parity: <N>/<N>` where N is the count of fixture cases whose status code matched **and** whose normalized body diff was empty, and on a failing run prints `error-parity: <M>/<N> mismatched — <case> (status|body)` naming the case and which of the two diverged; exit 0 requires M=N.
- [ ] Mutating-route parity includes the write side: for each of the write routes (run, jump, take-over, execute-command, phase, status, flag, assign-to-me, session PATCH/DELETE, cache/reload, config/reload), the same request against a freshly reset fixture vault produces the same HTTP status, the same body, **and** the same resulting vault-file bytes — evidence: after Python handles the request and the fixture is reset, Go handles the same request; `diff` of the two resulting task/goal files is empty and the two responses normalize-equal.
- [ ] WebSocket message parity: the same fixture vault change delivered to a client connected to the Go `/ws` and a client connected to the Python `/ws` produces the same frames in the same order for the same session — evidence: the harness prints `ws-parity: frames identical (<N> frames)` only when N is the count of frames whose normalized (`jq -S`) diff was empty **and** the two frame sequences are the same length and order, and on a failing run prints `ws-parity: <K> divergent frame(s), first at index <i> — <frame>` naming the first divergent frame; exit 0 requires zero divergent frames.
- [ ] The frontend is served byte-identically and the static tree is unchanged — evidence: `sha256sum` of the Go-served `/index.html`, `/app.js`, and `/style.css` equals the Python-served hashes (the static handler ignores query strings — `index.html` loads `app.js?v=…` / `style.css?v=…` — so the served bytes and their hashes match regardless of the cache-busting query), **and** `git diff --stat origin/master -- src/vault_ui/static/` prints nothing (negative evidence: empty diff).
- [ ] Static serving rejects path traversal — evidence: `curl --path-as-is -s -o /dev/null -w '%{http_code}' 'http://127.0.0.1:<port>/../config.yaml'` returns the same non-200 status as the Python backend, and the response body contains no file contents.
- [ ] vault-cli is pinned by `require` at v0.159.0 and never `replace`d — evidence: `grep -n 'vault-cli v0.159.0' go.mod` returns ≥1 line inside a `require` block, and `grep -nE 'replace.*vault-cli' go.mod` returns 0 lines.
- [ ] The Python backend is marked superseded and its removal is filed — evidence: `grep -rn 'SUPERSEDED' src/vault_ui/README*` (or an equivalent in-tree marker named in the prompt) returns ≥1 line, and the follow-up that removes it exists as a named file/issue referenced from the spec's completion note.
- [ ] No dark-factory prompt performs the cutover — evidence: `grep -rnE 'launchctl (bootout|bootstrap|kickstart)|plutil -extract ProgramArguments|uv tool install --force' prompts/ --include='spec-<NNN>-*'` returns 0 lines, where `<NNN>` is this spec's number as assigned on approval. Three deliberate choices make this check sound: the scope is the `spec-<NNN>-` prefix the prompt-creator gives every generated prompt (a spec-slug scope would go vacuous — the creator's free-text description need not contain the slug); the pattern matches only *imperative* cutover commands, so prose that merely forbids the cutover does not trip it; and the four command tokens appear **only here**, never in a Constraints or Verification line, because the prompt-creator copies those sections into every generated prompt and a token there would make this grep match the prompts' own text. Negative evidence: the cutover lives only in the operator rung and in `docs/go-cutover.md`, a direct doc change.
- [ ] `make precommit` exits 0 — evidence: exit code (Go format/vet/lint + unit and integration tests green; the retained Python suite still green while the backend is superseded).
- [ ] **Post-Deploy (Rung-2):** after the operator cutover the launchd service runs the Go binary — evidence: `launchctl list | grep vault-ui` reports the label with exit code 0, `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8000/` returns 200, the plist names the Go binary path, **and** the process actually listening on `:8000` is the Go binary — `lsof -p "$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)" -a -d txt | grep -q 'workspaces/go/bin/vault-ui'` exits 0 (the `launchctl` and `curl` checks alone are also satisfied by the still-running Python service, so only this running-process binary assertion distinguishes a real cutover from a repoint-without-restart).
  - `deploy_check:` `plutil -extract ProgramArguments.0 raw ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist`
  - `deploy_target:` `/Users/bborbe/Documents/workspaces/go/bin/vault-ui`
- [ ] **Post-Deploy (Rung-2):** after the cutover the live board is functional against the real vaults — evidence: `curl -s 'http://127.0.0.1:8000/api/tasks?vault=private-personal&status=in_progress' | jq 'length'` returns > 0, `curl -s 'http://127.0.0.1:8000/api/goals?vault=private-personal' | jq 'length'` returns > 0, the board at `http://127.0.0.1:8000` renders cards, and the service's `PATH` still resolves vault-cli before homebrew.
  - `deploy_check:` `plutil -extract ProgramArguments.0 raw ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist`
  - `deploy_target:` `/Users/bborbe/Documents/workspaces/go/bin/vault-ui`
- [ ] The parity harness fails loudly and proves it compares — on any mismatch (a route absent, a status/body/mutation/frame difference, a static-hash difference) `make parity` exits non-zero with a diagnostic naming the mismatched route, never a fixed success line regardless of outcome; a harness self-test injects **at least two** deliberate divergences into throwaway builds — (a) one route renamed and (b) one body/status divergence (a response field renamed or a status code changed) — and asserts `make parity` exits non-zero against each — evidence: the self-test passes only when both injected-divergence runs exit non-zero (a harness that still prints `routes: 25/25 matched` against the renamed-route build, or a full `body-parity: <N>/<N>` against the body/status-divergent build, fails the self-test); the route rename exercises the route-set comparison and the body/status divergence exercises the body- and error-parity comparisons, so neither can be a hardcoded constant.

## Verification

There is no Rung-3. vault-ui is a single-host macOS launchd service; the only deployed environment is the operator's machine on `:8000`. Rung-1 is the YOLO container, Rung-2 is the operator's host.

### Container-executable (runs inside the YOLO container at prompt time)

```
make precommit
```

Expected: exit 0 — Go format/vet/lint clean, unit + integration tests pass, the retained Python suite still green.

```
make parity
```

Expected: exit 0. Boots the Go binary and the Python ASGI app on two ephemeral localhost ports against a disposable fixture vault (a `vault-cli` config pointed at the fixture), then prints:

```
routes: 25/25 matched
body-parity: <N>/<N>
error-parity: <N>/<N>
mutation-parity: <N>/<N>
ws-parity: frames identical (<N> frames)
static-parity: 3/3 byte-identical
```

The harness fails loudly: any mismatch — a route absent, a status/body/mutation/frame difference, a static-hash difference — makes `make parity` exit non-zero with a diagnostic naming the mismatched route; it never prints a fixed success line regardless of outcome. A harness self-test injects **at least two** deliberate divergences into throwaway builds — one route renamed, and one body/status divergence (a response field renamed or a status code changed) — and asserts the parity run against each exits non-zero, proving the harness actually compares rather than emitting constants: the route rename must trip the route-set check, and the body/status divergence must trip the body- and error-parity checks.

Targeted assertions:

- `grep -n 'vault-cli v0.159.0' go.mod` — ≥1 line in a `require` block.
- `grep -nE 'replace.*vault-cli' go.mod` — 0 lines.
- the "no prompt performs the cutover" check is carried by the acceptance criterion of that name and is deliberately **not** repeated here — this section is copied verbatim into every generated prompt, so a token-bearing command here would make that criterion match the prompts' own text.
- `sha256sum` of the fixture-served `index.html` / `app.js` / `style.css` — equal to the Python-served hashes (query strings are ignored by the static handler).
- the harness self-test: `make parity` against a throwaway build with one route renamed, **and** against a second throwaway build with one response field renamed (or one status changed) — exit non-zero in both (the harness compares; it does not print constants).

### Operator-executable (runs on the host after the operator commits on the host, spec verification ladder)

Cutover runbook (documented under `docs/`, e.g. `docs/go-cutover.md`), in order:

1. Check for `⏳ Starting...` cards on the live board and wait for in-flight launches to finish — a restart kills vault-ui's own spawned subprocesses (see `docs/starting-marker-lifecycle.md`'s restart table).
2. Build the Go binary to the frozen path: `make build` (→ `~/Documents/workspaces/go/bin/vault-ui`).
3. Repoint the LaunchAgent: set `ProgramArguments[0]` to the Go binary path, keeping the existing `PATH` (vault-cli dir before `/opt/homebrew/bin`), `KeepAlive`, `RunAtLoad`, and log paths — the plist shape and PATH contract are documented in `docs/launchd-service.md`.
4. `launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist` then `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist`.
5. Verify:

```bash
plutil -extract ProgramArguments.0 raw ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist   # → the Go binary path
launchctl list | grep vault-ui                                                                     # exit code 0
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/                                    # → 200
lsof -p "$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)" -a -d txt | grep -q 'workspaces/go/bin/vault-ui' && echo "running binary: go"  # the listener is the Go binary, not the Python service
curl -s 'http://127.0.0.1:8000/api/tasks?vault=private-personal&status=in_progress' | jq 'length'          # → > 0
ps eww "$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)" | tr ' ' '\n' | grep '^PATH='            # vault-cli dir before homebrew
```

6. Drive the board (Playwright MCP or a click-through) on `:8000`: tasks, goals, and topics views render; a card's actions work; live updates arrive. Do NOT call `browser_snapshot` / `browser_find` on the board — query through `browser_evaluate` with scoped selectors (see the vault `[[vault-ui]]` page's DOM contract).
7. Regression guard: `git diff --stat origin/master -- src/vault_ui/static/` prints nothing.

Rollback (if the Go service misbehaves): repoint `ProgramArguments[0]` back to `~/.local/bin/vault-ui`, `uv tool install --force --no-cache .`, and `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui`.

## Desired Behavior

1. The Go service exposes the identical HTTP surface as the Python backend: the 24 REST routes under `/api/`, the WebSocket at `/ws`, and the frontend at `/` — same methods, same paths, same query-parameter names (`vault[]`, `status[]`, `phase`, `assignee[]`).
2. For every route, the same request returns the same status code and a byte-identical body after JSON object-key reordering only. Error responses (400 / 404 / 422 / 500) keep the Python backend's status and body shape.
3. For every mutating route, the same request against a freshly reset fixture vault also leaves the same vault-file bytes behind — parity covers the write path, not just the response.
4. The WebSocket channel delivers the same frames in the same order for the same session, driven by the same vault-change events the Python backend observes (in Go via the file watcher — no vault-cli subprocess is spawned).
5. The frontend is served byte-identically from `src/vault_ui/static/`, which is unchanged; static serving canonicalizes the request path and never serves a file outside that directory.
6. vault-cli remains the sole vault interface: pinned by `require` at v0.159.0, invoked through vault-cli's exported Go API (a library dependency — no vault-cli subprocess is spawned, and input is never passed through a shell), never `replace`d.
7. The Python backend is marked superseded in-tree (kept for one rollback window); its removal is filed as its own follow-up rather than done here.
8. The cutover is an explicit, operator-gated procedure: the operator repoints the launchd plist to the Go binary and restarts. No dark-factory prompt performs the service restart, the plist edit, or the tool reinstall — those live only in the operator rung and in `docs/go-cutover.md`; after the cutover the live service runs the Go binary on `:8000` with the same PATH contract and the board behaves as before.

## Constraints

- **Never code directly** — this repo's `CLAUDE.md` mandates the dark-factory pipeline (spec → prompts → daemon) for all code changes. This spec's prompts are the only path to the code.
- **Frozen path shape** — the 24 REST routes live under `/api/`, the WebSocket is `/ws`, static is `/`. `src/vault_ui/static/app.js` calls these paths and is not modified to accommodate the Go backend.
- **Frozen static tree** — `src/vault_ui/static/` (index.html, app.js, style.css) is out of scope and must not change; the Go backend serves it byte-identically (embedded or from disk — agent decides at impl time).
- **Frozen vault-cli pin** — `vault-cli v0.159.0` via `require`, never `replace`.
- **Frozen cutover target** — the Go binary installs to `~/Documents/workspaces/go/bin/vault-ui` (already on the plist's PATH and the directory that holds vault-cli), and the plist's `ProgramArguments[0]` is repointed there at cutover. This is what the Rung-2 `deploy_check` reads.
- **Preserved plist contract** — `PATH` must keep `~/Documents/workspaces/go/bin` before `/opt/homebrew/bin`, plus `KeepAlive=true`, `RunAtLoad=true`, and the existing log paths; the vault-cli-before-homebrew ordering is load-bearing (two different vault-cli versions exist on the machine). The launchd/PATH reference the cutover touches is `docs/launchd-service.md`.
- **Cutover runbook is a direct doc change** — `docs/go-cutover.md` is authored outside the dark-factory pipeline (the repo's `CLAUDE.md` flow table routes "Doc / config / yaml — no code" to Direct); no prompt authors it, so `prompts/` never carries the service-restart, plist-edit, or tool-reinstall commands it contains. (Deliberately token-free: this constraint is copied into every generated prompt, so naming a command here would make the negative acceptance criterion match the prompts' own text.)
- **Ports** — production `:8000`; the documented dev convention is `:8001` for a worktree.
- **Production-touching, operator-gated cutover** — the cutover stops and replaces a running service and must be an explicit operator step, never an unattended prompt action.
- **Coding guides** — follow `go-http-service-guide`, `go-http-handler-refactoring-guide`, `go-json-error-handler-guide`, `go-prometheus-metrics-guide` (for whatever request/log metrics the service emits — no new HTTP metrics endpoint), and `go-testing-guide`.
- **Python `--no-cache`** is a Python-path detail only (`uv` keys its build cache on a git-derived version string); it does not apply to the Go install path and must not be carried into the Go build instructions.
- **Build and parity targets** — the prompts create two Makefile targets that do not exist in the repo today (the Makefile currently has sync/format/lint/typecheck/check/test/test-integration/precommit/run/watch): `make parity` (the container-executable harness that boots both backends against a fixture vault and prints the parity summary) and `make build` (builds the Go binary to the frozen path `~/Documents/workspaces/go/bin/vault-ui` — the same target name the foundation spec uses, not a separate `make install`).
- **Restart side effect** — a service restart kills vault-ui's own spawned subprocesses, so in-flight `⏳ Starting...` launches die; the cutover must not run while launches are in flight.

## Assumptions

- **Parity is achievable for all 24 REST routes and `/ws`** at the frozen paths: the Python backend's response bodies are fully reproducible from vault-cli's exported Go API plus the same fixtures, so no route needs a response-shape change to be matched — the frontend is frozen, so a route that cannot be matched is a blocker, not a negotiation.
- **The fixture vault reproduces the live vault's frontmatter shapes** closely enough that enum- and date-derived fields (activity age, `defer_date`, urgency tier) exercise the same code paths in both backends; time-derived fields are pinned/normalized in the fixture rather than compared live.
- **The launchd plist's `PATH` ordering is stable across the cutover restart** — the vault-cli dir stays before `/opt/homebrew/bin`, so the Go binary resolves the same vault-cli the Python service did (see `docs/launchd-service.md`).
- **The retained Python suite keeps passing while the backend is superseded**, so `make precommit` stays green through the one-window rollback period.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---|---|---|---|---|---|
| vault-cli binary missing or not on the service PATH | Route returns the same error status+body as Python; no crash | Fix the plist PATH / install vault-cli | Parity harness error-case fails; live 500 toast | Reversible | — |
| vault-cli output schema drifts from the Python-era shape | Mapping returns 500 with the same error shape; parity fails on the fixture | Update the Go mapping; re-run `make parity` | `make parity` body-parity diff non-empty | Reversible | — |
| Cutover: plist repointed but Go binary missing / not executable | launchd crash-loops; board down | Rollback: repoint plist to Python, `uv tool install --force --no-cache .`, kickstart | `launchctl list` non-zero exit; `/` not 200 | Reversible | KeepAlive restarts repeatedly until fixed |
| Cutover restart while `⏳ Starting...` launches are in flight | Killed subprocesses; cards stuck on Starting | Operator waits for launches first, or clicks the badge afterwards | Board shows stuck Starting cards | Partial (session turn lost) | Two restarts racing leave markers inconsistent |
| Port 8000 already in use after cutover | Service fails to bind; KeepAlive loops | Stop the stray process; restart | `lsof -i :8000`; log bind error | Reversible | — |
| Fixture vault mutated by an earlier parity run | Body/mutation diffs become false positives | Reset the fixture before each run (harness resets per run) | `make parity` fails non-deterministically | Reversible | — |
| Date-dependent fields (activity age, defer_date, urgency tier) differ by timezone/clock | Fields differ between backends with no code bug | Pin TZ and normalize time-derived fields in the fixture | Parity diff on a date field | Reversible | — |
| Static request with `../` traversal | Non-200, no file contents (same as Python) | Fix the static handler's path canonicalization; re-run the traversal probe | Traversal probe returns 200 or file bytes | Reversible | — |
| Many WebSocket clients reconnect at once | Connections capped; no unbounded goroutine growth | Clients reconnect; cap holds | Memory/goroutine growth on the Go process | Reversible | Shared connection manager |

## Security / Abuse Cases

- **Local-only bind.** The service must listen on `127.0.0.1:8000`, never `0.0.0.0`; it has no authentication and is a local tool.
- **Argument injection via path params.** `{task_id}`, `{goal_id}`, `{topic_id}` flow into vault-cli. The Go backend must pass them through vault-cli's exported Go API (a library call — no subprocess, and never through a shell), and must reject ids containing newlines or shell metacharacters with the same status the Python backend returns.
- **Static path traversal.** The static handler must canonicalize the request path and refuse to serve anything outside the static directory; `../` and encoded variants return non-200.
- **Request body validation.** Malformed JSON and out-of-enum values (`status`, `phase`, `command`) must be rejected with the same status/body as the Python backend, before any vault-cli operation runs.
- **WebSocket abuse.** Local-only, but the connection manager must cap concurrent clients and must not echo unbounded frames; a slow or flooding client must not stall the watcher broadcast.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Go HTTP router + read-only routes (`vaults`, `assignees`, `tasks`, `goals`, `topics`, `topics/{id}`) + response/error models + static serving from the frozen tree; the vault-cli library pin (`require v0.159.0`, no `replace`); parity harness scaffold (with its loud-failure self-test) booting both backends | 1, 2, 5 | 1, 2, 3, 4, 7, 8, 9, 15 | — |
| 2 | Mutating routes (`run`, `jump`, `take-over`, `execute-command`, `phase`, `status`, `flag`, `assign-to-me`, session PATCH/DELETE, `cache/reload`, `config/reload`) through vault-cli's exported Go API (library, no subprocess) + write-side mutation parity | 3, 6 | 5 | prompt 1 (router + harness) |
| 3 | WebSocket `/ws` route + watcher wiring + WS message-parity harness | 4 | 6 | prompt 1 (harness) |
| 4 | Python backend superseded marker + removal follow-up (operator-gated; no prompt executes the cutover, and `docs/go-cutover.md` is authored as a direct doc change, not a prompt) | 7, 8 | 10, 12 | prompts 1–3 |
| 5 | (no generated prompt) — the Post-Deploy Rung-2 ACs are operator-run per the cutover runbook; the prompt-creator must emit nothing here | — | — | — |
| 6 | Global / no prompt — cross-cutting negative ACs enforced across every prompt (no prompt runs `launchctl`, edits the plist, or reinstalls the tool) | — | 11 | — |

Rationale: prompt 1 establishes the router, the shared response/error contract, the vault-cli library pin, and the parity harness (with its loud-failure self-test) — everything else is measured against it. Prompts 2 and 3 are independent of each other and both depend on the harness. Prompt 4 lands the superseded marker and the removal follow-up once the surface is complete; the cutover runbook (`docs/go-cutover.md`) is authored as a direct doc change, never a generated prompt. There is no prompt 5: the Post-Deploy Rung-2 ACs are operator-run per the cutover runbook, and the prompt-creator must emit nothing for them. The cross-cutting negative ACs (no prompt runs `launchctl`, edits the plist, or reinstalls the tool) are enforced across every prompt rather than owned by one, so they sit on the global row.

## Do-Nothing Option

The Go rewrite stays invisible: the operator's board keeps running the Python service, the vault-cli delegation layer rebuilt in Go is dead code, and the migration is paid for without being delivered. Leaving it half-migrated is worse than either end state — two implementations of the same surface, only one of them exercised, drifting apart silently until someone tries to finish the job against a frontend that has moved on. The cutover is reversible (the Python path is retained for one window), so the cost of doing it now is a single operator procedure; the cost of not doing it is an unbounded maintenance split.

## Verification Result

**Verified:** 2026-10-05T07:17:00Z (HEAD e971172)
**Binary:** /Users/bborbe/Documents/workspaces/go/bin/vault-ui — the live launchd service (pid 99959, built 2026-10-05 08:41 local, started 08:43)
**Scenario:** no scenario file (spec declares none) — ran `make parity` + `make parity-selftest` in the pinned YOLO container (`bborbe/claude-yolo:v0.15.1`), then probed the deployed Go service on :8000 and rendered the live board with Playwright.
**Evidence:**
- `make parity` exit 0 — `routes: 25/25 matched`, `body-parity: 23/23`, `error-parity: 3/3`, `mutation-parity: 34/34`, `ws-parity: frames identical (3 frames)`, `static-parity: 3/3 byte-identical`
- parity with extra cases (`/api/tasks?vault=personal`, `/tasks`, `/nope`, `/api/nope`) — `body-parity: 10/10` (prefixed and unprefixed path shapes identical to Python)
- `make parity-selftest` exit 0 — "OK — the harness rejects all four injected divergences" (renamed route, divergent body, skipped write, divergent WS frame)
- `make precommit` exit 0 in the container — 756 passed, ruff clean, mypy clean
- live :8000 — `/api/tasks` 200, `/tasks` 404, `/api/ws` 404, `/../config.yaml` 404 (`{"detail":"Not Found"}`); `status=in_progress` tasks 177, goals 262; Playwright `.task-card` count 402
- launchd — `plutil -extract ProgramArguments.0 raw` → `/Users/bborbe/Documents/workspaces/go/bin/vault-ui`; `launchctl list` → `99959 0 com.github.bborbe.vault-ui`; listener pid 99959 txt = the Go binary; PATH has `workspaces/go/bin` before `/opt/homebrew/bin`
- static tree `git diff --stat origin/master -- src/vault_ui/static/` empty; served asset hashes == on-disk; `require github.com/bborbe/vault-cli v0.159.0` and no `replace`; `SUPERSEDED` in `src/vault_ui/README.md` → follow-up `specs/ideas/remove-superseded-python-backend.md`; cutover-command grep over `prompts/spec-023-*` → 0 lines
**Known residual (outside this spec's AC scope):** real-vault `activity_date` — Go truncates sub-µs (`pkg/board/tasks.go` `dateTimeString`), Python rounds, so 14/404 real-vault tasks differ by exactly 1µs. The Rung-2 ACs are functional-only and the Assumptions pin time-derived fields in the fixture; tracked by `25 Tasks/Rewrite the vault-ui Backend in Go…` SC3.
**Verdict:** PASS
