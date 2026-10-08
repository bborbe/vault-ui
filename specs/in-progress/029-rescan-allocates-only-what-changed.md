---
status: verifying
approved: "2026-10-08T07:21:29Z"
generating: "2026-10-08T08:53:55Z"
prompted: "2026-10-08T09:21:42Z"
verifying: "2026-10-08T17:39:27Z"
branch: dark-factory/rescan-allocates-only-what-changed
---

## Summary

- The board's residual CPU cost is a burst every 50 seconds: the page index's periodic rescan of every indexed folder.
- Measured 2026-10-07/08 on a watcher-less diagnostic build of the release commit: four CPU bursts above 5 % in a 200 s timeline, 50.2 s apart (`mod 50` = 2.3 / 2.5 / 1.7 / 1.9), while the 60 s session refreshes produced nothing above 5 %; and 1,037 MB allocated in a 120 s window, dominated by the rescan's own path.
- A rescan pass rebuilds each folder's whole in-memory page snapshot, keeps a second full copy of the folder's recorded file fingerprints, and allocates two folder-sized lookup structures — on every pass, including passes that find nothing changed.
- This spec makes a rescan's allocation proportional to what changed: an unchanged pass publishes nothing, keeps the published snapshot and the recorded fingerprints, and allocates no per-page structure; a pass that changes K files updates the recorded fingerprints for those K names in place, so the *set's* own update is proportional to K — the pass still merges into the folder-sized pages snapshot, so its total cost there is O(N).
- The 50 s cadence, the 60 s staleness ceiling, the published-snapshot immutability, the read set of a stat-diff, the WebSocket frame ordering and read-your-writes are unchanged.

## Problem

The rescan runs every 50 s (`RescanInterval`) and each pass rebuilds each indexed folder's snapshot from scratch: it copies the folder's entire recorded-fingerprint set, allocates a per-name order slice and a folder-sized reads map, then rebuilds the published pages slice and a fresh fingerprint map, and discards the lot when the comparison shows nothing changed. On the real vault — `private-personal/25 Tasks` alone holds 5,908 files, 14,282 `.md` files vault-wide — that is megabytes of garbage per key per pass, and it is the measured residual: a 200 s CPU timeline on a watcher-less diagnostic of the release commit showed four bursts above 5 %, at `mod 50` = 2.3 / 2.5 / 1.7 / 1.9 (50.2 s apart), while the 60 s session-snapshot and session-state refreshes produced nothing above 5 % in the same window; the same board's steady-state allocation over 120 s was 1,037 MB, with `pageindex.(*pageIndex).collect` 15.5 % cumulative, `pageindex.(*directoryLister).ListFiles` 3.7 %, `pageindex.mergeSnapshot` 15.4 MB flat, plus `pageindex.recordedFingerprints`, `os.statNolog`, `os.(*File).readdir` and the vault read/YAML-parse path. Production's bursts reach 55 % CPU for one second, ~5.5× the diagnostic's 10 % — the same factor as the allocation gap between the two environments. This residual is what fails the board's CPU criterion.

Measured on the pre-change code by this spec's own allocation test (2026-10-08, `darwin`, one key over a real temp dir of identical small pages): one unchanged stat-diff allocated **2.16×** the folder listing's bytes at 2,000 files, **1.95×** at 5,000 and **1.74×** at 14,000 — i.e. roughly half of a pass's bytes are structures the pass throws away. The same measurement puts the pass's *wall time* at 0.95–1.14× the listing's, which locates the pass's CPU floor in the listing itself (one `readdir` plus one stat per file) and its removable cost in the allocation.

## Goal

A rescan's cost is proportional to what changed. A stat-diff over a folder whose entries are unchanged publishes no snapshot, keeps the published snapshot and the recorded fingerprint set exactly as they are, and allocates no per-page structure — its allocation is bounded by the directory listing it performs to detect change. A stat-diff that finds K changed, added or removed files updates the recorded fingerprints for those K names in place — so the *set's* own update is proportional to K and not to the folder's size — and builds a new published pages slice only when the page set or its order actually moved; the pass still merges into the pages snapshot, which is sized to the folder and walked in lockstep with the listing, so a pass with work costs O(N) there. The 50 s cadence, the 60 s staleness ceiling, the published-snapshot immutability, the read set of a stat-diff, the frame ordering and read-your-writes are unchanged.

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
- [ ] **AC5 — the stat-diff's read set and snapshot content are unchanged.** Evidence: `go test -race ./pkg/pageindex/...` exits 0 with `git diff` empty for `pkg/pageindex/pageindex_statdiff_test.go`, `pkg/pageindex/equivalence_test.go` and `pkg/pageindex/pageindex_revision_test.go`, and `make parity` exits 0. In the container the byte-identical half is a `sha256sum -c` digest check, not a `git diff`: `.dark-factory.yaml` sets `hideGit: true`, so `.git` is masked and `git diff` exits non-zero without comparing anything.
- [ ] **AC6 — the frame-ordering, read-your-writes and write-mark rules are untouched.** Evidence: `go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/mutations/... ./pkg/factory/...` exits 0 with `git diff` empty for the test files under `pkg/watchrefresh`, `pkg/mutations` and `pkg/factory` — a `sha256sum -c` digest check in the container, for the same `hideGit` reason, covering every `_test.go` file those three packages track. A passing test run is not this evidence: it does not show the files were unmodified.
- [ ] **AC7 — a pass with a pending write mark never takes the unchanged fast path.** A test warms a key over a real dir, marks one file with `MarkFileDirty`, and runs `Refresh` with no fingerprint change anywhere — evidence: the recording reader's names are exactly `["<that file>"]`, so the marked file was re-read rather than short-circuited.
- [ ] **AC8 — the contract is written down.** Evidence: `grep -n 'allocate' docs/page-index.md` returns ≥ 1 line inside `Incremental updates`, and the changelog records the rescan's allocation — `awk '/^## (Unreleased|v)/{sec=$0} /page-index|stat-diff/ && /allocat/ && sec != ""{found=1; print "allocation bullet under: " sec} END{exit !found}' CHANGELOG.md` exits 0 and prints the section holding it. The match is anchored twice over — the section must be `## Unreleased` or a `## v` release, and the matching line must carry a `page-index` or `stat-diff` token as well as `allocat` — because testing `allocat` alone in any section sets `found` on an unrelated bullet that merely contains the word, so a future `perf:` or `refactor:` entry would satisfy this AC for the wrong reason. That section is `## Unreleased` only in the window between the implementation prompt landing the bullet and the next release; the release bot then cuts it into a `## vX.Y.Z` section, and a released bullet is the better outcome rather than a failure. Requiring `## Unreleased` specifically makes this evidence expire on the first release — which is what happened here: v0.90.1 was cut carrying this bullet, so the check that demanded `## Unreleased` false-failed on a correctly-released entry. A bare `grep -A10 '^## Unreleased'` is not evidence for this: it prints the section's *other* bullets identically when this one never landed, and prints a released section's body when a release has folded `Unreleased` away. Nor is a printing `awk` (`/allocat/{print sec}`): an `awk` that only prints exits 0 whether or not it printed anything, so that form passes whenever the heading merely exists.
- [ ] **Post-Deploy (Rung-2):** AC9 — the board's CPU criterion passes, or the residual is attributed and filed. Run the Operator-executable CPU block ≥ 2 min after a restart, with browsers connected and the agent fleet writing. Evidence, one of:
  - (a) each of 6 consecutive 10 s windows prints a CPU share < 5.0 %, with ≥ 5 of the 6 below the bar, and each window's load precondition read from `http://127.0.0.1:9090/metrics`: `vault_ui_websocket_connected_clients` ≥ 1 throughout and `vault_ui_websocket_broadcast_total` averaging ≥ 2 per 10 s across the 6 windows (≥ 12 over the 60 s), so a quiet board cannot satisfy it; or
  - (b) a vault task file exists under `~/Documents/Obsidian/private-personal/25 Tasks/`, linked to goal `[[Vault UI Ultra-Fast Reads and Writes]]`, recording the six window values and a 10 s `sample` attribution naming ≥ 1 site outside this spec's allocation path as the leading residual. Check with `grep -lE 'statFingerprint|os\.Stat|readdir|directoryLister' "<that file>"`.
  - `deploy_check:` `pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}') && python3 -c 'import os,subprocess,sys,time; s=" ".join(subprocess.check_output(["ps","-o","lstart=","-p",sys.argv[1]],text=True,env={"LC_ALL":"C","PATH":"/bin:/usr/bin"}).split()); sys.exit(0 if time.mktime(time.strptime(s,"%a %b %d %H:%M:%S %Y"))>=int(os.path.getmtime(sys.argv[2])) else 1)' "$pid" ~/Documents/workspaces/go/bin/vault-ui && [ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2):** AC10 — the rescan's allocation drops where it is the dominant allocator. Build the watcher-less loopback diagnostic from the deployed commit (the recipe the parent task used: admin bound to `127.0.0.1:<spare>`, the vault watcher dropped from the run group only), then read `go_memstats_alloc_bytes_total` from its `/metrics` across a 120 s window and take two cumulative `alloc_space` snapshots 120 s apart. Evidence, in two parts, both measured on the live vault in any 120 s window:
  - **(a) Aggregate:** the window's total is ≤ 65 % of the recorded pre-change 1,037 MB per 120 s.
  - **(b) The previously dominant site no longer dominates:** `mergeSnapshot`'s **flat** `alloc_space` is ≤ 65 % of its recorded pre-change 15.4 MB flat (≤ 10.01 MB).
  - The original form of this clause also required `recordedFingerprints` to be absent from the top 15. That name no longer exists in the source — `grep -rn recordedFingerprints --include='*.go' pkg/` returns nothing; the set is `entry.fingerprints`, updated in place by `(*snapshotMerge).pruneFingerprints` — so an absence clause on it is satisfied by construction, the "a check that cannot fail" shape AC8 rejects twice. It is dropped rather than restated as a magnitude bound, because there is no live site left to bound.
  - **Why (b) is a magnitude bound and not an absence, and must stay one.** Requiring `mergeSnapshot` to be *absent* from the top 15 demands a pass with zero work, and DB3 makes that unmeasurable on a live vault: a pass with any work still merges into the folder-sized pages snapshot, walked in lockstep with the listing, so a window containing even one changed file runs the merge path and ranks `mergeSnapshot` however much this change saved. Measured 2026-10-08/09 on the diagnostic, a window with a 60 s flat precondition still recorded 5 rescan reads, and `mergeSnapshot` ranked #6 and #7 in two such windows — while (a) passed in both, at 64.9 MB and 43.9 MB against the 674.1 MB bar, and `mergeSnapshot`'s own flat figure had fallen from 15.4 MB to 2.02–3.52 MB. The live vault churns faster than the 50 s rescan interval, so a change-free window does not occur; (b) therefore measures the drop this change claims rather than an absence the environment cannot produce.
  - `deploy_check:` `pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}') && python3 -c 'import os,subprocess,sys,time; s=" ".join(subprocess.check_output(["ps","-o","lstart=","-p",sys.argv[1]],text=True,env={"LC_ALL":"C","PATH":"/bin:/usr/bin"}).split()); sys.exit(0 if time.mktime(time.strptime(s,"%a %b %d %H:%M:%S %Y"))>=int(os.path.getmtime(sys.argv[2])) else 1)' "$pid" ~/Documents/workspaces/go/bin/vault-ui && [ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario. AC2–AC7 reach every behavior with real temp dirs, the recording seams and the race detector; AC1/AC8 are build-time; AC9/AC10 measure the live service. A browser scenario would add no signal the live CPU probe does not already carry.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format + vet + `go test -race ./...` + lint + typecheck; exits 0
- `go test -race ./pkg/pageindex/... -count=1` — the stat-diff, equivalence, revision and write-mark specs pass
- `go test ./pkg/pageindex/... -run TestUnchangedRescanAllocation -count=1 -v` — prints the listing bytes, the pass bytes and their ratio
- `make parity` — exits 0; the equivalence harness over the real vault
- `grep -n 'allocate' docs/page-index.md` — docs prompt only
- `awk '/^## (Unreleased|v)/{sec=$0} /page-index|stat-diff/ && /allocat/ && sec != ""{found=1; print "allocation bullet under: " sec} END{exit !found}' CHANGELOG.md` — exits 0 and prints the section holding the bullet; docs prompt only

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

Allocation (AC10), on the watcher-less loopback diagnostic. Both parts run on the live vault in the same 120 s window.

```bash
a=$(curl -s http://127.0.0.1:9091/metrics | awk '/^go_memstats_alloc_bytes_total/{print $2}'); sleep 120
b=$(curl -s http://127.0.0.1:9091/metrics | awk '/^go_memstats_alloc_bytes_total/{print $2}')
echo "allocated in 120 s: $(python3 -c "print(($b-$a)/1048576)") MB"
# two cumulative alloc_space snapshots 120 s apart, diffed with -diff_base.
# Flags MUST precede the profile arguments: pprof stops parsing flags at the first
# positional argument, so `-diff_base a.pb.gz b.pb.gz -top` treats -top as a
# filename, prints no site list at all, and a grep for an absent function then
# "passes" on empty output - a check that cannot fail.
go tool pprof -diff_base=/tmp/alloc-1.pb.gz -top -nodecount=15 -sample_index=alloc_space /tmp/alloc-2.pb.gz
# (b): mergeSnapshot's flat column at or below 10.01 MB. pprof's diff header prints
# "of <N> total", which is the BASE
# profile's cumulative alloc_space and not the diff; the diff total is the
# "accounting for" figure, confirmed with -nodecount=100000.
```

## Desired Behavior

1. **An unchanged stat-diff allocates nothing beyond the listing.** The fast path requires a **published snapshot already serving the key**. A baseline hydrated from the store carries recorded fingerprints but no published pages, so taking the fast path there would publish nothing and leave the key serving nothing — a hydrated key with no published snapshot is a build, not an unchanged pass. Given a published snapshot, when a stat-diff finds that every listed entry is **present** in the recorded fingerprint set with a recorded value equal to the listed fingerprint, that the listed name set equals the recorded name set, and that the pass consumed no file to re-read, then: no new pages slice is built, the recorded fingerprint set is not copied or replaced, no per-name order structure and no folder-sized lookup map are allocated, nothing is published, and the key's recorded fingerprints and published snapshot are left in place. The pass still performs exactly one folder listing, still resolves the mark that triggered it, and still publishes nothing — so a reader is unaffected. Membership and equality are checked as two values — `recorded, ok := set[name]` then `ok && recorded == listed` — never as a truthiness test on the fingerprint alone: a stat that failed yields the fingerprint's zero value, so a zero-equals-zero comparison would read a missing or unreadable entry as unchanged.
2. **A pass with work to do is not an unchanged pass.** A stat-diff that has work — a name whose fingerprint differs, a name that is new, a file a write mark asked for, or a name the listing no longer holds — reads exactly the files that need re-reading (a removed name has no file to read, but it does make the pass a changed pass, so the removal is recorded and the name-set equality above is restored) and publishes a new snapshot when the page set or its order moved. The unchanged fast path is never taken on a pass that consumed a write mark.
3. **The recorded fingerprint set is index-private and updated in place.** The fingerprint set is read only by the pass's own comparison and written only under the index mutex; it is never published to a reader. A pass that changes K names updates the set for those K names rather than building a second full copy of it, so the *set's* update is proportional to K and not to the folder's file count. That bound is the set's alone: a pass with work still merges into the pages snapshot, which is sized to the folder and walked in lockstep with the listing, so a one-file change in an N-file folder still costs O(N) there. A name the listing no longer holds has its entry **deleted** from the set in that same pass: a tombstone kept past the pass would leave the recorded name set permanently larger than any listing, so the name-set equality above could never hold again and the unchanged fast path would be dead for that key for the life of the process.
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
| A key with no snapshot yet and no stored baseline | The cold build reads every file, as today; the fast path does not apply | None needed | Counter `reason="build"` | Reversible | Cold-read sharing is unchanged |
| A key hydrated from the store — a baseline, but no published snapshot | The pass is a changed pass, not an unchanged one: it reads the names whose fingerprints differ from the hydrated baseline and publishes its first snapshot. The fast path cannot fire, because there is no published snapshot to keep and it would publish nothing | None needed — the designed path | A published snapshot exists after one pass over an unchanged folder | Reversible | Hydration is not a published value, so no reader is ever served the unstat-diffed baseline |
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
| 2 | Contract lock: keep the stat-diff, equivalence, revision, frame-ordering, write-mark and factory specs green and unmodified, and run `make parity` | 5 | AC5, AC6 | prompt 1 — **no prompt of its own; folded into row 1's verification** |
| 3 | Docs: the allocation contract in `docs/page-index.md` | — | AC8 | prompt 1 |

Rationale: `prompts/1-spec-029-rescan-unchanged-fast-path.md` is the whole behavioral change and the only prompt that touches production code; its tests are the mechanical evidence. It also carries the contract lock (row 2) — the guard that the pass's observables did not move while its allocation did, which must not need to edit an existing spec, and if it does, that edit is the finding. `prompts/2-spec-029-allocation-contract-docs.md` cannot start before the shape is settled and before row 1 has added the `## Unreleased` bullet it asserts. AC1 is carried by every prompt; AC9 and AC10 run on the host after merge.

The container-executable list is split across the generated prompts and must be read that way, not as one list per prompt. `prompts/1-spec-029-rescan-unchanged-fast-path.md` — the implementation prompt, and the only prompt that touches production code — runs `make precommit`, the `pkg/pageindex` / `pkg/watchrefresh` / `pkg/mutations` / `pkg/factory` test run, `make parity` and the byte-identical digest checks over the frozen `pkg/pageindex` spec files and the `pkg/watchrefresh` / `pkg/mutations` / `pkg/factory` test files (AC5, AC6); `make parity` is not part of `make precommit`, so it is its own bullet. `prompts/2-spec-029-allocation-contract-docs.md` — the docs prompt — runs the two grep bullets, `docs/page-index.md` and the `## Unreleased` section (AC8). It *asserts* the changelog bullet but does not produce it: the implementation prompt owns that bullet (its requirement 11), which is why the docs prompt is verifier-only there and must not add a second entry. Neither prompt runs the other's bullets: running the list whole makes the docs prompt fail on an artifact it does not own, and the changelog bullet does not exist until the implementation prompt has run.

Two prompts were generated from the three rows above, not three: the contract lock (row 2) has no prompt of its own and is carried by the implementation prompt's verification, which is where the frozen-file and `make parity` evidence already lives — the same container run that changes the code is the one that must prove the observables did not move. If that guard ever needs a second pair of eyes rather than a second command, it is a new row, not a silent third prompt.

## Do-Nothing Option

The rescan keeps rebuilding every folder's snapshot and copying its fingerprint set every 50 s, for a pass that usually finds nothing. The board's CPU criterion stays red, and the cost grows with the vault — `private-personal/25 Tasks` went from 5,760 files on 2026-10-06 to 5,908 on 2026-10-08, and the rescan's allocation scales with that number. The parent task's CPU criterion cannot be met while a pass over an unchanged folder costs roughly twice the directory listing it is obliged to perform.
