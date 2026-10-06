---
status: completed
spec: [025-lazy-pane-resolution-at-jump-time]
summary: Deleted pkg/panecache and the 3s pane refresher, dropped the pane id from the board's read model, and made the jump route resolve panes on demand via the Go resolver.
execution_id: vault-ui-lazy-pane-exec-131-spec-025-drop-pane-state-and-delete-panecache
dark-factory-version: dev
created: "2026-10-06T06:16:43Z"
queued: "2026-10-06T06:48:20Z"
started: "2026-10-06T07:44:30Z"
completed: "2026-10-06T07:52:06Z"
branch: dark-factory/131-spec-025-drop-pane-state-and-delete-panecache
---

# Drop pane state from the board and delete the pane cache

<summary>
- A task list read answers from the page index alone and carries no pane id at all.
- The board's response model loses the pane field rather than emitting it empty, so the field cannot come back by accident.
- The three-second background pane refresher is deleted, and with it every Go-to-Python shell-out.
- The jump endpoint resolves the task's pane at the moment it is clicked, through the Go resolver, and answers with a non-2xx status and a body naming the failure when no pane resolves.
- A task list read makes zero pane-resolver calls, proven by a test whose recorder also fires once on a real jump, so a broken double cannot pass it.
- No other route's response body changes.
</summary>

<objective>
Make the board hold no pane state: the list response stops carrying a pane id, the background pane cache and its refresher are deleted, and the jump endpoint resolves the pane on demand through the Go resolver built by the previous prompt. This removes the ~95 % CPU burn the three-second refresher causes and makes the pane id exist only at the one action that needs it.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-lazy-pane-resolution-at-jump-time.md`. This prompt covers Desired Behaviors 2, 3 and 4, and Acceptance Criteria AC1, AC2, AC3 and AC4.

Prerequisite: the previous prompt (`1-spec-025-go-pane-resolver.md`) added `pkg/pane/resolver.go` with `pane.Resolver`, `pane.NewResolver(pane.ResolverParams{...})`, and `factory.CreatePaneResolver(homeDir) pane.Resolver`, and rewired both `CreatePaneRefresher` and `CreateMutationService` onto it. Read `pkg/pane/resolver.go` and `pkg/factory/api.go` first and use the real signatures you find there.

Read these files before writing (current shapes verified):

- `pkg/panecache/panecache.go` — the package you are deleting: `DefaultRefreshInterval` (3 s), `Resolver`, `Cache`, `NewCache`, `RefreshParams`, `Refresher`, `NewRefresher`, `Refresh`, `RunLoop`. Its `Resolver` interface is structural and does not import `pkg/pane`.
- `pkg/factory/api.go` — `CreatePaneCache()`, `CreatePaneRefresher(homeDir string, cache panecache.Cache) run.Func`, `liveSessionIDs(ctx, homeDir)`, and `CreateAPIHandler` (its fourth parameter is `paneCache panecache.Cache`, passed as `board.Deps.Pane`).
- `pkg/factory/mutations.go` — `CreateMutationService(loader, configPath, cache, launches, locks, homeDir, publisher, index)`; its `Deps.Pane` is built inline.
- `main.go` — `paneCache := factory.CreatePaneCache()`, the `CreateAPIHandler` call, and the `run.CancelOnFirstErrorWait` list containing `factory.CreatePaneRefresher(homeDir, paneCache)`.
- `pkg/board/board.go` — `PaneResolver` interface, `Deps.Pane`, the `pane` field on `board`.
- `pkg/board/tasks.go` — `resolvePanes(ctx, rows) map[string]string` (called from `tasksForVault`), the `paneMap` parameter of `taskResponse`, and the `jumpPane` block that sets `api.TaskResponse.JumpPane`. `resolvePanes` is the only user of the `sort` import in this file.
- `pkg/api/api.go` — `TaskResponse.JumpPane *string \`json:"jump_pane"\`` (line ~58). `GoalResponse` has no pane field.
- `pkg/board/topics.go` and `pkg/api/api.go` — `TopicDetailResponse.Tasks` is `[]string` of task ids, NOT nested task responses, so the topic detail route carries no pane field and must not change.
- `pkg/mutations/tasks.go` — `JumpTask`: it checks `sameOrigin`, resolves the vault, `Show`s the task, rejects an empty `ClaudeSessionID` with 409, then calls `s.deps.Pane.Resolve(ctx, sessionID)`; a `false` returns `newHTTPError(409, "no pane resolves for this session")`. Then `s.deps.Jump.ReadToken()` (503 when unreadable) and `s.deps.Jump.Perform` (502 on failure). This ordering matters to the AC3 test below: the pane resolver is called before the credential is read.
- `pkg/factory/api_test.go` — `newTestAPIHandler` / `newTestAPIHandlerWithManager` / `apiFixture`; `tempDir` lives in `pkg/factory/factory_test.go` and `writeFile` in `pkg/factory/ops_test.go` (all `package factory_test`).
- `pkg/factory/pageindex_test.go` — `indexHandler`, a second `CreateAPIHandler` call site.

Coding guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`
</context>

<requirements>

### 1. Delete `pkg/panecache`

Delete `pkg/panecache/panecache.go`, `pkg/panecache/panecache_test.go` and `pkg/panecache/panecache_suite_test.go`, and the directory with them. Nothing replaces them: there is no periodic pane work of any interval.

### 2. Remove the pane cache from the composition root (`pkg/factory/api.go`)

- Delete `CreatePaneCache`, `CreatePaneRefresher` and `liveSessionIDs`.
- Delete the `panecache` import. (The comment naming `who-needs-me.py` was already removed by prompt 1; delete it only if it is still present.)
- Change `CreateAPIHandler`'s fourth parameter from `paneCache panecache.Cache` to `paneResolver mutations.PaneResolver`, keeping its position in the parameter list. Drop `Pane: paneCache` from the `board.Deps` literal and pass the new parameter to `CreateMutationService`. Add the `github.com/bborbe/vault-ui/pkg/mutations` import.

### 3. Resolve the jump on demand (`pkg/factory/mutations.go`)

- Add a `paneResolver mutations.PaneResolver` parameter to `CreateMutationService`, directly after `homeDir`, and use it as `Deps.Pane`, replacing the `CreatePaneResolver(homeDir)` call prompt 1 left there. Do not change any other parameter or the `paneJumpClient` / `Jump` wiring.
- `CreateMutationService`'s `Create*` body stays pure assembly — no business logic.

### 4. Wire `main.go`

- Delete `paneCache := factory.CreatePaneCache()`.
- Add `paneResolver := factory.CreatePaneResolver(homeDir)` and pass it as the fourth argument of `factory.CreateAPIHandler`.
- Delete `factory.CreatePaneRefresher(homeDir, paneCache)` from the `run.CancelOnFirstErrorWait` list. Keep every other entry and its order.

### 5. Drop the pane field from the board

- `pkg/board/board.go`: delete the `PaneResolver` interface, the `Pane` field of `Deps`, and the `pane` field of `board` (and its assignment in `New`).
- `pkg/board/tasks.go`: delete `resolvePanes`; delete the `paneMap` parameter of `taskResponse` and its argument at the call site; delete the `jumpPane` block; delete the `JumpPane:` field from the returned `api.TaskResponse` literal. Remove the now-unused `sort` import.
- `pkg/api/api.go`: delete `JumpPane *string \`json:"jump_pane"\`` from `TaskResponse`. The response model loses the field; it is not emitted as an empty value.
- The board's `SessionSignals` usage (`RegistrySessionIDs`, `ResumeSessionIDs`) is unchanged in this prompt — the per-request `ps` scan stays.

### 6. Keep the existing tests compiling and honest

- `pkg/board/board_test.go`: delete `fakePane`, `recordingPane`, the `pane` field of `harness`, every `h.pane.panes[...]` write, and the `Pane:` entry from **every** `board.Deps{...}` literal in the file (there are four). Delete every assertion on `responses[0].JumpPane`, and delete the whole `It("resolves panes through the injected resolver and spawns nothing", ...)` block — its premise is now false and it references the deleted `recordingPane` and its `pane.calls`. Remove imports that become unused (`sync` among them). Also delete the `pane := fakePane{...}` local and `pane: pane` in `newHarness`, and rename the surviving It "classifies a session from the registry and resolves its pane" to "classifies a session from the registry" (it keeps its `session_state` assertions).
- `pkg/factory/api_test.go` and `pkg/factory/pageindex_test.go`: update every `CreateAPIHandler` call site to pass `factory.CreatePaneResolver(tempDir())` (or the home dir the helper already uses) in the fourth position.
- `pkg/mutations/service_test.go` and its `fakePane` stay unchanged — the `PaneResolver` interface it fakes is unchanged.

### 7. AC3 — a list read resolves no panes, and the recorder fires on a real jump

Add this as Ginkgo specs in `pkg/factory` (external package `factory_test`), in a new file.

Hand-write a recording double (a struct of function fields is fine; a nil field panics):

```go
// recordingPaneResolver records every session id handed to Resolve, so a spec
// can prove a list read never resolves a pane and a jump does.
type recordingPaneResolver struct {
	panes map[string]string
	calls []string
}

func (r *recordingPaneResolver) Resolve(_ context.Context, sessionID string) (string, bool) {
	r.calls = append(r.calls, sessionID)
	pane, ok := r.panes[sessionID]
	return pane, ok
}
```

Do NOT add a `//counterfeiter:generate` directive — this repo has no `make generate` target and counterfeiter is not a module dependency (the documented deviation in `prompts/completed/126-background-pane-resolution.md`).

Fixture: a temp vault with `24 Tasks/LiveTask.md` whose frontmatter carries a fixed `claude_session_id: 11111111-1111-1111-1111-111111111111` and `status: in_progress`, plus a `23 Goals/Goal A.md`. Also write `<home>/.claude/sessions/1.json` containing `{"sessionId":"11111111-1111-1111-1111-111111111111","name":"LiveTask"}`, where `<home>` is the home directory you pass to `CreateAPIHandler`, so the task classifies `live` (`activity.ReadRegistrySessionIDs` reads the `sessionId` key; create the directory with `os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0750)` first, since `writeFile` does not — and create `24 Tasks` and `23 Goals` with `os.MkdirAll(..., 0750)` first too, as `apiFixture` does) — without it `ClassifySessionState` returns `indeterminate`, the pre-change board would also make 0 resolver calls, and the 0-call assertion would pass against the unmodified build. Assert the task's `session_state` is `live` in the response as a fixture guard. Build the handler exactly as production does, over a `factory.CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime())` that you warm with `factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(ctx)` before the requests, and with the recording resolver in the fourth position. Give the resolver `map[string]string{"11111111-1111-1111-1111-111111111111": "7"}`.

- Issue `GET /api/tasks?vault=personal` against the handler with `httptest`. Assert 200 and `Expect(recorder.calls).To(BeEmpty())` — **exactly 0 calls** across the list read.
- Positive control in the same file: issue `POST /api/tasks/LiveTask/jump?vault=personal` with `httptest.NewRequest` (no `Origin` header, so the same-origin check passes). Assert `Expect(recorder.calls).To(HaveLen(1))` and `Expect(recorder.calls[0]).To(Equal("11111111-1111-1111-1111-111111111111"))`. Do **not** assert the response status: `JumpTask` resolves the pane before it reads the jump credential, so with no credential under the temp home dir the answer is 503 — the recorder is the evidence, not the status code.

### 8. AC4 — no list response carries a pane-bearing key

Add a second spec in the same new file. Build the handler with `newTestAPIHandler` over `apiFixture()`.

- `GET /api/tasks?vault=personal` and `GET /api/goals?vault=personal`; assert each is 200.
- Decode the body into `[]map[string]any` and assert `Expect(elements).NotTo(BeEmpty())` so the spec cannot pass vacuously.
- For every element, for every key: `Expect(strings.Contains(strings.ToLower(key), "pane")).To(BeFalse())`. The assertion must be over the marshalled **key set**, never over the literal `jump_pane` — so renaming the field cannot pass it. This spec fails against the pre-change build and passes after. Do not name `jump_pane` anywhere in the new file, not even in a comment: the `<verification>` grep for that literal across `pkg` must stay green.
- Run the same assertion for `/api/goals` as well as `/api/tasks`.

### 9. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes, then walk AC1, AC2, AC3 and AC4 and name the test or command output that establishes each. Confirm the AC3 recorder count is 0 for the list read and 1 for the jump — a double that never fires would make the 0 meaningless.

</requirements>

<constraints>
- Response bodies, status codes, routes, query parameters and WebSocket frame content of every route **other than** `/api/tasks` stay byte-identical to the Python reference (spec 023 parity contract). The `/api/tasks` change is the intentional drop of the pane field, re-baselined in a later prompt — do not widen it.
- `pkg/board` must keep resolving no panes: after this change the board has no pane dependency at all.
- The per-request `ps` scan on the request path (`pkg/board/tasks.go` via `sessionSignals.ResumeSessionIDs`) stays — moving it off the request path is the sibling task's job, not this one.
- No new external service dependency, no new HTTP route, no new query parameter, no opt-out flag, no configurable interval.
- No change to write semantics, to the write queue, or to the jump route's error contract (409 for no session, 409 for no resolvable pane, 503 for an unreadable credential, 502 for an unreachable jump server).
- `pkg/mutations`' `PaneResolver` and `JumpClient` interfaces and `JumpTask`'s ordering stay as they are.
- Nothing in the request path holds shared mutable pane state: two jump requests for the same session must each resolve independently and neither may corrupt the other. The deleted cache was the only such state — do not introduce a replacement.
- The jump endpoint resolves the caller's own task and enforces same-origin, exactly as today; this change does not widen it. Pane ids are resolved server-side and are never accepted from request input, and no token appears in any response body.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no `fmt.Errorf`, `Create*` factories carry no business logic, Ginkgo/Gomega tests, `glog` for logging.
- Concurrency goes through `github.com/bborbe/run`; a bare `go func()` is a violation.
- Hand-write test doubles; do not add a counterfeiter directive.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- All paths in this prompt and in the code are repo-relative.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/board/... ./pkg/mutations/... ./pkg/factory/...
```
Must pass.

```
test ! -d pkg/panecache
```
Must exit 0 (the directory is gone). Prefer this over `ls pkg/panecache`, whose non-zero exit is easy to misread.

```
! grep -rn --include='*.go' 'panecache' .
```
Must exit 0.

```
! grep -rn --include='*.go' 'who-needs-me' pkg
```
Must exit 0 (AC1, second half).

```
! grep -rn --include='*.go' 'python3' pkg
```
Must exit 0 (AC1, first half).

```
! grep -rn --include='*.go' 'jump_pane\|JumpPane' pkg
```
Must exit 0 — no Go code names the dropped field any more.

Positive control for the AC3 spec: run `go test -count=1 -v ./pkg/factory/... -run TestSuite -ginkgo.v -ginkgo.no-color -ginkgo.focus='pane' 2>&1 | grep -i 'pane'` and record what it prints — it must print the AC3 and AC4 spec names (spec names need BOTH flags: `-ginkgo.v` makes Ginkgo print them, and `-v` stops `go test` hiding a passing package's stdout — drop either and the grep matches nothing; `-count=1` stops a cached run replaying old output). Name the AC4 spec so its text also contains "pane" (e.g. "carries no pane-bearing key in any list response")., so the 0-call assertion is visibly attached to a spec that ran. `pkg/factory`'s Ginkgo entry point is `TestSuite` (there is no `TestFactory`), so `-run TestFactory` would run nothing and the control would be vacuous. Name the AC3 spec so its text contains "pane" (e.g. `"resolves no panes on a list read"`), otherwise the `grep -i 'pane'` has nothing to match even when the suite runs.
</verification>
