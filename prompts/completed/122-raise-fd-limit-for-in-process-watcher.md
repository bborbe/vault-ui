---
status: completed
summary: Added pkg/fdlimit with a pure TargetLimit and a syscall-backed Raise, called from main.go's execute to lift the process file-descriptor limit at startup, with glog reporting and Ginkgo tests.
execution_id: vault-ui-exec-122-raise-fd-limit-for-in-process-watcher
dark-factory-version: v0.196.0
created: "2026-10-04T19:10:51Z"
queued: "2026-10-04T19:23:42Z"
started: "2026-10-04T19:46:52Z"
completed: "2026-10-04T19:51:00Z"
branch: dark-factory/122-raise-fd-limit-for-in-process-watcher
---

# Raise the file-descriptor limit at startup so the in-process watcher can watch every vault

<summary>
- The service sets its own file-descriptor limit when it starts
- It can watch every configured vault without running out of descriptors
- Startup no longer depends on how the process happened to be launched
- The limit is raised toward the system maximum and never lowered
- A limit that cannot be raised is a warning, not a startup failure
- Behaviour is unchanged when the inherited limit is already sufficient
- The applied limit is visible in the startup log
- Existing tests continue to pass
</summary>

<objective>
The service runs the vault watcher in-process, so it needs roughly one file descriptor per watched file across every configured vault — about 10,000 on a twelve-vault machine. It never asks for them, so it inherits whatever the launcher granted and stops accepting connections once it runs out. Raise the process's own descriptor limit at startup so the service works regardless of how it was launched.
</objective>

<context>
Read `CLAUDE.md` for project conventions — but note that its Development Standards and Architecture sections still describe the superseded Python backend (`src/task_orchestrator/`, FastAPI, pytest) and never mention `pkg/`, `main.go`, Ginkgo, or `errors.Wrap`. For this change the Go conventions are the ones demonstrated in `pkg/` and `main.go`, plus the coding guides named in `<constraints>`.

See `docs/launchd-service.md` for how the service is launched: the launchd plist sets no descriptor limit, so the process inherits launchd's default.

See also `docs/go-cutover.md` — it repoints the LaunchAgent from the Python tool to the Go binary and is operator-run, so no plist edit and no service restart belongs in this prompt.

The Python backend this replaces does not have this problem, and the reason matters: it runs the watcher as a `vault-cli watch` **subprocess**, so the descriptors are held by a child process with its own limit. This rewrite moved the watcher **in-process**, which moved the descriptor demand into the main process — and nothing took over the job of raising the limit.

Measured on the operator's machine: the Python parent process holds 11 descriptors, its `vault-cli watch` child holds 10,000, and the Go binary exhausts whatever it is given — under an 8,000 limit it logs `accept tcp: too many open files` and serves nothing at all.

`main.go` holds `execute(ctx)`, which wires the application and starts the watcher via `factory.CreateWatcher(loader, manager)` inside `run.CancelOnFirstErrorWait`. `main` itself is not unit-testable, so the limit logic belongs in its own package, with the pure `TargetLimit` as the testable seam and the syscall confined to `Raise`. Do not introduce an injected syscall interface — requirement 6 deliberately exercises the real `setrlimit` call.

`pkg/` uses one lowercase single-word package per concern — `activity`, `cleanup`, `hierarchy`, `sigterm`, `statuscache` are the shape to follow.
</context>

<requirements>
1. Add a new package `pkg/fdlimit`.

2. In it, add a pure function:

   `func TargetLimit(current, hard uint64) uint64`

   It returns the descriptor limit to request, with these properties:
   - never lower than `current`
   - never higher than `hard`, except that `hard` may be "infinity" — in that case cap at a named constant chosen comfortably above 10,000 (65,536 is a reasonable ceiling). Detect infinity portably: `syscall.RLIM_INFINITY` is an untyped `-1` on linux and overflows a `uint64`, so neither `hard == syscall.RLIM_INFINITY` nor `uint64(syscall.RLIM_INFINITY)` compiles there — and `hard == ^uint64(0)` compiles but is wrong on darwin, whose infinity is `0x7fffffffffffffff`. Convert through a typed int first (`var inf int = syscall.RLIM_INFINITY; if hard == uint64(inf)`), or treat any `hard` above a large threshold (e.g. `hard > 1<<40`) as infinite. The package must build for both `GOOS=darwin` and `GOOS=linux` — the dev machine is darwin and the container is linux
   - when `hard` is a finite value above `current`, return `hard`
   - when `current` already equals or exceeds the cap, return `current`

   Keep it pure so it can be table-tested without touching the process.

3. In the same package, add:

   `func Raise(ctx context.Context) (uint64, error)`

   It reads the current soft and hard limits, computes the target with `TargetLimit`, applies it, and returns the resulting soft limit. Use the standard library's `syscall` package (`syscall.Getrlimit` / `syscall.Setrlimit` with `syscall.RLIMIT_NOFILE`); do not add a module dependency. Wrap errors with the project's `errors.Wrap(ctx, err, "...")` style.

4. Call `fdlimit.Raise(ctx)` from `main.go`'s `execute`, before `run.CancelOnFirstErrorWait(...)` — the watcher starts inside that call, so the limit must already be raised by then.

5. Log the result with `glog`: the applied limit on success, and a warning naming the error on failure. **A failure to raise the limit must not abort startup** — log it and continue. Rationale: a process that cannot raise its limit still serves the API routes it can reach, whereas aborting takes the whole board down for a condition an operator may be able to fix without a code change. Make the warning state the consequence — the limit actually applied, and that vault watching may be incomplete — so the degraded state is visible in the log rather than silent.

6. Add `pkg/fdlimit/fdlimit_suite_test.go` as a Ginkgo suite matching the shape of `pkg/factory/factory_suite_test.go` (all existing suites use the `<pkg>_suite_test.go` name). Include:
   - a `DescribeTable` over `TargetLimit` covering: `current` below a finite `hard`; `current` equal to `hard`; `hard` of `RLIM_INFINITY` with `current` below the cap — express it portably in the table too (`var inf int = syscall.RLIM_INFINITY; Entry(..., uint64(inf))`); a bare `uint64(syscall.RLIM_INFINITY)` fails to compile on linux just as it does in the implementation; `current` already above the cap; `current` equal to the cap
   - one test that traverses the real boundary: read the current soft limit, lower the process's own soft limit to a small value comfortably above the process's current open-descriptor count (256 is a safe choice — going lower makes unrelated opens fail), call `Raise`, then assert **(a)** it returns without error, **(b)** the returned value equals `TargetLimit(loweredValue, hard)`, and **(c)** the process's soft limit afterwards is **strictly greater** than the lowered value. Asserting only `after >= before-the-call` proves nothing: "before the call" is the lowered value, so a no-op `Raise` satisfies it. Restore the original soft limit in `AfterEach`/`t.Cleanup` so the suite does not inherit a changed limit

7. Before finishing, re-run `<verification>` and confirm it passes; walk each requirement above against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Repo-relative paths only — no absolute or home-relative paths
- Follow the project's existing conventions: `errors.Wrap(ctx, err, "...")` from `github.com/bborbe/errors`, `glog` for logging, Ginkgo/Gomega for tests
- Do not add a new module dependency for this — the standard library is sufficient
- Do not change the listening port, the config resolution, or any route
- New code has good test coverage — target >= 80%
- Coding guides to follow (do not inline): `go-testing-guide`, `go-error-wrapping-guide`, `go-package-layout-guide`, `go-glog-guide`
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
