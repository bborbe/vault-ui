---
status: completed
spec: [040-page-view]
summary: Confirmed the runC base already implements the re-pointed vault-api page service; added the missing AC1 verbatim-renderer, AC4 embed negative-control, AC5 dynamic-backlink and document-wrapper specs plus a CHANGELOG Unreleased entry, and make precommit exits 0.
execution_id: 5c213eaa-c160-5ada-8cfd-1cca50d390a5-exec-186-spec-040-page-service
dark-factory-version: dev
created: "2026-11-24T22:55:00Z"
queued: "2026-11-24T22:09:00Z"
started: "2026-11-24T22:09:03Z"
completed: "2026-11-24T22:16:29Z"
branch: test/spec-040-order-runC
---

<!--
Auditor note (open question, not an execution instruction): this prompt follows
spec 040's stated design — adapters over pkg/pageindex, github.com/bborbe/vault-markdown
pinned at v0.1.0. The repository's current HEAD has since moved pkg/pageview onto the
vault-api read seam, bumped vault-markdown to v0.1.2, and deleted pkg/pageindex. If the
execution base is that HEAD rather than the pre-page-view feature base (3ef54a9^), then
requirement 3's PageStorage seam must be re-pointed to the existing pageview.PageSource
interface and requirement 1 skipped. Confirm the execution base before running.
-->

# Render a vault page in-process with the released vault-markdown library

<summary>

- A vault page is rendered to HTML inside vault-ui instead of handing off to Obsidian
- Tables, callouts and embeds become real HTML elements, not raw markdown
- Wikilinks to existing pages become links inside the app; links to missing pages stay as plain text
- A backlinks section lists the pages that link to the one being viewed
- The page is wrapped in a small document that loads the board's existing stylesheet
- An "open in Obsidian" link keeps the old hand-off one click away
- Every read goes through the board's existing on-demand disk read and symlink guard
- Nothing is cached, so an edit is visible on the next request
- The released renderer is pinned and is the only thing that renders

</summary>

<objective>

Build the in-process page-rendering service so a vault page can be read inside vault-ui without launching Obsidian, reusing the released `github.com/bborbe/vault-markdown` renderer and the board's own on-demand page read. This is the integration only: the renderer itself is consumed at its released tag, unchanged.

</objective>

<context>

There is no `CLAUDE.md` in this repo; the Go binary is the live service.

Read these before writing anything:

- `pkg/pageindex/pageindex.go` and `docs/page-index.md` — the page index is metadata-only: `ListPages` returns pages whose `Content` is empty and never touches disk for a warm read; `ReadPage` reads the full page on demand and never caches. `docs/page-index.md` is the authoritative statement of that contract; cite it, do not restate it.
- `pkg/board/board.go` — `Vault` has `Name` (the vault-ui display name), `VaultName` (the underlying vault-cli name), and `Path` (the vault root); `VaultsProvider` is the vault resolver; `UnknownVaultError` is the typed-error style to mirror.
- `pkg/board/helpers.go` — `obsidianURL` (around line 136) and `pythonQuote` define the exact `obsidian://open?vault=<quote>&file=<quote>` shape the board emits.
- `pkg/board/tasks.go`, `pkg/board/goals.go`, `pkg/board/topics.go` — every card link calls `obsidianURL(vault.VaultName, ...)`, i.e. the **underlying vault-cli name**, not the display name.
- `pkg/handler/api_common.go` — `writeDetail`, `writeNotFound`, and the `stderrors.As` error-classification pattern the later route prompt mirrors.
- `src/vault_ui/static/index.html` and `pkg/handler/static.go` — the static tree is served at `/`, so the stylesheet is reachable at `/style.css`.
- `pkg/factory/api.go` — `CreateAPIHandler` already receives `pageIndex pageindex.PageIndex`; the route prompt wires the service from it.
- `pkg/pageindex/pageindex_suite_test.go` — the Ginkgo suite convention for this repo.
- `docs/dod.md` — the definition of done this prompt is verified against.
- Coding-plugin guides (in-container paths): `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` (`errors.Wrap`/`errors.Wrapf`, never `fmt.Errorf`), `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` (Ginkgo v2 / Gomega, external test package, `Describe`/`It`/`DescribeTable`), and `/home/node/.claude/plugins/marketplaces/coding/docs/go-mod-dependency-fix-guide.md` (fetching a module the proxy cannot serve).

The released renderer's public API (`github.com/bborbe/vault-markdown` v0.1.0), verified from its source:

```go
func New(opts ...Option) Renderer
func WithResolver(r Resolver) Option
func WithPages(p Pages) Option
type Resolver interface { Resolve(target string) (string, bool) }
type ResolverFunc func(target string) (string, bool)   // adapts a func to Resolver
type Pages interface { Page(name string) (string, bool) }
type PagesFunc func(name string) (string, bool)         // adapts a func to Pages
type Renderer interface { Render(ctx context.Context, source string) (string, error) }
func Backlinks(pages map[string]string, target string) []string
```

There is **no** package-level `Render`; rendering goes through `markdown.New(...).Render(ctx, src)`.

Verified behaviour of that library:

- `Backlinks` returns the **map keys** of the pages whose content links to `target`, sorted. It skips the entry whose name with `.md` trimmed equals `target`. Both `[[x]]` and `![[x]]` count; links inside frontmatter, fenced blocks and inline code do not.
- A resolved wikilink renders `<a class="wikilink" href="...">label</a>`; a missing target renders `<span class="wikilink missing">label</span>`.
- A line holding only `![[page]]` is replaced by that page's body, one level deep. With `WithPages` omitted the embed is dropped.
- A `> [!type] Title` blockquote renders `<div class="callout" data-callout="type">` with a `<p class="callout-title">Title</p>`.
- Raw HTML in the source is escaped (goldmark's safe default), so a body-level `<script>` renders as text.

The vault-cli storage API (`github.com/bborbe/vault-cli` v0.197.0), verified from its source:

```go
// storage.PageStorage
ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
ReadPage(ctx context.Context, vaultPath string, pagesDir string, name string) (*domain.Page, error)
```

- `ReadPage`'s `name` is a **bare base name without `.md`**; a name containing a path separator is rejected, and a file that is a symlink resolving outside the vault is refused with an error whose text contains `symlink outside vault`.
- A missing file is reported as an error matching `stderrors.Is(err, fs.ErrNotExist)`.
- `domain.Page` embeds `Content` (the raw file text, frontmatter included — `type Content string`), `FileMetadata{Name string; FilePath string; ModifiedDate *time.Time}`, and `FrontmatterMap`. `Name` is the bare base name.
- `pkg/pageindex.PageIndex` embeds `storage.PageStorage`, so it satisfies the two-method `PageStorage` seam below. Its `ListPages` returns metadata-only pages (`Content` empty); its `ReadPage` reads the full body and never caches.

</context>

<requirements>

1. **Add the dependency in this order.** First write the package below that imports `markdown "github.com/bborbe/vault-markdown"`, then run:
   ```bash
   export PATH=/usr/local/go/bin:$PATH
   GOPRIVATE=github.com/bborbe/vault-markdown go get github.com/bborbe/vault-markdown@v0.1.0
   GOPRIVATE=github.com/bborbe/vault-markdown go mod tidy
   ```
   The `GOPRIVATE` prefix is required: `proxy.golang.org` and `sum.golang.org` still serve cached "not found" results for this module, and a direct `go` invocation does not inherit the `Makefile`'s export. `go.mod` must end with exactly one line matching `github.com/bborbe/vault-markdown v0.1.0`. Do not edit `Makefile` or `.github/workflows/ci.yml` — both already carry `GOPRIVATE` and are landed directly on the branch.

2. **Add one public wrapper in `pkg/board/helpers.go`.** Do not change `obsidianURL` or any of its callers:
   ```go
   // ObsidianURL builds the Obsidian deep link, matching the shape the board's
   // cards already emit.
   func ObsidianURL(vaultName, filePath string) string { return obsidianURL(vaultName, filePath) }
   ```

3. **Create package `pkg/pageview` with a narrow read seam.** `pageindex.PageIndex` must satisfy `PageStorage`; reuse `board.VaultsProvider` for the vault set:
   ```go
   type PageStorage interface {
       ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
       ReadPage(ctx context.Context, vaultPath string, pagesDir string, name string) (*domain.Page, error)
   }
   ```
   Import `domain "github.com/bborbe/vault-cli/pkg/domain"`. Do not define a parallel vault interface; use `board.VaultsProvider` directly. `Vault.Path` is the vault root for storage; `Vault.VaultName` is the underlying vault-cli name used in the generated links.

4. **Add a typed 404 error**, mirroring `board.UnknownVaultError` so the handler can classify it with `stderrors.As`:
   ```go
   // NotFoundError reports a vault, path or page the route cannot serve as a 404.
   type NotFoundError struct{ Message string }
   func (e NotFoundError) Error() string { return e.Message }
   ```
   Return `NotFoundError` as a **value**, not a pointer, so the handler can classify with `var nf pageview.NotFoundError; stderrors.As(err, &nf)`.

5. **Add the URL helper** used by both the resolver and the `/open` redirect so the two cannot drift:
   ```go
   // PageURL returns /v/{vault}/page/{path} with each path segment escaped and
   // the slashes between segments preserved.
   func PageURL(vaultName, pagePath string) string
   ```
   Escape each `/`-separated segment with `url.PathEscape` and join with `/`. `PageURL("private-personal", "Beta.md")` is `/v/private-personal/page/Beta.md`; `PageURL("private-personal", "24 Goals/Some Goal.md")` is `/v/private-personal/page/24%20Goals/Some%20Goal.md`.

6. **Add the service**:
   ```go
   type Service interface {
       Render(ctx context.Context, vaultName string, pagePath string) (string, error)
       Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
   }
   func NewService(vaults board.VaultsProvider, pages PageStorage) Service
   ```

7. **`Render` sequence** (this is the contract; keep the read counts exact):
   a. Resolve `vaultName` against `Vaults(ctx)` case-insensitively by **either** `Vault.Name` or `Vault.VaultName` (the route receives the vault-cli name from `obsidian://` links and the display name from the board; both must work). An unknown name returns `NotFoundError` naming the vault. Wrap a `Vaults` error with `errors.Wrap`.
   b. Validate `pagePath` **before any storage call**: non-empty, ends in `.md`, no leading `/`, and every `/`-separated segment non-empty and not `.` or `..`. A failure returns `NotFoundError`. This is the only pre-read guard; it is what makes a traversal request cost zero reads.
   c. Split `pagePath` into `pagesDir` (everything before the last `/`; `""` at the root) and `name` (the last segment minus `.md`).
   d. `ReadPage(ctx, vault.Path, pagesDir, name)` for the requested page. Map a missing file (`stderrors.Is(err, fs.ErrNotExist)`) or a symlink-out-of-vault error (the error text contains `symlink outside vault`) to `NotFoundError`. Return any other read error wrapped (the handler answers `500`).
   e. `ListPages(ctx, vault.Path, pagesDir)` exactly once. This is the single listing the resolver and the backlinks pass share.
   f. In one pass over the listing build two maps: `pagesByName` (bare `page.Name` → raw body, for the resolver and embeds) and `backlinkPages` (`page.Name + ".md"` → raw body, for `markdown.Backlinks`). For the requested page reuse the body read in step d — never `ReadPage` it a second time. For every other listed page call `ReadPage` once; on error log at `glog.V(2)` and skip that page rather than failing the render.
   g. Build the renderer:
      ```go
      markdown.New(
          markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
              if _, ok := pagesByName[target]; !ok {
                  return "", false
              }
              return PageURL(vault.VaultName, path.Join(pagesDir, target+".md")), true
          })),
          markdown.WithPages(markdown.PagesFunc(func(name string) (string, bool) {
              body, ok := pagesByName[name]
              return body, ok
          })),
      )
      ```
   h. `Render(ctx, string(page.Content))` — pass the full raw source, frontmatter included, so the frontmatter table is rendered. A render error returns wrapped with `github.com/bborbe/errors` (the handler answers `500`).
   i. `markdown.Backlinks(backlinkPages, name)` gives the linking filenames, sorted. Render each as an anchor whose href is `PageURL(vault.VaultName, path.Join(pagesDir, filename))` and whose visible text is the filename with `.md` removed. Drop an entry whose basename equals the requested page's own name.
   j. Wrap the result in the document (requirement 8) and return it.

8. **Document wrapper (the spec's DB8).** Use `html/template`. The document must carry: `<!doctype html>`, `<meta charset="utf-8">`, `<link rel="stylesheet" href="/style.css">`, a container element (use `<main class="page-view">`), an "open in Obsidian" anchor whose href is `board.ObsidianURL(vault.VaultName, pagePath)`, the rendered body, and `<section class="backlinks">` holding the backlink anchors (an empty section when there are none). Only the renderer output is inserted as `template.HTML`; every other value — vault name, path, obsidian href, backlink names and hrefs — is escaped by the template. Do not splice the raw path, vault name or file content into the document.

9. **`Exists`** resolves the vault, validates the path, and calls `ReadPage` once. Return `(true, nil)` on success; `(false, NotFoundError)` for an unknown vault, invalid path, missing file or symlink-out; `(false, err)` for any other read error.

10. **Tests in `pkg/pageview`** — add a Ginkgo suite named `TestPageView` in `pageview_suite_test.go` (the filter `-run 'Page|Open'` selects it) and specs in `pageview_test.go`, both in package `pageview_test`. Build the fixture vault exactly as the spec's Acceptance Criteria fixture note describes, at the vault root:
    - `Alpha.md` — a pipe table, a callout with a title, and a `[[Beta]]` link.
    - `Beta.md` — plain text.
    - `Gamma.md` — an `![[Beta]]` embed on its own line.
    - `Delta.md` — a `[[Nope]]` link to a page that does not exist.
    - `Links.md` — a `[[Alpha]]` link (so `Alpha` has one backlink).
    - `Echo.md` — a body-level raw `<script>` tag.
    Use the production page index as the seam:
    ```go
    idx := pageindex.NewPageIndex(
        pageindex.NewPageReader(storage.NewPageStorage(nil)),
        pageindex.NewDirectoryLister(),
        libtime.NewCurrentDateTime(),
        libtime.NewWaiterDuration(),
    )
    ```
    with a fake `board.VaultsProvider` returning one vault `{Name: "private-personal", VaultName: "private-personal", Path: vaultDir}`. Wrap `idx` in a counting `PageStorage` double that records `ListPages`/`ReadPage` calls and delegates. Cover:
    - **AC1:** `Render` output contains, verbatim, the string produced by `markdown.New(markdown.WithResolver(<same mapping>), markdown.WithPages(<same mapping>)).Render(ctx, alphaSource)`; the body contains `<table>` and does **not** contain the raw `| --- |`.
    - **AC3:** Alpha's body contains `href="/v/private-personal/page/Beta.md"`; Delta's body contains the text `Nope` but no anchor whose href is `/v/private-personal/page/Nope.md`; the counting double shows exactly one `ListPages` per `Render` and exactly one `ReadPage` per page in the folder, with no page name read twice.
    - **AC4:** Gamma's body contains Beta's body text; a negative control that renders Gamma's source with `markdown.New(...)` **without** `WithPages` differs from the service output and omits Beta's body.
    - **AC5:** Alpha's backlinks section names `Links` and no page that does not link to it; Beta's names `Alpha` and `Gamma` and not `Delta`; Delta returns `200` with no backlink for `Nope`; after writing `Epsilon.md` linking Alpha and marking the key dirty (`idx.MarkDirty(pageindex.NewKey(vaultDir, ""))`), Alpha's next `Render` names `Epsilon`.
    - The document carries the doctype, `<meta charset="utf-8">`, `/style.css`, and (after `html.UnescapeString`) `obsidian://open?vault=private-personal&file=Alpha.md`.
    - `NotFoundError` for: unknown vault, empty path, a non-`.md` path, `../../../../etc/passwd`, and a symlink inside the vault pointing outside it; the traversal and non-`.md` cases perform zero `ListPages`/`ReadPage` calls (assert with the counting double).
    - A generic `ReadPage` error (stub storage) propagates as a non-`NotFoundError`.

11. **Self-check before finishing.** Re-run the verification below and confirm it passes; walk each numbered requirement and the spec's AC1, AC3, AC4 and AC5 against the change.

</requirements>

<constraints>

- `github.com/bborbe/vault-markdown` is pinned at `v0.1.0`. There is no package-level `Render`; rendering goes through `markdown.New(...).Render`. No renderer change; the library is consumed at its released tag as-is.
- `pkg/pageindex` holds page metadata only: `ListPages` returns empty `Content`, and `ReadPage` reads on demand without caching. The name→body map must be built with one `ListPages` plus one `ReadPage` per page; passing the index's own pages to `Backlinks` would produce an empty map.
- No caching of rendered pages or page bodies. Every read parses from disk, so an edit is visible on the next request.
- No new configuration key. The vault set is the one already configured.
- No editing, no write verbs, no mutations. This package only reads.
- Do not modify `Makefile` or `.github/workflows/ci.yml`; they already export `GOPRIVATE` and are landed directly on the branch, not by a prompt.
- Go error wrapping follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

```bash
export PATH=/usr/local/go/bin:$PATH
make precommit
GOPRIVATE=github.com/bborbe/vault-markdown go test ./pkg/pageview/ -count=1
GOPRIVATE=github.com/bborbe/vault-markdown go test ./... -run 'Page|Open'
test "$(grep -c 'github.com/bborbe/vault-markdown v0.1.0' go.mod)" = "1"
```

`make precommit` — format, vet and `go test -race ./...` must pass. The direct `go test` lines need the `GOPRIVATE` prefix because the environment does not inherit the `Makefile` export. The `grep -c` asserts exactly one pin line.

</verification>
