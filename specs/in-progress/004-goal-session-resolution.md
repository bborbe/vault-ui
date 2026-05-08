---
status: prompted
tags:
    - dark-factory
    - spec
approved: "2026-05-08T11:38:22Z"
generating: "2026-05-08T11:38:57Z"
prompted: "2026-05-08T11:45:40Z"
branch: dark-factory/goal-session-resolution
---

## Summary

- Goals (`page_type: goal`) carry a `claude_session_id` frontmatter field, just like tasks
- Today task-orchestrator only auto-resolves display-name session IDs on tasks; goals are ignored
- This spec extends eager resolution + cleanup parity to goals
- Net effect: a goal with `claude_session_id: ai-knowledge-sharing` resolves to the matching `.jsonl` UUID exactly like the task path does

## Problem

Users (e.g. on the goal `[[Share AI Knowledge at Seibert]]`) set `claude_session_id` to a human-readable display name to group related Claude sessions across tasks and the parent goal. On task files this gets resolved to a UUID by task-orchestrator's `_try_resolve_task_session` and survives the cleanup loop. On goal files nothing happens — the display name persists indefinitely (no resolution) and there is no parity mechanism. This causes the goal to stay disconnected from its actual Claude session in any UI / tooling that reads it back, and makes the convention silently asymmetric ("works for tasks, breaks for goals").

## Goal

After this work, goals are first-class for session resolution: setting `claude_session_id` on a goal triggers the same display-name → UUID resolution as on a task, and stale UUIDs on goals are cleared by the same cleanup pass. The user's experience is "works the same on goals as on tasks" with no new configuration.

## Non-goals

- Adding new UI for goals (kanban etc.) — out of scope
- Extending session resolution to themes, objectives, or other page types
- Changing the resolution algorithm itself (`session_resolver.py` stays as-is)
- Persisting a display-name → UUID cache across restarts
- Adding a per-goal API endpoint mirror (`PATCH /goals/{id}/session`); UI integration is out of scope
- Modifying vault-cli's CLI surface beyond what's strictly required (see Constraints)

## Desired Behavior

1. **Cleanup parity**: The cleanup loop iterates over goals in addition to tasks. Same rules apply — UUID + missing `.jsonl` → cleared; non-UUID at cleanup time → cleared as unresolved; UUID with assignee mismatch → cleared.
2. **Eager resolution on goal changes**: When a goal's `claude_session_id` changes (via file watch or API mutation, whichever paths exist), the system attempts to resolve a non-UUID value against `.jsonl` `customTitle` entries. If a match is found, the value is replaced with the UUID before persisting.
3. **No-op when already a UUID**: If `claude_session_id` is already a UUID, no resolution is attempted.
4. **No match → keep as-is**: If no `.jsonl` file matches at resolution time, the display name is stored unchanged (cleanup will later clear it if it stays unresolved).
5. **Same `project_dir` derivation**: Goals use the same `derive_claude_project_dir(vault_path, session_project_dir)` as tasks — there is one project directory per vault, shared across page types.
6. **Cleanup-loop is the contract**: goal session resolution and cleanup happen exclusively during the periodic cleanup pass. Watcher integration is out of scope (separate spec); a goal session may take up to one cleanup cycle to resolve, and this is documented in CHANGELOG and visible in logs.
7. **Logging**: Goal events use the same `[Cleanup]` / `[Factory]` prefixes as tasks, with `goal` instead of `task` in the log message ("[Cleanup] Clearing session %s from goal %s ...").

## Constraints

- **vault-cli dependency**: requires `vault-cli goal list`, `vault-cli goal show`, `vault-cli goal set` — already present per `vault-cli goal --help`. Watcher support (`vault-cli goal watch`) is explicitly out of scope for this spec.
- The `claude_session_id` field on goals must use the same JSON / frontmatter convention as on tasks (string value).
- Existing task behavior must not change — tests for tasks must still pass unchanged.
- Cleanup interval (300s) and overall API stay the same. Adding goal iteration must not increase the loop's worst-case time noticeably (<2× current task pass on a vault with similar goal count).
- Reuse of code paths: the resolution helper (`session_resolver.py`) is page-type-agnostic and must not duplicate.
- No new config knobs unless strictly necessary — goals piggy-back on the existing per-vault `vault_cli_path`, `vault_path`, `session_project_dir`.
- Display-name validity rules (no `/` or `\`) apply identically to goals.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---------|-------------------|----------|
| `vault-cli goal list` not available | Log error, skip goal cleanup pass for that vault, continue with tasks | Operator updates vault-cli; behavior resumes |
| `vault-cli goal set` fails for a specific goal | Log warning, leave session-id as-is, continue with next goal | Next cleanup cycle retries |
| Goal file has malformed frontmatter | vault-cli surfaces the error; treat the goal as skipped | No crash; task pass unaffected |
| Goal assignee differs from `current_user` | Same as task: clear the session ID (it was a stale assignment) | User re-assigns when they pick the goal up |
| Vault has thousands of goals | Cleanup pass for goals stays bounded (linear scan, no per-goal `.jsonl` re-scan beyond the existing logic) | Acceptable on realistic vaults; not optimised |

## Security / Abuse Cases

- **Path traversal in display name**: Same protection as tasks — `customTitle` is never used to construct file paths, only string-compared. The existing `is_uuid` + invalid-char checks in cleanup apply identically to goals.
- **Goal frontmatter injection**: vault-cli is the only writer; task-orchestrator passes string values to `vault-cli goal set`. No shell interpolation must be introduced when extending this path.
- **Resource exhaustion via many goals**: Cleanup loop pass over goals must be bounded by the number of goals, not by `.jsonl` content scanning (resolution scanning happens only once per non-UUID encountered, same as tasks).

## Acceptance Criteria

- [ ] A goal with `claude_session_id="ai-knowledge-sharing"` and a matching `.jsonl` `customTitle` entry is updated to the UUID by the cleanup loop within one cleanup cycle
- [ ] A goal with `claude_session_id` set to a UUID whose `.jsonl` file no longer exists has the field cleared by cleanup
- [ ] A goal with a non-UUID `claude_session_id` and no matching `.jsonl` is cleared as unresolved at cleanup time (mirrors task behavior)
- [ ] A goal whose `assignee` differs from `current_user` has its session cleared, mirroring task behavior
- [ ] Goal resolution uses the same `session_resolver.resolve_session_id` function as tasks (no duplicate algorithm)
- [ ] Cleanup logs distinguish goal events from task events (`[Cleanup] ... goal %s` vs `... task %s`)
- [ ] Existing task-side tests still pass without modification
- [ ] New tests cover: goal resolved via cleanup, goal cleared on missing `.jsonl`, goal cleared on assignee mismatch, goal-set field error path, malformed goal skipped without aborting task pass
- [ ] CHANGELOG entry added describing goal session-id parity
- [ ] `make precommit` passes

## Verification

```
make precommit
```

Manual smoke:

1. Create a goal with `claude_session_id: ai-knowledge-sharing` in a configured vault.
2. Start a Claude session, run `/rename ai-knowledge-sharing`.
3. Wait ≤300s.
4. Re-read the goal frontmatter — `claude_session_id` should now be a UUID matching the `.jsonl` file.
5. Delete the `.jsonl` file.
6. Wait ≤300s.
7. Re-read the goal — `claude_session_id` should be empty.

## Do-Nothing Option

Users continue setting human-readable session IDs on goals, but the value never resolves. Workaround: paste the UUID manually after each rename. Annoying and error-prone, especially with the goal+task hierarchy where the same name is used in multiple places.
