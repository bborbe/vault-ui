---
status: completed
spec: [035-bug-goal-card-ignores-manager-session]
summary: Goal cards now classify both claude_session_id and manager_session_id with strongest-outcome combination and fixed own-then-manager location precedence, with UUID validation for the manager id only.
execution_id: vault-ui-goal-manager-liveness-exec-168-spec-035-combine-goal-session-ids
dark-factory-version: v0.198.7
created: "2026-10-19T16:56:40Z"
queued: "2026-10-19T17:31:58Z"
started: "2026-10-19T17:36:42Z"
completed: "2026-10-19T17:38:51Z"
branch: dark-factory/bug-goal-card-ignores-manager-session
---

# Classify a goal against its own and its manager's session ids

<summary>
- A goal card now reads `live` when the manager session that armed it is live, even when the goal's own session id is stale or empty.
- Every non-empty session id the page records is classified independently, and the card takes the strongest outcome.
- A live own id is never downgraded by a stale manager id, and a live manager id is never downgraded by a stale own id.
- An empty own id plus a stale manager id reads `quiet`; an unreachable attention store keeps the card `indeterminate`.
- A malformed manager id reads `indeterminate`, never `live` or `quiet`, so Resume is suppressed rather than offered.
- The live badge's location comes from the first live id in a fixed order, with no fall-through when that id has no store location.
- Task-card classification is untouched, and the goal's starting marker and activity date still derive from its own session id.
- The four-outcome contract gains no fifth state and no config knob.
</summary>

<objective>
A goal card reads live whenever either session id its page records is live, so a goal armed by a running manager session stops offering Resume. The strongest per-id outcome wins, and the badge's location comes from the own id first, then the manager id.
</objective>

<context>
Read the spec `specs/in-progress/035-bug-goal-card-ignores-manager-session.md` in full before writing. This prompt covers Desired Behavior 1–3 and the combination, location, and unchanged-field Acceptance Criteria. Prompt 1 has already bumped vault-cli to a release whose `ops.List` populates `ops.TaskListItem.ManagerSessionID`; that field is available here. Prompt 3 updates the docs and changelog.

Read these files fully before writing:
- `docs/liveness-classification.md` — the frozen four-outcome contract and signal order this change must preserve. Its `none` row and "Multiple session ids on a card" section are prompt 3's job, not this prompt's.
- `pkg/board/goals.go` — `goalResponse` is the only function that changes. It currently classifies one id and selects `Location` from that same id.
- `pkg/board/tasks.go` — `sessionStatePtr` and `liveLocationPtr` are the rendering helpers this change reuses. Do **not** change `buildTaskRows` or `refreshTaskRows`.
- `pkg/session/classify.go` — `ClassifySessionState` and `ClassifyParams`. Do **not** change the classifier; the signal order and four outcomes are frozen.
- `pkg/session/session.go` — the `SessionState` constants and `DefaultLiveWindow`.
- `pkg/board/board_test.go` — the `fakeSignals`, `fakeProbe`, `newHarness`, `harness.build` and `item` helpers; the existing `ListGoals` and `live badge location` specs.
- `pkg/api/api.go` — `GoalResponse`. `SessionState` is `*string` with `json:"session_state"` (no `omitempty`), and `Location` is `*string` with `json:"location"`.

Verified facts you may rely on (read from the source above; do not re-derive):
- `session.ClassifyParams` fields are `SessionID string`, `ProjectDir string`, `ProjectsRoot string`, `Now libtime.DateTime`, `LiveWindow time.Duration`, `ResumeSessionIDs []string`, `RegistrySessionIDs []string`, `LiveIDsUnknown bool`, `TranscriptMtime activity.TranscriptMtimeGetter`.
- `session.ClassifySessionState(ctx context.Context, params ClassifyParams) SessionState` returns `SessionStateNone` for an empty `SessionID` before probing anything.
- `session.SessionStateNone == ""`, `SessionStateLive == "live"`, `SessionStateQuiet == "quiet"`, `SessionStateIndeterminate == "indeterminate"`.
- `liveLocationPtr(state session.SessionState, location string) *string` returns nil unless `state == SessionStateLive` and `location != ""`.
- `sessionStatePtr(state session.SessionState) *string` returns nil for `SessionStateNone`.
- `github.com/google/uuid` is already a direct dependency in `go.mod`; `uuid.Validate(s string) error` exists (`uuid.go:195` in v1.6.0).
- `fakeProbe.TranscriptMtime` currently returns one `mtime` for every session id; the tests below need per-id mtimes.
- `ops.TaskListItem` gains `ManagerSessionID string` from prompt 1's bump. This field is **not** in the tree's current `vault-cli v0.167.0`; requirement 0 confirms the bump landed before any code is written.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`
</context>

<requirements>

### 0. Precondition — the bumped read model must be in `go.mod`

Run:

```bash
grep -rn 'ManagerSessionID' \
  "$(go env GOMODCACHE)/github.com/bborbe/vault-cli@$(go list -m -f '{{.Version}}' github.com/bborbe/vault-cli)/pkg/ops/list.go"
```

If there is no match, stop and report that prompt 1's vault-cli bump has not landed. Every requirement below assumes `ops.TaskListItem.ManagerSessionID` exists and the package will not compile without it.

### 1. Add the combination and manager-classification helpers (`pkg/board/goals.go`)

Add `github.com/google/uuid` to the import block. Add these unexported helpers near `goalResponse`:

```go
// strongestSessionState combines per-id outcomes: live beats indeterminate,
// which beats quiet. none is the result only when every id is empty. The
// combination is strongest-outcome, never last-id-wins.
func strongestSessionState(states ...session.SessionState) session.SessionState {
	best := session.SessionStateNone
	for _, state := range states {
		if sessionStateRank(state) > sessionStateRank(best) {
			best = state
		}
	}
	return best
}

// sessionStateRank orders the four outcomes for combination.
func sessionStateRank(state session.SessionState) int {
	switch state {
	case session.SessionStateLive:
		return 3
	case session.SessionStateIndeterminate:
		return 2
	case session.SessionStateQuiet:
		return 1
	default:
		return 0
	}
}

// classifyGoalManagerSessionID classifies the manager session id a goal page
// records. An empty id is none. A malformed (non-UUID) id cannot be proven
// dead, so it is indeterminate rather than probed: the card suppresses Resume
// instead of offering it.
func classifyGoalManagerSessionID(
	ctx context.Context,
	sessionID string,
	params session.ClassifyParams,
) session.SessionState {
	if sessionID == "" {
		return session.SessionStateNone
	}
	if err := uuid.Validate(sessionID); err != nil {
		return session.SessionStateIndeterminate
	}
	params.SessionID = sessionID
	return session.ClassifySessionState(ctx, params)
}
```

Do not add UUID validation to the own id: `claude_session_id` may name an entity-name session, and the classifier's existing behavior for it must not change. Validation is scoped to the new manager field.

### 2. Combine both ids in `goalResponse` (`pkg/board/goals.go`)

Replace the single `state := session.ClassifySessionState(...)` block with a shared params value and two classifications:

```go
	params := session.ClassifyParams{
		ProjectDir:         projectDir,
		ProjectsRoot:       projectsRoot,
		Now:                b.clock.Now().UTC(),
		LiveWindow:         session.DefaultLiveWindow,
		ResumeSessionIDs:   resumeIDs,
		RegistrySessionIDs: registryIDs,
		LiveIDsUnknown:     liveIDsUnknown,
		TranscriptMtime:    b.transcriptProbe(),
	}

	ownParams := params
	ownParams.SessionID = item.ClaudeSessionID
	ownState := session.ClassifySessionState(ctx, ownParams)
	managerState := classifyGoalManagerSessionID(ctx, item.ManagerSessionID, params)
	state := strongestSessionState(ownState, managerState)
```

`ClassifySessionState` is a pure function over the params; calling it twice does not mutate shared state. `RegistrySessionIDs` and `ResumeSessionIDs` are read once in `ListGoals` and passed down, and `b.transcriptProbe()` returns a method value with no I/O, so the second classification adds no store call and no extra probe read.

### 3. Select `Location` from the first live id, with no fall-through

Keep `liveLocationPtr` and add the selection immediately after the combination:

```go
	var location *string
	switch {
	case ownState == session.SessionStateLive:
		location = liveLocationPtr(ownState, b.signals.RegistryLocation(ctx, item.ClaudeSessionID))
	case managerState == session.SessionStateLive:
		location = liveLocationPtr(managerState, b.signals.RegistryLocation(ctx, item.ManagerSessionID))
	}
```

Then render `SessionState: sessionStatePtr(state)` and `Location: location` in the returned `api.GoalResponse`. The fixed precedence is own id first, manager id second. When the own id is live but `RegistryLocation` returns empty (live by transcript recency or the `ps` cross-check, not in the store), `liveLocationPtr` returns nil and the switch does **not** fall through to the manager id.

### 4. Keep the unchanged fields unchanged

Do not change:
- `ClaudeSessionID: strPtr(item.ClaudeSessionID)`
- `ClaudeSessionStarted: b.sessionStarted(vault.Name, item.Name)`
- `ActivityDate: dateTimeString(activity.ComputeActivityDateWith(ctx, b.transcriptProbe(), parseDateTime(item.ModifiedDate), item.ClaudeSessionID, projectDir, projectsRoot))`

The starting marker and activity date continue to derive from the goal's own id and page marker. Do not add `ManagerSessionID` to `api.GoalResponse`, and do not touch `pkg/board/tasks.go` or `pkg/session/classify.go`.

### 5. Extend `fakeProbe` with per-id transcript mtimes (`pkg/board/board_test.go`)

The combination table needs one id with a stale transcript and another with no transcript in the same goal. Extend `fakeProbe`:

```go
type fakeProbe struct {
	mu         sync.Mutex
	mtime      *libtime.DateTime
	mtimes     map[string]*libtime.DateTime
	generation uint64
	// transcriptCalls counts TranscriptMtime calls, so a spec can prove a path
	// never probed a transcript.
	transcriptCalls seamCounter
}

func (f *fakeProbe) TranscriptMtime(
	_ context.Context,
	sessionID, _, _ string,
) *libtime.DateTime {
	f.transcriptCalls.inc()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mtimes != nil {
		return f.mtimes[sessionID]
	}
	return f.mtime
}
```

Existing tests that set only `mtime` keep working because `mtimes` stays nil. A non-nil `mtimes` map with a missing id returns nil (no transcript).

### 6. Add the combination table (`pkg/board/board_test.go`)

Add `"github.com/bborbe/vault-ui/pkg/api"` to the test file's imports. Add a new `Describe` after the existing `ListGoals` suite:

```go
var _ = Describe("goal session-id combination", func() {
	const (
		ownID     = "11111111-1111-1111-1111-111111111111"
		managerID = "22222222-2222-2222-2222-222222222222"
	)

	stale := libtime.DateTime(baseTime.Add(-time.Hour))
	fresh := libtime.DateTime(baseTime)

	goalItem := func(own, manager string) ops.TaskListItem {
		return ops.TaskListItem{
			Name:             "Goal",
			Status:           "in_progress",
			ClaudeSessionID:  own,
			ManagerSessionID: manager,
		}
	}

	listGoal := func(h *harness) api.GoalResponse {
		responses, err := h.board.ListGoals(
			context.Background(), board.GoalQuery{UpcomingHours: 8},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		return responses[0]
	}

	DescribeTable("combines every recorded session id by strongest outcome",
		func(
			own, manager string,
			registry []heartbeat.Session,
			mtimes map[string]*libtime.DateTime,
			expected string,
		) {
			h := newHarness()
			h.list.items["23 Goals"] = []ops.TaskListItem{goalItem(own, manager)}
			h.signals.registry = registry
			h.sessions = &fakeProbe{mtimes: mtimes}
			h.build()

			response := listGoal(h)
			if expected == "" {
				Expect(response.SessionState).To(BeNil())
				return
			}
			Expect(response.SessionState).NotTo(BeNil())
			Expect(*response.SessionState).To(Equal(expected))
		},
		Entry("own live, manager quiet", ownID, managerID,
			[]heartbeat.Session{{ID: ownID}},
			map[string]*libtime.DateTime{managerID: &stale}, "live"),
		Entry("manager live, own quiet", ownID, managerID,
			[]heartbeat.Session{{ID: managerID}},
			map[string]*libtime.DateTime{ownID: &stale}, "live"),
		Entry("own live, manager indeterminate", ownID, managerID,
			[]heartbeat.Session{{ID: ownID}}, nil, "live"),
		Entry("own empty, manager live", "", managerID,
			[]heartbeat.Session{{ID: managerID}}, nil, "live"),
		Entry("own empty, manager quiet", "", managerID, nil,
			map[string]*libtime.DateTime{managerID: &stale}, "quiet"),
		Entry("own quiet, manager indeterminate", ownID, managerID, nil,
			map[string]*libtime.DateTime{ownID: &stale}, "indeterminate"),
		Entry("both quiet", ownID, managerID, nil,
			map[string]*libtime.DateTime{ownID: &stale, managerID: &stale}, "quiet"),
		Entry("own live, manager empty", ownID, "",
			[]heartbeat.Session{{ID: ownID}}, nil, "live"),
		Entry("both empty", "", "", nil, nil, ""),
	)
```

The `manager live, own quiet` entry is the regression case: before this change it returned `quiet`, because only `claude_session_id` was classified.

Add the wire-shape assertion for the `none` state:

```go
	It("serializes the none state as null when neither id is set", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{goalItem("", "")}
		h.build()

		response := listGoal(h)
		Expect(response.SessionState).To(BeNil())
		encoded, err := json.Marshal(response)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"session_state":null`))
	})
```

### 7. Add the malformed-manager-id test

```go
	It("classifies a malformed manager id as indeterminate, never live or quiet", func() {
		const malformed = "not-a-uuid"
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{goalItem("", malformed)}
		// A fresh transcript would make an unvalidated id read live; the UUID
		// guard must win and suppress Resume instead.
		h.sessions = &fakeProbe{
			mtimes: map[string]*libtime.DateTime{malformed: &fresh},
		}
		h.build()

		response := listGoal(h)
		Expect(response.SessionState).NotTo(BeNil())
		Expect(*response.SessionState).To(Equal("indeterminate"))
		Expect(response.Location).To(BeNil())
	})
```

### 8. Add the unreachable-store test

```go
	It("keeps a manager-armed goal indeterminate when the attention store is unreachable", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{goalItem("", managerID)}
		h.signals.unknown = true
		h.sessions = &fakeProbe{
			mtimes: map[string]*libtime.DateTime{managerID: &stale},
		}
		h.build()

		response := listGoal(h)
		Expect(response.SessionState).NotTo(BeNil())
		Expect(*response.SessionState).To(Equal("indeterminate"))
	})
```

### 9. Add the location-precedence tests

```go
	It("takes location from the own id when both ids are live", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{goalItem(ownID, managerID)}
		h.signals.registry = []heartbeat.Session{
			{ID: ownID, Location: "local"},
			{ID: managerID, Location: "pod"},
		}
		h.build()

		response := listGoal(h)
		Expect(response.Location).NotTo(BeNil())
		Expect(*response.Location).To(Equal("local"))
	})

	It("does not fall through to the manager id when the own id is live without a store location", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{goalItem(ownID, managerID)}
		// Own id is live by transcript recency, not in the store, so its
		// RegistryLocation is empty. The manager id is live in the store with a
		// location; the location must still be null, not the manager's.
		h.signals.registry = []heartbeat.Session{{ID: managerID, Location: "pod"}}
		h.sessions = &fakeProbe{
			mtimes: map[string]*libtime.DateTime{ownID: &fresh},
		}
		h.build()

		response := listGoal(h)
		Expect(response.SessionState).NotTo(BeNil())
		Expect(*response.SessionState).To(Equal("live"))
		Expect(response.Location).To(BeNil())
	})

	It("takes location from the manager id when only the manager is live", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{goalItem("", managerID)}
		h.signals.registry = []heartbeat.Session{{ID: managerID, Location: "pod"}}
		h.build()

		response := listGoal(h)
		Expect(response.SessionState).NotTo(BeNil())
		Expect(*response.SessionState).To(Equal("live"))
		Expect(response.Location).NotTo(BeNil())
		Expect(*response.Location).To(Equal("pod"))
	})
```

### 10. Add the unchanged-fields test

```go
	It("keeps claude_session_started and activity_date tied to the own id", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{{
			Name:             "Goal",
			Status:           "in_progress",
			ManagerSessionID: managerID,
			ModifiedDate:     "2026-10-04T11:00:00Z",
		}}
		h.signals.registry = []heartbeat.Session{{ID: managerID}}
		// The manager transcript is newer than the page's modified date. If
		// either field derived from the manager id, activity_date would move to
		// 12:00; it must stay at the page's modified date.
		h.sessions = &fakeProbe{
			mtimes: map[string]*libtime.DateTime{managerID: &fresh},
		}
		h.cache.started["Goal"] = "2026-10-04T09:00:00Z"
		h.build()

		response := listGoal(h)
		Expect(response.SessionState).NotTo(BeNil())
		Expect(*response.SessionState).To(Equal("live"))
		Expect(response.ClaudeSessionID).To(BeNil())
		Expect(response.ClaudeSessionStarted).NotTo(BeNil())
		Expect(*response.ClaudeSessionStarted).To(Equal("2026-10-04T09:00:00Z"))
		Expect(response.ActivityDate).NotTo(BeNil())
		Expect(*response.ActivityDate).To(Equal("2026-10-04T11:00:00Z"))
	})
})
```

### 11. Self-check

Before finishing, re-run every `<verification>` command and confirm it passes. Walk the spec's combination, location, malformed-id, unreachable-store and unchanged-field Acceptance Criteria against the change and name the test that establishes each. Confirm `pkg/board/tasks.go`, `pkg/session/classify.go` and `pkg/api/api.go` are byte-unchanged, and that the only changed production file is `pkg/board/goals.go`.
</requirements>

<constraints>
- **The four outcomes and their signal order are frozen** (`docs/liveness-classification.md`). No fifth state, no reordering, no new window, no config knob. Do not edit `pkg/session/classify.go`.
- **Unreachable store stays `indeterminate`.** A store outage must never turn a live goal into Resume — the existing `LiveIDsUnknown` path is unchanged.
- **Task-card behaviour is unchanged.** `session.ClassifySessionState` is shared by `pkg/board/tasks.go` and `pkg/board/goals.go`; a change that alters task classification is a regression. Do not edit `pkg/board/tasks.go`.
- **`claude_session_started` keeps reading its own page marker, and `activity_date` keeps deriving from `claude_session_id`** — neither changes meaning (`docs/starting-marker-lifecycle.md`).
- Do NOT add a goal-card jump control: goal cards have no jump route (`src/vault_ui/static/app.js`, `if (kind !== 'task')`).
- Do NOT backfill `manager_session_id` on goal pages from code.
- Do NOT add a `manager_session_id` field to `api.GoalResponse` — nothing reads it; the badge renders from `session_state`. If a consumer appears, that is a separate spec.
- Do NOT add `session_state` to topic cards.
- Do NOT change the attention store, its heartbeat contract, or the session registry.
- Do NOT correct a stale manager id whose successor never re-armed the goal — that data accuracy belongs to claude-supervisor.
- The manager id's malformed-id guard is scoped to `manager_session_id`; do not apply UUID validation to `claude_session_id`, which may name an entity-name session.
- No new config knob, no new route, no new environment variable.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`; no `context.Background()` inside `pkg/`; Ginkgo/Gomega tests in an external test package; `glog` for logging.
- Do not commit — dark-factory handles git.
- Existing tests must still pass with their assertions unchanged.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first. This repo sets `hideGit: true`, so **no `git` command is available here** — every check below is filesystem- or test-based.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/board/... -count=1
```

Must pass, including the existing task-classification specs and the new goal combination, location, malformed-id, unreachable-store and unchanged-field specs.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```

Must exit 0.

```
grep -c 'func strongestSessionState' pkg/board/goals.go
```

Must print `1`.

```
test "$(grep -c 'classifyGoalManagerSessionID' pkg/board/goals.go)" -ge 2
```

Must exit 0 (the helper definition and its call site are both present).

```
! grep -q 'ManagerSessionID' pkg/board/tasks.go
```

Must find no match (task-card classification is untouched).

```
! grep -q 'ManagerSessionID' pkg/api/api.go
```

Must find no match (the wire response gains no new field).

```
! grep -q 'uuid.Validate' pkg/session/classify.go
```

Must find no match (the shared classifier is untouched).
</verification>
