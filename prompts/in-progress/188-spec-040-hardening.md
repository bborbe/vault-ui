---
status: approved
spec: [040-page-view]
created: "2026-11-24T22:57:00Z"
queued: "2026-11-24T22:15:05Z"
branch: test/spec-040-order-runC
---

# Lock the page route's read-only and traversal guarantees

<summary>

- A path that tries to escape the vault is refused without reading any file
- A link that resolves outside the vault through a symlink is refused the same way
- An empty path, a folder path and an unknown vault all answer "not found"
- Both new addresses refuse every write verb, not just the one the earlier tests checked
- A page body containing a script tag renders it as text, never as an executable element
- A path the page routes do not claim still reaches the board's static tree
- The board's existing addresses keep answering
- The renderer stays pinned to the released version

</summary>

<objective>

Harden the page and compatibility routes against traversal, symlink escape and write verbs, prove page content cannot inject HTML, and prove the new routes did not disturb the static tree or any existing address.

</objective>

<context>

Read these before writing anything:

- `pkg/handler/page.go` (added by prompt 2) — `NewPageHandler` and `NewOpenHandler`, and the method check that answers `405`.
- `pkg/handler/router.go` — the page and `/open` registrations sit before `router.PathPrefix("/")`; `router.SkipClean(true)` lets a traversal reach the handler.
- `pkg/pageview` — `Service`, `NotFoundError`, `PageURL`, and the read guard that rejects `..`/`.` segments before any storage call.
- `pkg/handler/page_test.go` (added by prompt 2) — the fixture vault, the fake `board.VaultsProvider`, the page-index seam, and the router builder to reuse.
- `pkg/handler/api_test.go` and `pkg/handler/queued_writes_test.go` — the existing `fakeBoard`, the static `fstest.MapFS`, and the router helpers.
- `pkg/handler/static.go` — the static handler and its `canonicalName` guard.
- `github.com/bborbe/vault-cli` `storage` — `ReadPage` refuses a name with a path separator and refuses a symlink resolving outside the vault; that is the guard these tests pin.
- Coding-plugin guides (in-container paths): `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md` and `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`.

The verification filter selects top-level tests named `TestPage...` or `TestOpen...`. The package already owns exactly one Ginkgo `RunSpecs` (under `TestSuite`), so add plain-`testing` functions, not a second suite.

</context>

<requirements>

1. **Traversal costs zero reads.** Add a `TestPageTraversalRefused` test. Wrap the page-storage seam in a counting double (counts `ListPages` and `ReadPage` calls) and build the router over it. Assert `GET /v/private-personal/page/../../../../etc/passwd` returns `404` and the double recorded **zero** `ListPages` and zero `ReadPage` calls. Do not assert only the status: the zero-read count is the point.

2. **Symlink escape is refused.** Add a `TestPageSymlinkOutsideVaultRefused` test. Create a file outside the fixture vault and a symlink inside it pointing at that file; request the symlink by name (e.g. `/v/private-personal/page/escape.md`). Assert `404`.

3. **Empty, folder and unknown-vault paths are 404.** Add tests asserting:
   - `GET /v/private-personal/page/` returns `404` (it does not match the page route and falls to the static handler);
   - `GET /v/private-personal/page/24%20Goals/` returns `404` — the `24 Goals` folder from prompt 2's fixture holds at least one page, and the response is never a listing;
   - `GET /v/nonexistent/page/Alpha.md` returns `404`, not `500`.

4. **The full method matrix is refused on both routes.** Add a `TestPageMethodsRefused` and a `TestOpenMethodsRefused` (or one test covering both) asserting `POST`, `PUT`, `PATCH` and `DELETE` each return `405` for `/v/private-personal/page/Alpha.md` and for `/open?vault=private-personal&file=Alpha.md`.

5. **Body-level script is escaped.** Add a `TestPageEscapesScript` test. `GET /v/private-personal/page/Echo.md` (whose body holds a raw `<script>` tag) must contain the escaped text `&lt;script&gt;` and must **not** contain a literal `<script>` element. Assert on the body, not the frontmatter — frontmatter is stripped and would prove nothing.

6. **Static fallback still works.** Add a `TestPageStaticFallback` test. Request a path no page route claims (e.g. `GET /app.js` against the `fstest.MapFS` used by the handler tests) and assert it returns the static tree's response. This proves the new routes were registered before the catch-all and did not shadow it.

7. **Existing addresses still answer.** Add a `TestPageExistingRoutesUnchanged` test asserting `GET /api/vaults` (or `GET /api/tasks`) still reaches the board fake and returns its response, so the new routes did not intercept an existing path.

8. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and the spec's AC7 and AC8 against the change.

</requirements>

<constraints>

- The routes are read-only: `GET` only; every other method is `405`, on both the page route and `/open`.
- Path traversal and symlink escape are the primary risk. Every read goes through the page service's `ReadPage`; the route must not open files itself, and a traversal must be refused before any storage call.
- An unknown vault is a `404`, never a `500`.
- There is no directory listing: a folder path returns `404`, never an index of names.
- Page content is untrusted markdown; the handler must not splice the raw path, vault name or file content into the document unescaped. The renderer escapes body-level HTML, and the document wrapper escapes every value except the renderer output.
- No change to the card link builder, the board's list responses, or any existing route.
- Do not modify `Makefile` or `.github/workflows/ci.yml`.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

```bash
export PATH=/usr/local/go/bin:$PATH
make precommit
GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open'
test "$(grep -c 'github.com/bborbe/vault-markdown v0.1.0' go.mod)" = "1"
```

`make precommit` must pass. The direct `go test` needs the `GOPRIVATE` prefix because the environment does not inherit the `Makefile` export. The `grep -c` re-asserts the renderer pin.

</verification>
