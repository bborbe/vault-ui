---
status: completed
summary: Added batch pane resolution and a jump_pane field so a card offers the jump control only when its live session has a WezTerm pane
execution_id: vault-ui-exec-170-jump-control-follows-pane-availability
dark-factory-version: v0.198.7
created: "2026-10-29T10:40:00Z"
queued: "2026-10-29T09:45:09Z"
started: "2026-10-29T09:46:28Z"
completed: "2026-10-29T09:57:02Z"
branch: dark-factory/170-jump-control-follows-pane-availability
---

# The jump control follows pane availability

<summary>
- Resolving panes for many sessions at once now costs one terminal listing instead of one per session, and clicking a card's jump still resolves fresh.
- A card renders the jump control only when its session actually has a terminal pane, so a headless session stops offering a jump that always fails after the click.
- The Live badge keeps saying a headless session is live; only the unreachable affordance goes away.
- The regression tests that used to forbid this now guard it instead of being deleted.
- The earlier pane-resolution spec keeps its old acceptance criteria, each carrying a dated note saying what replaced it.
</summary>

<objective>
Stop the board offering a jump control on a card whose session has no WezTerm pane. A headless worker is live — it stamps the heartbeat — but has no terminal, so the current control always fails with `409 no pane resolves for this session`, an error the operator only sees after clicking. The card must render the control only when a pane actually resolves.
</objective>

<context>
Read `CLAUDE.md` for project conventions, and `docs/dod.md` (this repo's `validationPrompt`) for the Definition of Done this change is held to.

Read these files fully before changing anything.

- `pkg/pane/resolver.go` — `Resolver` interface at line 49. `Resolve` reads the registry for one session's name via `candidateName` (line 156: unique case-insensitive prefix match, then glyph strip, reject empty), runs `wezterm cli list --format json`, and returns a pane id only on exactly one unambiguous title match (inline at lines 133-149). The subprocess runs through `r.exec` → `runCommand(ctx, timeout, …)` with `DefaultResolveTimeout` (lines 70-83).
- `pkg/board/snapshot.go` — `taskSnapshotRow` (line 30) carries every precomputed per-row field. `runBuild` is called synchronously from `List` at line 286 (definition at `:304`), on the caller's goroutine, so a cold `/api/tasks` read does run the build.
- `pkg/board/tasks.go` — `taskRow` (line 36) is what `taskResponse` renders. Three places write a row: `buildTaskRows` (function `:222`, row literal `:255`), `refreshTaskRows` (`:293`, which **recomputes** `sessionState`/`location` at `:324-325` and begins with `copy(refreshed, rows)` at `:312-313`) and `patchTaskRows` (`:376`, whose row literal at `:460` **carries** `sessionState`/`location` over at `:469-470` rather than recomputing). `ListTasks` copies snapshot fields onto the visible `taskRow` at `:102-104`. `taskResponse` is at `:735`.
- `pkg/api/api.go` — `TaskResponse` (line 34). `Location` (`:62-68`) is the model for a Go-only field's doc comment: it says the field is Go-only, why, and when it is null.
- `pkg/factory/api.go` — `CreateAPIHandler` (line 136) already receives `paneResolver` and forwards it only to `CreateMutationService` (`:163`); it builds `board.New(board.Deps{…})` at `:158` **without** it. `factory.CreatePaneResolver` returns a `pane.Resolver` (`:97`).
- `pkg/mutations/mutations.go:105-107` — `mutations.PaneResolver` declares only `Resolve`.
- `pkg/factory/pane_test.go` — `recordingPaneResolver` (`:36-47`) records into a single `calls []string`; `:142` is `Expect(recorder.calls).To(BeEmpty())`; `:159` is the `It("carries no pane-bearing key in any list response")` block, which loops `/api/tasks` **and** `/api/goals`. `paneFixture`/`paneHandler` are at `:51`/`:79`.
- `pkg/factory/api_test.go:37-60` — `newTestAPIHandler` uses `factory.CreatePaneResolver(tempDir())` and `apiFixture()` has no live task, so it cannot drive the `/api/tasks` half.
- `pkg/mutations/tasks.go:130-159` — `JumpTask` resolves at click time and 409s when no pane resolves. **Do not change this file.**
- `src/vault_ui/static/app.js:1575-1590` — the live branch of `sessionButtonHtml`; the comment "Offered on live session state alone" is at `:1586-1587`; the `kind !== 'task'` guard at `:1589`.
- `tests/test_jump_control.py` — `:43-52` asserts `jump_pane` is absent from the helper and from the whole file; `:196-217` is the Playwright case, marked `@pytest.mark.integration` at `:195`.
- `pyproject.toml:87` — `addopts = "-m 'not integration'"`, so `make precommit` skips the Playwright case. `make test-integration` is host-side.
- `docs/pane-resolution.md` — three headings only: `# Pane resolution`, `## When a pane is resolved` (`:11`), `## Session state freshness` (`:51`). **There is no "Failure contract" section**: the failure contract is the bullet at `:44-48`, and the section's opening bullet at `:12-17` says a pane is never resolved on a list read.
- `docs/go-cutover.md` § 6 (`:221`), anchor at `:234`; `scripts/parity/parity.sh:307`, `:325` and `:591`.
- `specs/completed/025-lazy-pane-resolution-at-jump-time.md` — AC3, AC4, AC6; also Desired Behavior 5 (`:131`) and the Failure Modes row at `:159`, which both say a live session with no resolvable pane still offers the control. Note: the file already contains the word "superseded" four times.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`

**Approved scope reversal.** The operator approved overturning spec 025's AC4 and AC6 on 2026-10-29. AC3 is **restated, not preserved**: a build runs synchronously on the caller's goroutine, so a cold read does run the batch. The honest rule is "one batch per build, no per-session shell-out", and that is what the tests must assert. This prompt is deliberately not linked to spec 025 in frontmatter — that spec is completed, and a `spec:` link would send the daemon looking for it in the inbox.
</context>

<requirements>

### 1. `pkg/pane`: add a batch resolver

Add to the `Resolver` interface (line 49) and implement on `*resolver`:

```go
ResolveAll(ctx context.Context, sessionIDs []string) map[string]string
```

Semantics: for every id in `sessionIDs`, the pane id it resolves to, **keyed by the caller's own session id** — not by the registry's id, which may be longer (the registry match is a case-insensitive prefix). An id that resolves to nothing is simply **absent** from the map, never present with an empty value. An empty or nil input returns an empty non-nil map.

Implementation requirements:

- Read the registry **once** (`r.registryNames(ctx, r.registryDir)`) and run `wezterm cli list --format json` **once**, for the whole batch — never once per session. A batch of N sessions must produce exactly one `exec` call.
- **Do not** call `wezterm cli list` at all when no session in the batch has a registry name; return an empty map.
- Extract **both** rules out of `Resolve` into shared helpers so the click path and the batch cannot drift: the registry-name rule (unique case-insensitive prefix, glyph strip, reject empty) **and** the pane-title match (exactly one glyph-stripped title equals the name; zero or several resolve nothing). Leave `Resolve`'s own behaviour byte-identical from the caller's point of view — re-express it in terms of the shared helpers rather than restructuring the click path.
- `ctx` is threaded to `r.exec` exactly as today, so `runCommand(ctx, DefaultResolveTimeout, …)` still bounds the call. A timeout, a cancellation or a `wezterm cli list` failure yields an **empty map** and one debug log — never an error, never a partial panic. The signature carries no error on purpose.

### 2. `pkg/board`: carry the pane on the row, and wire it

- Add `jumpPane *string` to **both** `taskSnapshotRow` (line 30) and `taskRow` (line 36). The snapshot field alone compiles but never reaches the wire: `taskResponse` renders a `taskRow`.
- Add a nil-able dependency to `Deps`, named exactly `Panes`, whose narrow interface (just `ResolveAll(ctx context.Context, sessionIDs []string) map[string]string`) is declared in `pkg/board/board.go` beside `Deps`. `*pane.resolver` satisfies it. Nil means no panes, which keeps existing `board.Deps{}` literals in the tests compiling — add a doc comment on the field naming `pkg/factory/api.go` as the production wiring site, so a future omission is visible rather than silently inert. The name and the declaration file are load-bearing: the verification block greps `pkg/board/board.go` for `ResolveAll` and the `board.Deps{…}` literal for `Panes`.
- In `buildTaskRows`: collect the distinct non-empty `claude_session_id` values of the rows whose `sessionState` is `live`, call `ResolveAll` **once**, and set each live row's `jumpPane` from the map.
- In `refreshTaskRows`: do the same in the same pass that recomputes `sessionState`/`location` (`:324-325`). It begins with `copy(refreshed, rows)` (`:312-313`), so you must **also set `jumpPane = nil` on every row that is not live** — a row that leaves `live` otherwise keeps the pointer it was copied with, and a quiet card would carry a non-null `jump_pane`.
- In `patchTaskRows`: carry `jumpPane` over from the held row (`rows[at].jumpPane`), exactly as `sessionState`/`location` are carried at `:469-470`. Do not recompute.
- In `ListTasks`: copy `visibleTask.jumpPane = row.jumpPane` beside the existing `sessionState`/`location` copies at `:102-104`.
- In `taskResponse` (line 735): set `JumpPane: row.jumpPane`.

### 3. Wire it in production, or nothing ships

`pkg/factory/api.go` must pass the pane resolver it already receives into `board.Deps` at line 158. Widen `CreateAPIHandler`'s `paneResolver` parameter type from `mutations.PaneResolver` (which declares only `Resolve`) to a type that also declares `ResolveAll` — `pane.Resolver` after §1 satisfies it, and `factory.CreatePaneResolver` already returns it. Keep the `CreateMutationService` hand-off working.

With the `Deps` field left nil, `jump_pane` is always null, the frontend renders no jump control on any card, and every test still passes. State this in a comment beside the wiring.

### 4. `pkg/api`: add the field

Add to `TaskResponse`:

```go
// JumpPane is the WezTerm pane the card's live session resolves to, so the
// card can offer a jump only when one exists. It is Go-only (see the package
// doc) and null unless the session is live and exactly one pane's title
// matches its registry name — the same rule the jump endpoint applies.
JumpPane *string `json:"jump_pane"`
```

Use the JSON name `jump_pane` exactly: it is the name the superseded Python backend already emits, and `scripts/parity/parity.sh:325` already strips it from both sides.

Update **both** stale comments in `scripts/parity/parity.sh`: `:307` ("intentionally absent from the Go backend's task responses") and `:591` ("the frontend no longer reads the removed `jump_pane` field"). State that both backends now emit it and that the `del()` keeps the comparison honest while the two resolution paths differ.

### 5. Frontend

In `src/vault_ui/static/app.js`, the live branch (`session_state === 'live'`):

- The Live badge is rendered exactly as today, with its take-over handler byte-identical.
- The jump control is rendered **only when `item.jump_pane` is truthy**, for `kind === 'task'`. A live card with no pane renders the badge and nothing else.
- Rewrite the block comment at `:1586-1587`: it currently says the control is "Offered on live session state alone". The new rule is that it follows pane availability — a headless session is live but has no terminal.

Bump the cache-bust token on `app.js` in `src/vault_ui/static/index.html`, or a warm browser keeps the old gating and the change is invisible.

### 6. Tests — move the contract, do not delete it

**`pkg/factory/pane_test.go`:**

- `recordingPaneResolver` (`:36-47`) records both methods into one `calls []string`, which cannot distinguish a `Resolve` call from a `ResolveAll` call. Add a second field (`resolveCalls`, `allCalls`) and implement `ResolveAll`, or the assertions below cannot fail.
- The `:142` assertion becomes the new rule: a list read makes **at most one** `ResolveAll` call and **never** a single-session `Resolve`. Keep a positive control — the jump endpoint still calls `Resolve` exactly once — so a dead double is distinguishable from a passing assertion.
- The `:159` block inverts: for `/api/tasks`, a live card with a resolvable pane **carries** `jump_pane` with that pane id, and a live card with no pane carries `null`. `/api/goals` **keeps carrying no pane-bearing key** — assert that half unchanged.
- Use `paneFixture` + `paneHandler` + `recordingPaneResolver` (`:51`, `:79`, `:128`) for the `/api/tasks` half; `newTestAPIHandler`/`apiFixture` have no live task and cannot produce it. Keep `apiFixture`/`newTestAPIHandler` for the `/api/goals` half.

**`pkg/pane/resolver_test.go`** (where `Describe("Resolver")` lives at `:106`) — cover `ResolveAll`:

- N sessions against one pane list produce exactly one `exec` call (count the injected `ExecFunc` calls).
- **The map is keyed by the caller's own id, not the registry's**: pass a truncated prefix of a registry id (so the registry's id is longer) and assert the map carries a key equal to the truncated input. The board looks up by `claude_session_id`, so keying by the registry id would compile, pass every other case, and silently break the board.
- Two sessions resolving to the same pane both map to it.
- Zero matches and two-or-more matches both leave the session absent.
- A session with no registry entry is absent, and a batch where **no** session has a registry entry makes no `exec` call at all.
- An empty input returns an empty map and makes no `exec` call.
- A failing `exec` returns an empty map, no error.

**`tests/test_jump_control.py`:**

- `:43-52` inverts: the helper **does** read `jump_pane`, and the whole-file absence assertion is removed (the field is now read, so asserting its absence from `APP_JS` would be false). Keep `kind !== 'task'` as the guard that a goal card has no jump route.
- The Playwright case at `:196-217` becomes one case with both halves:
  - **Tab half** — assert `.jump-btn` count is **1** before clicking (the current case clicks without ever asserting a count), click, and assert exactly one `POST …/jump` was recorded. **Keep the existing assertions** that the page did not navigate (`page.url == before`) and that `.live-badge` count is 1 (`:216-217`) — they are the "must not navigate" regression the case exists for.
  - **Headless half** — add a **second task to the fixture** with its own `claude_session_id`, a fresh transcript via `_write_transcript` so it classifies **live**, and **no entry in `_pane_map`**; assert its `.jump-btn` count is 0 and that no `/jump` request was recorded for it. A sessionless card would render no control on the unfixed build too and would prove nothing.

`pyproject.toml:87` sets `addopts = "-m 'not integration'"`, so the browser half does **not** run under `make precommit`. The container proof is the module-level static assertions only; the browser half is the host-side `make test-integration`. Do not report a green `make precommit` as evidence that the rendered behaviour was exercised.

### 7. Documentation and the spec record

- `docs/pane-resolution.md` § When a pane is resolved: rewrite the opening bullet (`:12-17`) — it still says a pane is never resolved on a list read and that a list read spawns no subprocess. The list response now carries pane availability, resolved once per snapshot build (and once per session-generation refresh) with a single `wezterm cli list`. The click still resolves fresh and validates — that is what keeps a recycled pane id from jumping to the wrong tab.
- `docs/pane-resolution.md`: there is **no "Failure contract" section**. The failure contract is the bullet at `:44-48` ("The jump control stays offered in that case, because it follows live session state rather than pane data"). Rewrite that bullet: the jump control follows pane availability, not session state, so a live session whose pane cannot currently be resolved shows no control until the next build.
- `docs/go-cutover.md` § 6: its `-S'jump_pane'` anchor at `:234` is only a fixed point while `jump_pane` is absent from `app.js`; once the read returns, `-S` re-anchors on the new commit and the guard silently changes meaning. Replace it with the `v0.82.0` tag (the release spec 025 landed in) as the baseline, and update the prose around it to say why.
- `specs/completed/025-lazy-pane-resolution-at-jump-time.md`: add a dated `2026-10-29` note under **AC3, AC4, AC6, Desired Behavior 5 (`:131`) and the Failure Modes row at `:159`**. **Do not delete the old text.** The note must say: the operator approved the reversal on 2026-10-29; AC4 and AC6 are superseded because the list response now carries `jump_pane` and the control follows it; AC3 is **restated** — the build runs synchronously on the caller's goroutine, so a cold read does run the batch, and the rule is now "one batch per build, no per-session shell-out".
- `CHANGELOG.md`: one bullet under `## Unreleased` naming `jump_pane`, following `changelog-guide.md`. `## Unreleased` already exists and already holds a bullet — add yours, do not replace the section.

</requirements>

<constraints>
- Do not change `pkg/mutations/tasks.go` — the click path keeps resolving fresh at click time, and the 409 stays for any caller that asks for a pane that does not exist.
- Do not add a background refresher, a timer, or a pane cache. The resolution happens inside the existing build and refresh paths and nowhere else.
- Do not change what the Live badge means, how the heartbeat poll works, or the session-state classification contract.
- Do not give a headless worker a pane; it has no terminal.
- Do not remove `jump_pane` from the Python backend (`src/vault_ui/api/models.py`, `tasks.py`) — it is the rollback copy and the parity harness compares against it.
- Do not change `/api/goals` — a goal card has no jump route and must keep carrying no pane-bearing key.
- No new config knob, no new route, no new query parameter.
- Do not commit — dark-factory handles git.
</constraints>

<verification>
`make precommit` must exit 0. `make parity` must exit 0 — the harness compares the Go and Python bodies and `del(.jump_pane, …)` means a divergence in the field's value is invisible to it, so this is the only end-to-end check that both backends still serve the same board. In addition:

```
grep -q 'ResolveAll' pkg/pane/resolver.go && grep -q 'ResolveAll' pkg/board/tasks.go && grep -q 'ResolveAll' pkg/board/board.go
```

Must exit 0 (the batch method exists and both the board's dependency seam and its call sites use it).

```
grep -q 'JumpPane' pkg/api/api.go && grep -q 'jump_pane' pkg/api/api.go
```

Must exit 0 (the wire field and its JSON name are present).

```
grep -q 'paneResolver pane.Resolver' pkg/factory/api.go && awk '/board.New\(board.Deps\{/,/\}\)/' pkg/factory/api.go | grep -q 'Panes'
```

Must exit 0 (the resolver is actually passed into `board.Deps`; a nil field ships the feature inert).

```
grep -q 'item.jump_pane' src/vault_ui/static/app.js
```

Must exit 0 (the frontend reads the field).

```
grep -q 'jump_pane' tests/test_jump_control.py && ! grep -q '"jump_pane" not in APP_JS' tests/test_jump_control.py
```

Must exit 0 (the helper now reads the field and the old whole-file absence assertion is gone — the first half proves the file was edited, not just that a line was deleted).

```
test "$(grep -c 'ResolveAll' pkg/factory/pane_test.go)" -ge 2
```

Must exit 0 (the test double implements the batch and at least one assertion names it).

```
awk '/^## /{sec=$0} sec=="## Unreleased" && /jump_pane/{n++} END{exit !(n>=1)}' CHANGELOG.md
```

Must exit 0 (an Unreleased bullet naming this change exists — a bare `- fix:` would match the pre-existing bullet already at line 7).

```
test "$(grep -c '2026-10-29' specs/completed/025-lazy-pane-resolution-at-jump-time.md)" -ge 5
```

Must exit 0 (the date appears nowhere in the file today, so five occurrences prove the notes landed).

```
grep -q 'AC3 — a list read resolves no panes' specs/completed/025-lazy-pane-resolution-at-jump-time.md && grep -q 'AC4 — no list response carries a pane-bearing key' specs/completed/025-lazy-pane-resolution-at-jump-time.md && grep -q 'AC6 — the jump button depends only on a live session' specs/completed/025-lazy-pane-resolution-at-jump-time.md
```

Must exit 0 (the original acceptance criteria still exist — the notes supersede, they do not delete). `grep -c 'superseded'` cannot be used for this: the file already contains four.

`go test ./pkg/pane/... ./pkg/board/... ./pkg/factory/...` must exit 0.
</verification>
