---
status: completed
spec: [040-page-view]
summary: Verified pkg/pageview satisfies the render-service contract and completed its Ginkgo coverage (verbatim renderer output, document wrapper/Obsidian link, no-cache Epsilon backlink, embed negative control, raw-HTML escaping), with an Unreleased CHANGELOG entry.
execution_id: e80c0b78-3280-5fa3-b067-ba942945cedc-exec-190-spec-040-page-render-service
dark-factory-version: dev
created: "2026-11-24T15:01:22Z"
queued: "2026-11-24T15:24:01Z"
started: "2026-11-24T15:24:04Z"
completed: "2026-11-24T15:30:01Z"
branch: test/spec-040-order-run2
---

# Render a vault page in-process with the released vault-markdown library

<summary>

- A vault page is rendered to HTML inside vault-ui instead of handing off to Obsidian
- Tables and callouts become real HTML elements, not raw markdown
- Wikilinks to existing pages become links inside the app; links to missing pages stay plain text
- An embedded page is inlined into the page that embeds it
- A backlinks section lists the pages that link to the page being viewed
- The page is wrapped in a small document that loads the board's existing stylesheet
- An "open in Obsidian" link keeps the old hand-off one click away
- The released renderer is the only thing that renders, and it is pinned
- The page view reads through the vault service, so it opens no file and holds no index
- Nothing is cached, so an edit is visible on the next request

</summary>

<objective>

Ensure the in-process page-rendering service turns a vault-relative markdown path into a complete HTML document — rendered body, resolving wikilinks, inlined embeds and a backlinks section — using the released `github.com/bborbe/vault-markdown` renderer and the vault service's page reads, so a page can be read inside vault-ui without launching Obsidian.

</objective>

<context>

There is no `CLAUDE.md` in this repo — the Go binary is the live service. Read these before changing anything.

The page view already exists on this branch: `pkg/pageview/pageview.go` implements the service and `pkg/pageview/pageview_test.go` plus `pageview_seam_test.go` cover it. It reads through `pkg/vaultapi`, not the older `pkg/pageindex` design the spec was written against. Treat the requirements below as the contract to **verify and complete**, not as a reason to rewrite working code. If a behavior is present and its test passes, change only what is missing.

- `pkg/pageview/pageview.go` — the whole service: `Service`, `Render`, `Exists`, `PageURL`, `NotFoundError`, `splitPagePath`, `newRenderer`, `buildDocument`, `pageTemplate`.
- `pkg/pageview/pageview_test.go` and `pkg/pageview/pageview_seam_test.go` — the Ginkgo suite and the recording-source seam spec.
- `pkg/vaultapi/client.go` — `Client.Page`, `Client.Backlinks`, `Page{Vault,Path,Name,Content,Links,Embeds}`, `Backlink{Path,Name}`, `NotFoundError`, the 5 s `requestTimeout`, and the segment-escaping helpers.
- `pkg/vaultapi/source.go` — `Source.Client() Client`, the seam the factory passes in.
- `pkg/board/board.go` — `Vault{Name, VaultName, Path, ...}` (`Name` is the config key, `VaultName` the display name) and `VaultsProvider`.
- `pkg/board/helpers.go` — `board.ObsidianURL(vaultName, filePath string) string`, the exact `obsidian://open?vault=&file=` shape the cards emit.
- `src/vault_ui/static/style.css` — the `.page-view`, `.page-toolbar`, `.backlinks` block the document wrapper relies on; do not add styling.
- `go.mod` — the `github.com/bborbe/vault-markdown v0.1.0` pin.
- `docs/page-index.md` — describes `pkg/pageindex`, which no longer exists in this tree. Treat it as historical, not as the current read contract.

Read these coding guides in the container (the contract for this change):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package (`package pageview_test`), `Describe`/`It`/`DescribeTable`, never stdlib table tests.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — services live in their own package and are wired by a `Create*` function; the handler prompt consumes this service.

The released renderer's API (`github.com/bborbe/vault-markdown` v0.1.0), verified from the module and its in-tree use:

```go
func New(opts ...Option) Renderer
func WithResolver(r Resolver) Option
func WithPages(p Pages) Option
type Resolver interface { Resolve(target string) (string, bool) }
type ResolverFunc func(target string) (string, bool)
type Pages interface { Page(name string) (string, bool) }
type PagesFunc func(name string) (string, bool)
type Renderer interface { Render(ctx context.Context, source string) (string, error) }
func Backlinks(pages map[string]string, target string) []string
```

Behavior of that library, verified from its source: a resolved wikilink renders `<a class="wikilink" href="...">label</a>`; a missing target renders `<span class="wikilink missing">label</span>`; a line holding only `![[page]]` is replaced by that page's body (dropped when `WithPages` is omitted); a `> [!type] Title` blockquote renders `<div class="callout" data-callout="type">` with a `<p class="callout-title">Title</p>`; raw HTML in the source is escaped by goldmark's safe default, so a body-level `<script>` renders as text.

<!-- OPEN QUESTION (for human reviewer): the spec's Problem/Goal/Assumptions describe reading through `pkg/pageindex` (`ListPages` + `ReadPage`, AC3's "exactly one ReadPage per page", AC7's symlink guard). That package is gone; the tree reads through `vaultapi.Client.Page`/`Backlinks`, and the symlink guard now lives in vault-api. Completed prompt 189 and CHANGELOG v0.115.1 record this supersession. The prompts follow the current code. -->

</context>

<requirements>

1. **Confirm the renderer pin.** `go.mod` contains exactly one line matching `github.com/bborbe/vault-markdown v0.1.0`. Do not bump it, do not vendor it, do not add a second renderer.

2. **Keep the service surface.** In `pkg/pageview/pageview.go` the exported contract is:
   ```go
   type Service interface {
       Render(ctx context.Context, vaultName string, pagePath string) (string, error)
       Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
   }
   func NewService(vaults board.VaultsProvider, pages PageSource) Service
   func PageURL(vaultName string, pagePath string) string
   type NotFoundError struct{ Message string }
   ```
   `PageSource` is the read seam, satisfied by `vaultapi.Client`:
   ```go
   type PageSource interface {
       Page(ctx context.Context, vaultName string, pagePath string) (*vaultapi.Page, error)
       Backlinks(ctx context.Context, vaultName string, pagePath string) ([]vaultapi.Backlink, error)
   }
   ```

3. **Render in this order** (`service.Render`):
   1. Resolve the vault from `VaultsProvider.Vaults`: match `vault.Name` or `vault.VaultName`, case-insensitively. Unknown name → `NotFoundError`.
   2. Validate and split the path (`splitPagePath`): reject an empty path, a leading `/`, a path not ending in `.md`, and any segment that is empty, `.` or `..`. Derive the bare page name by trimming `.md`. Any rejection → `NotFoundError` **before any read**.
   3. Read the page with `s.pages.Page(ctx, vault.Name, pagePath)` — pass `vault.Name` (the config key), which is what the vault service is keyed by.
   4. Render the returned `Content` through `markdown.New(...)` (requirement 4).
   5. Read `s.pages.Backlinks(ctx, vault.Name, pagePath)`. A backlink error fails the render; never serve a page with the section silently dropped.
   6. Wrap the rendered body with `buildDocument` (requirement 6).

4. **Wire the renderer from the page read.** `newRenderer` must build:
   - resolver: if `target == pageName` return `PageURL(vaultName, pagePath), true`; else `rel, ok := page.Links[target]` and return `PageURL(vaultName, rel), true`; otherwise `"", false`.
   - pages: if `name == pageName` return `page.Content, true`; else `body, ok := page.Embeds[name]`.
   Use `markdown.WithResolver(markdown.ResolverFunc(...))` and `markdown.WithPages(markdown.PagesFunc(...))`. Both options are required — without `WithPages` the embed is dropped.

5. **Escape the in-app URL per segment.** `PageURL` splits the path on `/`, `url.PathEscape`s each segment, and rejoins with `/`, producing `/v/<vault>/page/<path>`. A space in a folder name becomes `%20` and the slashes stay literal. Both the resolver and the `/open` redirect (prompt 2) call this one helper.

6. **Build the document wrapper.** `pageTemplate` emits: a `<!doctype html>` declaration, `<html lang="en">`, `<meta charset="utf-8">`, a `<title>` carrying the page name, `<link rel="stylesheet" href="/style.css">`, `<main class="page-view">`, a toolbar anchor whose `href` is `board.ObsidianURL(vault.VaultName, pagePath)` (typed `template.URL`, because html/template otherwise renders `#ZgotmplZ` for the `obsidian://` scheme) with the text `Open in Obsidian`, the rendered body inserted as `template.HTML` (the renderer output is trusted), and a `<section class="backlinks">` listing entries. Every value other than the rendered body is escaped by html/template.

7. **Render the backlinks section from the service read.** For each `vaultapi.Backlink`, use `Path` to build `PageURL(vault.Name, link.Path)` and `Name`/basename (trim `.md`) as the label. Skip an entry whose basename equals the requested page name. If the list is empty, still emit the section with an empty list — no special case is required.

8. **Map errors with the repo convention.** `vaultapi.NotFoundError` → `pageview.NotFoundError` as a **value** (so callers classify it with `var nf pageview.NotFoundError; stderrors.As(err, &nf)`). Any other `Page` error → wrapped with `errors.Wrap(ctx, err, ...)` and NOT a `NotFoundError`. A `Backlinks` error → wrapped and returned. No `fmt.Errorf`.

9. **`Exists` reads once.** Resolve the vault and validate the path as in requirement 3, then call `s.pages.Page`. A not-found is `(false, nil)`; any other failure is `(false, err)`. Do not render for `Exists`.

10. **Keep the package free of filesystem reads.** `pkg/pageview/pageview.go` must contain none of `filepath.WalkDir`, `os.ReadFile`, or `markdown.Backlinks`. The page body, links, embeds and backlinks all come from the `PageSource`. A source-reading test in `pageview_seam_test.go` already asserts this — keep it.

11. **Tests (Ginkgo v2 + Gomega, `package pageview_test`).** Keep `pageview_suite_test.go` with `TestPageView`, `RegisterFailHandler(Fail)` and `RunSpecs`; without it every Ginkgo node is silently skipped. Cover, against the fixture vault (`Alpha.md` table + callout + `[[Beta]]`; `Beta.md` plain; `Gamma.md` `![[Beta]]`; `Delta.md` `[[Nope]]`; `Links.md` `[[Alpha]]`; `Echo.md` body `<script>`):
    - the rendered body contains the same output `markdown.New(...).Render(ctx, src)` produces for `Alpha.md` verbatim, plus `<table>` and the callout title text;
    - `Alpha.md` contains an anchor with `href="/v/private-personal/page/Beta.md"`; `Delta.md` contains the text `Nope` and no page-route anchor for `Nope`;
    - `Gamma.md` contains `Beta.md`'s body text (embed inlined);
    - `Alpha.md` names `Links` in the backlinks section; `Beta.md` names `Alpha` and `Gamma` and not `Delta`; creating `Epsilon.md` (linking `Alpha`) after the fixture is built makes the next `Alpha` render name `Epsilon`;
    - the seam spec records exactly one `Page` and one `Backlinks` call per render (assert count and order), on two freshly constructed services;
    - error mapping: not-found → `NotFoundError`; generic error → wrapped, not `NotFoundError`; `Backlinks` failure fails `Render`; `Exists` missing page → `(false, nil)`.

12. **Self-check.** Before finishing, re-run the verification commands below and confirm they pass; walk each acceptance criterion in the spec's AC1, AC3, AC4 and AC5 against the code and tests.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- `github.com/bborbe/vault-markdown` stays pinned at `v0.1.0`; no renderer change and no dependency bump.
- No caching of pages, bodies or rendered documents. Every `Render` reads through the seam.
- No file read in `pkg/pageview`; the vault service owns the read and the symlink-out-of-vault guard.
- No new configuration key; the vault set is the one already configured.
- Errors use `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- Do not add HTTP routing here — that is prompt 2.
- Existing tests must still pass.
- Go error wrapping and Ginkgo conventions follow the coding guides listed in `<context>`.

</constraints>

<verification>

Run from the repo root; each must exit 0:

- `make precommit`
- `go test ./pkg/pageview/... -count=1`
- `go test ./... -run 'Page|Open' -count=1`
- `grep -n 'github.com/bborbe/vault-markdown v0.1.0' go.mod`
- `! grep -qE 'filepath\.WalkDir|os\.ReadFile|markdown\.Backlinks' pkg/pageview/pageview.go`

</verification>
