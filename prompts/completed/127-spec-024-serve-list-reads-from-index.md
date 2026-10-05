---
status: completed
spec: [024-serve-list-reads-from-page-index]
summary: Board list reads now serve from one process-wide pageindex.PageIndex built concurrently at startup and rescanned every 50s, with a factory-level test proving 25 warm requests cause zero additional page-storage reads and parity unchanged.
execution_id: vault-ui-page-index-exec-127-spec-024-serve-list-reads-from-index
dark-factory-version: v0.196.0
created: "2026-10-05T19:39:53Z"
queued: "2026-10-05T20:18:36Z"
started: "2026-10-05T20:26:11Z"
completed: "2026-10-05T20:31:50Z"
branch: dark-factory/127-spec-024-serve-list-reads-from-index
---

# Serve every board list read from the process-wide page index

<summary>
- The board's task, assignee, goal and topic lists, and the goal and task lists inside a topic's detail view, now read vault pages from the in-memory index instead of re-parsing every file on each request.
- One index is shared by the whole process, so all of those endpoints share the same snapshots.
- At startup every configured vault's task, goal and topic folders are loaded concurrently; a request that arrives before that finishes waits for the same load instead of starting a second one.
- A background safety-net rescan runs alongside the service, refreshing every folder within a minute.
- Responses stay byte-identical: vault-cli's list logic still produces them, and the parity harness against the Python backend still passes.
- A test proves that once warm, 25 list requests across two vaults cause zero additional file-folder reads.
- Change notifications and write invalidation are not wired yet; until the next two prompts land, external edits appear via the rescan and writes via the rescan as well.
</summary>

<objective>
Make `pkg/factory`'s board ops provider build `ops.NewListOperation(<index>)` over one process-wide `pageindex.PageIndex`, start the index's concurrent startup build and its rescan loop from `main.go`, and prove with a factory-level test that warm list reads never call the underlying page storage. This removes the per-request vault re-parse that makes `/api/tasks` and `/api/assignees` take 4–9 s.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/024-serve-list-reads-from-page-index.md`. This prompt covers Desired Behaviors 2 and 3 and Acceptance Criteria AC1 and AC7.

Prerequisite: prompt `1-spec-024-page-index-package.md` created `pkg/pageindex` with:
- `type Key struct{ VaultPath, PagesDir string }`, `func NewKey(vaultPath, pagesDir string) Key`
- `type PageIndex interface { storage.PageStorage; Build(ctx, keys []Key) error; Refresh(ctx, key Key) error; MarkDirty(keys ...Key); MarkAllDirty(); Rescan(ctx) error }`
- `func NewPageIndex(pageStorage storage.PageStorage, currentDateTimeGetter libtime.CurrentDateTimeGetter, waiter libtime.WaiterDuration) PageIndex`
Read `pkg/pageindex/pageindex.go` and `docs/page-index.md` first and use the real signatures you find there.

Read these files (current shapes verified):
- `pkg/factory/api.go`:
  - `type opsProvider struct{}` with `func (opsProvider) List(vault board.Vault) ops.ListOperation { return ops.NewListOperation(storage.NewPageStorage(vault.StorageConfig())) }` and `TopicShow(vault board.Vault) ops.EntityShowOperation` (TopicShow stays on disk — out of scope).
  - `func CreateAPIHandler(loader config.Loader, configPath string, cache statuscache.Cache, launches launchregistry.Registry, homeDir string, readiness vaultui.Readiness, manager websocket.ConnectionManager) http.Handler` — builds `board.New(board.Deps{... Ops: opsProvider{} ...})`.
  - `func CreateStatusCacheLoader(loader config.Loader, configPath string, cache statuscache.Cache) run.Func` — the sibling startup run func whose shape the warm-up follows (loads `vaultconfig.Load(ctx, loader, configPath)`, returns the wrapped load error).
- `pkg/factory/factory.go` — `CreateOpSet` keeps its own `storage.NewPageStorage(storageConfig)`; the mutation side keeps reading disk (spec constraint), do not change it.
- `pkg/board/board.go` — `board.Vault` (`Path`, `TasksFolder`, `GoalsFolder`, `TopicsFolder`), `OpsProvider` interface.
- `pkg/board/vaults.go`, `tasks.go`, `goals.go`, `topics.go` — every list read is `b.ops.List(vault).Execute(ctx, vault.Path, vault.Name, <folder>, nil, true, "", "")`; `ListTopics` skips a vault with empty `TopicsFolder`; `ShowTopic` lists goals and tasks.
- `pkg/vaultconfig/vaultconfig.go` — `Load` and `Vault` (`Path`, `TasksFolder`, `GoalsFolder`, `TopicsFolder`); `BuildVaultConfig` skips a vault whose tasks folder is not a directory on disk.
- `main.go` — `execute` builds `apiHandler` via `factory.CreateAPIHandler(...)` and runs `run.CancelOnFirstErrorWait(ctx, CreateVaultDiscovery, CreateStatusCacheLoader, CreateWatcher, CreateHTTPServer, CreateAPIServer)`. It is the only binary entry point (no `cmd/`).
- `pkg/factory/api_test.go` — `newTestAPIHandler` / `newTestAPIHandlerWithManager` / `apiFixture` (the helpers every API factory test uses; they must keep compiling).
- vault-cli `/home/node/go/pkg/mod/github.com/bborbe/vault-cli@v0.159.0/pkg/storage/page.go` (if the path is absent, run `go mod download github.com/bborbe/vault-cli` first) — `ListPages` uses only its arguments, never the storage config, so one shared `storage.NewPageStorage(nil)` behind the index is byte-identical to today's per-vault instances.
- vault-cli mocks `github.com/bborbe/vault-cli/mocks`: `mocks.PageStorage` (`ListPagesStub`, `ListPagesCallCount`, `ListPagesArgsForCall`), `mocks.Loader` (`GetAllVaultsReturns`, `GetCurrentUserReturns`).
- `scripts/parity/parity.sh` — read-route parity runs against the pristine fixture before any mutation case.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — `Create*` functions are pure composition.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md` — create `libtime.NewCurrentDateTime()` in `main.go`, inject it into factories.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. Factory functions for the index (`pkg/factory/pageindex.go`, new file)

Put all index construction in a new file so `pkg/factory/api.go` no longer mentions `NewPageStorage` at all:

```go
// CreatePageIndex returns the process-wide page index over pageStorage.
func CreatePageIndex(
	pageStorage storage.PageStorage,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
) pageindex.PageIndex
```
Body: `pageindex.NewPageIndex(pageStorage, currentDateTimeGetter, libtime.NewWaiterDuration())` — nothing else.

```go
// CreatePageIndexWarmup returns a run.Func that builds every configured vault's
// tasks, goals and topics folders concurrently once at startup.
func CreatePageIndexWarmup(
	loader config.Loader,
	configPath string,
	pageIndex pageindex.PageIndex,
) run.Func
```
The closure loads `vaultconfig.Load(ctx, loader, configPath)` (wrap and return its error, exactly like `CreateStatusCacheLoader`), derives the keys with a small unexported helper `pageIndexKeys(vaults []vaultconfig.Vault) []pageindex.Key` — for each vault `NewKey(Path, TasksFolder)`, `NewKey(Path, GoalsFolder)`, and `NewKey(Path, TopicsFolder)` only when `TopicsFolder != ""` — and calls `pageIndex.Build(ctx, keys)`. If `Build` returns an error and `ctx.Err() != nil`, return nil (shutdown is not a failure, matching how `CreateWatcher` returns nil on cancel); otherwise return `errors.Wrap(ctx, err, "build page index")`. The `vaultconfig.Load` error is always returned wrapped, cancelled or not. A key that fails to build is logged by the index and must not stop the service (the index already guarantees this — `Build` does not fail on per-key errors).

### 2. Read through the index (`pkg/factory/api.go`)

- Change `opsProvider` to carry the index: `type opsProvider struct{ pageIndex pageindex.PageIndex }` and `List` returns `ops.NewListOperation(p.pageIndex)`. `TopicShow` is unchanged.
- Add a trailing parameter `pageIndex pageindex.PageIndex` to `CreateAPIHandler` and pass `opsProvider{pageIndex: pageIndex}` into `board.Deps`. Do not change any other parameter or the mutation-service wiring in this prompt.
- After the change, `pkg/factory/api.go` must contain no `NewPageStorage` call.

### 3. Wire `main.go`

In `execute`:
- `pageIndex := factory.CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime())` — add a one-line comment that `ListPages` ignores the storage config, so one shared instance serves every vault. Imports: `github.com/bborbe/vault-cli/pkg/storage`, `libtime "github.com/bborbe/time"`.
- Pass `pageIndex` as the new last argument of `factory.CreateAPIHandler`.
- Add `factory.CreatePageIndexWarmup(loader, configPath, pageIndex)` and `pageIndex.Rescan` (a method value is a `run.Func`) to the `run.CancelOnFirstErrorWait` list. Keep the existing entries and their order; put the two new entries after `CreateStatusCacheLoader`.

`CreateWatcher`'s signature is unchanged in this prompt (prompt 3 changes it).

### 4. Keep existing tests compiling

Update `pkg/factory/api_test.go` helpers so every existing call to `CreateAPIHandler` passes an index built with `factory.CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime())` (a fresh index per helper call, so tests stay isolated). Existing assertions must not change.

### 5. AC1 factory-level test (new `It`s in `pkg/factory`, external package `factory_test`)

Build the full API handler from `pkg/factory` exactly as production does, but with the index's underlying storage replaced by a counting fake:
- Fixture: two temp vaults (names e.g. `alpha` and `beta`), each with tasks, goals and topics folders on disk containing at least one task, one goal and one topic file (real files — `vaultconfig.BuildVaultConfig` stats the tasks folder, and `/api/topics/{id}` reads the topic file through `TopicShow`, which stays on disk). `mocks.Loader.GetAllVaultsReturns` lists both vaults with `TasksDir`, `GoalsDir`, `TopicsDir`; `GetCurrentUserReturns` a user; a vault-ui `config.yaml` like `apiFixture`'s.
- Counting fake: `fake := &mocks.PageStorage{}` with `fake.ListPagesStub` delegating to `storage.NewPageStorage(nil).ListPages` (so responses are real), counting per `(vaultPath, pagesDir)` via `ListPagesArgsForCall`.
- `pageIndex := factory.CreatePageIndex(fake, libtime.NewCurrentDateTime())`; handler via `factory.CreateAPIHandler(..., pageIndex)` with a ready readiness gate; warm with `factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(ctx)`.
- Positive control: after warm-up, the fake recorded ≥ 1 call for each of the six `(vaultPath, folder)` pairs.
- Then issue 5 requests each to `/api/tasks`, `/api/assignees`, `/api/goals`, `/api/topics`, and `/api/topics/{id}?vault=<name>` for an existing topic (use `httptest` against the handler); each must return 200 with a body naming the fixture items.
- Evidence: the fake's total `ListPagesCallCount()` did not change across those 25 requests (exactly 0 additional calls).

Also add a unit-level test for `CreatePageIndexWarmup`: a config load failure returns an error; a cancelled `ctx` returns nil; topics keys are skipped for a vault with no topics dir (assert via the fake's recorded args).

### 6. CHANGELOG

Under `## Unreleased` (created by prompt 1; create it above the newest `## vX.Y.Z` heading if absent) add a `feat:` bullet stating that list reads (`/api/tasks`, `/api/assignees`, `/api/goals`, `/api/topics`, and the lists inside `/api/topics/{id}`) are now served from an in-memory page index built at startup and rescanned at least every minute, instead of re-parsing every vault file per request.

### 7. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk AC1 and AC7 and name the test or command output that establishes each. If `make parity` reports a body mismatch on any read route, the index is not byte-identical — find the cause (e.g. a key derived differently from the folder the board passes); never weaken `scripts/parity/`.

</requirements>

<constraints>
- Response bodies, status codes, routes, query parameters, and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set is unchanged.
- vault-cli `ops.ListOperation` and its filter/sort/blocked logic stay the only list logic; vault-ui must not parse page files itself. vault-cli stays pinned at `v0.159.0` via `require`, never `replace`.
- `ShowTopic`'s own topic-file read (`TopicShow`) stays on disk; only list reads go through the index.
- `pkg/mutations` reads and `pkg/cleanup` keep reading disk directly — do not route `CreateOpSet`'s list operation through the index.
- Snapshots are never mutated after publication; `go test -race` must stay clean.
- Index keys come from server config, never from request input.
- No new HTTP route, query parameter, opt-out flag or configurable interval; no persistence of the index to disk.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` injected (created in `main.go`).
- Do NOT modify anything under `src/vault_ui/` or weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/pageindex/... ./pkg/board/... ./pkg/factory/...
```
Must pass.

```
make parity
```
Must exit 0 and print the full summary with every parity line at full ratio. Run `make precommit` first so the container has a Linux `.venv`. If it dies before printing the comparison (e.g. `Error 127` in `build_vault_cli`), that is an environment failure, not a pass — fix `PATH` and re-run.

```
! grep -n 'NewPageStorage' pkg/factory/api.go
```
Must exit 0 (no per-request page storage left in the board's ops provider).
</verification>
