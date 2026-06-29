---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-06-26T15:03:47Z"
generating: "2026-06-26T15:17:39Z"
verifying: "2026-06-26T16:33:20Z"
branch: dark-factory/vault-ui-goals-view
---

## Summary

- Task Orchestrator's Kanban board today shows tasks only; checking goal status forces a vault crawl (sort `23 Goals/`, eyeball 25+ files, 3-5× per planning session).
- This spec adds a single toggle at the top of the board that switches between Tasks and Goals, reusing the same status columns and live-update plumbing.
- Goals render read-only — cards link back to the Obsidian file for edits. No new columns, no merged view, no goal editing.
- The chosen view persists in the URL (`?view=tasks` default, `?view=goals`) so reloads and shared links land in the right view.
- vault-cli already exposes everything needed (`goal list --output json`, `watch --types goal`); changes are confined to Task Orchestrator.

## Problem

Operators use Task Orchestrator's Kanban board multiple times daily to see task state at a glance, but the same operators must drop into the vault and manually sort `23 Goals/` whenever they want to check goal state. That friction kills the casual "is this goal still on track" glance and causes goal status to drift silently between planning sessions. The Kanban habit doesn't transfer to the next altitude up.

## Goal

Operators see goal status in the Task Orchestrator Kanban board, in the same columns and with the same live-update behaviour as tasks today, by clicking a single toggle. The currently active view is encoded in the URL so reloads and shared links open in the intended view. No vault crawl is required to answer "what state is each goal in".

## Non-goals

- Editing goals from the UI (read-only first pass; status changes still happen in vault files).
- Goal-specific columns, phase, or any column-set difference from tasks.
- Goal hierarchy / theme grouping in the UI (flat list per status column).
- Cross-view filters or a merged stream (strict toggle, not union).
- Backfilling missing `page_type: goal` frontmatter in the vault (covered by ongoing vault hygiene).
- Renaming Task Orchestrator to vault-ui (separate, later goal).
- Adding any vault-cli surface — confirmed unnecessary by backend investigation.
- Do NOT add a flag to disable the toggle — invariant; if a future consumer demands tasks-only mode, that is a separate spec.

## Desired Behavior

1. The board renders a single, persistent toggle control above the columns that names the two views (Tasks / Goals) and shows which is active.
2. Activating "Goals" repopulates the columns with goal cards drawn from configured vaults, using the same status columns as tasks (`in_progress`, `next`, `backlog`, `completed`, `hold`, `aborted`).
3. The active view is reflected in the URL query string (`?view=tasks` default, `?view=goals`); a direct visit to `?view=goals` opens in the Goals view without flicker through the Tasks view first.
4. Each goal card links to the goal file in Obsidian (`obsidian://open?vault=...&file=...`); clicking opens the file in Obsidian.
5. Editing a goal's frontmatter in the vault (e.g. status flip, rename) updates the Goals view in-place within 2 seconds without manual refresh — same UX as tasks today.
6. Editing a goal does NOT cause the Tasks view to re-render, and editing a task does NOT cause the Goals view to re-render — caches and event routing are kind-scoped.
7. The Tasks view, its API, and its WebSocket behaviour are unchanged for any client unaware of the new view (no regression).
8. README documents the toggle and `?view=goals` URL; CHANGELOG carries a Goals-view entry; a release tag is cut and installable.

## Constraints

- vault-cli is treated as frozen. The implementation MUST NOT add or modify vault-cli commands. Use the existing `goal list --output json` and `watch --types goal` surface.
- The existing `/api/tasks` response shape and the existing WebSocket payload contract for task events MUST remain backwards-compatible for any consumer that does not opt into goal awareness (i.e. adding fields is allowed; renaming or removing existing fields is not).
- The existing status taxonomy is reused verbatim — no new status values, no goal-only columns.
- Goal cards are read-only; the UI MUST NOT expose any control that writes back to a goal file.
- `make precommit` MUST stay green in the changed module.
- Cross-references: see goal page [[Task Orchestrator Dashboard Switches Between Tasks and Goals]] and the four task pages it lists for scope split and per-prompt success criteria.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection |
|---------|-------------------|----------|-----------|
| `vault-cli goal list --output json` omits date fields (`defer_date`, `target_date`, `completed_date`) | Missing fields surface as `null` in the API response; card renders with available fields, null dates absent (no "1970" placeholder), no crash. Per the Constraints section vault-cli is frozen, so a per-goal `goal show` fallback is NOT introduced unless dogfooding shows null dates are unacceptable. | Card renders with available fields; null dates absent on the card | Endpoint returns HTTP 200 with `null` date fields; no 500s |
| `vault-cli` binary missing or returns non-zero | `/api/goals` returns HTTP 500 with a structured error mirroring the existing `/api/tasks` failure path; UI shows the same "vault unreachable" state it shows for tasks today | Operator fixes vault-cli install; reload restores view | HTTP 500; existing error log line emitted by `VaultCLIClient` |
| WebSocket dropped mid-session | Reconnect logic reuses the same path as tasks today; on reconnect the active view re-fetches (tasks OR goals, whichever is active) | Automatic on reconnect | UI shows the same reconnect indicator used today |
| Goal file edited while user is on Tasks view | Goals cache is invalidated; Tasks view does NOT re-render | Switching to Goals view shows the updated state | DOM: tasks columns unchanged; switching to Goals shows new state |
| User opens `?view=goals` before backend goal endpoint is ready (race during startup) | UI shows the same "loading" state used for tasks at startup; falls through to populated board once the endpoint is up | Automatic | UI loading indicator visible; no JS console error |
| Goal name collides with a task name | Event routing uses `item_kind` from the payload, not the name, so the correct view receives the update | n/a — distinguished by `item_kind` | WebSocket payload includes `item_kind: "task" \| "goal"` |
| Operator deep-links `?view=goals` and then bookmarks; vault is later renamed | URL stays valid (view selector is independent of vault); the per-vault filter behaves the same way it does for tasks | Operator re-selects vault | Same behaviour as tasks today |

## Security / Abuse Cases

- Goal cards render text from vault frontmatter (title, status). Existing rendering path for task cards MUST be reused so any existing escaping applies; do NOT introduce a new innerHTML path for goal cards.
- Goal-card click constructs `obsidian://` URLs from the goal's vault + relative path. URL parameters MUST be encoded the same way task-card Obsidian links are encoded today; no new shell or process invocation.
- `/api/goals` accepts the same query parameters as `/api/tasks` (`vault`, `status`, `assignee`, `defer_date`). Filter validation mirrors `/api/tasks` — unknown parameters rejected via the existing `extra="forbid"` Pydantic pattern. No new auth surface; the endpoint binds to the same loopback the existing API binds to.

## Acceptance Criteria

- [ ] `GET /api/goals?vault=Personal` against a running orchestrator returns HTTP 200 with a JSON array containing every goal in the Personal vault — evidence: HTTP status 200 AND `jq 'length'` ≥ 1 on the response.
- [ ] The response shape includes per-goal `status`, `priority`, `obsidian_url`, and date fields (`defer_date`, `target_date`, `completed_date`) that may be `null` but MUST be present as keys — evidence: `jq '.[0] | keys'` includes all six keys.
- [ ] `GET /api/tasks` response shape is byte-identical to pre-spec for any field that existed before — evidence: schema diff between pre/post spec captured in PR description; existing task UI loads without console error.
- [ ] WebSocket payload for goal-file events includes `item_kind: "goal"`; for task-file events includes `item_kind: "task"` — evidence: `wscat` or browser devtools shows the field on a sample event of each kind.
- [ ] Toggle control is visible above the Kanban columns and labelled with the two view names — evidence: `document.querySelector('[data-testid="view-toggle"]')` returns a non-null element AND its `innerText` contains both "Tasks" and "Goals".
- [ ] Clicking the toggle switches the rendered cards to the other kind AND mutates the URL to `?view=tasks` or `?view=goals` — evidence: `window.location.search` matches after click in a smoke run.
- [ ] Direct navigation to `?view=goals` lands on the Goals view without first flashing the Tasks view — evidence: on initial load `performance.getEntriesByType('resource')` filtered by URL containing `/api/tasks` returns length 0.
- [ ] Editing a goal's `status:` frontmatter in the vault moves the corresponding card to the new column within 2s without manual refresh — evidence: scripted edit + observed DOM change ≤2s; recorded in PR.
- [ ] Editing a task does NOT trigger a goals re-fetch; editing a goal does NOT trigger a tasks re-fetch — evidence: network panel / server log shows no `/api/goals` call after a task edit and vice versa.
- [ ] README contains the `?view=goals` URL and documents the toggle — evidence: `grep -n '?view=goals' README.md` returns ≥1 line.
- [ ] CHANGELOG `## Unreleased` (or the new release section) carries a Goals-view bullet — evidence: `grep -n -i 'goals view' CHANGELOG.md` returns ≥1 line.
- [ ] A new release tag of the form `vX.Y.Z` (semver) is pushed and `uv sync` against it succeeds locally — evidence: `git tag --list 'v*'` shows the new tag matching `v[0-9]+\.[0-9]+\.[0-9]+`; `uv sync` exits 0.
- [ ] `make precommit` exits 0 — evidence: exit code 0.

Scenario coverage: NO new scenario. Live-update behaviour is covered by unit + integration tests (mocked watcher event → DOM assertion). The behaviour does not require real Docker, a real cluster, or any tool outside the orchestrator process.

## Verification

```
# Backend
make precommit

# Live smoke against the Personal vault
make run &
curl -s 'http://127.0.0.1:8000/api/goals?vault=Personal' | jq 'length'   # ≥1
curl -s 'http://127.0.0.1:8000/api/tasks?vault=Personal' | jq 'length'   # ≥1, unchanged

# UI smoke (headless — mirrors AC evidence shapes)
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:8000/?view=goals'   # 200
curl -s -o /dev/null -w '%{http_code}\n' 'http://127.0.0.1:8000/?view=tasks'   # 200
# Browser devtools: load /?view=goals, run in console:
#   performance.getEntriesByType('resource').filter(r => r.name.includes('/api/tasks')).length
# Expect: 0
# Edit any goal's `status:` in the vault → card moves columns within 2s (covered by AC #8 scripted check).

# Release
git tag v0.X.Y && git push origin v0.X.Y      # semver vX.Y.Z form per AC #12
uv sync                                        # against the new tag
```

## Suggested Decomposition

Four prompts, matching the four already-planned task pages on the goal. Backend first (frontend has nothing to query without it), then UI toggle, then live-update routing, then docs+release.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Backend: `GET /api/goals` mirroring `/api/tasks`, Goal model + `_parse_goal()` field extension (date-field gap decision documented in PR) — see task page [[Add Goals API Endpoint to Task Orchestrator Backend]] | 2, 7 (no-regression half) | 1, 2, 3, 13 (make precommit half) | — |
| 2 | Frontend: top-of-board Tasks/Goals toggle + URL `?view=` plumbing + Obsidian-link goal cards — see task page [[Add Tasks Goals View Toggle to Task Orchestrator Frontend]] | 1, 2, 3, 4, 7 (UI half) | 5, 6, 7, 13 | prompt 1 |
| 3 | WebSocket routing: backend adds `item_kind` to payload; frontend dispatches to per-view cache so live updates land in the right view only — see task page [[Route Goal WebSocket Events to Task Orchestrator Goals View]] | 5, 6 | 4, 8, 9, 13 | prompt 2 |
| 4 | Docs + release: README + CHANGELOG + tag + local `uv sync` + dogfood against Personal vault — see task page [[Document and Release Task Orchestrator Goals View]] | 8 | 10, 11, 12 | prompt 3 |

Rationale: backend before UI so the toggle has something to query; live-update routing after the UI exists so the dispatch target is real; docs/release last so README and CHANGELOG reflect what actually shipped. Each prompt corresponds to exactly one already-written task page on the goal — no extra coordination required.

## Do-Nothing Option

Operators continue the manual vault crawl 3-5× per planning session (~2-3 min each) to check goal status. Goal state drifts silently between sessions and the Kanban habit stays stuck at the task altitude. Acceptable only as long as the operator is willing to keep doing the crawl; the friction is the explicit reason this goal exists.
