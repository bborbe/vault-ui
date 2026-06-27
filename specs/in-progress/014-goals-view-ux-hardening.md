---
status: prompted
tags:
    - dark-factory
    - spec
approved: "2026-06-27T12:03:51Z"
generating: "2026-06-27T12:04:06Z"
prompted: "2026-06-27T12:29:00Z"
branch: dark-factory/goals-view-ux-hardening
---

## Summary

- Spec 013 shipped the Tasks/Goals toggle (PR #14), but two days of dogfooding surfaced one correctness bug and three UX papercuts that block declaring the parent goal done.
- Correctness: tasks bleed into `?view=goals` after refresh or any sidebar interaction — the unit test that was meant to prevent this gives false negatives.
- UX: column headers are task-phase labels even when viewing goals (a `completed` goal lives under a column called "DONE"); a `groupBy=phase|status` selector lets the operator pick the right dimension per kind.
- Cleanups: drop the redundant "Open in Obsidian →" link from goal cards (title is already the link); drop the silently-ignored `goal=` filter param from `loadGoals()` requests.
- One PR covers all four; operator dogfoods the launchd install end-to-end before approving.

## Problem

The Tasks/Goals toggle landed in PR #14 and immediately exposed three problems the test suite did not catch:

1. **Cross-view leak**: the operator's 2026-06-27 screenshot shows `?view=goals` rendering task cards ("Start Day - 2026-06-27", "Add Goals API Endpoint…", "PR Review github - bborbe-agent…"). Spec 013 AC#9 ("no cross re-render") passes its unit test but production behaviour is broken — any sidebar interaction (vault switch, status filter change, assignee filter change, refresh button, periodic poll) triggers `loadTasks()` unconditionally and overwrites the columns. The Goals view's premise ("see only goals at a glance") collapses the moment the operator clicks anything in the sidebar.
2. **Confusing columns on Goals view**: goals have no phase, so the existing hard-coded `TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE` headers are nonsense for them. The current code maps goal `status` onto those columns (a `completed` goal appears under "DONE") which is wrong-axis labelling.
3. **Visual noise on goal cards**: every goal card renders a separate "Open in Obsidian →" link below the title even though the title itself is already an `obsidian://` link. PR #14 review flagged it; operator confirmed it after using the view.
4. **Misleading URL on `/api/goals` requests**: `loadGoals()` appends `goal=` query params that the endpoint does not accept (silently ignored). PR #14 bot review NIT.

Together these four block closing the parent goal "Task Orchestrator Display Tasks and Goals". They share a single code surface (`static/app.js` view dispatcher + goal-card renderer) and a single dogfood loop, so they ship as one umbrella spec rather than four separate ones.

## Goal

The Goals view is trustworthy enough for the operator to use as the primary "what's my goal state" surface: tasks never appear under it, column headers match the dimension being viewed, and the card chrome carries no redundant links or silently-ignored URL params. The operator installs the resulting release via `uv sync` + `launchctl kickstart`, exercises all four behaviours by hand, and approves the PR.

## Non-goals

- Renaming Task Orchestrator to vault-ui (covered by its own goal).
- Editing goals from the UI (read-only stays read-only; same constraint as spec 013).
- Goal hierarchy / theme grouping in the UI.
- Adding `groupBy=assignee` / `groupBy=priority` modes — only `phase` and `status` ship here.
- Persisting selector state in `localStorage` — URL is the single source of truth.
- Adding a real goal filter to `/api/goals` (the cleanup removes a dead client param; it does NOT add a new backend feature).
- Changing the WebSocket payload shape — `item_kind` already exists from spec 013 prompt 3; this spec only fixes how the frontend routes on it.
- Do NOT add a flag to disable the groupBy selector — invariant; if a future consumer demands a fixed grouping, that is a separate spec.

## Desired Behavior

1. The Goals view never contains task cards under any sequence of operator interactions: page refresh on `?view=goals`, vault switch, status-filter change, assignee-filter change, refresh-button click, periodic poll, or a WebSocket event for a task. Symmetrically, the Tasks view never contains goal cards.
2. The board header carries a `groupBy` selector with exactly two options, `phase` and `status`. Switching it re-renders the columns under the new grouping; the active value is reflected in the URL as `?groupBy=phase` or `?groupBy=status` and survives a refresh / deep-link.
3. Defaults: Tasks view opens with `groupBy=phase` (preserves pre-spec UX); Goals view opens with `groupBy=status`. The default applies only when the URL does not already specify `groupBy=`.
4. Under `groupBy=phase` the columns are `TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE`. Under `groupBy=status` the columns are `IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED`. The column set switches as a whole — no mixed labels.
5. On the Goals view under `groupBy=phase`, goals without a `phase` field land in a single "—" (unknown) column and the other phase columns render empty; the page does not crash and does not silently drop the goal.
6. Goal cards do NOT render a separate "Open in Obsidian" hint or link inside the card body. Clicking the card title still opens the goal file in Obsidian (no regression of the existing link behaviour).
7. `loadGoals()` does NOT append `goal=` query params when calling `/api/goals`. The Network panel shows a clean URL containing only parameters the endpoint accepts (`vault`, `status`, `assignee`).
8. A regression test exists that, with spec 013's code restored, fails — and passes after this spec's fix. The test exercises the actual leak path (a non-task-edit event firing while `currentView === 'goals'`), not just a WebSocket dispatch in isolation.
9. README documents the `?groupBy=` URL param and the selector; CHANGELOG carries an entry naming the four fixes; a new release tag of the form `vX.Y.Z` is cut and `uv sync` against it succeeds locally.

## Constraints

- The backend `/api/goals` and `/api/tasks` response shapes and the WebSocket payload contract (including `item_kind`) MUST stay backwards-compatible — additive changes only, no field renames or removals.
- vault-cli is frozen — no new flags, no new subcommands. The spec is frontend-and-test only on the orchestrator side; the only backend touchpoint allowed is changes to how `/api/goals` parses query params (rejecting unknown ones via the existing `extra="forbid"` Pydantic pattern is fine).
- The existing Tasks-view UX MUST be preserved: opening `/` or `?view=tasks` with no `groupBy=` param renders the phase columns it renders today.
- The existing status taxonomy (`in_progress / next / backlog / completed / hold / aborted`) and phase taxonomy (`todo / planning / execution / ai_review / human_review / done`) are reused verbatim — no new values.
- `make precommit` MUST stay green in the changed module.
- Cross-references: parent goal `[[Task Orchestrator Display Tasks and Goals]]`; precedent spec at `specs/in-progress/013-task-orchestrator-goals-view.md` (merged via PR #14, commit `37bcf16`); four umbrella task pages — `[[Fix Task Cards Leaking into Goals View on Task Orchestrator]]`, `[[Add GroupBy Selector to Task Orchestrator Kanban]]`, `[[Remove Redundant Open in Obsidian Link from Goal Cards]]`, `[[Remove Ignored Goal Filter Param from loadGoals]]`.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---------|-------------------|----------|-----------|
| Operator on Goals view changes a sidebar filter (vault / status / assignee) | The active view's loader runs (goals stay rendered as goals); columns are NOT clobbered by a tasks fetch | Automatic | No `/api/tasks` request appears in the Network panel while `currentView === 'goals'`; columns continue to contain only goal cards |
| WebSocket event arrives for a task while operator is on Goals view | Goals DOM is unchanged; no `/api/tasks` fetch is issued | Automatic | Network panel + server log show no `/api/tasks` call following the event |
| URL specifies `?groupBy=bogus` (unknown value) | UI falls back to the kind-default (Tasks → `phase`, Goals → `status`); the URL is rewritten to the resolved value | Automatic on load | `window.location.search` after load contains a valid `groupBy=` value |
| Goal lacks a `phase` field and view is `?view=goals&groupBy=phase` | Goal renders in a single "—" column; other phase columns render empty; no JS console error | Operator switches to `groupBy=status` if desired | DOM shows the "—" column populated; `console.error` empty for the load |
| Two browser tabs open on different views; user edits a task in the vault | Tasks-view tab updates; Goals-view tab does NOT re-render its columns with tasks | Automatic | Tab-A (tasks) DOM diff shows the edit; Tab-B (goals) DOM is byte-equal before/after |
| `/api/goals` request URL inspected | Query string contains only `vault`, `status`, `assignee` keys (no `goal=`) | n/a | Network panel shows clean URL; server access log records the same |
| `uv sync` after release tag | Resolves to the new version; orchestrator restarts cleanly under launchd | Operator reruns `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator`. **Reversibility**: a bad tag cannot be re-pushed at the same version (tags are immutable on the registry once consumed) — recovery is a new patch tag (`v0.X.Y+1`); a forced re-tag is explicitly out of bounds. | `uv sync` exits 0; `launchctl list` shows the service running on the new version |

## Security / Abuse Cases

- Goal cards still render text from vault frontmatter. The existing rendering path MUST be reused — do NOT introduce a new `innerHTML` write site when removing the redundant "Open in Obsidian" link. Removal happens by deleting the link element, not by rewriting card construction with raw HTML.
- `?groupBy=` value is read from the URL and used to pick a column set from a fixed in-code map. Unknown values fall through to the kind-default; the value MUST NOT be interpolated into the DOM, into a CSS selector, or into a fetch URL path.
- `/api/goals` keeps `extra="forbid"` so a future stray client param fails loudly rather than being silently ignored (the very class of bug fix #4 cleans up).

## Acceptance Criteria

- [ ] Initial load of `?view=goals` followed by each of the four sidebar interactions in turn leaves the columns containing only goal cards — verifier checks each sub-bullet independently; evidence per sub-bullet: `document.querySelectorAll('[data-card-kind="task"]').length` returns 0 after that interaction.
  - [ ] (a) vault selector changed
  - [ ] (b) status filter changed
  - [ ] (c) assignee filter changed
  - [ ] (d) refresh button clicked
- [ ] `?view=goals` Network panel shows ZERO requests to `/api/tasks` across the full interaction sequence above — evidence: `performance.getEntriesByType('resource').filter(r => r.name.includes('/api/tasks')).length` returns 0; recorded in PR.
- [ ] Triggering a WebSocket task event (e.g. edit a task file in the active vault) while on `?view=goals` does NOT cause `/api/tasks` to be fetched and does NOT mutate the goals DOM — evidence: server access log shows no `/api/tasks` line for the event window; DOM hash before/after is equal.
- [ ] A regression test in `tests/` fails when run against spec 013's code (`git revert` this spec, run test → red) and passes after the fix — evidence: PR description includes the red→green transcript; test name referenced in CHANGELOG entry.
- [ ] The header contains a `groupBy` selector with exactly the options `phase` and `status` — evidence: `document.querySelector('[data-testid="groupby-select"] option').length === 2` and option values are `phase` and `status`.
- [ ] Changing the selector mutates `window.location.search` to include the new `?groupBy=` value AND re-renders columns under the new grouping — evidence: URL match after change; column headers (`document.querySelectorAll('[data-column-header]')` text) match the expected set for the chosen grouping.
- [ ] Default behaviour: opening `/?view=tasks` (no `groupBy=`) renders phase columns; opening `/?view=goals` (no `groupBy=`) renders status columns — evidence: column-header text snapshot for both URLs in the PR.
- [ ] `/?view=goals&groupBy=phase` with at least one goal lacking a `phase` field renders that goal in a single "—" column; no JS console error — evidence: DOM contains a column with header text `—` and at least one card inside; `console.error` count for the page load is 0. `console.warn` is permitted (legitimate deprecation / fallback warnings exist in app.js today; tightening to warn-free is out of scope for this spec).
- [ ] Goal cards do NOT contain a "Open in Obsidian" link below the title — evidence: `document.querySelectorAll('[data-card-kind="goal"] a').length` is 1 (the title link) on a page with at least one goal card.
- [ ] Clicking the goal card title still opens the goal file in Obsidian — evidence: card title `href` matches `^obsidian://open\?vault=.+&file=.+` for every rendered goal card.
- [ ] `loadGoals()` request URL contains no `goal=` parameter — evidence: browser Network panel screenshot in PR; `grep -n "params.append('goal'" src/task_orchestrator/static/app.js` returns zero lines inside `loadGoals`.
- [ ] README documents the `?groupBy=` URL param and the selector — evidence: `grep -n 'groupBy' README.md` returns ≥1 line.
- [ ] CHANGELOG `## Unreleased` (or the new release section) names all four fixes — evidence: `grep -n -i -E 'leak|groupby|obsidian link|goal= param' CHANGELOG.md` returns ≥4 distinct lines.
- [ ] A new release tag matching `v[0-9]+\.[0-9]+\.[0-9]+` is pushed and `uv sync` against it exits 0 — evidence: `git tag --list 'v*' | tail -n1` shows the new tag; `uv sync` exit code 0.
- [ ] Operator dogfood: after `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator`, the operator exercises all four fixes by hand and posts a before/after screenshot pair in the PR — evidence: PR contains two screenshots (Goals view leak before/clean after) AND mentions selector toggling on both views.
- [ ] `make precommit` exits 0 in the changed module — evidence: exit code 0.

Scenario coverage: NO new scenario. The leak fix is a unit + integration test (mocked sidebar interaction → assert no `/api/tasks` fetch). The groupBy selector and the two cleanups are unit + DOM-level integration tests. None of these need real Docker, real `gh`, or a real cluster — the existing `test_websocket_routing.py` style is the right altitude. The dogfood step is a one-shot operator verification captured as a PR screenshot, not an automated scenario.

## Verification

```
# Backend + test suite
make precommit

# Regression-test red→green
git revert HEAD --no-commit                                  # back to spec 013 state
pytest tests/test_websocket_routing.py -k cross_view -x      # expect FAIL
git revert --abort
pytest tests/test_websocket_routing.py -k cross_view -x      # expect PASS

# Live smoke against the Personal vault
make run &
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:8000/?view=goals'              # 200
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:8000/?view=goals&groupBy=phase' # 200
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:8000/?view=tasks&groupBy=status'# 200

# Browser smoke (mirrors AC evidence shapes):
#   Load /?view=goals, click vault selector, status filter, assignee filter, refresh.
#   In devtools:
#     document.querySelectorAll('[data-card-kind="task"]').length    // expect 0
#     performance.getEntriesByType('resource').filter(r => r.name.includes('/api/tasks')).length  // expect 0
#   Toggle groupBy from status → phase → status; assert URL and column headers update.

# Release + operator dogfood
git tag v0.X.Y && git push origin v0.X.Y
uv sync                                                       # against the new tag
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator
# Operator visits 127.0.0.1:8000, exercises all 4 fixes, attaches before/after screenshots to PR.
```

## Suggested Decomposition

Four prompts, one per source task page. The leak fix lands first because the regression test is the binding contract for the rest of the work; the groupBy selector lands second because its rendering touches the same dispatcher the leak fix just hardened; the two cleanups land third together (both are small edits inside `loadGoals()` / `createGoalCard()`); docs + release wraps the umbrella.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Fix cross-view leak: replace unconditional `loadTasks()` call sites with view-aware dispatch; harden WebSocket task-event handler to no-op while `currentView === 'goals'`; write the regression test that exercises a sidebar interaction (not just a WS dispatch) — see task page `[[Fix Task Cards Leaking into Goals View on Task Orchestrator]]` | 1, 8 | 1, 2, 3, 4, 16 | — |
| 2 | Add `groupBy` selector + URL plumbing + column-set switch; remove the status→phase mapping when `groupBy=status` is active; handle the "no phase" case under `groupBy=phase` on Goals view — see task page `[[Add GroupBy Selector to Task Orchestrator Kanban]]` | 2, 3, 4, 5 | 5, 6, 7, 8, 16 | prompt 1 |
| 3 | Two cleanups together: remove the redundant "Open in Obsidian" link from `createGoalCard()`; remove the `goal=` param appends from `loadGoals()` — see task pages `[[Remove Redundant Open in Obsidian Link from Goal Cards]]` and `[[Remove Ignored Goal Filter Param from loadGoals]]` | 6, 7 | 9, 10, 11, 16 | prompt 2 |
| 4 | Docs + release + dogfood: README + CHANGELOG + tag + `uv sync` + operator screenshot in the PR — single PR for all three preceding prompts merged together | 9 | 12, 13, 14, 15 | prompt 3 |

Rationale: leak first (the test it adds is the contract every later prompt must keep green); groupBy second (touches the dispatcher the leak fix just stabilised); cleanups third (independent of the dispatcher, low-risk, batch them); docs/release last (must reflect what actually shipped). All four prompts land on the same PR — the operator verifies the whole bundle in one dogfood pass rather than four.

## Do-Nothing Option

The parent goal `[[Task Orchestrator Display Tasks and Goals]]` stays open indefinitely. The operator's screenshot showing tasks leaking into Goals view stays the current state of master, and the Goals view becomes a "looked nice in PR review but never trusted in practice" feature. Unacceptable — the bug invalidates the spec 013 acceptance criteria the operator already signed off on, and a half-trusted Goals view is worse than no Goals view at all because operators stop looking after the first cross-view bleed.
