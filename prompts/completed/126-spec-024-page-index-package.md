---
status: completed
spec: [024-serve-list-reads-from-page-index]
summary: Added pkg/pageindex, a concurrency-safe in-memory snapshot store implementing vault-cli's storage.PageStorage with per-key shared builds, dirty marks, coalesced event-triggered rebuilds and a clock-driven rescan loop, plus its counterfeiter fake, 15 Ginkgo specs at 97.9% coverage, docs/page-index.md and a CHANGELOG entry; nothing is wired into the service yet.
execution_id: vault-ui-page-index-exec-126-spec-024-page-index-package
dark-factory-version: v0.196.0
created: "2026-10-05T19:39:53Z"
queued: "2026-10-05T20:18:36Z"
started: "2026-10-05T20:19:54Z"
completed: "2026-10-05T20:26:04Z"
branch: dark-factory/126-spec-024-page-index-package
---

# Add the in-memory page index package that serves vault page lists from snapshots

<summary>
- A new building block keeps the parsed pages of each vault folder in memory, one snapshot per folder, so a list read can be answered without opening any vault file.
- It plugs in exactly where vault-cli reads a folder today, so vault-cli's own filtering, sorting and blocked-state logic keep producing the responses; nothing about the list logic is reimplemented.
- When a folder has never been read, or was marked stale by a write, the first reader triggers one rebuild and every concurrent reader waits for that same rebuild instead of starting its own.
- A change notification for a folder rebuilds that folder in the background; readers keep getting the previous snapshot until the new one is swapped in, so a refresh never slows a read down.
- A caller that triggered a refresh can wait until a rebuild that started after its request has been swapped in — the hook the live-update channel will use so a browser never re-fetches stale data.
- Bursts of change notifications collapse: at most one rebuild runs per folder, plus one queued follow-up that covers everything that arrived meanwhile.
- A background loop rebuilds every known folder at least once a minute as a safety net for missed notifications.
- A failed rebuild keeps serving the previous snapshot and logs the folder that failed; the next trigger retries.
- The staleness rules, frame-ordering rule and folder-key rule are written down in a new doc page.
- Nothing is wired into the running service yet — that is the next prompt.
</summary>

<objective>
Create `pkg/pageindex`: a process-wide, concurrency-safe snapshot store that implements vault-cli's `storage.PageStorage` and adds shared builds, dirty marks, event-triggered rebuilds with a post-swap completion signal, and a clock-driven rescan loop. This is the foundation that lets the board's list endpoints (4–9 s per request today, because every request re-parses every vault file) answer from memory in later prompts.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md` (the Definition of Done the validation step checks).

Read the spec `specs/in-progress/024-serve-list-reads-from-page-index.md` in full. This prompt covers Desired Behaviors 1, 3, 6, 7 and, at the unit level, Acceptance Criteria AC2, AC3 and AC6. Later prompts wire the index into reads (prompt 2), the watcher (prompt 3) and writes (prompt 4).

Read these vault-cli v0.159.0 sources (in-container module path `/home/node/go/pkg/mod/github.com/bborbe/vault-cli@v0.159.0`; if that path is absent, run `go mod download github.com/bborbe/vault-cli` first):
- `pkg/storage/storage.go` — the interface to implement, verbatim:
  ```go
  //counterfeiter:generate -o ../../mocks/page-storage.go --fake-name PageStorage . PageStorage
  type PageStorage interface {
  	ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
  }
  ```
  `NewPageStorage(storageConfig *Config) PageStorage` (nil config → `DefaultConfig()`).
- `pkg/storage/page.go` — `(*pageStorage).ListPages`: a missing directory returns `nil, nil`; an unreadable page file is skipped with a warning (not an error); a `ctx` cancellation mid-walk returns the pages read so far plus a wrapped error. It reads only its `(vaultPath, pagesDir)` arguments — the storage config is not consulted on this path.
- `pkg/ops/list.go` — `listOperation.Execute` calls `ListPages` once per request, then `filterTasks` builds a NEW slice before `sort.Slice`, and `IsBlocked(blockedBy, tasks)` only reads. So a snapshot slice and its `*domain.Page` pointers can be shared read-only by concurrent requests.
- `mocks/page-storage.go` — the counterfeiter fake `mocks.PageStorage` (import `github.com/bborbe/vault-cli/mocks`) to use as the underlying storage in tests (`ListPagesStub`, `ListPagesCallCount`, `ListPagesArgsForCall`).
- `pkg/domain/page.go` — `domain.NewPage(data map[string]any, meta FileMetadata, content Content) *Page`, for building test pages.

Read these `github.com/bborbe/time` (`libtime`) v1.27.14 APIs you will use (verified):
- `type CurrentDateTimeGetter interface { Now() DateTime }`; `libtime.NewCurrentDateTime() CurrentDateTime` with `SetNow(now DateTime)` for tests.
- `type WaiterDuration interface { Wait(ctx context.Context, duration Duration) error }`; `libtime.NewWaiterDuration()`; `libtime.WaiterDurationFunc` adapter.
- `type Duration time.Duration` with `.Duration() time.Duration`; `func (d DateTime) Sub(time HasTime) Duration`.

Read `github.com/bborbe/run` v1.11.0: `type Func func(context.Context) error`; `func All(ctx context.Context, funcs ...Func) error`.

Patterns in this repo to follow:
- `pkg/statuscache/statuscache.go` + `pkg/statuscache/statuscache_suite_test.go` — package doc comment, mutex-guarded in-memory state, Ginkgo suite file shape.
- `pkg/websocket/metrics.go` — the `//counterfeiter:generate -o ./mocks/<file>.go --fake-name <Name> . <Interface>` directive form; `pkg/websocket/mocks/websocket-metrics.go` — generated mock file with the copyright header prepended.
- `pkg/cleanup/cleanup.go` — `glog.Errorf` / `glog.V(2).Infof` logging style.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — no raw `go func()`; use `run.All` for parallel work.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md` — inject `CurrentDateTimeGetter`, test with `SetNow`, never mock the clock.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. Package surface (`pkg/pageindex/pageindex.go`)

Every new `.go` file (source, tests, generated mock) starts with the repo's existing copyright header — copy it verbatim from `pkg/statuscache/statuscache.go`.

Create package `pageindex` with a package doc comment stating: what the index caches, that it implements vault-cli's `storage.PageStorage`, that snapshots are immutable after publication, and a pointer to `docs/page-index.md`.

Exported surface (exact names; bodies are yours):

```go
// Key identifies one indexed folder: a vault root plus a vault-relative pages dir.
type Key struct {
	VaultPath string
	PagesDir  string
}

// NewKey builds a Key, applying filepath.Clean to both parts so the read side
// and the event side derive identical keys from equivalent paths.
func NewKey(vaultPath, pagesDir string) Key

// RescanInterval is the time between full rescans of every known key.
const RescanInterval = 50 * time.Second

//counterfeiter:generate -o ./mocks/pageindex-page-index.go --fake-name PageIndex . PageIndex
type PageIndex interface {
	storage.PageStorage
	Build(ctx context.Context, keys []Key) error
	Refresh(ctx context.Context, key Key) error
	MarkDirty(keys ...Key)
	MarkAllDirty()
	Rescan(ctx context.Context) error
}

func NewPageIndex(
	pageStorage storage.PageStorage,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
) PageIndex
```

Imports: `github.com/bborbe/vault-cli/pkg/storage`, `github.com/bborbe/vault-cli/pkg/domain`, `libtime "github.com/bborbe/time"`, `github.com/bborbe/run`, `github.com/bborbe/errors`, `github.com/golang/glog`.

`RescanInterval` is 50 s (not 60 s) so that interval + the 1 s poll granularity + a ~2 s rebuild of the largest folder stays inside the spec's 60 s ceiling. Add two unexported constants: `rescanPollInterval = time.Second` and `rebuildTimeout = 2 * time.Minute` (bounds one storage call so a hung filesystem cannot hold a key's rebuild — and the frames waiting on it — forever). None of these are configurable; the spec forbids new knobs.

`ListPages(ctx, vaultPath, pagesDir)` derives its key with `NewKey(vaultPath, pagesDir)` and passes the key's cleaned values to the underlying storage.

### 2. Per-key state and the rules it obeys

Keep one entry per `Key`, guarded by a single `sync.Mutex` on the index (never hold it during a storage call). Each entry tracks:
- the current snapshot (`[]*domain.Page`), whether one exists, and `snapshotSeq` — the start sequence of the build that produced it;
- `requestSeq` — a per-key counter bumped by every `Refresh` and every dirty mark;
- `dirtySeq` — the value of `requestSeq` at the most recent dirty mark;
- `inflight` — the build currently calling storage (at most one), with its `startSeq`, a `done` channel, and its result;
- `followUp` — at most one queued build that has not started yet.

A key is **clean** when it has a snapshot and `snapshotSeq >= dirtySeq`.

Rules (these are the contract; the tests in step 4 check each one):

- **R1 clean read** — `ListPages` on a clean key returns the snapshot slice itself with no storage call. Do not copy pages; never mutate a published slice or page.
- **R2 read that needs a build** (no entry, no snapshot, or dirty) — create the entry if missing, then wait on the first of: `inflight` if `inflight.startSeq >= dirtySeq`; else the existing `followUp`; else, if a build is in flight, a newly created `followUp`; else a new build started now. A never-built key with a build already in flight therefore shares that build (one `ListPages` per key at cold start), and concurrent readers of a dirty key share exactly one rebuild that started after the dirty mark.
- **R3 Refresh(key)** — create the entry if missing; bump `requestSeq`; if nothing is in flight start a build now, otherwise join the `followUp` (creating it if absent). Block until that build finishes. Returns nil when it finishes, whether the storage call succeeded or failed (a failure is logged, see R6); returns a wrapped `ctx` error only if `ctx` is cancelled first. A refresh never marks the key dirty, so reads keep returning the previous snapshot while it runs.
- **R4 MarkDirty / MarkAllDirty** — bump `requestSeq` and set `dirtySeq = requestSeq` for each named key (`MarkDirty`) or every known key (`MarkAllDirty`). No storage call happens here; the next read performs the rebuild (R2). Marking a key the index has never seen is a no-op (its first read builds it anyway).
- **R5 one build per key at a time** — when a build finishes, under the same lock acquisition that clears it: if a `followUp` exists, promote it immediately (`inflight = followUp`, `followUp = nil`, `startSeq = requestSeq` at that moment) so no other caller can start a parallel build in the gap. The caller that created a build is its runner: it waits for promotion if it is a follow-up, then performs the storage call outside the lock. The runner must finish its build even if its own `ctx` is cancelled — derive the storage call's context with `context.WithoutCancel(ctx)` bounded by `context.WithTimeout(..., rebuildTimeout)`. This includes the wait for promotion: a follow-up runner never abandons its follow-up on `ctx.Done()` while waiting to be promoted (an abandoned follow-up would be promoted to `inflight` with nobody to run it, wedging the key forever); it returns its `ctx` error to its caller only after the build it owns has swapped or failed. Callers that only joined a build select on `done` and on their own `ctx.Done()`.
- **R6 swap and failure** — on success, swap in the new slice and set `snapshotSeq = startSeq`. On failure, keep the previous snapshot unchanged and log `glog.Errorf` naming the vault path and pages dir and the error. A failed build leaves a dirty key dirty, so the next read, event or rescan retries.
- **R7 ListPages result after waiting** — the build's pages on success; on failure the current (previous) snapshot with a nil error if one exists, otherwise the build error wrapped with `errors.Wrapf(ctx, err, ...)` naming the key (this keeps today's 500 for a folder that never built).
- **R8 Build(ctx, keys)** — warm the given keys concurrently via `run.All`, each behaving like a cold read (R1/R2) whose pages are discarded. A per-key failure is logged (R6) and does not fail `Build`. Return a wrapped error only if `ctx` was cancelled before all keys finished.
- **R9 Rescan(ctx)** — record `lastRescan := currentDateTimeGetter.Now()`, then loop: `waiter.Wait(ctx, libtime.Duration(rescanPollInterval))`; on `ctx` cancellation return nil; when `Now().Sub(lastRescan).Duration() >= RescanInterval`, set `lastRescan = Now()` and `Refresh` every known key concurrently via `run.All`, waiting for all before the next poll so rescans never pile up. Rescan never touches WebSocket frames (the index has no such dependency).

No raw `go func()` / `go f()` in non-test code — every concurrent fan-out uses `run.All`, and every build runs on the goroutine of the caller that created it.

### 3. Counterfeiter fake

Add the `//counterfeiter:generate` directive shown above and generate `pkg/pageindex/mocks/pageindex-page-index.go`: from `pkg/pageindex`, run `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate` (the `@version` form leaves `go.mod` untouched; do not add counterfeiter to `go.mod`). Prepend the same copyright header the existing generated mocks carry. If the module proxy is unreachable, hand-write the fake in the exact shape of `pkg/mutations/mocks/event_publisher.go`.

### 4. Tests (`pkg/pageindex/pageindex_suite_test.go`, `pkg/pageindex/pageindex_test.go`)

External test package `pageindex_test`, Ginkgo/Gomega, suite file shaped like `pkg/statuscache/statuscache_suite_test.go`. Underlying storage is `mocks.PageStorage` from `github.com/bborbe/vault-cli/mocks`, with a `ListPagesStub` serving per-key page slices (build pages with `domain.NewPage`, distinguishable by name or a frontmatter value) and, where a test needs it, blocking on a channel. Clock: `libtime.NewCurrentDateTime()` + `SetNow`. Waiter: a `libtime.WaiterDurationFunc` that returns after ~1 ms real time or on `ctx.Done()`.

Cover at least:
1. **Clean read touches no storage** — after the first read of a key, five more reads add 0 `ListPages` calls and return the same pages.
2. **Cold read shares one build** — with the stub blocked, start N concurrent `ListPages` on a never-built key (and a `Build` of the same key); release; exactly 1 call, all callers get the pages.
3. **AC2 (unit level)** — two vaults A and B, tasks + goals folders, all warmed with `Build`; change the stub's content for A's tasks; `Refresh(A tasks)` → exactly 1 new call with args `(A.path, A.tasks)` and 0 new calls for every other key; the next read returns the new content.
4. **AC3** — with a snapshot in place, block the stub, start `Refresh` in a test goroutine, wait until the stub reports it was entered, then `ListPages` on that key returns the previous snapshot within 100 ms (measure it); release and confirm the next read returns the new content.
5. **Follow-up coalescing and post-start guarantee** — while a refresh build is blocked, issue three more `Refresh` calls for the same key; give them a settle window (a ≥ 100 ms `Consistently` that the `ListPages` call count stays at 1) so all three have joined the follow-up; then set new content; release; total exactly 2 calls (in-flight + one follow-up); none of the three later `Refresh` calls returns before the follow-up has swapped (assert by having each caller read the key immediately after its `Refresh` returns and see the newest content).
6. **Dirty mark** — `MarkDirty(key)` makes 0 calls; the next read makes exactly 1 call and returns new content; two concurrent reads after one dirty mark make exactly 1 call; `MarkDirty` while a build that started before the mark is in flight forces the reader onto a build that started after the mark (the reader sees the post-mark content). `MarkAllDirty` dirties every known key. Marking an unknown key is a no-op.
7. **Failure** — a build error with a previous snapshot: the read returns the previous snapshot with nil error, `Refresh` returns nil, the key stays dirty if it was dirty (next read calls storage again); a first build that errors: the read returns an error. 
8. **AC6** — `SetNow` the clock to a fixed instant first; warm several keys; run `Rescan` in a test goroutine with the fake waiter; assert 0 new calls while the clock is not advanced (use `Consistently` over a short window); `SetNow` past `RescanInterval`; `Eventually` every known key has exactly one new call and new content is visible; then still no further calls without another clock advance; cancelling `ctx` makes `Rescan` return nil. Also assert `pageindex.RescanInterval <= 60*time.Second`.
9. **Context** — a reader waiting on another caller's blocked build returns a wrapped error when its own `ctx` is cancelled, and the build still completes and swaps for the next reader; `Build` with a cancelled `ctx` returns an error. A reader that created a follow-up (dirty key, pre-mark build in flight) and whose `ctx` is cancelled while waiting for promotion (settle ≥ 100 ms via `Consistently` before cancelling, so the follow-up exists) still runs that follow-up: after release the key is clean, exactly 2 calls were made in total, and a further read makes 0 calls (guards R5 against a wedged key).
10. **NewKey** — `NewKey("/v/", "24 Tasks/")` equals `NewKey("/v", "24 Tasks")`.

Run the package under `-race`. The new package needs ≥ 80% statement coverage (see verification).

### 5. Docs (`docs/page-index.md`)

Create `docs/page-index.md` with these three `##` sections (exact headings) plus a short intro:
- `## Staleness bounds` — warm reads never touch disk; an external edit becomes visible after the watcher's ~100 ms debounce plus one rebuild of that folder; a missed watcher event is repaired by the rescan within `RescanInterval` + poll + one rebuild (under 60 s normally; if a rebuild hangs, each storage call is bounded by `rebuildTimeout` (2 min), so the worst case is rescan interval + that timeout); writes made through vault-ui are visible to the next read (dirty mark → one shared rebuild); topics folders are not watched and refresh via rescan and `POST /api/cache/reload`; a failed rebuild keeps serving the previous snapshot; a process restart starts empty and cold reads wait for the startup build.
- `## Frame ordering` — a task/goal watcher event's WebSocket frame is sent only after a rebuild that started after that event has been swapped in (so a client re-fetching on the frame sees fresh data); events for the same folder share at most one in-flight rebuild plus one follow-up; no ordering guarantee across folders beyond vault-cli's per-file delivery; theme/objective frames and route-originated frames are unchanged; the rescan never sends frames.
- `## Key derivation` — a key is `NewKey(vault path, folder)`; the read side derives it from the board vault's `Path` + `TasksFolder`/`GoalsFolder`/`TopicsFolder` (vault-cli's `ListOperation` passes exactly those to `ListPages`); the event side maps the watcher's vault name to the configured vault-cli vault and uses its `Path` + `GetTasksDir()`/`GetGoalsDir()`; both come from the same vault-cli config entry, and `vaultconfig.BuildVaultConfig` sets `TasksFolder = TasksDir` (non-empty) and `GoalsFolder = GetGoalsDir()`, so they match; a mismatch silently disables refresh, which the watcher tests guard.

### 6. CHANGELOG

`CHANGELOG.md` has no `## Unreleased` section yet. Add one directly above the newest `## vX.Y.Z` heading, with a `feat:` bullet describing the new in-memory page index package (not yet wired into the service), following `changelog-guide.md`.

### 7. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes; walk Desired Behaviors 1, 3, 6, 7 and AC2/AC3/AC6 against the tests you wrote and name the test that establishes each.

</requirements>

<constraints>
- Response bodies, status codes, routes, query parameters, and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set is unchanged.
- vault-cli `ops.ListOperation` and its filter/sort/blocked logic stay the only list logic; vault-ui must not parse page files itself. vault-cli stays pinned at `v0.159.0` via `require`, never `replace`.
- Snapshots are never mutated after publication; concurrent readers share `*domain.Page` pointers read-only. `go test -race` must stay clean.
- No persistence of the index to disk; no new external service dependency; no new HTTP route, query parameter, or opt-out flag; no configurable interval.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` for the rescan clock, ≥ 80% coverage on the new package.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- This prompt does not wire the index into `pkg/factory`, `main.go`, `pkg/board` or `pkg/mutations` — later prompts do.
- Do NOT modify anything under `src/vault_ui/` (Python backend and frozen frontend).
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0 (format, vet, `go test -race ./...`, Python lint/typecheck/tests).

```
go test -race ./pkg/pageindex/...
```
Must pass.

```
go test -race -coverprofile=/tmp/pageindex.cover ./pkg/pageindex/ && go tool cover -func=/tmp/pageindex.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80%).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/pageindex/
```
Must exit 0 (no raw goroutines in non-test code).

```
grep -q '^## Staleness bounds' docs/page-index.md && grep -q '^## Frame ordering' docs/page-index.md && grep -q '^## Key derivation' docs/page-index.md
```
Must exit 0.
</verification>
