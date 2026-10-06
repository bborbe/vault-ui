---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-10-06T08:59:58Z"
generating: "2026-10-06T09:01:26Z"
prompted: "2026-10-06T09:21:27Z"
verifying: "2026-10-06T11:30:50Z"
completed: "2026-10-06T13:17:20Z"
branch: dark-factory/incremental-page-index-updates
---

## Summary

- The board's in-memory page index (spec 024) re-reads a **whole folder** whenever one file in it changes. In the agent vault that means about 10,700 files re-read for every single task write. With agents writing ~11 task files per minute, the service sits at 71–78 % CPU.
- After this change, a change to one file re-reads only that file and splices the result into a new snapshot. The periodic safety-net rescan checks each file's size and timestamps and re-reads only files that changed, appeared or disappeared.
- A queued board write that edits one task's or goal's frontmatter marks only that file stale, provided the file can be identified exactly. Every other write falls back to the cheap size-and-timestamp check.
- List responses, WebSocket frames, the 60 s staleness ceiling and the frame-after-fresh-data ordering stay as they are. The costs outside the index (the per-request `ps` scan on task and goal lists) are measured here and become a follow-up. This spec does not take them on, and neither does it dedupe the symlinked `OpenClaw` vault.

Traceability: goal `[[Vault UI Ultra-Fast Reads and Writes]]` (Personal vault). It continues spec 024 (`specs/in-progress/024-serve-list-reads-from-page-index.md`), whose failure-mode row "Rebuild churn" deferred single-file re-reads.

## Problem

Measured on 2026-10-06 against the deployed v0.82.0 (restarted 10:33): the board process uses 71–78 % CPU continuously, `/api/tasks` p50 is ~3.0 s against `/api/vaults` at 1.5 ms, and right after a restart even `/api/vaults` takes 0.73 s because the process is saturated. A `sample` profile puts the hot path in the page index's folder rebuild, which two things drive:

1. Every task or goal watcher event rebuilds the event's entire folder.
2. The 50 s rescan re-parses every key.

The indexed folders are large: `private-agent/tasks` has 10,713 files and `private-personal/25 Tasks` has 5,760, out of 21,163 indexed files across 14 vaults. `OpenClaw` is a symlink to `private-agent`, so those 10,713 files are also indexed twice; dedupe is a separate spec. Because the cost of an update scales with folder size and not with change size, the CPU burn grows every day as the agent vault grows.

## Goal

The page index's work is proportional to what changed:

- A watcher event re-reads one file.
- A rescan of an unchanged folder re-reads nothing.
- A queued single-item write re-reads the file it wrote.

Every published snapshot still equals what vault-cli's own folder listing would return, and is never mutated after publication. A watcher event's frame is still sent only after a snapshot containing a read started after that event has been published. Any change to a file's presence, size or timestamps is still visible within 60 s without a watcher event. The idle board's CPU drops below 5 %, or the residual is attributed and filed as a follow-up.

## Non-goals

- No change to the per-request uncached `ps -axww -o args=` scan behind the task and goal lists' resume-session check. It measured 0.13–0.16 s wall per call on the host on 2026-10-06. That cost is a measured follow-up (AC12), not in scope.
- No change to vault-cli's list operation cost (filtering, status normalisation and sorting over ~21k in-memory pages). If AC9 or AC12 attribute residual cost to it, that is the same follow-up.
- Alias dedupe (`OpenClaw` → `private-agent` indexed and watched twice): separate spec.
- No vault-cli change and no vault-cli version bump. Single-file reads compose vault-cli's exported frontmatter parser and page constructor; see Constraints.
- No persistence of the index (the Bolt snapshot is a separate task).
- No change to routes, query parameters, response bodies, WebSocket frame content, or the watched directory set. Topics folders stay unwatched.
- No change to mutation write semantics, queue ordering or frames. Only the kind of dirty mark changes.
- Do NOT add a configurable rescan interval, an opt-out flag for incremental updates, or a selectable fingerprint. These are invariants; if a future consumer demands variation, that is a separate spec.
- Do NOT add a periodic forced full re-parse. The status-change-time fingerprint (Desired Behavior 3) covers rewrites that preserve the modification time. `POST /api/cache/reload` is the operator escape hatch.

## Acceptance Criteria

Fixture notes: "reader fake" and "lister fake" mean counterfeiter fakes of the index's injectable single-file reader and directory lister seams. "Real dir" means a temp directory on disk read through the production seams.

- [ ] **AC1: a single-file event reads exactly one file.** A Ginkgo test drives the watch handler that `factory.CreateWatcher` builds (fake watch operation, reader and lister fakes, a warm key holding 1,000 pages) with one `modified` task event. Evidence:
  - The reader fake records exactly **1** read, naming that event's file.
  - The lister fake records **0** directory listings.
  - The next `ListPages` read of the key returns the new content for that file.
  - The event's frame is broadcast strictly after that snapshot is published.
- [ ] **AC2: add, modify and delete splice into a new snapshot.** Tests against a warm key, each followed by a read. Evidence:
  - (a) a `created` event for a new file: the file appears at its filename-ordered position.
  - (b) `modified`: the file's page is replaced, and every other page in the new snapshot is the **same `*domain.Page` pointer** as in the previous snapshot. This proves a splice, not a rebuild.
  - (c) `deleted`: the page is gone.
  - (d) a `modified` event for a file that no longer exists on disk removes it. The file's current state decides; the event type is advisory.
  - (e) a `deleted` event for a file that exists re-reads and keeps it.
  - (f) a slice obtained from `ListPages` before each splice is unchanged after it: same length, same pointers.
- [ ] **AC3: snapshots equal vault-cli's listing (real dir).** A test builds a real dir containing:
  - plain pages;
  - a page whose frontmatter holds a bare `[[wikilink]]` value;
  - a file without frontmatter;
  - a file with invalid YAML;
  - a non-`.md` file;
  - a subdirectory;
  - a symlink pointing outside the vault;
  - a symlink pointing to a page inside the vault;
  - a page with a non-ASCII filename.

  It then runs a scripted sequence of create, modify, delete and rename (as a delete plus a create) through per-file updates and stat-diff rescans. The sequence includes rewriting the in-vault symlink's target page with no event delivered, after which a stat-diff must pick it up. Evidence: after every step, the key's snapshot is `reflect.DeepEqual` to `storage.NewPageStorage(...).ListPages` over the same dir. That covers the same pages, the same order and the same exclusions, with identical `FrontmatterMap`, `FileMetadata` and `Content`.
- [ ] **AC4: a stat-diff rescan reads only what changed.** A test with a fake clock advances past `RescanInterval`. Evidence:
  - (a) Nothing changed: **0** reads, exactly 1 listing per key, and the published snapshot is the identical slice (no new snapshot). The fixture holds one unparsable file left unchanged across 2 rescans, and the captured log has exactly **1** warning line naming it.
  - (b) One file modified, one added and one removed: exactly **2** reads (the modified and the added file) and the removed file is gone.
  - (c) One file rewritten with the same size and its modification time restored (`os.Chtimes` back to the old value): exactly **1** read, detected through the status-change time.
  - (d) `RescanInterval` ≤ 60 s.
- [ ] **AC5: queued single-item writes mark files, everything else stat-diffs.** End-to-end through the factory with reader and lister fakes and a real tasks dir. Evidence:
  - (i) A queued task phase change and a queued `PATCH /api/tasks/{id}/session`, each applied by the consumer and followed by a read whose id names an existing file exactly: exactly **1** read of that file and **0** listings. Two concurrent reads after the write still produce exactly 1 read, and the read returns the post-write value.
  - (ii) `POST /api/tasks/{id}/execute-command` with `defer-task`, a synchronous site that keeps a folder-level mark (chosen because it launches no `claude` process), followed by a read: 1 listing, plus reads only for files whose fingerprint changed (0 when none did). The read returns the post-write value.
  - (iii) `POST /api/cache/reload` followed by reads of every key: each file is read exactly once, so the read count equals the total file count.
  - (iv) A queued write whose id matches its file only through vault-cli's fallback lookup or a case-insensitive filesystem gets a folder-level mark. The fallback lookup is a case-insensitive substring match: for example id `probe` resolving to `Page Probe Task.md`, or id `page probe task` resolving to `Page Probe Task.md`. Each case produces exactly 1 listing, and the next read returns the post-write value.
- [ ] **AC6: concurrency never loses data or reads stale writes.** Tests with a blocking reader fake. Evidence:
  - (a) Two event-driven reads of the same file, where the earlier-started one finishes last: the published snapshot holds the later-started read's content.
  - (b) Event-driven reads of two different files of one key finishing in either order: both changes are visible.
  - (c) A `ListPages` call during a blocked **event- or rescan-driven** read returns the previous snapshot within 100 ms.
  - (d) A `ListPages` call on a key with a pending write mark, while the marked file's re-read is blocked, does **not** return before that re-read completes, and then returns the post-write content.
  - `go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/mutations/...` exits 0.
- [ ] **AC7: parse work is observable.** A factory-level test scrapes `GET /metrics`. Evidence:
  - The body contains `vault_ui_page_index_files_read_total` with a `reason` label.
  - After the AC1 event, the `reason="event"` series increased by exactly 1.
  - After a warm build of N files, the `reason="build"` series equals N.
- [ ] **AC8: build health, wire contract and docs.** Evidence:
  - `make precommit` exits 0; it runs on linux in the container, so the status-change-time fingerprint must compile and pass there as well as on darwin.
  - `make parity` exits 0 and its summary reports no mismatch.
  - `grep -n '^## ' docs/page-index.md` lists `Staleness bounds`, `Frame ordering`, `Key derivation` and a new `Incremental updates`.
  - Each of `grep -n 'status-change'`, `grep -n 'stat-diff'`, `grep -n 'POST /api/cache/reload'` and `grep -n 'most recently started'` on `docs/page-index.md` returns ≥1 line.
  - `grep -A10 '^## Unreleased' CHANGELOG.md` shows a bullet about per-file page index updates.
- [ ] **Post-Deploy (Rung-2): AC9: idle board CPU < 5 %, or an attributed follow-up.** Run the Operator-executable CPU block ≥ 2 min after restart. Its preconditions must print: ≥1 session registry file, ≥1 established browser connection, and ≥5 indexed task files modified in the last minute. Evidence, one of:
  - (a) each of 6 consecutive 10 s windows (one rescan included) prints a CPU share < 5.0 %. The pre-fix baseline is 71–78 %.
  - (b) AC10 passes, and a vault task file exists under `~/Documents/Obsidian/private-personal/25 Tasks/`, linked to goal `[[Vault UI Ultra-Fast Reads and Writes]]`. It records the six window values and a 10 s `sample` attribution naming ≥1 package outside `pkg/pageindex` as the top residual. Check with `grep -lE 'pkg/(session|ops|board)|ps -axww|ListOperation' "<that file>"`.
  - `deploy_check:` `pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}') && python3 -c 'import os,subprocess,sys,time; s=" ".join(subprocess.check_output(["ps","-o","lstart=","-p",sys.argv[1]],text=True,env={"LC_ALL":"C","PATH":"/bin:/usr/bin"}).split()); sys.exit(0 if time.mktime(time.strptime(s,"%a %b %d %H:%M:%S %Y"))>=int(os.path.getmtime(sys.argv[2])) else 1)' "$pid" ~/Documents/workspaces/go/bin/vault-ui && [ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC10: parse cost scales with change size on the live service.** Run the Operator-executable counter and profile block. Evidence:
  - Over a 60 s window in which ≥5 indexed files were modified, `vault_ui_page_index_files_read_total` (summed over reasons) increases by ≥1 and < 500. Pre-fix, a single event re-read ~10,700.
  - A 10 s `sample` of the process contains **0** lines matching `pageStorage).ListPages`, so vault-cli's folder-wide parse is off the steady-state path.
  - `deploy_check:` same command as AC9.
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC11: the process is no longer saturated.** Run the Operator-executable latency block on the loaded board (AC9 preconditions). Evidence: `/api/vaults` p50 ≤ 5 ms (the post-restart saturated value was 0.73 s); the `/api/assignees` p50 is recorded.
  - `deploy_check:` same command as AC9.
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC12: goal gate for `/api/tasks`, with an explicit follow-up.** From the same latency block, either:
  - (a) `/api/tasks` p50 ≤ 5 × `/api/vaults` p50; or
  - (b) a vault task file exists under `~/Documents/Obsidian/private-personal/25 Tasks/`, linked to goal `[[Vault UI Ultra-Fast Reads and Writes]]`. Its body records the measured `/api/tasks`, `/api/vaults` and `/api/assignees` p50s and a `sample` attribution of the residual (expected: the per-request `ps` scan, measured 0.13–0.16 s, alone is ≥17 × the 7.5 ms budget).

  Evidence: the latency numbers, plus for (b) `grep -lE 'ps -axww|ps scan' "<that file>"` succeeds. (b) is the expected outcome; this spec does not own the `ps` scan.
  - `deploy_check:` same command as AC9.
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario. AC1–AC7 reach every behavior with fakes and real temp dirs, AC8 covers the wire contract, and AC9–AC12 measure the live service.

## Verification

### Container-executable

- `make precommit` (includes `go test -race ./...`)
- `make parity`
- `go test -race ./pkg/pageindex/... ./pkg/watchrefresh/... ./pkg/mutations/... ./pkg/factory/...`
- `grep -n '^## ' docs/page-index.md` lists `Incremental updates`
- `grep -A10 '^## Unreleased' CHANGELOG.md`

### Operator-executable (host, after merge and deploy)

Deploy:

```bash
cd ~/Documents/workspaces/vault-ui && git pull && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
sleep 120   # warm build finished, startup excluded from measurements
pid=$(launchctl list | awk '$3=="com.github.bborbe.vault-ui"{print $1}')
```

Preconditions (AC9–AC12):

```bash
ls ~/.claude/sessions/*.json | wc -l                                                # >= 1
lsof -nP -a -p "$pid" -iTCP:8000 -sTCP:ESTABLISHED | tail -n +2 | wc -l             # >= 1 browser
find "$HOME/Documents/Obsidian/private-agent/tasks" "$HOME/Documents/Obsidian/private-personal/25 Tasks" \
  -maxdepth 1 -name '*.md' -mmin -1 | wc -l                                         # >= 5
```

CPU (AC9):

```bash
cpu() { ps -o cputime= -p "$pid" | python3 -c 'import sys; p=sys.stdin.read().strip().split(":"); print(sum(float(x)*60**i for i,x in enumerate(reversed(p))))'; }
for w in 1 2 3 4 5 6; do a=$(cpu); sleep 10; b=$(cpu); python3 -c "print(f'window $w: {($b-$a)*10:.1f}%')"; done
```

Counter and profile (AC10; the profile also serves the AC9(b) attribution):

```bash
m() { curl -s http://127.0.0.1:8000/metrics | awk '/^vault_ui_page_index_files_read_total/{s+=$2} END{printf "%d\n", s}'; }
a=$(m); sleep 60; b=$(m); echo "files read in 60 s: $((b-a))"
find "$HOME/Documents/Obsidian/private-agent/tasks" "$HOME/Documents/Obsidian/private-personal/25 Tasks" \
  -maxdepth 1 -name '*.md' -mmin -1 | wc -l
sample "$pid" 10 -file /tmp/vault-ui-sample.txt >/dev/null; grep -c 'pageStorage).ListPages' /tmp/vault-ui-sample.txt   # 0
```

Latency (AC11, AC12):

```bash
for url in "/api/vaults" "/api/assignees" "/api/tasks"; do
  for i in 1 2 3; do curl -s -o /dev/null "http://127.0.0.1:8000$url"; done
  printf '%s p50: ' "$url"
  for i in $(seq 10); do curl -s -o /dev/null -w "%{time_total}\n" "http://127.0.0.1:8000$url"; done | sort -n | sed -n '5p'
done
```

## Desired Behavior

1. **Per-file event update.** A task or goal watcher event re-reads only the file named by the event's path; it neither lists the folder nor reads any other file. The file's current on-disk state decides the outcome, whatever the event type says:
   - The file is present and readable: its page replaces or inserts at its filename-ordered position.
   - The file is absent, unreadable or unparsable: it is removed. Unreadable and unparsable files are excluded, exactly as vault-cli's listing skips them.

   The result is published as a new snapshot: a copy holding the previous snapshot's untouched page pointers. Published snapshots are never mutated. The event's frame is broadcast only after a snapshot containing a read that **started after** the event has been published. An event whose path, relative to the key's folder, is not a single plain filename falls back to a stat-diff of that key (behavior 3).
2. **Snapshot equivalence.** A published snapshot always equals what vault-cli's folder listing returns for the files read: same pages, same filename order, same exclusions, identical parsed fields. The cold build at startup uses the same single-file reads, so every file has a fingerprint from the start.
3. **Fingerprints and stat-diff.** Every file read records a fingerprint (size, modification time, status-change time) taken from a stat **before** its content is read, including files that end up excluded. The stat follows symlinks, as vault-cli's read does, so a symlinked page's fingerprint tracks its target. A stat-diff of a key works as follows:
   - It lists the folder's entries with their current fingerprints.
   - It re-reads only entries whose fingerprint differs from the recorded one or that are new. "Differs" means inequality, not "newer", so a restored older timestamp counts.
   - It drops entries that vanished.
   - It publishes a new snapshot only when something changed.

   The background rescan performs a stat-diff of every key once per `RescanInterval` (unchanged, 50 s) and sends no frames.
4. **Write marks: per-file only when the file is known exactly.** The mutation service marks the index at the 11 sites that mark today:

   | Sites | Writes | Mark |
   |---|---|---|
   | The 3 queued post-write callbacks (task publishing, goal publishing, task session silent), applied by the queue consumer before any `Publish*Updated` frame | One item's frontmatter | **Per-file**, in the item's own key (tasks key for task writes, goals key for goal writes), when the exactness rule below holds. Otherwise a folder-level mark of that key |
   | The 8 synchronous sites: `RunTask`, `TakeOverTask`, `ExecuteTaskCommand` (deferred and pre-publish marks), `RunGoal`, `TakeOverGoal`, `ExecuteGoalCommand` (deferred and pre-publish marks) | vault-cli work-on, complete and defer operations, which may write beyond the named item | **Folder-level** marks of the vault's tasks and goals keys, as today |

   Exactness rule for a per-file mark: take the id with `[[`/`]]` stripped, as vault-cli's lookup does. It must contain no path separator and no `..`, the key's current snapshot must hold a page whose name equals it byte-for-byte, and `<folder>/<id>.md` must exist at mark time. Otherwise vault-cli may have written a different file through its case-insensitive substring fallback lookup, or a case-different name on a case-insensitive filesystem, so the write gets a folder-level mark.

   A folder-level mark is resolved by a stat-diff, never a full re-read. The first read after any mark waits for the re-read of the marked files (or the stat-diff), and every concurrent reader waits on that same work. Read-your-writes is unchanged.
5. **Reload is the forced full re-read.** `POST /api/cache/reload` makes the next read of every key re-read every file, whatever the fingerprints say. It is the only path that ignores fingerprints.
6. **Per-file ordering under concurrency.**
   - A file's page in a published snapshot always comes from the most recently **started** read of that file.
   - Concurrent reads of different files of the same key never drop each other's results.
   - Event-driven and rescan-driven reads never block `ListPages` when a snapshot exists.
   - A key with a pending write mark blocks its readers until the marked files are re-read (behavior 4).
7. **Observability.** A Prometheus counter `vault_ui_page_index_files_read_total{reason}` counts single-file reads, with `reason` ∈ `build`, `event`, `write`, `rescan`, `reload`. Exclusions are still logged as vault-cli does: a per-file warning, logged once per fingerprint change, so an unchanged broken file is not re-logged every rescan.

## Constraints

- **The index keeps the `storage.PageStorage` read contract.** It still implements vault-cli's `storage.PageStorage` (`ListPages`) and stays the only list data source behind `ops.NewListOperation`. It no longer *depends* on a folder-wide `storage.PageStorage` for its own builds: those go through the new single-file reader and directory lister seams. The filter, sort and blocked-state logic stays in vault-cli. vault-cli stays at v0.159.0.
- **Single-file read composition (frozen choice for this spec).** vault-cli exports no single-page reader: `readPageFromPath` is unexported, and spec 024 deferred single-file reads for that reason. vault-cli v0.159.0 does export the parser (`storage.ParseFrontmatterMap`) and the constructor (`domain.NewPage`). vault-ui composes those two and mirrors only the thin glue around them from `readEntityComponentsFromPath`:
  - the symlink-outside-vault exclusion;
  - the read followed by stat (following symlinks) for the modification date (UTC);
  - the name as the filename without `.md`;
  - the file path joined from the vault path, the folder and the filename.

  AC3's equivalence test against the real `ListPages` is the guard. It must fail on any vault-cli bump that changes that glue.
- Snapshot order equals `os.ReadDir` order (filename ascending) so responses stay byte-identical.
- Published snapshots and the `*domain.Page` values in them are never mutated. `go test -race` stays clean.
- The status-change-time fingerprint reads platform stat fields. It must build and pass on linux (container) and darwin (deploy).
- Frame ordering, staleness bounds, key derivation (`NewKey` plus `filepath.Clean`) and the read-your-writes guarantees of `docs/page-index.md` hold. The 60 s ceiling now holds for every change to a file's presence, size, modification time or status-change time.
- `pkg/mutations` reads and `pkg/cleanup` keep reading disk directly. Mutation write semantics, queue ordering and frames are unchanged; only the mark kind changes, per behavior 4.
- Response bodies, status codes, routes, query parameters, WebSocket frame content and the watched directory set are frozen (spec 023 parity contract).
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, `Create*` factories without business logic, counterfeiter mocks for the new reader and lister seams, Ginkgo/Gomega tests, `libtime.CurrentDateTimeGetter` for the rescan clock, ≥80 % coverage on changed packages.
- `docs/page-index.md` is updated:
  - a new `Incremental updates` section covering per-file events, fingerprints and stat-diff, the write-mark exactness rule, and the forced reload;
  - `Staleness bounds` rewritten for the stat-diff rescan, per-file write marks and the forced `POST /api/cache/reload`;
  - `Frame ordering` gets its burst/collapse bullet ("one in-flight rebuild plus one follow-up") rewritten for per-file updates: per-file reads, with the most recently started read winning per file.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---|---|---|---|
| Watcher misses an event (fsnotify overflow, editor atomic rename) | Snapshot stale for that file until the next event or stat-diff | Rescan within ≤ 60 s re-reads the file, whose fingerprint changed | AC4. The value appears late on the board |
| Rewrite preserves size and modification time (`touch -r`, `rsync -t`) and its event is missed | The status-change time differs, so the stat-diff re-reads it | Rescan ≤ 60 s. `POST /api/cache/reload` forces a full re-read | AC4(c) |
| Write id resolved by vault-cli's substring or case-insensitive fallback | Exactness rule fails, so the write gets a folder-level mark and the stat-diff finds the file vault-cli actually wrote | None needed | AC5(iv) |
| File read mid-write: empty or partial, parse fails | File excluded from that snapshot, exactly as vault-cli's listing would. The pre-read fingerprint is recorded | The writer's own event re-reads it. Otherwise the completed write changes the fingerprint and the rescan re-reads it | One warning log for that file |
| Folder listing fails during stat-diff (TCC denial, unmounted) | Previous snapshot keeps serving; error logged with the key | Next rescan or event retries. TCC fix per runbook "Vault UI - Task List 500 (TCC)" | ERROR log with key |
| Folder deleted and recreated | Stat-diff sees every file as vanished, then new: the snapshot empties and refills | None needed | — |
| Burst of events (git pull touches 500 files) | 500 single-file reads, each spliced copy-on-write (O(folder size) pointer copy per splice). Coalescing several pending files into one published snapshot is allowed if behavior 1's frame rule and behavior 6 hold; agent decides at impl time | None needed | Counter `reason="event"` jumps by ~500 |
| Two handlers for the same file run concurrently (debouncer restarts while one is blocked) | The later-started read wins (behavior 6) | None needed | AC6 |
| vault-cli bump changes the read glue (new exclusion rule, new metadata field) | AC3 fails at build time, before deploy | Mirror the change, or adopt an upstream single-page reader | `make precommit` red |
| Clock skew or a restored backup with older timestamps | Fingerprint comparison is inequality, so the changed file is re-read | None needed | AC4 |
| Process restart | Cold build reads every file once (~21k) | None. Bolt snapshot is a separate task | Counter `reason="build"` |
| Memory | Fingerprints add a few dozen bytes per file; snapshots are unchanged in size | Accepted | — |

## Security / Abuse Cases

- Event paths come from fsnotify inside watched directories. The single-file reader reads only a file directly inside the key's folder. An event path that, relative to the key's folder, contains a separator or `..` is not read, and that key falls back to a stat-diff.
- Write marks take the item id from the request. `requireSafeID` only rejects a leading `-`, so the index applies its own exactness rule (behavior 4): an id with a separator, `..`, or no byte-exact snapshot match is never turned into a file path, and that write gets a folder-level mark.
- The symlink-outside-vault exclusion is preserved (AC3 fixture), so a symlink in a vault folder cannot pull an outside file into a response.
- Nothing new hangs: each single-file read and each folder listing runs under the existing per-call `rebuildTimeout`.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Single-file reader and directory lister seams composed from vault-cli's exported parser and constructor, with symlink-following fingerprints (size, mtime, status-change time) for linux and darwin; the cold build moves onto them; equivalence test against the real `ListPages` | 2, 3 (fingerprint part) | AC3 | — |
| 2 | Per-file update in `pkg/pageindex`: copy-on-write splice in filename order, current-state-decides, newest-started-read-wins, a frame-safe completion signal; `files_read_total` counter | 1 (index part), 6, 7 | AC2, AC6(a–c), AC7 | prompt 1 |
| 3 | Stat-diff: rescan becomes stat-based with once-per-fingerprint warnings, folder-level dirty marks resolve by stat-diff, per-file write marks that block readers, `POST /api/cache/reload` forces a full re-read | 3, 4 (index part), 5 | AC4, AC5(iii), AC6(d) | prompt 2 |
| 4 | Wiring and docs: `pkg/watchrefresh` hands each event's file to the per-file update with frame-after-swap kept; `pkg/mutations` applies the behavior-4 table and exactness rule at all 11 sites; `docs/page-index.md`, CHANGELOG, parity run | 1 (wiring), 4 (sites) | AC1, AC5(i)(ii)(iv), AC8 | prompts 2, 3 |

Rationale: prompt 1 builds the read primitive that every later step relies on, and its equivalence test catches drift early. Prompts 2 and 3 change only the index and are testable in isolation. Prompt 4 swaps the callers over once the index supports per-file work and marks. AC9–AC12 run on the host after merge. This spec has 7 behaviors and 12 ACs (above the 50 surface guideline), so it stays one spec only because of this decomposition.

## Do-Nothing Option

The board keeps burning 71–78 % of a core while idle, and that cost grows linearly with the agent vault, which only grows. Under saturation even trivial endpoints degrade (`/api/vaults` 0.73 s after a restart), and the laptop pays in heat and battery. The per-vault queue and lazy pane work (specs 025 and 026) cannot show their latency benefit while the process is saturated.
