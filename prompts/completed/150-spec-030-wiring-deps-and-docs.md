---
status: completed
spec: [030-page-index-snapshot]
summary: Wired the persisted Bolt page-index store into the process at its fixed user-cache path, added a factory wiring test, and documented the store and codec trap in docs/page-index.md and CHANGELOG.md
execution_id: vault-ui-exec-150-spec-030-wiring-deps-and-docs
dark-factory-version: v0.196.0
created: "2026-10-08T09:37:33Z"
queued: "2026-10-08T10:23:49Z"
started: "2026-10-08T13:32:10Z"
completed: "2026-10-08T13:38:38Z"
branch: dark-factory/page-index-snapshot
---

# Wire the store into the process, finalize the dependency, and document it

<summary>
- The embedded-Bolt store dependency is a declared, direct requirement at the pinned version.
- The process-wide page index is opened with the store at startup, so every vault's first read hydrates from it.
- The store lives at one fixed path under the user cache directory; no configuration selects, relocates, disables or bounds it.
- The page-index design doc gains a `Page index store` section describing what the store holds, when it is written, when it is discarded, and the frontmatter codec trap.
- The design doc no longer claims a restart starts with an empty index.
- The changelog records the persisted page index as a feature.
- The parity harness stays green: the wire contract is untouched.
- The store is never created inside a vault or the repository tree, and it adds no config surface.
- The no-store factory path used by the existing tests keeps working unchanged.
- The rescan interval, the read paths and the response bodies are unchanged.

</summary>

<objective>
Make the persisted page index real on the running service: verify the Bolt store dependency at its pinned version (prompt 1 declares it), build the store at its fixed user-cache path and hand it to the process-wide page index at startup, and document the store and the codec trap in `docs/page-index.md` and `CHANGELOG.md`. This prompt covers the spec's Suggested Decomposition row 4 (Desired Behaviors 7 and 8; Acceptance Criterion AC8). It runs after prompts 1–3.
</objective>

<context>
Read the spec `specs/in-progress/030-page-index-snapshot.md` in full, especially Desired Behavior 7, the Constraints on dependencies, `CHANGELOG.md` and `docs/page-index.md`, and AC6 and AC8.

Read `docs/dod.md` for the project's Definition of Done and `CLAUDE.md` at the repo root.

Read the current code before changing anything:
- `main.go` — `execute`, where `factory.CreatePageIndex(...)` is called and how the `pageIndex` value is passed to `run.CancelOnFirstErrorWait` (warmup, rescan, watcher, API handler).
- `pkg/factory/pageindex.go` — `CreatePageIndex`, `CreatePageIndexWarmup`, `pageIndexKeys`.
- `pkg/pageindex/pageindex.go` — `NewPageIndex` and `NewPageIndexWithStore` (added by prompt 2), `RescanInterval`.
- `pkg/pageindex/store.go` — `Store`, `StoredEntry`, `WriterIdentity`, `StoreFormatVersion`, `DefaultStorePath`, `CurrentWriterIdentity`, `NewBoltStore`, `OpenDefaultStore` (added by prompt 1).
- `pkg/pageindex/store_codec.go` — the gob codec and the `RawMap()` escape hatch (added by prompt 1).
- `go.mod` — the current direct and indirect require blocks.
- `docs/page-index.md` — in full; note the `## Staleness bounds` sentence "A process restart starts with an empty index. Cold reads wait for the startup build of their key rather than each starting their own." and the existing `## Task-list snapshot`, `## Staleness bounds`, `## Incremental updates`, `## Frame ordering`, `## Key derivation` headings.
- `CHANGELOG.md` — the file begins with `# Changelog`, then a preamble, then `## v0.87.3`. There is no `## Unreleased` section yet unless another spec's prompt added one.
- `scripts/parity/parity.sh` — read enough to confirm the harness boots the built binary against a disposable fixture vault and compares responses.

Verified library facts (cite these exactly):
- `github.com/bborbe/boltkv` v1.15.3 exists and is the pinned version; `github.com/bborbe/kv` v1.21.13 is already present as an indirect dependency and satisfies boltkv exactly; `go.etcd.io/bbolt` v1.5.0 is already in `go.sum` through boltkv and becomes a direct requirement once `bolt.Options` is referenced.
- `pageindex.OpenDefaultStore(ctx, warnf, vlogf)` resolves `DefaultStorePath()` and never fails; `DefaultStorePath()` is `filepath.Join(os.UserCacheDir(), "vault-ui", "page-index.bolt")`.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-composition.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/git-workflow.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

`.dark-factory.yaml` sets `hideGit: true`, so the container's `.git` is masked. Never rely on a `git` command in this prompt: `git diff` silently becomes `--no-index` and prints fabricated results. All verification below is non-git.
</context>

<requirements>

### 1. Verify the dependency

Prompt 1 declares the dependency; this prompt only ties it off. Do not introduce it here.

- Confirm `grep -n 'boltkv' go.mod` shows `github.com/bborbe/boltkv v1.15.3` in the direct `require` block (not the indirect one), that `github.com/bborbe/kv` is pinned at v1.21.13, and that `go.etcd.io/bbolt` is a direct requirement.
- If — and only if — the requirement is missing or still indirect because prompt 1 could not complete its `go get`/`go mod tidy` step, run `go get github.com/bborbe/boltkv@v1.15.3` then `go mod tidy` to finish it, and report that you had to repair prompt 1's output.
- Do NOT bump `github.com/bborbe/kv` away from v1.21.13 and do NOT bump vault-cli. Do not add counterfeiter to `go.mod`. Do not run `go mod vendor`.

### 2. Wire the store into the process-wide index

In `pkg/factory/pageindex.go`, keep the existing `CreatePageIndex` and `CreatePageIndexWarmup` unchanged and add two factory functions:

```go
// CreatePageIndexStore opens the process-wide page-index store at its fixed
// location under the user cache directory. It never fails: an unusable location
// yields a store that serves nothing and reports a discard.
func CreatePageIndexStore(ctx context.Context) pageindex.Store {
	return pageindex.OpenDefaultStore(ctx, glog.Warningf, func(format string, args ...any) {
		glog.V(2).Infof(format, args...)
	})
}

// CreatePageIndexWithStore returns the process-wide page index over the reader
// and lister seams, hydrated from store.
func CreatePageIndexWithStore(
	reader pageindex.PageReader,
	lister pageindex.DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	store pageindex.Store,
) pageindex.PageIndex {
	return pageindex.NewPageIndexWithStore(
		reader,
		lister,
		currentDateTimeGetter,
		libtime.NewWaiterDuration(),
		store,
	)
}
```

Both functions are one-liners with no branches — keep them that way (the `Create*` factory rule forbids business logic in a factory). Add the `github.com/golang/glog` import.

In `main.go`'s `execute`, replace the `factory.CreatePageIndex(...)` call with:

```go
	// The process-wide page index reads single page files through the
	// production reader and lister seams, shared by every vault, and hydrates
	// from the on-disk store at its fixed cache path.
	pageIndex := factory.CreatePageIndexWithStore(
		pageindex.NewPageReader(storage.NewPageStorage(nil)),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
		factory.CreatePageIndexStore(ctx),
	)
```

Do not add a shutdown hook to close the store: it lives for the process lifetime, exactly like the other process-wide singletons in `execute`.

**Existing call sites are deliberately unchanged.** `CreatePageIndex` and `NewPageIndex` keep their signatures and every current caller keeps compiling without an edit. Do not modify any of these:
- `pkg/factory/mutations_index_test.go:43`
- `pkg/factory/watcher_refresh_test.go:346`
- `pkg/factory/pane_test.go:94`
- `pkg/factory/watcher_internal_test.go:30` and `:194`
- `pkg/factory/pageindex_test.go:228`, `:276`, `:287`, `:312`
- `pkg/factory/factory_test.go:227`
- `pkg/factory/api_test.go:72`
- `pkg/pageindex/export_test.go`, `pkg/pageindex/pageindex_test.go`, `pkg/pageindex/equivalence_test.go`

The no-store path is what those tests exercise; the store-backed path is reached from `main.go` and covered by the tests prompt 4 adds below.

### 3. A wiring test

Add `pkg/factory/pageindex_store_test.go` (package `factory_test`, Ginkgo/Gomega) with:

1. **The store is opened at the fixed path.** Point the cache directory at a temp dir so the real user cache is never touched: `t.Setenv("XDG_CACHE_HOME", tempDir)` (linux) and `t.Setenv("HOME", tempDir)` (darwin) before calling `factory.CreatePageIndexStore(ctx)`. Assert its `Path()` equals `filepath.Join(tempDir, "vault-ui", "page-index.bolt")` on linux (`filepath.Join(tempDir, "Library", "Caches", "vault-ui", "page-index.bolt")` on darwin) — prefer computing the expectation with `pageindex.DefaultStorePath()` after the same `Setenv`, so the test cannot disagree with the implementation about the layout. Assert the parent directory was created.
2. **A store-backed index serves the same data as a no-store index.** Build a small vault fixture, warm `factory.CreatePageIndexWithStore(...)` over the counting seams — reuse `newCountingSeams()` from `pkg/factory/pageindex_test.go` — and assert the served pages equal the no-store index's pages.
3. **No `.bolt` file appears under a vault.** After the warm-up in (2), walk the fixture vault directory and assert no file whose name ends in `.bolt` exists there.

Use `GinkgoT().TempDir()` for the fixture and the cache dir. Do not write into the real `os.UserCacheDir()`.

### 4. Document the store in `docs/page-index.md`

- In the `## Staleness bounds` section, replace the sentence "A process restart starts with an empty index. Cold reads wait for the startup build of their key rather than each starting their own." with wording for the persisted store: a restart loads each key's stored pages and fingerprints and resolves exactly one stat-diff before that key is served, reading only the files that changed while the process was down; the stored snapshot is never served as it was written. The literal string `starts with an empty index` must no longer appear anywhere in the file.
- Add a new `## Page index store` section (its own `##` heading, so `grep -n '^## '` lists it). In the document's existing short, factual voice, cover:
  - **What it holds.** One entry per indexed file: the parsed page plus the pre-read fingerprint — size, modification time and status-change time — taken from the symlink-following stat before the read. A file excluded from the index (unreadable, unparsable, or a symlink out of the vault) stores its fingerprint and no page, so an unchanged excluded file is neither re-read nor re-warned on the next start.
  - **Where it lives.** `<user cache directory>/vault-ui/page-index.bolt`, never inside a vault and never inside the repository tree; its directory is created when absent. Name the literal `page-index.bolt` so the path is greppable. No configuration selects, relocates, disables or bounds it.
  - **When it is written.** Whenever a snapshot is published, exactly the entries that publication changed — the files it re-read and the files that vanished — are written in a single transaction, outside the index mutex, so a reader never waits on store I/O. A failed write leaves the previous content intact and serving unaffected. No timer flushes the store.
  - **When it is discarded.** A store that is missing, empty, damaged, unreadable, or written under a different store-format version or by a different vault-cli parser version is discarded and the key takes the full parse; it is never migrated. A discard logs one warning naming the reason and the path, and never changes a served response and never stops the process from starting.
  - **The codec trap.** State plainly that `domain.Page` embeds `FrontmatterMap`, whose only field is unexported, so `json.Marshal` on a `domain.Page` silently drops every frontmatter field and produces a page with no status, phase, goals, assignee, priority, dates or page_type; the store therefore encodes frontmatter through `FrontmatterMap.RawMap()` and rebuilds each page with `domain.NewPage`, and a JSON codec is not an option. Say this so the next reader does not rediscover it the hard way.
- Do not change the meaning of any other section, and do not edit `README.md` or any other doc.

### 5. Record it in `CHANGELOG.md`

- If `## Unreleased` does not exist, add it directly above the highest `## vX.Y.Z` heading (`## v0.87.3`). If it already exists (for example from spec 029's prompt), append to it — do not create a second one.
- Add one `feat:` bullet describing the persisted page index: the board keeps its parsed page index on local disk at a fixed user-cache path and a restart loads it and re-checks each file's size and timestamps, re-parsing only the files that actually changed, so the first list after a restart no longer re-parses every indexed file; the on-disk copy is a cache that is discarded on any mismatch and never changes what the board serves.
- Follow `changelog-guide.md`: `- feat: <what> [context]`, one bullet per logical change, specific. Do not copy shell comments from this prompt's `<verification>` section. Do not use the prompt filename as the entry. Do not edit or move the `# Changelog` preamble or any released section.

### 6. Do not add a configuration surface

- Do not touch `pkg/vaultconfig/` and do not add any key to `config.yaml.example` or `config.yaml` for the store. There is no store path, retention, size limit, toggle or opt-out.
- Do NOT verify this with `git diff` — `.git` is masked in this container and `git diff` would silently report fabricated results. The absence of a config surface is proven by the `grep` checks in `<verification>` instead. (The spec's AC6 `git diff --stat config.yaml.example config.yaml` line belongs on the spec's operator-side Verification rung, not in this prompt.)

### 7. Run parity

`make parity` boots the built Go backend against a disposable fixture vault and compares it with the Python backend. It must stay green. The store does not affect it: the harness uses a fresh temp fixture vault per run, so its keys never match entries left by a previous run and every key takes the full-parse path.

### 8. Self-check

Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 7 and 8 and AC6 and AC8 against the changes and name what establishes each. Confirm that `pkg/pageindex/pageindex.go`'s `RescanInterval` is still `50 * time.Second`, that `grep -rn 'RawMap()' pkg/` finds the codec, and that the wire contract (routes, bodies, status codes, query parameters, WebSocket frame content, the watched directory set) is untouched.

</requirements>

<constraints>
- No configuration for the store: not a path, not a retention or size limit, not a toggle, and no opt-out flag. The path is `DefaultStorePath()` and nothing else.
- The store is never created inside a vault directory or the repository tree.
- No change to routes, query parameters, response bodies, status codes, WebSocket frame content, or the watched directory set (the spec 023 parity contract).
- `RescanInterval` stays 50 s and no new timer is added.
- Do NOT change the `PageIndex`, `PageReader` or `DirectoryLister` interfaces, and do NOT change `CreatePageIndex` or `NewPageIndex` signatures.
- Do NOT edit prompts 1–3, `pkg/pageindex/equivalence_test.go`, `pkg/pageindex/pageindex_statdiff_test.go` or `pkg/pageindex/pageindex_revision_test.go`.
- Do NOT add a store close hook, a background flush, or a metrics family the spec did not ask for.
- `Create*` factories contain no business logic: no loops, no switch, no conditionals.
- `make parity` is a real gate and must stay green.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
grep -n 'boltkv' go.mod
```
Must print `github.com/bborbe/boltkv v1.15.3` in the direct require block.

```
grep -n 'RescanInterval = ' pkg/pageindex/pageindex.go
```
Must print `const RescanInterval = 50 * time.Second`.

```
grep -rn 'RawMap()' pkg/
```
Must print at least one line.

```
grep -n '^## ' docs/page-index.md
```
Must list `Task-list snapshot`, `Staleness bounds`, `Incremental updates`, `Frame ordering`, `Key derivation` and `Page index store`.

```
grep -n 'page-index.bolt' docs/page-index.md
```
Must print at least one line.

```
! grep -n 'starts with an empty index' docs/page-index.md
```
Must exit 0 (the stale sentence is gone).

```
grep -A10 '^## Unreleased' CHANGELOG.md
```
Must show a `feat:` bullet about the persisted page index.

```
! grep -rn 'bolt\|storePath\|cacheDir' pkg/vaultconfig/
```
Must exit 0 (no store configuration surface in the vault config package).

```
! grep -n 'bolt\|store' config.yaml.example
```
Must exit 0 (no store key in the example config).

```
test -f pkg/factory/pageindex_store_test.go
```
Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/pageindex/... ./pkg/factory/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make parity
```
Must exit 0 and its summary must report no mismatch.
</verification>
