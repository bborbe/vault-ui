# Page index

`pkg/pageindex` keeps the parsed pages of each vault folder in memory so a list
read can be answered without opening a vault file. It implements vault-cli's
`storage.PageStorage`, so it plugs in exactly where vault-cli reads a folder
today: filtering, sorting and blocked-state logic stay in vault-cli's list
operation, and vault-ui reads single page files through its own reader seam,
which delegates the read to vault-cli's `storage.PageStorage.ReadPage`, so a
parsed page is identical to what vault-cli's folder listing returns. The read is
delegated so that one parse path and one symlink-out-of-vault guard serve both
the index and vault-cli's own folder listing, instead of two copies that have to
be kept in sync.

One immutable snapshot (`[]*domain.Page`) is held per key — a vault root plus a
vault-relative pages dir. A published snapshot is never mutated; concurrent
readers share the same `*domain.Page` pointers read-only.

## Task-list snapshot

The board holds one immutable **task-list snapshot** per page-index key — the
same key, a vault root plus its tasks folder. It is a second published snapshot,
derived from the page snapshot and the session snapshot; it is never a field on
`domain.Page` and is never mutated after publication. `GET /api/tasks` serves it.

- **Rebuild triggers.** A key's task-list snapshot is rebuilt when the page
  index's revision for that key moves (a publication or a mark) or when a new
  session snapshot is available (the session refresh). A rebuild that started
  before an invalidation does not clear it: the build records the revision and
  the generation it saw at build start, so a racing invalidation leaves the entry
  dirty and the next read rebuilds.
- **Atomic swap.** A rebuild produces a complete new row list and swaps it in
  atomically; a reader concurrent with a rebuild sees either the whole previous
  list or the whole new one, never a mixture.
- **Cold-read sharing.** Two concurrent first reads of a key with no snapshot
  share exactly one build; every waiter observes its result.
- **Failed rebuild.** A failed rebuild keeps serving the previous snapshot and
  logs the error with the key; the next event, dirty read or refresh retries. A
  fresh process starts empty and builds on demand.
- **One accepted cost.** A rebuild's own page read can resolve a pending write
  mark, which moves the revision, so a write can cost one extra rebuild on the
  next read. The extra build lists an unchanged folder and classifies from the
  cached session snapshot.
- **No new timer.** The task-list snapshot owns no timer; the only timer this
  change adds is the session refresh at ≥ 60 s.

## Staleness bounds

- A warm read never touches disk. `ListPages` returns the published snapshot
  without any filesystem access. A warm `/api/tasks` read likewise returns the
  published task-list rows and performs no page-storage call, no transcript probe
  and no process spawn.
- An external edit becomes visible after the watcher's ~100 ms debounce plus one
  re-read of that file. An event whose path is not a single plain filename
  directly inside its folder costs one stat-diff of that folder instead.
- A missed watcher event is repaired by the rescan within `RescanInterval`
  (50 s) plus the poll granularity plus one stat-diff — under 60 s normally. If
  a listing or a read hangs, each storage call is bounded by `rebuildTimeout`
  (2 min), so the worst case is the rescan interval plus that timeout. The 60 s
  ceiling holds for every change to a file's presence, size, modification time
  or status-change time.
- Writes made through vault-ui are visible to the next read. The queued writes
  (see [optimistic writes](optimistic-writes.md)) mark one item's file from the
  queue consumer, after the file is written and immediately before their
  `Publish*Updated` frame, so a client that re-fetches on the frame never sees
  stale data; a read in the in-flight window returns the pre-write value, which
  the board's overlay hides. The eight synchronous sites — `Run*`, `TakeOver*`
  and both `execute-command` routes — keep the folder-level mark of their
  vault's tasks and goals keys, before they return, including paths that write
  before failing. `JumpTask` and `ReloadConfig` write nothing and mark nothing.
- The write-visibility rule now runs the whole chain: a synchronous or queued
  write marks the page-index key before its `Publish*Updated` frame, the mark
  moves the key's revision, and the next read of that key's task-list snapshot
  rebuilds; the rebuild's vault-cli list walk resolves the pending page mark
  before reading. A watcher event's page read likewise moves the revision before
  its frame is broadcast. No stronger bound than that is claimed.
- The page-derived task-list fields keep the bounds above. The session-derived
  fields — `session_state` and the activity date — carry the **session-refresh
  bound** instead: they come from the session snapshot, which is refreshed on a
  fixed interval of at least 60 s (`SessionRefreshInterval`), so they can lag
  reality by at most one refresh plus one rebuild. This is the one place the
  bounds above no longer apply.
- The first read after a mark resolves it before serving: a per-file mark
  re-reads exactly the marked files, a folder-level mark runs one stat-diff, and
  every concurrent reader waits on that same work — a reader of a key with a
  pending write mark does not return until the marked files have been re-read.
  Marking a key the index has never seen is a no-op: its first read builds it
  anyway.
- An event-driven (`RefreshFile`) or rescan-driven (`Refresh`) read never blocks
  `ListPages`: a reader is served from the current snapshot immediately.
- `POST /api/cache/reload` is the forced path: it ignores fingerprints, so the
  next read of every key re-reads every file, regardless of its `vault`
  parameter. It is the only path that does.
- Topics folders are not watched. They refresh through the rescan loop and
  `POST /api/cache/reload`.
- A failed listing keeps serving the previous snapshot and logs the error with
  the key; the mark stays pending, so the next read retries.
- A process restart loads each key's stored pages and fingerprints from the
  on-disk store and resolves exactly one stat-diff before that key is served,
  reading only the files that changed while the process was down. The stored
  snapshot is never served as it was written: cold reads wait for that stat-diff
  rather than each starting their own.

## Incremental updates

- A task or goal watcher event re-reads **only the file the event names** — the
  file's base name inside the event's key folder — and lists nothing. The file's
  current on-disk state decides the outcome whatever the event type says: present
  and readable replaces or inserts its page at its filename-ordered position,
  absent or unreadable removes it. An event whose path, relative to the key's
  folder, is not a single plain filename ending in `.md` falls back to a
  stat-diff of that key. Theme and objective events, and events for an unknown
  vault, touch no key at all.
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
  read of that file; reads of different files never drop each other's results. A
  folder no longer keeps a single in-flight rebuild and a queued follow-up:
  per-file reads are independent, so a burst of events for different files runs
  its reads concurrently, and each event's frame is sent only after a snapshot
  containing a read started after that event has been published.
- There is no ordering guarantee across folders beyond vault-cli's own per-file
  delivery.
- Theme and objective frames are unchanged. Frames originating from routes are
  unchanged in content; the nine queued writes' frames are published by the
  vault's queue consumer after the file is written (see
  [optimistic writes](optimistic-writes.md)).
- The rescan never sends frames.
- The task-list snapshot does not change this: it is derived from the page
  snapshot, so a task/goal watcher frame is still sent only after a read that
  started after that event has been applied, and the next `/api/tasks` read
  rebuilds on the revision that read moved.

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

## Page index store

The parsed page index is persisted on local disk as a cache, so a restart does
not have to re-parse every indexed file. The store is never a source of truth:
it is read as untrusted input, discarded on any mismatch, and never changes what
the board serves.

- **What it holds.** One entry per indexed file: the parsed page plus the
  pre-read fingerprint — size, modification time and status-change time — taken
  from the symlink-following stat before the read. A file excluded from the
  index (unreadable, unparsable, or a symlink out of the vault) stores its
  fingerprint and no page, so an unchanged excluded file is neither re-read nor
  re-warned on the next start.
- **Where it lives.** `<user cache directory>/vault-ui/page-index.bolt` — on
  darwin, under `~/Library/Caches` — never inside a vault and never inside the
  repository tree; its directory is created when absent. No configuration
  selects, relocates, disables or bounds it.
- **When it is written.** Whenever a snapshot is published, exactly the entries
  that publication changed — the files it re-read and the files that vanished —
  are written in a single transaction, outside the index mutex, so a reader
  never waits on store I/O. A failed write leaves the previous content intact
  and serving unaffected. No timer flushes the store.
- **When it is discarded.** A store that is missing, empty, damaged, unreadable,
  or written under a different store-format version or by a different vault-cli
  parser version is discarded and the key takes the full parse; it is never
  migrated. A discard logs one warning naming the reason and the path, and never
  changes a served response and never stops the process from starting.
- **The codec trap.** `domain.Page` embeds `FrontmatterMap`, whose only field is
  unexported, so `json.Marshal` on a `domain.Page` silently drops every
  frontmatter field and produces a page with no status, phase, goals, assignee,
  priority, dates or page_type. The store therefore encodes frontmatter through
  `FrontmatterMap.RawMap()` and rebuilds each page with `domain.NewPage`; a JSON
  codec is not an option.
