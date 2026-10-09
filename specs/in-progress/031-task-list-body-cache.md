---
status: prompted
approved: "2026-10-09T20:35:40Z"
generating: "2026-10-09T20:42:00Z"
prompted: "2026-10-09T21:05:30Z"
branch: dark-factory/task-list-body-cache
---

## Summary

- A warm `GET /api/tasks` performs no vault I/O, but it still walks the published rows and marshals a ~2.3 MB JSON body on every request.
- The task-list snapshot caches the parsed rows; it does not cache the body derived from them.
- This spec adds a body cache beside the snapshot: one encoded body and one gzipped body per (snapshot generation, query).
- The gzipped form is served when the client negotiates it, the identity form otherwise.
- The decoded bytes are unchanged — this changes how the body is served, not what it contains.

## Problem

A warm `/api/tasks` read is already free of vault I/O and process spawns, so the remaining cost is the per-request projection and marshal of the whole row set. Measured on the deployed board: bare `/api/tasks` returns 2,292,409 B at p50 7.16 ms while `/api/vaults` returns 1,897 B at p50 0.638 ms — a ratio of roughly 11×, against a bar of 5×. The snapshot caches the parse, but not the answer, so identical requests repeat the same work and the ratio cannot move while the denominator sits at its floor.

## Goal

A warm `/api/tasks` read for an already-built query writes stored bytes instead of producing them. The encoded and gzipped forms are built once per (snapshot generation, query) and served until the snapshot generation moves or the clock crosses the boundary that changes a clock-derived field.

## Non-goals

- Changing the decoded JSON — field set, order and values stay identical.
- `/api/assignees`, `/api/vaults`, `/api/goals`, `/api/topics` — these keep the existing per-request path.
- Reducing the body by trimming fields or paginating.
- Compressing any route other than `/api/tasks`.
- Cold start, snapshot persistence, or the page index's own on-disk store.
- The per-request `ps` scan (already removed) and vault-cli's per-request page walk.

## Assumptions

- A warm `/api/tasks` read already performs no vault I/O and no process spawn, so the remaining per-request cost is the projection and marshal of the published rows.
- The task-list snapshot exposes a generation that moves on every rebuild, and a rebuild is what makes new rows visible to a read.
- `upcoming_hours` is the only clock input that changes the body. The request's `now` enters the response through `upcoming`, `recently_completed` and `phaseOverride` — **and through row visibility**: `visibleRow` drops a deferred task whose `defer_date` is beyond `now + upcoming_hours`, so the body omits it entirely and must gain it back when the clock passes `defer_date - upcoming_hours`. The boundary is therefore computed over the snapshot's rows, **not** over the already-filtered responses — a row dropped for being beyond the window is precisely the row whose entry instant matters.
- The 5× ratio against `/api/vaults` is the acceptance denominator, and `/api/vaults` keeps its current cost — the ratio is not reachable by making the denominator slower.
- Clients that matter negotiate gzip; a client that does not is served the identity form and sees no behaviour change.

## Acceptance Criteria

- [ ] A second request for the same (generation, query) serves a held body without marshalling and without re-projecting — evidence: `make test` row `serves a held body without re-encoding` passes, asserting BOTH an injected counting encoder's call count is 1 AND an injected projection counter's call count is 1 across two identical requests. Counting only the encoder would leave Desired Behavior 2's "no row walk, no projection" unproven at unit level.
- [ ] Cached and uncached bodies are byte-identical, decoded, for every query in the fixture set — evidence: `make test` row `cached and uncached bodies are byte-identical` passes over a fixture of at least four queries covering the vault, status and phase filters.
- [ ] Negotiation is correct in both directions — evidence: an integration test asserts that a request carrying `Accept-Encoding: gzip` receives `Content-Encoding: gzip` and `Vary: Accept-Encoding`, and that a request carrying none receives no `Content-Encoding` and `Vary: Accept-Encoding`.
- [ ] The gzipped form decodes to the identity form — evidence: an integration test gunzips the negotiated response and compares it byte-for-byte with the identity response; the comparison is equal.
- [ ] A snapshot rebuild discards held bodies — evidence: `make test` row `rebuild discards held bodies` passes, asserting the counting encoder runs again after the key is marked dirty and read.
- [ ] Crossing the `upcoming_hours` boundary discards held bodies — evidence: `make test` row `clock boundary discards held bodies` passes, advancing an injected clock past the boundary and asserting a re-encode.
- [ ] `/api/assignees` is unaffected — negative evidence: `git diff --stat -- pkg/handler pkg/board` lists no assignees test file, and the existing assignees tests pass with no change to their assertions.
- [ ] **Post-Deploy (Rung-2):** the live board serves the gzipped form and the ratio bar holds — evidence: the deployed board answers `Accept-Encoding: gzip` with `Content-Encoding: gzip`, and the 10-sample same-window ratio probe reads ≤5× on both the bare and the 5-vault filter URL, with the filtered p50 <50 ms.
  - `deploy_check:` `curl -s -D- -o /dev/null -H 'Accept-Encoding: gzip' http://127.0.0.1:8000/api/tasks | grep -i '^content-encoding:' | tr -d '\r'`
  - `deploy_target:` `Content-Encoding: gzip`

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format, vet, lint, test all clean
- `make test` — unit and integration suites pass
- `go test ./pkg/board/... ./pkg/handler/... -run 'Cache|Body|Held'` — the new rows pass
- `grep -rn 'gzip.NewWriter' pkg/` — exactly one site, in the board layer's body-build path, and zero matches under `pkg/handler`

### Operator-executable (runs on the host after merge, spec-verification ladder)

- `make build` — writes `~/Documents/workspaces/go/bin/vault-ui`
- `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui` — restart the board (kills every session the board tracks as live; take it in a quiet window)
- the 10-sample ratio probe — both URLs, `Accept-Encoding: gzip` sent on both, `/api/vaults` sampled in the same window
- the decoded parity diff against `~/Library/Logs/vault-ui-parity-before-b4ba15959675.json`

## Desired Behavior

1. For a given task-list snapshot generation and a given query, the board builds the response body once, holds the encoded form and the gzipped form, and reuses them for every matching request.
2. A request whose (generation, query) matches a held entry is served the stored bytes: it performs no row walk, no projection and no marshal.
3. A request whose query has no held entry is built by the existing path and the result is held for reuse.
4. The gzipped form is served with `Content-Encoding: gzip` and `Vary: Accept-Encoding` when the request's `Accept-Encoding` includes gzip; otherwise the identity form is served with no `Content-Encoding`. `Vary: Accept-Encoding` is set on **every** task-list response, including the identity one — see Security / Abuse Cases for why.
5. The decoded bytes of a served body are identical to the bytes the current implementation produces for the same query at the same clock instant.
6. A held entry is discarded when the snapshot generation moves, and when the clock crosses the next instant at which the response would change — the earliest of: a row's `defer_date` (upcoming → visible); a row's `defer_date - upcoming_hours` (a currently-dropped deferred row entering the window, so the body **gains** a row); and a row's completion time plus `LookbackHours` (leaving `recently_completed`). The boundary is computed over the snapshot's rows, **not** over the already-filtered responses, because a row dropped for being beyond the window is exactly the row whose entry instant matters. A discarded entry is not served again.
7. `/api/assignees` continues to read the snapshot rows through the existing path, unaffected by the body cache.

## Constraints

- The decoded JSON is unchanged: same fields, same order, same values, same `SetEscapeHTML(false)` behaviour.
- The invalidation model this spec builds on — rebuild triggers, the atomic swap, the failed-rebuild-keeps-serving rule — is the one documented in `docs/page-index.md` § *Task-list snapshot*. This spec does not restate it and does not change it.
- No new middleware layer is introduced. `pkg/handler/router.go` keeps returning a bare router, and the negotiation lives at the handler's write seam.
- The cache holds at most one snapshot generation's entries; a superseded generation's entries are dropped rather than retained.
- The cache key includes every query parameter that changes the body, including `upcoming_hours`.
- The clock boundary is derived from the request's `now` and `upcoming_hours`, not from a fixed timer.
- `pkg/board/vaults.go` `ListAssignees` keeps reading parsed rows; the snapshot does not become bytes-only.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---|---|---|---|
| Body build panics or errors | The previous generation's entries keep serving; the failed build is logged with the key and query | The next read retries the build | Log line naming key, query and error |
| `Accept-Encoding` absent or unparseable | The identity form is served, no `Content-Encoding` | None needed — this is the defined path | Integration test asserts the header set |
| Clock crosses an `upcoming_hours` boundary mid-request | The in-flight request serves the pre-boundary body; the next request rebuilds | Automatic on the next read | Test row `clock boundary discards held bodies` |
| Snapshot generation moves during a build | The build records the generation it started from; a racing invalidation leaves the entry unheld and the next read rebuilds | Automatic on the next read | Test row `rebuild discards held bodies` |
| Unbounded query cardinality | Held entries are bounded by the queries actually requested, and dropped with their generation | Generation change frees them | Memory assertion in the build test |

## Security / Abuse Cases

- The route is read-only and unauthenticated today; this spec adds no authentication and no new input surface beyond the existing query parameters.
- `Accept-Encoding` is parsed for the single token `gzip`. No other encoding token is honoured, and no request-supplied value reaches the gzip writer.
- The server compresses and never decompresses request bodies, so a compression bomb is not reachable through this path.
- `Vary: Accept-Encoding` is required on every `/api/tasks` response, including the identity one, so a shared cache cannot serve the wrong form for a client's negotiation.

## Suggested Decomposition

Prompts are generated in this order — each row is one prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Body cache in the board layer: encode once, hold identity + gzip per (generation, query) | 1, 2, 3, 5 | 1, 2 | — |
| 2 | Invalidation: snapshot generation move and the `upcoming_hours` clock boundary | 6 | 5, 6 | prompt 1 |
| 3 | Handler serves stored bytes and negotiates `Content-Encoding` / `Vary` | 4, 7 | 3, 4, 7 | prompt 1 |
| 4 | Container-runnable gzip negotiation over the real board + real handler, plus docs and changelog | — | 4, 8 (container half) | prompts 1-3 |

Rationale: prompt 1 establishes the cache and its identity guarantee; prompt 2 adds the two invalidation axes on top of it; prompt 3 is the serving seam and can be written against prompt 1's interface; prompt 4 proves the negotiation end to end through the real board and the real handler, and owns the docs and changelog.

**AC8's operator half is not prompt scope, and that is deliberate.** A prompt runs in a read-only YOLO container with no deploy access, so the deployed board's `Content-Encoding: gzip` check and the 10-sample ratio bar stay on the Verification § *Operator-executable* rung, where AC8's `deploy_check:`/`deploy_target:` are consumed by spec-verification. The consequence is worth stating plainly: **the ratio bar — this spec's central acceptance metric — depends on that operator ladder actually being run, not on any prompt.**

## Do-Nothing Option

The ratio stays where it is: bare `/api/tasks` at roughly 11× `/api/vaults`, against a 5× bar. The board remains usable — the goal's absolute bar (`/api/tasks` warm p50 <50 ms) is met at 2.96 ms filtered — so the cost of doing nothing is a missed ratio criterion and the sibling task's SC1 staying un-ticked, which gates spec 025's AC11 and the goal hand-off. It is not a user-facing latency problem.
