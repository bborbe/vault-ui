---
status: completed
spec: [025-optimistic-writes-through-a-per-vault-queue]
summary: Added pkg/queue, an in-memory per-vault FIFO write queue with one consumer per vault, exposing Enqueue/Consume/Done behind a counterfeiter-mocked interface, at 98.2% coverage and race-clean, wired into nothing yet.
execution_id: vault-ui-write-queue-exec-130-spec-025-per-vault-write-queue
dark-factory-version: dev
created: "2026-10-06T06:49:18Z"
queued: "2026-10-06T07:16:00Z"
started: "2026-10-06T07:22:06Z"
completed: "2026-10-06T07:29:09Z"
branch: dark-factory/130-spec-025-per-vault-write-queue
---

# Add `pkg/queue`: an in-memory per-vault FIFO write queue with one consumer per vault

<summary>
- vault-ui gains a place to park a vault write so the request that asked for it does not have to wait for it.
- Each vault has its own line of pending writes; writes to one vault are applied one at a time, in the order they arrived.
- A slow or stuck write in one vault never holds up another vault's writes.
- A vault with nothing pending runs no background work at all.
- A write that fails or panics is logged with its vault, and the writes behind it still run.
- On shutdown, writes not yet applied are dropped and counted in the log; the queue is memory-only by design.
- The seam has exactly three operations (add a write, run the consumers, report "this vault is idle"), so it can later be swapped for the shared queue library.
- Nothing uses the queue yet; the next prompt routes the board's writes through it.
</summary>

<objective>
Create a new package `pkg/queue` holding pending vault writes in memory, one unbounded FIFO per vault, with one consumer goroutine per vault that has pending writes. The package exposes exactly three operations — `Enqueue`, `Consume`, `Done` — behind an interface with a counterfeiter fake, and is fully unit-tested (≥80% coverage, race-clean). It is not wired into the service in this prompt.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-optimistic-writes-through-a-per-vault-queue.md`. This prompt covers Desired Behaviors 1 and 3 and Acceptance Criterion AC2. Later prompts of this spec route the nine frontmatter-writing routes through this queue (prompt 2), add the board's optimistic overlay (prompt 3), and amend the parity harness and docs (prompt 4).

Read these files for the house style before writing code:
- `pkg/pageindex/pageindex.go` — package doc comment style, `github.com/bborbe/errors` usage, `github.com/bborbe/run` import, the `//counterfeiter:generate` directive form placed directly above the interface.
- `pkg/pageindex/pageindex_suite_test.go` — the Ginkgo suite file shape to copy (`time.Local = time.UTC`, `format.TruncatedDiff = false`, `suiteConfig.Timeout = 60 * time.Second`).
- `pkg/pageindex/mocks/` — a generated counterfeiter mock with the repo copyright header prepended.
- `pkg/watchrefresh/handler.go` (`glog.V(2).Infof`, `glog.V(3).Infof`) and `pkg/watcher/watcher.go` (`glog.Errorf`, `glog.Warningf`) — `github.com/golang/glog` logging levels used in this repo.

Verified library APIs (from the module cache, `github.com/bborbe/run@v1.11.0` and `github.com/bborbe/errors@v1.6.1`):
- `run.Func` is `type Func func(context.Context) error`.
- `errors.New(ctx context.Context, message string) error`, `errors.Errorf(ctx, format, args...) error`, `errors.Wrapf(ctx, err, format, args...) error`, `errors.Is(err, target error) bool`.

Coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
</context>

<requirements>

### 1. Package API — `pkg/queue/queue.go`

Create `pkg/queue/queue.go` with the repo copyright header and a package doc comment stating: the queue holds vault-ui's pending frontmatter writes in memory, one FIFO per vault; each vault's writes are applied one at a time in submission order by that vault's consumer; vaults never wait on each other; nothing is persisted, so a crash loses unapplied writes; the seam is deliberately three operations wide and is to be replaced by `libqueue` when that library lands.

Declare exactly this public surface (the doc comments may be reworded, the names and signatures may not):

```go
// Write is one queued vault write.
type Write struct {
	// Vault is the configured vault name. It keys the FIFO the write joins;
	// writes to different vaults never wait on each other.
	Vault string
	// ItemID names the task or goal the write targets. It is used for logs only.
	ItemID string
	// Apply performs the write and its post-write side-effects. The vault's
	// consumer calls it with the consumer context passed to Consume — never
	// with the context passed to Enqueue.
	Apply run.Func
}

// ErrClosed is returned (wrapped) by Enqueue once Consume has returned.
var ErrClosed = stderrors.New("write queue closed")

//counterfeiter:generate -o ./mocks/queue-queue.go --fake-name Queue . Queue

// Queue is vault-ui's in-tree producer/consumer seam for vault writes.
type Queue interface {
	// Enqueue appends write to its vault's FIFO and returns without waiting
	// for it to be applied. The queue is unbounded: an accepted write is never
	// dropped while Consume runs.
	Enqueue(ctx context.Context, write Write) error
	// Consume applies queued writes until ctx is cancelled: one consumer
	// goroutine per vault with pending writes, each applying its vault's writes
	// one at a time in submission order. It returns nil after ctx is cancelled
	// and every in-flight Apply has returned.
	Consume(ctx context.Context) error
	// Done returns a channel that is closed once the vault has no pending and
	// no in-flight write. For an idle or unknown vault it returns an
	// already-closed channel.
	Done(vault string) <-chan struct{}
}

// NewQueue returns an empty Queue. Writes enqueued before Consume starts are
// held and applied once it does.
func NewQueue() Queue
```

`stderrors` is the stdlib `errors` package imported under that alias (the repo already does this in `pkg/websocket/connection_manager.go`); everything else uses `github.com/bborbe/errors`.

### 2. Implementation

Use one `sync.Mutex` guarding a `map[string]*vaultQueue`, where a `vaultQueue` holds:
- `pending []Write` — an unbounded slice (no buffered channel, no capacity limit);
- `running bool` — a consumer goroutine is active for this vault;
- `done chan struct{}` — created fresh when the vault goes from idle to busy, closed when it becomes idle again.

The queue struct also holds the consumer context (nil until `Consume` starts), a `closed bool`, and a `sync.WaitGroup` counting live consumer goroutines.

This is a deliberate, documented exception to `go-concurrency-patterns.md` rule `go-concurrency/no-raw-go-func`: consumers are spawned per vault on demand, which none of the `github.com/bborbe/run` strategies express (`run.ConcurrentRunner` has a channel bounded by `maxConcurrent` whose `Add` blocks — both violate this prompt's unbounded, never-blocking contract). Use a raw `go q.drain(...)` plus the `sync.WaitGroup` exactly as specified here, put a one-line comment above the `go` statement naming this exception, and do not replace it with a `run` primitive.

**Enqueue(ctx, write)**:
1. Return `errors.Errorf(ctx, "enqueue write: empty vault")` when `write.Vault == ""`, and `errors.Errorf(ctx, "enqueue write for vault %s: nil Apply", write.Vault)` when `write.Apply == nil`. Nothing is queued in either case.
2. Under the lock: if `closed`, return `errors.Wrapf(ctx, ErrClosed, "enqueue write for vault %s", write.Vault)`.
3. Get or create the vault's `vaultQueue`. If the vault is idle (`len(pending) == 0 && !running`), set `done = make(chan struct{})`.
4. Append the write. If the consumer context is set and `!running`, set `running = true`, `wg.Add(1)`, and start the vault's consumer goroutine (step "drain" below).
5. After unlocking, log `glog.V(2).Infof("write queue vault=%s item=%s depth=%d", ...)` where depth is the pending count observed under the lock. This is the per-vault queue-depth log the spec's Failure Modes table names.
6. Never block on the write being applied. The `ctx` argument is used only for error wrapping — it must never reach `Apply`.

**drain (one goroutine per busy vault)** — loop:
1. Lock. If `consumerCtx.Err() != nil`: count `len(pending)`, set `pending = nil`, `running = false`, close `done`, unlock, log `glog.Warningf("write queue vault=%s dropped %d unapplied writes on shutdown", ...)` when the count is > 0, `wg.Done()`, return.
2. If `len(pending) == 0`: set `running = false`, close `done`, unlock, `wg.Done()`, return. The vault now has no running work.
3. Pop the head write (`w := pending[0]; pending[0] = Write{}; pending = pending[1:]` so the popped closure is not retained), unlock.
4. Apply it via a helper `applyOne(ctx, vault, w)` that:
   - recovers a panic, logging `glog.Errorf("write queue vault=%s item=%s panicked: %v", vault, w.ItemID, r)` — the consumer must not die; the loop continues with the next write;
   - logs a returned error with `glog.Warningf("write queue vault=%s item=%s failed: %v", vault, w.ItemID, err)` and continues.
5. Loop.

**Consume(ctx)**:
1. Under the lock: if a consumer context is already set, return `errors.New(ctx, "write queue already consuming")`. Otherwise store `ctx` as the consumer context and start a drain goroutine (with `running = true`, `wg.Add(1)`) for every vault that has pending writes and is not running.
2. Unlock and block on `<-ctx.Done()`.
3. Lock, set `closed = true`, unlock, then `wg.Wait()` so every in-flight `Apply` has returned (the drains observe the cancelled context and exit, dropping what is left — step 1 of drain).
4. Return `nil`. If an `Apply` ignores its cancelled context, step 3 blocks until it returns; this is accepted (the service manager escalates to SIGKILL) — do not add a shutdown timeout, and say so in the package doc comment.

**Done(vault)**: under the lock, return the vault's `done` channel when the vault is busy (`len(pending) > 0 || running`); otherwise return a package-level channel that is already closed (create it once, e.g. `var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()`). Writes held before `Consume` starts count as busy, so `Done` for such a vault returns an open channel that closes after `Consume` drains it.

No other exported identifiers. No configuration knobs, no capacity limit, no retry, no metrics, no persistence.

### 3. Counterfeiter fake

Generate `pkg/queue/mocks/queue-queue.go` from the directive above: from `pkg/queue`, run `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`. Prepend the copyright header used by the existing files in `pkg/pageindex/mocks/`. Do not add counterfeiter to `go.mod`. If the module proxy is unreachable, hand-write the fake in counterfeiter's exact shape (fake name `Queue`, package `mocks`, `EnqueueStub`/`EnqueueCallCount`/`EnqueueArgsForCall`/`EnqueueReturns`, `ConsumeStub`/…, `DoneStub`/…, `Invocations`, and the `var _ queue.Queue = new(Queue)` assertion).

### 4. Tests — `pkg/queue/queue_suite_test.go` and `pkg/queue/queue_test.go`

External test package `queue_test`, Ginkgo v2 + Gomega, suite file copied from `pkg/pageindex/pageindex_suite_test.go` (suite name "Queue Suite"). Each test runs `Consume` in a goroutine on a cancellable context and cancels it in `DeferCleanup`, waiting for `Consume` to return. Use a mutex-guarded apply log (a `[]string` of write identities) and per-write release channels to block a write deterministically. A blocking test `Apply` must `select` on its release channel and on `ctx.Done()` — except test 10's in-flight write, which deliberately waits only on its release channel — and every release channel must be closed in a cleanup that runs before the Consume-cancel cleanup (`DeferCleanup` is LIFO), so no spec can leave `Consume` stuck in `wg.Wait()`. Use `Eventually`/`Consistently` with short explicit timeouts, never bare `time.Sleep` assertions.

Cover, at minimum:
1. **AC2 — per-vault FIFO.** Enqueue write `A` (blocks on a release channel) then `B` to vault `alpha`. `Consistently` the log does not contain `B` while `A` is blocked. Release `A`; `Eventually` the log equals exactly `["A", "B"]`.
2. **AC2 — no cross-vault serialization.** With `A` still blocked in `alpha`, enqueue `C` to vault `beta`; `Eventually` the log contains `C` while `A` is still unreleased (assert `A`'s apply has not returned at that moment).
3. **Enqueue does not wait.** `Enqueue` returns while the write's `Apply` is blocked.
4. **Consumer context, not producer context.** Enqueue with an already-cancelled context; `Apply` observes `ctx.Err() == nil`.
5. **Writes held before Consume starts** are applied in order once `Consume` starts; `Done(vault)` is open before and closes after.
6. **A failing write does not stop the vault**: `Apply` returning an error is followed by the next write being applied.
7. **A panicking write does not kill the consumer**: the next write in the same vault is applied.
8. **Unbounded**: with the first write blocked, enqueue 1000 more writes to the same vault; after release all 1001 are applied, in submission order.
9. **Done**: an unknown vault returns a closed channel; a busy vault returns an open channel that closes once drained; a new write after idle produces a fresh open channel.
10. **Shutdown**: cancel the `Consume` context while a write is in flight and others are pending; `Consume` returns `nil` only after the in-flight `Apply` returns; the pending writes are never applied; a later `Enqueue` returns an error for which `errors.Is(err, queue.ErrClosed)` is true.
11. **Consume twice** returns an error.
12. **Validation**: empty `Vault` and nil `Apply` are rejected and nothing is applied.

Coverage on `pkg/queue` must be ≥ 80% (excluding `mocks/`).

### 5. No wiring, no docs, no changelog in this prompt

Do not import `pkg/queue` from any other package yet, and do not edit `main.go`, `pkg/factory/`, `pkg/mutations/`, `pkg/handler/`, `docs/`, or `CHANGELOG.md`. Prompt 2 of this spec creates the `## Unreleased` CHANGELOG section (the spec assigns `CHANGELOG.md` to prompt 2 alone so no two prompts edit it), and prompt 4 writes `docs/optimistic-writes.md`.

### 6. Self-check

Re-run every `<verification>` command and confirm each passes. Name the test establishing each of AC2's two halves (FIFO order; cross-vault independence).

</requirements>

<constraints>
- The seam is an in-tree interface with exactly three operations — enqueue one write, consume in FIFO order, report the consumer's done state — and no wider surface. No `libqueue` dependency.
- No durable queue: in-memory only; a crash loses only unflushed writes.
- The queue is unbounded; a write accepted by the queue is never dropped silently while it runs. One consumer runs per vault; a vault with no pending writes has no running work.
- No cross-vault ordering guarantee and no global ordering across vaults; a write to another vault is never blocked by this vault's backlog.
- No retry of a failed write beyond surfacing it (here: logging it).
- A consumer panic must not kill that vault's consumer silently: recover, log at ERROR naming the vault, continue.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no silently ignored error returns, counterfeiter mocks, Ginkgo/Gomega tests in external `_test` packages, ≥ 80% coverage on the new package.
- `go test -race` must stay clean.
- No new configuration field, flag, capacity knob or metric.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`. Do NOT run `go mod vendor`.
- Do NOT edit `CHANGELOG.md` (prompt 2 owns it) and do not create a `## Unreleased` section — prompt 2 inserts it and asserts exactly one exists. This intentionally leaves `docs/dod.md`'s "CHANGELOG.md has an entry under `## Unreleased`" criterion unmet for this prompt only: when self-reviewing against `docs/dod.md`, record that criterion as "deferred to prompt 2 of spec 025 by design" and do NOT report it as a blocker or downgrade the result to partial.
- Do NOT commit and do NOT run any git command — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
set -o pipefail; go test -race -coverprofile=/tmp/queue.cover ./pkg/queue/ && go tool cover -func=/tmp/queue.cover | awk '/^total:/{sub("%","",$3); exit !($3+0 >= 80)}'
```
Must exit 0 — `./pkg/queue/` only, so `mocks/` does not count toward the 80% coverage floor.

```
go vet ./pkg/queue/...
```
Must exit 0.

```
test -f pkg/queue/mocks/queue-queue.go && grep -q 'var _ queue.Queue = new(Queue)' pkg/queue/mocks/queue-queue.go
```
Must exit 0.

```
! grep -rn '"github.com/bborbe/vault-ui/pkg/queue"' --include='*.go' pkg main.go | grep -v '^pkg/queue/'
```
Must exit 0 (nothing outside `pkg/queue` imports it yet).

```
make precommit
```
Must exit 0.
</verification>
