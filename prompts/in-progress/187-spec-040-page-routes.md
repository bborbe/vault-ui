---
status: approved
spec: [040-page-view]
created: "2026-11-24T19:36:00Z"
queued: "2026-11-24T19:46:10Z"
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

- `pkg/handler/router.go` — `CreateHTTPRouter`. `router.SkipClean(true)` is in force, and `router.PathPrefix("/")` is the catch-all that serves the static tree and must stay last.
- `pkg/handler/api_common.go` — `writeDetail(resp, status, detail)`, `writeNotFound(resp)`, and the `stderrors.As` error-classification pattern.
- `pkg/handler/static.go` and `pkg/handler/api_vaults.go` — the existing handler shape and the static fallback.
- `pkg/handler/page.go` — the handlers this prompt owns; it imports `github.com/bborbe/vault-ui/pkg/pageview`.
- `pkg/pageview` (built by the previous prompt) — `Service` (`Render`, `Exists`), `NotFoundError`, `PageURL`.
- `pkg/factory/api.go` — `CreateAPIHandler` builds the board. It already receives `vaultSource *vaultapi.Source` (line 174) and constructs `vaults := &vaultProvider{...}` (line 180). The page service is wired with `pageview.NewService(vaults, vaultSource.Client())` and passed to `handler.CreateHTTPRouter(...)`.
- `pkg/vaultapi/source.go` — `Source.Client()` returns the `vaultapi.Client` the page service reads through.
- `pkg/handler/api_test.go` — `fakeBoard`, `fakeMutations`, `newRouterWithMutations`, the `fstest.MapFS` static tree, and `newStubPageService()` / `stubVaultsProvider` / `stubPageSource` (lines 29–53).
- `pkg/handler/queued_writes_test.go` — the second `CreateHTTPRouter` call site (line 236).
- `pkg/handler/page_test.go` — the route specs this prompt adds.
- `github.com/gorilla/mux` — `mux.Vars(req)` reads path variables; `{path:.+}` captures a path containing slashes.

Coding-plugin guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`

Why the handlers check the method themselves: the static catch-all is registered without a method restriction and matches every path and verb. A `POST` to a page address would therefore reach the catch-all (and answer the static 404) unless the page route claims the path first for **all** methods and refuses non-GET itself. Register the page and `/open` routes without `.Methods(...)` and enforce GET-only inside the handler.

<!-- Reviewer note: spec 040 assumes a `pageindex` seam in `CreateAPIHandler`. On this branch page reads come from `vaultSource.Client()`, so the wiring line is `pageview.NewService(vaults, vaultSource.Client())`; `CreateAPIHandler`'s signature does not change. AC8's origin/master diff check is an operator-side proof (the container has `.git` masked), so this prompt proves the same property with a static-fallback test instead. -->

</context>

<requirements>

1. **Create `pkg/handler/page.go`** (replace its contents if it already exists) with two exported constructors:
   ```go
   func NewPageHandler(pages pageview.Service) http.Handler
   func NewOpenHandler(pages pageview.Service) http.Handler
   ```
   Imports: `stderrors "errors"`, `net/http`, `"github.com/golang/glog"`, `"github.com/gorilla/mux"`, `"github.com/bborbe/vault-ui/pkg/pageview"`.

2. **`NewPageHandler`:**
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
   - On success: set `Content-Type: text/html; charset=utf-8`, write `200`, then `resp.Write([]byte(doc))`. This line is the call site AC1 cites: the string returned by `Render` goes straight to the response writer.

3. **`NewOpenHandler`:**
   - If `req.Method != http.MethodGet`, answer `405` exactly as the page handler does.
   - Read `vault := req.URL.Query().Get("vault")` and `file := req.URL.Query().Get("file")`. `Query()` has already percent-decoded both, so a `file` of `24%20Goals%2FSome%20Goal.md` arrives as `24 Goals/Some Goal.md`.
   - `ok, err := pages.Exists(req.Context(), vault, file)`.
   - On error: classify a `pageview.NotFoundError` as `writeNotFound(resp)`; otherwise log with `glog.Errorf("open page %s/%s: %v", vault, file, err)` and answer `500` with `writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")`. `Exists` already reports a missing page, an unknown vault and an invalid path as `(false, nil)`, so only a genuine failure reaches the `500` branch.
   - If `!ok`, answer `writeNotFound(resp)`.
   - Otherwise redirect with `http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)` (302). Using `PageURL` guarantees the redirect target matches the wikilink resolver's encoding.

4. **Register both routes in `CreateHTTPRouter` before `router.PathPrefix("/")`,** and add a `pages pageview.Service` parameter to `CreateHTTPRouter`:
   ```go
   router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
   router.Path("/open").Handler(NewOpenHandler(pages))
   ```
   Place these lines after the `/ws` registration and immediately before the catch-all. Do not add `.Methods(...)` — the handler's method check is what answers `405`; the catch-all would otherwise swallow non-GET requests. Do not modify any existing route registration line.

5. **Wire the service in `pkg/factory/api.go`.** In `CreateAPIHandler`, after `vaults := &vaultProvider{...}` is built:
   ```go
   pages := pageview.NewService(vaults, vaultSource.Client())
   ```
   Pass `pages` as the final argument to `handler.CreateHTTPRouter(...)`. Do not introduce a new configuration key or a second config load. Do not change any other dependency wiring.

6. **Update every `CreateHTTPRouter` call site.** Run `grep -rn 'CreateHTTPRouter' pkg/` and confirm all four sites compile:
   - `pkg/factory/api.go` — pass the real page service.
   - `pkg/handler/api_test.go` (`newRouterWithMutations`, line 134) — already passes `newStubPageService()`; keep it.
   - `pkg/handler/queued_writes_test.go` (line 236) — already passes `newStubPageService()`; keep it.
   - `pkg/handler/page_test.go` — build a real page service over the fixture vault (below).

7. **Add route specs in `pkg/handler/page_test.go`** (`package handler_test`; the handler package already owns exactly one Ginkgo `RunSpecs` under `TestSuite`, so add plain-`testing` functions, not a second suite). Name each test `TestPage...` or `TestOpen...` so `-run 'Page|Open'` selects it. Build the router with `handler.CreateHTTPRouter`, the existing `fakeBoard` and `fakeMutations`, an `fstest.MapFS` static tree, a ready `vaultui.Readiness`, a `websocket.ConnectionManager`, and a real page service over a fixture `PageSource`:
   - A `board.VaultsProvider` returning one vault `{Name: "private-personal", VaultName: "private-personal", Path: vaultDir}`.
   - A `pageview.PageSource` over `vaultDir` (a temp directory) that reads `Alpha.md` (table, callout titled `Important Title`, `[[Beta]]`), `Beta.md`, `Gamma.md` (`![[Beta]]`), `Delta.md` (`[[Nope]]`), `Links.md` (`[[Alpha]]`), `Echo.md` (body-level `<script>`), and `24 Goals/Some Goal.md`. Its `Page` returns a `*vaultapi.Page` with `Content` plus `Links` (name → relative path for every fixture page) and `Embeds` (name → body); its `Backlinks` returns `nil` for these specs.
   Cover:
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

Run from the repo root; each must exit 0:
- `make precommit`
- `GOPRIVATE=github.com/bborbe/vault-markdown go test ./pkg/handler/ -count=1`
- `GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open' -count=1`
- `test "$(grep -n 'NewPageHandler' pkg/handler/router.go | head -1 | cut -d: -f1)" -lt "$(grep -n 'PathPrefix("/")' pkg/handler/router.go | cut -d: -f1)"`

`make precommit` runs format, vet and `go test -race ./...`. The ordering check asserts the page registration sits before the catch-all. The direct `go test` commands carry the `GOPRIVATE` prefix because the shell does not inherit the `Makefile` export.

</verification>
