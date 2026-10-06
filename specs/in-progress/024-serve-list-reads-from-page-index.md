---
status: verifying
approved: "2026-10-05T19:31:28Z"
generating: "2026-10-05T20:04:07Z"
prompted: "2026-10-05T20:04:07Z"
verifying: "2026-10-06T07:36:09Z"
branch: dark-factory/serve-list-reads-from-page-index
---

## Summary

- Every board list request (`/api/tasks`, `/api/assignees`, `/api/goals`, `/api/topics`, and the goal/task lists inside `/api/topics/{id}`) re-reads every page file of every selected vault from disk; on the operator's vaults that is 4–9 s per request, and polls pile up.
- Keep each vault's parsed pages in memory instead, built once at startup and kept fresh by the in-process file watcher, with a periodic full rescan as a safety net.
- The cache sits underneath vault-cli's own list operation, so filtering, sorting and blocked-state logic are unchanged and responses stay byte-identical.
- Writes are untouched by this spec; the per-vault write queue is a separate change. Reads never go through a queue.

Linked vault task (traceability): `[[Vault UI Serves Task and Assignee Lists from a Watcher-Maintained Index]]` (Personal vault), goal `[[Vault UI Ultra-Fast Reads and Writes]]`.

## Problem

The Go backend lost the Python backend's in-process list cache during the cutover (spec 023). Every list endpoint calls `ops.List(vault).Execute(...)` per request, and `opsProvider.List` builds a fresh vault-cli `storage.NewPageStorage` each time, whose `ListPages` opens and parses every `.md` file in the folder — about 1.6 s for the personal vault (5694 files) and 1.7 s for the agent vault, summed across all selected vaults. Measured 2026-10-05 against the running service: `/api/tasks` 7.7 s, `/api/assignees` 4.1–8.6 s. The board polls faster than a request completes, so requests overlap and the UI feels frozen. The operator's criterion for the fix is architectural: reads served from memory, no per-request vault re-parse; latency numbers are guidelines.

## Goal

List reads answer from an in-memory page index. A read never opens a vault file unless its snapshot is missing or was invalidated by a write made through vault-ui itself. External edits reach the index through the existing in-process watcher, and each watcher event's WebSocket frame is sent only after a rebuild that started after that event has been swapped in, so a client that re-fetches on the frame sees fresh data. A full rescan at least every 60 s repairs anything a missed watcher event left stale. The rules are documented in `docs/page-index.md`.

## Non-goals

- No change to write semantics or ordering, and no write queue (writes only gain an index dirty-mark) (separate task: per-vault queue behind a libqueue interface).
- No change to per-request session/pane resolution (separate task); `/api/tasks` keeps that cost.
- No persistence of the index to disk (separate task: Bolt snapshot).
- No change to vault-cli; no single-file parse (vault-cli exports no single-page reader — `storage.readPageFromPath` is unexported, and re-implementing the parser here would break byte-identity).
- `ShowTopic`'s own topic-file read (`TopicShow`) stays on disk; only list reads go through the index.
- No new external service dependency, no new HTTP route, no new query parameter, no opt-out flag.
- No change to the set of directories the WebSocket watcher reports or to frame content.

## Acceptance Criteria

- [ ] **AC1 — warm reads touch no disk, through the real factory.** A Ginkgo test obtains the board's ops provider from `pkg/factory` (with the page index's underlying `storage.PageStorage` replaced by a counting fake — two vaults, tasks + goals + topics folders), warms it, then issues 5 requests each to `/api/tasks`, `/api/assignees`, `/api/goals`, `/api/topics`, `/api/topics/{id}` — evidence: the fake records ≥1 `ListPages` call per `(vaultPath, folder)` during warm-up (positive control) and exactly **0** additional calls across the 25 requests. Negative evidence: `grep -n 'NewPageStorage' pkg/factory/api.go` has no hit inside `opsProvider.List`.
- [ ] **AC2 — a watcher event refreshes only its own snapshot.** A test delivers one watcher event for vault A's tasks folder — evidence: the fake records exactly 1 new `ListPages(A.path, A.tasksFolder)` call and 0 new calls for any other `(vaultPath, folder)`; the next read returns the content the fake now serves.
- [ ] **AC3 — refresh never blocks reads.** A test holds the fake's `ListPages` blocked during a watcher-triggered rebuild and issues a read of that key — evidence: the read returns the previous snapshot's result within 100 ms while the rebuild is still blocked.
- [ ] **AC4 — frame after a fresh swap, through `CreateWatcher`.** The watch operation used by `factory.CreateWatcher` becomes injectable; a test drives the callback `CreateWatcher` builds with a fake watch operation — evidence: (a) for a task event, the frame is broadcast strictly after the swap of that event's key; (b) when the event arrives while a rebuild of the same key is already in flight, the frame is held until a rebuild that **started after** the event has swapped, and a read issued when the frame is observed returns the new content; (c) theme/objective event frames are broadcast unchanged without touching the index.
- [ ] **AC5 — read-your-writes after any vault-ui write.** Tests run (i) a publishing task mutation (e.g. phase change) and (ii) a non-publishing write (end-to-end through the factory: `PATCH /api/tasks/{id}/session` / `SetTaskSession`; `RunTask` and take-over additionally asserted at mutation-service level with a fake invalidator, since end-to-end `RunTask` would launch the real `claude` script) and (iii) `POST /api/cache/reload`, each followed by a read — evidence: each read returns the post-write value; for (i) and (ii) the fake records exactly 1 `ListPages` call for the affected vault's tasks folder caused by that read, and two concurrent reads still produce exactly 1 call.
- [ ] **AC6 — rescan heals.** A test with a fake clock advances past the rescan interval without any watcher event — evidence: every indexed `(vaultPath, folder)` gets exactly one new `ListPages` call, changed content becomes visible, and the interval constant is ≤ 60 s.
- [ ] **AC7 — byte-identical responses.** `make parity` exits 0 (Go vs Python over all routes, error shapes, write path and WebSocket frames) — evidence: exit code 0 and its summary reports no mismatch.
- [ ] **AC8 — build health + docs.** `make precommit` exits 0 (includes `go test -race ./...`); `grep -A10 '^## Unreleased' CHANGELOG.md` shows a bullet about serving list reads from the in-memory page index; `docs/page-index.md` exists and contains the headings `Staleness bounds`, `Frame ordering` and `Key derivation`.
- [ ] **Post-Deploy (Rung-2): AC9 — warm latency guideline on the running service.** Run the Operator-executable latency block below — evidence: 10 warm samples each recorded; `/api/assignees` p50 is ≥10× below the 4.1 s pre-change baseline (target < 50 ms, guideline); `/api/tasks?assignee=bborbe` p50 recorded (target < 50 ms, guideline — per-request session/pane work is out of scope and may keep it higher).
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC10 — external edit visible within 2 s.** Run the Operator-executable edit probe below on a scratch task — evidence: the new `priority` appears in `/api/tasks` in ≤ 2 s (guideline; ≤ 60 s is the hard ceiling via rescan), and the scratch task is removed afterwards.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario: AC1–AC6 reach every behavior with fakes, AC7 covers the wire contract, and AC9–AC10 are measured on the live service.

## Verification

### Container-executable

- `make precommit`
- `make parity`
- `go test -race ./pkg/pageindex/... ./pkg/board/... ./pkg/factory/... ./pkg/mutations/...`
- `grep -n 'NewPageStorage' pkg/factory/api.go` — no hit inside `opsProvider.List`

### Operator-executable (host, after merge + deploy)

Deploy (the binary must be newer than the pulled checkout — `deploy_check` above):

```bash
cd ~/Documents/workspaces/vault-ui && git pull && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
```

Latency (AC9):

```bash
for url in "/api/assignees" "/api/tasks?assignee=bborbe"; do
  for i in 1 2 3; do curl -s -o /dev/null "http://127.0.0.1:8000$url"; done
  for i in $(seq 10); do curl -s -o /dev/null -w "%{time_total}\n" "http://127.0.0.1:8000$url"; done | sort -n | sed -n '5,6p'
done
```

Edit probe (AC10):

```bash
f="$HOME/Documents/Obsidian/private-personal/25 Tasks/Page Index Probe.md"
printf -- '---\nstatus: in_progress\nphase: execution\npriority: 3\n---\nprobe\n' > "$f"; sleep 70   # rescan ceiling: probe exists in the index
now() { python3 -c 'import time; print(time.time())'; }
sed -i '' 's/^priority: 3$/priority: 1/' "$f"; start=$(now); n=0
until [ $((n+=1)) -gt 300 ] || curl -s 'http://127.0.0.1:8000/api/tasks?vault=private-personal' | python3 -c 'import json,sys; sys.exit(0 if any(t.get("id")=="Page Index Probe" and t.get("priority")==1 for t in json.load(sys.stdin)) else 1)'; do sleep 0.2; done
[ $n -gt 300 ] && echo "FAIL: not visible within 60 s" || echo "visible after $(python3 -c "print(round($(now) - $start, 2))") s"; rm "$f"
```

## Desired Behavior

1. A page index implements `storage.PageStorage`. It holds one immutable snapshot (`[]*domain.Page`) per `(vaultPath, pagesDir)` key. `ListPages` on a key with a clean snapshot returns that snapshot's pages without any filesystem access.
2. `factory`'s ops provider builds `ops.NewListOperation(<index>)` for every vault, using one process-wide index, so tasks, goals, assignees, topics and the lists inside topic detail all read through it.
3. At startup the index builds every configured vault's tasks, goals and topics folders concurrently. A read on a key whose first build has not finished waits for that build (one shared `ListPages` per key) rather than starting its own.
4. The callback built by `factory.CreateWatcher` hands each task/goal event to the index, which schedules a rebuild of that event's `(vaultPath, folder)` key off the request path (at most one rebuild in flight per key, plus at most one queued follow-up that covers every event received meanwhile). Each event's frame is broadcast only after a rebuild that started after the event has been swapped in. There is no cross-key ordering guarantee beyond today's (vault-cli already delivers events from per-file goroutines). Theme/objective events are broadcast as today.
5. Every successful vault-ui write — publishing mutations, the non-publishing `Run*`/`TakeOver*` writes, and `POST /api/cache/reload` — marks the affected vault's tasks and goals keys dirty before it returns (and before any `Publish*Updated` frame). `/api/cache/reload` marks every key dirty. The first read of a dirty key performs one shared rebuild and every concurrent reader waits on that same rebuild.
6. A background loop rebuilds every key at most every 60 s, swapping each snapshot atomically; it does not broadcast frames. Topics folders, which the watcher does not watch, rely on this loop and on dirty marks.
7. A rebuild that fails keeps serving the previous snapshot and logs the error with the key; the next event, read of a dirty key, or rescan retries.

## Constraints

- Response bodies, status codes, routes, query parameters, and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set is unchanged.
- vault-cli `ops.ListOperation` and its filter/sort/blocked logic stay the only list logic; vault-ui must not parse page files itself. vault-cli version unchanged.
- Snapshots are never mutated after publication; concurrent readers share `*domain.Page` pointers read-only (vault-cli's `filterTasks` copies before sorting). `go test -race` must stay clean.
- The index key must be derived the same way on the read side (`board.Vault.Path` + folder) and the event side (watcher vault name → configured vault + `GetTasksDir()`/`GetGoalsDir()`) — a mismatch silently disables refresh; AC2 and AC4 guard it.
- `pkg/mutations` reads and `pkg/cleanup` keep reading disk directly.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` for the rescan clock, ≥80% coverage on the new package.
- Memory: the index holds every page (frontmatter + body) of the configured folders; for the current vaults this is tens of MB, accepted.
- `docs/page-index.md` documents staleness bounds, frame ordering and key derivation.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---|---|---|---|
| Watcher misses an event (fsnotify overflow, editor atomic rename) | Snapshot stale until next event or rescan | Rescan within ≤ 60 s, or `POST /api/cache/reload` | AC6; operator sees the value appear late |
| File read mid-write: `ListPages` skips the unreadable page without error | Page missing from that snapshot | The write's own watcher event triggers another rebuild; else rescan ≤ 60 s | Page flickers out briefly |
| Rebuild errors (folder unreadable, macOS TCC denial) | Previous snapshot keeps serving; error logged with vault + folder | Next event / rescan retries; TCC fix per runbook "Vault UI - Task List 500 (TCC)" | ERROR log with key |
| First build of a key fails at startup | Reads of that key return the error as today (500), other keys serve normally | Retry on next read / rescan | Existing error path |
| Event vault name not in config (config changed while running) | Event ignored by the index, frame still broadcast | Restart picks up new config | V(2) log |
| Burst of events (git pull touches 500 files) | One rebuild in flight + one queued follow-up per key; all held frames sent after the follow-up swaps | None needed | — |
| Rebuild churn: agent fleet writes to the personal vault continuously | At most one rebuild in flight per key, so CPU is bounded at roughly one core per busy folder; reads unaffected | Accepted; single-file reparse needs a vault-cli export (out of scope) | CPU in Activity Monitor |
| Process restart | Index empty; startup build repopulates (cold reads wait on it) | None — by design; Bolt snapshot is a separate task | — |

## Security / Abuse

No new input surface. Index keys come from server config, never from request input. Memory is bounded by the configured folders' size.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | New `pkg/pageindex`: snapshot store implementing `storage.PageStorage`, per-key shared build, dirty marks, event-triggered rebuild (one in flight + one follow-up) with a post-swap completion signal, rescan loop with injected clock, error retention; unit tests + counterfeiter fake; `docs/page-index.md` | 1, 3, 6, 7 | AC2, AC3, AC6 | — |
| 2 | Read wiring: factory ops provider uses one process-wide index; startup concurrent build as a run func; factory-level test | 2, 3 | AC1, AC7 | prompt 1 |
| 3 | Watcher wiring: injectable watch operation in `CreateWatcher`; task/goal events go through the index and their frames wait for the fresh swap | 4 | AC4, AC7 | prompt 2 |
| 4 | Write invalidation: every vault-ui write and `/api/cache/reload` marks keys dirty; tests; CHANGELOG | 5 | AC5, AC7, AC8 | prompt 2 |

## Do-Nothing Option

Every board poll keeps costing 4–9 s of disk parsing, requests overlap, and the board stays unusable as a live view. The goal's architecture criterion (reads from memory) stays unmet, and the sibling tasks (background pane resolution, optimistic writes) cannot show their benefit while every read still re-parses the vaults.
