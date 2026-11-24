---
status: draft
spec: [040-page-view]
created: "2026-11-24T19:37:00Z"
branch: test/spec-040-order-runA
---

# Lock the page route's read-only and traversal guarantees

<summary>

- A path that tries to escape the vault is refused without reading any page.
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
- `pkg/pageview/pageview.go` — `Service`, `PageSource`, `NotFoundError`, `PageURL`, and the path validation (`splitPagePath`) that rejects `..`/`.`/empty segments before any seam call.
- `pkg/vaultapi/client.go` — `Client.Page`/`Backlinks`; the vault service owns the file read and the symlink-out-of-vault guard, and reports a missing page or an escaped symlink as `vaultapi.NotFoundError`.
- `pkg/handler/page_test.go` (built by the previous prompt) — the fixture vault, `pageTestSource`, `newPageTestRouter`, `doPageRequest`; extend them rather than duplicating.
- `pkg/handler/api_test.go` — `fakeBoard`, `fakeMutations`, the `fstest.MapFS` static tree (`index.html`, `app.js`, `style.css`), and `newStubPageService`.
- `pkg/handler/static.go` — the static handler and its `canonicalName` guard.

Coding-plugin guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`

The verification filter selects top-level tests named `TestPage...` or `TestOpen...`. The handler package already owns exactly one Ginkgo `RunSpecs` (under `TestSuite`), so add plain-`testing` functions, not a second suite.

Verified renderer behaviour that the escaping test relies on: `github.com/bborbe/vault-markdown` v0.1.2 renders through goldmark with its safe default, so a raw `<script>` in the page body is dropped and replaced by `<!-- raw HTML omitted -->`. It is not emitted as an executable element. Assert the security property (no executable `<script>` element reaches the response), not a particular escaping form.

<!-- Reviewer note: spec 040's AC7 pins a symlink-escape `404` against a local file read. The read and its guard now live in vault-api; the page view maps vault-api's `NotFoundError` to `404`. The symlink test below therefore models the vault-api contract in the fixture source and asserts the route's mapping, while the traversal test proves the page view's own pre-read path validation. AC8's origin/master diff check and the post-deploy probe are operator-side rungs on the spec; this prompt proves the equivalent property with the in-container static-fallback test. -->

</context>

<requirements>

1. **Traversal costs zero reads.** Add `TestPageTraversalRefused`. Wrap the fixture `pageTestSource` in a counting `pageview.PageSource` that increments a `Page` counter and a `Backlinks` counter before delegating; build the router with `pageview.NewService(vaults, counter)`. Assert `GET /v/private-personal/page/../../../../etc/passwd` returns `404` and the double recorded **zero** `Page` and zero `Backlinks` calls. Do not assert only the status: the zero-read count is the point.

2. **Symlink escape is refused.** Extend the fixture `pageTestSource.Page` so that a requested file which is a symlink resolving outside `vaultDir` returns `vaultapi.NotFoundError` (this models the vault-api guard the page view relies on). Add `TestPageSymlinkEscapeRefused`: create a file outside the fixture vault and a symlink inside it pointing at the outside file (for example `escape.md`), request the symlink by name (`GET /v/private-personal/page/escape.md`), and assert `404`. Add a comment that the guard itself is vault-api's; the page view's contract is to map its not-found to `404`.

3. **Empty, folder and unknown-vault paths are 404.** Add tests asserting:
   - `GET /v/private-personal/page/` returns `404` (it does not match the page route and falls to the static handler);
   - `GET /v/private-personal/page/24%20Goals/` returns `404` — the fixture already holds `24 Goals/Some Goal.md`, so this is a real folder, and the response must never be a listing;
   - `GET /v/nonexistent/page/Alpha.md` returns `404`, not `500`.

4. **The full method matrix is refused on both routes.** Add `TestOpenMethodsRefused` asserting `PUT`, `PATCH` and `DELETE` on `/open?vault=private-personal&file=Alpha.md` each return `405`. (`POST` on `/open` and the four verbs on the page route are already covered by the previous prompt.)

5. **Body-level script cannot execute.** Add `TestPageEscapesScript`. `GET /v/private-personal/page/Echo.md` (whose body holds a raw `<script>alert(1)</script>`) must return `200`, and the body must **not** contain a literal `<script>` element and must **not** contain `alert(1)`. Assert on the body, not the frontmatter — frontmatter is stripped and would prove nothing.

6. **Static fallback still works.** Add `TestPageStaticFallback`. Build the router with an `fstest.MapFS` static tree containing `index.html` (`<html>index</html>`) and request a path no page route claims (`GET /`); assert it returns the static tree's response (`200`, body contains `index`). This proves the new routes were registered before the catch-all and did not shadow it.

7. **Existing addresses still answer.** Add `TestPageExistingRoutesUnchanged`. Build a router with a `fakeBoard` initialized the way `api_test.go`'s `BeforeEach` does (empty slices, and `taskBody: board.TaskListBody{Identity: []byte("[]"), Gzipped: []byte("[]")}`) and assert `GET /api/tasks`, `GET /api/goals` and `GET /api/topics` each return `200`, and that `GET /ws` is **not** `404` (a plain request without upgrade headers proves the route is still registered). Do not modify any existing route registration line; the page and `/open` registrations are the only additions.

8. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and the spec's AC7 and AC8 against the change.

</requirements>

<constraints>

- The routes are read-only: `GET` only; every other method is `405`, on both the page route and `/open`.
- Path traversal and symlink escape are the primary risk. Every read goes through the page service's `PageSource`; the route must not open files itself, and a traversal must be refused before any seam call.
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

Run from the repo root; each must exit 0:
- `make precommit`
- `GOPRIVATE=github.com/bborbe/vault-markdown go test ./pkg/handler/ ./pkg/pageview/ -count=1`
- `GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open' -count=1`
- `test "$(grep -c 'github.com/bborbe/vault-markdown v0.1.2' go.mod)" = "1"`

`make precommit` runs format, vet and `go test -race ./...`. The direct `go test` commands carry the `GOPRIVATE` prefix because the shell does not inherit the `Makefile` export. The `grep -c` re-asserts the renderer pin.

</verification>
