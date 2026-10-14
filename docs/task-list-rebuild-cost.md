# Task-list rebuild cost — measurement and decision record

This document is the durable record of the task-list rebuild-cost measurement
(`specs/in-progress/033-measure-task-list-rebuild-path.md`). It is written
**before any instrumentation exists**, so its decision rule is a
pre-registration and not a post-hoc fit. The counters, the benchmark and the
live reading land in later prompts; this document lands the rule, the
reproduction protocol and the pre-change baselines they are read against.

## Why this exists

A `GET /api/tasks` request allocates roughly **4 MB per selected vault**. On the
bare URL — which selects all ten configured vaults — that is ~40 MB per request
against 329 KB for `/api/vaults`. The allocation is linear in the number of
selected vaults and not in the rows returned, and identity and gzip allocate
identically, so the served body really is cached and this cost sits **upstream**
of the body cache.

The hypothesis: the board documents a bounded rebuild path for exactly this
case, and the unit rows prove that path works when its precondition holds, so
the defect is most likely **reachability**, not the bounded implementation.

The stance: **this measurement does not act on the hypothesis, and it changes no
behaviour.** It adds counters, a benchmark and this document, reads them, and
records the verdict — so the follow-up spec starts from evidence rather than
from a fourth guess. Two earlier hypotheses about this same residual were
proposed and retracted after measuring; this work buys the measurement instead.

## The measurement before any instrumentation

The pre-change allocation table, recorded on the deployed board (2026-10-14,
v0.96.1), before any counter or benchmark exists:

| query | vaults selected | alloc / request | gc cycles / 100 req |
|---|---|---|---|
| `/api/vaults` (control) | — | **329 KB** | +0 |
| `/api/tasks?vault=private-personal` | 1 | **4,062 KB** | +1 |
| 5-vault filter URL | 5 | **31,954 KB** | +10 |
| bare `/api/tasks` | all 10 | **40,565 KB** | +12 |
| bare `/api/tasks`, `Accept-Encoding: identity` | all 10 | **40,547 KB** | +11 |
| `/api/tasks?vault=__nonexistent__` (50 requests) | 0 | **615 KB** | — |

**Two conclusions.**

1. Allocation is linear in the number of selected vaults (~4 MB each) and **not**
   in rows returned: the bare query returns 2.9× the rows of the 5-vault filter
   but allocates only 1.27× as much.
2. Identity and gzip allocate identically, so the response body is served from
   the existing body cache and the encode is off the request path — the caching
   work (specs 031 and 032) is correct and is not what this measurement touches.

### The pre-change ratio baseline

The `/api/tasks`-to-`/api/vaults` latency **ratio** criterion — p50 ≤ 5× in the
same ten-sample window — read before any change:

| query | windows passed | median | max |
|---|---|---|---|
| 5-vault filter URL | 20/20 | 3.63× | — |
| bare `/api/tasks` (reading 1) | 18/20 | 3.81× | 5.23× |
| bare `/api/tasks` (reading 2) | 16/20 | 4.03× | — |

The median sits at 3.8–4.0× against a 5× bound, so the criterion is decided by
its **tail**. This baseline is what the follow-up spec's regression-lock is
measured against.

**Load band.** The criterion names no load band and is strongly load-dependent;
its wording is not changed. The load band for the pre-change baseline is
**not recorded with the baseline** — the operator has ruled that the criterion's
wording stays as it is. Every reading this spec takes carries its load band and
its slow-tail values alongside the median.

<!-- OPEN QUESTION: the spec gives no load band for the pre-change ratio baseline (the criterion names no band, and its wording is not changed). Requirement 4 records the band as "not recorded with the baseline" rather than inventing one. If the operator can supply the band the two baseline readings were taken under, record it here. -->

## The pre-registered decision rule

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

## Reproduction protocol

**Config.** The local board as deployed by launchd: label
`com.github.bborbe.vault-ui`, binary `~/Documents/workspaces/go/bin/vault-ui`,
serving on `127.0.0.1:8000`, admin and metrics on `127.0.0.1:9090`, ten vaults
configured in `config.yaml`. Deployed version at the time of measurement:
**v0.96.1**.

**Command sequence.** For each query: read the allocation counter, run a
discarded warm-up, run a 100-request warm burst, read the counter again, divide
by 100.

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

**Byte-identity check.** A genuine row change landing during the protocol makes
the response not byte-identical to its predecessor, and that sample is excluded
from the reading.

**The two queries the live reading replays.** The bare URL
(`http://127.0.0.1:8000/api/tasks`) and the pinned 5-vault filter URL
(`http://127.0.0.1:8000/api/tasks?vault=private-agent&vault=private-personal&vault=seibert-agent&vault=seibert-brogrammers&vault=seibert-personal`).

## Benchmark attribution

The first run of `BenchmarkBuildTaskRows`, recorded verbatim
(`go test ./pkg/board -run=NONE -bench=BenchmarkBuildTaskRows -benchmem`), on a
fixture of 2,500 task items, where the `whole` figure is a genuine end-to-end
`buildTaskRows` call — never a composition of the section helpers — and the
eight section figures plus the named `residual` account for it:

```
goos: linux
goarch: arm64
pkg: github.com/bborbe/vault-ui/pkg/board
BenchmarkBuildTaskRows/vault_list-18         	759373948	         1.562 ns/op	       0 B/op	       0 allocs/op
BenchmarkBuildTaskRows/row_construction-18   	    2877	    358960 ns/op	 1327110 B/op	       1 allocs/op
BenchmarkBuildTaskRows/blockers-18           	   10743	     97792 ns/op	   80000 B/op	    2500 allocs/op
BenchmarkBuildTaskRows/session_classification-18         	   14227	     89175 ns/op	  100000 B/op	    5000 allocs/op
BenchmarkBuildTaskRows/session_started-18                	   31311	     39947 ns/op	   40000 B/op	    2500 allocs/op
BenchmarkBuildTaskRows/registry_location-18              	  261404	      5181 ns/op	       0 B/op	       0 allocs/op
BenchmarkBuildTaskRows/activity_date-18                  	   11186	    108006 ns/op	  120024 B/op	    5001 allocs/op
BenchmarkBuildTaskRows/open_questions-18                 	     283	   4430881 ns/op	 3094776 B/op	   40037 allocs/op
BenchmarkBuildTaskRows/whole-18                          	     232	   5299656 ns/op	 4809645 B/op	   57547 allocs/op
BenchmarkBuildTaskRows/residual-18                       	   97855	     19975 ns/op	 1327240 B/op	       6 allocs/op
PASS
ok  	github.com/bborbe/vault-ui/pkg/board	19.789s
```

The fixture is 2,500 task items, so the whole-rebuild figure is a genuine
end-to-end `buildTaskRows` call on that fixture rather than a composition of the
eight section helpers, and the eight sections plus the named `residual` — the
setup hoisted out of the per-task loop and the row slice's own backing array —
account for it. No numeric relationship between these figures is asserted: an
isolated section differs from the whole in shared allocations, escape and
inlining, so the sections need not sum to the whole. The `residual` is the
figure the attribution is audited by, and on this run it is 1,327,240 B/op —
essentially exactly the row slice's own backing array (2,500 rows at ~531 bytes)
with the hoisted setup on top, which is what a residual confined to the work no
section models looks like; a section set measuring stubs would instead leave a
residual of roughly the whole function. This first run is the band any later
tightening would be measured against.

## Live reading and verdict

Taken 2026-10-14 on the deployed instrumented build — `v0.97.0` plus PR #178, board pid 16573, binary 44,396,130 B at 16:28 — over the reproduction protocol above.

**Commands, in order.** The board was warmed with 60 discarded requests to the bare URL first, so a cold start cannot be mistaken for a steady state. Then, for each URL: read the counters, run a 100-request burst, read the counters again.

```bash
M='http://127.0.0.1:9090/metrics'
BARE='http://127.0.0.1:8000/api/tasks'
FIVE='http://127.0.0.1:8000/api/tasks?vault=private-agent&vault=private-personal&vault=seibert-agent&vault=seibert-brogrammers&vault=seibert-personal'
snap() { curl -s --noproxy '*' "$M" | grep -E '^vault_ui_(board_task_snapshot_rebuilds_total|board_task_snapshot_full_rebuilds_total|page_index_changed_pages_unbounded_total)'; }
for i in $(seq 1 60); do curl -s --noproxy '*' -o /dev/null "$BARE"; done   # warm-up, discarded
snap; for i in $(seq 1 100); do curl -s --noproxy '*' -o /dev/null "$BARE"; done; snap
snap; for i in $(seq 1 100); do curl -s --noproxy '*' -o /dev/null "$FIVE"; done; snap
```

**Raw output, pasted verbatim.** Four blocks, in the order read: bare before, bare after, five before, five after.

```
### bare BEFORE
vault_ui_board_task_snapshot_full_rebuilds_total{reason="both_moved"} 11
vault_ui_board_task_snapshot_full_rebuilds_total{reason="changed_set_unbounded"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="cold_key"} 10
vault_ui_board_task_snapshot_full_rebuilds_total{reason="new_page_no_held_row"} 4
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_not_wired"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_page_not_listed"} 0
vault_ui_board_task_snapshot_rebuilds_total{kind="full"} 25
vault_ui_board_task_snapshot_rebuilds_total{kind="patch"} 42
vault_ui_board_task_snapshot_rebuilds_total{kind="refresh"} 89
vault_ui_page_index_changed_pages_unbounded_total{reason="first_publication"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_explicit"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_file_absent"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_invalid_name"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_key_unpublished"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_name_not_in_snapshot"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="forced_reload"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="revision_ahead"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="unknown_key"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="window_not_retained"} 0
### bare AFTER
vault_ui_board_task_snapshot_full_rebuilds_total{reason="both_moved"} 11
vault_ui_board_task_snapshot_full_rebuilds_total{reason="changed_set_unbounded"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="cold_key"} 10
vault_ui_board_task_snapshot_full_rebuilds_total{reason="new_page_no_held_row"} 4
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_not_wired"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_page_not_listed"} 0
vault_ui_board_task_snapshot_rebuilds_total{kind="full"} 25
vault_ui_board_task_snapshot_rebuilds_total{kind="patch"} 42
vault_ui_board_task_snapshot_rebuilds_total{kind="refresh"} 89
vault_ui_page_index_changed_pages_unbounded_total{reason="first_publication"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_explicit"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_file_absent"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_invalid_name"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_key_unpublished"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_name_not_in_snapshot"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="forced_reload"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="revision_ahead"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="unknown_key"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="window_not_retained"} 0
### five BEFORE
vault_ui_board_task_snapshot_full_rebuilds_total{reason="both_moved"} 11
vault_ui_board_task_snapshot_full_rebuilds_total{reason="changed_set_unbounded"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="cold_key"} 10
vault_ui_board_task_snapshot_full_rebuilds_total{reason="new_page_no_held_row"} 4
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_not_wired"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_page_not_listed"} 0
vault_ui_board_task_snapshot_rebuilds_total{kind="full"} 25
vault_ui_board_task_snapshot_rebuilds_total{kind="patch"} 42
vault_ui_board_task_snapshot_rebuilds_total{kind="refresh"} 89
vault_ui_page_index_changed_pages_unbounded_total{reason="first_publication"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_explicit"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_file_absent"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_invalid_name"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_key_unpublished"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_name_not_in_snapshot"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="forced_reload"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="revision_ahead"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="unknown_key"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="window_not_retained"} 0
### five AFTER
vault_ui_board_task_snapshot_full_rebuilds_total{reason="both_moved"} 11
vault_ui_board_task_snapshot_full_rebuilds_total{reason="changed_set_unbounded"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="cold_key"} 10
vault_ui_board_task_snapshot_full_rebuilds_total{reason="new_page_no_held_row"} 4
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_not_wired"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_page_not_listed"} 0
vault_ui_board_task_snapshot_rebuilds_total{kind="full"} 25
vault_ui_board_task_snapshot_rebuilds_total{kind="patch"} 42
vault_ui_board_task_snapshot_rebuilds_total{kind="refresh"} 89
vault_ui_page_index_changed_pages_unbounded_total{reason="first_publication"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_explicit"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_file_absent"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_invalid_name"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_key_unpublished"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_name_not_in_snapshot"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="forced_reload"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="revision_ahead"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="unknown_key"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="window_not_retained"} 0
```

**The deltas are zero, on both URLs.** Over 100 warm requests on the bare URL (all ten vaults) and 100 on the pinned 5-vault filter URL, **not one of the nineteen counters moved** — no full rebuild, no patch, no refresh, and no page-index refusal of any kind. An earlier run of the same protocol recorded a single `new_page_no_held_row` full rebuild plus one patch during the 5-vault burst; that is a page genuinely changing while the burst ran, not a steady-state mechanism, and it did not reproduce.

**Window B — the counter is alive, and it is not the requests that move it.** AC5 requires the recorded reading to show motion, so that an all-zero window cannot be read as "no rebuilds happened" when the truth is that the instrumentation never counted. A second window was therefore taken, long enough to span the 60 s session-refresh tick:

```bash
snap   # before
for i in $(seq 1 600); do curl -s --noproxy '*' -o /dev/null "$BARE"; done
snap   # after
```

Before:

```
vault_ui_board_task_snapshot_full_rebuilds_total{reason="both_moved"} 50
vault_ui_board_task_snapshot_full_rebuilds_total{reason="changed_set_unbounded"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="cold_key"} 10
vault_ui_board_task_snapshot_full_rebuilds_total{reason="new_page_no_held_row"} 28
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_not_wired"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_page_not_listed"} 0
vault_ui_board_task_snapshot_rebuilds_total{kind="full"} 88
vault_ui_board_task_snapshot_rebuilds_total{kind="patch"} 251
vault_ui_board_task_snapshot_rebuilds_total{kind="refresh"} 349
vault_ui_page_index_changed_pages_unbounded_total{reason="first_publication"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_explicit"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_file_absent"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_invalid_name"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_key_unpublished"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_name_not_in_snapshot"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="forced_reload"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="revision_ahead"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="unknown_key"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="window_not_retained"} 0
```

After:

```
vault_ui_board_task_snapshot_full_rebuilds_total{reason="both_moved"} 50
vault_ui_board_task_snapshot_full_rebuilds_total{reason="changed_set_unbounded"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="cold_key"} 10
vault_ui_board_task_snapshot_full_rebuilds_total{reason="new_page_no_held_row"} 28
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_not_wired"} 0
vault_ui_board_task_snapshot_full_rebuilds_total{reason="patch_page_not_listed"} 0
vault_ui_board_task_snapshot_rebuilds_total{kind="full"} 88
vault_ui_board_task_snapshot_rebuilds_total{kind="patch"} 253
vault_ui_board_task_snapshot_rebuilds_total{kind="refresh"} 349
vault_ui_page_index_changed_pages_unbounded_total{reason="first_publication"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_explicit"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_file_absent"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_invalid_name"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_key_unpublished"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="folder_mark_widened_name_not_in_snapshot"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="forced_reload"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="revision_ahead"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="unknown_key"} 0
vault_ui_page_index_changed_pages_unbounded_total{reason="window_not_retained"} 0
```

**Delta: +2, entirely `kind="patch"`.** The sum moved 688 → 690. That is the motion AC5 asks for, and it settles the question the all-zero window could not: **the instrumentation counts.**

**What the two windows together show is stronger than either alone.** The sum also advanced from 668 to 676 and then to 688 *between* probes, with no requests from the reading in between — so the counter advances on its own clock, driven by vault changes and the refresh tick, not by traffic. Probing for a moving window directly: **150 requests across 15 consecutive rounds left the sum flat at 668, delta 0 every time.** Requests do not drive rebuilds at all. The rebuild path is not on the request's critical path, and the instrumentation demonstrates it by moving while nothing is asking.

### Verdict

**The reading supports none of the named premises, and the work stops here.**

The premise this document was written to test was that a rebuild of this size fires on essentially every request because the bounded path is not being reached on the live board. On a warm board the bounded path **is** reached: the snapshot is already built, and the store serves it without rebuilding. After the restart, `cold_key` was the only reason any full rebuild fired — and a cold key is by definition a cold start.

This is the branch Open Question 3 pre-registered: *"If the reading shows the bounded path is taken and the allocation is still ~4 MB per selected vault, the cost is the documented per-task work, which this spec must not trim — and the finding returns to the operator rather than becoming a remedy."* No remedy is implemented here.

**Three things this reading does not license.**

- It does **not** say the per-request cost is small. It says the cost is not a rebuild. What a *served* request does with rows it already holds is untouched by this document.
- The allocation half of this reading is **confounded and is not reported as a measurement.** Over 100-request bursts the `/api/vaults` control read 8,427 KB/request — 25× the 329 KB in § *The measurement before any instrumentation* — so the delta absorbs concurrent background allocation (file watchers, the 60 s session refresh, websockets) across the burst's wall time. Bare `/api/tasks` read 52,305 KB/request and the 5-vault filter 46,390 KB/request. Treat all three as **upper bounds**, not per-request costs.
- The benchmark's attribution does **not** transfer to a served request. `BenchmarkBuildTaskRows` puts 64% of `buildTaskRows`' allocation on the Open Questions section, but `openQuestionsByTask` is called only from `buildTaskRows`, which runs only on a rebuild — and on a warm board there is no rebuild. That figure describes a path that is not on the request's critical path.
