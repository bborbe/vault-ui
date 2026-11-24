---
status: draft
spec: [040-page-view]
created: "2026-11-24T09:04:18Z"
branch: feat/page-view
---

# Lock the page route's boundary and prove nothing existing moved

<summary>

- A traversal request is refused before any file is read, and the test proves zero reads.
- A symlink inside the vault that points outside it is refused with the same 404 the board's read already gives.
- An empty path, a folder path and an unknown vault each return 404, never 500 and never a directory listing.
- A page whose body carries a raw `<script>` tag cannot render as an executable element.
- A path no page route claims still reaches the static tree, so the board keeps serving.
- The new routes stay registered ahead of the static catch-all.
- The module pin is unchanged and the full test suite stays green.
- A changelog entry records that a page view is rendered per request and nothing is cached.

</summary>

<objective>

Prove and lock the boundary the two previous prompts opened. This prompt adds the negative-path tests (traversal, symlink escape, empty/folder path, unknown vault, method rejection on `/open`), the body-level script-escaping assertion, the static-fallback and registration-order checks, and a changelog entry. It changes implementation only where a test exposes a gap; it does not widen the routes.

</objective>

<context>

Read these before writing code.

- `pkg/pageview/pageview.go` — `Render` resolves the vault and validates the path before any storage call, and `ReadPage` owns the symlink guard. `splitPagePath` rejects empty, leading-`/`, non-`.md`, and `.`/`..`/empty segments; `resolveVault` rejects an unknown vault. Both return `NotFoundError` before `ListPages`/`ReadPage` runs.
- `pkg/handler/page.go` — `NewPageHandler` / `NewOpenHandler`: a non-GET returns `405`; a `pageview.NotFoundError` returns `404`; any other error logs and returns `500`.
- `pkg/handler/router.go` — the two page routes are registered before `router.PathPrefix("/")`; `router.SkipClean(true)` is set, so a traversal request reaches the handler rather than being normalised by the mux.
- `pkg/handler/page_test.go` — the fixture and router builder from the previous prompt; extend them, do not duplicate them.
- `github.com/bborbe/vault-cli@v0.197.0` `pkg/storage` — `storage.NewPageStorage(nil)` is the real seam; wrap it in a counting double to prove zero reads.
- `github.com/bborbe/vault-markdown@v0.1.0` — the renderer does not enable goldmark's `html.WithUnsafe`, so raw HTML in a page body is omitted (`<!-- raw HTML omitted -->`), never emitted as an element. Do not enable `html.WithUnsafe` to make a test pass.
- `docs/dod.md` (the repo's validation prompt) — a behavior change needs a `CHANGELOG.md` entry under `## Unreleased`.
- `README.md` already documents `GET /v/{vault}/page/{path}` and `GET /open` and already says "nothing is cached"; verify it and change it only if it contradicts the shipped behavior.
- Conventions: `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` for the plain `testing` shape, and `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for the `## Unreleased` + conventional-prefix entry.

</context>

<requirements>

1. Add a `countingStorage` to `pkg/handler/page_test.go` that implements `pageview.PageStorage` by delegating to `storage.NewPageStorage(nil)` and recording every `ListPages` and `ReadPage` call. Add a router-builder variant that accepts a `pageview.PageStorage` so a test can inject it. Reset the counters at the start of each test.

2. **AC7 traversal** — `GET /v/private-personal/page/../../../../etc/passwd` through the real router (call `router.ServeHTTP(recorder, httptest.NewRequest(...))`; do not use an `http.Client`, which may clean the path) returns `404`, and the counting storage records **0** `ListPages` and **0** `ReadPage` calls. Assert the zero explicitly: a path guard that runs after a read fails this test.

3. **AC7 symlink** — create `Escape.md` inside the fixture vault as a symlink to a file outside it, then `GET /v/private-personal/page/Escape.md` returns `404`. The guard is `ReadPage`'s; do not add a second file check in the handler.

4. **AC7 boundary cases** — each returns `404`:
   - `GET /v/private-personal/page/` (empty path; the `{path:.+}` route does not match, so the static catch-all answers 404).
   - `GET /v/private-personal/page/24%20Goals/` (a folder path, never a listing).
   - `GET /v/nonexistent/page/Alpha.md` (unknown vault), with the counting storage recording 0 reads.

5. **AC8 script escaping** — `GET /v/private-personal/page/Echo.md` returns `200`, and the body contains no executable `<script>` element: assert the body does **not** contain the substring `<script>alert(1)</script>` and does **not** contain a `<script` tag from the page body. With the pinned renderer the raw HTML is omitted, so also assert the body records `<!-- raw HTML omitted -->` (the existing assertion). Add a comment stating the spec's wording ("appears as text") and the renderer's actual treatment (omitted); the security property — no executable element — is what the test locks. Do not enable `html.WithUnsafe`.

6. **AC8 static fallback and registration order**:
   - Keep `TestPageRouteFallsBackToStaticTree`: `GET /` returns `200` and the `index.html` body, proving a path no page route claims still reaches the static handler.
   - Add a test (or a `grep`-based verification below) that the page-route registrations precede `router.PathPrefix("/")`. The behavioural fallback test is the required assertion; the ordering check is a belt-and-braces guard.

7. **Changelog** — add a `## Unreleased` section directly below the existing preamble and above the top version heading, with one `- feat:` bullet describing the in-app page view: the two routes, the released `vault-markdown` v0.1.0 renderer (tables, callouts, embeds, resolving wikilinks, backlinks), the on-demand `ReadPage` read and symlink guard, `GET`-only with `405`, `404` for an unknown vault / missing page / escaping path, and that nothing is cached. Follow `changelog-guide.md`. Do not edit the historical version entries. Do not change `README.md` unless it contradicts the shipped behavior (it already documents both routes and "nothing is cached").

8. Run the full verification below and confirm it exits 0. Then walk AC7 and AC8 against the change and name the test that proves each one.

</requirements>

<constraints>

- The route remains read-only: `GET` only, every other verb `405` on both routes. No upload, form or state change.
- Traversal and an unknown vault are refused **before** any page-storage call (0 reads). A symlink out of the vault is refused by `ReadPage`'s guard.
- No directory listing: a folder path is `404`, never an index of names.
- The page body is untrusted markdown; the renderer escapes/omits raw HTML. Never splice the raw path, vault name or file content into the document unescaped, and never enable `html.WithUnsafe`.
- No cache, no new configuration key, no new dependency, no metrics, no frontend change, no renderer change.
- Do not modify `pkg/pageview/` or `pkg/handler/page.go` unless a test exposes a real gap; do not widen the routes to make a test pass.
- Do not touch `.github/workflows/`, `go.mod`/`go.sum`, or any existing route registration.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

`go` is not always on `PATH` in this container. Run each command with `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test ./... -run 'Page|Open' -count=1
make precommit
```

`go test ./... -run 'Page|Open'` runs every page and `/open` test across packages (the filter is why every new test is named `TestPage…` or `TestOpen…`). `make precommit` runs `go fmt ./...`, `go vet ./...` and `go test -race ./...` and must exit 0.

Confirm the dependency pin is exactly one line:

```
grep -n 'github.com/bborbe/vault-markdown v0.1.0' go.mod
```

Confirm the page routes are registered before the static catch-all (the two page-route lines must have smaller line numbers than the `PathPrefix("/")` line):

```
grep -n 'Path("/v/{vault}/page/\|Path("/open")\|PathPrefix("/")' pkg/handler/router.go
```

</verification>
