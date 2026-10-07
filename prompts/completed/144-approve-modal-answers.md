---
status: completed
summary: Wired vault-cli's TaskAnswerOperation into the board's OpSet and carried optional answers through PATCH /api/tasks/{id}/phase, writing them before the todo→planning approval, validating their shape with 422 and refusing answers any other move would discard with 400.
execution_id: vault-ui-approve-modal-exec-144-approve-modal-answers
dark-factory-version: v0.196.0
created: "2026-10-07T09:42:10Z"
queued: "2026-10-07T09:42:10Z"
started: "2026-10-07T09:42:37Z"
completed: "2026-10-07T09:49:39Z"
pr-url: https://github.com/bborbe/vault-ui/pull/146
branch: dark-factory/144-approve-modal-answers
---

# Carry the Approve modal's answers through the phase route

<summary>
- The board's todo-to-planning move now approves the task, but an approval can carry answers to the task's open questions and nothing writes them yet.
- The vault CLI already has the operation that records answers; this wires it into the board's own op set and lets the phase request carry them.
- Answers are written before the approval, so a failure leaves the task unapproved with its answers recorded rather than approved with the questions unanswered.
- No UI change; the request body gains one optional key.
- Nothing else about a phase move changes.
</summary>

<objective>
Give the board a way to record an operator's answers to a task's open questions as part of the same todo-to-planning move that approves it.
</objective>

<context>
Read `README.md` and `docs/optimistic-writes.md` for project conventions. (`CLAUDE.md` is gitignored and absent from a worktree.)

Read these before writing:

- `pkg/ops.go` — `OpSet`. `Approve ops.TaskApproveOperation` is at line 18; requirement 1 adds a sibling field. `pkg/factory/factory.go:72` is where `Approve` is constructed; the new op is constructed the same way, from the same `taskStore` (in scope at `:61`).
- `pkg/mutations/tasks.go` — find `UpdateTaskPhase` (~line 446) and its `write` closure (~459), then `approveTask` (~510) and `approveOwnerGuard` (~538) below it. `approveTask` is the helper requirement 2 extends; it already loads the config and calls `set.Approve.Execute` with the seven arguments the Start path passes. Its import block (`:7-19`) has **no** `github.com/bborbe/vault-cli/pkg/domain`, which requirement 2's new parameter needs.
- `pkg/api/mutations.go` — `UpdatePhaseRequest` (line 15). It carries `Phase`, `Reason` and `GateSuccessor`; requirement 2 adds the answers. **This file has no import block today** — it is `package api` followed directly by types — so it must add its own `import "github.com/bborbe/vault-cli/pkg/domain"`. Go imports are per-file; `pkg/api/api.go:17`'s existing `domain` import does not cover it.
- `pkg/mutations/service_test.go` — the two multi-operation `OpSet` literals (`opsFactory` at 237 and `opsFactoryWithStarter` at 270, whose `Approve:` fields are at 243 and 276) are where requirement 1 adds the new op. `service_test.go:1416` is a partial literal and needs no change. `pkg/handler/queued_writes_test.go:198` is also a partial literal, but requirement 4 extends it — see below.
- `pkg/handler/api_mutations.go` — `decodeBody` (~37) and `writeStatusEnumError` (~101). This is where the repo validates request bodies; the 422 writer itself is `writeValidation` in `pkg/handler/api_common.go`.
- `pkg/handler/queued_writes_test.go` — `newQueuedFixture` (~103; its `Show` stub, which requirement 4 changes, is at 133–141) and its `OpSet` literal (~198). Requirement 4 extends this fixture; read it before writing that test, because as it stands it cannot host the case.
- `docs/optimistic-writes.md` — the request-path / write-queue split this change follows. The route answers 202 before the vault is written. Note it distinguishes **body validation (422, the FastAPI shape)** from the phase route's **semantic guard (400)**, and that its request-path list enumerates the synchronous checks — requirement 5 extends that list.
- `docs/dod.md` — the coverage bar: new behaviour needs tests that cover it.

`github.com/bborbe/vault-cli` is already pinned at v0.163.0, which is the version this needs — **do not change `go.mod`**.

The operation already exists and is exported. Read its own doc comment at `github.com/bborbe/vault-cli/pkg/ops/task_answer.go` (in the container, under `$(go env GOMODCACHE)/github.com/bborbe/vault-cli@v0.163.0/` — it is not a path in this repo, which has only `pkg/ops.go`) rather than working from a paraphrase here:

```
TaskAnswerOperation.Execute(
    ctx context.Context, vaultPath, taskName, vaultName string,
    answers []domain.OpenAnswer,
) (MutationResult, error)

func NewTaskAnswerOperation(taskStorage storage.TaskStorage) TaskAnswerOperation

type domain.OpenAnswer struct {
    Index  int    `json:"index"`
    Answer string `json:"answer"`
}
```

Three of its properties matter below: answering the same item again **replaces** the previous answer rather than adding a second one, so re-running is safe; it refuses an index naming no item, and any answer for a task with no such section, **with nothing written**; and it refuses an answer containing a line break, the ` → **` delimiter, or `**`. `Approve.Execute` re-reads the task from disk (`FindTaskByName`) before it writes, so answers written first survive the approval write.
</context>

<requirements>
1. **Make the answer operation available to the board.** Add `Answer ops.TaskAnswerOperation` to `OpSet` in `pkg/ops.go`, and construct it in `pkg/factory/factory.go` beside `Approve` as `ops.NewTaskAnswerOperation(taskStore)`.

   Extend the two multi-operation `OpSet` literals in `pkg/mutations/service_test.go` (starting at lines 237 and 270) the same way. Omitting it still compiles — a Go composite literal need not set every field — so the reason is not the build: it is that requirement 4's answers test would panic on a nil `set.Answer` interface when it reaches `approveTask`.

   Also, in `pkg/factory/ops_test.go`'s `It("populates every operation")` case, add `Expect(set.Answer).NotTo(BeNil())` — and while there add `Expect(set.Approve).NotTo(BeNil())`, which that case has been missing: it asserts 15 fields and `Approve` is the only one left out.

2. **Let the phase request carry answers, and write them before the approval.** Add `Answers []domain.OpenAnswer` to `UpdatePhaseRequest` in `pkg/api/mutations.go` with json tag `answers` — **no `omitempty`**, matching the file's other fields; a missing or `null` key decodes to a nil slice either way, and `len(req.Answers) == 0` is what keeps today's behaviour. Add `github.com/bborbe/vault-cli/pkg/domain` to that file's new import block and to `pkg/mutations/tasks.go`'s existing one.

   Thread the answers into `approveTask` — it takes no `req` today, so add an `answers []domain.OpenAnswer` parameter — and, when `answers` is non-empty, call `set.Answer.Execute(ctx, resolved.Path, taskID, resolved.Name, answers)` **before** `set.Approve.Execute`. On an answer error, return without calling `Approve`.

   Answers first is deliberate: if the approval then fails, the task stays at `todo` with its answers recorded — recoverable by dragging again, because the operation replaces an existing answer rather than duplicating it — whereas approving first and failing to write the answers would leave a task approved with its questions unanswered and nothing prompting a retry. Say that in the comment.

   An empty or absent `Answers` keeps today's behaviour exactly: approve only, no answer write.

3. **Reject any request that carries answers it cannot record.**
   - **Shape — a malformed answer.** Validate in the **handler**, next to `decodeBody`, answering the FastAPI **422** shape that `writeValidation` in `pkg/handler/api_common.go` produces and that `pkg/handler/queued_writes_test.go` already asserts. Do **not** answer 400 for this — `docs/optimistic-writes.md` reserves 400 for the route's semantic guard and puts body validation in the 422 bucket, so a 400 here would be a category error. Check from the body alone the things the operation refuses only asynchronously, after a 202: a non-positive index, a duplicate index, and an answer containing a line break (`\n` or `\r`), the ` → **` delimiter, or `**`. Also reject an empty or whitespace-only answer here: the operation does **not** refuse one (it would write `→ ****`), so this check is a deliberate addition, not a mirror of the operation. All of those are checkable from the body alone, and leaving any of them to the operation means a plausible input — a pasted multi-line answer, markdown bold — returns 202 and then fails asynchronously while, because requirement 2 returns before `Approve.Execute`, the approval silently does not happen. Name the offending index in the message.
   - **Semantics — answers on a request that cannot record them.** Only the todo → planning move writes answers, so any request carrying them that does not meet that condition (a `planning` request against a task not at `todo`, **and any non-`planning` phase**) takes the old `FrontmatterSet` path and drops them. Answering 202 would tell the operator their answers were accepted when they were discarded, so answer **400** with a message saying so whenever `len(req.Answers) > 0` and the todo → planning condition does not hold — not only for `planning`. Thread `req.Answers` into the existing request-path guard (`approveOwnerGuard`) rather than adding a second task read. It has two early returns: `if phase != "planning" { return nil }` (~543), which fires first and never reads the task, and `if showErr != nil || task.Phase != "todo" { return nil }` (~549). Insert the answers branch before **both**: before ~543 add `if len(answers) > 0 && phase != "planning" { return 400 }` (no task read needed), and at ~549 split the combined condition into `if showErr != nil { return nil }` followed by `if len(answers) > 0 && task.Phase != "todo" { return 400 }` (preserving today's 202 + async `write_failed` for an unknown task). Without the first insertion a non-`planning` request carrying answers returns nil at ~543 and 202s, silently dropping them.
   - **An index that names no real item** stays the operation's own refusal. The guard's own read already carries the task's parsed questions, but keeping the bound in the operation avoids a second source of truth for it, so let it surface as an asynchronous `write_failed` frame and say so in the comment.

4. **Tests.** Ginkgo/Gomega, matching the suites in `pkg/mutations/` and `pkg/handler/`, and hold new code to the project's coverage bar (`docs/dod.md`).
   - **A handler-level test that decodes a real JSON body.** The tests in `pkg/mutations/` build the Go struct directly and so never prove the `answers` tag decodes; `pkg/handler/queued_writes_test.go` already PATCHes real bodies to this exact route, so add the case there — but the fixture needs three additions first, or the case cannot pass:
     - give `queuedFixture` a `showPhase string` field and return it from `show.ExecuteStub` (`ops.TaskDetail{Name: taskName, Phase: f.showPhase, ClaudeSessionID: f.showSessionID}`), because that stub hardcodes the phase to `""` and a `planning` request with answers against a non-`todo` task now answers 400;
     - wire `Answer` and `Approve` into the fixture's `OpSet` (`vcmocks.TaskAnswerOperation` and `vcmocks.TaskApproveOperation`; both fakes exist in vault-cli v0.163.0), because the literal wires neither and `approveTask` would panic on a nil `set.Answer`;
     - give the `Answer` fake an `ExecuteStub` that records through `f.apply` so the answer appears in `f.records()` — its signature takes `[]domain.OpenAnswer`, so add `"github.com/bborbe/vault-cli/pkg/domain"` to the file's imports.
     Then set `f.showPhase = "todo"` and PATCH `{"phase":"planning","answers":[{"index":1,"answer":"yes"}]}` to `/api/tasks/{id}/phase?vault=personal`, asserting 202 and that the queued write carries the answer. Without it a mistyped tag would silently drop every answer while all other tests stayed green.

     Cheaper alternative if you would rather not touch the fixture: assert **422** for `{"phase":"planning","answers":[{"index":0,"answer":"yes"}]}` against the untouched fixture. A mistyped tag leaves `Answers` nil, the shape check never fires, and the request returns 202 — so a 422 is a real decode proof. It does not prove the write path, so keep the fixture-extended case as the primary.
   - A `planning` request against a `todo` task **with** answers records both: the answers appear against their own questions in the task file **and** the task reads `phase: planning`, `status: next`, `approved_by` and `approved_at`. Note `h.writeTodoTask` (~1480) writes no body at all, so it produces a task with no `Open Questions` section and the operation would refuse index 1 — this test needs a task file written with a real `## Open Questions` heading and list items, e.g. via `writeFile`/`h.dir` rather than `writeTodoTask`.
   - **Assert the call order** — a spy on `Answer` and `Approve` pins the invariant requirement 2 argues for in prose. Build it on the `newService` pattern in the `SetTaskSession` describe (`service_test.go:~1404-1426`) — write your own closure in the same shape rather than editing `newService`; `newHarnessWith` uses the real `opsFactory` and will not give you the spy.
   - A `planning` request against a `todo` task with **no** answers still approves and writes no answer.
   - A non-positive index, a blank answer text, a duplicate index, and an answer containing a newline each answer **422** on the request path and enqueue no write.
   - A request carrying answers for a task **not** at `todo`, and one carrying answers on a **non-`planning`** phase, each answer **400** and enqueue no write.
   - Every other transition, carrying no answers, still takes the old path and writes no answer.

5. **Document the new key.** `README.md` has **no** `PATCH /api/tasks/{id}/phase` section today — its API section documents only the `GET` routes. Add a new section titled ``### `PATCH /api/tasks/{id}/phase` `` documenting the request body (`phase`, `reason`, `gate_successor`, and the new `answers`), describing `answers` as an optional array of `{index, answer}` where `index` is the 1-based position of the item in the task's `Open Questions` section. Keep it to the shape of the existing `session_state` paragraph rather than inventing a table, and backtick the route in the heading as the file's other route headings do. Keep the `{id}` spelling exactly as written — it matches `docs/optimistic-writes.md`, and `{task_id}` would not.

   Note in that section that `answers` is a **Go-only request field**, like `open_questions` is a Go-only response field — the superseded Python backend never emits it and ignores it on the way in (`UpdatePhaseRequest` in `src/vault_ui/api/tasks.py:396` sets no `extra="forbid"`) — and that it needs no parity normalization because it is request-only.

   Extend `docs/optimistic-writes.md`'s request-path list to name the new 400 alongside the owner check it already records, and add the new malformed-answers 422 to that sentence's parenthetical list of body validations, since that list enumerates the synchronous checks and this change adds one of each.

   `CHANGELOG.md`'s released sections start at `## v0.85.0`, and a `## Unreleased` may or may not sit above it depending on the branch you start from — so check before writing: if `## Unreleased` exists, add exactly one bullet under it; if it does not, create it above the first `## v…` heading and add the bullet there. Either way, exactly one bullet, matching the existing prefix convention (`- feat: …`), and never a second `## Unreleased`.

6. **Self-check before finishing.** Re-run `<verification>` and confirm it passes. Then walk requirements 1–5 against the change one at a time and report any you could not satisfy rather than silently dropping it.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT change the Start button's behaviour, the approve path's own semantics, or any other phase transition.
- Do NOT change the plain (non-JSON) output of any command; the new key is additive and JSON-only.
- Do NOT change `go.mod` — v0.163.0 is already the version this needs.
- Do NOT create a tag or edit a version string — the version is derived from git tags and the release agent owns the tag after merge.
- Do NOT edit any `CHANGELOG.md` section other than adding the one bullet in requirement 5.
- Do NOT write answers for any transition other than todo → planning.
- Use repo-relative paths only.
</constraints>

<verification>
Run each; record the output verbatim in the report.

- `go build -buildvcs=false ./...` → exit 0
- `grep -c 'Answer' pkg/ops.go` → at least 1, **and read `OpSet` to confirm the field is `ops.TaskAnswerOperation`**
- `grep -c 'json:"answers' pkg/api/mutations.go` → exactly 1
- `grep -c 'set.Answer.Execute' pkg/mutations/tasks.go` → exactly 1
- `grep -n 'NewTaskAnswerOperation' pkg/factory/factory.go pkg/mutations/service_test.go` → three lines (factory plus both multi-operation literals)
- `grep -c 'set.Answer' pkg/factory/ops_test.go` → at least 1
- `grep -c 'PATCH /api/tasks/{id}/phase' README.md` → at least 1, **and read the section to confirm it names `answers`**
- `make precommit` → exit 0 (**run this FIRST** — its `sync` target is what gives the container a Linux `.venv`)
- `make parity` → exit 0 (the read-parity lock; it builds with `GOFLAGS=-buildvcs=false`, so it is hideGit-safe). It boots the Python backend from `./.venv/bin/python`, and the mounted `.venv` points at a host macOS interpreter, so it only works after `make precommit`. A non-zero exit from `build_vault_cli` (`set -euo pipefail`, so a failed `go mod init`/`go get`/`go build`) or a missing `./.venv/bin/python` is an environment failure, not a pass.

`hideGit: true` is set for this project, so `/workspace/.git` is an empty tmpfs and Go's VCS stamping fails with `error obtaining VCS status: exit status 128`. Every `go build` here needs `-buildvcs=false`, exactly as `Makefile`'s `build` target and `main_test.go` already do. `ROOTDIR` is not read by this repo's Makefile; passing it changes nothing.

If `make precommit` fails, STOP and report `"status":"failed"` with the exact failing command and its output. Do not attempt a partial fix of unrelated pre-existing failures — report them and stop.
</verification>
