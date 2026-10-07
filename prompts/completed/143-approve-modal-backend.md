---
status: completed
summary: Routed the board's todo-to-planning phase move through vault-cli's approve operation with a request-path owner guard, exposed a task's open questions on the task API via a bumped vault-cli v0.163.0 and a new pageindex.ReadPage, and documented both.
execution_id: vault-ui-approve-modal-exec-143-approve-modal-backend
dark-factory-version: v0.196.0
created: "2026-10-07T05:48:53Z"
queued: "2026-10-07T05:48:53Z"
started: "2026-10-07T05:49:57Z"
completed: "2026-10-07T06:00:46Z"
branch: dark-factory/143-approve-modal-backend
---

# Route a todo-to-planning move through approve, and expose open questions on the task API

<summary>
- Moving a task card from the Todo column to Planning currently fails with a red banner, because the board writes the phase directly and the vault CLI refuses that one transition on purpose — the transition is the operator's approval and has its own command.
- The board already knows how to approve: the Start button does it before launching a session. This makes the drag path take the same route.
- A task can also carry open questions that must be answered before it is planned, so the board's API now exposes them alongside the task.
- No UI change yet; the task payload gains a key.
- Nothing else about a phase move changes: every other transition keeps the path it has today.
</summary>

<objective>
Make the board's phase route able to perform the operator's approval, and expose a task's open questions on the task API, so a UI can ask for approval and for answers to whatever the task is waiting on.
</objective>

<context>
Read `README.md` and `docs/optimistic-writes.md` for project conventions. (`CLAUDE.md` is gitignored and absent from a worktree.)

Read these before writing:

- `pkg/mutations/tasks.go` — find `RunTask`. Its `set.Approve.Execute(...)` call is the existing approval path the Start button uses; requirement 2 routes the phase move through the same operation. `OpSet.Approve` is already wired in `pkg/ops.go`.
- `pkg/mutations/tasks.go` — find `UpdateTaskPhase`. Its `write` closure is what requirement 2 changes. Note it already calls `set.Show` to read the task's current status.
- `pkg/mutations/tasks.go` — find `AssignTaskToMe`. It shows the request-path guard pattern for a task with no resolvable owner.
- `pkg/api/api.go` — `TaskResponse` is the task payload the board reads; requirement 3 adds a field to it. It currently imports nothing.
- `pkg/board/tasks.go` — where `TaskResponse` values are built. `Description` is set to `nil` there today, which is the shape a field the board does not populate takes.
- `pkg/board/board.go` — `board.Deps` and `OpsProvider`; requirement 3 extends the former.
- `pkg/factory/api.go` — `CreateAPIHandler` already receives a `pageIndex` and passes it to `CreateMutationService`; requirement 3 passes it to `board.New` too.
- `scripts/parity/parity.sh` — its `normalize()` deletes `jump_pane` before diffing the Go and Python `/api/tasks` bodies key-for-key; requirement 4 changes it.
- `docs/optimistic-writes.md` — the overlay / own-write-echo contract the phase route answers into. The route answers 202 before the vault is written; keep that.

`github.com/bborbe/vault-cli` is pinned in `go.mod` at v0.159.0, which predates the Open Questions support. Requirement 1 bumps it. That module is not in the **container's** module cache (`.dark-factory.yaml` mounts only `~/.cache/uv`), so the container must reach the Go proxy — a known precondition, not something to work around.
</context>

<requirements>
1. **Bump the vault CLI dependency.** Raise `github.com/bborbe/vault-cli` in `go.mod` to `v0.163.0` and run `go mod tidy`. That version exports `storage.ParseOpenQuestions(ctx, content) ([]storage.OpenQuestionItem, error)`, whose items carry `Index`, `Question`, `Answer`, `Marker` and `Line`.

   **The same bump widens `storage.PageStorage`, which this repo implements.** v0.161.0 added `ReadPage(ctx, vaultPath, pagesDir, name string) (*domain.Page, error)` to vault-cli's `storage.PageStorage`, and `pkg/pageindex.PageIndex` embeds that interface — so the bump does not compile until `*pageIndex` gains the method. Verified on a scratch copy of this repo with the `go.mod` line changed and `go mod tidy` run — the `go.sum` entry is needed to reach the first error at all, and the second appears only once the first is fixed:

   ```
   pkg/pageindex/pageindex.go:216:9: cannot use &pageIndex{…} (value of type *pageIndex) as PageIndex value in return statement: *pageIndex does not implement PageIndex (missing method ReadPage)
   pkg/pageindex/mocks/pageindex-page-index.go:598:29: cannot use new(PageIndex) … *PageIndex does not implement pageindex.PageIndex (missing method ReadPage)
   ```

   Implement `ReadPage` on `*pageIndex` in `pkg/pageindex/pageindex.go` to the interface's contract: `name` is a bare base name **without** the `.md` suffix (the value `Page.FileMetadata.Name` reports), it reads only that one file, and it returns an error when the file is missing or unparseable rather than skipping it. Serve it from the in-memory snapshot: iterate the snapshot for the page whose `FileMetadata.Name` matches (`snapshotHasName` in `pkg/pageindex/pageindex.go` is a *predicate* — it answers whether the name is present, not which page it is — so the lookup itself is new). On a miss, fall through to the existing `PageReader` seam (`pkg/pageindex/seams.go`) — **the two conventions differ: the seam's fourth argument is a filename *including* `.md` (`pkg/pageindex/reader.go` joins it and trims the suffix), so pass `name+".md"`** — and map the seam's excluded-file error to the interface's fail-don't-skip contract.

   Nothing in this repo calls `ReadPage`; it exists to satisfy the embedded `storage.PageStorage`. Keep it minimal and cover it per requirement 4.

   Then extend the counterfeiter fake at `pkg/pageindex/mocks/pageindex-page-index.go` so it also implements `ReadPage` (directive at `pkg/pageindex/pageindex.go`). Counterfeiter is not a module dependency, so if regeneration is unavailable, hand-add the `ReadPageStub` field plus `ReadPage`, `ReadPageCallCount`, `ReadPageArgsForCall` and `ReadPageReturns` in the same shape as the fake's existing `ListPages`.

   After this, `go build -buildvcs=false ./...` and `go vet ./...` both pass at v0.163.0, with no further breakage.

2. **Route the todo-to-planning move through approve.** In `pkg/mutations/tasks.go`, `UpdateTaskPhase`'s `write` closure currently sets `phase` with `FrontmatterSet` unconditionally. Before that call, read the task's current phase (`set.Show`, as the closure already does for status) and when the requested phase is `planning` **and** the current phase is `todo`, call `set.Approve.Execute(ctx, resolved.Path, taskID, resolved.Name, "operator", "", cfg.CurrentUser)` instead of the `phase` `FrontmatterSet` — the same seven arguments `RunTask` passes — resolving `cfg` via `s.deps.Config.Load(ctx)`.

   The approval writes `phase: planning`, `status: next`, `approved_by` and `approved_at` in one call. `status` is deliberately `next`, not `in_progress` — writing `in_progress` is the write that removes a freshly approved row from the ready-to-start bucket. So on the approve path skip **both** the `phase` write and the closure's `status` write; approve owns all four keys. Setting only the phase would race the approval's own write, and leaving the status logic running would overwrite `next` with `in_progress`.

   Every other transition — including `planning` requested on a task that is not at `todo` — keeps the existing `FrontmatterSet` path unchanged.

   **Owner resolution.** `Approve.Execute` refuses when neither the task's assignee nor the configured current user names an owner, and this closure runs on the write queue, so such a refusal surfaces as an asynchronous `write_failed` frame rather than a red banner. Check the owner on the request path and answer 400 with a clear message, the way `AssignTaskToMe` already does, so the operator is told before the request is accepted. Apply this guard **only** when the requested phase is `planning` and the task is currently at `todo` — reading the task on the request path to make that decision — so every other transition keeps today's behaviour, including on a vault with no `current_user` configured. A request-path read that *fails* (unknown task) must also leave today's behaviour untouched — 202 plus the queued write's `write_failed` frame, as `pkg/mutations/service_test.go` already asserts — so do **not** copy `AssignTaskToMe`'s request-path 404.

   **One refusal stays asynchronous, by design.** `taskApproveOperation.Execute` also refuses a `todo` task that already carries `approved_by`/`approved_at` (`refuseExistingApprovalRecord`). That case is not worth a request-path read; let it surface as the async `write_failed` frame and say so in the code comment.

3. **Expose a task's open questions on the API.** Add `OpenQuestions []domain.OpenQuestion` to `TaskResponse` with json tag `open_questions`, mapping each parsed item's `Question` to the domain type's `Text` and dropping `Marker` and `Line`. `domain.OpenQuestion` carries `Index` and `Text` only — an already-answered item's `Answer` is deliberately not exposed, and answered items are **included** rather than filtered out, because the section is the source of truth for what the task is waiting on and a UI is better placed to decide what an already-answered item means. State that in the README line rather than leaving it to be read as a bug. Add `github.com/bborbe/vault-cli/pkg/domain`; `pkg/api/api.go` currently imports nothing.

   `pkg/api/api.go`'s package doc asserts its JSON keys "must match the Python models in `src/vault_ui/api/models.py` exactly". Correct it: `open_questions` is deliberately Go-only, and `normalize()` strips it from the parity comparison.

   Initialise the slice to non-nil so the field serialises as `[]`, never `null`, and a task with no such section still carries the key.

   **The board does not hold task content today.** `pkg/board/tasks.go` builds `TaskResponse` from `ops.TaskListItem`, which carries derived fields only — no content — and neither `board.Deps` nor `OpsProvider` exposes a page. So add `PageIndex pageindex.PageIndex` to `board.Deps`, pass the `pageIndex` that `CreateAPIHandler` already has in scope to `board.New` as well as to `CreateMutationService`, and per vault call `PageIndex.ListPages(ctx, vault.Path, vault.TasksFolder)` **once**, indexing the result by `page.FileMetadata.Name` (the promoted `page.Name` is the same value, but match `snapshotHasName`'s explicit form); a page's raw text for `ParseOpenQuestions` is `page.Content.String()`. **One `ListPages` call per vault — never one per task**, which would put a vault walk on the board's hottest path. This is a second listing of the same snapshot `opsProvider.List` already takes (`pkg/factory/api.go` builds it as `ops.NewListOperation(p.pageIndex)`), so it costs no extra I/O — say that in the code comment rather than implying the board reads pages for the first time.

   A parse error is not fatal to a board read: log it and carry an empty list rather than failing the whole task list.

4. **Tests.**
   - Ginkgo/Gomega, matching the suites already in `pkg/board/` and `pkg/mutations/`, and hold new code to the project's coverage bar (`docs/dod.md`).
   - The new `pageIndex.ReadPage` needs its own tests: a snapshot hit returns that page; a snapshot miss falls through to the `PageReader` seam, **asserting the seam received `name+".md"`**; a seam miss returns an error rather than a skipped page; and an empty or path-separator name is refused.
   - Adding a required `PageIndex` to `board.Deps` breaks the existing board tests, which build `Deps` without it — `pkg/board/board_test.go` has three `board.New(board.Deps{...})` call sites (the harness's `build()` plus two inline ones), and each needs a fake `PageIndex`, or the board must nil-guard it. Update every site rather than leaving the field optional.
   - A `planning` request against a `todo` task with an empty `assignee` and no configured `current_user` answers 400 on the request path and enqueues no write.
   - A `planning` request against a `todo` task records `phase: planning`, `approved_by`, `approved_at` **and `status: next`** — asserting `next` specifically, since `in_progress` is the failure this guards.
   - A `planning` request against a task already past `todo` does **not** call approve and takes the old path.
   - The other transitions (`execution`, `ai_review`, `done`) are untouched.
   - A JSON contract test for the new field: `[]` for a task with no such section, items in section order for one that has them. **Drive it through the real parser** — feed the fake `PageIndex.ListPages` a `*domain.Page` whose `Content` is actual markdown carrying the section, so `storage.ParseOpenQuestions` runs and the `item.Question → Text` mapping and section order are proven against the library. A hand-built `[]domain.OpenQuestion` asserts only the shape the test itself typed.
   - Update `scripts/parity/parity.sh`'s `normalize()` to delete `open_questions` from both sides, exactly as it already deletes `jump_pane`, with a comment naming this change as the reason. The harness compares the Go body against the frozen Python backend key-for-key, so a Go-only key makes every task case red, and the Python reference cannot be updated — it is superseded.

5. **Document the new field.** `README.md`'s `### GET /api/tasks` section has a query-parameter table plus a `session_state` paragraph but no response-field list — follow the `session_state` paragraph's form when adding `open_questions`.

   `CHANGELOG.md` has **no `## Unreleased` section** today; its top section is `## v0.84.1`. Create `## Unreleased` above it and add exactly one bullet there, matching the existing prefix convention (`- feat: …`).

6. **Self-check before finishing.** Re-run `<verification>` and confirm it passes. Then walk requirements 1–5 against the change one at a time and report any you could not satisfy rather than silently dropping it.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT change the Start button's behaviour; it already approves and must keep doing so.
- Do NOT change any other phase transition, and do NOT make the approve path reachable from anything but a `planning` request against a `todo` task.
- Do NOT change the plain (non-JSON) output of any command; the new field is additive and JSON-only.
- Do NOT create a tag or edit a version string — the version is derived from git tags (hatch-vcs) and the release agent owns the tag after merge.
- Do NOT edit any `CHANGELOG.md` section other than adding the one bullet in requirement 5.
- Use repo-relative paths only.
</constraints>

<verification>
Run each; record the output verbatim in the report.

- `go build -buildvcs=false ./...` → exit 0
- `go list -m github.com/bborbe/vault-cli` → prints `v0.163.0`
- `grep -c 'Approve.Execute' pkg/mutations/tasks.go` → at least 1, **and read `UpdateTaskPhase`'s `write` closure to confirm the `planning`-at-`todo` branch reaches it** (sharing one helper between `RunTask` and the closure is a correct implementation and leaves the count at 1 — the count is a shape probe, the read is the check)
- `grep -c 'json:"open_questions"' pkg/api/api.go` → exactly 1
- `grep -c 'open_questions' scripts/parity/parity.sh` → at least 1, **and read `normalize()` to confirm the key reaches jq's `del(...)`** — the count alone cannot distinguish the filter from a comment, and the correct single-call form `del(.jump_pane, .open_questions)` matches no `del(.open_questions)` substring
- `make parity` → exit 0 (the repo's only lock on Go-vs-Python read parity, and it is **not** part of `make precommit`; it builds with `GOFLAGS=-buildvcs=false`, so it is hideGit-safe)
- `make precommit` → exit 0

`hideGit: true` is set for this project, so `/workspace/.git` is an empty tmpfs and Go's VCS stamping fails with `error obtaining VCS status: exit status 128`. Every `go build` here needs `-buildvcs=false`, exactly as `Makefile`'s `build` target and `main_test.go` already do. `ROOTDIR` is not read by this repo's Makefile; passing it changes nothing.

If `make precommit` fails, STOP and report `"status":"failed"` with the exact failing command and its output. Do not attempt a partial fix of unrelated pre-existing failures — report them and stop.
</verification>
