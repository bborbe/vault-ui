# Design note — incremental task-list row rebuild on a per-file write mark

Status: design note, not an approved spec. Argues the shape for the prompt
`prompts/incremental-task-list-row-rebuild.md`, which closes the
`Vault UI Task List Reflects a Write Within 100ms` row's SC2.

## The problem, measured

On the live board (PID 59137, binary built 2026-10-07 20:03, pre-#154), against
`private-personal`:

| stage | latency |
|---|---|
| `PATCH /api/tasks/{id}/flag` answers 202 | p50 1.0 ms |
| file on disk | 1.7–7.8 ms |
| **value visible in `GET /api/tasks`** | **p50 175.9 ms · p95 203.5 ms · min 144.4 ms** |

The first read after the write **blocks 106.7 ms** and then returns fresh — one
rebuild, no polling in between. That is the whole echo.

Spec 027 already made the **page index** incremental: per write the counter deltas
are exactly `{write:+1, event:+1}` with `build` and `rescan` flat. The cost is the
**task-list snapshot rebuild** sitting on top of it. `pkg/board/tasks.go`
`buildTaskRows` calls `b.ops.List(vault).Execute(...)` over the whole tasks folder,
then `b.openQuestionsByTask`, then `session.ClassifySessionState` **per row** with
`RegistrySessionIDs`/`ResumeSessionIDs` and `b.transcriptProbe()`.

So: **the mark is per-file; the rebuild is per-vault.** The mark only decides *when*
the rebuild starts, never what it costs.

## Proposed shape

Teach `taskSnapshotStore.List` (`pkg/board/snapshot.go`) a third rebuild kind beside
the existing full build and `refreshOnly`: a **row patch**.

The store cannot currently tell *why* `IndexRevisions.Revision(key)` moved — it sees
one opaque `uint64`. A patch needs the changed set, so the page index exposes one
more read:

```go
// ChangedPagesSince returns the names of the pages whose parsed form changed in
// key's publications and marks after revision. ok is false when the key cannot
// bound the set — a folder-level mark, a forced reload, an unknown key, or a
// revision the key has since advanced past.
ChangedPagesSince(key Key, revision uint64) (names []string, ok bool)
```

`List` then chooses:

- **`ok == true`** — re-derive only those rows: build each changed name's row from
  its page in the published snapshot, splice into a copy of the held row list at its
  filename-ordered position, drop a name whose page vanished, insert a new one.
- **`ok == false`** — the existing full `buildTaskRows`.
- session generation alone moved — the existing `refreshOnly`.
- **both moved** — full build. Conservative and correct; a patch composed with a
  session refresh is two mechanisms on one entry and not worth the coupling.

## The four questions

**(a) What the patch does on a revision move.** It re-derives the rows named by
`ChangedPagesSince`, and nothing else. Any move it cannot attribute to a bounded set
of single files falls back to the full build. The fallback is the *normal* path for
folder-level marks — the eight synchronous sites (`Run*`, `TakeOver*`, both
`execute-command` routes) deliberately widen to a folder-level mark, and they keep
taking a full rebuild.

**(b) Atomic swap.** Unchanged, and it must stay unchanged. The patch builds a **new
slice** — copy the held list, splice — and publishes it by the same assignment under
the store mutex the full build uses. The published slice is never mutated in place,
so a concurrent reader still sees either the whole previous list or the whole new
one, never a mixture. This is the invariant a patch could plausibly break (mutating
the held slice in place is the tempting shortcut), so the prompt states it as a
prohibition, and the `Atomic swap` wording in `docs/page-index.md` survives the
change verbatim.

**(c) Cold-read sharing.** Unchanged in meaning: there is still exactly one in-flight
work item per key, and every waiter on it observes its result. The existing
`taskSnapshotBuild`/`inflight` machinery carries a patch exactly as it carries a
build — `startRevision`/`startGeneration` are recorded at start, so a racing
invalidation still leaves the entry dirty and the next read re-derives. Cold keys
(no snapshot) still take one shared full build; a patch is only ever chosen when a
snapshot is already published.

**(d) Relation to `refreshOnly`.** A **sibling, not a subsumption.** `refreshOnly`
re-derives *session-derived fields* for *all* rows when only the session generation
moved. A patch re-derives *all fields* for *a few* rows when only page files moved.
They answer different questions and neither generalises the other; the "both moved"
rule above is what keeps them from having to compose. `refreshOnly` is untouched.

## The risk this note does NOT settle

**The patch only helps if the rebuild's cost is dominated by per-row work.** Three
costs in `buildTaskRows` are *per-rebuild*, not per-row: `RegistrySessionIDs` and
`ResumeSessionIDs` (process spawns), and `openQuestionsByTask` (one `ListPages` for
the vault). If those dominate the 106.7 ms, patching rows will not reach <100 ms.

I did **not** measure that split — it needs instrumentation on a build. The prompt
therefore states the contract in terms that cover both: a write-triggered rebuild
must perform **no vault-wide work** — no vault list, no vault-wide page scan, no
process spawn — not merely "fewer rows". A patch path that still calls `ops.List`
would satisfy the narrow reading and fail the criterion.

## Alternatives considered, and why not

**Cache `buildTaskRows`' output keyed on the revision.** Rejected: it is the snapshot
store's existing job and would add a second cache with the same invalidation
problem, without making the *first* post-write read any cheaper — which is the read
the criterion measures.

**Make the rebuild cheaper without changing its scope** (cache the session snapshot,
cache `openQuestionsByTask`). Not an alternative — it is the *complement*. If the
measurement shows the fixed costs dominate, this is the actual fix and the patch is
unnecessary. The prompt's contract is written so that either satisfies it.
