---
status: completed
summary: Bumped vault-cli to v0.167.0 and routed every task start/resume path through ops.ResolveTaskLauncher, adding GET /api/tasks/{task_id}/resume-command and switching app.js's task resume short-circuit to it.
execution_id: vault-ui-launcher-exec-154-launcher-field-resolution
dark-factory-version: v0.196.0
created: "2026-10-09T10:56:36Z"
queued: "2026-10-09T10:56:36Z"
started: "2026-10-09T10:57:39Z"
completed: "2026-10-09T11:27:57Z"
branch: dark-factory/154-launcher-field-resolution
---

# Open and resume tasks with the task / goal `launcher:` frontmatter

<summary>
- A task whose frontmatter (or whose goal's frontmatter) names a `launcher:` opens and resumes with that launcher from the vault-ui, not the vault default
- The Start button gets this for free from vault-cli, once the vault-cli dependency is bumped to the release that carries it
- The Open button for a task that already has a session currently builds its command in the browser from the vault default; it now asks the server, which resolves the launcher with the same rule `vault-cli` and `/supervisor:open` use
- The take-over / resume command the server returns uses the same resolved launcher
- An invalid launcher value, or two goals naming different launchers, is shown as an error instead of silently opening on the vault default
- Tasks without the field behave exactly as before
</summary>

<objective>
Make every vault-ui path that starts or resumes a task's Claude session use the launcher resolved by vault-cli's exported `ops.ResolveTaskLauncher` (task `launcher:` > goal `launcher:` > vault `claude_script`), so the Start button, the Open button and `/supervisor:open` follow one shared rule. Today the Open button resumes on the vault default even when the task asks for a different model.
</objective>

<context>
Read `go.mod` — `github.com/bborbe/vault-cli` is pinned at v0.163.0.
Read `pkg/factory/mutations.go` — `mutationOpsFactory` builds the vault-cli op set and calls `ops.NewClaudeSessionStarter` / `ops.NewClaudeResumer` with `vault.ClaudeScript`, then `CreateOpSet(...)`. Find `CreateOpSet` and the `ops.NewWorkOnOperation` call it makes.
Read `pkg/mutations/mutations.go` — `resumeCommand(vault, sessionID, taskTitle)` and `sessionResponse(...)`; and `pkg/mutations/tasks.go` — the run, take-over and execute-command task routes that return `sessionResponse`.
Read `pkg/handler/router.go` (route table) and `pkg/handler/api_tasks.go` / `pkg/handler/api_mutations.go` for the handler + Mutations-interface pattern; `pkg/handler/fake_mutations_test.go` for the test fake.
Read `src/vault_ui/static/app.js` around the "Resume short-circuit" comment — it builds `${vaultConfig.claude_script} --resume ${item.claude_session_id}` client-side. `src/vault_ui/static_embed.go` embeds that directory into the Go binary, so this file IS live.
In the vendored/module copy of vault-cli after the bump, read `pkg/ops/launcher.go`: `ResolveLauncher`, `ResolveTaskLauncher(ctx, goalStorage, vaultPath, vaultScript, task) (resolved, warnings, err)`, `LauncherFactory`; and `pkg/ops/workon.go` `NewWorkOnOperation` for its new parameters (a goal storage and a launcher factory).
</context>

<requirements>
1. Bump `github.com/bborbe/vault-cli` to v0.167.0 (the first release containing `ops.ResolveTaskLauncher`) with `go get github.com/bborbe/vault-cli@v0.167.0 && go mod tidy`.
2. Add a `launcherFactory ops.LauncherFactory` parameter to `CreateOpSet` (update both callers: `pkg/factory/mutations.go` and `pkg/factory/ops_test.go`) and fix the `ops.NewWorkOnOperation` call for its new signature: pass `goalStore` and that launcherFactory. In `mutationOpsFactory`, build the launcher factory that builds `ops.NewClaudeSessionStarter(script, locker)` / `ops.NewClaudeResumer(script, locker)` sharing the SAME `locker` as the default pair in `mutationOpsFactory`. This alone makes the Start button honour `launcher:`.
3. Make `resumeCommand` use the resolved launcher instead of `vault.ClaudeScript`: resolve with `ops.ResolveTaskLauncher` for the task being resumed (load the task as a vault-cli `*domain.Task` through the op set's task storage, and pass the goal storage). Thread the resolution through `sessionResponse` for the task routes; goal routes keep the vault default (goal work-on is unchanged in vault-cli). Add `TaskStorage storage.TaskStorage` and `GoalStorage storage.GoalStorage` fields to `vaultui.OpSet` in `pkg/ops.go`, populated in `CreateOpSet` from the existing `taskStore`/`goalStore`; load the task with `set.TaskStorage.FindTaskByName(ctx, resolved.Path, taskID)` (tests use vault-cli's `mocks.TaskStorage` / `mocks.GoalStorage`). Resolve at the TOP of RunTask, TakeOverTask and the execute-command route — after loading the task, before any marker write, `Launch.Begin`, SIGTERM or `WorkOn` call — and pass the resolved script into `sessionResponse`. A resolution error (invalid value, conflicting goals, task not found) becomes an HTTP 4xx naming the problem — never a silent fallback to the vault default. Log resolution warnings (stale goal links).
4. Add `GET /api/tasks/{task_id}/resume-command` returning the same `SessionResponse` shape (session id, command, working dir, task title) for a task's EXISTING `claude_session_id`, with no session mutation — 400 when the task has no session. Register it in `router.go`, add it to the `mutations.Service` interface and the hand-written test fake.
5. In `src/vault_ui/static/app.js` `runSession`, change the resume short-circuit ONLY for `kind === 'task'` to fetch that endpoint and pass its `command` and `working_dir` to `showModal`, instead of building the command from `vaultConfig.claude_script`. Show the server's error message on a non-2xx response. The goal branch keeps its existing client-side command unchanged. Leave every other `app.js` path untouched.
6. Tests (Ginkgo/Gomega, counterfeiter fakes where they exist, matching the existing handler/mutations tests):
   - `resumeCommand` / task route: task with `launcher: cc-private-claude` and vault script `/s/cc-private` → command starts with `/s/cc-private-claude --resume`; task without the field → `/s/cc-private --resume` (unchanged); invalid value → 400, no command; take-over with an invalid launcher → 400 and the session is NOT terminated (signaler fake not called).
   - Goal inheritance: task without the field under a goal with `launcher: cc-private-claude` → `/s/cc-private-claude`.
   - The new endpoint: 200 with the resolved command for a task with a session; 400 for a task without one; router test proving the route is registered.
   - The factory wiring: the op set's work-on builds its starter via the factory for a resolved launcher (assert the script passed to the factory).
7. Add a `## Unreleased` CHANGELOG bullet: `feat: Start and Open honour the task/goal launcher: frontmatter via vault-cli ops.ResolveTaskLauncher; new GET /api/tasks/{task_id}/resume-command`.
8. Self-check: before finishing, re-run `<verification>` and confirm it passes; walk each requirement above against the diff.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass; a task without `launcher:` (on itself or its goals) must produce exactly today's command
- Never fall back to the vault default when resolution fails — surface the error
- Do NOT re-implement the precedence or the value check in vault-ui — call vault-cli's `ops.ResolveTaskLauncher`, the single home of the rule
- Do NOT change goal Start/Open behaviour
- Do NOT hand-bump versions or tags; only the `## Unreleased` bullet
- Do NOT run `go mod vendor`
- Repo-relative paths only
</constraints>

<verification>
Run `make precommit` — must pass.
Run `grep -n 'resume-command' pkg/handler/router.go` — must print one line.
Run `grep -n 'resume-command' src/vault_ui/static/app.js` — must print at least one line (the task resume path fetches the server command).
</verification>
