---
status: completed
spec: [040-page-view]
summary: Reconciled pkg/pageview to the vault-api Page/Backlinks seam at vault-markdown v0.1.2, completed the nested fixture and page-view CHANGELOG entry, and verified make precommit exits 0
execution_id: 51b1babd-1ad1-524e-9756-485d1d5e2f1e-exec-186-spec-040-page-service
dark-factory-version: dev
created: "2026-11-24T19:35:00Z"
queued: "2026-11-24T19:41:58Z"
started: "2026-11-24T19:42:01Z"
completed: "2026-11-24T19:48:18Z"
branch: test/spec-040-order-runA
---

# Render a vault page in-process with the released vault-markdown library

<summary>

- A vault page can be read inside vault-ui instead of being handed off to Obsidian.
- Tables, callouts and embedded pages become real HTML elements rather than raw markdown.
- Links to existing pages become links inside the app; links to missing pages stay as plain text and the page still renders.
- A backlinks section lists the pages that link to the one being read.
- Every render reads the page fresh, so an edit is visible on the next request; nothing is cached.
- The rendered page is wrapped in a small document that loads the board's existing stylesheet and keeps an "open in Obsidian" link one click away.
- The released renderer is the only thing that renders the page, and its output is inserted verbatim.
- An unknown vault, a missing page, a folder path and a path that tries to leave the vault are reported as not found, never as server errors.

</summary>

<objective>

Build (or reconcile) `pkg/pageview` as a read-only page-rendering service: read one page through the existing vault-service read seam, render it with the released `github.com/bborbe/vault-markdown` renderer, resolve wikilinks and embeds from the page read's own link and embed maps, append a backlinks section read from the same service, and wrap the result in a document carrying the board's stylesheet and an Obsidian hand-off link. This is the integration only; the renderer is consumed at its released tag, unchanged.

</objective>

<context>

There is no `CLAUDE.md` in this repo. Read these before writing anything.

Project files:

- `pkg/vaultapi/client.go` — the page-read seam this service consumes. Verified API:
  - `type Client interface { ...; Page(ctx context.Context, vaultName string, pagePath string) (*Page, error); Backlinks(ctx context.Context, vaultName string, pagePath string) ([]Backlink, error) }`
  - `type Page struct { Vault string; Path string; Name string; Content string; Links map[string]string; Embeds map[string]string }` (JSON tags `vault`, `path`, `name`, `content`, `links`, `embeds`)
  - `type Backlink struct { Path string; Name string }` (JSON tags `path`, `name`)
  - `type NotFoundError struct{ Message string }` with `func (e NotFoundError) Error() string`
  - `Links` maps a wikilink target name to that target's vault-relative path; `Embeds` maps an embedded page name to its body. A missing page or unknown vault comes back as `vaultapi.NotFoundError`.
- `pkg/board/board.go` — `type Vault struct { Name, VaultName, Path, TasksFolder, GoalsFolder, TopicsFolder, ClaudeScript, SessionProjectDir string }` and `type VaultsProvider interface { Vaults(ctx context.Context) ([]Vault, error) }`.
- `pkg/board/helpers.go` — `func ObsidianURL(vaultName, filePath string) string` builds the exact `obsidian://open?vault=...&file=...` shape the board's cards already emit.
- `pkg/pageview/pageview.go`, `pkg/pageview/pageview_test.go`, `pkg/pageview/pageview_seam_test.go`, `pkg/pageview/pageview_suite_test.go` — the current service and its specs. This prompt owns this package; keep the public names `PageURL`, `NotFoundError`, `Service`, `Render`, `Exists`, `NewService`, `PageSource`.
- `pkg/handler/page.go` — the HTTP handlers that consume `pageview.Service` (`Render`, `Exists`, `PageURL`, `NotFoundError`). Do not change them here; they are the next prompt's file.

Verified library API (`github.com/bborbe/vault-markdown` v0.1.2, from the module source):

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

There is **no** package-level `Render`. `Renderer.Render` passes the full raw source (frontmatter included) through the resolver and pages adapters. Raw HTML in the source is dropped by goldmark's safe default (a body-level `<script>` becomes `<!-- raw HTML omitted -->`, never an executable element).

Coding-plugin guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package (`package pageview_test`), `Describe`/`It`/`DescribeTable`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.

<!-- Reviewer note: spec 040 was written against `pkg/pageindex` (`ListPages` + `ReadPage` per page). That package was deleted when page reads moved to the vault service (commit bf58dfd); the actual seam on this branch is `pkg/vaultapi.Client.Page`/`Backlinks`, and the renderer is pinned at v0.1.2, not v0.1.0. Consequences for the spec's acceptance criteria:
  - AC1: the pin line in go.mod is `github.com/bborbe/vault-markdown v0.1.2`.
  - AC3's "exactly one ListPages per folder and one ReadPage per page" maps to "exactly one Page and one Backlinks call per render" (the vault service does the listing and reading; the page view holds no index).
  - AC7's symlink-escape guard now lives in vault-api. The page view's own contribution is path validation before any read (traversal, folder, empty, non-.md); the symlink refusal is a vault-api contract and is not re-tested in this package.
  - `docs/page-index.md` describes the deleted `pkg/pageindex`; do not treat it as the seam contract for this package. -->

</context>

<requirements>

1. **Confirm the dependency pin.** `go.mod` must carry exactly one line matching `github.com/bborbe/vault-markdown v0.1.2`:
   ```bash
   grep -n 'github.com/bborbe/vault-markdown v0.1.2' go.mod
   ```
   If the module is absent, add it with the `GOPRIVATE` prefix (the proxy and checksum database still serve a cached "not found" for this module):
   ```bash
   GOPRIVATE=github.com/bborbe/vault-markdown go get github.com/bborbe/vault-markdown@v0.1.2
   GOPRIVATE=github.com/bborbe/vault-markdown go mod tidy
   ```
   Do not edit `Makefile` or `.github/workflows/ci.yml`; both already export `GOPRIVATE` and are landed directly on the branch.

2. **`pkg/pageview/pageview.go` — the read seam.** Declare:
   ```go
   // PageSource is the vault-service read seam. It is satisfied by
   // vaultapi.Client, so the service holds no index and reads no file.
   type PageSource interface {
       Page(ctx context.Context, vaultName string, pagePath string) (*vaultapi.Page, error)
       Backlinks(ctx context.Context, vaultName string, pagePath string) ([]vaultapi.Backlink, error)
   }
   ```

3. **`NotFoundError` and `Service`.** Declare, returned as a value so callers classify it with `stderrors.As`:
   ```go
   type NotFoundError struct{ Message string }
   func (e NotFoundError) Error() string { return e.Message }

   type Service interface {
       Render(ctx context.Context, vaultName string, pagePath string) (string, error)
       Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
   }
   ```

4. **`PageURL`.** `func PageURL(vaultName, pagePath string) string` splits `pagePath` on `/`, escapes each segment with `url.PathEscape`, and rejoins with `/`, preserving the slashes between segments. `PageURL("private-personal", "24 Goals/Some Goal.md")` must be `/v/private-personal/page/24%20Goals/Some%20Goal.md`. The result is `/v/` + escaped vault + `/page/` + escaped path.

5. **`NewService`.** `func NewService(vaults board.VaultsProvider, pages PageSource) Service` — keep the two-argument shape; return a `service` struct holding both.

6. **`Render(ctx, vaultName, pagePath)` sequence — keep the reads exact:**
   a. Resolve `vaultName` against `s.vaults.Vaults(ctx)` with `strings.EqualFold` against `Vault.Name` **or** `Vault.VaultName`. The board's Obsidian deep links carry a display name while the config key is another name, so accepting both (case-insensitively) is what makes the `/open` compatibility route serve links that already exist. A `Vaults` error is wrapped with `errors.Wrap(ctx, err, "load vaults")`. An unknown name logs `glog.Warningf("unknown vault: %s", vaultName)` and returns `NotFoundError{Message: "Unknown vault: " + vaultName}`.
   b. Validate `pagePath` **before any seam call**: non-empty, no leading `/`, ends in `.md`, every `/`-separated segment non-empty and not `.` or `..`, and the last segment minus `.md` non-empty. A failure returns `NotFoundError{Message: "Page not found: " + pagePath}`. This pre-read guard is what makes a traversal request cost zero reads.
   c. Split `pagePath` into `pagesDir` (everything before the last `/`; `""` at the root) and `name` (the last segment minus `.md`).
   d. `page, err := s.pages.Page(ctx, vault.Name, pagePath)` — the same vault name field the current wiring passes to the vault service. A `vaultapi.NotFoundError` (classify with `stderrors.As`) maps to `NotFoundError{Message: "Page not found: " + pagePath}`; any other error is returned wrapped with `errors.Wrap(ctx, err, "read page")` and is **not** a `NotFoundError`.
   e. Build the renderer:
      ```go
      markdown.New(
          markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
              if target == name {
                  return PageURL(vault.Name, pagePath), true
              }
              rel, ok := page.Links[target]
              if !ok {
                  return "", false
              }
              return PageURL(vault.Name, rel), true
          })),
          markdown.WithPages(markdown.PagesFunc(func(pageName string) (string, bool) {
              if pageName == name {
                  return page.Content, true
              }
              body, ok := page.Embeds[pageName]
              return body, ok
          })),
      )
      ```
      A page that links to itself resolves to its own URL; do not special-case self-links or cycles. The renderer bounds embed expansion itself.
   f. `rendered, err := renderer.Render(ctx, page.Content)` — pass the full raw source, frontmatter included. A render error is wrapped with `errors.Wrap(ctx, err, "render page")` so the handler answers `500` with no partial body.
   g. `links, err := s.pages.Backlinks(ctx, vault.Name, pagePath)`; a backlink error is wrapped with `errors.Wrap(ctx, err, "read backlinks")` and fails the render (a page with a body but no backlinks section is a silent regression). Convert each `vaultapi.Backlink` to its `Path` and pass the relative paths to `buildDocument`.
   h. Return `buildDocument(ctx, vault, pagePath, name, rendered, backlinks)`.

7. **`Exists(ctx, vaultName, pagePath)` contract.** Resolve the vault and validate the path exactly as `Render` does, then call `s.pages.Page` once. Return `(true, nil)` on success. Return `(false, nil)` for **every** `NotFoundError` (unknown vault, invalid path, missing page, or a vault-service not-found) — the `/open` route turns that into a `404`. Return `(false, err)` for any other error so the handler can answer `500`. Factor the mapping into one small helper so `Render` and `Exists` classify identically.

8. **Document wrapper.** Use `html/template`. The document must carry `<!doctype html>`, `<meta charset="utf-8">`, a `<title>` of the page name, `<link rel="stylesheet" href="/style.css">`, a `<main class="page-view">` container, an "Open in Obsidian" anchor, the rendered body, and a `<section class="backlinks">` with one `<li>` per backlink (an empty section when there are none). Template data:
   ```go
   type document struct {
       Title       string
       ObsidianURL template.URL
       Body        template.HTML
       Backlinks   []backlink
   }
   ```
   - `ObsidianURL` **must** be `template.URL(board.ObsidianURL(vault.VaultName, pagePath))`. A plain `string` makes `html/template` refuse the `obsidian://` scheme and emit `#ZgotmplZ`, so the hand-off link would not work.
   - `Body` is `template.HTML(rendered)` — only the renderer output is inserted as HTML.
   - Every other value (page name, backlink names and hrefs) is escaped by the template. Never splice the raw path, vault name or file content into the document.
   - Each backlink entry's `Name` is the basename of the backlink path with `.md` removed; its `URL` is `PageURL(vault.Name, rel)`. Skip an entry whose basename equals the requested page's name (a self-link is not a backlink).

9. **Tests in `pkg/pageview`.** Keep the Ginkgo suite entry point `TestPageView` in `pkg/pageview/pageview_suite_test.go` (`RegisterFailHandler(Fail)`, `RunSpecs`, `suiteConfig.Timeout = 2 * time.Minute`) — without it every node is silently skipped. Specs live in `pkg/pageview/pageview_test.go` (`package pageview_test`) over a fixture vault on disk exposed through a fake `PageSource` (a `Page` read answers the body plus link/embed maps built from the fixture; a `Backlinks` read computes the inbound set with `markdown.Backlinks` over the fixture). Fixture files at the vault root: `Alpha.md` (a pipe table, a callout titled `Important Title`, a `[[Beta]]` link), `Beta.md` (plain text), `Gamma.md` (an `![[Beta]]` embed), `Delta.md` (a `[[Nope]]` link), `Links.md` (a `[[Alpha]]` link), `Echo.md` (a body-level `<script>`), plus `24 Goals/Some Goal.md` for the nested-path case. Cover:
   - **AC1:** `Render("private-personal", "Alpha.md")` contains, verbatim, the string produced by `markdown.New(markdown.WithResolver(<same Beta mapping>), markdown.WithPages(<same mapping>)).Render(ctx, alphaSource)`; it contains `<table>` and does not contain the raw `| --- |`.
   - **AC3:** Alpha's body contains `href="/v/private-personal/page/Beta.md"`; Delta's body contains the text `Nope` but no anchor whose href is `/v/private-personal/page/Nope.md`. Use a recording `PageSource` to assert exactly one `Page` call and one `Backlinks` call per render on two freshly constructed services (proves nothing is cached between renders without sleeping).
   - **AC4:** Gamma's body contains Beta's body text (`Beta body text.`); a negative control that renders `Gamma.md` with `markdown.New()` (no `WithPages`) differs from the service output and omits Beta's body.
   - **AC5:** Alpha's backlinks section names `Links` and no page that does not link to it; Beta's names `Alpha` and `Gamma` and not `Delta`; Delta returns without error and lists no backlink for `Nope`. Then write `Epsilon.md` (linking `Alpha`) after the fixture is built and assert Alpha's next `Render` names `Epsilon`.
   - The document carries the doctype, `<meta charset="utf-8">`, `/style.css`, `<main class="page-view">`, `<section class="backlinks">`, and (after `html.UnescapeString`) `obsidian://open?vault=private-personal&file=Alpha.md`.
   - Error mapping: a `vaultapi.NotFoundError` from `Page` maps to `pageview.NotFoundError` whose message starts `Page not found: `; a generic `Page` error is wrapped and is **not** a `pageview.NotFoundError`; a `Backlinks` error fails `Render`; `Exists` returns `(false, nil)` for a missing page and `(false, err)` for a generic error.
   - Path validation before any read: a counting `PageSource` records **zero** `Page` and zero `Backlinks` calls for `""`, `Alpha`, `24 Goals/`, `../../../../etc/passwd` and `../../../../etc/passwd.md`; each returns a `NotFoundError`.
   - `PageURL` table: `Beta.md` → `/v/private-personal/page/Beta.md`; `24 Goals/Some Goal.md` → `/v/private-personal/page/24%20Goals/Some%20Goal.md`.
   - **Seam assertion:** read `pageview.go` with `os.ReadFile` in a spec and assert it contains none of `filepath.WalkDir`, `os.ReadFile`, `markdown.Backlinks`, `indexTTL` — the only way to catch a direct call that bypasses the seam.
   - Keep the existing cross-folder and no-frontmatter specs.

10. **CHANGELOG.** Add one bullet under `## Unreleased` in `CHANGELOG.md` describing the page view: a vault page renders in-process with the released `github.com/bborbe/vault-markdown` renderer; wikilinks, embeds and a backlinks section come from the vault service; every render reads fresh and caches nothing; the spec's AC3 (one `ReadPage` per page) and AC7 (the symlink guard) now live in the vault service.

11. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and the named acceptance criteria against the change.

</requirements>

<constraints>

- `github.com/bborbe/vault-markdown` is pinned at `v0.1.2`. There is no package-level `Render`; rendering goes through `markdown.New(...).Render`.
- Page reads go through `pkg/vaultapi.Client` (`Page` + `Backlinks`). `pkg/pageview` must not open a file, walk a directory, or call `markdown.Backlinks` itself — the vault service owns the read, the listing and the symlink-out-of-vault guard.
- No caching of rendered pages or page bodies. Every render is two service reads, so an edit is visible on the next request.
- No live refresh over SSE or WebSocket; the page is rendered per request.
- No editing, no write verbs, no mutations of any kind. This package only reads.
- No new configuration key. The vault set is the one already configured.
- No renderer change; the library is consumed at its released tag as-is.
- No change to vault-cli and no vault-cli version bump.
- No standalone viewer binary and no new process.
- No change to the card link builder, the board's list responses, or any existing route.
- No styling work beyond what the existing static stylesheet already provides.
- A very large page is served whole: add no size limit and no truncation.
- Concurrent requests read independently: no lock, no shared cache, no package-level mutable state, no `go func()`.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.
- Do not modify `Makefile` or `.github/workflows/ci.yml`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

Run from the repo root; each must exit 0:
- `make precommit`
- `GOPRIVATE=github.com/bborbe/vault-markdown go test ./pkg/pageview/ -count=1`
- `GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open' -count=1`
- `test "$(grep -c 'github.com/bborbe/vault-markdown v0.1.2' go.mod)" = "1"`
- `test -z "$(grep -n 'filepath.WalkDir\|os.ReadFile\|markdown.Backlinks\|indexTTL' pkg/pageview/pageview.go)"`

`make precommit` runs format, vet and `go test -race ./...`. The direct `go test` commands carry the `GOPRIVATE` prefix because the shell does not inherit the `Makefile` export.

</verification>
