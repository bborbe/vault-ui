---
status: approved
spec: [040-page-view]
created: "2026-11-24T09:04:18Z"
queued: "2026-11-24T09:34:19Z"
branch: feat/page-view
---

# Serve the page route and the `/open` compatibility redirect

<summary>

- A page is reachable at `GET /v/{vault}/page/{path}` and served as HTML.
- The route is read-only: every verb other than `GET` gets `405`, not the static page.
- An unknown vault, a missing page or a path that escapes the vault gets `404`, never `500`.
- `GET /open?vault=X&file=Y` answers Obsidian's own parameters with a `302` to the page route.
- A missing page or an unknown vault on `/open` gets `404`, and a non-GET verb gets `405`.
- A `file` value containing an encoded space and slash redirects to the corresponding page route.
- Both routes are registered before the static catch-all, so the board's static tree still serves every other path.
- No existing route changes and no new configuration key; the renderer is wired from the previous prompt.

</summary>

<objective>

Expose the page service over HTTP. This prompt ends with two handlers — `GET /v/{vault}/page/{path}` and `GET /open` — registered in `pkg/handler/router.go` ahead of `router.PathPrefix("/")`, both `GET`-only with a `405` for every other verb, a `404` for an unknown vault / missing page / escaping path, and a `302` from `/open` to the page route. The router gains a `pageview.Service` parameter and every call site is updated.

</objective>

<context>

Read these before writing code.

- `pkg/pageview/pageview.go` (previous prompt) — `pageview.Service` (`Render(ctx, vaultName, pagePath) (string, error)`, `Exists(ctx, vaultName, pagePath) (bool, error)`), `pageview.PageURL(vaultName, pagePath) string`, and `pageview.NotFoundError` (classify with `stderrors.As`). `Render` returns the full HTML document; `Exists` returns `(false, NotFoundError)` for a missing page and a `NotFoundError` for an unknown vault.
- `pkg/handler/router.go` — `CreateHTTPRouter(b board.Board, m mutations.Service, staticFS fs.FS, readiness vaultui.Readiness, manager websocket.ConnectionManager, pages pageview.Service) http.Handler`. `router.SkipClean(true)` is set. `router.PathPrefix("/")` is the static catch-all and must stay last.
- `pkg/handler/api_common.go` — `writeDetail(resp, status, detail)` writes the framework-shaped `{"detail": "..."}` body; `writeNotFound(resp)` writes the FastAPI default 404. Use these for the error responses.
- `pkg/handler/static.go` — `NewStaticHandler(fsys fs.FS)` serves the embedded tree; a path it does not recognise returns 404. The catch-all must still reach it for paths no page route claims.
- `pkg/factory/api.go` — `CreateAPIHandler(...)` builds `vaults := &vaultProvider{...}` (a `board.VaultsProvider`) and calls `handler.CreateHTTPRouter(...)`. This is the production wiring point; `storage.NewPageStorage(nil)` is the seam to inject.
- `pkg/handler/api_test.go` (`newRouterWithMutations`), `pkg/handler/queued_writes_test.go` (`newQueuedFixture`) and `pkg/handler/page_test.go` (`newPageTestRouter`) each call `CreateHTTPRouter` and must pass the new argument.
- `github.com/gorilla/mux` — `mux.Vars(req)` returns the `vault` and `path` variables from the matched route.
- Conventions: `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` for the handler shape, `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` for wrapping, and `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` for the plain `testing` + `t.Fatalf` shape the page route tests use.

</context>

<requirements>

1. Add `pkg/handler/page.go` with `NewPageHandler(pages pageview.Service) http.Handler`. It claims every method so a non-GET request is answered here instead of falling through to the static catch-all:

```go
func NewPageHandler(pages pageview.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		vars := mux.Vars(req)
		vault := vars["vault"]
		pagePath := vars["path"]
		doc, err := pages.Render(req.Context(), vault, pagePath)
		if err != nil {
			var notFound pageview.NotFoundError
			if stderrors.As(err, &notFound) {
				writeNotFound(resp)
				return
			}
			glog.Errorf("render page %s/%s: %v", vault, pagePath, err)
			writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		resp.Header().Set("Content-Type", "text/html; charset=utf-8")
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write([]byte(doc))
	})
}
```

Imports: `stderrors "errors"`, `net/http`, `github.com/golang/glog`, `github.com/gorilla/mux`, `github.com/bborbe/vault-ui/pkg/pageview`.

2. Add `NewOpenHandler(pages pageview.Service) http.Handler` in the same file. It answers `vault` and `file` exactly as `obsidian://open` spells them (the query string is URL-decoded by `req.URL.Query()`):

```go
func NewOpenHandler(pages pageview.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		query := req.URL.Query()
		vault := query.Get("vault")
		file := query.Get("file")
		ok, err := pages.Exists(req.Context(), vault, file)
		if err != nil {
			var notFound pageview.NotFoundError
			if stderrors.As(err, &notFound) {
				writeNotFound(resp)
				return
			}
			glog.Errorf("open page %s/%s: %v", vault, file, err)
			writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		if !ok {
			writeNotFound(resp)
			return
		}
		http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)
	})
}
```

`Exists` returns `(false, NotFoundError)` for a missing page, so the `err` branch already answers 404; keep the `!ok` guard for a service that reports a miss without an error.

3. Register both routes in `pkg/handler/router.go` **after** the `/ws` route and **before** `router.PathPrefix("/")`. Do not attach `.Methods(http.MethodGet)` — the handler returns the 405 itself, and a method-restricted mux route would let a non-GET request fall through to the catch-all:

```go
	// The page routes are registered without a method restriction so a non-GET
	// request is refused by the handler with 405 rather than swallowed by the
	// catch-all below, which matches every path and verb.
	router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
	router.Path("/open").Handler(NewOpenHandler(pages))

	router.PathPrefix("/").Handler(NewStaticHandler(staticFS))
```

4. Thread the service through the router signature. `CreateHTTPRouter` already has a `pages pageview.Service` parameter; if it is absent, add it after `manager`. Update **every** call site (run `grep -rn 'CreateHTTPRouter' .` and confirm these four):
   - `pkg/factory/api.go` — build `pages := pageview.NewService(vaults, storage.NewPageStorage(nil))` from the existing `vaults := &vaultProvider{...}` and pass it as the last argument. Import `github.com/bborbe/vault-cli/pkg/storage` and `github.com/bborbe/vault-ui/pkg/pageview` if not already imported.
   - `pkg/handler/api_test.go` — pass the existing `newStubPageService()`.
   - `pkg/handler/queued_writes_test.go` — pass `newStubPageService()`.
   - `pkg/handler/page_test.go` — pass a real `pageview.NewService(pageTestVaults{dir: vaultDir}, storage.NewPageStorage(nil))`.

5. Add the route tests in `pkg/handler/page_test.go` (package `handler_test`, plain `testing`, every test named `TestPage…` or `TestOpen…`). Build the real router with `handler.CreateHTTPRouter(...)`, a `fstest.MapFS` static tree (at least `"index.html"`), `vaultui.NewReadiness()` set ready, `websocket.NewConnectionManager(websocket.NewMetrics())`, and the page service over the fixture vault from the previous prompt. Cover:

   a. **AC2** — `GET /v/private-personal/page/Alpha.md` is `200`, the body contains `<table>` and the callout title `Important Title`, and `Content-Type` is `text/html; charset=utf-8`. `GET /v/private-personal/page/Beta.md` is `200` and its body differs from Alpha's. `POST`, `PUT`, `PATCH` and `DELETE` on `/v/private-personal/page/Alpha.md` each return `405`.

   b. **AC1 at the route** — the `Alpha.md` body contains, verbatim, the output of `markdown.New(markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) { if target == "Beta" { return "/v/private-personal/page/Beta.md", true }; return "", false })), markdown.WithPages(markdown.PagesFunc(func(string) (string, bool) { return "", false }))).Render(context.Background(), alphaSource)`.

   c. **AC6** — `GET /open?vault=private-personal&file=Alpha.md` is `302` with `Location: /v/private-personal/page/Alpha.md`; `GET /open?vault=private-personal&file=Missing.md` is `404`; `GET /open?vault=nonexistent&file=Alpha.md` is `404`; `POST /open?vault=private-personal&file=Alpha.md` is `405`; `GET /open?vault=private-personal&file=24%20Goals%2FSome%20Goal.md` is `302` with `Location: /v/private-personal/page/24%20Goals/Some%20Goal.md` (add `24 Goals/Some Goal.md` to the fixture).

   d. Keep the static-fallback test (`GET /` returns the `index.html` body with `200`) — it proves the catch-all is still reachable, and the next prompt tightens it.

6. Do not modify any existing route registration, handler or response shape. The two new registrations are the only lines added to `router.go` besides the new parameter.

</requirements>

<constraints>

- Both routes serve `GET` only; every other verb returns `405`. Do not attach `.Methods(http.MethodGet)` to either route.
- An unknown vault, a missing page, a path that escapes the vault, an empty path and a folder path return `404`, never `500`. A renderer or read failure that is not a `NotFoundError` returns `500` and logs the vault and path.
- `/open` reads `vault` and `file` exactly as `obsidian://open` spells them; `file` is URL-decoded by the query parser, and `pageview.PageURL` re-encodes each segment on the redirect.
- The page route must be registered before `router.PathPrefix("/")`; the catch-all stays last.
- No cache, no new configuration key, no new dependency, no frontend change, no metrics.
- Do not modify `pkg/pageview/` (previous prompt), the existing `/api/` routes, `src/vault_ui/static/`, `README.md`, `CHANGELOG.md` or `go.mod`/`go.sum`.
- Do not touch `.github/workflows/`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

`go` is not always on `PATH` in this container. Run each command with `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/handler/ -run 'Page|Open' -count=1
make precommit
```

`go test ./pkg/handler/ -run 'Page|Open'` runs the page-route, `/open` and static-fallback tests. `make precommit` runs `go fmt ./...`, `go vet ./...` and `go test -race ./...` and must exit 0.

Confirm the dependency pin (exactly one line):

```
grep -n 'github.com/bborbe/vault-markdown v0.1.0' go.mod
```

</verification>
