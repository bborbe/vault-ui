---
status: draft
created: "2026-11-22"
---

## Summary

- Clicking a card on the board today hands the page to Obsidian through an `obsidian://` URL, so reading a page costs an application launch.
- This change adds a read-only page route that renders the page inside vault-ui, using the released `github.com/bborbe/vault-markdown` library (`v0.1.0`).
- Wikilinks resolve through the existing page index and navigate inside vault-ui; a small "open in Obsidian" icon keeps the old hand-off one click away.
- A compatibility route answers the same `?vault=&file=` parameters as `obsidian://open` with a redirect, so links that already exist keep working.
- Nothing about the page is cached: every read parses the page from disk, so an edit is visible on the next request.

## Problem

The board renders lists of tasks, goals and topics, but not the pages themselves. Opening a card leaves the browser: the link is an `obsidian://open?vault=…&file=…` URL built by `obsidianURL` (`pkg/board/helpers.go:136`), whose return on line 137 is the literal. Obsidian then starts, loads its index and renders the note, which is a multi-second cold start on a laptop for a read that only needed to display markdown. The operator's need is read-only viewing — the page is read, not edited — and the browser tab showing the board is already open.

The renderer does not belong in vault-ui: it was built and released separately as `github.com/bborbe/vault-markdown` (`v0.1.0`, annotated tag object `eb96b20` at commit `47e9565`), and its sibling task is complete. This work is the integration only.

## Goal

A vault page is readable inside vault-ui. `GET /v/{vault}/page/{path}` returns the rendered page for a vault-relative markdown path, with tables as `<table>`, callouts as callout elements, `![[embed]]`s inlined, `[[wikilinks]]` as links into the same in-app route, a backlinks section listing the pages that link to it, and a small icon that still opens the page in Obsidian. `GET /open?vault=X&file=Y` answers the same parameters as `obsidian://open` with a `302` to the page route, so existing links keep working. The route is read-only: it serves `GET` and nothing else, and every page read goes through the same on-demand disk read and symlink guard the rest of the board uses.

## Non-goals

- No live refresh of an open page over SSE or WebSocket. The page is rendered per request; reload shows changes.
- No editing, no write verbs, no mutations of any kind on these routes.
- No caching of rendered pages or page bodies. `ReadPage` already reads on demand and caches nothing; this change adds no cache in front of it.
- No change to the card link builder, the board's list responses, or any existing route. The frontend change that makes a card open the page view is a separate deliverable, edited directly.
- No standalone viewer binary and no new process.
- No renderer change. `github.com/bborbe/vault-markdown` is consumed at its released tag as-is.
- No change to vault-cli, and no vault-cli version bump.
- No styling work beyond what the existing static stylesheet already provides for a rendered document.

## Alternatives Considered

- **Render inside vault-ui (chosen).** One process, the existing static stylesheet, and the page read reuses the board's own disk read and symlink guard.
- **Embed a headless Obsidian.** Reuses the renderer the operator already trusts, but drags a second binary, its config and its index in for a read-only view.
- **Shell out to a vault-cli render command.** No such command exists, and adding one to vault-cli widens that repo's surface for a UI concern.
- **Do nothing** — see the Do-Nothing Option below.

## Assumptions

- `github.com/bborbe/vault-markdown` `v0.1.0` is frozen for this work: its API (`New`, `WithResolver`, `WithPages`, `Renderer.Render`, `Backlinks`) does not change while this spec is implemented.
- `pkg/pageindex` stays metadata-only and keeps `ReadPage` as the on-demand disk read; this spec does not change the index.
- The frontend deliverable (card click opening the in-app view) lands separately and does not change these routes' contract.
- The vault set is already configured on the board; no new configuration is introduced.
- The `/open` compatibility route and the backlinks section are in scope by construction: they are the owning task's SC5 and DoD item, so removing them would leave the task's own success criteria unmet.

## Acceptance Criteria

Fixture note: "fixture vault" means a temp directory holding `Alpha.md` (a pipe table, a callout and a `[[Beta]]` link), `Beta.md` (plain text), `Gamma.md` (an `![[Beta]]` embed), `Delta.md` (a `[[Nope]]` link to a page that does not exist), `Links.md` (a `[[Alpha]]` link, so `Alpha` has one backlink) and `Echo.md` (a raw `<script>` tag in the body). Route tests run against the real router built by `CreateHTTPRouter` with an `httptest` server; the page storage seam is the production page-index adapter over the fixture vault. Every new test is named `TestPage…` or `TestOpen…` so the verification filter below actually runs it.

- [ ] **AC1: the released renderer is the one that renders (the load-bearing criterion).** Evidence:
  - `grep -n 'github.com/bborbe/vault-markdown v0.1.0' go.mod` returns exactly one line.
  - The page handler's call site is cited by file:line and that line passes `Render`'s returned string to the response writer.
  - A test renders `Alpha.md`'s source with `markdown.New(markdown.WithResolver(...), markdown.WithPages(...)).Render(ctx, src)` and asserts the `GET /v/private-personal/page/Alpha.md` body **contains** that output verbatim — the document wrapper DB8 requires surrounds it. A handler that echoes the raw markdown fails, because the raw source contains `| --- |` and the rendered body contains `<table>`.
- [ ] **AC2: the page route renders and is read-only.** Evidence, against the fixture vault:
  - `GET /v/private-personal/page/Alpha.md` returns `200`; the body contains `<table>` and a callout element carrying the callout's title text.
  - The same request for `Beta.md` returns `200` and a **different** body — a constant response fails both.
  - `POST /v/private-personal/page/Alpha.md` returns `405`; likewise `PUT`, `PATCH` and `DELETE`.
- [ ] **AC3: wikilinks resolve through the page index.** Evidence:
  - The body for `Alpha.md` contains an anchor whose `href` is `/v/private-personal/page/Beta.md` — the `[[Beta]]` target resolved to the page route, not to `obsidian://`.
  - The body for `Delta.md` (`[[Nope]]`) contains no anchor with a `href` to a page route for `Nope`, and the link text `Nope` is still present.
  - The resolver adds no listing of its own: with the page-index seam wrapped by a counting double, rendering `Alpha.md` performs exactly **one** `ListPages` per folder — the single listing the backlinks pass needs — and exactly one `ReadPage` per page in that folder, with no second read for the same page. A resolver that re-lists per wikilink exceeds the first count; one that re-reads a page exceeds the second.
- [ ] **AC4: embeds are inlined.** Evidence:
  - The body for `Gamma.md` contains `Beta.md`'s body text (`![[Beta]]` inlined).
  - Negative control: with `markdown.WithPages` omitted, the same render **drops** the embed — the test asserts the two bodies differ, so a passing AC4 cannot come from a renderer that ignores the option.
- [ ] **AC5: a backlinks section lists the linking pages.** Evidence:
  - The body for `Alpha.md` contains a backlinks section naming `Links` (the page that links to it) and naming no page that does not link to it.
  - The body for `Beta.md` names `Alpha` and `Gamma` (a plain link and an embed both count) and not `Delta`.
  - `Delta.md` (a link to a missing page) lists no backlinks for `Nope`, and the request still returns `200`.
  - The test then creates `Epsilon.md` (linking `Alpha`) after the fixture is built and asserts `Alpha`'s next response lists `Epsilon` — a name-keyed table over the fixed fixture cannot satisfy this.
- [ ] **AC6: `/open` answers Obsidian's parameters with a redirect.** Evidence:
  - `GET /open?vault=private-personal&file=Alpha.md` returns `302` with `Location: /v/private-personal/page/Alpha.md`.
  - `GET /open?vault=private-personal&file=Missing.md` returns `404`.
  - `GET /open?vault=nonexistent&file=Alpha.md` returns `404` — an unknown vault is a miss, not a `500`.
  - `POST /open?vault=private-personal&file=Alpha.md` returns `405`, so the read-only rule covers both routes.
  - The parameters are read exactly as `obsidian://open` spells them: `vault` and `file`, with `file` URL-encoded (a path containing a space and a slash, e.g. `24%20Goals%2FSome%20Goal.md`, redirects to the corresponding page route).
- [ ] **AC7: the read path refuses traversal and symlink escape.** Evidence:
  - `GET /v/private-personal/page/../../../../etc/passwd` returns `404`, and no file outside the vault is read (a counting reader records 0 reads for that request).
  - A symlink inside the fixture vault pointing outside it, requested by name, returns `404` — the same guard `ReadPage` already applies.
  - `GET /v/private-personal/page/` (empty path), `GET /v/private-personal/page/24%20Goals/` (a folder path, never a listing) and `GET /v/nonexistent/page/Alpha.md` each return `404`.
- [ ] **AC8: nothing existing regresses.** Evidence:
  - `make precommit` exits 0.
  - The new routes are registered **before** `router.PathPrefix("/")` in `pkg/handler/router.go`; a test asserts that a request for a path no page route claims still reaches the static handler and returns the static tree's response, so the catch-all still serves the board.
  - `GET /api/tasks`, `GET /api/goals`, `GET /api/topics` and `GET /ws` are unchanged: after `git fetch origin`, `git diff origin/master -- pkg/handler/router.go` shows only added route registrations and no modified existing line.
  - A page whose **body** contains a raw `<script>` tag renders with that tag escaped — it appears as text, not as an executable element. The body is the surface `goldmark` actually governs; frontmatter is stripped and would prove nothing.
- [ ] **Post-Deploy (Rung-3):** the local launchd install serves the new routes from the merged commit. Evidence: the operator probes below return `302` with the expected `Location` for an existing page, `404` for a missing one, and a real page body contains `<table>`.
  - `deploy_check:` `test "$(git -C ~/Documents/workspaces/vault-ui rev-parse --short HEAD)" = "$(git -C ~/Documents/workspaces/vault-ui rev-parse --short origin/master)" && test "$(stat -f %m ~/Documents/workspaces/go/bin/vault-ui)" -gt "$(git -C ~/Documents/workspaces/vault-ui log -1 --format=%ct)" && echo fresh`
  - `deploy_target:` `fresh`

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format, vet and `go test -race ./...` clean. The `Makefile` exports `GOPRIVATE` for the one dependency whose proxy cache is stale, so this resolves on a clean machine.
- `go test ./... -run 'Page|Open'` — every route and page-service test behind AC1–AC8. Package-agnostic, so the prompt may place the page service in any package.
- `grep -n 'github.com/bborbe/vault-markdown v0.1.0' go.mod` — the pin.

### Operator-executable (runs on the host after PR merge, spec verification ladder)

- `make build` — the binary builds with the embedded static tree.
- Restore the board: `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist` (a booted-out job is not in the domain, so `kickstart -k` cannot revive it).
- `curl -sI '127.0.0.1:8000/open?vault=private-personal&file=24%20Goals%2FVault%20Pages%20Are%20Read%20in%20vault-ui%2C%20Not%20Obsidian.md'` — expect `302` with `Location: /v/private-personal/page/...` (the encoded-slash form AC6 pins).
- `curl -sI '127.0.0.1:8000/open?vault=private-personal&file=Missing.md'` — expect `404`.
- `curl -s '127.0.0.1:8000/v/private-personal/page/<a page with a table>' | grep -c '<table>'` — expect ≥1.
- Open one real page each containing a table, a callout, an `![[embed]]`, a resolving `[[wikilink]]`, a missing link and backlinks; confirm the wikilink stays on `/v/...` and the backlinks section lists entries.

## Desired Behavior

1. A page request resolves `{vault}` to a configured vault and `{path}` to a vault-relative markdown file, then reads that file through the page storage's on-demand `ReadPage` — the same parse path and symlink-out-of-vault guard the board already uses.
2. The page source is rendered by `markdown.New(...).Render(ctx, src)` with two adapters over `pkg/pageindex`: a resolver that maps a wikilink target to its in-app page URL, and a pages source that supplies embed content.
3. A wikilink whose target exists renders as an anchor to `/v/{vault}/page/{resolved-path}`; a wikilink whose target does not exist renders as text with no page link, and the page still returns `200`.
4. An `![[embed]]` is replaced by the embedded page's rendered content.
5. A backlinks section follows the page body, listing the pages that link to it, derived by `markdown.Backlinks(pages, target)` over the name→body map the pages adapter builds from the page index.
6. `GET /open?vault=X&file=Y` redirects `302` to `/v/X/page/Y` when `Y` exists and `404`s when it does not.
7. Every route here serves `GET` only; any other method is `405`, and a path that escapes the vault or names an unknown vault is `404`.
8. The rendered page is wrapped in a document carrying a doctype, `<meta charset="utf-8">`, a link to the existing static stylesheet, a container element, and an "open in Obsidian" link built from the same `obsidian://open?vault=&file=` form the board already emits.

## Constraints

- `github.com/bborbe/vault-markdown` is pinned at `v0.1.0`. Its public API is `New(opts ...Option) Renderer`, `WithResolver(Resolver) Option`, `WithPages(Pages) Option`, `Renderer.Render(ctx, src) (string, error)` and `Backlinks(pages map[string]string, target string) []string`. There is **no** package-level `Render`.
- `pkg/pageindex` holds page **metadata only** — `ListPages` returns pages whose `Content` is always empty, and `ReadPage` reads the full page from disk without caching. A name→body map must therefore be built with `ListPages` plus one `ReadPage` per page; passing the index's own pages as the `Backlinks` map would produce an empty map. `docs/page-index.md` is the authoritative statement of this contract; it is cited, not restated.
- **The dependency is public, but the Go proxy and checksum database still serve cached "not found" results for it.** `github.com/bborbe/vault-markdown` was made public on 2026-11-22 and now fetches anonymously (`git ls-remote` returns its `v0.1.0` tag with no credentials; the GitHub API answers `200`). However `proxy.golang.org/.../@v/v0.1.0.info` and `sum.golang.org/lookup/...` both still return `404`, cached from when the repo was private, so a plain `go mod download` fails on a clean machine. The `Makefile` therefore exports `GOPRIVATE ?= github.com/bborbe/vault-markdown` — fetching this one path straight from GitHub and skipping the checksum database for it — so every `go` target, `make precommit` included, works on a clean machine and inside the prompt container; `.github/workflows/ci.yml` sets the same variable for its direct `go vet` / `go test` calls. Measured locally: `go mod download github.com/bborbe/vault-markdown@v0.1.0` exits **1** with no env and **0** with that variable. Both files are landed directly on the branch, never inside a prompt — the cluster agent's GitHub App holds no `workflows` permission. Remove the variable from both once the proxy serves `.info`.
- The new routes must be registered in `pkg/handler/router.go` **before** `router.PathPrefix("/")`, because that catch-all serves the static tree and would otherwise swallow every page path.
- `router.SkipClean(true)` is in force: a traversal request reaches the handler instead of being normalized by the mux, so the `404` must come from the read guard, not from path cleaning.
- No new configuration key. The routes exist on every board instance; the vault set is the one already configured.
- `make precommit` must stay green; the e2e lane (`make e2e`) is not part of this spec — it needs the live board stopped and belongs to the frontend deliverable.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `{vault}` not in the configured set | `404`, one warning naming the vault | None needed; the caller corrects the URL |
| `{path}` names a file that does not exist | `404`, no error log above V(2) | None needed |
| `{path}` escapes the vault or resolves through a symlink pointing outside it | `404`, and zero file reads | None needed; the guard is the point |
| Page file is unreadable (permissions, encoding) | `500` with the error logged and the path named; no partial body | Operator fixes the file; the next request re-reads |
| Renderer returns an error | `500` with the error logged; the page is not served half-rendered | Re-request after the library is fixed; the pin is the contract |
| Module resolution hits the cached proxy/checksum 404 | The build fails before any test runs, naming `sum.golang.org` | The `Makefile` already exports `GOPRIVATE`; if a tool bypasses it, export it explicitly and re-run. The permanent fix is the proxy indexing the module |
| A page links to itself or a cycle of pages | The wikilink renders as a normal link; no recursion happens, because only the requested page is rendered and embeds are expanded by the library, which bounds its own expansion | None needed |
| The vault root is missing or unmounted | `404` for every page request, one warning naming the vault and the path; the board's list routes keep serving their previous snapshot | Operator remounts the vault; the next request re-reads |
| A very large page | The response is served whole; no truncation and no new limit | None; size is the vault's business |
| Concurrent requests for the same page | Each reads independently; no lock and no shared cache | None needed |

## Security / Abuse Cases

- **Path traversal** is the primary risk: the route takes a path from the URL. Every read goes through `ReadPage`, whose symlink-out-of-vault guard is the single check; the route must not open files itself. AC7 pins the negative evidence (0 reads on a traversal request).
- **No write surface.** The routes accept `GET` only; `POST`/`PUT`/`PATCH`/`DELETE` are `405`. There is no upload, no form, and no state change.
- **HTML injection.** Page content is untrusted markdown, and a vault page can contain `<script>`. The renderer escapes page text; the handler must not splice the raw path, vault name or file content into the document unescaped. AC8 asserts a **body-level** `<script>` tag is escaped — frontmatter is stripped, so a frontmatter assertion would prove nothing.
- **Information disclosure.** The route exposes only pages inside a configured vault, read-only. It must not serve `.bolt` stores, the page-index cache, or anything outside the vault root; the guard covers the last, and the route only ever asks for a `.md` page.
- **No directory listing.** A request for a folder path returns `404`, never an index of names.

## Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Page service: read through `ReadPage`, render with `vault-markdown`, resolver + pages adapters over `pkg/pageindex`, backlinks section | 1–5, 8 | 1, 3, 4, 5 | — |
| 2 | Page route `GET /v/{vault}/page/{path}` + `/open` compatibility redirect, registered before the static catch-all | 6, 7 | 2, 6 | prompt 1 |
| 3 | Read-only and traversal hardening, method rejection, escaping assertion, no-regression checks, post-deploy probe | 7 | 7, 8, 9 | prompts 1, 2 |

Rationale: prompt 1 establishes the render contract and its adapters; prompt 2 exposes it over HTTP; prompt 3 locks the boundary and proves nothing existing moved. The daemon executes in filename order, so this ordering is the dependency order.

## Do-Nothing Option

Cards keep handing off to Obsidian. Every page read pays an application launch and a cold index load, on a machine where the board is already open in a browser tab. The goal `[[Vault Pages Are Read in vault-ui, Not Obsidian]]` cannot meet its first success criterion without this route, so the cost of doing nothing is that the goal stays open and the operator keeps paying a multi-second hand-off for a read-only view.
