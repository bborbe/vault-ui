---
status: draft
---

## Summary

- A topic card in the TODO column prints the subtitle `in_progress`, contradicting the column it sits in.
- The column is chosen from the page's `phase`; the subtitle prints `status` — two different axes rendered as one claim.
- The phase column is the board's established grouping and stays. The status badge goes, as it already has on task and goal cards.
- A topic card shows nothing about whether a manager session is running for it.
- The topic's manager session id is already on the page and already reaches the service; only the response model drops it.

## Problem

On the running board (`127.0.0.1:8000`, Topics tab, vault `seibert-brogrammers`), five topics — `CDB in Weldall`, `Data Division Documentation`, `MDM Bugs`, `Security Vulnerability Remediation`, `Sentry` — sit in the **TODO** column while every one of their cards shows the subtitle `in_progress`.

Both fields are read correctly and disagree: all five carry `phase: todo` and `status: in_progress`.

The second half is an omission. A topic page records `manager_session_id` when a manager arms it, and that id reaches this service on `ops.TaskListItem.ManagerSessionID`, but `api.TopicResponse` carries no session field, so the card cannot show whether a manager is running.

## Reproduction

**Config.** The locally deployed board (launchd label `com.github.bborbe.vault-ui`, see `docs/launchd-service.md`), serving `127.0.0.1:8000`.

**State.** Five topics under `23 Topics/` in `seibert-brogrammers` carrying `phase: todo` and `status: in_progress`.

```bash
curl -s --noproxy '*' 'http://127.0.0.1:8000/api/topics?vault=seibert-brogrammers' \
  | jq -r '.[] | "\(.title) | status=\(.status) | phase=\(.phase)"'
```

**Observed** (2026-10-29):

```
CDB in Weldall                     | status=in_progress | phase=todo
Data Division Documentation        | status=in_progress | phase=todo
MDM Bugs                           | status=in_progress | phase=todo
Security Vulnerability Remediation | status=in_progress | phase=todo
Sentry                             | status=in_progress | phase=todo
```

Driving the board (`?view=topics&vault=seibert-brogrammers`) returns all five under `#cards-todo`, each with badge text `in_progress`, and zero live indicators on any topic card.

## Expected vs Actual

| | |
|---|---|
| **Expected** | A topic card does not assert a state that contradicts the column it occupies, and states whether a manager session is running for it. |
| **Actual** | The card renders `status` in the same affordance the column already speaks to, so the two disagree whenever a page's `status` and `phase` differ; and no session state is rendered at all. |

## Why this is a bug

A card that contradicts its own column cannot be read at a glance, which is the board's whole purpose. Task and goal cards render no status badge for exactly this reason: their column is their phase, and a second axis in the same slot produces a claim neither field makes.

The missing manager indicator is the same class of gap spec 035 closed for goals: a live manager session is a session for that card, and the page records its id for that purpose.

## Goal

A topic card is self-consistent with the phase column it renders into, and shows a display-only live indicator when a manager session is running for it. A topic with no manager session, or a dead one, shows nothing. No topic page is written.

## Assumptions

- `phase` is the Topics view's column discriminator and stays so; phase grouping for goals and topics landed in v0.98.0 and is not revisited here.
- `manager_session_id` on a topic page is written by claude-supervisor's topic-resolve step and is a lead, never a verdict — it must be classified, never trusted.
- No topic page carries `claude_session_id` today (0 of 27), so a topic's outcome is its single manager id's outcome.
- `ops.TaskListItem.ManagerSessionID` is populated (vault-cli v0.171.0, `pkg/ops/list.go:58,115`), the same dependency spec 035 already relies on.

## Constraints

- **Do NOT change task or goal cards, or any task or goal response field.** They render exactly as they do today.
- **Do NOT write to any `23 Topics/` page** and do not backfill `phase`, `assignee`, `priority` or `manager_session_id`. This is a rendering and read-path change.
- **The four-outcome liveness contract and its signal order are frozen** (`docs/liveness-classification.md`). No fifth state, no reordering, no new window, no config knob.
- **Unreachable store stays `indeterminate`.** A store outage must never turn a live manager into a silent card.
- The indicator is **display-only**: no start, resume, take-over or jump control on a topic card, and no topic mutation route.
- The superseded Python backend's response model carries the same field so its integration harness renders the same board; it emits `null` for the value because it never reads `manager_session_id`.
- Add a `## Unreleased` entry to `CHANGELOG.md`.

## Desired Behavior

1. The topic card renders no status badge. The column is the topic's phase; the topic's own `status` stays visible in the detail modal (`#topic-modal-status`) and keeps driving the board's status filter.
2. `GET /api/topics` carries `session_state` for every topic, classified from the page's `manager_session_id` with the same helper and parameters the goal response uses: `live` / `indeterminate` / `quiet`, and `null` when the page records no manager id.
3. A topic card renders a live indicator when and only when `session_state` is `live`. The indicator is not clickable and carries no pointer affordance.

## Acceptance Criteria

- [ ] No topic card carries a `.status-badge`; evidence: the running board's topic cards report no status badge text.
- [ ] Each of the five named topics renders in the column its `phase` names — `#cards-todo` — with no contradicting subtitle; evidence: driven from the running board.
- [ ] `GET /api/topics` returns `session_state` on every row: a topic whose `manager_session_id` is live in the attention store returns `live`; a topic carrying none returns `null`; a topic whose id is stale returns `quiet`; an unreachable store returns `indeterminate`.
- [ ] The Go wire shape is pinned: a decoded `ListTopics` row's field set is exactly `flag, id, obsidian_url, phase, session_state, status, title, vault`, so a tag typo cannot ship silently.
- [ ] A topic card renders a `.live-badge` when `session_state` is `live`, and none when it is `null`, `quiet` or `indeterminate`.
- [ ] No file under any `23 Topics/` directory is modified — evidence: a checksum manifest of all topic pages is identical before and after.
- [ ] Task and goal cards are unchanged — their existing tests pass untouched.
- [ ] `make precommit` exits 0.
- [ ] `README.md` documents `session_state` in the `GET /api/topics` payload.
- [ ] `CHANGELOG.md` carries the bullet under `## Unreleased`.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```bash
make precommit
go test ./pkg/board/... -count=1
```

### Operator-executable (host, after merge)

⚠️ **production-touching:** the restart below replaces the running board process.

```bash
make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
curl -s --noproxy '*' 'http://127.0.0.1:8000/api/topics?vault=seibert-brogrammers' \
  | jq -r '.[] | "\(.title) | \(.phase) | \(.session_state)"'
```

Expected: the five named topics read `todo` with `session_state: null` (none has a live manager), and no card in `#cards-todo` carries a status badge.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Attention store unreachable | `indeterminate`, never `quiet`; no live indicator | Restore the store; the next snapshot refresh returns the real state |
| `manager_session_id` names a session with no local transcript and not in the store | `indeterminate`; no indicator | Expected — the card stays silent rather than claiming dead |
| Malformed (non-UUID) manager id | `indeterminate`; no indicator | Expected — correct the page data |
| A page carrying both a status that disagrees with its phase | Only the phase column speaks; no contradicting subtitle | Expected — that is the fix |
| Browser holds the old `app.js` | Old card keeps rendering the badge | The cache-buster token is bumped in the same change |

## Do-Nothing Option

Do nothing and the five topics keep reading as a contradiction, and no topic card can ever show that a manager is running for it. The cost is an unreadable board for exactly the containers the manager layer is scoped to.

## Suggested Decomposition

| # | Prompt focus | Covers ACs | Depends on |
|---|---|---|---|
| 1 | `session_state` on `TopicResponse` + `ListTopics` classification + Go wire-shape test | AC3, AC4 | — |
| 2 | Topic card: drop the status badge, add the display-only live indicator, cache-buster bump, CSS | AC1, AC2, AC5, AC6 | prompt 1 |
| 3 | Python response-model parity, `README.md`, `CHANGELOG.md`, docs sentence | AC8, AC9 | prompt 2 |

One prompt covers all three here: the halves share a single card builder and a single test file, and splitting them would ship a half-rendered card between merges.

**Scenario coverage — no new scenario.** The classification is reachable by `pkg/board` rows with fake signals, and the deployed half is covered by the operator probe.

## Security / Abuse Cases

None. The change reads operator-written frontmatter, adds no file, no auth path and no write surface; the endpoints stay read-only.

## Non-goals

- Do not add `assignee` or `priority` to topic pages, or render them on the topic card — that is a schema decision for the operator.
- Do not converge the three card builders into one renderer. `createTaskCard`, `createGoalCard` and `createTopicCard` are three renderers; `cardShellHtml` is shared by the task card alone, and the goal card re-implements the same markup inline. Unifying them is a separate refactor.
- Do not add a take-over, start, resume or jump control to a topic card.
- Do not backfill `manager_session_id` on topic pages from code.
- Do not change the attention store, its heartbeat contract, or the session registry.
