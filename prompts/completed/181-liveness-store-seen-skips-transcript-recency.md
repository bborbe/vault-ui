---
status: completed
summary: Made the attention store's seen-id set authoritative for transcript recency so a killed session no longer renders live for five minutes, threading a Listing through heartbeat, sessionstate, sessionsnapshot, board and classifier with tests, docs and changelog.
execution_id: vault-ui-liveness-store-seen-exec-181-liveness-store-seen-skips-transcript-recency
dark-factory-version: v0.198.17
created: "2026-11-11T10:28:47Z"
queued: "2026-11-11T10:28:47Z"
started: "2026-11-11T10:33:16Z"
completed: "2026-11-11T10:38:02Z"
branch: dark-factory/181-liveness-store-seen-skips-transcript-recency
---

# A store-seen, store-not-live session is no longer held live by transcript recency

<summary>

- A killed session stops rendering `● Live` within the store's own window instead of five more minutes
- The store's answer "this session is not live" becomes authoritative for a session it has seen before
- A session the store has never seen still falls back to transcript recency, unchanged
- The idle-but-open safety check still runs for every session, so a session whose process is still alive keeps reading live
- An unreachable store still reads "cannot tell" rather than "dead"
- The store already returns the rows this needs; they are simply discarded today
- The board's four outcomes and signal order are unchanged in every other respect

</summary>

<objective>

Stop the board from rendering a killed session as `● Live` for the transcript-recency window. The attention store already knows the session is dead within its own 60 s window, but the classifier never sees that answer because the store reader throws away every `live: false` row — so the transcript written at the kill instant holds the card live for five more minutes.

</objective>

<context>

Read `docs/liveness-classification.md` — the contract this change narrows. § Signal order and the four-outcome table are authoritative.

Read `pkg/session/classify.go` — `ClassifySessionState`, the fixed-order classifier. Today's branch order is: empty id → `none`; id in `RegistrySessionIDs` → `live`; transcript missing → `indeterminate`; transcript within `LiveWindow` → `live`; id in `ResumeSessionIDs` → `live`; `LiveIDsUnknown` → `indeterminate`; else `quiet`.

Read `pkg/heartbeat/heartbeat.go` — `Store` and its `LiveSessions` reader. The store's collection endpoint returns every retained heartbeat row with its own `live` flag; `LiveSessions` keeps only the live ones and drops the rest.

Read `pkg/sessionstate/sessionstate.go`, `pkg/sessionsnapshot/snapshot.go` and `pkg/factory/api.go` (`CreateSessionSnapshot`, `CreateSessionStateWatcher`) — the chain the live set travels.

Read `pkg/board/board.go` (`SessionSignals`) and its three consumers: `pkg/board/tasks.go`, `pkg/board/goals.go`, `pkg/board/topics.go`.

Read `pkg/session/classify_test.go` for the existing table shape.

</context>

<requirements>

1. In `pkg/heartbeat/heartbeat.go`, add a `Listing` type carrying both halves of one store read: `Live []Session` (the rows whose `live` flag is true — exactly what `LiveSessions` returns today) and `SeenIDs []string` (every non-empty `session_id` in the payload, live or not). Change `Store.LiveSessions` to return `(Listing, bool)` and update its doc comment: the second result keeps its current meaning — `false` is "cannot tell" (an unreachable or unparseable store), never "nothing is live". Populate `SeenIDs` from every row, including the `live: false` ones the current loop skips. Leave `IsLive` alone.

2. Thread `Listing` through the read chain, keeping each package's existing responsibility:
   - `pkg/sessionstate/sessionstate.go`: `WatchParams.Read` returns `(heartbeat.Listing, bool)`; `State` gains `SeenIDs(ctx context.Context) []string`; `Replace` takes the listing and stores both halves, and its changed-detection compares the seen set as well as the live set using the same order-insensitive, deduped comparison `normalize` gives the live set (sort a copy of the seen ids) — a set move must push a refresh frame, a reordered payload must not, and `Replace`'s doc comment must say so. `MarkUnknown` keeps the last known listing, including its seen ids — "cannot tell" must not erase what the store previously knew.
   - `pkg/sessionsnapshot/snapshot.go`: `Params.Registry` returns `(heartbeat.Listing, bool)`; the `Snapshot` interface gains `SeenSessionIDs(ctx context.Context) []string`; `RefreshOnce` swaps it in on a successful read and keeps the previous value on an unknown one.
   - `pkg/board/board.go`: `SessionSignals` gains `SeenSessionIDs(ctx context.Context) []string`, documented as the ids in the last store read, live or not — not the live set.
   - `pkg/factory/api.go`: update the two closures that read `state.Sessions` / `store.LiveSessions` to the new signatures.
   - `pkg/sessionsnapshot/mocks/session-snapshot.go` is generated; regenerate it from the `counterfeiter:generate` annotation on the interface, or extend it by hand so the package compiles. Every hand-written double of a changed surface needs the same treatment: `pkg/board/board_test.go` `fakeSignals` and `pkg/board/bench_internal_test.go` `benchSignals` (SessionSignals), `pkg/sessionstate/sessionstate_test.go` `sequenceRead` and its `Replace` calls (WatchParams.Read / State), `pkg/sessionsnapshot/snapshot_test.go` `countingRegistry.Get` (Params.Registry), and `pkg/factory/sessionstate_test.go` `fakeStore.LiveSessions` plus `pkg/factory/pane_test.go`'s `sessionState.Replace(...)`.

3. In `pkg/session/classify.go`, add `SeenSessionIDs []string` to `ClassifyParams`, with a field comment mirroring `LiveIDsUnknown`'s: the ids in the last store read, live or not — not the live set. Compute `storeSaysNotLive := !params.LiveIDsUnknown && slices.Contains(params.SeenSessionIDs, params.SessionID)` once, and use it to guard **only the recency half** of the transcript signal:
   - the transcript probe and its missing-transcript `indeterminate` return run unconditionally, in their current order;
   - only the recency check (`now.Sub(*mtime) <= LiveWindow`) is skipped when `storeSaysNotLive`, so a store-seen session with a fresh transcript falls through to the `ps` cross-check instead of reading `live`;
   - the `ResumeSessionIDs` check, the `LiveIDsUnknown` `indeterminate` and the final `quiet` run unconditionally, in their current order.
   Do **not** skip the missing-transcript `indeterminate` return. A pod or cloud session that posted a heartbeat but has no transcript on this host must still read `indeterminate`, because a local `claude --resume` for it cannot be honoured and `quiet` would enable the Resume button (`src/vault_ui/static/app.js` disables Resume for `indeterminate` only).
   Write the rule into `ClassifySessionState`'s doc comment before the code, as a short paragraph naming why: the store is authoritative in both directions once it has answered about a session it has seen, so recency must not override a "not live" answer; it remains the only signal for a session the store has never seen.

4. Pass the store's seen ids into every **production** `ClassifyParams` construction: `pkg/board/tasks.go` (both call sites), `pkg/board/goals.go` (`goalResponse`, threaded from `ListGoals` through `goalsForVault` alongside `registryIDs` / `resumeIDs` / `liveIDsUnknown`) and `pkg/board/topics.go`. Hoist it the way those three already are — `seenIDs := b.signals.SeenSessionIDs(ctx)` once per request or vault, never inside the per-row loop. Test and bench constructions keep the zero value deliberately, except one new `pkg/board/board_test.go` case that sets `fakeSignals`' seen ids and asserts a task whose session is store-seen with a fresh transcript renders `quiet` — the only test that proves the production wiring at a call site.

5. Extend `pkg/session/classify_test.go` with these cases, each as its own entry in the existing table shape:
   - store reached, id in `SeenSessionIDs`, not in `RegistrySessionIDs`, transcript written this second → `quiet` (the killed session; it fails on today's code, which returns `live`);
   - store reached, id absent from `SeenSessionIDs`, transcript written this second → `live` (a never-seen session keeps recency);
   - `LiveIDsUnknown` true, id absent from both sets, transcript written this second → `live` (an unreachable store is not "dead");
   - `LiveIDsUnknown` true, id still present in the retained `SeenSessionIDs`, transcript stale, not in `ResumeSessionIDs` → `indeterminate` (a store outage must not turn a retained seen id into `quiet` — this case is the `!params.LiveIDsUnknown` conjunct, and it is the only one that fails if the conjunct is dropped);
   - id in `SeenSessionIDs`, transcript stale, id in `ResumeSessionIDs` → `live` (the guard this change must not break);
   - id in `SeenSessionIDs`, **no transcript on this host** → `indeterminate` (the pod/cloud case the missing-transcript return exists for).
   Add one `pkg/heartbeat/heartbeat_test.go` case that drives the real payload: a body carrying one `live: true` row and one `live: false` row must return a `Listing` whose `Live` holds only the first id and whose `SeenIDs` holds both, and a row with an empty `session_id` must appear in neither. This is the serialization contract the classifier's hand-built slices cannot exercise; update the existing "excludes a non-live row" case to assert the id now lands in `SeenIDs`.
   Also cover the new non-classifier paths: `heartbeat.LiveSessions` returning a zero `Listing` and `false` on an unparseable body, `State.SeenIDs` still returning the last listing after `MarkUnknown`, `State.Replace` reporting `true` when only the seen set moves (same live set, one id added to `SeenIDs`) and `false` when a reordered payload carries the same two sets, and `Snapshot.RefreshOnce` keeping the previous seen ids on an unknown read.

6. Update `docs/liveness-classification.md`: in § Signal order, state that only the recency half of signal #3 (the `live` return for a transcript written within the window) is skipped for a session the store has reported; the transcript lookup and its missing-transcript `indeterminate` return stay, and signal #4 (the `ps` cross-check) stays unconditional. A store-reported session with a stale transcript falls through the `ps` cross-check and then `quiet`. Keep the four outcomes' meanings unchanged, but amend the `live` row's signal cell so its transcript-recency clause no longer reads as an unconditional third path — it applies only to a session the store has not reported. Leave the `quiet` and `indeterminate` rows as they are.

7. Add a `## Unreleased` entry to `CHANGELOG.md` describing the behaviour change in the repo's existing style — one `fix:` bullet, prose, no file list. If `## Unreleased` already exists, append to it; do not create a second heading.

8. Before finishing, re-run `<verification>` and confirm it passes; then walk each requirement above against the change and confirm each one is met.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- The four outcomes and the five-minute `DefaultLiveWindow` value do not change; only which sessions the transcript signals apply to.
- Do not remove the transcript-recency or `ps` fallbacks — both are load-bearing for the open-but-idle guard.
- No new dependency.
- `go mod vendor` is never run; `vendor/` is a build artifact.

</constraints>

<verification>

`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run:

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```

Must exit 0.

</verification>
