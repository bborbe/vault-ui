---
status: completed
summary: Page view now renders from two vault-service reads (Page + Backlinks) with no vault index; added vaultapi Page/Backlinks client methods, rewrote pageview around a PageSource seam, wired the factory, ported tests to Ginkgo with a recording-source seam spec, and extended the e2e stub.
execution_id: vault-ui-page-api-exec-189-page-view-from-vault-api
dark-factory-version: v0.198.17
created: "2026-11-24T09:30:00Z"
queued: "2026-11-24T09:31:55Z"
started: "2026-11-24T09:49:28Z"
completed: "2026-11-24T09:57:07Z"
branch: dark-factory/189-page-view-from-vault-api
---

# Serve the page view from vault-api and delete the whole-vault walk

<summary>
- Opening a page in the board no longer parses every markdown file in the vault. The page view asks the vault service for the page and for its backlinks, so a page load costs two small HTTP calls instead of a scan over twenty-two thousand files.
- Today the same page takes between fourteen and twenty-seven seconds on the operator's vault, and three of twenty requests fail outright. The same page against the new service endpoints answers in about a millisecond.
- The temporary index, its thirty-second refresh timer, the whole-vault directory scan and the per-request backlink scan are all deleted. The page view keeps no index, reads no file and holds no lock.
- The vault service returns a page's body together with the links and embedded pages the renderer needs, and a separate endpoint returns the pages that link to it.
- Backlinks read identically to today: the rendered backlink set is unchanged for ten real pages carrying eleven hundred and forty-nine entries in total.
- A recording test double proves the page view makes exactly two service calls per render and touches no filesystem — including on a second render, after the old refresh window would have elapsed.
</summary>

<objective>
Replace the page view's local vault index with two vault-service reads, so its server time stops scaling with vault size. Add page and backlink reads to the vault-service client, rewrite the page-view service around them, wire the client in the factory, and prove the result with a recording test double plus the existing end-to-end page-view suite.
</objective>

<context>
There is no `CLAUDE.md` in this repo — the Go binary is the live service. The build/deploy truth is `docs/go-cutover.md` (historical cutover record) and `docs/launchd-service.md` (current service setup). `src/vault_ui/` is the deleted Python backend; ignore it.

Read these coding guides in the container (they are the contract for this change):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package (`package pageview_test`), `DescribeTable`/`Entry`, never stdlib `t.Run` tables.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; no `fmt.Errorf`, no bare `return err` at outer scope.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md` — handlers live in `pkg/handler/`, wired by `Create*` in `pkg/factory/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.

The repo is `github.com/bborbe/vault-ui` (Go), branch `feat/page-view-from-vault-api`.

Read these source files before changing anything:
- `pkg/pageview/pageview.go` — the whole file is being replaced; keep `PageURL`, `NotFoundError`, `Service`, `Render`, `Exists`, `splitPagePath`, `newRenderer`, `buildDocument`, `pageTemplate` and their behaviour.
- `pkg/pageview/pageview_test.go` — 13 stdlib tests today; every one needs an explicit disposition (see requirement 4).
- `pkg/vaultapi/client.go` — the HTTP client for the vault service (`DefaultBaseURL` `http://127.0.0.1:9091`, `requestTimeout` 5 s, `ConfigFromEnv`, `Client` interface, `get`).
- `pkg/vaultapi/source.go` — `Source` holds the `client`; it is where the page view's client comes from.
- `pkg/vaultapi/source_internal_test.go` — two fakes (`fakeClient` at :28, `failingClient` at :39) that must gain the two new methods.
- `pkg/factory/api.go` — `pageview.NewService(vaults, storage.NewPageStorage(nil))` at :202 is the wiring line; `CreateAPIHandler` already receives `vaultSource *vaultapi.Source` at :174.
- `pkg/board/board.go` — `Vault` carries `Name` (the vault-cli name, e.g. `private-personal`) and `VaultName` (the display name, e.g. `Personal`).
- `e2e/harness.go` — `stubHandler()` at :81 is the fixture vault service the e2e suite runs against.
- `e2e/page_view_test.go` — the six specs that must still pass.
- `specs/in-progress/040-page-view.md` — its AC3 ("exactly one `ReadPage` per page") and AC7 (symlink escape → 404 via `ReadPage`) are superseded by this change: the file read and its symlink guard move to the vault service. Note that in the CHANGELOG bullet; do not edit the spec.

The vault-service endpoints this change consumes are already merged and released (`bborbe/vault-api` v0.36.0):

- `GET /api/v1/pages/{path}?vault=<name>` → `{"vault","path","name","content","links":{target: relPath},"embeds":{target: body}}`
- `GET /api/v1/pages/{path}/backlinks?vault=<name>` → `{"vault","path","name","backlinks":[{"path","name"}]}`

`path` is vault-relative and URL-escaped per segment (`25%20Tasks/Foo.md`); `vault` defaults to the primary vault when omitted. A missing page is `404 {"error":"not_found"}`; an unknown vault is `404 {"error":"unknown_vault"}`. The symlink-escape guard now lives in the vault service, which refuses to serve a page reached through a symlink — the page view no longer reads files at all.
</context>

<constraints>
- Do NOT commit. Leave the work tree for the caller.
- `github.com/bborbe/vault-markdown` stays at its current version and stays imported by `pkg/pageview` — the renderer still needs `markdown.New`, `markdown.WithResolver` and `markdown.WithPages`. Only the `markdown.Backlinks` call goes.
- Errors go through `github.com/bborbe/errors` (`errors.Wrapf` / `errors.Errorf`); no `fmt.Errorf`, no bare `return err` at outer scope.
- `pkg/pageview` tests become Ginkgo v2 + Gomega: add `pkg/pageview/pageview_suite_test.go` with `TestPageView`, `RegisterFailHandler(Fail)`, `RunSpecs` and `suiteConfig.Timeout = 2 * time.Minute`, and port the stdlib tests to `Describe`/`It`/`DescribeTable`. **Without that suite file every Ginkgo node in the package is silently skipped** — a green `make test` that proves nothing.
- `pkg/handler` and `e2e` keep their existing test style.
- No `time.Now()`, no new package-level mutable state, no new `go func()`.
- No `go mod vendor`, no dependency bump.
- No new configuration key; inputs are the configured vault set and `VAULT_API_URL`.
- The two reads inherit the client's existing 5 s `requestTimeout`, so a render can take up to 10 s when the vault service is slow, and a vault service that is down turns a page view into a 500. Do not add a retry, a cache or a fallback — that is what this change removes.
</constraints>

<requirements>

### 1. Add `Page` and `Backlinks` to `pkg/vaultapi`

Extend the `Client` interface in `pkg/vaultapi/client.go` with:

```go
Page(ctx context.Context, vaultName string, pagePath string) (*Page, error)
Backlinks(ctx context.Context, vaultName string, pagePath string) ([]Backlink, error)
```

with these types:

```go
type Page struct {
    Vault   string            `json:"vault"`
    Path    string            `json:"path"`
    Name    string            `json:"name"`
    Content string            `json:"content"`
    Links   map[string]string `json:"links"`
    Embeds  map[string]string `json:"embeds"`
}

type Backlink struct {
    Path string `json:"path"`
    Name string `json:"name"`
}
```

Rules:
- Build the path as `/api/v1/pages/` + the page path with each segment escaped (`url.PathEscape`, slashes preserved) — the same shape `pageview.PageURL` uses.
- Send `?vault=` only when `vaultName` is non-empty; an empty value lets the service use its primary vault.
- A `404` must be reported as a typed not-found so the caller can map it to its own 404. Add an exported `NotFoundError` to the package and wrap the status into it; every other non-200 is a plain error carrying the status.
- Reuse the existing `get` helper's structure and the package's `http.Client`; do not build a second client.
- Add `func (s *Source) Client() Client` returning the source's client, so the factory passes `vaultSource.Client()` to the page-view service and `CreateAPIHandler`'s signature stays unchanged.
- Add the two methods to both fakes in `pkg/vaultapi/source_internal_test.go` (`fakeClient`, `failingClient`) so the package compiles.

### 2. Rewrite `pkg/pageview` around the vault service

Delete from `pkg/pageview/pageview.go`: `indexTTL`, `vaultIndex`, `loadIndex`, `PageStorage`, `readPage`, `readVaultFile`, `isMissing`, `isNoFrontmatter`, the `mu`/`index` fields, and the imports only those symbols need (`os`, `io/fs`, `path/filepath`, `sync`). **No `filepath.WalkDir`, no `os.ReadFile`, and no `markdown.Backlinks` may remain in `pkg/pageview`.**

Introduce the seam the service reads through:

```go
// PageSource is the vault-service read seam.
type PageSource interface {
    Page(ctx context.Context, vaultName string, pagePath string) (*vaultapi.Page, error)
    Backlinks(ctx context.Context, vaultName string, pagePath string) ([]vaultapi.Backlink, error)
}
```

`NewService(vaults board.VaultsProvider, pages PageSource) Service` — keep the name and the two-argument shape.

`Render(ctx, vaultName, pagePath)`:
1. `resolveVault` exactly as today (case-insensitive match on `Name` or `VaultName`, `NotFoundError` when unknown).
2. `splitPagePath` exactly as today (rejects empty, absolute, non-`.md`, `.`/`..` segments; returns pagesDir and name).
3. Call `s.pages.Page(ctx, vault.Name, pagePath)` — **`vault.Name`, the vault-cli name**, because that is what the vault service is keyed by. A `vaultapi.NotFoundError` becomes a `pageview.NotFoundError` with the same `"Page not found: "` message; any other error is returned wrapped and is NOT a `NotFoundError`.
4. Render with the resolver and pages built from the returned `Links` and `Embeds`:
   - resolver: `target == name` → `PageURL(vault.Name, pagePath)`; else `rel, ok := page.Links[target]` → `PageURL(vault.Name, rel)`; else unresolved.
   - pages: `name == pageName` → `page.Content`; else `body, ok := page.Embeds[name]`.
5. Call `s.pages.Backlinks(ctx, vault.Name, pagePath)` and pass the resulting relative paths to `buildDocument` unchanged. A backlink error is returned wrapped — a page with a body but no backlinks section would be a silent regression.

`Exists` calls `s.pages.Page` and maps a not-found to `(false, nil)`, any other error to `(false, err)`.

`buildDocument` keeps its current filter — drop an entry whose basename equals the page name — and its template unchanged.

### 3. Wire it in `pkg/factory/api.go`

Replace `pageview.NewService(vaults, storage.NewPageStorage(nil))` with `pageview.NewService(vaults, vaultSource.Client())`. Drop the now-unused `storage` import from that file if nothing else needs it.

### 4. Tests

**`pkg/pageview/pageview_test.go`** — every existing test needs an explicit disposition. The file has 13 today:

| Test | Disposition |
|---|---|
| `TestPageViewResolvesVaultByDisplayName`, `TestPageViewRendersWikilinksAndBacklinks`, `TestPageViewEmbedsPage`, `TestPageViewMissingLinkStaysPlainText`, `TestPageViewNotFoundCases`, `TestPageViewPageURL`, `TestPageViewResolvesCrossFolderWikilink`, `TestPageViewListsCrossFolderBacklink`, `TestPageViewRendersPageWithoutFrontmatter` | keep, re-pointed at a `PageSource` fake, ported to Ginkgo |
| `TestPageViewExists` | keep, but change the missing-page assertion to `(false, nil)` — the new contract |
| `TestPageViewGenericReadErrorPropagates` | keep, re-pointed at a `PageSource` whose `Page` returns a non-not-found error |
| `TestPageViewConcurrentRenderIsRaceFree` | delete — no shared index remains |
| `TestPageViewSymlinkEscapeIsNotFound` | delete — the file read and its symlink guard moved to the vault service; state in a comment that the vault service owns the guard, and keep `TestPageViewNotFoundCases`'s traversal case |

**New: the seam test (a required acceptance criterion).** Add `pkg/pageview/pageview_seam_test.go` (in `pageview_test`, so the suite file covers it):

- A recording `PageSource` records every call it receives (`Page`, `Backlinks`) with its arguments.
- `Render` is called once on a freshly constructed service, then again on a **second, freshly constructed service** — do not sleep past the old 30 s TTL; a 31 s sleep would tax every future `go test` run. Note in a comment that a second cold service is what makes "no rebuild on demand" testable.
- Assert each render recorded exactly `[Page(vault, path), Backlinks(vault, path)]` — assert the **count and the order**, so an implementation that adds a third call fails.
- Add a **source assertion** in the same file: read `pageview.go` with `os.ReadFile` and assert it contains none of `filepath.WalkDir`, `os.ReadFile`, `markdown.Backlinks`, `indexTTL`. Reading the source from a test is the only way to catch a direct call that bypasses the seam.

**New: error-mapping specs** in the same Ginkgo suite:
- `Page` returning `vaultapi.NotFoundError` → `pageview.NotFoundError` with the `"Page not found: "` message.
- `Page` returning a generic error → wrapped and NOT a `pageview.NotFoundError`.
- `Backlinks` returning an error → `Render` fails.
- `Exists` with a not-found `Page` → `(false, nil)`.

**`pkg/vaultapi` client specs** (add to the package's existing test file):
- a successful decode of both endpoints;
- `?vault=` present and absent;
- a 404 mapped to the typed not-found;
- **the request URL built for a path containing a space**: `24 Goals/Some Goal.md` → `/api/v1/pages/24%20Goals/Some%20Goal.md` with the slashes preserved, asserted from the `httptest` handler's `r.URL.EscapedPath()`.

**`pkg/handler/page_test.go`** — replace `pageview.NewService(pageTestVaults{dir: vaultDir}, storage.NewPageStorage(nil))` with a `PageSource` fake over the same fixture directory.

**`pkg/handler/api_test.go`** — line 53 uses `stubPageStorage{}` over an empty vault set and the page routes are not exercised there; the minimal change is a `PageSource` stub returning `(nil, nil)` for both methods.

**`e2e/harness.go`** — extend `stubHandler()` with the two page routes, answering from `stubRoot`:
- `/api/v1/pages/` → read the file at the decoded path under `stubRoot`, compute `links` (page name → relative path for every `[[target]]` the body carries) and `embeds` (body of every `![[target]]` on a line of its own), and return the JSON shape above. A missing file is `404 {"error":"not_found"}`.
- `/api/v1/pages/.../backlinks` → compute the inbound set with `markdown.Backlinks` over every `.md` file in `stubRoot` (the fixture vault is tiny, so a full read per request is correct and cheap here) and return `{"vault","path","name","backlinks":[{"path","name"}]}`.
- Keep the existing three list routes untouched. The stub is the test double, so it may use `markdown.Backlinks`; `pkg/pageview` may not.
- Route ordering matters: register the `/backlinks` route before the plain page route, or the greedy path match swallows the suffix.

**`e2e/page_view_test.go`** — no assertion changes. It must pass as written.

### 5. CHANGELOG

Add an `## Unreleased` bullet: the page view reads the vault service instead of walking the vault (14–27 s, 3/20 failing, before; ~1 ms against the service endpoints after), the backlink set is unchanged, and this supersedes spec 040's AC3 (one `ReadPage` per page) and AC7 (the symlink guard), both of which now live in the vault service.

</requirements>

<verification>
Run from the repo root; each must exit 0:
- `make precommit`
- `go test ./pkg/pageview/ ./pkg/vaultapi/ ./pkg/handler/ -count=1`
- `grep -n 'WalkDir\|os.ReadFile\|markdown.Backlinks\|indexTTL' pkg/pageview/pageview.go` prints nothing
- `go vet ./...`
</verification>

<acceptance_criteria>
- `make precommit` exits 0.
- `pkg/pageview/pageview.go` contains none of `filepath.WalkDir`, `os.ReadFile`, `markdown.Backlinks`, `indexTTL`.
- `pkg/pageview` has a `pageview_suite_test.go`, and its suite contains a spec that asserts, through a recording `PageSource`, exactly one `Page` and one `Backlinks` call per `Render` on two freshly constructed services, plus the source assertion above.
- `pkg/vaultapi`'s `Client` has `Page` and `Backlinks`, and a test covers a successful decode, `?vault=` present and absent, a 404 mapped to the typed not-found, and the escaped request URL for a path containing a space.
- `pkg/pageview`'s error-mapping specs cover not-found → `NotFoundError`, generic error → wrapped and not a `NotFoundError`, a `Backlinks` failure failing `Render`, and `Exists` returning `(false, nil)` for a missing page.
- `go test ./...` passes, including `pkg/pageview`, `pkg/vaultapi` and `pkg/handler`.
- `e2e/harness.go` compiles under `make precommit` (it carries no build tag). The Playwright suite is NOT run here — it needs Chromium and a free `:9090`; it belongs to the change's verification ladder, not to this prompt.
- No `time.Now()`, no new package-level mutable state, no new `go func()`.
</acceptance_criteria>
