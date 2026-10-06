---
status: failed
spec: [027-incremental-page-index-updates]
created: "2026-10-06T09:08:51Z"
queued: "2026-10-06T09:33:58Z"
completed: "2026-10-06T09:33:59Z"
branch: dark-factory/incremental-page-index-updates
lastFailReason: 'setup workflow: working tree is not clean; cannot switch to branch "dark-factory/incremental-page-index-updates"; uncommitted changes: specs/in-progress/027-incremental-page-index-updates.md'
---

# Read single page files through injectable reader and lister seams with fingerprints

<summary>
- The page index no longer reads a whole folder to build a snapshot; it reads one file at a time through two new injectable seams.
- A directory lister returns a folder's page-file entries together with the size and timestamps of each file.
- A single-file reader parses one page file and reports the file's size, modification time and status-change time, taken from a stat before the read.
- The fingerprint reads platform stat fields, so it compiles and passes on both the Linux container and the macOS deploy host.
- The cold startup build now goes through those seams, so every file has a fingerprint from the very first read.
- The index still answers list reads exactly as vault-cli's own folder listing would: same pages, same order, same exclusions, identical parsed fields.
- A test builds a real folder holding awkward files (a bare wikilink value, a file with no frontmatter, a file with invalid YAML, a non-markdown file, a subdirectory, a symlink out of the vault, a symlink to a page inside the vault, a non-ASCII filename) and asserts the index's snapshot deep-equals vault-cli's own listing of that folder.
- The two seams have counterfeiter test doubles, so later prompts can drive single-file updates with a fake reader and a fake lister.
- The watcher, the mutation service and the running wiring are untouched here, and the page-index doc changes by one sentence only; later prompts change the rest.
</summary>

<objective>
Add the injectable single-file reader and directory lister seams to `pkg/pageindex`, composed from vault-cli v0.159.0's exported `storage.ParseFrontmatterMap` and `domain.NewPage`, with symlink-following fingerprints (size, modification time, status-change time) that build on linux and darwin; move the index's cold build onto those seams; and prove the built snapshot is `reflect.DeepEqual` to the real `storage.PageStorage.ListPages` over the same folder. This is the read primitive every later prompt relies on.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md` (the Definition of Done the validation step checks).

Read the spec `specs/in-progress/027-incremental-page-index-updates.md` in full. This prompt covers Desired Behavior 2 (snapshot equivalence), Desired Behavior 3's fingerprint part, and Acceptance Criterion AC3's cold-build half. Later prompts add per-file updates (prompt 2), the stat-diff and write marks (prompt 3) and the wiring and docs (prompt 4).

Read these vault-cli v0.159.0 sources (in-container module path `/home/node/go/pkg/mod/github.com/bborbe/vault-cli@v0.159.0`; if that path is absent, run `go mod download github.com/bborbe/vault-cli` first):
- `pkg/storage/storage.go` — the interface the index still implements, verbatim:
  ```go
  //counterfeiter:generate -o ../../mocks/page-storage.go --fake-name PageStorage . PageStorage
  type PageStorage interface {
  	ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
  }
  ```
  `NewPageStorage(storageConfig *Config) PageStorage` (nil config → `DefaultConfig()`).
- `pkg/storage/page.go` — `(*pageStorage).ListPages` is the reference the new lister and cold build must match: it does `targetDir := filepath.Join(vaultPath, pagesDir)`, `os.ReadDir(targetDir)` (a missing directory returns `nil, nil`), then for each entry skips `entry.IsDir()`, skips names not ending in `.md`, computes `fileName := strings.TrimSuffix(entry.Name(), ".md")` and `filePath := filepath.Join(targetDir, entry.Name())`, and calls `readPageFromPath`; a failed read is warned (slog) and the entry skipped. The result is in `os.ReadDir` order.
- `pkg/storage/base.go` — `ParseFrontmatterMap(ctx context.Context, content []byte) (map[string]any, error)` (line 75, exported) is the parser to call. `(*baseStorage).readEntityComponentsFromPath` (line 303, unexported) is the glue to MIRROR, verbatim in behaviour:
  - `isSymlinkOutsideVault(filePath, vaultPath)` (line 375, unexported — reimplement it in vault-ui) returns true when `os.Lstat(path)` shows a symlink and `filepath.EvalSymlinks` resolves outside the vault (a broken symlink counts as outside);
  - `content, err := os.ReadFile(filePath)` then, separately, `if info, err := os.Stat(filePath); err == nil { t := info.ModTime().UTC(); modTime = &t }`;
  - `data, parseErr := b.parseToFrontmatterMap(ctx, content)` which is just `ParseFrontmatterMap(ctx, content)`;
  - `meta := domain.FileMetadata{Name: name, FilePath: filePath, ModifiedDate: modTime}`;
  - returns `data, meta, domain.Content(content), nil`.
- `pkg/domain/page.go` — `NewPage(data map[string]any, meta FileMetadata, content Content) *Page`.
- `pkg/domain/file_metadata.go` — `FileMetadata{Name string; FilePath string; ModifiedDate *time.Time}`.
- `pkg/domain/content.go` — `type Content string`.

Read these `github.com/bborbe/time` (`libtime`) v1.27.14 APIs you will use (verified): `type CurrentDateTimeGetter interface { Now() DateTime }`, `libtime.NewCurrentDateTime() CurrentDateTime`, `type WaiterDuration interface { Wait(ctx context.Context, duration Duration) error }`, `libtime.NewWaiterDuration()`, `libtime.WaiterDurationFunc`.

Current index code you will change — read it fully first:
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey(vaultPath, pagesDir string) Key`, the `PageIndex` interface, `type pageIndex struct { mu sync.Mutex; entries map[Key]*entry; pageStorage storage.PageStorage; currentDateTimeGetter libtime.CurrentDateTimeGetter; waiter libtime.WaiterDuration }`, and `func NewPageIndex(pageStorage storage.PageStorage, currentDateTimeGetter libtime.CurrentDateTimeGetter, waiter libtime.WaiterDuration) PageIndex`.
- `pkg/pageindex/pageindex_build.go` — `(*pageIndex).execute(ctx, key, b)` currently calls `p.pageStorage.ListPages(buildCtx, key.VaultPath, key.PagesDir)`; that is the only place the folder-wide storage is used for the index's own builds.
- `pkg/pageindex/pageindex_test.go` — the `storageFake` (a `*mocks.PageStorage` wrapper) and `newIndex`.
- `pkg/pageindex/mocks/pageindex-page-index.go` — the generated `PageIndex` fake and its header shape.

Callers you must update (verified by grep):
- `pkg/factory/pageindex.go` — `CreatePageIndex(pageStorage storage.PageStorage, currentDateTimeGetter libtime.CurrentDateTimeGetter) pageindex.PageIndex`.
- `main.go:54` — `factory.CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime())`.
- `pkg/factory/api_test.go:71`, `pkg/factory/watcher_internal_test.go:29` (`testPageIndex`), `pkg/factory/pageindex_test.go:141/188/198/223` (with the `countingPageStorage`/`listPagesCounts` helpers), `pkg/factory/mutations_index_test.go:42`, `pkg/factory/pane_test.go:93`, `pkg/factory/watcher_refresh_test.go:195` (the `refreshStorage` fake) — every one constructs the index and must keep compiling.

Patterns in this repo to follow:
- `pkg/statuscache/statuscache.go` + `pkg/statuscache/statuscache_suite_test.go` — package doc comment, mutex-guarded state, Ginkgo suite file shape.
- `pkg/websocket/metrics.go` — the `//counterfeiter:generate -o ./mocks/<file>.go --fake-name <Name> . <Interface>` directive form; `pkg/mutations/mocks/event_publisher.go` — a generated fake with the copyright header prepended.
- `pkg/cleanup/cleanup.go` — `glog.Errorf` / `glog.V(2).Infof` logging style.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md` — `#nosec G304` annotations need a reason.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — line length 100, function length 80, license header.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. The two seams (`pkg/pageindex/seams.go`)

Every new `.go` file (source, test, generated mock) starts with the repo's existing copyright header — copy it verbatim from `pkg/statuscache/statuscache.go`.

Do NOT add a package doc comment to `seams.go` (the package doc lives in `pageindex.go`). Define exactly:

```go
// FileFingerprint is the pre-read stat of one page file. Size, ModTime and
// StatusChangeTime come from a single stat that follows symlinks.
type FileFingerprint struct {
	Size             int64
	ModTime          time.Time
	StatusChangeTime time.Time
}

// FileEntry is one directory entry with its current fingerprint.
type FileEntry struct {
	Name        string
	Fingerprint FileFingerprint
}

//counterfeiter:generate -o ./mocks/pageindex-page-reader.go --fake-name PageReader . PageReader

// PageReader reads one page file from a folder. The filename is the file's
// base name including the ".md" suffix. It returns the parsed page and the
// file's pre-read fingerprint; a non-nil error means the file is excluded
// (absent, unreadable, unparsable, or a symlink out of the vault) and the
// returned fingerprint is still the best available (zero when the stat failed).
type PageReader interface {
	ReadPage(
		ctx context.Context,
		vaultPath string,
		pagesDir string,
		filename string,
	) (*domain.Page, FileFingerprint, error)
}

//counterfeiter:generate -o ./mocks/pageindex-directory-lister.go --fake-name DirectoryLister . DirectoryLister

// DirectoryLister lists one folder's page-file entries with their current
// fingerprints, in os.ReadDir order (filename ascending).
type DirectoryLister interface {
	ListFiles(ctx context.Context, vaultPath string, pagesDir string) ([]FileEntry, error)
}

// NewPageReader returns the production single-file reader.
func NewPageReader() PageReader

// NewDirectoryLister returns the production directory lister.
func NewDirectoryLister() DirectoryLister
```

### 2. Fingerprints (`pkg/pageindex/fingerprint_linux.go`, `pkg/pageindex/fingerprint_darwin.go`)

One unexported function per platform, same signature, build-tagged:

```go
// statFingerprint returns path's size, modification time and status-change
// time in UTC. It follows symlinks (os.Stat, not os.Lstat).
func statFingerprint(path string) (FileFingerprint, error)
```

- `fingerprint_linux.go` starts with `//go:build linux`; the status-change time is `syscall.Stat_t.Ctim` (`time.Unix(stat.Ctim.Unix()).UTC()`).
- `fingerprint_darwin.go` starts with `//go:build darwin`; the status-change time is `syscall.Stat_t.Ctimespec` (`time.Unix(stat.Ctimespec.Unix()).UTC()`).
- Both: `Size` is `info.Size()`; `ModTime` is `info.ModTime().UTC()`; when `info.Sys().(*syscall.Stat_t)` does not assert, leave `StatusChangeTime` zero rather than failing.
- Return the raw `os.Stat` error unwrapped; the caller wraps it with the context.

`syscall` is not a banned package in this repo, and the container builds linux/arm64 while the deploy host is darwin, so both files are required and both must compile.

### 3. The reader (`pkg/pageindex/reader.go`)

`pageReader` (unexported struct, returned by `NewPageReader`) implements `ReadPage` by mirroring `readEntityComponentsFromPath` exactly:

1. `filePath := filepath.Join(vaultPath, pagesDir, filename)`.
2. `fingerprint, err := statFingerprint(filePath)` — on error return `(nil, FileFingerprint{}, errors.Wrapf(ctx, err, "stat %s", filePath))`.
3. If `isSymlinkOutsideVault(filePath, vaultPath)` return `(nil, fingerprint, errors.Errorf(ctx, "symlink outside vault: %s", filePath))`.
4. `content, err := os.ReadFile(filePath) //#nosec G304 -- user-controlled vault path` — on error return `(nil, fingerprint, errors.Wrapf(ctx, err, "read file %s", filePath))`.
5. `if info, statErr := os.Stat(filePath); statErr == nil { t := info.ModTime().UTC(); modTime = &t }` (a failed second stat leaves `ModifiedDate` nil, as vault-cli does).
6. `data, parseErr := storage.ParseFrontmatterMap(ctx, content)` — on error return `(nil, fingerprint, errors.Wrapf(ctx, parseErr, "parse frontmatter %s", filePath))`.
7. Return `domain.NewPage(data, domain.FileMetadata{Name: strings.TrimSuffix(filename, ".md"), FilePath: filePath, ModifiedDate: modTime}, domain.Content(content)), fingerprint, nil`.

Reimplement `isSymlinkOutsideVault(path, vaultPath string) bool` in `pkg/pageindex` with the same body as vault-cli's `pkg/storage/base.go` (Lstat → symlink check → `filepath.EvalSymlinks(vaultPath)` → `filepath.Abs` → `filepath.EvalSymlinks(path)` → a broken symlink returns true → `!strings.HasPrefix(absResolved, absVault)`). Do not export it.

### 4. The lister (`pkg/pageindex/lister.go`)

`directoryLister` (unexported struct, returned by `NewDirectoryLister`) implements `ListFiles` mirroring `ListPages`'s walk:

1. `targetDir := filepath.Join(vaultPath, pagesDir)`; `entries, err := os.ReadDir(targetDir)`. A missing directory returns `(nil, nil)`; an existing directory always returns a non-nil slice (`make([]FileEntry, 0, len(entries))`), empty when it holds no page files. Any other error is `errors.Wrapf(ctx, err, "read directory %s", targetDir)`.
2. For each entry: skip `entry.IsDir()`; skip names not ending in `.md`. For the rest, call `statFingerprint(filepath.Join(targetDir, entry.Name()))`; append `FileEntry{Name: entry.Name(), Fingerprint: fingerprint}` — when the stat fails, still append the entry with a zero `FileFingerprint` so the reader's exclusion path runs and the file is dropped from the snapshot, and the recorded zero fingerprint keeps a later stat-diff from re-reading it every pass.
3. Return the entries in `os.ReadDir` order. Do not sort again.

### 5. Move the cold build onto the seams (`pkg/pageindex/pageindex.go`, `pkg/pageindex/pageindex_build.go`)

- Replace the `pageStorage storage.PageStorage` field with `reader PageReader` and `lister DirectoryLister`.
- Change the constructor (and its doc comment) to:
  ```go
  func NewPageIndex(
  	reader PageReader,
  	lister DirectoryLister,
  	currentDateTimeGetter libtime.CurrentDateTimeGetter,
  	waiter libtime.WaiterDuration,
  ) PageIndex
  ```
- Add a per-key fingerprint store: on `entry` add `fingerprints map[string]FileFingerprint` keyed by the file's base name including `.md`. It is populated by the cold build and read by later prompts.
- Rewrite `execute` so the folder read becomes: `entries, err := p.lister.ListFiles(buildCtx, key.VaultPath, key.PagesDir)`; on error keep the existing failure path (log `glog.Errorf` naming the key, keep the previous snapshot). On success, a nil listing (missing folder) publishes a nil pages slice; otherwise pages start as `make([]*domain.Page, 0, len(entries))` so nil-vs-empty matches vault-cli. For each entry, first check `buildCtx.Err()`; when set, fail the build through the same failure path as a listing error (log, keep the previous snapshot, publish nothing). Otherwise call `p.reader.ReadPage(buildCtx, key.VaultPath, key.PagesDir, entry.Name)`, record the returned fingerprint in a fresh `fingerprints` map for that name, append the page in listing order on success, and on a read error log a per-file warning (mirroring vault-cli's "skipping unreadable page" warning) and skip the entry. Cap per-file warnings at 10 per build, then emit one summary line with the total skipped count, as vault-cli's `maxUnreadablePageWarnings` does in `pkg/storage/page.go`. Publish the pages plus the fingerprint map under the lock, exactly as the current code publishes the pages. Do not change the `build`/`inflight`/`followUp` concurrency machinery or any other rule.
- `ListPages`, `Refresh`, `MarkDirty`, `MarkAllDirty`, `Rescan`, `Build` keep their current signatures and behaviour.

### 6. Counterfeiter fakes

Generate `pkg/pageindex/mocks/pageindex-page-reader.go` and `pkg/pageindex/mocks/pageindex-directory-lister.go` from the directives in step 1 (from `pkg/pageindex`, `go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; the `@version` form leaves `go.mod` untouched — do not add counterfeiter to `go.mod`). Prepend the copyright header the existing generated mocks carry. When `go generate`/counterfeiter regenerates `pkg/pageindex/mocks/pageindex-page-index.go`, re-prepend its copyright header too (its first lines must stay the copyright block followed by `// Code generated by counterfeiter. DO NOT EDIT.`). If the module proxy is unreachable, hand-write both fakes in the exact shape of `pkg/mutations/mocks/event_publisher.go`.

### 7. Update every index-construction call site so the tree compiles

- `pkg/factory/pageindex.go`: change `CreatePageIndex` to
  ```go
  func CreatePageIndex(
  	reader pageindex.PageReader,
  	lister pageindex.DirectoryLister,
  	currentDateTimeGetter libtime.CurrentDateTimeGetter,
  ) pageindex.PageIndex
  ```
  and pass `reader, lister, currentDateTimeGetter, libtime.NewWaiterDuration()` to `NewPageIndex`.
- `main.go`: replace the comment above the `CreatePageIndex` call (it currently talks about "one shared page storage") with one saying the process-wide page index reads single files through the production reader and lister seams, shared by every vault; build the index with `factory.CreatePageIndex(pageindex.NewPageReader(), pageindex.NewDirectoryLister(), libtime.NewCurrentDateTime())`; drop the now-unused `github.com/bborbe/vault-cli/pkg/storage` import and add `github.com/bborbe/vault-ui/pkg/pageindex`.
- `pkg/factory/watcher_internal_test.go` (`testPageIndex`), `pkg/factory/api_test.go`, `pkg/factory/pane_test.go`: pass the real seams `pageindex.NewPageReader(), pageindex.NewDirectoryLister()`.
- `pkg/factory/pageindex_test.go`: replace `countingPageStorage()`/`listPagesCounts` with counting decorators over the real seams — a small type that embeds `pageindex.PageReader` and `pageindex.DirectoryLister` (or wraps `NewPageReader()`/`NewDirectoryLister()`) and counts `ReadPage` and `ListFiles` calls, exposing per-`(vaultPath, pagesDir)` counts for `ListFiles`. Keep every assertion: the warm-up builds each `(vault, folder)` pair at least once and 25 warm requests add 0 reads.
- `pkg/factory/mutations_index_test.go`: build the index over counting seams as above; keep the "reflects a publishing write … with exactly one rebuild" and "serves an external edit from memory until POST /api/cache/reload" assertions (count `ListFiles` calls where the test counted `ListPages`).
- `pkg/factory/watcher_refresh_test.go`: replace the `refreshStorage` PageStorage fake with a lister+reader pair serving the same test-controlled per-`(vaultPath, pagesDir)` content, and keep `callsFor`/`totalCalls`/`setGate`/`reset` semantics by counting `ListFiles` calls (a folder-level `Refresh` is one listing). Required shape:
  - `ListFiles` captures the key's current content (pages + per-file versions) at call START, before blocking on the gate, and stores that capture as the key's "last listed" content; it returns one `FileEntry` per page.
  - `ReadPage` serves the page from the content captured by the most recent `ListFiles` of that key — never from the live content — so the existing "captured v1 before blocking" test keeps its meaning.
  - `setError` makes `ListFiles` return the error.
  - Each file carries a per-file version counter bumped whenever that file's content changes; its fingerprint is derived from the counter (e.g. `Size: version`, `ModTime: time.Unix(version, 0).UTC()`), so a content change always changes the fingerprint.
  Keep every assertion in the file passing.

### 8. Tests

- `pkg/pageindex/pageindex_test.go`: rewrite the `storageFake` as a reader/lister fake (a `*mocks.PageReader` and a `*mocks.DirectoryLister` serving per-key pages and blocking on a channel, replacing `ListPagesStub`; every fake file's fingerprint changes whenever its content changes, via a per-file version counter), and keep every existing `Describe`/`It` assertion. `newIndex` now calls `NewPageIndex(reader, lister, clock, fastWaiter())`. The "cold read shares one build" and "coalesces a burst of refreshes into one follow-up" tests must still assert exactly one listing per build.
- Add `pkg/pageindex/equivalence_test.go` (external package `pageindex_test`) with the AC3 fixture and its cold-build half: build a real temp folder containing a plain page, a page whose frontmatter holds a bare `[[wikilink]]` value, a file without frontmatter, a file with invalid YAML, a non-`.md` file, a subdirectory, a symlink pointing outside the vault, a symlink pointing to a page inside the vault, and a page with a non-ASCII filename. Build the index over the real seams for that folder, then assert
  ```go
  Expect(reflect.DeepEqual(got, want)).To(BeTrue())
  ```
  where `want, err := storage.NewPageStorage(nil).ListPages(ctx, dir, folder)` and `got, err := index.ListPages(ctx, dir, folder)`. Fixture rules:
  - The outside-symlink target lives in a separate `GinkgoT().TempDir()` (never under the vault dir), and the fixture also holds a broken symlink (`*.md` pointing at a non-existent path).
  - Check every error with `Expect(err).NotTo(HaveOccurred())` — no `_` discards for `want`/`got`.
  - Positive control: before the DeepEqual, assert the exact expected page-name list of `want` (e.g. `Expect(names(want)).To(Equal([]string{...}))`), so a fixture that silently excludes everything cannot pass.
  - Add an empty-folder case (exists, no files: both sides non-nil empty, DeepEqual true) and a missing-folder case (both sides nil, DeepEqual true).
  This proves the same pages, order, exclusions and `FrontmatterMap`/`FileMetadata`/`Content` values. Prompt 3 extends this fixture with the per-file and stat-diff steps.
- `pkg/pageindex` needs ≥ 80% statement coverage (see verification).

### 8b. Docs

In `docs/page-index.md`, replace the sentence ending "and vault-ui never parses a page file itself." with: vault-ui reads single page files through its own reader seam, composed from vault-cli's exported `storage.ParseFrontmatterMap` and `domain.NewPage`, so a parsed page is identical to what vault-cli's folder listing returns.

### 9. CHANGELOG

Under `## Unreleased` in `CHANGELOG.md` (the section already exists) append a `refactor:` bullet: the page index now reads single page files through injectable reader and lister seams composed from vault-cli's exported parser and constructor, records size/modification/status-change fingerprints per file, and the cold build goes through those seams; the built snapshot is proven deep-equal to vault-cli's folder listing. Follow `changelog-guide.md`.

### 10. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk Desired Behaviors 2 and 3 (fingerprint part) and AC3's cold-build half against the tests you wrote and name the test that establishes each.

</requirements>

<constraints>
- The index keeps the `storage.PageStorage` read contract: it still implements `ListPages` and stays the only list data source behind `ops.NewListOperation`. It no longer *depends* on a folder-wide `storage.PageStorage` for its own builds. vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli subprocess is spawned; no vault-cli change and no version bump.
- Snapshot order equals `os.ReadDir` order (filename ascending), so responses stay byte-identical. Order by the file's base name **including** the `.md` suffix, not the trimmed page name (trimming changes the order of names like `A.md` vs `A.B.md`).
- Published snapshots and the `*domain.Page` values in them are never mutated; `go test -race` must stay clean.
- The status-change-time fingerprint reads platform stat fields; it must build and pass on linux (container) and darwin (deploy).
- `pkg/mutations` reads and `pkg/cleanup` keep reading disk directly. Mutation write semantics, queue ordering and frames are unchanged. The watcher's watched directory set is unchanged. Response bodies, status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract).
- No new HTTP route, query parameter, opt-out flag, configurable interval or configurable fingerprint.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks for the new reader and lister seams, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` for the rescan clock, ≥ 80% coverage on changed packages.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- `NewPageIndex`/`CreatePageIndex` take the reader and lister as injected seams (decided): disk-backed factory tests count reads through them.
- The lister returns an entry whose stat fails with a zero fingerprint (decided); the AC3 broken-symlink fixture is the guard.
- Only AC3's cold-build half is in scope here; prompt 3 adds its per-file and stat-diff steps.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0 (format, vet, `go test -race ./...`, Python lint/typecheck/tests).

```
go test -race ./pkg/pageindex/... ./pkg/factory/...
```
Must pass.

```
go test -race -coverprofile=/tmp/pageindex.cover ./pkg/pageindex/ && go tool cover -func=/tmp/pageindex.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (≥ 80%).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/pageindex/
```
Must exit 0 (no raw goroutines in non-test code).

```
grep -q 'func NewPageReader' pkg/pageindex/seams.go && grep -q 'func NewDirectoryLister' pkg/pageindex/seams.go && grep -q 'go:build linux' pkg/pageindex/fingerprint_linux.go && grep -q 'go:build darwin' pkg/pageindex/fingerprint_darwin.go
```
Must exit 0 (both seams and both platform fingerprint files exist).

```
! grep -n 'pageStorage' pkg/pageindex/pageindex.go pkg/pageindex/pageindex_build.go
```
Must exit 0 (the index no longer holds a folder-wide storage).

```
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go vet ./pkg/pageindex/... ./pkg/factory/... && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -buildvcs=false ./...
```
Must exit 0 (the darwin fingerprint file compiles and vets on the deploy platform).
</verification>
