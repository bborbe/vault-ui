# Page index

`pkg/pageindex` keeps the parsed pages of each vault folder in memory so a list
read can be answered without opening a vault file. It implements vault-cli's
`storage.PageStorage`, so it plugs in exactly where vault-cli reads a folder
today: filtering, sorting and blocked-state logic stay in vault-cli's list
operation, and vault-ui reads single page files through its own reader seam,
composed from vault-cli's exported `storage.ParseFrontmatterMap` and
`domain.NewPage`, so a parsed page is identical to what vault-cli's folder
listing returns.

One immutable snapshot (`[]*domain.Page`) is held per key — a vault root plus a
vault-relative pages dir. A published snapshot is never mutated; concurrent
readers share the same `*domain.Page` pointers read-only.

## Staleness bounds

- A warm read never touches disk. `ListPages` returns the published snapshot
  without any filesystem access.
- An external edit becomes visible after the watcher's ~100 ms debounce plus one
  re-read of that file.
- A missed watcher event is repaired by the rescan within `RescanInterval`
  (50 s) plus the poll granularity plus one stat-diff — under 60 s normally. If
  a listing or a read hangs, each storage call is bounded by `rebuildTimeout`
  (2 min), so the worst case is the rescan interval plus that timeout. The 60 s
  ceiling holds for every change to a file's presence, size, modification time
  or status-change time.
- Writes made through vault-ui are visible to the next read. The synchronous
  writes — `Run*`, `TakeOver*` and both `execute-command` routes — still mark
  their vault's tasks and goals keys stale before they return, including paths
  that write before failing. The queued writes (see
  [optimistic writes](optimistic-writes.md)) mark from the queue consumer, after
  the file is written and immediately before their `Publish*Updated` frame, so a
  client that re-fetches on the frame never sees stale data; a read in the
  in-flight window returns the pre-write value, which the board's overlay hides.
  `JumpTask` and `ReloadConfig` write nothing and mark nothing.
- The first read after a mark resolves it before serving: a per-file mark
  re-reads exactly the marked files, a folder-level mark runs one stat-diff, and
  every concurrent reader waits on that same work. Marking a key the index has
  never seen is a no-op: its first read builds it anyway.
- An event-driven (`RefreshFile`) or rescan-driven (`Refresh`) read never blocks
  `ListPages`: a reader is served from the current snapshot immediately.
- Topics folders are not watched. They refresh through the rescan loop and
  `POST /api/cache/reload`, which forces a full re-read of every key regardless
  of its `vault` parameter.
- A failed listing keeps serving the previous snapshot and logs the error with
  the key; the mark stays pending, so the next read retries.
- A process restart starts with an empty index. Cold reads wait for the startup
  build of their key rather than each starting their own.

## Incremental updates

- Every file read records a fingerprint — size, modification time and
  status-change time — taken from a stat that follows symlinks, including files
  that end up excluded. A symlinked page's fingerprint therefore tracks its
  target.
- `Refresh` and the rescan loop are a **stat-diff**: list the folder, compare
  each entry's fingerprint with the one recorded at its last read, and re-read
  only the entries that differ or are new. "Differs" means inequality, not
  "newer", so a restored older timestamp counts; a rewrite that preserves both
  size and modification time is caught through the status-change time. A
  stat-diff of an unchanged folder reads nothing and keeps the identical
  snapshot slice. An excluded file is warned about once per fingerprint change,
  so an unchanged broken file is not re-logged every pass.
- `MarkDirty(keys...)` marks a **folder** stale: the next read resolves it by
  that stat-diff, never by a full re-read.
- `MarkFileDirty(key, id)` marks **one file** stale, so the next read re-reads
  exactly it and lists nothing. It applies only when the id is exact: strip a
  leading `[[` and a trailing `]]` as vault-cli's lookup does, reject an empty
  name, a path separator or `..`, require the key's current snapshot to hold a
  page whose name equals it byte-for-byte, and require `<name>.md` to exist at
  mark time. Anything else — a case-different name, a case-insensitive
  filesystem, vault-cli's substring fallback — widens to a folder-level mark.
- `ForceReload()` is the operator escape hatch behind `POST /api/cache/reload`
  and the only path that ignores fingerprints: the next read of every known key
  re-reads every file. There is no periodic forced full re-parse and no
  configurable rescan interval or selectable fingerprint.

## Frame ordering

- A task/goal watcher event's WebSocket frame is sent only after a read that
  started after that event has been applied, so a client that re-fetches on the
  frame sees fresh data.
- A file's page in a published snapshot always comes from the most recently started
  read of that file; reads of different files never drop each other's results.
  Concurrent work on one folder shares at most one in-flight read plus one queued
  follow-up, so a burst collapses into that pair.
- There is no ordering guarantee across folders beyond vault-cli's own per-file
  delivery.
- Theme and objective frames are unchanged. Frames originating from routes are
  unchanged in content; the nine queued writes' frames are published by the
  vault's queue consumer after the file is written (see
  [optimistic writes](optimistic-writes.md)).
- The rescan never sends frames.

## Key derivation

A key is `NewKey(vault path, folder)`, which applies `filepath.Clean` to both
parts so equivalent paths produce identical keys.

- The read side derives the key from the board vault's `Path` plus
  `TasksFolder`, `GoalsFolder` or `TopicsFolder` — exactly the values vault-cli's
  `ListOperation` passes to `ListPages`.
- The event side is `pkg/watchrefresh`: the handler `factory.CreateWatcher`
  composes maps the watcher's vault name to the configured vault-cli vault and
  uses its `Path` plus `GetTasksDir()` or `GetGoalsDir()`.
- Both sides come from the same vault-cli config entry: `vaultconfig.BuildVaultConfig`
  sets `TasksFolder = TasksDir` (never empty) and `GoalsFolder = GetGoalsDir()`,
  so the two derivations match.
- A mismatch silently disables refresh — the key simply never matches and the
  folder keeps its rescan cadence. The watcher tests guard this.
