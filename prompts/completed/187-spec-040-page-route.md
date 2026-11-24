---
status: completed
spec: [040-page-view]
summary: Verified the existing page route and /open redirect implementation against the contract, added a feat CHANGELOG entry under Unreleased, and confirmed all targeted tests and make precommit exit 0
execution_id: aa692478-dac4-5bc3-93e4-7cc13386c89c-exec-187-spec-040-page-route
dark-factory-version: dev
created: "2026-11-24T22:15:00Z"
queued: "2026-11-24T21:22:27Z"
started: "2026-11-24T21:33:24Z"
completed: "2026-11-24T21:40:11Z"
branch: test/spec-040-order-runB
---

# Serve the page route and the /open compatibility redirect

<summary>

- A browser can read a vault page at a URL inside vault-ui and get rendered HTML back.
- The page route answers only reads; any other verb is refused rather than handed to the static catch-all.
- Existing `obsidian://open?vault=…&file=…` links keep working: the compatibility route answers the same parameters with a redirect to the in-app page.
- A missing page or an unknown vault is a 404, never a 500.
- The page routes are registered ahead of the static catch-all, so a board path still reaches the static tree.
- Page URLs keep the vault's display name and the page's nested folders, with each path segment escaped.

</summary>

<objective>

Expose the page-rendering service over HTTP: add the `GET /v/{vault}/page/{path}` route and the `GET /open?vault=&file=` compatibility redirect, wire the page service into the router and the factory, and prove both routes through the real router with an `httptest` server.

</objective>

<context>

Read these before changing anything:

- `pkg/pageview/pageview.go` — `pageview.Service` with `Render(ctx, vaultName, pagePath) (string, error)` and `Exists(ctx, vaultName, pagePath) (bool, error)`, plus `pageview.PageURL(vaultName, pagePath string) string` and `pageview.NotFoundError`. This is the service the handlers call (built by prompt 1).
- `pkg/handler/router.go` — `CreateHTTPRouter(b board.Board, m mutations.Service, staticFS fs.FS, readiness vaultui.Readiness, manager websocket.ConnectionManager, pages pageview.Service) http.Handler`; it sets `router.SkipClean(true)` and ends with `router.PathPrefix("/").Handler(NewStaticHandler(staticFS))`.
- `pkg/handler/static.go` — `NewStaticHandler` serves the embedded frontend and returns its own 404 for a non-GET/HEAD request.
- `pkg/handler/api_common.go` — `writeDetail(resp, status, detail)` writes the framework-shaped `{"detail": ...}` body; `writeNotFound(resp)` writes the 404 body.
- `pkg/handler/api_test.go` — `newRouterWithMutations` calls `CreateHTTPRouter(..., newStubPageService())`; `stubVaultsProvider`, `stubPageSource` and `newStubPageService` already exist there.
- `pkg/handler/queued_writes_test.go` — a second `CreateHTTPRouter` call site that also passes `newStubPageService()`.
- `pkg/factory/api.go` — `CreateAPIHandler` builds `pages := pageview.NewService(vaults, vaultSource.Client())` and passes it to `CreateHTTPRouter`. This is the production wiring; keep it.
- `pkg/vaultapi/source.go` — `Source.Client()` returns the `vaultapi.Client` that satisfies `pageview.PageSource`.
- `pkg/board/helpers.go` — `board.ObsidianURL(vaultName, filePath string) string`.

Read these coding guides in the container:

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — handlers in `pkg/handler/`, wired by `Create*` in `pkg/factory/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — the handler package already runs Ginkgo for API specs, but every new page test must be a stdlib `TestPage…`/`TestOpen…` function (see requirement 6) so the `-run 'Page|Open'` filter actually runs it.

<!--
Open questions for the human reviewer:

1. Spec 040 registers the routes "without a method restriction" so a non-GET request is answered 405 by the handler. This is load-bearing: `router.Methods(http.MethodGet).Path(...)` would let a POST fall through to `PathPrefix("/")`, whose static handler answers 404, not 405. Keep the unqualified `router.Path(...)` registration.
2. The page route pattern must be `{path:.+}` so nested folders match and the traversal case reaches the handler (`SkipClean(true)` keeps the mux from normalising it).
3. The implementation may already exist on the branch (`pkg/handler/page.go`, `pkg/handler/page_test.go`). If so, read it first and make the smallest change that satisfies this contract; do not duplicate handlers.
-->

</context>

<requirements>

1. Create `pkg/handler/page.go` with two constructors:

   ```go
   func NewPageHandler(pages pageview.Service) http.Handler
   func NewOpenHandler(pages pageview.Service) http.Handler
   ```

2. `NewPageHandler`:
   - If `req.Method != http.MethodGet`, write `writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")` and return.
   - Read `vault := mux.Vars(req)["vault"]` and `pagePath := mux.Vars(req)["path"]`.
   - Call `pages.Render(req.Context(), vault, pagePath)`.
   - On a `pageview.NotFoundError` (classified with stdlib `errors.As`, imported as `stderrors "errors"`; `github.com/bborbe/errors` supplies the wrapping), call `writeNotFound(resp)`.
   - On any other error, log it with `glog.Errorf` naming vault and path, then `writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")`. Never write a partial body.
   - On success, set `Content-Type: text/html; charset=utf-8`, `WriteHeader(http.StatusOK)`, and write the document.

3. `NewOpenHandler`:
   - If `req.Method != http.MethodGet`, answer 405 exactly as above.
   - Read `vault := req.URL.Query().Get("vault")` and `file := req.URL.Query().Get("file")` — the same parameter names `obsidian://open` uses. Query decoding handles the URL-encoded file value.
   - Call `pages.Exists(req.Context(), vault, file)`. A `pageview.NotFoundError` is a 404; any other error is logged and is a 500.
   - When `ok` is false, `writeNotFound(resp)`.
   - When `ok` is true, redirect with `http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)`.

4. Register both routes in `pkg/handler/router.go` **before** `router.PathPrefix("/")`, using the unqualified `Path` (no `Methods`) so the handler itself answers non-GET:

   ```go
   router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
   router.Path("/open").Handler(NewOpenHandler(pages))
   router.PathPrefix("/").Handler(NewStaticHandler(staticFS))
   ```

   If `CreateHTTPRouter` does not already take a `pages pageview.Service` last parameter, add it and update every call site in the same change: `pkg/factory/api.go` (pass `pageview.NewService(vaults, vaultSource.Client())`), `pkg/handler/api_test.go` (pass `newStubPageService()`), `pkg/handler/queued_writes_test.go` (pass `newStubPageService()`), and the new `pkg/handler/page_test.go`.

5. Create `pkg/handler/page_test.go` (stdlib tests, external package `handler_test`) that builds the real router with `handler.CreateHTTPRouter` over a fixture vault and a `pageview.PageSource` fake reading that fixture directory, then asserts through `httptest`:
   - `GET /v/private-personal/page/Alpha.md` → `200`, body contains `<table>` and the callout title, `Content-Type` is `text/html; charset=utf-8`.
   - `GET /v/private-personal/page/Beta.md` → `200` and a **different** body.
   - `POST /v/private-personal/page/Alpha.md` → `405`.
   - `GET /open?vault=private-personal&file=Alpha.md` → `302` with `Location: /v/private-personal/page/Alpha.md`.
   - `GET /open?vault=private-personal&file=Missing.md` → `404`.
   - `GET /open?vault=nonexistent&file=Alpha.md` → `404` (unknown vault is a miss, not a 500).
   - `POST /open?vault=private-personal&file=Alpha.md` → `405`.
   - `GET /open?vault=private-personal&file=24%20Goals%2FSome%20Goal.md` → `302` with `Location: /v/private-personal/page/24%20Goals/Some%20Goal.md`.
   - `GET /` still reaches the static tree (the catch-all serves `index.html`), proving the page routes did not swallow it.

6. Name every new test `TestPage…` or `TestOpen…` and keep it a stdlib `func TestX(t *testing.T)`, not a Ginkgo node. The verification filter `go test ./... -run 'Page|Open'` matches top-level test functions; a Ginkgo `TestSuite` is skipped by that filter and would hide a broken route.

7. Do not change the existing `/api/…` or `/ws` registrations.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- The page and `/open` routes are read-only: `GET` only, everything else `405`.
- Register the page routes before `router.PathPrefix("/")`; `router.SkipClean(true)` stays in force.
- No new configuration key; the vault set is the one already configured.
- Errors go through `github.com/bborbe/errors`; no `fmt.Errorf`.
- Do not open files, walk the vault or cache anything in the handlers; they call the `pageview.Service` only.
- Do not modify the card link builder, the board's list responses, or any existing route.
- Existing tests must still pass.

</constraints>

<verification>

Run from the repo root; each must exit 0:

- `make precommit`
- `go test ./pkg/handler/ -count=1 -run 'TestPage|TestOpen'`
- `go test ./... -run 'Page|Open'`

Before finishing, re-run the verification and walk each acceptance criterion against the change.

</verification>

<acceptance_criteria>

- `GET /v/{vault}/page/{path}` returns `200` and rendered HTML; a second page returns a different body.
- A non-GET on the page route is `405`, not the static tree's 404.
- `GET /open` answers Obsidian's `vault`/`file` parameters with a `302` to the page route, including an encoded nested path.
- A missing page and an unknown vault are `404`; `POST /open` is `405`.
- The static catch-all still serves `/`.
- The page routes are registered before `router.PathPrefix("/")`.
- `make precommit` exits 0.

</acceptance_criteria>
