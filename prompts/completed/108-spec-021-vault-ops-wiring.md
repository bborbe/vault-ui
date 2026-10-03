---
status: completed
spec: [021-go-backend-foundation-vault-ops]
summary: Wired all 15 vault-cli operations through their exported ops constructors into a single vaultui.OpSet assembled by factory.CreateOpSet, and proved the wiring with Ginkgo/Gomega integration tests driving the real ops against a temp vault fixture.
execution_id: vault-ui-exec-108-spec-021-vault-ops-wiring
dark-factory-version: v0.196.0
created: "2026-10-03T21:40:00Z"
queued: "2026-10-03T21:29:32Z"
started: "2026-10-03T21:38:43Z"
completed: "2026-10-03T21:42:46Z"
branch: dark-factory/go-backend-foundation-vault-ops
---

# Wire vault-cli ops and prove them against a temp vault

<summary>
- Every vault operation the backend needs is wired by calling vault-cli's exported constructor functions directly, rather than through the operation interfaces they return.
- Reading a vault: list, show, and topic-show operations return the entities from a real temp vault fixture.
- Goal listing and topic listing both route through the generic list operation, given the goals directory and the topics directory respectively — the same operation, two directories, two distinct result sets.
- Writing a vault: set-field, clear-field, work-on, defer, complete, and the goal/topic variants each produce the expected change in the vault file's frontmatter.
- The full set of operations is assembled in one place by the composition root, so the HTTP spec can consume it without re-touching the foundation.
- A Ginkgo/Gomega integration test drives the real operations against a temp vault and asserts the concrete frontmatter each one writes.
- The Python backend is untouched and keeps building and testing exactly as before.
</summary>

<objective>
Wire every vault-cli operation the backend needs through vault-cli's exported `ops.*` constructors, assembled by `pkg/factory` into a single `vaultui.OpSet`, and prove the wiring with Ginkgo/Gomega integration tests that drive the real operations against a temp vault fixture — so spec 3's HTTP surface consumes the op set without re-touching the foundation.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — `Create*` factories are pure wiring (no `if`/`switch`/`for`, no `error` return); §4.4 list/struct composition.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — implementation types live in the flat `pkg/`, not in `pkg/factory/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega suite, external test package, `DescribeTable`, no stdlib table tests.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the previous prompts `prompts/1-spec-021-go-module-skeleton-admin-block.md` and `prompts/2-spec-021-vault-cli-dependency-and-discovery.md` so you know the module layout already in place.

Read the spec `specs/in-progress/021-go-backend-foundation-vault-ops.md` (Desired Behavior 4, 5, 7; Acceptance Criteria 7, 8, 9; Assumptions; Failure Modes; Constraints).

**The authoritative exemplar for the wiring is vault-cli's own CLI.** After `go get`, read
`$(go env GOPATH)/pkg/mod/github.com/bborbe/vault-cli@v0.159.0/pkg/cli/cli.go` — it constructs every one of these operations from `storage.NewConfigFromVault(vault)` and shows the exact argument order. Mirror it. Read the individual `pkg/ops/*.go` files for the constructor signatures below if anything is unclear.
</context>

<requirements>

### 1. `pkg/ops.go` (package `vaultui`)

Add the op-set type. It holds the callable operation interfaces returned by the constructors:

```go
package vaultui

import "github.com/bborbe/vault-cli/pkg/ops"

// OpSet is the full set of vault-cli operations the backend needs, wired once
// from a single vault. List is used with the tasks, goals, and topics
// directories (the directory is passed to Execute, not to the constructor).
type OpSet struct {
	List             ops.ListOperation
	Show             ops.ShowOperation
	FrontmatterSet   ops.FrontmatterSetOperation
	FrontmatterClear ops.FrontmatterClearOperation
	WorkOn           ops.WorkOnOperation
	Defer            ops.DeferOperation
	Complete         ops.CompleteOperation
	GoalSet          ops.EntitySetOperation
	GoalClear        ops.EntityClearOperation
	GoalWorkOn       ops.GoalWorkOnOperation
	GoalDefer        ops.GoalDeferOperation
	GoalComplete     ops.GoalCompleteOperation
	TopicShow        ops.EntityShowOperation
	TopicSet         ops.EntitySetOperation
	TopicClear       ops.EntityClearOperation
}
```

### 2. Extend `pkg/factory/factory.go` — `CreateOpSet`

`CreateOpSet` is pure wiring: a storage-config derivation, six storage constructors, and a struct literal of constructor calls. No `if`, no `switch`, no `for`, no `error` return.

```go
// CreateOpSet builds the full vault-cli op set for a single vault. The
// injectable dependencies are parameters so callers and tests can substitute
// fakes for the session-spawning and publishing behaviour.
func CreateOpSet(
	vault *config.Vault,
	currentDateTime libtime.CurrentDateTime,
	publisher ops.EscalationPublisher,
	starter ops.ClaudeSessionStarter,
	resumer ops.ClaudeResumer,
	interactionCounter ops.InteractionCounter,
	uuidGenerator func() string,
) vaultui.OpSet {
	storageConfig := storage.NewConfigFromVault(vault)
	taskStore := storage.NewTaskStorage(storageConfig)
	goalStore := storage.NewGoalStorage(storageConfig)
	topicStore := storage.NewTopicStorage(storageConfig)
	dailyStore := storage.NewDailyNoteStorage(storageConfig)
	pageStore := storage.NewPageStorage(storageConfig)
	return vaultui.OpSet{
		List:             ops.NewListOperation(pageStore),
		Show:             ops.NewShowOperation(taskStore),
		FrontmatterSet:   ops.NewFrontmatterSetOperation(taskStore, currentDateTime, publisher, vault.Name, vault.GetTasksDir()),
		FrontmatterClear: ops.NewFrontmatterClearOperation(taskStore, publisher, vault.Name, vault.GetTasksDir()),
		WorkOn:           ops.NewWorkOnOperation(taskStore, dailyStore, currentDateTime, uuidGenerator, starter, resumer),
		Defer:            ops.NewDeferOperation(taskStore, dailyStore, currentDateTime),
		Complete:         ops.NewCompleteOperation(taskStore, dailyStore, currentDateTime, interactionCounter),
		GoalSet:          ops.NewGoalSetOperation(goalStore),
		GoalClear:        ops.NewGoalClearOperation(goalStore),
		GoalWorkOn:       ops.NewGoalWorkOnOperation(goalStore, uuidGenerator, starter, resumer),
		GoalDefer:        ops.NewGoalDeferOperation(goalStore, currentDateTime),
		GoalComplete:     ops.NewGoalCompleteOperation(goalStore, taskStore, currentDateTime),
		TopicShow:        ops.NewTopicShowOperation(topicStore),
		TopicSet:         ops.NewTopicSetOperation(topicStore),
		TopicClear:       ops.NewTopicClearOperation(topicStore),
	}
}
```

Add imports `libtime "github.com/bborbe/time"`, `"github.com/bborbe/vault-cli/pkg/ops"`, `"github.com/bborbe/vault-cli/pkg/storage"` (plus the `config` import already present from the previous prompt).

**Verified constructor signatures (vault-cli v0.159.0 — do not deviate):**
```go
func NewListOperation(pageStorage storage.PageStorage) ListOperation
func NewShowOperation(taskStorage storage.TaskStorage) ShowOperation
func NewFrontmatterSetOperation(taskStorage storage.TaskStorage, currentDateTime libtime.CurrentDateTime, publisher EscalationPublisher, vaultName, tasksDir string) FrontmatterSetOperation
func NewFrontmatterClearOperation(taskStorage storage.TaskStorage, publisher EscalationPublisher, vaultName, tasksDir string) FrontmatterClearOperation
func NewWorkOnOperation(taskStorage storage.TaskStorage, dailyNoteStorage storage.DailyNoteStorage, currentDateTime libtime.CurrentDateTime, uuidGenerator func() string, starter ClaudeSessionStarter, resumer ClaudeResumer) WorkOnOperation
func NewDeferOperation(taskStorage storage.TaskStorage, dailyNoteStorage storage.DailyNoteStorage, currentDateTime libtime.CurrentDateTime) DeferOperation
func NewCompleteOperation(taskStorage storage.TaskStorage, dailyNoteStorage storage.DailyNoteStorage, currentDateTime libtime.CurrentDateTime, interactionCounter InteractionCounter) CompleteOperation
func NewGoalWorkOnOperation(goalStorage storage.GoalStorage, uuidGenerator func() string, starter ClaudeSessionStarter, resumer ClaudeResumer) GoalWorkOnOperation
func NewGoalDeferOperation(goalStorage storage.GoalStorage, currentDateTime libtime.CurrentDateTime) GoalDeferOperation
func NewGoalCompleteOperation(goalStorage storage.GoalStorage, taskStorage storage.TaskStorage, currentDateTime libtime.CurrentDateTime) GoalCompleteOperation
func NewGoalSetOperation(goalStorage storage.GoalStorage) EntitySetOperation
func NewGoalClearOperation(goalStorage storage.GoalStorage) EntityClearOperation
func NewTopicShowOperation(topicStorage storage.TopicStorage) EntityShowOperation
func NewTopicSetOperation(topicStorage storage.TopicStorage) EntitySetOperation
func NewTopicClearOperation(topicStorage storage.TopicStorage) EntityClearOperation
```
Storage constructors (all take `*storage.Config`): `storage.NewConfigFromVault(*config.Vault) *storage.Config`, `NewTaskStorage`, `NewGoalStorage`, `NewTopicStorage`, `NewDailyNoteStorage`, `NewPageStorage`.

**Verified `Execute` signatures (for the tests):**
```go
ListOperation.Execute(ctx, vaultPath, vaultName, pagesDir string, statusFilters []string, showAll bool, assigneeFilter, goalFilter string) ([]TaskListItem, error)
ShowOperation.Execute(ctx, vaultPath, vaultName, taskName string) (TaskDetail, error)
EntityShowOperation.Execute(ctx, vaultPath, vaultName, entityName string) (EntityShowResult, error)
FrontmatterSetOperation.Execute(ctx, vaultPath, taskName, key, value, reason, gateSuccessor, actor string, force bool) error
FrontmatterClearOperation.Execute(ctx, vaultPath, taskName, key string) error
WorkOnOperation.Execute(ctx, vaultPath, taskName, assignee, vaultName string, isInteractive bool, sessionDir string, vault *config.Vault) (MutationResult, error)
DeferOperation.Execute(ctx, vaultPath, taskName, dateStr, vaultName string) (MutationResult, error)
CompleteOperation.Execute(ctx, vaultPath, taskName, vaultName string, force bool, reason, gateSuccessor string) (MutationResult, error)
GoalWorkOnOperation.Execute(ctx, vaultPath, goalName, assignee, vaultName string, isInteractive bool, sessionDir string, vault *config.Vault) (MutationResult, error)
GoalDeferOperation.Execute(ctx, vaultPath, goalName, dateStr, vaultName string) (MutationResult, error)
GoalCompleteOperation.Execute(ctx, vaultPath, goalName, vaultName string, force bool, reason, gateSuccessor string) (MutationResult, error)
EntitySetOperation.Execute(ctx, vaultPath, entityName, key, value, reason, gateSuccessor string) error
EntityClearOperation.Execute(ctx, vaultPath, entityName, key string) error
```
Result fields used below: `TaskListItem{Name, Status}`, `TaskDetail{Name, Status}`, `EntityShowResult{Name}`, `MutationResult{Success, Name, SessionID}`.

### 3. Tests — `pkg/factory/ops_test.go` (package `factory_test`)

Reuse the existing `pkg/factory/factory_suite_test.go` suite (do NOT create a second suite). Build a temp vault fixture and drive the real operations. New Go code must reach ≥80% statement coverage per `definition-of-done.md`.

**Fixture** — in `BeforeEach`, `os.MkdirTemp`, then create `Tasks/`, `Goals/`, `23 Topics/`, `Daily Notes/` (mode `0750`) and write:
- `Tasks/Task A.md` = `"---\nstatus: next\npage_type: task\n---\n# Task A\n\nA task.\n"`
- `Goals/Goal A.md` = `"---\nstatus: active\npage_type: goal\n---\n# Goal A\n\nA goal.\n"`
- `23 Topics/Topic A.md` = `"---\npage_type: topic\n---\n# Topic A\n\nA topic.\n"`

Register `DeferCleanup(func() { _ = os.RemoveAll(vaultDir) })`.

**Vault** — write a vault-cli config file pointing `vaults.test.path` at `vaultDir` (same shape as the previous prompt's test) and obtain the vault via `factory.CreateConfigLoader(configPath)` + `loader.GetVault(ctx, "test")`. Using the loader proves the discovered path is the root the ops are invoked against.

**Op set** — build once per spec:
```go
starter := &mocks.ClaudeSessionStarter{}
starter.StartSessionReturns(nil)
resumer := &mocks.ClaudeResumer{}
resumer.ResumeSessionReturns(nil)
counter := &mocks.InteractionCounter{}
counter.CountReturns(0)
publisher := ops.NewEscalationPublisher("", "", ops.NewKafkaNotificationSenderFactory())
set := factory.CreateOpSet(vault, libtime.NewCurrentDateTime(), publisher, starter, resumer, counter, uuid.NewString)
```
(`mocks` = `github.com/bborbe/vault-cli/mocks`, `uuid` = `github.com/google/uuid`. The no-op publisher with empty brokers publishes nothing and opens no connection. The fake starter/resumer keep the work-on operations from spawning `claude`.)

**Coverage:**

1. **The op set is fully populated** (AC 8): assert every field of `set` is non-nil (List, Show, FrontmatterSet, FrontmatterClear, WorkOn, Defer, Complete, GoalSet, GoalClear, GoalWorkOn, GoalDefer, GoalComplete, TopicShow, TopicSet, TopicClear).

2. **Read ops against the temp vault** (AC 7):
   - `set.List.Execute(ctx, vaultDir, "test", "Tasks", nil, true, "", "")` returns exactly one item named `Task A` with status `next`.
   - `set.Show.Execute(ctx, vaultDir, "test", "Task A")` returns `Name == "Task A"` and `Status == "next"`.
   - `set.TopicShow.Execute(ctx, vaultDir, "test", "Topic A")` returns `Name == "Topic A"`.

3. **Goal/topic list is the same op with two directories** (AC 8):
   - `set.List.Execute(ctx, vaultDir, "test", "Goals", nil, true, "", "")` returns `Goal A`.
   - `set.List.Execute(ctx, vaultDir, "test", "23 Topics", nil, true, "", "")` returns `Topic A`.
   - Assert the two result sets differ (distinct names).

4. **Write ops produce the expected frontmatter** (AC 9) — one named `It` per op, each asserting the concrete expected value by re-reading the vault file. Add a test helper that reads a file, extracts the `---`-delimited frontmatter block, and returns the value of a key (returning `""` when absent). Assertions:
   - `set.FrontmatterSet.Execute(ctx, vaultDir, "Task A", "priority", "5", "", "", "tester", false)` → `Tasks/Task A.md` frontmatter has `priority` = `5`.
   - then `set.FrontmatterClear.Execute(ctx, vaultDir, "Task A", "priority")` → `priority` is gone.
   - `set.WorkOn.Execute(ctx, vaultDir, "Task A", "alice", "test", false, vaultDir, vault)` → task frontmatter has a non-empty `claude_session_id`, and `MutationResult.Success` is true.
   - `set.Defer.Execute(ctx, vaultDir, "Task A", "2026-12-31", "test")` → task frontmatter `defer_date` starts with `2026-12-31`.
   - `set.Complete.Execute(ctx, vaultDir, "Task A", "test", false, "", "")` → task frontmatter `status` = `completed`.
   - `set.GoalWorkOn.Execute(ctx, vaultDir, "Goal A", "alice", "test", false, vaultDir, vault)` → goal frontmatter has a non-empty `claude_session_id`.
   - `set.GoalDefer.Execute(ctx, vaultDir, "Goal A", "2026-12-31", "test")` → goal frontmatter `defer_date` starts with `2026-12-31`.
   - `set.GoalComplete.Execute(ctx, vaultDir, "Goal A", "test", false, "", "")` → goal frontmatter `status` = `completed`.
   - `set.GoalSet.Execute(ctx, vaultDir, "Goal A", "assignee", "alice", "", "")` → goal frontmatter `assignee` = `alice`; then `set.GoalClear.Execute(ctx, vaultDir, "Goal A", "assignee")` → `assignee` gone.
   - `set.TopicSet.Execute(ctx, vaultDir, "Topic A", "assignee", "alice", "", "")` → topic frontmatter `assignee` = `alice`; then `set.TopicClear.Execute(ctx, vaultDir, "Topic A", "assignee")` → `assignee` gone.

   Use a fresh fixture per spec (the suite `BeforeEach` already does this) so ordering between `It` blocks never matters. Do NOT reuse a mutated file across specs.

Notes: `GoalSet`/`TopicSet` use the plain `assignee` string field (verified: `GoalFrontmatter.SetField`/`TopicFrontmatter.SetField` accept it without validation). `GoalComplete` with `force=false` succeeds when no open task references the goal — the fixture has none.

5. **Write-op errors surface** (spec 021 Failure Modes rows "a write op crashes mid-write" and "disk full or permission denied"): drive a write op against an entity that does not exist — e.g. `set.Complete.Execute(ctx, vaultDir, "Does Not Exist", "test", false, "", "")` — and assert it returns a non-nil error, and that the vault is left byte-identical — snapshot the fixture files' bytes before the call and assert they are unchanged afterward, with no file created for the missing entity. This proves the wiring propagates a write failure instead of reporting success, and that the op set does not swallow errors. A not-found target proves the op returns an error instead of a success result; the "crashes mid-write" and "disk full or permission denied" rows share the same error-propagation contract and are not separately injectable in-process, so this single not-found assertion covers all three rows.

### 4. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- feat: Wire every vault-cli operation (list, show, set/clear field, work-on, defer, complete, and the goal/topic variants) through its exported ops constructors, assembled into a single op set by the composition root.`
Do NOT modify any existing section.

### 5. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.
</requirements>

<constraints>
- Copy of spec 021 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline.
  - The existing Python backend must keep working: `make test` and `make precommit` must continue to pass the Python steps.
  - vault-cli is pinned by `require` to a tagged release; a `replace` directive is a MUST-violation.
  - vault-cli stays the sole vault interface — the Go backend reads and writes no vault file directly; the temp-vault fixture is the only place a file is inspected, and only to assert an op's effect.
  - The entity set/clear ops are per-entity: use `ops.NewGoalSetOperation(storage.GoalStorage)` / `NewGoalClearOperation(storage.GoalStorage)` / `NewTopicSetOperation(storage.TopicStorage)` / `NewTopicClearOperation(storage.TopicStorage)`. There is no generic `EntitySetOperation`/`EntityClearOperation` constructor — those names are interfaces only.
  - A consumer for `ops.NewTopicSetOperation` / `ops.NewTopicClearOperation` is a Non-goal — they are wired for symmetry only; do NOT add a consumer.
  - Serving any vault operation over HTTP is a Non-goal — wire the library layer only; do NOT add HTTP routes.
  - Package layout follows `go-package-layout-guide`, composition follows `go-factory-pattern`, tests follow `go-testing-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT reimplement any vault-cli behaviour in vault-ui; call the exported constructors only.
- Do NOT touch `src/`, `tests/`, `Makefile`, or `docs/dod.md`.
- Existing Go tests from the previous prompts must still pass.
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test ./...` — must exit 0 (runs the admin-block suite and the new op-set integration suite).
4. `grep -nE 'ops\.New(List|Show|FrontmatterSet|FrontmatterClear|WorkOn|Defer|Complete|GoalSet|GoalClear|GoalWorkOn|GoalDefer|GoalComplete|TopicShow|TopicSet|TopicClear)Operation' pkg/factory/factory.go` — must print one line per constructor (15 lines).
5. `! grep -rn --include='*.go' 'exec.Command' .` — must succeed.
6. `make test` — the existing pytest suite must still pass.
7. `make precommit` — must exit 0 (parity with the spec's Verification section).
</verification>
