# Optimistic writes

Nine frontmatter-writing routes answer before the vault file is touched:

- `PATCH /api/tasks/{id}/phase`
- `PATCH /api/tasks/{id}/status`
- `PATCH /api/tasks/{id}/flag`
- `PATCH /api/tasks/{id}/assign-to-me`
- `PATCH /api/tasks/{id}/session`
- `DELETE /api/tasks/{id}/session`
- `PATCH /api/goals/{id}/status`
- `PATCH /api/goals/{id}/assign-to-me`
- `DELETE /api/goals/{id}/session`

Each validates its request synchronously, enqueues one write describing the
frontmatter change, and answers **202** with the same typed body it returned
before, carrying the requested value. The board renders that value at once; the
vault file is rewritten just behind by the vault's queue consumer. The six
process-spawning routes — task and goal `run`, `take-over` and
`execute-command` — plus `POST /api/tasks/{id}/jump`,
`POST /api/config/reload` and `POST /api/cache/reload` stay synchronous and
unchanged.

## Queue and ordering

`pkg/queue` exposes exactly three operations: `Enqueue` (append one write,
return without waiting), `Consume` (apply queued writes until the context is
cancelled) and `Done` (a channel closed once a vault has no pending and no
in-flight write). Each configured vault gets one in-memory, unbounded FIFO, and
writes to the same vault are applied one at a time in submission order
(**per-vault FIFO**). A write to another vault is never held up by this vault's
backlog (**cross-vault independence**); there is no cross-vault or global
ordering. A vault with no pending writes runs no consumer goroutine at all.
Queue depth is logged per vault at `V(2)`.

A panic inside a write is recovered and logged at `ERROR`, naming the vault, and
the next write still runs. Nothing is persisted: a crash or shutdown loses only
the writes that had not been applied yet, and those are counted in a log line per
vault. Durable persistence is the separate Bolt-snapshot task.

The checks that stay on the request path are the ones whose bodies are part of
the parity contract: request validation (missing vault, malformed body and an
out-of-enum status answer the FastAPI 422 shape), an unknown vault, a
dash-prefixed id, a missing close-out reason, an unset current user, and the two
existing pre-write reads — assign-to-me's task-existence check (404) and
set-session's different-session overwrite check (409). Everything else — the
vault-cli write and the post-write side-effects — moves to the consumer.

## Frame timing

The `task_updated`/`goal_updated` frame is published only **after the file is
written**, by the consumer, in today's order: invalidate the status cache for the
item, mark the vault's tasks and goals page-index keys dirty, then publish.
Nothing is published and nothing is marked dirty before the write. The two task
session routes (`DELETE` and `PATCH /api/tasks/{id}/session`) publish no frame,
as before, but still invalidate and mark after the write; the goal session clear
does publish `goal_updated`. The file watcher also sees the write's own change
and publishes its own frame — the echo.

## Failure and revert

A write that fails (or panics) publishes one `write_failed` frame and performs
none of the success side-effects. Its fields are:

- `type` — `write_failed`.
- `task_id` — the **item id**; the key is `task_id` for goals too, like watcher
  frames.
- `item_kind` — `task` or `goal`.
- `vault` — the vault name.
- `reason` — the failure text: an `HTTPError`'s detail, otherwise the vault-cli
  error.

The file keeps its previous value, so the next read returns it; a partial write
is picked up by the watcher on its own. There is no retry — the operator acts
again.

## Board overlay

The 202 body is applied to the card immediately and held as pending, per item.
A refetch (a poll, or an unrelated frame in the same vault) overlays the pending
values instead of reverting them. The item's own frame clears the entry with
exactly one re-read; watcher frames for that item are treated as the echo and
ignored while it is pending and for `OWN_WRITE_ECHO_WINDOW_MS` after
confirmation. A `write_failed` frame clears the entry, shows an error toast and
re-reads, so the card returns to its previous value. The task session-clear entry
(publishes no frame) clears when a re-read shows the server caught up. A
WebSocket reconnect discards all pending state before its catch-up read.

## Parity amendment

The Python backend is superseded and cannot answer 202, so
`scripts/parity/parity.sh` dropped **only** the mutation status comparison. The
route, read status, body, error, mutation body, mutation file-tree, WebSocket,
static and traversal comparisons all remain. The mutation file-tree comparison
now waits (up to 5 s, plus a 0.3 s settle) for the queued Go write before
snapshotting, so a write may land up to 5 s after its 202; equality of the two
trees is still required, though a write that diverges and lands more than 0.3 s
after the trees first match is not caught. The self-test still proves a skipped
write fails.
