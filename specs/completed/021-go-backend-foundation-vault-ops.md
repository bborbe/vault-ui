---
status: completed
approved: "2026-10-03T20:58:01Z"
generating: "2026-10-03T21:02:49Z"
prompted: "2026-10-03T21:28:07Z"
verifying: "2026-10-03T21:45:19Z"
completed: "2026-10-03T22:56:13Z"
branch: dark-factory/go-backend-foundation-vault-ops
---

## Summary

- vault-ui's backend is Python/FastAPI today (14 modules, 7,678 LOC, 24 routes) and reaches the vault by shelling out to 18 vault-cli subcommands.
- This spec lays the Go foundation for the replacement backend: `main.go` at the repo root over a flat `pkg/` package, plus the two conventional exceptions the cited `go-package-layout-guide` mandates — `pkg/factory/` (composition root) and `pkg/handler/` (admin HTTP handlers) — and the canonical admin block on `:9090`.
- vault-cli is imported as a **library** — pinned to a tagged release via `require`, never a `replace ../vault-cli` — and every vault operation the backend needs goes through its exported `ops.New*Operation` constructors instead of a subprocess.
- Goal and topic listing reuse the generic `ops.NewListOperation` with a different directory, because vault-cli exposes no dedicated goal-list / topic-list constructor; the entity set/clear ops are **per-entity** — `ops.NewGoalSetOperation(storage.GoalStorage)`, `ops.NewTopicSetOperation(storage.TopicStorage)`, and the matching `*ClearOperation` — because vault-cli's generic `EntitySetOperation` / `EntityClearOperation` are interfaces with no constructor.
- The Python backend keeps working throughout; `make test` and `make precommit` gain Go steps alongside the existing Python ones.

## Problem

vault-ui's backend spawns a `vault-cli` subprocess for every vault operation. That makes each operation a process fork plus a JSON round-trip, couples the backend to the CLI's stdout contract, and forces the tests to mock a subprocess boundary rather than exercise the real vault logic. vault-cli already ships that logic as an importable Go library — the same `ops.*` constructors the CLI itself calls — but vault-ui has no Go code at all, so there is nothing to import into. The foundation has to exist before the private vault logic (spec 2) and the HTTP surface (spec 3) can be built on it.

## Goal

vault-ui has a Go backend foundation: a compilable Go module whose composition root owns the admin block on `:9090`, which depends on vault-cli as a pinned library dependency, and which exposes every vault operation the backend needs through vault-cli's exported `ops.*` constructors — all proven by a passing Go test suite, with the Python backend still building and testing unchanged.

## Non-goals

- The vault-ui-private logic — cleanup sweep, session liveness/termination, registries, pane resolution, status cache, file watcher. Sibling spec 2 owns these.
- The HTTP API surface, the WebSocket, route-by-route parity with the 24 existing routes, and the production cutover. Sibling spec 3 owns these.
- Replacing or removing the Python backend — it keeps serving `:8001` until spec 3's cutover.
- Serving any vault operation over HTTP in this spec — this spec wires the library layer only.
- Reimplementing vault-cli behavior in vault-ui — no direct vault file reads or writes.
- A consumer for `ops.NewTopicSetOperation` / `ops.NewTopicClearOperation` — they are wired for symmetry with the goal set/clear ops only, since today's Python client exposes no topic write (9 methods, none writes a topic) and `src/vault_ui/api/tasks.py` never sets or clears a topic; naming a real consumer is spec 3's call, not this spec's.
- Do NOT add a `replace` directive for vault-cli (not even a local `../vault-cli` path for convenience) — invariant; if a future consumer demands a local override, that is a separate spec.
- Do NOT add a config knob for the admin port — the canonical `:9090` block is fixed; if a future consumer demands variation, that is a separate spec.

## Acceptance Criteria

- [ ] `go.mod` at the repo root declares `module github.com/bborbe/vault-ui` and `go 1.27.1` — evidence: file content (`grep -n '^module github.com/bborbe/vault-ui$' go.mod` returns line ≥1 and `grep -n '^go 1.27.1$' go.mod` returns line ≥1).
- [ ] `go build ./...` and `go vet ./...` both exit 0 — evidence: exit code.
- [ ] The in-repo Go integration test starts the service and asserts each of `GET /healthz`, `GET /readiness`, `GET /metrics`, `GET /setloglevel/info`, and `GET /gc` on `:9090` returns HTTP 200 — evidence: HTTP status (asserted per endpoint by the test).
- [ ] `GET /readiness` returns 503 before `config.Loader` vault discovery succeeds and 200 after — evidence: HTTP status (the test queries readiness before the loader has discovered the vaults and asserts 503, then after discovery asserts 200).
- [ ] `GET /metrics` body is Prometheus text exposition format containing the vault-ui-owned metric `vault_ui_build_info` by name and at least one `go_*` runtime metric line — evidence: HTTP response body match (`grep -cE '^vault_ui_build_info' <captured-body>` ≥ 1 AND `grep -cE '^go_' <captured-body>` ≥ 1). A hardcoded body cannot satisfy both a named service-owned metric and a real runtime metric.
- [ ] `go.mod` pins vault-cli via `require github.com/bborbe/vault-cli v<semver>` resolved to a tagged release, and `grep -nE 'replace.*vault-cli' go.mod` returns 0 lines; `grep -rn --include='*.go' 'exec.Command' .` returns 0 lines — evidence: negative evidence (both greps return zero results). The repo-wide form is deliberate: the layout has no `cmd/` directory, and a grep naming a missing directory exits 2 — a vacuous pass.
- [ ] The library integration test drives the read ops against a temp vault fixture: `ops.NewListOperation` returns the task entities under the tasks dir, `ops.NewShowOperation` returns one task's frontmatter fields, and `ops.NewTopicShowOperation` returns a topic — evidence: file artifact / test output (per-op entity assertion against the fixture).
- [ ] The library integration test obtains the op set from the composition root — `pkg/factory` returns the full named op set — and drives the goal/topic list ops obtained from it: the list op's `Execute` invoked with the goals dir returns the goal entities and with the topics dir returns the topic entities — the same op, two dirs, two distinct result sets, matching what `goal list` and `topic list` emit today — evidence: test output (the factory-returned set asserted to contain each named op, both result sets asserted, and the two sets differ).
- [ ] The library integration test drives each write op against a temp vault and asserts the resulting vault-file frontmatter: `ops.NewFrontmatterSetOperation` sets the named key to the named value, `ops.NewFrontmatterClearOperation` removes it, `ops.NewWorkOnOperation` sets the session field, `ops.NewDeferOperation` sets `defer_date`, `ops.NewCompleteOperation` sets `status: completed`, and `ops.NewGoalWorkOnOperation` / `ops.NewGoalDeferOperation` / `ops.NewGoalCompleteOperation` / `ops.NewGoalSetOperation(storage.GoalStorage)` / `ops.NewGoalClearOperation(storage.GoalStorage)` / `ops.NewTopicSetOperation(storage.TopicStorage)` / `ops.NewTopicClearOperation(storage.TopicStorage)` produce the same transitions on a goal (and topic) file — evidence: file artifact (one named test per op asserting the concrete expected frontmatter value).
- [ ] `config.Loader` discovers the configured vaults and the discovered vault path is the root the ops are invoked against — evidence: test output (discovered path equals the fixture path passed to the loader).
- [ ] `make test` runs both the Go suite and the existing pytest suite and exits 0; `make precommit` exits 0 with the Python lint/typecheck/test steps unchanged and the Go format/test/vet steps added — evidence: exit code.
- [ ] `docs/dod.md` contains a Go code-quality line alongside the existing Python/JavaScript line — evidence: file content (`grep -n 'Go' docs/dod.md` returns a line naming Go conventions).

**Scenario coverage: no new scenario.** The behavior here is a compilable module plus library calls exercised against a temp-vault fixture — unit and integration tests in the implementation prompts reach it fully. No Docker, no real `gh`, no cluster, no browser.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `go build ./...` — module compiles
- `go vet ./...` — static checks clean
- `go test ./...` — Ginkgo/Gomega unit + integration suite passes (includes the admin-block HTTP assertions and the per-op vault-file assertions)
- `make test` — Go suite + existing pytest suite both pass
- `make precommit` — Python format/lint/typecheck/test unchanged, Go steps added
- `grep -nE 'replace.*vault-cli' go.mod` — returns 0 lines
- `grep -rn --include='*.go' 'exec.Command' .` — returns 0 lines (repo-wide; a `pkg/ cmd/` grep would exit 2 on the missing `cmd/`)
- `grep -n 'Go' docs/dod.md` — returns the new Go line

### Operator-executable (runs on the host after merge, spec verification ladder)

- `make build` — a `make build` target (added by the build-tooling prompt) produces the Go binary on the host toolchain
- Start the Go binary and `curl -s -o /dev/null -w '%{http_code}' localhost:9090/healthz` returns `200`
- `curl -s localhost:9090/metrics | grep -E '^vault_ui_build_info'` returns ≥ 1 line and `curl -s localhost:9090/metrics | grep -E '^go_'` returns ≥ 1 line
- `make run` still starts the Python backend on `:8001` and the board renders unchanged — the Python backend is not regressed by the Go addition

## Desired Behavior

1. A Go module named `github.com/bborbe/vault-ui` (Go 1.27.1) exists at the repo root with `main.go` at the root over a flat `pkg/` package, plus the two conventional exceptions the cited `go-package-layout-guide` mandates: `pkg/factory/` — the composition root, the single place constructing the service and its dependencies — and `pkg/handler/`, holding the admin HTTP handlers.
2. The service exposes the canonical bborbe admin block on `:9090`: `/healthz`, `/readiness`, `/metrics`, `/setloglevel/{level}`, `/gc`, wired per the repo's admin-block convention; `/readiness` reports ready once vault discovery has succeeded.
3. vault-cli is a library dependency, pinned by `require` to a tagged release of `github.com/bborbe/vault-cli`; no `replace` directive exists and no vault-cli subprocess is spawned.
4. Every vault operation the backend needs is wired through vault-cli's exported `ops.New*Operation` constructors — the callable constructors, not the `*Operation` interfaces they return: `ops.NewListOperation`, `ops.NewShowOperation`, `ops.NewFrontmatterSetOperation`, `ops.NewFrontmatterClearOperation`, `ops.NewWorkOnOperation`, `ops.NewGoalWorkOnOperation`, `ops.NewDeferOperation`, `ops.NewCompleteOperation`, `ops.NewGoalDeferOperation`, `ops.NewGoalCompleteOperation`, `ops.NewTopicShowOperation`. The entity set/clear ops are **per-entity** (vault-cli has no generic `EntitySetOperation` / `EntityClearOperation` constructor — those names are interfaces only), so the foundation wires `ops.NewGoalSetOperation(storage.GoalStorage)` / `ops.NewGoalClearOperation(storage.GoalStorage)` and `ops.NewTopicSetOperation(storage.TopicStorage)` / `ops.NewTopicClearOperation(storage.TopicStorage)`, each taking its own `storage.*` type. Op → consumer (today's Python `vault_cli_client` method → the route spec 3 owns): `NewListOperation`(tasks dir)→`list_tasks`/`GET /tasks`; `NewShowOperation`→`show_task`/`GET /tasks/{id}`; `NewFrontmatterSetOperation`→`set_field`/`PATCH /tasks/{id}/*`; `NewFrontmatterClearOperation`→`clear_field`/`DELETE /tasks/{id}/session`; `NewListOperation`(goals dir)→`list_goals`/`GET /goals`; `NewGoalSetOperation`→`set_goal_field`/goal PATCH routes; `NewGoalClearOperation`→`clear_goal_field`/`DELETE /goals/{id}/session`; `NewListOperation`(topics dir)→`list_topics`/`GET /topics`; `NewTopicShowOperation`→`show_topic`/`GET /topics/{id}`. The remaining ops (`NewWorkOnOperation`, `NewDeferOperation`, `NewCompleteOperation`, and their `Goal*` variants) already have today's consumers — `src/vault_ui/api/tasks.py` invokes `vault-cli work-on`, `vault-cli goal work-on`, and the defer/complete fast paths — so they are wired now **ahead of spec 3's route parity**, and the HTTP spec consumes the full op set without re-touching the foundation.
5. Goal listing and topic listing both route through the generic `ops.NewListOperation` with the goals dir and the topics dir respectively supplied by the caller — replicating the cobra glue vault-cli's CLI uses, because no dedicated goal-list or topic-list constructor exists.
6. Vault discovery goes through vault-cli's `config.Loader`; the discovered vault path is the root every ops call is invoked against — vault-ui never resolves vault paths itself.
7. A Ginkgo/Gomega test suite proves the wiring: an integration test drives the ops against a temp vault fixture and asserts the resulting vault-file frontmatter, and an integration test starts the admin block and asserts the five endpoints.
8. `make test` and `make precommit` gain Go steps alongside the existing Python ones; the Python steps and their behavior are unchanged, and `docs/dod.md` gains a Go code-quality line.

## Constraints

- **Never code directly** — this repo's `CLAUDE.md` mandates the dark-factory pipeline for all code changes; the Suggested Decomposition below is what the daemon turns into prompts.
- The existing Python backend must keep working: `make precommit` and `make test` continue to pass the Python format/lint/typecheck/pytest steps until spec 3's cutover.
- vault-cli is pinned by `require` to a tagged release; a `replace ../vault-cli` directive is a MUST-violation — see the coding `go-mod-replace-guide`.
- vault-cli stays the sole vault interface — the Go backend reads and writes no vault file directly; the temp-vault fixture in tests is the only place a file is inspected, and only to assert an op's effect.
- The 3 routes with no vault-cli equivalent (`POST /tasks/{id}/jump`, `POST /cache/reload`, `WS /ws`) are out of this spec's library layer — spec 3 owns their Go replacement.
- Test framework is Ginkgo/Gomega per the coding `go-testing-guide`; package layout follows `go-package-layout-guide`, composition follows `go-factory-pattern`, the admin block follows `go-http-service-guide`, metrics follow `go-prometheus-metrics-guide`, Makefile targets follow `go-makefile-commands`, logging follows `go-glog-guide`. Reference these guides; do not inline their content.
- The spec is larger than the size threshold (8 DBs × 12 ACs); the `## Suggested Decomposition` table below is the bounding mechanism — four prompts, each independently reviewable, together covering the full AC set with no overlap.
- The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — prompts commit nothing and release nothing.

## Assumptions

- vault-cli's ops logic is importable as a **library** at the pinned tag: the `ops.*` constructors and their `storage.*` dependencies are exported from `github.com/bborbe/vault-cli/pkg/ops` and `.../pkg/storage`, so vault-ui calls the same code the CLI itself calls — no subprocess, no `replace`.
- The YOLO container can build Go: image `bborbe/claude-yolo:v0.15.1` ships go1.27.0 with `GOTOOLCHAIN=auto`, which downloads go1.27.1 on demand — so the `go 1.27.1` directive compiles inside the prompt container.
- The named ops are exactly the ones the backend needs: the read/write set above covers the Python `vault_cli_client` methods 1:1, and the extra `WorkOn` / `Defer` / `Complete` (and `Goal*`) ops are wired ahead of spec 3's route parity — no op is invented and none the API spec needs is missing.

## Failure Modes

| Trigger | Expected behavior | Recovery | Detection | Reversibility | Concurrency |
|---|---|---|---|---|---|
| The pinned vault-cli tag is unavailable (module proxy unreachable, tag deleted) | `go build ./...` fails at module resolution with an error naming the missing module version; no partial artifact | Operator pins to the last resolvable tag, or restores proxy access, and re-runs | Non-zero `go build` exit naming the module and version | Reversible | n/a — build-time |
| vault-cli's exported `ops.*` signature drifts in a later release | The pin is not bumped implicitly; `go build` fails at compile time naming the changed symbol — never a silent runtime mismatch | Operator bumps the pin deliberately and fixes the call sites | `go build` compile error naming the symbol | Reversible | n/a |
| A write op crashes mid-write to a vault file | The vault file may hold a partially written frontmatter block; the op returns an error rather than reporting success | Re-run the op (set / complete are idempotent for the same value) or restore the file from obsidian-git history | File diff against the expected frontmatter; the integration test fails | Partial — the file is on disk and may be half-written | vault-cli owns the write; this spec introduces no second writer to the same file |
| Disk full or permission denied while an op writes the vault file | The op returns an error; no silent truncation of the file | Operator frees space or fixes permissions, then re-runs the op | Non-zero op error / failing integration test | Reversible | n/a |
| Clock skew or timezone difference at `defer_date` write time | `defer_date` is written with the date vault-cli computes, not a value vault-ui derives | Operator corrects the date in the vault, or fixes the host clock | File artifact shows an unexpected `defer_date` | Reversible | n/a |
| Both backends running during the transition | The Go admin block binds `:9090` and the Python backend binds `:8001` — distinct ports, no conflict | n/a — by construction | `curl` on each port returns 200 | n/a | n/a |

## Security / Abuse Cases

- The admin block on `:9090` carries unauthenticated `/setloglevel/{level}` and `/gc`, the same as the canonical bborbe admin block. Both are bounded and reversible (the log level can be reset; GC is a runtime hint) — no new exposure beyond the existing norm.
- No HTTP input reaches the vault in this spec: the vault ops are wired as a library, and the only external input is the vault path discovered by `config.Loader` from the operator's own vault-cli config — not attacker-controlled. No path-traversal surface is introduced.
- `config.Loader` reads the vault-cli config file; vault-ui must not accept a vault path from any other source in this spec.
- The invariant that vault-cli is the sole vault interface holds: the Go backend performs no direct vault file I/O, so no file-read or file-write surface is opened outside vault-cli's own validation.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Go module skeleton (`main.go` at root + flat `pkg/` + `pkg/factory/` composition root + `pkg/handler/` admin handlers) + admin block on `:9090` (`/healthz`, `/readiness`, `/metrics`, `/setloglevel/{level}`, `/gc`) + metrics wiring (`vault_ui_build_info` + the Go runtime collector) + admin-block integration test | 1, 2 | 1, 2, 3, 4, 5 | — |
| 2 | vault-cli library dependency pinned by `require` (no `replace`) + `config.Loader` vault discovery + negative guards (no `replace`, no `exec.Command`) | 3, 6 | 6, 10 | prompt 1 |
| 3 | `ops.New*Operation` wiring for the read ops (`ops.NewListOperation`, `ops.NewShowOperation`, `ops.NewTopicShowOperation`), the goal/topic list cobra glue, and the write ops (`ops.NewFrontmatterSetOperation`/`NewFrontmatterClearOperation`, `ops.NewWorkOnOperation`, `ops.NewGoalWorkOnOperation`, `ops.NewDeferOperation`, `ops.NewCompleteOperation`, `ops.NewGoalDeferOperation`, `ops.NewGoalCompleteOperation`, `ops.NewGoalSetOperation`/`NewGoalClearOperation`, `ops.NewTopicSetOperation`/`NewTopicClearOperation`) + Ginkgo/Gomega temp-vault integration tests | 4, 5, 7 | 7, 8, 9 | prompt 2 |
| 4 | Makefile Go targets (`make build` producing the Go binary, `make test` / `make precommit` running both suites) + `docs/dod.md` Go code-quality line | 8 | 11, 12 | prompts 1, 2, 3 |

Rationale: prompt 1 produces a compiling, testable skeleton so later prompts have a factory to register into; prompt 2 establishes the dependency and discovery contract the ops need; prompt 3 is the bulk — the library wiring and its per-op vault-file assertions — and needs both; prompt 4 only touches build tooling and docs and can land last. No cycles: each prompt depends only on earlier ones.

## Do-Nothing Option

vault-ui keeps its Python/FastAPI backend and keeps spawning a `vault-cli` subprocess per vault operation. That is workable today, so the cost is not a broken system — it is a foundation that never gets laid: the private vault logic (spec 2) and the HTTP surface (spec 3) would have no Go module to live in, the per-operation process fork and stdout contract stay, and the tests keep mocking a subprocess boundary instead of exercising vault-cli's real `ops.*` logic. Deferring only defers the same work.

## Verification Result

**Verified:** 2026-10-03T22:55:52Z (HEAD d945617; merged work 124f9ba, tag v0.75.0)
**Binary:** /Users/bborbe/Documents/workspaces/vault-ui/bin/vault-ui (make build; go1.27.1 darwin/arm64)
**Scenario:** no scenario file (spec declares "no new scenario") - replayed the spec's Operator-executable ladder against the freshly built binary plus the in-repo Ginkgo integration suites.
**Evidence:**
- `go build ./...` exit 0; `go vet ./...` exit 0
- live binary on :9090: GET /healthz /readiness /metrics /setloglevel/info /gc all HTTP 200 (curl -w '%{http_code}')
- live GET /metrics: `vault_ui_build_info 1` (1 line) + 35 `go_*` runtime lines (go_gc_duration_seconds, ...)
- `go test -race -v ./...` exit 0: 32 specs / 4 packages, incl. `flips readiness 503 -> 200 on successful discovery` and the per-op temp-vault frontmatter assertions
- `make test` exit 0 (Go ok + 751 pytest passed); `make precommit` exit 0 (go fmt/vet/test + ruff + mypy 20 files)
- `grep -nE 'replace.*vault-cli' go.mod` -> 0 lines; `grep -rn --include='*.go' 'exec.Command' .` -> 0 lines; `go list -m github.com/bborbe/vault-cli` -> v0.159.0
**Verdict:** PASS
