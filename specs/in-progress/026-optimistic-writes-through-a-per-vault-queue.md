---
status: verifying
approved: "2026-10-06T06:18:21Z"
generating: "2026-10-06T07:16:13Z"
prompted: "2026-10-06T07:16:13Z"
verifying: "2026-10-06T08:28:57Z"
branch: dark-factory/optimistic-writes-through-a-per-vault-queue
---

## Summary

- Every frontmatter-writing action on the board — dragging a card to a new phase, changing a status, toggling a flag, assigning, setting or clearing a session — currently blocks the browser until the vault file has been rewritten on disk.
- This change makes those actions answer at once: the board renders the change immediately and the vault write is applied just behind it by a per-vault worker.
- Writes to the same vault are applied one at a time in the order they were made, so two quick changes cannot land out of order; a different vault is never held up by another's backlog.
- A write that fails puts the previous value back on the board and says so, instead of leaving it showing a change that never happened.
- The live-update message for a write is sent only once the vault actually holds the new value, so a browser that re-reads on it never sees the old one.

Linked vault task (traceability): `[[Vault UI Applies Writes Optimistically Through a Per-Vault Queue]]` (Personal vault), goal `[[Vault UI Ultra-Fast Reads and Writes]]`.

## Problem

Reads now answer from memory (spec 024), but writes did not change: every frontmatter-writing route still runs the whole vault write inside the HTTP request. The handler resolves the vault's operation set, calls into vault-cli's library, waits for the file to be rewritten, marks the page index dirty, publishes a WebSocket frame, and only then answers 200. The browser is blocked for that entire round trip and cannot render the change until it completes, so the board's most frequent interaction is also its slowest, and it feels worse next to reads that no longer touch disk. The operator's criterion is architectural rather than a latency number: the board must not wait on the vault write. A second, latent defect comes with the same change — today the write's own file change is picked up a second time by the file watcher, which publishes its own frame for the same item; with an optimistic board that second frame is a refetch that can land before the queued write and visibly undo it.

## Goal

A frontmatter-writing action returns to the browser before the vault is touched, and the board renders the change at once. The vault write is applied by a per-vault worker that processes that vault's writes one at a time in submission order. The write's live-update message is published only after the file has been rewritten — never before — so any client that re-reads on it sees the new value. A write that fails restores the previous value on the board and surfaces the failure. Reads are untouched: a read never goes through the queue, and the page index continues to reflect disk.

## Non-goals

- The six process-spawning routes (`POST /api/tasks/{id}/run`, `POST /api/tasks/{id}/take-over`, `POST /api/tasks/{id}/execute-command`, `POST /api/goals/{id}/run`, `POST /api/goals/{id}/take-over`, `POST /api/goals/{id}/execute-command`) stay synchronous — five return a session id minted during the write, which cannot exist before it, and the goal `execute-command` route returns a command result while spawning a process, so its spawn must be awaited too.
- `POST /api/tasks/{id}/jump`, `POST /api/config/reload` and `POST /api/cache/reload` write no frontmatter and are unchanged.
- No durable queue. The queue is in-memory, so a crash loses only unflushed writes; durable persistence is the separate Bolt-snapshot task.
- No cross-vault ordering guarantee, and no global ordering across vaults.
- No change to read paths, the page index's staleness bounds or frame-ordering rules, the frontmatter written, the routes, or the query parameters.
- No `libqueue` dependency. The seam is an in-tree interface with exactly three operations — enqueue one write, consume in FIFO order, and report the consumer's done state — and no wider surface; it is swapped for `libqueue` when `[[Build libqueue - Queue Interface with Local and Kafka Implementations]]` lands, which is the only consumer that would justify a wider contract.
- No change to vault-cli, and no new external service.
- No retry of a failed write beyond surfacing it; the operator retries by acting again.

## Acceptance Criteria

- [ ] **AC1 — the nine queued routes answer 202 before the vault is written.** A Ginkgo test drives the real router with a fake operation set whose write blocks on a channel — evidence: each of `PATCH /api/tasks/{id}/phase`, `PATCH /api/tasks/{id}/status`, `PATCH /api/tasks/{id}/flag`, `PATCH /api/tasks/{id}/assign-to-me`, `PATCH /api/tasks/{id}/session`, `DELETE /api/tasks/{id}/session`, `PATCH /api/goals/{id}/status`, `PATCH /api/goals/{id}/assign-to-me` and `DELETE /api/goals/{id}/session` returns HTTP 202 while that write is still blocked; the fake's applied-write count is **0** at the moment the response is read; releasing the block yields exactly one applied write per route; and each 202 body carries the requested value in the same typed shape that route returns today. Negative control: the nine routes named in Non-goals return their current status codes and body shapes unchanged, asserted by the existing handler tests passing without edits.
- [ ] **AC2 — per-vault FIFO ordering, and no cross-vault serialization.** A test submits write A then write B to one vault with the fake recording each applied write's identity — evidence: the fake's ordered apply log greps to `A` then `B`, and a write submitted to a second vault is applied while the first vault's queue is still blocked, so the two queues are independent.
- [ ] **AC3 — the frame and the dirty-mark happen only after the file is written.** A test blocks the fake write and counts broadcasts and index marks — evidence: while the write is blocked, `Broadcast` has recorded **0** frames and the fake index has recorded **0** dirty marks for that vault; after the write completes, exactly one `task_updated` frame with the correct `vault` and `task_id` is broadcast, the fake index records the vault's tasks and goals keys marked dirty, and the status cache records the item invalidated.
- [ ] **AC4 — a failed write reverts and says so.** A test makes the fake write return an error — evidence: exactly one error frame naming the item id and the vault is broadcast, **0** `task_updated` frames are broadcast for that item, and the next list read for that vault returns the pre-write value.
- [ ] **AC5 — the board renders a queued write without refetching.** Playwright integration test serving the real static bundle, with `/api/**` and `/ws` answered by the test (a live server cannot hold a write for AC6(a); the Go 202 bodies and frame JSON are pinned by the Go tests in AC1/AC3/AC4) — evidence: after dragging a task card to another phase column, the card's phase element shows the new phase before any `GET /api/tasks` response completes (asserted from the recorded request log: no completed list read between the PATCH and the DOM assertion).
- [ ] **AC6 — an in-flight optimistic value survives a refetch, and the write's own echo applies once.** Playwright integration test with the vault write held — evidence: (a) after moving a card, an unrelated watcher frame in the same vault causes a refetch and the card still shows the new phase; (b) once the write is released, ≥1 own-write frame for that item is observed (the positive control) and a `MutationObserver` on the card's phase element records **exactly one** write of the confirmed value.
- [ ] **AC7 — the parity contract is amended.** `make parity` exits 0 with the mutation **status** comparison removed from `scripts/parity/parity.sh` while the route, body and mutation file-tree comparisons stay — evidence: exit code 0, `grep -n 'mutation mismatch' scripts/parity/parity.sh | grep -c '(status)'` returns **0**, the `mutation mismatch … (body)` comparison is still present, and all four read-path comparisons (`status mismatch` at `scripts/parity/parity.sh:418`, `:436`, `:453`, `:568`) are unchanged. The amendment is recorded in `docs/optimistic-writes.md`, naming that the Python backend is superseded and that only the mutation status comparison was dropped.
- [ ] **AC8 — build health, docs and changelog.** `make precommit` exits 0; `CHANGELOG.md` carries an `## Unreleased` section — it has none today, the top heading being `## v0.81.0` — with a bullet about optimistic writes, asserted by `grep -A10 '^## Unreleased' CHANGELOG.md`; `docs/optimistic-writes.md` exists and its `Queue and ordering` section states the per-vault FIFO rule and cross-vault independence, its `Frame timing` section states that the frame is published only after the file is written, and its `Failure and revert` section names the error frame's fields (item id, vault, reason).
- [ ] **Post-Deploy (Rung-2): AC9 — a queued write is instant on the running board.** Run the Operator-executable latency block below against the deployed service — evidence: the two middle samples of 10 recorded `PATCH /api/tasks/{id}/phase` round trips (the block prints `sed -n '5,6p'`) are both < 100 ms; repeating the block with the target task file made read-only, so every write fails, leaves p50 < 100 ms — the response does not depend on the write completing.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC10 — the operator sees a failed write revert.** The operator runs the revert click-through in the Operator-executable block below on the deployed board — evidence: with the vault file made read-only, moving a card shows the new phase, then the card returns to its previous phase and an error frame naming the item is observed on the `/ws` socket — its `type`, `task_id` and reason are quoted in `# Results`.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario: AC1–AC4 reach the queue, ordering, frame timing and failure paths with fakes through the real router; AC5–AC6 reach the browser behaviour with Playwright; AC7 covers the wire contract; AC9–AC10 are measured on the live service.

## Verification

### Container-executable

- `make precommit` — format, vet, unit tests, lint, typecheck
- `go test -race ./pkg/queue/... ./pkg/mutations/... ./pkg/handler/... ./pkg/factory/...`
- `export PATH=/usr/local/go/bin:$PATH; make parity` (AC7) — runs only in the container: on the macOS host it fails on BSD `date` and the Linux-built `.venv`, and in the container `go` is not on `PATH` (`docs/go-cutover.md` § Known limits)
- `grep -n 'Queue and ordering\|Frame timing\|Failure and revert' docs/optimistic-writes.md` — the three required headings are present
- `grep -A10 '^## Unreleased' CHANGELOG.md` — the section exists (created by this work) and carries the optimistic-writes bullet

### Operator-executable (host, after merge + deploy)

Deploy (the binary must be newer than the pulled checkout — `deploy_check` above):

```bash
cd ~/Documents/workspaces/vault-ui && git pull && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
```

Browser tests (AC5, AC6) — Playwright, needs a host browser (the specs serve the bundle and stub the API themselves, so no running server is needed):

```bash
cd ~/Documents/workspaces/vault-ui && make test-integration
```

Latency (AC9):

```bash
tid=$(curl -s 'http://127.0.0.1:8000/api/tasks?vault=private-personal' | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["id"])')
for i in $(seq 10); do
  curl -s -o /dev/null -w "%{time_total}\n" -X PATCH \
    -H 'Content-Type: application/json' -d '{"phase":"execution"}' \
    "http://127.0.0.1:8000/api/tasks/$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))" "$tid")/phase?vault=private-personal"
done | sort -n | sed -n '5,6p'
```

Revert click-through (AC10):

```bash
f="$HOME/Documents/Obsidian/private-personal/25 Tasks/Optimistic Revert Probe.md"
printf -- '---\nstatus: in_progress\nphase: execution\npriority: 3\n---\nprobe\n' > "$f"
chmod 444 "$f"   # make the write fail
echo "Move the 'Optimistic Revert Probe' card to another phase on the board; it must snap back and surface an error."
# operator confirms, then:
chmod 644 "$f" && rm "$f"
```

## Desired Behavior

1. A per-vault queue holds that vault's pending frontmatter writes in memory, in submission order, behind an in-tree producer/consumer seam in `pkg/queue` (the path is frozen by the linked task) exposing exactly three operations: enqueue one write, consume in FIFO order, report done. One consumer runs per vault; a vault with no pending writes has no running work.
2. Each of the nine frontmatter-writing routes validates its request as today (missing or malformed input still answers the current 422), then enqueues one write describing the target frontmatter change and answers **202** with the same typed body it returns today, carrying the requested value. No vault-cli write, no index mark and no frame happens on the request path. Only two vault reads stay synchronous, so their frozen non-2xx bodies are unchanged: assign-to-me's task-existence check (404) and session-set's different-session conflict check (409).
3. The vault's consumer applies its queued writes one at a time, in submission order. A write to another vault is never blocked by this vault's backlog. The queue is unbounded; a write accepted by the queue is never dropped silently.
4. After a write lands, the consumer performs exactly the post-write side-effects the route performs today, in the same order: invalidate the status cache for the item, mark the vault's tasks and goals keys dirty on the page index, then publish the `task_updated` or `goal_updated` frame. Nothing is published and nothing is marked dirty before the file has been written.
5. A write that fails publishes an error frame carrying the item id, the vault and the failure reason, and performs none of the success side-effects. The vault file keeps its previous value, so the next read returns it.
6. The board applies a queued write optimistically: the response's value is rendered immediately and held as pending for that item. A pending value is not overwritten by a refetch or by an unrelated frame in the same vault, and it is cleared when the item's own frame confirms the write or when an error frame names it. The confirmed value is rendered exactly once — the write's own file change is also seen by the file watcher, and that second frame must not produce a second visible application or a revert.
7. The parity harness stops comparing mutation **status** codes (the Python backend is superseded and cannot answer 202), keeping the route, body and file-tree comparisons. `docs/optimistic-writes.md` records the queue, the ordering rule, the frame timing and the parity amendment.

## Constraints

- The nine routes' **body** shapes and their validation errors are frozen; only the success status changes from 200 to 202. The nine non-queued routes are entirely unchanged, status codes included.
- Reads never go through the queue. The page index's staleness bounds, frame-ordering rule and snapshot immutability (`docs/page-index.md`) are unchanged; the queue only moves the existing post-write side-effects from the request path to the consumer.
- `pkg/mutations` keeps its current dependency shape: the queue is injected as a narrow consumer-side interface and mocked with counterfeiter, like `IndexInvalidator` and `EventPublisher` today.
- The frontend is the embedded static bundle (`src/vault_ui/static/app.js`); no build step is added and no framework is introduced.
- The Playwright specs (AC5, AC6) are written by the prompts but run only on the host — the YOLO container has no browser. `make precommit` must stay green with them present, and their first real run is the operator-executable rung.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, ≥80% coverage on the new package.
- A queue is bounded by memory, not disk: the accepted backlog is whatever the operator's vault can produce between two writes, and an unbounded channel is accepted for that reason.

## Assumptions

- The nine queued routes are consumed by the embedded bundle only; no external client depends on their status code. Every mutation helper gates on `if (!response.ok)` (`src/vault_ui/static/app.js:782` and its siblings), which is true for any 2xx, so 200 → 202 takes the existing success path and a non-2xx keeps today's error handling. Verified against the bundle 2026-10-06.
- The Python backend is superseded and will not be developed further, so the parity harness may drop a comparison it can no longer satisfy rather than the Go side regressing to match it.
- The queue is process-local and single-tenant: one vault-ui process serves the operator's board, so no second writer competes for the same queue.
- The writes are small frontmatter edits; the in-memory backlog accepted between two writes is bounded by human interaction speed.
- The page index keeps reflecting disk — the queue never writes a value into it — so a read during the in-flight window returns the pre-write value, and the board's pending overlay is what hides that.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility |
|---|---|---|---|---|
| Vault write fails (read-only file, TCC denial, vault-cli error) | Error frame naming the item; the file keeps its previous value; the board reverts | Operator acts again; the error frame's `type`, `task_id` and reason are quoted in `# Results` | AC4 — 0 `task_updated` frames for the item, exactly 1 error frame | Reversible — the file was never modified |
| Process crashes with writes queued | Queued writes are lost; the vault keeps its last written state | Board reconnects and re-reads; the card shows the last written value | Reconnect refetch returns the pre-crash value | Reversible — unflushed writes were never applied |
| Burst of writes to one vault (many drags) | Applied one at a time in submission order; the board stays responsive | None needed | AC2 — the recorded apply order matches submission order | Reversible |
| Two clients write the same item at once | Both are queued in arrival order; the later write wins on disk | The board shows the final value once both land | The consumer's applied-write log shows arrival order | Reversible |
| A frame is missed by a client | That client's pending overlay is not cleared by the frame | The client's next refetch (poll or unrelated frame) returns the written value | The card shows the written value after the next read | Reversible |
| Watcher echo of our own write | A second frame for the same item; the value is already rendered | None — the value is already correct | AC6(b) — exactly one DOM write of the confirmed value | Reversible |
| Queue grows without bound (a stuck vault write) | Writes accumulate in memory; the board keeps accepting | Operator restarts the service; unflushed writes are lost | Queue depth logged per vault | Reversible — unflushed writes were never applied |
| Consumer panic on one write | That vault's consumer must not die silently | Consumer recovers and logs; remaining writes continue | ERROR log naming the vault | Reversible |

Schema / version drift, rate limiting and clock skew do not apply: the queue holds no persisted schema, the service is local and single-tenant, and ordering is by arrival rather than by time.

## Security / Abuse

No new input surface: the queued routes take the same bodies and query parameters as today, and validation happens before enqueueing. The queue is keyed by configured vault, never by request input. An unbounded queue is reachable only by an authenticated local client making writes; the board is a local service.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | New `pkg/queue`: producer/consumer seam with a per-vault FIFO and one consumer per vault; unit tests + counterfeiter fake | 1, 3 | AC2 | — |
| 2 | Route the nine frontmatter mutations through the queue: narrow consumer-side interface, enqueue-and-answer-202 with the optimistic body, post-write side-effects moved into the consumer, failure frame; handler and mutation tests; `## Unreleased` CHANGELOG section + bullet | 2, 4, 5 | AC1, AC3, AC4, AC8 (changelog half) | prompt 1 |
| 3 | Board optimistic overlay in `src/vault_ui/static/app.js`: apply the 202 body, hold pending values across refetches, clear on confirm or error; Playwright specs | 6 | AC5, AC6 | prompt 2 |
| 4 | Parity amendment (drop the mutation status comparison) + `docs/optimistic-writes.md` | 7 | AC7, AC8 (doc half) | prompt 2 |

Prompts 1–2 are the backend and verify inside the container. Prompt 3 is the browser half; its Playwright specs are written here but first run on the host. Prompt 4 is the harness and the docs. `CHANGELOG.md` is written by prompt 2 alone so no two prompts edit it. AC8 spans prompts 2 and 4 (its changelog bullet and its doc); AC9–AC10 are operator-executable and carry no prompt work — they are the spec-verification rung.

This spec is deliberately not split despite 7 Desired Behaviors × 10 Acceptance Criteria. The two natural halves — the backend queue and the browser overlay — are coupled: a 202 without the overlay makes the board render the pre-write value on its next refetch, so neither half is shippable alone. The decomposition above is that split.

## Do-Nothing Option

Every drag, flag, assign and session change keeps blocking the browser for a full vault round trip, so the board's most frequent interaction stays its slowest and the read-path work from spec 024 cannot show its benefit — a fast read behind a slow write still feels slow. The goal's write criterion (a UI write reflected in under 100 ms with a visible rollback) stays unmet, and the watcher-echo defect stays latent until something else makes writes optimistic.
