---
status: completed
summary: Carried the attention store's per-session location through heartbeat→sessionstate→sessionsnapshot→board→api and rendered it on the shared live badge as `● Live·<location>`, with a plain `● Live` for sessions the store gives no location for.
execution_id: vault-ui-exec-159-live-badge-session-location
dark-factory-version: v0.198.7
created: "2026-10-13T20:17:41Z"
queued: "2026-10-13T21:24:31Z"
started: "2026-10-13T21:26:14Z"
completed: "2026-10-13T21:37:04Z"
branch: dark-factory/159-live-badge-session-location
---

# Show the live session's location in the board's live badge

<summary>
- The board's live badge shows where a live session runs — `● Live·local`, `● Live·pod` — instead of a bare `● Live`.
- The location already exists in the attention store's session-heartbeat record; the board discards it today.
- A live session whose heartbeat record carries no location renders the plain `● Live`, with no dangling separator. (One of the 38 live rows on the running board currently carries an empty location.)
- The location reaches the browser as a new nullable `location` field on both the task-list and the goal-list response.
- A live session the store gives no location for — and a session classified live by transcript recency or a live resume process rather than by the store — carries no location and renders the plain badge.
- Non-live cards are untouched: quiet, indeterminate and session-less cards render exactly as they do today.
- The live badge stays the take-over affordance — same role, tabindex and click behaviour; only its visible text changes.
- Goal cards render the location too — the same badge builder serves both card kinds, so one change covers tasks and goals.
- Nothing about the liveness classification changes: the four outcomes and the fixed signal order are untouched.
</summary>

<objective>
Make the board's live badge name the session's location (`● Live·local`) so an operator scanning the wall can tell a local session from a cluster/pod session without opening anything. The location is already on every row of the attention store's session-heartbeat response, but the store read drops it, so nothing downstream can see it: the value must be carried from the store through the in-memory live-session state and the session snapshot to the task-list and goal-list responses, then rendered by the badge, which serves both card kinds. The take-over affordance on that badge must be preserved exactly, and a live session with no location must keep rendering the plain `● Live`.
</objective>

<context>
Read `CLAUDE.md` at the repo root and `docs/dod.md` (the project's Definition of Done and this repo's `validationPrompt`) before starting.

Read these files in full before editing anything — the change threads one value through all of them:
- `pkg/heartbeat/heartbeat.go` — the attention store's session-heartbeat read. `heartbeatRow` declares only `SessionID` and `Live`; the `Store` interface exposes `LiveSessionIDs(ctx) ([]string, bool)` and `IsLive(ctx, sessionID) (bool, bool)`. The store's payload carries a `location` per row that is never decoded.
- `pkg/sessionstate/sessionstate.go` — the in-memory live-id set the board reads, refreshed by the watcher. `State.RegistrySessionIDs`, `State.Replace(ids []string)`, `State.MarkUnknown`, `WatchParams.Read`.
- `pkg/sessionsnapshot/snapshot.go` — the process-wide, timer-refreshed session snapshot. `Params.Registry`, `Snapshot.RegistrySessionIDs`, `RefreshOnce`.
- `pkg/board/board.go` — the `SessionSignals` interface (satisfied by the session snapshot).
- `pkg/board/tasks.go` — `taskRow`, `buildTaskRows`, `refreshTaskRows`, `patchTaskRows`, `ListTasks`, `taskResponse`, `sessionStatePtr`.
- `pkg/board/goals.go` — `ListGoals`, `goalRow`, `goalsForVault`, `goalResponse` (which classifies the session per row on the request path, unlike the task path).
- `pkg/board/snapshot.go` — `taskSnapshotRow`.
- `pkg/factory/api.go` — `CreateSessionSnapshot` (the **only** caller of `Params.Registry`: its closure at ~line 190 reads `state.RegistrySessionIDs(ctx)` / `state.LiveIDsKnown()` and returns `([]string, bool)`) and `CreateSessionStateWatcher` (the watcher's `Read:` at ~line 232). Both closures must move to the new type in this change.
- `pkg/api/api.go` — `TaskResponse` and `GoalResponse` (its package doc explains the Go-only-field convention this change follows).
- `src/vault_ui/static/app.js` — `sessionButtonHtml(kind, item)` at ~line 1549 builds the live badge for **both** card kinds (the task card calls it at ~line 1765, the goal card at ~line 1828); `escapeHtml` (~line 2367).
- `src/vault_ui/static/index.html` — the `app.js?v=...` cache-bust token.
- `scripts/parity/parity.sh` — `normalize()` (the Go-only-field strip list).
- `docs/liveness-classification.md` — the durable liveness contract.
- `tests/test_blocked_badge.py` — the repo's established static-assertion pattern for frontend-only behaviour, which the new JS test follows.

The chain after this change, in order, so the value has one path to follow:
`heartbeat.Store.LiveSessions` → `factory.CreateSessionStateWatcher`'s `Read` → `sessionstate.State.Replace` / `State.Sessions` → `factory.CreateSessionSnapshot`'s `Registry` closure (`state.Sessions(ctx), state.LiveIDsKnown()`) → `sessionsnapshot.Snapshot.RegistrySessionIDs` / `RegistryLocation` → `board.SessionSignals.RegistrySessionIDs` / `RegistryLocation` → `session.ClassifyParams.RegistrySessionIDs` → `taskSnapshotRow.sessionState` / `location` → `taskRow.sessionState` / `location` → `api.TaskResponse.SessionState` / `Location` and `api.GoalResponse.SessionState` / `Location` → `sessionButtonHtml` → `liveBadgeLabel`.

The goal path is shorter: `ListGoals` → `goalsForVault` → `goalResponse`, which classifies the session per row on the request path — so the goal-side location lookup sits beside that existing classification rather than in a precomputed row.

The board's task request path performs no store call, no process spawn and no transcript probe — every derived field is precomputed at build time in `buildTaskRows` and carried by `taskSnapshotRow`. The location must be precomputed the same way, not looked up per row on the request path.

Note on test level: the badge is frontend-only, and this repo's browser-level check for it (`tests/test_session_live_gating.py`) drives the *superseded Python backend*, whose `Task` model has no `location` field — so that suite cannot assert the new text. The per-function behaviour is asserted statically in the pytest suite (the `tests/test_blocked_badge.py` pattern), which `make precommit` runs; the Go side is covered by the Ginkgo/Gomega suites.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/test-pyramid-triggers.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
</context>

<requirements>

## 1. `pkg/heartbeat/heartbeat.go` — decode the location and widen the store read

1.1 Add the location to the decoded row:

```go
// heartbeatRow is one row of the session-heartbeat payload. Only the fields
// vault-ui reads are declared; the store may carry more.
type heartbeatRow struct {
	SessionID string `json:"session_id"`
	Live      bool   `json:"live"`
	Location  string `json:"location"`
}
```

1.2 Add an exported value type next to it, carrying one live session's id and location:

```go
// Session is one live session the store reports: its id and the location it
// runs from (the store's own `location` field, e.g. "local" or "pod"). Location
// is empty when the store carries none.
type Session struct {
	ID       string
	Location string
}
```

1.3 Change the `Store` interface's collection method from `LiveSessionIDs(ctx context.Context) ([]string, bool)` to:

```go
// LiveSessions returns the sessions the store currently reports live, with the
// location each one carries. The second result keeps the existing meaning: it
// is false when the store could not be reached — "cannot tell", which a caller
// must never read as "nothing is live". A store that answers returns
// (sessions, true), and sessions may be empty.
LiveSessions(ctx context.Context) ([]Session, bool)
```

Keep the method's existing doc comment body verbatim in meaning (an answered store with no live sessions is not the same as an unreachable one). Keep `IsLive(ctx context.Context, sessionID string) (bool, bool)` on the interface unchanged, **except** for the cross-reference inside its doc comment: the clause reading `the same "cannot tell" as LiveSessionIDs` must name `LiveSessions` instead. Leaving it strands a `LiveSessionIDs` token in `pkg/heartbeat/heartbeat.go`, which verification step 2's no-stale-name grep forbids.

1.4 Rename the `store` method `LiveSessionIDs` to `LiveSessions` and change its return to `([]Session, bool)`; update that method's doc comment at ~line 93 to open with `LiveSessions` too. It must keep both existing behaviours exactly:
- the filter (`row.Live && row.SessionID != ""`), with the results in payload order;
- every failure — a transport error, a non-200 status, an unparseable body — returns `(nil, false)`, never an empty live set.

1.5 Update the two call sites of the renamed method, so nothing is left calling the old name:
- in `pkg/factory/api.go`, `CreateSessionStateWatcher` passes `Read: store.LiveSessionIDs` — it becomes `Read: store.LiveSessions`;
- in `pkg/factory/sessionstate_test.go`, the hand-written double `fakeStore` (its doc comment calls it "a hand-written heartbeat.Store double whose live set a spec moves") implements `LiveSessionIDs(_ context.Context) ([]string, bool)` — rename it to `LiveSessions` returning `([]heartbeat.Session, bool)`, building the sessions from the ids it already holds (`[]heartbeat.Session{{ID: id}}`). That file must gain the `github.com/bborbe/vault-ui/pkg/heartbeat` import for the new return type. Keep its `IsLive` and its `newFakeStore`/`set` helpers, which may keep taking bare ids.
`grep -rn 'LiveSessionIDs' pkg/` must show no remaining `heartbeat.Store` caller. The unrelated `pkg/session/process.go` `LiveSessionIDs` / `ParseLiveSessionIDs` is a different surface — leave it alone.

## 2. `pkg/sessionstate/sessionstate.go` — hold the location in memory

2.1 Import `github.com/bborbe/vault-ui/pkg/heartbeat`.

2.2 Change `State.Replace(ids []string) bool` to `Replace(sessions []heartbeat.Session) bool`. Its contract:
- normalization keeps its current shape, extended to the pair: drop rows with an empty id, dedupe by id (the last row for an id wins), sort by id, so two spellings of the same set compare equal;
- it reports a change when the id set **or** any id's location differs from what is stored — a location move (a session going from `local` to `pod`) is a real change, because it is what pushes a board refresh frame;
- it always marks the set authoritative again (a successful read is what clears a previous `MarkUnknown`).

2.3 Replace `State.RegistrySessionIDs(ctx context.Context) []string` with a read that returns the stored sessions whole, so the one consumer that needs both the ids and the locations gets them in a single call:

```go
// Sessions returns a copy of the current live sessions — ids and locations.
// The result is never nil and never aliases the internal state.
Sessions(ctx context.Context) []heartbeat.Session
```

2.4 Do NOT add a per-session `LiveLocation` accessor to `State`. Nothing reads one session's location off the state, and the per-session accessor belongs to the snapshot (`RegistryLocation`, requirement 3.3) — one read, one shape. Do NOT keep `RegistrySessionIDs` alongside `Sessions` either: two reads of the same set drift.

`State.RegistrySessionIDs` has exactly three call sites, all updated in this change:
- `pkg/factory/api.go`, `CreateSessionSnapshot`'s `Registry` closure (~line 190): `return state.RegistrySessionIDs(ctx), state.LiveIDsKnown()` becomes `return state.Sessions(ctx), state.LiveIDsKnown()`. Without this the closure returns `([]string, bool)` where `Params.Registry` now needs `([]heartbeat.Session, bool)` and the package does not compile.
- `pkg/factory/sessionstate_test.go` (~line 95): `Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).Should(ConsistOf("seed"))` becomes an `Eventually` over `func() []heartbeat.Session { return state.Sessions(ctx) }` comparing against `heartbeat.Session{ID: "seed"}`.
- `pkg/sessionstate/sessionstate_test.go`: the `RegistrySessionIDs` specs are renamed to `Sessions` (requirement 13.2).

`State.Replace` has one caller outside the `sessionstate` package: `pkg/factory/pane_test.go` (~line 98) seeds the state directly with `sessionState.Replace([]string{liveSessionID})`. It becomes `sessionState.Replace([]heartbeat.Session{{ID: liveSessionID}})`, which requires adding the `github.com/bborbe/vault-ui/pkg/heartbeat` import to that file — it imports no `heartbeat` today. Find every `Replace(` caller the same way; a `[]string` argument no longer compiles.

2.5 `WatchParams.Read` becomes `func(ctx context.Context) ([]heartbeat.Session, bool)`. `watcher.seed` and `watcher.apply` pass the sessions straight through; the seed-pushes-nothing and change-pushes-a-frame semantics are unchanged.

2.6 `MarkUnknown` keeps the last known sessions — ids **and** locations — and clears the authoritative flag, exactly as it keeps the ids today.

2.7 Update the package doc comment's description of what is held: the state holds the live sessions (id and location), not only the ids.

## 3. `pkg/sessionsnapshot/snapshot.go` — carry the locations into the cached view

3.1 `Params.Registry` becomes `func(ctx context.Context) ([]heartbeat.Session, bool)`; update its doc comment to say it returns the live sessions and whether the source answered. This requires adding the `github.com/bborbe/vault-ui/pkg/heartbeat` import to `pkg/sessionsnapshot/snapshot.go` (it does not import `heartbeat` today) and, for the same reason, to `pkg/sessionsnapshot/snapshot_test.go` in requirement 13.3. Its only caller is `CreateSessionSnapshot` in `pkg/factory/api.go` — update that closure in the same change (requirement 2.4), or the package does not compile.

3.2 Store the last refresh's sessions alongside `registryIDs` (a `map[string]string` of id → location, or an equivalent pair-preserving shape). Keep the existing rule exactly: a refresh replaces them only when `liveKnown` is true; a read that could not reach the store keeps the previous ids and locations and marks the set unknown.

3.3 Add to the `Snapshot` interface:

```go
// RegistryLocation returns the location the last refresh's live set carried for
// sessionID, or "" when the session is not in that set or its location is empty.
RegistryLocation(ctx context.Context, sessionID string) string
```

3.4 `RefreshOnce` copies the locations when `liveKnown` and leaves them untouched when not, mirroring what it already does with `registryIDs`. The result is never nil and never aliases internal state.

3.5 Update `pkg/sessionsnapshot/mocks/session-snapshot.go` by hand so the compile-time assertion `var _ sessionsnapshot.Snapshot = &mocks.Snapshot{}` in `pkg/sessionsnapshot/snapshot_test.go` still holds. The counterfeiter binary is not available in the container, so add the method following the file's existing generated pattern for `RegistrySessionIDs`, adapted to `func(context.Context, string) string`:
- the struct field `RegistryLocationStub func(context.Context, string) string`, plus `registryLocationMutex sync.RWMutex`, `registryLocationArgsForCall []SnapshotRegistryLocationArgs`, `registryLocationReturns struct{ result1 string }` and `registryLocationReturnsOnCall map[int]struct{ result1 string }`;
- the methods `RegistryLocation(arg1 context.Context, arg2 string) string`, `RegistryLocationCallCount() int`, `RegistryLocationCalls(stub func(context.Context, string) string)`, `RegistryLocationArgsForCall(i int) (context.Context, string)`, `RegistryLocationReturns(result1 string)`, `RegistryLocationReturnsOnCall(i int, result1 string)`;
- the args type `SnapshotRegistryLocationArgs struct { Arg1 context.Context; Arg2 string }`, next to the other `Snapshot…Args` types.
`Invocations()` and `recordInvocation` are generic and need no change. Match the file's existing style exactly so a later counterfeiter regeneration is a no-op.

## 4. `pkg/board/board.go` — widen `SessionSignals`

4.1 Add to the `SessionSignals` interface, next to `RegistrySessionIDs`:

```go
// RegistryLocation returns the location the live set carries for sessionID, or
// "" when the session is not in it or its location is empty.
RegistryLocation(ctx context.Context, sessionID string) string
```

`sessionsnapshot.Snapshot` satisfies it after requirement 3. Do not add a second dependency to `board.Deps` — the session snapshot is the board's cache of the session-liveness inputs and stays the single source.

## 5. `pkg/board/snapshot.go` — carry the location on the snapshot row

5.1 Add `location *string` to `taskSnapshotRow`, immediately after `sessionState`.

## 6. `pkg/board/tasks.go` — precompute it and render it

6.1 Add `location *string` to `taskRow`, immediately after `sessionState`.

6.2 Add the helper next to `sessionStatePtr`:

```go
// liveLocationPtr renders the store's location for a live session as a nullable
// wire value: nil unless the session is live and the store reported a non-empty
// location. A live session the store gives no location for — and a session
// classified live by transcript recency or a live resume process rather than by
// the store — therefore carries null, and the badge renders the plain "● Live".
func liveLocationPtr(state session.SessionState, location string) *string {
	if state != session.SessionStateLive || location == "" {
		return nil
	}
	return &location
}
```

`liveLocationPtr` is package-level in `pkg/board` and is shared with `pkg/board/goals.go` (requirement 7) — define it once, in `tasks.go`.

6.3 In `buildTaskRows`, set the row's `location` from the same signal read the classification used, at the point the row is appended:

```go
location: liveLocationPtr(state, b.signals.RegistryLocation(ctx, item.ClaudeSessionID)),
```

6.4 In `refreshTaskRows`, recompute `location` the same way the session state is recomputed there — it is session-derived and moves with the session snapshot. Use the classification result the function already computes rather than classifying twice.

6.5 In `patchTaskRows`, carry `location` over from the held row exactly as `sessionState` is carried: add `location: rows[at].location,` to the struct literal.

Extend the doc comment at the top of `patchTaskRows` — the bullet that currently reads "The classified session state and the activity date are carried over from the held row" (~line 292) — to name the location alongside them, e.g. "The classified session state, the activity date and the location are carried over from the held row." That bullet is the one place the carry-over is explained: do not add a second, separately-worded comment on the struct literal. The comment above `started:` is about the marker only — leave it alone.

6.6 In `ListTasks`, copy the field across with the others: `visibleTask.location = row.location`.

6.7 In `taskResponse`, set `Location: row.location` on the returned `api.TaskResponse`, next to `SessionState: row.sessionState`.

6.8 Update `buildTaskRows`' doc comment: it lists the fields it precomputes — add the session's location to that list.

## 7. `pkg/api/api.go` and `pkg/board/goals.go` — the wire field on both card kinds

7.1 Add to `TaskResponse`, immediately after `SessionState`:

```go
	// Location is the location the attention store reports for the card's live
	// session (e.g. "local", "pod"), so the wall's live badge can say where the
	// session runs. It is Go-only (see the package doc) — the superseded Python
	// backend cannot emit it — and null unless the session is live and the store
	// carried a non-empty location.
	Location *string `json:"location"`
```

7.2 Add the same field to `GoalResponse`, immediately after `SessionState`:

```go
	// Location is the location the attention store reports for the goal's live
	// session, on the same terms as TaskResponse.Location.
	Location *string `json:"location"`
```

7.3 In `pkg/board/goals.go`, `goalResponse` already binds the classification to `state` (`state := session.ClassifySessionState(...)`) and renders `SessionState: sessionStatePtr(state)`. Add the location beside it, applying the same nil-unless-live-and-non-empty rule:

```go
		Location: liveLocationPtr(state, b.signals.RegistryLocation(ctx, item.ClaudeSessionID)),
```

Use the shared `liveLocationPtr` from requirement 6.2 — do not write a goal-specific helper. `goalResponse` classifies per row on the request path (that is how the goal path already works), so this lookup sits beside the existing classification; do not add a field to `goalRow` and do not introduce a goal-side snapshot.

## 8. `src/vault_ui/static/app.js` — render it

8.1 Add this helper immediately above `sessionButtonHtml`:

```js
// liveBadgeLabel renders the live badge's text: "● Live" plus the session's
// location when the attention store reports one, so the badge says where the
// session runs. An empty or absent location renders the plain "● Live" — never
// a dangling separator. The location is store-supplied text, so it is escaped.
function liveBadgeLabel(location) {
    return location ? `● Live·${escapeHtml(location)}` : '● Live';
}
```

8.2 In `sessionButtonHtml(kind, item)`, in the `item.session_state === 'live'` branch, change only the badge's text node. The `liveBadge` template's `<span class="live-badge" role="button" tabindex="0" onclick="takeOverSession('${kind}', '${escapeJsAttr(item.id)}')" title="Session is live — click to take over and resume (ends the running turn; in-flight work is lost)">` prefix and the closing `</span>` stay byte-identical; only the literal `● Live` between them becomes `${liveBadgeLabel(item.location)}`.

8.3 Both card kinds are covered by this one edit: `sessionButtonHtml` is called for tasks (~line 1765) and for goals (~line 1828), and both now pass a card whose payload carries `location`, so a live goal card renders `● Live·<location>` through the same helper with no goal-specific branch. Do not add one.

8.4 Change nothing else in `sessionButtonHtml`: the `starting-badge`, `jump-btn`, `resume-btn` and `start-btn` branches, the order in which `liveBadge` and `jump-btn` are concatenated, the goal/task branching, and the badge's `title` attribute all stay as they are. The location is visible text only.

## 9. `src/vault_ui/static/index.html` — bump the app.js cache-bust token

9.1 Change `app.js?v=2026-10-07-approve-modal` to a new token whose suffix names this change, e.g. `app.js?v=2026-10-13-live-badge-location`. Leave the `style.css` token untouched. An un-bumped token is how this repo has shipped a frontend fix browsers never received, and `tests/test_blocked_badge.py::test_cachebust_tokens_bumped` asserts the token shape.

## 10. `scripts/parity/parity.sh` — strip the Go-only field

10.1 In `normalize()`, add `location` to both `del(...)` filters, so they read `del(.jump_pane, .open_questions, .location)` — once inside the array branch's `map(...)` and once in the object branch. Extend the comment above the filter to name `location` and why it is stripped: it is a Go-only addition on the task body that the superseded Python backend cannot emit, so leaving it in makes every task-list case diverge. Parity compares the two backends' bodies, and `scripts/parity/cases.txt` carries two `/api/goals` read cases (`goals-filter`, `goals-upcoming`) alongside the task ones, so an unstripped Go-only key on either body is a permanent false failure.

## 11. `docs/liveness-classification.md` — one sentence

11.1 In § "Signal order (checked first to last)", item 2 (the attention store), add one sentence: the same store read also carries each live row's `location` (e.g. `local`, `pod`), which the wall's live badge displays and which is **never** a classification signal — the four outcomes and the signal order above are unchanged. Do not change any other line of that document.

## 12. `CHANGELOG.md` — one `## Unreleased` bullet

12.1 Add one `- feat: ...` bullet under the existing `## Unreleased` section at the top of the file (do not add a new section, do not bump a version). It states: the board's live badge now shows the session's location from the attention store (`● Live·local`), a live session the store reports no location for keeps the plain `● Live` with no dangling separator, and the location is carried from the store through the in-memory session state and the session snapshot to `GET /api/tasks` and `GET /api/goals` as a new nullable `location` field on both card kinds.

## 13. Tests

13.1 `pkg/heartbeat/heartbeat_test.go` — rename `Describe("LiveSessionIDs")` to `Describe("LiveSessions")` and adapt the existing specs to the new return type, keeping all four cannot-tell specs (non-200, unparseable body, unreachable store, answered-empty) and the "only live rows" spec. Add:
- a payload row with `"location":"local"` yields `Session{ID: liveID, Location: "local"}`;
- a row with `"location":""` and a row with no `location` key both yield `Location: ""` without an error;
- a non-live row is still excluded even when it carries a location.

13.2 `pkg/sessionstate/sessionstate_test.go` — add the `github.com/bborbe/vault-ui/pkg/heartbeat` import (the file imports none today), update `sequenceRead.Get` and every spec to the `[]heartbeat.Session` shape, and rename the `RegistrySessionIDs` specs to `Sessions`, then add:
- `Replace` + `Sessions` round-trip: each stored location comes back paired with its own id, a second session's location does not leak onto the first, and `Sessions` returns a copy that does not alias the internal state;
- `Sessions` is empty and never nil before the first `Replace` and for an answered source with no live sessions;
- **`Replace` reports a change when only a location moved** — the same id set with `local` becoming `pod` — because that is what pushes a board refresh frame;
- `Replace` reports no change when the same id/location set arrives reordered;
- `MarkUnknown` keeps the last known sessions and their locations (`Sessions` still answers with them, `LiveIDsKnown` is false) and a later successful `Replace` restores authority.

13.3 `pkg/sessionsnapshot/snapshot_test.go` — update `countingRegistry` to the `func(ctx) ([]heartbeat.Session, bool)` shape, then add:
- `RefreshOnce` publishes the registry's locations: `RegistryLocation(ctx, id)` returns the location the registry supplied;
- a registry read that reports not-known keeps the previous locations and clears `LiveIDsKnown` (mirror the existing registry spec);
- `RegistryLocation` returns `""` for a session id the last refresh did not carry.

13.4 `pkg/board/board_test.go` — add the `github.com/bborbe/vault-ui/pkg/heartbeat` import if the file does not already carry it, then widen `fakeSignals` so it satisfies the new `SessionSignals`: its `registry` field becomes `[]heartbeat.Session` and it gains a `RegistryLocation` method (returning the location it holds for the id, `""` otherwise); update `newHarness` and every spec that sets `h.signals.registry`. Then add specs asserting the rendered response:
- a live task whose store row carries `location: "local"` renders `Location` as a pointer to `"local"`;
- a live task whose store row carries an empty location renders `Location` nil — the wire `location: null` case that 1 of the board's 38 live rows is in;
- a task with no session id renders `Location` nil;
- a live goal whose store row carries `location: "local"` renders `Location` as a pointer to `"local"` on the goal response, and a live goal whose store row carries an empty location renders `Location` nil — the same rule as the task side (set `h.list.items["23 Goals"]` and drive `ListGoals`, the pattern the existing `ListGoals` specs use);
- the JSON actually carries the key — the `json:"location"` tag is never exercised by a struct-field assertion, and `scripts/parity/parity.sh` strips `location` from its comparison, so nothing else joins the Go tag to the frontend's `item.location` read: render the task-list body through `ListTasksBody` (the production body path) for a live task carrying `location: "local"` and assert the `Identity` bytes contain `"location":"local"`; assert a live task with no location renders `"location":null` (the field has no `omitempty`); and, since the goal path has no body cache, marshal the live `api.GoalResponse` that `ListGoals` returns and assert its JSON carries `location` with the value;
- the task path precomputes the location, never looks it up per request: give `fakeSignals` a `locationCalls *seamCounter` alongside the existing `registryCalls`/`resumeCalls`, and assert that a second, warm `ListTasks` (the snapshot already built) leaves that counter unchanged — the value is carried on the snapshot row, so a task request pays no signal read for it.

13.5 Add `tests/test_live_badge_location.py` (a plain, non-integration pytest — the `tests/test_blocked_badge.py` static-assertion pattern; reuse its `_slice_function` helper shape). Assert, against `src/vault_ui/static/app.js`:
- `liveBadgeLabel` exists and its body references `location`, contains the `● Live` literal and the `·` separator, and wraps the location in `escapeHtml(` — so the separator is conditional on a location being present and the store-supplied text is escaped before it reaches the DOM;
- `sessionButtonHtml` builds the live badge's text through `liveBadgeLabel(item.location)`;
- `sessionButtonHtml` still builds the live badge as a `<span class="live-badge" role="button" tabindex="0" ...>` whose `onclick` is `takeOverSession('${kind}', ...)` — the take-over affordance is unchanged;
- `sessionButtonHtml` is called for both card kinds (`sessionButtonHtml('task', task)` and `sessionButtonHtml('goal', goal)`), so the one label change reaches tasks and goals with no goal-specific branch.
Give the module a docstring saying the badge is frontend-only and that the browser-level check lives in `tests/test_session_live_gating.py` (marked `integration`, run by `make test-integration`), while these per-function assertions run in `make precommit`.

## 14. Self-check

14.1 Before finishing, re-run every `<verification>` command and confirm each passes, then walk each bullet of `<summary>` and each requirement above against the change. Confirm in particular: a live session with a location renders `● Live·<location>` on both a task card and a goal card; a live session with an empty or absent location renders exactly `● Live`; non-live cards are unchanged; and the live badge still carries `role="button"`, `tabindex="0"` and the `takeOverSession` click.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass. Update the existing specs whose shape changes (heartbeat, sessionstate, sessionsnapshot, board, factory); do not delete a spec to make the suite green. `pkg/factory/pane_test.go` (~line 98) is a `sessionstate.State.Replace` caller outside the sessionstate package: `sessionState.Replace([]string{liveSessionID})` must become `sessionState.Replace([]heartbeat.Session{{ID: liveSessionID}})`, adding the `github.com/bborbe/vault-ui/pkg/heartbeat` import to that file.
- This is one change: the live badge's text. Do NOT add a config knob, an opt-out flag, a tunable, a Prometheus metric, a new HTTP route or a new `board.Deps` field.
- Goal cards get the same treatment: `GoalResponse` carries the same nullable `location` and `goalResponse` populates it through the shared `liveLocationPtr`. Do NOT add a goal-specific frontend branch and do NOT duplicate the helper.
- Do NOT change the liveness classification: the four outcomes, the fixed signal order, the five-minute window (`session.DefaultLiveWindow`) and the "cannot tell" semantics of an unreachable store are frozen. `docs/liveness-classification.md` gets one clarifying sentence and nothing else.
- Do NOT change the take-over affordance: the `.live-badge` span keeps `role="button"`, `tabindex="0"`, its `onclick="takeOverSession('<kind>', '<id>')"` and its `title` text. Only the visible label changes.
- Do NOT change the `.jump-btn` control, its ordering after the badge, or any other card affordance.
- On the task path, do NOT move the location lookup onto the request path: the board precomputes every derived field in `buildTaskRows`, and a task request must keep making no store call, no process spawn and no transcript probe. The goal path already classifies per row inside `goalResponse`, so its location lookup belongs beside that existing classification — do NOT restructure the goal path to precompute it.
- Do NOT add a second live-id source: the location rides the existing chain (store → sessionstate → sessionsnapshot → board), and `sessionsnapshot.Snapshot` stays the board's single `SessionSignals` dependency.
- Do NOT run `go mod vendor`; do NOT add a dependency (this change adds none). Use `-mod=mod` if a `go test` flag is ever needed.
- Do NOT prescribe or apply a version bump — releases are cut by the release process, not by this change. The only `CHANGELOG.md` edit is one bullet under the existing `## Unreleased` section.
- bborbe conventions per `docs/dod.md` apply: Go errors wrapped with `github.com/bborbe/errors`, no silently ignored error returns, `factory.Create*` functions hold no business logic.
</constraints>

<verification>
1. Run `make precommit` — must pass (format + test + lint + typecheck). `make test` runs `go test -race ./...` and the pytest suite with the repo's `-m 'not integration'` addopts, so the new `tests/test_live_badge_location.py` runs and the Playwright suite is deselected. The Go suites cover requirements 1–7; the static pytest covers requirement 8.

2. Then confirm each edit is in place. Absence is asserted with `! grep -q` (a bare `grep -c` exits non-zero at a count of zero):
   - `grep -cE 'Location[[:space:]]+\*string' pkg/api/api.go` — prints 2 (the new `TaskResponse.Location` and `GoalResponse.Location` fields; gofmt aligns the field names, so the separator is a run of spaces, not one).
   - `grep -n 'liveLocationPtr' pkg/board/tasks.go pkg/board/goals.go` — prints 5 lines: the doc-comment line, the `func`, and its three calls (`buildTaskRows`, `refreshTaskRows`, `goalResponse`).
   - `grep -n 'LiveSessions' pkg/heartbeat/heartbeat.go pkg/factory/api.go` — prints the interface method with its doc comment, the store method's doc comment and signature, the `IsLive` doc comment's cross-reference (requirement 1.3), and the watcher's `Read:` call site.
   - `grep -n 'state.Sessions' pkg/factory/api.go pkg/factory/sessionstate_test.go` — prints the updated `CreateSessionSnapshot` `Registry` closure and the updated `Eventually` read.
   - `grep -n 'del(.jump_pane, .open_questions, .location)' scripts/parity/parity.sh` — prints 2 lines (the array branch and the object branch of `normalize()`).
   - `grep -n 'liveBadgeLabel' src/vault_ui/static/app.js` — prints at least 2 lines (the definition and the call site in `sessionButtonHtml`).
   - `! grep -q 'app.js?v=2026-10-07-approve-modal' src/vault_ui/static/index.html` — the stale cache-bust token is gone, and `grep -n 'app.js?v=' src/vault_ui/static/index.html` prints the new one.
   - `grep -n 'Live·' CHANGELOG.md` — prints the new `## Unreleased` bullet.
   - `! grep -rnq 'LiveSessionIDs\|RegistrySessionIDs' pkg/heartbeat/ pkg/factory/ pkg/sessionstate/` — the two renames (the store read and the state read) left no stale name behind in the store, the state, their factory wiring or the factory's `fakeStore` double. Scoped deliberately: `pkg/sessionsnapshot` and `pkg/board` **keep** their own `RegistrySessionIDs` (the snapshot's read and the board's `SessionSignals`), and `pkg/session/process.go`'s `LiveSessionIDs` / `ParseLiveSessionIDs` is an unrelated surface — leave all three alone.

3. Re-run the frontend guards that already cover this badge, since they are what the change hangs on:
   - `uv run pytest tests/test_live_badge_location.py -v` — must pass.
   - `uv run pytest tests/test_activity.py tests/test_jump_control.py -v` — must still pass. These are the existing `sessionButtonHtml` guards: `test_activity.py::test_session_button_gates_on_session_state` asserts the function still emits `class="live-badge"` on `item.session_state === 'live'`, and `test_jump_control.py::test_jump_control_does_not_replace_the_live_badge_handler` asserts the badge's `onclick="takeOverSession('${kind}', '${escapeJsAttr(item.id)}')"` is byte-identical. Both slice the function by a fixed 4000-character window, so keep the edit inside it.
   - `uv run pytest tests/test_blocked_badge.py -v` — must still pass; it guards the `blockedBadgeHtml` conventions and the `index.html` cache-bust token shape, which this change bumps.
</verification>
