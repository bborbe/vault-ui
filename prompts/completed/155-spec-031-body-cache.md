---
status: completed
spec: [031-task-list-body-cache]
summary: Added a task-list body cache keyed by (snapshot generation, query) that holds the identity and gzip forms beside the snapshot, exposed SnapshotGeneration, and wired ListTasksBody through the board interface with internal tests.
execution_id: vault-ui-exec-155-spec-031-body-cache
dark-factory-version: v0.196.0
created: "2026-10-09T20:51:14Z"
queued: "2026-10-09T21:18:12Z"
started: "2026-10-09T21:48:36Z"
completed: "2026-10-09T21:53:05Z"
branch: dark-factory/task-list-body-cache
---

# Hold one encoded and gzipped task-list body per (snapshot generation, query)

<summary>
- A warm task-list read stops repeating the per-request projection and JSON marshal of the whole row set.
- The board builds the response body once for a given query and holds both the plain and the gzip-compressed form.
- A second identical request is served the stored bytes, with no row walk, no projection and no marshal.
- A request for a query that has not been built yet is built by the existing path and its result is held.
- The bytes served are exactly the bytes the current implementation produces for the same query, so the decoded JSON is unchanged.
- Held bodies are keyed by the task-list snapshot generation, so a new snapshot makes new rows visible and retires the old bodies.
- `/api/assignees` keeps reading the published rows and is untouched.
- The handler still runs its existing path in this step; nothing about how responses are written changes yet.
- No new configuration, no size limit, no time-to-live and no timer.
</summary>

<objective>
Give the board a body cache beside the task-list snapshot: for a given snapshot generation and a given query it builds the response body once, holds the identity and gzip forms, and reuses them for every matching request. This is the foundation the later prompts build on — the clock-boundary invalidation, the handler's negotiation and the end-to-end proof. It covers the spec's Suggested Decomposition row 1 (Desired Behaviors 1, 2, 3 and 5; Acceptance Criteria 1 and 2).
</objective>

<context>
Read the spec `specs/in-progress/031-task-list-body-cache.md` in full, especially Desired Behaviors 1, 2, 3 and 5, the Constraints, the Failure Modes rows for a failed build and for a generation move during a build, and Acceptance Criteria 1 and 2.

Read `docs/dod.md` and `CLAUDE.md` at the repo root.

Read the current code in full before changing anything:
- `pkg/board/board.go` — `Deps`, `Board`, `board`, `New`, `TaskQuery`, `DefaultUpcomingHours`, `LookbackHours`. Note `b.snapshot = newTaskSnapshotStore(...)` in `New`.
- `pkg/board/tasks.go` — `ListTasks`, `taskRow`, `visibleRow`, `taskResponse`. `ListTasks` is the projection the body cache wraps; it must keep its current signature and behaviour.
- `pkg/board/snapshot.go` — `taskSnapshotStore`, `taskSnapshotEntry`, `taskSnapshotBuild`, `newTaskSnapshotStore`, `List`, `runBuild` (the success path is the block that assigns `e.rows = rows`, `e.hasSnapshot = true`, `e.pageRevision`, `e.sessionGeneration` and then `close(b.done)`).
- `pkg/board/vaults.go` — `ListAssignees`. It reads `b.snapshot.List` and must keep doing so.
- `pkg/board/helpers.go` — `parseDateTime`, `parseDeferDate`, `strPtr`. The encoder's trim and the date parsers are already here; do not duplicate them.
- `pkg/handler/api_common.go` — `writeJSON`. The body cache's encoder must reproduce this function's bytes exactly (`json.NewEncoder`, `SetEscapeHTML(false)`, trim the trailing newline).
- `pkg/handler/api_tasks.go` — `NewTasksHandler`. It keeps calling `b.ListTasks` in this prompt.
- `pkg/handler/api_test.go` — `fakeBoard` and the `BeforeEach` that builds it. Adding a method to the `Board` interface makes `fakeBoard` fail to compile unless it implements it.
- `pkg/board/snapshot_internal_test.go` — the existing internal test file (package `board`) and its fakes. The new cache tests go in a sibling internal test file.
- `pkg/board/board_test.go` — `harness`, `newHarness`, `fakeCache`, `fakeIndex`, `fakeProbe`, `fakeSignals`, `fakeVaults`, `fakeOps`, `item`, `listCounter`, `seamCounter`. These live in package `board_test` (external), so they are NOT visible to the new internal test file (package `board`): do not try to import or reuse them there.
- `docs/page-index.md` § *Task-list snapshot* — the rebuild/generation model this prompt extends; do not restate it.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-logging-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

`.dark-factory.yaml` sets `hideGit: true`, so the container's `.git` is masked. Never rely on a `git` command in this prompt: `git diff` silently becomes `--no-index` and prints fabricated results. All verification below is non-git.

Binding invariants that must not move:
- The decoded JSON of `/api/tasks` is unchanged: same fields, same order, same values, same `SetEscapeHTML(false)` behaviour.
- The task-list snapshot is never mutated after publication, and `ListAssignees` keeps reading parsed rows — the snapshot does not become bytes-only.
- No new middleware layer; `pkg/handler/router.go` keeps returning a bare router.
- No new timer and no configurable interval.
</context>

<requirements>

### 1. Expose the task-list snapshot generation

In `pkg/board/snapshot.go`:

- Add a `snapshotGeneration uint64` field to `taskSnapshotStore`.
- In `runBuild`, on the success path only — the block that sets `e.rows = rows`, `e.hasSnapshot = true`, `e.pageRevision = b.startRevision`, `e.sessionGeneration = b.startGeneration`, `b.rows = rows` and then `close(b.done)` — increment `s.snapshotGeneration` while the mutex is still held. Do NOT increment on the failure path, and do NOT increment anywhere else.
- Add:

```go
// SnapshotGeneration returns the number of successful task-list snapshot
// rebuilds so far, across every key. It advances by one whenever a rebuild
// publishes rows — a full build, a session refresh or a row patch — because
// each of those can change what a body renders. The body cache keys on it and
// drops a superseded generation's bodies.
func (s *taskSnapshotStore) SnapshotGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotGeneration
}
```

A rebuild is what makes new rows visible to a read, so a generation move is exactly the event that must retire a held body.

### 2. The body type, the seams and the cache

Add a new file `pkg/board/taskbody.go` (package `board`) with the imports `bytes`, `compress/gzip`, `context`, `encoding/json`, `fmt`, `sync`, `github.com/bborbe/errors`, `github.com/golang/glog` and `github.com/bborbe/vault-ui/pkg/api`. Add no other import: `time` and `libtime "github.com/bborbe/time"` are unused by the code below and Go rejects an unused import; prompt 2 adds them when the entry gains its boundary field.

Define the body type:

```go
// TaskListBody is a rendered GET /api/tasks response in the two forms the
// handler can serve: Identity is the JSON body the board produces today,
// Gzipped is its gzip-compressed form. Both are immutable once returned and are
// shared with every reader.
type TaskListBody struct {
	Identity []byte
	Gzipped  []byte
}
```

Define the cache's injectable parameters. Every field is a seam; `New` supplies the production values and a test supplies counting ones. There is deliberately no `Gzip` seam — the cache compresses through `gzipBody` directly, since no test injects a compressor:

```go
// taskBodyParams carries the body cache's injectable dependencies.
type taskBodyParams struct {
	// Project renders the query's rows. Production: the board's ListTasks.
	Project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	// Encode marshals the projected rows into the identity body. Production:
	// encodeTaskList, which reproduces the handler's writeJSON exactly.
	Encode func(value any) ([]byte, error)
	// Generations reports the task-list snapshot generation.
	Generations func() uint64
}
```

Define the entry and the cache:

```go
// taskBodyEntry is one held body. It is replaced wholesale on invalidation
// rather than mutated in place.
type taskBodyEntry struct {
	body TaskListBody
}

// taskBodyCache holds one rendered body per (snapshot generation, query). A
// superseded generation's entries are dropped rather than retained, so the cache
// holds at most one generation's bodies.
type taskBodyCache struct {
	mu          sync.Mutex
	generation  uint64
	entries     map[string]*taskBodyEntry
	project     func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	encode      func(value any) ([]byte, error)
	generations func() uint64
}

// newTaskBodyCache returns an empty cache over the given seams.
func newTaskBodyCache(params taskBodyParams) *taskBodyCache
```

`newTaskBodyCache` starts with an empty `map[string]*taskBodyEntry` and copies each seam. It has no branches.

### 3. Get: hold on a miss, serve on a hit

Add:

```go
// Get returns the rendered body for the query, building and holding it on a
// miss. A hit performs no projection, no marshal and no compression. A
// generation move drops the superseded generation's entries; a build that
// started before a generation move is not held.
func (c *taskBodyCache) Get(ctx context.Context, query TaskQuery) (TaskListBody, error)
```

It must do exactly this:

1. `key := taskBodyKey(query)`.
2. Lock. `gen := c.generations()`. If `gen != c.generation`, replace `c.entries` with a new empty map and set `c.generation = gen`. If an entry for `key` exists, copy its `body`, unlock and return it.
3. Unlock. Build: `responses, err := c.project(ctx, query)`. On error, log with `glog.Errorf` naming the `key`, the `query` and the error, and return the zero body and `errors.Wrapf(ctx, err, "build task-list body")`.
4. `identity, err := c.encode(responses)`; on error, log the same way and wrap it.
5. `gzipped, err := gzipBody(identity)`; on error, log the same way and wrap it.
6. `body := TaskListBody{Identity: identity, Gzipped: gzipped}`.
7. Lock. If `c.generations() == gen`, store `c.entries[key] = &taskBodyEntry{body: body}`. If the generation moved while the build ran, leave the entry unheld. Unlock.
8. Return `body, nil`.

Notes that are part of the contract:
- The build runs off the cache mutex, so a reader never waits on a build it did not start. A concurrent miss for the same key may build more than once; that is acceptable and does not affect correctness — the last successful build is held. Do NOT add a single-flight map.
- A failed build stores nothing and evicts nothing: every other held entry keeps serving. The triggering request returns the error; the next read retries. This is the spec's failed-build failure mode.
- The generation is captured once at step 2 and re-read at step 7. Do not capture it twice at the same point.

### 4. The key

Add:

```go
// taskBodyKey is a canonical key over every query field that changes the body,
// including upcoming_hours and session_live. It must distinguish repeated values
// and their order, because selectVaults preserves the requested order and the
// body follows it.
func taskBodyKey(query TaskQuery) string {
	return fmt.Sprintf("%q|%q|%q|%q|%q|%d|%t",
		query.Vaults,
		query.Statuses,
		query.Phases,
		query.Assignees,
		query.Goals,
		query.UpcomingHours,
		query.SessionLive,
	)
}
```

`%q` on a `[]string` prints the elements in order and distinguishes duplicates, so the key covers every body-changing parameter.

### 5. The production encoder and the one gzip site

Add both:

```go
// encodeTaskList marshals the projected rows exactly as the handler's writeJSON
// did: encoding/json with SetEscapeHTML(false) and the encoder's trailing
// newline trimmed. The bytes must match the handler's old output byte-for-byte,
// so the decoded body is unchanged.
func encodeTaskList(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// gzipBody compresses the identity body. It is the only place the board
// compresses a task-list body.
func gzipBody(identity []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(identity); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
```

`gzip.NewWriter` must appear exactly once in `pkg/` — here — and nowhere under `pkg/handler`. Use the default compression level; do not make it configurable.

### 6. The board method and the interface

In `pkg/board/board.go`:

- Add to the `Board` interface, after `ListTasks`:

```go
	// ListTasksBody renders the GET /api/tasks response for the query, serving a
	// held body when one exists for the current snapshot generation.
	ListTasksBody(ctx context.Context, query TaskQuery) (TaskListBody, error)
```

- Add a `bodies *taskBodyCache` field to the `board` struct.
- In `New`, after `b.snapshot = newTaskSnapshotStore(...)`, add:

```go
	b.bodies = newTaskBodyCache(taskBodyParams{
		Project:     b.ListTasks,
		Encode:      encodeTaskList,
		Generations: b.snapshot.SnapshotGeneration,
	})
```

In `pkg/board/tasks.go`, add:

```go
// ListTasksBody renders the GET /api/tasks response for the query from the body
// cache, which builds it once per (snapshot generation, query) and holds the
// identity and gzip forms. A hit is served the stored bytes with no row walk,
// no projection and no marshal.
func (b *board) ListTasksBody(ctx context.Context, query TaskQuery) (TaskListBody, error) {
	return b.bodies.Get(ctx, query)
}
```

### 7. Make the handler fakes satisfy the interface

`pkg/handler/api_test.go`'s `fakeBoard` implements `board.Board`, so it must implement `ListTasksBody` or the package will not compile.

- Add a field `taskBody board.TaskListBody` to `fakeBoard`.
- In the `BeforeEach` that builds the fake, set `taskBody: board.TaskListBody{Identity: []byte("[]"), Gzipped: []byte("[]")}` so prompt 3's handler test has a body to serve.
- Add:

```go
func (f *fakeBoard) ListTasksBody(
	_ context.Context, query board.TaskQuery,
) (board.TaskListBody, error) {
	f.gotTaskQuery = query
	if f.err != nil {
		return board.TaskListBody{}, f.err
	}
	return f.taskBody, nil
}
```

Do NOT import `compress/gzip` in `pkg/handler`; the fake returns bytes, it does not compress.

### 8. Do not change the handler in this prompt

`pkg/handler/api_tasks.go` keeps calling `b.ListTasks` and `writeJSON` exactly as it does today. Wiring the handler to `ListTasksBody` is prompt 3. This prompt only makes the new method exist and be correct.

### 9. Tests (`pkg/board/taskbody_internal_test.go`, package `board`)

Ginkgo/Gomega, internal package `board` (mirror `snapshot_internal_test.go`), building `newTaskBodyCache` directly with counting seams. Add small counters:

- a projection counter that increments and delegates to a supplied projection func;
- an encoder counter that increments and delegates to `encodeTaskList`;
- a generation func backed by a settable `uint64`.

Cover at least:

1. **AC1 — a held body is served without re-encoding or re-projecting** (name the `It` row exactly as the spec's evidence string, `serves a held body without re-encoding`). Two `Get` calls with the same query and a fixed generation: assert the encoder count is 1 AND the projection count is 1, and assert both returned `TaskListBody` values are equal. Counting only the encoder would leave "no row walk, no projection" unproven, so both counters are required.
2. **AC2 — the encoder reproduces the handler's bytes** (name the `It` row exactly as the spec's evidence string, `cached and uncached bodies are byte-identical`). Over a fixture of at least four queries covering the vault, status and phase filters, assert the cache's `Identity` equals `encodeTaskList(Project(query))` byte-for-byte (so a hit and a miss are byte-identical). Then pin the two properties that are the whole of the identity guarantee and are exactly what a plain `json.Marshal` would get wrong: (a) HTML is not escaped — include a fixture row whose title contains `<`, `>` and `&` and assert the identity bytes contain those characters literally, not `\u003c` / `\u003e` / `\u0026`; (b) there is no trailing newline — assert the last byte of the identity body is `]` (the body is a JSON array of tasks, so the encoder's newline-trimmed output ends with `]`, never `}`). Field order needs no assertion: it comes from the `api.TaskResponse` struct tags and cannot drift with the encoder.
3. **A distinct query builds again.** Two different queries each build once (projection and encoder counts both reach 2).
4. **A generation move drops the held entries.** `Get` once (counts 1); set the generation func to a new value; `Get` again (counts 2); assert the cache's `entries` map holds exactly one entry — the new generation's — so the superseded one was dropped rather than retained.
5. **A generation move during a build leaves the entry unheld.** Use a projection that bumps the generation before returning. Assert the returned body is still correct for the caller, but the cache holds no entry for the key, so the next read at the new generation builds again.
6. **The key distinguishes every body-changing field.** A `DescribeTable` over each `TaskQuery` field that changes the body — `Vaults`, `Statuses`, `Phases`, `Assignees`, `Goals`, `UpcomingHours` and `SessionLive` — asserting that two queries differing only in that field each build separately (both the projection and the encoder counts reach 2), so the key covers the whole body-changing field set and not just the string slices.
7. **The gzip form decompresses to the identity form.** Assert `Gzipped` round-trips: a `gzip.NewReader` over it reads back exactly `Identity`, byte-for-byte, so the compressor is pinned where it is introduced rather than only in prompt 4.
8. **A failed build stores nothing and evicts nothing.** Hold an entry for a first query with a successful `Get`, then call `Get` for a second, different query whose projection seam returns an error: assert `Get` returns the zero `TaskListBody` and the wrapped error, assert the cache holds no entry for the second query's key, and assert a further `Get` for the first query is still served from its held entry without re-projecting (its projection count does not advance). This pins requirement 3's failure contract and the spec's failed-build failure mode.
9. **The generation counter advances on success, and only on success.** This assertion belongs in `pkg/board/snapshot_internal_test.go` (package `board`), beside the existing "keeps the previous rows when a rebuild fails" row, not in the body-cache file. After a successful `List` assert `SnapshotGeneration() == 1`; after a build that fails assert it is unchanged (still `1`); after a second successful rebuild assert `2`. This is what proves requirement 1's "increment on the success path only".

Assert equality of `TaskListBody` values with `Equal`, and byte equality with `Equal` on the `[]byte` (Gomega compares byte slices element-wise).

### 10. Coverage and self-check

`pkg/board` must keep `>= 80 %` statement coverage. Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 1, 2, 3 and 5 and Acceptance Criteria 1 and 2 against the tests and name what establishes each. Confirm `pkg/board/vaults.go` is unchanged and `ListAssignees` still reads `b.snapshot.List`.

</requirements>

<constraints>
- The decoded JSON is unchanged: same fields, same order, same values, same `SetEscapeHTML(false)` behaviour. The identity bytes must equal what `writeJSON` produced for the same rows.
- The cache holds at most one snapshot generation's entries; a superseded generation's entries are dropped rather than retained.
- The cache key includes every query parameter that changes the body, including `upcoming_hours` and `session_live`.
- `pkg/board/vaults.go`'s `ListAssignees` keeps reading parsed rows; the snapshot does not become bytes-only.
- No new middleware layer; `pkg/handler/router.go` is untouched and keeps returning a bare router.
- `gzip.NewWriter` appears exactly once in `pkg/`, in `pkg/board`'s `gzipBody`, and zero times under `pkg/handler`.
- No configuration, no opt-out flag, no size limit, no time-to-live, no timer. `RescanInterval` and `SessionRefreshInterval` are unchanged.
- Do NOT touch `pkg/handler/api_tasks.go` (prompt 3 wires the handler), `pkg/handler/router.go`, `docs/` or `CHANGELOG.md` (prompt 4 does those).
- Do NOT change the `Board.ListTasks`, `Board.ListAssignees` or `taskSnapshotStore.List` signatures, and do NOT change what `ListTasks` returns.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/... ./pkg/handler/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
test -f pkg/board/taskbody.go && test -f pkg/board/taskbody_internal_test.go
```
Must exit 0.

```
grep -n 'func (s \*taskSnapshotStore) SnapshotGeneration() uint64' pkg/board/snapshot.go
```
Must print one line.

```
grep -c 'gzip.NewWriter' pkg/board/taskbody.go
```
Must print `1`.

```
! grep -rn 'gzip.NewWriter' pkg/handler/
```
Must exit 0.

```
grep -n 'ListTasksBody' pkg/board/board.go pkg/board/tasks.go pkg/handler/api_test.go
```
Must print at least three lines.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
