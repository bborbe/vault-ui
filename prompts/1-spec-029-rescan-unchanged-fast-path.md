---
status: draft
spec: [029-rescan-allocates-only-what-changed]
created: "2026-10-08T09:07:08Z"
branch: dark-factory/rescan-allocates-only-what-changed
---

# Make a page-index rescan allocate only what changed

<summary>
- A rescan pass over a folder that has not changed now allocates nothing beyond the directory listing it has to perform to detect the change.
- An unchanged pass keeps the published snapshot exactly as it is, so a concurrent reader's page pointers do not move.
- An unchanged pass keeps the folder's recorded file fingerprints exactly as they are, instead of rebuilding a fresh copy every 50 seconds.
- A pass that finds K changed, added or removed files does work and allocates in proportion to K, not to the folder's size.
- A file a board write marked stale is always re-read, even when no fingerprint changed, so a reader still sees its own write.
- Every existing observable of a rescan is untouched: which files are re-read, what the snapshot contains and in what order, how a write mark is resolved, the WebSocket frame ordering and read-your-writes.
- The 50 second cadence, the 60 second staleness ceiling and the forced-reload path are unchanged.
- The change is proven by a test that measures the allocation of one unchanged pass against the directory listing it performs, and asserts the published snapshot and the recorded fingerprints were not replaced.
- A `## Unreleased` changelog bullet records the fix.
</summary>

<objective>
Make a rescan's cost proportional to what changed. A stat-diff over a folder whose entries are unchanged must publish nothing, keep the published snapshot and the recorded fingerprint set exactly in place, and allocate no per-page structure — its allocation is bounded by the one directory listing it performs to detect change. A stat-diff that finds K changed, added or removed files must allocate in proportion to K, not to the folder's file count. Every observable of the stat-diff stays exactly as it is today.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present and `docs/dod.md` for the project's Definition of Done.

Read the spec `specs/in-progress/029-rescan-allocates-only-what-changed.md` in full. This prompt covers Desired Behaviors 1, 2, 3, 4 and 5, and Acceptance Criteria AC2, AC3, AC4 and AC7. Prompt 2 documents the allocation contract in `docs/page-index.md`; it does not touch production code.

Read the current code before changing anything:
- `pkg/pageindex/pageindex_build.go` — `executeListing`, `collect`, `recordedFingerprints`, `mergeSnapshot`, `snapshotReads`, `fileRead`, `snapshotMerge` (`listed`/`unlisted`/`kept`), `samePages`, `pendingLocked`, `acquireLocked`, `release`.
- `pkg/pageindex/pageindex.go` — the `entry` struct fields (`snapshot`, `hasSnapshot`, `fingerprints`, `fileReadSeq`, `requestSeq`, `dirtySeq`, `dirtyResolvedSeq`, `reloadSeq`, `reloadResolvedSeq`, `writeMarked`, `revision`), `build`, `buildKind` and its constants (`buildCold`, `buildStatDiff`, `buildFileReads`, `buildReload`), `takeReadSeq`, `Refresh`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `Rescan`.
- `pkg/pageindex/pageindex_file.go` — `applyFileReadLocked`, `splicePage` (reuse; do not duplicate ordering logic).
- `pkg/pageindex/reader.go` — `pageReader.ReadPage` takes the fingerprint before the read.
- `pkg/pageindex/lister.go` — `directoryLister.ListFiles` returns `nil` for a missing folder and returns the entries in `os.ReadDir` (filename ascending) order.
- `pkg/pageindex/seams.go` — `FileFingerprint`, `FileEntry`, `PageReader`, `DirectoryLister`.
- `pkg/pageindex/export_test.go` — the existing `NewPageIndexWithWarnf` and `FilesReadTotal` accessors.
- `pkg/pageindex/pageindex_statdiff_test.go` — the `recordingReader`, `recordingLister`, `realIndex`, `newRealIndex`, `settle`, `newStatDiffVault` helpers, and the AC4(a) test that asserts the unchanged snapshot's page pointer is identical before and after a rescan. **This file is frozen — do not modify it.**
- `pkg/pageindex/equivalence_test.go` — the AC3 equivalence fixture. **This file is frozen — do not modify it.**
- `pkg/pageindex/pageindex_revision_test.go` — the "does not advance when a stat-diff finds nothing changed" case. **This file is frozen — do not modify it.**
- `pkg/pageindex/pageindex_test.go` — the `storageFake` reader/lister harness, `newIndex`, `fastWaiter`, `titles`.
- `docs/page-index.md` — the current `Incremental updates` section.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

The read-sequence rule is binding and unchanged: every single-file read takes `p.takeReadSeq()` under the mutex at read START and applies its result to the current snapshot only when `seq >= e.fileReadSeq[name]` (via `applyFileReadLocked` or `snapshotMerge.listed`), then sets `e.fileReadSeq[name] = seq`; a listing also takes a seq at listing start. A cancelled caller never aborts shared work nor removes a page. All storage calls stay bounded by the existing per-call `rebuildTimeout`.
</context>

<requirements>

### 1. Split the stat-diff into "list once, then decide"

Today `executeListing` (in `pkg/pageindex/pageindex_build.go`) hands off to `collect`, which lists the folder, copies the recorded fingerprints with `recordedFingerprints`, and always allocates a `snapshotReads` whose `order` slice and `reads` map are both sized to the folder's entry count — even when nothing changed. Then `mergeSnapshot` always allocates a fresh pages slice and a fresh fingerprint map.

Restructure so that:
- `executeListing` takes the listing seq (`p.takeReadSeq()`) and calls `p.lister.ListFiles(ctx, key.VaultPath, key.PagesDir)` exactly once, directly (not through a helper that also allocates per-name state).
- On a listing error: log with `glog.Errorf` naming the key (as today), set `b.err`, publish nothing, and leave the folder mark pending — `e.dirtyResolvedSeq` is NOT advanced. This is unchanged behaviour (see Failure Modes in the spec).
- The listing is then compared against the entry's recorded fingerprints **under the mutex**, before any per-name structure is allocated. No `recordedFingerprints` whole-map copy is made; the comparison reads `e.fingerprints` in place.
- `full` is still `b.kind == buildCold || b.kind == buildReload || !e.hasSnapshot` (a cold key and a forced reload re-read every file and never take the fast path).

### 2. The unchanged fast path

A pass is an unchanged pass exactly when all of the following hold:
- `full` is false (not a cold build, not a forced reload, the key has a snapshot);
- no file was consumed from `e.writeMarked` for this pass (no forced names — see requirement 5);
- the listing's name set equals the recorded name set (`e.fingerprints`), and
- for every listed entry, `e.fingerprints[fe.Name] == fe.Fingerprint`.

Establish the name-set equality allocation-free: since `directoryLister.ListFiles` returns distinct names and every listed name must be present in `e.fingerprints`, the two sets are equal exactly when `len(entries) == len(e.fingerprints)` and every listed entry's recorded fingerprint equals the listed one. Also require that the missing/empty state did not flip: `(entries == nil) == (e.snapshot == nil)`, because a missing folder lists as `nil` while an existing empty folder lists as a non-nil empty slice. Do not allocate a map, slice or set to make this decision.

On an unchanged pass, under the mutex:
- do NOT call `collect` and do NOT call `mergeSnapshot`;
- do NOT touch `e.snapshot`, `e.fingerprints`, `e.fileReadSeq` or `e.revision`;
- set `b.pages = e.snapshot` (which is `nil` when the key's snapshot is empty/nil);
- advance `e.dirtyResolvedSeq = startSeq` (the mark that triggered the pass is resolved by a successful listing even when it publishes nothing);
- return.

The pass still performs exactly one folder listing and allocates nothing else per-name.

### 3. The changed path allocates in proportion to K

When the pass is not unchanged, read exactly the entries whose fingerprint differs or that are new, plus the forced names from requirement 5 — and no others. Concretely:
- Build a K-sized result (a `reads` map holding only the re-read entries, where K is the number of names actually re-read). Do not allocate a folder-sized map, and do not build a separate per-name order slice: iterate the `[]FileEntry` the lister returned directly.
- For each re-read, take the read seq under the mutex, call `p.reader.ReadPage` under the detached bounded `readCtx`, call `recordRead(reason)`, and keep the fingerprint `ReadPage` returned (including a zero fingerprint when the stat failed). On a read error, warn exactly as `collect` does today (bounded by the existing `maxUnreadablePageWarnings` cap) and remove any existing page for that name. Because only changed, new and forced entries are re-read, an unchanged broken file is neither re-read nor re-warned.
- Merge the listing and the reads into the entry's current snapshot in ONE O(n) pass (the existing `mergeSnapshot` shape), but update `e.fingerprints` **in place**: set the entry for each re-read name, keep the recorded fingerprint for each unchanged listed name, and `delete` the fingerprint for each name the listing no longer holds (subject to the existing `fileReadSeq` guard — a name whose later-started read won is not dropped). Do not build a second full fingerprint map.
- Build a new pages slice only when the page set or its order moved: keep the existing `samePages` guard so a merge that reproduces the same page pointers in the same order keeps the current `e.snapshot` and advances no revision.
- After a successful merge, set `e.dirtyResolvedSeq = startSeq`; set `e.reloadResolvedSeq = startSeq` only when `full`.

Drop `snapshotReads.order` — the listing's `[]FileEntry` replaces it. `snapshotReads` keeps `listingSeq`, the K-sized `reads` map and `missing`. Change `mergeSnapshot` to mutate `e.fingerprints` in place and return only the new pages slice; it no longer returns a fingerprint map. Remove `recordedFingerprints` — its whole-map copy is gone. Keep `collect` as the helper that reads the changed, new and forced entries: give it the listing `[]FileEntry` and the set of names to read, and have it list nothing and copy no fingerprints. Keep the function names meaningful; the public `PageIndex` interface is unchanged.

**Keep the recorded set in sync after a single-file read (`pkg/pageindex/pageindex_file.go`).** In `applyFileReadLocked`, when the read removed a page because the file is gone — `page == nil && fingerprint == (FileFingerprint{})` — `delete(e.fingerprints, filename)` instead of storing the zero fingerprint, so a removed file leaves no fingerprint entry that a later merge would never visit. Keep today's behaviour for an excluded-but-present file (a non-zero fingerprint is stored) and keep `e.fileReadSeq` tombstones exactly as they are. Without this, a name removed by `RefreshFile` or by a write-mark read would leave an entry the stat-diff's merge never visits, and the unchanged predicate in requirement 2 (which compares `len(entries)` with `len(e.fingerprints)`) would never hold again for that key.

### 4. A cold key and a forced reload never take the fast path

A key with no snapshot does the cold build (`buildCold`, every file read, reason `build`). A `buildReload` re-reads every file, ignoring fingerprints, reason `reload`. Neither may return early on the unchanged predicate. `ForceReload` is the only path that ignores fingerprints.

### 5. A pending write mark defeats the fast path

`Refresh` runs a `buildStatDiff`; `executeListing` still consumes, for a non-full pass, the `e.writeMarked` entries whose seq is `<= startSeq` into a `forced` set (and deletes them from `writeMarked`), exactly as today. Any non-empty `forced` set makes the pass a changed pass, so a file a board write marked stale is always re-read even when no fingerprint changed. This is AC7: the unchanged fast path is never taken on a pass that consumed a write mark. A mark recorded while the pass is in flight stays pending for the next reader.

### 6. Everything observable stays exactly as it is

Do not change: the read set of a stat-diff (exactly the entries whose fingerprint differs or that are new, plus forced names); the snapshot content, its filename-ascending order and its exclusions; the `samePages`/`splicePage` ordering key (`page.FileMetadata.Name + ".md"`); the write-mark resolution rules; the frame-ordering contract; the read-your-writes guarantee; the `RescanInterval` (50 s) and `rescanPollInterval` (1 s); the `rebuildTimeout` bound on each storage call. The `PageIndex`, `PageReader` and `DirectoryLister` interfaces are unchanged, so no counterfeiter mock is regenerated.

### 7. Add the test-only identity accessors

Add to `pkg/pageindex/export_test.go` (package `pageindex`) two accessors that expose the identity of the key's recorded fingerprint set and of its published snapshot slice, so an external test can assert a pass did not replace either. Use `fmt.Sprintf("%p", ...)` on the map and the slice (both support `%p`; a nil slice prints `0x0`):

```go
// FingerprintSetIdentity returns an identity token for the key's recorded
// fingerprint set, so a test can assert a pass did not replace it. An unknown
// key reports "".
func FingerprintSetIdentity(index PageIndex, key Key) string

// SnapshotIdentity returns an identity token for the key's published snapshot
// slice, so a test can assert a pass did not replace it. An unknown key, or a
// key with no published snapshot, reports "".
func SnapshotIdentity(index PageIndex, key Key) string
```

Type-assert the `PageIndex` to `*pageIndex` and read the entry under the mutex.

### 8. Add the allocation and identity tests

Add `pkg/pageindex/pageindex_allocation_test.go` in package `pageindex_test` (it may reuse the `recordingReader`, `recordingLister`, `realIndex`, `newRealIndex` and `settle` helpers already defined in `pageindex_statdiff_test.go`, which is in the same package).

It must contain `func TestUnchangedRescanAllocation(t *testing.T)` — a plain Go test (NOT a Ginkgo spec), because the spec's verification runs it with `-run TestUnchangedRescanAllocation`, which does not run `TestPageIndex` and therefore does not register Ginkgo's fail handler. Use `t.Fatalf`/`t.Errorf` and `t.Logf`, not Gomega `Expect`.

The test:
1. Builds a real temp vault dir (via `t.TempDir()`) with a pages folder holding **5,000** `.md` files, each with valid frontmatter (e.g. `---\ntitle: Page%d\n---\n# Page%d\n`), and warms the key with `ListPages` through `newRealIndex()`.
2. Measures the `runtime.MemStats.TotalAlloc` delta around one direct `ri.lister.ListFiles(ctx, vaultDir, folder)` call and around one `ri.index.Refresh(ctx, key)` over the unchanged folder. Read `TotalAlloc` via `runtime.ReadMemStats` (call `runtime.GC()` first). Print both byte counts and their ratio with `t.Logf`.
   - Assert the ratio `passBytes / listingBytes <= 1.35`.
   - Assert the absolute `passBytes <= 5_500_000` (5.5 MB).
3. Captures `pageindex.FingerprintSetIdentity(ri.index, key)` and `pageindex.SnapshotIdentity(ri.index, key)` immediately before and after the measured `Refresh`; prints the four values; asserts both before/after pairs are equal (AC3).
4. Resets the recording reader, rewrites **10** of the 5,000 files (call `settle()` first, and change the byte count so the fingerprint moves), runs `Refresh`, prints the pass byte count, its ratio to the listing, and the recording reader's names; asserts the names are exactly the 10 changed files and the ratio is `<= 1.5` (AC4).
5. Empties the folder (`os.RemoveAll` the pages folder), runs `Refresh` once (a change: the snapshot becomes nil/empty), then captures the identities and runs `Refresh` again; asserts the second pass is unchanged — the snapshot identity and the fingerprint-set identity are unchanged and the recording reader performed no read (the empty listing equals the empty recorded set).
6. Skips the whole test under the race detector (`if raceDetectorEnabled { t.Skip("allocation measurement is only meaningful without -race") }`), because `make precommit` runs `go test -race ./...` where the race runtime inflates allocation and the absolute bound is meaningless. The spec's dedicated non-race command exercises it.

Also add a plain test for AC7, `func TestWriteMarkSkipsUnchangedFastPath(t *testing.T)` in the same file: create a real temp dir with two files, warm the key, call `ri.index.MarkFileDirty(key, "Alpha")`, run `Refresh` with no fingerprint change anywhere, and assert `ri.reader.Names()` equals `[]string{"Alpha.md"}` exactly. Write the fixture files with `os.WriteFile` + `t.Fatalf` so the test does not depend on Gomega.

### 9. Guard the allocation test against the race detector

Add two tiny build-tagged files in package `pageindex_test` so the plain test can detect `-race` at compile time:
- `pkg/pageindex/race_enabled_test.go` with `//go:build race` defining `const raceDetectorEnabled = true`.
- `pkg/pageindex/norace_enabled_test.go` with `//go:build !race` defining `const raceDetectorEnabled = false`.

### 10. Coverage

`pkg/pageindex` must keep `>= 80 %` statement coverage. The new fast path, the changed path and the in-place fingerprint update are all exercised by the new test and the existing frozen specs.

### 11. CHANGELOG

Under `## Unreleased` in `CHANGELOG.md`, append a `fix:` bullet describing that the page-index rescan now allocates only what changed — an unchanged stat-diff keeps the published snapshot and the recorded fingerprints in place and allocates nothing beyond the folder listing, while a pass that changes K files allocates in proportion to K. `## Unreleased` goes directly above the highest `## vX.Y.Z` section (`## v0.87.3`); do not move or edit the `# Changelog` preamble. Follow `changelog-guide.md`. Do not add a `feat:` entry — this is a performance fix.

### 12. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk Desired Behaviors 1–5 and AC2, AC3, AC4 and AC7 against the tests you wrote and name the test that establishes each. Confirm that `pkg/pageindex/pageindex_statdiff_test.go`, `pkg/pageindex/equivalence_test.go` and `pkg/pageindex/pageindex_revision_test.go` are byte-for-byte unchanged.

</requirements>

<constraints>
- The 50 s `RescanInterval` and the 1 s `rescanPollInterval` are unchanged; no new timer is added anywhere.
- One immutable `[]*domain.Page` per key, never mutated after publication; concurrent readers share the same page pointers. This change alters how the slice is *built*, never whether it is mutated.
- The read set of a stat-diff is exactly the entries whose fingerprint differs or that are new (plus forced write-marked names), and "differs" means inequality, not "newer".
- Snapshot order equals the folder listing's order (filename ascending), and resolving a page by name keeps the listing's sorted first-match behaviour. Neither may change.
- A mark recorded before a pass started is resolved by that pass; a mark recorded while a pass is in flight stays pending for the next reader.
- An incomplete listing is never published: a listing or a single-file read error leaves the previous snapshot serving, logs the error with the key, and leaves the mark pending.
- `ForceReload` (behind `POST /api/cache/reload`) and the cold build keep re-reading every file, ignoring fingerprints.
- The read surface is unchanged: the same `PageReader` and `DirectoryLister` seams, the same symlink-out-of-vault exclusion, and the same per-call `rebuildTimeout` bound on each storage call.
- Do NOT change `RescanInterval`. Do NOT add a knob, an opt-out flag, a selectable fingerprint or an allocation metric. Do NOT expose pprof or any new profiler route.
- Do NOT remove or replace the per-entry stat in the directory listing, and do NOT reach for a platform-specific bulk-attribute syscall.
- Do NOT change what a stat-diff reads, the snapshot's content or order, the write-mark rules, the frame contract, the response bodies, or vault-cli (no version bump).
- Do NOT change the cold build or `ForceReload`.
- Do NOT modify `pkg/pageindex/pageindex_statdiff_test.go`, `pkg/pageindex/equivalence_test.go` or `pkg/pageindex/pageindex_revision_test.go`.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no ignored error returns, `Create*` factories without business logic, Ginkgo/Gomega tests, counterfeiter mocks for the existing seams, `libtime` injection for the clock, new code >= 80 % covered.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/mutations/... ./pkg/factory/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/pageindex/... -run TestUnchangedRescanAllocation -count=1 -v
```
Must pass and print the listing bytes, the pass bytes, their ratio, the four identity values and the recording reader's names.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/pageindex.cover ./pkg/pageindex/ && go tool cover -func=/tmp/pageindex.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/pageindex/
```
Must exit 0 (no raw goroutines in non-test code).

```
grep -q 'FingerprintSetIdentity' pkg/pageindex/export_test.go && grep -q 'SnapshotIdentity' pkg/pageindex/export_test.go && test -f pkg/pageindex/pageindex_allocation_test.go && test -f pkg/pageindex/race_enabled_test.go && test -f pkg/pageindex/norace_enabled_test.go
```
Must exit 0 (the new accessors and test files exist).

```
grep -A10 '^## Unreleased' CHANGELOG.md | grep -q '^- fix:'
```
Must exit 0 (the changelog bullet exists).
</verification>
