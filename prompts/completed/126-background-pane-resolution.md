---
status: completed
summary: Moved WezTerm pane resolution off the request path into a new pkg/panecache background refresher; the board now reads pane ids from an in-memory cache refreshed every 3s, and GET /api/tasks spawns no subprocess.
execution_id: vault-ui-exec-126-background-pane-resolution
dark-factory-version: v0.196.0
created: "2026-10-05T19:45:39Z"
queued: "2026-10-05T19:45:39Z"
started: "2026-10-05T19:48:04Z"
completed: "2026-10-05T19:53:58Z"
branch: dark-factory/126-background-pane-resolution
---

# Resolve panes in a background refresher, off the request path

<summary>
- Serving the task board no longer starts a helper process
- Pane links are read from an in-memory cache instead of being resolved on demand
- A background refresher keeps that cache current for every live session
- A session's pane link is at most one refresh interval out of date
- The board's response shape and the frozen frontend are unchanged (a session live by transcript recency alone — no registry entry, no live process — no longer attempts a pane lookup; it had no resolvable pane in practice)
</summary>

<objective>
Move WezTerm pane resolution off the request path into a background refresher, so GET /api/tasks stops paying one `who-needs-me` helper subprocess per live session per request (measured 7.7s on 2026-10-05), while pane links keep resolving correctly from a cache refreshed every three seconds.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these before writing:

- `pkg/board/board.go` — the `PaneResolver` interface (`Resolve(ctx, sessionID) (string, bool)`) and the `Deps.Pane` field the board resolves through.
- `pkg/board/tasks.go` — `resolvePanes`: it collects the distinct live session ids off the rows, sorts them, then calls `b.pane.Resolve` once per id. That call is the request-path subprocess.
- `pkg/factory/api.go` — `paneResolver` (the current `Deps.Pane` implementation; its `Resolve` spawns `who-needs-me.py --pane-for <sid8>` through `pane.ResolvePaneID`), `CreateWatcher` (the `run.Func` factory shape), `sessionSignals` (the live-id source to mirror), and `CreateAPIHandler` (the composition root that builds `board.New`).
- `pkg/cleanup/cleanup.go` — `RunLoop`: the in-repo periodic-loop shape. Run once immediately, then a `time.Timer` plus a `select` on `ctx.Done()`; `timer.Stop()` and return nil when the context is cancelled.
- `pkg/statuscache/` — the in-repo in-memory cache shape (constructor plus a mutex-guarded map).
- `pkg/pane/pane.go` — `ResolvePaneID`, `WhoNeedsMePath`, `BuildSubprocessEnv`, `DefaultResolveTimeout`. Keep this package. Two callers remain after this lands: the new refresher, and `CreateMutationService`'s `paneResolver` (the jump route's on-demand resolution — out of scope here).
- `pkg/factory/mutations.go` and `pkg/mutations/tasks.go` — the second `paneResolver` composition root and `JumpTask`, which resolves on demand. Read them to see what you must leave alone.
- `main.go` — `run.CancelOnFirstErrorWait(ctx, ...)` is where every background `run.Func` is launched.
- `docs/liveness-classification.md` — the session liveness contract. Note that the refresher's live-id set is the registry ∪ live-process set (mirroring `sessionSignals`), which is narrower than the board's `ClassifySessionState` live set: a session that is live by transcript recency alone is not refreshed.

The refresher is concurrency plus cancellation logic. The coding plugin docs are available in the container at `/home/node/.claude/plugins/marketplaces/coding/docs/` — read `go-concurrency-patterns.md` and `go-context-cancellation-in-loops.md` before writing the fan-out and the loop.
</context>

<requirements>
1. Add package `pkg/panecache`.

2. Declare the resolver seam in it:

   ```go
   // Resolver resolves a live session id to its WezTerm pane id.
   type Resolver interface {
       Resolve(ctx context.Context, sessionID string) (string, bool)
   }
   ```

   Do not import `pkg/board` — the cache satisfies `board.PaneResolver` structurally.

   Do not add a `//counterfeiter:generate` directive: this repo has no `make generate` target and counterfeiter is not a module dependency, so a directive would leave the fake ungenerated and break the test build. Hand-write the test double in the test file instead, following the existing hand-written doubles — `pkg/board/board_test.go`'s `fakePane` (a hand-written double for this very `PaneResolver` interface) and `pkg/handler/fake_mutations_test.go`: a struct of function fields, so a test can assert the exact arguments passed and a nil field panics on an unexpected call. This is a deliberate, documented deviation from the `counterfeiter-mocks-required` coding rule; wiring counterfeiter's toolchain into the repo is out of scope for this prompt.

3. Implement the cache:

   ```go
   type Cache interface {
       // Resolve returns the cached pane id for a session id.
       Resolve(ctx context.Context, sessionID string) (string, bool)
       // Replace atomically swaps the whole resolved map.
       Replace(resolved map[string]string)
   }
   func NewCache() Cache
   ```

   Back it with a mutex-guarded `map[string]string`. **`Resolve` must be a pure map lookup and must never call the injected `Resolver` or spawn a process** — that property is what the request path's no-subprocess guarantee rests on.

4. Implement the refresher in the same package:

   ```go
   const DefaultRefreshInterval = 3 * time.Second

   type RefreshParams struct {
       Cache          Cache
       Resolver       Resolver
       LiveSessionIDs func(ctx context.Context) []string
       Interval       time.Duration
   }

   type Refresher interface {
       // Refresh runs one pass and returns the number of panes resolved.
       Refresh(ctx context.Context) (int, error)
       // RunLoop runs Refresh once, then every Interval until ctx is cancelled.
       RunLoop(ctx context.Context) error
   }
   func NewRefresher(params RefreshParams) Refresher
   ```

   One `Refresh` pass: collect the live session ids, dedupe and sort them, build one `run.Func` per id, and fan them out with `run.All(ctx, funcs...)`. Collect the successful resolutions into a fresh map and hand it to `Cache.Replace`. A single session failing to resolve is logged and skipped — it must never abort the pass or empty the cache for the other sessions. A cancelled context returns without replacing the cache.

   `RunLoop` mirrors `cleanup.RunLoop`: run once immediately, then a `time.Timer` for `Interval` with a `select` on `ctx.Done()`; `timer.Stop()` and return nil on cancel. A pass error is logged and the loop continues.

   Concurrency must go through `github.com/bborbe/run` — no raw `go func()`. Each per-session `run.Func` checks `ctx.Err()` before calling the resolver.

5. Wire it in `pkg/factory/api.go`:

   - Add `CreatePaneCache() panecache.Cache` returning `panecache.NewCache()`.
   - Add `CreatePaneRefresher(homeDir string, cache panecache.Cache) run.Func`, returning a `run.Func` that calls `RunLoop`. Its `LiveSessionIDs` mirrors the existing `sessionSignals`: the deduped union of `activity.ReadRegistrySessionIDs(ctx, filepath.Join(homeDir, ".claude", "sessions"))` and `session.ParseLiveSessionIDs(session.NewPSScanner("-axww", "-o", "args=")(ctx))`; a ps-scanner error yields an empty resume-id list, mirroring `sessionSignals.ResumeSessionIDs`. Its `Resolver` is the existing `paneResolver` value. Keep the closure thin — the union and dedupe belong in a package-level helper, not in the `Create*` factory.
   - Change `CreateAPIHandler` to take the cache and pass it as `board.Deps.Pane` instead of constructing `paneResolver` inline. Keep `paneResolver` — the refresher and `CreateMutationService` are its callers now. Leave `CreateMutationService` (`pkg/factory/mutations.go`) and `pkg/mutations`' `JumpTask` unchanged: the jump route keeps resolving on demand and is out of scope for this change. Update the `CreateAPIHandler` test callers in `pkg/factory/api_test.go` (`newTestAPIHandlerWithManager`, and `newTestAPIHandler` through it) to pass the new cache argument, so the factory suite still compiles.

6. In `main.go`, construct the cache before `CreateAPIHandler`, pass it to both the handler and `CreatePaneRefresher`, and add `factory.CreatePaneRefresher(...)` to the `run.CancelOnFirstErrorWait` list alongside `CreateWatcher`.

7. In `pkg/board/tasks.go`, leave `resolvePanes` in place and unchanged in shape. It already resolves through the injected `Deps.Pane`; once that is the cache, the per-request subprocess is gone. Do not change any response value or the frozen frontend's contract.

8. Tests, using the repo's Ginkgo/Gomega conventions:

   - `panecache` unit tests: `Resolve` returns a stored pane id and `false` for an unknown session id; `Resolve` does **not** call the injected resolver (assert the fake records zero calls) — this is the regression lock for the request path; `Replace` swaps the map so a stale entry disappears; a pass where one session's resolve returns `false` still caches the others; `RunLoop` returns nil when its context is cancelled.
   - A `Refresh` test asserting the session ids are handed to the resolver as given (the subprocess argument boundary).
   - A board-level test injecting a `PaneResolver` that records its calls, asserting `ListTasks` resolves through it and spawns nothing.
   - Do not spawn a real subprocess in any test; use the hand-written fakes.

   `pkg/panecache` must reach at least 80% statement coverage.

9. Add a `## Unreleased` entry to `CHANGELOG.md` describing the change in the file's existing style.

10. Before finishing, re-run `<verification>` and confirm it passes, then walk each acceptance criterion above against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Concurrency must use `github.com/bborbe/run`; a raw `go func()` is a violation.
- Wrap errors with `github.com/bborbe/errors`. No `fmt.Errorf`.
- A `Create*` factory function carries no business logic.
- Use `glog` for logging, matching `pkg/cleanup`.
- Do not delete `pkg/pane` or `pkg/board`'s `resolvePanes`; do not change any HTTP response value.
- All paths in this prompt and in the code are repo-relative.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
