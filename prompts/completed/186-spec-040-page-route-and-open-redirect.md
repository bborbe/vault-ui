---
status: completed
spec: [040-page-view]
summary: Added GET-only page and /open compatibility routes registered before the static catch-all, implemented the missing pkg/pageview rendering prerequisite, wired the service through the factory, and added route/unit tests, README docs and a CHANGELOG entry.
execution_id: 69a0781b-d3c5-57ba-a3b1-c3b0c1f3257a-exec-186-spec-040-page-route-and-open-redirect
dark-factory-version: dev
created: "2026-11-23T14:10:00Z"
queued: "2026-11-23T14:35:36Z"
started: "2026-11-23T14:35:39Z"
completed: "2026-11-23T14:46:46Z"
branch: feat/page-view
---

# Serve the rendered page route and the `/open` compatibility redirect

<summary>

- A page can be opened at its own in-app address and rendered inside vault-ui
- Requests for any page are answered only for reads; every other verb is refused
- A compatibility address answers the same parameters as an Obsidian link and redirects to the in-app page
- Links that already exist keep working without editing them
- The new addresses are registered ahead of the catch-all that serves the board, so the board still loads
- The page body is served with an HTML content type
- An unknown vault or a missing page answers "not found", never a server error
- The board's existing addresses are untouched

</summary>

<objective>

Expose the page-rendering service over HTTP: a read-only page route and a compatibility redirect that answers Obsidian's `vault`/`file` parameters, both registered before the static catch-all so the board keeps serving.

</objective>

<context>

Read these before writing anything:

- `pkg/handler/router.go` — `CreateHTTPRouter`: `router.SkipClean(true)` is in force, and `router.PathPrefix("/")` is the catch-all that serves the static tree and must stay last.
- `pkg/handler/api_common.go` — `writeDetail`, `writeNotFound`, and the `stderrors.As` error-classification pattern.
- `pkg/handler/api_vaults.go` and `pkg/handler/static.go` — the existing handler shape and the static fallback.
- `pkg/pageview` (added by the previous prompt) — `Service` (`Render`, `Exists`), `NotFoundError`, and `PageURL`.
- `pkg/factory/api.go` — `CreateAPIHandler` builds the board and calls `CreateHTTPRouter`; it already has `pageIndex` in scope and builds `&vaultProvider{...}` inline.
- `pkg/board/board.go` — `board.VaultsProvider` (the `vaultProvider` satisfies it).
- `pkg/handler/api_test.go` and `pkg/handler/queued_writes_test.go` — both call `CreateHTTPRouter` and must be updated.
- `github.com/gorilla/mux` — `mux.Vars(req)` reads the path variables; `{path:.+}` captures a path containing slashes.
- Coding-plugin guides: `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`, `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md`, and `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`.

Why the handlers check the method themselves: the static catch-all is registered without a method restriction and matches every path and verb. A `POST` to a page address would therefore reach the catch-all (and answer the static 404) unless the page route claims the path first for **all** methods and refuses non-GET itself. Register the page and `/open` routes without `.Methods(...)` and enforce GET-only inside the handler.

Scope note on the spec's AC8 diff check: getting the page service into `CreateHTTPRouter` requires adding one `pages pageview.Service` parameter to its signature. Every existing route registration line stays byte-identical; the added parameter is the sole modified line in `pkg/handler/router.go`, and it is the wiring the decomposition requires. Flag it to the reviewer at audit if the operator's diff check is read as literally "no modified existing line".

</context>

<requirements>

1. **Add `pkg/handler/page.go`** with two exported constructors:
   ```go
   func NewPageHandler(pages pageview.Service) http.Handler
   func NewOpenHandler(pages pageview.Service) http.Handler
   ```

2. **`NewPageHandler`**:
   - If `req.Method != http.MethodGet`, answer `writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")` and return.
   - Read `vault := mux.Vars(req)["vault"]` and `pagePath := mux.Vars(req)["path"]`.
   - `doc, err := pages.Render(req.Context(), vault, pagePath)`.
   - On error, classify with the repo's typed-error idiom:
     ```go
     var nf pageview.NotFoundError
     if stderrors.As(err, &nf) {
         writeNotFound(resp)
         return
     }
     glog.Errorf("render page %s/%s: %v", vault, pagePath, err)
     writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")
     ```
     No partial body on the `500`.
   - On success: set `Content-Type: text/html; charset=utf-8`, write `200`, and write `doc`. This line is the call site the spec's AC1 cites: the string returned by `Render` goes straight to the response writer.

3. **`NewOpenHandler`**:
   - If `req.Method != http.MethodGet`, answer `405` exactly as the page handler does.
   - Read `vault := req.URL.Query().Get("vault")` and `file := req.URL.Query().Get("file")` — `Query()` has already percent-decoded both, so a `file` of `24%20Goals%2FSome%20Goal.md` arrives as `24 Goals/Some Goal.md`.
   - `ok, err := pages.Exists(req.Context(), vault, file)`.
   - On error: classify `pageview.NotFoundError` to `writeNotFound`; any other error logs and answers `500`.
   - If `!ok`, answer `writeNotFound`.
   - Otherwise redirect with `http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)` (302). Using `PageURL` guarantees the redirect target matches the wikilink resolver's encoding.

4. **Register both routes in `CreateHTTPRouter` before `router.PathPrefix("/")`**, and add a `pages pageview.Service` parameter to `CreateHTTPRouter`:
   ```go
   router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
   router.Path("/open").Handler(NewOpenHandler(pages))
   ```
   Place these lines after the `/ws` registration and immediately before the catch-all. Do not add `.Methods(...)` — the handler's method check is what answers `405`; the catch-all would otherwise swallow non-GET requests. Do not modify any existing route line.

5. **Wire the service in `pkg/factory/api.go`.** In `CreateAPIHandler`, extract the vault provider into a variable so both the board and the page service share it:
   ```go
   vaults := &vaultProvider{loader: loader, configPath: configPath}
   service := board.New(board.Deps{ Vaults: vaults, /* ...unchanged... */ })
   // ...
   pages := pageview.NewService(vaults, pageIndex)
   return handler.CreateHTTPRouter(service, mutationsService, CreateStaticFS(), readiness, manager, pages)
   ```
   `pageIndex` is already a parameter of `CreateAPIHandler`. Do not introduce a new configuration key or a second config load.

6. **Update the two existing `CreateHTTPRouter` call sites** in `pkg/handler/api_test.go` (`newRouterWithMutations`) and `pkg/handler/queued_writes_test.go` to pass a page service. Both files are `package handler_test`; add one shared stub in `api_test.go` (or a new test file) — a `board.VaultsProvider` returning no vaults and a `pageview.PageStorage` returning empty — and pass `pageview.NewService(stubVaults, stubStorage)`. These tests do not exercise the page routes; the stub only satisfies the signature.

7. **Add route tests** in a new plain-`testing` file `pkg/handler/page_test.go` (`package handler_test`). The package already owns exactly one Ginkgo `RunSpecs` (under `TestSuite`), so do **not** add a second suite. Name each test `TestPage...` or `TestOpen...` so `-run 'Page|Open'` selects it. Build the router with the existing `newRouter`-style helpers plus a real page service over the fixture vault (the same fixture the previous prompt defines), using `pageindex.NewPageIndex(pageindex.NewPageReader(storage.NewPageStorage(nil)), pageindex.NewDirectoryLister(), libtime.NewCurrentDateTime(), libtime.NewWaiterDuration())` as the seam and a fake `board.VaultsProvider` returning `{Name: "private-personal", VaultName: "private-personal", Path: vaultDir}`. Cover:
   - **AC1 (route level):** `GET /v/private-personal/page/Alpha.md` returns `200` and its body contains, verbatim, the string produced by `markdown.New(markdown.WithResolver(...), markdown.WithPages(...)).Render(ctx, alphaSource)`, and contains `<table>`.
   - **AC2:** `GET /v/private-personal/page/Alpha.md` returns `200` with `<table>` and the callout title text; `GET .../Beta.md` returns `200` with a **different** body; `POST`, `PUT`, `PATCH` and `DELETE` to the Alpha page each return `405`.
   - **AC6:** `GET /open?vault=private-personal&file=Alpha.md` returns `302` with `Location: /v/private-personal/page/Alpha.md`; `GET /open?vault=private-personal&file=Missing.md` returns `404`; `GET /open?vault=nonexistent&file=Alpha.md` returns `404` (not `500`); `POST /open?...` returns `405`; `GET /open?vault=private-personal&file=24%20Goals%2FSome%20Goal.md` (add that page to the fixture) returns `302` with `Location: /v/private-personal/page/24%20Goals/Some%20Goal.md`.

8. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and each named acceptance criterion against the change.

</requirements>

<constraints>

- The route is read-only: it serves `GET` and nothing else; every other method is `405`. The `/open` route obeys the same rule.
- The new routes must be registered in `pkg/handler/router.go` before `router.PathPrefix("/")`; that catch-all serves the static tree and would otherwise swallow every page path.
- `router.SkipClean(true)` is in force: a traversal request reaches the handler instead of being normalized by the mux, so the `404` comes from the page service's read guard, not from path cleaning.
- An unknown vault is a `404`, never a `500`.
- No new configuration key. The vault set is the one already configured.
- No change to the card link builder, the board's list responses, or any existing route.
- Do not modify `Makefile` or `.github/workflows/ci.yml`.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

```bash
make precommit
GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open'
test "$(grep -n 'NewPageHandler\|NewOpenHandler' pkg/handler/router.go | head -1 | cut -d: -f1)" -lt "$(grep -n 'PathPrefix("/")' pkg/handler/router.go | cut -d: -f1)"
```

`make precommit` must pass. The ordering check asserts the first page/open registration line sits before the catch-all line. The direct `go test` needs the `GOPRIVATE` prefix because the environment does not inherit the `Makefile` export.

</verification>
