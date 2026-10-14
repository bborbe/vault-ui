---
status: completed
spec: [033-measure-task-list-rebuild-path]
summary: Created docs/task-list-rebuild-cost.md with the pre-registered decision rule copied verbatim, the reproduction protocol, the pre-change allocation table and ratio baseline, and marked placeholders for the benchmark and live-reading sections.
execution_id: vault-ui-rebuild-fix-exec-162-spec-033-rebuild-cost-document
dark-factory-version: v0.198.7
created: "2026-10-14T12:29:16Z"
queued: "2026-10-14T13:02:28Z"
started: "2026-10-14T13:22:28Z"
completed: "2026-10-14T13:28:47Z"
branch: dark-factory/measure-task-list-rebuild-path
---

# Write the task-list rebuild-cost measurement and decision record first

<summary>
- A new document records, before any instrumentation exists, what the board is about to measure and how the measurement will be read.
- The document states the decision rule up front, so the rule cannot be fitted to a reading that does not exist yet.
- It carries the reproduction protocol an operator replays: the burst helper, the allocation counter read, and the exact URLs.
- It carries the pre-change allocation table (the ~4 MB per selected vault reading) as the baseline the instrumentation must not perturb.
- It carries the pre-change latency-ratio baseline (bare URL 18/20 and 16/20, filtered URL 20/20) that the follow-up spec's regression-lock is measured against.
- Two sections are left deliberately empty and clearly marked: the benchmark attribution (filled once the benchmark lands) and the live reading and verdict (the operator rung).
- Nothing in the codebase changes: this prompt touches one new Markdown file and nothing else.
- The document names each candidate mechanism and the one-line implication each carries, so the later verdict is unambiguous.
</summary>

<objective>
Create `docs/task-list-rebuild-cost.md` — the durable record of the task-list rebuild-cost measurement. It must land the pre-registered decision rule, the reproduction protocol and the pre-change baselines **before any instrumentation code is written**, so the rule is a pre-registration and not a post-hoc fit. This is prompt 1 of 4; the counters follow in prompt 2, the benchmark in prompt 3, and the CHANGELOG and regression fence in prompt 4.
</objective>

<context>
Read the spec `specs/in-progress/033-measure-task-list-rebuild-path.md` in full before writing. This prompt covers Desired Behavior 4 and Acceptance Criterion 4. Acceptance Criteria 5 and 7 are the spec's operator rung (a deploy and a live reading) and are **not** this prompt's scope — do not write a deploy step, a `launchctl` command, or a live metric read into this prompt's document as if it had happened.

Read these files before writing (read-only; do not modify them):
- `docs/page-index.md` — § *Task-list snapshot* (the documented bounded/full rebuild model the measurement is about) and § *Task-list body cache*. The document must be consistent with them and must not restate or contradict the invalidation model.
- `pkg/board/tasks.go` — `buildTaskRows` (the per-task work the measurement attributes) and `patchTaskRows` (the two in-patch full-build fallback sites).
- `pkg/board/snapshot.go` — `List` (the refresh / patch / full-build path selection) and `runBuild`.
- `pkg/pageindex/pageindex.go` — `ChangedPagesSince` (the `ok == false` refusal), `MarkDirty`, `MarkFileDirty`, `ForceReload`, `maxChangeLog`, `pageChange`.
- `pkg/pageindex/metrics.go` — the existing `vault_ui_page_index_files_read_total{reason}` labelled-counter pattern.
- `CHANGELOG.md` — read only, to confirm the current newest heading (this prompt does not edit it).

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

Verified facts you may rely on (read from the source above; do not re-derive):
- `pkg/board/tasks.go` `buildTaskRows` per task performs: the vault-cli list walk (`b.ops.List(vault).Execute(...)`), `b.uncompletedBlockers(vault.Name, item.BlockedBy)`, `session.ClassifySessionState` (which reads `b.transcriptProbe()`), `b.sessionStarted(vault.Name, item.Name)`, `b.signals.RegistryLocation(ctx, item.ClaudeSessionID)`, `activity.ComputeActivityDateWith(...)` (a second transcript probe), and `openQuestionsFor(openQuestions, item.Name)`.
- `pkg/board/snapshot.go` `List` picks the kind top-down: a session-only move takes the refresh; a page-only move takes the patch only when `ChangedPagesSince` reports `ok`; a move of both takes the full build; a cold key takes the full build.
- `pkg/pageindex/pageindex.go` `ChangedPagesSince` returns `ok == false` for: an unknown key; a revision ahead of the key's current one; a revision the retained change window no longer covers (`len(e.changeLog) == 0 || e.changeLog[0].revision > revision+1`); and any advance in the window recorded as unbounded. It reports only `ok`, never why.
- `pkg/pageindex/pageindex.go` `MarkFileDirty` widens to a folder-level `MarkDirty` when the key is unknown or has no published snapshot, when the name is not a plain base name, when the snapshot does not already hold the name (`snapshotHasName`), or when the file is absent on disk at mark time.
- `maxChangeLog = 64`; `SessionRefreshInterval = 60 * time.Second`.
- `pkg/board/tasks.go` `patchTaskRows` returns the full build from two sites: a changed page with no held row (`case present:` in the first loop) and a changed page the snapshot holds but the list operation dropped (`itemByName[name]` missing in the second loop).
</context>

<requirements>

### 1. Create `docs/task-list-rebuild-cost.md`

Create the file with the section skeleton below. Every heading named here must appear verbatim (the `<verification>` greps match on them).

```
# Task-list rebuild cost — measurement and decision record

## Why this exists

## The measurement before any instrumentation

## The pre-registered decision rule

## Reproduction protocol

## Benchmark attribution

## Live reading and verdict
```

### 2. § *Why this exists* — the problem and the stance

State, in a few sentences and without changing any behaviour: a `GET /api/tasks` request allocates ~4 MB per selected vault; on the bare URL that is ~40 MB per request against 329 KB for `/api/vaults`; the allocation is linear in the number of selected vaults and not in rows returned; identity and gzip allocate identically, so the served body really is cached and this cost sits upstream of it. State the hypothesis in one sentence: the board documents a bounded rebuild path for exactly this case and the unit rows prove it works when its precondition holds, so the defect is most likely reachability, not the bounded implementation. State the stance explicitly: **this measurement does not act on the hypothesis, and it changes no behaviour** — it adds counters, a benchmark and this document, reads them, and records the verdict so the follow-up spec starts from evidence.

### 3. § *The measurement before any instrumentation* — the allocation table and the two conclusions

Reproduce the spec's *Observed evidence (2026-10-14, v0.96.1)* table exactly:

| query | vaults selected | alloc / request | gc cycles / 100 req |
|---|---|---|---|
| `/api/vaults` (control) | — | 329 KB | +0 |
| `/api/tasks?vault=private-personal` | 1 | 4,062 KB | +1 |
| 5-vault filter URL | 5 | 31,954 KB | +10 |
| bare `/api/tasks` | all 10 | 40,565 KB | +12 |
| bare `/api/tasks`, `Accept-Encoding: identity` | all 10 | 40,547 KB | +11 |
| `/api/tasks?vault=__nonexistent__` (50 requests) | 0 | 615 KB | — |

Below it, state the two conclusions verbatim in substance: (a) allocation is linear in the number of selected vaults (~4 MB each) and not in rows returned — the bare query returns 2.9× the rows of the 5-vault filter but allocates only 1.27× as much; (b) identity and gzip allocate identically, so the response body is served from the existing body cache and the encode is off the request path.

### 4. § *The measurement before any instrumentation* — the ratio baseline with its load band

Record the pre-change latency-ratio baseline: the `/api/tasks`-to-`/api/vaults` ratio criterion (p50 ≤ 5× in the same ten-sample window) passes on the 5-vault filter URL in **20/20** windows (median 3.63×) but only **18/20** (median 3.81×, max 5.23×) and **16/20** (median 4.03×) in two independent readings of the bare URL. State that the median sits at 3.8–4.0× against a 5× bound, so the criterion is decided by its tail.

Add a **Load band** line. The criterion names no load band and is strongly load-dependent; its wording is not changed. Record the load band for the pre-change baseline as **not recorded with the baseline** (the operator has ruled the criterion's wording stays as it is), and state that every reading this spec takes carries its load band and its slow-tail values alongside the median. Do not invent a band number.

<!-- OPEN QUESTION: the spec gives no load band for the pre-change ratio baseline (the criterion names no band, and its wording is not changed). Requirement 4 records the band as "not recorded with the baseline" rather than inventing one. If the operator can supply the band the two baseline readings were taken under, record it here. -->

### 5. § *The pre-registered decision rule* — copy the rule verbatim

Copy the rule below into the document **verbatim** (the wording is the pre-registration; do not paraphrase, reorder, or trim it). Keep the bold markers and the ⚠️ marker.

---

**The pre-registered decision rule.** Read R = (the rebuild-kind deltas, the full-rebuild-reason deltas, the page-index unbounded-reason deltas, the allocation table, the benchmark attribution) over the protocol.

**The prior this rule is weighted by — a prior, not a proof.** The measurement is **uniform**: ~4 MB on every request, across all ten vaults, over a 100-request burst. That shape is itself evidence, and it rules the two board-side premises out as *dominant* causes because both are event-shaped rather than per-request: `both_moved` needs a page move to coincide with the session-generation tick, and the generation advances once per successful refresh (≥60 s), so it can touch at most about one request per minute; `new_page_no_held_row` fires per newly created page, a few per minute across the vault, not per request. `window_not_retained` needs more than 64 revision advances **on one key between two consecutive requests**, and the board re-reads on every request, so on a burst fired in about half a second that would need >64 advances in the milliseconds between two of the 100 requests. That leaves **`folder_mark` as the primary suspect**, and its mechanism is readable from source: `MarkFileDirty` widens to a folder-level `MarkDirty` when the key's snapshot does not already hold the page name (`!snapshotHasName(e.snapshot, stripped)`), and for a newly created file that is always true — the snapshot is the last read's page list and cannot hold a file that did not exist then. A folder-level `MarkDirty` records `recordChangeLocked(nil, false)`, an explicitly unbounded change, so `ChangedPagesSince` returns `ok=false` and the store takes the full rebuild.

⚠️ **The prior's one unestablished link, named so a reader can check it.** `MarkFileDirty`'s callers are the board's write routes, and each names an item id for a file it is editing, while a file created by a live session reaches the index through the **watcher** — whose per-file path (`RefreshFile`) records a *bounded* change, and whose folder fallback (`Refresh` → stat-diff) is also bounded on an established key (`bounded = !full && e.hasSnapshot`, and `full` is set only for a cold build, a reload, or a key with no baseline). So a folder-level mark lands on the request cadence only if a write names a file the snapshot does not yet hold, or if a write route's own folder-level mark fires between requests. **Which of those carries the cadence is exactly what the reading must show**; the prior says `folder_mark` dominates, not which of its sub-causes does. This is why Desired Behavior 2 separates the folder-level causes from each other instead of reporting one `folder_mark` label.

**The rule, weighted by that prior.**

- **primary** — the page-index dominant reason is one of the `folder_mark_*` reasons ⇒ the premise **P2** — a folder-level mark stands in for a write that touched one file; the sub-cause names which code site carries it.
- **unlikely, recorded so it is not equally weighted** — `kind="full"` accounts for the rebuilds, `full_rebuilds_total{reason="changed_set_unbounded"}` dominates, and the page-index dominant reason is `window_not_retained` ⇒ the premise **P1** — the retained change window does not cover the span a read can fall behind by. Unlikely on this protocol: more than 64 advances on one key between two consecutive requests, per the prior above.
- **unlikely, recorded so it is not equally weighted** — the board's dominant reason is `both_moved` ⇒ the premise **P3** — a page move coinciding with a session-generation move is never patched. Unlikely as a dominant cause: event-shaped, at most about one request per minute.
- **unlikely, recorded so it is not equally weighted** — the board's dominant reason is `new_page_no_held_row` ⇒ the premise **P4** — a changed page with no held row forces a full rebuild from inside the patch. Unlikely as a dominant cause: event-shaped, a few per minute across the vault.
- `kind="patch"` or `kind="refresh"` accounts for the rebuilds and the allocation is still ~4 MB per selected vault ⇒ the bounded path **is** taken and the remaining cost is the documented per-task work: record that finding and stop.
- any other reading (for example `cold_key` dominating, or no rebuilds at all) ⇒ record it and stop.

**The branches are ordered and the first match wins.** They can co-occur: a folder-level mark can land in the same read window as a session tick or as a new page, and the same burst can show a `folder_mark_*` reason alongside a `both_moved` or `new_page_no_held_row` count. The ordering resolves that in favour of the primary branch — the one whose sub-cause the instrumentation exists to name — and a reader must take the first matching branch rather than averaging the branches or reporting two. The `unlikely` branches are consulted only when no `folder_mark_*` reason dominates.

This rule is the decision record, and this spec implements none of it. The one-line implication each premise carries is recorded so the verdict is unambiguous and so the follow-up spec starts from the decision rather than re-deriving it: **P1** ⇒ the window must cover the span a read can fall behind by; **P2** ⇒ the folder-level mark must be narrowed at its source — where the widening is `!snapshotHasName`, the mark stays **file-level for a file that exists on disk but is not yet in the snapshot**, one guard in `MarkFileDirty`, and where it is a write route marking the folder outright, that route marks the file it touched; **P3** ⇒ the page-derived rows must be patched and the session fields refreshed rather than rebuilt in full; **P4** ⇒ the missing row must be derived and spliced. P2 is the one whose fix is a single guard rather than a structural change, which is part of why it is the primary suspect, and the sub-cause the reading names says which guard. Which of those, if any, is the follow-up spec's scope is decided by the verdict and by the operator — not here.

---

### 6. § *Reproduction protocol* — the config, the command sequence, the parity check

Record the protocol so an operator can replay it without re-deriving it:

**Config.** The local board as deployed by launchd: label `com.github.bborbe.vault-ui`, binary `~/Documents/workspaces/go/bin/vault-ui`, serving on `127.0.0.1:8000`, admin and metrics on `127.0.0.1:9090`, ten vaults configured in `config.yaml`. Deployed version at the time of measurement: **v0.96.1**.

**Command sequence.** For each query: read the allocation counter, run a discarded warm-up, run a 100-request warm burst, read the counter again, divide by 100. Copy the spec's helper verbatim:

```bash
alloc() { curl -s --noproxy '*' 'http://127.0.0.1:9090/metrics' \
            | awk '/^go_memstats_alloc_bytes_total/{print $2}'; }
burst() {  # $1 = tasks URL, $2 = label
  curl -s --noproxy '*' -o /dev/null "$1"
  b=$(alloc)
  for i in $(seq 1 100); do curl -s --noproxy '*' -o /dev/null "$1"; done
  a=$(alloc)
  awk -v a="$a" -v b="$b" -v l="$2" 'BEGIN{printf "%-28s %6d KB/req\n", l, (a-b)/100/1024}'
}
burst 'http://127.0.0.1:8000/api/vaults'                          '/api/vaults (control)'
burst 'http://127.0.0.1:8000/api/tasks?vault=private-personal'     'tasks, 1 vault'
burst 'http://127.0.0.1:8000/api/tasks?vault=private-agent&vault=private-personal&vault=seibert-agent&vault=seibert-brogrammers&vault=seibert-personal' 'tasks, 5-vault filter'
burst 'http://127.0.0.1:8000/api/tasks'                            'tasks, bare (all 10)'
burst 'http://127.0.0.1:8000/api/tasks?vault=__nonexistent__'      'tasks, no vault selected'
```

Also record the **byte-identity check inside the protocol**: a genuine row change landing during the protocol makes the response not byte-identical to its predecessor, and that sample is excluded from the reading. Name the pinned 5-vault filter URL above as the second query the live reading replays alongside the bare URL.

### 7. § *Benchmark attribution* — leave a marked placeholder

Under the heading, leave a short placeholder line such as `_Pending: the raw -benchmem output of BenchmarkBuildTaskRows and its residual figure, recorded by the benchmark prompt._` Do **not** invent benchmark numbers, section names or a tolerance. Do not write a numeric band.

### 8. § *Live reading and verdict* — leave a marked placeholder

Under the heading, leave a short placeholder line such as `_Pending: the raw metric reads and the verdict over the reproduction protocol, recorded on the deployed instrumented build (the spec's Post-Deploy rung). Not produced by any container prompt._` State that if the reading supports none of the named premises the document says so and the work stops there, with no remedy implemented. Do **not** write a verdict.

### 9. Scope fence

This prompt writes one new Markdown file and nothing else. Do not create, edit or move any Go file, test file, `CHANGELOG.md`, `.dark-factory.yaml`, or any other document. The counters, the benchmark, the CHANGELOG entry and the regression fence belong to prompts 2–4.
</requirements>

<constraints>
- This spec changes no behaviour: it adds counters, a benchmark and a document. Do not change the invalidation, the rebuild's choice of path, the per-task work, the decoded JSON, the wire shape, the body cache, the page index's parse model or its store, or any endpoint.
- Do **not** disable, weaken or gate the invalidation; do **not** trim or weaken the per-task work; do **not** add a cache, a memo, a timer, a configuration key or a scenario.
- The decision rule must be copied verbatim from requirement 5, including the bold markers and the ⚠️ marker, and must not be paraphrased or trimmed.
- Do not record a numeric tolerance or band anywhere; the benchmark's band is established by its first run (prompt 3) and the ratio criterion's load band is recorded per reading.
- Do not fill in the benchmark section or the live-reading section.
- Do not write an operator command (a deploy, a restart, a `launchctl` line) into the document as though it had run; those belong to the spec's Verification § *Operator-executable* rung.
- Do not commit — dark-factory handles git.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
test -f docs/task-list-rebuild-cost.md && echo present
```
Must print `present`.

```
grep -n '^## ' docs/task-list-rebuild-cost.md
```
Must list exactly: `Why this exists`, `The measurement before any instrumentation`, `The pre-registered decision rule`, `Reproduction protocol`, `Benchmark attribution`, `Live reading and verdict`.

```
grep -n 'branches are ordered and the first match wins' docs/task-list-rebuild-cost.md
```
Must print at least one line (the ordered-branch rule is present verbatim).

```
grep -n '40,565 KB\|4,062 KB\|329 KB' docs/task-list-rebuild-cost.md
```
Must print at least three lines (the pre-change allocation table is present).

```
grep -n '18/20\|16/20\|20/20' docs/task-list-rebuild-cost.md
```
Must print at least three lines (the pre-change ratio baseline is present).

```
grep -n 'Load band' docs/task-list-rebuild-cost.md
```
Must print at least one line.

```
grep -n 'Pending' docs/task-list-rebuild-cost.md
```
Must print at least two lines (the benchmark and live-reading placeholders are marked).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0 (no Go code changed, but the suite must stay green).
</verification>
