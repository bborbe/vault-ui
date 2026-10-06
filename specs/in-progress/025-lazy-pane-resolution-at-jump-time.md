---
status: prompted
approved: "2026-10-06T06:06:48Z"
generating: "2026-10-06T06:48:20Z"
prompted: "2026-10-06T06:48:20Z"
branch: dark-factory/lazy-pane-resolution-at-jump-time
---

## Summary

- A card's WezTerm pane is resolved only when the operator clicks its jump link, in Go, against the live session — never on a list read.
- The 3-second background pane refresher is deleted, and with it every Go→Python shell-out (`who-needs-me.py`, `pkg/panecache`).
- `/api/tasks` stops carrying a pane id, so the list response no longer depends on pane data at all.
- The Live badge and the jump button's availability come from session state kept current by file events on `~/.claude/sessions/*.json` — no timer.
- The Go↔Python parity harness is re-baselined for the two surfaces this intentionally changes: the `/api/tasks` body and the static tree.

Linked vault task (traceability): `[[Vault UI Resolves a Task's Pane Only When the Jump Link Is Clicked]]` (Personal vault), goal `[[Vault UI Ultra-Fast Reads and Writes]]`.

## Problem

Measured 2026-10-05 on the deployed v0.81.0: the board process sits at ~95% CPU with zero requests in flight, and spawns 1146 child processes in a 10-second window. The cost is `pkg/panecache`'s background refresher — every 3 s it resolves each live session's pane by shelling out to `python3 who-needs-me.py --pane-for` (~0.23 s each), continuously, whether or not anyone is looking at the board.

Two operator design rules (2026-10-05) make that shape wrong rather than merely expensive:

1. **Clicks are rare; views are frequent.** The pane id exists for one action — the jump link — so it belongs at that action, not in every list response.
2. **Go must not call Python.** Pane resolution is rebuilt in Go, and the background refresher is replaced by event-driven session state.

The vault goal's Success Criteria name a fixed short-interval refresher as a design defect, not a tuning problem: background work is event-driven (file watcher, session events) or computed on demand, and a slow safety-net rescan (minutes) is the only permitted timer.

## Goal

The board holds no pane state. A list read answers from the page index and carries no pane id. Clicking a card's jump link resolves that card's session to its WezTerm pane on the spot, in Go, and focuses it; a session with no resolvable pane produces a visible error rather than a dead control. The Live badge and whether the jump control is offered both follow session state that updates from file events on the harness session registry, so neither depends on a timer or on pane data.

The rules are documented in `docs/pane-resolution.md`.

## Non-goals

- No change to `/api/topics` or any other already-fast endpoint.
- No removal of the per-request `ps` scan on the request path (`pkg/board/tasks.go` calls `ResumeSessionIDs`, which runs `ps -axww`). It stays in this spec, and AC11 measures the residual it leaves; moving it off the request path is the sibling task `[[Vault UI Serves Task and Assignee Lists from a Watcher-Maintained Index]]`'s job.
- No change to `who-needs-me.py` itself, and no removal of the Python tree or of the parity harness's existence.
- No new external service dependency, no new HTTP route, no new query parameter, no opt-out flag.
- No change to write semantics, to the write queue, or to any other route's response body.
- No persistence of session state to disk.
- No change to the session liveness classification contract in `docs/liveness-classification.md` — the same four outcomes, the same signal order, the same five-minute window.

## Acceptance Criteria

- [ ] **AC1 — no Go→Python call remains.** `grep -rn 'python3\|\.py"' --include='*.go' pkg` returns no line outside `_test.go` files, and `grep -rn 'who-needs-me' --include='*.go' pkg` returns nothing — evidence: both greps print zero lines; a positive control records the pre-change hits (four non-test call sites) so a silently-broken grep is distinguishable from a passing one.
- [ ] **AC2 — the background pane refresher is gone.** `ls pkg/panecache` fails, and `grep -rn 'panecache' --include='*.go' .` returns nothing — evidence: `ls` exits non-zero with "No such file or directory"; the grep prints zero lines.
- [ ] **AC3 — a list read resolves no panes and spawns no subprocess.** A Ginkgo test drives the real board handler with a `PaneResolver` that records every call — evidence: across a `ListTasks` request the recorder holds exactly **0** calls (positive control: the same recorder is called once by the jump endpoint in the same test file, proving the double fires on a real call).
- [ ] **AC4 — no list response carries a pane-bearing key.** A Ginkgo test drives the real handler, marshals the `ListTasks` response and asserts that no key in any element names a pane — evidence: the assertion is over the marshalled key set, not over the literal `jump_pane`, so renaming the field cannot pass it; it fails against the pre-change build and passes after. The same assertion runs for `/api/goals`, which DB3 also names.
- [ ] **Post-Deploy (Rung-2): AC5 — the jump endpoint resolves the pane in Go.** POST `/api/tasks/{id}/jump` on a task whose session is live focuses that session's terminal pane — evidence: operator click-through on the deployed board, recorded as the focused pane id matching the session's WezTerm pane. On a task whose session has no resolvable pane the endpoint returns a non-2xx status with a non-empty body — evidence: `curl -s -o /dev/null -w '%{http_code}' -X POST .../jump` is ≥ 400, and the UI shows a visible error message rather than a silent no-op.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC6 — the jump button depends only on a live session.** On the deployed board a task with a live session shows the jump control, a task with no live session does not, and a live task whose pane cannot currently be resolved **still** shows the control — evidence: operator observes all three cases; the served `app.js` contains no read of `jump_pane` (`grep -c 'jump_pane' src/vault_ui/static/app.js` prints `0`), and `src/vault_ui/static/index.html` carries a new cache-bust token on both `app.js` and `style.css` (`grep -c '2026-10-01-jump-control' src/vault_ui/static/index.html` prints `0`) — without it a warm browser keeps the pre-change `app.js`, whose live branch still gates on the removed field and renders no jump control at all.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC7 — the badge and the jump control follow registry file events, not a ticker.** Creating or removing a session-registry file under `~/.claude/sessions/` changes the card's Live badge within 2 s with the board otherwise idle — evidence: the badge appears/disappears inside the window, timed; and across that window the recording `PaneResolver` from AC3 holds 0 calls, so the update demonstrably did not come from pane resolution. **Negative control:** with the event source held silent (the injected watcher delivers no events), the badge does not change before the rescan interval elapses and does change once it fires — evidence: the badge is still stale at the rescan interval minus 5 s and fresh after it, which a ticker-driven implementation cannot produce.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **AC8 — the liveness classification contract is unchanged.** `git diff origin/master -- pkg/session/classify.go` is empty, and the classification tests still pass — evidence: the diff prints nothing, and `go test ./pkg/session/...` exits 0 with the four-outcome, signal-order and five-minute-window cases still present by name.
- [ ] **AC9 — build health, parity, docs.** `make precommit` exits 0 (includes `go test -race ./...`); `make parity` exits 0 against the re-baselined harness; `grep -A10 '^## Unreleased' CHANGELOG.md` shows a bullet about resolving the pane on demand; `docs/pane-resolution.md` contains the headings `When a pane is resolved` and `Session state freshness` **and** records the resolver's seam signature (`grep -n 'Resolve(ctx' docs/pane-resolution.md` returns ≥1 line) — evidence: a headings-only file fails this. The re-baseline is narrow — evidence: `make parity`'s summary reports every route, case, error, mutation and static-asset ratio full (a route that lost its comparison drops below full), and the only harness logic edit is the `/api/tasks` body filter in `normalize()` (a comment above the static-asset loop is the only other change). The harness holds no stored static hash — `scripts/parity/parity.sh` hashes both backends' served bytes live, and both serve the same `src/vault_ui/static/` tree — so the static-tree re-baseline is the `docs/go-cutover.md` § 6 regression guard, not a harness line.
- [ ] **Post-Deploy (Rung-2): AC10 — the idle burn is gone.** With ≥1 live session and zero requests for 10 s, the board spawns exactly 0 child processes and its CPU stays below 5 % — evidence: the two numbers from the Operator-executable idle probe below; pre-fix baseline 1146 children/10 s at ~95 %.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`
- [ ] **Post-Deploy (Rung-2): AC11 — `/api/tasks` is within a small multiple of an in-memory reference.** 10 warm samples each of `/api/tasks` and `/api/vaults` in the same window — evidence: `/api/tasks` p50 ≤ 5× `/api/vaults` p50, both recorded. Sampled relative, not absolute, because the host routinely runs at load 36+; the pre-change reading was ~750× (1.5 s against 2 ms). This AC gates: the per-request `ps` scan is known to remain, and if it alone holds the ratio above 5× the spec does not complete — the recorded number is then the evidence that hands the work to the sibling index task.
  - `deploy_check:` `[ ~/Documents/workspaces/go/bin/vault-ui -nt ~/Documents/workspaces/vault-ui/.git/ORIG_HEAD ] && cd ~/Documents/workspaces/vault-ui && git rev-parse --short HEAD`
  - `deploy_target:` `$(cd ~/Documents/workspaces/vault-ui && git fetch -q && git rev-parse --short origin/master)`

No new scenario: AC1–AC4, AC8 and AC9 are container-reachable with fakes and greps, and AC5–AC7 and AC10–AC11 are observed on the running service.

## Verification

### Container-executable

- `make precommit`
- `make parity`
- `go test -race ./pkg/pane/... ./pkg/session/... ./pkg/sessionstate/... ./pkg/board/... ./pkg/mutations/... ./pkg/factory/...`
- `grep -rn 'python3\|\.py"' --include='*.go' pkg` — no hit outside `_test.go`
- `ls pkg/panecache` — fails

`pkg/sessionstate` is created by prompt 4 of the decomposition; the test command above is written against the finished tree. Prompts inherit the container rung as their `<verification>` block, so each prompt scopes it to the packages it actually touches — a prompt that has not yet created `pkg/sessionstate` must not list it.

### Operator-executable (host, after merge + deploy)

Deploy:

```bash
cd ~/Documents/workspaces/vault-ui && git pull && make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
until curl -s -o /dev/null http://127.0.0.1:8000/api/vaults; do sleep 1; done
```

Idle probe (AC10) — run with at least one live Claude session on the host:

```bash
pid=$(pgrep -f 'workspaces/go/bin/vault-ui' | head -1)
cpu0=$(ps -o %cpu= -p "$pid"); sleep 10; cpu1=$(ps -o %cpu= -p "$pid")
echo "children: $(pgrep -P "$pid" | wc -l | tr -d ' ')"; echo "cpu: $cpu0 -> $cpu1"
```

Latency (AC11):

```bash
for url in "/api/vaults" "/api/tasks?vault=private-personal"; do
  for i in 1 2 3; do curl -s -o /dev/null "http://127.0.0.1:8000$url"; done
  echo -n "$url p50: "
  for i in $(seq 10); do curl -s -o /dev/null -w "%{time_total}\n" "http://127.0.0.1:8000$url"; done | sort -n | sed -n '5,6p' | tail -1
done
```

Session-state probe (AC7) — with the board open and idle:

```bash
ls ~/.claude/sessions/*.json | head -1   # note a registry file
# remove and restore it, timing the badge in the browser
```

## Desired Behavior

1. A session id resolves to a WezTerm pane through a Go implementation: it enumerates the WezTerm panes and matches one to the session, with no Python interpreter and no `who-needs-me.py` in the path. The seam keeps the shape its callers already use, so no caller's contract changes.
2. `pkg/panecache` and its refresher are deleted. Nothing replaces them: there is no periodic pane work of any interval.
3. `/api/tasks` and every other list response carry no pane id. The board's response model loses the field rather than emitting it empty.
4. POST `/api/tasks/{id}/jump` resolves the task's session to a pane at request time through the Go resolver and focuses it. A task whose session has no resolvable pane answers with a non-2xx status and a body naming the failure; the frontend surfaces that as a visible error on the card.
5. The frontend offers the jump control whenever the task's session state is live, and only then. Its presence no longer depends on any pane field, and a live task whose pane cannot currently be resolved still offers the control — the failure is reported when it is clicked, not by hiding it. Because `app.js` and `style.css` change, both cache-bust tokens in `index.html` are bumped so no browser keeps the pre-change script.
6. Session state is kept current by a new `pkg/sessionstate` watcher on the harness session registry (`~/.claude/sessions/*.json`), following the pattern the page index uses for the vault: an initial read, file events driving updates, and a rescan every 60 s as the safety net for a missed event. The Live badge and the jump control's availability both read from this state, and a change in the live set pushes the existing refresh frame to connected browsers so an idle board updates without waiting for its 60 s poll.
7. The classification of a card as live / quiet / indeterminate / none is unchanged — the same four outcomes, the same signal order, the same five-minute window documented in `docs/liveness-classification.md`. Only the freshness mechanism changes.
8. The Go↔Python parity harness is re-baselined for exactly the two surfaces this change moves — the `/api/tasks` body comparison in the harness, and the static-tree baseline in the `docs/go-cutover.md` regression guard (the harness itself compares served static bytes live, so it needs no static edit) — so `make parity` exits 0 again. Every other route's comparison is untouched, and the `docs/go-cutover.md` regression guard is updated to the new baseline. `docs/pane-resolution.md` records when a pane is resolved and how fresh session state is, including the resolver's seam signature and where the composition root wires it.

## Constraints

- `docs/liveness-classification.md` is a frozen contract: the four outcomes, the signal order and the five-minute window do not change. This spec moves only where the state is read from.
- Every route other than the two named in Desired Behavior 8 keeps its response body, status codes, query parameters and WebSocket frame content byte-identical to the Python reference (spec 023 parity contract). The re-baseline is narrow by design — widening it would silently drop the safety net for routes this change does not touch.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no `fmt.Errorf`, `Create*` factories carry no business logic, Ginkgo/Gomega tests, `glog` for logging, ≥80 % coverage on new packages.
- Concurrency goes through `github.com/bborbe/run`; a bare `go func()` is a violation. Loop and watcher code honours `ctx.Done()`.
- Hand-write test doubles: the repo has no `make generate` target and counterfeiter is not a module dependency, so a `//counterfeiter:generate` directive would leave the fake ungenerated and break the test build. This follows the documented deviation already recorded in `prompts/completed/126-background-pane-resolution.md`.
- No test spawns a real subprocess, a real `wezterm` invocation, or a real network call; the resolver's process boundary is injected.
- `who-needs-me.py` and `pkg/pane`'s Python-invoking helpers may be deleted only if nothing else in the repo calls them; the Python tree itself stays.
- The spec `024-serve-list-reads-from-page-index` is in flight and also touches `/api/tasks`; the re-baseline must not re-pin a body that spec is still changing.

## Assumptions

- `wezterm cli list --format json` is available on the operator's host and its output carries a field that identifies the session each pane is running; if the shape changes, AC5 fails loudly (see Failure Modes).
- The harness writes one `<pid>.json` per live session under `~/.claude/sessions/`, as documented in `docs/liveness-classification.md`; the watcher relies on that directory and no other.
- `who-needs-me.py` has no caller outside this repo once the Go resolver lands; if it does, it is left in place and only the Go-side calls are removed.
- The Go module has no `make generate` target and does not depend on counterfeiter — asserted in Constraints, verified by the existing hand-written doubles.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---|---|---|---|
| `wezterm` is not running, or its CLI is absent | The jump request answers non-2xx with a body naming the failure; the list reads and the board are unaffected | Start WezTerm and click again; no state to repair | Card shows the visible error (AC5) |
| A live session has no matching pane (launched outside WezTerm) | Same non-2xx answer; the jump control stays offered because the session is live | None needed — the session simply has no pane | AC5's no-pane case |
| A session registry file is written but the watcher misses the event | Badge stale until the 60 s rescan fires | Rescan within 60 s | Badge updates late; AC7's negative control is the bound |
| Session registry directory missing or unreadable | Watcher starts with an empty set and keeps serving; badge reads `none` for cards with no session id | Directory reappears → next event or rescan repopulates | V(2) log naming the path |
| WezTerm's `cli list --format json` output shape changes | The resolver fails to match and returns `false` for every session, so every jump answers non-2xx | Fix the parser; no cache to invalidate | AC5 fails loudly on the next click |
| Two jump requests for the same session at once | Both resolve independently and both focus the same pane; no shared mutable state to corrupt | None needed | — |
| The re-baseline is widened past the two intended surfaces | Routes this change does not touch lose their comparison silently | Narrow the re-baseline; re-run `make parity` | Review of the harness diff |
| The per-request `ps` scan alone holds `/api/tasks` above the AC11 ratio | AC11 fails and the spec does not complete; the measured ratio is recorded against the sibling task | The sibling index task moves the scan off the request path — out of scope here | AC11's recorded numbers |
| The `pkg/sessionstate` watcher goroutine exits or crashes | Badge stops updating from events and falls back to the 60 s rescan only; the board keeps serving | Restart the service; the watcher's own error path logs before it returns | V(2) log line; AC7's negative control still passes, so the degradation is silent without the log |
| The host clock jumps backwards across the five-minute liveness window | Classification is unchanged in kind — the window is compared against transcript mtimes, so a backward jump can briefly read a stale transcript as live | None needed; the next event or rescan re-evaluates | Badge flicker only |

## Security / Abuse Cases

The jump endpoint already resolves the caller's own task and enforces same-origin; this change does not widen that. Pane ids are resolved server-side from the local WezTerm instance and are not accepted from request input. The session registry path is server config plus the home directory, never request input. The jump token discipline from the earlier jump-route work is unchanged — no token appears in any response body.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Go pane resolver in `pkg/pane`: enumerate panes, match a session id, keep the seam its callers use, injected process boundary; unit tests with a hand-written double; delete the Python-invoking helpers | 1 | AC1 | — |
| 2 | Delete `pkg/panecache` and the refresher wiring; drop the pane id from the list response; the jump endpoint resolves through the Go resolver and answers non-2xx when there is no pane; board + mutation tests | 2, 3, 4 | AC2, AC3, AC4 | prompt 1 |
| 3 | Frontend: offer the jump control on live session state alone, drop the `jump_pane` read, surface the endpoint's error visibly | 5 | AC6 | prompt 2 |
| 4 | New `pkg/sessionstate`: watcher on `~/.claude/sessions/*.json` with an initial read, file events, and a 60 s rescan; badge and jump availability read from it; `pkg/session` classification untouched (AC8); `docs/pane-resolution.md` | 6, 7 | AC7, AC8, AC9 (docs) | prompt 2 |
| 5 | Re-baseline the parity harness for the `/api/tasks` body; update the `docs/go-cutover.md` regression guard; CHANGELOG | 8 | AC9 | prompts 2, 3 |

Rationale: prompt 1 establishes the resolver seam every later prompt calls. Prompts 2 and 3 are the request-path change and can land together; prompt 4 is independent of 3 and could run in parallel. Prompt 5 must come last, because it re-pins the two surfaces the earlier prompts move.

## Do-Nothing Option

The board keeps burning ~95 % of a core spawning Python interpreters around the clock, whether or not anyone is looking at it, and every list response keeps carrying a pane id that is stale within three seconds of being computed. The vault goal's architectural criterion — background work event-driven or on demand, a fixed short-interval refresher being a design defect — stays unmet, and the sibling read-path tasks cannot show their benefit while this burn continues alongside them.
