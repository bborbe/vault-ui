---
status: draft
spec: [023-go-backend-api-and-cutover]
created: "2026-10-04T00:00:00Z"
---

# Serve `/ws`, drive it from the vault watcher, and prove WebSocket frame parity

<summary>
- The board's live-update channel is now answered by the Go backend at the same address the frozen frontend connects to, and it speaks the same protocol: a ping is answered with a pong, and nothing else the client sends changes anything.
- A change to a vault file reaches a connected browser as the same frame the Python backend would have sent, with the same field names and in the same order.
- An action taken through the API also reaches every connected browser as a live update, so a card that moves refreshes without a page reload.
- The frames come from an in-process file watcher — no helper process is spawned to watch the vault.
- A slow or flooding client cannot stall the updates for everyone else, and the number of simultaneous connections is bounded.
- The write routes from the previous prompt publish their updates through the same channel, so there is one place that decides who hears about a change.
- The parity harness now compares the live channel: it connects a client to both backends, changes the fixture vault, and compares the frames.
- With this prompt the full `make parity` run finally goes green: all twenty-five routes matched, every comparison at full ratio.
</summary>

<objective>
Implement the `/ws` WebSocket route on the Go `:8000` surface — a bounded, non-blocking connection manager fed by vault-cli's in-process file watcher and by the mutating routes' event publisher — and extend the parity harness to compare the frames both backends emit for the same vault change.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read the spec `specs/in-progress/023-go-backend-api-and-cutover.md` in full. This prompt covers Acceptance Criteria 1 (route 25), 6 (WebSocket message parity), and the failure-mode rows for many clients reconnecting and for watcher-driven updates. It also completes Acceptance Criterion 15 by making `make parity` fully green.

Read prompts 1 and 2 before you start — they shipped the router, the wire models, the error contract, the harness scaffold, the mutating routes, and a no-op `EventPublisher` you will now replace:
- `prompts/1-spec-023-http-router-read-routes-and-parity-harness.md`
- `prompts/2-spec-023-mutating-routes-and-write-parity.md`

Read these coding guides in the container — they are the contract for this change:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — `run.CancelOnFirstErrorWait` over raw `go func()`; caller-owned channels; bounded producers `defer close(ch)`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — non-blocking `select` on `ctx.Done()` in long loops.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md` — compose via injected interfaces.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md` — for the connected-client gauge and broadcast counters.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Read the Python source that is the behavior contract — reproduce it exactly, do not "improve" it:
- `src/vault_ui/api/websocket.py` (~1–60) — the `/ws` handshake, the not-ready close, the ping/pong, and the disconnect handling.
- `src/vault_ui/websocket/connection_manager.py` — `connect`, `disconnect`, `broadcast`, `send_personal`.
- `src/vault_ui/factory.py` (~240–305, ~530–566) — how watcher events are turned into frames and how the connection manager is wired.
- `src/vault_ui/vault_cli_watcher.py` — the `--types task,goal,theme,objective` event stream the Go watcher replaces.

Read the vault-cli v0.159.0 watcher API you will call (module source at `/home/node/go/pkg/mod/github.com/bborbe/vault-cli@v0.159.0`):
- `pkg/ops/watch.go` — `NewWatchOperation() WatchOperation`; `Execute(ctx, vaults []WatchTarget, handler func(WatchEvent) error) error`; `WatchTarget{VaultPath, VaultName, WatchDirs}`; `WatchDir{Dir, Kind}`; `WatchEvent{Event, Name, Vault, Path, Type}` (json tags `event`, `name`, `vault`, `path`, `type`; events `modified`/`created`/`deleted`; types `task`/`goal`/`theme`/`objective`).
- `pkg/cli/cli.go` (~2924) — `buildWatchTargets`, the reference for assembling `[]WatchTarget` from `config.Loader.GetAllVaults` and the `Get*Dir` accessors.

Read `pkg/ops.go` and `pkg/factory/factory.go` for the wiring style, and `pkg/discovery.go` for how vaults are resolved at startup.

A WebSocket library is NOT yet a dependency. `github.com/gorilla/mux` already is, so use `github.com/gorilla/websocket` for consistency; add it with `go get` (the container has Go proxy access). Set the upgrader's `CheckOrigin` to accept every origin — the service is local-only and the Python backend performs no origin check on `/ws`; an origin rejection would be a parity divergence.
</context>

<requirements>

### 1. Build the connection manager

Create the connection manager in its own package (e.g. `pkg/websocket/`), mirroring `src/vault_ui/websocket/connection_manager.py`:

- `Connect` accepts the upgraded connection and registers it.
- `Disconnect` removes it.
- `Broadcast` delivers a frame to every registered client.
- A per-client identity is not needed for parity; keep the shape simple.

Two properties are required by the spec's Security section and must not be dropped even though the Python backend lacks them:

- **Bounded clients** — cap the number of concurrent connections. Choose a cap high enough that it cannot affect parity (the harness uses a handful of clients); when the cap is reached, refuse the new connection rather than growing without bound.
- **Non-blocking broadcast** — a slow or stalled client must not block the watcher loop or the other clients. Give each client its own buffered send channel; if a client's buffer is full, drop that client (and close it) rather than blocking. Never hold a lock while writing to a socket.

Guarded by a mutex or a single owning goroutine — pick one and be consistent; the race detector (`go test -race`, already in the Makefile's `go-test` target) must stay clean.

### 2. Implement the `/ws` route

Add the handler under `pkg/handler/` and register it at `/ws` on the `:8000` router from prompt 1 — **not** `/api/ws`, and mounted so it takes precedence over the static catch-all.

Protocol, matching the Python backend exactly:

- **Not ready** — if the service's readiness gate is not ready, close the connection with code `1011` and reason `Server not ready`. Do not upgrade-and-then-close in a way that changes the observable handshake.
- **Connect** — accept and register. **No initial frame is sent** on connect.
- **Client text `ping`** — reply with the text frame `pong`.
- **Any other client text** — log and ignore; do not echo it, do not close.
- **Client disconnect** — deregister; do not leave a goroutine behind.
- **Client binary frames** — handle without echoing (the Python only inspects text).

### 3. Drive frames from the in-process watcher

Wire `ops.NewWatchOperation()` into the service lifecycle in `pkg/factory` and `main.go`:

- Assemble `[]ops.WatchTarget` from the configured vaults and their `Get*Dir` accessors, following `buildWatchTargets` in vault-cli's `pkg/cli/cli.go` (~2937). Watch the same kinds the Python backend watches: `task`, `goal`, `theme`, `objective`.
- Run the watcher as a `run.Func` alongside the servers via `run.CancelOnFirstErrorWait` in `main.go`, so it starts and stops with the service and its errors surface.
- For each `WatchEvent`, broadcast the frame the Python backend would send. The Python's watcher-originated frame is:

```json
{"type": "<event>", "task_id": "<item name>", "vault": "<vault>", "item_kind": "<task|goal|theme|objective>"}
```

Note the two easy-to-miss details, both confirmed against `src/vault_ui/factory.py`:
- the identifier key is **`task_id`** even for goals, themes, and objectives — the Python reuses the key here;
- `item_kind` carries the entity type, while `type` carries the change event (`modified`/`created`/`deleted`).

Verify both against the Python source before implementing; the parity harness is the final oracle.

No vault-cli subprocess is spawned for watching.

### 4. Wire the mutating routes' publisher

Prompt 2 injected a no-op `EventPublisher` into the mutating handlers. Replace the no-op with the connection-manager-backed implementation in `pkg/factory` — **do not edit the handlers**. The published frames must match the Python's route-originated frames:

- task events — `{"type": "task_updated", "task_id": "<id>", "item_kind": "task", "vault": "<vault>"}`
- goal events — `{"type": "goal_updated", "goal_id": "<id>", "item_kind": "goal", "vault": "<vault>"}`

Note the goal key is **`goal_id`**, not `task_id` — the two families differ. Confirm the exact set of mutations that publish against the Python (prompt 2 lists them).

### 5. Metrics

Emit whatever request/log metrics the guide calls for through an injected interface (do not add a new metrics endpoint); a connected-clients gauge is a reasonable minimum if you emit any. Register them in `init()` per `go-prometheus-metrics-guide.md`; the `:9090` admin block already owns `/metrics`.

### 6. Extend the parity harness for the WebSocket

Extend `scripts/parity/` so the `ws-parity` line is real:

1. Connect a client to the Go `/ws` and a client to the Python `/ws` against the same fixture vault.
2. Drive the same vault change for both — write a fixture task file (a change the watcher picks up), and separately exercise one route-originated broadcast.
3. Collect the frames each backend sends, in order, and normalize each with `jq -S .`.
4. Compare the two sequences for length, order, and normalized content.

Print `ws-parity: frames identical (<N> frames)` only when all N frames matched and the sequences are the same length and order. On failure print `ws-parity: <K> divergent frame(s), first at index <i> — <frame>` naming the first divergent frame, and exit non-zero.

Handle the timing sensitivity honestly: the watcher debounces (~100 ms per path in vault-cli), so allow a bounded settle window before comparing, and make the comparison fail (not hang) if no frame arrives within it. A harness that passes by timing out into an empty comparison must not be possible — assert N > 0.

### 7. Make `make parity` fully green

With all 25 routes present, the full run must now print a complete, real summary and exit 0:

```
routes: 25/25 matched
body-parity: <N>/<N>
error-parity: <N>/<N>
mutation-parity: <N>/<N>
ws-parity: frames identical (<N> frames)
static-parity: 3/3 byte-identical
```

Replace any remaining `0/0` or `0 frames` stubs left by prompts 1 and 2. If any line is still a constant, the harness self-test must catch it — extend `scripts/parity/selftest.sh` with a fourth injected divergence if the WebSocket comparison is not already exercised (e.g. a throwaway binary that emits a wrong frame field), and update `make parity-selftest`.

### 8. Tests

Add Ginkgo/Gomega tests (external `package X_test`) covering:

- The `/ws` handshake: not-ready close (`1011`, `Server not ready`), successful connect with no initial frame, `ping` → `pong`, other text ignored, disconnect deregisters.
- The connection manager: broadcast reaches all clients; a full/slow client is dropped rather than blocking; the client cap holds.
- The watcher → frame mapping: a `WatchEvent` of each kind produces the expected frame, including the `task_id`-for-goals detail and the `task_updated`/`goal_updated` split.
- The publisher wiring: a mutating route's publish reaches a connected client.

Run the suite with `-race` (the Makefile's `go-test` already does). Use Counterfeiter fakes for injected interfaces.

New code must reach ≥80% statement coverage. Check with `go test -race -coverprofile=/tmp/cover.out ./pkg/... && go tool cover -func=/tmp/cover.out`.

### 9. Update the changelog

Add an `## Unreleased` entry describing the live channel, following `changelog-guide.md`.

### Self-check before finishing

Re-run the `<verification>` commands and confirm they pass. Walk the spec's Acceptance Criteria 1 (route 25), 6, and 15 against this change — all three should now be fully satisfied.

</requirements>

<constraints>
- **Never code directly** — this repo's `CLAUDE.md` mandates the dark-factory pipeline (spec → prompts → daemon) for all code changes. This prompt is the only path to this code.
- **Frozen path shape** — the 24 REST routes live under `/api/`, the WebSocket is `/ws`, static is `/`. `src/vault_ui/static/app.js` calls these paths and is not modified to accommodate the Go backend.
- **Frozen static tree** — `src/vault_ui/static/` (index.html, app.js, style.css) is out of scope and must not change.
- **Frozen vault-cli pin** — `vault-cli v0.159.0` via `require`, never `replace`. Call its exported Go API in-process; the watcher is `ops.WatchOperation`, never a spawned helper process.
- **No new routes on `:8000`** — exactly the 25 in the spec's frozen table. The `:9090` admin block is unchanged.
- **Local-only bind** — `127.0.0.1`, never `0.0.0.0`. No authentication; this is a local tool.
- **WebSocket abuse limits** — cap concurrent clients and never echo unbounded frames; a slow or flooding client must not stall the watcher broadcast.
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

Expected: exit 0 — Go format/vet/lint clean, unit tests pass (race detector clean), the retained Python suite still green.

```
make parity
```

Expected: exit 0, printing the full summary with real ratios — `routes: 25/25 matched`, every parity line at full ratio, `ws-parity: frames identical (<N> frames)` with N > 0, `static-parity: 3/3 byte-identical`.

```
make parity-selftest
```

Expected: exit 0 — the harness rejects every injected divergence, including the WebSocket one.
</verification>
