---
status: approved
spec: [031-task-list-body-cache]
created: "2026-10-09T20:51:14Z"
queued: "2026-10-09T21:18:12Z"
branch: dark-factory/task-list-body-cache
---

# Prove the wired path end to end, then document and record it

<summary>
- A real board wired to the real task-list handler serves the gzip form when the client negotiates it.
- The compressed response decompresses to exactly the plain response, byte for byte.
- The plain response carries the same task rows the board's row projection produces, so the decoded body is unchanged.
- Two identical requests through the real path return identical bytes, proving the held body is reused end to end.
- The page-index design doc gains a short section describing the body cache and when a held body is discarded.
- The changelog records the body cache as a feature under a new unreleased heading.
- The operator's live-board ratio probe is left on the spec's verification ladder; it cannot run inside the container.
</summary>

<objective>
Prove the body cache and the handler's negotiation through the real board and the real handler, not a fake, so a wiring mistake between the two layers cannot ship — then document the body cache in `docs/page-index.md` and record the change in `CHANGELOG.md`. This covers the container-runnable half of the spec's Suggested Decomposition row 4 — the gzip negotiation proved through the real board and the real handler — plus Acceptance Criterion 4 over the real path. AC8's operator half (the deployed board's `Content-Encoding: gzip` deploy_check and the 10-sample ratio bar) stays on the spec's Verification § Operator-executable rung and is covered by no prompt. It runs after prompts 1–3.
</objective>

<context>
Read the spec `specs/in-progress/031-task-list-body-cache.md` in full, especially Acceptance Criteria 2, 4 and 8, the Post-Deploy note on AC8, and the Verification section.

Read the code the earlier prompts added and the tests they read:
- `pkg/board/board.go` — `New`, `Deps`, `Board`, `ListTasks`, `ListTasksBody`.
- `pkg/board/taskbody.go` — `TaskListBody`, `taskBodyCache`, `Get`.
- `pkg/board/tasks.go` — `ListTasks`.
- `pkg/handler/api_tasks.go` — `NewTasksHandler` and the negotiation (prompt 3).
- `pkg/board/board_test.go` — `newHarness`, `harness`, `item`, `vault`, and the fakes they build. The new test reuses `newHarness`; it must not duplicate its fakes.
- `docs/page-index.md` — in full; note the `## Task-list snapshot` section and its surrounding headings. The spec says the invalidation model documented there is unchanged, so do not edit its content — add a new section beside it.
- `CHANGELOG.md` — the file begins with `# Changelog`, then a preamble, then `## v0.93.1`. There is no `## Unreleased` section yet unless another spec's prompt added one.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

`.dark-factory.yaml` sets `hideGit: true`, so the container's `.git` is masked. Never rely on a `git` command in this prompt: `git diff` silently becomes `--no-index` and prints fabricated results. All verification below is non-git.

Deviation from the spec's Suggested Decomposition, for the reviewer. The spec's row 4 is a live-board observation of the deployed board, and its AC8 is marked Post-Deploy (Rung-2). A prompt executes inside a YOLO container that cannot reach the deployed board, so an operator observation cannot be a prompt. This prompt instead builds the container-runnable half of AC8 — the gzip negotiation proved through the real board and the real handler — and documents the change. AC8's ratio bar stays on the spec's Verification § *Operator-executable* rung, which already names the 10-sample same-window probe; no prompt can run it in-container.
</context>

<requirements>

### 1. An end-to-end test through the real board and the real handler

Add `pkg/board/tasks_body_http_test.go`, package `board_test` (the same external test package and Ginkgo/Gomega suite as `board_test.go`, so it can use `newHarness`); the cases below are `Describe`/`It` blocks. It imports `context`, `encoding/json`, `io`, `net/http`, `net/http/httptest`, `compress/gzip` (for `gzip.NewReader` only — do not construct a gzip writer here), `github.com/bborbe/vault-cli/pkg/ops`, `github.com/bborbe/vault-ui/pkg/api`, `github.com/bborbe/vault-ui/pkg/board` and `github.com/bborbe/vault-ui/pkg/handler`, plus the suite's Ginkgo/Gomega dot-imports.

Build a real board with the existing harness, for example:

```go
h := newHarness(
	item("Keep", func(i *ops.TaskListItem) { i.Status = "todo" }),
	item("Done", func(i *ops.TaskListItem) { i.Status = "completed" }),
)
router := handler.NewTasksHandler(h.board)
```

Add a helper that serves one `/api/tasks` request with a given `Accept-Encoding`:

```go
serve := func(acceptEncoding string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	router.ServeHTTP(recorder, req)
	return recorder
}
```

Cover:

1. **AC8 (gzip half) — the real path negotiates.** `serve("gzip")` → status 200, `Content-Encoding` equals `gzip`, `Vary` equals `Accept-Encoding`.
2. **AC8 (gzip half) — the identity path.** `serve("")` → status 200, `Content-Encoding` is absent, `Vary` equals `Accept-Encoding`.
3. **AC4 — the gzipped form decodes to the identity form.** Gunzip the gzip response's body with `gzip.NewReader` and `io.ReadAll`, and assert it equals the identity response's body byte-for-byte with `Equal`. This is the byte-parity proof over the real path; the fake-based header test in prompt 3 does not replace it.
4. **AC2 — the decoded body is the board's projection.** Unmarshal the identity response's body into `[]api.TaskResponse` and assert its task ids and statuses equal those of `h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: board.DefaultUpcomingHours})`. Compare the ids and statuses field by field, NOT the whole struct with `Equal`: `TaskResponse.Priority` is an `any` that the board fills with an `int` and `encoding/json` decodes back as a `float64`, so a whole-struct deep comparison cannot be equal even when the body is correct. Keep Priority at its zero value in this fixture — a non-zero priority round-trips as `float64` and would break the `Equal` assertion for a type reason, not a behavioural one.
5. **The held body is reused end to end.** Call `serve("gzip")` twice and assert the two bodies are byte-identical and the two responses carry the same headers. With the harness's fixed clock and no generation move, the second call is served from the cache.

Do not add a fake here — the point of this test is that it runs the real `board.New` and the real `handler.NewTasksHandler` together.

### 2. Document the body cache in `docs/page-index.md`

Add a new `## Task-list body cache` section (its own `##` heading, so `grep -n '^## '` lists it) directly after `## Task-list snapshot`. Do not change the wording or meaning of `## Task-list snapshot` or any other existing section. In the document's short, factual voice, cover:

- **What it holds.** One rendered `GET /api/tasks` response body per (task-list snapshot generation, query), in two forms: the identity JSON and its gzip-compressed form. Both are built once and reused.
- **What a hit costs.** A request whose query matches a held entry is served the stored bytes: no row walk, no projection and no marshal. A miss is built by the existing path and held.
- **What it is keyed by.** The task-list snapshot generation plus every query parameter that changes the body, including `upcoming_hours` and `session_live`.
- **When it is discarded.** A snapshot rebuild moves the generation and drops the superseded generation's bodies; the clock crossing the next instant at which `upcoming`, `recently_completed` or the recently-completed phase override can change drops the body, and that instant is derived from the request's clock reading and its upcoming-hours window rather than a timer. A discarded body is never served again.
- **What it does not change.** The decoded JSON is identical to what the board produced before; only how it is served changes. `GET /api/assignees` keeps reading the published rows and never touches the body cache.
- **The wire shape.** The gzip form is served with `Content-Encoding: gzip` when the request negotiates gzip; `Vary: Accept-Encoding` is set on every task-list response, so a shared cache cannot serve the wrong form.

Name the literal `Vary: Accept-Encoding` and `Content-Encoding: gzip` so the behaviour is greppable. Do not edit `README.md` or any other doc.

### 3. Record it in `CHANGELOG.md`

- If `## Unreleased` does not exist, add it directly above the highest `## vX.Y.Z` heading (`## v0.93.1`). If it already exists, append to it — do not create a second one.
- Add one `feat:` bullet describing the body cache: the board builds the `GET /api/tasks` response once per task-list snapshot generation and query and holds both the plain and gzip forms, serving a held body with no re-projection or re-marshal and dropping it when the snapshot rebuilds or the clock crosses the boundary that changes a clock-derived field; the gzip form is served when the client negotiates it, and the decoded JSON is unchanged.
- Follow `changelog-guide.md`: `- feat: <what> [context]`, one bullet per logical change, specific. Do not copy shell comments from this prompt's `<verification>` section. Do not use the prompt filename as the entry. Do not edit or move the `# Changelog` preamble or any released section.

### 4. Leave the live-board probe on the spec's ladder

Do NOT add an operator command, a `curl` to a running board, or a "run this on the host" note to this prompt's `<verification>` or `<constraints>`. The 10-sample same-window ratio probe and the `deploy_check` for `Content-Encoding: gzip` are the spec's Post-Deploy evidence and stay on the spec's Verification § *Operator-executable* rung. Nothing in this prompt runs against a live board.

### 5. Self-check

Before finishing, re-run every `<verification>` command. Walk Acceptance Criteria 2, 4 and 8 against the tests and the doc and name what establishes each. Confirm `docs/page-index.md` § *Task-list snapshot* is unchanged in content and that `grep -n '^## '` lists the new section.

</requirements>

<constraints>
- The decoded JSON is unchanged: same fields, same order, same values, same `SetEscapeHTML(false)` behaviour.
- `Vary: Accept-Encoding` is set on every `/api/tasks` response, gzip and identity alike; the gzip form carries `Content-Encoding: gzip`.
- `GET /api/assignees` keeps reading the published rows; the body cache is not reachable from the assignees path.
- No new middleware layer; `pkg/handler/router.go` keeps returning a bare router.
- No new timer, no configuration, no opt-out flag, no size limit, no time-to-live knob.
- The test uses `gzip.NewReader` only; `gzip.NewWriter` stays exactly once in `pkg/board`'s production code and nowhere under `pkg/handler`.
- Do NOT change the `Board` interface or any signature prompts 1–3 established.
- Do NOT restate or edit the invalidation model in `docs/page-index.md` § *Task-list snapshot*; add the new section beside it.
- Do NOT edit `README.md`.
- Do NOT modify `pkg/board/snapshot_internal_test.go`, `pkg/board/taskbody_internal_test.go`, `pkg/handler/api_test.go` or `pkg/handler/api_tasks_body_test.go`.
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
test -f pkg/board/tasks_body_http_test.go
```
Must exit 0.

```
grep -n 'NewTasksHandler' pkg/board/tasks_body_http_test.go
```
Must print at least one line.

```
grep -c 'gzip.NewWriter' pkg/board/taskbody.go
```
Must print `1`.

```
! grep -rn 'gzip.NewWriter' pkg/handler/ pkg/board/tasks_body_http_test.go
```
Must exit 0 (the end-to-end test decompresses only).

```
grep -n '^## ' docs/page-index.md
```
Must list `Task-list snapshot`, `Task-list body cache`, `Staleness bounds`, `Incremental updates`, `Frame ordering`, `Key derivation` and `Page index store`.

```
grep -n 'Vary: Accept-Encoding' docs/page-index.md
```
Must print at least one line.

```
grep -A3 '^## Unreleased' CHANGELOG.md
```
Must show a `feat:` bullet about the task-list body cache.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
