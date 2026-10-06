---
status: completed
spec: [026-optimistic-writes-through-a-per-vault-queue]
summary: Dropped only the mutation status comparison from scripts/parity/parity.sh, made the mutation file-tree comparison wait (via a shared trees_match comparator) for the queued Go write, updated the parity header/comment/mutations.txt wording, and wrote docs/optimistic-writes.md plus minimal corrections to docs/page-index.md
execution_id: vault-ui-write-queue-exec-133-spec-025-parity-amendment-and-docs
dark-factory-version: dev
created: "2026-10-06T06:49:18Z"
queued: "2026-10-06T07:16:00Z"
started: "2026-10-06T08:21:58Z"
completed: "2026-10-06T08:28:56Z"
pr-url: https://github.com/bborbe/vault-ui/pull/130
branch: dark-factory/133-spec-025-parity-amendment-and-docs
---

# Amend the parity harness for 202 writes and document optimistic writes

<summary>
- The side-by-side comparison against the old Python backend stops comparing the success status of write requests, because the Go side now answers "accepted" (202) where the superseded Python side answers 200.
- Everything else the comparison checks stays: the route list, every read's status and body, every write's response body, and the vault files each write produces.
- Because a write now lands just after its response, the comparison waits for the write to show up in the vault files before comparing them, instead of reading them too early.
- The self-test that proves the comparison really compares (for example, a build that skips a write must still fail) keeps passing.
- A new document explains the per-vault write line, its ordering rule, when the live-update message is sent, what a failed write looks like and how the board reverts it, and exactly how the parity contract changed.
- The page-index document's read-your-writes wording is brought in line with writes that land just after the response.
</summary>

<objective>
Drop only the mutation **status** comparison from `scripts/parity/parity.sh` (the Python backend is superseded and cannot answer 202), make the mutation file-tree comparison wait for the queued Go write to land, keep every other comparison and the self-test intact, and write `docs/optimistic-writes.md` (sections `Queue and ordering`, `Frame timing`, `Failure and revert`, plus the board overlay and the parity amendment), updating `docs/page-index.md` where its wording describes synchronous writes.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-optimistic-writes-through-a-per-vault-queue.md`. This prompt covers Desired Behavior 7 and Acceptance Criteria AC7 and the doc half of AC8.

What prompts 1–3 of this spec delivered — read the real code before writing docs:
- `pkg/queue/queue.go` — `Queue` (`Enqueue`, `Consume`, `Done`), one unbounded FIFO per configured vault, one consumer goroutine per busy vault, panics recovered and logged at ERROR naming the vault, unapplied writes dropped (and counted in the log) on shutdown, queue depth logged per vault at `V(2)`.
- `pkg/mutations/mutations.go` (`enqueueWrite`, `failureReason`, `taskWritten`, `goalWritten`, `itemWrittenSilently`, `WriteQueue`), `pkg/mutations/tasks.go`, `pkg/mutations/goals.go` — which checks stay on the request path, what runs in the consumer, the success side-effect order, the failure path.
- `pkg/handler/api_mutations.go` — the nine handlers answering `http.StatusAccepted`.
- `pkg/websocket/frames.go` — `WriteFailedFrame(vault, itemKind, itemID, reason)` → `{"type":"write_failed","task_id":…,"item_kind":…,"vault":…,"reason":…}`; `pkg/websocket/publisher.go` — `MutationPublisher`.
- `src/vault_ui/static/app.js` — `pendingWrites`, `applyOptimisticWrite`, `overlayPendingWrites`, `handleWriteFailed`, `OWN_WRITE_ECHO_WINDOW_MS`, the own-frame confirmation and echo suppression in `handleTaskUpdate`, the reconnect clear in `ws.onopen`.

Parity harness (verified):
- `scripts/parity/parity.sh` — header comment lists what the harness compares ("a status difference" among them). The mutation loop (`while IFS=$'\t' read -r name method path body norm; do … done <"$MUTATIONS_FILE"`) resets the fixture with `write_vault`, issues the request to Python, `snapshot_vault "$WORK/py.tree"`, resets, issues the same request to Go, `snapshot_vault "$WORK/go.tree"`, then compares in order: status (`diagnostics+=("mutation mismatch ${name} (status): python=${py_status} go=${go_status}")` then `continue`), normalized body (`mutation mismatch ${name} (body)`), and file tree (`mutation mismatch ${name} (files)`).
- The four read-path status comparisons — `status mismatch for ${method} ${target}` (route table), `status mismatch for case ${name}` (extra read cases), `status mismatch for error case ${name}` (error cases) and `traversal probe status mismatch` (traversal probe) — must stay byte-identical. Locate them with `grep -n 'status mismatch' scripts/parity/parity.sh` before editing and confirm the same four lines are present, unchanged, afterwards.
- `scripts/parity/mutations.txt` — header comment says "The two statuses, the two normalized bodies, and the two resulting vault trees must all match."
- `scripts/parity/selftest.sh` — the baseline expects `mutation-parity: 1/1` against `scripts/parity/mutations-selftest.txt` (`task-flag-set`), and the skipped-write build (`selftest_write_on.go` in `pkg/mutations`, which skips the flag write) must still fail on the file diff.
- `docs/go-cutover.md` § Known limits — `make parity` runs in the `bborbe/claude-yolo` container (GNU tooling), not on the macOS host.

Docs (verified): `docs/page-index.md` § Staleness bounds says "Writes made through vault-ui are visible to the next read. Every vault-writing mutation marks its vault's tasks and goals keys dirty before it returns … The publishing mutations also mark immediately before their `Publish*Updated` frame …"; § Frame ordering says "Theme and objective frames, and frames originating from routes, are unchanged."

Coding plugin guide (in-container path): `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` (for wording consistency only — this prompt does not edit `CHANGELOG.md`).
</context>

<requirements>

### 1. Drop the mutation status comparison — `scripts/parity/parity.sh`

In the mutation loop delete exactly the status block:

```bash
  if [[ "$py_status" != "$go_status" ]]; then
    diagnostics+=("mutation mismatch ${name} (status): python=${py_status} go=${go_status}")
    continue
  fi
```

Keep capturing `py_status` and `go_status` (they are used by requirement 2). Add a comment at that spot: the Python backend is superseded (spec 025) and answers 200 where the Go backend answers 202 for the nine queued writes, so mutation statuses are not compared; the body and file-tree comparisons below still are.

None of the comments you add or edit may contain the literal phrase `status mismatch` (the verification counts that phrase to prove the four read-path comparisons survive). Do not touch the four read-path `status mismatch` comparisons (route table, extra read cases, error cases, traversal probe), the route table loop, the extra-case and error-case loops, the WebSocket section, the static-hash section or the traversal probe.

### 2. Wait for the queued Go write before snapshotting

Replace the Go-side `snapshot_vault "$WORK/go.tree"` in the mutation loop with a bounded wait so the file-tree comparison sees the write that the 202 accepted:

```bash
  snapshot_vault "$WORK/go.tree"
  if [[ "$go_status" == "202" ]]; then
    # The Go backend answers 202 before its per-vault queue writes the file
    # (spec 025). Poll until the tree matches Python's or 5 s pass, then take
    # one more snapshot after a short settle so a late divergent write is
    # still caught by the diff below.
    for _ in $(seq 1 50); do
      if trees_match "$WORK/py.tree" "$WORK/go.tree"; then break; fi
      sleep 0.1
      snapshot_vault "$WORK/go.tree"
    done
    sleep 0.3
    snapshot_vault "$WORK/go.tree"
  fi
```

Define `trees_match() { diff -rq "$1" "$2" >/dev/null; }` once, next to `snapshot_vault`, and change the existing `(files)` check to `if ! trees_match "$WORK/py.tree" "$WORK/go.tree"; then` so the wait and the final comparison use **one** comparator. Any later normalization of the file-tree comparison (a separate task normalizes generated UUIDs and timestamps there) must go inside `trees_match`, never into one call site only — a wait and a final check that disagree would make every such case time out after 5 s and then pass silently. The body comparison is unchanged. A Go build that never performs the write still never matches and fails on `(files)` after the wait.

### 3. Comment updates

- `scripts/parity/parity.sh` header: replace "a status difference" with wording that keeps it true — read-path status differences are still divergences; mutation statuses are not compared (spec 025).
- `scripts/parity/parity.sh` mutation-loop comment (the two lines starting `# Mutation cases: reset, run against Python, reset, run against Go, compare the`): drop "the status," so it reads that the loop compares the normalized body and the resulting vault-file tree, and that statuses are not compared (spec 025).
- `scripts/parity/mutations.txt` header: replace "The two statuses, the two normalized bodies, and the two resulting vault trees must all match." with: the two normalized bodies and the two resulting vault trees must match; statuses are not compared because the Go backend answers 202 for queued writes (spec 025) and the Python backend is superseded.

Do not add, remove or edit any case line in `mutations.txt`, `mutations-selftest.txt`, `routes.txt`, `routes-read.txt`, `cases.txt` or `errors.txt`, and do not edit `selftest.sh` or `ws_probe.py`.

### 4. `docs/optimistic-writes.md`

Create it with these sections, in this order, describing the code as built (read it; do not restate the spec from memory):

- An intro paragraph: the nine frontmatter-writing routes (list them) validate synchronously, enqueue one write and answer **202** with today's body carrying the requested value; the board renders that value at once; the vault file is written just behind by a per-vault worker. The six process-spawning routes (task/goal `run`, `take-over`, `execute-command`), `jump`, `POST /api/config/reload` and `POST /api/cache/reload` stay synchronous and unchanged.
- `## Queue and ordering` — `pkg/queue`'s three operations; one in-memory unbounded FIFO per configured vault; writes to the same vault are applied one at a time in submission order (**per-vault FIFO**); a write to another vault is never held up by this vault's backlog (**cross-vault independence**; no cross-vault or global ordering); a vault with no pending writes runs no work; queue depth is logged per vault; a panic is recovered and logged at ERROR naming the vault and the next write still runs; nothing is persisted — a crash or shutdown loses only unapplied writes (counted in the log), and durable persistence is the separate Bolt-snapshot task; which checks stay on the request path (validation 422s, unknown vault, dash-prefixed id, close-out reason, current user, and the two existing pre-write reads: assign-to-me's task-existence 404 and set-session's overwrite 409) and why (their bodies are part of the parity contract).
- `## Frame timing` — the `task_updated`/`goal_updated` frame is published only **after the file is written**, by the consumer, in today's order: invalidate the status cache, mark the vault's tasks and goals page-index keys dirty, then publish; nothing is published or marked dirty before the write; `DELETE /api/tasks/{id}/session` and `PATCH /api/tasks/{id}/session` publish no frame (as before) but still invalidate and mark after the write; the file watcher also sees the write's own change and publishes its own frame (the echo).
- `## Failure and revert` — a failed (or panicking) write publishes one `write_failed` frame with its fields: `type` (`write_failed`), `task_id` (the **item id** — the key is `task_id` for goals too, like watcher frames), `item_kind` (`task`/`goal`), `vault`, and `reason` (the failure text — an `HTTPError`'s detail or the vault-cli error); it performs none of the success side-effects; the file keeps its previous value (a partial write is picked up by the watcher); there is no retry — the operator acts again.
- `## Board overlay` — the 202 body is applied to the card immediately and held as pending per item; refetches (poll, unrelated frames) overlay pending values instead of reverting them; the item's own frame clears it with exactly one re-read, and watcher frames for that item are treated as the echo and ignored while pending and for `OWN_WRITE_ECHO_WINDOW_MS` after confirmation; a `write_failed` frame clears it, shows an error toast and re-reads so the card returns to its previous value; the task session-clear entry (no frame) clears when a re-read shows the server caught up; a WebSocket reconnect discards all pending state before its catch-up read.
- `## Parity amendment` — the Python backend is superseded and cannot answer 202, so `scripts/parity/parity.sh` dropped **only** the mutation status comparison; the route, read status, body, error, mutation body, mutation file-tree, WebSocket, static and traversal comparisons all remain; the mutation file-tree comparison now waits (up to 5 s plus a 0.3 s settle) for the queued Go write before snapshotting, so a write may land up to 5 s after its 202; equality of the two trees is still required, though a write that diverges and lands more than 0.3 s after the trees first match is not caught; the self-test still proves a skipped write fails.

Keep it factual, ≤ 200 lines, no H1 other than the document title line `# Optimistic writes`.

### 5. `docs/page-index.md`

Minimal edits so it stays true; do not change any staleness bound, frame-ordering rule or snapshot-immutability statement. The index's own bound is unchanged — it still reflects disk; what moved is only *when* disk is written for a queued write:
- § Staleness bounds, the "Writes made through vault-ui …" bullet: the synchronous writes (`Run*`, `TakeOver*`, both `execute-command` routes) still mark before they return; the nine queued writes mark from the queue consumer after the file is written and before their frame, so a client that re-fetches on the frame never sees stale data, and a read in the in-flight window returns the pre-write value (the board's overlay hides it). Link `docs/optimistic-writes.md`.
- § Frame ordering, "frames originating from routes, are unchanged": state that queued-write frames are published by the consumer after the write (see `docs/optimistic-writes.md`) and their content is unchanged.

### 6. README / cutover runbook

Read `README.md` and `docs/go-cutover.md`. If either states that write routes answer 200 or that parity compares mutation statuses, correct that sentence and link `docs/optimistic-writes.md`; otherwise leave them unchanged.

### 7. Self-check

Re-run every `<verification>` command and confirm each passes. Quote the `make parity` summary lines (`routes:`, `body-parity:`, `error-parity:`, `mutation-parity:`, `ws-parity:`, `static-parity:`) in your report.

</requirements>

<constraints>
- The parity harness stops comparing mutation **status** codes only; the route, body and mutation file-tree comparisons stay, and all four read-path `status mismatch` comparisons stay byte-identical.
- The Python backend is superseded and will not be developed further; the harness drops the one comparison it can no longer satisfy rather than the Go side regressing to match it.
- No change to read paths, the page index's staleness bounds or frame-ordering rules, the frontmatter written, the routes, or the query parameters.
- Do not edit any parity case file, `selftest.sh` or `ws_probe.py`.
- Do NOT edit Go code, `src/vault_ui/`, or `CHANGELOG.md` (prompt 2 wrote the only changelog bullet for this spec).
- `docs/optimistic-writes.md` must contain the headings `Queue and ordering`, `Frame timing` and `Failure and revert`.
- Do NOT commit and do NOT run any git command — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first. Run `make precommit` before `make parity` so the container has a Linux `.venv`.

```
make precommit
```
Must exit 0.

```
make parity
```
Must exit 0 with every parity line at full ratio. An `Error 127` in `build_vault_cli` or a missing `.venv` is an environment failure, not a pass — report it as a blocker.

```
make parity-selftest
```
Must exit 0 (the baseline passes; the renamed-route, divergent-body, skipped-write and corrupted-frame builds are all still rejected).

```
test "$(grep -n 'mutation mismatch' scripts/parity/parity.sh | grep -c '(status)')" -eq 0
```
Must exit 0.

```
grep -q 'mutation mismatch ${name} (body)' scripts/parity/parity.sh && grep -q 'mutation mismatch ${name} (files)' scripts/parity/parity.sh
```
Must exit 0.

```
test "$(grep -c 'status mismatch' scripts/parity/parity.sh)" -eq 4
```
Must exit 0 (the four read-path status comparisons remain).

```
grep -n 'Queue and ordering\|Frame timing\|Failure and revert' docs/optimistic-writes.md
```
Must print the three headings.

```
grep -q 'superseded' docs/optimistic-writes.md && grep -q 'write_failed' docs/optimistic-writes.md
```
Must exit 0.
</verification>
