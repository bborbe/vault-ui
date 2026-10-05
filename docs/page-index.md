# Page index

`pkg/pageindex` keeps the parsed pages of each vault folder in memory so a list
read can be answered without opening a vault file. It implements vault-cli's
`storage.PageStorage`, so it plugs in exactly where vault-cli reads a folder
today: filtering, sorting and blocked-state logic stay in vault-cli's list
operation and vault-ui never parses a page file itself.

One immutable snapshot (`[]*domain.Page`) is held per key — a vault root plus a
vault-relative pages dir. A published snapshot is never mutated; concurrent
readers share the same `*domain.Page` pointers read-only.

## Staleness bounds

- A warm read never touches disk. `ListPages` returns the published snapshot
  without any filesystem access.
- An external edit becomes visible after the watcher's ~100 ms debounce plus one
  rebuild of that folder.
- A missed watcher event is repaired by the rescan within `RescanInterval`
  (50 s) plus the poll granularity plus one rebuild — under 60 s normally. If a
  rebuild hangs, each storage call is bounded by `rebuildTimeout` (2 min), so the
  worst case is the rescan interval plus that timeout.
- Writes made through vault-ui are visible to the next read: the write marks the
  affected keys dirty and the first read performs one shared rebuild that every
  concurrent reader waits on.
- Topics folders are not watched. They refresh through the rescan loop and
  `POST /api/cache/reload`.
- A failed rebuild keeps serving the previous snapshot and logs the error with
  the key; the next event, dirty read or rescan retries.
- A process restart starts with an empty index. Cold reads wait for the startup
  build of their key rather than each starting their own.

## Frame ordering

- A task/goal watcher event's WebSocket frame is sent only after a rebuild that
  started after that event has been swapped in, so a client that re-fetches on
  the frame sees fresh data.
- Events for the same folder share at most one in-flight rebuild plus one queued
  follow-up; a burst of events collapses into that pair.
- There is no ordering guarantee across folders beyond vault-cli's own per-file
  delivery.
- Theme and objective frames, and frames originating from routes, are unchanged.
- The rescan never sends frames.

## Key derivation

A key is `NewKey(vault path, folder)`, which applies `filepath.Clean` to both
parts so equivalent paths produce identical keys.

- The read side derives the key from the board vault's `Path` plus
  `TasksFolder`, `GoalsFolder` or `TopicsFolder` — exactly the values vault-cli's
  `ListOperation` passes to `ListPages`.
- The event side maps the watcher's vault name to the configured vault-cli vault
  and uses its `Path` plus `GetTasksDir()` or `GetGoalsDir()`.
- Both sides come from the same vault-cli config entry: `vaultconfig.BuildVaultConfig`
  sets `TasksFolder = TasksDir` (never empty) and `GoalsFolder = GetGoalsDir()`,
  so the two derivations match.
- A mismatch silently disables refresh — the key simply never matches and the
  folder keeps its rescan cadence. The watcher tests guard this.
