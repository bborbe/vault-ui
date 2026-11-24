---
status: approved
spec: [040-page-view]
created: "2026-11-24T19:17:00Z"
queued: "2026-11-24T18:29:47Z"
branch: test/spec-040-order-runA
---

# Lock the page route's read-only and traversal guarantees

<summary>

- A path that tries to escape the vault is refused without reading any file.
- A symlink that points outside the vault is refused the same way.
- An empty path, a folder path and an unknown vault all answer "not found".
- Both new addresses refuse every write verb, not just the one the earlier tests checked.
- A page body containing a script tag cannot execute it.
- A path the page routes do not claim still reaches the board's static files.
- The board's existing addresses keep answering.
- The renderer stays pinned to the released version.

</summary>

<objective>

Harden the page and compatibility routes against traversal, symlink escape and write verbs, prove page content cannot inject executable HTML, and prove the new routes did not disturb the static tree or any existing address.

</objective>

<context>

Read these before writing anything.

Project files:

- `pkg/handler/page.go` (built by the previous prompt) — `NewPageHandler` and `NewOpenHandler`, and the method check that answers `405`.
- `pkg/handler/router.go` — the page and `/open` registrations sit before `router.PathPrefix("/")`; `router.SkipClean(true)` lets a traversal reach the handler.
- `pkg/pageview` — `Service`, `PageStorage`, `NotFoundError`, `PageURL`, and the read guard that rejects `..`/`.` segments before any storage call.
- `pkg/handler/page_test.go` (built by the previous prompt) — the fixture vault, the router builder and the stub page service; extend them rather than duplicating.
- `pkg/handler/api_test.go` — `fakeBoard`, `fakeMutations`, and the `fstest.MapFS` static tree (`index.html`, `app.js`, `style.css`).
- `pkg/handler/static.go` — the static handler and its `canonicalName` guard.
- `github.com/bborbe/vault-cli` `storage` — `ReadPage` refuses a name with a path separator and refuses a symlink resolving outside the vault; that is the guard these tests pin.

Coding-plugin guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`

The verification filter selects top-level tests named `TestPage...` or `TestOpen...`. The handler package already owns exactly one Ginkgo `RunSpecs` (under `TestSuite`), so add plain-`testing` functions, not a second suite.

Verified renderer behaviour that the escaping test relies on: the released `github.com/bborbe/vault-markdown` renders through goldmark with its safe default, so a raw `<script>` in the page body is dropped and replaced by `<!-- raw HTML omitted -->`. It is not emitted as an executable element and its text does not survive. Assert the security property (no literal `<script>` element), not a particular escaping form.

</context>

<requirements>

1. **Traversal costs zero reads.** Add a `TestPageTraversalRefused` test. Define a counting double over `pageview.PageStorage` that increments a `ListPages` counter and a `ReadPage` counter before delegating to the production page index; build the router with `pageview.NewService(vaults, counter)`. Assert `GET /v/private-personal/page/../../../../etc/passwd` returns `404` and the double recorded **zero** `ListPages` and zero `ReadPage` calls. Do not assert only the status: the zero-read count is the point.

2. **Symlink escape is refused.** Add a `TestPageSymlinkOutsideVaultRefused` test. Create a file outside the fixture vault and a symlink inside the vault pointing at it (for example `escape.md`). Request the symlink by name (`/v/private-personal/page/escape.md`) and assert `404`.

3. **Empty, folder and unknown-vault paths are 404.** Add tests asserting:
   - `GET /v/private-personal/page/` returns `404` (it does not match the page route and falls to the static handler);
   - `GET /v/private-personal/page/24%20Goals/` returns `404` — the fixture already holds `24 Goals/Some Goal.md`, so this is a real folder, and the response must never be a listing;
   - `GET /v/nonexistent/page/Alpha.md` returns `404`, not `500`.

4. **The full method matrix is refused on both routes.** Add a `TestOpenMethodsRefused` test asserting `PUT`, `PATCH` and `DELETE` on `/open?vault=private-personal&file=Alpha.md` each return `405`. (`POST` on `/open` and the four verbs on the page route are already covered by the previous prompt.)

5. **Body-level script cannot execute.** Add a `TestPageEscapesScript` test. `GET /v/private-personal/page/Echo.md` (whose body holds a raw `<script>alert(1)</script>`) must return `200`, and the body must **not** contain a literal `<script>` element and must **not** contain `alert(1)`. Do not assert `&lt;script&gt;`: the released renderer omits raw HTML rather than escaping it, and the load-bearing property is that no executable element reaches the response. Assert on the body, not the frontmatter — frontmatter is stripped and would prove nothing.

6. **Static fallback still works.** Add a `TestPageStaticFallback` test. Request a path no page route claims (for example `GET /app.js` against the `fstest.MapFS` static tree used by the handler tests) and assert it returns the static tree's response (`200`, body `console.log('app')`). This proves the new routes were registered before the catch-all and did not shadow it. If the static tree in the test lacks `app.js`, add it to the map.

7. **Existing addresses still answer.** Add a `TestPageExistingRoutesUnchanged` test asserting `GET /api/tasks`, `GET /api/goals` and `GET /api/topics` each still reach the board fake and return `200`, and that `GET /ws` still reaches its handler (a plain request without upgrade headers is not a `404`, which proves the route is still registered). Do not modify any existing route registration line; the page and `/open` registrations are the only additions.

8. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and the spec's AC7 and AC8 against the change.

</requirements>

<constraints>

- The routes are read-only: `GET` only; every other method is `405`, on both the page route and `/open`.
- Path traversal and symlink escape are the primary risk. Every read goes through the page service's `ReadPage`; the route must not open files itself, and a traversal must be refused before any storage call.
- An unknown vault is a `404`, never a `500`.
- There is no directory listing: a folder path returns `404`, never an index of names.
- Page content is untrusted markdown; the handler must not splice the raw path, vault name or file content into the document unescaped. The renderer drops body-level raw HTML, and the document wrapper escapes every value except the renderer output.
- No caching, no live refresh, no editing or write verbs, no renderer change, no standalone viewer process, no styling work beyond the existing stylesheet.
- No new configuration key. The vault set is the one already configured.
- No change to the card link builder, the board's list responses, or any existing route.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.
- Do not modify `Makefile` or `.github/workflows/ci.yml`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

```bash
make precommit
GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open' -count=1
test "$(grep -c 'github.com/bborbe/vault-markdown v0.1.0' go.mod)" = "1"
```

`make precommit` must pass. The direct `go test` needs the `GOPRIVATE` prefix because the shell does not inherit the `Makefile` export. The `grep -c` re-asserts the renderer pin.

</verification>
