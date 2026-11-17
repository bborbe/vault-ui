---
status: approved
created: "2026-11-17T13:04:18Z"
queued: "2026-11-17T13:04:18Z"
completed: "2026-11-17T13:05:01Z"
lastFailReason: 'setup workflow: working tree is not clean; cannot switch to branch "dark-factory/186-pprof-admin-listener"; uncommitted changes: specs/in-progress/039-mode-pill.md'
---

# Mount Go pprof on the admin listener

<summary>

- The admin listener serves the standard Go profiling endpoints
- An operator can fetch a heap profile from a running vault-ui to diagnose the large heap seen in the page index
- Goroutine, allocation, block, mutex and thread-creation profiles are available the same way
- CPU profile, execution trace, command line and symbol lookup are available too
- The index page at the profiling root lists every available profile
- The public UI listener is untouched and does not expose any profiling endpoint
- Existing admin endpoints (health, readiness, metrics, log level, GC) behave exactly as before
- The changelog records the new capability under an Unreleased section

</summary>

<objective>
Expose Go pprof on vault-ui's admin HTTP router so a heap profile can be captured from a live process. This is needed to diagnose the roughly 1.5 GB `heap_alloc` observed in the page index (`pkg/pageindex`) before a larger refactor of that package is planned. The endpoints live on the admin listener only, which `main.go` binds to `:9090` and which is not the public UI listener.
</objective>

<context>
Read `CLAUDE.md` for project conventions (if present) and `docs/dod.md` for the Definition of Done (errors wrapped with `github.com/bborbe/errors`, `CHANGELOG.md` entry under `## Unreleased`, factory `Create*` functions carry no business logic).

Files to read before editing:
- `pkg/factory/factory.go` — `CreateHTTPServer(listen string, readiness vaultui.Readiness) run.Func`. It builds a `*mux.Router` (`github.com/gorilla/mux`) with `router.Path("/healthz")`, `/readiness`, `/metrics`, `/setloglevel/{level}`, `/gc`, then returns `libhttp.NewServer(listen, router).Run(ctx)`. This is the only place to change production code.
- `pkg/factory/factory_test.go` — the `Describe("CreateHTTPServer", ...)` block, in particular `DescribeTable("serves the canonical admin block", ...)` inside `Context("once the service is ready", ...)`. A package-level `BeforeEach` starts a real server via `factory.CreateHTTPServer(addr, readiness)` on a free loopback port and sets `baseURL`; the helper `statusCode(url string) int` performs a GET and returns the status code.
- `pkg/factory/api.go` — `CreateAPIServer(listen string, apiHandler http.Handler)` and `CreateAPIHandler(...)` build the public UI side. `CreateAPIHandler` takes many collaborators, so there is no cheap public-router constructor for a negative test.
- `main.go` — `const adminListen = ":9090"` is passed to `factory.CreateHTTPServer`; the public UI uses `CreateAPIServer`. Neither file needs changes.
- `CHANGELOG.md` — top section is `## v0.110.0`; there is no `## Unreleased` section.

Pattern discovery exemplars (found with rg in the repo; follow their style):
- Router registration style: `router.Path("/gc").Handler(libhttp.NewGarbageCollectorHandler())` in `pkg/factory/factory.go`.
- Ginkgo `DescribeTable` / `Entry` style: the existing "serves the canonical admin block" table in `pkg/factory/factory_test.go`.
- Error wrapping style: `github.com/bborbe/errors` (used across `pkg/`, e.g. `pkg/pane/resolver.go`, `pkg/discovery.go`). No new error paths are expected in this change.
- Changelog entry style: the `- feat:` bullets under `## v0.110.0` in `CHANGELOG.md`.
- Prior prompt exemplars for tone and structure: `prompts/completed/181-liveness-store-seen-skips-transcript-recency.md` and `prompts/completed/182-board-e2e-harness.md`.

Library fact: `libhttp.NewServer(addr string, router http.Handler, optionFns ...func(serverOptions *ServerOptions)) run.Func` from `github.com/bborbe/http` builds the server with defaults `WriteTimeout: 30 * time.Second` and `ReadTimeout: 30 * time.Second` (see `CreateServerOptions` in that module). Go's `/debug/pprof/profile` and `/debug/pprof/trace` default to `seconds=30` and reject a request whose duration is not shorter than the server `WriteTimeout`. `ServerOptions.WriteTimeout` is a `time.Duration` set in code, not parsed from flags, so stdlib `time.Duration` is correct here.
</context>

<requirements>
1. In `pkg/factory/factory.go`, import `net/http/pprof` explicitly (named import, `"net/http/pprof"`, placed with the other stdlib imports). Do NOT use a blank import (`_ "net/http/pprof"`) and do NOT rely on `http.DefaultServeMux`; handlers are registered explicitly on the existing gorilla router.

2. Add an unexported helper in `pkg/factory/factory.go`, with a short comment stating that it mounts the Go profiling endpoints on the admin router only:
   `func registerPprof(router *mux.Router)`
   It registers, using `router.Path(...)` exactly like the existing admin routes:
   - `/debug/pprof/` -> `pprof.Index` (via `HandlerFunc`)
   - `/debug/pprof/cmdline` -> `pprof.Cmdline`
   - `/debug/pprof/profile` -> `pprof.Profile`
   - `/debug/pprof/symbol` -> `pprof.Symbol`
   - `/debug/pprof/trace` -> `pprof.Trace`
   - for each name in `heap`, `goroutine`, `allocs`, `block`, `mutex`, `threadcreate`: `/debug/pprof/<name>` -> `pprof.Handler(name)` (loop over a slice of the six names).
   Use exact paths (`router.Path`), not `PathPrefix`, so nothing outside this list is served.

3. In `CreateHTTPServer`, call `registerPprof(router)` after the existing `/gc` registration and before the `glog.V(2).Infof("starting http server listen on %s", listen)` line. Do not change any existing route, handler, or the function signature.

4. Raise the admin server's write timeout so a CPU profile or trace with the default 30 second duration is not rejected: pass an option func to `libhttp.NewServer` on the final line of `CreateHTTPServer` that sets `serverOptions.WriteTimeout = 2 * time.Minute` (`time` is already imported in `factory.go`). Leave every other server option at its library default. Add a one-line comment giving the reason (pprof `profile` and `trace` run for `seconds=30` by default and must finish within `WriteTimeout`).

5. Do not touch `CreateAPIServer`, `CreateAPIHandler`, `main.go`, or the public UI router. pprof must never be reachable on the public UI listener. `pkg/factory/api.go` has no cheap public-router constructor, so do NOT add a negative test for the public router.

6. In `pkg/factory/factory_test.go`, extend the existing `DescribeTable("serves the canonical admin block", ...)` with four new `Entry` rows after the `gc` entry. The existing table body already asserts `http.StatusOK` against the real server started by `CreateHTTPServer`:
   - `Entry("pprof index", "/debug/pprof/")`
   - `Entry("pprof cmdline", "/debug/pprof/cmdline")`
   - `Entry("pprof heap", "/debug/pprof/heap")`
   - `Entry("pprof goroutine", "/debug/pprof/goroutine")`
   Also add one `It` (or `Entry`) asserting `GET /debug/pprof/profile?seconds=1` returns 200 (a 1 second sample; use the existing `statusCode` helper). Do not add a `trace` test.
   This traverses the real boundary (mux routing plus the `net/http/pprof` handlers over a live listener).

7. In `CHANGELOG.md`, insert a new `## Unreleased` section directly above `## v0.110.0` (blank line before and after the heading, matching the existing section spacing) containing exactly one bullet:
   `- feat: Mount Go pprof on the admin listener — `/debug/pprof/` (index, cmdline, profile, symbol, trace) plus the heap, goroutine, allocs, block, mutex and threadcreate profiles are served on the admin port only, never on the public UI listener, so a heap profile can be taken from a running vault-ui to diagnose the large page-index heap; the admin server's write timeout is raised to two minutes so a default 30 second CPU profile or trace completes`
   Do not edit any released section. The file must contain exactly one `## Unreleased` heading afterwards.

8. Self-check before finishing: re-run `<verification>`, confirm it passes, and walk each acceptance criterion below against the change.

Acceptance criteria:
- A Go test in `pkg/factory/factory_test.go` asserts the admin router served by `CreateHTTPServer` returns 200 for `/debug/pprof/heap` (plus index, cmdline, goroutine).
- `CHANGELOG.md` has an `## Unreleased` section above `## v0.110.0` containing the `- feat:` bullet.
- `ROOTDIR=/workspace make precommit` is green.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Register handlers explicitly on the existing gorilla `mux.Router` in `CreateHTTPServer`; no blank import of `net/http/pprof`, no use of `http.DefaultServeMux`.
- Admin listener only: leave the public UI router, `CreateAPIServer`, `CreateAPIHandler` and `main.go` untouched; `adminListen` stays a constant, with no new flag or env var.
- Owner-directed: this deliberately reverses the pprof non-goal in `specs/in-progress/029-rescan-allocates-only-what-changed.md` (admin port `:9090` binds all interfaces, unauthenticated). Do not add a bind or auth knob here (spec 021 forbids an admin-port knob); the exposure is limited to the admin listener, and the reversal is recorded in the PR for owner sign-off.
- No new configuration knobs, metrics or dependencies (`net/http/pprof` is standard library; `go.mod` needs no change).
- Go errors, if any are introduced, use `github.com/bborbe/errors` per project style; factory code stays wiring only, with no business logic.
- Add GoDoc for any new exported function; the unexported helper also gets a short comment.
- Paths in this prompt are repo-relative; do not use absolute paths.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
</verification>
