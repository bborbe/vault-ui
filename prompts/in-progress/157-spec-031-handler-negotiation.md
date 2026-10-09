---
status: approved
spec: [031-task-list-body-cache]
created: "2026-10-09T20:51:14Z"
queued: "2026-10-09T21:18:12Z"
branch: dark-factory/task-list-body-cache
---

# Serve the stored task-list bytes and negotiate Content-Encoding

<summary>
- The task-list route writes the body the board holds instead of marshalling the rows itself.
- A client that negotiates gzip is sent the compressed form with the right encoding header.
- A client that does not is sent the plain form with no content-encoding header.
- Every task-list response carries the vary header, so a shared cache keys on the negotiation and never serves the wrong form.
- Only the single gzip token is honoured; any other encoding token, an absent header or an unparseable value falls to the plain form.
- The validation and error responses of the route are unchanged.
- The assignees route and its tests are untouched.
- No middleware is added; the negotiation lives at the handler's write seam.
</summary>

<objective>
Wire the task-list handler to the body the board holds and make it choose between the plain and gzip forms from the request's `Accept-Encoding`, setting `Content-Encoding` and `Vary` at the handler's write seam. This covers the spec's Suggested Decomposition row 3 (Desired Behaviors 4 and 7; Acceptance Criteria 3 and 7). It runs after prompt 1, which added the board's held-body read.
</objective>

<context>
Read the spec `specs/in-progress/031-task-list-body-cache.md` in full, especially Desired Behaviors 4 and 7, the Security / Abuse Cases section, the Failure Modes row for an absent or unparseable `Accept-Encoding`, and Acceptance Criteria 3, 4 and 7.

Read the current code in full before changing anything:
- `pkg/handler/api_tasks.go` — `NewTasksHandler`, `first`. It parses the query, calls `b.ListTasks`, and writes with `writeJSON`.
- `pkg/handler/api_common.go` — `writeJSON`, `writeValidation`, `writeBoardError`, `parseUpcomingHours`, `parseBool`, `validationItem`.
- `pkg/handler/api_vaults.go` — `NewAssigneesHandler`. It must not change.
- `pkg/handler/router.go` — `CreateHTTPRouter` registers `NewTasksHandler(b)` at `/api/tasks` and `NewAssigneesHandler(b)` at `/api/assignees`. It must not change.
- `pkg/handler/api_test.go` — `fakeBoard` (prompt 1 added `taskBody` and `ListTasksBody`), `newRouter`, `doGet`, and the "serves the read routes" table whose `tasks` entry asserts the body equals `[]`.
- `pkg/board/board.go` — `Board`, `TaskQuery`, `ListTasksBody`.
- `pkg/board/taskbody.go` — `TaskListBody` (prompt 1).

Read `docs/page-index.md` § *Task-list snapshot* for the invalidation model this cache builds on. Note the handler package predates `go-http-handler-refactoring-guide.md` and deliberately uses plain `http.Handler` + `writeJSON`/`writeBoardError` for FastAPI wire parity — mirror `api_vaults.go`, do not convert to `libhttp.WithError`.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-handler-refactoring-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-json-error-handler-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`

`.dark-factory.yaml` sets `hideGit: true`, so the container's `.git` is masked. Never rely on a `git` command in this prompt. All verification below is non-git.

OPEN QUESTION for the reviewer (spec conflict). The spec contradicts itself on `Vary`:
- Desired Behavior 4 and Acceptance Criterion 3 say the identity response carries "no `Content-Encoding` and no `Vary`" / "receives neither header".
- The Security / Abuse Cases section says "`Vary: Accept-Encoding` is required on every `/api/tasks` response, including the identity one, so a shared cache cannot serve the wrong form for a client's negotiation."

The requirement below follows the Security section — `Vary: Accept-Encoding` on every `/api/tasks` response, gzip and identity alike — because serving a gzip form chosen from `Accept-Encoding` without `Vary` on the identity response is the cache-poisoning bug that section exists to prevent. This means the test asserts `Vary` on both responses, not "neither header". The auditor should confirm this resolution; if the Acceptance Criterion is to win instead, the only change is to set `Vary` on the gzip response only.
</context>

<requirements>

### 1. Wire the handler to the held body

In `pkg/handler/api_tasks.go`, replace the `b.ListTasks` call and the `writeJSON` call with the body path. Keep the query parsing, the `parseUpcomingHours` validation and the `writeValidation` branch exactly as they are:

```go
// NewTasksHandler returns the GET /api/tasks handler.
func NewTasksHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		query := req.URL.Query()
		hours, invalid := parseUpcomingHours(query)
		if invalid != nil {
			writeValidation(resp, []validationItem{*invalid})
			return
		}
		body, err := b.ListTasksBody(req.Context(), board.TaskQuery{
			Vaults:        query["vault"],
			Statuses:      query["status"],
			Phases:        query["phase"],
			Assignees:     query["assignee"],
			Goals:         query["goal"],
			UpcomingHours: hours,
			SessionLive:   parseBool(first(query["session_live"])),
		})
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		writeTaskBody(resp, req.Header.Get("Accept-Encoding"), body)
	})
}
```

Add the import `"strings"` to this file. Do NOT change `first`.

### 2. The write seam

Add to `pkg/handler/api_tasks.go`:

```go
// writeTaskBody serves a rendered task-list body, choosing the gzip form when
// the request negotiates it. Vary is set on every response — gzip and identity
// alike — so a shared cache keys on the negotiation and never serves the wrong
// form.
func writeTaskBody(resp http.ResponseWriter, acceptEncoding string, body board.TaskListBody) {
	resp.Header().Set("Content-Type", "application/json")
	resp.Header().Set("Vary", "Accept-Encoding")
	if acceptsGzip(acceptEncoding) {
		resp.Header().Set("Content-Encoding", "gzip")
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write(body.Gzipped)
		return
	}
	resp.WriteHeader(http.StatusOK)
	_, _ = resp.Write(body.Identity)
}
```

Set every header before `WriteHeader`, so the header set is complete when the status is written.

### 3. The negotiation

Add to `pkg/handler/api_tasks.go`:

```go
// acceptsGzip reports whether the Accept-Encoding header names the gzip token.
// Only that single token is honoured; any other encoding token, an absent header
// or an unparseable value falls to the identity form. Query values (`;q=`) are
// not parsed.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		token := strings.TrimSpace(part)
		if i := strings.IndexByte(token, ';'); i >= 0 {
			token = strings.TrimSpace(token[:i])
		}
		if strings.EqualFold(token, "gzip") {
			return true
		}
	}
	return false
}
```

No other encoding token is honoured, and no request-supplied value reaches a compressor — the handler only selects between two bodies the board already built. The server never decompresses a request body.

### 4. Do not touch the assignees path

- Do NOT modify `pkg/handler/api_vaults.go`, `pkg/board/vaults.go`, or any assignees test file.
- `/api/assignees` keeps calling `b.ListAssignees`, which keeps reading `b.snapshot.List`. The body cache must not be reachable from the assignees path.
- Do NOT add middleware, and do NOT change `pkg/handler/router.go`.

### 5. Tests (`pkg/handler/api_tasks_body_test.go`, package `handler_test`)

Ginkgo/Gomega, external package `handler_test` (mirror `api_test.go`). Use `newRouter`, `doGet` and `fakeBoard`. Build the request with the header set:

```go
recorder := httptest.NewRecorder()
req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
req.Header.Set("Accept-Encoding", "gzip")
router.ServeHTTP(recorder, req)
```

Set `fake.taskBody = board.TaskListBody{Identity: []byte("identity-form"), Gzipped: []byte("gzip-form")}` in the relevant specs so the two forms are distinguishable. Cover:

1. **AC3 — gzip negotiated.** `Accept-Encoding: gzip` → status 200, `Content-Encoding` equals `gzip`, `Vary` equals `Accept-Encoding`, body equals `gzip-form`.
2. **AC3 — identity when none.** No `Accept-Encoding` header → status 200, `Content-Encoding` is absent, `Vary` equals `Accept-Encoding`, body equals `identity-form`.
3. **Another token is not honoured.** `Accept-Encoding: deflate` → no `Content-Encoding`, body equals `identity-form`.
4. **A list containing gzip is honoured.** `Accept-Encoding: deflate, gzip` → `Content-Encoding` equals `gzip`, body equals `gzip-form`.
5. **The validation and error paths are unchanged.** The existing `upcoming_hours` 422 cases and the board-error 500 case still pass (they live in `api_test.go`); add nothing that duplicates them.

The existing "serves the read routes" table's `tasks` entry asserts the body equals `[]`. `fakeBoard`'s default `taskBody` (prompt 1) is `{Identity: []byte("[]"), Gzipped: []byte("[]")}`, so that assertion keeps passing unchanged. Do NOT weaken it.

### 6. Coverage and self-check

`pkg/handler` must keep `>= 80 %` statement coverage. Before finishing, re-run every `<verification>` command. Walk Desired Behaviors 4 and 7 and Acceptance Criteria 3 and 7 against the tests and name what establishes each. Confirm `pkg/handler/api_vaults.go`, `pkg/handler/router.go` and `pkg/board/vaults.go` are byte-for-byte unchanged.

Acceptance Criterion 4 — the byte-for-byte "the gzipped form decodes to the identity form" proof over a real board — runs in prompt 4 (the end-to-end test through the real handler and board). This prompt proves header negotiation and form selection with the fake, which is the handler's contract.

</requirements>

<constraints>
- The decoded JSON is unchanged: same fields, same order, same values, same `SetEscapeHTML(false)` behaviour. The handler writes the board's bytes verbatim.
- `Vary: Accept-Encoding` is set on every `/api/tasks` response, gzip and identity alike.
- Only the single `gzip` token is honoured. No other encoding token is honoured, and no request-supplied value reaches a compressor.
- No new middleware layer; `pkg/handler/router.go` keeps returning a bare router and the negotiation lives at the handler's write seam.
- `/api/assignees`, `pkg/handler/api_vaults.go` and `pkg/board/vaults.go` are untouched; the body cache is not reachable from the assignees path.
- The route's `upcoming_hours` validation and its FastAPI-shaped 422 body are unchanged, and a board error still maps through `writeBoardError`.
- Do NOT add `compress/gzip` to `pkg/handler`; the fake returns bytes, it does not compress. `gzip.NewWriter` stays in `pkg/board`.
- Do NOT touch `pkg/board/taskbody.go`'s cache, `docs/` or `CHANGELOG.md`. The `docs/page-index.md` section and the `CHANGELOG.md` entry are prompt 4's job; this prompt's DoD CHANGELOG line is satisfied there — do not add one here.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`. No ignored error returns.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
export PATH=/usr/local/go/bin:$PATH
go test -race ./pkg/handler/... ./pkg/board/...
```
Must pass.

```
export PATH=/usr/local/go/bin:$PATH
go test -race -coverprofile=/tmp/handler.cover ./pkg/handler/ && go tool cover -func=/tmp/handler.cover | awk '/^total:/ { sub("%", "", $3); print "coverage " $3 "%"; if ($3 + 0 < 80) exit 1 }'
```
Must print the coverage and exit 0 (>= 80 %).

```
test -f pkg/handler/api_tasks_body_test.go
```
Must exit 0.

```
grep -n 'ListTasksBody' pkg/handler/api_tasks.go
```
Must print one line.

```
grep -n 'Vary' pkg/handler/api_tasks.go
```
Must print at least one line.

```
grep -n 'Content-Encoding' pkg/handler/api_tasks.go
```
Must print at least one line.

```
! grep -rn 'compress/gzip\|gzip\.NewWriter' pkg/handler/
```
Must exit 0 (no gzip writer in the handler package).

```
! grep -rn 'ListTasksBody\|gzip\|Vary\|Content-Encoding' pkg/handler/api_vaults.go pkg/board/vaults.go
```
Must exit 0 (the assignees path is untouched).

```
! grep -n 'ListTasksBody\|gzip\|Vary\|Content-Encoding\|Middleware\|middleware' pkg/handler/router.go
```
Must exit 0 (no negotiation or middleware leaked into the router).

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
