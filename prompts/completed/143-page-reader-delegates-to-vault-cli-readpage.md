---
status: completed
summary: Delegated the page index's single-file read to vault-cli's storage.PageStorage.ReadPage, deleting the duplicate isSymlinkOutsideVault guard while keeping the pre-read fingerprint; updated all 9 call sites, docs and CHANGELOG, with make precommit exiting 0.
execution_id: vault-ui-single-page-reader-exec-143-page-reader-delegates-to-vault-cli-readpage
dark-factory-version: v0.196.0
created: "2026-10-06T20:05:00Z"
queued: "2026-10-06T20:14:04Z"
started: "2026-10-07T16:16:31Z"
completed: "2026-10-07T16:22:44Z"
branch: dark-factory/143-page-reader-delegates-to-vault-cli-readpage
---

# Read single page files through vault-cli's ReadPage

<summary>
- The page index's single-file read stops parsing pages itself and delegates to vault-cli's single-page read
- A page read this way is the page vault-cli's own folder listing produces, because there is now one parse path rather than two
- The second copy of the symlink-out-of-vault guard is deleted — vault-cli enforces it
- The per-file fingerprint the stat-diff depends on is still taken here, before the read
- Existing behaviour is unchanged for every filename the index actually produces, except a file named exactly `.md`, whose stripped base name is empty and which vault-cli rejects
- The vault-cli dependency is bumped to the version that exports the read
</summary>

<objective>
Delete the duplicated page-read path in `pkg/pageindex`. `pageReader` currently re-implements vault-cli's file read — including its own copy of `isSymlinkOutsideVault` — so two guards have to be kept in sync. Delegate the read to vault-cli's `storage.PageStorage.ReadPage` (exported in v0.162.0) and keep only what is genuinely vault-ui's: the fingerprint stat.
</objective>

<context>
Read CLAUDE.md for project conventions, `docs/dod.md` (this repo's Definition of Done) and `docs/page-index.md` (the page index's staleness, frame-ordering and key-derivation rules).

Read these files before implementing:

- `pkg/pageindex/reader.go` — the file under change. `pageReader.ReadPage` builds `filePath`, calls `statFingerprint`, then its own `isSymlinkOutsideVault`, then `os.ReadFile`, a second `os.Stat` for `ModifiedDate`, `storage.ParseFrontmatterMap` and `domain.NewPage`. Everything from `isSymlinkOutsideVault` onward is what vault-cli's `ReadPage` already does.
- `pkg/pageindex/seams.go` — the `PageReader` interface and `NewPageReader`. The interface does **not** change; only the concrete constructor gains a dependency.
- `pkg/pageindex/fingerprint_darwin.go` / `fingerprint_linux.go` — `statFingerprint`, which stays. It reads platform stat fields vault-cli does not expose, so it cannot be delegated.
- vault-cli's `ReadPage` — source at module cache `github.com/bborbe/vault-cli@v0.162.0/pkg/storage/page.go`. This repo has **no** `vendor/` directory, and the cache holds v0.162.0 only after requirement 5 has run, so treat the signature inlined in requirement 1 as authoritative. It rejects a `name` containing a path separator, applies `isSymlinkOutsideVault`, reads the file, stats it for `ModifiedDate`, and returns `domain.NewPage(...)`. Note its contract: **`name` is the base name without the `.md` suffix**, and it returns **no fingerprint**.
- Coding plugin `go-time-injection.md` (constructor injection — the pattern requirement 3 mandates) and `go-error-wrapping-guide.md` (`errors.Wrapf` with `ctx`) — reference rather than re-derive.
- `main.go` — the process-wide wiring at the `factory.CreatePageIndex(...)` call, which currently passes `pageindex.NewPageReader()`.

Call sites to update (all pass `storage.NewPageStorage(nil)`; `ReadPage` takes `pagesDir` per call, so the storage needs no per-vault configuration):

- `main.go` (the `factory.CreatePageIndex` call)
- `pkg/pageindex/equivalence_test.go` (`newEquivalenceIndex`)
- `pkg/pageindex/pageindex_statdiff_test.go` (the `recordingReader` wrapper)
- `pkg/factory/pane_test.go`, `pkg/factory/watcher_internal_test.go` (two sites), `pkg/factory/pageindex_test.go`, `pkg/factory/api_test.go`, `pkg/factory/factory_test.go`

Add the `github.com/bborbe/vault-cli/pkg/storage` import wherever it is not already present.
</context>

<requirements>
1. Give `pageReader` the storage seam and delegate the read. Replace `reader.go`'s `pageReader` and its `ReadPage` with:

   ```go
   // pageReader reads one page file from disk through vault-cli's single-page
   // read, so a page parsed here is the page vault-cli's own folder listing
   // produces.
   type pageReader struct {
       pages storage.PageStorage
   }

   // ReadPage parses one page file and reports its pre-read fingerprint.
   //
   // The fingerprint is this package's own — it is taken before the read so the
   // stat-diff can decide whether a later read is needed — while the read itself
   // delegates to vault-cli's storage.PageStorage.ReadPage, which enforces the
   // symlink-out-of-vault guard and the frontmatter parse.
   func (r *pageReader) ReadPage(
       ctx context.Context,
       vaultPath string,
       pagesDir string,
       filename string,
   ) (*domain.Page, FileFingerprint, error) {
       filePath := filepath.Join(vaultPath, pagesDir, filename)

       fingerprint, err := statFingerprint(filePath)
       if err != nil {
           return nil, FileFingerprint{}, errors.Wrapf(ctx, err, "stat %s", filePath)
       }

       page, err := r.pages.ReadPage(
           ctx,
           vaultPath,
           pagesDir,
           strings.TrimSuffix(filename, ".md"),
       )
       if err != nil {
           return nil, fingerprint, err
       }
       return page, fingerprint, nil
   }
   ```

   Three details are load-bearing:
   - **The fingerprint is still returned on failure**, exactly as today — a caller must be able to record the stat even when the read failed.
   - **`filename` carries `.md` and `name` does not.** `ReadPage`'s parameter is the base name, so strip the suffix; passing the filename through unchanged would look for `X.md.md`.
   - **Do not add a second `isSymlinkOutsideVault` call or re-check anything.** vault-cli's read enforces the guard; that is the point of the change.

2. Delete `isSymlinkOutsideVault` from `pkg/pageindex/reader.go` and drop the imports it needed. After the change `reader.go` needs `context`, `path/filepath`, `strings`, `github.com/bborbe/errors`, `github.com/bborbe/vault-cli/pkg/domain` and `github.com/bborbe/vault-cli/pkg/storage`. `os` and `time` are no longer used there. Confirm with `goimports`/`go vet` rather than by hand.

3. Change the constructor in `pkg/pageindex/seams.go` to take the seam and keep its doc comment:

   ```go
   // NewPageReader returns the production single-file reader.
   func NewPageReader(pages storage.PageStorage) PageReader {
       return &pageReader{pages: pages}
   }
   ```

   Do **not** construct the storage inside `NewPageReader` — the dependency is injected, as every other seam in this package is.

4. Update every call site listed in `<context>` to `pageindex.NewPageReader(storage.NewPageStorage(nil))`, adding the `storage` import where needed.

5. Bump the vault-cli dependency to the version that exports the read:

   ```
   go get github.com/bborbe/vault-cli@v0.162.0
   go mod tidy
   ```

   Write the code that imports the new API **before** running `go mod tidy`; tidy drops a direct requirement nothing imports yet.

6. Add a `## Unreleased` section to `CHANGELOG.md` (create it above the current top version heading) with one bullet:

   ```
   ## Unreleased

   - refactor: Read the page index's single page files through vault-cli's `storage.PageStorage.ReadPage` instead of a local re-implementation, so one parse path and one symlink-out-of-vault guard serve both the index and vault-cli's folder listing; the per-file fingerprint the stat-diff uses is still taken here before the read.
   ```

7. Update `docs/page-index.md`. Its opening paragraph currently says the reader seam is "composed from vault-cli's exported `storage.ParseFrontmatterMap` and `domain.NewPage`" — after this change the read delegates to `storage.PageStorage.ReadPage`. Correct that sentence, and add one sentence recording **why**, since this prompt is not linked to a spec and is the only place the rationale currently lives: the read is delegated so that one parse path and one symlink-out-of-vault guard serve both the index and vault-cli's own folder listing, instead of two copies that have to be kept in sync. Leave the rest of the document (staleness bounds, frame ordering, key derivation, incremental updates) unchanged.

8. Before finishing, re-run `<verification>` and confirm it passes, then walk each `<summary>` bullet against the change and confirm it still holds.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT change the `PageReader` or `DirectoryLister` interfaces, `statFingerprint`, `FileFingerprint`, the merge/splice logic, or anything in `pageindex_build.go`. This change touches the concrete reader, its constructor, the call sites, the dependency and the docs.
- Do NOT regenerate counterfeiter mocks: the `PageReader` interface is unchanged, so `mocks/pageindex-page-reader.go` stays as it is.
- The `equivalence_test.go` fixtures and assertions must keep passing unchanged — that test compares the index's snapshot against vault-cli's own `ListPages` over awkward files (outside symlink, broken symlink, inside symlink, no frontmatter, invalid YAML, non-ASCII name), which is exactly the property this refactor must preserve. Do not weaken it.
- Two deliberate behaviour changes, both acceptable and both worth stating in the commit body rather than working around:
  - A parse failure's message loses the file path. vault-ui wrapped it as `parse frontmatter <path>`; vault-cli wraps it as `parse frontmatter`. The caller still logs the file name — `collect` emits `skipping unreadable page %q in %q/%q: %v` with the entry name — so nothing becomes undiagnosable.
  - A `filename` containing a path separator or `..` is now an error rather than a read. No realistic caller hits it: `DirectoryLister.ListFiles` yields `os.ReadDir` entries and `EventFilename` rejects a path that is not a single plain filename inside the key's folder. One pathological case does change — a file named exactly `.md`: `ListFiles` keeps it (the name ends in `.md`), `validatePageFilename` accepts it, and `strings.TrimSuffix(".md", ".md")` yields `""`, which vault-cli rejects. No real vault has such a file and the equivalence fixture does not include one.
- Error handling uses `github.com/bborbe/errors` (`errors.Wrapf(ctx, ...)`); never `fmt.Errorf`.
- Repo-relative paths only — no absolute or home-relative paths.
- Formatting and vet are enforced by `make precommit`.
</constraints>

<verification>
Run `make precommit` — must pass (`sync`, `format`, `go-vet`, `test`, `check`; this repo's precommit covers the Go half via `go-test` and the Python half via `pytest`/`ruff`/`mypy`).

Then confirm the delegation is in place and the duplicate guard is gone:
`grep -nE 'isSymlinkOutsideVault' pkg/pageindex/reader.go` — must print nothing; the identifier may not appear, not even in a comment.
`grep -n "r.pages.ReadPage" pkg/pageindex/reader.go` — must print at least one line.

Then confirm the dependency moved:
`grep -n "bborbe/vault-cli" go.mod` — must show `v0.162.0`.
</verification>
