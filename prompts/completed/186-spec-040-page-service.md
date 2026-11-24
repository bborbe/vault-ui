---
status: completed
spec: [040-page-view]
summary: Reconciled pkg/pageview to the branch's vault-api read seam, fixed the Exists not-found contract and added the unknown-vault warning, expanded the page-view specs, and recorded the change under CHANGELOG's Unreleased; make precommit exits 0.
execution_id: 198a4926-2d28-59a6-b0c6-e10299f6f9d8-exec-186-spec-040-page-service
dark-factory-version: dev
created: "2026-11-24T19:15:00Z"
queued: "2026-11-24T18:20:53Z"
started: "2026-11-24T18:20:57Z"
completed: "2026-11-24T18:29:48Z"
branch: test/spec-040-order-runA
---

# Render a vault page in-process with the released vault-markdown library

<summary>

- A vault page can be rendered to HTML inside vault-ui instead of being handed off to Obsidian.
- Tables, callouts and embedded pages become real HTML elements rather than raw markdown.
- Links to existing pages become links inside the app; links to missing pages stay as plain text and the page still renders.
- A backlinks section lists the pages that link to the one being read.
- Every read comes from disk on demand, so an edit is visible on the next request; nothing is cached.
- The rendered page is wrapped in a small document that loads the board's existing stylesheet and keeps an "open in Obsidian" link one click away.
- The released renderer is pinned and is the only thing that renders the page.
- An unknown vault, a missing page, a folder path and a path that tries to leave the vault are reported as not found, never as server errors.
- The service exposes a narrow read seam over the existing page index, so it adds no file access of its own.

</summary>

<objective>

Build `pkg/pageview`, a read-only page-rendering service that reads one page through the existing page-index seam, renders it with the released `github.com/bborbe/vault-markdown` library, resolves wikilinks and embeds from the same folder listing, appends a backlinks section, and wraps the result in a document carrying the board's stylesheet and an Obsidian hand-off link. This is the integration only: the renderer is consumed at its released tag, unchanged.

</objective>

<context>

There is no `CLAUDE.md` in this repo. Read these before writing anything.

Project files:

- `docs/page-index.md` — the authoritative contract for `pkg/pageindex`: `ListPages` returns page **metadata only** (`Content` always empty) and `ReadPage` reads the full page from disk on demand and never caches. This is cited, not restated.
- `pkg/board/board.go` — `Vault` (`Name`, `VaultName`, `Path`), `VaultsProvider` (`Vaults(ctx context.Context) ([]Vault, error)`), and the typed-error style (`UnknownVaultError`).
- `pkg/board/helpers.go` — `obsidianURL(vaultName, filePath string) string` and `pythonQuote` define the exact `obsidian://open?vault=<quote>&file=<quote>` shape the board's cards already emit.
- `pkg/pageindex/pageindex.go` — `PageIndex` is an alias of vault-cli's `index.PageIndex`, which embeds `storage.PageStorage`. Constructors: `NewPageIndex(reader PageReader, lister DirectoryLister, currentDateTimeGetter libtime.CurrentDateTimeGetter, waiter libtime.WaiterDuration) PageIndex`, `NewPageReader(pages storage.PageStorage) PageReader`, `NewDirectoryLister() DirectoryLister`, and `NewKey(vaultPath, pagesDir string) Key`.
- `pkg/factory/pageindex.go` — `CreatePageIndex` shows the production page-index constructor.

Coding-plugin guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `Describe`/`It`/`DescribeTable`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mod-dependency-fix-guide.md` — fetching a module the proxy cannot serve.

Verified library API (`github.com/bborbe/vault-markdown` v0.1.0):

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

There is **no** package-level `Render`. Verified behaviour of that library:

- `Backlinks` returns the map keys (filenames) of pages whose content links to `target`, sorted. It skips the entry whose name with `.md` trimmed equals `target`. Both `[[x]]` and `![[x]]` count; links inside frontmatter, fenced blocks and inline code do not.
- A resolved wikilink renders `<a class="wikilink" href="...">label</a>`; a missing target renders `<span class="wikilink missing">label</span>`.
- A line holding only `![[page]]` is replaced by that page's body, one level deep. With `WithPages` omitted the embed is dropped.
- A `> [!type] Title` blockquote renders `<div class="callout" data-callout="type">` with `<p class="callout-title">Title</p>`.
- Frontmatter renders as `<table class="frontmatter">`.
- Raw HTML in the source is dropped (goldmark's safe default), so a body-level `<script>` renders as `<!-- raw HTML omitted -->`, not as an executable element.

Verified vault-cli seam (`github.com/bborbe/vault-cli` `pkg/storage`):

```go
type PageStorage interface {
    ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
    ReadPage(ctx context.Context, vaultPath string, pagesDir string, name string) (*domain.Page, error)
}
```

- `ReadPage`'s `name` is a bare base name without `.md`; a name with a path separator is rejected, and a file that is a symlink resolving outside the vault is refused with an error whose text contains `symlink outside vault`.
- `domain.Page` embeds `Content` (a `string` type holding the raw file text, frontmatter included) and `FileMetadata{Name string; FilePath string; ModifiedDate *time.Time}`. `Name` is the bare base name.

<!-- Reviewer note: this spec names pkg/pageindex as the production page-storage seam and its AC3/AC7 pin ListPages/ReadPage counts and the ReadPage symlink guard. If the branch has since moved page reads to another seam, reconcile the seam before approving; the service interface and tests below assume pageindex.PageIndex satisfies PageStorage. -->

</context>

<requirements>

1. **Add the dependency.** Write the package below that imports `markdown "github.com/bborbe/vault-markdown"`, then run:
   ```bash
   GOPRIVATE=github.com/bborbe/vault-markdown go get github.com/bborbe/vault-markdown@v0.1.0
   GOPRIVATE=github.com/bborbe/vault-markdown go mod tidy
   ```
   The `GOPRIVATE` prefix is required: `proxy.golang.org` and `sum.golang.org` still serve cached "not found" results for this module, and the shell does not inherit the `Makefile` export. `go.mod` must contain exactly one line matching `github.com/bborbe/vault-markdown v0.1.0`. Do not edit `Makefile` or `.github/workflows/ci.yml`; both already carry `GOPRIVATE` and are landed directly on the branch.

2. **Add one exported wrapper in `pkg/board/helpers.go`** if it is not already present. Do not change `obsidianURL` or any of its callers:
   ```go
   // ObsidianURL builds the Obsidian deep link, matching the shape the board's
   // cards already emit.
   func ObsidianURL(vaultName, filePath string) string { return obsidianURL(vaultName, filePath) }
   ```

3. **Create `pkg/pageview/pageview.go`** (replace its contents if the file already exists) with these declarations:

   ```go
   type PageStorage interface {
       ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
       ReadPage(
           ctx context.Context,
           vaultPath string,
           pagesDir string,
           name string,
       ) (*domain.Page, error)
   }

   // NotFoundError reports a vault, path or page the route cannot serve as a 404.
   // It is returned as a value so callers can classify it with stderrors.As.
   type NotFoundError struct{ Message string }

   func (e NotFoundError) Error() string { return e.Message }

   type Service interface {
       Render(ctx context.Context, vaultName string, pagePath string) (string, error)
       Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
   }

   func PageURL(vaultName, pagePath string) string
   func NewService(vaults board.VaultsProvider, pages PageStorage) Service
   ```

   `PageURL` escapes each `/`-separated segment with `url.PathEscape` and joins with `/`, preserving the slashes between segments: `PageURL("private-personal", "24 Goals/Some Goal.md")` is `/v/private-personal/page/24%20Goals/Some%20Goal.md`.

   Imports needed: `context`, `html/template`, `io/fs`, `net/url`, `path`, `strings`, `stderrors "errors"`, `"github.com/bborbe/errors"`, `"github.com/bborbe/vault-cli/pkg/domain"`, `markdown "github.com/bborbe/vault-markdown"`, `"github.com/golang/glog"`, `"github.com/bborbe/vault-ui/pkg/board"`.

4. **`Render` sequence — keep the read counts exact:**
   a. Resolve `vaultName` against `Vaults(ctx)` by `strings.EqualFold` on `Vault.Name` **or** `Vault.VaultName`. The board's Obsidian deep links carry the display name (`VaultName`) while the config key is `Name`; accepting both is what makes the `/open` compatibility route serve links that already exist. An unknown name logs `glog.Warningf("unknown vault: %s", vaultName)` and returns `NotFoundError` naming the vault.
   b. Validate `pagePath` **before any storage call**: non-empty, no leading `/`, ends in `.md`, every `/`-separated segment non-empty and not `.` or `..`, and the last segment minus `.md` non-empty. A failure returns `NotFoundError`. This is the only pre-read guard; it is what makes a traversal request cost zero reads.
   c. Split `pagePath` into `pagesDir` (everything before the last `/`; `""` at the root) and `name` (the last segment minus `.md`).
   d. `ReadPage(ctx, vault.Path, pagesDir, name)` for the requested page, exactly once. Map a missing file (`stderrors.Is(err, fs.ErrNotExist)`) or an error whose text contains `symlink outside vault` or `invalid page name` to `NotFoundError`. Return any other read error unchanged so the handler can answer `500`.
   e. `ListPages(ctx, vault.Path, pagesDir)` exactly once. This is the single listing the resolver, the embeds and the backlinks pass share.
   f. In one pass over the listing build `pagesByName` (bare `page.Name` → raw body) and `backlinkPages` (`page.Name + ".md"` → raw body). Seed both with the requested page's body from step d — never `ReadPage` it a second time. For every other listed page call `ReadPage` once; on error log at `glog.V(2)` and skip that page rather than failing the render.
   g. Build the renderer:
      ```go
      markdown.New(
          markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
              if _, ok := pagesByName[target]; !ok {
                  return "", false
              }
              return PageURL(vault.Name, path.Join(pagesDir, target+".md")), true
          })),
          markdown.WithPages(markdown.PagesFunc(func(name string) (string, bool) {
              body, ok := pagesByName[name]
              return body, ok
          })),
      )
      ```
      A page that links to itself resolves to its own URL; do not special-case self-links or cycles. The renderer bounds embed expansion to one level.
   h. `Render(ctx, string(page.Content))` — pass the full raw source, frontmatter included. A render error is wrapped with `errors.Wrap(ctx, err, "render page")` so the handler answers `500` with no partial body.
   i. `markdown.Backlinks(backlinkPages, name)` gives the linking filenames, sorted.
   j. Wrap the result in the document (requirement 5) and return it.

5. **Document wrapper.** Use `html/template`. The document must carry `<!doctype html>`, `<meta charset="utf-8">`, `<link rel="stylesheet" href="/style.css">`, a `<main class="page-view">` container, an "Open in Obsidian" anchor, the rendered body, and `<section class="backlinks">` with one list item per backlink (an empty section when there are none). The template data must use:
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
   - Every other value (vault name, page name, backlink names and hrefs) is escaped by the template. Do not splice raw path, vault name or file content into the document.
   - `Title` is the page name. Each backlink href is `PageURL(vault.Name, path.Join(pagesDir, filename))` and its visible text is `filename` with `.md` removed.

6. **`Exists` contract.** Resolve the vault and validate the path exactly as `Render` does, then call `ReadPage` once. Return `(true, nil)` on success. Return `(false, nil)` when the error is a `NotFoundError` (unknown vault, invalid path, missing file, symlink escape) — the route turns that into a `404`. Return `(false, err)` for any other error so the handler can answer `500`.

7. **Tests in `pkg/pageview`.** Add or update `pkg/pageview/pageview_suite_test.go` with a Ginkgo suite entry point named `TestPageView` (`RegisterFailHandler(Fail)`, `RunSpecs`, `suiteConfig.Timeout = 2 * time.Minute`), and specs in `pkg/pageview/pageview_test.go` (`package pageview_test`). Build the fixture vault the spec's Acceptance Criteria describe, at the vault root: `Alpha.md` (a pipe table, a callout titled `Important Title` and a `[[Beta]]` link), `Beta.md` (plain text), `Gamma.md` (an `![[Beta]]` embed), `Delta.md` (a `[[Nope]]` link to a missing page), `Links.md` (a `[[Alpha]]` link), `Echo.md` (a body-level raw `<script>`), and `24 Goals/Some Goal.md`. Use the production page index as the seam:
   ```go
   idx := pageindex.NewPageIndex(
       pageindex.NewPageReader(storage.NewPageStorage(nil)),
       pageindex.NewDirectoryLister(),
       libtime.NewCurrentDateTime(),
       libtime.NewWaiterDuration(),
   )
   ```
   with a fake `board.VaultsProvider` returning one vault `{Name: "private-personal", VaultName: "private-personal", Path: vaultDir}`. Cover:
   - **AC1:** `Render("private-personal", "Alpha.md")` returns a document that contains, verbatim, the string produced by `markdown.New(markdown.WithResolver(<same Beta mapping>), markdown.WithPages(<same mapping>)).Render(ctx, alphaSource)`; the document contains `<table>` and does not contain the raw `| --- |`.
   - **AC3:** Alpha's body contains `href="/v/private-personal/page/Beta.md"`; Delta's body contains the text `Nope` but no anchor whose href is `/v/private-personal/page/Nope.md`. Wrap the seam in a counting double and assert exactly one `ListPages` per `Render` and exactly one `ReadPage` per page in the folder, with no page name read twice.
   - **AC4:** Gamma's body contains Beta's body text; a negative control that renders Gamma's source with `markdown.New(...)` **without** `WithPages` differs from the service output and omits Beta's body.
   - **AC5:** Alpha's backlinks section names `Links` and no page that does not link to it; Beta's names `Alpha` and `Gamma` and not `Delta`; Delta returns without error and lists no backlink for `Nope`. Then write `Epsilon.md` (linking `Alpha`) after the fixture is built, call `idx.MarkDirty(pageindex.NewKey(vaultDir, ""))`, and assert Alpha's next `Render` names `Epsilon`.
   - The document carries the doctype, `<meta charset="utf-8">`, `/style.css`, and (after `html.UnescapeString` if needed) `obsidian://open?vault=private-personal&file=Alpha.md`.
   - `NotFoundError` for: unknown vault, empty path, a non-`.md` path, `../../../../etc/passwd`, and a symlink inside the vault pointing outside it. A generic `ReadPage` error (stub storage) propagates as a non-`NotFoundError`. The traversal and invalid-path cases perform zero `ListPages`/`ReadPage` calls (assert with the counting double).

   Route-level tests for the page and `/open` addresses belong to the next prompt; do not build the HTTP router here.

8. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and the named acceptance criteria against the change.

</requirements>

<constraints>

- `github.com/bborbe/vault-markdown` is pinned at `v0.1.0`. There is no package-level `Render`; rendering goes through `markdown.New(...).Render`.
- `pkg/pageindex` holds page metadata only: `ListPages` returns empty `Content`, and `ReadPage` reads on demand without caching. The name→body map must be built with one `ListPages` plus one `ReadPage` per page; passing the index's own pages to `Backlinks` would produce an empty map.
- No caching of rendered pages or page bodies. Every read parses from disk, so an edit is visible on the next request.
- No live refresh over SSE or WebSocket; the page is rendered per request.
- No editing, no write verbs, no mutations of any kind. This package only reads.
- No new configuration key. The vault set is the one already configured.
- No renderer change; the library is consumed at its released tag as-is.
- No change to vault-cli and no vault-cli version bump.
- No standalone viewer binary and no new process.
- No change to the card link builder, the board's list responses, or any existing route.
- No styling work beyond what the existing static stylesheet already provides.
- A very large page is served whole: add no size limit and no truncation.
- Concurrent requests read independently: no lock, no shared cache, no package-level mutable state.
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

`make precommit` runs format, vet and `go test -race ./...`; it must pass. The direct `go test` needs the `GOPRIVATE` prefix because the shell does not inherit the `Makefile` export. The `grep -c` asserts exactly one pin line.

</verification>
