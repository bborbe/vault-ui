---
status: prompted
tags:
    - dark-factory
    - spec
approved: "2026-05-10T15:37:31Z"
generating: "2026-05-10T15:37:32Z"
prompted: "2026-05-10T15:41:24Z"
branch: dark-factory/unify-task-list-filter-syntax
---

## Summary

- The `GET /tasks` endpoint exposes four filter parameters (`vault`, `status`, `phase`, `assignee`) that today each accept a different syntax — repeated, comma-separated, or single-value only.
- This spec unifies all four to accept both repeated and comma-separated forms, and combinations of the two.
- The `assignee` filter additionally accepts an empty string token to match unassigned tasks (tasks where the frontmatter `assignee` is missing or empty).
- Every URL that works today continues to work unchanged.
- Enables board views like "tasks for bborbe OR unassigned" via `?assignee=,bborbe`, which is required by upcoming work to surface escalated agent tasks alongside operator-owned tasks.

## Problem

The four task filters on `GET /tasks` use inconsistent query syntaxes today: `vault` is repeated-only, `status` and `phase` are comma-separated-only, and `assignee` is single-value-only. Callers cannot combine values for `assignee`, and there is no way to ask for "tasks with no assignee" — let alone combine that with a named assignee in one request. The Kanban board needs a single-request way to show "my tasks plus everything currently unassigned" so escalated agent tasks (which land with an empty `assignee`) become visible to the operator. The current API forces multiple requests or client-side merging.

## Goal

After this work, all four list filters on `GET /tasks` accept both syntaxes (`?x=a,b` and `?x=a&x=b` and any mix). The `assignee` filter additionally treats an empty string in the value list as "unassigned", matching tasks whose frontmatter `assignee` is missing or empty. Every URL the API accepted before this change still produces the same result.

## Non-goals

- No frontend / Kanban UI changes. The board can adopt the new syntax in a follow-up.
- No changes to `POST /cache/reload`'s `vault` parameter (that is a target selector, not a filter).
- No changes to single-value query parameters on other endpoints.
- No new filter dimensions (no filtering by tag, hierarchy, defer date, etc.).
- No change to the default behavior when a filter is omitted.
- No change to how tasks are persisted, watched, or cached.

## Desired Behavior

1. `GET /tasks?vault=a,b` and `GET /tasks?vault=a&vault=b` both return tasks from vaults `a` and `b`. `GET /tasks?vault=a,b&vault=c` returns tasks from `a`, `b`, and `c`.
2. `GET /tasks?status=todo,in_progress` and `GET /tasks?status=todo&status=in_progress` produce identical results, as do mixed forms.
3. `GET /tasks?phase=planning,human_review` and `GET /tasks?phase=planning&phase=human_review` produce identical results, as do mixed forms.
4. `GET /tasks?assignee=bborbe,alice` and `GET /tasks?assignee=bborbe&assignee=alice` return tasks assigned to either of those names.
5. `GET /tasks?assignee=` (empty value) returns tasks whose frontmatter `assignee` is missing or empty.
6. `GET /tasks?assignee=,bborbe` and `GET /tasks?assignee=&assignee=bborbe` return tasks assigned to `bborbe` plus tasks with no assignee, in one response.
7. Whitespace around values is trimmed: `?status=todo, in_progress` is equivalent to `?status=todo,in_progress`.
8. Omitting a filter parameter preserves today's behavior (no filtering on that dimension; status defaults to `todo,in_progress,completed`).
9. Existing single-value URLs (`?assignee=bborbe`, `?status=todo`, `?phase=planning`, `?vault=mine`) behave exactly as before.

## Constraints

- Must not change the response shape of `GET /tasks`.
- Must not change the default status filter (`todo,in_progress,completed`) when `status` is omitted.
- Must not change the phase-default-to-`todo` rule for tasks with a missing or invalid phase.
- Must not change the deferred-task visibility rules.
- Must not change the per-vault iteration pattern or which vault-cli calls are issued.
- The OpenAPI schema for `GET /tasks` must list each of the four filter parameters as an optional repeatable array.

## Assumptions

- **vault-cli collapses missing and empty `assignee` to `None`.** Verified by reading `src/task_orchestrator/vault_cli_client.py:240` (`assignee=data.get("assignee") or None`). If this changes, behaviors 5–6 (empty-token-matches-unassigned) must be re-validated against the new representation.
- **FastAPI dependency-injection is the binding mechanism.** The framework binds repeated and array query parameters into a `list[str]` natively; no custom parser is needed beyond the comma-flattening helper.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Empty filter token in `status` or `phase` (e.g. `?status=,todo`) | Empty token is dropped; only non-empty values filter | None — caller adjusts URL if surprised |
| All tokens empty (e.g. `?status=,,`) | Treated as if filter was omitted (falls back to default behavior for that filter) | None |
| Unknown status or phase value | Filter applied as-is; no matches returned for that value (existing behavior) | Caller corrects value |
| Unknown vault name | Vault is skipped silently (existing behavior) | Caller corrects value |
| Whitespace-only token (`?assignee= `) | Treated as empty token, i.e. matches unassigned tasks | Caller omits or removes the token |
| Very long repeated parameter list | Behaves the same as a comma-separated list of equivalent length; no special limits introduced | None |

## Security / Abuse Cases

- All four parameters cross the HTTP trust boundary. Values are used only for in-memory filtering (Python list membership and string comparison) — no value is interpolated into a shell command, SQL, or filesystem path. The vault-cli subprocess receives status values as discrete arguments via the existing `list_tasks` client, unchanged by this spec.
- An attacker can submit arbitrarily many repeated query parameters or very long comma-separated lists. Filtering is O(n*m) over tasks and tokens; document that no new explicit limit is introduced and the behavior degrades linearly. If this becomes a problem, rate limiting belongs at the gateway, not in this filter.
- The empty-string assignee token must not be confused with "no filter". The contract is: parameter absent = no filter; parameter present with at least one non-empty token OR an explicit empty token = filter active. An all-empty list of tokens is equivalent to absent.
- No values are logged at info level beyond what is logged today.

## Acceptance Criteria

- [ ] `GET /tasks?vault=a&vault=b` returns the same task set as `GET /tasks?vault=a,b`.
- [ ] `GET /tasks?status=todo&status=in_progress` returns the same task set as `GET /tasks?status=todo,in_progress`.
- [ ] `GET /tasks?phase=planning&phase=human_review` returns the same task set as `GET /tasks?phase=planning,human_review`.
- [ ] `GET /tasks?assignee=bborbe&assignee=alice` returns the same task set as `GET /tasks?assignee=bborbe,alice`.
- [ ] `GET /tasks?assignee=` returns only tasks whose `assignee` field is missing or empty.
- [ ] `GET /tasks?assignee=,bborbe` returns the union of unassigned tasks and tasks assigned to `bborbe`.
- [ ] `GET /tasks?assignee=bborbe` (single value, pre-existing URL) returns exactly the tasks assigned to `bborbe` and nothing else.
- [ ] Omitting `status` still applies the default `todo,in_progress,completed` filter.
- [ ] Whitespace around comma-separated tokens is trimmed in all four parameters.
- [ ] An all-empty filter (e.g. `?status=`) behaves as if the parameter were omitted.
- [ ] FastAPI's generated OpenAPI schema lists all four parameters as optional, repeatable arrays.
- [ ] Existing tests for `GET /tasks` continue to pass without modification.
- [ ] New unit tests cover: comma-only, repeated-only, mixed, single-value, empty-token assignee, whitespace, and all-empty cases.
- [ ] No new scenario/E2E test is added — unit and integration tests at the FastAPI test-client level fully cover the behavior.

## Verification

```
make precommit
```

Manual smoke test against a running server:
1. `curl 'http://localhost:PORT/tasks?assignee=bborbe'` — confirm only bborbe's tasks return.
2. `curl 'http://localhost:PORT/tasks?assignee='` — confirm only unassigned tasks return.
3. `curl 'http://localhost:PORT/tasks?assignee=,bborbe'` — confirm union of the above two.
4. `curl 'http://localhost:PORT/tasks?status=todo&status=in_progress'` and `curl 'http://localhost:PORT/tasks?status=todo,in_progress'` — confirm identical responses.
5. Open `/docs` (FastAPI Swagger UI) and verify all four parameters render as repeatable arrays.

## Do-Nothing Option

Without this work, the upcoming "make stalled PR-reviewer tasks visible to operator" task cannot present the operator with a single board view that includes both their own tasks and escalated/unassigned ones. Workarounds (two requests merged client-side, or always assigning escalated tasks to a sentinel name) leak orchestration state into the UI and make the API harder to use from other clients. The current asymmetry across the four filters is also a long-standing papercut for anyone scripting against the API. Doing nothing is not acceptable, but the change is small enough to be done in a single PR.
