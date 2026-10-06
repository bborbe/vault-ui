---
status: failed
spec: [027-incremental-page-index-updates]
created: "2026-10-06T09:08:51Z"
queued: "2026-10-06T09:33:58Z"
completed: "2026-10-06T09:34:00Z"
branch: dark-factory/incremental-page-index-updates
lastFailReason: 'setup workflow: working tree is not clean; cannot switch to branch "dark-factory/incremental-page-index-updates"; uncommitted changes: specs/in-progress/027-incremental-page-index-updates.md'
---

# Update the page index one file at a time with a copy-on-write splice

<summary>
- A change to one file now re-reads only that file; the folder is neither listed nor otherwise read.
- The file's current on-disk state decides the outcome, whatever the change notification's type says: present and readable inserts or replaces the page at its filename-ordered position, absent or unreadable removes it.
- The result is published as a new snapshot that shares the untouched page pointers with the previous one — a splice, not a rebuild — and the previous snapshot is never mutated.
- When two reads of the same file overlap and the earlier-started one finishes last, the published page is the later-started read's content.
- Concurrent reads of two different files of one folder never drop each other's results.
- A list read during a blocked single-file read still returns the previous snapshot immediately.
- The notification's live-update message is sent only after a snapshot containing a read that started after the notification has been published.
- A Prometheus counter counts single-file reads, labelled by why the read happened (startup build, notification, write, rescan, forced reload).
- The stat-diff rescan, the write marks and the wiring are not changed here; later prompts do that.
</summary>

<objective>
Add a per-file update entry point to `pkg/pageindex` — `RefreshFile(ctx, key, filename)` — that re-reads exactly one file, splices its page into a new copy-on-write snapshot at the filename-ordered position (or drops it when the file is absent/unreadable/unparsable), resolves concurrent reads of the same file by "most recently started wins" and concurrent reads of different files without loss, publishes the new snapshot, and returns only after a snapshot containing a read started after the call has been published. Add the `vault_ui_page_index_files_read_total{reason}` counter and instrument the build and event reads.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/027-incremental-page-index-updates.md` in full. This prompt covers Desired Behaviors 1 (index part), 6 and 7, and Acceptance Criteria AC2, AC6(a–c) and AC7. Prompt 1 (already done) added the reader/lister seams, fingerprints and the cold build; prompt 3 adds the stat-diff, write marks and forced reload; prompt 4 wires the watcher and the mutation service.

Prerequisites from prompt 1 — read the real code first:
- `pkg/pageindex/seams.go` — `FileFingerprint{Size int64; ModTime time.Time; StatusChangeTime time.Time}`, `FileEntry{Name string; Fingerprint FileFingerprint}`, `PageReader.ReadPage(ctx, vaultPath, pagesDir, filename string) (*domain.Page, FileFingerprint, error)`, `DirectoryLister.ListFiles(ctx, vaultPath, pagesDir string) ([]FileEntry, error)`, `NewPageReader()`, `NewDirectoryLister()`. The `filename` is the base name including `.md`.
- `pkg/pageindex/pageindex.go` — `Key`, `NewKey`, `PageIndex`, `pageIndex` (fields `reader PageReader`, `lister DirectoryLister`, `currentDateTimeGetter`, `waiter`), `entry` (now with `snapshot`, `hasSnapshot`, `snapshotSeq`, `requestSeq`, `dirtySeq`, `inflight`, `followUp`, `fingerprints map[string]FileFingerprint`), and `NewPageIndex(reader, lister, currentDateTimeGetter, waiter)`.
- `pkg/pageindex/pageindex_build.go` — the `build` struct, `role`, `ensure`, `selectBuildLocked`, `selectRefreshBuildLocked`, `queueFollowUpLocked`, `execute`. Read all of it; `RefreshFile` must slot in beside this machinery without breaking it.
- `docs/page-index.md` — the current index design (read it; prompt 4 updates it).
- `pkg/pageindex/mocks/` — the generated fakes, including `pageindex-page-index.go`, `pageindex-page-reader.go`, `pageindex-directory-lister.go`.

Patterns in this repo to follow:
- `pkg/websocket/metrics.go` — Prometheus metric declaration, `init()` registration and pre-initialization (`Add(0)`).
- `pkg/pageindex/pageindex_test.go` — the reader/lister fake shape and the `fastWaiter()`/`newIndex` helpers from prompt 1.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — no raw `go func()`; caller-owned channels.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 1. Per-file update (`pkg/pageindex/pageindex.go`, new `pkg/pageindex/pageindex_file.go`)

Add to the `PageIndex` interface (with a doc comment) and implement:

```go
// RefreshFile re-reads one file of the key's folder and publishes a new
// snapshot with the result spliced in. It blocks until a snapshot containing a
// read that started after this call has been published. The file's current
// on-disk state decides the outcome, not the caller's reason for calling:
// present and readable replaces or inserts the page at its filename-ordered
// position, absent or unreadable removes it. The filename is a single plain
// base name including ".md"; a filename that is empty, contains a path
// separator, contains "..", or does not end in ".md" is rejected with an error and nothing is read.
RefreshFile(ctx context.Context, key Key, filename string) error
```

#### Read-sequence design (this prompt owns it; prompts 3 and 4 reference it by name)

- Add one per-index monotonic counter `readSeq uint64` on `pageIndex`, mutated only under `p.mu`. It is separate from `requestSeq`, `dirtySeq` and `snapshotSeq` and is never compared with them.
- Add `fileReadSeq map[string]uint64` on `entry`: for each base filename, the `readSeq` of the read whose result is currently applied (page present or removed). Entries are kept for removed names as tombstones — never deleted when a page is removed.
- Every single-file read from ANY path — `RefreshFile`, the folder build's `execute`, and (prompt 3) stat-diff, write-mark resolution and `ForceReload` — takes `p.readSeq++; seq := p.readSeq` under the lock at read START. Its result is applied under the lock to the CURRENT `e.snapshot` via `splicePage` only when `seq >= e.fileReadSeq[name]`; then `e.fileReadSeq[name] = seq`. An older result is discarded.
- The folder build (`execute`) takes one seq per file before that file's `ReadPage`, collects its results, and publishes by MERGING in ONE O(n) pass under the lock (never one `splicePage` per file — that is O(n²) on a 10k-file folder). `splicePage` locates the position with `sort.Search` comparing `pages[i].FileMetadata.Name + ".md"` against `name` per probe; never build a slice of all names per call: for each listed name it applies its result only if its seq ≥ `e.fileReadSeq[name]`; a name absent from the listing is removed only if the listing started after that name's recorded `fileReadSeq` (take a seq for the listing itself at listing start). It never wholesale-replaces pages a later-started read already published. The build keeps setting `hasSnapshot`/`snapshotSeq` as today, and on success sets `b.pages = e.snapshot` (the merged snapshot), so callers waiting on the build never receive a page an applied later-started read has superseded. Extend test 14: a `ListPages` that joined the blocked build (after a `MarkDirty`) returns the new A when the build releases.
- All new I/O runs under `readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rebuildTimeout)` (the existing build timeout constant): a cancelled caller never aborts shared work nor removes a page.

#### RefreshFile behaviour (all under `p.mu` except the read itself)

1. Validate `filename`: non-empty, `filepath.Base(filename) == filename`, no `filepath.Separator`, no `..`, and it ends in `.md`. On failure return `errors.Errorf(ctx, "invalid page filename %q", filename)` without touching the entry or calling the reader.
2. Lock, `e := p.entryLocked(key)`. If the key has **no** published snapshot, unlock and return `p.Refresh(ctx, key)` (i.e. `ensure(ctx, key, true)`, which waits for a build that starts after this call — never `ensure(ctx, key, false)`, which could join a build that predates the event); the file is included by that build. Otherwise take `p.readSeq++; seq := p.readSeq`, unlock.
3. Read outside the lock under the detached bounded `readCtx`: `page, fingerprint, readErr := p.reader.ReadPage(readCtx, key.VaultPath, key.PagesDir, filename)`; `recordRead(reasonEvent)`.
4. Lock again:
   - If `seq < e.fileReadSeq[filename]`, a later-started read already applied — discard and return (no fingerprint update).
   - Otherwise `e.snapshot = splicePage(e.snapshot, filename, pageOrNil)` (nil page when `readErr != nil`), `e.fileReadSeq[filename] = seq`, and update the fingerprint: on success or a read error with a valid stat set `e.fingerprints[filename] = fingerprint`; when the stat itself failed, record the zero fingerprint (`e.fingerprints[filename] = FileFingerprint{}`) exactly as prompt 1's cold build does, so a still-listed broken symlink is not re-read every stat-diff; a name the stat-diff no longer lists is dropped by the stat-diff (prompt 3), not here.
   - Logging: a read error on a file whose previous fingerprint was absent or differs from the new one logs one `glog.Warningf` naming the file ("unreadable page excluded"); a plain deletion (stat failed) logs at `glog.V(2)` only.
   - Publish ONLY `e.snapshot` (plus `fingerprints`/`fileReadSeq`). `RefreshFile` NEVER touches `requestSeq`, `dirtySeq`, `snapshotSeq` or `hasSnapshot` — otherwise a one-file event would satisfy a pending write mark and break read-your-writes.
5. Return nil (a read error only excludes the file, as vault-cli's listing does). If the caller's `ctx` is done when the read returns, still apply the result as above, then return `errors.Wrap(ctx, ctx.Err(), "refresh file")` so the caller drops its frame.

`RefreshFile` never takes a write mark and never holds the mutex during the read, so a `ListPages` on the key is never blocked by it (AC6(c)).

Helper: the ordered insert/replace/remove over `[]*domain.Page` is the unexported function `splicePage(pages []*domain.Page, name string, page *domain.Page) []*domain.Page` (nil page = remove; always returns a new slice, never mutates `pages`) where `name` is the base filename including `.md`; the ordering key is the page's `FileMetadata.Name` plus `.md`. Add unit tests for it directly (insert into empty, insert first, insert last, replace, remove, no-op remove).

### 2. Read counter (`pkg/pageindex/metrics.go`)

Declare and register a counter (namespace `vault_ui`, subsystem `page_index`, name `files_read_total`, label `reason`) following `pkg/websocket/metrics.go`, and pre-initialize every label series so the metric appears from boot:

```go
var filesReadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "page_index",
	Name:      "files_read_total",
	Help:      "Total single-file page reads, by reason.",
}, []string{"reason"})

const (
	reasonBuild  = "build"
	reasonEvent  = "event"
	reasonWrite  = "write"
	reasonRescan = "rescan"
	reasonReload = "reload"
)

func recordRead(reason string) { filesReadTotal.WithLabelValues(reason).Inc() }
```

Register in `init()` and call `filesReadTotal.WithLabelValues(reason).Add(0)` for each of the five reasons so the series exist at boot. Instrument exactly two call sites in this prompt: every `p.reader.ReadPage` call made by `RefreshFile` (`reasonEvent`) and, until prompt 3, every read in `execute` (`reasonBuild`). Prompt 3 adds the `write`, `rescan` and `reload` call sites.

The final metric name is `vault_ui_page_index_files_read_total`.

### 3. Regenerate the `PageIndex` fake

The `PageIndex` interface gained a method, so regenerate `pkg/pageindex/mocks/pageindex-page-index.go` (`go run github.com/maxbrunsfeld/counterfeiter/v6@v6.14.0 -generate` from `pkg/pageindex`; do not add counterfeiter to `go.mod`; keep the copyright header). If the proxy is unreachable, hand-edit the fake to add `RefreshFileStub`, `RefreshFileCallCount`, `RefreshFileArgsForCall`, `RefreshFileReturns`, `RefreshFileReturnsOnCall` in counterfeiter's exact shape.

### 4. Tests (`pkg/pageindex/pageindex_test.go`, new `pkg/pageindex/pageindex_file_test.go`)

External package `pageindex_test`, Ginkgo/Gomega, over the reader/lister fakes from prompt 1. Extend the fake so `ReadPage` can block on per-filename gates and record its arguments, and so `ListFiles`/`ReadPage` counts are queryable.

Cover:
1. **AC2(a) created** — a warm key; add a file to the fake; `RefreshFile(key, "New.md")`; the next `ListPages` has the new page at its filename-ordered position.
2. **AC2(b) modified, splice not rebuild** — a warm key with several pages; change one file; `RefreshFile`; the next `ListPages` has the changed page replaced and every other element is the **same `*domain.Page` pointer** as before (`BeIdenticalTo`), proving a splice.
3. **AC2(c) deleted** — remove the file from the fake (or make `ReadPage` fail); `RefreshFile`; the page is gone from the next `ListPages`.
4. **AC2(d) modified event for a missing file removes it** — `RefreshFile` on a filename whose `ReadPage` fails removes an existing page (the file's current state decides, not the caller's reason).
5. **AC2(e) deleted event for an existing file keeps it** — `RefreshFile` on a filename whose `ReadPage` succeeds keeps/replaces the page even though the caller's reason was "deleted" (the reason is not passed to `RefreshFile`; assert the state decides).
6. **AC2(f) a previously obtained slice is unchanged** — capture the slice and pointers from `ListPages` before a splice; after the splice the old slice has the same length and the same pointers.
7. **AC6(a) newest-started wins** — with a blocking `ReadPage`, start two `RefreshFile` calls for the same file; release them so the earlier-started finishes last; the published page is the later-started read's content.
8. **AC6(b) different files never drop each other** — with a blocking `ReadPage`, start reads of two different files of one key, release in either order; both changes are visible.
9. **AC6(c) list during a blocked event read** — with `ReadPage` blocked, a `ListPages` on the key returns the previous snapshot within 100 ms (measure it).
10. **Counter (AC7 unit part)** — a warm build of N files raises the `reason="build"` series by N; after one `RefreshFile` the `reason="event"` series increased by exactly 1. Read the counter through `prometheus/testutil` (`testutil.ToFloat64(filesReadTotal.WithLabelValues("event"))`) or by scraping a `prometheus.NewRegistry` the package exposes for tests; if the metric is a package-level var registered on the default registry, expose it via an `export_test.go` `func FilesReadTotal(reason string) float64` helper and use that.
11. **Invalid filename** — `RefreshFile` with `""`, `"a/b.md"`, `"../x.md"` and `"x.txt"` returns an error and makes 0 reader calls.
12. **Cold key** — `RefreshFile` on a key with no snapshot falls back to the folder build (one listing plus the folder's reads) and the file is present.
13. **Dirty mark survives an unrelated RefreshFile** — warm key; `MarkDirty(key)` after adding a file to the fake; `RefreshFile` on a different existing file; the next `ListPages` still lists +1 page and returns the post-write content (the mark was not cleared).
14. **Event beats an older folder build** — start a folder build first and block its `ReadPage` of `A.md` (old content captured); `RefreshFile("A.md")` publishes new A; release the build; `ListPages` has new A.
15. **Newer folder build beats older event** — start `RefreshFile("A.md")` and block its read (old A); start and complete a folder build reading new A; release the event; `ListPages` has new A.
16. **Cancelled caller** — `ReadPage` blocked; cancel the `RefreshFile` ctx; release the read; `RefreshFile` returns an error wrapping `context.Canceled`, and `ListPages` reflects the read's result (the page is updated, not removed).

Counter tests assert DELTAS only (read the value before, act, compare): the counter is package-global and other specs increment it.

`pkg/pageindex` needs ≥ 80% statement coverage (see verification).

### 5. CHANGELOG

Under `## Unreleased` in `CHANGELOG.md` append a `feat:` bullet: the page index now updates one file at a time — a single-file re-read is spliced into a new copy-on-write snapshot at its filename-ordered position (or removed when the file is gone), the most recently started read of a file wins, and a new `vault_ui_page_index_files_read_total{reason}` counter counts single-file reads. Follow `changelog-guide.md`.

### 6. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Walk Desired Behaviors 1 (index part), 6 and 7 and AC2, AC6(a–c) and AC7 against the tests you wrote and name the test that establishes each.

</requirements>

<constraints>
- The index keeps the `storage.PageStorage` read contract; snapshots and their `*domain.Page` values are never mutated after publication; `go test -race` must stay clean. A splice shares untouched page pointers.
- A published snapshot is never rebuilt by `RefreshFile`; only the changed file's slot is touched.
- Order is by the file's base name **including** `.md` (the `os.ReadDir` order), never by the trimmed page name.
- `RefreshFile` never blocks `ListPages` when a snapshot exists, never marks the key dirty and never takes a write mark.
- vault-cli stays pinned at `v0.159.0` via `require`, never `replace`; no vault-cli change and no version bump.
- Response bodies, status codes, routes, query parameters and WebSocket frame content are frozen (spec 023 parity contract). The watcher's watched directory set is unchanged.
- No new HTTP route, query parameter, opt-out flag, configurable interval or configurable fingerprint.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` for the rescan clock, ≥ 80% coverage on changed packages.
- No raw `go func()` in non-test code; use `github.com/bborbe/run`.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT run `go mod vendor`; do not add counterfeiter to `go.mod`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- A `ReadPage` error excludes the file and is never returned to the caller (decided).
- AC7's unit read uses an `export_test.go` helper `FilesReadTotal(reason string) float64`; no injectable metrics interface. The factory `/metrics` scrape is prompt 4.
- The `write`/`rescan`/`reload` series are registered and pre-initialized here but first incremented in prompt 3 (decided).
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

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
grep -q 'RefreshFile' pkg/pageindex/pageindex.go && grep -q 'files_read_total' pkg/pageindex/metrics.go && grep -q 'RefreshFileStub' pkg/pageindex/mocks/pageindex-page-index.go
```
Must exit 0 (the method, the counter and the regenerated fake exist).
</verification>
