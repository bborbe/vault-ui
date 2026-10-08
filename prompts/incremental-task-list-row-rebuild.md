---
status: draft
---

# Rebuild only the marked rows on a single-file write

<summary>
- A frontmatter write through the board becomes visible to the next list read in
  well under 100 ms, instead of the ~150–200 ms it costs today
- Today the write correctly marks exactly one file in the page index, but the
  list snapshot rebuilt on top of that mark still re-reads the whole vault
- After this change a write-triggered rebuild re-derives only the rows whose
  files actually changed
- Anything the system cannot attribute to a bounded set of single files keeps
  taking the existing full rebuild — no behaviour change on those paths
- A reader still sees either the whole previous list or the whole new list, never
  a mixture; the published list is still never mutated in place
- Two concurrent readers still share exactly one rebuild
- The existing session-only refresh path is unchanged
</summary>

<objective>
Make the task-list snapshot rebuild that a single-file write mark triggers
re-derive only the marked rows, so a UI write is reflected in the next
`GET /api/tasks` within the goal's 100 ms budget. The mark already tells the page
index exactly which file changed; today that knowledge stops at the page index and
the rebuild above it discards it and re-reads the vault.
</objective>

<context>
Read `CLAUDE.md` for project conventions, then read these before writing anything:

- `docs/page-index.md` — the acceptance contract. Read § "Task-list snapshot" and
  § "Incremental updates" in full. This change must not weaken any statement in
  them; § "Atomic swap", § "Cold-read sharing" and § "Failed rebuild" survive
  verbatim.
- `specs/ideas/incremental-task-list-row-patch.md` — the design note for this
  change. It states the shape, answers the atomic-swap and cold-read-sharing
  questions, and names the one risk this prompt's contract is written to cover.
- `pkg/board/snapshot.go` — `taskSnapshotStore`, `taskSnapshotStore.List`,
  `taskSnapshotBuild`, `taskSnapshotParams`, `IndexRevisions`, `generationSource`.
  The `refreshOnly` field on `taskSnapshotBuild` is the existing exemplar for
  "a cheaper rebuild kind".
- `pkg/board/tasks.go` — `buildTaskRows` (the expensive full path) and
  `refreshTaskRows` (the session-only path).
- `pkg/board/board.go` — `newTaskSnapshotStore`, where the two funcs are wired.
- `pkg/pageindex/pageindex.go` — `Revision`, `MarkFileDirty`, `MarkDirty`, `Key`,
  `NewKey`, and the `writeMarked` / `requestSeq` bookkeeping inside `MarkFileDirty`.
- `pkg/mutations/mutations.go` — `taskWritten`, which calls `MarkFileDirty` for the
  queued writes. This is the caller whose mark must become cheap to honour.
</context>

<requirements>
1. Add a read to the page index that reports which pages changed since a revision,
   on `pkg/pageindex/pageindex.go`:

   ```go
   // ChangedPagesSince returns the names of the pages whose parsed form changed in
   // key's publications and marks after revision. ok is false when the key cannot
   // bound the set — a folder-level mark, a forced reload, an unknown key, or a
   // revision the key has since advanced past.
   ChangedPagesSince(key Key, revision uint64) (names []string, ok bool)
   ```

   It must answer `ok == false` for a folder-level mark (`MarkDirty`), for
   `ForceReload`, for a key with no entry, and for any revision it cannot place.
   Add it to the interface that `pkg/board` consumes for revisions, or to a
   sibling interface alongside `IndexRevisions` — pick whichever keeps
   `pkg/board`'s dependency on `pkg/pageindex` a narrow interface, and wire it in
   `pkg/board/board.go` next to `Revisions`.

2. Teach `taskSnapshotStore.List` in `pkg/board/snapshot.go` a third rebuild kind.
   Extend `taskSnapshotBuild` the way `refreshOnly` already extends it — a field
   naming the changed pages, set only when the page revision moved *and*
   `ChangedPagesSince` returned `ok`. Selection order in `List`:

   - page revision moved and the changed set is known → **row patch**
   - page revision moved and the set is not known → full `build` (today's path)
   - only the session generation moved → `refreshOnly` (unchanged)
   - both moved → full `build`

   `refreshOnly` keeps its current meaning and its current code path.

3. Implement the patch so that it re-derives only the changed rows and performs
   **no vault-wide work**: it must not call the vault list operation, must not
   scan the vault's pages, and must not spawn a process. Derive each changed
   name's row from the page the index already holds, using the session data the
   store already has for the current generation. If a name's page is gone, drop
   its row; if it is new, insert it; otherwise replace it at its
   filename-ordered position. Where a per-row derivation would need data the
   patch path does not already hold, fall back to the full build for the whole
   key rather than fetching it — a fallback is correct, a vault-wide fetch on
   this path is not.

4. The patch publishes a **new slice**. Copy the held row list, splice the changed
   rows into the copy, and publish the copy by the same assignment under the store
   mutex that the full build uses. Do **not** mutate the held slice in place, and
   do **not** mutate any `taskSnapshotRow` a reader may already hold.

5. Record `startRevision` and `startGeneration` on a patch exactly as the full
   build does, so a racing invalidation still leaves the entry dirty and the next
   read re-derives it. `awaitSnapshotBuild`, `inflight` and the failed-rebuild
   behaviour are unchanged.

6. Update `docs/page-index.md` § "Task-list snapshot" so it describes the patch
   path: that a rebuild may re-derive a bounded set of rows instead of all of
   them, that the swap is still atomic, that cold-read sharing is unchanged, and
   that a rebuild which cannot bound the changed set still rebuilds in full. Keep
   every existing guarantee statement intact.

7. Tests, in the package that owns each behaviour, following the existing Ginkgo
   style in `pkg/board/snapshot_internal_test.go` and `pkg/pageindex/`:

   - a `pkg/pageindex` table test for `ChangedPagesSince`: a per-file mark returns
     exactly that name with `ok == true`; a folder-level mark, a forced reload, an
     unknown key and a stale revision each return `ok == false`
   - a `pkg/board` test that publishes a snapshot with a **counting** build func
     (the existing `Build` seam), applies a per-file `MarkFileDirty`, and asserts
     the next `List` (a) returns the updated row, (b) does **not** increment the
     build counter, and (c) leaves the previously returned slice's contents
     unchanged — the atomic-swap invariant
   - a `pkg/board` test that a folder-level mark still increments the build
     counter, and one that a session-generation-only move still takes
     `refreshOnly` without incrementing it

   These tests assert the *mechanism* (which path ran, and what work it did), not
   a wall-clock duration — a latency assertion would be flaky and is not what this
   prompt is verified by.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- `pkg/board` must keep consuming `pkg/pageindex` through narrow interfaces, never
  the concrete `*pageIndex`
- Wrap errors with `github.com/bborbe/errors` (`errors.Wrapf(ctx, err, ...)`); no
  `fmt.Errorf`, no bare `return err`
- `glog` calls at Info level must be `V(n)`-gated
- The published row slice is read-only to every reader; never mutate it in place
- Do not change the eight synchronous sites (`Run*`, `TakeOver*`, both
  `execute-command` routes) — they keep their folder-level mark and their full
  rebuild
- Do not weaken any guarantee in `docs/page-index.md`; amend the wording of
  § "Task-list snapshot" only where the patch path adds to it
- If the patch path cannot be made to avoid vault-wide work for a case, fall back
  to the full build for that case rather than fetching the missing data
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must pass.

Then, before finishing, re-run that command and confirm it passes, and walk each
numbered requirement above against the change: state for each one what in the
diff satisfies it, and name any requirement you did not meet.
</verification>
