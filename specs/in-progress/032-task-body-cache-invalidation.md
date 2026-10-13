---
status: approved
approved: "2026-10-13T22:09:00Z"
branch: dark-factory/task-body-cache-invalidation
---

## Summary

- The board caches the rendered `GET /api/tasks` body, but it throws that body away far too often: on the deployed board, 96% of discards were of a body that had not changed.
- One counter, shared by every vault, moves on every rebuild of any kind — a full rebuild, a row patch, or a session refresh — and the cache treats any move as "everything changed".
- This spec makes a discard follow the content: a vault's rows signal a change only when they actually differ from the rows last published for that vault, and a query is affected only by the vaults it reads.
- The decoded JSON is unchanged. Only *when* a held body is discarded changes.
- This spec addresses the **frequency** of discards, not the cost of one: a cold build still pays the full encode and gzip.

## Problem

The task-list body cache (spec 031, merged as `72efec78`, released v0.95.0) cut the wire body from 2,785,805 B to 212,035 B (13.1×) and moved the median ratio inside its bar, but it regressed the bare URL's tail: a warm hit costs ~3 ms while a cold rebuild costs 30–1,232 ms, and the cache is cold far more often than the data changes. Measured on the deployed board over 30 sequential requests, the cache went cold 26 times and 25 of those 26 cold bodies were byte-identical to the one before them — the body's own hash changed once in the whole run. So the cache discards a correct body roughly nine times out of ten, and the ratio criterion the change shipped to fix still fails on variance (6 of 10 windows pass, bare ratios 1.40–80.79×). The cause is that the cache's invalidation input is a single process-wide rebuild counter, not the content it renders.

## Goal

A held task-list body is discarded only when an input that can change that body has actually changed: a vault whose published task rows differ from the rows the held body was rendered from, or the clock crossing the next instant at which a clock-derived field of that body changes. A vault whose rows are re-derived to the same values discards nothing. A query filtered to a subset of vaults is unaffected by a change in a vault outside that subset. The decoded JSON served is byte-identical to today's.

## Non-goals

- Changing the decoded JSON — field set, order, values, escaping and the trailing-newline trim stay as they are.
- Reducing the body: no field trimming, no pagination, no change to the gzip level, and no change to the `Content-Encoding` / `Vary: Accept-Encoding` negotiation.
- Cutting the cost of a cold build. The encode-plus-gzip a cold build pays is unchanged; this spec reduces how often that cost is paid, not what it costs.
- Scoping the invalidation to a single vault as the whole answer. The bare query covers every configured vault, so a per-vault signal alone leaves it cold — see Constraints.
- A new timer, a new configuration key, or a change to the session refresh interval.
- A new log line, metric or counter on the body-build path. The two Post-Deploy probes and the deployed-binary freshness check are this change's observability; adding production instrumentation so an invalidation can be counted is a separate spec.
- The page index, its on-disk store, cold start, and the snapshot's own rebuild model.
- `/api/assignees`, `/api/vaults`, `/api/goals` and `/api/topics` — they keep their existing path.

## Assumptions

- The over-eager invalidation, not the payload size, is what makes the ratio fail on variance: the discarded bodies were byte-identical 25 times out of 26, so the cache was cold while the content stood still.
- The published task rows and the request clock are the whole of the body's input. The clock is handled by the existing boundary; this spec changes only the row-derived input.
- The bare query selects every configured vault; `GET /api/vaults` is the authority on the count.
- `/api/vaults` keeps its current cost — the ratio is not reachable by making the denominator slower.
- A rebuild is the only thing that makes new rows visible to a read; a cache hit performs no read that could change them.

## Reproduction

**Config.** The local board as deployed by launchd: label `com.github.bborbe.vault-ui`, binary `~/Documents/workspaces/go/bin/vault-ui`, listening on `127.0.0.1:8000`, vaults as configured in `config.yaml`. Deployed version at the time of measurement: **v0.95.0** (`72efec78`), serving 212,035 B on the wire.

**Command sequence.** Warm once, then 30 sequential requests, each timed and its decoded body hashed:

```bash
curl -s --noproxy '*' --compressed -o /dev/null 'http://127.0.0.1:8000/api/tasks'
for i in $(seq 1 30); do
  t=$(curl -s --noproxy '*' --compressed -o /tmp/vaultui-repro.json \
        -w '%{time_total}' 'http://127.0.0.1:8000/api/tasks')
  h=$(sha256sum < /tmp/vaultui-repro.json | cut -d' ' -f1)
  echo "$i $t $h"
done
```

**Observed evidence (2026-10-13).** 26 of the 30 responses were cold (30–1,232 ms against a ~3 ms warm hit) and **25 of those 26 carried a decoded body byte-identical to the immediately preceding response**; the body hash changed exactly once in the run (sample 18, `f21e703581…` → `46e093b0e4…`), and that is the only legitimate discard.

**Verified in the source** (line numbers as of the merged v0.95.0 build; they drift, so read the symbol):

1. `pkg/board/snapshot.go` `runBuild` — `s.snapshotGeneration++` (line 338) runs after **every** successful build, whatever kind it was: a full rebuild, a `patchOnly` row patch, or a `refreshOnly` session refresh.
2. `pkg/board/snapshot.go` `SnapshotGeneration` — the counter is a single field on the store, incremented regardless of which key was rebuilt. It is **global across every vault**.
3. `pkg/board/taskbody.go` `taskBodyCache.Get` (line 97) — `if gen != c.generation { c.entries = map[string]*taskBodyEntry{} }` drops **every** held body whenever that number moves.
4. `pkg/board/snapshot.go` `List` — a session-generation move alone (`refreshOnly`) is enough to trigger a rebuild.
5. `pkg/sessionsnapshot/snapshot.go` — `SessionRefreshInterval = 60 * time.Second` (line 38) and `RefreshOnce` advances the session generation on **every** tick whether or not anything changed (line 332).

The brief's cited line numbers are stale by 1 to 24 lines (`snapshotGeneration++` is at 338, not 337; `SessionRefreshInterval` at 38, not 37; the session generation advance at 332, not 308). The **mechanism** the brief describes is confirmed by the code on every point.

Point 5 is a real but **minor** contributor: it fires once a minute, so it cannot explain cold events that cluster inside 0.8 s. It is not the driver and this spec does not change its interval. The driver is a page-index revision move — ordinary vault writes, which are constant on this board (many live sessions plus obsidian-git autocommit) — combined with the fact that the counter is global and content-blind.

## Expected vs Actual

**Expected.** Per `docs/page-index.md` § *Task-list body cache*: "A snapshot rebuild moves the generation and drops the superseded generation's bodies." Read in context — the generation stands for the rows a body was rendered from — a body is dropped when the rows it renders actually change.

**Actual.** The generation is a rebuild counter that moves on every successful rebuild of any kind, for any key, across every vault. A rebuild that reproduces identical rows, or a session refresh that reclassifies nothing, drops every held body for every query over every vault. 96% of the discards observed were of a body that had not changed.

**Why this is a bug.** It contradicts the documented intent (drop the bodies whose inputs moved) and it regresses the very metric the body cache shipped to improve — the cache is cold on roughly nine of every ten warm reads, each cold read paying its most expensive work for no content change.

**Workaround.** None user-side: the discard is internal and unconditional on a rebuild, and a client cannot prevent it. The operator-side mitigation is the one the probe already uses — sample `/api/vaults` in the same window and read the median, which is why the median passes while individual windows do not.

## Acceptance Criteria

- [ ] A rebuild that reproduces identical rows keeps a held body, and a rebuild that changes a rendered row discards it — evidence: two `make test` rows in `pkg/board` pass **together**. `a rebuild that reproduces identical rows keeps the held body` drives a page-index revision move whose rebuild yields byte-identical rows and asserts the harness's counting encoder has run exactly once across the move; `a rebuild that changes a rendered row discards the held body` drives the same move with one row's status changed and asserts the encoder has run twice. Either row alone is satisfied by a no-op (never invalidate, or always invalidate); only the pair pins the gate.
- [ ] A change confined to any single rendered field of a single row discards the held body — evidence: the `make test` table row `a change to any rendered row field discards the held body` passes for every Entry, one per rendered field: `status`, `phase`, `defer_date`, `completed_date`, `priority`, `assignee`, `goals`, `blocked`, `blockers`, `session_state`, `activity_date`, `claude_session_started`, `open_questions`, `flag`. Each Entry asserts the counting encoder runs again after that one field changes.
- [ ] A change in a vault the query does not select leaves the query's held body served — evidence: `make test` row `a move in an unselected vault keeps the query's held body` passes. Two keys are wired and a query selects only the first; a rendered-row change in the second leaves the counting encoder at 1, and the identical change in the first takes it to 2.
- [ ] The bare query follows every vault it selects — evidence: `make test` row `the bare query discards its body when any vault's rows change` passes: a query with no vault filter over three keys, a rendered-row change in each key in turn taking the counting encoder to the next count. A second row `a selection change discards the bare query's body` passes: adding a key to the selection discards the held body.
- [ ] A body served from the cache is byte-identical, decoded, to what the uncached path produces for the same query at the same clock instant — evidence: the existing `make test` row `cached and uncached bodies are byte-identical` still passes with no change to its assertions, and the new row `a body served after an identical-row rebuild is byte-identical to a fresh build` passes. The new row **forces** the rebuild it tests (a revision move whose rows are unchanged) rather than waiting for one to happen, because the fix removes the spurious rebuilds that would otherwise supply it — a row that merely observed a rebuild would test nothing once the fix lands.
- [ ] The clock boundary and the failure path are unregressed — evidence: the existing `make test` rows `clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `stores nothing and evicts nothing when a build fails` and `leaves the entry unheld when the generation moves during a build` all pass with no change to their assertions.
- [ ] The number of held bodies is bounded, **and not trivially so** — evidence: `make test` row `the held-entry count never exceeds the cap` passes: after requesting the cap plus N distinct queries with no input move, the cache's held-entry count reads exactly the cap. The row must **also assert a floor**: driving two distinct query shapes — one unfiltered and one selecting a single vault — leaves **both** held, so a degenerate `cap = 1`, which satisfies `count == cap` and every other `make test` row in this spec, fails here instead of passing everything but AC9.
- [ ] **Post-Deploy (Rung-2):** an identical-row rebuild does not discard the held body — evidence: one protocol against the deployed board, wholly inside a single window, all requests to the bare `/api/tasks` URL, with the existing `POST /api/cache/reload` route as the trigger, invoked literally as `curl -s -X POST 'http://127.0.0.1:8000/api/cache/reload'`. That route calls `Index.ForceReload()` and re-reads every key **ignoring recorded fingerprints**, so it always moves the page-index revision and always rebuilds the snapshot while leaving every row identical — precisely the event under test, and deterministic rather than dependent on vault churn. It touches only keys already in the page index, so the board must be warm (any prior bare request) for the trigger to reach every configured vault. The window is ordered **≥1 response, then the reload, then ≥3 responses**, so the forced rebuild necessarily has an in-window predecessor and at least three successors. A response is **counted** when all three hold: it is byte-identical (decoded) to its immediate in-window predecessor; it is slower than **5× the fastest response in the window**; and it is slower than **50 ms**. The count must be **0**. The relative baseline is anchored on the **minimum**, never on a median or p50 taken over the same responses, and that is deliberate rather than incidental: the responses under test are exactly the ones the bug inflates, so any central statistic of them rises with the bug and hides it — at a two-response window the bar `5·(cold+warm)/2` exceeds the cold latency for every positive pair, so the count would be 0 on unfixed code by arithmetic identity rather than because the fix works, and at long windows the median itself becomes a cold latency. A minimum is a warm hit whenever a warm successor is in-window, so the bar is stable at every window size. The **50 ms absolute floor** is what keeps the min anchor from turning a warm GC or host-load spike into a false failure on correctly-fixed code: under the fix every response is byte-identical, so latency is the only filter left, and a single spike above 5× a ~3 ms minimum would otherwise fail the AC on a build that is correct. It costs no true positive — under unfixed code the reload's cold rebuilds span 30–1,232 ms and the bulk of them sit above 50 ms. Five readings that would otherwise pass on unfixed code are explicitly void: a rebuild forced **before** the window; a rebuild whose cold response lands on the **first** response (no in-window predecessor); any window shorter than the three post-reload responses this AC mandates; any baseline computed over the **counted** responses rather than their minimum; and any window that omits the absolute floor. A genuine row change during the window is out of scope rather than a failure — it makes the cold response non-byte-identical to its predecessor, so it is excluded from the count automatically. No rebuild counter is read: this spec's Non-goals forbid one on the build path, and the trigger's own contract is what establishes that the rebuild happened. **The pre-change reading of this reload window is ≥1 spurious response** — the reload forces one identical-row rebuild whose cold response is counted. The `25 spurious of 26 cold events` figure quoted in this spec's Reproduction belongs to the natural-churn protocol *without* the reload and is deliberately **not** this AC's baseline.
  - `deploy_check:` `[ "$(stat -f %m /Users/bborbe/Documents/workspaces/go/bin/vault-ui)" -ge "$(cd /Users/bborbe/Documents/workspaces/vault-ui && git log -1 --format=%ct)" ] && echo fresh`
  - `deploy_target:` `fresh`
- [ ] **Post-Deploy (Rung-2):** the ratio bar holds on both URLs across ten windows — evidence: each of ten windows samples ten requests to the bare URL, ten to the pinned 5-vault filter URL and ten to `/api/vaults` in the same window, all with `Accept-Encoding: gzip`; every window reports p50(`/api/tasks`) ≤ 5× p50(`/api/vaults`) on **both** URLs. The pre-change reading is 6 of 10 windows passing, bare ratios 1.40–80.79× and filtered 1.03–7.80×. The pinned filter URL is `vault=private-agent&vault=private-personal&vault=seibert-agent&vault=seibert-brogrammers&vault=seibert-personal`.
  - `deploy_check:` `[ "$(stat -f %m /Users/bborbe/Documents/workspaces/go/bin/vault-ui)" -ge "$(cd /Users/bborbe/Documents/workspaces/vault-ui && git log -1 --format=%ct)" ] && echo fresh`
  - `deploy_target:` `fresh`

**Scenario coverage — no new scenario.** The behaviour is reachable end to end by the board-level integration rows above (a real store, a real body cache, a real counting encoder over a real revision source), and the deployed half is covered by the two Post-Deploy probes. A scenario would add a Docker/E2E harness without reaching anything the counting-encoder rows cannot, so this spec adds none.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format, vet, lint, test all clean
- `go test ./pkg/board/... -race` — the board suite passes, including the new rows
- `go test ./pkg/handler/... -race` — the handler suite passes
- `go test ./pkg/board -ginkgo.focus='a rebuild that reproduces identical rows keeps the held body'` — the central row passes
- `go test ./pkg/board -ginkgo.focus='the held-entry count never exceeds the cap'` — the cap row passes
- `go test ./pkg/board -ginkgo.focus='a change to any rendered row field discards the held body'` — the field table passes

### Operator-executable (runs on the host after merge, spec-verification ladder)

- `make build` — writes `~/Documents/workspaces/go/bin/vault-ui`
- `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui` — ⚠️ production-touching: restarts the operator's board and kills every session the board tracks as live; take it in a quiet window
- the reproduction protocol replayed against the deployed board — expected 0 spurious cold events
- the ratio probe — ten windows, bare URL and the pinned 5-vault filter URL, `Accept-Encoding: gzip` on both, `/api/vaults` sampled in the same window — expected every window ≤ 5× on both URLs
- the served-form check — the identity and gzip forms the live board returns decode to the same bytes:
  `diff <(curl -s --noproxy '*' --compressed 'http://127.0.0.1:8000/api/tasks?vault=private-personal') <(curl -s --noproxy '*' -H 'Accept-Encoding: identity' 'http://127.0.0.1:8000/api/tasks?vault=private-personal')` is empty

## Desired Behavior

1. Each task-list snapshot key carries a **published-rows signal** that advances only when that key's published rows differ from the rows last published for that key. A full rebuild, a session refresh or a row patch that reproduces identical rows leaves the signal unchanged.
2. A held body is discarded when the published-rows signal of a vault the query selects advances, or when the clock crosses the next instant at which a clock-derived field of that body can change. It is **not** discarded by a move in a vault the query does not select.
3. The bare query selects every configured vault, so it is discarded by a genuine change in any of them and kept through an identical-row rebuild in any of them. A change to the set of configured vaults discards it.
4. A served body decodes to bytes identical to what the uncached path produces for the same query at the same clock instant.
5. The cache's bounds hold: the clock boundary discards a held body exactly as today; a failed build keeps the previous body serving and is retried on the next read; and the number of held bodies is capped, evicting the least recently used, so a client requesting many distinct queries cannot grow it without bound.
6. `/api/assignees`, `/api/vaults`, `/api/goals` and `/api/topics` keep their existing path, and the snapshot's own rebuild model — its triggers, the row patch, the session refresh, the atomic swap and the failed-rebuild-keeps-serving rule — is unchanged.

## Constraints

- The decoded JSON is unchanged: same fields, same order, same values, same escaping, same trailing-newline trim.
- **Per-vault invalidation alone is not the fix.** The bare query covers every configured vault by construction, so a signal scoped to one vault still leaves the bare body cold whenever any other vault's rows are re-derived. Desired Behavior 1 (the content gate) is what keeps the bare URL warm; Desired Behavior 2 (the query scope) is what stops a change in an unselected vault from dropping a filtered query. Neither alone is sufficient and neither is optional.
- This spec changes **when** a held body is discarded, never what it contains. It addresses the frequency of discards, not the cost of one: a cold build still pays the full encode and gzip.
- The invalidation model this builds on — rebuild triggers, the row patch, the session refresh, the atomic swap, the failed-rebuild-keeps-serving rule — is the one documented in `docs/page-index.md` § *Task-list snapshot*. This spec does not restate it and does not change it. The section this spec **updates** is `docs/page-index.md` § *Task-list body cache*, whose "When it is discarded" rule must describe the content-gated, query-scoped discard after this change.
- The session refresh (once every 60 s) is a real but minor contributor: it re-derives the session-derived fields of every key's rows, so under the content gate it discards a body only when those fields actually differ. Its interval is not changed.
- The body-cache key keeps every query parameter that changes the body, including `upcoming_hours` and `session_live`.
- No new timer, no new configuration key, and no change to the session refresh interval.
- The cap on held bodies is a fixed constant, never a configuration key. Its exact value — agent decides at impl time; it is local to the cache and reversible.
- `pkg/board/vaults.go` `ListAssignees` keeps reading parsed rows.
- `CHANGELOG.md` gains one `fix:` entry under `## Unreleased`.
- The existing `make test` rows `serves a held body without re-encoding`, `cached and uncached bodies are byte-identical`, the `keys every body-changing field` table, `gzip-compresses the identity form losslessly`, `stores nothing and evicts nothing when a build fails`, `clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `boundary is the earliest transition` and `leaves the entry unheld when the generation moves during a build` keep their assertions and must still pass.
- The rows that assert a bare counter move discards a body keep their intent, but the seam they move becomes the query's selected-vault signals rather than one process-wide counter. Where a row's name asserts a *generation* move specifically — `drops the held entries when the generation moves` and `rebuild discards held bodies` in `pkg/board/taskbody_internal_test.go`, and `advances the snapshot generation on a successful rebuild only` in `pkg/board/snapshot_internal_test.go` — it is renamed to name the signal it now moves, and the store-level content gate is asserted by a new row.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---|---|---|---|
| A rebuild reproduces identical rows | The key's signal stays put; every query over that key keeps serving its held body | None — this is the fixed path | `make test` row `a rebuild that reproduces identical rows keeps the held body` |
| A rebuild changes a rendered row | The key's signal advances; the affected queries' bodies are discarded and rebuilt on the next read | Automatic on the next read | `make test` row `a rebuild that changes a rendered row discards the held body` |
| The content comparison misses a rendered field | A stale body is served for the affected query until the next genuine change or clock boundary | Correct the comparison; the affected body is rebuilt; a process restart clears the cache | The `a change to any rendered row field discards the held body` table fails |
| A body build fails | The previous body keeps serving; the error is logged with the key and query; the next read retries | Automatic on the next read | Log line naming the key, the query and the error |
| The clock crosses an `upcoming_hours` boundary mid-request | The in-flight request serves the pre-boundary body; the next request rebuilds | Automatic on the next read | `make test` row `clock boundary discards held bodies` |
| The wall clock steps backwards across a body's boundary | The body stays held until the next read recomputes the boundary from the request's clock | Automatic on the next read | The existing boundary rows; the boundary is recomputed per read |
| A vault is added to or removed from the configuration | The bare query's selection changes; its held body is discarded and rebuilt for the new selection | Automatic on the next read | `make test` row `a selection change discards the bare query's body` |
| An unauthenticated client requests many distinct queries | The held-entry count stays at the cap; the least recently used body is evicted | Automatic on the next request | `make test` row `the held-entry count never exceeds the cap` |
| Two requests race a signal move | The build records the input it started from; a racing move leaves the entry unheld and the next read rebuilds | Automatic on the next read | `make test` row `leaves the entry unheld when the generation moves during a build` |

The path performs no external call, no network I/O and no process spawn, so external-unavailability and rate-limiting rows do not apply. There is no partial-progress state: a rebuild publishes all rows or none, and the cache holds a whole body or none.

## Security / Abuse Cases

- The route is read-only and unauthenticated. This change adds no input surface: the invalidation input is derived entirely from server-side state — the page index's per-key revision, the session snapshot and the published rows — and never from a request-supplied value. A client cannot force a body to be held past its input moving, nor force a discard beyond choosing which vaults it reads.
- What a client controls is the query, and therefore the number of distinct cache keys. Today the blanket generation drop incidentally bounds the held set — it is cleared every few requests. The per-query scoping this spec introduces **removes that incidental bound**, so the cap in Desired Behavior 5 is what replaces it. Without the cap, an unauthenticated client could hold one body per distinct query indefinitely; the cap makes that impossible. This is the one abuse case the change would otherwise introduce, and it is why the cap is in scope.
- Trust boundaries: no new one. The handler parses the query as it does today; the cache reads no request bytes beyond it.
- What can hang or retry forever: nothing new. A build is bounded by the existing snapshot build timeout; a failed build is retried by the next read and does not loop.
- `Vary: Accept-Encoding` remains on every task-list response, unchanged, so a shared cache cannot serve the wrong form.

## Suggested Decomposition

Prompts are generated in this order — each row is one prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Content gate: the store advances a key's published-rows signal only when that key's published rows differ from the rows it published before | 1 | 1, 2 | — |
| 2 | Query scope: the body cache reads the selected vaults' signals instead of one process-wide counter, and the held-entry count is capped | 2, 3, 5 | 3, 4, 7 | prompt 1 |
| 3 | Docs and CHANGELOG, plus the byte-identity and failure-path regression rows | 4, 6 | 5, 6 | prompts 1-2 |

Rationale: prompt 1 is the load-bearing content gate and must land first — prompt 2's per-query scoping is meaningless without it, because a per-query signal that still moves on an identical rebuild leaves the bare URL cold exactly as it is today. Prompt 2 rewires the body cache and adds the cap. Prompt 3 owns the durable doc update and the regression rows.

**AC8 and AC9 are the operator rung and are not prompt scope.** A prompt runs in a read-only YOLO container with no deploy access, so the 30-request spurious-rate probe and the ten-window ratio probe stay on the Verification § *Operator-executable* rung, where their `deploy_check:` / `deploy_target:` are consumed by spec-verification. The consequence is worth stating plainly: **this spec's central metric — the spurious-invalidation rate — depends on that operator ladder actually being run, not on any prompt.**

## Do-Nothing Option

Do nothing and the cache stays as shipped: a warm `/api/tasks` read is cold on roughly nine of every ten requests, each cold read costing 30–1,232 ms against a ~3 ms warm hit. The ratio criterion stays met on the median (bare 3.07×, filtered 2.85×) but passes only 6 of 10 windows, so the owning task's ratio criterion cannot be ticked and the goal hand-off it gates stays blocked. The board is not unusable — the absolute bar (warm p50 < 50 ms) is met and the decoded payload is unchanged — so the cost of doing nothing is a criterion that fails on variance and a cache that pays its most expensive work for no content change. The alternative to fixing it is removing the body cache, which returns the bare URL to its ~7.16 ms pre-change p50 and its ~11× ratio: strictly worse than either the fix or the status quo.
