---
status: generating
approved: "2026-10-14T21:34:37Z"
generating: "2026-10-14T21:34:37Z"
branch: dark-factory/bug-idle-board-spawns-ps
---

## Summary

- An idle board — no requests at all — still spawns `ps -axww -o args=` once per 60 s, forever, because `pkg/sessionsnapshot`'s refresh loop runs on a fixed timer with no demand gate.
- Every consumer of that snapshot is request-path: nothing reads the resume-id set while the board is idle, so the spawn rebuilds a value nobody has asked for.
- The decision this spec makes: **gate the refresh on demand.** The 60 s ceiling stays; the spawn happens only when the snapshot has actually been read since the previous refresh.
- Dropping the timer and refreshing on first read is rejected — it would move the `ps` spawn back onto the request path, which is the exact cost spec 025 and the vault goal exist to remove.
- End state: an idle board spawns **0** child processes over a measured ≥5 min window, and the resume-id endpoint still answers correctly as soon as the board is read.

Linked vault task (traceability): `[[Vault UI Board Spawns ps Every Minute While Idle]]` (Personal vault), goal `[[Vault UI Ultra-Fast Reads and Writes]]`.

## Problem

Spec 025's AC10 children clause — *"spawns exactly 0 child processes"* — was true at its release (v0.82.0). The timer-driven path landed **after** it, in `fe86ca2`, first released **v0.87.0**, so the clause expired without anything having regressed it; the operator ruled 025 `completed` and immutable, and this residual needs its own home.

Today the board spawns `ps -axww -o args=` roughly every **60–121 s** with zero requests in flight. `pkg/sessionsnapshot/refresh.go:20-31` runs `RefreshOnce` once and then once per `SessionRefreshInterval` (60 s, `snapshot.go:38`) with no gate; `RefreshOnce` calls the injected scanner unconditionally (`snapshot.go:298-309`); the scanner is wired at `pkg/factory/api.go:195` and the loop is in main's run group (`main.go:109`).

The magnitude is small — ~1 spawn per 60–121 s against the pre-025 baseline of 1146 per 10 s — but it is the vault goal's own design rule, still open: *"No tight polling loops … background work is event-driven (file watcher, session events) or computed on demand … A slow safety-net rescan (minutes, not seconds) is the only allowed timer."* A 60 s timer that spawns a process to rebuild a set nobody asked for is neither event-driven nor on-demand.

The set exists to answer *"is this session still live?"* — but only for a caller that is asking. Every consumer is request-path (see `## Reproduction` § Consumer read), so while the board is idle the refresh is pure waste.

## Goal

The board's session snapshot is refreshed only when something has read it. An idle board — one serving no requests — spawns no `ps` and does no refresh work; a board being read keeps the snapshot current at the same 60 s cadence it has today. The resume-id endpoint answers with a live session's id as soon as the board is read, and the liveness classification contract is unchanged.

## Non-goals

- **Moving the `ps` scan onto the request path.** Rejected alternative, named here so it is not silently chosen: refreshing on first read would make the first request after idle pay a process spawn, which is the per-request cost spec 025 removed.
- **Removing the `ps` signal from liveness classification.** The resume-id cross-check stays a signal; this spec changes only when it is recomputed. `docs/liveness-classification.md` — the four outcomes, the fixed signal order, the five-minute window — is a frozen contract.
- Changing `SessionRefreshInterval` (60 s). The interval is not the defect; the unconditional refresh is.
- Changing the attention-store read (`pkg/sessionstate`'s poll), the page-index rescan timer, or the 1 s rescan poll.
- Changing any HTTP response shape, any WebSocket frame, the write path, or the frontend.
- Amending spec 025 — it is `completed` and immutable on the operator's ruling (attention card `9a28dc95189ac3945db8467358c8bca6`). Its `## Verification Result` block keeps the observation; the work happens here.

## Acceptance Criteria

- [ ] **Post-Deploy (Rung-2):** **AC1 — an idle board spawns no `ps` child.** With the board driven by **zero** requests for a ≥5 min window, sampling the board's children at ≥2 Hz throughout, the probe records **0** `ps -axww -o args=` children — evidence: the probe's own child-scan output, pasted raw, with a zero count. The operator driving no requests is the primary evidence; the counter below is corroboration. The same probe reads `sum(vault_ui_board_task_snapshot_rebuilds_total)` (labels `kind="full"`, `"patch"`, `"refresh"`) at both ends of the window and prints the delta, which must be **0** — this counter advances when a task-list read reaches the snapshot store. It is a *lower bound*: a warm read that rebuilds nothing does not move it, and a page-index change moves it without any read. So a non-zero delta voids the window and the probe is re-run, while a zero delta is corroboration and not proof — the operator's own no-requests window is what makes the window demand-free. The window starts no earlier than 5 s after the process starts, so the startup refresh is outside it.
  - `deploy_check:` `cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch -q && test -x /Users/bborbe/Documents/workspaces/go/bin/vault-ui && test /Users/bborbe/Documents/workspaces/go/bin/vault-ui -nt .git/ORIG_HEAD && git merge-base --is-ancestor origin/master HEAD && git rev-parse --short origin/master`
  - `deploy_target:` `$(cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2):** **AC2 — positive control: the same probe sees the child on the pre-fix binary.** The identical probe, run unchanged against a **pre-fix** binary (built from the commit before this change), records **≥1** `ps -axww -o args=` child in a ≥5 min window — evidence: the same command's raw output from the pre-fix run, showing a non-zero count, proving the probe can see the child it claims is absent. Both binaries run in the same session on the same host, each with its own listen and metrics ports, so the two windows are comparable. This is a control for the probe's sensitivity, not an assertion about the deployed post-fix system; its `deploy_check` is AC1's and asserts the post-fix deployment is still in place when the control runs, while its own precondition — that the pre-fix artifact exists — is the operator's to confirm.
  - `deploy_check:` `cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch -q && test -x /Users/bborbe/Documents/workspaces/go/bin/vault-ui && test /Users/bborbe/Documents/workspaces/go/bin/vault-ui -nt .git/ORIG_HEAD && git merge-base --is-ancestor origin/master HEAD && git rev-parse --short origin/master`
  - `deploy_target:` `$(cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2):** **AC3 — non-idle control: the resume-id set is still correct.** With one named live Claude session present, the endpoint that reads the resume-id set (named by the consumer read below) returns **that session's id** — evidence: the endpoint URL, the session id, and the response excerpt pasted. The set is not exposed as a field: it is an input to classification, so the observable is a row classified `live` by the resume signal — a `GET /api/tasks?vault=private-personal&session_live=true` whose body contains the task row for that session, with its `session_state` reading `live`, for a session whose transcript is older than the five-minute window (so the live verdict can only have come from the resume cross-check). The zero AC1 buys must not be bought with a stale or empty set.
  - `deploy_check:` `cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch -q && test -x /Users/bborbe/Documents/workspaces/go/bin/vault-ui && test /Users/bborbe/Documents/workspaces/go/bin/vault-ui -nt .git/ORIG_HEAD && git merge-base --is-ancestor origin/master HEAD && git rev-parse --short origin/master`
  - `deploy_target:` `$(cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **AC4 — the refresh is lazy, and a read does not latch it on forever.** A Ginkgo test drives `Run` with an injected `Waiter` and a scanner that counts calls, in **three phases**, all asserted numerically:
  1. **No read** — release the waiter N ≥ 3 times with **zero** reads between releases: the count is **1** (the startup refresh alone).
  2. **Read once** — read the snapshot, then release the waiter once: the count is **2**.
  3. **Read stops again** — release the waiter **twice more with no further reads**: the count **stays 2**; then read once more and release again, and it becomes **3**.
  Phase 3 is load-bearing and is the reason this AC is not satisfied by a one-line "ever read" latch: a latch passes phases 1 and 2 and then keeps spawning `ps` every 60 s forever on a board that was read once and left idle — which is still an idle board by the Goal's definition, and still violates AC1 over a long-enough window. A count that never moves at all also fails, at phase 2.
  The existing assertion this replaces — `pkg/sessionsnapshot/snapshot_test.go:289`, *"refreshes once per SessionRefreshInterval (AC8)"*, which expects one scanner call per wait — must be **rewritten, not left passing**: it asserts exactly the unconditional behaviour this spec removes.
- [ ] **AC5 — the request path still spawns no `ps`.** A Ginkgo test drives the real board handler with a scanner that records every call — evidence: across a `ListTasks` request and a `ListGoals` request the recorder holds exactly **0** calls, proving the demand gate did not move the spawn onto the read path (positive control: the same recorder is non-zero after a `RefreshOnce` in the same test file).
- [ ] **AC6 — the liveness classification contract is unchanged.** The contract file is byte-identical to the pre-change tree and its cases still pass — evidence, container-runnable (this repo sets `hideGit: true`, so `git diff` is **not** available at prompt time): `grep -c 'func ClassifySessionState' pkg/session/classify.go` prints `1`; `go test ./pkg/session/... -v 2>&1` exits 0 **and** its output names all four outcomes and both precedence rules — `classifies a transcript written within the window as live`, `classifies a transcript older than the window as quiet`, `classifies a session with no transcript as indeterminate`, `lets the registry beat a stale transcript`, `reads an unknown live-id source with a stale transcript as indeterminate`. The byte-identity half is confirmed host-side in the operator rung (`git diff origin/master -- pkg/session/classify.go docs/liveness-classification.md` prints nothing), because `hideGit: true` makes that command operator-executable only.
- [ ] **AC7 — the docs describe the gated refresh.** `docs/page-index.md`'s session-derived-fields bullet no longer states the refresh runs on a fixed interval regardless of reads — evidence: `grep -c 'fixed interval of at least 60 s' docs/page-index.md` prints `0`, and the same bullet now names the demand gate, asserted as `grep -niE 'read(s)? (of|from) the (session )?snapshot|snapshot has been read|when the board is read' docs/page-index.md` returning ≥1 line (the implementer's own wording — the assertion is that the bullet names reads as the trigger, not a particular phrasing). A doc left saying "fixed interval" while the code gates on reads fails this.
- [ ] **AC8 — build health and changelog.** `make precommit` exits 0, and the fix adds a `## Unreleased` section carrying its bullet — evidence: the make target's exit status, plus `awk '/^## /{sec=$0} /idle/ && /ps/ && sec=="## Unreleased"{print sec; f=1} END{exit !f}' CHANGELOG.md` exits 0 and prints `## Unreleased`. ⚠️ This worktree's `CHANGELOG.md` currently has **no** `## Unreleased` section at all — its first heading is `## v0.97.1` — so the fix must **create** the section above it, not append to an existing one. The pattern is anchored to the section name and to both keywords (`idle`, `ps`); a looser `/ps/` matches the substring inside *"keeps"* in the released `## v0.87.1` bullet and makes the criterion pass on the pre-fix tree.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit`
- `go test -race ./pkg/sessionsnapshot/... ./pkg/board/... ./pkg/session/... ./pkg/factory/...`
- `go test ./pkg/session/... -v` — exits 0 and names the four outcomes and the two precedence rules (AC6)
- `grep -c 'fixed interval of at least 60 s' docs/page-index.md` — prints `0` (AC7)
- `awk '/^## /{sec=$0} /idle/ && /ps/ && sec=="## Unreleased"{print sec; f=1} END{exit !f}' CHANGELOG.md` — prints `## Unreleased` (AC8)

⚠️ **No `git` command belongs in this rung**: this repo's `.dark-factory.yaml` sets `hideGit: true`, so git is unavailable at prompt time. Anything needing `git diff` is operator-executable and lives in the rung below.

### Operator-executable (host, after merge + deploy)

- `git diff origin/master -- pkg/session/classify.go docs/liveness-classification.md` — prints nothing (AC6's byte-identity half)

Deploy:

```bash
cd /Users/bborbe/Documents/workspaces/vault-ui && git fetch && git merge origin/master && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
```

⚠️ **Why the deploy gate asserts ancestry and not HEAD equality.** The production clone carries local-only merge commits and `prompts/in-progress/156-158-spec-031-*`, and the operator's ruling is to merge `origin/master` in and **keep** them. After that merge the clone's `HEAD` is a local merge commit whose SHA is *never* `origin/master`'s — so a `deploy_check` printing `HEAD` against a `deploy_target` of `origin/master` would read red permanently, not merely until the merge. Both sides of the gate therefore print `origin/master`'s short SHA, and the load-bearing assertion is the non-zero exit of `git merge-base --is-ancestor origin/master HEAD`: it proves the deployed tree actually contains the merged fix. Keying freshness on `.git/ORIG_HEAD` alone would not: that file is absent on a fresh clone and a no-op pull may not rewrite it, which would make the test pass vacuously.

Idle probe (AC1) — sample the board's children at 2 Hz for 5 min, driving no requests:

```bash
demand() { curl -s http://127.0.0.1:9090/metrics | awk '/^vault_ui_board_task_snapshot_rebuilds_total/{s+=$2} END{print s+0}'; }
pid=$(pgrep -f 'workspaces/go/bin/vault-ui' | head -1)
start=$(demand)
end=$(( $(date +%s) + 300 ))
hits=0
while [ "$(date +%s)" -lt "$end" ]; do
  pgrep -P "$pid" -f '^ps -axww -o args=' >/dev/null && hits=$((hits+1))
  sleep 0.5
done
stop=$(demand)
echo "ps children sampled: $hits"; echo "board reads in window: $((stop - start))"
```

The `pgrep` pattern is pinned and anchored (`^ps -axww -o args=`) because it carries AC1's primary evidence — an unanchored `ps -axww` would also match an unrelated child whose arguments merely contain that string. The metrics port (`:9090`, `main.go`'s `adminListen`) and the counter's label set are the implementer's to confirm against the served metrics; the AC's observable is the two printed numbers, both `0`. Mirror the windowed-sampling shape already documented in `docs/task-list-rebuild-cost.md` rather than inventing a new probe. Sanity-check the pattern before the real window by confirming it matches nothing on the pre-fix board at an instant when no tick is in flight, and matches during one.

Positive control (AC2) — run the identical probe against the pre-fix binary, started on its own listen and metrics ports; its `ps children sampled` must be ≥1.

Non-idle control (AC3) — with one named live session, the endpoint the consumer read names returns that session's id.

## Desired Behavior

1. **A tick with no read since the previous refresh does no work.** The refresh loop keeps its 60 s cadence ceiling, but a tick that finds the snapshot unread since the last refresh performs no `ps` scan and publishes nothing. An idle board therefore spawns no child process, indefinitely.
2. **A read re-arms exactly one refresh.** When the snapshot has been read since the previous refresh, the next tick refreshes once — and only once. The read does not latch the refresher on: if reads stop again, the tick after the refresh does nothing, so a board read once and then left idle returns to zero spawns rather than spawning forever.
3. **No request path spawns a process.** Reading the snapshot from a board handler stays a pure in-memory read. The `ps` scan runs on the refresh loop only, never in a handler — the property spec 025 bought and this change must not spend.
4. **The liveness classification contract is untouched.** The four outcomes, the fixed signal order and the five-minute window are unchanged, and the resume-id set remains a signal — this change moves only *when* it is recomputed, not what it means.
5. **The docs state the gated bound.** `docs/page-index.md`'s session-derived-fields bullet describes the refresh as read-gated rather than as a fixed interval that runs regardless, so the documented staleness bound matches the code.

## Reproduction

**Environment:**

- **dark-factory:** v0.198.7
- **Deploy source (where the probe runs):** `~/Documents/workspaces/vault-ui` — the production clone, read-only except for the deploy step. ⚠️ It is **not** clean against `origin/master`: at the time of writing it is 6 ahead / 4 behind (`HEAD=7a93145`, `origin/master=f105f45`), because it carries local-only merge commits and `prompts/in-progress/156-158-spec-031-*` while lacking the four commits that completed spec 025 (`63ebe09`, `e8555f2`, `529fc2c`, `f105f45`). The operator's ruling is to merge `origin/master` into it at deploy time and **keep** the local spec-031 files — not to reset. Because that leaves a local merge commit as `HEAD`, the deploy gate keys on `git merge-base --is-ancestor origin/master HEAD` and prints `origin/master` on both sides rather than comparing `HEAD` to `origin/master`; see `## Verification` for why.
- **Spec project root (where the spec was authored):** the feature worktree `~/Documents/workspaces/vault-ui-session-idle` on `feat/session-snapshot-idle`, cut from `origin/master` (`f105f45`).
- Deployed via `make build` + `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui`.

**Steps:**

```bash
pid=$(pgrep -f 'workspaces/go/bin/vault-ui' | head -1)
for i in $(seq 40); do
  pgrep -P "$pid" -f 'ps -axww' >/dev/null && echo "$(date +%T) ps child"
  sleep 10
done
```

**Observed:** a `ps -axww -o args=` child roughly every 60–121 s with zero requests in flight, indefinitely — the 60 s `SessionRefreshInterval` plus scheduling jitter. Recorded in spec 025's `## Verification Result` block: *"The only child the process spawns while idle is the session-snapshot `ps -axww -o args=` (pkg/sessionsnapshot, added after this spec in v0.87.0), once per 60 s."*

**Consumer read (decides drop-timer vs gate-timer).** Every consumer of the snapshot is request-path; nothing reads it while the board is idle:

| Consumer | Site | Reached by |
|---|---|---|
| `ResumeSessionIDs` / `RegistrySessionIDs` | `pkg/board/tasks.go:235,303` | a `GET /api/tasks` request |
| `ResumeSessionIDs` / `RegistrySessionIDs` | `pkg/board/goals.go:42` | a `GET /api/goals` request |
| `RegistryLocation` | `pkg/board/tasks.go:263,326`, `pkg/board/goals.go:172` | a list request |
| `Generation` | `pkg/board/snapshot.go:215` | `taskSnapshotStore.List`, itself a list request |
| `TranscriptMtime` | the board's task and goal builds | a list request |

No background loop, timer, WebSocket broadcast or cleanup sweep reads it. With zero requests, the refresh maintains a value no caller has asked for.

## Expected vs Actual

**Expected** — per the vault goal `[[Vault UI Ultra-Fast Reads and Writes]]`: *"No tight polling loops … background work is event-driven (file watcher, session events) or computed on demand … A slow safety-net rescan (minutes, not seconds) is the only allowed timer."* And per spec 025's AC10 children clause as the operator restated it: an idle board spawns **0** child processes.

**Actual** — the board spawns `ps -axww -o args=` every 60–121 s while idle. The refresh is on a fixed timer with no gate (`pkg/sessionsnapshot/refresh.go:20-31`), and nothing reads the snapshot while idle, so every one of those spawns rebuilds a value no caller wants.

## Why this is a bug

Two sources of truth, and the code contradicts both.

1. **The goal's own design rule.** A timer that spawns a process on a fixed sub-minute interval, with no consumer, is precisely the shape the goal names as a design defect rather than a tuning problem.
2. **Spec 025's AC10 children clause, as the operator restated it on 2026-10-14.** That verification records the clause met in every one of its 67 windows — *"0 children, against 1146/10 s before this spec"* — and the same block records that the only remaining idle child is this `ps`. The clause is therefore true today **only because those windows did not span a 60 s tick**; over a ≥5 min window it is false. The restated clause names no window length, so the ≥5 min window this spec measures is what makes the clause's own claim honest.

The mechanism is not a regression of 025 — `fe86ca2` landed after it — which is why this is a new spec rather than an amendment.

## The decision

**Gate the refresh on demand: keep `SessionRefreshInterval` as the cadence ceiling, and refresh only when the snapshot has been read since the previous refresh.**

Alternatives, and why they lose:

| Option | Verdict |
|---|---|
| **Gate the refresh on demand** (this spec) | Chosen. Reaches 0 idle spawns, keeps the `ps` scan off the request path, and keeps the 60 s cadence while the board is being read. |
| Drop the timer; refresh on first read | Rejected — moves the `ps` spawn onto the request path, the exact per-request cost spec 025 removed and the goal forbids. It would also make the first request after idle the slowest one. |
| Delete the resume-id signal entirely | Rejected — it is one of three liveness signals in a frozen contract (`docs/liveness-classification.md`); removing it changes classification. |
| Lengthen the interval to minutes | Rejected — the goal permits a slow safety-net rescan, but a 5-minute timer still spawns while idle, so AC1 stays red. |

The chosen option's cost, stated plainly: after an idle period the resume-id set is at most one interval stale when reads resume, because the first read records demand and the next tick refreshes. The authoritative live signal is the attention store (`RegistrySessionIDs`, read from the store the session-state watcher already keeps current), so the staleness lands only on the `ps` cross-check — the signal consulted last, for sessions the store does not list and whose transcript is older than the five-minute window.

## Constraints

- `docs/liveness-classification.md` is a frozen contract: the four outcomes, the signal order and the five-minute window do not change. This spec moves only **when** one input is recomputed.
- No HTTP response body, status code, query parameter or WebSocket frame changes.
- No new dependency, no new route, no new config knob, no new environment variable.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no `fmt.Errorf`, `Create*` factories carry no business logic, Ginkgo/Gomega tests, `glog` for logging.
- Concurrency goes through `github.com/bborbe/run`; a bare `go func()` is a violation. The refresh loop honours `ctx.Done()`.
- No test spawns a real subprocess: the scanner is injected, and the `Waiter` is injected so a test drives the interval without waiting.
- Hand-write test doubles — the repo has no `make generate` target and counterfeiter is not a module dependency, so a `//counterfeiter:generate` directive would leave the fake ungenerated and break the test build.
- **Keep the demand gate internal to the concrete `snapshot` type, so the `Snapshot` interface does not change.** `pkg/sessionsnapshot/mocks/session-snapshot.go` is a committed counterfeiter fake (`// Code generated by counterfeiter. DO NOT EDIT.`) generated from the directive at `snapshot.go:40`; with no `make generate` target and no counterfeiter in `go.mod`, an interface change would leave that fake stale and break the test build. If the gate genuinely requires a new interface method, say so and regenerate the mock with the counterfeiter CLI — never hand-edit the generated file.
- `specs/in-progress/028-precompute-task-list-snapshot.md` introduced this package's interval and is still in flight; its AC8 asserts `SessionRefreshInterval >= 60 s`, which this spec leaves true. Do not edit that spec.
- `specs/in-progress/029-rescan-allocates-only-what-changed.md` owns the page-index rescan residual. This spec does not touch it.

## Assumptions

- **The operator driving no requests is what makes AC1's window demand-free; the metrics counter only corroborates it.** The board exposes no counter that tracks HTTP requests — `pkg/metrics.go` holds only `vault_ui_build_info`, `pkg/websocket/metrics.go` only client and broadcast counters, and `grep -rn 'http_requests\|requests_total' pkg/` returns nothing — so there is no direct reading to point at. `vault_ui_board_task_snapshot_rebuilds_total` (verified live: `{kind="full"}`, `{kind="patch"}`, `{kind="refresh"}`) is the nearest proxy and is a *lower bound* on demand: a warm read that rebuilds nothing does not move it. A zero delta therefore cannot prove the window was request-free — it corroborates. AC1 is written to that: the operator's own no-requests window is the primary evidence, the counter a secondary reading.
- Every consumer of the snapshot is reached only from a request handler. The consumer read above is the evidence; if a consumer outside the request path is found during implementation, the demand gate must count that consumer's reads too, and AC4's positive control is what forces it to be found.
- `ps -axww -o args=` remains the only subprocess the board spawns while idle. The pre-025 baseline and 025's verification both support this; AC1 measures it directly.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---|---|---|---|
| The board is idle for hours, then a request arrives | The request is served from the last refresh; the next tick (≤60 s) refreshes, and the tick after that serves fresh ids | None needed | AC3's non-idle control; a session started during the idle window shows `live` within two intervals |
| The snapshot is never read at all (no browser, no poller) | Zero refreshes after the startup one; zero `ps` spawns | None needed — this is the intended state | AC1 |
| A reader appears and disappears repeatedly, one read per interval | One refresh per interval while reads continue; none in the gaps | None needed | AC4's positive control |
| The `Waiter` returns an error (ctx cancelled) | `Run` returns nil and the run group unwinds cleanly — unchanged from today | Restart the service | Existing `Run` contract; the `main.go` run group |
| A refresh fails (`ps` unavailable) | Logged with the interval and cause; the previous values stay served; the loop continues | The next tick retries | Existing `glog.Errorf` in `RefreshOnce` (`snapshot.go:300-306`) |
| The demand counter is read or written without synchronisation | A data race on the hot request path | Fix the counter's synchronisation | `go test -race ./pkg/sessionsnapshot/...` |
| A future consumer reads the snapshot outside the request path | That consumer's reads do not count as demand, so the snapshot it reads is only as fresh as the last request-driven refresh | Register that consumer's read with the demand counter | AC4's positive control, plus review of any new caller of the `Snapshot` interface |
| The attention store is unreachable | Unchanged: the last ids are kept and marked non-authoritative, so cards read `indeterminate`, never `none`/`quiet` | The store returns and the next refresh re-authoritative the set | `LiveIDsKnown()` false; existing behaviour |

## Security / Abuse Cases

None new. The change removes work rather than adding a surface: no new input, no new route, no new file read, no new process. The `ps` invocation that remains is the same fixed-argument, non-shell scan the board already runs, on the same timer ceiling. The demand counter carries no user data — it is a count of reads.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Demand gate in `pkg/sessionsnapshot`: count reads, refresh only when the count moved since the last refresh, keep the 60 s ceiling; rewrite the now-false `snapshot_test.go:289` assertion and add the three-phase test; reword `docs/page-index.md`'s session-refresh bound; add the `## Unreleased` section and its bullet to `CHANGELOG.md` | 1, 2, 4, 5 | AC4, AC5, AC6, AC7, AC8 | — |

Rationale: single-layer, single-behavior change. One prompt, one package, plus the doc bullet and the CHANGELOG entry. DB3 is not this prompt's: it is the no-spawn-on-the-request-path property, which the prompt must preserve (AC5 asserts it) rather than implement — it is spec 025's already-delivered behaviour, restated here as an invariant. The operator-executable rung (AC1–AC3) runs on the host after merge and deploy and is not a prompt.

## Do-Nothing Option

The board keeps spawning a `ps` every 60–121 s around the clock, whether or not anyone is looking at it — roughly 500 spawns a day, each rebuilding a set no caller has asked for. Spec 025's AC10 children clause stays true only inside windows shorter than the tick, so the one place the board's idle behaviour is asserted keeps passing while the assertion is false over any realistic observation window. The vault goal's design rule — background work event-driven or on demand — stays unmet for the last remaining idle timer the board owns.
