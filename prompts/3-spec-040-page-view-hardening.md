---
status: draft
spec: [040-page-view]
created: "2026-11-24T15:01:22Z"
branch: test/spec-040-order-run2
---

# Lock down the page routes against traversal and prove nothing regressed

<summary>

- A path that tries to escape the vault is refused without reading anything
- A folder path, an empty path and an unknown vault all answer not found
- Every non-read verb is refused on both page addresses
- A script tag inside a page body renders as inert text, not an executable element
- A request no page route claims still reaches the static board
- The board's existing endpoints keep their behavior
- The change is documented and recorded in the changelog

</summary>

<objective>

Harden the read path so an untrusted URL path cannot escape the configured vault or cause a file read, and prove that adding the page routes did not change the board's existing endpoints, so the route is safe to expose on every board instance.

</objective>

<context>

There is no `CLAUDE.md` in this repo. Read these before changing anything.

The hardening behavior already exists on this branch (`pkg/pageview/pageview.go`'s `splitPagePath`, `pkg/handler/page.go`, and the tests in `pkg/handler/page_test.go` / `pkg/pageview/pageview_test.go`). Treat the requirements below as the contract to **verify and complete**: if a case is already covered, keep it; add only the missing cases and do not weaken an existing assertion.

- `pkg/pageview/pageview.go` — `splitPagePath` rejects empty, leading-`/`, non-`.md`, and any empty/`.`/`..` segment **before** any read; `service.Render` resolves the vault before the read.
- `pkg/handler/page.go` — both handlers refuse non-GET with 405 and map `pageview.NotFoundError` to 404.
- `pkg/handler/router.go` — `router.SkipClean(true)` means a traversal request reaches the handler instead of being normalized by the mux; the page routes are registered before `router.PathPrefix("/")`.
- `pkg/handler/page_test.go` — the route fixtures and existing `TestPageRoute…` / `TestOpen…` specs, including the static-fallback and script-escape specs.
- `pkg/handler/static.go` — `NewStaticHandler` and `canonicalName`; a request no page route claims falls here and a missing asset is a 404.
- `pkg/handler/api_test.go` — the existing `/api/tasks`, `/api/goals`, `/api/topics` and `/ws` specs; these must stay green unchanged.
- `pkg/vaultapi/client.go` — the vault service owns the symlink-out-of-vault guard now; `Page` answers a not-found for a page it refuses to serve.
- `README.md` — the `### GET /v/{vault}/page/{path}` and `### GET /open` sections already describe the routes; keep them accurate.
- `CHANGELOG.md` — `v0.114.0` records the page routes and `v0.115.1` records the move to the vault service; do not add a duplicate bullet.

Read these coding guides in the container:

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega for `pkg/pageview`, stdlib table style for `pkg/handler` as the surrounding tests use.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md` — the escaping and path-handling expectations.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — the `## Unreleased` entry format.

<!-- OPEN QUESTION (for human reviewer): the spec's AC7 asks that a traversal request record "0 file reads" and that a symlink inside the vault pointing outside it return 404. In this tree the page view no longer opens files at all: `vaultapi.Client.Page` makes an HTTP call and vault-api owns the symlink guard. The prompt therefore proves 0 `Page`/`Backlinks` calls through the recording `PageSource`, and leaves the symlink case to the vault service. Completed prompt 189 and CHANGELOG v0.115.1 record this supersession. -->

<!-- OPEN QUESTION (for human reviewer): `GET /v/private-personal/page/` cannot match the route pattern `{path:.+}` (it requires at least one character), so it falls through to the static catch-all and is a 404 there. The prompt asserts the 404 either way; confirm this routing outcome is acceptable. -->

</context>

<requirements>

1. **Prove no read on a traversal path.** In `pkg/pageview/pageview_test.go` (or the seam test), assert `splitPagePath`/`Render` returns `pageview.NotFoundError` for `../../../../etc/passwd`, an empty path, `Alpha` (no `.md`), a leading `/`, and a folder path ending in `/`. Add a route-level test in `pkg/handler/page_test.go` that builds the **real** `pageview.NewService` over a recording `PageSource` (so the real `splitPagePath` runs) and drives it through `CreateHTTPRouter`: `GET /v/private-personal/page/../../../../etc/passwd` returns 404 **and** the recording source records zero `Page` calls (the rejection happens before the read). Do not open a file in the test to prove it — the double's call count is the evidence.

2. **Cover the not-found shapes on the page route.** With the real router over the fixture vault, assert:
   - `GET /v/private-personal/page/` → 404 (it falls to the static catch-all; assert the status, not which handler);
   - `GET /v/private-personal/page/24%20Goals/` → 404 (folder path, never a listing);
   - `GET /v/nonexistent/page/Alpha.md` → 404 (unknown vault);
   - `GET /v/private-personal/page/Alpha.md` → 200, as the positive control.

3. **Re-assert the read-only rule on both routes.** A table over `POST`, `PUT`, `PATCH`, `DELETE` for both `/v/private-personal/page/Alpha.md` and `/open?vault=private-personal&file=Alpha.md` asserts 405. Do not add any handler branch that accepts a write verb.

4. **Prove body-level escaping.** `GET /v/private-personal/page/Echo.md` → 200; the body must not contain `<script>alert(1)</script>` and must not contain `alert(1)`; it must contain `<!-- raw HTML omitted -->` (goldmark's safe-default marker). This is a **body** assertion — frontmatter is stripped, so a frontmatter-only check proves nothing.

5. **Prove the static fallback.** `GET /` returns the static index (200, body from the fixture static tree). A request for a path no page route claims (for example `/style.css` or `/nonexistent-static-asset`) reaches `NewStaticHandler`; assert its response is the static handler's, not a page-route 500. This is the evidence that the page routes did not swallow the catch-all.

6. **Non-regression.** Run the existing `pkg/handler` API specs unchanged. Do not modify any existing route registration or handler for `/api/tasks`, `/api/goals`, `/api/topics`, `/api/assignees`, `/api/source-status`, `/api/topics/{topic_id}`, `/ws`, or the mutation routes. If a test must change because a shared fixture moved, change only the fixture wiring, never an assertion about those endpoints.

7. **Documentation and changelog.** Confirm `README.md` still describes both routes accurately (a route path, the `302` compatibility behavior, the `GET`-only rule, and the 404 shapes). Do not rewrite the section if it is correct. `CHANGELOG.md` already records the page routes (`v0.114.0`) and the vault-service move (`v0.115.1`); do not add a duplicate entry. If a genuine gap exists, add one bullet under `## Unreleased`.

8. **Self-check.** Before finishing, re-run the verification commands below and confirm they pass; walk the spec's AC7 and AC8 against the code and tests.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- No write surface: the routes accept `GET` only; there is no upload, form or state change.
- No directory listing: a folder path is a 404, never an index of names.
- No file outside a configured vault may be served; path validation happens before any read, and the vault service owns the symlink guard.
- No new configuration key; the routes exist on every board instance.
- Errors use `github.com/bborbe/errors`; no `fmt.Errorf`.
- Do not edit `Makefile` or `.github/workflows/ci.yml` — the `GOPRIVATE` export for the pinned renderer is already landed on the branch.
- Existing tests must still pass; the e2e lane is out of scope for this prompt.
- Do not run `git` commands; `.git` is masked in the container, so a git-based non-regression check cannot prove anything here.

</constraints>

<verification>

Run from the repo root; each must exit 0:

- `make precommit`
- `go test ./pkg/handler/... -count=1`
- `go test ./... -run 'Page|Open' -count=1`
- `! grep -qE 'fmt\.Errorf' pkg/pageview/pageview.go pkg/handler/page.go`
- `grep -qF 'GET /v/{vault}/page/{path}' README.md`
- `grep -qF 'GET /open' README.md`
- `grep -q 'github.com/bborbe/vault-markdown v0.1.0' go.mod`

</verification>
