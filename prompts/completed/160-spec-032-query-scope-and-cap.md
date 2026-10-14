---
status: completed
spec: [032-task-body-cache-invalidation]
summary: Rewired the rendered task-list body cache to gate on the query's selected-vault published-rows signals instead of one process-wide generation counter, and bounded the held set with an LRU cap of 64.
execution_id: vault-ui-cache-invalidation-exec-160-spec-032-query-scope-and-cap
dark-factory-version: v0.198.7
created: "2026-10-13T22:16:50Z"
queued: "2026-10-13T22:54:57Z"
started: "2026-10-13T22:58:39Z"
completed: "2026-10-13T23:04:42Z"
branch: dark-factory/task-body-cache-invalidation
---

# Query scope: the body cache discards a held body only for the vaults the query selects, and bounds the held set

<summary>
- The rendered `/api/tasks` body cache stops reading one process-wide rebuild counter.
- A held body is now gated on the published-rows signals of exactly the vaults its query selects, so a change in a vault the query does not read leaves the held body serving.
- The bare query selects every configured vault, so it is still discarded by a genuine change in any of them and kept through an identical-row rebuild in any of them.
- Adding or removing a vault from the configuration changes the bare query's selection and discards its held body.
- The number of held bodies is capped at a fixed constant with least-recently-used eviction, so an unauthenticated client requesting many distinct queries cannot grow it without bound.
- The clock boundary and the failed-build path are unchanged.
- No document is edited and the CHANGELOG is untouched in this prompt.
</summary>

<objective>
Rewire the rendered task-list body cache so a held body is invalidated by the published-rows signals of the vaults its query selects — never by a vault outside that selection — and cap the number of held bodies with LRU eviction. This is prompt 2 of 3: prompt 1 landed the store's content gate and its per-key `Signal`; prompt 3 owns the docs, the CHANGELOG and the byte-identity/failure-path rows.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it is absent in this checkout) and `docs/dod.md` (the Definition of Done the validation step checks).

Read the spec `specs/in-progress/032-task-body-cache-invalidation.md` in full. This prompt covers Desired Behaviors 2, 3 and 5 and Acceptance Criteria 3, 4 and 7. Prompt 1 has already landed (do not redo it): `pkg/board/snapshot.go` now has a content-gated `signalSeq` field, a per-key `signal` field on `taskSnapshotEntry`, `func (s *taskSnapshotStore) Signal(key pageindex.Key) uint64`, and `SnapshotGeneration()` returning the content-gated aggregate.

Read these files fully before changing anything:
- `pkg/board/taskbody.go` — the cache under change. Today `taskBodyParams.Generations func() uint64` is the seam; `taskBodyCache.generation` is the last-seen value; `Get` drops every entry when it moves; `taskBodyEntry` holds `body` and `expiresAt`. `taskBodyKey` and `taskBodyBoundary` are unchanged by this prompt.
- `pkg/board/board.go` — `New` wires `b.bodies = newTaskBodyCache(taskBodyParams{ Project: b.ListTasks, Rows: b.taskSnapshotRows, Encode: encodeTaskList, Generations: b.snapshot.SnapshotGeneration, Clock: b.clock })`. `Board` and `Deps` are unchanged.
- `pkg/board/tasks.go` — `ListTasksBody` (calls `b.taskSnapshotRows` then `b.bodies.Get`; this is the production read order the tests must mirror), `taskSnapshotRows` (reads `b.vaults.Vaults`, `b.selectVaults`, then `b.snapshot.List` per selected vault), and `selectVaults` (`pkg/board/vaults.go`: a nil/empty filter selects every vault in config order). `pkg/board/tasks.go`'s import block does **not** currently import `pageindex`; the new `taskSnapshotSignals` needs it.
- `pkg/board/snapshot.go` — `Signal(key)` and `SnapshotGeneration()` (prompt 1). Remove `SnapshotGeneration()` here, since nothing uses it once the body cache reads `Signals`.
- `pkg/board/taskbody_internal_test.go` — `bodyHarness`, `newBodyHarness`, `bodyFixture`, `bodySnapshotRow`, `setGeneration`, `Generation`, `entryCount`, `hasEntry`, and the rows `drops the held entries when the generation moves`, `rebuild discards held bodies`, `leaves the entry unheld when the generation moves during a build`, `clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `stores nothing and evicts nothing when a build fails`, `serves a held body without re-encoding`, `cached and uncached bodies are byte-identical`, the `keys every body-changing field` table and `gzip-compresses the identity form losslessly`.
- `pkg/board/taskbody_internal_test.go` also holds the `Describe("task-list body cache content gate", ...)` block and its single-vault store↔cache harness that prompt 1 added (rows `a rebuild that reproduces identical rows keeps the held body`, `a rebuild that changes a rendered row discards the held body`, and the `a change to any rendered row field discards the held body` table). Its harness read helper calls `store.List` for every configured vault and then `cache.Get`; its body cache is currently built with `Generations: store.SnapshotGeneration`. This prompt changes that to `Signals` and keeps those rows' assertions unchanged.
- `pkg/board/snapshot_internal_test.go` — `fakeRevisions` (`bump`, `markPages`, `markFolder`), `snapshotHarness`, `snapshotRow`, `numberedRows`, `rowsBuilder`, `statusPatch`, `allStatusRefresh`. Reuse these; do not change this file.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-functional-options-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — coverage rules.
</context>

<requirements>

### 1. Replace the generation seam with a selected-vault signal vector (`pkg/board/taskbody.go`)

- Add the exported value type:
  ```go
  // VaultSignal is one selected vault's published-rows signal. A held body is
  // gated on the signals of exactly the vaults its query selects.
  type VaultSignal struct {
      Name   string
      Signal uint64
  }
  ```
  `VaultSignal` is comparable (a string and a uint64), so a slice of them compares with `slices.Equal`.
- In `taskBodyParams`, replace `Generations func() uint64` with:
  ```go
  // Signals returns, in selection order, the published-rows signal of every
  // vault the query selects. It changes when any selected vault's rows change or
  // when the selection changes, and is unaffected by a vault the query does not
  // select. Production: the board's taskSnapshotSignals.
  Signals func(selected []Vault) []VaultSignal
  ```
  Import `"slices"`. Update the doc comment on `taskBodyParams` and on `newTaskBodyCache` to match.
- On `taskBodyEntry`, add `signals []VaultSignal` (the selected vaults' signals the body was built from) and `lastUsed uint64` (the cache's monotonic access ordinal for LRU). Keep `body` and `expiresAt`.
- On `taskBodyCache`, remove the `generation uint64` field, add `signals func(selected []Vault) []VaultSignal` (the seam `Get` calls) and add `lastUsedSeq uint64`. Update the type's doc comment: it holds one rendered body per (selected-vault signal vector, query), and the held set is bounded by `taskBodyCacheMaxEntries` with least-recently-used eviction.
- Add the fixed cap constant:
  ```go
  // taskBodyCacheMaxEntries bounds the number of held bodies. It is a fixed
  // constant, never a configuration key: the per-query scoping removes the
  // incidental bound the old blanket generation drop provided, so an
  // unauthenticated client requesting many distinct queries must not be able to
  // grow the held set without bound. Its value is local to this cache and
  // reversible; it must be at least 2.
  const taskBodyCacheMaxEntries = 64
  ```
  Choose any value `>= 2` (the AC7 floor row requires at least two distinct bodies to be held at once); do not make it configurable.

### 2. Rewrite `Get` to gate on the selected vaults' signals (`pkg/board/taskbody.go`)

Rewrite `(*taskBodyCache).Get` so a held body is discarded exactly when the query's selected-vault signals differ from the ones the body was built from, or when the clock boundary has passed. Keep the existing error wrapping (`errors.Wrapf(ctx, err, "build task-list body")`) and the existing `glog.Errorf` lines. The shape:

```go
func (c *taskBodyCache) Get(ctx context.Context, query TaskQuery, selected []Vault) (TaskListBody, error) {
	key := taskBodyKey(query)
	now := c.clock.Now().UTC().Time()

	signals := c.signals(selected)

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		if slices.Equal(entry.signals, signals) &&
			(entry.expiresAt.IsZero() || now.Before(entry.expiresAt)) {
			body := entry.body
			c.touchLocked(entry)
			c.mu.Unlock()
			return body, nil
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()

	responses, err := c.project(ctx, query)
	if err != nil {
		glog.Errorf("build task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	identity, err := c.encode(responses)
	if err != nil {
		glog.Errorf("encode task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	gzipped, err := gzipBody(identity)
	if err != nil {
		glog.Errorf("gzip task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	body := TaskListBody{Identity: identity, Gzipped: gzipped}

	rows, err := c.rows(ctx, query)
	if err != nil {
		glog.Errorf("read task-list rows for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	expiresAt := taskBodyBoundary(rows, now, query.UpcomingHours)

	// Re-read the signals the build started from: a signal that moved while the
	// build ran leaves the entry unheld and the next read rebuilds.
	current := c.signals(selected)
	c.mu.Lock()
	if slices.Equal(current, signals) {
		c.storeLocked(key, &taskBodyEntry{body: body, expiresAt: expiresAt, signals: current})
	}
	c.mu.Unlock()

	return body, nil
}
```

Add the two unexported helpers; each is called with the mutex held:

```go
// touchLocked marks the entry most recently used.
func (c *taskBodyCache) touchLocked(e *taskBodyEntry) {
	c.lastUsedSeq++
	e.lastUsed = c.lastUsedSeq
}

// storeLocked holds the entry, evicting the least recently used held body first
// when the cache is at its cap.
func (c *taskBodyCache) storeLocked(key string, e *taskBodyEntry) {
	c.lastUsedSeq++
	e.lastUsed = c.lastUsedSeq
	c.entries[key] = e
	for len(c.entries) > taskBodyCacheMaxEntries {
		var oldestKey string
		var oldest uint64
		first := true
		for k, entry := range c.entries {
			if first || entry.lastUsed < oldest {
				oldestKey, oldest, first = k, entry.lastUsed, false
			}
		}
		delete(c.entries, oldestKey)
	}
}
```

`newTaskBodyCache` sets `signals: params.Signals`. `taskBodyKey`, `taskBodyBoundary`, `encodeTaskList` and `gzipBody` are unchanged. The failed-build contract is unchanged: a failed project/encode/gzip/rows call returns the wrapped error, stores nothing and evicts nothing.

### 3. Wire the board to the selected vaults' signals (`pkg/board/board.go`, `pkg/board/tasks.go`)

- In `pkg/board/board.go`'s `New`, replace `Generations: b.snapshot.SnapshotGeneration` with `Signals: b.taskSnapshotSignals`. Change nothing else in `board.go`.
- In `pkg/board/tasks.go`, beside `taskSnapshotRows`, add:
  ```go
  // taskSnapshotSignals returns each selected vault's published-rows signal, in
  // selection order. A vault the query does not select is absent, so a change in
  // it cannot invalidate the query's held body; the bare query selects every
  // configured vault, so a genuine change in any of them does. It reads the same
  // selection ListTasks makes and performs no vault I/O.
  func (b *board) taskSnapshotSignals(selected []Vault) []VaultSignal {
  	signals := make([]VaultSignal, 0, len(selected))
  	for _, vault := range selected {
  		signals = append(signals, VaultSignal{
  			Name:   vault.Name,
  			Signal: b.snapshot.Signal(pageindex.NewKey(vault.Path, vault.TasksFolder)),
  		})
  	}
  	return signals
  }
  ```
  Add the import `"github.com/bborbe/vault-ui/pkg/pageindex"` to `pkg/board/tasks.go` — it is not currently imported there. Do not change `ListTasks`, `taskSnapshotRows` or `selectVaults`. **`ListTasksBody` must load the selection once** — `b.vaults.Vaults(ctx)` then `b.selectVaults(all, query.Vaults)` — and pass that same selection to `b.bodies.Get(ctx, query, selected)`, so the signals are read without a second `Vaults()` load. (`ListTasksBody` does not compute a selection today; it only forwards to `taskSnapshotRows`, which computes one internally and returns rows.) `vaultProvider.Vaults` never caches (its own doc comment says a vault added to vault-cli is visible on the next request) and `vaultconfig.Load` does `os.ReadFile` plus two loader calls, so a second call is uncached I/O added to the very path the ratio criterion measures. **Update `ListTasksBody`'s own doc comment as part of this change** — it currently describes the cache as generation-keyed (`the snapshot generation the cache keys on only moves when a read rebuilds a key`), which this change makes false. Replace that sentence with one describing the selected vaults' published-rows signals. This is the only prompt that can repair it: prompt 3 is forbidden from touching production `.go` files, so a stale comment here ships contradicting the code with no path back.
- In `pkg/board/snapshot.go`, remove `func (s *taskSnapshotStore) SnapshotGeneration() uint64` and its doc comment. `Signal` and `signalSeq` stay. Confirm no remaining reference to `SnapshotGeneration` anywhere:
  ```
  grep -rn 'SnapshotGeneration' pkg/
  ```
  must print nothing.

### 4. Rewire the body-cache test harness and rename the two rows (`pkg/board/taskbody_internal_test.go`)

- In `bodyHarness`, remove the `generation uint64` field, `Generation()` and `setGeneration`. Add `signals []VaultSignal` and:
  ```go
  func (h *bodyHarness) Signals(_ []Vault) []VaultSignal {
      h.mu.Lock()
      defer h.mu.Unlock()
      return append([]VaultSignal(nil), h.signals...)
  }

  func (h *bodyHarness) setSignals(signals ...VaultSignal) {
      h.mu.Lock()
      defer h.mu.Unlock()
      h.signals = signals
  }
  ```
  Build the cache with `Signals: h.Signals` in place of `Generations: h.Generation`.
- Rename the row `drops the held entries when the generation moves` to `drops the held entries when a selected vault's signal moves`. Its body keeps its assertions (two `Get` calls for the same query with a signal move between them give 2 projections, and `entryCount()` is 1): drive it with `h.setSignals(VaultSignal{Name: "a", Signal: 1})` in place of `h.setGeneration(1)`.
- Rename the row `rebuild discards held bodies` to `a rebuild that moves a selected vault's signal discards held bodies`. Keep its assertions (encoder count 1 then 2, `entryCount()` 1) and drive it with `setSignals` in place of `setGeneration`.
- Keep the row `leaves the entry unheld when the generation moves during a build` with its **name and its observable assertions unchanged** (the entry is not held; the next read re-projects, count 2); change only the harness call it uses to move the seam mid-build (`h.setSignals(...)` in place of `h.setGeneration(1)`). Do not rename it — the spec's Constraints list it among the rows that keep their assertions and do not list it among the rows to rename.
- The rows `serves a held body without re-encoding`, `cached and uncached bodies are byte-identical`, the `keys every body-changing field` table, `gzip-compresses the identity form losslessly`, `stores nothing and evicts nothing when a build fails`, `clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `boundary is the earliest transition` and `keeps the lookback and upcoming windows` keep their assertions and must still pass unchanged (they never touch the generation seam). **Every existing `h.cache.Get(ctx, query)` call in this file gains the selection as its third argument — `h.cache.Get(ctx, query, selected)` — because `Get` now takes one; the assertions themselves are unchanged.**
- Update the `Describe("task-list body cache content gate", ...)` harness that prompt 1 added: build its body cache with `Signals` (a function returning `[]VaultSignal{{Name: vault.Name, Signal: store.Signal(pageindex.NewKey(vault.Path, vault.TasksFolder))}}` for each selected vault) in place of the removed `Generations: store.SnapshotGeneration`. Keep its read helper's order (call `store.List` for every configured vault, then `cache.Get`) and keep the rows `a rebuild that reproduces identical rows keeps the held body`, `a rebuild that changes a rendered row discards the held body` and the `a change to any rendered row field discards the held body` table with their assertions unchanged.

### 5. Add the query-scope and cap rows (`pkg/board/taskbody_internal_test.go`)

Extend the content-gate harness (or add a sibling harness in the same file) so it wires **several** vaults/keys to one real `taskSnapshotStore` and one real `taskBodyCache`, with a mutable per-vault row fixture and a mutable configured-vault list. Use a shared `fakeRevisions` and `fakeGenerations`; a "page-index revision move" is `revisions.bump()`, which rebuilds every key, so only the vault whose fixture actually changed advances its signal.

The read helper must **first** call `store.List(ctx, vault)` for **every configured vault** — not just the selected ones — so that a key the query does not select still has a live, current signal (otherwise a change in an unselected vault would never advance its signal and the row would pass trivially) — and **then** call `cache.Get(ctx, query, selected)`, where `selected` is the same selection the cache's `Signals` closure reads. This mirrors `ListTasksBody`'s read-before-consult order.

The body cache's `Signals` returns the current selection's `[]VaultSignal` (reading `store.Signal` per selected vault); its `Rows` calls `store.List` for each selected vault; its `Encode` is counted. Add these rows with these exact names:

- `It("a move in an unselected vault keeps the query's held body")` — wire two keys; a query selects only the first. Read (encoder count 1). Change **only the second vault's** fixture and bump the revision. Read again → encoder count is still **1**. Then change the **first** vault's fixture the same way and bump. Read again → encoder count is **2**.
- `It("the bare query discards its body when any vault's rows change")` — wire three keys; the query has no vault filter (so it selects all three). Read (encoder count 1). Change the first vault's fixture and bump → count **2**. Change the second vault's fixture and bump → count **3**. Change the third vault's fixture and bump → count **4**.
- `It("a selection change discards the bare query's body")` — wire two keys; read the bare query (encoder count 1). Add a third vault to the configured set. Read the bare query again → encoder count is **2** (the selection changed, so the held body is discarded and rebuilt).
- `It("the held-entry count never exceeds the cap")` — request `taskBodyCacheMaxEntries + 8` distinct queries (vary `UpcomingHours`, or `Vaults`, so each has a distinct `taskBodyKey`) with no input move between them, then assert `entryCount()` is exactly `taskBodyCacheMaxEntries`. The row must **also assert a floor**, on a **fresh** harness/cache so the cap+N burst cannot have evicted it: drive two distinct query shapes — one unfiltered (`TaskQuery{}`) and one selecting a single vault (`TaskQuery{Vaults: []string{"a"}}`) — and assert **both** are held (`hasEntry` for each is true). This floor fails a degenerate `cap = 1`, which would otherwise satisfy `count == cap` and every other row.

Run the package under `-race`. The new code needs `>= 80 %` statement coverage.

### 6. Self-check

Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 2, 3 and 5 and Acceptance Criteria 3, 4 and 7 against the tests and name the row that establishes each. Confirm the body-cache key still includes `upcoming_hours` and `session_live`, and confirm `pkg/board/vaults.go`'s `ListAssignees` still reads `b.snapshot.List`.
</requirements>

<constraints>
- The decoded JSON is unchanged: same fields, same order, same values, same escaping and trailing-newline trim. This prompt changes **when** a held body is discarded, never what it contains.
- The body-cache key keeps every query parameter that changes the body, including `upcoming_hours` and `session_live`.
- The cap on held bodies is a fixed constant, never a configuration key.
- The clock boundary and the failure path are unregressed. The existing rows `clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `boundary is the earliest transition`, `stores nothing and evicts nothing when a build fails` and `leaves the entry unheld when the generation moves during a build` keep their assertions and must still pass.
- The existing rows `serves a held body without re-encoding`, `cached and uncached bodies are byte-identical`, the `keys every body-changing field` table and `gzip-compresses the identity form losslessly` keep their assertions and must still pass.
- `pkg/board/vaults.go`'s `ListAssignees` keeps reading parsed rows.
- No new timer, no new configuration key, no change to `SessionRefreshInterval` or `RescanInterval`. No new log line, metric or counter on the body-build path.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns. No raw `go func()` in non-test code.
- Do NOT change the `Board` interface, `ListTasks`, `ListAssignees`, `ListTasksBody` or `taskSnapshotStore.List` signatures. Do NOT change `IndexRevisions` or `generationSource`.
- Do NOT touch `pkg/handler/`, `docs/` or `CHANGELOG.md` — prompt 3 does those. Do NOT touch `pkg/board/vaults.go`.
- Do NOT modify `pkg/board/snapshot_internal_test.go` (prompt 1's renamed row stays as it is).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/board/... ./pkg/handler/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/board.cover ./pkg/board/ && go tool cover -func=/tmp/board.cover | awk '/^total:/ { sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
grep -n 'type VaultSignal struct' pkg/board/taskbody.go
grep -n 'Signals func(selected \[\]Vault) \[\]VaultSignal' pkg/board/taskbody.go
grep -n 'const taskBodyCacheMaxEntries' pkg/board/taskbody.go
grep -n 'func (b \*board) taskSnapshotSignals' pkg/board/tasks.go
```
Each must print at least one line.

```
! grep -rn 'SnapshotGeneration' pkg/
```
Must exit 0 (the process-wide counter accessor is gone).

```
! grep -rn --include='*.go' --exclude='*_test.go' 'time.NewTicker\|time.AfterFunc\|time.Tick\|go func' pkg/board/
```
Must exit 0 (no timer and no raw goroutine in non-test board code).

```
grep -n "a move in an unselected vault keeps the query's held body" pkg/board/taskbody_internal_test.go
grep -n "the bare query discards its body when any vault's rows change" pkg/board/taskbody_internal_test.go
grep -n "a selection change discards the bare query's body" pkg/board/taskbody_internal_test.go
grep -n 'the held-entry count never exceeds the cap' pkg/board/taskbody_internal_test.go
grep -n "drops the held entries when a selected vault's signal moves" pkg/board/taskbody_internal_test.go
grep -n "a rebuild that moves a selected vault's signal discards held bodies" pkg/board/taskbody_internal_test.go
```
Each must print at least one line.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.

```
grep -n 'func (b \*board) taskSnapshotSignals(selected \[\]Vault) \[\]VaultSignal' pkg/board/tasks.go
```
Must print one line — the signals seam takes the already-loaded selection and performs no vault I/O. Note what this check deliberately does **not** assert: the *count* of `b.vaults.Vaults(ctx)` calls in `tasks.go`. That count is **2** today (`ListTasks` and `taskSnapshotRows`) and stays 2 after this change, because both call sites sit in functions this prompt forbids changing — so a count-based check here could never pass, and a check that cannot pass is as useless as one that cannot fail.
</verification>
