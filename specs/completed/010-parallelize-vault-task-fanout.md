---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-06-20T12:49:47Z"
generating: "2026-06-20T12:50:19Z"
prompted: "2026-06-20T13:02:39Z"
verifying: "2026-06-20T13:23:28Z"
completed: "2026-06-20T15:51:06Z"
branch: dark-factory/parallelize-vault-task-fanout
---

## Summary

- The `GET /api/tasks` endpoint serially awaits one `vault-cli task list` subprocess per configured vault; warm p50 latency is 270-330 ms and feels sluggish on every board refresh.
- Replace the per-vault loop with a concurrent fan-out so the slowest single vault dominates wall-clock time instead of the sum of all vaults.
- If the concurrent fan-out alone does not bring warm p50 below 100 ms, add an in-process result cache keyed on the per-vault tasks-directory mtime; cache entries are invalidated automatically when any task file is created, modified, or deleted.
- Response shape, query parameters, and all existing filters (status, phase, assignee, goal, vault, defer/blocked visibility) remain byte-identical.
- Acceptance is verified by a curl-loop measurement against a running local server: the p50 of ten warm samples for the four-vault refresh URL is below 0.100 s.

Linked vault task (traceability): `[[Speed Up Task-Orchestrator Api Tasks Endpoint]]` (Personal vault, in_progress / execution).

## Problem

`GET /api/tasks` is the hot path for every UI refresh of the Kanban board and is measurably slow. The user has profiled a representative four-vault request (`personal + trading + family + openclaw`, status `in_progress,completed`, full phase set, two assignee filters) and observed:

- Sequential per-vault subprocess wall-clock: ~184 ms (74 + 6 + 7 + 97).
- JSON parse and Python-side filter over ~357 KB combined payload: ~50-80 ms.
- FastAPI / Pydantic response serialization: ~30-50 ms.
- Warm p50 end-to-end: 270-330 ms.

The dominant fixable cost is the sequential `for vault_name in vault_names:` loop in `src/vault_ui/api/tasks.py` that awaits one `client.list_tasks()` per vault before starting the next. Each call is an independent subprocess against an independent vault; nothing forces them to be serial. Vaults already run in parallel processes — the orchestrator just refuses to overlap them.

Until the fan-out is concurrent (and, if necessary, results are cached on a stable mtime key), every board refresh, drag-and-drop reload, and filter change pays the full sequential bill.

## Goal

After this work, a warm `GET /api/tasks` request that fans out across all four configured vaults completes with p50 latency below 100 ms while returning a result set byte-identical to the current sequential implementation.

Concretely:

- The endpoint issues all per-vault `vault-cli task list` subprocesses concurrently and awaits the gathered set, so wall-clock time tracks the slowest single vault rather than the sum.
- If concurrent fan-out alone leaves warm p50 ≥ 100 ms, the endpoint additionally consults an in-process cache keyed on each vault's tasks-directory mtime; on a cache hit no subprocess runs at all.
- The cache, when present, never serves stale data: any creation, modification, or deletion of a task file inside the vault's tasks directory bumps the mtime and invalidates the entry on the next request.
- All filters, ordering, defer-visibility rules, and blocked-task hiding behave identically to the current implementation.

## Non-goals

- No change to the `vault-cli` Go binary or its JSON output schema.
- No cold-start (first-subprocess-boot) optimisation — only warm-path latency is targeted.
- No reduction of the set of vaults queried per request (that is a UI concern owned elsewhere).
- No removal or restructuring of Pydantic response validation; if that becomes necessary it is a separate spec.
- No persistent/disk cache, no cross-process cache, no Redis, no shared memory — the optional cache is in-process only and dies with the process.
- No new public query parameter, no opt-out flag for the concurrent path, no opt-out flag for the cache. The current behaviour is the regression; there is nothing to preserve under a flag.
- No change to authentication, error envelopes, or HTTP status codes.
- No streaming / chunked response.
- No new metrics export pipeline; structured-log timing entries are allowed but no Prometheus surface is added.
- No support for filesystems that do not bump directory mtime on task-file create/delete (e.g. some NFS configurations). Local dev and prod run on APFS / ext4 where this holds; behaviour on other filesystems is out of scope.

## Desired Behavior

1. A single `GET /api/tasks` request that resolves to N vaults issues N `vault-cli task list` subprocesses concurrently and waits for all of them via a single gather point before assembling the response.
2. The end-to-end ordering of tasks in the response matches the order of the resolved `vault_names` list, exactly as the current sequential implementation produces (vault-major ordering preserved).
3. If any one vault's `list_tasks` call raises `ValueError` during client/config lookup, that vault is skipped and the remaining vaults still return — matching today's `except ValueError: continue` behaviour.
4. If any one vault's `list_tasks` call raises `RuntimeError` (vault-cli non-zero exit), the endpoint fails the whole request with HTTP 500, matching today's behaviour of propagating that error out of the loop.
5. The response body for any given input is byte-identical to the response produced by the sequential implementation for the same on-disk state and the same query string — same task list, same field values, same order.
6. **Conditional — only if concurrent fan-out alone misses the latency target.** An in-process per-vault result cache exists, is mtime-keyed (cache miss on any creation/modification/deletion of a task file in the vault's tasks directory), bounded (does not grow without bound under churn — single-slot replacement per vault is the default; LRU acceptable with prompt-noted justification), process-scoped (empty at startup, dies with the process), and on a cache hit no `vault-cli` subprocess is spawned for that vault.

## Constraints

- All vault file access continues to go through the `vault-cli` subprocess; the spec MUST NOT introduce direct file reads of task notes, frontmatter parsing in Python, or any bypass of the vault-cli boundary documented in the repo `CLAUDE.md`.
- The endpoint signature, query parameters, response model (`list[TaskResponse]`), and HTTP status codes are frozen.
- Defer-visibility windowing (`now ± 8h`), `recently_completed` flag setting, `upcoming` flag setting, and blocked-task hiding via the status cache remain in place and produce identical outputs.
- The existing test suite under `tests/` must continue to pass with no removed or weakened assertions. Tests that mock `VaultCLIClient.list_tasks` continue to work without modification to their mock setup.
- `make precommit` must exit 0 with zero new ruff or mypy findings.
- The cache, if added, is keyed on a quantity the implementation can observe without spawning a subprocess — a `stat()` of the vault's tasks directory is acceptable; anything requiring an additional `vault-cli` invocation is not.
- The cache mtime probe MUST handle a missing tasks directory without raising — a missing directory is treated as "cache miss, run the subprocess as before".
- `.dark-factory.yaml` settings (`workflow: direct`, `autoRelease: false`, `validationPrompt: docs/dod.md`) are unchanged.
- Python 3.12+, FastAPI, uv, ruff, mypy, pytest toolchain unchanged.
- No new third-party dependency is added. `asyncio.gather`, `os.stat`, and `functools.lru_cache` (or equivalent) are sufficient.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Concurrency |
|---------|-------------------|----------|-----------|-------------|
| One configured vault has no installed config (`get_vault_cli_client_for_vault` raises `ValueError`) | That vault is skipped silently; the other vaults' results are still returned | Operator fixes the vault config and reloads; behaviour matches today | Today: silent skip — no log line. Spec preserves today's behaviour; no new log required | Vaults run concurrently; one ValueError does not cancel the others — the gather call collects results and skipped vaults contribute nothing |
| One vault's `vault-cli task list` exits non-zero (RuntimeError) | The whole request fails with HTTP 500 (current behaviour preserved) | Operator inspects logs, fixes vault-cli or vault state, retries | FastAPI logs the exception with stack trace as today | The gather call surfaces the first RuntimeError; sibling subprocesses are awaited or cancelled by the asyncio runtime but the response is the 500. No partial response is returned |
| Two concurrent inbound `GET /api/tasks` requests for the same vault set | Each request triggers its own fan-out; with cache absent, subprocesses double-up; with cache present, one of them is likely to find a fresh entry from the other and skip its own subprocess | None required | Standard FastAPI access logs | Cache reads/writes use only thread-safe / asyncio-safe primitives (a plain dict under the GIL is acceptable for a single-process uvicorn worker); agent decides at impl time |
| Vault tasks directory does not exist when cache probes its mtime | Probe is treated as a cache miss (no exception escapes); the subprocess runs as in the no-cache path | Operator creates the vault or fixes config | None required | No special concurrency concern |
| Vault tasks directory mtime changes mid-request after the cache lookup but before the subprocess would have run | Cache returns the prior value for the in-flight request; the next request observes the new mtime and re-fetches | None required | None required | Spec accepts a one-request staleness window; this is a deliberate trade for the latency goal |
| A task file is created or deleted but the directory mtime is not bumped by the filesystem (defensive case: some network filesystems) | Cache may return stale results until any other observable mtime change occurs | Operator can restart the server to clear the cache | Operator notices stale data on the board | Out of scope: the local dev and prod deployment both run on an APFS/ext4-backed filesystem where directory mtime updates on entry add/remove |
| Server restarts | Cache is empty; first request after restart pays full subprocess cost; subsequent warm requests benefit from the cache | None required — by design | Standard startup log line | No concurrency concern |

## Security / Abuse Cases

This change does not introduce a new input surface, network endpoint, or shell construction. The endpoint already accepts `vault`, `status`, `phase`, `assignee`, and `goal` query parameters and forwards them to the existing `VaultCLIClient.list_tasks` which uses `asyncio.create_subprocess_exec` with an arg list (no shell). The concurrent fan-out neither alters nor expands what is passed to the subprocess.

The optional cache reads task lists into memory and indexes them by vault name plus mtime. No user-controlled string is used as a cache key in a way that could induce collision; the vault name comes from the server's config file, not the request. The cache is bounded (see Desired Behavior 6) to prevent an attacker who can cause vault directory churn from inducing unbounded memory growth.

## Acceptance Criteria

- [ ] `GET /api/tasks?vault=personal&vault=trading&vault=family&vault=openclaw&status=in_progress&status=completed&phase=todo,planning,in_progress,execution,ai_review,human_review,done&assignee=&assignee=bborbe` returns HTTP 200 against a running local server — evidence: `curl -o /dev/null -s -w "%{http_code}\n" "<URL>"` prints `200`.
- [ ] Warm p50 of ten samples for the canonical four-vault refresh URL above is below 0.100 s — evidence: the script in the Verification section below prints two numeric lines (the 5th and 6th samples after `sort -n`), and both are less than `0.100`. **Authoritative baseline = operator's local dev laptop (macOS, APFS, the four configured vaults already indexed on disk, server started via `make run` with the project's standard config), best-effort; this AC is not asserted in CI — CI runs unit / integration tests only.** The verifier records the raw ten timings as part of the verification artifact.
- [ ] The response body for the canonical four-vault refresh URL is byte-identical between the new concurrent implementation and the prior sequential implementation when both run against the same on-disk vault state — evidence: a pytest integration test captures the response body via FastAPI's `TestClient` against a deterministic mocked `VaultCLIClient` and asserts byte equality with a fixture captured from the pre-change behaviour; the test exits 0 as part of `make test`.
- [ ] At least one new pytest unit test in `tests/` proves that the per-vault `list_tasks` calls overlap in time when the endpoint handles a multi-vault request — evidence: the test installs a `VaultCLIClient.list_tasks` mock that records `(start_monotonic, end_monotonic)` per call, asserts that for two vaults the start of the second call occurs before the end of the first call, and exits 0 under `uv run pytest`.
- [ ] Every existing filter on the endpoint (status, phase, assignee, goal, vault) returns the same result set under the new implementation as it did under the old — evidence: the existing tests in `tests/test_api.py` that exercise these filters continue to pass unchanged; `uv run pytest tests/test_api.py` exits 0 with no test removed or skipped.
- [ ] The endpoint preserves vault-major ordering of results — evidence: a pytest unit test that supplies two mocked vaults each returning one distinguishable task asserts the response list orders the first vault's task before the second vault's task, matching the `vault_names` list order. Test exits 0.
- [ ] `RuntimeError` raised by a single vault's `list_tasks` still surfaces as HTTP 500 — evidence: a pytest unit test mocks one of two vaults to raise `RuntimeError` and asserts the FastAPI `TestClient` response status is 500; test exits 0.
- [ ] `ValueError` raised by `get_vault_cli_client_for_vault` for one vault still results in that vault being silently skipped while siblings return results — evidence: a pytest unit test installs a vault that raises `ValueError` at client construction and a second vault that returns one task, asserts the response is HTTP 200 with exactly that one task; test exits 0.
- [ ] If, and only if, the concurrent-only implementation does not meet the p50 < 100 ms criterion in a measured baseline run (same operator-laptop baseline as above), the cache layer described in Desired Behavior 6 is implemented. The decision is recorded in the implementation prompt's notes. — evidence: either (a) the concurrent-only measurement shows p50 < 100 ms and the cache is not added, OR (b) the cache is added and a pytest unit test asserts that mutating the vault tasks-directory mtime between two requests produces a cache miss while leaving the mtime unchanged between two requests produces a cache hit (the test observes hit/miss by counting calls to a mocked `VaultCLIClient.list_tasks`). Test exits 0.
- [ ] `make precommit` exits 0 — evidence: exit code 0; no new ruff or mypy findings.
- [ ] `CHANGELOG.md` has an entry under `## Unreleased` describing the latency improvement — evidence: `grep -A 20 "^## Unreleased" CHANGELOG.md` shows a bullet referencing the concurrent vault fan-out (and the cache, if added).

No new scenario test is added. The behaviour change is observable at the unit and integration level, and the canonical latency measurement is operator-run against a live server as part of verification — adding a scenario would require a running multi-vault fixture and offers no regression catch beyond the integration test above.

## Verification

Backend regression and unit/integration tests:

```
make precommit
```

Live-server latency measurement, run after the server is started with the standard four-vault config:

```
# Warm the path: throw away the first three samples
for i in 1 2 3; do
  curl -o /dev/null -s "http://localhost:8000/api/tasks?vault=personal&vault=trading&vault=family&vault=openclaw&status=in_progress&status=completed&phase=todo,planning,in_progress,execution,ai_review,human_review,done&assignee=&assignee=bborbe"
done

# Ten warm samples, sorted; print the 5th and 6th (rough p50)
for i in $(seq 10); do
  curl -o /dev/null -s -w "%{time_total}\n" \
    "http://localhost:8000/api/tasks?vault=personal&vault=trading&vault=family&vault=openclaw&status=in_progress&status=completed&phase=todo,planning,in_progress,execution,ai_review,human_review,done&assignee=&assignee=bborbe"
done | sort -n | tee /tmp/parallelize-p50.txt | sed -n '5,6p'
```

Both lines printed by the final `sed` must be less than `0.100`. The full ten samples in `/tmp/parallelize-p50.txt` are attached as the verification artifact.

Byte-equivalence check (manual sanity, optional but recommended):

```
curl -s "http://localhost:8000/api/tasks?vault=personal&vault=trading&vault=family&vault=openclaw&status=in_progress&status=completed" | jq -S . > /tmp/after.json
# Compare against /tmp/before.json captured from the prior commit
diff /tmp/before.json /tmp/after.json
```

Empty diff confirms response-shape preservation under live data.

## Suggested Decomposition

This spec touches at most two layers (the endpoint handler in `src/vault_ui/api/tasks.py` and, conditionally, a new cache helper module). The Desired Behaviors split cleanly into a "definitely ship" half and a "only if needed" half. Implementer should run the prompts in order and stop early if Prompt 1 alone meets the latency goal.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Replace the sequential per-vault loop in `list_tasks` with `asyncio.gather`; preserve vault-major ordering, `ValueError`-skip semantics, and `RuntimeError`-propagation semantics. Add unit + integration tests covering concurrency, ordering, filter parity, and error paths. Measure live p50. | 1-5 | All except the cache-conditional AC and the cache mtime-invalidation AC | — |
| 2 | ONLY IF Prompt 1's measured p50 is ≥ 100 ms: add the per-vault mtime-keyed in-process cache with bounded eviction; add unit tests proving hit/miss based on mtime; re-measure live p50. | 6 | Cache-conditional AC and cache mtime-invalidation AC | Prompt 1 |

Rationale: concurrency is the higher-confidence, lower-risk change and is sufficient by itself in the optimistic case (slowest vault is 97 ms, plus parse + serialisation overhead — close to the target). The cache is strictly more complex (mtime probe, invalidation, eviction) and only earns its complexity if measurement demands it. Keeping the cache decision behind a real measurement avoids speculatively shipping cache infrastructure that may turn out to be unnecessary.

## Do-Nothing Option

If nothing is done, every UI board refresh pays 270-330 ms of warm-path latency, dominated by a sequential per-vault loop that has no semantic reason to be sequential. The board feels sluggish at every interaction. The user has explicitly flagged this as a daily friction point. The cost of the concurrent fan-out is small (one `asyncio.gather`, preserved ordering via list comprehension), the upside is measurable, and the change is reversible by a single revert. Doing nothing keeps a known, profiled, fixable regression in the hot path.
