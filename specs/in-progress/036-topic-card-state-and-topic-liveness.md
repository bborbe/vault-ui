---
status: verifying
approved: "2026-10-29T12:59:56Z"
generating: "2026-10-29T13:00:43Z"
prompted: "2026-10-29T13:01:04Z"
verifying: "2026-10-29T13:05:17Z"
branch: dark-factory/topic-card-state-and-topic-liveness
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
- No topic page carries `claude_session_id` today (0 of 27), so a topic's outcome is its single manager id's outcome. A page that later gains one would still classify from the manager id alone; that is a known limitation of this spec, not a silent claim that topics have one id by design.
- `ops.TaskListItem.ManagerSessionID` is populated (vault-cli v0.171.0, `pkg/ops/list.go:58,115`), the same dependency spec 035 already relies on.

## Constraints

- **Do NOT change task or goal cards, or any task or goal response field.** They render exactly as they do today.
- **Do NOT write to any `23 Topics/` page** and do not backfill `phase`, `assignee`, `priority` or `manager_session_id`. This is a rendering and read-path change.
- **The four-outcome liveness contract and its signal order are frozen** (`docs/liveness-classification.md`). No fifth state, no reordering, no new window, no config knob.
- **Unreachable store stays `indeterminate`.** A store outage must never turn a live manager into a silent card.
- The indicator is **display-only** and must not reuse the take-over affordance's class. `.live-badge` is contractually the take-over control — `cursor: pointer`, hover/active shades (`src/vault_ui/static/style.css:773-786`) and `role="button" tabindex="0" onclick="takeOverSession(...)"` (`src/vault_ui/static/app.js:1582`), pinned by `tests/test_live_badge_location.py:60-73`. The topic indicator uses its own class.
- **`tests/test_topics_view.py` must be rewritten, not deleted.** `test_topics_render_each_own_status_and_phase_column` reads `.status-badge` (`tests/test_topics_view.py:207`) and asserts `in_progress` / `completed` for both fixture topics; removing the badge makes it fail. It is `integration`-marked and deselected by `make test`, so `make precommit` cannot catch this — the rewrite is mandatory, and its status coverage moves to the modal test that already asserts it (`tests/test_topics_view.py:260`).
- The superseded Python backend's response model carries the same field so the parity body diff does not diverge: `scripts/parity/parity.sh` normalizes only `jump_pane`, `open_questions` and `location`, so a Go-only `session_state` key would fail `scripts/parity/cases.txt:18`. Adding the key to the normalize list instead is deliberately rejected — it would weaken the safety net for every future field.
- **Update `docs/liveness-classification.md`** in the same change: its § "Multiple session ids on a card" describes only goals, and `docs/dod.md` requires the update once described behaviour changes.
- **Update `README.md`'s `GET /api/topics` example** to the real field set.
- Add a `## Unreleased` entry to `CHANGELOG.md`.

## Desired Behavior

1. The topic card renders no status badge. The column is the topic's phase; the topic's own `status` stays visible in the detail modal (`#topic-modal-status`) and keeps driving the board's status filter (`src/vault_ui/static/app.js:1210`).
2. `GET /api/topics` carries `session_state` for every topic, classified from the page's `manager_session_id` with the same helper and parameters the goal response uses: `live` / `indeterminate` / `quiet`, and `null` when the page records no manager id.
3. A topic card renders a display-only live indicator when and only when `session_state` is `live`. The indicator carries no `onclick`, no `role="button"`, no `tabindex`, and its CSS rule carries no `cursor: pointer` and no hover shade.

## Acceptance Criteria

- [ ] A `pkg/board` table test covers the four classification cases — manager id live in the fake store → `live`; no manager id → `session_state` null on the wire; stale manager id → `quiet`; `LiveIDsKnown() == false` → `indeterminate` — modeled on the goal cases at `pkg/board/board_test.go:879-930` and `:945-950`, and failing on the pre-fix tree where no field exists. Evidence: `go test ./pkg/board/... -count=1`.
- [ ] The Go wire shape is pinned: a decoded `ListTopics` row's field set is exactly `flag, id, obsidian_url, phase, session_state, status, title, vault` (pattern: `pkg/board/parity_test.go:24-42`), so a tag typo cannot ship silently.
- [ ] `testTopicCard`'s body carries no `.status-badge` and does carry the `session_state === 'live'` gate — a static assertion in the `tests/test_card_render_unify.py` lane (pattern: `_function_body(name)` at `tests/test_card_render_unify.py:27-33`), which runs in `make precommit`.
- [ ] The topic indicator's markup carries no `onclick`, no `role="button"` and no `tabindex`, and its CSS rule carries no `cursor: pointer` — evidence: `grep -n 'topic-live-badge' src/vault_ui/static/app.js src/vault_ui/static/style.css` shows the class in both files and no interactive attribute on the same line.
- [ ] `tests/test_topics_view.py` is rewritten and passes: no topic card carries a `.status-badge`; each card sits in the column its `phase` names; exactly the fixture topic with `session_state="live"` renders the indicator. Evidence: `uv run pytest tests/test_topics_view.py -m integration`.
- [ ] A topic whose `status` is outside the active filter still renders no card — the status filter is unchanged code (`src/vault_ui/static/app.js:1210`, untouched by this change) and its existing coverage stands.
- [ ] The cache-buster token in `src/vault_ui/static/index.html:175` is bumped and a test asserts the old `2026-10-29-jump-pane-gating` token is gone (pattern: `tests/test_card_render_unify.py:113-115`).
- [ ] No file under any `23 Topics/` directory is modified — evidence: a checksum manifest of all 27 topic pages is byte-identical before and after.
- [ ] Task and goal cards are unchanged — their existing tests pass untouched (`go test ./pkg/board/... -count=1`).
- [ ] `docs/liveness-classification.md` § "Multiple session ids on a card" states that a topic page records only `manager_session_id`, so a topic's outcome is that single id's outcome — evidence: `grep -c 'topic' docs/liveness-classification.md` prints at least `1`.
- [ ] `README.md`'s `GET /api/topics` example lists `phase`, `flag` and `session_state` — evidence: `sed -n '/### `GET \/api\/topics`/,/### `GET \/api\/topics\//p' README.md | grep -c session_state` prints `1`.
- [ ] `CHANGELOG.md` carries the bullet under `## Unreleased` — evidence: `awk '/^## /{sec=$0} /topic card/ && sec=="## Unreleased"{print sec; f=1} END{exit !f}' CHANGELOG.md` prints `## Unreleased`.
- [ ] `make precommit` exits 0.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```bash
make precommit
go test ./pkg/board/... -count=1
```

### Operator-executable (host, after merge)

⚠️ **production-touching:** the restart below replaces the running board process.

```bash
uv run pytest tests/test_topics_view.py -m integration
make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
curl -s --noproxy '*' 'http://127.0.0.1:8000/api/topics?vault=seibert-brogrammers' \
  | jq -r '.[] | "\(.title) | \(.phase) | \(.session_state)"'
```

Expected: the five named topics read `todo` with `session_state: null` (none has a live manager), and no card in `#cards-todo` carries a status badge.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Attention store unreachable | `indeterminate`, never `quiet`; no live indicator | Restore the store; the next snapshot refresh returns the real state. Observable: `session_state` flips from `indeterminate` to the real value on the next poll. |
| `manager_session_id` names a session with no local transcript and not in the store | `indeterminate`; no indicator | Expected — the card stays silent rather than claiming dead |
| Malformed (non-UUID) manager id | `indeterminate`; no indicator | Expected — correct the page data |
| A page carrying both a status that disagrees with its phase | Only the phase column speaks; no contradicting subtitle | Expected — that is the fix |
| Browser holds the old `app.js` | Old card keeps rendering the badge | The cache-buster token is bumped in the same change and a test pins the old token's absence |
| A topic page later gains a `claude_session_id` | Ignored — the card classifies from the manager id only | Accepted limitation, stated in Assumptions; extending it is a separate change |
| A parity fixture topic gains a `manager_session_id` | The Go and Python bodies diverge on `session_state` and `make parity` fails | Add the key to the fixture's expectation or keep fixtures manager-free — do not add `session_state` to the normalize list |

## Do-Nothing Option

Do nothing and the five topics keep reading as a contradiction, and no topic card can ever show that a manager is running for it. The cost is an unreadable board for exactly the containers the manager layer is scoped to.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | `session_state` on `TopicResponse`; `ListTopics` classification via the shared helper; the four-case table test; the wire-shape pin; Python model parity | 2 | AC1, AC2 | — |
| 2 | Topic card: drop the status badge, add the display-only indicator under its own class, cache-buster bump, CSS; rewrite `tests/test_topics_view.py` | 1, 3 | AC3, AC4, AC5, AC6, AC7 | prompt 1 |
| 3 | `README.md`, `CHANGELOG.md`, the `docs/liveness-classification.md` sentence; `make precommit` | — | AC8–AC12 | prompt 2 |

One prompt covers all three here: the halves share a single card builder and a single test file, and splitting them would ship a half-rendered card between merges. The three rows above exist to show coverage, not to schedule separate runs.

**Scenario coverage — no new scenario.** The classification is reachable by `pkg/board` rows with fake signals, and the deployed half is covered by the operator probe.

## Security / Abuse Cases

None. The change reads operator-written frontmatter, adds no file, no auth path and no write surface; the endpoints stay read-only.

## Non-goals

- Do not add `assignee` or `priority` to topic pages, or render them on the topic card — that is a schema decision for the operator.
- Do not converge the three card builders into one renderer. `createTaskCard`, `createGoalCard` and `createTopicCard` are three renderers; `cardShellHtml` is shared by the task card alone, and the goal card re-implements the same markup inline. Unifying them is a separate refactor.
- Do not add a take-over, start, resume or jump control to a topic card.
- Do not backfill `manager_session_id` on topic pages from code.
- Do not change the attention store, its heartbeat contract, or the session registry.
- Do not add `session_state` to the parity normalize list to paper over a divergence.
