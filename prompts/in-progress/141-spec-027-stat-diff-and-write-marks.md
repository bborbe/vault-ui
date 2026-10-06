---
status: approved
spec: [027-incremental-page-index-updates]
created: "2026-10-06T09:08:51Z"
queued: "2026-10-06T09:33:58Z"
branch: dark-factory/incremental-page-index-updates
---

# Rescan by stat-diff, resolve write marks by re-reading only what changed, and force a full reload

<summary>
- The periodic safety-net rescan no longer re-parses every file: it lists each folder, compares each file's size and timestamps with the fingerprint recorded at the last read, and re-reads only files that changed, appeared or disappeared.
- A rescan of an unchanged folder re-reads nothing and publishes no new snapshot; an unparsable file that has not changed is warned about once, not on every rescan.
- A rewrite that preserves the size and modification time (a `touch -r` or `rsync -t`) is still caught, through the file's status-change time.
- A board write that could not be tied to one exact file marks the whole folder stale, and that folder is resolved by the same cheap size-and-timestamp check instead of a full re-read.
- A board write tied to one exact file marks only that file; the next list read waits for that one file to be re-read, and every concurrent reader waits on the same work, so read-your-writes is unchanged.
- A list read during a blocked rescan or single-file read still returns the previous snapshot immediately.
- `POST /api/cache/reload` is the only path that ignores fingerprints: the next read of every folder re-reads every file.
- The full create/modify/delete/rename equivalence test against vault-cli's own folder listing lands here.
- The watcher and the mutation sites are not changed here; prompt 4 wires them.
</summary>

<objective>
Turn `pkg/pageindex`'s folder-level work into stat-diff work: `Refresh` and the rescan become fingerprint comparisons that read only changed files and publish only when something changed; folder-level write marks resolve by that stat-diff; per-file write marks re-read only the marked file and block their readers; and a forced reload re-reads every file ignoring fingerprints. Extend the AC3 equivalence fixture with the per-file and stat-diff steps.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/027-incremental-page-index-updates.md` in full. This prompt covers Desired Behaviors 3, 4 (index part) and 5, and Acceptance Criteria AC4, AC5(iii), AC6(d) and AC3's full sequence. Prompt 1 added the seams and fingerprints; prompt 2 added `RefreshFile` and the read counter; prompt 4 wires the watcher and the mutation sites.

Prerequisites from prompts 1 and 2 — read the real code first:
- `pkg/pageindex/seams.go` — `FileFingerprint`, `FileEntry`, `PageReader.ReadPage`, `DirectoryLister.ListFiles`, `NewPageReader`, `NewDirectoryLister`.
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey`, the `PageIndex` interface (now including `RefreshFile`), `pageIndex`, `entry` (with `fingerprints map[string]FileFingerprint` and `fileReadSeq map[string]uint64`), `NewPageIndex`.
- `pkg/pageindex/pageindex_build.go` — `build`, `role`, `ensure`, `selectBuildLocked`, `selectRefreshBuildLocked`, `queueFollowUpLocked`, `execute`. `Refresh` and `MarkDirty` are the surfaces whose meaning changes.
- `pkg/pageindex/pageindex_file.go` — `RefreshFile` and the `splicePage` helper. Reuse `splicePage`; do not duplicate the ordering logic.
- `pkg/pageindex/metrics.go` — `filesReadTotal`, `recordRead`, the `reason*` constants.
- `pkg/pageindex/equivalence_test.go` — the AC3 fixture and its cold-build half from prompt 1.
- `pkg/pageindex/pageindex_test.go` — the existing "dirties every known key on MarkAllDirty" case (~line 393) becomes a `ForceReload` case.
- `pkg/factory/watcher_refresh_test.go`, `pkg/factory/mutations_index_test.go`, `pkg/factory/pageindex_test.go` — factory tests over the index that must keep passing; their fakes' fingerprints must change whenever content changes.
- `docs/page-index.md` — the current index design.
- `pkg/mutations/mutations.go` — `IndexInvalidator` (`MarkDirty(keys ...pageindex.Key)`, `MarkAllDirty()`) and `markVaultDirty`.
- `pkg/mutations/maintenance.go` — `ReloadCache`, which calls `s.deps.Index.MarkAllDirty()` in both branches (lines 28–29 and 40–41, each under a "Reload marks every key dirty" comment).
- `pkg/mutations/service_test.go` — the `mocks.IndexInvalidator` harness and the `MarkAllDirtyCallCount` reload assertions at lines ~1240–1266.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`

Cross-prompt read-sequence design (owned by prompt 2, binding here): every single-file read in this prompt — stat-diff re-reads, write-mark resolution, `ForceReload` — takes `p.readSeq++; seq := p.readSeq` under the lock at read START and applies its result to the CURRENT `e.snapshot` via `splicePage` only when `seq >= e.fileReadSeq[name]`, then sets `e.fileReadSeq[name] = seq` (tombstones kept for removed names). Listings take a seq at listing start too. All new I/O (single reads and listings) runs under `context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)`; a cancelled caller never aborts shared work nor removes a page.
</context>

<requirements>

### 1. `Refresh` becomes a stat-diff

`Refresh(ctx, key Key) error` keeps its signature and its "blocks until a read started after this call is reflected in a published snapshot" guarantee, but its body becomes a fingerprint comparison instead of a full folder rebuild:

0. A cold key (no snapshot) does the cold folder build instead (reason `build`).
1. Take a listing seq, then `entries, err := p.lister.ListFiles(readCtx, key.VaultPath, key.PagesDir)` under the detached bounded `readCtx`. On error keep serving the previous snapshot and log `glog.Errorf` naming the key; return nil (a listing failure is not a caller error — the next rescan or event retries).
2. Compare each `FileEntry.Fingerprint` with the key's recorded `e.fingerprints[fe.Name]`. Re-read (via `p.reader.ReadPage`) only entries whose fingerprint differs from the recorded one, or that have no recorded fingerprint. "Differs" means **inequality**, not "newer", so a restored older timestamp counts as a change.
3. For each re-read (loop variable `fe`, not `entry`, to avoid shadowing the `entry` type): under the read-sequence rule, keep the fingerprint `ReadPage` returned (success or exclusion) and `recordRead(<reason>)`; on success splice the page into a new snapshot at its filename-ordered position; on a read error remove any existing page for that name. For a re-read that fails, log the per-file warning **only because the fingerprint changed** (see step 4).
4. Drop recorded files (fingerprint and page) whose names are absent from the listing — but only when the listing's seq is greater than that name's `fileReadSeq` (a read that started after the listing wins).
5. Publish a new snapshot **only when something changed** — a re-read that produced a different page, an add, or a removal. When nothing changed, keep the exact same `e.snapshot` slice and pointer (no new snapshot). Fingerprint recording: a re-read file keeps the fingerprint `ReadPage` returned; an unchanged file keeps its recorded fingerprint; a vanished file's fingerprint is dropped.
6. Take the read counter's reason from an unexported parameter so the same helper serves several callers: `Refresh` (public) records `reasonRescan`; the write-mark resolution (requirement 3) records `reasonWrite`; the forced reload (requirement 4) records `reasonReload`.

The rescan loop `Rescan(ctx)` keeps its clock/waiter shape (still `RescanInterval = 50 s`, still no frames) and now just calls the stat-diff `Refresh` for every known key. Real-dir stat-diff tests must not race timestamp granularity: after an edit, wait until a re-stat of the file differs from the recorded fingerprint (or sleep ≥ 20 ms before editing), and prefer size-changing edits except in the same-size test. `RescanInterval` stays ≤ 60 s.

### 2. Once-per-fingerprint warnings

An excluded file (unreadable, unparsable, symlink out of the vault) is warned about once per fingerprint change: the warning is emitted only when the stat-diff actually re-read that file because its fingerprint changed. An unchanged broken file is neither re-read nor re-warned, so two rescans of an unchanged unparsable file produce exactly one warning line in total. Keep the existing `glog` warning wording as close to vault-cli's `skipping unreadable page` as practical and name the file.

### 3. Folder-level write marks resolve by stat-diff; per-file write marks block readers

Change `MarkDirty(keys ...Key)` so it no longer triggers a full folder rebuild: it records, per named key, a folder-level write mark exactly as today — `e.requestSeq++; e.dirtySeq = e.requestSeq` (there is no bool `dirty` field). Track two resolution watermarks per key, each set under the lock to the `requestSeq` captured when the resolving work STARTED, and only when that work's listing succeeded — whether or not it published a new snapshot: `dirtyResolvedSeq` (advanced by a successful stat-diff from any caller — `Refresh`, folder-mark resolution — and by a successful forced reload) and `reloadResolvedSeq` (advanced ONLY by a successful forced reload). "Folder mark pending" means `dirtySeq > dirtyResolvedSeq`; "reload mark pending" means `reloadSeq > reloadResolvedSeq`. A stat-diff never advances `reloadResolvedSeq`. `RefreshFile` advances neither (prompt 2). Keep `snapshotSeq` only if existing code needs it; do not use it as either watermark.. The next `ListPages` for that key resolves the mark by the stat-diff of step 1 (recording `reasonWrite`) and blocks until that stat-diff has published or found nothing changed; concurrent readers share that one stat-diff. Read-your-writes is preserved: the read returns the post-write content.

Add to the `PageIndex` interface (with a doc comment) and implement:

```go
// MarkFileDirty marks one file of the key stale so the next read re-reads it
// before serving. The name is the item id; it is applied only when the
// exactness rule holds, otherwise the whole key is marked folder-level.
MarkFileDirty(key Key, name string)
```

Exactness rule, applied inside `MarkFileDirty`:
- First do the cheap checks (strip, reject, snapshot lookup) under the lock; do the `os.Stat` OUTSIDE the lock, then re-lock to record the mark.
- Strip a leading `[[` and a trailing `]]` from `name`, exactly as vault-cli's `findFileByName` does.
- Reject (→ folder-level mark) when the stripped name is empty, contains a path separator, or contains `..`.
- Require the key's current snapshot to hold a page whose `FileMetadata.Name` equals the stripped name byte-for-byte. If the key has no snapshot, this fails.
- Require `<vaultPath>/<pagesDir>/<stripped name>.md` to exist at mark time (`os.Stat`).
- When all hold: record the per-file write mark for `stripped name + ".md"` as `entry.writeMarked map[string]uint64` holding the `requestSeq` at mark time (`e.requestSeq++; e.writeMarked[n] = e.requestSeq`), and do not bump `dirtySeq`.
- Otherwise: fall back to `MarkDirty(key)`.

Mark invariants:
- A mark is resolved only by work STARTED after the mark. A mark recorded while resolution is in flight queues a follow-up resolution.
- Per-file marks are taken out of `writeMarked` under the lock when the resolving read STARTS (not when it finishes), so a re-mark during the read stays pending.
- A failed listing leaves a folder mark or a reload mark pending (the next read retries); the reader gets the previous snapshot and nil.
- A `ForceReload` satisfies older pending folder and per-file marks — the reload drops `writeMarked` entries whose seq is below the reload's start seq when it starts.
- `Refresh` on a cold key does the cold build.

The first `ListPages` after a per-file mark re-reads exactly those marked files (one `ReadPage` each, `recordRead(reasonWrite)`, applied via the read-sequence rule and `splicePage`), and **blocks** until that re-read has published; every concurrent reader waits on the same work (AC6(d)). A `ListPages` on a key whose only pending work is an event (`RefreshFile`) or a rescan (`Refresh`) does **not** block and returns the previous snapshot immediately (AC6(c)).

### 4. Forced reload replaces the every-key dirty mark

`POST /api/cache/reload` must force the next read of every key to re-read **every** file, ignoring fingerprints. Replace `MarkAllDirty()` with:

```go
// ForceReload marks every known key so the next read of each re-reads every
// file, whatever the fingerprints say. It is the only path that ignores
// fingerprints.
ForceReload()
```

`ForceReload` records, for every known key, `e.requestSeq++; e.reloadSeq = e.requestSeq` (same sequence pattern as `dirtySeq`). The next `ListPages` for a key whose reload mark is pending (`reloadSeq > reloadResolvedSeq`) lists the folder and re-reads **every** listed file (`recordRead(reasonReload)`), rebuilds the snapshot in listing order, each read under the read-sequence rule (rebuilding in listing order by merging in ONE O(n) pass under the lock, exactly like prompt 2's folder-build merge — never one `splicePage` per file), marks the reload resolved for the seq it started at, and blocks until done; concurrent readers share the work. Remove `MarkAllDirty` from `PageIndex` (decided: its only caller is `ReloadCache`).

### 5. `ListPages` resolution order (`pkg/pageindex/pageindex_build.go`)

Restructure `ensure` so that, for a key with a published snapshot, the pending work is resolved in this order, each shared across concurrent readers and each blocking the reader that triggers it:
1. no snapshot → cold folder build (unchanged behaviour);
2. reload mark pending → full re-read of every file;
3. `writeMarked` non-empty → re-read exactly the marked files;
4. folder mark pending (`dirtySeq`) → stat-diff;
5. otherwise → return the current snapshot with no I/O.

An event (`RefreshFile`) and a rescan (`Refresh`) never record a reload, per-file or folder mark, so they never block `ListPages`. You may restructure the internal `build`/`entry` state as needed, but the public `PageIndex` methods, their signatures and their observable behaviour are fixed.

### 6. Instrument the new read sites (`pkg/pageindex/metrics.go` is unchanged)

The stat-diff, the write-mark resolution and the forced reload must each increment `files_read_total` with `reasonWrite`, `reasonRescan` and `reasonReload` respectively (per read). The `build` and `event` reasons stay as prompt 2 left them. Do not add a metric the spec did not ask for.

### 7. Regenerate the `PageIndex` fake

The interface gained `MarkFileDirty`/`ForceReload` and lost `MarkAllDirty`, so regenerate `pkg/pageindex/mocks/pageindex-page-index.go` (`go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate`; do not add counterfeiter to `go.mod`; keep the copyright header). If the proxy is unreachable, hand-edit in counterfeiter's exact shape.

### 8. Update the mutation service's index seam and the reload route

- `pkg/mutations/mutations.go`: change `IndexInvalidator` to
  ```go
  type IndexInvalidator interface {
  	MarkDirty(keys ...pageindex.Key)
  	ForceReload()
  }
  ```
  and update the doc comment (a read-side invalidation seam; it deliberately exposes no page-content read method). `pageindex.PageIndex` already satisfies it after steps 3–4.
- `pkg/mutations/maintenance.go`: in both `ReloadCache` branches call `s.deps.Index.ForceReload()` in place of `MarkAllDirty()`, and update both "Reload marks every key dirty…" comments to say reload forces a full re-read of every key, ignoring fingerprints. No other change to `ReloadCache`.
- Regenerate `pkg/mutations/mocks/index_invalidator.go` and update `pkg/mutations/service_test.go`'s reload assertions (lines ~1240–1266) from `MarkAllDirtyCallCount` to `ForceReloadCallCount`. The other `expectVaultMarked`/`MarkDirty` assertions are prompt 4's concern — leave them as they are for now.

### 9. Tests

Extend `pkg/pageindex/equivalence_test.go` with the full AC3 sequence over the same real-dir fixture: run a scripted sequence of create, modify, delete and rename (as a delete plus a create) through per-file updates (`RefreshFile`) and stat-diff rescans (`Refresh`), including rewriting the in-vault symlink's target page with no event delivered and then picking it up with a stat-diff. After **every** step assert `reflect.DeepEqual(indexSnapshot, storage.NewPageStorage(nil).ListPages(ctx, dir, folder))` — same pages, same order, same exclusions, identical `FrontmatterMap`, `FileMetadata` and `Content`.

Rewrite the `pageindex_test.go` "dirties every known key on MarkAllDirty" case as a `ForceReload` case. Test fakes' fingerprints must change whenever the fake file's content changes (per-file version counter).

Add Ginkgo tests covering:
1. **AC4(a)** — with a fake clock advanced past `RescanInterval`, an unchanged folder: 0 reads, exactly 1 listing per key, and the published snapshot is the identical slice — `Expect(len(after)).To(Equal(len(before)))` and `Expect(&after[0]).To(BeIdenticalTo(&before[0]))`. Include one unparsable file left unchanged across 2 rescans and assert exactly 1 warning naming it: route the per-file warning through an unexported warn-func field on `pageIndex` (default `glog.Warningf`), replaced per index instance in tests via an `export_test.go` helper (e.g. `NewPageIndexWithWarnf(...)`), set before any build — no package-level var.
2. **AC4(b)** — one file modified, one added and one removed: exactly 2 reads (the modified and the added file), and the removed file is gone.
3. **AC4(c)** — one file rewritten with the same size and its modification time restored with `os.Chtimes` back to the old value: exactly 1 read, detected through the status-change time.
4. **AC4(d)** — `pageindex.RescanInterval <= 60*time.Second`.
5. **AC5(iii)** — `ForceReload()` followed by a read of every key: each file is read exactly once, so the read count equals the total file count. Also end-to-end in `pkg/factory/mutations_index_test.go`: after `POST /api/cache/reload`, reading every key gives a `ReadPage` total equal to the fixture's `.md` file count.
6. **AC6(d)** — with a blocking reader fake, mark a file with `MarkFileDirty` for an exactly-matching id, block its re-read, and assert a concurrent `ListPages` does **not** return before the re-read completes and then returns the post-write content.
7. **Exactness rule** — per-file mark (the next read re-reads exactly 1 file and lists 0 times) for: a plain name matching the snapshot byte-for-byte with an existing `<name>.md`, AND a `[[X]]`-wrapped id whose stripped form matches byte-for-byte. Folder-level fallback (the next read lists once) for: a case-different name, a name with a separator, a name containing `..`, a name empty after stripping (`[[]]`), and a name absent from the snapshot.
8. **Folder-level write mark** — `MarkDirty(key)` then a read: exactly 1 listing plus reads only for files whose fingerprint changed (0 when none did).
9. **Listing error during stat-diff** — `ListFiles` errors: the snapshot is unchanged, `Refresh` returns nil, and a pending folder mark stays pending (the next read lists again).
10. **Folder deleted and recreated** — remove the folder: a stat-diff publishes an empty/nil snapshot; recreate it with files: the next stat-diff refills it.
11. **Mark during in-flight resolution** — block the resolving read; record a new `MarkFileDirty` for the same file; release; the next `ListPages` re-reads it again.

`pkg/pageindex` needs ≥ 80% statement coverage (see verification).

### 10. CHANGELOG

Under `## Unreleased` in `CHANGELOG.md` append a `feat:` bullet: the page index rescan is now a size-and-timestamp stat-diff that re-reads only changed files and publishes only on change, board writes mark a folder stale (resolved by the same stat-diff) or one exact file (whose re-read blocks its readers), and `POST /api/cache/reload` forces a full re-read ignoring fingerprints. Follow `changelog-guide.md`.

### 11. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk Desired Behaviors 3, 4 (index part) and 5 and AC3, AC4, AC5(iii) and AC6(d) against the tests you wrote and name the test that establishes each.

</requirements>

<constraints>
- The index keeps the `storage.PageStorage` read contract; snapshots and their `*domain.Page` values are never mutated after publication; `go test -race` must stay clean.
- The stat-diff compares fingerprints by inequality, not "newer"; a rewritten file whose modification time was restored is detected through the status-change time.
- A stat-diff of an unchanged folder reads nothing and publishes no new snapshot; an unchanged excluded file is warned about once, not per rescan.
- Event-driven (`RefreshFile`) and rescan-driven (`Refresh`) work never blocks `ListPages` when a snapshot exists; write marks (`MarkDirty`, `MarkFileDirty`) and `ForceReload` block their readers until resolved.
- `ForceReload` is the only path that ignores fingerprints; there is no periodic forced full re-parse and no configurable rescan interval, opt-out flag or selectable fingerprint.
- The 60 s ceiling now holds for every change to a file's presence, size, modification time or status-change time.
- `pkg/mutations` reads and `pkg/cleanup` keep reading disk directly. Mutation write semantics, queue ordering and frames are unchanged; only the mark kind changes. Response bodies, status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set is unchanged.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli change and no version bump.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` for the rescan clock, ≥ 80% coverage on changed packages.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- `Refresh`'s reads count as `rescan`; prompt 4's event fallback through `Refresh` therefore also counts as `rescan` (decided; AC sums over reasons).
- The full AC3 sequence is owned by this prompt.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/pageindex/... ./pkg/mutations/... ./pkg/factory/...
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
grep -q 'MarkFileDirty' pkg/pageindex/pageindex.go && grep -q 'ForceReload' pkg/pageindex/pageindex.go && ! grep -rn 'MarkAllDirty' pkg/pageindex pkg/mutations --include='*.go' --exclude-dir=mocks
```
Must exit 0 (the new marks exist and `MarkAllDirty` is gone from the source).
</verification>
