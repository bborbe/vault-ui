---
status: completed
spec: [035-bug-goal-card-ignores-manager-session]
summary: Bumped vault-cli to v0.171.0, proved ops.List populates ManagerSessionID, and restored the status:next approval contract in both callers
execution_id: vault-ui-goal-manager-liveness-exec-167-spec-035-bump-vault-cli-manager-session-id
dark-factory-version: v0.198.7
created: "2026-10-19T16:56:40Z"
queued: "2026-10-19T17:31:58Z"
started: "2026-10-19T17:32:55Z"
completed: "2026-10-19T17:36:34Z"
branch: dark-factory/167-spec-035-bump-vault-cli-manager-session-id
---

# Bump vault-cli to a release whose list read model populates `ManagerSessionID`

<summary>
- The board's vault-cli dependency is moved to a release that exposes the manager session id on the goal list read model.
- The release is discovered from the Go module proxy, not hardcoded: the prompt walks the published versions after `v0.170.2` and takes the first one that carries the field.
- A new test proves the read model actually populates the field from a goal page, so a release that only declares the struct field cannot pass.
- If no published release carries the field, the prompt stops and reports failure instead of faking the dependency.
- No board classification behavior changes in this prompt; it establishes the dependency precondition and restores the approval status the bump would otherwise drop.
- Existing tests and `make precommit` continue to pass.
- The deployed verification and the launchd restart remain on the spec's operator rung, not in this prompt.
</summary>

<objective>
Bump `github.com/bborbe/vault-cli` in `go.mod` to the first published release after `v0.170.2` whose `ops.List` read model both declares and populates `TaskListItem.ManagerSessionID`, and add a focused test proving the field is populated from a goal page. This is the precondition for spec 035, and it restores the approval contract the bump drops (`status: next` on an approved task); no board classification code changes here.
</objective>

<context>
Read the spec `specs/in-progress/035-bug-goal-card-ignores-manager-session.md` in full before starting. This prompt covers the spec's dependency precondition only. Acceptance Criteria 1–17 are prompt 2 and prompt 3; the Post-Deploy Rung-2 check and the operator visual check are on the spec's operator rung and are **not** this prompt's scope.

Read these files before writing:
- `go.mod` — the current requirement is `github.com/bborbe/vault-cli v0.167.0`.
- `pkg/board/goals.go` — `goalResponse` is the consumer that will read `item.ManagerSessionID` in prompt 2; do not change it in this prompt.
- `pkg/handler/queued_writes_test.go` around the `ops.NewListOperation(storage.NewPageStorage(nil)).Execute(...)` call — the in-repo pattern for driving a real vault-cli list read against a temporary vault directory.
- `pkg/board/board_suite_test.go` — the Ginkgo suite for `package board_test`, so a new test file in that package runs without extra wiring.

Verified facts you may rely on (read from the source above; do not re-derive):
- `ops.TaskListItem` in `github.com/bborbe/vault-cli@v0.167.0` has `ClaudeSessionID string` and **no** `ManagerSessionID` field. `v0.170.2` was checked the same way and also has no field, so `v0.170.2` is not a candidate.
- `ops.NewListOperation(pageStorage storage.PageStorage) ListOperation` is the constructor; `Execute(ctx, vaultPath, vaultName, pagesDir string, statusFilters []string, showAll bool, assigneeFilter, goalFilter string) ([]TaskListItem, error)` is the read.
- `storage.NewPageStorage(nil)` is valid: a nil config falls back to `storage.DefaultConfig()`, and `ListPages` reads `<vaultPath>/<pagesDir>/*.md`.
- `GinkgoT().TempDir()` is already used in this repo (`pkg/handler/queued_writes_test.go`, `pkg/watcher/watcher_test.go`).
- The spec's target is "the first vault-cli release after `v0.170.2` that carries the list field from `fix/manager-session-id-in-task-list`", verified by grepping the module cache after the bump — not by assuming a version number.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`
</context>

<requirements>

### 1. Discover the release — do not guess a version

Run the module-version listing and walk the versions after `v0.170.2` in ascending order:

```bash
export PATH=/usr/local/go/bin:$PATH
versions="$(go list -m -versions github.com/bborbe/vault-cli \
  | tr ' ' '\n' | grep -E '^v' | sort -V)"
printf '%s\n' "$versions" | grep -qx 'v0\.170\.2' \
  || { echo "v0.170.2 missing from version list — refusing to guess the boundary"; exit 1; }
printf '%s\n' "$versions" | awk '/^v0\.170\.2$/{seen=1; next} seen'
```

For each candidate version `$v`, download it and inspect its list read model:

```bash
go mod download "github.com/bborbe/vault-cli@$v"
src="$(go env GOMODCACHE)/github.com/bborbe/vault-cli@$v"
grep -n 'ManagerSessionID' "$src/pkg/ops/list.go"
```

A candidate qualifies only when `grep -n` shows **at least two** occurrences in `pkg/ops/list.go`:
1. the `ManagerSessionID` field in `type TaskListItem struct`, and
2. a `ManagerSessionID:` assignment inside the `items[i] = TaskListItem{...}` literal in `Execute`.

Read the surrounding lines to confirm the second occurrence is inside that literal. A release that declares the field but never assigns it does not qualify — the spec calls that a silent no-op.

Take the **first** (lowest) qualifying version after `v0.170.2`. Do not bump to `v0.170.2`; it has no field.

### 2. Stop condition — no qualifying release

If the version listing or a candidate download fails or hangs (network/proxy error, non-zero or timed-out `go list`/`go mod download`; bound each call, e.g. `timeout 120`), report `status: failed` naming the command and its error — that is a tooling failure, not "no qualifying release". If the loop completes and finds no qualifying version, **stop immediately**:
- Do not edit `go.mod` or `go.sum`.
- Do not add a local `ManagerSessionID` field, a shim, or a frontmatter re-parse to route around the dependency.
- Do not add the test in requirement 4 (it would not compile).
- Report `status: failed` with a message naming the versions checked and stating that no published vault-cli release after `v0.170.2` carries a populated `ManagerSessionID`.

This is the spec's explicit precondition: "If the release is not yet available, this spec's prompts do not start."

### 3. Bump the dependency

Using the qualifying version `$v`:

```bash
go get "github.com/bborbe/vault-cli@$v"
go mod tidy
```

Then confirm the module source and the compile:

```bash
grep -n 'github.com/bborbe/vault-cli' go.mod
grep -c 'ManagerSessionID' "$(go env GOMODCACHE)/github.com/bborbe/vault-cli@$v/pkg/ops/list.go"
go build ./...
```

`grep -c` must print at least `2`. `go build ./...` must succeed. If the build fails because the bumped release changed an unrelated API this repo consumes, report `status: failed` naming the breakage — do not refactor unrelated call sites in this prompt. Accept transitive `go.mod`/`go.sum` changes produced by `go mod tidy`; do not pin unrelated modules by hand.

The bump also breaks the repo's own pin test: `pkg/factory/api_test.go` (`Describe("vault-cli pin")`, ~line 276) asserts `github.com/bborbe/vault-cli v0.167.0`. Update that assertion and its `It` description to the bumped version `$v`; keep the `replace` assertion unchanged. Do not delete the test or make it version-agnostic — it is the guard that the pin is a tagged `require` and never a `replace`.

### 4. Add the read-model population test

Create `pkg/board/goals_manager_session_list_test.go` in `package board_test` (the suite in `board_suite_test.go` already runs it). It must prove the dependency populates the field from real page data, not merely that the struct compiles:

```go
// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vault-cli list read model", func() {
	It("populates ManagerSessionID for a goal page that carries it", func() {
		const managerSessionID = "33333333-3333-3333-3333-333333333333"
		vaultPath := GinkgoT().TempDir()
		goalsDir := filepath.Join(vaultPath, "24 Goals")
		Expect(os.MkdirAll(goalsDir, 0o750)).To(Succeed())

		content := "---\nstatus: in_progress\nmanager_session_id: " +
			managerSessionID + "\n---\n# Goal\n"
		Expect(os.WriteFile(
			filepath.Join(goalsDir, "Goal.md"), []byte(content), 0o600,
		)).To(Succeed())

		items, err := ops.NewListOperation(storage.NewPageStorage(nil)).Execute(
			context.Background(), vaultPath, "personal", "24 Goals", nil, true, "", "",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Name).To(Equal("Goal"))
		Expect(items[0].ManagerSessionID).To(Equal(managerSessionID))
	})
})
```

If this test fails because the release populates the field from a different frontmatter key or does not populate it at all, do **not** weaken the test to make it pass. Report `status: failed` with the observed `TaskListItem` value and the release version.

### 5. Restore the approve contract the bump breaks

The qualifying release's `task approve` no longer rewrites `status` (confirm on the bumped source: `grep -n '"status"' "$(go env GOMODCACHE)/github.com/bborbe/vault-cli@$v/pkg/ops/task_approve.go"` prints nothing for v0.171.0; its CHANGELOG says "`vault-cli task approve` no longer rewrites `status`"). This repo has **two** callers of `set.Approve.Execute` in `pkg/mutations/tasks.go`, and both relied on that write: `approveTask` (~line 615) and `RunTask` (~line 87, which approves a `todo` task on the way to starting its session). Two specs in `pkg/mutations/service_test.go` (`UpdateTaskPhase todo-to-planning`, ~lines 1588 and 1620) assert `status: next` and fail after the bump; the `RunTask` test at ~line 866 asserts only `phase: planning` and would not catch the sibling regression. Leaving either alone means an approved row keeps `status: todo`, so the manager sweep's ready-to-start bucket no longer offers it — a board behavior change this prompt must not ship.

Restore the existing contract in **both** callers: extract one helper (e.g. `approveAndQueue(ctx, set, resolved, taskID, currentUser)` that runs `set.Approve.Execute` then the status write) and call it from both `approveTask` and `RunTask`, or apply the identical status write after each `set.Approve.Execute(...)` call. Either way, after the approval succeeds, write the status through the same operation the non-approval branch uses —

```go
if setErr := set.FrontmatterSet.Execute(
    ctx, resolved.Path, taskID, "status", "next", "", "", "", false,
); setErr != nil {
    return newHTTPError(500, setErr.Error())
}
```

— and update both callers' comments so neither claims Approve writes the status. The approved task must end with `phase: planning`, `status: next`, `approved_by`, and `approved_at`, whether it was approved through `UpdateTaskPhase` or through `RunTask`. Extend the existing `RunTask` approval test ("approves a todo task before failing on the unavailable starter", `pkg/mutations/service_test.go` ~line 866) to assert `status: next` as well as `phase: planning`.

If that contract cannot be restored without changing board behavior, stop and report `status: failed` naming the vault-cli release and the changed `Approve` behavior — do not ship a bump that silently changes the board.

### 6. Scope fence

- Do not change `pkg/board/goals.go`, `pkg/board/tasks.go`, `pkg/session/classify.go`, `pkg/api/api.go`, `docs/liveness-classification.md`, or `CHANGELOG.md` in this prompt. Prompt 2 changes the goal classification; prompt 3 changes the docs and changelog.
- Do not add `ManagerSessionID` to `api.GoalResponse`.
- Do not edit the sibling vault-cli repository; it is not part of this checkout.

### 7. Self-check

Before finishing, re-run every `<verification>` command and confirm it passes. Confirm the only production files changed are `go.mod`, `go.sum`, and `pkg/mutations/tasks.go`, the only existing test files changed are `pkg/factory/api_test.go` and `pkg/mutations/service_test.go`, and the only new file is `pkg/board/goals_manager_session_list_test.go`. Walk the spec's dependency precondition against the change and name the release version that satisfied it.
</requirements>

<constraints>
- **Precondition from the spec:** a released vault-cli exposing a populated `ManagerSessionID` on `ops.TaskListItem`, and `go.mod` bumped to it. Sibling artifact: `prompts/add-manager-session-id-to-list-read-model.md` on branch `fix/manager-session-id-in-task-list` in `github.com/bborbe/vault-cli`. Do NOT re-parse goal frontmatter in this repo to route around the dependency. If the release is not yet available, this spec's prompts do not start.
- Verify the target version with `grep ManagerSessionID` in the module cache after the bump; never assume a version number from the spec.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Follow `docs/dod.md` and the bborbe Go conventions: errors wrapped with `github.com/bborbe/errors`, no `fmt.Errorf`, Ginkgo/Gomega tests in an external test package. One `docs/dod.md` bullet is deliberately deferred: its Documentation section asks for a `CHANGELOG.md` entry under `## Unreleased`, which prompt 3 (`3-spec-035-liveness-docs-and-changelog.md`) owns — do not treat that bullet as a blocker here and do not edit `CHANGELOG.md`.
- No new config knob, no new route, no new dependency beyond the vault-cli bump.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first. This repo sets `hideGit: true`, so **no `git` command is available here** — every check below is filesystem- or test-based.

If requirement 2 fired (no qualifying release), these checks do not apply; report `status: failed` with the versions checked instead.

```
export PATH=/usr/local/go/bin:$PATH
test "$(go list -m -f '{{.Version}}' github.com/bborbe/vault-cli)" != "v0.167.0"
test "$(go list -m -f '{{.Version}}' github.com/bborbe/vault-cli)" != "v0.170.2"
go list -m -f '{{.Version}}' github.com/bborbe/vault-cli
```

Both `test` calls must exit 0, and the printed version must be the qualifying release (not `v0.167.0`, not `v0.170.2`).

```
export PATH=/usr/local/go/bin:$PATH
test "$(grep -c 'ManagerSessionID' "$(go env GOMODCACHE)/github.com/bborbe/vault-cli@$(go list -m -f '{{.Version}}' github.com/bborbe/vault-cli)/pkg/ops/list.go")" -ge 2
```

Must exit 0 (the struct field and the population assignment are both present).

```
export PATH=/usr/local/go/bin:$PATH
go build ./...
```

Must exit 0.

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/board/... -count=1
```

Must pass, including the new read-model population test.

```
export PATH=/usr/local/go/bin:$PATH
make precommit
```

Must exit 0.
</verification>
