---
status: completed
spec: [034-bug-idle-board-spawns-ps]
summary: Gated the session-snapshot refresh on demand inside the concrete snapshot type, so an idle board spawns no ps child while a read re-arms exactly one refresh at the unchanged 60 s ceiling.
execution_id: vault-ui-session-idle-exec-166-spec-034-gate-session-snapshot-refresh-on-demand
dark-factory-version: v0.198.7
created: "2026-10-14T21:38:00Z"
queued: "2026-10-14T21:44:39Z"
started: "2026-10-14T21:45:08Z"
completed: "2026-10-14T21:48:25Z"
branch: dark-factory/166-spec-034-gate-session-snapshot-refresh-on-demand
---

# Gate the session-snapshot refresh on demand so an idle board spawns no `ps`

<summary>
- An idle board — one serving no requests — stops spawning the `ps -axww -o args=` child that today fires once per 60 s forever.
- The 60 s refresh cadence is unchanged; the only change is that a tick which finds the snapshot unread since the previous refresh does no work at all.
- Reading the snapshot re-arms exactly one refresh, so a board that is being read keeps its session view as current as it is today.
- A read does not latch the refresher on: a board read once and then left idle returns to spawning nothing, rather than spawning every minute forever.
- Reading the snapshot from a request handler stays a pure in-memory read — no request path ever spawns a process.
- The liveness classification contract (the four outcomes, the signal order, the five-minute window) and the snapshot interface are untouched.
- The design document's session-refresh bullet now describes the read-gated bound instead of a fixed interval that runs regardless.
- The CHANGELOG records the fix under a new `## Unreleased` section.
- Two new container-runnable tests pin the behaviour: a three-phase demand test and a read-path-spawns-no-`ps` test.
- The live no-spawn evidence (a 5-minute idle window on the deployed board) stays on the spec's operator rung, not in this prompt.
</summary>

<objective>
Make the board's session snapshot refresh only when something has read it, so an idle board spawns no `ps` process and does no refresh work, while a board being read keeps the snapshot current at the same 60 s cadence it has today. This closes the last timer the board owns that spawns a process with no consumer, restoring spec 025's AC10 children clause over any realistic observation window. This is the single prompt the spec's Suggested Decomposition calls for; Acceptance Criteria 1–3 are the spec's operator rung (a deploy plus a ≥5 min idle window) and are **not** this prompt's scope.
</objective>

<context>
Read the spec `specs/in-progress/034-bug-idle-board-spawns-ps.md` in full before writing. It covers Acceptance Criteria 4, 5, 6, 7 and 8. Acceptance Criteria 1–3 (the idle-board probe, its positive control, and the non-idle resume-id control) run on the host after merge and deploy — do **not** write a deploy step, a `launchctl` command, a `pgrep` probe, or a "run this on the host" note into this prompt.

Read these files fully before writing:
- `pkg/sessionsnapshot/snapshot.go` — the `Snapshot` interface, `Params`, the concrete `snapshot` struct, the read methods (`RegistrySessionIDs`, `RegistryLocation`, `LiveIDsKnown`, `ResumeSessionIDs`, `TranscriptMtime`, `Generation`) and `RefreshOnce`. The package doc comment (top of file) and the `Run` interface doc comment both currently describe the refresh as unconditional and must be corrected.
- `pkg/sessionsnapshot/refresh.go` — `Run`, the loop this prompt gates.
- `pkg/sessionsnapshot/snapshot_test.go` — the `package sessionsnapshot_test` harness: `countingScanner` (`Scan`, `callCount`), `countingRegistry` (`Get`), `countingProbe` (`Probe`), `fakeWaiter`, `mutableClock`, `newSnapshot(scanner, registry, probe, clock, waiter)`, `fixedClock()`, and the board helpers `newSnapshotBoard(snapshot)`, `staticVaults`, `staticOps`, `staticList`, `staticIndex`, `classifyFirstTask`. The existing `It("refreshes once per SessionRefreshInterval (AC8)")` asserts exactly the unconditional behaviour this spec removes and must be rewritten (not left passing).
- `pkg/board/board.go` — `Board` (`ListTasks`, `ListGoals`), `SessionSignals`, `SessionProbe`, `Deps`, `TaskQuery`, `GoalQuery`. The board's `transcriptProbe()` returns `b.sessions.TranscriptMtime` when `Sessions` is set.
- `pkg/sessionsnapshot/mocks/session-snapshot.go` — the committed counterfeiter fake. It is generated from the directive at `snapshot.go:40`; the interface must NOT change, so this file must NOT change.
- `docs/page-index.md` — the session-refresh bullet in the *Task-list snapshot* section (the paragraph beginning "The page-derived task-list fields keep the bounds above.").
- `CHANGELOG.md` — its first heading is `## v0.97.1` and there is **no** `## Unreleased` section; one must be created directly above `## v0.97.1`.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-logging-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

Verified facts you may rely on (read from the source above; do not re-derive):
- `Snapshot` interface methods are: `RegistrySessionIDs(ctx) []string`, `RegistryLocation(ctx, sessionID string) string`, `LiveIDsKnown() bool`, `ResumeSessionIDs(ctx) []string`, `TranscriptMtime(ctx, sessionID, projectDir, projectsRoot string) *libtime.DateTime`, `Generation() uint64`, `RefreshOnce(ctx) error`, `Run(ctx) error`.
- `Params.Waiter` is `libtime.WaiterDuration`; `Run` waits with `s.params.Waiter.Wait(ctx, libtime.Duration(SessionRefreshInterval))`. `libtime.WaiterDurationFunc` exists (used by `fastWaiter()` in `pkg/pageindex/pageindex_test.go`) and satisfies `libtime.WaiterDuration`.
- `SessionRefreshInterval = 60 * time.Second` (`snapshot.go`), and `Run` is the only production caller of the loop (`main.go:109` passes `sessionSnapshot.Run` to the run group). No production code calls `RefreshOnce` directly.
- The board reads the snapshot only through request-path calls: `RegistrySessionIDs`, `LiveIDsKnown`, `ResumeSessionIDs`, `RegistryLocation` (`pkg/board/tasks.go`, `pkg/board/goals.go`), `Generation` (`pkg/board/snapshot.go`, from `taskSnapshotStore.List`), and `TranscriptMtime` (via `b.transcriptProbe()`). None of them calls the scanner.
</context>

<requirements>

### 1. Add the demand counter to the concrete `snapshot` type (`pkg/sessionsnapshot/snapshot.go`)

Keep the gate internal to the concrete `snapshot` type — the `Snapshot` interface must not change, and `pkg/sessionsnapshot/mocks/session-snapshot.go` must not be edited or regenerated.

1. Add two unexported fields to `type snapshot struct`, each with a short doc comment:
   - `reads uint64` — the number of public read calls served since the process started.
   - `readsAtRefresh uint64` — the value of `reads` when the last **successful** refresh ran.

2. Count every read by incrementing `s.reads` inside the existing `s.mu` critical section of each read method — do not add a second lock acquisition and do not add a separate helper that re-locks:
   - `RegistrySessionIDs` — in its existing lock block.
   - `RegistryLocation` — in its existing lock block.
   - `LiveIDsKnown` — in its existing lock block.
   - `ResumeSessionIDs` — in its existing lock block.
   - `TranscriptMtime` — in its **first** lock block (the cache lookup), which is reached only after the `if sessionID == "" { return nil }` early return. A blank-session-id call returns before any lock and therefore does not count, which is intended: it reads nothing.
   - `Generation` — in its existing lock block.

3. In `RefreshOnce`, in the success path under the existing `s.mu` critical section (the block that sets `s.refreshedAt`, `s.indices` and `s.generation++`), add `s.readsAtRefresh = s.reads`.
   - Do **not** update `s.readsAtRefresh` on the scanner-error path: that path returns before taking the lock, so a failed refresh leaves the demand outstanding and the next tick with demand retries (matching the spec's Failure Modes row for a failed `ps`). Do not add a retry-on-failure special case.

4. Add an unexported method:

```go
// demanded reports whether the snapshot has been read since the last successful
// refresh. A tick with no demand performs no work, so an idle board spawns no
// `ps` child; a read re-arms exactly one refresh and does not latch the
// refresher on.
func (s *snapshot) demanded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads != s.readsAtRefresh
}
```

`NewSnapshot` already zero-initialises both fields; no change is needed there.

### 2. Gate the loop in `Run` (`pkg/sessionsnapshot/refresh.go`)

Keep the unconditional startup refresh. After each successful wait, skip the tick when there is no demand:

```go
func (s *snapshot) Run(ctx context.Context) error {
	// The startup refresh is unconditional: the snapshot must have values before
	// the first read, and it is the one refresh an idle board ever performs.
	_ = s.RefreshOnce(ctx)

	for {
		if err := s.params.Waiter.Wait(ctx, libtime.Duration(SessionRefreshInterval)); err != nil {
			return nil
		}
		if !s.demanded() {
			// No read since the last refresh: this tick does no work. The 60 s
			// cadence is a ceiling, not a schedule.
			continue
		}
		_ = s.RefreshOnce(ctx)
	}
}
```

The wait duration and the `ctx.Done()` handling (via `Waiter.Wait` returning an error → `Run` returns nil) are unchanged.

Update the `Run` doc comment in `refresh.go` and the `Run` method doc comment on the `Snapshot` interface in `snapshot.go` so both describe the demand gate (startup refresh unconditional; subsequent ticks refresh only when the snapshot has been read since the previous refresh). Also correct the package doc comment in `snapshot.go`, which currently says the `ps` scan "runs once per refresh on a fixed interval of at least 60 s" — reword it to state that a refresh runs at a 60 s cadence ceiling but only when the snapshot has been read since the previous refresh.

### 3. Rewrite the now-false interval test and add the three-phase demand test (`pkg/sessionsnapshot/snapshot_test.go`)

The existing `It("refreshes once per SessionRefreshInterval (AC8)")` asserts one scanner call per wait — exactly the unconditional behaviour this change removes. Delete that row **and the two-line `// AC8: the added timer is slow …` comment above it**, which describes the behaviour being removed, and replace them with the three-phase demand test below. Do **not** leave the old assertion in place.

Retain the still-true constant assertion in the replacement: `Expect(sessionsnapshot.SessionRefreshInterval).To(BeNumerically(">=", 60*time.Second))` (spec 028's AC8 depends on it and this spec leaves it true).

Add a row named exactly:

```go
It("refreshes only when the snapshot has been read since the last refresh (AC4)", func() {
```

It must drive `Run` with an injected `Waiter` whose behaviour is scripted per wait, and a scanner that counts calls, in three phases, asserting the scanner count numerically:

1. **No read** — three waits with **zero** reads between them. The scanner count is **1** (the startup refresh alone).
2. **Read once** — read the snapshot, then one wait. The count becomes **2**.
3. **Read stops again** — two more waits with no reads: the count **stays 2**; then read once more and wait again: the count becomes **3**.

Build the waiter as a `libtime.WaiterDurationFunc` closure that pops the next scripted step (a `func()` that may read the snapshot) before returning nil, records the duration it was asked to wait for, and returns `context.Canceled` once the steps are exhausted so `Run` terminates. `pkg/pageindex/pageindex_test.go`'s `fastWaiter()` is the in-repo `libtime.WaiterDurationFunc` idiom to mirror. Because `Run` is called synchronously in this test, the closure runs on the test goroutine and needs no mutex.

Assert the phase boundaries numerically, not only the final count — a one-line "ever read" latch passes phases 1 and 2 and then keeps refreshing forever, which phase 3 must catch:
- before the phase-2 read, assert `scanner.callCount()` is `1`;
- before the phase-3 read, assert `scanner.callCount()` is `2` (the two no-read waits did not move it);
- after `Run` returns, assert `scanner.callCount()` is `3`;
- assert every recorded duration equals `libtime.Duration(sessionsnapshot.SessionRefreshInterval)`.

The reads must go through the **same instance `Run` is called on**, and the boundary assertions must run inside the waiter closure, immediately before each step is invoked. Suggested shape (adapt names to the file's style):

```go
// Declared first so both the scripted steps and the waiter can close over it;
// assigned once, after the waiter exists.
var running sessionsnapshot.Snapshot

steps := []func(){
	nil, nil, nil, // phase 1: three waits, zero reads
	func() { running.ResumeSessionIDs(ctx) }, // phase 2: one read, then one wait
	nil, nil, // phase 3: two waits, no reads
	func() { running.RegistrySessionIDs(ctx) }, // phase 3: one read, then one wait
}
var durations []libtime.Duration
next := 0
waiter := libtime.WaiterDurationFunc(func(_ context.Context, d libtime.Duration) error {
	durations = append(durations, d)
	if next >= len(steps) {
		return context.Canceled
	}
	// The boundary assertions live here, immediately before the step runs.
	switch next {
	case 3: // about to perform the phase-2 read
		Expect(scanner.callCount()).To(Equal(1))
	case 6: // about to perform the phase-3 read
		Expect(scanner.callCount()).To(Equal(2))
	}
	if step := steps[next]; step != nil {
		step()
	}
	next++
	return nil
})

running = newSnapshot(scanner, registry, probe, fixedClock(), waiter)
Expect(running.Run(ctx)).To(Succeed())
Expect(scanner.callCount()).To(Equal(3))
```

⚠️ Two traps in this shape, both of which make the test fail rather than pass, so neither is silent: the steps must read `running` (not the `BeforeEach` snapshot instance, whose read counter `Run` never observes), and `running` must be the instance built over the scripted `waiter` before `Run` is called, since `Params.Waiter` is fixed at construction.

### 4. Add the read-path-spawns-no-`ps` test (`pkg/sessionsnapshot/snapshot_test.go`)

Add a row named exactly:

```go
It("spawns no ps scan on the board read path (AC5)", func() {
```

It must drive the real board read path over the `snapshot` built in `BeforeEach` (which uses `countingScanner`), and prove the demand gate did not move the `ps` spawn onto the read path:

1. Build `service := newSnapshotBoard(snapshot)` (the existing helper).
2. Call `service.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})` and fail the spec if it errors.
3. Call `service.ListGoals(ctx, board.GoalQuery{})` and fail the spec if it errors.
4. Assert `scanner.callCount()` is `0` — neither request spawned a `ps` scan.
5. Positive control: call `snapshot.RefreshOnce(ctx)` and assert `scanner.callCount()` is `1` — the same recorder is non-zero after an explicit refresh, proving the recorder works.

This row is the invariant spec 025 bought (reads never spawn a process) restated for the new gate; it is a guard, not new behaviour. Do not assert any response body or classification here.

### 5. Reword the session-refresh bound in `docs/page-index.md`

In the *Task-list snapshot* section, reword the paragraph beginning "The page-derived task-list fields keep the bounds above." so the session-derived fields' bound is described as read-gated rather than as a fixed interval that runs regardless. The bullet must:
- no longer contain the string `fixed interval of at least 60 s`;
- name reads of the snapshot as the trigger for a refresh.

The implementer chooses the exact wording; the assertion is that the bullet names reads as the trigger, not a particular phrasing. A shape that satisfies both, to adapt:

```
- The page-derived task-list fields keep the bounds above. The session-derived
  fields — `session_state` and the activity date — carry the **session-refresh
  bound** instead: they come from the session snapshot, which is refreshed at a
  60 s cadence (`SessionRefreshInterval`) but only when the snapshot has been
  read since the previous refresh, so on a board that is being read they can lag
  reality by at most one refresh plus one rebuild, and an idle board refreshes
  nothing at all. This is the one place the bounds above no longer apply.
```

Do not touch the other bullets in that section (the "No new timer" bullet stays accurate: the timer still exists, it is now gated).

### 6. Create the `## Unreleased` section in `CHANGELOG.md`

`CHANGELOG.md`'s first heading is `## v0.97.1` and there is no `## Unreleased` section. Create one directly above `## v0.97.1`, carrying exactly one `fix:` bullet that names both the idle board and the `ps` child. Follow `changelog-guide.md`: `- fix: <what> [context]`, one bullet per logical change, specific. Keep the bullet on **one line** — the AC8 `awk` matches `idle` and `ps` on the same line, so a wrapped bullet fails the check even though the content is right. Do not copy shell comments from this prompt's `<verification>`, do not use the prompt filename as the entry, and do not edit the `# Changelog` preamble or any released section.

Suggested shape (adapt, keeping both the word `idle` and the token `ps` in the bullet):

```
## Unreleased

- fix: Gate the session-snapshot refresh on demand so an idle board spawns no `ps` child — the refresh loop keeps its 60 s cadence ceiling but skips a tick when the snapshot has not been read since the previous refresh, so a board serving no requests spawns no `ps -axww -o args=` process and does no refresh work, while a board being read stays current at the same cadence; the demand gate lives inside the concrete snapshot type, the `Snapshot` interface and the liveness classification contract are unchanged, and no request path spawns a process.
```

### 7. Self-check

Before finishing, re-run every `<verification>` command and confirm it passes. Walk Acceptance Criteria 4, 5, 6, 7 and 8 against the change and name what establishes each. Confirm `pkg/session/classify.go`, `docs/liveness-classification.md`, `pkg/sessionsnapshot/mocks/session-snapshot.go`, `pkg/board/**` and `pkg/factory/**` are byte-unchanged, and that the only changed files are `pkg/sessionsnapshot/snapshot.go`, `pkg/sessionsnapshot/refresh.go`, `pkg/sessionsnapshot/snapshot_test.go`, `docs/page-index.md` and `CHANGELOG.md`.
</requirements>

<constraints>
- `docs/liveness-classification.md` is a frozen contract: the four outcomes, the signal order and the five-minute window do not change. This change moves only **when** one input is recomputed. Do not edit `pkg/session/classify.go` or `docs/liveness-classification.md`.
- No HTTP response body, status code, query parameter or WebSocket frame changes.
- No new dependency, no new route, no new config knob, no new environment variable.
- **Keep the demand gate internal to the concrete `snapshot` type, so the `Snapshot` interface does not change.** `pkg/sessionsnapshot/mocks/session-snapshot.go` is a committed counterfeiter fake; the repo has no `make generate` target and counterfeiter is not a module dependency, so an interface change would leave that fake stale and break the test build. Do not edit that file by hand. If you believe the gate genuinely requires a new interface method, stop and report it as a blocker instead of changing the interface.
- No test spawns a real subprocess: the scanner is injected and the `Waiter` is injected. Hand-write test doubles — do not add a `//counterfeiter:generate` directive (it would leave the fake ungenerated and break the build).
- Concurrency goes through `github.com/bborbe/run`; a bare `go func()` is a violation. The refresh loop honours `ctx.Done()` through `Waiter.Wait` (unchanged). The demand counter is read and written only under `s.mu`, so `go test -race ./pkg/sessionsnapshot/...` stays clean.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`; no `context.Background()` inside `pkg/`; Ginkgo/Gomega tests in an external test package; `glog` for logging.
- Do not edit `specs/in-progress/028-precompute-task-list-snapshot.md` (its AC8 asserts `SessionRefreshInterval >= 60 s`, which this change leaves true) or `specs/in-progress/029-rescan-allocates-only-what-changed.md`.
- Do not commit — dark-factory handles git.
- Existing tests must still pass, with their assertions unchanged, except the one row this prompt explicitly rewrites (`It("refreshes once per SessionRefreshInterval (AC8)")`).

<!-- Open question for the human reviewer (spec Failure Modes row 5): the table says a failed `ps` refresh is retried on "the next tick". Under the demand gate a failed refresh is retried on the next tick **that has demand**; with no reads there is no demand and no retry. This is the intended reading of Desired Behavior 1 ("a tick with no read does no work") and is what makes an idle board stop spawning `ps` even after a transient failure. If the reviewer wants a failed refresh to retry unconditionally, the gate would need an exception for the failure path — that would re-introduce an idle spawn, so it is deliberately not implemented here. -->
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first. This repo sets `hideGit: true`, so **no `git` command is available here** — every check below is filesystem- or test-based.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/sessionsnapshot/... ./pkg/board/... ./pkg/session/... ./pkg/factory/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/session/... -v -args -ginkgo.v 2>&1 | grep -cE 'classifies a transcript written within the window as live|classifies a transcript older than the window as quiet|classifies a session with no transcript as indeterminate|lets the registry beat a stale transcript|reads an unknown live-id source with a stale transcript as indeterminate'
```
Must print `5` (the four outcomes and the two precedence rules are named; AC6). Note: plain `go test -v` is **not** enough — Ginkgo's default reporter prints only `•` dots on a passing run and names a spec only when it fails; `-args -ginkgo.v` makes it print every spec name, one per line.

```
grep -c 'func ClassifySessionState' pkg/session/classify.go
```
Must print `1` (AC6).

```
! grep -q 'fixed interval of at least 60 s' docs/page-index.md
```
Must find no match (AC7).

```
grep -niE 'read(s)? (of|from) the (session )?snapshot|snapshot has been read|when the board is read' docs/page-index.md
```
Must print at least one line (AC7).

```
awk '/^## /{sec=$0} /idle/ && /ps/ && sec=="## Unreleased"{print sec; f=1} END{exit !f}' CHANGELOG.md
```
Must print `## Unreleased` and exit 0 (AC8).

```
grep -c 'refreshes only when the snapshot has been read since the last refresh' pkg/sessionsnapshot/snapshot_test.go
```
Must print `1` (AC4 row present).

```
! grep -q 'refreshes once per SessionRefreshInterval' pkg/sessionsnapshot/snapshot_test.go
```
Must find no match (the now-false AC8 row is gone).

```
grep -c 'spawns no ps scan on the board read path' pkg/sessionsnapshot/snapshot_test.go
```
Must print `1` (AC5 row present).

```
! grep -q 'fixed interval of at least 60 s' pkg/sessionsnapshot/snapshot.go
```
Must find no match (the package doc comment no longer claims an unconditional fixed interval).
</verification>
