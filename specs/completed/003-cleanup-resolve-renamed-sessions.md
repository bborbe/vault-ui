---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-03-17T12:58:41Z"
verifying: "2026-03-17T13:15:12Z"
completed: "2026-03-17T14:08:50Z"
branch: dark-factory/cleanup-resolve-renamed-sessions
---

## Summary

- Renamed sessions (via Claude `/rename`) store a display name instead of a UUID as `claude_session_id`
- Resolution of display names to UUIDs happens eagerly at assignment time (API PATCH or file watch), not during cleanup
- When a `claude_session_id` is set and the value is not a UUID, the system scans `.jsonl` files for a matching `customTitle` and replaces it with the real UUID immediately
- Cleanup stays simple: it only checks UUID-based file existence. Non-UUID session IDs that were never resolved are cleared as stale.
- Net effect: renamed sessions are resolved instantly on assignment, cleanup has no scanning responsibility

## Problem

When a user renames a Claude session via `/rename`, the task stores the display name (e.g. "trading-alerts") as `claude_session_id`. The cleanup job looks for `trading-alerts.jsonl` which doesn't exist — the actual file is still named by UUID. The session gets incorrectly cleared, disconnecting the task from a live session. This causes vault-ui to lose track of active work.

## Goal

After this work, display-name session IDs are resolved to their real UUIDs immediately when assigned — not deferred to cleanup. Cleanup only deals with UUIDs and never scans `.jsonl` file contents. Renamed sessions are never incorrectly cleared.

## Non-goals

- Modifying the rename workflow itself
- Handling sessions across multiple machines or project dirs
- Persisting a display-name-to-UUID mapping cache across restarts
- Changing the cleanup loop interval or its public API

## Desired Behavior

1. **Eager resolution on assignment**: When a task's `claude_session_id` is set (via API PATCH or file watch), and the value is NOT a UUID, the system immediately attempts to resolve it by scanning `.jsonl` files for a matching `customTitle` entry.
2. **Match found at assignment → replace**: If a `.jsonl` file contains a `custom-title` entry matching the assigned display name, the task's `claude_session_id` is replaced with the UUID from that filename before being persisted. The caller receives the resolved UUID in the response.
3. **No match at assignment → keep as-is**: If no matching `.jsonl` file is found at assignment time, the display name is stored unchanged. The user may still be in the process of starting or renaming the session.

   > **Note:** Between a failed resolution at assignment time and the next cleanup pass (up to 300s), the unresolved display name persists as the `claude_session_id`. This is expected behavior, not a bug.
4. **Cleanup handles UUIDs only**: The cleanup loop checks each task's `claude_session_id`. If the value is a UUID, it checks whether the corresponding `.jsonl` file exists — same as today. If the file is gone, the session ID is cleared.
5. **Cleanup clears unresolved display names**: If a `claude_session_id` is still not a UUID at cleanup time (resolution never succeeded), cleanup clears it. The session was never resolved and is considered stale.
6. **UUID detection**: A session ID is a UUID if it matches the standard UUID format (8-4-4-4-12 hex pattern). Everything else is treated as a display name.

## Constraints

- The `.jsonl` file format is owned by Claude Code — treat it as read-only and unstable. Parse defensively (ignore malformed lines).
- `custom-title` lines have the shape `{"type":"custom-title","customTitle":"<name>"}` but additional fields may exist. The UUID comes from the `.jsonl` filename, not from inside the JSON.
- **JSON naming convention**: The JSON line type is `custom-title` (hyphenated), the field containing the display name is `customTitle` (camelCase).
- The cleanup loop interval (300s) and the overall cleanup API (`cleanup_stale_sessions` returning cleared count) must not change.
- Existing tests for UUID-based cleanup must still pass.
- Resolution at assignment time **mutates** the task's `claude_session_id` (replacing display name with UUID). This is intentional — it's a correction, not a side effect.
- The API PATCH response must reflect the resolved UUID, not the original display name, when resolution succeeds.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---------|-------------------|----------|
| `.jsonl` file contains malformed JSON lines | Skip that line, continue scanning | Log warning, don't crash |
| `.jsonl` file is unreadable (permissions) | Treat as "no match found" for that file | Log warning, continue to next file |
| Thousands of `.jsonl` files in project dir | Scan completes without blocking the API response | Resolution should complete within a few seconds for up to 5,000 .jsonl files |
| `custom-title` line has no `customTitle` field | Skip that line | Defensive key access |
| Two `.jsonl` files claim same custom title | First match wins | Log info about duplicate |
| Resolution fails mid-scan (e.g. I/O error) | Store display name as-is | Cleanup will clear it later if still unresolved |
| Session renamed after assignment | Task keeps the original UUID; re-assignment needed | User sets session ID again, triggering fresh resolution |

## Security / Abuse Cases

- **Path traversal in customTitle**: A `.jsonl` file could contain a `customTitle` value like `../../etc/passwd`. The resolver must never use `customTitle` to construct file paths or perform file system operations beyond the lookup comparison. It is only used as a string-matching key against `claude_session_id` values.
- **Extremely long strings**: A `customTitle` could be megabytes long. The parser must bound how much data it reads per line and per field to avoid memory exhaustion during resolution.
- **Malformed JSON**: `.jsonl` files may contain truncated writes, binary garbage, or non-UTF-8 bytes. Every line must be parsed independently; a bad line must not abort parsing of subsequent lines or other files.
- **Duplicate titles across files**: Multiple `.jsonl` files may contain the same `customTitle`. This must not cause crashes, infinite loops, or incorrect clearing. First match wins; duplicates are logged but otherwise harmless.
- **Denial of service via frequent PATCH**: Rapid re-assignment of display names could trigger repeated `.jsonl` scans. The system should tolerate this without locking up, but no rate-limiting is required in this spec.

## Acceptance Criteria

- [ ] Setting `claude_session_id="trading-alerts"` via API PATCH resolves it to the UUID from the matching `.jsonl` filename when a file contains a custom-title entry for "trading-alerts"
- [ ] The API PATCH response contains the resolved UUID, not the original display name
- [ ] Setting `claude_session_id` via file watch also triggers resolution
- [ ] When no `.jsonl` file matches at assignment time, the display name is stored as-is (not cleared)
- [ ] Cleanup clears a `claude_session_id` that is still a display name (non-UUID) — it was never resolved
- [ ] Cleanup checks UUID-based session IDs against `.jsonl` file existence — same behavior as today
- [ ] Cleanup does NOT scan `.jsonl` file contents — it only checks filenames
- [ ] Malformed `.jsonl` lines do not crash resolution
- [ ] Unreadable `.jsonl` files are skipped gracefully without affecting other files
- [ ] Path traversal strings in customTitle do not cause file system access beyond string comparison
- [ ] `make precommit` passes

## Verification

```
make precommit
```

## Do-Nothing Option

Users who rename sessions will have their `claude_session_id` cleared every 5 minutes, requiring manual re-assignment. Workaround: don't use `/rename`. This is annoying but not catastrophic — the task still exists, just loses its session link.
