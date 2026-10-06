---
status: approved
spec: [028-precompute-task-list-snapshot]
created: "2026-10-06T20:28:50Z"
queued: "2026-10-06T20:57:28Z"
branch: dark-factory/precompute-task-list-snapshot
---

# Document the task-list snapshot staleness bounds and add the changelog entry

<summary>
- `docs/page-index.md` gains the task-list snapshot as a second published snapshot derived from the page snapshot, with its own rebuild triggers and staleness bound.
- The page-derived fields keep the page-index staleness bound; the session-derived fields (`session_state` and the activity date) carry the slower session-refresh bound instead, and the doc now says so.
- The write-visibility rule is stated for the whole chain: a write marks the page index, the mark moves the key's revision, and the next read rebuilds before serving.
- The failed-rebuild, cold-read-sharing and atomic-swap rules are written down next to the page index's own rules, so the invariant is one document.
- The liveness classification doc notes that the classification inputs are cached with a timestamp; the four outcomes, the signal order and the five-minute window are unchanged.
- A single `feat:` changelog bullet covers the whole change under `## Unreleased`.
- No code changes.
</summary>

<objective>
Update `docs/page-index.md` with the task-list snapshot's staleness bounds, rebuild triggers, atomicity and write-visibility rules; note the timestamped input cache in `docs/liveness-classification.md`; and add the spec's `feat:` entry under `## Unreleased` in `CHANGELOG.md`.
</objective>

<context>
Read the spec `specs/in-progress/028-precompute-task-list-snapshot.md` in full, especially Constraints and Failure Modes. This prompt is prompt 4 of 4 and runs after prompts 1–3 have landed the code, so read the code first and describe what it actually does:
- `pkg/sessionsnapshot/snapshot.go` — `SessionRefreshInterval`, the refresh loop, the transcript epoch cache, `Generation`.
- `pkg/board/snapshot.go` — `taskSnapshotStore`, the revision/generation dirty check, the single-flight build, the failed-rebuild retention, `snapshotBuildTimeout`.
- `pkg/pageindex/pageindex.go` — `Revision` and the published-snapshot invariant.
- `pkg/board/tasks.go` — `ListTasks` and `buildTaskRows`.
- `pkg/mutations/mutations.go` — `markVaultDirty`, `taskWritten`, `goalWritten`, `itemWrittenSilently` (the marks that precede a frame).
- `pkg/watchrefresh/handler.go` — the event-side refresh that precedes a frame.
- `docs/page-index.md`, `docs/liveness-classification.md`, `docs/optimistic-writes.md`, `CHANGELOG.md`.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/git-workflow.md`
</context>

<requirements>

### 1. `docs/page-index.md`

Extend the document (do not rewrite it) with the task-list snapshot as a **second** published snapshot derived from the page snapshot, and state:
- The board holds one immutable task-list snapshot per page-index key (a vault root plus its tasks folder). It is derived from the page snapshot and the session snapshot; it is never a field on `domain.Page` and is never mutated after publication.
- **Rebuild triggers.** A key's task-list snapshot is rebuilt when the page index's revision for that key moves (a publication or a mark) or when a new session snapshot is available (the session refresh). A rebuild that started before an invalidation does not clear it: the build records the revision and generation it saw at build start, so a racing invalidation leaves the entry dirty and the next read rebuilds.
- **Atomic swap.** A rebuild produces a complete new row list and swaps it in atomically; a reader concurrent with a rebuild sees either the whole previous list or the whole new one, never a mixture.
- **Cold-read sharing.** Two concurrent first reads of a key with no snapshot share exactly one build; every waiter observes its result.
- **Failed rebuild.** A failed rebuild keeps serving the previous snapshot and logs the error with the key; the next event, dirty read or refresh retries. A fresh process starts empty and builds on demand.
- **Staleness bounds.** A warm `/api/tasks` read returns the published rows and performs no page-storage call, no transcript probe and no process spawn. Page-derived fields keep the existing page-index bound. The session-derived fields (`session_state` and the activity date) carry the **session-refresh bound** instead: they are refreshed on a fixed interval of at least 60 s (`SessionRefreshInterval`), so they can lag reality by at most one refresh plus one rebuild. State this explicitly as the one place the existing bounds no longer apply.
- **Write visibility.** The write-visibility rule now runs the whole chain: a synchronous or queued write marks the page-index key before its `Publish*Updated` frame; the mark moves the key's revision; the next read of that key's task-list snapshot rebuilds, and the rebuild's vault-cli list walk resolves the pending page mark before reading. A watcher event's page read likewise moves the revision before its frame is broadcast. Do not claim a stronger bound than that.
- **One accepted cost.** Because a rebuild's own page read can resolve a pending write mark (moving the revision), a write can cost one extra rebuild on the next read; the extra build lists an unchanged folder and classifies from the cached session snapshot.
- **No new timer.** The task-list snapshot owns no timer; the only timer this change adds is the session refresh at ≥ 60 s.

Keep the existing sections (staleness bounds, incremental updates, frame ordering, key derivation) accurate. In particular, the frame-ordering section must still say a task/goal watcher frame is sent only after a read that started after that event has been applied — that contract is unchanged and asserted by the `pkg/watchrefresh` tests.

### 2. `docs/liveness-classification.md`

Add a short subsection stating that the classification inputs are now read from a process-wide, timer-refreshed session snapshot: the registry ids and the live `--resume`/`--session-id` ids (one `ps` scan per refresh) and the transcript mtimes (at most one probe per session per refresh) are cached with a timestamp and refreshed on a fixed interval of at least 60 s. The `ps` cross-check stays signal #4 in the fixed order, the four outcomes and the five-minute window are unchanged, and a cached verdict never outlives its refresh window. Do not change the signal-order or outcome tables.

### 3. `docs/optimistic-writes.md`

Add one sentence to the frame-timing section: the page-index mark a write performs also invalidates that key's task-list snapshot, so the next `/api/tasks` read rebuilds before serving. Do not restate the whole chain.

### 4. `CHANGELOG.md`

Under `## Unreleased`, append one `feat:` bullet covering the whole change, following `changelog-guide.md`:
- `/api/tasks` is served from a precomputed, atomically published task-list snapshot rebuilt only when its inputs change (a page-index change or a session refresh), so a warm request does no vault read, transcript probe or `ps` spawn; session-derived fields refresh on a ≥ 60 s interval; the JSON contract, the frame-ordering contract and the write-visibility guarantee are unchanged.
Use the `feat:` prefix (this is a feature). If `## Unreleased` already exists, append to it rather than replacing it. One bullet, not one per file.

</requirements>

<constraints>
- Documentation only: no `.go` file, no test, no route, no configuration is changed by this prompt.
- The documented staleness bounds must match the code as landed by prompts 1–3; read the code, do not describe the spec's intent where the code differs.
- The JSON contract of `/api/tasks` is unchanged; do not document a new field, route or query parameter.
- `docs/liveness-classification.md` stays authoritative for classification: the four outcomes, the fixed signal order (registry, transcript recency, live process) and the five-minute window are unchanged.
- Do NOT modify anything under `src/vault_ui/`; do NOT weaken anything under `scripts/parity/`.
- Do NOT commit — dark-factory handles git.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
grep -q 'session-refresh' docs/page-index.md && grep -q 'SessionRefreshInterval' docs/page-index.md
```
Must exit 0 (the session-refresh bound is documented).

```
grep -q 'task-list snapshot' docs/page-index.md && grep -q 'task-list snapshot' docs/optimistic-writes.md
```
Must exit 0 (the second published snapshot is documented where the staleness and write rules live).

```
sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -qE '^\s*-\s*feat:'
```
Must exit 0 (a `feat:` bullet is under `## Unreleased`).

```
sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -qE '^\s*-\s*fix:'
```
Must exit 0 (the pre-existing `fix:` bullet was appended to, not replaced or renamed away).

```
grep -rn 'SessionRefreshInterval\|RescanInterval' pkg/ | grep -q '60'
```
Must exit 0 (AC8: the session-refresh interval is configured at ≥ 60 s).

```
! grep -rnE 'NewTicker|time\.AfterFunc' pkg/pageindex pkg/board
```
Must exit 0 (AC8: no sub-60 s ticker in the snapshot packages).
</verification>
