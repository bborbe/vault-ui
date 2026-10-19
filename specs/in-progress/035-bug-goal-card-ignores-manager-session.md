---
status: verifying
approved: "2026-10-19T16:50:59Z"
generating: "2026-10-19T16:54:24Z"
prompted: "2026-10-19T17:06:50Z"
verifying: "2026-10-19T17:39:25Z"
branch: dark-factory/bug-goal-card-ignores-manager-session
---

## Summary

- A goal card whose manager session is running renders `▶ Resume`, so clicking it opens a second session onto a live one.
- The board decides a goal is live from the goal's own session id only. The id of the manager session that armed the goal is never read.
- That manager id does not even reach this service: the goal list data vault-cli returns carries no such field. A sibling vault-cli change adds and populates it; this spec consumes it.
- Two observed goals failed for different reasons: one carries a manager id (a read-path defect), the other carried none (a page-data gap, fixed separately).
- The four-outcome liveness contract is unchanged. No fifth state is added.

## Problem

`GET /api/goals` returns `session_state: indeterminate` for `Build Sentry Issue Analyzer Agent` and `Auto-Resolve Sentry Issues` while live manager sessions run for both (wezterm panes 794 and 796, `/supervisor:manager-loop`). The card therefore offers `▶ Resume`, and Resume on a live session either is flock-refused or ends the running turn — the defect this board exists to prevent.

Root cause, traced on 2026-10-19:

- `pkg/board/goals.go` `goalResponse` calls `session.ClassifySessionState` with `SessionID: item.ClaudeSessionID` — one id, the goal's own.
- `pkg/board/goals.go` writes `ClaudeSessionID: strPtr(item.ClaudeSessionID)` into `api.GoalResponse`; `Location` likewise resolves from that id.
- `item` is a `github.com/bborbe/vault-cli/pkg/ops.TaskListItem`, whose struct has `ClaudeSessionID` and no manager field at all.
- claude-supervisor's manager-resolve step writes `manager_session_id` onto the goal page when a manager arms it. `24 Goals/Build Sentry Issue Analyzer Agent.md` carries `manager_session_id: eeaa3356-…` (live in the attention store); `24 Goals/Auto-Resolve Sentry Issues.md` carried none.

## Reproduction

**Config.** The locally deployed board (launchd label `com.github.bborbe.vault-ui`, see `docs/launchd-service.md`), serving `127.0.0.1:8000`.

**State.** A goal page under `24 Goals/` carrying a `manager_session_id` whose session is live in the attention store, and a `claude_session_id` that is not:

```bash
grep -n '^manager_session_id\|^claude_session_id' "24 Goals/Build Sentry Issue Analyzer Agent.md"
curl -s --noproxy '*' 'http://127.0.0.1:18080/api/1.0/session-heartbeat' \
  | jq -r '.[] | select(.live==true) | .session_id'
```

**Command.**

```bash
curl -s --noproxy '*' 'http://127.0.0.1:8000/api/goals?vault=private-personal' \
  | jq -r '.[] | select(.title|test("Sentry")) | "\(.title) | \(.session_state) | \(.claude_session_id)"'
```

**Observed** (2026-10-19):

```
Build Sentry Issue Analyzer Agent | indeterminate | 6debe005-…
Auto-Resolve Sentry Issues       | indeterminate | 02056c3c-…
```

Both manager sessions were live in the attention store; neither card read `live`.

## Expected vs Actual

| | |
|---|---|
| **Expected** | A goal card whose manager session is live renders the live badge (`● Live`), on the same terms as a task card. `docs/liveness-classification.md` defines `live` as "a Claude session for this card is running right now; the wall must not offer Resume" — the manager session *is* a session for this card. |
| **Actual** | Both cards read `indeterminate` and offer Resume, because only `claude_session_id` is classified and neither goal's own id was live. |

## Why this is a bug

`docs/liveness-classification.md` states the card contract in terms of "a Claude session for this card", not "the session recorded in `claude_session_id`". A manager session armed for a goal is a session for that goal, and the page records its id for exactly this purpose. Offering Resume while it runs is the failure the contract exists to prevent.

## Goal

A goal card classifies against every session id its page records — its own and its manager's — and renders the live badge whenever any of them is live. A goal with no live id keeps offering Resume, and the four-outcome contract is unchanged.

## Assumptions

- claude-supervisor's manager-resolve step is the only writer of `manager_session_id` on a goal page; it overwrites the field each time a manager arms the subject and never clears it.
- The attention store remains the single shared **live-id** source; `~/.claude/sessions` stays an identity lookup; transcript recency and the `ps --resume` cross-check remain the other two signals in the fixed order (`docs/liveness-classification.md` § Signal order).
- The sibling vault-cli change both **adds the field and populates it** in `ops.List` — a struct field that is never populated would make this fix a silent no-op.
- Target dependency: the first vault-cli release after `v0.170.2` that carries the list field from `fix/manager-session-id-in-task-list` — verify with `grep ManagerSessionID` in the module cache after the bump, rather than assuming a version number.

## Constraints

- **Precondition: a released vault-cli exposing a populated `ManagerSessionID` on `ops.TaskListItem`**, and `go.mod` bumped to it. Sibling artifact: `prompts/add-manager-session-id-to-list-read-model.md` on branch `fix/manager-session-id-in-task-list` in `github.com/bborbe/vault-cli`. Do NOT re-parse goal frontmatter in this repo to route around the dependency. If the release is not yet available, this spec's prompts do not start.
- **The four outcomes and their signal order are frozen** (`docs/liveness-classification.md`). No fifth state, no reordering, no new window, no config knob.
- **Unreachable store stays `indeterminate`.** A store outage must never turn a live goal into Resume — the existing `LiveIDsUnknown` path is unchanged.
- **Task-card behaviour is unchanged.** `session.ClassifySessionState` is shared by `pkg/board/tasks.go` and `pkg/board/goals.go`; a change that alters task classification is a regression.
- **`claude_session_started` keeps reading its own page marker, and `activity_date` keeps deriving from `claude_session_id`** — neither changes meaning (`docs/starting-marker-lifecycle.md`).
- **The approve contract is `phase: planning` + `status: next`.** vault-cli v0.171.0's `task approve` stopped writing `status`; both vault-ui callers of `set.Approve.Execute` (`approveTask`, `RunTask`) must write it themselves. Do not drop that restoration as out of scope.
- Do NOT add a goal-card jump control: goal cards have no jump route (`src/vault_ui/static/app.js`, `if (kind !== 'task')`).
- Do NOT backfill `manager_session_id` on goal pages from code.
- **Update `docs/liveness-classification.md`** in the same change: its `none` row currently reads "The card carries no `claude_session_id`", which this change narrows to "no session id at all (neither own nor manager)", plus a "Multiple session ids on a card" section stating the combination order.
- **Add a `## Unreleased` entry to `CHANGELOG.md`** — it does not exist today (the newest heading is `## v0.97.2`), so create it above that heading.

## Desired Behavior

1. For a goal, classify every session id the page records (`claude_session_id`, `manager_session_id`) that is non-empty, and combine the outcomes: `live` if any is live; otherwise `indeterminate` if any is indeterminate; otherwise `quiet` if any is quiet; `none` only when both ids are empty. The combination is strongest-outcome, never last-id-wins — a goal whose own id is live and whose manager id is stale still reads `live`.
2. An id that cannot be proven dead, or that is malformed, keeps the card `indeterminate` — never `quiet`, never `live` — so Resume is suppressed rather than offered.
3. `Location` comes from the first id whose outcome is `live`, in a fixed order: `claude_session_id` first, `manager_session_id` second. If that id is live but `RegistryLocation` returns empty (live by transcript recency or `ps`, not in the store), `location` is null — do not fall through to the next id.

## Acceptance Criteria

- [ ] A goal page carrying a live `manager_session_id` and a non-live `claude_session_id` returns `session_state: live` from `GET /api/goals`.
- [ ] A goal page carrying an empty `claude_session_id` and a live `manager_session_id` returns `session_state: live` — the manager-armed-but-never-started case.
- [ ] A goal page carrying an empty `claude_session_id` and a stale `manager_session_id` returns `session_state: quiet`.
- [ ] A goal page carrying a live `claude_session_id` and no manager id still returns `session_state: live` (no regression).
- [ ] A goal page carrying neither id returns internal state `none` — `session_state` is `null` on the wire (`sessionStatePtr` serializes `SessionStateNone` as null).
- [ ] A goal page carrying only stale ids returns `session_state: quiet`.
- [ ] A goal page carrying an id that cannot be proven dead returns `session_state: indeterminate`.
- [ ] A goal page carrying a malformed (non-UUID) manager id returns `indeterminate`, never `quiet` — Resume suppressed rather than offered.
- [ ] When both ids are live and the two fake locations differ, `location` is the location reported for `claude_session_id` (the fixed precedence) — a `pkg/board` test with two distinct non-empty fake locations, so an empty-location implementation cannot pass vacuously.
- [ ] When the own id is live by transcript recency (not in the store, so `RegistryLocation` is empty) and the manager id is live in the store with a non-empty location, `location` is null — no fall-through to the manager id.
- [ ] A unit test in `pkg/board` covers the combination table — own-live + manager-quiet → live; manager-live + own-quiet → live; own-live + manager-indeterminate → live; own-empty + manager-live → live; own-empty + manager-quiet → quiet; quiet + indeterminate → indeterminate; empty + empty → none — and fails on the pre-fix tree, where `manager-live + own-quiet` returns `quiet`.
- [ ] `pkg/board/tasks.go` classification is unchanged — the task-card tests pass untouched (`go test ./pkg/board/...`).
- [ ] `claude_session_started` and `activity_date` still derive from `claude_session_id` — a `pkg/board` test asserts both fields for a goal carrying a manager id.
- [ ] An unreachable attention store (`LiveIDsKnown() == false`) still yields `indeterminate`, never `quiet`, for a goal with a manager id.
- [ ] `docs/liveness-classification.md` carries a "Multiple session ids on a card" section stating the combination order (`live` > `indeterminate` > `quiet` > `none`) and the location precedence, and its `none` row no longer says "no `claude_session_id`" — evidence: `grep -c 'Multiple session ids' docs/liveness-classification.md` prints `1`, and `grep -c 'live.*indeterminate.*quiet.*none' docs/liveness-classification.md` prints at least `1`.
- [ ] `CHANGELOG.md` has a `## Unreleased` section carrying this fix's bullet — evidence: `awk '/^## /{sec=$0} /manager/ && sec=="## Unreleased"{print sec; f=1} END{exit !f}' CHANGELOG.md` prints `## Unreleased`.
- [ ] `make precommit` exits 0.
- [ ] **Post-Deploy (Rung-2):** the deployed board classifies a goal from its live manager id — evidence: `curl -s --noproxy '*' 'http://127.0.0.1:8000/api/goals?vault=private-personal'` shows `session_state: live` for `Build Sentry Issue Analyzer Agent` while its `manager_session_id` is live in the attention store. The second Sentry goal is verified only after its separate page-data fix. The check is conditioned on the manager session being live at verification time; if it has ended, re-arm the goal and re-run.
  - `deploy_check:` `curl -s --noproxy '*' 'http://127.0.0.1:8000/api/goals?vault=private-personal' | jq -e 'any(.[]; .title=="Build Sentry Issue Analyzer Agent" and .session_state=="live")'`
  - `deploy_target:` `local board (com.github.bborbe.vault-ui), 127.0.0.1:8000`
- [ ] **Operator visual (supplementary, not machine evidence):** both Sentry cards show `● Live`, and a control goal with no live session still shows `▶ Resume` — recorded as the operator's click-through confirmation.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```bash
make precommit
go test ./pkg/board/... -count=1
```

### Operator-executable (host, after merge)

⚠️ **production-touching:** the restart below kills every session the board tracks as live. Take it in a quiet window, with its own consent — never fold it into a merge.

```bash
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
curl -s --noproxy '*' 'http://127.0.0.1:8000/api/goals?vault=private-personal' \
  | jq -r '.[] | select(.title|test("Sentry")) | "\(.title) | \(.session_state)"'
```

Expected: both lines end in `live` while the manager sessions are running.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Attention store unreachable | `indeterminate`, never `quiet`; no Resume offered | Restore the store; the next snapshot refresh returns `live`/`quiet`, confirmed by re-running the Verification `curl` |
| `manager_session_id` names a session that is not in the attention store and has no local transcript | `indeterminate` | Expected — the card never offers Resume; no action. A store-live session with no local transcript still reads `live`. |
| Malformed (non-UUID) manager id | `indeterminate`; Resume suppressed | Expected — safe but wrong; correct the page data |
| Board restarted before the dependency bump lands | Old binary, old behaviour, no regression | Redeploy after the merge |
| Both ids empty | `none`; Start is offered | Expected for a human goal |

## Do-Nothing Option

Do nothing and every goal with a live manager session keeps offering Resume. Clicking it either fails the vault-cli flock or ends the running turn — the exact failure the board exists to prevent. The cost of not fixing is a corrupted manager session per mis-click.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Bump `github.com/bborbe/vault-cli` to the release exposing a populated `ManagerSessionID`; confirm it compiles and that `ops.List` returns a non-empty value for a goal page carrying the field | — | dependency precondition | sibling vault-cli release |
| 2 | Combine the two ids in `goalResponse` with the strongest-outcome rule; select `Location` from the first live id with no fall-through | 1, 2, 3 | the combination, location, and unchanged-field ACs | prompt 1 |
| 3 | Update `docs/liveness-classification.md` (`none` definition + "Multiple session ids on a card") | 1 | AC15 | prompt 2 |
| 4 | CHANGELOG `## Unreleased` entry and the deployed verification | — | AC16, AC17 | prompts 2, 3 |

**Scenario coverage — no new scenario.** The behaviour is reachable by `pkg/board` rows with fake signals, and the deployed half is covered by the Post-Deploy probe. A Docker/E2E harness would not reach anything those rows cannot.

## Security / Abuse Cases

None. The change reads operator-written frontmatter (never user input), adds no file, no auth path, and no write surface; the endpoints stay read-only.

## Non-goals

- Do not fix the missing `manager_session_id` on a goal page — that is a page-data gap, written separately, not code.
- Do not add `manager_session_id` to `task show` (`TaskDetail`) in vault-cli — the consumer reads the list model.
- Do not add a `manager_session_id` field to `api.GoalResponse` — nothing reads it; the badge renders from `session_state`. If a consumer appears, that is a separate spec.
- Do not change the attention store, its heartbeat contract, or the session registry.
- Do not add `session_state` to topic cards — the sibling surfaces `manager_session_id` on topic pages too, but `TopicResponse` has no `session_state` and no consumer exists today.
- Do not correct a stale manager id whose successor never re-armed the goal — that data accuracy belongs to claude-supervisor, whose contract calls the field evidence of *who*, never of *alive*.
