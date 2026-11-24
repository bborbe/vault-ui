---
status: completed
spec: [040-page-view]
summary: Rewrote pkg/pageview to render a vault page in-process through the PageStorage seam with the pinned vault-markdown v0.1.0 renderer (folder-scoped resolver/pages maps, embeds, backlinks, escaped document wrapper), removed the cache/walk/raw-file fallback, rewrote the tests, and added an Unreleased changelog entry.
execution_id: 65a9c064-8832-5fff-83ba-0f21837fbb27-exec-186-spec-040-page-service
dark-factory-version: dev
created: "2026-11-24T09:04:18Z"
queued: "2026-11-24T09:28:49Z"
started: "2026-11-24T09:28:52Z"
completed: "2026-11-24T09:35:45Z"
branch: feat/page-view
---

# Render a vault page in-process over the page-storage seam

<summary>

- A vault page can now be turned into a complete HTML document inside vault-ui, with no Obsidian launch.
- The page body is read through the board's own on-demand page read, so the same symlink-out-of-vault guard applies.
- Wikilinks resolve to in-app page URLs; a link to a page that does not exist stays plain text and the page still renders.
- An `![[embed]]` is inlined with the embedded page's content.
- A backlinks section follows the body, listing the pages that link to the requested page.
- The page's folder is listed once and each page in it is read once per request; nothing is cached between requests.
- The released `github.com/bborbe/vault-markdown` v0.1.0 renderer does all rendering; no renderer change and no new dependency.
- Only the renderer's output is inserted as HTML; every other value in the document wrapper is escaped.
- No route, configuration, frontend or factory change in this prompt — the HTTP surface is the next prompt.

</summary>

<objective>

Make one vault page renderable to an HTML document in-process. Today the page is handed to Obsidian; this prompt ends with a `pageview.Service` whose `Render` resolves a vault and a vault-relative markdown path, reads the page through the page-storage seam, renders it with the released `vault-markdown` renderer (resolving wikilinks, inlining embeds, and adding a backlinks section), and wraps the result in a small document carrying the existing stylesheet and an "open in Obsidian" link. Every page read goes through `PageStorage.ReadPage`; nothing is cached.

</objective>

<context>

Read these before writing code.

- `pkg/pageview/pageview.go` — the file this prompt rewrites. It currently holds `Service` (`Render`, `Exists`), `NewService`, `PageURL`, `NotFoundError`, `PageStorage`, and a `vaultIndex` built by `filepath.WalkDir` and cached for `indexTTL` (30s). The cache, the mutex, the walk and the raw-file fallback must go; the exported names `Service`, `NewService`, `PageURL`, `NotFoundError` and `PageStorage` stay, so the handler and factory keep compiling.
- `pkg/board/board.go` — `Vault` (fields `Name`, `VaultName`, `Path`, `TasksFolder`, `GoalsFolder`, `TopicsFolder`, `ClaudeScript`, `SessionProjectDir`) and `VaultsProvider` (`Vaults(ctx) ([]Vault, error)`).
- `pkg/board/helpers.go` — `ObsidianURL(vaultName, filePath string) string` builds `obsidian://open?vault=…&file=…`, the same form the board's cards emit. Use the vault's display name (`Vault.VaultName`), not the config key.
- `github.com/bborbe/vault-cli@v0.197.0` `pkg/storage` — the page seam. Verified API:
  - `type PageStorage interface { ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error); ReadPage(ctx context.Context, vaultPath string, pagesDir string, name string) (*domain.Page, error) }`
  - `func NewPageStorage(storageConfig *Config) PageStorage`
  - `ReadPage` requires a bare base name (no `/`, no `\`, not `.`/`..`) and applies the symlink-out-of-vault guard in `baseStorage.readEntityComponentsFromPath`, returning an error whose text contains `symlink outside vault`.
  - `pkg/domain` — `type Page struct { FrontmatterMap; FileMetadata; Content }`, `type Content string`, and `FileMetadata{Name, FilePath, ModifiedDate}`.
- `github.com/bborbe/vault-markdown@v0.1.0` — verified public API:
  - `func New(opts ...Option) Renderer`
  - `func WithResolver(r Resolver) Option`, `func WithPages(p Pages) Option`
  - `type ResolverFunc func(target string) (string, bool)` with method `Resolve(target string) (string, bool)`
  - `type PagesFunc func(name string) (string, bool)` with method `Page(name string) (string, bool)`
  - `type Renderer interface { Render(ctx context.Context, source string) (string, error) }`
  - `func Backlinks(pages map[string]string, target string) []string` — returns the map keys whose body links to `target` (both `[[target]]` and `![[target]]` count; a link in frontmatter, a fenced block or an inline code span does not), sorted, and excludes a key equal to `target`.
  - There is **no** package-level `Render`. `Render` is a method on the value returned by `New`.
- `docs/page-index.md` — the authoritative statement that the page index is metadata-only: `ListPages` returns pages with empty `Content`, `ReadPage` reads the full page on demand and caches nothing. Cite it; do not restate it.
- Conventions: `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` for the `github.com/bborbe/errors` wrapping style (`errors.Wrap`, `errors.Errorf`; no `fmt.Errorf`), and `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` for the plain `testing` + `t.Fatalf` shape `pkg/pageview/pageview_test.go` already uses.

Seam naming: the spec calls this seam `pkg/pageindex`. At this commit that package does not exist — the concrete production type is vault-cli's `storage.PageStorage`, and the page-index type `vault-cli/pkg/index.PageIndex` embeds that interface. Do **not** import a local `pkg/pageindex`. Keep the narrow `PageStorage` interface in `pkg/pageview` so tests can inject a counting double; production injects `storage.NewPageStorage(nil)`.

</context>

<requirements>

1. Keep the seam interface in `pkg/pageview/pageview.go`, matching vault-cli's `storage.PageStorage` exactly so `storage.NewPageStorage(nil)` satisfies it:

```go
// PageStorage is the narrow page-read seam the service reads through. It is
// satisfied by vault-cli's storage.PageStorage (and its page-index
// implementation, index.PageIndex). The spec calls this seam "pkg/pageindex";
// at this commit the concrete type lives in vault-cli, so the service depends
// on this interface and tests inject a counting double.
type PageStorage interface {
	ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
	ReadPage(ctx context.Context, vaultPath string, pagesDir string, name string) (*domain.Page, error)
}
```

2. Keep the exported surface unchanged:

```go
type Service interface {
	// Render returns the rendered HTML document for a vault-relative page path.
	Render(ctx context.Context, vaultName string, pagePath string) (string, error)
	// Exists reports whether the page can be served.
	Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
}

func NewService(vaults board.VaultsProvider, pages PageStorage) Service
```

`NewService` returns `&service{vaults: vaults, pages: pages}` — no map, no mutex, no TTL field.

3. Keep `NotFoundError` a value type (`type NotFoundError struct{ Message string }`, `func (e NotFoundError) Error() string`) so callers classify a 404 with `stderrors.As`.

4. `resolveVault` — iterate `s.vaults.Vaults(ctx)`; match `strings.EqualFold(vault.Name, vaultName) || strings.EqualFold(vault.VaultName, vaultName)` (the card's in-app link carries the display name, a real `obsidian://` link may carry either). An unknown name returns `NotFoundError{Message: "Unknown vault: " + vaultName}` **before** any page-storage call. A `Vaults` error is wrapped and returned (a 500).

5. `splitPagePath(pagePath string) (pagesDir string, name string, err error)` — validate the URL path before any storage call. Reject with `NotFoundError` when: the path is empty; it starts with `/`; it does not end in `.md`; any `/`-separated segment is empty, `.` or `..`; or the base name (after trimming `.md`) is empty. Return `pagesDir` as the joined prefix (`""` for a root page) and `name` as the bare base name without `.md`.

6. `readPage(ctx, vault, pagesDir, name) (*domain.Page, error)` — call `s.pages.ReadPage(ctx, vault.Path, pagesDir, name)`. Map to `NotFoundError` when `stderrors.Is(err, fs.ErrNotExist)`, or the message contains `symlink outside vault`, or it contains `invalid page name`. Any other error is wrapped with `errors.Wrap(ctx, err, "read page")` and returned (a 500). Do **not** add a raw-file fallback: a page with no frontmatter is the same parse failure the board's own `ReadPage` returns, and the route must not open files itself.

7. `Render` — build a per-request, per-folder map and render. Do all of this with exactly one `ListPages` and one `ReadPage` per page, and no cache:

```go
func (s *service) Render(ctx context.Context, vaultName, pagePath string) (string, error) {
	vault, err := s.resolveVault(ctx, vaultName)
	if err != nil {
		return "", err
	}
	pagesDir, name, err := splitPagePath(pagePath)
	if err != nil {
		return "", err
	}
	requested, err := s.readPage(ctx, vault, pagesDir, name)
	if err != nil {
		return "", err
	}

	// The requested page was read once above; the listing below supplies the
	// other pages of the same folder. Read each of those exactly once.
	byName := map[string]string{name: string(requested.Content)}
	paths := map[string]string{name: pagePath}
	listed, listErr := s.pages.ListPages(ctx, vault.Path, pagesDir)
	if listErr != nil {
		glog.Warningf("list pages %s/%s for page view: %v", vault.Path, pagesDir, listErr)
		listed = nil
	}
	for _, page := range listed {
		if page.Name == name {
			continue // already read once above; never read it twice
		}
		body, readErr := s.pages.ReadPage(ctx, vault.Path, pagesDir, page.Name)
		if readErr != nil {
			glog.V(2).Infof("skip page %s for page view: %v", page.Name, readErr)
			continue
		}
		byName[page.Name] = string(body.Content)
		paths[page.Name] = path.Join(pagesDir, page.Name+".md")
	}

	renderer := markdown.New(
		markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
			rel, ok := paths[target]
			if !ok {
				return "", false
			}
			return PageURL(vault.Name, rel), true
		})),
		markdown.WithPages(markdown.PagesFunc(func(target string) (string, bool) {
			body, ok := byName[target]
			return body, ok
		})),
	)
	rendered, err := renderer.Render(ctx, string(requested.Content))
	if err != nil {
		return "", errors.Wrap(ctx, err, "render page")
	}
	return buildDocument(ctx, vault, pagePath, name, rendered, markdown.Backlinks(byName, name))
}
```

The resolver and the pages adapter must perform **no** storage call: they read only the two maps. A resolver that calls `ListPages` per wikilink, or a pages adapter that calls `ReadPage` per embed, violates AC3.

8. `PageURL(vaultName, pagePath string) string` — keep the existing implementation: split `pagePath` on `/`, `url.PathEscape` each segment, join with `/`, and prefix `/v/` + `url.PathEscape(vaultName)` + `/page/`. (`url.PathEscape` escapes a `/` inside a segment, so splitting first is what preserves the folder separators.)

9. `buildDocument(ctx, vault, pagePath, name, rendered string, backlinks []string) (string, error)` — render an `html/template` document with:
   - `<!doctype html>`, `<html lang="en">`, `<head>`, `<meta charset="utf-8">`, `<title>{{.Title}}</title>`, `<link rel="stylesheet" href="/style.css">`.
   - `<main class="page-view">` containing `<nav class="page-toolbar"><a class="obsidian-link" href="{{.ObsidianURL}}">Open in Obsidian</a></nav>`, then `{{.Body}}`, then `<section class="backlinks"><h2>Backlinks</h2><ul>…</ul></section>` with one `<li><a href="{{.URL}}">{{.Name}}</a></li>` per backlink.
   - `Title` is the page name.
   - `ObsidianURL` is `template.URL(board.ObsidianURL(vault.VaultName, pagePath))` — the `template.URL` conversion is required or `html/template` replaces the `obsidian://` scheme with `#ZgotmplZ`.
   - `Body` is `template.HTML(rendered)` — the renderer output is the only trusted HTML.
   - Backlink entries are built from the returned names: `Name` is the name, `URL` is `PageURL(vault.Name, paths[name])` (fall back to `path.Join(pagesDir, name+".md")` only if the name is absent from `paths`). Skip an entry whose name equals the requested page. `Name`, `Title` and every backlink URL are ordinary strings, so `html/template` escapes them; do not mark them `template.HTML`.

10. `Exists(ctx, vaultName, pagePath) (bool, error)` — `resolveVault`, `splitPagePath`, then `readPage` once. Return `(true, nil)` on success; propagate `NotFoundError` for an unknown vault, missing page, invalid path or symlink escape. Do not call `ListPages`.

11. Delete the cache and the walk: remove `indexTTL`, `vaultIndex`, `service.mu`, `service.index`, `loadIndex`, `newRenderer`, `readVaultFile`, `isNoFrontmatter`, and every `filepath.WalkDir` / `os.ReadFile` call. Keep `isMissing` (the `fs.ErrNotExist` / `symlink outside vault` / `invalid page name` classifier). Drop the now-unused imports (`io/fs` stays only if `isMissing` still uses `fs.ErrNotExist`; drop `os`, `filepath`, `sync`, `time`).

12. Rewrite `pkg/pageview/pageview_test.go` (package `pageview_test`, plain `testing`, every test named `TestPage…` or `TestOpen…`). Build the service with `pageview.NewService(fixtureVaults{dir: dir}, storage.NewPageStorage(nil))` over the shared fixture vault (`Alpha.md` with a pipe table, a callout titled `Important Title` and `[[Beta]]`; `Beta.md`; `Gamma.md` with `![[Beta]]`; `Delta.md` with `[[Nope]]`; `Links.md` with `[[Alpha]]`; `Echo.md` with a body-level `<script>`). Add or keep:

   a. **AC1 renderer is used** — `Render(ctx, "private-personal", "Alpha.md")` contains, verbatim, the output of `markdown.New(markdown.WithResolver(...), markdown.WithPages(...)).Render(ctx, alphaSource)` (resolver returns `"/v/private-personal/page/Beta.md"` for `Beta`). A raw-markdown echo fails because the raw source has `| --- |` and the rendered output has `<table>`.

   b. **AC3 counting** — a `countingStorage` implementing `pageview.PageStorage` wraps a real `storage.NewPageStorage(nil)` and records every `ListPages` call keyed by `pagesDir` and every `ReadPage` call keyed by `pagesDir`/`name`. Rendering `Alpha.md` must produce exactly one `ListPages` for `pagesDir == ""`, exactly one `ReadPage` for each of the six page names, and exactly one `ReadPage` for `Alpha` (no second read). Assert the per-name counts, not just the totals, so a resolver that re-lists or re-reads fails.

   c. **AC3 wikilinks** — `Alpha` contains `href="/v/private-personal/page/Beta.md"`; `Delta` contains the text `Nope` and no `href="/v/private-personal/page/Nope.md"`, and its `Render` returns no error.

   d. **AC4 embeds** — `Gamma` contains `Beta body text.`; the negative control renders `Gamma`'s source with `markdown.New(markdown.WithResolver(...))` (no `WithPages`) and asserts the two bodies differ (the embed is dropped without the option).

   e. **AC5 backlinks** — `Alpha` names `Links` and names neither `Beta` nor `Delta` as a backlink; `Beta` names `Alpha` and `Gamma` and not `Delta`; `Delta` returns a document with no `Nope` backlink and no error; then write `Epsilon.md` (linking `Alpha`) into the fixture after it was built and assert `Alpha`'s next `Render` names `Epsilon` (a name-keyed table over a fixed fixture cannot pass this).

   f. Keep `TestPageViewResolvesVaultByDisplayName`, `TestPageViewNotFoundCases` (unknown vault, empty path, non-markdown path, traversal, folder path), `TestPageViewSymlinkEscapeIsNotFound`, `TestPageViewExists` and `TestPageViewPageURL`.

   g. **Remove the superseded vault-wide tests** — `TestPageViewResolvesCrossFolderWikilink`, `TestPageViewListsCrossFolderBacklink` and `TestPageViewRendersPageWithoutFrontmatter`, plus `newFolderFixture` and `errorStorage` if unused. The spec scopes the map to the requested page's folder and removes the raw-file fallback, so those tests describe behavior this change drops. Keep a concurrent-render smoke test (`TestPageViewConcurrentRenderIsRaceFree`) — with no shared state it is cheap and still guards against a reintroduced cache.

13. Before finishing, run the verification commands below and confirm they exit 0; then walk DB1–DB5 and DB8 and AC1, AC3, AC4 and AC5 against the change, naming for each the test that proves it.

</requirements>

<constraints>

- `github.com/bborbe/vault-markdown` stays pinned at `v0.1.0`; there is no package-level `Render` — use `markdown.New(...).Render(ctx, src)`.
- No cache of any kind: no TTL, no map stored on `*service`, no mutex, no `filepath.WalkDir`, no `os.ReadFile` for page content. Every page body comes from `PageStorage.ReadPage`.
- Exactly one `ListPages` per folder per render and exactly one `ReadPage` per page, with no second read for the requested page (AC3).
- The resolver and the pages adapter perform no storage calls; they read only the per-request maps.
- A page path that escapes the vault, names an unknown vault, or is empty/a folder returns `NotFoundError` **before** any storage call. A symlink out of the vault returns `NotFoundError` from `ReadPage`.
- Error wrapping uses `github.com/bborbe/errors` (`errors.Wrap`, `errors.Errorf`); no `fmt.Errorf`.
- Do not change `pkg/handler/`, `pkg/factory/`, `main.go`, `src/vault_ui/static/`, `README.md`, `CHANGELOG.md` or `go.mod`/`go.sum`. The route and factory wiring are the next prompt.
- No new configuration key, no new dependency, no Prometheus metric, no frontend change, no renderer change.
- Do not touch `.github/workflows/`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass, except the three superseded tests listed in requirement 12g.

</constraints>

<verification>

`go` is not always on `PATH` in this container. Run each command with `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/pageview/ -run 'Page|Open' -count=1
make precommit
```

`go test ./pkg/pageview/ -run 'Page|Open'` runs the render, counting, wikilink, embed, backlink and not-found tests. `make precommit` runs `go fmt ./...`, `go vet ./...` and `go test -race ./...` and must exit 0. The `Makefile` exports `GOPRIVATE ?= github.com/bborbe/vault-markdown`, so the dependency resolves on a clean machine.

</verification>
