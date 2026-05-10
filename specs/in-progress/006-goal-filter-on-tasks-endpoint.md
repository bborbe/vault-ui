---
status: verifying
tags:
    - dark-factory
    - spec
approved: "2026-05-10T21:35:46Z"
generating: "2026-05-10T21:35:47Z"
prompted: "2026-05-10T21:41:19Z"
verifying: "2026-05-10T21:54:01Z"
branch: dark-factory/goal-filter-on-tasks-endpoint
---

## Summary

- Operators managing dozens of in-flight tasks across multiple goals need to focus the Kanban on one goal at a time.
- Today there is no way to ask "show me only tasks under [[Goal X]]"; users scroll and visually scan tags.
- This spec adds a `goal` filter to `GET /tasks` and a URL pass-through in the frontend (no UI controls).
- The model gains a `goals` field populated from the task's `goals:` frontmatter, with `[[wiki-link]]` brackets stripped at parse time.
- Lays the API/URL primitive for a future goal-board view that links a goal to its filtered task board.

## Problem

The Kanban board lists every task across every vault and assignee, with no way to slice by the goal a task belongs to. Operators tracking multiple goals in parallel cannot focus on the subset of work for one goal without manually eyeballing tags on each card. As the goal hierarchy grows, the board becomes unusable for goal-focused work. The `goals:` frontmatter exists on tasks already, but task-orchestrator ignores it: the Task model has no `goals` field, the parser does not read it, and the `/tasks` endpoint exposes no filter. A future goal-board view (out of scope) will need both the API filter and the URL primitive that this spec establishes.

## Goal

After this work, `GET /tasks` accepts an optional repeatable `goal` query parameter that restricts the response to tasks whose frontmatter `goals:` list contains any of the provided goal names (exact match, brackets stripped). The Kanban frontend reads and writes the `goal` parameter in the URL just like it already does for `vault`, `status`, and `assignee` — no new UI controls. Every URL that works today continues to work unchanged.

## Non-goals

- No goal-board view (a page that lists goals with task counts) — separate future spec.
- No click-to-navigate from a goal card to a filtered task board.
- No `/api/goals` endpoint to enumerate goal names — future, depends on the board view.
- No goal dropdown / picker in the Kanban header — URL-driven only.
- No reverse view (goals listed with their tasks).
- No partial / fuzzy / case-insensitive goal matching.
- No backfilling `goals:` frontmatter on existing tasks (operator-side hygiene).
- No change to how `goals:` is persisted or written back; task-orchestrator only reads it.

## Desired Behavior

1. `GET /tasks?vault=personal&goal=Eliminate%20Agent%20Task%20Rot` returns only tasks whose `goals:` frontmatter contains the exact entry `Eliminate Agent Task Rot` (with `[[` `]]` brackets stripped).
2. `GET /tasks?goal=A&goal=B` returns the union: every task whose `goals` list contains `A` OR `B`.
3. `GET /tasks?goal=A,B` returns the same response as the repeated form (comma-separated parity matching the existing four filters).
4. Mixed forms work: `GET /tasks?goal=A,B&goal=C` returns tasks matching `A`, `B`, or `C`.
5. When the `goal` parameter is present, tasks whose frontmatter has no `goals:` field (or an empty list) are excluded from the response.
6. When the `goal` parameter is omitted, the response is unchanged from today — tasks without a `goals:` field still appear.
7. The Task response model includes a `goals` field — a list of goal names with brackets stripped, or `None` when the frontmatter has no `goals:` entry. An empty `goals:` list in frontmatter is normalised to `None`.
8. The frontend parses `goal` from the URL on load, forwards it to `/api/tasks`, and preserves it through `updateURL` writebacks (drag-and-drop phase changes, assignee toggles, status changes, etc.).
9. The FastAPI-generated OpenAPI schema lists `goal` as an optional repeatable array (same shape as `vault`/`status`/`phase`/`assignee`).
10. Whitespace around comma-separated tokens is trimmed (`?goal=A, B` equivalent to `?goal=A,B`), matching the unified filter syntax shipped in spec 005.

## Constraints

- The existing four filters (`vault`, `status`, `phase`, `assignee`) must continue to work unchanged. All existing tests must pass without modification.
- The `_flatten_filter` helper introduced in spec 005 must be reused for the `goal` parameter — no duplicate comma-flatten / repeated-param parsing logic.
- The `goal` query parameter name is singular (matching `vault`, `status`, `phase`, `assignee`), even though the frontmatter field and the model field are plural (`goals`). The asymmetry is intentional: query-param name describes the filter dimension; the field name describes the data.
- Bracket stripping happens at parse time in `_parse_task`, so the model's `goals` field is always clean. Filter-time comparison is plain string equality.
- The canonical form of a goal identifier in the URL is the wiki-link target text verbatim, URL-encoded for spaces (e.g. `Eliminate%20Agent%20Task%20Rot`). No slugging, lowercasing, or normalisation.
- Empty `goal=` tokens are dropped (treated identically to spec 005's handling for `status` / `phase` / `vault`). There is no "tasks with no goal" sentinel in this spec — that semantic, if needed, would be a follow-up matching the assignee-empty-string pattern.
- Default behaviour (no `?goal=` URL param) shows all tasks regardless of goal — matches today exactly.
- `make precommit` must pass (Python format, lint, type-check, tests).
- No change to how tasks are persisted, watched, or cached.
- Adding the `goals` field to `TaskResponse` is a backwards-compatible additive change — no existing field is removed or renamed. Every consumer of `/api/tasks` continues to parse today's response shape; new consumers may opt in to read `goals`.

## Assumptions

- **vault-cli emits the `goals:` frontmatter list verbatim under the JSON key `goals`.** Verified shape: a list of strings, each typically wrapped in `[[ ]]` (wiki-link form). If vault-cli changes this representation (e.g. flattens brackets itself, or splits goals into a separate field), the parser logic must be re-validated.
- **The Task model is the right place for the `goals` field.** Today the model exposes `blocked_by: list[str] | None` from frontmatter using the same shape; `goals` mirrors that pattern.
- **No task has a goal name that contains literal `[[` or `]]` substrings.** Bracket stripping is a simple prefix/suffix removal; nested or partial brackets are an upstream data error.
- **FastAPI dependency injection binds repeated and array query parameters into a `list[str]` natively** — the same binding the four existing filters use.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `goals:` frontmatter missing on a task | `goals` field is `None`; task is included when no `goal` filter is set, excluded when any `goal` filter is set | None |
| `goals:` frontmatter present but empty list (`goals: []`) | Normalised to `None`; behaves identically to missing field | None |
| `goals:` entry without brackets (e.g. `goals: - "Eliminate Agent Task Rot"`) | Stored as-is (no brackets to strip); matched by `?goal=Eliminate%20Agent%20Task%20Rot` | None |
| `goals:` entry with only opening bracket (`[[Foo`) or only closing (`Foo]]`) | Stored as-is including the partial bracket — upstream data error; will not match a clean `?goal=Foo` query | Operator fixes the frontmatter |
| `goals:` entry is not a string (e.g. nested object) | Coerced to string via `str()` — same behaviour as `blocked_by` parsing in `vault_cli_client.py:207`. Bracket-strip is applied to the coerced result. | None |
| Empty `?goal=` token in URL (`?goal=,A`) | Empty token dropped; only `A` filters (matches spec-005 behaviour for `status`/`phase`) | None |
| Unknown goal name in query (no task matches) | Response is empty (or filtered to other matches); no error | Caller corrects value |
| Goal name contains URL-reserved characters | Caller URL-encodes; backend receives decoded value and compares verbatim against stored goal names | Caller fixes encoding |
| Whitespace-only token (`?goal= `) | Trimmed to empty, then dropped — equivalent to no token | None |

## Security / Abuse Cases

- The `goal` parameter crosses the HTTP trust boundary. Values are used only for in-memory string-equality filtering — never interpolated into a shell command, SQL, filesystem path, or vault-cli argument.
- An attacker can submit arbitrarily many repeated `goal` parameters or a long comma-separated list. Filtering cost is O(tasks * tokens * goals-per-task), bounded by the same asymptotic behaviour as the four existing filters. No new explicit limit is introduced; rate limiting belongs at the gateway.
- No new logging of user-supplied values beyond what is logged for the existing four filters.

## Acceptance Criteria

- [ ] `GET /tasks?vault=personal` returns the same task set before and after this change — no regression.
- [ ] `GET /tasks?vault=personal&goal=<goal-name>` returns only tasks whose `goals:` frontmatter (with brackets stripped) contains that exact name.
- [ ] `GET /tasks?goal=A&goal=B` returns the union of tasks matching `A` or `B`.
- [ ] `GET /tasks?goal=A,B` returns the same task set as `GET /tasks?goal=A&goal=B`.
- [ ] `GET /tasks?goal=A,B&goal=C` returns tasks matching `A`, `B`, or `C`.
- [ ] Tasks whose frontmatter has no `goals:` field are excluded when any `goal` filter is set.
- [ ] Tasks whose frontmatter has no `goals:` field appear normally when no `goal` filter is set.
- [ ] Each `TaskResponse` includes a `goals` field whose value is either `null` or a list of strings with `[[ ]]` brackets stripped.
- [ ] An empty `goals:` list in frontmatter serialises as `null` on the wire (not `[]`).
- [ ] The OpenAPI schema (`/openapi.json`) lists `goal` as an optional repeatable array, structurally identical to `assignee`, `status`, and `phase`.
- [ ] Whitespace around comma-separated `goal` tokens is trimmed.
- [ ] All-empty `?goal=` tokens behave as if the parameter were omitted.
- [ ] The frontend stores the goal filter in the same shape as the existing assignee filter — parses every `goal` value from `window.location.search` on load, forwards every value to `/api/tasks`, and re-emits all values from `updateURL`.
- [ ] Reloading `?vault=personal&goal=Eliminate%20Agent%20Task%20Rot` preserves the URL and shows a board filtered to that goal.
- [ ] A drag-and-drop phase change on a goal-filtered task leaves the `goal=` URL param intact after the writeback.
- [ ] All existing tests continue to pass without modification.
- [ ] New unit tests cover: parser (three frontmatter shapes — missing, empty list, populated wiki-link strings), filter (five query forms — single, repeated, comma, mixed, no-filter regression), and the OpenAPI-shape assertion.
- [ ] No new scenario / E2E test is added. Unit + FastAPI test-client integration coverage is sufficient — the frontend pass-through is mechanical state mirroring of the assignee pattern.

**Scenario coverage — NO new scenario.** Backend filter behaviour and parsing are fully reachable via unit + FastAPI test-client tests. The frontend URL pass-through duplicates an existing, already-tested pattern; no new E2E test is justified.

## Verification

```
make precommit
```

Manual smoke test against a running server (with the user's `~/Documents/Obsidian/Personal` vault):

1. `curl 'http://localhost:PORT/api/tasks?vault=personal' | jq 'length'` — record baseline count.
2. `curl 'http://localhost:PORT/api/tasks?vault=personal&goal=Eliminate%20Agent%20Task%20Rot' | jq '.[] | {id, goals}'` — confirm every returned task lists `Eliminate Agent Task Rot` in `goals`.
3. `curl 'http://localhost:PORT/api/tasks?vault=personal&goal=A&goal=B'` vs `curl 'http://localhost:PORT/api/tasks?vault=personal&goal=A,B'` — confirm identical id sets.
4. `curl 'http://localhost:PORT/api/tasks?vault=personal' | jq '[.[] | select(.goals == null)] | length'` — confirm tasks with no `goals` are present without a filter.
5. Open `/docs` (Swagger UI) and verify `goal` renders as a repeatable array under `GET /tasks`.
6. Open `http://localhost:PORT/?vault=personal&goal=<goal-name>` in a browser, drag a card to a different phase column, and confirm the URL still contains the `goal=` param after the writeback.

## Do-Nothing Option

Without this work, operators cannot focus the Kanban on a single goal as the goal hierarchy grows. Workarounds — visual scanning, multiple browser tabs filtered by other dimensions, or scripting against the unfiltered API and merging client-side — all push orchestration state into the user's head or into bespoke scripts. The follow-up goal-board view (which links a goal to its tasks) is blocked on this URL/API primitive: without a `?goal=` filter there is nowhere for a "view tasks for this goal" link to point. The change is small (one new model field, one parser branch, one filter, one frontend state variable) and naturally splits into a backend prompt and a frontend prompt. Doing nothing is not acceptable.
