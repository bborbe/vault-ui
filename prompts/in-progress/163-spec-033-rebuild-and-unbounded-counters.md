---
status: approved
spec: [033-measure-task-list-rebuild-path]
created: "2026-10-14T12:29:16Z"
queued: "2026-10-14T13:02:28Z"
branch: dark-factory/measure-task-list-rebuild-path
---

# Add the rebuild-kind, full-rebuild-cause and unbounded-reason counters

<summary>
- The board can now say, from its own metrics, what kind of task-list rebuild ran — a full build, a row patch, or a session refresh.
- Every full rebuild is attributed to its cause, including a full rebuild that happens inside the patch path.
- A full rebuild that fell back from the patch is counted as a full rebuild, never as a patch, so a nominally-bounded build cannot be read as the bounded path being taken.
- The page index can now say why it could not bound a changed set, one reason per documented case, on the same metrics endpoint.
- A folder-level mark a write route issued outright is told apart from a `MarkFileDirty` widening, and the widening's four sub-causes are told apart from each other.
- One refused bound reports exactly one reason: the earliest unbounded advance in the window, by lowest revision.
- Every label is pre-initialised at boot, so each series reads 0 before any request is served.
- Each counter is incremented once per rebuild, never per row, so the per-request fixed overhead stays at its 615 KB baseline.
- No behaviour changes: the counters are the only new code, and every existing test row keeps its assertions.
</summary>

<objective>
Add the instrumentation the measurement needs: two labelled counters in `pkg/board` (the rebuild kind and the full-rebuild cause) and one labelled counter in `pkg/pageindex` (why a changed set could not be bounded), each with a fixed label enum pre-initialised at boot, and each incremented off the request path. This is prompt 2 of 4; the document landed in prompt 1, and the benchmark and regression fence follow in prompts 3 and 4. Nothing about how the board serves a request changes.
</objective>

<context>
Read the spec `specs/in-progress/033-measure-task-list-rebuild-path.md` in full before changing anything. This prompt covers Desired Behaviors 1 and 2 and Acceptance Criteria 1 and 2. Acceptance Criteria 5 and 7 are the spec's operator rung (a deploy and a live reading) and are **not** this prompt's scope.

Read these files fully before editing:
- `pkg/board/snapshot.go` — `taskSnapshotParams`, `taskSnapshotBuild`, `List` (the refresh / patch / full-build path selection), `runBuild` (the three branches).
- `pkg/board/tasks.go` — `patchTaskRows` (its two in-patch full-build fallback sites: `case present:` in the first loop, and `itemByName[name]` missing in the second loop).
- `pkg/board/board.go` — `New` (the wiring of `Build`/`Refresh`/`Patch`; do not change its signature).
- `pkg/pageindex/pageindex.go` — `pageChange`, `recordChangeLocked`, `MarkDirty`, `MarkFileDirty`, `ForceReload`, `ChangedPagesSince`, `maxChangeLog`.
- `pkg/pageindex/pageindex_build.go` — `executeListing` (the publication's `recordChangeLocked` call and its `!full && e.hasSnapshot` bounded test).
- `pkg/pageindex/pageindex_file.go` — `applyFileReadLocked` (the single-file read's `recordChangeLocked` call).
- `pkg/pageindex/metrics.go` — the existing `vault_ui_page_index_files_read_total{reason}` counter: this is the exact pattern the new counter follows (package-level `prometheus.NewCounterVec`, a `const` block of reason labels, a `[]string` slice of every series, `init()` with `prometheus.MustRegister` plus a pre-initialise loop calling `.WithLabelValues(reason).Add(0)`, and a `record…` helper).
- `pkg/metrics.go` and `pkg/websocket/metrics.go` — the boot pre-initialise convention (`Add(0)` with the comment "Pre-initialize the counters so alerting rules see the series from boot").

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-licensing-guide.md` (every new file starts with the copyright header)
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`

Verified facts you may rely on (read from the source above; do not re-derive):
- `pkg/board/snapshot.go` `runBuild` runs off the store mutex (`s.mu`) and switches on `b.patchOnly`, `b.refreshOnly`, and a default branch. The path selection happens in `List` **under** `s.mu`.
- `patchTaskRows` is called only through the store's `Patch` seam, off `s.mu`.
- `pkg/pageindex/pageindex.go` `ChangedPagesSince` returns `ok == false` for an unknown key, a revision ahead of the key's current one, a revision the retained window no longer covers, and any advance in the window recorded as unbounded. Its loop walks `e.changeLog` oldest-first, skipping entries with `change.revision <= revision`.
- `pkg/pageindex/pageindex_build.go` `executeListing` records the publication as `e.recordChangeLocked(listingChangedNames(full, delta), !full && e.hasSnapshot)`, where `e.hasSnapshot` is read **before** it is set true below, so a first publication (cold build, hydrate) and a reload publication are both unbounded.
- No test constructs `pageChange` or calls `recordChangeLocked` directly (verified), so adding a field to `pageChange` and a parameter to `recordChangeLocked` breaks no test.
- No `vault_ui_board_*` series exists today; every counter this prompt adds is new.
</context>

<requirements>

### 1. New file `pkg/board/metrics.go` — the two board counters

Create `pkg/board/metrics.go` (with the standard copyright header) holding two package-level `*prometheus.CounterVec`s and their record helpers, following the `pkg/pageindex/metrics.go` pattern exactly.

```go
package board

import "github.com/prometheus/client_golang/prometheus"

// taskSnapshotRebuildsTotal counts task-list snapshot rebuilds by the kind of
// work the rebuild performed.
var taskSnapshotRebuildsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "board",
	Name:      "task_snapshot_rebuilds_total",
	Help:      "Total task-list snapshot rebuilds, by kind.",
}, []string{"kind"})

// The kinds of work a task-list rebuild performs.
const (
	rebuildKindFull    = "full"
	rebuildKindPatch   = "patch"
	rebuildKindRefresh = "refresh"
)

// rebuildKinds is every series the rebuild-kind counter carries.
var rebuildKinds = []string{rebuildKindFull, rebuildKindPatch, rebuildKindRefresh}

// taskSnapshotFullRebuildsTotal counts full task-list snapshot rebuilds by the
// cause that forced the full build.
var taskSnapshotFullRebuildsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "board",
	Name:      "task_snapshot_full_rebuilds_total",
	Help:      "Total task-list snapshot full rebuilds, by cause.",
}, []string{"reason"})

// The causes a full task-list rebuild is attributed to.
const (
	fullReasonColdKey             = "cold_key"
	fullReasonBothMoved           = "both_moved"
	fullReasonPatchNotWired       = "patch_not_wired"
	fullReasonChangedSetUnbounded = "changed_set_unbounded"
	fullReasonNewPageNoHeldRow    = "new_page_no_held_row"
	fullReasonPatchPageNotListed  = "patch_page_not_listed"
)

// fullRebuildReasons is every series the full-rebuild-cause counter carries.
var fullRebuildReasons = []string{
	fullReasonColdKey,
	fullReasonBothMoved,
	fullReasonPatchNotWired,
	fullReasonChangedSetUnbounded,
	fullReasonNewPageNoHeldRow,
	fullReasonPatchPageNotListed,
}

func init() {
	prometheus.MustRegister(taskSnapshotRebuildsTotal, taskSnapshotFullRebuildsTotal)
	// Pre-initialize the series so alerting rules see them from boot.
	for _, kind := range rebuildKinds {
		taskSnapshotRebuildsTotal.WithLabelValues(kind).Add(0)
	}
	for _, reason := range fullRebuildReasons {
		taskSnapshotFullRebuildsTotal.WithLabelValues(reason).Add(0)
	}
}

// recordRebuild counts one task-list rebuild by the kind of work it performed.
func recordRebuild(kind string) { taskSnapshotRebuildsTotal.WithLabelValues(kind).Inc() }

// recordFullRebuild counts one full task-list rebuild by its cause.
func recordFullRebuild(reason string) { taskSnapshotFullRebuildsTotal.WithLabelValues(reason).Inc() }
```

Carry the label names **verbatim** — `full`, `patch`, `refresh`, and `cold_key`, `both_moved`, `patch_not_wired`, `changed_set_unbounded`, `new_page_no_held_row`, `patch_page_not_listed`. Do not rename, add, or drop any of them.

### 2. `pkg/board/snapshot.go` — carry the full-rebuild cause on the build, and record the store's own branches

**2a.** Add a `fullReason string` field to `taskSnapshotBuild`, next to `changed`, with a doc comment stating it is the cause to attribute when this build ends up a full build, and that it is empty for a build whose cause has no enum value (the refresh seam unwired).

**2b.** In `List`, replace the path-selection block

```go
refreshOnly := !pageMoved && sessionMoved && s.refresh != nil
var changed []string
patchOnly := false
if pageMoved && !sessionMoved && s.patch != nil {
	names, ok := s.revisions.ChangedPagesSince(key, e.pageRevision)
	if ok {
		patchOnly = true
		changed = names
	}
}
```

with a switch that names the cause for every full-build branch:

```go
refreshOnly := !pageMoved && sessionMoved && s.refresh != nil
var changed []string
patchOnly := false
fullReason := ""
switch {
case !e.hasSnapshot:
	fullReason = fullReasonColdKey
case pageMoved && sessionMoved:
	fullReason = fullReasonBothMoved
case pageMoved:
	if s.patch == nil {
		fullReason = fullReasonPatchNotWired
	} else if names, ok := s.revisions.ChangedPagesSince(key, e.pageRevision); ok {
		patchOnly = true
		changed = names
	} else {
		fullReason = fullReasonChangedSetUnbounded
	}
case sessionMoved:
	// Only the session generation moved and the refresh seam is unwired, so the
	// key rebuilds in full. The spec's cause enum has no label for this
	// (test-only) configuration, so the kind is counted with no cause.
}
```

Set `fullReason: fullReason` in the `taskSnapshotBuild` literal that follows.

<!-- OPEN QUESTION: the spec's full-rebuild-cause enum (`cold_key`, `both_moved`, `patch_not_wired`, `changed_set_unbounded`, `new_page_no_held_row`, `patch_page_not_listed`) has no label for the case where only the session generation moved and the `Refresh` seam is unwired, which is a test-only configuration (`board.New` always wires `Refresh`). The switch above counts that rebuild as `kind="full"` with no cause. If the reviewer wants a cause there, one label must be added to the enum, which the spec's AC2 list does not authorise. -->

**2c.** In `runBuild`, record the kind and cause for the two branches the store owns. The patch branch records nothing here — `patchTaskRows` records its own kind (requirement 3):

```go
switch {
case b.patchOnly:
	// The patch func records its own rebuild kind and any full-rebuild cause,
	// because only it knows whether it fell back to the full build.
	s.mu.Lock()
	held := s.entryLocked(key).rows
	s.mu.Unlock()
	rows, buildErr = s.patch(buildCtx, vault, held, b.changed)
case b.refreshOnly:
	recordRebuild(rebuildKindRefresh)
	s.mu.Lock()
	held := s.entryLocked(key).rows
	s.mu.Unlock()
	rows, buildErr = s.refresh(buildCtx, vault, held)
default:
	recordRebuild(rebuildKindFull)
	if b.fullReason != "" {
		recordFullRebuild(b.fullReason)
	}
	rows, buildErr = s.build(buildCtx, vault)
}
```

The increments run off `s.mu` (the switch is outside any `s.mu` critical section) and once per rebuild, never per row. They count the work the branch performs regardless of whether the build later fails, so do not move them after the error handling.

### 3. `pkg/board/tasks.go` — `patchTaskRows` records its own kind and fallback cause

`patchTaskRows` is the only place that can tell a genuine patch from a patch that fell back to the full build, so it records the kind itself. Add a deferred recorder at the top of the function, after the signature and before the first statement, and set the fallback cause at each of the two fallback sites.

```go
func (b *board) patchTaskRows(
	ctx context.Context,
	vault Vault,
	rows []taskSnapshotRow,
	names []string,
) ([]taskSnapshotRow, error) {
	kind := rebuildKindPatch
	fullReason := ""
	// Record exactly one rebuild kind per call, on every return path: the
	// bounded path counts as a patch, and a fallback to the full build counts
	// as a full rebuild attributed to the site that forced it, never as a patch.
	defer func() {
		recordRebuild(kind)
		if fullReason != "" {
			recordFullRebuild(fullReason)
		}
	}()

	pages, err := b.index.ListPages(ctx, vault.Path, vault.TasksFolder)
	// ... existing body unchanged ...
```

Then, at the first fallback site (the `case present:` arm of the first `switch` over `names`):

```go
case present:
	// A page with no held row carries no session data to reuse.
	kind = rebuildKindFull
	fullReason = fullReasonNewPageNoHeldRow
	return b.buildTaskRows(ctx, vault)
```

And at the second fallback site (the `if !ok` inside the loop over `changed`):

```go
item, ok := itemByName[name]
if !ok {
	// The list operation dropped the page the snapshot holds.
	kind = rebuildKindFull
	fullReason = fullReasonPatchPageNotListed
	return b.buildTaskRows(ctx, vault)
}
```

Do not change anything else in `patchTaskRows`: the held-row splice, the `recomputeDependentBlockers` walk, the `withoutRows` drop and the returned slice are unchanged. The two labels stay separate; do not collapse them into one.

### 4. `pkg/pageindex/metrics.go` — the unbounded-reason counter

Add a second labelled counter to `pkg/pageindex/metrics.go`, alongside `filesReadTotal`, in the same style.

```go
// changedPagesUnboundedTotal counts the times the page index refused to bound a
// changed set, by the cause of the refusal. Its totals are counts of refusals
// by cause, never of revision advances.
var changedPagesUnboundedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "page_index",
	Name:      "changed_pages_unbounded_total",
	Help:      "Total changed-page sets the page index could not bound, by reason.",
}, []string{"reason"})

// The reasons a changed set cannot be bounded.
const (
	unboundedUnknownKey            = "unknown_key"
	unboundedRevisionAhead         = "revision_ahead"
	unboundedWindowNotRetained     = "window_not_retained"
	unboundedForcedReload          = "forced_reload"
	unboundedFirstPublication      = "first_publication"
	unboundedFolderMarkExplicit    = "folder_mark_explicit"
	unboundedFolderMarkWidenedName = "folder_mark_widened_name_not_in_snapshot"
	unboundedFolderMarkWidenedFile = "folder_mark_widened_file_absent"
	unboundedFolderMarkWidenedNameInvalid = "folder_mark_widened_invalid_name"
	unboundedFolderMarkWidenedKey  = "folder_mark_widened_key_unpublished"
)

// unboundedReasons is every series the counter carries.
var unboundedReasons = []string{
	unboundedUnknownKey,
	unboundedRevisionAhead,
	unboundedWindowNotRetained,
	unboundedForcedReload,
	unboundedFirstPublication,
	unboundedFolderMarkExplicit,
	unboundedFolderMarkWidenedName,
	unboundedFolderMarkWidenedFile,
	unboundedFolderMarkWidenedNameInvalid,
	unboundedFolderMarkWidenedKey,
}
```

Extend the existing `init()` (or add a second `init()`) so `changedPagesUnboundedTotal` is registered and every label in `unboundedReasons` is pre-initialised with `.Add(0)`. Add the record helper:

```go
// recordUnbounded counts one refused bound by its cause.
func recordUnbounded(reason string) { changedPagesUnboundedTotal.WithLabelValues(reason).Inc() }
```

Carry the ten label names **verbatim**. Do not rename, add, or drop any of them.

### 5. `pkg/pageindex/pageindex.go` — carry a cause on each change-log entry

**5a.** Add a `reason string` field to `pageChange`, documented as the unbounded cause this advance carries (empty for a bounded advance).

**5b.** Change `recordChangeLocked` to take the cause:

```go
func (e *entry) recordChangeLocked(names []string, bounded bool, reason string) {
	e.changeLog = append(e.changeLog, pageChange{
		revision: e.revision,
		names:    names,
		bounded:  bounded,
		reason:   reason,
	})
	// ... existing trim unchanged ...
}
```

**5c.** Add a folder-mark helper used by both `MarkDirty` and the `MarkFileDirty` widening sites, so the two are distinguishable:

```go
// markFolderDirty marks one key folder-level with the given unbounded cause.
func (p *pageIndex) markFolderDirty(key Key, reason string) {
	key = NewKey(key.VaultPath, key.PagesDir)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[key]; ok {
		p.markFolderDirtyLocked(e, reason)
	}
}

// markFolderDirtyLocked records one folder-level mark. The caller must hold the
// mutex.
func (p *pageIndex) markFolderDirtyLocked(e *entry, reason string) {
	e.requestSeq++
	e.dirtySeq = e.requestSeq
	e.revision++
	e.recordChangeLocked(nil, false, reason)
}
```

Rewrite `MarkDirty` to keep its exported signature and call `p.markFolderDirtyLocked(e, unboundedFolderMarkExplicit)` for each known key. The key-filtering behaviour is unchanged (an unknown key is still ignored).

**5d.** Rewrite `MarkFileDirty` so the widening path records its **own** cause rather than reusing the explicit mark's. Split the compound guard so each condition reports its own label, in this order:

```go
stripped := stripWikilinkName(name)

p.mu.Lock()
e, ok := p.entries[key]
switch {
case !ok || !e.hasSnapshot:
	p.mu.Unlock()
	p.markFolderDirty(key, unboundedFolderMarkWidenedKey)
	return
case !validFileMarkName(stripped):
	p.mu.Unlock()
	p.markFolderDirty(key, unboundedFolderMarkWidenedNameInvalid)
	return
case !snapshotHasName(e.snapshot, stripped):
	p.mu.Unlock()
	p.markFolderDirty(key, unboundedFolderMarkWidenedName)
	return
}
p.mu.Unlock()

filename := stripped + ".md"
if _, err := os.Stat(filepath.Join(key.VaultPath, key.PagesDir, filename)); err != nil {
	p.markFolderDirty(key, unboundedFolderMarkWidenedFile)
	return
}

p.mu.Lock()
defer p.mu.Unlock()
e = p.entryLocked(key)
e.requestSeq++
e.writeMarked[filename] = e.requestSeq
e.revision++
e.recordChangeLocked([]string{filename}, true, "")
```

The bounded (non-widening) path keeps its exact behaviour; only its `recordChangeLocked` call gains the empty reason.

**5e.** In `ForceReload`, pass `unboundedForcedReload` to the `recordChangeLocked` call. Its mark behaviour is otherwise unchanged.

**5f.** Rewrite `ChangedPagesSince` to report **why** it refused, and to report at most one reason per refused bound — the **earliest** unbounded advance in the window, by lowest revision. Split the mutex-held work into a helper that returns the reason, and record after releasing `p.mu` so the increment does not run under the index mutex:

```go
func (p *pageIndex) ChangedPagesSince(key Key, revision uint64) ([]string, bool) {
	key = NewKey(key.VaultPath, key.PagesDir)
	p.mu.Lock()
	names, ok, reason := p.changedPagesSinceLocked(key, revision)
	p.mu.Unlock()
	if !ok {
		recordUnbounded(reason)
	}
	return names, ok
}

// changedPagesSinceLocked is ChangedPagesSince's body under the mutex. It
// returns the refusal's reason as its third value. The caller must hold the
// mutex.
func (p *pageIndex) changedPagesSinceLocked(key Key, revision uint64) ([]string, bool, string) {
	e, ok := p.entries[key]
	if !ok {
		return nil, false, unboundedUnknownKey
	}
	if revision > e.revision {
		return nil, false, unboundedRevisionAhead
	}
	if revision == e.revision {
		return nil, true, ""
	}
	if len(e.changeLog) == 0 || e.changeLog[0].revision > revision+1 {
		return nil, false, unboundedWindowNotRetained
	}
	names := make([]string, 0, len(e.changeLog))
	seen := make(map[string]bool, len(e.changeLog))
	for _, change := range e.changeLog {
		if change.revision <= revision {
			continue
		}
		if !change.bounded {
			// The earliest unbounded advance in the window decides the cause;
			// entries after it are not consulted.
			return nil, false, change.reason
		}
		for _, name := range change.names {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, true, ""
}
```

The loop walks `e.changeLog` oldest-first, so the first unbounded entry with `change.revision > revision` **is** the earliest one in the window. Do not average or collect reasons; return on the first.

### 6. `pkg/pageindex/pageindex_file.go` — the bounded single-file read

In `applyFileReadLocked`, pass an empty reason to the `recordChangeLocked` call for the bounded single-file advance:

```go
e.recordChangeLocked([]string{filename}, true, "")
```

Its behaviour is otherwise unchanged.

### 7. `pkg/pageindex/pageindex_build.go` — the publication's cause

In `executeListing`, replace the publication's `recordChangeLocked` call so a first publication and a reload publication each carry their own cause:

```go
bounded := !full && e.hasSnapshot
reason := ""
if !bounded {
	if !e.hasSnapshot {
		reason = unboundedFirstPublication
	} else {
		reason = unboundedForcedReload
	}
}
e.recordChangeLocked(listingChangedNames(full, delta), bounded, reason)
```

Note `e.hasSnapshot` is read here **before** it is set true a few lines below, exactly as the current `!full && e.hasSnapshot` test relies on. A cold build's and a hydrate's first publication therefore carry `unboundedFirstPublication`; a reload of an established key carries `unboundedForcedReload`. Do not change anything else in `executeListing`.

### 8. Do not add or change any test

This prompt adds no test row and changes no existing test assertion. Acceptance Criteria 1 and 2 are **declaration** requirements: they assert no per-label delta, and the attribution belongs to the reading, not to a test. The existing rows are this prompt's coverage and must pass unchanged — in particular `re-derives only the changed rows and never rebuilds`, `rebuilds in full for a folder-level mark`, `takes the session-only refresh when only the generation moves`, `rebuilds in full when the page revision and the generation both move`, `rebuilds in full when no patch func is wired`, `falls back to the full build for a changed page with no held row`, `keeps the previous rows when a rebuild fails and retries on the next read` (all in `pkg/board`), and `reports unknown for a key it has never seen`, `reports unknown for a revision the key has never reached`, `reports unknown once an unbounded mark falls inside the range`, `reports unknown after a cold build publishes the whole snapshot`, `reports unknown after a hydrate publishes the whole snapshot`, the `bounds the set of pages that moved` table, and `counts the write, rescan and reload read sites under their own reason` (all in `pkg/pageindex`).

Do **not** modify `pkg/board/snapshot_internal_test.go`, `pkg/board/rowpatch_test.go`, `pkg/board/taskbody_internal_test.go`, or any `pkg/pageindex` test file. Do not change the `taskSnapshotPatchFunc` signature or any `Deps`/`taskSnapshotParams` field type: the patch func reports its fallback through the counter, not through a changed return value.
</requirements>

<constraints>
- This spec changes no behaviour: the invalidation stays, the rebuild's choice of path stays, the per-task work stays, the decoded JSON, the wire shape and the body cache stay. The new code is counters only.
- Do **not** disable, weaken or gate the invalidation; do **not** trim or weaken the per-task work.
- **One refused bound reports one reason: the earliest unbounded advance in the window (lowest revision).** The counter's totals are counts of refusals by cause, never of advances; a reading must not sum the reasons against the request count.
- **The widening path records its own cause, not the explicit mark's.** The four `folder_mark_widened_*` labels must be produced at the widening sites, and `folder_mark_explicit` only where a caller marked the folder outright.
- **Each counter is incremented once per rebuild, never per row**, so the instrumentation adds no per-request allocation proportional to the vault's task count. The per-request fixed overhead stays at its 615 KB baseline.
- The counters are fixed names, pre-initialised at boot, and carry a fixed enum of labels so a client cannot grow their cardinality. **No metric is read on the request path.** The board's full/refresh counters are recorded off `s.mu`; the page-index refusal is recorded after `p.mu` is released. Because `List` calls `ChangedPagesSince` under `s.mu`, that one increment does run under the store mutex — unavoidable while `IndexRevisions` keeps its frozen signature, and it is an O(1) increment on an already-initialised series. Do not restructure `List` to move it.
- `IndexRevisions` (`Revision`, `ChangedPagesSince`, `ListPages`) keeps its signatures. `pageChange` is internal, so carrying a cause on it is not an interface change.
- Do not add a configuration key, a timer, a cache, a scenario, or a new endpoint.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. Every new file carries the copyright header.
- Do not commit — dark-factory handles git.
- Existing tests must still pass, with their assertions unchanged.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/... ./pkg/pageindex/...
```
Must pass.

```
grep -n 'task_snapshot_rebuilds_total\|task_snapshot_full_rebuilds_total' pkg/board/metrics.go
```
Must print at least two lines.

```
grep -c 'Add(0)' pkg/board/metrics.go
```
Must print at least `2` (every label pre-initialised).

```
grep -n 'changed_pages_unbounded_total' pkg/pageindex/metrics.go
```
Must print at least one line.

```
grep -n 'unboundedFolderMarkWidenedName\b\|unboundedFolderMarkWidenedFile\b\|unboundedFolderMarkWidenedNameInvalid\b\|unboundedFolderMarkWidenedKey\b' pkg/pageindex/pageindex.go
```
Must print at least four lines (the four widening sites each carry their own cause).

```
grep -n 'recordChangeLocked(' pkg/pageindex/*.go
```
Must print only three-argument forms (`names, bounded, reason`) — the new declaration and every call; no two-argument call may remain.

```
grep -n 'folder_mark_widened_name_not_in_snapshot\|folder_mark_widened_file_absent\|folder_mark_widened_invalid_name\|folder_mark_widened_key_unpublished' pkg/pageindex/metrics.go
```
Must print four lines (the four widening label values, verbatim as the spec names them).

```
grep -n 'recordUnbounded(' pkg/pageindex/pageindex.go
```
Must print at least one line (the refusal is actually recorded, not merely declared).

```
grep -n 'fullReasonNewPageNoHeldRow\|fullReasonPatchPageNotListed' pkg/board/tasks.go
```
Must print at least two lines (the two in-patch fallback sites carry separate labels).

```
grep -n 'rebuildKindPatch\|rebuildKindFull\|rebuildKindRefresh' pkg/board/*.go
```
Must print lines in `metrics.go` (the enum), `snapshot.go` (the full and refresh branches) and `tasks.go` (the patch default and the two full-build fallbacks).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
