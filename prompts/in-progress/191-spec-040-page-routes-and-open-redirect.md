---
status: approved
spec: [040-page-view]
created: "2026-11-24T15:01:22Z"
queued: "2026-11-24T15:29:27Z"
branch: test/spec-040-order-run2
---

# Serve the page route and the /open compatibility redirect

<summary>

- A page has its own in-app address and is rendered inside vault-ui
- A compatibility address answers the same parameters as an Obsidian link with a redirect to that page
- Links that already exist keep working without editing them
- The new addresses are registered ahead of the catch-all that serves the board, so the board still loads
- Only reads are answered; every other verb is refused on both addresses
- An unknown vault or a missing page answers not found, never a server error
- The page body is served as HTML with the document wrapper from the render service
- The board's existing addresses are untouched

</summary>

<objective>

Expose the page-rendering service over HTTP: a read-only page route and a compatibility redirect that answers Obsidian's `vault`/`file` parameters, both registered before the static catch-all so the board keeps serving, so an existing `obsidian://open` link resolves to the in-app page instead of launching Obsidian.

</objective>

<context>

There is no `CLAUDE.md` in this repo. Read these before changing anything.

The routes already exist on this branch: `pkg/handler/page.go` implements both handlers, `pkg/handler/router.go` registers them, and `pkg/handler/page_test.go` covers them. Treat the requirements below as the contract to **verify and complete**; if a behavior is present and its test passes, change only what is missing. Do not move the routes or rewrite the handlers to a different shape.

- `pkg/handler/router.go` — `CreateHTTPRouter(b board.Board, m mutations.Service, staticFS fs.FS, readiness vaultui.Readiness, manager websocket.ConnectionManager, pages pageview.Service) http.Handler`. `router.SkipClean(true)` is in force; `router.PathPrefix("/")` is the static catch-all and must stay last.
- `pkg/handler/page.go` — `NewPageHandler(pages pageview.Service) http.Handler` and `NewOpenHandler(pages pageview.Service) http.Handler`.
- `pkg/handler/api_common.go` — `writeDetail(resp, status, detail)` and `writeNotFound(resp)`, the error-body helpers; the `stderrors.As` classification pattern.
- `pkg/handler/page_test.go` — `TestPageRoute…` / `TestOpen…` route specs against the real router and an `httptest` server.
- `pkg/handler/api_test.go` and `pkg/handler/queued_writes_test.go` — the other `CreateHTTPRouter` call sites; each passes `newStubPageService()`. Keep them compiling.
- `pkg/pageview/pageview.go` — `Service` (`Render`, `Exists`), `PageURL(vaultName, pagePath string) string`, `NotFoundError`.
- `pkg/factory/api.go` — `CreateAPIHandler` builds `pages := pageview.NewService(vaults, vaultSource.Client())` and passes it as the last argument to `handler.CreateHTTPRouter`.
- `pkg/handler/static.go` — the catch-all that serves the embedded static tree and answers a missing asset with `writeNotFound`.

Read these coding guides in the container:

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — handlers live in `pkg/handler/`, are built by `New*`, and are wired in one place.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md` — the `{"detail": "..."}` error shape the helpers reproduce.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`.

<!-- OPEN QUESTION (for human reviewer): the spec's AC8 asks that `git diff origin/master -- pkg/handler/router.go` show only added lines. This repo sets `hideGit: true`, so `.git` is masked in the container and that check cannot run in the verification block; it belongs on the spec's operator verification ladder. The prompt instead proves non-regression by running the existing handler and API tests and by asserting the page routes precede the catch-all. -->

</context>

<requirements>

1. **Page handler.** `NewPageHandler(pages pageview.Service) http.Handler` returns an `http.HandlerFunc` that:
   - claims every method; if `req.Method != http.MethodGet`, write `writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")` and return. Do not restrict the route with `.Methods(...)`: the catch-all matches every verb, so a non-GET must be refused here or it reaches the static handler.
   - reads `vault := mux.Vars(req)["vault"]` and `pagePath := mux.Vars(req)["path"]`.
   - calls `pages.Render(req.Context(), vault, pagePath)`.
   - on error, classifies with a value target and answers 404 or 500:
     ```go
     var nf pageview.NotFoundError
     if stderrors.As(err, &nf) {
         writeNotFound(resp)
         return
     }
     glog.Errorf("render page %s/%s: %v", vault, pagePath, err)
     writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")
     ```
     Never write a partial body.
   - on success: set `Content-Type: text/html; charset=utf-8`, `WriteHeader(http.StatusOK)`, write the document.

2. **`/open` handler.** `NewOpenHandler(pages pageview.Service) http.Handler` returns an `http.HandlerFunc` that:
   - refuses non-GET with 405 exactly as in requirement 1.
   - reads `vault := req.URL.Query().Get("vault")` and `file := req.URL.Query().Get("file")` — the names `obsidian://open` uses.
   - calls `pages.Exists(req.Context(), vault, file)`. A `pageview.NotFoundError` (unknown vault) is `writeNotFound`; any other error is logged and answered 500; `ok == false` is `writeNotFound` (404).
   - on success: `http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)` (302). The redirect target is built by the same helper the resolver uses, so an encoded path round-trips.

3. **Register before the catch-all.** In `CreateHTTPRouter`, register:
   ```go
   router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
   router.Path("/open").Handler(NewOpenHandler(pages))
   ```
   **before** `router.PathPrefix("/")`. The greedy `{path:.+}` captures nested and traversal-looking paths so the handler decides; `router.SkipClean(true)` is already in force, so the mux does not normalize the path away first. If the routes were registered after the catch-all, every page path would be swallowed by the static tree.

4. **Keep the wiring whole.** `CreateHTTPRouter` already takes `pages pageview.Service` as its last parameter. If you change that signature, update every caller in the same change: `pkg/factory/api.go` (`CreateAPIHandler`), `pkg/handler/api_test.go`, `pkg/handler/queued_writes_test.go`, and `pkg/handler/page_test.go`. The factory line is `pageview.NewService(vaults, vaultSource.Client())`.

5. **Route tests** in `pkg/handler/page_test.go`, against the real router from `CreateHTTPRouter` and an `httptest` server with a fixture vault (`Alpha.md` table + callout, `Beta.md` different body, `Gamma.md` embed, `Delta.md` missing link, `Links.md` backlink, `Echo.md` script, `24 Goals/Some Goal.md` nested). Name every new test `TestPage…` or `TestOpen…` so `go test -run 'Page|Open'` runs it. Cover:
   - `GET /v/private-personal/page/Alpha.md` → 200, `Content-Type: text/html; charset=utf-8`, body contains `<table>` and the callout title;
   - `GET /v/private-personal/page/Beta.md` → 200 with a different body;
   - `POST`/`PUT`/`PATCH`/`DELETE` on the page route → 405;
   - `GET /open?vault=private-personal&file=Alpha.md` → 302 with `Location: /v/private-personal/page/Alpha.md`;
   - `GET /open?vault=private-personal&file=Missing.md` → 404;
   - `GET /open?vault=nonexistent&file=Alpha.md` → 404 (a miss, not a 500);
   - `POST /open?vault=private-personal&file=Alpha.md` → 405;
   - `GET /open?vault=private-personal&file=24%20Goals%2FSome%20Goal.md` → 302 with `Location: /v/private-personal/page/24%20Goals/Some%20Goal.md`.

6. **Self-check.** Before finishing, re-run the verification commands below and confirm they pass; walk the spec's AC2 and AC6 against the code and tests.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- The routes are read-only: `GET` only; `POST`/`PUT`/`PATCH`/`DELETE` are 405 on both. No upload, no form, no state change.
- No directory listing: a folder path is a 404, never an index of names.
- Register the page routes before `router.PathPrefix("/")`; leave every existing route registration untouched.
- No new configuration key; the routes exist on every board instance.
- Errors use `github.com/bborbe/errors` and the existing `writeDetail`/`writeNotFound` helpers; no `fmt.Errorf`.
- No caching; the handler delegates to the render service, which reads per request.
- Existing tests must still pass, including the handler and API suites that build `CreateHTTPRouter`.

</constraints>

<verification>

Run from the repo root; each must exit 0:

- `make precommit`
- `go test ./pkg/handler/... -run 'Page|Open' -count=1`
- `go test ./... -run 'Page|Open' -count=1`
- `grep -n 'Path("/v/{vault}/page/{path:.+}")' pkg/handler/router.go`
- `grep -n 'Path("/open")' pkg/handler/router.go`
- `awk '/\.Path\("\/v\/\{vault\}\/page\/\{path:\.\+\}"\)/ {p=NR} /\.PathPrefix\("\/"\)/ {c=NR} END { exit !(p>0 && c>0 && p<c) }' pkg/handler/router.go`

</verification>
