---
status: completed
spec: [040-page-view]
summary: 'Completed pkg/pageview: kept the in-process renderer, made the fixture source read the vault per call, added nested fixture plus verbatim/embed/backlink/escaping specs, wrapped outer-scope errors per the guide, and added an Unreleased changelog entry.'
execution_id: e0476470-c010-5f79-942d-90667c89a3ae-exec-186-spec-040-page-service
dark-factory-version: dev
created: "2026-11-24T22:10:00Z"
queued: "2026-11-24T21:19:25Z"
started: "2026-11-24T21:19:28Z"
completed: "2026-11-24T21:27:14Z"
branch: test/spec-040-order-runB
---

# Render a vault page in-process with the released renderer

<summary>

- A vault page can be rendered to HTML inside vault-ui instead of handing the read off to Obsidian.
- Tables, callouts, embeds and wikilinks render exactly the way the released markdown renderer already renders them.
- A wikilink resolves to the in-app page route; a link to a page that does not exist stays plain text and the page still loads.
- Every rendered page carries a backlinks section listing the pages that link to it, including pages created after the process started.
- The rendered page is wrapped in a small document that loads the board's existing stylesheet and keeps a one-click "open in Obsidian" link.
- Page content is untrusted: a raw HTML tag in a page body is never emitted as an executable element.
- Each page read is on demand and uncached, so an edit is visible on the next render.
- The page view costs exactly two reads per render and never walks the vault or opens a file itself.

</summary>

<objective>

Build the in-process page rendering service so a vault page can be displayed inside vault-ui without launching Obsidian. The service reads one page plus its backlinks through the existing vault-service seam, renders the page with the released `github.com/bborbe/vault-markdown` renderer, and returns a complete HTML document with wikilinks, inlined embeds and a backlinks section.

</objective>

<context>

There is no `CLAUDE.md` in this repo; the Go binary is the live service. Read these before changing anything:

- `pkg/pageview/pageview.go` — if this file exists, it is the current implementation of the service this prompt specifies. Read it first and keep every behaviour that already satisfies the contract below; make the smallest change that closes the gap. Do not duplicate it in a second package.
- `pkg/pageview/pageview_test.go`, `pkg/pageview/pageview_seam_test.go`, `pkg/pageview/pageview_suite_test.go` — if present, the existing Ginkgo suite and its fakes. Extend them rather than deleting coverage.
- `pkg/vaultapi/client.go` — the page read seam. `Client` already declares `Page(ctx, vaultName, pagePath) (*Page, error)` and `Backlinks(ctx, vaultName, pagePath) ([]Backlink, error)`. `Page` carries `Vault`, `Path`, `Name`, `Content`, `Links map[string]string` (wikilink target → vault-relative path) and `Embeds map[string]string` (page name → body). `Backlink` carries `Path` and `Name`.
- `pkg/vaultapi/source.go` — `Source.Client()` returns the `Client` the service reads through.
- `pkg/board/board.go` — `VaultsProvider` is `Vaults(ctx) ([]Vault, error)`; `Vault` carries `Name` (vault-cli/config key) and `VaultName` (display name used by the board's Obsidian links).
- `pkg/board/helpers.go` — `board.ObsidianURL(vaultName, filePath string) string` builds the existing `obsidian://open?vault=…&file=…` link. Reuse it; do not re-implement the quoting.
- `src/vault_ui/static/style.css` — the existing stylesheet the document must link to as `/style.css`.
- `docs/page-index.md` — background only. Its `pkg/pageindex` contract no longer exists in this tree; page reads now go through vault-api. Do not add a page index.

Read these coding guides in the container (they are the contract for this change):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `DescribeTable`/`Entry`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md` — `glog` for the warnings this package logs.

<!--
Open questions for the human reviewer (non-interactive run; the spec is stale in three places):

1. Spec 040 names `pkg/pageindex` (`ListPages` + `ReadPage`) as the read seam. That package was deleted in commit bf58dfd ("delete pkg/pageindex, pkg/statuscache and pkg/watcher"); page reads now go through the local vault-api service. This prompt therefore uses the vault-api seam, and AC3's "one ListPages per folder / one ReadPage per page" is replaced by "exactly one Page and one Backlinks call per render", which is the same no-redundant-read guarantee.
2. Spec 040 pins `github.com/bborbe/vault-markdown v0.1.0`; this tree already requires `v0.1.2` (prompt 190). Do not downgrade.
3. Spec 040's AC7 symlink-escape assertion moves with the file read: the vault service owns that guard now. The page view still validates the path before any read and never opens a file itself; the route-level negative evidence stays in prompt 3.
-->

</context>

<requirements>

1. Create package `pkg/pageview` with this seam and contract (import paths: `context`, `github.com/bborbe/vault-ui/pkg/board`, `github.com/bborbe/vault-ui/pkg/vaultapi`, `github.com/bborbe/vault-markdown` aliased as `markdown`):

   ```go
   // PageSource is the vault-service read seam; *vaultapi.Client satisfies it.
   type PageSource interface {
       Page(ctx context.Context, vaultName string, pagePath string) (*vaultapi.Page, error)
       Backlinks(ctx context.Context, vaultName string, pagePath string) ([]vaultapi.Backlink, error)
   }

   // NotFoundError reports a vault, path or page the route cannot serve as a 404.
   // Returned as a value so callers classify it with stdlib errors.As.
   type NotFoundError struct{ Message string }

   func (e NotFoundError) Error() string { return e.Message }

   type Service interface {
       Render(ctx context.Context, vaultName string, pagePath string) (string, error)
       Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
   }

   // PageURL returns /v/{vault}/page/{path}, escaping each path segment and
   // preserving the slashes between segments.
   func PageURL(vaultName, pagePath string) string

   func NewService(vaults board.VaultsProvider, pages PageSource) Service
   ```

2. `Render(ctx, vaultName, pagePath)` must run these steps in order:
   - Resolve `vaultName` against the configured set from `vaults.Vaults(ctx)`. Accept a case-insensitive match on either `Vault.Name` or `Vault.VaultName` — the board's card links and real `obsidian://` links carry the display name, while the config key is `Name`. An unknown name is a `NotFoundError`; a provider error is wrapped with `errors.Wrap`.
   - Validate `pagePath` before any read: reject empty, a leading `/`, a path not ending in `.md`, and any empty, `.` or `..` segment, each as a `NotFoundError`. Derive the bare page name from the final segment with the `.md` suffix removed.
   - Call `pages.Page(ctx, vault.Name, pagePath)` using the config key (`vault.Name`), not the display name. Map `vaultapi.NotFoundError` to `pageview.NotFoundError` with a `"Page not found: "` message; wrap any other error and do **not** classify it as not-found.
   - Render the page with `markdown.New(markdown.WithResolver(...), markdown.WithPages(...))`. Use `markdown.ResolverFunc` and `markdown.PagesFunc`:
     - resolver: a target equal to the page's own name resolves to `PageURL(vault.Name, pagePath)`; otherwise `rel, ok := page.Links[target]` resolves to `PageURL(vault.Name, rel)`; otherwise `("", false)`.
     - pages: a name equal to the page's own name returns `page.Content`; otherwise `body, ok := page.Embeds[name]`.
     - `Renderer.Render(ctx, page.Content)` returns the rendered body; wrap its error with `errors.Wrap`.
   - Call `pages.Backlinks(ctx, vault.Name, pagePath)`. A failure fails the render with a wrapped error — serving a body with no backlinks section is a silent regression.
   - Wrap the rendered body with the document described in requirement 3.

3. The document wrapper must contain, in order: `<!doctype html>`, a `<head>` with `<meta charset="utf-8">`, a `<title>` holding the page name, and `<link rel="stylesheet" href="/style.css">`; a `<body>` with a `<main class="page-view">` container; a toolbar anchor with class `obsidian-link` whose `href` is `board.ObsidianURL(vault.VaultName, pagePath)` and whose text is `Open in Obsidian`; the rendered body; and a `<section class="backlinks">` with `<h2>Backlinks</h2>` and one `<a>` per backlink whose `href` is `PageURL(vault.Name, backlink.Path)` and whose label is the backlink's basename without `.md`. Drop a backlink whose basename equals the page's own name.
   - Insert only the renderer output as `template.HTML`. Every other value (title, backlink label, path) must go through `html/template` escaping.
   - The Obsidian link must be typed `template.URL`, otherwise `html/template` refuses the `obsidian://` scheme and emits `#ZgotmplZ`.
   - Build the document with `html/template` (no hand-built string concatenation of untrusted values).

4. `Exists(ctx, vaultName, pagePath)` resolves the vault and validates the path exactly as `Render` does, then reads the page once. A not-found page returns `(false, nil)`; any other error returns `(false, err)`.

5. Tests — `pkg/pageview` (Ginkgo v2 + Gomega, external package `pageview_test`):
   - Add `pkg/pageview/pageview_suite_test.go` with `func TestPageView(t *testing.T)`, `RegisterFailHandler(Fail)`, `RunSpecs`, and `suiteConfig.Timeout = 2 * time.Minute`. Without this file every Ginkgo node in the package is silently skipped.
   - Build a fixture vault in a temp dir with: `Alpha.md` (a pipe table, a `> [!note] Important Title` callout, and a `[[Beta]]` link), `Beta.md` (plain text), `Gamma.md` (`![[Beta]]`), `Delta.md` (`[[Nope]]`), `Links.md` (`[[Alpha]]`), `Echo.md` (a raw `<script>alert(1)</script>` in the body), and a nested `24 Goals/Some Goal.md`.
   - The fixture `PageSource` must read the vault directory on each call (not build a fixed name→body table at construction), so a page written after the fixture is built is visible on the next render.
   - Assert: the document for `Alpha.md` contains the output of `markdown.New(markdown.WithResolver(...), markdown.WithPages(...)).Render(ctx, alphaSource)` verbatim, and contains `<table>` and `Important Title` (AC1/AC2).
   - Assert: `Beta.md` returns a different body from `Alpha.md`.
   - Assert: the `Alpha.md` body contains `href="/v/private-personal/page/Beta.md"` (AC3). The `Delta.md` body contains the text `Nope` and no `href` to a page route for `Nope`.
   - Add a recording `PageSource` (records method + arguments) and assert a single `Render` makes exactly `[Page(vault, path), Backlinks(vault, path)]` — count and order — on a freshly constructed service, and again on a second freshly constructed service (no hidden cache). Add a source assertion that `pkg/pageview/pageview.go` contains none of `filepath.WalkDir`, `os.ReadFile`, `markdown.Backlinks`, `indexTTL` (AC3, adapted from the spec's ListPages/ReadPage counts).
   - Assert: `Gamma.md` contains `Beta body text.` (AC4). Negative control: the same source rendered with a renderer built without `markdown.WithPages` does not contain it, so the two bodies differ.
   - Assert backlinks (AC5): `Alpha.md` names `Links` and no non-linking page; `Beta.md` names `Alpha` and `Gamma` and not `Delta`; `Delta.md` lists no backlink for `Nope` and still renders. Then write `Epsilon.md` linking `Alpha` after the fixture is built and assert `Alpha`'s next render lists `Epsilon`.
   - Assert `Echo.md` does not contain the literal `<script>alert(1)</script>` (AC8 body-level escaping).
   - Assert `PageURL` table: `Beta.md` → `/v/private-personal/page/Beta.md`; `24 Goals/Some Goal.md` → `/v/private-personal/page/24%20Goals/Some%20Goal.md`.
   - Assert error mapping: `vaultapi.NotFoundError` from `Page` becomes a `pageview.NotFoundError`; a generic `Page` error is wrapped and is **not** a `pageview.NotFoundError`; a `Backlinks` error fails `Render`; `Exists` on a missing page is `(false, nil)`.
   - Assert the vault resolves by config key, display name and upper-cased display name.

6. Confirm `go.mod` requires `github.com/bborbe/vault-markdown v0.1.2` and leave the version unchanged. Its public API used here is `New(opts ...Option) Renderer`, `WithResolver(Resolver) Option`, `WithPages(Pages) Option`, `ResolverFunc`, `PagesFunc`, and `Renderer.Render(ctx, src) (string, error)`; there is no package-level `Render`. `markdown.Backlinks(pages map[string]string, target string) []string` exists but must not be called from production code.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- `github.com/bborbe/vault-markdown` is consumed at its released tag; do not change the version and do not vendor the module.
- Page reads go through the `PageSource` seam only. Do not open a file, walk the vault, or read `pkg/pageindex` from this package.
- No caching of page bodies or rendered output. Every `Render` reads on demand.
- No new configuration key; the vault set is the one already configured.
- Errors go through `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- No `time.Now()`, no new package-level mutable state, no new `go func()`.
- Keep the vault-service's own 5 s request timeout; do not add a retry, cache or fallback.
- Do not modify `pkg/handler`, `pkg/factory`, or the router — wiring is the next prompt's job.
- Existing tests must still pass.

</constraints>

<verification>

Run from the repo root; each must exit 0:

- `make precommit`
- `go test ./pkg/pageview/ -count=1`
- `go test ./... -run 'Page|Open'`
- `grep -n 'github.com/bborbe/vault-markdown v0.1.2' go.mod` — prints exactly one line
- `test -f pkg/pageview/pageview.go`
- `! grep -q 'filepath.WalkDir\|os.ReadFile\|markdown.Backlinks' pkg/pageview/pageview.go`

Before finishing, re-run the verification and walk each acceptance criterion against the change.

</verification>

<acceptance_criteria>

- `pkg/pageview` exposes `PageSource`, `NotFoundError`, `Service`, `PageURL`, `NewService` with the signatures above.
- A render of `Alpha.md` contains the released renderer's output verbatim, a `<table>` and the callout title.
- A wikilink resolves to `/v/{vault}/page/{path}`; a missing target stays plain text and the page still renders.
- An embed is inlined, and a renderer built without `markdown.WithPages` drops it (negative control).
- Backlinks list the linking pages and pick up a page created after the fixture is built.
- A recording source proves exactly one `Page` and one `Backlinks` call per render.
- A body-level `<script>` tag is never emitted as an executable element.
- `make precommit` exits 0.

</acceptance_criteria>
