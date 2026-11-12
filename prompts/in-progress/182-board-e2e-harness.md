---
status: approved
execution_id: vault-ui-e2e-exec-182-board-e2e-harness
dark-factory-version: v0.198.17
created: "2026-11-12T23:22:33Z"
queued: "2026-11-12T23:22:33Z"
started: "2026-11-12T23:23:49Z"
completed: "2026-11-12T23:25:42Z"
branch: dark-factory/182-board-e2e-harness
lastFailReason: 'execute prompt: docker run failed: wait command: exit status 1'
---

# Add a Playwright harness that runs the board scenarios

<summary>
- The two board scenarios that ship as markdown stop being hand-walked and become executable
- `make e2e` builds the real binary, serves it on a private port, and drives it in a real browser
- The suite asserts the board's rendered behaviour, not the shape of its markup
- A broken filter, a broken view toggle, or a JavaScript exception fails the suite
- The suite is deliberately outside `precommit`, so ordinary work stays fast
- The parts that need no browser are unit-tested by `precommit` instead of only compiled
- The command fails fast with an actionable message when the live board holds the admin port
- The harness reads a throwaway vault, so it never touches the operator's real one
</summary>

<objective>
Give vault-ui the executable form of the gate `docs/release-process.md` describes: a Playwright suite that runs both scenarios against a real binary, so a render-time regression is caught by a command instead of by an operator remembering to click through a browser.
</objective>

<context>
This repo has **no `CLAUDE.md`** (it is gitignored), and **no `tools.env`** — the Makefile has no `include` directive at all. Do not reference or create either. Read these before writing anything:

- `docs/release-process.md` — the gate this suite implements; its "Why a browser walk and not `make test`" section is the reason the suite exists.
- `scenarios/001-board-views-and-filters.md` and `scenarios/002-task-mutations.md` — the assertions to port. They are the source of truth for what to assert, not a starting point for reinterpretation.
- `docs/frontend-card-render.md` § Test coverage — records that the old Python Playwright lane was removed with the Python backend and that `pkg/board/jsasset` is the current structural lane. That is why this suite is new work rather than a restore.
- `Makefile` — note `build` uses `go build -buildvcs=false`, and `precommit: format vet test`. Do not add `e2e` to it.
- `main.go` — `const adminListen = ":9090"` is bound unconditionally at `factory.CreateHTTPServer(adminListen, readiness)`. There is no flag or env var for it, by design (spec 021 non-goals).
- `pkg/factory/api.go` — `CreateAPIListen` reads `VAULT_UI_LISTEN` (the only port the harness can choose), and `CreateVaultUIConfigPath` resolves the config path **XDG-first**: `$HOME/.config/vault-ui/config.yaml`, falling back to `$HOME/config.yaml`.
- `pkg/factory/factory.go` — `CreateHealthzHandler` serves `/healthz` on the admin port.
- `src/vault_ui/static/app.js` — the behaviour under test. The anchors in requirements 3 and 6 are load-bearing.

There is no e2e package in this repo yet. The reference implementation lives in a sibling repo that is **not mounted in this container**, so its shape is described here rather than linked: a build-tagged package of its own, one `RunSpecs`, a built binary on a free port, a temp data dir, and `playwright-go`.
</context>

<requirements>
1. **Split the package so `precommit` can test half of it.** Everything that needs no browser lives in **tag-free** `e2e/harness.go`; only the Ginkgo specs and the Playwright wiring carry `//go:build e2e`. `e2e/harness.go` must not import `playwright-go` — importing it there would pull the browser dependency into every build.

2. **Add the dependency, in this order.** Write the tagged files that import `github.com/mxschmitt/playwright-go` (requirements 5 and 6) **before** running `go mod tidy` — tidy removes a direct requirement nothing imports yet. Do not put the import in `harness.go` or `harness_test.go`; those stay browser-free so `precommit` can compile and test them.
   - Pin the version in the `Makefile` itself (requirement 7), **not** in an env file: this repo has none.
   - After the importing file exists: `go get github.com/mxschmitt/playwright-go@v0.6201.1 && go mod tidy`.

3. **`e2e/harness.go`** (no build tag) — the browser-free wiring. Use `github.com/bborbe/errors` for wrapping, consistent with `pkg/`, and thread `ctx context.Context` through everything that can block. Contracts:
   - `buildBinary(ctx context.Context, repoRoot, out string) error` — `go build -buildvcs=false -o <out> .` with `cmd.Dir = repoRoot` (matching the `build` target; VCS stamping fails under a masked or worktree `.git`). Resolve `repoRoot` from the test binary's working directory (`filepath.Abs("..")`, since Ginkgo runs in `e2e/`), never from `$HOME` or an env var. On a non-zero exit, return a wrapped error carrying the **tail of the compiler stderr** — a bare `exit status 1` is unactionable.
   - `freePort() (int, error)` — bind `127.0.0.1:0`, read the port, close.
   - `seedFixture(root string) error` — write a throwaway vault under `root` holding at least: two tasks in different statuses, one goal, one topic, and one task with no `claude_session_id`. Use the folder names `25 Tasks`, `24 Goals` and `23 Topics` directly under `root` — `writeConfig` declares those exact names to vault-cli, and a mismatch silently drops the vault. Write the markdown files directly; do not shell out to `vault-cli`.
   - `writeConfig(homeDir, vaultPath string) error` — write **two** files. The config load merges vault-ui's own file with vault-cli's loader and fails if either half is missing:
     - `$HOME/.config/vault-cli/config.yaml` — the path `config.NewLoader("")` resolves. It must carry `current_user: e2e` and one vault entry keyed `fixture` whose `name:` is **also** `fixture`, with `path: <vaultPath>`, `tasks_dir: 25 Tasks`, `goals_dir: 24 Goals`, `topics_dir: 23 Topics`. Without `current_user`, `vaultconfig.Load` fails with `current_user not configured`; without `name:`, the vault is skipped because the resolver matches by vault-cli vault name, not by map key.
     - `$HOME/.config/vault-ui/config.yaml` — the XDG path `CreateVaultUIConfigPath` prefers (the legacy `$HOME/config.yaml` is the fallback), with a `vaults:` block naming `fixture`.
     Pass `HOME=<tmpRoot>` on the binary's `cmd.Env` only — never `os.Setenv`, which would repoint Playwright's own driver cache out of the real home.
   - `startBinary(ctx context.Context, port int) error` — start the built binary with `VAULT_UI_LISTEN=127.0.0.1:<port>` and `HOME=<tmpRoot>` in the environment (there is no `-listen` flag). Poll **`http://127.0.0.1:9090/readiness`** for HTTP 200 for up to 30s: `/healthz` and `/readiness` exist **only** on the fixed admin port, the API router has no health route, and `/readiness` answers 503 until vault discovery finishes. Then issue one `GET /api/vaults` (ctx-bounded, with an `http.Client` timeout) against `http://127.0.0.1:<port>` — that proves both that the API port bound and that the merged config loads, which readiness alone does not (discovery only calls `loader.GetAllVaults`). Make the readiness poll fail fast once the child has exited, otherwise a live board already holding `:9090` answers the poll while the child is dead. On timeout: kill the process and return an error carrying the last readiness response **and** the captured stderr.
   - `stopBinary()` — kill the process and wait for it to exit.
   - Keep the binary path and base URL in package-level vars so the tagged specs can read them.

4. **`e2e/harness_test.go`** (no build tag) — unit tests for the pieces that need no browser: `freePort` returns a port that is currently free (bind it again and assert success); `seedFixture` writes the expected tree (assert the files exist and the sessionless task has no `claude_session_id`); and **`writeConfig` produces a config the production loader accepts** — call `vaultconfig.Load(ctx, factory.CreateConfigLoader(""), filepath.Join(homeDir, ".config", "vault-ui", "config.yaml"))` with `t.Setenv("HOME", homeDir)` after seeding the fixture, and assert it returns exactly one vault whose `Name` is `fixture` (not `VaultName`, the display name) with `TasksFolder == "25 Tasks"` — the seed matters, because `BuildVaultConfig` silently skips a vault whose `tasks_dir` is absent on disk. That last test is the only guard on the two hand-written YAML files, because `make e2e` cannot run in this container. These run under `make precommit`, which is the point of the split in requirement 1. Write them with the plain `testing` package (as `pkg/board/jsasset/card_baseline_test.go` does) and **no Ginkgo `RunSpecs`** — `e2e_suite_test.go` owns the package's single one, and two in the tagged build would panic.

5. **`e2e/e2e_suite_test.go`** (`//go:build e2e`) — `package e2e`, the same package as `harness.go` (never `package e2e_test`: the harness vars are unexported), with one `RunSpecs` (Ginkgo permits exactly one per package). Package doc comment must state that the suite never runs automatically and is not part of `precommit`. `BeforeSuite` builds, reserves a port, seeds the fixture, writes the config, starts the binary, starts Playwright and a Chromium browser; `AfterSuite` tears all of it down. A `BeforeSuite` failure must print the captured binary stderr.

6. **`e2e/board_views_test.go` and `e2e/task_mutations_test.go`** (`//go:build e2e`) — the assertions. Each on **rendered state** (element plus text or URL), never on presence alone. Use Playwright's own waits and `Expect` polling; no fixed sleeps.

   Scenario 001 — eight assertions:
   - default load renders the Tasks view, URL bare or `?view=tasks`
   - each toggle swaps the card set and writes `?view=tasks` / `?view=goals` / `?view=topics`; the URL never keeps a stale view
   - **the goal card carries a ⋮ menu that mutates** — it offers Complete / Defer / Abort and Hold or Resume Goal, plus Reset Session when one is live. Assert the menu opens and its entries are present. The goal card is **not** read-only.
   - the vault, status, **assignee** and live-session filters apply to all three views. Tasks and goals filter server-side; **topics filter client-side** via `matchesAssigneeSelection`, so the assignee filter **does** narrow the Topics view — assert it does.
   - a reload with a non-default view and two filters active restores exactly that state
   - `?view=goals` lands directly in the Goals view with no flash through Tasks — assert `/api/tasks` is absent from `performance.getEntriesByType('resource')` (the invariant `loadCurrentView` already documents in a comment)
   - a vault change reaches the open board without a manual reload, and the card's column moves
   - the console is clean: collect `page.on("pageerror")` and `page.on("console")` and assert zero uncaught errors and zero 5xx `/api/` responses over the walk

   Scenario 002 — five assertions:
   - the control renders the label **`▶ Start`** (`.start-btn`) and posts to `POST /api/tasks/{task_id}/run` without a 5xx or an uncaught exception. The label is `Start`, never `Run` — assert the label, and assert the route from the intercepted request. **Intercept `POST /api/tasks/*/run` and `POST /api/tasks/*/jump` with `page.route` and fulfil them**: the suite must never spawn a real session, so it needs neither `vault-cli` nor `claude` on `PATH`, and `AfterSuite` has no session to close.
   - **a sessionless task renders no jump control at all.** `app.js` returns early unless `kind === 'task' && item.jump_pane`, so there is no click-to-fail path; assert the control is absent. Do not write a test that clicks a jump control expecting an error.
   - jump on a task with a live session moves terminal focus — assert the request reaches `/api/tasks/{id}/jump`
   - a second `▶ Start` on the same task creates no second session and does not report success for work it did not start
   - each card's rendered state after an action matches what the API returned

7. **`Makefile`** — add an `e2e` target, a sibling of `precommit`, never part of it. The module and version are pinned here because there is no env file:
   ```make
   PLAYWRIGHT_GO_MODULE  := github.com/mxschmitt/playwright-go
   PLAYWRIGHT_GO_VERSION := v0.6201.1

   .PHONY: e2e
   e2e:
   	@if lsof -nP -iTCP:9090 -sTCP:LISTEN >/dev/null 2>&1; then \
   		echo "make e2e: port 9090 is held by the live board. Stop it first:"; \
   		echo "  launchctl bootout gui/$$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist"; \
   		echo "  ... run make e2e ..."; \
   		echo "  launchctl bootstrap gui/$$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist"; \
   		exit 1; \
   	fi
   	go run $(PLAYWRIGHT_GO_MODULE)/cmd/playwright@$(PLAYWRIGHT_GO_VERSION) install chromium
   	go test -mod=mod -tags e2e -count=1 -timeout 15m ./e2e/
   ```
   The port check is not optional: the binary binds `:9090` unconditionally, so without it the suite dies on a bind error that reads as a harness bug.

8. **`README.md`** — add `make e2e   # Run the board scenarios in a real browser (needs Chromium and a free :9090)` to the Development block's make-target list, below its `make run` line. The repo's own `docs/dod.md` requires the README to cover changes to usage, so leaving it out makes the agent's own validation step report a blocker.

9. **Update `docs/release-process.md`** so the gate names `make e2e` as what must be green, removes the *"the walk is manual because vault-ui has no browser harness"* caveat, corrects *"a JavaScript exception inside the poll loop reports itself as a connection problem"* to what `app.js` actually does (a caught render error reaches `showToast` plus a console line; an uncaught one keeps the last render, and the `#ws-status` indicator reports WebSocket state, never render state), and states the real precondition — `make e2e` needs the live board stopped because the admin port is fixed.

10. **Remove the manual-walk warnings** from both scenario files: the `⚠️ ... walked by hand` line in `scenarios/001-board-views-and-filters.md` and the capitalised `**Walked by hand in a browser**` one in `scenarios/002-task-mutations.md`. Replace each with a line naming `make e2e` as the runner. Leave the `Setup` / `Action` / `Expected` sections untouched — they are the harness's specification.

11. **`CHANGELOG.md`** — add a bullet under `## Unreleased` (create the heading if it is absent) describing the suite as the executable form of the release gate.

12. **Self-check before finishing:** re-run `<verification>`, and walk each assertion in requirement 6 against the code — confirm every one is on rendered state, and that the jump assertion asserts absence.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- `precommit` stays `format vet test`. Do not add `e2e` to it, and do not add `e2e` to CI.
- Repo-relative paths for every path written into the repo or the docs; no absolute host paths. The one exception is the operator-facing launchd plist path in requirement 7's Makefile recipe (`~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist`), which matches `docs/launchd-service.md`.
- Do not modify `main.go` to add a port flag or env var — spec 021 forbids it. The harness adapts to the fixed admin port.
- Do not point the suite at the operator's real vault or real home dir; every run uses a temp `HOME` and a fixture vault.
- Do not create a `tools.env`, and do not reference a `CLAUDE.md` — neither exists in this repo.
- The `e2e` package is test code; the 80% coverage target in `docs/dod.md` does not apply to it. It applies to the browser-free wiring `harness.go` exposes, via requirement 4 (`freePort`, `seedFixture`, `writeConfig`); `buildBinary`, `startBinary` and `stopBinary` are covered by `make e2e` itself.
- No fixed sleeps for synchronisation.
- Existing tests must still pass.
</constraints>

<verification>
```
ROOTDIR=/workspace make precommit
ROOTDIR=/workspace go vet -tags e2e ./e2e/
test -z "$(gofmt -l e2e/)" || { gofmt -l e2e/; exit 1; }
```
All three must pass. The second proves the tagged package compiles; the third is required because CI runs `gofmt -l .` over the whole tree while `make precommit`'s `go fmt ./...` **skips** build-tagged files — without it a green local run can still fail CI. `make e2e` itself needs a browser and the admin port free, so it is not runnable here and must not be used as the verification command.
</verification>
