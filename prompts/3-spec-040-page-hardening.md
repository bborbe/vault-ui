---
status: draft
spec: [040-page-view]
created: "2026-11-24T22:20:00Z"
branch: test/spec-040-order-runB
---

# Lock the page routes' read-only boundary and prove nothing regressed

<summary>

- The page routes refuse every write verb, so the read-only promise is enforced rather than implied.
- A traversal request, an empty path, a folder path or an unknown vault is a 404, and a traversal request reads no file at all.
- A page whose body contains a script tag renders without an executable element.
- The page routes sit ahead of the static catch-all, so the board's own paths still reach the static tree.
- The existing task, goal, topic and WebSocket routes are unchanged and still answer.
- The release notes carry one line describing the new in-app page view.

</summary>

<objective>

Harden and prove the page-view boundary: pin the read-only method matrix and the not-found cases (traversal, empty, folder, unknown vault), prove the routes did not displace the static catch-all or any existing API route, and add the release-note entry. This is the negative-evidence prompt: it exists to catch a route that works on the happy path but leaks, swallows or regresses.

</objective>

<context>

Read these before changing anything:

- `pkg/handler/page.go` — `NewPageHandler` and `NewOpenHandler` (from prompt 2). Both claim every method and answer non-GET with `writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")`.
- `pkg/handler/router.go` — the route registrations. The page routes must appear before `router.PathPrefix("/")`; `router.SkipClean(true)` means a traversal request reaches the handler instead of being normalised by the mux.
- `pkg/handler/static.go` — `NewStaticHandler` refuses a path that is not a valid `fs` path, so the catch-all still 404s a traversal request that reaches it.
- `pkg/handler/page_test.go` — the route tests from prompt 2 and their fixture vault. Extend them; do not duplicate the fixture.
- `pkg/pageview/pageview.go` — `splitPagePath` rejects empty, absolute, non-`.md`, `.`/`..` and empty segments before any read; `Render` maps a vault-service not-found to `pageview.NotFoundError`.
- `pkg/handler/api_test.go` — `fakeBoard` and the existing `/api/tasks`, `/api/goals`, `/api/topics` and `/ws` route assertions.
- `CHANGELOG.md` — the `## Unreleased` section convention; the releaser rewrites the file after each release (see `docs/release-process.md`).

Read these coding guides in the container:

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — stdlib `Test…` functions in the handler package, named so the `-run 'Page|Open'` filter runs them.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — the `## Unreleased` bullet format.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — the repo's validation prompt requires a CHANGELOG entry.

<!--
Open questions for the human reviewer:

1. Spec 040's AC7 symlink-escape case names the `ReadPage` guard. That guard now lives in the vault-api service; this tree's page view never opens a file. This prompt pins the route-level evidence (404, zero page reads) and a source assertion that the page view and handlers open no file, and simulates the vault service's 404 for a symlinked path. The production guard is verified by the vault service's own suite.
2. Spec 040's AC8 asks for `git diff origin/master -- pkg/handler/router.go` to show only additions. `.dark-factory.yaml` sets `hideGit: true`, so a bare `git` command cannot run here. This prompt substitutes two container-safe checks: a test that reads `router.go` and asserts every pre-existing route path is still present and that the page routes precede the catch-all, and route tests proving the existing API paths still answer.
3. The Playwright e2e suite (`make e2e`) and the post-deploy curl probes are out of scope for this prompt — they are part of the spec's Verification ladder, not the container run.
-->

</context>

<requirements>

1. Extend `pkg/handler/page_test.go` (stdlib `TestPage…`/`TestOpen…` functions) with the read-only method matrix on both routes:
   - `PUT`, `PATCH` and `DELETE` on `/v/private-personal/page/Alpha.md` → `405`.
   - `PUT`, `PATCH` and `DELETE` on `/open?vault=private-personal&file=Alpha.md` → `405`.

2. Add the not-found matrix (each must be `404`):
   - `GET /v/private-personal/page/../../../../etc/passwd`.
   - `GET /v/private-personal/page/` (empty path — this reaches the static catch-all, which must 404 rather than list anything).
   - `GET /v/private-personal/page/24%20Goals/` (folder path, never a listing).
   - `GET /v/nonexistent/page/Alpha.md` (unknown vault).

3. Prove the traversal request reads nothing. Wrap the fixture `pageview.PageSource` in a counting double that records every `Page` and `Backlinks` call, then assert the traversal request in requirement 2 recorded **zero** calls. The path is rejected before the seam is touched.

4. Prove the page view opens no file itself. Add a source assertion (read the file with `os.ReadFile` from the test) that neither `pkg/pageview/pageview.go` nor `pkg/handler/page.go` contains `os.Open`, `os.ReadFile` or `filepath.EvalSymlinks`. The symlink guard belongs to the vault service; a direct open here would bypass it.

5. Add a symlink-escape route test. In the fixture vault, create a symlink whose name ends in `.md` pointing at a file outside the vault, and have the fixture `PageSource` answer `vaultapi.NotFoundError` for it (that is the vault service's production behaviour). Assert `GET /v/private-personal/page/<symlink>.md` is `404` and the response carries no file contents.

6. Assert the page body cannot execute script: `GET /v/private-personal/page/Echo.md` returns `200` and the body does not contain the literal `<script>alert(1)</script>`.

7. Add a no-regression route test in `pkg/handler/page_test.go` or `pkg/handler/api_test.go`:
   - `GET /` still reaches the static tree and returns the fixture `index.html` body.
   - `GET /api/tasks`, `GET /api/goals`, `GET /api/topics` still answer through the existing `fakeBoard` exactly as before.
   - `GET /ws` is still registered (an unauthenticated request reaches the WebSocket handler rather than the static tree).

8. Add a source-order test (stdlib, in `pkg/handler/page_test.go`) that reads `pkg/handler/router.go` and asserts:
   - `index("Path(\"/v/{vault}/page/")` is less than `index("PathPrefix(\"/\")")`;
   - `index("Path(\"/open\")")` is less than `index("PathPrefix(\"/\")")`;
   - every pre-existing route path string (`/api/tasks`, `/api/goals`, `/api/topics`, `/api/assignees`, `/api/source-status`, `/ws`) still appears exactly once, so an edit cannot silently rewrite an existing registration.

9. Add an `## Unreleased` bullet to `CHANGELOG.md` (create the section above the newest `## vX.Y.Z` heading if absent, matching `docs/release-process.md`): a vault page is now readable inside vault-ui at `/v/{vault}/page/{path}`, `obsidian://open` links redirect to it, and the Obsidian hand-off remains one click away. If an existing `## Unreleased` bullet already covers this change, extend that bullet instead of adding a duplicate.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- The routes stay read-only: `GET` only; every other verb is `405`.
- The page routes must stay registered before `router.PathPrefix("/")`.
- Do not open files, walk the vault or cache anything in the handlers; they call the `pageview.Service` only.
- Do not modify the card link builder, the board's list responses, or any existing route.
- No new configuration key.
- Errors go through `github.com/bborbe/errors`; no `fmt.Errorf`.
- Existing tests must still pass.

</constraints>

<verification>

Run from the repo root; each must exit 0:

- `make precommit`
- `go test ./pkg/handler/ -count=1 -run 'TestPage|TestOpen'`
- `go test ./pkg/pageview/ ./pkg/handler/ -count=1`
- `go test ./... -run 'Page|Open'`
- `test -f pkg/pageview/pageview.go`
- `test -f pkg/handler/page.go`
- `! grep -q 'os.Open\|os.ReadFile\|filepath.EvalSymlinks' pkg/pageview/pageview.go pkg/handler/page.go`

Before finishing, re-run the verification and walk each acceptance criterion against the change.

</verification>

<acceptance_criteria>

- `PUT`, `PATCH` and `DELETE` are `405` on both the page route and `/open`.
- Traversal, empty path, folder path and unknown vault are `404`; the traversal request records zero page reads.
- A symlink pointing outside the vault is `404` with no file contents.
- A body-level `<script>` tag is not emitted as an executable element.
- The static catch-all still serves `/`; `/api/tasks`, `/api/goals`, `/api/topics` and `/ws` still answer.
- A source-order test proves the page routes precede `router.PathPrefix("/")` and every pre-existing route string is still present once.
- `CHANGELOG.md` has an `## Unreleased` entry for the in-app page view.
- `make precommit` exits 0.

</acceptance_criteria>
