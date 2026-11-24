---
status: approved
spec: [040-page-view]
created: "2026-11-24T19:16:00Z"
queued: "2026-11-24T18:25:28Z"
branch: test/spec-040-order-runA
---

# Serve the rendered page route and the `/open` compatibility redirect

<summary>

- A page can be opened at its own in-app address and is rendered inside vault-ui.
- Requests for a page are read-only; every other HTTP verb is refused with "method not allowed".
- A compatibility address answers the same parameters as an Obsidian link and redirects to the in-app page, so links that already exist keep working.
- An unknown vault or a missing page answers "not found", never a server error.
- The new addresses are registered ahead of the catch-all that serves the board, so the board still loads.
- The page is served with an HTML content type and the renderer's output goes straight to the response.
- The board's existing addresses are untouched.

</summary>

<objective>

Expose the page-rendering service over HTTP: a read-only page route and a compatibility redirect that answers Obsidian's `vault`/`file` parameters, both registered before the static catch-all so the board keeps serving.

</objective>

<context>

Read these before writing anything.

Project files:

- `pkg/handler/router.go` — `CreateHTTPRouter`: `router.SkipClean(true)` is in force, and `router.PathPrefix("/")` is the catch-all that serves the static tree and must stay last.
- `pkg/handler/api_common.go` — `writeDetail(resp, status, detail)`, `writeNotFound(resp)`, and the `stderrors.As` error-classification pattern.
- `pkg/handler/api_vaults.go` and `pkg/handler/static.go` — the existing handler shape and the static fallback.
- `pkg/pageview` (built by the previous prompt) — `Service` (`Render`, `Exists`), `NotFoundError`, and `PageURL`.
- `pkg/factory/api.go` — `CreateAPIHandler` builds the board; `vaults := &vaultProvider{...}` and the page-index seam are already in scope, and it calls `handler.CreateHTTPRouter(...)`.
- `pkg/handler/api_test.go` — `fakeBoard`, `fakeMutations`, `newRouterWithMutations`, and the `fstest.MapFS` static tree (`index.html`, `app.js`, `style.css`).
- `pkg/handler/queued_writes_test.go` — the second `CreateHTTPRouter` call site.
- `github.com/gorilla/mux` — `mux.Vars(req)` reads path variables; `{path:.+}` captures a path containing slashes.

Coding-plugin guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`

Why the handlers check the method themselves: the static catch-all is registered without a method restriction and matches every path and verb. A `POST` to a page address would therefore reach the catch-all (and answer the static 404) unless the page route claims the path first for **all** methods and refuses non-GET itself. Register the page and `/open` routes without `.Methods(...)` and enforce GET-only inside the handler.

<!-- Reviewer note: the factory wiring below assumes CreateAPIHandler has a page-index seam in scope, matching the spec-time layout. If the branch has moved page reads to vault-api, pass whatever seam pkg/pageview.NewService requires and keep the rest of the wiring unchanged. -->

Scope note on the spec's AC8 diff check: getting the page service into `CreateHTTPRouter` requires adding one `pages pageview.Service` parameter to its signature. Every existing route registration line stays byte-identical; the added parameter and the two new registration lines are the only changes in `pkg/handler/router.go`. Flag this to the reviewer at audit if the operator's diff check is read as literally "no modified existing line".

</context>

<requirements>

1. **Create `pkg/handler/page.go`** (replace its contents if the file already exists) with two exported constructors:
   ```go
   func NewPageHandler(pages pageview.Service) http.Handler
   func NewOpenHandler(pages pageview.Service) http.Handler
   ```
   Imports needed: `stderrors "errors"`, `net/http`, `"github.com/golang/glog"`, `"github.com/gorilla/mux"`, `"github.com/bborbe/vault-ui/pkg/pageview"`.

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
     Write no partial body on the `500`.
   - On success: set `Content-Type: text/html; charset=utf-8`, write `200`, then `resp.Write([]byte(doc))`. This line is the call site the spec's AC1 cites: the string returned by `Render` goes straight to the response writer.

3. **`NewOpenHandler`**:
   - If `req.Method != http.MethodGet`, answer `405` exactly as the page handler does.
   - Read `vault := req.URL.Query().Get("vault")` and `file := req.URL.Query().Get("file")`. `Query()` has already percent-decoded both, so a `file` of `24%20Goals%2FSome%20Goal.md` arrives as `24 Goals/Some Goal.md`.
   - `ok, err := pages.Exists(req.Context(), vault, file)`.
   - On error: log with `glog.Errorf("open page %s/%s: %v", vault, file, err)` and answer `500` with `writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")`. `Exists` reports a missing page, an unknown vault and an invalid path as `(false, nil)`, so only a genuine failure reaches this branch.
   - If `!ok`, answer `writeNotFound(resp)`.
   - Otherwise redirect with `http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)` (302). Using `PageURL` guarantees the redirect target matches the wikilink resolver's encoding.

4. **Register both routes in `CreateHTTPRouter` before `router.PathPrefix("/")`**, and add a `pages pageview.Service` parameter to `CreateHTTPRouter`:
   ```go
   router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
   router.Path("/open").Handler(NewOpenHandler(pages))
   ```
   Place these lines after the `/ws` registration and immediately before the catch-all. Do not add `.Methods(...)` — the handler's method check is what answers `405`; the catch-all would otherwise swallow non-GET requests. Do not modify any existing route line.

5. **Wire the service in `pkg/factory/api.go`.** In `CreateAPIHandler`, the vault provider (`vaults`) and the page-index seam (`pageIndex`) are already in scope. Add:
   ```go
   pages := pageview.NewService(vaults, pageIndex)
   ```
   and pass `pages` as the final argument to `handler.CreateHTTPRouter(...)`. Do not introduce a new configuration key or a second config load. Do not change any other dependency wiring.

6. **Update every `CreateHTTPRouter` call site.** Besides `pkg/factory/api.go`, the handler tests call it:
   - `pkg/handler/api_test.go` (`newRouterWithMutations`) — pass a stub page service built over a `board.VaultsProvider` returning no vaults and a `pageview.PageStorage` returning empty results. If a stub helper already exists, update it rather than adding a second.
   - `pkg/handler/queued_writes_test.go` — pass the same stub.
   Run `grep -rn 'CreateHTTPRouter' pkg/` and confirm every call site compiles.

7. **Add route tests** in a plain-`testing` file `pkg/handler/page_test.go` (`package handler_test`; add it, or extend it if the previous prompt created it). The handler package already owns exactly one Ginkgo `RunSpecs` (under `TestSuite`), so do **not** add a second suite. Name each test `TestPage...` or `TestOpen...` so `-run 'Page|Open'` selects it. Build the router with `CreateHTTPRouter`, the existing `fakeBoard` and `fakeMutations`, an `fstest.MapFS` static tree, readiness, a websocket manager, and a real page service over the fixture vault (the same fixture the previous prompt defines), using `pageindex.NewPageIndex(pageindex.NewPageReader(storage.NewPageStorage(nil)), pageindex.NewDirectoryLister(), libtime.NewCurrentDateTime(), libtime.NewWaiterDuration())` as the seam and a fake `board.VaultsProvider` returning `{Name: "private-personal", VaultName: "private-personal", Path: vaultDir}`. Cover:
   - **AC1 (route level):** `GET /v/private-personal/page/Alpha.md` returns `200`; its body contains, verbatim, the string produced by `markdown.New(markdown.WithResolver(<same Beta mapping>), markdown.WithPages(<same mapping>)).Render(ctx, alphaSource)`; the body contains `<table>` and does not contain the raw `| --- |`; `Content-Type` is `text/html; charset=utf-8`.
   - **AC2:** `GET .../Alpha.md` returns `200` with `<table>` and the callout title `Important Title`; `GET .../Beta.md` returns `200` with a **different** body; `POST`, `PUT`, `PATCH` and `DELETE` to the Alpha page each return `405`.
   - **AC6:** `GET /open?vault=private-personal&file=Alpha.md` returns `302` with `Location: /v/private-personal/page/Alpha.md`; `GET /open?vault=private-personal&file=Missing.md` returns `404`; `GET /open?vault=nonexistent&file=Alpha.md` returns `404` (not `500`); `POST /open?vault=private-personal&file=Alpha.md` returns `405`; `GET /open?vault=private-personal&file=24%20Goals%2FSome%20Goal.md` returns `302` with `Location: /v/private-personal/page/24%20Goals/Some%20Goal.md`.

8. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and each named acceptance criterion against the change.

</requirements>

<constraints>

- The route is read-only: it serves `GET` and nothing else; every other method is `405`. The `/open` route obeys the same rule.
- The new routes must be registered in `pkg/handler/router.go` before `router.PathPrefix("/")`; that catch-all serves the static tree and would otherwise swallow every page path.
- `router.SkipClean(true)` is in force: a traversal request reaches the handler instead of being normalized by the mux, so the `404` comes from the page service's read guard, not from path cleaning.
- An unknown vault is a `404`, never a `500`.
- No new configuration key. The vault set is the one already configured.
- No change to the card link builder, the board's list responses, or any existing route.
- No caching, no live refresh, no editing or write verbs, no renderer change, no standalone viewer process, no styling work beyond the existing stylesheet.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.
- Do not modify `Makefile` or `.github/workflows/ci.yml`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

```bash
make precommit
GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open' -count=1
test "$(grep -n 'NewPageHandler\|NewOpenHandler' pkg/handler/router.go | head -1 | cut -d: -f1)" -lt "$(grep -n 'PathPrefix("/")' pkg/handler/router.go | cut -d: -f1)"
```

`make precommit` must pass. The ordering check asserts the first page/open registration line sits before the catch-all line. The direct `go test` needs the `GOPRIVATE` prefix because the shell does not inherit the `Makefile` export.

</verification>
