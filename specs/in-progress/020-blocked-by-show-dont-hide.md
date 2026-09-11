---
status: prompted
approved: "2026-09-11T07:46:11Z"
generating: "2026-09-11T08:00:26Z"
prompted: "2026-09-11T08:00:26Z"
branch: dark-factory/blocked-by-show-dont-hide
---

## Summary

- The board currently removes tasks with an uncompleted `blocked_by` blocker from the visible set. Blocked work vanishes with no explanation and never gets unblocked.
- Blocked tasks and goals stay on the board, annotated with a "blocked by X" badge naming the blocker; clicking the badge navigates to the blocker card.
- Blocked state is computed from `blocked_by` frontmatter plus each blocker's status (derived), not from `status: hold`.
- Goals get `blocked_by` parsing parity with tasks.
- Unknown blocker names count as blocked (cannot verify the blocker is done), matching the vault-cli model.

## Problem

The dependency model exists as data (`blocked_by` frontmatter) but the UI hides the very signal it is supposed to surface. A task whose blocker is unfinished is silently dropped from the board, so the operator cannot see that work is waiting on other work, cannot click through to the blocker, and cannot distinguish "blocked" from "not on the board". Goals have no `blocked_by` support at all. The purpose of declaring dependencies — "it's clear we can't start the others until the first one is finished" — is defeated by hiding the evidence.

## Goal

The board shows the dependency graph: a blocked task or goal is visibly marked, names its blocker, and links to it — while unblocked work renders exactly as today.

## Non-goals

- Cross-kind blockers (task blocked by a goal, goal blocked by a task) — same-kind only.
- Auto-setting `status: hold` from `blocked_by` — block state stays derived; the Hold column keeps its existing status-driven behavior.
- Backend vault-cli model changes — covered by the vault-cli spec (blocked-by frontmatter model); this spec consumes `blocked_by` from the data vault-cli already emits or the status cache provides.
- Filtering/reordering controls for blocked items beyond the badge — no dedicated "blocked" column or sort.
- Migrating existing vault tasks to add `blocked_by`.

## Acceptance Criteria

- [ ] A task whose `blocked_by` names a blocker that is not `completed` renders on the board (:8001) with a "blocked by X" badge naming the blocker — the task was absent from the board before this change (state transition: present where previously filtered out).
- [ ] Clicking the "blocked by X" badge on a blocked task navigates to the blocker's card (HTTP/browser: navigation to the blocker task).
- [ ] A task whose blockers are all `completed` renders without the blocked badge, identical to a task with no `blocked_by` (negative evidence: no badge markup in the rendered card).
- [ ] A goal with `blocked_by` in frontmatter renders the same badge on the goals board (:8001), naming the blocker (artifact: rendered UI state).
- [ ] A task or goal whose `blocked_by` names a blocker whose file is missing or unreadable is treated as blocked and shows the badge on :8001 (cannot verify the blocker is done) (rendered state).
- [ ] A task with no `blocked_by` frontmatter renders byte-identically to pre-change rendering for non-blocked work (negative evidence: no visual delta on unblocked cards).

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — Python test suite + lint pass
- `make test` — unit tests cover blocked-state computation (uncompleted blocker → blocked, completed blockers → unblocked, unknown blocker → blocked, no field → unblocked)
- `grep -n 'blocked' src/vault_ui/static/app.js` returns lines ≥1 for the badge rendering

### Operator-executable (runs on the host after merge, spec verification ladder)

- Human click-through on :8001: create a fixture task with `blocked_by: ["[[Some Blocker]]"]` in frontmatter (and a matching blocked goal), confirm both render with the badge, click badge → blocker card opens
- `make test-integration` (host-side Playwright) passes against the dev server

## Desired Behavior

1. The board no longer removes tasks with uncompleted blockers from the visible set; instead each task carries a derived `blocked` flag and the names of its blockers.
2. A task is blocked iff at least one blocker's entity has `status != completed`. A blocker whose file is missing or has no parseable status counts as not completed (blocked).
3. The task card renders a "blocked by X" badge when the derived blocked flag is set; the badge lists all blocker names and is a clickable link. Clicking the badge navigates to the first not-completed blocker's card in the same board view.
4. The Goals view reads `blocked_by` from goal frontmatter (via the vault-cli JSON / status cache path used by tasks) and renders the same badge on goal cards.
5. The goal parsing path gains `blocked_by` parity with tasks — a goal's `blocked_by` frontmatter reaches the Goal model as a list, same as tasks.
6. Unblocked tasks and goals render exactly as today — the badge is the only addition for blocked items; the derived blocked state never mutates `status` or `phase`.

## Constraints

- This spec pairs with the vault-cli spec `blocked-by-frontmatter-model` (vault-cli-blocked-by/specs/) — the blocked-state semantics (derived, unknown=blocked, same-kind only) are shared; implement both in the same session.
- The badge and its navigation use the existing card component and event wiring in `app.js`/`style.css` — no new framework, no build step.
- The existing explicit `?status=` filter behavior is unchanged; blocked items are subject to the same status filters as any other card.
- `blocked_by` values are read from data vault-cli emits or the status cache provides; this spec adds no new backend endpoint.
- Follow vault-ui conventions: dataclasses in `src/vault_ui/api/models.py`, parsing in `src/vault_ui/vault_cli_client.py`, pytest unit tests, Playwright integration test host-side.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Blocker renamed/removed after `blocked_by` set | Task/goal shows blocked badge (unknown blocker = blocked) | Operator fixes `blocked_by` or completes the blocker; badge clears when all blockers complete |
| Circular `blocked_by` | Both show blocked badges; no recursion or hang (single-level status read) | Operator breaks the cycle |
| Blocker file present but status unreadable | Treated as blocked; no error on the board | Operator repairs the blocker file |
| Status cache misses a blocker's status | Treated as blocked (cannot verify) | Cache refresh resolves once the blocker file is readable |
| Status cache unavailable (full outage) | Tasks/goals with `blocked_by` render with the blocked badge (all statuses unknown → blocked); no crash; a WARN log line `status_cache_unavailable` is emitted once per refresh cycle so the operator can distinguish outage from genuine blocking | Cache recovers; board re-renders on next refresh / watch event |

## Security / Abuse

- The badge renders blocker names as plain text in the card; names are escaped by the existing text-rendering path (no HTML injection).
- Navigation target is a vault-local entity name resolved within the board's data set — no external URLs, no path traversal.
- No new network calls: blocked state uses data already loaded (status cache / vault-cli JSON).

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Backend: drop the hide-blocked filter, derive blocked flag + blocker names, `_parse_goal`/Goal model `blocked_by` parity, unit tests | 1, 2, 5, 6 | 1, 3, 5, 6 | — |
| 2 | Frontend: badge render + click-navigation in `app.js`/`style.css`, Playwright integration test | 3, 4 | 2, 4 | prompt 1 |

Rationale: prompt 1 establishes the data (blocked flag + blocker names reach the card model); prompt 2 renders and wires the badge on top.

## Do-Nothing Option

Blocked work stays invisible: operators cannot see what is waiting on what, blocked tasks are indistinguishable from "not on the board", goals never express dependencies, and the only way to notice a dependency is blocked is the vault itself. The hidden-filter behavior also silently contradicts the vault-cli model this feature pairs with.
