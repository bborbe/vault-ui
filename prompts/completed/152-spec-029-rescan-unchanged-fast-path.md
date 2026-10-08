---
status: completed
spec: [029-rescan-allocates-only-what-changed]
summary: 'Made the page-index stat-diff allocate only what changed: an unchanged rescan now publishes nothing and keeps the published snapshot and recorded fingerprints in place, allocating only the folder listing, while a K-file change updates fingerprints in place and allocates in proportion to K — proven by new allocation/identity tests plus a listing-path store-delete test.'
execution_id: vault-ui-exec-152-spec-029-rescan-unchanged-fast-path
dark-factory-version: v0.196.0
created: "2026-10-08T09:07:08Z"
queued: "2026-10-08T16:46:16Z"
started: "2026-10-08T17:23:57Z"
completed: "2026-10-08T17:39:26Z"
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
- `pkg/pageindex/pageindex.go` — the `entry` struct fields (`snapshot`, `hasSnapshot`, `fingerprints`, `fileReadSeq`, `requestSeq`, `dirtySeq`, `dirtyResolvedSeq`, `reloadSeq`, `reloadResolvedSeq`, `writeMarked`, `revision`, `changeLog`), `build`, `buildKind` and its constants (`buildCold`, `buildStatDiff`, `buildFileReads`, `buildReload`, `buildHydrate`), `takeReadSeq`, `Refresh`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `Rescan`.
- `pkg/pageindex/pageindex_file.go` — `applyFileReadLocked`, `splicePage` (reuse; do not duplicate ordering logic).
- `pkg/pageindex/reader.go` — `pageReader.ReadPage` takes the fingerprint before the read.
- `pkg/pageindex/lister.go` — `directoryLister.ListFiles` returns `nil` for a missing folder and returns the entries in `os.ReadDir` (filename ascending) order.
- `pkg/pageindex/seams.go` — `FileFingerprint`, `FileEntry`, `PageReader`, `DirectoryLister`.
- `pkg/pageindex/export_test.go` — the existing `NewPageIndexWithWarnf` and `FilesReadTotal` accessors.
- `pkg/pageindex/pageindex_statdiff_test.go` — the `recordingReader`, `recordingLister`, `realIndex`, `newRealIndex`, `settle`, `newStatDiffVault` helpers, and the AC4(a) test that asserts the unchanged snapshot's page pointer is identical before and after a rescan. **This file is frozen — do not modify it.**
- `pkg/pageindex/equivalence_test.go` — the AC3 equivalence fixture. **This file is frozen — do not modify it.**
- `pkg/pageindex/pageindex_revision_test.go` — the "does not advance when a stat-diff finds nothing changed" case. **This file is frozen — do not modify it.**
- `pkg/pageindex/pageindex_changedpages_test.go` — the `ChangedPagesSince` spec that guards the change-attribution calls this prompt must preserve. **This file is frozen — do not modify it.**
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

Today `executeListing` (in `pkg/pageindex/pageindex_build.go`) hands off to `collect`, which lists the folder, copies the recorded fingerprints with `recordedFingerprints`, and always allocates a `snapshotReads` whose `order` slice and `reads` map are both sized to the folder's entry count — even when nothing changed. Then `mergeSnapshot` allocates a fresh pages slice and a fresh fingerprint map on every successful listing (it returns a nil pages slice only when the folder is missing and the merged page set is empty).

Restructure so that:
- `executeListing` takes the listing seq (`p.takeReadSeq()`) and calls `p.lister.ListFiles(ctx, key.VaultPath, key.PagesDir)` exactly once, directly (not through a helper that also allocates per-name state).
- On a listing error: log with `glog.Errorf` naming the key (as today), set `b.err`, publish nothing, and leave the folder mark pending — `e.dirtyResolvedSeq` is NOT advanced. This is unchanged behaviour (see Failure Modes in the spec).
- The listing is then compared against the entry's recorded fingerprints **under the mutex**, before any per-name structure is allocated. No `recordedFingerprints` whole-map copy is made; the comparison reads `e.fingerprints` in place.
- `full` keeps today's shape: `baseline := e.hasSnapshot || e.storedBaseline`, then `full := b.kind == buildCold || b.kind == buildReload || !baseline` (a cold key and a forced reload re-read every file and never take the fast path). The stored baseline the page-index store hydrates counts as a baseline, so a hydrated key stat-diffs rather than re-reading the vault — do not narrow this to `!e.hasSnapshot`, which would turn every hydrated key back into a cold build.
- The publication branch keeps its **change-attribution** call: `e.recordChangeLocked(listingChangedNames(full, delta), !full && e.hasSnapshot)` stays where it is today — after the revision advance, and only on the branch that actually publishes a new snapshot. The unchanged fast path returns before it, which is correct: an unchanged pass publishes nothing, so there is no advance to attribute. `listingChangedNames` reads `delta.puts` and `delta.deletes`, so it must keep compiling against whatever shape `listingDelta` ends up with; do not delete the call, and do not widen `listingChangedNames`'s `full` argument.

### 2. The unchanged fast path

A pass is an unchanged pass exactly when all of the following hold:
- `full` is false (not a cold build, not a forced reload);
- **`e.hasSnapshot` is true — the key has a published snapshot.** `!full` is not enough on its own: a key hydrated from the store carries `storedBaseline` with `hasSnapshot` still false, so the fast path there would set `b.pages = e.snapshot` (nil), publish nothing and leave the key serving nothing. A hydrated key with no published snapshot is a build, not an unchanged pass;
- no file was consumed from `e.writeMarked` for this pass (no forced names — see requirement 5);
- the listing's name set equals the recorded name set (`e.fingerprints`), and
- for every listed entry the recorded fingerprint is **present and equal** — `recorded, ok := e.fingerprints[fe.Name]`, then `ok && recorded == fe.Fingerprint`.

Establish the name-set equality allocation-free: since `directoryLister.ListFiles` returns distinct names and every listed name must be present in `e.fingerprints`, the two sets are equal exactly when `len(entries) == len(e.fingerprints)` and every listed entry's recorded fingerprint is present and equal to the listed one. **The presence half is not optional.** A map read for an absent name yields the fingerprint's zero value, so a plain `e.fingerprints[fe.Name] == fe.Fingerprint` reads an unrecorded name as unchanged whenever the listing reports a zero fingerprint — which is exactly what a failed `stat` produces. A zero fingerprint on a name that was never recorded must make the pass a changed pass, not an unchanged one. Also require that the missing/empty state did not flip: `(entries == nil) == (e.snapshot == nil)`, because a missing folder lists as `nil` while an existing empty folder lists as a non-nil empty slice. Do not allocate a map, slice or set to make this decision.

On an unchanged pass, under the mutex:
- do NOT call `collect` and do NOT call `mergeSnapshot`;
- do NOT touch `e.snapshot`, `e.fingerprints`, `e.fileReadSeq` or `e.revision`;
- set `b.pages = e.snapshot` (which is `nil` when the key's snapshot is empty/nil);
- leave `e.hasSnapshot` true and `e.storedBaseline` as it is — a key that reached the fast path already has a published snapshot and is not a hydrated baseline;
- advance `e.dirtyResolvedSeq = startSeq` (the mark that triggered the pass is resolved by a successful listing even when it publishes nothing);
- return.

The pass still performs exactly one folder listing and allocates nothing else per-name.

### 3. The changed path allocates in proportion to K

When the pass is not unchanged, read exactly the entries whose fingerprint differs or that are new, plus the forced names from requirement 5 — and no others. Concretely:
- Build a K-sized result (a `reads` map holding only the re-read entries, where K is the number of names actually re-read). Do not allocate a folder-sized map, and do not build a separate per-name order slice: iterate the `[]FileEntry` the lister returned directly.
- For each re-read, take the read seq under the mutex, call `p.reader.ReadPage` under the detached bounded `readCtx`, call `recordRead(reason)`, and keep the fingerprint `ReadPage` returned (including a zero fingerprint when the stat failed). On a read error, warn exactly as `collect` does today (bounded by the existing `maxUnreadablePageWarnings` cap) and remove any existing page for that name. Because only changed, new and forced entries are re-read, an unchanged broken file is neither re-read nor re-warned.
- Merge the listing and the reads into the entry's current snapshot in ONE O(n) pass (the existing `mergeSnapshot` shape), but update `e.fingerprints` **in place**: set the entry for each re-read name, keep the recorded fingerprint for each unchanged listed name, and `delete` the fingerprint for each name the listing no longer holds (subject to the existing `fileReadSeq` guard — a name whose later-started read won is not dropped). Do not build a second full fingerprint map.
- Build a new pages slice only when the page set or its order moved: keep the existing `samePages` guard so a merge that reproduces the same page pointers in the same order keeps the current `e.snapshot` and advances no revision.
- Update `listingDelta` for the new shape: it currently consumes the fingerprint map `mergeSnapshot` returned, and `mergeSnapshot` no longer returns one. Because the merge now mutates `e.fingerprints` in place, the pre-merge names must be captured **before** that mutation — have `mergeSnapshot` also return the names it dropped, or compute the delta before the in-place update. The delta is a put for every re-read entry plus a delete for every name the entry held before the merge that the merged set no longer holds, so the persisted store stays correct. A delete set derived from `e.fingerprints` *after* the in-place update is empty by construction and leaves the store stale — and nothing else would catch it: the existing store-delete test exercises the `RefreshFile` path, not this listing path, and the allocation test in requirement 8 builds its index with `newRealIndex()`, which has no store at all. **So add the missing test.** In `pkg/pageindex/store_writethrough_test.go` (not frozen, and in neither digest list), as a sibling of `It("AC5: a deletion writes exactly one delete")` and reusing that file's `store` BeforeEach, `newWriteThroughIndex` and `newStoreVault` (with `pageFile` from `pageindex_statdiff_test.go`): build a store-backed index over a real temp vault, list the folder once, `store.reset()`, `os.Remove(pageFile(vaultDir, "Page02.md"))`, run `index.Refresh(ctx, key)` — the **listing** path, never `RefreshFile` — then assert `store.recorded()` has exactly one write, that its `deletes` is `[]string{"Page02.md"}` and its `puts` is empty, and that `store.stored(key)` no longer holds `Page02.md`. A stat-diff that finds a name the entry held and the merged set dropped must write exactly one delete.
- After a successful merge, set `e.dirtyResolvedSeq = startSeq`; set `e.reloadResolvedSeq = startSeq` only when `full`.

Drop `snapshotReads.order` — the listing's `[]FileEntry` replaces it. `snapshotReads` keeps `listingSeq`, the K-sized `reads` map and `missing`. Change `mergeSnapshot` to mutate `e.fingerprints` in place and return only the new pages slice; it no longer returns a fingerprint map. Remove `recordedFingerprints` — its whole-map copy is gone. Keep `collect` as the helper that reads the changed, new and forced entries: give it the listing `[]FileEntry` and the set of names to read, and have it list nothing and copy no fingerprints. Pass the listing seq in as well and have `collect` set it on the `snapshotReads` it returns — `snapshotMerge.unlisted` needs `listingSeq` for its `fileReadSeq` guard. Keep the function names meaningful; the public `PageIndex` interface is unchanged.

**Keep the recorded set in sync after a single-file read (`pkg/pageindex/pageindex_file.go`).** In `applyFileReadLocked`, when the read removed a page because the file is gone — `page == nil && fingerprint == (FileFingerprint{})` — `delete(e.fingerprints, filename)` instead of storing the zero fingerprint, so a removed file leaves no fingerprint entry that a later merge would never visit. Keep today's behaviour for an excluded-but-present file (a non-zero fingerprint is stored) and keep `e.fileReadSeq` tombstones exactly as they are. Keep the `e.recordChangeLocked([]string{filename}, true)` call the change-attribution work placed in this function, on the path that advanced the revision: one file was re-read, so the advance is attributable to exactly it. Deleting that call would silently break `ChangedPagesSince` for every reader that follows a write, and no test in this prompt's scope would notice. Without this, a name removed by `RefreshFile` or by a write-mark read would leave a zero-fingerprint entry that the unchanged predicate in requirement 2 (which compares `len(entries)` with `len(e.fingerprints)`) can never accept — so every key that has ever lost a file through a single-file read pays one extra changed pass before its fast path becomes available again. It does not kill the fast path for good: that next listing finds the name unlisted and the merge drops it.

### 4. A cold key and a forced reload never take the fast path

A key with no published snapshot **and no stored baseline** does the cold build (`buildCold`, every file read, reason `build`). A key hydrated from the store carries a baseline without a published snapshot, so it stat-diffs rather than cold-building — it reads the names that differ from the hydrated fingerprints and publishes its first snapshot (see requirement 1, which defines `baseline`). A `buildReload` re-reads every file, ignoring fingerprints, reason `reload`. Neither may return early on the unchanged predicate. `ForceReload` is the only path that ignores fingerprints.

### 5. A pending write mark defeats the fast path

`Refresh` runs a `buildStatDiff`; `executeListing` still consumes, for a non-full pass, the `e.writeMarked` entries whose seq is `<= startSeq` into a `forced` set (and deletes them from `writeMarked`), exactly as today. Any non-empty `forced` set makes the pass a changed pass, so a file a board write marked stale is always re-read even when no fingerprint changed. This is AC7: the unchanged fast path is never taken on a pass that consumed a write mark. A mark recorded while the pass is in flight stays pending for the next reader.

### 6. Everything observable stays exactly as it is

Do not change: the read set of a stat-diff (exactly the entries whose fingerprint differs or that are new, plus forced names); the snapshot content, its filename-ascending order and its exclusions; the `samePages`/`splicePage` ordering key (`page.FileMetadata.Name + ".md"`); the write-mark resolution rules; the frame-ordering contract; the read-your-writes guarantee; the `RescanInterval` (50 s) and `rescanPollInterval` (1 s); the `rebuildTimeout` bound on each storage call. The `PageReader` and `DirectoryLister` interfaces are unchanged, so neither is re-mocked. The `PageIndex` interface is **not** yours to change either: it already carries `ChangedPagesSince`, added by the page-index change-attribution work, and its counterfeiter mock under `pkg/pageindex/mocks/` is current — do not add, remove or re-sign a method on it, and do not regenerate the mocks.

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
1. Builds a real temp vault dir (via `t.TempDir()`) with a pages folder holding **5,000** `.md` files, each with valid frontmatter (e.g. `---\ntitle: Page%d\n---\n# Page%d\n`), and warms the key with `ListPages` through `newRealIndex()`. Write the fixture files with `os.WriteFile` + `t.Fatalf`; do **not** call `writeFixtureFile`, which asserts through Gomega and panics outside a running spec.
2. Measures the `runtime.MemStats.TotalAlloc` delta around one direct `ri.lister.ListFiles(ctx, vaultDir, folder)` call and around one `ri.index.Refresh(ctx, key)` over the unchanged folder. Read `TotalAlloc` via `runtime.ReadMemStats` (call `runtime.GC()` first). Print both byte counts and their ratio with `t.Logf`.
   - Assert the ratio `passBytes / listingBytes <= 1.35`.
   - Assert the absolute `passBytes <= 5_500_000` (5.5 MB).
3. Captures `pageindex.FingerprintSetIdentity(ri.index, key)` and `pageindex.SnapshotIdentity(ri.index, key)` immediately before and after the measured `Refresh`; prints the four values; asserts each of the four is non-empty — an unknown key reports `""`, so a key-lookup miss would make the pair-equality assertion vacuous — and that both before/after pairs are equal (AC3).
4. Resets the recording reader, rewrites **10** of the 5,000 files (call `settle()` first, and change the byte count so the fingerprint moves), runs `Refresh`, prints the pass byte count, its ratio to the listing, and the recording reader's names; asserts the names are exactly the 10 changed files and the ratio is `<= 1.5` (AC4).
5. Empties the folder (`os.RemoveAll` the pages folder), runs `Refresh` once (a change: the snapshot becomes nil/empty), then captures the identities and runs `Refresh` again; asserts the second pass is unchanged — the snapshot identity and the fingerprint-set identity are unchanged and the recording reader performed no read (the empty listing equals the empty recorded set).
6. Skips the whole test under the race detector (`if raceDetectorEnabled { t.Skip("allocation measurement is only meaningful without -race") }`), because `make precommit` runs `go test -race ./...` where the race runtime inflates allocation and the absolute bound is meaningless. The spec's dedicated non-race command exercises it.

Also add a plain test for AC7, `func TestWriteMarkSkipsUnchangedFastPath(t *testing.T)` in the same file: create a real temp dir with two files, warm the key, call `ri.index.MarkFileDirty(key, "Alpha")`, run `Refresh` with no fingerprint change anywhere, and assert `ri.reader.Names()` equals `[]string{"Alpha.md"}` exactly. Write the fixture files with `os.WriteFile` + `t.Fatalf` so the test does not depend on Gomega.

### 9. Guard the allocation test against the race detector

Add two tiny build-tagged files in package `pageindex_test` so the plain test can detect `-race` at compile time:
- `pkg/pageindex/race_enabled_test.go` with `//go:build race` defining `const raceDetectorEnabled = true`.
- `pkg/pageindex/norace_enabled_test.go` with `//go:build !race` defining `const raceDetectorEnabled = false`.

### 10. Coverage

`pkg/pageindex` must keep `>= 80 %` statement coverage. The new fast path, the changed path and the in-place fingerprint update are exercised by the existing frozen specs (`pageindex_statdiff_test.go`'s AC4(a) unchanged pass and `equivalence_test.go`) and by `TestWriteMarkSkipsUnchangedFastPath`. `TestUnchangedRescanAllocation` skips itself under `-race`, which is what the coverage command runs, so it contributes no coverage there.

### 11. CHANGELOG

Under `## Unreleased` in `CHANGELOG.md`, append a `fix:` bullet describing that the page-index rescan now allocates only what changed — an unchanged stat-diff keeps the published snapshot and the recorded fingerprints in place and allocates nothing beyond the folder listing, while a pass that changes K files allocates in proportion to K. Place it under `## Unreleased`, directly above the highest `## vX.Y.Z` section — read that version off the file, never from this line: it moves on every release, and the release bot may have cut one since this prompt was written. Create the `## Unreleased` section if it is absent; the last release renames it, so it may well be missing. Do not move or edit the `# Changelog` preamble. Follow `changelog-guide.md`. Do not add a `feat:` entry — this is a performance fix. The bullet's text must contain `allocation` or `allocates` — the verification locates it by the stem `allocat` under `## Unreleased`.

### 12. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk Desired Behaviors 1–5 and AC2, AC3, AC4 and AC7 against the tests you wrote and name the test that establishes each. Confirm that `pkg/pageindex/pageindex_statdiff_test.go`, `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_revision_test.go` and `pkg/pageindex/pageindex_changedpages_test.go` are byte-for-byte unchanged — the last one covers the change-attribution behaviour this prompt must preserve, so a failure there is the signal that `recordChangeLocked` or `listingChangedNames` was disturbed.

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
- Do NOT modify `pkg/pageindex/pageindex_statdiff_test.go`, `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_revision_test.go` or `pkg/pageindex/pageindex_changedpages_test.go`.
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
go test ./pkg/pageindex/... -run '^TestUnchangedRescanAllocation$' -count=1 -v | tee /tmp/alloc.out && grep -q '^--- PASS: TestUnchangedRescanAllocation' /tmp/alloc.out
```
Must pass and print the listing bytes, the pass bytes, their ratio, the four identity values and the recording reader's names. The trailing `grep` is not decoration: `go test -run <no-match>` prints `no tests to run` and exits **0**, so without it a renamed or absent test would satisfy this bullet while the AC2/AC3/AC4 evidence never ran.

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
grep -q 'FingerprintSetIdentity' pkg/pageindex/export_test.go && grep -q 'SnapshotIdentity' pkg/pageindex/export_test.go && test -f pkg/pageindex/pageindex_allocation_test.go && test -f pkg/pageindex/race_enabled_test.go && test -f pkg/pageindex/norace_enabled_test.go && grep -q 'func TestWriteMarkSkipsUnchangedFastPath' pkg/pageindex/pageindex_allocation_test.go
```
Must exit 0 (the new accessors and test files exist, and AC7's test is present rather than silently omitted).

```
awk '/^## /{sec=$0} /allocat/{if (sec=="## Unreleased") found=1} END{exit !found}' CHANGELOG.md
```
Must exit 0 — the allocation bullet sits under `## Unreleased`, not under a released `## vX.Y.Z`. Do not use `grep -A10 '^## Unreleased' CHANGELOG.md | grep -q '^- fix:'`: it passes on any other `fix:` bullet in the section when this one never landed, and prints nothing at all once a release has renamed the section. Do not use a bare `awk '/^## /{sec=$0} /allocat/{print sec}'` either: an `awk` that only prints exits 0 whether or not it printed anything, so that form passes whenever `## Unreleased` merely exists and cannot fail on the condition it asserts.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make parity
```
Must exit 0 (AC5 — the parity harness still passes). `make parity` is not part of `make precommit`, so it has to be run on its own — but run `make precommit` first so the container has a Linux `.venv` (the script uses `$REPO_ROOT/.venv/bin/python`), and note it needs the network for `go get github.com/bborbe/vault-cli@v0.159.0`. An `Error 127` in `build_vault_cli` is an environment failure, not a pass.

```
printf '%s\n' \
  'fe11c3119bb30ec441368b1196cc920da352194d205e6433302ae1eaeb46e7ec  pkg/pageindex/pageindex_statdiff_test.go' \
  'dd408c20b57abd99f4296b8389c9b8c4305103c6c55821617887ef3f5a529f1f  pkg/pageindex/equivalence_test.go' \
  '1f73338e058c5486a1b659bf1af09d541f339ba82726c674716276cc53590b87  pkg/pageindex/pageindex_revision_test.go' \
  'aafe458b79ce5061f4d4ef20b8d43b3a429e912774105645132412ab6024f2be  pkg/pageindex/pageindex_changedpages_test.go' \
  | sha256sum -c -
```
Must exit 0 (AC5 — the three frozen `pkg/pageindex` spec files AC5 names, plus `pageindex_changedpages_test.go`, frozen by requirement 12 as the regression guard for the change-attribution calls this prompt must preserve). Use `shasum -a 256 -c -` if `sha256sum` is absent. Do NOT use `git diff --quiet -- <those files>`: `.dark-factory.yaml` sets `hideGit: true`, so the container masks `.git`, `git diff` dies with `fatal: not a git repository`, and the check proves nothing.

```
printf '%s\n' \
  'addc23c7dbae5494267775958d1d626e1f4a3805d7e9ec0f0b54a9d6445e7fbb  pkg/watchrefresh/handler_test.go' \
  '6e4d77e98eb270adfbbd85f2aec462ea99d6eb49aa5015e8cdd218254fc3e6f9  pkg/watchrefresh/watchrefresh_suite_test.go' \
  'b2a36ac421cf80253efba2c79031aa8885b9ae5b7a4273d5770f3d6ff20388da  pkg/mutations/mutations_suite_test.go' \
  'c9483f455748b5aa293d8601b069e111180802aff2e09b16404236cab8cbc006  pkg/mutations/service_test.go' \
  '2a062f34faea3c65e960c93b946159b6c99aff5da0317e55d871a3c7c4590eeb  pkg/factory/api_test.go' \
  '8aaec4b317f191e225a0869e955ffe3748b24989e9e43d8cf29b52fd4c700206  pkg/factory/factory_suite_test.go' \
  '69f10a8ef620e6686dfdc2235ffb3debbf2c751a63b14fa29953f59069c7b911  pkg/factory/factory_test.go' \
  'cadb404311fb2c67bee975789a81b5721fd757d56d577e3dcd96d33a561ef9c7  pkg/factory/mutations_index_test.go' \
  '6bb2991b5d536555e1e56b0297a259977f78c38461864603a4cd84a254b05bb1  pkg/factory/ops_test.go' \
  '35614a39c19e6de88158fe1ebab2fb24192541573dc5b87debcac4bf4c421c9a  pkg/factory/pageindex_store_test.go' \
  'c0046a32b66e48984363a87c0e0e0e6e74f3db409610dc5027103a63538dca8f  pkg/factory/pageindex_test.go' \
  '330554aa5d7b696c866a4605721d960fb9f33b7ff68a67f0d80d0ea94fff7a3b  pkg/factory/pane_test.go' \
  'c5a84ceaa73a457fd31a666448dcc53d2afe7deb5fd52750c702d77c7665bc56  pkg/factory/sessionstate_test.go' \
  'c0701a0f1951d9026c520cf576f8c5b43a4e078ec3013afcd436d4c288ecfc33  pkg/factory/watcher_internal_test.go' \
  '582b1f74ff6b26f8752f10364ab39ea9ff52247371831b81242102e3059d6ce9  pkg/factory/watcher_refresh_test.go' \
  | sha256sum -c -
```
Must exit 0 (AC6 — the test files under `pkg/watchrefresh`, `pkg/mutations` and `pkg/factory` are byte-for-byte unchanged). A passing test run does **not** prove those files are unmodified, so AC6's byte-identical clause needs its own digest check rather than a claim that the test run covers it. This list is every `_test.go` file git tracks in those three packages; if a new test file is added there, add its digest.
</verification>
