---
status: prompted
approved: "2026-10-06T19:52:21Z"
generating: "2026-10-06T20:15:36Z"
prompted: "2026-10-06T20:47:14Z"
branch: dark-factory/precompute-task-list-snapshot
---

## Summary

- A warm `/api/tasks` request currently does all of its work per request: a vault-cli list walk over every indexed page, a per-task activity and session probe, and a `ps -axww` subprocess.
- Measured p50 is ~1.0 s against `/api/vaults` at ~1.2 ms — a ratio near 800×, where the goal's bar is ≤5× (~6 ms).
- This spec makes the board publish a precomputed task-list snapshot, rebuilt when its inputs change, so a warm request performs no per-task work at all.
- The snapshot is derived state: it is rebuilt whole and swapped atomically, never mutated in place, so concurrent readers always see one consistent list.
- Session-derived fields keep a slower refresh cadence of their own, so live/quiet transitions stay visible without a `ps` scan on the request path.

## Problem

Every `/api/tasks` request re-derives the entire list from scratch. Profiling a live board under load (`sample` on the board pid, 20 s, six concurrent request loops) shows the cost is spread across three per-task activities rather than concentrated in one: `activity.TranscriptMtime` is the single largest leaf, and it is a filesystem probe that stats one path per task and, on a miss, falls back to globbing every project directory — a directory scan per task. `session.ClassifySessionState` follows it, and the vault-cli list walk (`ops.(*listOperation).Execute`, `domain.Page.Status`, `FrontmatterMap.GetString`) runs alongside. The `ps -axww` scan the board spawns per request adds a further 0.064–0.166 s and a child process each time. Because every one of these is per request, the cost scales with how often browsers re-fetch, which is why the board's CPU spikes exactly when the watcher is busy.

## Goal

A warm `/api/tasks` request returns an already-built list. The board keeps one published task-list snapshot per key, derived from the page index and a session snapshot; a request reads it and does nothing else. The list a browser renders is byte-identical to today's, session state still tracks reality within a bounded interval, and the board spawns no subprocess to serve a request.

## Non-goals

- Changing the JSON shape, field set, ordering, or filtering semantics of `/api/tasks`.
- Changing vault-cli. The list walk is vault-cli's and stays vault-cli's; this spec removes the board's *call* to it per request, not the operation itself.
- Introducing a sub-second refresher of any kind. The goal forbids tight polling: the only timer added here is a slow safety-net refresh measured in minutes.
- Changing the frontend, the optimistic-write path, or the WebSocket frame contract.
- Making `/api/goals` fast. It shares the session probe, so it inherits the session snapshot and consequently stops spawning a `ps` scan per request — an incidental consequence, not a goal, and it carries no acceptance criterion. Its own list cost is out of scope.

## Acceptance Criteria

- [ ] **Post-Deploy (Rung-2): AC1 — `/api/tasks` is within a small multiple of an in-memory reference.** 10 warm samples each of `/api/tasks` and `/api/vaults` in the same window on the deployed board — evidence: `/api/tasks` p50 ≤ 5× `/api/vaults` p50, both recorded. Sampled relative, not absolute, because the host routinely runs under load; the pre-change reading was ~800× (~1.0 s against ~1.2 ms).
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC2 — serving a request spawns no `ps` scan attributable to it.** Sample `pgrep -P <board-pid> -f 'ps -axww'` at 20 Hz across two equal 10 s windows driving 10 and 2 `GET /api/tasks` requests respectively — evidence: the two non-zero sample counts differ by ≤1, i.e. spawns do not scale with request volume. On the pre-change build this measured **17 vs 10** (delta 7); a build that spawns per request cannot produce a delta ≤1.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC3 — board CPU is no longer request-driven.** With browsers connected and the fleet writing, board CPU stays below 5 % in at least 5 of 6 consecutive 10 s windows — evidence: `top -l 2 -pid <board-pid> -stats cpu` second (instantaneous) sample once per probe cycle, each window's mean counted. Each window must also satisfy its load precondition observably, read from `http://127.0.0.1:9090/metrics`: `vault_ui_websocket_connected_clients` ≥ 1 throughout and `vault_ui_websocket_broadcast_total` up by ≥ 2, so a quiet board cannot satisfy the criterion.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC4 — the task list a browser renders is unchanged.** Diff `~/Library/Logs/vault-ui-parity-before-<sha>.json` (frozen before the first edit) against `…-after-<sha>.json` — evidence: **any field removal, any order change, any task removed without a matching vault write in the window, or any `session_state` value change on a task whose session did not change fails this criterion.** Whitespace and encoding-only differences pass; every remaining difference must be named. The before-baseline must contain ≥ 1 non-null `session_state` — the frozen baseline holds 135, recorded beside it.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC5 — `session_state` still tracks reality.** End one live session, wait one full session-refresh interval, re-fetch `/api/tasks` — evidence: that row's `session_state` changed in the re-fetch. A snapshot built once and never refreshed fails this, which is the guard against trading freshness for speed.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **AC6 — a published snapshot is never mutated in place.** `go test -race ./pkg/...` exits 0, and a test that writes to a published snapshot fails the build — evidence: the race detector is clean and the mutation test fails against a deliberately-mutating build.
- [ ] **AC7 — watcher frame ordering is preserved.** A task/goal watcher frame is still sent only after a rebuild that started after that event has been swapped in — evidence: the existing watcher tests under `pkg/watchrefresh` exit 0 unchanged.
- [ ] **AC8 — the added refresh timer is slow.** `grep -rn 'SessionRefreshInterval\|RescanInterval' pkg/` shows the session-refresh interval configured at ≥ 60 s, and no ticker or `time.AfterFunc` in the snapshot packages has a period below 60 s — evidence: the greps print the configured value and return no sub-60 s period.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — `sync format go-vet test check`; exits 0
- `go test -race ./pkg/...` — race detector clean
- `grep -rn 'SessionRefreshInterval' pkg/` — the interval is declared and configured
- `grep -rnE 'NewTicker|time.AfterFunc' pkg/pageindex pkg/board` — no sub-60 s period appears

### Operator-executable (runs on the host after PR merge, spec verification ladder)

- `cd ~/Documents/workspaces/vault-ui && git pull && make build` then `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui` — the Go binary is rebuilt and the board restarted
- `curl -s -o /dev/null -w '%{time_total}\n' 'http://127.0.0.1:8000/api/tasks?vault=private-personal'` ×10 — record the p50
- `curl -s -o /dev/null -w '%{time_total}\n' 'http://127.0.0.1:8000/api/vaults'` ×10 — the same-window reference; AC1's binding check is the ratio of the two recorded p50s, and an absolute figure is a guideline only, since the host runs under variable load
- `pgrep -P $(pgrep -f 'go/bin/vault-ui' | head -1)` while driving requests — no `ps` child
- `curl -s http://127.0.0.1:9090/metrics | grep vault_ui_websocket_` — load-precondition reads for the CPU criterion

## Desired Behavior

1. The board holds one published task-list snapshot per key (a vault root plus a pages folder). A warm `/api/tasks` request returns it without opening a vault file, without running the list operation, without probing a transcript, and without spawning a process.
2. A rebuild produces a complete new snapshot and swaps it in atomically. A reader concurrent with a rebuild sees either the whole previous list or the whole new one, never a mixture, and a published snapshot is never written to.
3. The snapshot is rebuilt when either of its inputs changes: a page-index rebuild for its key, or a new session snapshot. A page-index rebuild that was already in flight when a watcher event arrived does not satisfy that event — the frame is sent only after a rebuild started after the event is swapped in, preserving today's ordering contract.
4. Session-derived fields (`session_state` and the activity date derived from a transcript mtime) come from a session snapshot refreshed on a fixed interval of at least 60 s, never from the request path. The transcript probe runs at most once per task per refresh, not once per task per request.
5. The first request for a key whose snapshot has not been built waits for that build rather than building a second one; concurrent first requests share a single build, matching the page index's existing cold-read behavior.
6. A failed rebuild keeps serving the previous snapshot and logs the error with its key; the next event, dirty read, or refresh retries. A process restart starts with an empty set and rebuilds on demand.
7. Writes made through vault-ui remain visible to the next read: the synchronous and queued write paths that mark a page-index key dirty also invalidate that key's task-list snapshot before their `Publish*Updated` frame is sent.

## Constraints

- The JSON contract of `/api/tasks` is frozen: same fields, same order, same filtering and sorting semantics. This is enforced by the parity criterion, not by convention.
- `pkg/pageindex`'s published-snapshot invariant is extended, not weakened: a task-list snapshot is a *second* published snapshot derived from the page snapshot, never a field added to `domain.Page` and never mutated after publication.
- The existing `docs/page-index.md` staleness bounds continue to hold for page-derived fields. The session-derived fields carry the session-refresh bound instead, and the doc must say so.
- `docs/liveness-classification.md` and `vault-cli/docs/session-liveness.md` remain authoritative for classification: the `ps` scan stays signal #4 in the fixed order, and its result is cached *with* a timestamp so a cached verdict never outlives its window.
- No new sub-second timer. The only timer added is the session refresh, at ≥ 60 s.
- Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no ignored error returns, `Create*` factories hold no business logic, new code ≥ 80 % covered, CHANGELOG entry under `## Unreleased`.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---|---|---|---|---|---|
| Session refresh fails (registry read or `ps` spawn errors) | Keep serving the previous session snapshot; log the error with the interval and the cause | Next refresh retries — evidence: the error log line is followed within one interval by a successful refresh, or the interval repeats | Log line naming the refresh and the error | Reversible — the previous snapshot is intact | A refresh and a rebuild may overlap; the swap is atomic, so a reader sees either the old or the new list, never a mixture |
| A session ends between refreshes | Its row keeps the stale `session_state` until the next refresh, then flips | None needed — bounded by one refresh interval; AC5 asserts the flip happens | `session_state` unchanged past one interval on a session known to have ended | Reversible | — |
| A rebuild panics or hangs | Readers keep the previous snapshot; the build is bounded by a timeout | The next event or refresh retries — evidence: the snapshot's build timestamp advances after the retry | Error log with the key; the snapshot's build timestamp stops advancing | Reversible | A hung build must not hold the snapshot lock: concurrent readers keep serving the previous snapshot rather than blocking past the timeout |
| A watcher event arrives during a rebuild | The event is not satisfied by the in-flight build; a follow-up rebuild is queued and the frame is sent after it swaps in | None needed — this is the designed path, asserted by AC7 | Frame ordering test in `pkg/watchrefresh` | Reversible | One in-flight rebuild plus one queued follow-up, matching the page index's existing event-collapse rule; a burst of events collapses into that pair |
| Two concurrent first requests for a cold key | Both wait on one shared build | None needed — the shared build is the designed path | Cold-read test asserting exactly one build ran | Reversible | This is the concurrency case: exactly one build starts, and every waiter observes its result |
| A write path forgets to invalidate the snapshot | A read returns pre-write data until the next rebuild | The write paths are enumerated in the constraint and covered by a test; the rescan is the backstop | Stale read after a write frame — AC4's `session_state` and task-removal clauses catch a systematic version | Reversible — bounded by the rescan interval | An invalidation racing a rebuild must not be lost: a rebuild that started before the invalidation does not clear the key's dirty flag, so the next read rebuilds |
| Snapshot memory grows with vault size | Bounded by the number of keys × rows, the same order as the page index's existing footprint | None needed unless measured growth is pathological | Resident memory of the board process | Reversible | — |

## Security / Abuse

- `/api/tasks` is read-only and stays read-only; the snapshot adds no write surface.
- The transcript probe reads `~/.claude/projects/**/<session>.jsonl` paths derived from session ids already present in task frontmatter. A refresh must not follow a session id out of the projects root: the path is joined and cleaned under `projectsRoot`, as it is today, and a glob result outside that root is rejected.
- No credential, token, or file content is added to the response. The snapshot stores exactly the fields the current response already carries.
- The admin port (`:9090`) and the API port (`:8000`) stay separate; nothing in this change adds a route to either.

## Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Session snapshot: refresh loop, timestamped cache, wired through `pkg/factory/api.go` — the only new timer, so it carries the ≥ 60 s bound | 4 | AC2, AC3, AC5, AC8 | — |
| 2 | Task-list snapshot in `pkg/pageindex` (or a sibling package): build from page snapshot + session snapshot, publish atomically, cold-read sharing, failed-rebuild retention | 1, 2, 3, 5, 6 | AC6, AC7 | prompt 1 (consumes the session snapshot) |
| 3 | Board read path: serve `/api/tasks` from the snapshot; drop the per-request list walk and `ps` call | 1 | AC1, AC2, AC3 | prompt 2 |
| 4 | Write-path invalidation + watcher frame ordering | 3, 7 | AC4, AC7 | prompt 2 |
| 5 | Docs: `docs/page-index.md` staleness bounds gain the session-refresh bound; CHANGELOG under `## Unreleased` | — | — | prompts 1–4 |

Rationale: prompt 1 establishes the session snapshot, which is the only new timer and the only input that is not vault-derived. Prompt 2 builds the derived snapshot on top of it and is where the immutability invariant lives. Prompt 3 switches the read path, which is the change that actually moves the latency criterion. Prompt 4 closes the staleness loop. Prompt 5 is documentation and cannot start before the shape is settled.

## Do-Nothing Option

The board keeps spending ~1 s of CPU per `/api/tasks` request and spawning a `ps` process each time, on a service whose stated design rule is that background work is event-driven or computed on demand and that a fixed short-interval refresher is a defect. Every browser re-fetch after a watcher frame repeats the whole cost, so the board's CPU tracks how many tabs are open rather than how much work there is. The two sibling criteria in the parent goal — idle CPU and no per-request subprocess — cannot be met without this change, and spec 025's AC11 latency gate stays blocked behind it.
