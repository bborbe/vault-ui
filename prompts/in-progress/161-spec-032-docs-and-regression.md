---
status: approved
spec: [032-task-body-cache-invalidation]
created: "2026-10-13T22:16:50Z"
queued: "2026-10-13T22:55:00Z"
branch: dark-factory/task-body-cache-invalidation
---

# Docs and CHANGELOG, plus the byte-identity and failure-path regression rows

<summary>
- The task-list body cache's documented discard rule now describes what the code does: a held body is discarded when a vault the query selects publishes different rows, or when the clock crosses the body's boundary — never by a vault the query does not read.
- The document also records that the held set is bounded.
- The CHANGELOG gains one `fix:` entry describing the content-gated, query-scoped invalidation.
- A new test row proves a body served after an identical-row rebuild is byte-identical to a fresh build, and it forces the rebuild rather than waiting for one.
- The existing byte-identity and failure-path rows are confirmed to still pass unchanged.
- The snapshot rebuild model and the wire shape are documented as unchanged.
- No operator probe is added — the two Post-Deploy probes stay on the spec's ladder.
</summary>

<objective>
Write the durable documentation of the new invalidation rule, record the change in the CHANGELOG, and add the one regression row the fix needs (a held body served across an identical-row rebuild is byte-identical to a fresh build). This is prompt 3 of 3: prompts 1 and 2 have landed the content gate and the query scope.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it is absent in this checkout) and `docs/dod.md` (the Definition of Done the validation step checks).

Read the spec `specs/in-progress/032-task-body-cache-invalidation.md` in full. This prompt covers Desired Behaviors 4 and 6 and Acceptance Criteria 5 and 6. The two Post-Deploy probes (AC8 and AC9) are the spec's operator rung — they are not this prompt's scope and must not be added here.

Read these files fully before changing anything:
- `docs/page-index.md` — § `## Task-list body cache` (the section this prompt updates) and § `## Task-list snapshot` (the section this prompt must **not** change). The body-cache section currently says the cache holds one body per `(task-list snapshot generation, query)`, is keyed by "the task-list snapshot generation plus every query parameter", and that "A snapshot rebuild moves the generation and drops the superseded generation's bodies."
- `CHANGELOG.md` — currently the newest heading is `## v0.95.0` and there is **no** `## Unreleased` section; one must be added above `## v0.95.0`.
- `pkg/board/taskbody_internal_test.go` — the `Describe("task-list body cache content gate", ...)` block and its store↔cache harness (added by prompt 1, rewired by prompt 2). The existing row `cached and uncached bodies are byte-identical` and the failure-path rows `clock boundary discards held bodies`, `never expires a body with no clock-derived rows` and `stores nothing and evicts nothing when a build fails` live in the `Describe("taskBodyCache", ...)` block.
- `pkg/board/taskbody.go` — `encodeTaskList`, `taskBodyBoundary`, `Get` (read only, to confirm what the doc should say).
- `pkg/board/snapshot.go` — `Signal(key)` and `runBuild`'s content gate (read only).
- `pkg/board/vaults.go` — `ListAssignees` (must keep reading `b.snapshot.List`; not changed here).

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format, prefix, anti-patterns.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`
</context>

<requirements>

### 1. Update `docs/page-index.md` § *Task-list body cache*

Rewrite the section so it describes the content-gated, query-scoped discard the code now performs. Do not change any other section, and in particular do **not** restate or edit § *Task-list snapshot* — the rebuild model (triggers, row patch, session refresh, atomic swap, failed-rebuild-keeps-serving) is unchanged and documented there.

- The section's intro sentence: replace "one rendered `GET /api/tasks` response body per (task-list snapshot generation, query)" with the selected vaults' published-rows signals in place of the generation, e.g. "per (the published-rows signals of the vaults its query selects, query)".
- The bullet **What it holds.** — replace "(generation, query)" with "(selected-vault signals, query)" and state that the held set is bounded at a fixed number of entries (the cache's cap) with least-recently-used eviction, so a client requesting many distinct queries cannot grow it without bound. State that the cap is a fixed constant, not a configuration key.
- The bullet **What it is keyed by.** — replace "The task-list snapshot generation plus every query parameter" with the published-rows signals of the vaults the query selects, plus every query parameter that changes the body, including `upcoming_hours` and `session_live`.
- The bullet **When it is discarded.** — replace the whole bullet with the new rule, covering all three parts:
  - a held body is discarded when the published-rows signal of a vault **the query selects** advances — a rebuild that reproduces identical rows leaves the signal (and the held body) alone, and a rebuild that changes a rendered row advances it and discards the affected queries' bodies;
  - a change in a vault the query does **not** select discards nothing for that query;
  - the bare query selects every configured vault, so a genuine change in any of them discards it, and a change to the configured set discards it too;
  - the clock crossing the next instant at which `upcoming`, `recently_completed` or the recently-completed phase override can change drops the body; that instant is derived from the request's clock reading and its upcoming-hours window, not from a timer;
  - a discarded body is never served again.
- Leave the bullets **What a hit costs.**, **What it does not change.** and **The wire shape.** as they are (the wire shape, `Vary: Accept-Encoding` and the identity/gzip negotiation are unchanged).

After the edit, `grep -n '^## ' docs/page-index.md` must still list exactly the same `##` headings, and the body-cache section must no longer claim that a snapshot rebuild drops the superseded generation's bodies (the term "generation" may still appear in § *Task-list snapshot*, which is unchanged).

### 2. Add the CHANGELOG entry

Add a `## Unreleased` section directly above `## v0.95.0` in `CHANGELOG.md`, with exactly one `fix:` bullet, following `changelog-guide.md`: `- fix: <what> [context]`, one bullet per logical change, specific (name the store's content gate and the body cache's selected-vault scoping). Do not copy shell comments from this prompt's `<verification>` section. Do not use the prompt filename as the entry. Do not edit or move the `# Changelog` preamble or any released section. Suggested shape:

```
## Unreleased

- fix: Discard a held `GET /api/tasks` body only when the content it was rendered from actually changes — the task-list store now advances a key's published-rows signal only when that key's rebuild produces rows that differ from the rows it last published, and the body cache gates on the published-rows signals of the vaults its query selects instead of one process-wide rebuild counter, so an identical-row rebuild or a session refresh that reclassifies nothing no longer drops a correct body and a change in a vault a query does not select no longer invalidates it; the number of held bodies is capped with least-recently-used eviction, replacing the incidental bound the blanket generation drop provided.
```

### 3. Add the byte-identity-after-identical-rebuild row (`pkg/board/taskbody_internal_test.go`)

In the `Describe("task-list body cache content gate", ...)` block, add:

- `It("a body served after an identical-row rebuild is byte-identical to a fresh build")` — this row **forces** the rebuild it tests rather than waiting for one: read the query once (encoder count 1), move the page-index revision (`revisions.bump()`) with the fixture **unchanged** so the store rebuilds byte-identical rows, then read again. Assert the encoder count is still **1** (the held body was served, not rebuilt), and assert the served body's `Identity` equals `encodeTaskList(<the harness's projected responses for the same query>)` — call the harness's own project function for the query directly and encode its result — i.e. the held body is byte-identical to what the uncached path produces for the same query at the same clock instant. Use the query and the harness's project function that the block already wires; do not reach for `bodyFixture`/`projectBody` from the other `Describe`.

Do not change the row `cached and uncached bodies are byte-identical` or any other existing row's assertions.

### 4. Confirm the failure-path rows are unregressed

Do **not** modify these rows; confirm each still passes with its assertions unchanged. If one does not pass, report it as a blocker and stop — do **not** edit production code in this prompt (the constraints below forbid it, and the files involved belong to prompts 1 and 2), and do not weaken the row to make it pass:
`clock boundary discards held bodies`, `never expires a body with no clock-derived rows`, `stores nothing and evicts nothing when a build fails`, `leaves the entry unheld when the generation moves during a build`, `boundary is the earliest transition`, `serves a held body without re-encoding`, `cached and uncached bodies are byte-identical`, the `keys every body-changing field` table and `gzip-compresses the identity form losslessly`.

### 5. Leave the Post-Deploy probes on the spec's ladder

Do NOT add an operator command, a `curl` to a running board, a `POST /api/cache/reload` invocation, or a "run this on the host" note to this prompt's `<verification>` or `<constraints>`. Acceptance Criteria 8 and 9 (the spurious-invalidation protocol and the ten-window ratio probe) are the spec's Post-Deploy evidence and stay on the spec's Verification § *Operator-executable* rung. Nothing in this prompt runs against a live board.

### 6. Self-check

Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 4 and 6 and Acceptance Criteria 5 and 6 against the test and the doc and name what establishes each. Confirm `docs/page-index.md` § *Task-list snapshot* is unchanged in content, `pkg/board/vaults.go`'s `ListAssignees` still reads `b.snapshot.List`, and no production file changed in this prompt.
</requirements>

<constraints>
- The decoded JSON is unchanged: same fields, same order, same values, same escaping and trailing-newline trim.
- `Vary: Accept-Encoding` is set on every `/api/tasks` response, gzip and identity alike; the gzip form carries `Content-Encoding: gzip`. The identity/gzip negotiation is unchanged.
- `GET /api/assignees` keeps reading the published rows; `/api/vaults`, `/api/goals` and `/api/topics` keep their existing path. `pkg/board/vaults.go`'s `ListAssignees` still reads `b.snapshot.List`.
- No new timer, no new configuration key, no change to `SessionRefreshInterval` or `RescanInterval`. No new log line, metric or counter on the body-build path.
- Do NOT restate or edit the invalidation model in `docs/page-index.md` § *Task-list snapshot*; update only § *Task-list body cache*.
- Do NOT edit `README.md`.
- Do NOT change any production `.go` file. This prompt edits `docs/page-index.md`, `CHANGELOG.md` and `pkg/board/taskbody_internal_test.go` only.
- Do NOT modify `pkg/board/snapshot_internal_test.go`.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns.
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
grep -n 'a body served after an identical-row rebuild is byte-identical to a fresh build' pkg/board/taskbody_internal_test.go
```
Must print at least one line.

```
grep -n '^## ' docs/page-index.md
```
Must list `Task-list snapshot`, `Task-list body cache`, `Staleness bounds`, `Incremental updates`, `Frame ordering`, `Key derivation` and `Page index store`.

```
grep -A4 '^## Unreleased' CHANGELOG.md
```
Must show a `fix:` bullet about the content-gated, query-scoped task-list body invalidation.

```
grep -n 'least-recently-used' docs/page-index.md
```
Must print at least one line (the LRU eviction is documented, by the name requirement 1 mandates). `least-recently-used` is absent from the file today, so this check can fail — the earlier `cap` form could not, because it matched the pre-existing word `escape` inside the line `ForceReload()` is the operator escape hatch.

```
! grep -q 'superseded generation' docs/page-index.md
```
Must exit 0 (the old discard claim is gone from the body-cache section). The claim wraps across two lines in the source, so a literal match on the whole sentence never fires — this form does, and `superseded generation` occurs exactly once today, so it can fail.

```
grep -n 'selected-vault signals' docs/page-index.md
```
Must print at least one line (the new "(selected-vault signals, query)" keying from requirement 1 is in place; the phrase is absent today).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
