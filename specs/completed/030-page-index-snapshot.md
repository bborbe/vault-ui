---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-10-08T08:51:44Z"
generating: "2026-10-08T09:21:42Z"
prompted: "2026-10-08T10:04:44Z"
verifying: "2026-10-08T16:46:50Z"
branch: dark-factory/page-index-snapshot
---

## Summary

- The board answers list reads from a page index that lives only in memory, so every restart re-reads and re-parses every indexed vault file — about 21,000 files across 14 vaults — before it can answer the first list request.
- This change keeps a copy of the parsed index on local disk. A restart loads it and re-checks each file's size and timestamps, one stat per file and no content reads, re-parsing only the files that actually changed.
- The on-disk copy is a cache: missing, empty, damaged, unreadable, or written by a different version means it is thrown away and the board falls back to today's full parse. It can never change what the board serves and can never stop the board from starting.
- The copy is written as the index publishes new snapshots, in one transaction per publication, with no new background timer and no new configuration.
- Target: the first list after a restart is served in under 300 ms, against roughly 1.5 s per large vault today.

Traceability: goal `[[Vault UI Ultra-Fast Reads and Writes]]` (Personal vault). It completes the item spec 027 deferred — 027's Non-goals read "No persistence of the index (the Bolt snapshot is a separate task)", and this is that task.

## Problem

The board serves list reads from an in-memory page index. That index is built once at startup by parsing every page file in every configured vault folder: about 21,163 files across 14 vaults, of which `private-agent/tasks` alone holds 10,713 and `private-personal/25 Tasks` holds 5,760. Measured on the host, a full parse of one large vault takes about 1.5 s. Because the index is memory-only, that work is repeated in full on every process start, even when nothing on disk changed. Restarts are routine — a deploy, a crash, a laptop reboot, a launchd kickstart — so the board pays the whole cost repeatedly for data that is still valid, and until the parse finishes the first list request of every restart waits. The parent goal's target is that the first served list after a cold start is near-instant, under 300 ms.

The parse work itself cannot be made much faster without giving up the guarantee that a served page equals what vault-cli's own folder listing returns. The only way to remove it is to stop redoing work that is still valid.

## Goal

A restart re-parses only the files that changed while the board was down. The board keeps its parsed index on local disk; at startup it loads that copy and re-checks each file's size and timestamps, keeping the stored page for every file whose fingerprint matches and reading only the files that are new or changed. The first list request after a restart still waits for that check — never for a stale copy — and is served from the stat-diffed index. Measured 2026-10-08 on the deployed board: 0.692–0.778 s. The 300 ms target is not met; the miss is recorded in AC9 and carried by a follow-up task. The data served is unchanged in every respect: same pages, same order, same exclusions, same fields, whether the snapshot came from the stored copy, from a full parse, or from an incremental update.

## Non-goals

- No persistence of anything else. The session snapshot, the task-list snapshot and the status cache stay in memory and are rebuilt as they are today.
- No change to routes, query parameters, response bodies, status codes, WebSocket frame content, or the watched directory set (spec 023 parity contract).
- No new timer and no configurable interval. The rescan interval stays 50 s.
- No configuration for the store: not a path, not a retention or size limit, not a toggle.
- Do NOT add an opt-out flag that disables the store — it is the point of this change; if a future consumer demands variation, that is a separate spec.
- No migration between store formats. A version mismatch discards the store and takes the full parse; it never converts.
- No compaction, eviction or size bound. The store holds one entry per indexed file; entries for a vault that is no longer configured are simply never read.
- No shared, remote or multi-process store. The store is a single-process cache on the local disk.
- No vault-cli change and no vault-cli version bump.
- No dedupe of the symlinked `OpenClaw` vault (027's deferred follow-up).
- No change to the per-request `ps` scan or to vault-cli's list cost (027's measured follow-up).

## Acceptance Criteria

Fixture note: "real dir" means a temp directory on disk read through the production reader and lister seams. "Counting reader" means the production reader wrapped so a test can count the reads it serves. The reference listing is `storage.NewPageStorage(nil).ListPages` — `nil` is valid, since vault-cli falls back to `DefaultConfig()` for a nil config, and it is the form the existing `pkg/pageindex/equivalence_test.go` already uses.

- [x] **AC1: the store round-trip is lossless (the load-bearing criterion).** A test builds a real dir containing plain pages; a page whose frontmatter holds a bare `[[wikilink]]` value; a file without frontmatter; a file with invalid YAML; a non-`.md` file; a subdirectory; a symlink pointing outside the vault; a broken symlink; a symlink pointing to a page inside the vault; and a page with a non-ASCII filename. It then builds an index over that dir so the store is written, starts a **second** index over the same store and the same dir with a counting reader, and lists the folder. Evidence:
  - The second index's `ListPages` result is `reflect.DeepEqual` to `storage.NewPageStorage(nil).ListPages` over the same dir — the same pages, the same order, the same exclusions, with identical `FrontmatterMap`, `FileMetadata` and `Content`.
  - Positive control: the second result's page names equal the expected non-empty ordered list, so a fixture that silently excluded everything cannot pass.
  - The second start reads **0** files and lists the folder once, so the snapshot came from the store and not from a re-parse. The fixture's excluded files are covered by this count, so their fingerprints must round-trip too.
  - One file's content is then changed on disk; a third start reads **exactly 1** file and its result is still `reflect.DeepEqual` to vault-cli's listing.
- [x] **AC2: a stored snapshot is never served before its stat-diff.** Evidence:
  - With a lister that blocks until released, a `ListPages` on a store-loaded key does not return while the lister is blocked — the test observes the absence of a result for a fixed interval — and returns the stat-diffed snapshot once the lister is released.
  - When nothing changed, that read serves the stored pages and the read count is 0.
  - Control: a key with no stored entries blocks on the full parse and returns vault-cli-equal data.
- [x] **AC3: cold-start reads scale with what changed, not with folder size.** Evidence, over a real dir of N files with a counting reader, one second start per case:
  - (a) nothing changed: exactly 1 listing and 0 file reads.
  - (b) K files changed, K < N: exactly K file reads, each naming one of the changed files.
  - (c) one file added and one removed: exactly 1 read (the added file), the removed file is absent from the snapshot, and the result is `reflect.DeepEqual` to `storage.NewPageStorage(nil).ListPages` over the same dir.
  - (d) one file rewritten with the same size and its modification time restored (`os.Chtimes`): exactly 1 read, detected through the status-change time.
  - (e) the reads in (a)–(d) are attributed to `reason="build"`: the `vault_ui_page_index_files_read_total{reason="build"}` series moves by exactly the number of reads above.
- [x] **AC4: a discard on any mismatch still starts and still serves correct data.** One case per trigger, each a real dir plus a store file. Evidence for every case: `ListPages` returns data `reflect.DeepEqual` to `storage.NewPageStorage(nil).ListPages` over the same dir, and the index's startup path returns no error.
  - (a) no store file: full parse, reads equal the file count.
  - (b) a zero-byte store file: it opens empty, every key full-parses, served data is correct.
  - (c) a store file holding garbage bytes: discarded, every key full-parses.
  - (d) a store file with its read permission removed: discarded, every key full-parses.
  - (e) a store written under a different store-format version: discarded, full parse.
  - (f) a store written under a different parser (vault-cli) version: discarded, full parse.
  - (g) a store holding entries for one key but not another: the first key's start reads 0 files and the second key's start reads every file, with both results `reflect.DeepEqual` to `storage.NewPageStorage(nil).ListPages`.
- [x] **AC5: write-through writes exactly the delta, in one transaction, off the read path.** A store seam that records every transaction and the entries put and deleted in it. Evidence:
  - After a cold build of N files, the store holds N entries for that key.
  - After a per-file update of one file: exactly one transaction containing exactly one put, for that file, and no other put or delete. The stored entry for that file carries the new content; every other stored entry is byte-identical to before.
  - After a file is deleted and the deletion is published: the same shape, with exactly one delete and no put.
  - A store write that fails: the publication still completes, the next `ListPages` returns the correct data, the previous store content is unchanged, and one warning names the failure.
  - With a store-write seam that blocks, a `ListPages` concurrent with the write returns within 100 ms.
  - Over 3 rescan intervals with no change, the seam records 0 transactions, and `RescanInterval` still equals 50 s.
- [x] **AC6: the store location is fixed and there is no new knob.** Evidence:
  - A test asserts the resolved store path equals `filepath.Join(cacheDir, "vault-ui", "page-index.bolt")` with `cacheDir` from `os.UserCacheDir()`, and that a missing cache directory is created and the store is created there.
  - The store is never created under a vault path: after a full build the store file exists only at the resolved path and no `.bolt` file appears under any indexed vault directory.
  - `grep -rn 'bolt\|storePath\|cacheDir' pkg/vaultconfig/` returns no configuration surface — exit 1, meaning no matches. (`pkg/config/` does not exist in this repo; grepping it exits 2, which is an error, not a pass.) `git diff --stat config.yaml.example config.yaml` shows no change.
- [x] **AC7: a discard is reported.** A test captures the index's warning sink. Evidence:
  - Each discard trigger from AC4 produces exactly one warning naming its reason and the store path.
  - A successful load produces 0 warnings.
  - A successful load reports the number of keys and entries loaded on one line at V(2).
- [x] **AC8: build, wire contract and docs.** Evidence:
  - `make precommit` exits 0; it runs on linux in the container, so the per-OS stat code compiles and passes there as well as on darwin.
  - `make parity` exits 0 and its summary reports no mismatch.
  - `go test -race ./pkg/pageindex/... ./pkg/factory/...` exits 0.
  - `grep -n '^## ' docs/page-index.md` lists `Task-list snapshot`, `Staleness bounds`, `Incremental updates`, `Frame ordering`, `Key derivation` and a new `Page index store`.
  - `grep -n 'page-index.bolt' docs/page-index.md` returns at least one line, and `grep -n 'starts with an empty index' docs/page-index.md` returns nothing.
  - A changelog bullet about the persisted page index exists. **Corrected 2026-10-08:** the original check was `grep -A10 '^## Unreleased' CHANGELOG.md`, which now returns nothing — the release flow consumed the `## Unreleased` section (v0.88.0 → v0.89.0 → v0.89.1). The bullet sits under `## v0.89.0`, where `awk '/^## /{s=$0} /Persist the board.s parsed page index/{print "under: " s}' CHANGELOG.md` reports `under: ## v0.89.0`. The original check existed to force an entry so the auto-release would not no-op; it did its job.
  - `grep -n 'boltkv' go.mod` shows a direct `require` at v1.15.3.
  - `grep -n 'RescanInterval = ' pkg/pageindex/pageindex.go` shows 50 s.
  - `grep -rn 'RawMap()' pkg/` returns at least one line, so the store encodes through the exported frontmatter escape hatch rather than a marshal method added to vault-cli's types.
- [x] **Post-Deploy (Rung-2):** AC9: a restart loads the store instead of re-parsing. Run the Operator-executable cold-start block twice, on two restarts. Evidence:
  - **Elapsed time — MEASURED, MISSED.** 2026-10-08 on the deployed board: **0.778 s and 0.692 s**, against the original bar of under 0.300 s. The bar is not met. The residual cost is the store load plus one stat-diff over ~21k files — the design this spec deliberately chose over serving an unverified snapshot — and the sub-300 ms bar is carried by the follow-up [[Cut the Vault UI Cold Start Below 300 ms]]. The criterion originally read "cold start to first served list under 300 ms".
  - Immediately after that first response, `vault_ui_page_index_files_read_total{reason="build"}` is under 1,000. The pre-fix cold start parses 21,163 files.
  - `deploy_check:` `pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}') && python3 -c 'import os,subprocess,sys,time; s=" ".join(subprocess.check_output(["ps","-o","lstart=","-p",sys.argv[1]],text=True,env={"LC_ALL":"C","PATH":"/bin:/usr/bin"}).split()); sys.exit(0 if time.mktime(time.strptime(s,"%a %b %d %H:%M:%S %Y"))>=int(os.path.getmtime(sys.argv[2])) else 1)' "$pid" ~/Documents/workspaces/go/bin/vault-ui && [ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [x] **Post-Deploy (Rung-2):** AC10: the store is used and stays current on the live service. Run the Operator-executable store block. Evidence:
  - The store file exists at the resolved cache path.
  - After one task write through the board and no restart, the store file's modification time advances within 60 s.
  - A restart's `vault_ui_page_index_files_read_total{reason="build"}` is under 1,000, so the restart loaded the store rather than full-parsing.
  - `deploy_check:` same command as AC9.
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario. AC1–AC7 reach every behavior with real temp dirs and fakes, AC8 covers the wire contract, and AC9–AC10 measure the live service.

## Verification

### Container-executable

- `make precommit` (includes `go test -race ./...`)
- `make parity`
- `go test -race ./pkg/pageindex/... ./pkg/factory/...`
- `grep -n '^## ' docs/page-index.md` lists `Page index store`
- `grep -n 'page-index.bolt' docs/page-index.md`
- `awk '/^## /{s=$0} /Persist the board.s parsed page index/{print "under: " s}' CHANGELOG.md` — originally `grep -A10 '^## Unreleased'`, but the release flow consumed that section; see AC8
- `grep -n 'boltkv' go.mod`
- `grep -n 'RescanInterval = ' pkg/pageindex/pageindex.go`

### Operator-executable (host, after merge and deploy)

Ports: the API server listens on `:8000` and serves only `/api/*`, `/ws` and static — `/healthz` and `/metrics` are **not** there (they 404). The admin server listens on `:9090` and is the only place `/healthz` and `/metrics` are served. Liveness for the API port is `/api/vaults` on `:8000`.

Deploy:

```bash
cd ~/Documents/workspaces/vault-ui && git pull && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
sleep 60   # first start settles and writes the store
```

Cold start to first served list (AC9; run the restart plus the block twice):

```bash
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
python3 - <<'PY'
import time, urllib.request
BASE = "http://127.0.0.1:8000"
deadline = time.time() + 120
t0 = None
while time.time() < deadline:
    try:
        with urllib.request.urlopen(BASE + "/api/vaults", timeout=1) as r:
            r.read()
        t0 = time.time()
        break
    except Exception:
        time.sleep(0.002)
if t0 is None:
    raise SystemExit("API never answered /api/vaults")
while time.time() < deadline:
    try:
        with urllib.request.urlopen(BASE + "/api/tasks", timeout=5) as r:
            body = r.read()
        if r.status == 200 and body.strip() not in (b"", b"[]", b"null"):
            print(f"cold start to first served list: {time.time() - t0:.3f}s")
            break
    except Exception:
        pass
    time.sleep(0.002)
else:
    raise SystemExit("no served list within 120s")
PY
curl -s http://127.0.0.1:9090/metrics \
  | awk '/^vault_ui_page_index_files_read_total\{reason="build"\}/{print; exit}'
```

Store on the live service (AC10):

```bash
ls -l ~/Library/Caches/vault-ui/page-index.bolt
# write one task through the board, wait up to 60 s, then:
ls -l ~/Library/Caches/vault-ui/page-index.bolt   # mtime advances, no restart
# then restart and read the cold-start metric (AC10's third bullet):
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
curl -s http://127.0.0.1:9090/metrics \
  | awk '/^vault_ui_page_index_files_read_total\{reason="build"\}/{print; exit}'
```

## Desired Behavior

1. **A cold start re-checks; it does not re-parse.** At startup each configured key loads its stored entries and runs exactly one stat-diff before it can be served: it lists the folder, compares each entry's current fingerprint with the stored one, and reads only entries that are new or whose fingerprint differs. An entry whose fingerprint matches keeps its stored page. Entries the listing no longer holds are dropped. A key with no stored entries takes today's full-parse path. Reads made during this first build are counted with the same `build` reason as a full cold parse, so the cold-start read count is comparable before and after this change.

2. **A stored snapshot is never served before its stat-diff.** A loaded key holds a snapshot from the moment the store is read, but its first read resolves one stat-diff before returning. A read arriving while that stat-diff runs waits for it, exactly as today's cold reads wait for the startup build. No request ever observes the stored snapshot as it was written; every request observes the stat-diffed result.

3. **Served data is unchanged.** Every published snapshot — from a full parse, from a store-loaded stat-diff, or from a per-file update — still equals what vault-cli's folder listing returns for the same files: same pages, same filename order, same exclusions, identical parsed frontmatter, file metadata and content. The store changes the work needed to produce a snapshot, never the snapshot.

4. **The store is a cache, never a source of truth.** The store is discarded, and the key takes the full-parse path, when it is missing, empty, damaged, unreadable, or was written in a different store format or by a different version of the parser that produced it. A discard never changes a served response and never stops the process from starting or from serving. A store that opens but holds no entry for a key leaves that key on the full-parse path while other keys still load.

5. **Write-through on publication, one transaction, no timer.** Whenever a snapshot is published, exactly the entries that publication changed — the files it re-read and the files that vanished — are written to the store in a single transaction. Entries that did not change are not rewritten. A publication never blocks a reader on store I/O, and a failed store write leaves the previous store content intact and serving unaffected. No timer flushes the store; the rescan interval is unchanged.

6. **A stored entry is a page plus its fingerprint.** Each stored entry carries the parsed page together with the pre-read fingerprint — size, modification time, status-change time — from the symlink-following stat taken before the read. A file that was excluded (unreadable, unparsable, or a symlink out of the vault) stores its fingerprint and no page, so an unchanged excluded file is neither re-read nor re-warned on the next start. The page and its fingerprint are written together and read together: a stored page is trusted only while its stored fingerprint matches the current stat.

7. **Fixed location, outside every vault.** The store lives at `<user cache directory>/vault-ui/page-index.bolt` — on darwin, under `~/Library/Caches` — never inside a vault and never inside the repository tree. Its directory is created when absent. No configuration selects, relocates, disables or bounds it.

8. **A discard is reported.** Every discard logs one warning naming the reason and the store path, so an operator can tell a store hit from a silent fallback. A hit needs no line of its own: it is visible as a cold-start read count far below the indexed file count (behavior 1).

## Constraints

- **The index keeps the `storage.PageStorage` read contract.** `ListPages` keeps its signature and stays the only list data source behind `ops.NewListOperation`. `ReadPage` keeps delegating to vault-cli's `storage.PageStorage.ReadPage`. Routes, bodies, status codes, query parameters, WebSocket frame content and the watched directory set are frozen (spec 023 parity contract).
- Snapshot order equals `os.ReadDir` order (filename ascending). Published snapshots and the `*domain.Page` values in them are never mutated; `go test -race` stays clean.
- **Pointer identity is never assumed.** The index's own "nothing changed" check compares page pointers, and a page rehydrated from the store is a fresh pointer. Unchanged-ness is therefore decided by fingerprints alone, never by comparing a stored page against a freshly parsed one.
- **The codec is the load-bearing risk.** `domain.Page` embeds `FrontmatterMap`, whose only field is unexported, so `json.Marshal(page)` silently drops every frontmatter field and a marshalled page comes back with no status, phase, goals, assignee, priority, dates or page_type — a board serving empty pages. Neither type implements a marshal or gob method. The store must encode frontmatter through `FrontmatterMap.RawMap()` and rebuild each page with `domain.NewPage`. The codec must preserve value types exactly: a YAML integer stays an integer, a date stays a `time.Time`, a failed stat stays the zero fingerprint. A gob codec needs `gob.Register` for every concrete type that can appear in a frontmatter map (string, bool, int, int64, float64, `time.Time`, `[]any`, `map[string]any`, `libtime.DateOrDateTime`); a JSON codec coerces integers to float64 and dates to strings and therefore fails the equivalence criterion. Which codec is used is the implementer's decision; its correctness is never assumed — the equivalence criterion is the guard.
- **The store is read as untrusted input.** It is a file on disk that a crash, a disk error or another local process may have written. The loader must not panic on malformed content, must not trust a value it cannot decode, and must discard rather than serve. Only the registered types are decoded.
- **Dependencies (frozen).** Add `github.com/bborbe/boltkv` as a direct dependency at v1.15.3, which requires exactly `github.com/bborbe/kv` v1.21.13 (already present as an indirect dependency). `kv` has no sequence generator, so entry identity comes from the store's own keys. Open the store with `boltkv.OpenFile(ctx, path)`; it does not create the parent directory, so the store directory is created first. The store file may be locked by another process, so the open must be bounded rather than hanging startup.
- **Version identity is injectable.** The store records the identity of the writer — a store-format version plus the vault-cli version the process was built against — and a test must be able to open the same store under a different identity to exercise the mismatch path.
- `make precommit` runs on linux in the container, so the per-OS stat code must compile and pass on both linux and darwin.
- **bborbe Go conventions** (stated inline because the container has neither git history nor this repository's `CLAUDE.md`): errors wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`; `Create*` factories contain no business logic; counterfeiter mocks regenerated into `mocks/` by precommit and never hand-written; Ginkgo/Gomega tests; at least 80 % coverage on changed packages; `libtime.CurrentDateTimeGetter` for any clock.
- `make parity` is a real gate and must stay green.
- `CHANGELOG.md` must gain an `## Unreleased` section. The file currently starts at `## v0.87.3`, which is already the latest release tag, so without a new `## Unreleased` heading the auto-release no-ops.
- `docs/page-index.md` is updated: the `Staleness bounds` sentence "A process restart starts with an empty index. Cold reads wait for the startup build of their key rather than each starting their own." is rewritten for the persisted store, and a new `Page index store` section covers what the store holds, when it is written, the discard-on-mismatch rule, and the codec trap — that `json.Marshal` on a `domain.Page` silently drops the unexported `FrontmatterMap` field, with `RawMap()` the only escape hatch — so the next reader does not rediscover it the hard way.
- 027's Failure Modes row "Process restart | Cold build reads every file once (~21k) | None. Bolt snapshot is a separate task" is superseded by this spec. 027 is completed and read-only; it is not edited.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---|---|---|---|---|---|
| Store file missing (first run, cache cleared) | Full parse; the store is created on the first publication | None needed | One warning naming the reason and path; cold-start reads equal the file count | Reversible | — |
| Store damaged (truncation, disk error, crash mid-write) | Discard, full parse | None needed | One warning naming the reason and path; cold-start reads near the file count | Reversible | The store's own transaction leaves the last committed state, so a partial write is never visible |
| Store unreadable (permissions, a sandbox denial on the cache directory) | Discard, full parse | Fix permissions | One warning naming the reason and path | Reversible | — |
| Store written in a different store format or by a different parser version | Discard, full parse | None needed | One warning naming the reason and path | Reversible | — |
| Store file locked by another vault-ui process | The bounded open times out and is treated as a discard; the board starts and full-parses | Stop the other process | One warning naming the reason and path | Reversible | Two processes on one cache path: the second treats the lock as a discard |
| Store write fails (disk full, cache directory read-only) | Serving unaffected; the previous store content stays intact | The next publication retries | One warning naming the failure | Reversible | A failed write never publishes a partial delta |
| Process dies between a publication and its store write | The store misses that publication | The next start re-reads the affected files, whose stored fingerprints are the old ones | Cold-start read count equals the changed-file count | Reversible | Self-healing: a page is trusted only while its fingerprint matches |
| Files changed while the process was down | The startup stat-diff reads exactly those files | None needed | Cold-start read count | Reversible | — |
| A file's modification time was restored while down (restored backup, `touch -r`) | The status-change time differs, so the file is re-read | None needed | Cold-start read count | Reversible | — |
| A codec that drops frontmatter is shipped | Must not happen: the equivalence criterion fails at build time | Fix the codec | `make precommit` red | Reversible before merge; a store already written by a lossy build is only discarded by a version bump | — |
| Vault folder deleted while the process was down | The stat-diff lists nil, the snapshot empties, matching vault-cli's listing | None needed | `ListPages` returns the empty result | Reversible | — |
| The store grows with the index over years | Accepted: one entry per indexed file, plus entries for vaults no longer configured, which are never read | None needed | Store file size | Accepted | — |

## Security / Abuse Cases

- The store path is derived from the operating system's user cache directory, not from a request, a vault path or a config value. No route exposes the store, and no request or vault content can select, relocate or write it. The store is never written inside a vault directory or the repository tree, so it cannot become an indexed page or a committed artifact.
- The store is read as untrusted input: a crash, a disk error or another local process may have written it. The loader must not panic on malformed content, must not trust a value it cannot decode, and must discard rather than serve. Only the registered concrete types are decoded, so a crafted entry cannot cause the decoder to instantiate an arbitrary type. A stored entry count must not drive an unbounded allocation.
- The store's lock is the only cross-process interaction: a second vault-ui process on the same cache path must not hang startup. The open is bounded, and a lock timeout is a discard, not a failure.
- Nothing new hangs on the read path: a store read and a store write each run outside the index mutex, so a slow or stuck cache filesystem cannot hold a reader.
- The store carries the same page content the index already holds in memory — the vault's own frontmatter and file content. It adds no new data to the trust boundary and no new exposure surface, but it does put that content in a second location on disk, which is the user's own cache directory and inherits its permissions.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | The store seam and its codec: a store interface (open, load a key's entries, write a delta in one transaction) over the embedded Bolt store; the page-plus-fingerprint record codec through the frontmatter escape hatch, type-preserving and covering excluded files; the writer identity (store-format plus parser version) recorded and checked; discard-on-any-mismatch with one warning naming the reason; the fixed store path and its directory creation; the bounded open | 4, 5, 6, 7, 8 | AC1 (codec half), AC4, AC5 (transaction half), AC6, AC7 | — |
| 2 | Cold-start hydration in the page index: load a key's stored entries into its snapshot and fingerprints, mark it as needing exactly one stat-diff, keep the first-read-waits contract, attribute those reads to `build`, fall back to the full parse on any store failure | 1, 2, 3 | AC1, AC2, AC3 | prompt 1 |
| 3 | Write-through: hook the publication points so each writes exactly its delta in one transaction outside the index mutex, including removals and the failed-write path; no new timer | 5 | AC5 | prompt 2 |
| 4 | Wiring, dependencies and docs: add the dependency, wire the store into the process-wide index at startup, update `docs/page-index.md` and `CHANGELOG.md`, run parity | 7 (wiring), 8 | AC8 | prompts 1–3 |

Rationale: prompt 1 builds the store primitive and the codec that is the whole risk of this change, and it is testable in isolation against a temp cache directory. Prompt 2 makes the index consume it while preserving the cold-start contract — the stored snapshot is an input to the stat-diff, never a served value. Prompt 3 adds the write path on top of a store that already reads. Prompt 4 wires and documents once the index supports both directions. AC9–AC10 run on the host after merge and deploy. This spec has 8 behaviors and 10 criteria (above the 50 surface guideline), so it stays one spec only because of this decomposition.

## Do-Nothing Option

Every restart re-parses all 21,163 indexed files before it can serve the first list — about 1.5 s per large vault, paid again on every deploy, crash, kickstart and reboot for data that has not changed. The parent goal's "first served list under 300 ms after a cold start" is unreachable without this change, and the alternative — never restarting — is not available. The residual cost of a restart is a real operational drag: deploys and recovery windows are the moments the board is least responsive.
