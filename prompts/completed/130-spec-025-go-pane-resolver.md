---
status: completed
spec: [025-lazy-pane-resolution-at-jump-time]
summary: Replaced the python3 who-needs-me.py shell-out with a Go pane.Resolver that matches the harness session registry name against WezTerm pane titles through an injected process boundary, and rewired pkg/factory's composition root onto it.
execution_id: vault-ui-lazy-pane-exec-130-spec-025-go-pane-resolver
dark-factory-version: dev
created: "2026-10-06T06:16:43Z"
queued: "2026-10-06T06:48:20Z"
started: "2026-10-06T07:36:32Z"
completed: "2026-10-06T07:44:23Z"
branch: dark-factory/130-spec-025-go-pane-resolver
---

# Resolve a session's WezTerm pane in Go

<summary>
- A live Claude session is turned into its WezTerm pane id by Go code, with no Python interpreter in the path.
- The pane lookup reads the harness session registry for the session's current name and matches it against the WezTerm pane titles.
- The lookup asks WezTerm once, through a boundary the tests replace with a double, so no test runs the real binary.
- An ambiguous or missing match answers "no pane" instead of guessing, so a wrong terminal is never handed over.
- The jump endpoint and the background refresher both go through this one implementation, so there is a single place that decides what a pane is.
- The Python-invoking helpers in the pane package are deleted; the jump credential and jump-proxy code stay exactly as they are.
</summary>

<objective>
Replace the `who-needs-me.py` shell-out with a Go implementation that resolves a live Claude session id to its WezTerm pane id, so the board stops depending on a Python interpreter and a helper script that the supervisor owns. The seam keeps the shape its callers already use — `Resolve(ctx, sessionID) (string, bool)` — so no caller's contract changes.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-lazy-pane-resolution-at-jump-time.md`. This prompt covers Desired Behavior 1 and AC1's first clause (the `python3`/`.py"` grep). AC1's second clause — the repo-wide `grep -rn 'who-needs-me' --include='*.go' pkg` — still prints `pkg/panecache/panecache.go`'s package comment after this prompt and closes in prompt 2, which deletes that package.

Read these files before writing (current shapes verified):

- `pkg/pane/pane.go` — the package you are extending. `ResolvePaneID` is the Python shell-out you are deleting; `WhoNeedsMePath` and `paneForPrefixLen` exist only to serve it. `BuildSubprocessEnv`, `WeztermBinDir`, `PidAlive`, `WeztermGuiSocket`, `DefaultWeztermBundleDir`, `ReadJumpToken`, `JumpTokenPath`, `PerformJump`, `DefaultJumpServerURL`, `DefaultJumpTimeout`, `SetLogger`/`Logger`/`logDebug` all STAY.
- `pkg/factory/api.go` — `type paneResolver struct{ homeDir, pluginRoot, interpreter string }` and its `Resolve` method (the only caller of `pane.WhoNeedsMePath` / `pane.ResolvePaneID`), plus `CreatePaneRefresher`, whose `Resolver` field is that type today.
- `pkg/factory/mutations.go` — `CreateMutationService` wires `Pane: paneResolver{homeDir: homeDir, interpreter: "python3"}`. This is the jump route's resolver.
- `pkg/panecache/panecache.go` — declares its own `Resolver interface { Resolve(ctx context.Context, sessionID string) (string, bool) }` and does NOT import `pkg/pane`. It must keep compiling unchanged in this prompt.
- `pkg/board/board.go` — `PaneResolver interface { Resolve(ctx context.Context, sessionID string) (string, bool) }`, the second structural consumer of the same seam.
- `pkg/mutations/mutations.go` — `PaneResolver` interface and `Deps.Pane`.
- `pkg/activity/activity.go` — `ReadRegistrySessionIDs(ctx, root)`: the existing registry reader. Note it returns only the session ids; the resolver additionally needs each session's `name`, which is why this prompt adds its own reader.
- `pkg/pane/pane_test.go` — the tests for the helpers you keep, and the tests for `ResolvePaneID` / `WhoNeedsMePath` you delete. Note the suite is `package pane_test` (external), so unexported helpers are exercised through the exported functions.

The supervisor's script is the behavioural reference for the name match. It lives outside this repo (a Claude Code plugin under the host's `~/.claude/`, not under the mounted `~/.claude-yolo`, so it is NOT readable in the container) — the rules you must reproduce are written out in requirement 2 below.

The harness session registry file shape is `<pid>.json` under `~/.claude/sessions/`, carrying at least:

```json
{"pid": 10736, "sessionId": "64b4a415-906d-457f-81f9-48a842007b88", "name": "Fleet Manager", "cwd": "..."}
```

The same directory also holds `<pid>.<hex>.key` files, which are not session entries.

WezTerm's `wezterm cli list --format json` prints a JSON array whose elements carry at least `pane_id` (number) and `title` (string), e.g.

```json
[{"window_id": 0, "tab_id": 0, "pane_id": 7, "title": "✳ Fleet Manager", "is_active": true}]
```

Coding guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md`
</context>

<requirements>

### 1. The resolver seam (`pkg/pane/resolver.go`, new file)

```go
// ExecFunc runs one external command with env and returns its stdout.
type ExecFunc func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)

// RegistryNamesFunc reads the harness session registry under dir and returns
// session id -> the session's current name.
type RegistryNamesFunc func(ctx context.Context, dir string) map[string]string

// ResolverParams carries the resolver's injectable dependencies. Exec and
// RegistryNames fall back to the real implementations when nil; a non-positive
// Timeout falls back to DefaultResolveTimeout.
type ResolverParams struct {
	HomeDir       string
	BundleDir     string
	RegistryDir   string
	Timeout       time.Duration
	Exec          ExecFunc
	RegistryNames RegistryNamesFunc
}

// Resolver resolves a live session id to its WezTerm pane id.
type Resolver interface {
	Resolve(ctx context.Context, sessionID string) (string, bool)
}

// NewResolver creates a Resolver.
func NewResolver(params ResolverParams) Resolver
```

`NewResolver` only fills defaults and stores them — no resolution work.

### 2. The resolution contract

`Resolve` returns `("", false)` for every failure and `(paneID, true)` only on exactly one unambiguous match. Steps, in this order:

1. An empty `sessionID` returns `("", false)` **without calling `Exec` or reading the registry**.
2. Read the registry names for `RegistryDir` (default `filepath.Join(HomeDir, ".claude", "sessions")`). Candidates are the entries whose session id has the requested id as a **case-insensitive prefix** — for a full id this is an exact match, for the 8-char prefix the sweep digest carries it is a prefix match. Zero candidates, or more than one, returns `("", false)`.
3. `name := stripStatusGlyph(candidateName)`. An empty result returns `("", false)`.
4. Run `wezterm cli list --format json` through `Exec`, with `env` built by `BuildSubprocessEnv(os.Environ(), HomeDir, BundleDir, PidAlive)` and bounded by `Timeout`. A non-zero exit, a timeout, or output that does not unmarshal into the pane list returns `("", false)`.
5. Decode into a slice of `struct { PaneID int \`json:"pane_id"\`; Title string \`json:"title"\` }`. Collect the panes whose `stripStatusGlyph(Title)` equals `name`. Exactly one hit returns `(strconv.Itoa(hit.PaneID), true)`; zero hits or more than one returns `("", false)`.

`stripStatusGlyph(text string) string` is an unexported helper: trim surrounding whitespace, then drop leading runes while the first rune is neither alphanumeric nor one of `/~._-`, stripping the whitespace after each dropped rune. This reproduces the supervisor's `strip_status_glyph` and is why `"✳ Fleet Manager"` matches the registry name `"Fleet Manager"`.

### 3. The default `Exec`

`exec.CommandContext(ctx, name, args...)` with `cmd.Env = env`, wrapped in `context.WithTimeout(ctx, Timeout)`; capture stdout; return an error (wrapped with `github.com/bborbe/errors`) on a non-zero exit, naming the exit error and the trimmed stderr. Never build a shell string and never pass argv through a shell.

### 4. The default registry reader

`readRegistryNames(ctx, dir) map[string]string` (unexported): `os.ReadDir(dir)`; an error returns an **empty map** (never nil, never an error) and logs at `glog.V(4)` naming the directory and the error, mirroring `activity.ReadRegistrySessionIDs`. Skip directory entries and any entry whose name does not end in `.json` (the `.key` files are not session entries). Unmarshal each file into `struct { SessionID string \`json:"sessionId"\`; Name string \`json:"name"\` }`; a read or unmarshal failure skips that entry with a `glog.V(4)` line naming the path. Skip entries with an empty `sessionId`. Honour `ctx.Done()` inside the loop.

### 5. Delete the Python-invoking helpers (`pkg/pane/pane.go`)

- Delete `ResolvePaneID`, `WhoNeedsMePath`, and `paneForPrefixLen`.
- Keep `DefaultResolveTimeout` (now the bound on one `wezterm cli list` call — update its doc comment), `DefaultJumpTimeout`, `DefaultJumpServerURL`, `DefaultWeztermBundleDir`, `BuildSubprocessEnv`, `WeztermBinDir`, `PidAlive`, `WeztermGuiSocket`, `ReadJumpToken`, `JumpTokenPath`, `PerformJump`, `SetLogger`, `Logger`, `logDebug`, `isASCIIDigits`, `envValue`, `prependPath`, `setEnv`.
- Rewrite the package doc comment: it currently says "Pane resolution belongs to the supervisor's own who-needs-me.py, so this package shells out rather than re-implementing its resolution order." That is no longer true. Say instead that the package resolves a session's pane itself by matching the harness session registry's name against the WezTerm pane titles, and keeps the jump proxy (the shared credential never reaches the browser).

### 6. Rewire the composition root (`pkg/factory`)

- In `pkg/factory/api.go`: delete `type paneResolver struct{...}` and its `Resolve` method, and delete the comment block that names `who-needs-me.py`. Add a thin `Create*` composition helper and use it everywhere the old type was used:

  ```go
  // CreatePaneResolver returns the Go pane resolver: the session registry under
  // homeDir matched against the WezTerm pane titles.
  func CreatePaneResolver(homeDir string) pane.Resolver {
  	return pane.NewResolver(pane.ResolverParams{
  		HomeDir:     homeDir,
  		BundleDir:   pane.DefaultWeztermBundleDir,
  		RegistryDir: filepath.Join(homeDir, ".claude", "sessions"),
  		Timeout:     pane.DefaultResolveTimeout,
  	})
  }
  ```

  It contains no business logic beyond assembling the params.
- `CreatePaneRefresher`: its `Resolver` field becomes `CreatePaneResolver(homeDir)`.
- `pkg/factory/mutations.go`: `CreateMutationService`'s `Pane:` becomes `CreatePaneResolver(homeDir)`.
- Leave `pkg/panecache` and `pkg/board` untouched in this prompt: they consume the same structural seam and must keep compiling unchanged.

### 7. Tests (`pkg/pane/pane_test.go` plus a new `pkg/pane/resolver_test.go`)

Hand-write the doubles in the test files — a struct of function fields, so a nil field panics on an unexpected call. **Do NOT add a `//counterfeiter:generate` directive**: the repo has no `make generate` target and no `go:generate` wiring, and counterfeiter is not a module dependency, so a directive alone would leave the fake ungenerated — mirror `pkg/panecache/panecache_test.go`'s hand-written `fakeResolver` for this same seam instead. `resolver_test.go` is `package pane_test`, matching `pane_suite_test.go`. This is the documented deviation already recorded in `prompts/completed/126-background-pane-resolution.md`.

- `fakeExec` records every `(env, name, args...)` it is handed and returns canned stdout / an error.
- `fakeRegistryNames` returns a fixed `map[string]string` and records the `dir` it was asked for.
- Delete the tests for `ResolvePaneID` and `WhoNeedsMePath` (including the `timeout-kills-helper` `DescribeTable` entry), and any `_test.go` string that names `who-needs-me.py`.
- Add a `DescribeTable` over `Resolve` covering at least: an exact full-id match; a unique 8-char prefix match; two registry entries sharing the prefix → `false`; an unknown id → `false`; an empty id → `false` with `fakeExec` never called; a registry name carrying a leading status glyph matched against a pane title carrying a different one → the pane id; a title match that yields two panes → `false`; a title match that yields none → `false`; an empty registry name → `false`; a non-zero `wezterm` exit → `false`; malformed JSON → `false`; and an `Exec` that returns a context deadline error → `false`.
- Add one explicit subprocess-boundary assertion: the argv handed to `Exec` is exactly `"wezterm", "cli", "list", "--format", "json"` — this is the contract the WezTerm CLI must keep, and a wrong flag fails silently in production.
- Add a test that the registry reader is asked for `<homeDir>/.claude/sessions` when `RegistryDir` is left empty.
- `pkg/pane` must keep at least 80% statement coverage.

### 8. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes, then walk AC1 against the change and name the command output that establishes it.

</requirements>

<constraints>
- The seam's shape is frozen: `Resolve(ctx context.Context, sessionID string) (string, bool)`. `pkg/board`, `pkg/panecache` and `pkg/mutations` each declare it structurally — do not change their interfaces in this prompt.
- No test spawns a real subprocess, a real `wezterm` invocation, or a real network call; the resolver's process boundary is injected.
- No new external service dependency, no new HTTP route, no new query parameter, no opt-out flag.
- No change to write semantics, to the write queue, or to any route's response body.
- Pane ids come only from the WezTerm CLI's own output; no pane id, session id or registry path is ever taken from request input. The registry directory is server config plus the home directory.
- The jump credential's discipline is unchanged: the token value is never logged and never returned to any caller that would expose it to the browser.
- `who-needs-me.py` is not part of this repo and must not be created, edited, or deleted anywhere on disk.
- bborbe Go conventions per `docs/dod.md`: errors wrapped with `github.com/bborbe/errors`, no `fmt.Errorf`, `Create*` factories carry no business logic, Ginkgo/Gomega tests, `glog` for logging, ≥80 % coverage on new code.
- Concurrency goes through `github.com/bborbe/run`; a bare `go func()` is a violation. Honour `ctx.Done()`.
- Hand-write test doubles; do not add a counterfeiter directive.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- All paths in this prompt and in the code are repo-relative.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0.

```
go test -race ./pkg/pane/... ./pkg/factory/...
```
Must pass.

```
! grep -rn --include='*.go' 'who-needs-me' pkg/pane
```
Must exit 0 (no line names the supervisor's script in this package).

```
! grep -rn --include='*.go' '\.py"' pkg
```
Must exit 0.

```
! grep -rn --include='*.go' 'python3' pkg
```
Must exit 0. Note this is the AC1 grep: it is only fully satisfied once the two `pkg/factory` sites are rewired in requirement 6 — if it still prints a line, requirement 6 is incomplete.

Positive control for the greps (run it once and record the output in your final message, so a silently-broken grep is distinguishable from a passing one): before the change, `grep -rn --include='*.go' 'python3\|\.py"' pkg` prints ten lines — the two `pkg/factory` sites, the two `pkg/pane/pane.go` sites, and six `pkg/pane/pane_test.go` sites; AC1's wording excludes the `_test.go` hits, and after this prompt all ten are gone.

```
go test -cover ./pkg/pane/...
```
Must report ≥ 80.0% statement coverage for `pkg/pane`.
</verification>
