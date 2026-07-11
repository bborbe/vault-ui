---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-07-11T12:21:20Z"
generating: "2026-07-11T12:29:57Z"
prompted: "2026-07-11T12:29:57Z"
verifying: "2026-07-11T12:36:27Z"
branch: dark-factory/goal-durable-starting-flag
---

## Summary

- Goals lack a durable "Starting…" signal: v0.49.0 shipped a per-tab JS hint (`startingGoals`) that stops the ▶ Start button flashing back mid-mint, but only in the clicking tab.
- Reload the page or open a second tab during the multi-second Claude mint and the goal button shows Start again, because goals never persist the `claude_session_started` frontmatter flag that tasks already have.
- This spec surfaces `claude_session_started` for goals end-to-end on the **backend only** — the frontend `createGoalCard` already reads `goal.claude_session_started` and just needs the API to send it.
- The change mirrors the existing task-side lifecycle exactly: set the flag before minting, clear it on mint failure / session reset / stale-session cleanup, and read it from the status cache when listing goals.
- Parent task: [[Durable Starting State for Goal Cards in Vault UI]].

## Problem

When an operator clicks ▶ Start on a goal card, minting the Claude session takes several seconds. The button must read "Starting…" for that whole window. Today that state lives only in a browser-local JavaScript set (`startingGoals`), so it is lost the moment the page reloads and is invisible to any other open tab — those views render ▶ Start as if nothing were happening, inviting a duplicate click. Tasks already solved this with a durable `claude_session_started` frontmatter flag; goals were left with only the optimistic per-tab hint. The two code paths have drifted, and the goal card cannot honestly report its own in-flight state.

## Goal

The `claude_session_started` flag is a durable, cross-tab source of truth for goals, exactly as it already is for tasks. After a goal's session mint begins, every view of that goal — a fresh page load, a second browser tab, a poll/WebSocket re-render — shows "Starting…" until either the `claude_session_id` lands (button becomes Resume) or the mint fails / the session is reset / a stale session is cleaned up (flag clears, button returns to Start). The goal and task lifecycles for this flag are behaviorally identical.

## Non-goals

- Do NOT touch the frontend (`src/vault_ui/static/app.js`). `createGoalCard` already computes `isStarting = !hasSession && (!!goal.claude_session_started || startingGoals.has(goal.id))` and ships in v0.49.0.
- Do NOT change any task-side behavior — the task path is the reference, not a target.
- Do NOT remove or alter the `startingGoals` JS set. It stays as the optimistic per-tab hint alongside the durable flag, exactly as `startingTasks` coexists with the task flag.
- Do NOT modify vault-cli. Use the existing goal field surfaces (`set_goal_field` / `clear_goal_field`, and the `goal clear` subprocess already used by `clear_goal_session` / cleanup).
- Do NOT add a config flag, opt-out, or tunable to enable/disable the flag — it is an invariant of goal lifecycle, mirroring tasks. If a future consumer needs variation, that is a separate spec.

## Desired Behavior

1. `GET /api/goals` returns each goal's `claude_session_started` value, surfaced from the status cache's `get_session_started(vault, goal_id)` (the same direct-frontmatter-read path the task list already uses), with the same "cache is authoritative, fall back to whatever the parse had" precedence as the task path.
2. `POST /api/goals/{goal_id}/run` sets `claude_session_started=true` on the goal frontmatter **before** minting the session, so any concurrent view already reads "Starting…" while the mint is in flight.
3. If the goal mint fails, `POST /api/goals/{goal_id}/run` clears `claude_session_started`, so the card returns to Start rather than sticking on "Starting…".
4. `DELETE /api/goals/{goal_id}/session` clears `claude_session_started` in addition to `claude_session_id`, so a session reset returns the card to Start.
5. The background stale-session cleanup, when it clears a stale goal's `claude_session_id`, also clears that goal's `claude_session_started` (when present), keeping the flag in lockstep with the session-id lifecycle.

## Constraints

- `GoalResponse` declares `model_config = {"extra": "forbid"}`. The new `claude_session_started: str | None = None` field must be added to the model; existing tests asserting the exact `GET /api/goals` response shape must be updated in lockstep, and no other field may change.
- The flag's value is normalized to `"true"` or `None` — never the string `"false"` — matching `StatusCache._extract_fields` and the task path. The status cache already extracts `claude_session_started` generically from any frontmatter, so no cache change is required.
- vault-cli is frozen: goal writes go through `set_goal_field` / `clear_goal_field` (or the existing inline `goal clear` subprocess in `clear_goal_session` and `cleanup.py`) — no new subcommands, no direct frontmatter writes.
- The `goal_id` argument-injection guard (reject IDs starting with `-` before any subprocess) already present on the goal run/clear endpoints must remain and must run before any new `set_goal_field` call.
- The existing goal `/run` behavior (mint, store `claude_session_id`, return resume command, 400/404/500 mapping) must not regress; the new flag write is additive.
- Reference implementations to mirror (task side, same repo): flag surfacing `tasks.py:595-602`; run set/clear `tasks.py:839,850`; clear-session `tasks.py:1524`; cleanup `cleanup.py:113-128`.
- **Goal-vs-task structural divergence (do NOT blindly copy the task line):** the task path sets `task.claude_session_started = get_session_started(...) or task.claude_session_started` because the `Task` dataclass HAS that field. The `Goal` dataclass has **no** `claude_session_started` field and `_parse_goal` never populates it. So `_process_goal_vault` must call `get_status_cache().get_session_started(vault, g.id)` and thread the value into `_goal_to_response` (add a parameter), setting it on the `GoalResponse` directly — do not reference a nonexistent `Goal.claude_session_started`.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---------|-------------------|----------|-----------|---------------|-------------|
| Goal mint (`vault-cli goal work-on`) fails after flag set | Endpoint clears `claude_session_started`, returns HTTP 500 | Card returns to Start; operator retries | HTTP 500 body names goal id + vault; frontmatter has no `claude_session_started` | Reversible (flag cleared) | Clear wrapped so a clear failure does not mask the original mint error |
| `set_goal_field("claude_session_started","true")` itself fails before mint | Endpoint surfaces HTTP 500; no session minted | Card stays at Start; operator retries | HTTP 500 | Reversible (nothing written) | n/a |
| Status cache has no entry for the goal (not yet loaded / just created) | `GET /api/goals` falls back to the goal's parsed value (or `None`); no error | Next cache load/invalidate surfaces the true value | Response field is `null`, HTTP 200 | n/a | Cache load is atomic per vault |
| Stale-session cleanup clears `claude_session_id` but the follow-up flag clear fails | Cleanup logs the failure and continues; session-id clear already succeeded | Next cleanup pass (≤5 min) or a `DELETE session` clears the flag | Cleanup log line for the goal | Partial (id cleared, flag lingers) | Cleanup pass is idempotent; re-runs converge |
| Two tabs both click Start on the same session-less goal | Both set the flag "true" (idempotent); first mint wins the `claude_session_id`, second is a redundant mint | Card flips to Resume once an id lands | Both requests HTTP 200/500 independently | Reversible | Idempotent flag write; duplicate mint is pre-existing behavior, out of scope |

## Security / Abuse Cases

- Attacker-controllable input: the `goal_id` path segment and `vault` query param, both already flowing to vault-cli subprocesses on the existing goal endpoints.
- Trust boundary: `goal_id` reaches `vault-cli goal set/clear` as an argument. The existing guard rejecting `goal_id` starting with `-` (HTTP 400 before any subprocess) must gate the new `set_goal_field` call in `/run` exactly as it gates the mint today.
- No new hang/retry surface: the new writes reuse existing bounded subprocess paths (`clear_goal_session` already enforces a 10s timeout; the run path already maps failures to HTTP 500). No unbounded loop is introduced.
- No new data crosses a trust boundary: the flag value is a constant `"true"` written by the server, never echoed user input.

## Suggested Decomposition

Keep-whole spec (one independently-deployable behavior change), but the seams for the prompt-creator:

| Seam | Files | Desired Behaviors | Notes |
|------|-------|-------------------|-------|
| Model + list surfacing | `models.py` (`GoalResponse` field), `tasks.py` (`_goal_to_response` param + `_process_goal_vault` threading `get_session_started`) | DB #1 | Update existing `GET /api/goals` shape tests (`extra:forbid`) in lockstep |
| Run set/clear | `tasks.py` `POST /goals/{id}/run` (set before mint, clear on failure) | DB #2, #3 | Guard (`-` reject) before `set_goal_field`; clear wrapped so it can't mask the mint error |
| Session-reset + cleanup clear | `tasks.py` `DELETE /goals/{id}/session`, `cleanup.py` | DB #4, #5 | Mirror `clear_task_session` + task cleanup |

One prompt covers all three (tight mirror pattern); split only if the prompt-creator finds the AC set too large for one.

## Acceptance Criteria

- [ ] `GoalResponse` includes `claude_session_started: str | None = None` — evidence: `grep -n "claude_session_started" src/vault_ui/api/models.py` returns a line inside the `GoalResponse` model.
- [ ] `GET /api/goals` returns `claude_session_started` for a goal whose frontmatter has it set — evidence: API test asserts the response JSON for that goal has `claude_session_started == "true"` (sourced from a mocked status cache `get_session_started`).
- [ ] `GET /api/goals` returns `claude_session_started == null` for a goal without the flag — evidence: API test asserts the field is `None` in the response, HTTP 200.
- [ ] `POST /api/goals/{goal_id}/run` sets the flag before minting — evidence: API test with a mocked mint asserts `set_goal_field` (or equivalent) was called with `("claude_session_started", "true")` before the mint call, ordering verified.
- [ ] `POST /api/goals/{goal_id}/run` clears the flag when the mint raises — evidence: API test forces the mint to raise and asserts `clear_goal_field(goal_id, "claude_session_started")` was called and the response is HTTP 500.
- [ ] `POST /api/goals/{goal_id}/run` with a `goal_id` starting with `-` returns HTTP 400 with no subprocess call — evidence: API test asserts HTTP 400 and that no vault-cli mock (set/mint) was invoked.
- [ ] `DELETE /api/goals/{goal_id}/session` clears `claude_session_started` in addition to `claude_session_id` — evidence: API test asserts a `goal clear ... claude_session_started` invocation occurs alongside the `claude_session_id` clear; HTTP 200.
- [ ] Stale-session cleanup clears `claude_session_started` when it clears a stale goal's `claude_session_id` — evidence: cleanup unit test with a mocked goal carrying a stale session and the flag asserts a `goal clear ... claude_session_started` subprocess is issued after the id clear.
- [ ] Changed behavior has ≥80% test coverage and no real subprocess/network calls in tests — evidence: `make precommit` exits 0; coverage report shows the changed lines covered.
- [ ] `CHANGELOG.md` has an entry under `## Unreleased` describing the durable goal `claude_session_started` flag — evidence: `grep -n "Unreleased" CHANGELOG.md` precedes a bullet naming goal `claude_session_started`.

No new scenario: this behavior is reachable by API-level and cleanup unit tests with mocked vault-cli; no real Docker/cluster/`gh` is required, and the cross-tab/reload user journey is verified by the human end-to-end check below rather than an automated E2E scenario.

## Verification

```
make precommit
```

Expected: exit 0 (format + test + lint + typecheck all pass), with the new goal-flag tests present and green.

Human end-to-end (single environment: run the server on `:8001` for testing, then reinstall to `:8000`):

1. Click ▶ Start on a session-less goal; while it reads "Starting…", reload the page → the button still shows "Starting…".
2. While a goal is mid-mint, open a second browser tab on the board → that tab also shows "Starting…" for the goal.
3. After the session lands, the button becomes ▶ Resume in both tabs; choosing Reset Session returns it to ▶ Start.

## Do-Nothing Option

If we skip this, the goal card keeps the v0.49.0 per-tab-only behavior: correct in the clicking tab, but a reload or second tab still shows Start mid-mint, inviting duplicate mints and contradicting the task-card behavior operators already rely on. The inconsistency is a standing papercut, not a data-loss risk — acceptable only as a temporary state, which is why v0.49.0 explicitly flagged the durable flag as a follow-up.
