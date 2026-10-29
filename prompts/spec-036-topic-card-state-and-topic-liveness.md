---
spec: ["036-topic-card-state-and-topic-liveness"]
status: draft
created: "2026-10-29T13:03:19Z"
---

<!-- Generation note for the reviewer (not an instruction to the executor):
     At generation time (2026-10-29T13:03Z) the working tree already carried this
     change, written by a concurrently executing prompt
     (prompts/in-progress/171-topic-card-state-and-live-manager.md). Requirement 0
     is a fail-loud precondition so this prompt cannot re-apply the change to an
     already-fixed tree. If that run landed successfully, close this prompt as a
     duplicate rather than approving it. -->

# Topic cards stop contradicting their column and show a live manager

<summary>
- A topic card no longer prints a status that contradicts the phase column it sits in.
- The column keeps following the topic's `phase`, which is the board's grouping for every view.
- The topic card stops rendering a status badge at all, exactly as task and goal cards already do.
- The topic's own status stays visible in the detail modal and still drives the board's status filter.
- A topic whose manager session is live now shows a live indicator on its card.
- A topic with no manager session, or a dead one, shows no indicator.
- The live indicator is display-only: not a take-over control, not clickable, and it has its own CSS class.
- A topic's live state reaches the board on the same terms a goal's already does, through one shared classification helper.
- Both backends emit the same topic payload field set, so the parity harness does not diverge.
- The four-outcome liveness contract, the signal order, task cards and goal cards are unchanged.
</summary>

<objective>
Make the topic card self-consistent with the phase column it renders into, and surface whether a live manager session is running for that topic. The card currently reads its column from `phase` but its subtitle from `status`, so a topic carrying `phase: todo` + `status: in_progress` sits in the TODO column while its card says `in_progress`. The topic response also drops the page's `manager_session_id`, so no card can show a running manager.
</objective>

<context>
Read the spec `specs/in-progress/036-topic-card-state-and-topic-liveness.md` in full before writing. It is the source of truth for the acceptance criteria and failure modes.

Read these files fully before changing anything:
- `pkg/api/api.go` — `TopicResponse` (line 114) and `GoalResponse.SessionState` (line 104).
- `pkg/board/goals.go` — `goalResponse` and the manager-id classification helper; this is the pattern to reuse.
- `pkg/board/topics.go` — `ListTopics` (line 35), the function to extend.
- `pkg/board/tasks.go` — `sessionStatePtr` (line 965), the nil-for-none renderer.
- `pkg/board/board_test.go` — the `newHarness`, `harness.build`, `fakeSignals`, `fakeProbe` helpers, the goal session-id combination table (around line 874), and the `ListTopics` Describe block (line 1079).
- `pkg/board/parity_test.go` — the task-list payload fence, the pattern for the topic wire-shape pin.
- `src/vault_ui/static/app.js` — `createTopicCard` (line 1233), `renderTopics` (line 1207, status filter at line 1210), `liveBadgeLabel` (line 1561), `sessionButtonHtml`'s take-over badge (line 1578).
- `src/vault_ui/static/style.css` — the `.live-badge` rule (line 759) and `.topic-modal-header .status-badge` (line 1387).
- `src/vault_ui/static/index.html` — the `app.js?v=…` token (line 175).
- `src/vault_ui/api/models.py` — the `Topic` dataclass (line 69) and `TopicResponse` (line 170).
- `src/vault_ui/api/tasks.py` — `_topic_to_response` (line 1252).
- `tests/test_topics_view.py` — the `_topic_cards` helper and the topics-view specs.
- `tests/test_card_render_unify.py` — the `_function_body(name)` helper (line 24) and `test_cachebust_token_bumped` (line 108).
- `tests/test_api.py` — `test_list_topics_returns_topic_response_key_set` (line 6904).
- `docs/liveness-classification.md` — the frozen four-outcome contract; § "Multiple session ids on a card" starts at line 57.
- `README.md` — the `GET /api/topics` section (line 128).
- `CHANGELOG.md` — the current top heading is `## v0.100.0`; there is no `## Unreleased` section yet.
- `docs/dod.md` — the Definition of Done the daemon's `validationPrompt` points at.

Verified facts you may rely on (read from the source above; do not re-derive):
- `api.TopicResponse` is `{ID, Title, Status string; Phase *string; Flag bool; Vault, ObsidianURL string}` with JSON tags `id, title, status, phase, flag, vault, obsidian_url`. Add `SessionState *string` with `json:"session_state"` after `Phase` and before `Flag`, so the decoded field set is exactly `flag, id, obsidian_url, phase, session_state, status, title, vault`.
- `api.GoalResponse.SessionState` is `*string` with `json:"session_state"` and no `omitempty`; `TopicResponse` must match that form.
- `func classifyGoalManagerSessionID(ctx context.Context, sessionID string, params session.ClassifyParams) session.SessionState` lives in `pkg/board/goals.go` (line 225). It returns `session.SessionStateNone` for an empty id, `session.SessionStateIndeterminate` for a non-UUID id, and otherwise calls `session.ClassifySessionState` with `params.SessionID = sessionID`.
- `func sessionStatePtr(state session.SessionState) *string` lives in `pkg/board/tasks.go` (line 965) and returns nil for `session.SessionStateNone`.
- `session.ClassifyParams` fields are `ProjectDir string`, `ProjectsRoot string`, `Now libtime.DateTime`, `LiveWindow time.Duration`, `ResumeSessionIDs []string`, `RegistrySessionIDs []string`, `LiveIDsUnknown bool`, `TranscriptMtime activity.TranscriptMtimeGetter`.
- `func cleanup.DeriveClaudeProjectDir(homeDir, vaultPath, sessionProjectDir string) string` lives in `pkg/cleanup/derive.go` (line 21). `board.Vault` carries `SessionProjectDir` and `Path`.
- The `board` struct has `homeDir string`, `clock libtime.CurrentDateTimeGetter`, `signals SessionSignals`, and `sessions SessionProbe`; `b.transcriptProbe()` is the existing accessor used by `goalResponse`.
- `SessionSignals` provides `RegistrySessionIDs(ctx) []string`, `LiveIDsKnown() bool`, `ResumeSessionIDs(ctx) []string`, `RegistryLocation(ctx, id) string`.
- `newHarness()` builds a vault whose topics folder is `"23 Topics"`; set rows with `h.list.items["23 Topics"] = []ops.TaskListItem{...}`, the live set with `h.signals.registry = []heartbeat.Session{{ID: id}}`, unreachability with `h.signals.unknown = true`, and per-id transcripts with `h.sessions = &fakeProbe{mtimes: map[string]*libtime.DateTime{id: &stale}}`; call `h.build()` after mutating fakes.
- The Python `Topic` dataclass and `TopicResponse` are separate types; `_topic_to_response` builds the response from a `Topic`.
- `scripts/parity/parity.sh`'s `normalize()` deletes only `jump_pane`, `open_questions` and `location` from both sides. `scripts/parity/cases.txt` line 18 is `topics-filter	/api/topics?vault=personal`. The fixture topic `TopicOne` carries no `manager_session_id`, so both backends must serve `"session_state": null` for it.
- There is **no** `.topic-card .status-badge` CSS rule to delete; the only status-badge rule is `.topic-modal-header .status-badge` (line 1387), which stays.
- `tests/test_topics_view.py` is `integration`-marked and deselected by `make test`; it cannot run in the container.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

### 0. Precondition — the change must not already be present

Run:

```bash
grep -n 'SessionState: sessionStatePtr(classifyManagerSessionID' pkg/board/topics.go
```

If it prints a line, `ListTopics` already classifies the manager id and this spec has already landed. Do **not** re-apply anything; instead verify the Acceptance Criteria in the spec, report the outcome, and stop. Every requirement below assumes the pre-fix tree, where `api.TopicResponse` has no `SessionState` field.

### 1. Add `session_state` to `TopicResponse` (`pkg/api/api.go`)

Add the field after `Phase` and before `Flag`, matching `GoalResponse.SessionState` exactly:

```go
	// SessionState classifies the topic's manager session id on the same terms
	// as GoalResponse.SessionState: always emitted, null when the topic page
	// records no manager_session_id.
	SessionState *string `json:"session_state"`
```

No `omitempty`. The wire key must always be present.

### 2. Share the manager-id helper (`pkg/board/goals.go`)

Rename `classifyGoalManagerSessionID` to `classifyManagerSessionID`, update its doc comment to say it classifies the manager session id of a goal **or a topic** page, and update its single existing call site in `goalResponse`. Behaviour is unchanged: empty id → `session.SessionStateNone`; non-UUID → `session.SessionStateIndeterminate`; otherwise `session.ClassifySessionState` with `params.SessionID` set. Do not add `strongestSessionState` to topics — a topic has only one recorded id.

### 3. Classify the manager id in `ListTopics` (`pkg/board/topics.go`)

Read the three signals once, before the vault loop, exactly as `ListGoals` does:

```go
	registryIDs := b.signals.RegistrySessionIDs(ctx)
	liveIDsUnknown := !b.signals.LiveIDsKnown()
	resumeIDs := b.signals.ResumeSessionIDs(ctx)
```

Inside the vault loop, after the `ops.List(...).Execute(...)` call and before the item loop, build one `session.ClassifyParams` per vault:

```go
		params := session.ClassifyParams{
			ProjectDir:         cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir),
			ProjectsRoot:       filepath.Join(b.homeDir, ".claude", "projects"),
			Now:                b.clock.Now().UTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   resumeIDs,
			RegistrySessionIDs: registryIDs,
			LiveIDsUnknown:     liveIDsUnknown,
			TranscriptMtime:    b.transcriptProbe(),
		}
```

Then set the new field per item:

```go
				SessionState: sessionStatePtr(classifyManagerSessionID(ctx, item.ManagerSessionID, params)),
```

Leave every other field of `api.TopicResponse` untouched. Do not add `location`, `claude_session_started` or `activity_date` to a topic — a topic carries only the manager id. Add the imports `path/filepath`, `github.com/bborbe/vault-ui/pkg/cleanup` and `github.com/bborbe/vault-ui/pkg/session` to `topics.go`.

### 4. Mirror the field in the superseded Python backend

- `src/vault_ui/api/models.py`: add `session_state: str | None = None` to the `Topic` dataclass, with a comment that the Go backend classifies it from `manager_session_id` while this superseded harness sets it directly (`_parse_topic` never reads that field) and therefore always sends null in production.
- `src/vault_ui/api/models.py`: add `session_state: str | None = None` to `TopicResponse`, with a comment pointing at `TaskResponse.session_state` for the semantics.
- `src/vault_ui/api/tasks.py`: `_topic_to_response` passes `session_state=topic.session_state`.

Do not add a classifier to the Python backend, and do not add `manager_session_id` parsing to `_parse_topic`.

### 5. Topic card: drop the badge, add the display-only indicator (`src/vault_ui/static/app.js`)

In `createTopicCard`:
- Remove `<span class="status-badge">${escapeHtml(topic.status || '')}</span>` from `.card-footer` entirely.
- Add a display-only indicator as the first child of `.card-footer`, rendered only when the session is live:

```js
            ${topic.session_state === 'live' ? `<span class="topic-live-badge">${liveBadgeLabel(null)}</span>` : ''}
```

- Use the distinct class `topic-live-badge`, never `live-badge`. `.live-badge` is contractually the take-over control (`cursor: pointer`, hover/active shades, `role="button" tabindex="0" onclick="takeOverSession(...)"`, pinned by `tests/test_live_badge_location.py`). The topic indicator carries no `onclick`, no `role`, no `tabindex`, and is not clickable.
- Add a short comment recording why the badge is gone: the column is the topic's phase, the badge printed a different axis, and task and goal cards render no status badge either.
- Leave `renderTopics` (including the status filter at line 1210), `flagButtonHtml`, the detail button and `sessionButtonHtml` untouched.

### 6. Style the indicator (`src/vault_ui/static/style.css`)

Add a `.topic-live-badge` rule with the live pill's colours (green-200 text on green-700 background, the same values as `.live-badge`), but `cursor: default` and **no** `:hover`, `:active` or `:focus-visible` shade. Do not modify `.live-badge` or `.topic-modal-header .status-badge`. There is no `.topic-card .status-badge` rule to delete.

### 7. Bump the cache-buster and pin the old token (`src/vault_ui/static/index.html`)

Change the token at line 175 to `2026-10-29-topic-card-state`:

```html
    <script src="app.js?v=2026-10-29-topic-card-state"></script>
```

In `tests/test_card_render_unify.py`, extend `test_cachebust_token_bumped` with:

```python
    assert "app.js?v=2026-10-29-jump-pane-gating" not in INDEX_HTML
```

Do not pin the new literal token; the test asserts the shape plus the known-stale tokens, matching its existing comment.

### 8. Go tests (`pkg/board/board_test.go`, `pkg/board/parity_test.go`)

Add a new `Describe("topic session state", ...)` block (or extend the existing `ListTopics` block) with a `DescribeTable` covering the four spec cases. Model it on the goal table at `pkg/board/board_test.go` around line 874:

```go
var _ = Describe("topic session state", func() {
	const managerID = "22222222-2222-2222-2222-222222222222"

	stale := libtime.DateTime(baseTime.Add(-time.Hour))

	topicItem := func(manager string) ops.TaskListItem {
		return ops.TaskListItem{
			Name:             "Topic",
			Status:           "in_progress",
			Phase:            "todo",
			ManagerSessionID: manager,
		}
	}

	listTopic := func(h *harness) api.TopicResponse {
		responses, err := h.board.ListTopics(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		return responses[0]
	}

	DescribeTable("classifies the topic's manager session id",
		func(
			manager string,
			registry []heartbeat.Session,
			mtimes map[string]*libtime.DateTime,
			unknown bool,
			expected string,
		) {
			h := newHarness()
			h.list.items["23 Topics"] = []ops.TaskListItem{topicItem(manager)}
			h.signals.registry = registry
			h.signals.unknown = unknown
			h.sessions = &fakeProbe{mtimes: mtimes}
			h.build()

			response := listTopic(h)
			if expected == "" {
				Expect(response.SessionState).To(BeNil())
				return
			}
			Expect(response.SessionState).NotTo(BeNil())
			Expect(*response.SessionState).To(Equal(expected))
		},
		Entry("manager live", managerID,
			[]heartbeat.Session{{ID: managerID}}, nil, false, "live"),
		Entry("no manager id", "", nil, nil, false, ""),
		Entry("stale manager id", managerID, nil,
			map[string]*libtime.DateTime{managerID: &stale}, false, "quiet"),
		Entry("store unreachable", managerID, nil,
			map[string]*libtime.DateTime{managerID: &stale}, true, "indeterminate"),
	)

	It("serializes a missing manager id as null on the wire", func() {
		h := newHarness()
		h.list.items["23 Topics"] = []ops.TaskListItem{topicItem("")}
		h.build()

		response := listTopic(h)
		Expect(response.SessionState).To(BeNil())
		encoded, err := json.Marshal(response)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"session_state":null`))
	})
})
```

The `store unreachable` entry is load-bearing: it gives the manager id a stale transcript, so without `h.signals.unknown` it would read `quiet`. The expected `indeterminate` proves the store outage wins.

Add the wire-shape pin to `pkg/board/parity_test.go`, next to the task-list fence (add the `github.com/bborbe/vault-cli/pkg/ops` import):

```go
var _ = Describe("topic-list payload parity", func() {
	It("serves the topic-list payload field set", func() {
		h := newHarness()
		h.list.items["23 Topics"] = []ops.TaskListItem{
			{Name: "Topic", Status: "in_progress", Phase: "todo"},
		}

		responses, err := h.board.ListTopics(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred())
		body, err := json.Marshal(responses)
		Expect(err).NotTo(HaveOccurred())

		var rows []map[string]json.RawMessage
		Expect(json.Unmarshal(body, &rows)).To(Succeed())
		Expect(rows).NotTo(BeEmpty())

		fields := make([]string, 0, len(rows[0]))
		for field := range rows[0] {
			fields = append(fields, field)
		}
		sort.Strings(fields)

		Expect(fields).To(Equal([]string{
			"flag",
			"id",
			"obsidian_url",
			"phase",
			"session_state",
			"status",
			"title",
			"vault",
		}))
	})
})
```

### 9. Frontend static test (`tests/test_card_render_unify.py`)

Add, using the existing `_function_body(name)` helper:

```python
def test_topic_card_drops_status_badge_and_gates_the_live_indicator() -> None:
    """The topic card renders no status badge — its column is its phase — and
    shows the display-only live indicator only when the session is live."""
    body = _function_body("createTopicCard")
    assert "status-badge" not in body
    assert "session_state === 'live'" in body
    assert "topic-live-badge" in body
```

### 10. Rewrite `tests/test_topics_view.py` (do not delete it)

This file is `integration`-marked and deselected by `make test`, so `make precommit` cannot catch a stale assertion here. Rewrite it:
- Change the `_topic_cards` helper to report `live: c.querySelector('.topic-live-badge')?.textContent`, `hasStatusBadge: !!c.querySelector('.status-badge')` and `column: c.closest('[id^=cards-]')?.id` — it must not query `.status-badge` as the card's status.
- Rename `test_topics_render_each_own_status_and_phase_column` to `test_topic_cards_carry_no_status_badge_and_sit_in_their_phase_column`. Assert every card has no status badge, assert each card's column matches its `phase` (`cards-execution` and `cards-done`), and add a third fixture `Topic` with `session_state="live"` (e.g. id `Manager Armed`, `phase="planning"`) so exactly that card renders a `.topic-live-badge` while the other two render none. Bump the card-count assertion from 2 to 3.
- Rewrite the docstring of `test_opening_a_topic_shows_its_own_status`: the card no longer renders the status, so the modal is the only place it stays visible. Keep both status assertions there.
- Leave the status-filter behaviour and its coverage unchanged; the filter is client-side code this change does not touch.

### 11. Python API test (`tests/test_api.py`)

Add `"session_state"` to the expected key set in `test_list_topics_returns_topic_response_key_set` (line 6904).

### 12. Documentation (`docs/liveness-classification.md`)

In § "Multiple session ids on a card" (line 57), add one sentence: a topic page records only `manager_session_id` (no topic page carries `claude_session_id`), so a topic's outcome is that single id's outcome. Do not change the four outcomes, the signal order or the window.

### 13. README (`README.md`)

In the `GET /api/topics` section (line 128), update the example payload to the real field set:

```json
[
  {
    "id": "Manager Layer",
    "title": "Manager Layer",
    "status": "in_progress",
    "phase": "todo",
    "session_state": null,
    "vault": "personal",
    "obsidian_url": "obsidian://open?vault=personal&file=23%20Topics/Manager%20Layer.md",
    "flag": false
  }
]
```

Describe the state in prose using the tokens `live`, `indeterminate`, `quiet` and null, but **do not write the literal word `session_state` anywhere else in this section** — the acceptance check counts occurrences and requires exactly one, which is the JSON key above.

### 14. CHANGELOG (`CHANGELOG.md`)

Create `## Unreleased` directly above `## v0.100.0` with one `fix:` bullet containing the phrase `topic card`. Cover both halves: the card no longer renders a status badge that contradicts its phase column, and `GET /api/topics` carries the manager-session state rendered as a display-only live indicator. One bullet, one line, no shell comments copied from this prompt.

</requirements>

<constraints>
- **Do NOT change task or goal cards, or any task or goal response field.** They render exactly as they do today.
- **Do NOT write to any `23 Topics/` page** and do not backfill `phase`, `assignee`, `priority` or `manager_session_id`. This is a rendering and read-path change.
- **The four-outcome liveness contract and its signal order are frozen** (`docs/liveness-classification.md`). No fifth state, no reordering, no new window, no config knob.
- **Unreachable store stays `indeterminate`.** A store outage must never turn a live manager into a silent card.
- The indicator is **display-only** and must not reuse the take-over affordance's class. `.live-badge` is contractually the take-over control (`cursor: pointer`, hover/active shades, `role="button" tabindex="0" onclick="takeOverSession(...)"`). The topic indicator uses `topic-live-badge` and carries no `onclick`, no `role`, no `tabindex`, no pointer cursor and no hover shade.
- **`tests/test_topics_view.py` must be rewritten, not deleted.** It is `integration`-marked and deselected by `make test`, so `make precommit` cannot catch a stale assertion there.
- The superseded Python backend's `TopicResponse` carries the same field so `scripts/parity/parity.sh` and `scripts/parity/cases.txt:18` do not diverge. **Do NOT add `session_state` to the parity normalize list** — that would weaken the safety net for every future field.
- Do NOT converge the three card builders into one renderer. Do NOT add a take-over, start, resume or jump control to a topic card.
- Do NOT commit — dark-factory handles git. Do NOT bump the version or tag; the operator cuts the release.
- The container has no background commands, no `tail -f`, no `watch`, and no kubectl. Run commands in the foreground. Use `make test` iteratively after each change and run `make precommit` once at the very end.
- Existing tests must still pass. New code must meet the repo's coverage rule (`docs/dod.md`).
</constraints>

<verification>
Run in the foreground, in this order:

```bash
make precommit
```

Must exit 0.

```bash
go test ./pkg/board/... -count=1
```

Must exit 0.

```bash
grep -q 'SessionState \*string `json:"session_state"`' pkg/api/api.go
```

Must exit 0 (the wire field and its JSON name are present).

```bash
grep -q 'topic-list payload parity' pkg/board/parity_test.go
```

Must exit 0 (the topic wire-shape pin exists).

```bash
uv run pytest tests/test_card_render_unify.py tests/test_api.py -q
```

Must exit 0 (the static card assertion, the stale-token assertion and the topic key-set assertion).

```bash
grep -n 'topic-live-badge' src/vault_ui/static/app.js src/vault_ui/static/style.css
```

Must print at least one line per file (the class exists in both).

```bash
! grep -nE 'topic-live-badge[^`]*(onclick|role=|tabindex)' src/vault_ui/static/app.js
```

Must find no match (the indicator carries no interactive attribute).

```bash
! awk '/^\.topic-live-badge/,/^}/' src/vault_ui/static/style.css | grep -q 'cursor: pointer'
```

Must exit 0 (the indicator is not a pointer control).

```bash
! grep -q '^\.topic-live-badge:hover' src/vault_ui/static/style.css
```

Must find no match (no hover shade on the display-only indicator).

```bash
grep -q 'app.js?v=2026-10-29-topic-card-state' src/vault_ui/static/index.html && ! grep -q 'app.js?v=2026-10-29-jump-pane-gating' src/vault_ui/static/index.html
```

Must exit 0 (the token is bumped and the old one is gone).

```bash
grep -q 'topic' docs/liveness-classification.md
```

Must exit 0 (the topic sentence landed).

```bash
test "$(sed -n '/### `GET \/api\/topics`/,/### `GET \/api\/topics\//p' README.md | grep -c session_state)" = "1"
```

Must exit 0 (the README example carries the key exactly once).

```bash
awk '/^## /{sec=$0} /topic card/ && sec=="## Unreleased"{print sec; f=1} END{exit !f}' CHANGELOG.md
```

Must print `## Unreleased` and exit 0.

```bash
! awk '/^function createTopicCard\(/,/^}/' src/vault_ui/static/app.js | grep -q 'status-badge'
```

Must find no match (the topic card renders no status badge).

### Operator-executable — host only, do NOT run in the container

⚠️ **production-touching:** the restart below replaces the running board process. These commands need a browser, the host's `launchctl` and the deployed board; they are not part of the container verification.

```bash
uv run pytest tests/test_topics_view.py -m integration
make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
curl -s --noproxy '*' 'http://127.0.0.1:8000/api/topics?vault=seibert-brogrammers' \
  | jq -r '.[] | "\(.title) | \(.phase) | \(.session_state)"'
```

Expected: the five named topics read `todo` with `session_state: null`, and no card in `#cards-todo` carries a status badge. AC8's byte-identical topic-page check is also operator-side: checksum every `23 Topics/*.md` before and after (`find "23 Topics" -name '*.md' -print0 | sort -z | xargs -0 shasum | shasum`) and compare.

## Completion report

Write the standard DARK-FACTORY-REPORT block appended by the daemon, then stop. Report `"status":"success"` only if `make precommit` exited 0; include `"verification":{"command":"make precommit","exitCode":N}` with the real exit code. Never self-report success when a verification command failed. Then append the required `## Improvements` section (categorize each item as PROMPT, GUIDE or GLOBAL; write `- None` if there is nothing).
</verification>
