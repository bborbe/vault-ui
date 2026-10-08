---
status: prompted
approved: "2026-10-08T07:21:29Z"
generating: "2026-10-08T08:53:55Z"
prompted: "2026-10-08T09:21:42Z"
branch: dark-factory/rescan-allocates-only-what-changed
---

## Summary

- The board's residual CPU cost is a burst every 50 seconds: the page index's periodic rescan of every indexed folder.
- Measured 2026-10-07/08 on a watcher-less diagnostic build of the release commit: four CPU bursts above 5 % in a 200 s timeline, 50.2 s apart (`mod 50` = 2.3 / 2.5 / 1.7 / 1.9), while the 60 s session refreshes produced nothing above 5 %; and 1,037 MB allocated in a 120 s window, dominated by the rescan's own path.
- A rescan pass rebuilds each folder's whole in-memory page snapshot, keeps a second full copy of the folder's recorded file fingerprints, and allocates two folder-sized lookup structures — on every pass, including passes that find nothing changed.
- This spec makes a rescan's allocation proportional to what changed: an unchanged pass publishes nothing, keeps the published snapshot and the recorded fingerprints, and allocates no per-page structure; a pass that changes K files allocates in proportion to K.
- The 50 s cadence, the 60 s staleness ceiling, the published-snapshot immutability, the read set of a stat-diff, the WebSocket frame ordering and read-your-writes are unchanged.

## Problem

The rescan runs every 50 s (`RescanInterval`) and each pass rebuilds each indexed folder's snapshot from scratch: it copies the folder's entire recorded-fingerprint set, allocates a per-name order slice and a folder-sized reads map, then rebuilds the published pages slice and a fresh fingerprint map, and discards the lot when the comparison shows nothing changed. On the real vault — `private-personal/25 Tasks` alone holds 5,908 files, 14,282 `.md` files vault-wide — that is megabytes of garbage per key per pass, and it is the measured residual: a 200 s CPU timeline on a watcher-less diagnostic of the release commit showed four bursts above 5 %, at `mod 50` = 2.3 / 2.5 / 1.7 / 1.9 (50.2 s apart), while the 60 s session-snapshot and session-state refreshes produced nothing above 5 % in the same window; the same board's steady-state allocation over 120 s was 1,037 MB, with `pageindex.(*pageIndex).collect` 15.5 % cumulative, `pageindex.(*directoryLister).ListFiles` 3.7 %, `pageindex.mergeSnapshot` 15.4 MB flat, plus `pageindex.recordedFingerprints`, `os.statNolog`, `os.(*File).readdir` and the vault read/YAML-parse path. Production's bursts reach 55 % CPU for one second, ~5.5× the diagnostic's 10 % — the same factor as the allocation gap between the two environments. This residual is what fails the board's CPU criterion.

Measured on the pre-change code by this spec's own allocation test (2026-10-08, `darwin`, one key over a real temp dir of identical small pages): one unchanged stat-diff allocated **2.16×** the folder listing's bytes at 2,000 files, **1.95×** at 5,000 and **1.74×** at 14,000 — i.e. roughly half of a pass's bytes are structures the pass throws away. The same measurement puts the pass's *wall time* at 0.95–1.14× the listing's, which locates the pass's CPU floor in the listing itself (one `readdir` plus one stat per file) and its removable cost in the allocation.

## Goal

A rescan's cost is proportional to what changed. A stat-diff over a folder whose entries are unchanged publishes no snapshot, keeps the published snapshot and the recorded fingerprint set exactly as they are, and allocates no per-page structure — its allocation is bounded by the directory listing it performs to detect change. A stat-diff that finds K changed, added or removed files allocates in proportion to K, not to the folder's size: it updates the recorded fingerprints for those K names in place and builds a new published pages slice only when the page set or its order actually moved. The 50 s cadence, the 60 s staleness ceiling, the published-snapshot immutability, the read set of a stat-diff, the frame ordering and read-your-writes are unchanged.

## Non-goals

- Do NOT change `RescanInterval` (50 s). The interval is the freshness guarantee; the cost per pass is the defect.
- Do NOT add a knob or a new metric: no configurable interval, no opt-out flag for the unchanged fast path, no selectable fingerprint, no allocation counter. These are invariants; if a future consumer demands variation, that is a separate spec.
- Do NOT expose pprof or any new profiler route. PR #149 was closed on a verified security Critical: `:9090` binds all interfaces and spec 021 forbids an admin-port knob.
- Do NOT remove or replace the per-entry stat in the directory listing, and do NOT reach for a platform-specific bulk-attribute syscall. The listing is how a stat-diff detects change without an event, and it is the pass's CPU floor; if the CPU criterion's residual turns out to be that stat cost, it is a separate spec.
- Do NOT change what a stat-diff reads, the snapshot's content or order, the write-mark rules, the frame contract, the response bodies, or vault-cli (no version bump).
- Do NOT change the cold build or `ForceReload`: both re-read every file by design, and neither is a rescan.

## Acceptance Criteria

**Write the AC before the Desired Behavior.** The container ACs are the mechanical proof that the pass's allocation moved; the two Post-Deploy ACs are the board's own acceptance bar.

- [ ] **AC1 — the tree is green.** `make precommit` exits 0 — evidence: exit code.
- [ ] **AC2 — an unchanged stat-diff allocates no more than the folder listing it must perform.** A test warms a key over a real temp dir holding 5,000 pages, then measures the `runtime.MemStats.TotalAlloc` delta around one direct `ListFiles` call and around one `Refresh` over the unchanged folder — evidence: the test prints both byte counts and their ratio, and the ratio is ≤ 1.35. Pre-change readings from the same test (2026-10-08): 2.16 at 2,000 files, 1.95 at 5,000, 1.74 at 14,000, so this AC discriminates against the pre-change code at every size measured. The same test also prints the pass's absolute byte count and asserts it is ≤ 5.5 MB at 5,000 files — the pre-change reading was 6,160,336 B and the expected post-change value is ≈ 3.2 MB — so a pass that merely inflates the listing it is measured against cannot satisfy this AC.
- [ ] **AC3 — an unchanged stat-diff replaces neither the recorded fingerprint set nor the published snapshot.** Through an `export_test.go` accessor, capture the identity of the key's recorded fingerprint set and of the published snapshot slice before and after one `Refresh` over the unchanged folder — evidence: the four identity values are printed and both before/after pairs are equal. On the pre-change code the fingerprint set's identity differs, because the map is replaced wholesale.
- [ ] **AC4 — a changed stat-diff allocates in proportion to the change.** The same test, after rewriting 10 of the 5,000 files — evidence: the test prints the pass's byte count, its ratio to the listing, and the recording reader's names; the names are exactly the 10 changed files and the ratio is ≤ 1.5.
- [ ] **AC5 — the stat-diff's read set and snapshot content are unchanged.** Evidence: `go test -race ./pkg/pageindex/...` exits 0 with `git diff` empty for `pkg/pageindex/pageindex_statdiff_test.go`, `pkg/pageindex/equivalence_test.go` and `pkg/pageindex/pageindex_revision_test.go`, and `make parity` exits 0.
- [ ] **AC6 — the frame-ordering, read-your-writes and write-mark rules are untouched.** Evidence: `go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/mutations/... ./pkg/factory/...` exits 0 with `git diff` empty for the test files under `pkg/watchrefresh`, `pkg/mutations` and `pkg/factory`.
- [ ] **AC7 — a pass with a pending write mark never takes the unchanged fast path.** A test warms a key over a real dir, marks one file with `MarkFileDirty`, and runs `Refresh` with no fingerprint change anywhere — evidence: the recording reader's names are exactly `["<that file>"]`, so the marked file was re-read rather than short-circuited.
- [ ] **AC8 — the contract is written down.** Evidence: `grep -n 'allocate' docs/page-index.md` returns ≥ 1 line inside `Incremental updates`, and `grep -A10 '^## Unreleased' CHANGELOG.md` shows a bullet about the rescan's allocation.
- [ ] **Post-Deploy (Rung-2):** AC9 — the board's CPU criterion passes, or the residual is attributed and filed. Run the Operator-executable CPU block ≥ 2 min after a restart, with browsers connected and the agent fleet writing. Evidence, one of:
  - (a) each of 6 consecutive 10 s windows prints a CPU share < 5.0 %, with ≥ 5 of the 6 below the bar, and each window's load precondition read from `http://127.0.0.1:9090/metrics`: `vault_ui_websocket_connected_clients` ≥ 1 throughout and `vault_ui_websocket_broadcast_total` averaging ≥ 2 per 10 s across the 6 windows (≥ 12 over the 60 s), so a quiet board cannot satisfy it; or
  - (b) a vault task file exists under `~/Documents/Obsidian/private-personal/25 Tasks/`, linked to goal `[[Vault UI Ultra-Fast Reads and Writes]]`, recording the six window values and a 10 s `sample` attribution naming ≥ 1 site outside this spec's allocation path as the leading residual. Check with `grep -lE 'statFingerprint|os\.Stat|readdir|directoryLister' "<that file>"`.
  - `deploy_check:` `pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}') && python3 -c 'import os,subprocess,sys,time; s=" ".join(subprocess.check_output(["ps","-o","lstart=","-p",sys.argv[1]],text=True,env={"LC_ALL":"C","PATH":"/bin:/usr/bin"}).split()); sys.exit(0 if time.mktime(time.strptime(s,"%a %b %d %H:%M:%S %Y"))>=int(os.path.getmtime(sys.argv[2])) else 1)' "$pid" ~/Documents/workspaces/go/bin/vault-ui && [ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2):** AC10 — the rescan's allocation drops where it is the dominant allocator. Build the watcher-less loopback diagnostic from the deployed commit (the recipe the parent task used: admin bound to `127.0.0.1:<spare>`, the vault watcher dropped from the run group only), then read `go_memstats_alloc_bytes_total` from its `/metrics` across a 120 s window and take two cumulative `alloc_space` snapshots 120 s apart — evidence: the window's total is ≤ 65 % of the recorded pre-change 1,037 MB per 120 s, and neither `mergeSnapshot` nor `recordedFingerprints` appears among the top 15 `alloc_space` sites.
  - `deploy_check:` `pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}') && python3 -c 'import os,subprocess,sys,time; s=" ".join(subprocess.check_output(["ps","-o","lstart=","-p",sys.argv[1]],text=True,env={"LC_ALL":"C","PATH":"/bin:/usr/bin"}).split()); sys.exit(0 if time.mktime(time.strptime(s,"%a %b %d %H:%M:%S %Y"))>=int(os.path.getmtime(sys.argv[2])) else 1)' "$pid" ~/Documents/workspaces/go/bin/vault-ui && [ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario. AC2–AC7 reach every behavior with real temp dirs, the recording seams and the race detector; AC1/AC8 are build-time; AC9/AC10 measure the live service. A browser scenario would add no signal the live CPU probe does not already carry.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format + vet + `go test -race ./...` + lint + typecheck; exits 0
- `go test -race ./pkg/pageindex/... -count=1` — the stat-diff, equivalence, revision and write-mark specs pass
- `go test ./pkg/pageindex/... -run TestUnchangedRescanAllocation -count=1 -v` — prints the listing bytes, the pass bytes and their ratio
- `grep -n 'allocate' docs/page-index.md`
- `grep -A10 '^## Unreleased' CHANGELOG.md`

### Operator-executable (runs on the host after PR merge, spec verification ladder)

Deploy:

```bash
cd ~/Documents/workspaces/vault-ui && git pull && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
sleep 120   # warm build finished, startup excluded from measurements
pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}')
```

CPU (AC9), the criterion's named probe plus its cross-check:

```bash
for w in 1 2 3 4 5 6; do
  top -l 2 -pid "$pid" -stats cpu | tail -1 | awk -v w="$w" '{print "window " w ": " $1 "%"}'
  sleep 10
done
c () { ps -o cputime= -p "$pid" | python3 -c 'import sys; p=sys.stdin.read().strip().split(":"); print(sum(float(x)*60**i for i,x in enumerate(reversed(p))))'; }
for w in 1 2 3 4 5 6; do a=$(c); sleep 10; b=$(c); python3 -c "print(f'cputime window $w: {($b-$a)*10:.1f}%')"; done
a=$(curl -s http://127.0.0.1:9090/metrics | awk '/^vault_ui_websocket_broadcast_total/{print $2}'); sleep 60
b=$(curl -s http://127.0.0.1:9090/metrics | awk '/^vault_ui_websocket_broadcast_total/{print $2}')
curl -s http://127.0.0.1:9090/metrics | grep vault_ui_websocket_connected_clients
echo "broadcasts over 60 s: $(python3 -c "print($b-$a)")"
```

Allocation (AC10), on the watcher-less loopback diagnostic:

```bash
a=$(curl -s http://127.0.0.1:9091/metrics | awk '/^go_memstats_alloc_bytes_total/{print $2}'); sleep 120
b=$(curl -s http://127.0.0.1:9091/metrics | awk '/^go_memstats_alloc_bytes_total/{print $2}')
echo "allocated in 120 s: $(python3 -c "print(($b-$a)/1048576)") MB"
# two cumulative alloc_space snapshots 120 s apart, diffed with -diff_base
```

## Desired Behavior

1. **An unchanged stat-diff allocates nothing beyond the listing.** When a stat-diff finds that every listed entry's fingerprint equals the one recorded for it and the listed name set equals the recorded name set, and the pass consumed no file to re-read, then: no new pages slice is built, the recorded fingerprint set is not copied or replaced, no per-name order structure and no folder-sized lookup map are allocated, nothing is published, and the key's recorded fingerprints and published snapshot are left in place. The pass still performs exactly one folder listing, still resolves the mark that triggered it, and still publishes nothing — so a reader is unaffected.
2. **A pass with work to do is not an unchanged pass.** A stat-diff that has a file to re-read — a name whose fingerprint differs, a name that is new, a file a write mark asked for — reads exactly those files and publishes a new snapshot when the page set or its order moved. The unchanged fast path is never taken on a pass that consumed a write mark.
3. **The recorded fingerprint set is index-private and updated in place.** The fingerprint set is read only by the pass's own comparison and written only under the index mutex; it is never published to a reader. A pass that changes K names updates the set for those K names rather than building a second full copy of it, so the pass's allocation is proportional to K and not to the folder's file count. A name the listing no longer holds keeps its entry as a tombstone, as today.
4. **A new pages snapshot is built only when the page set or its order moved.** A stat-diff whose reads produce the same pages in the same order publishes no new snapshot and allocates no new pages slice; the snapshot identity a reader holds is unchanged. A stat-diff that inserts, removes or replaces a page builds one new slice holding the previous snapshot's untouched page pointers and publishes it atomically.
5. **Every observable of a stat-diff is unchanged.** The read set is exactly the entries whose fingerprint differs or that are new; the snapshot equals what vault-cli's folder listing returns for the files read, in the same filename-ascending order with the same exclusions; a mark recorded before the pass started is resolved by it; a mark recorded while the pass is in flight stays pending for the next reader; a file's page in a published snapshot still comes from the most recently started read of that file; a listing or read error still publishes nothing and leaves the previous snapshot serving; frame ordering and read-your-writes are unchanged.

## Constraints

- `RescanInterval` stays 50 s and `rescanPollInterval` stays 1 s. No new timer is added anywhere.
- One immutable `[]*domain.Page` per key, never mutated after publication; concurrent readers share the same page pointers. This spec changes how the slice is *built*, never whether it is mutated.
- The read set of a stat-diff is exactly the entries whose fingerprint differs or that are new, and "differs" means inequality, not "newer".
- Snapshot order equals the folder listing's order (filename ascending), and resolving a page by name keeps the listing's sorted first-match behaviour. Neither may change.
- A mark recorded before a pass started is resolved by that pass; a mark recorded while a pass is in flight stays pending for the next reader.
- An incomplete listing is never published: a listing or a single-file read error leaves the previous snapshot serving, logs the error with the key, and leaves the mark pending.
- `ForceReload` (behind `POST /api/cache/reload`) and the cold build keep re-reading every file, ignoring fingerprints.
- The read surface is unchanged: the same `PageReader` and `DirectoryLister` seams, the same symlink-out-of-vault exclusion, and the same per-call `rebuildTimeout` bound on each storage call.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no ignored error returns, `Create*` factories without business logic, Ginkgo/Gomega tests, counterfeiter mocks for the existing seams, `libtime` injection for the clock, new code ≥ 80 % covered, CHANGELOG entry under `## Unreleased`.
- `docs/page-index.md` gains the allocation contract in its `Incremental updates` section: an unchanged stat-diff allocates no snapshot state, the recorded fingerprint set is index-private and updated in place, and a new snapshot is built only when the page set or its order moved.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---|---|---|---|---|---|
| A write mark is pending when a stat-diff starts | The pass is not an unchanged pass: the marked files are read | None needed — the designed path, asserted by AC7 | AC7's recording reader names exactly the marked file | Reversible | A mark recorded during the pass stays pending for the next reader, so a reader still sees its own write |
| A listing fails (TCC denial, unmounted folder) | The previous snapshot keeps serving; nothing is published; the error is logged with the key | The next rescan or read retries | ERROR log line naming the key | Reversible — the previous snapshot is intact | The mark stays pending, so concurrent readers wait on the retry rather than being served stale |
| A file changes while the pass lists it | The entry's fingerprint was taken before its read, so the change is caught by the next pass; the change is never half-published | Next pass, within the 60 s ceiling | The value appears late on the board | Reversible | — |
| A file is added or removed while the pass lists it | The name-set comparison sees it on the next pass; a partial listing is never published | Next pass | Board value appears late | Reversible | — |
| Two readers race the same unchanged pass | One runs the listing; the other waits or is served the current snapshot, per the existing single-flight rule | None needed | The lister call count in the test | Reversible | Exactly one listing runs; both readers observe the same snapshot |
| The recorded fingerprint set is empty but a snapshot exists (empty folder) | The empty listing equals the empty set, so the unchanged fast path applies and nothing is published | None needed | The lister call count and the unchanged snapshot identity in AC2's test, run once with the folder emptied | Reversible | — |
| Clock skew or a restored backup with older timestamps | Fingerprint comparison is inequality, so a changed file is read; unchanged files still take the fast path | None needed | AC4 | Reversible | — |
| A key with no snapshot yet | The cold build reads every file, as today; the fast path does not apply | None needed | Counter `reason="build"` | Reversible | Cold-read sharing is unchanged |
| Memory | The pass's peak allocation drops; the retained fingerprint set is unchanged in size | Accepted | Resident memory of the board process | Reversible | — |

## Security / Abuse Cases

- This spec adds no route, no request input, no configuration and no new file path. The pass is driven by `Refresh` and `Rescan` over keys derived exactly as today (`NewKey` plus `filepath.Clean`).
- The unchanged fast path reads nothing, so it cannot widen the read surface: the same `PageReader` (with vault-cli's symlink-out-of-vault exclusion) and the same `DirectoryLister` serve every read.
- Each listing and each single-file read stays bounded by the existing per-call `rebuildTimeout`, so a hung filesystem cannot hold a key — and its waiters — forever.
- Nothing new hangs or retries forever: the pass has no loop, no retry and no backoff of its own.
- No credential, token or file content is added to any response. No new metric is registered, so no new series can be scraped into an operator's alerting surface.
- No profiler is exposed: the admin port (`:9090`) and the board port (`:8000`) keep exactly the routes spec 021 fixes, and the attribution in AC10 uses a throwaway loopback build that is never deployed.

## Suggested Decomposition

Prompts are generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Index: fingerprint-set ownership (in-place update, no per-pass copy), the unchanged fast path in the stat-diff, and building a new pages slice only when the page set or its order moved — with the allocation and identity tests | 1, 2, 3, 4 | AC2, AC3, AC4, AC7 | — |
| 2 | Contract lock: keep the stat-diff, equivalence, revision, frame-ordering, write-mark and factory specs green and unmodified, and run `make parity` | 5 | AC5, AC6 | prompt 1 |
| 3 | Docs and changelog: the allocation contract in `docs/page-index.md`, a `## Unreleased` bullet | — | AC8 | prompt 1 |

Rationale: prompt 1 is the whole behavioral change and the only prompt that touches production code; its tests are the mechanical evidence. Prompt 2 is the guard that the pass's observables did not move while its allocation did — it must not need to edit an existing spec, and if it does, that edit is the finding. Prompt 3 cannot start before the shape is settled. AC1 is carried by every prompt; AC9 and AC10 run on the host after merge.

## Do-Nothing Option

The rescan keeps rebuilding every folder's snapshot and copying its fingerprint set every 50 s, for a pass that usually finds nothing. The board's CPU criterion stays red, and the cost grows with the vault — `private-personal/25 Tasks` went from 5,760 files on 2026-10-06 to 5,908 on 2026-10-08, and the rescan's allocation scales with that number. The parent task's CPU criterion cannot be met while a pass over an unchanged folder costs roughly twice the directory listing it is obliged to perform.
