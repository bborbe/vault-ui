---
status: completed
spec: [030-page-index-snapshot]
summary: Added pkg/pageindex/store.go (Store seam, embedded-Bolt implementation with bounded open, writer-identity check and discard semantics), pkg/pageindex/store_codec.go (type-preserving gob page+fingerprint codec through RawMap), the generated Store mock, and store_test.go covering the codec round-trip, AC4 discard triggers, AC5 transaction half, AC6 fixed location and AC7 reporting.
execution_id: vault-ui-exec-147-spec-030-store-seam-and-codec
dark-factory-version: v0.196.0
created: "2026-10-08T09:37:33Z"
queued: "2026-10-08T10:22:12Z"
started: "2026-10-08T12:37:17Z"
completed: "2026-10-08T12:57:54Z"
branch: dark-factory/page-index-snapshot
---

# Persist the page index: the store seam and its codec

<summary>
- A new store primitive keeps a copy of the parsed page index on local disk, at one fixed path outside every vault.
- The store records each indexed file's parsed page together with the fingerprint taken just before it was read.
- A file that was excluded from the index (unreadable, unparsable, or a symlink out of the vault) stores its fingerprint and no page, so an unchanged excluded file is not re-read or re-warned on a later start.
- The page round-trips losslessly: the frontmatter travels through the exported escape hatch, so a page reloaded from the store is identical to the page the vault-cli reader produced, field for field and type for type.
- The store records which writer produced it — a store-format version plus the parser version the process was built against — and refuses to serve a copy written by a different one.
- Any store that is missing, empty, damaged, unreadable, or written by a different writer is discarded, never served, and never stops the process from starting.
- A discard is reported once, with the reason and the store path.
- The store is a cache only: nothing here changes what the board serves.
- The store is opened with a bounded wait, so a copy locked by another process cannot hang startup.
- The store writes one publication's entries in a single transaction.

</summary>

<objective>
Build the on-disk store primitive and its page-plus-fingerprint codec in `pkg/pageindex`, testable in isolation against a temp directory: a `Store` interface with a load-per-key and a single-transaction delta write, an embedded-Bolt implementation at a fixed user-cache path, an injectable writer identity that is recorded and checked, discard-on-any-mismatch with exactly one warning naming the reason and the path, and a bounded open. This prompt does not change the page index's behaviour; it adds the store that later prompts consume.
</objective>

<context>
Read the spec `specs/in-progress/030-page-index-snapshot.md` in full. This prompt covers the spec's Suggested Decomposition row 1 (Desired Behaviors 4, 5, 6, 7, 8 and Acceptance Criteria AC1's codec half, AC4, AC5's transaction half, AC6, AC7).

Read `CLAUDE.md` at the repo root if present and `docs/dod.md` for the project's Definition of Done.

Read the current code before writing anything:
- `pkg/pageindex/seams.go` — `FileFingerprint` (`Size int64`, `ModTime time.Time`, `StatusChangeTime time.Time`), `FileEntry`, `PageReader`, `DirectoryLister`.
- `pkg/pageindex/pageindex.go` — `Key` (`VaultPath string`, `PagesDir string`), `NewKey`, `NewPageIndex`, and the package doc comment.
- `pkg/pageindex/reader.go` — `pageReader.ReadPage` takes the fingerprint with `statFingerprint` before delegating the parse to `storage.PageStorage.ReadPage`.
- `pkg/pageindex/lister.go` — `directoryLister.ListFiles` returns entries in `os.ReadDir` order and a zero fingerprint when a stat fails.
- `pkg/pageindex/equivalence_test.go` — `buildEquivalenceVault()` (the awkward-files fixture), `indexedNames()`, and the `storage.NewPageStorage(nil).ListPages` reference listing. Reuse these helpers; do not modify this file.
- `pkg/pageindex/pageindex_statdiff_test.go` — `warnSink` and its `naming(substr)` helper, for capturing warnings.
- `pkg/pageindex/export_test.go` — the existing `NewPageIndexWithWarnf` and `FilesReadTotal` accessors.

Library facts, verified in the module cache (cite these exactly):
- `github.com/bborbe/boltkv` v1.15.3 is NOT yet a dependency. `boltkv.OpenFile(ctx context.Context, path string, fn ...ChangeOptions) (DB, error)` where `type ChangeOptions func(opts *bolt.Options)` and `bolt` is `go.etcd.io/bbolt`. `boltkv.DB` embeds `github.com/bborbe/kv`'s `DB` (`Update(ctx, func(ctx, tx kv.Tx) error) error`, `View(...)`, `Sync()`, `Close()`, `Remove()`, `Stats`, `StatsDetailed`). `boltkv.OpenFile` does NOT create the parent directory.
- `bolt.Options` (bbolt v1.5.0) has `Timeout time.Duration`; the zero value waits forever on the file lock. Set it through a `boltkv.ChangeOptions` to bound the open.
- `github.com/bborbe/kv` v1.21.13 (already present as an indirect dependency): `Tx.Bucket(ctx, name)`, `Tx.CreateBucketIfNotExists(ctx, name)`, `Tx.DeleteBucket(ctx, name)`, `Tx.ListBucketNames(ctx)`; `Bucket.Put(ctx, key, value []byte) error`, `Bucket.Get`, `Bucket.Delete`; `kv.BucketName` is `[]byte` with `kv.NewBucketName(string)`; `kv.BucketNotFoundError` is a sentinel usable with `errors.Is`; `kv.ForEach(ctx, bucket, fn func(item kv.Item) error) error`.
- `github.com/bborbe/vault-cli` v0.163.0 `pkg/domain`: `type Page struct { FrontmatterMap; FileMetadata; Content }`; `func NewPage(data map[string]any, meta FileMetadata, content Content) *Page`; `func (f FrontmatterMap) RawMap() map[string]any`; `type FileMetadata struct { Name string; FilePath string; ModifiedDate *time.Time }`; `type Content string`. `FrontmatterMap`'s only field is unexported, so `json.Marshal(page)` silently drops every frontmatter field.
- `github.com/bborbe/time` v1.27.14 `DateOrDateTime` implements `encoding.TextMarshaler`/`TextUnmarshaler`, `MarshalJSON`/`UnmarshalJSON` and `MarshalBinary`, but NOT `UnmarshalBinary` and not `GobEncode`/`GobDecode`. gob does not use `TextMarshaler`: it uses `BinaryMarshaler` on encode and `BinaryUnmarshaler` on decode, so a `DateOrDateTime` value encodes and then fails to decode (`gob: type mismatch in decoder`). Do not register or rely on `DateOrDateTime` for gob; if such a value can appear in a frontmatter map, convert it to `time.Time` before encoding. `libtime.CurrentDateTimeGetter` has `Now() DateTime`.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-security-linting.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-logging-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

bborbe conventions (binding): errors wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`; no ignored error returns; `Create*` factories contain no business logic; counterfeiter annotations on interfaces with the mock generated into `mocks/`; Ginkgo/Gomega tests in an external test package; `libtime` injection for any clock; new code at least 80 % covered.
</context>

<requirements>

### 1. The `Store` seam and its types (`pkg/pageindex/store.go`)

Add `pkg/pageindex/store.go` with the package doc comment on the file and these exported declarations:

```go
// StoreFormatVersion is the on-disk layout version. A store written under a
// different version is discarded, never converted.
const StoreFormatVersion = 1

// WriterIdentity identifies the process that wrote the store: the store-format
// version plus the vault-cli parser version this process was built against.
type WriterIdentity struct {
	StoreFormat   int
	ParserVersion string
}

// StoredEntry is one indexed file's stored record: the parsed page, or nil when
// the file was excluded, plus the pre-read fingerprint.
type StoredEntry struct {
	Filename    string
	Page        *domain.Page
	Fingerprint FileFingerprint
}

//counterfeiter:generate -o ./mocks/pageindex-store.go --fake-name Store . Store

// Store persists the parsed page index on local disk. It is a cache: a Load
// that cannot supply entries never fails the caller, it reports them absent.
type Store interface {
	// Load returns the key's stored entries. ok is false when there is nothing
	// usable to load for the key, which is the full-parse path. err is non-nil
	// only when ctx was cancelled.
	Load(ctx context.Context, key Key) (entries []StoredEntry, ok bool, err error)
	// Write applies one publication's delta for the key in a single
	// transaction: puts replaces or inserts entries keyed by Filename, deletes
	// removes them.
	Write(ctx context.Context, key Key, puts []StoredEntry, deletes []string) error
	// Path returns the store file path, for logging and warnings.
	Path() string
	// Close releases the store file.
	Close() error
}
```

Add the fixed-location constructor and identity helpers to the same file:

```go
// DefaultStorePath returns <user cache directory>/vault-ui/page-index.bolt.
func DefaultStorePath() (string, error)

// CurrentWriterIdentity returns this process's writer identity.
func CurrentWriterIdentity(ctx context.Context) WriterIdentity

// OpenDefaultStore opens the store at DefaultStorePath with the current writer
// identity. It never returns nil and never fails: an unusable location yields a
// store that serves nothing and warns once per Load.
func OpenDefaultStore(
	ctx context.Context,
	warnf func(format string, args ...any),
	vlogf func(format string, args ...any),
) Store

// NewBoltStore opens the embedded-Bolt store at path under the given writer
// identity. It never returns nil and never fails.
func NewBoltStore(
	ctx context.Context,
	path string,
	identity WriterIdentity,
	warnf func(format string, args ...any),
	vlogf func(format string, args ...any),
) Store
```

`DefaultStorePath` is `filepath.Join(cacheDir, "vault-ui", "page-index.bolt")` with `cacheDir, err := os.UserCacheDir()`. `CurrentWriterIdentity` returns `WriterIdentity{StoreFormat: StoreFormatVersion, ParserVersion: vaultCLIParserVersion()}`, where `vaultCLIParserVersion()` reads `runtime/debug.ReadBuildInfo()` and returns the `Version` of the dependency whose `Path` is `github.com/bborbe/vault-cli` (falling back to `"(unknown)"` when absent or when `ReadBuildInfo` returns false).

### 2. The page codec (`pkg/pageindex/store_codec.go`)

Add `pkg/pageindex/store_codec.go`. The codec is **gob**; do not use `encoding/json` for page records — it coerces YAML integers to `float64` and dates to strings and fails the equivalence criterion. Use `encoding/json` only for the writer identity (an int and a string, where JSON is exact).

Encode each entry into a `storeRecord` and rebuild the page with `domain.NewPage`:

```go
type storeRecord struct {
	Filename    string
	HasPage     bool
	Frontmatter map[string]any
	Metadata    domain.FileMetadata
	Content     string
	Fingerprint FileFingerprint
}
```

- Encoding: `HasPage = entry.Page != nil`; when true, `Frontmatter = entry.Page.RawMap()`, `Metadata = entry.Page.FileMetadata`, `Content = string(entry.Page.Content)`; when false, leave `Frontmatter` nil and write no page. `RawMap()` is the ONLY escape hatch — `json.Marshal` on a `domain.Page` would silently drop the unexported `FrontmatterMap` field and produce a board serving empty pages.
- Decoding: `HasPage` false yields `StoredEntry{Filename, Fingerprint}` with a nil `Page`. `HasPage` true yields `StoredEntry{Filename, domain.NewPage(r.Frontmatter, r.Metadata, domain.Content(r.Content)), r.Fingerprint}`.
- Register every concrete type that can appear as a value in a `map[string]any` in a `func init()`: `string`, `bool`, `int`, `int64`, `float64`, `time.Time`, `[]any`, `map[string]any` (via `gob.Register("")`, `gob.Register(false)`, `gob.Register(int(0))`, `gob.Register(int64(0))`, `gob.Register(float64(0))`, `gob.Register(time.Time{})`, `gob.Register([]any{})`, `gob.Register(map[string]any{})`). Do NOT register `libtime.DateOrDateTime` — gob cannot round-trip it (no `UnmarshalBinary`), so a `DateOrDateTime` value makes `Load` fail. If a `DateOrDateTime` value can appear in a frontmatter map, convert it to `time.Time` during encoding and document that conversion in the codec. If a fixture type the equivalence test produces is not registered, the test fails loudly — extend the list rather than dropping the type.
- A decode error must be returned as an error, never a panic and never a partially-filled record.

### 3. The Bolt implementation

One bucket per key, plus one reserved meta bucket. Bucket name for a key: `kv.BucketName("vault-ui-page-index\x00" + key.VaultPath + "\x00" + key.PagesDir)` (a NUL byte cannot appear in a path, so keys cannot collide). Meta bucket name: `kv.BucketName("vault-ui-page-index-meta")` (it does not carry the NUL-separated prefix, so it never collides with an entry bucket). Inside a key's bucket, the entry key is the file's base name (including `.md`) and the value is the gob-encoded `storeRecord`. Inside the meta bucket, key `[]byte("identity")` holds the JSON-encoded `WriterIdentity`.

`NewBoltStore`:
1. `os.MkdirAll(filepath.Dir(path), 0700)` — `boltkv.OpenFile` does not create the parent directory. On error, warn once with the reason `"create store directory"` and return a discarded store (see requirement 4).
2. `boltkv.OpenFile(ctx, path, func(opts *bolt.Options) { opts.Timeout = storeOpenTimeout })` with `const storeOpenTimeout = 5 * time.Second`. On error, warn once with the reason `"open store"` (a timeout on the file lock is the same discard) and return a discarded store.
3. In one `db.Update` transaction, read the meta identity and reconcile it:
   - absent (a fresh or zero-byte store) — warn once with the reason `"store file missing or empty"`, write the current identity, and leave the store live and empty;
   - present and equal to `identity` — write nothing, warn nothing;
   - present and different (either `StoreFormat` or `ParserVersion` differs) — warn once with the reason `"writer identity mismatch"`, delete every non-meta bucket, write the current identity, and leave the store live and empty.
   On an error from this transaction, warn once with the reason `"record writer identity"` and return a discarded store.
4. Emit exactly one line at V(2) through `vlogf` naming the store path, the number of entry buckets and the total number of stored entries, for example `vlogf("page index store %s: %d keys, %d entries", path, keys, entries)`. Count by iterating the buckets; this runs once at startup.
5. Return the live store. Track whether it holds any entry bucket in a `sync/atomic.Bool` (`empty`).

`Load(ctx, key)`:
- If `empty` is true, return `(nil, false, nil)` without warning.
- Otherwise open a `db.View`: `tx.Bucket(ctx, bucketName(key))`. When the error `errors.Is` `kv.BucketNotFoundError`, warn once with the reason `"no stored entries for key"` and return `(nil, false, nil)`; on any other error, warn once naming the reason and path and return `(nil, false, nil)`. When the bucket exists, `kv.ForEach` over it, decoding each value with the codec; a decode error warns once naming the reason and path and returns `(nil, false, nil)`.
- Never preallocate a slice from a count read out of the store: `append` as entries are decoded, so a crafted count cannot drive an unbounded allocation.
- Return `(entries, true, nil)` when the bucket existed and decoded.

`Write(ctx, key, puts, deletes)`:
- One `db.Update`: write the current identity into the meta bucket, `tx.CreateBucketIfNotExists(ctx, bucketName(key))`, `Put` each `puts` entry (encode with the codec) and `Delete` each `deletes` name. Return the wrapped error on failure; do not warn here — the caller owns the write-failure warning.
- On success set `empty` to false.

`Path()` returns the path; `Close()` closes the DB.

### 4. Discard semantics and warnings

A store that cannot open, or whose directory cannot be created, is replaced by a discarded store: a type whose `Load` calls `warnf("page index store discarded (%s): %s", reason, path)` once per call and returns `(nil, false, nil)`, whose `Write` returns a wrapped error (so the caller warns), whose `Path()` returns the path, and whose `Close()` is a no-op. Exactly one warning per `Load` call and none anywhere else.

The warning text must contain the reason and the store path. The live store warns exactly once per discarded trigger as described in requirement 3: `"store file missing or empty"`, `"writer identity mismatch"`, `"create store directory"`, `"open store"`, `"record writer identity"`, `"no stored entries for key"`. A successful open of a store that holds entries warns nothing; a `Load` that returns entries warns nothing.

### 5. Store tests (`pkg/pageindex/store_test.go`, package `pageindex_test`)

Use Ginkgo/Gomega, reuse `buildEquivalenceVault()` and `indexedNames()` from `equivalence_test.go`, and reuse `warnSink` from `pageindex_statdiff_test.go`. Capture the store's `warnf` and `vlogf` in the tests. Cover:

1. **Codec round-trip (AC1's codec half).** Build the awkward-files fixture with `buildEquivalenceVault()`. For every `.md` entry the production `pageindex.NewDirectoryLister()` reports (in listing order), read it with `pageindex.NewPageReader(storage.NewPageStorage(nil))` to obtain its page and pre-read fingerprint, and `Write` them as the key's `puts`. `Load` the key and assert, for each name: a page entry is `reflect.DeepEqual` to the page a fresh `storage.NewPageStorage(nil).ReadPage` returns, its `Fingerprint` equals the one the reader returned, and every excluded entry (`BadYaml.md`, `Broken.md`, `Outside.md`) loads with a nil `Page` and its fingerprint intact. Positive control: the loaded names equal the expected non-empty ordered list, so a fixture that silently excluded everything cannot pass.
2. **AC4(a) no store file.** `Load` on a store whose file never existed returns `ok=false`, and exactly one warning naming `"store file missing or empty"` and the path was captured.
3. **AC4(b) zero-byte store file.** Create an empty file at the path, open the store, `Load` a key: `ok=false`, no panic, and the open-time warning names the empty reason.
4. **AC4(c) garbage bytes.** Write random bytes to the path, open the store, `Load` a key: `ok=false`, no panic, exactly one warning naming `"open store"` and the path.
5. **AC4(d) unreadable store file.** `os.Chmod(path, 0)` after writing a valid store, open, `Load`: `ok=false` and one warning. Skip this case when `os.Geteuid() == 0` (a root process can still read a `0000` file) with a `GinkgoT().Skip`.
6. **AC4(e) store-format mismatch.** Write entries under `WriterIdentity{StoreFormat: 1, ParserVersion: "x"}`, `Close`, reopen with `WriterIdentity{StoreFormat: 2, ParserVersion: "x"}`: `Load` returns `ok=false` and exactly one warning names `"writer identity mismatch"` and the path; after the mismatch the store is usable — a `Write` then `Load` returns the new entries.
7. **AC4(f) parser-version mismatch.** Same as (e) with the same `StoreFormat` and a different `ParserVersion`.
8. **AC4(g) one key present, one absent.** Write entries for key A only. Open a store and `Load` key A: `ok=true`, entries returned, no warning. `Load` key B: `ok=false`, exactly one warning naming `"no stored entries for key"`.
9. **AC5's transaction half.** `Write` with two puts then `Load`: both entries present. `Write` with one delete then `Load`: that entry gone, the other unchanged. A `Write` with a decode-breaking value cannot corrupt the store: after a `Write` error the previous `Load` result is unchanged. A `Write` to a key whose entries were written is observable through a second, independently opened store after `Close`.
10. **AC6 fixed location.** Assert `DefaultStorePath()` equals `filepath.Join(os.UserCacheDir(), "vault-ui", "page-index.bolt")` and that its error is nil. Open a store at a temp path whose parent directory does not exist and assert the parent was created and the store file exists. Build the fixture vault, write the store at the temp path, and assert no `*.bolt` file exists under the vault directory (walk it).
11. **AC7 warnings and the V(2) line.** Assert a successful `Load` of a key with entries produces 0 warnings, and that opening a store holding entries calls `vlogf` exactly once with a line naming the path, the key count and the entry count.
12. **Bounded open (failure mode).** With one store already open on a path, opening a second store on the same path returns promptly (under `storeOpenTimeout` plus a margin) rather than hanging, and its `Load` returns `ok=false` with one warning. Keep this test under a Ginkgo `It` with a `SpecTimeout`/`Eventually` bound so it cannot hang the suite.

### 6. Regenerate the counterfeiter mock

Run `counterfeiter -generate` from the repo root (the binary is on `PATH` in the container; otherwise `/home/node/go/bin/counterfeiter -generate`) so `pkg/pageindex/mocks/pageindex-store.go` is generated for the `Store` interface. If the run rewrites unrelated mocks, keep only the new one — git is unavailable in this container (`hideGit: true`), so reverting is not possible; scope the generation so unrelated mocks are not rewritten in the first place. Do not hand-write the mock.

### 7. Declare the dependency

`pkg/pageindex/store.go` imports `github.com/bborbe/boltkv` and, for the bounded open, `go.etcd.io/bbolt`; it also imports `github.com/bborbe/kv` directly. Declare them before building:

- Run `go get github.com/bborbe/boltkv@v1.15.3` then `go mod tidy` (Go module proxy access is available in this container).
- `grep -n 'boltkv' go.mod` must show `github.com/bborbe/boltkv v1.15.3` in the direct `require` block.
- `go mod tidy` will also promote `github.com/bborbe/kv` (imported directly here) and `go.etcd.io/bbolt` (referenced for `bolt.Options`) to direct requirements and may add boltkv's own indirect requirements. Accept those changes.
- Do NOT bump `github.com/bborbe/kv` away from v1.21.13 and do NOT bump vault-cli. Do not add counterfeiter to `go.mod`. Do not run `go mod vendor`.
- Prompt 4 only verifies and ties off this requirement; it does not introduce it.

### 8. Coverage and self-check

`pkg/pageindex` must keep `>= 80 %` statement coverage. Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 4–8 and AC4, AC5 (transaction half), AC6, AC7 against the tests you wrote and name the test that establishes each. Treat `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_statdiff_test.go` and `pkg/pageindex/pageindex_revision_test.go` as read-only — do not modify them. (git is unavailable in this container, so an unchanged-check is not possible; the prohibition is the enforceable form.)

</requirements>

<constraints>
- This prompt adds the store primitive only. Do NOT change the page index's behaviour, `pkg/factory`, `main.go`, `pkg/vaultconfig`, `docs/`, or `CHANGELOG.md`; later prompts in this spec do that.
- Do NOT add configuration for the store: no path knob, no retention or size limit, no toggle, and no opt-out flag. The path is `DefaultStorePath()` and nothing else.
- No migration between store formats: a version mismatch wipes and takes the full parse; it never converts.
- No compaction, eviction or size bound. One entry per indexed file; entries for a vault no longer configured are simply never read.
- The store is a single-process cache on the local disk. No shared, remote or multi-process store. Do not add a second process-safety mechanism beyond the bounded open.
- The store is read as untrusted input: never panic on malformed content, never trust a value that cannot be decoded, discard rather than serve. Only the registered concrete types are decoded. A stored entry count must never drive an unbounded allocation.
- The open must be bounded; a lock timeout is a discard, not a failure.
- Write the store file at the mode `boltkv.OpenFile` uses (0600) and create its directory at 0700. Do not widen either.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code.
- Counterfeiter mocks are generated, never hand-written.
- Do NOT add a `## Unreleased` changelog entry in this prompt; prompt 4 adds it once.
- This prompt DOES declare the dependency: add the direct `require github.com/bborbe/boltkv v1.15.3` to `go.mod` via `go get github.com/bborbe/boltkv@v1.15.3` followed by `go mod tidy`, which also records the indirect requirements boltkv pulls (including `go.etcd.io/bbolt`) and promotes `github.com/bborbe/kv` to a direct requirement because `pkg/pageindex/store.go` imports it. Prompt 4 only verifies that the requirement is direct and pinned.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/pageindex/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/pageindex.cover ./pkg/pageindex/ && go tool cover -func=/tmp/pageindex.cover | awk '/^total:/ { f=1; sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 } END { if (!f) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
test -f pkg/pageindex/store.go && test -f pkg/pageindex/store_codec.go && test -f pkg/pageindex/store_test.go && test -f pkg/pageindex/mocks/pageindex-store.go
```
Must exit 0.

```
grep -q 'func NewBoltStore(' pkg/pageindex/store.go && grep -q 'func DefaultStorePath(' pkg/pageindex/store.go && grep -q 'func OpenDefaultStore(' pkg/pageindex/store.go
grep -q 'RawMap()' pkg/pageindex/store_codec.go
```
Must exit 0.

```
grep -q 'gob.Register' pkg/pageindex/store_codec.go && ! grep -q 'encoding/json' pkg/pageindex/store_codec.go
```
Must exit 0 (gob is the page codec).

```
grep -rn 'bolt.Options\|Timeout' pkg/pageindex/store.go
```
Must print at least one line (the bounded open).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'go func' pkg/pageindex/
```
Must exit 0 (no raw goroutines in non-test code).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
