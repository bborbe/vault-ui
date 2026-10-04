---
status: verifying
approved: "2026-10-03T20:58:01Z"
generating: "2026-10-03T21:28:07Z"
prompted: "2026-10-03T21:55:28Z"
verifying: "2026-10-04T10:27:46Z"
branch: dark-factory/go-backend-private-logic
---

## Summary

- The rewrite's real cost is the vault-ui-private logic — the modules with no vault-cli counterpart. Ten of them carry the behavior: session liveness, process termination, two process-local registries, session-name resolution, the status cache, the vault-cli watcher supervisor, WezTerm pane resolution, vault topology discovery, and the cleanup sweep policy.
- This spec ports each to a Go package in the new backend with its own tests, reproducing the Python behavior exactly — the Python modules are the contract.
- The destructive path (SIGTERM of a matched `claude` process) gets explicit guard conditions plus a failure-mode table covering the wrong process, an already-exited process, and a permission failure.
- The Python backend keeps serving unchanged until spec 3's cutover; its test suite stays green as the regression lock for the whole port.
- Depends on spec 1's module skeleton; the HTTP surface, WebSocket fan-out, parity harness, and cutover are spec 3.

## Problem

The vault-ui backend is being rewritten in Go. A prior analysis split the work in two: logic vault-cli already offers as a library (cheap to reuse) and logic that lives only in vault-ui (the actual cost). The private half is where the behavior is — whether a card's session is live, when a stale session may be terminated, when a `claude_session_id` is safe to clear, how a WezTerm pane is found, which folders a vault's items live in. None of it has a library equivalent, so it must be re-implemented and re-proved in Go. Porting it without a written contract risks silently changing the liveness and retention behavior the board and the five-minute cleanup sweep depend on — a change that would surface as a card offering "Start" for work already running, or a live session terminated by mistake.

## Goal

A set of Go packages in the new backend, each reproducing one Python module's behavior, each with tests that assert the same classifications, transitions, and refusals the Python tests assert. The Python backend is untouched and still serving traffic; the Go packages are proven in isolation and ready for spec 3 to wire behind the HTTP surface.

## Non-goals

- The Go module skeleton, ops wiring, admin block, and toolchain — spec 1's foundation; this spec adds packages into it.
- The HTTP API surface, WebSocket fan-out, frontend parity harness, and cutover — spec 3. Until spec 3 ships, the Python backend serves all traffic.
- Re-implementing vault-cli's own operations (task CRUD, session management, the `watch` op itself) — those come from the vault-cli library; only the supervisor wrapper around `vault-cli watch` is ported here.
- Any behavior change or new feature — this is a behavior-preserving port.
- New config fields, opt-out flags, or tunable thresholds — the Python constants (liveness window, TTLs, timeouts, grace periods) are ported as-is; no knobs are added.
- Frontend changes of any kind.

## Acceptance Criteria

- [ ] The new Go backend module builds and its test suite passes — `go build ./...` and `go test ./...` each exit 0 from the backend module root (exit code).
- [ ] Session-state classification reproduces the Python contract — unit tests assert `live` / `quiet` / `indeterminate` / `None` for the same fixtures the Python classification tests use, including registry-live-beats-stale-transcript, empty-id-is-None-despite-registry, and stale-transcript-without-process-is-quiet (`go test -run TestClassifySessionState -v` exits 0 and the row `empty-id-is-None-despite-registry` asserts `None` for an empty id even when the registry has an entry, while the row `stale-transcript-without-process-is-quiet` asserts `quiet`).
- [ ] Activity-date computation returns the newer of the task-file mtime and the transcript mtime and `None` only when neither signal exists — `go test -run TestActivityDate -v` exits 0 and the row `newer-signal-wins` asserts the task-file mtime is returned when it is the newer of the two, while the row `neither-signal-is-none` asserts `None` when both mtimes are absent.
- [ ] Display-name resolution reproduces the Python contract — live process table first, the last `custom-title` line wins, an ambiguous match returns None, a missing project dir returns None, a malformed line is skipped (`go test -run TestResolveDisplayName -v` exits 0 and the row `last-custom-title-wins` asserts the UUID taken from the final `custom-title` line, while the row `ambiguous-match-is-none` asserts `None` for two transcripts sharing one title).
- [ ] Process termination issues SIGTERM only for a row that is a claude invocation carrying an exact session-pinning flag for the target id — an absent match sends no signal and returns false, and the launch-termination path never signals a `--resume`-only row (`go test -run TestTerminateGuards -v` exits 0 and the row `matching-row-signals-once` asserts the signal-send spy records exactly one call naming the matched pid, while the row `no-match-signals-nothing` asserts zero calls and a false return, and the row `resume-only-row-signals-nothing` asserts zero calls for a `--resume`-only row).
- [ ] Termination failure paths are non-fatal — a process that exited between the scan and the kill (process-lookup failure) and a permission failure each leave the call returning false without raising (`go test -run TestTerminateFailurePaths -v` exits 0 and the row `process-gone-between-scan-and-kill` asserts the call returns false with no error raised, while the row `permission-denied` asserts false plus a warning-level log line naming the pid and the error).
- [ ] The launch registry reproduces its state machine — begin→in_flight, finish→finished, evict-if-finished drops only a finished record, a new begin clears a take-over mark, two racing begins for one key leave one record (`go test -run TestLaunchRegistry -v` exits 0 and the row `evict-if-finished-drops-only-a-finished-record` asserts the in-flight record survives eviction).
- [ ] The session lock serialises callers sharing one `(vault, item)` key and does not serialise different keys; the entry is evicted only when its holder count returns to zero; a cancelled waiter does not wedge the entry (`go test -run TestSessionLock -v` exits 0 and the row `evict-only-at-holder-count-zero` asserts the entry survives while a holder remains and is gone once the count returns to zero, while the row `cancelled-waiter-does-not-wedge` asserts a later acquirer still obtains the lock).
- [ ] The status cache reproduces frontmatter extraction and invalidation — `status` and the raw `claude_session_started` marker, a YAML boolean `true` normalised to `"true"`, invalidate sets and clears both fields, and a file whose status field was removed drops the item (`go test -run TestStatusCache -v` exits 0 and the row `yaml-true-normalised` asserts the cached marker reads `"true"`, while the row `status-field-removed-drops-item` asserts the item is absent after invalidation).
- [ ] Vault topology discovery reproduces the Python contract — hierarchy folders matched by the `Themes`/`Objectives`/`Goals`/`Tasks` suffix and ordered by category then numeric prefix, the configured tasks folder preferred with other `*Tasks` folders excluded, and the config merge of `config.yaml` with vault-cli `config list` skipping a vault whose tasks folder is absent on disk (`go test -run 'TestHierarchyFolders|TestConfigMerge' -v` exits 0 and the row `suffix-and-numeric-order` asserts folders are returned category-then-prefix ordered, while the row `absent-tasks-folder-skips-vault` asserts that vault is omitted rather than failing the board).
- [ ] The vault-cli watcher supervisor starts one subprocess for all vaults, restarts it after the restart delay when it exits non-zero, stops it with SIGTERM and escalates to kill after the stop timeout, and logs (never raises on) a malformed JSON line (`go test -run TestWatcherSupervisor -v` exits 0 and the row `restart-after-nonzero-exit` asserts a second subprocess is spawned after the restart delay, while the row `malformed-json-line-is-skipped` asserts the callback receives no event and no error is returned).
- [ ] Pane resolution reproduces the Python contract — the jump token is read stripped and `None` when missing or whitespace-only, the `who-needs-me` path prefers `CLAUDE_PLUGIN_ROOT`, the subprocess environment prepends the WezTerm bundle and selects the newest live GUI socket while never mutating the process environment, a resolution exceeding its timeout is killed and returns `None`, and the jump percent-encodes both query values and raises on a non-2xx status (`go test -run TestPaneResolver -v` exits 0 and the row `timeout-kills-helper` asserts the resolution returns `None`, while the row `jump-percent-encodes-both-query-values` asserts both values are encoded in the request URL; negative evidence: the token value never appears in a log line).
- [ ] The cleanup sweep policy reproduces the retention invariant — a valid UUID is cleared only when the launch registry records a launch for the item AND its transcript is absent; a foreign-assignee item and a non-local session are retained; a task's unresolvable display name is retained while a goal's is cleared; an empty id is re-bound only when exactly one live session carries the title AND its transcript is in this vault's project dir; a `claude_session_started` marker older than the 45-minute TTL with no registry record is cleared (`go test -run TestCleanupSweep -v` exits 0 and the row `clear-requires-registry-record-and-absent-transcript` asserts a valid UUID with a launch record but a present transcript is retained, while the row `foreign-assignee-retained` asserts the foreign-assignee id survives).
- [ ] The existing Python backend is untouched and green — `git diff --stat -- src/vault_ui/` returns empty after the port lands and `make test` exits 0 (negative evidence: no Python source diff + exit code). Operator-executable.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `go build ./...` from the backend module root established by spec 1 — exits 0
- `go test ./...` from the backend module root — exits 0
- `go vet ./...` — exits 0
- `gofmt -l .` — prints nothing
- `go test -race ./...` — exits 0 (exercises the registry and lock concurrency)
- `grep -rn '5[[:space:]]*\*[[:space:]]*time.Minute\|45[[:space:]]*\*[[:space:]]*time.Minute\|120[[:space:]]*\*[[:space:]]*time.Second' <backend-module>/` — the ported window, marker TTL, and orphan grace constants appear with the Python values (five minutes, 45 minutes, 120 seconds), proving the values were carried over rather than invented; the Go identifiers are not required to match the Python names (`liveWindow` / `startingMarkerTTL` / `orphanGrace` are expected), so a name-grep is not the check — the value is

### Operator-executable (runs on the host after merge, spec verification ladder)

- `make precommit` at the repo root — the existing Python format/test/lint/typecheck still pass; the Python backend keeps serving until spec 3's cutover
- `make test` — the pytest suite is green
- Manual read of the termination guard against this spec's Failure Modes rows for wrong-process, already-exited, and permission-denied; the guard is the one irreversible action in the port and is reviewed by a human before spec 3 wires it behind an endpoint

## Desired Behavior

1. **Session identity and liveness.** A package classifies a card's Claude session as `live` / `quiet` / `indeterminate` / `None` from three signals in order: the harness session registry (authoritative, checked first), transcript recency within a five-minute window, and a live `--resume` / `--session-id` process cross-check. A session id with no transcript and no registry entry is `indeterminate`, never `quiet`. The same package resolves a non-UUID display name to its UUID from the live process table first, then from the last `custom-title` line of each transcript, refusing an ambiguous match, and computes the activity date as the newer of the task-file mtime and the transcript mtime.
2. **Process termination guards.** A destructive package issues SIGTERM only under named guards: the target is resolved from a fresh (uncached) process scan immediately before the signal; the row must be a claude invocation carrying an exact session-pinning flag for the resolved id; the take-over path signals only a launch row (`--session-id`) and never an interactive resume row (`--resume`); the launcher wrapper is never a target. When no row matches, nothing is signaled and the call reports false.
3. **Process-local registries.** Two in-memory registries reproduce their Python state machines: the launch registry maps `(vault, item)` to in-flight/finished with kind, supports take-over marking, evicts only a finished record, and drops a stale take-over mark on a fresh begin; the session-lock registry hands out one lock per `(vault, item)`, evicts an entry only when its holder count returns to zero, and unwinds a cancelled waiter. Both are sound only under a single-process server and must preserve that assumption.
4. **Vault topology discovery.** A package discovers a vault's hierarchy folders by the `Themes`/`Objectives`/`Goals`/`Tasks` suffix, orders them by category then numeric prefix, prefers the configured tasks folder while excluding other `*Tasks` folders, and merges the vault-ui `config.yaml` with vault-cli's `config list` output — skipping a vault whose tasks folder is absent on disk rather than failing the whole board.
5. **Status cache.** An in-memory cache loads each discovered folder's items, extracting `status` and the raw `claude_session_started` marker, invalidates a single item on a watcher event (set, clear, or drop when the status field is gone), and serves the marker to the cleanup sweep and the API enrichment path.
6. **vault-cli watcher supervisor.** A supervisor runs one `vault-cli watch` subprocess for all configured vaults, dispatches each parsed JSON event to a callback, restarts the subprocess after the restart delay when it exits non-zero, and stops it with SIGTERM escalating to kill after the stop timeout.
7. **WezTerm pane resolution.** A package reads the jump credential from its file (never logging or returning the value to a caller that would expose it), resolves the `who-needs-me` script path, builds a subprocess environment that prepends the WezTerm bundle directory and points `WEZTERM_UNIX_SOCKET` at the newest live GUI socket without mutating the process environment, resolves a session's pane within a bounded timeout (killing a wedged helper), and proxies the jump with both query values percent-encoded.
8. **Cleanup sweep policy.** A package reproduces the five-minute sweep's retention invariant and its three secondary passes (empty-id re-bind, orphaned-marker TTL, resurrected-marker re-clear) for tasks and goals, with each awaited subprocess bounded so one stuck helper cannot freeze the pass.

## Constraints

- **Never code directly.** This repository's `CLAUDE.md` mandates the dark-factory pipeline for all code changes: spec → approved → auto-generated prompts → audited → daemon. No prompt in this spec may be executed by hand-editing source.
- **Behavior parity is the contract.** Each ported package reproduces its Python module's behavior; the Python source and its pytest suite are the reference. No behavior may be added, removed, or "improved".
- **The Python backend keeps working until spec 3's cutover.** No change under `src/vault_ui/` in this spec; the pytest suite stays green.
- **Single-process assumption.** The launch registry and the session-lock registry are sound only because the server runs one worker. The Go port must preserve that assumption and must not introduce multi-worker sharing that would silently reintroduce the races they exist to close.
- **Time is injectable.** Every window, TTL, grace period, restart delay, and timeout (five-minute liveness window, 45-minute marker TTL, 120-second orphan grace, five-second restart and stop timeouts, ten-second field timeout, five-second resolve and jump timeouts) is a parameter, not a wall-clock read inside the logic — follow `go-time-injection`.
- **Coding guides** (read before writing the affected code, do not inline): `go-testing-guide`, `go-concurrency-patterns`, `go-context-cancellation-in-loops`, `go-time-injection`, `go-factory-pattern`, `go-package-layout-guide`, `go-error-wrapping-guide`.
- **Errors are wrapped and classified** so a caller can distinguish an expected absence (no token, no pane, no matching process) from an unexpected failure — follow `go-error-wrapping-guide`.
- **This spec depends on spec 1** for the Go module layout, toolchain, and container build; the package paths and module root it establishes are the ones used here.
- `config.yaml` format and the vault-cli `config list` JSON contract are frozen — the port consumes them, it does not change them.
- The `docs/starting-marker-lifecycle.md` marker lifecycle (set and clear paths, concurrent writers) is the frozen contract for the cleanup sweep's marker handling.
- **The ported rules get a durable doc home.** The liveness-classification table (`live` / `quiet` / `indeterminate` / `None`, and the signal behind each) and the cleanup retention invariant currently live only in the Python source, which the sibling API spec deletes at cutover; this spec must capture both in `docs/` — a sibling to the frozen `docs/starting-marker-lifecycle.md` — and reference them from the spec, so the Go port and the cutover keep a written contract once the Python reference is gone.
- **Constants carry values, not Python names.** The five-minute liveness window, 45-minute starting-marker TTL, and 120-second orphan grace are frozen values (checked in Verification); the Go identifiers may be idiomatic (`liveWindow`, `startingMarkerTTL`, `orphanGrace`), so the port is verified by value, never by a name-grep that would fail a correct port.
- **Size is over the audit threshold (8 domains × 14 ACs = 112, against a threshold of 50) and is deliberately not split** — the `## Suggested Decomposition` table is the bounding mechanism: it slices the work into six prompts with declared dependencies, so no single prompt carries the whole spec.

## Failure Modes

| Trigger | Expected behavior | Detection | Reversibility | Concurrency | Recovery |
|---|---|---|---|---|---|
| Termination: no process matches the target id | No signal is sent; the call returns false; the session is treated as already quiet | Caller log line naming the session id and the false result | n/a — nothing acted on | A concurrent take-over of the same id is idempotent: the second finds no process and returns false | None needed — the resume path proceeds |
| Termination: the process exited between the fresh scan and the signal | The lookup failure is swallowed as an expected absence; no raise; the call returns false | Debug log line naming the pid and the session id | Irreversible but already occurred (process is gone) | Two take-overs race harmlessly — only one signal can land | None — the process is already dead |
| Termination: permission failure signaling the pid | The failure is logged at warning level; no raise; the call returns false | Warning log line naming the pid, the session id, and the error | Irreversible — the process was not terminated | — | Operator confirms `grep -c 'permission denied' <log>` returns ≥ 1 line naming the pid and the session id, the card still shows the session as live, and the take-over returns false |
| Termination: the resolved row is an interactive `--resume` process, not a launch | The launch-termination path never signals it; it returns the card's own id with false | Test assertion that the signal-send spy records zero calls for a resume-only row | n/a — nothing acted on | — | The take-over returns the card's own id and the launch registry holds no take-over mark for the item (a fresh begin is not suppressed) |
| Status cache: a file is unreadable or its frontmatter is malformed | The item contributes nothing to the cache; no crash; the sweep continues | Debug log line naming the file | Reversible — the next load retries | A concurrent watcher invalidation and a full load both end in a consistent map | Operator confirms the repaired file's item is present again on the next map read after an invalidation or load |
| Config discovery: vault-cli is unavailable or returns non-JSON | Startup fails fast with a named error rather than serving an empty board | Error raised at load time naming the failing command and its output | Reversible — restart after fixing vault-cli | — | Operator confirms `vault-cli config list` exits 0 and the restarted process serves the board with no startup-error log line |
| Watcher: the `vault-cli watch` subprocess exits non-zero | The supervisor logs the exit code and restarts it after the restart delay; the stop flag suppresses a restart during shutdown | Error log line naming the vaults and the exit code | Reversible — the subprocess is restarted | A stop during a restart window sets the stop flag so no new subprocess is spawned | The next restart cycle spawns a subprocess with a new pid; a repeating exit code shows as ≥ 2 consecutive error log lines naming the same exit code |
| Watcher: a line is not valid JSON | The line is logged at warning level and skipped; the event stream continues | Warning log line carrying the raw line | n/a — the line is discarded | — | None — the next well-formed line is processed |
| Pane resolution: the helper script hangs past its timeout | The helper is killed and the call returns `None`; the HTTP request that was waiting is not held open | Debug log line naming the script and the session id | Reversible — the next request retries | Two concurrent resolutions each spawn their own bounded helper | Operator confirms the helper pid is gone (`ps -p <pid>` prints nothing) and a subsequent resolution returns a non-`None` pane id |
| Pane resolution: the jump token is missing, unreadable, or whitespace-only | The call returns `None` and the jump is refused; the token value is never logged | Debug log line naming the token path only, never the value | Reversible — the operator restores the file | — | Operator confirms the token file mode reads `0600` (`stat -f '%Sp' <path>` → `-rw-------`) and the next jump returns a 2xx |
| Cleanup: the sweep crashes mid-pass after some writes | The next five-minute sweep re-runs the pass; each action is idempotent; launch-registry records for failed clears are retained so the clear is retried | Info log line at pass end naming the count cleared; retained records visible in the registry-size line | Partial — some items were written before the crash; a retry converges | A concurrent launch calling begin for an item whose record is mid-eviction keeps its fresh record (evict-if-finished re-checks) | Automatic on the next sweep |
| Cleanup: a `vault-cli task set` helper hangs | The child is killed and reaped, the field is left untouched on disk, and the next sweep retries | Warning log line naming the task, the vault, and the timeout | Reversible — the field was not changed | The kill-and-reap keeps no zombie; the per-item lock is released | Automatic on the next sweep |
| Clock skew: a marker carries a naive timestamp or the legacy literal `true` | A naive timestamp is assumed UTC; an unparseable marker is treated as expired (it predates the release that introduced timestamps) | Info log line reporting the marker age as `unknown` when it could not be parsed | Irreversible — the marker is cleared | — | Operator confirms the card's status-cache entry carries no marker and the launch registry still holds the item's record (a live launch is skipped, not cleared) |
| Resource exhaustion: the board renders many cards | The process table is scanned once per 30-second window for the board's read paths, not per card; only the rare take-over path scans fresh | Debug log line on a scan failure; the cached table is reused within the window | n/a | The cache is module state shared by all read paths; both derived views come from one scan | None needed — the cache self-expires after its window |

## Security / Abuse Cases

- **The jump credential never leaves the server.** The token is read from a `0600` file, is never logged (not even truncated, not on the error path), and is never returned to the browser — the jump is proxied server-side. The only value that reaches a log is the token path.
- **The termination path cannot be aimed at an arbitrary pid.** The pid comes from the matched `ps` row, never from the request; the request supplies a session id that must match an exact UUID already present in the host process table, and the row must be a claude invocation. A caller cannot name a pid, a process name, or a signal.
- **Untrusted `ps` and transcript content is parsed defensively.** A `ps` row counts only when it matches the exact session-pinning-flag-plus-UUID pattern and contains the claude token. A transcript title is matched against the display name but the returned UUID is the filename stem, so a title containing path separators cannot escape the project directory (the Python test suite already asserts this).
- **Subprocess arguments are passed as argv, never a shell.** Vault names, item ids, and field values reach `vault-cli` as discrete arguments; no shell interpolation, so an item id containing shell metacharacters is inert.
- **The WezTerm socket is validated before use.** A `gui-sock-<pid>` candidate counts only when the pid is alive and signalable and the suffix is ASCII digits; a stale socket file left by an exited GUI is skipped rather than handed to `wezterm cli`.
- **What can hang or retry forever:** the pane-resolution helper (bounded, killed on timeout), the cleanup pass's awaited subprocesses (each bounded; a timeout leaves the field untouched and the next sweep retries), and the watcher subprocess (restarts on a delay, suppressed during shutdown). No path retries without a bound.
- **What must be validated:** the session id (must be a UUID or a resolvable display name, and must not contain a path separator before any file lookup), the token file (exists, readable, non-empty), and the vault-cli config output (parsed as a list of objects, non-JSON treated as a startup error).

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Session identity, liveness, activity date, and display-name resolution (Go package + tests) | 1 | 2, 3, 4 | spec 1 (module layout) |
| 2 | Process termination guards (fresh scan, launch-vs-resume distinction, non-fatal failure paths) | 2 | 5, 6 | prompt 1 (reuses the process-table parsing) |
| 3 | Process-local registries: launch registry and session-lock registry | 3 | 7, 8 | — |
| 4 | Vault topology discovery (hierarchy folders + config merge) and the status cache | 4, 5 | 9, 10 | — |
| 5 | External process bridges: vault-cli watcher supervisor and WezTerm pane resolution | 6, 7 | 11, 12 | prompt 4 (uses vault config) |
| 6 | Cleanup sweep policy + parity regression lock (module build/test, Python suite untouched) | 8 | 1, 13, 14 | prompts 1–5 |

Rationale: prompt 1 establishes the process-table and transcript parsing every later prompt reuses; prompt 2 is the destructive path and depends on prompt 1's parser; prompts 3 and 4 are independent of each other and of prompt 1; prompt 5 needs prompt 4's vault config; prompt 6 composes all of them and carries the cross-cutting parity lock, so it runs last. Prompt 2 is deliberately separate from prompt 1 so the SIGTERM guard gets its own review before spec 3 wires it behind an endpoint.

## Do-Nothing Option

The Python backend keeps working, but the Go rewrite cannot progress: spec 3's cutover has nothing to wire behind its HTTP surface, and the private logic — liveness, retention, pane resolution, topology — would have to be re-derived from the Python source at cutover time, under time pressure, with the board's own behavior as the only test. The cost of this spec is one port with tests; the cost of skipping it is discovering a liveness or retention regression after the Python backend has been removed and there is no reference left to compare against.

## Verification Result

**Verified:** 2026-10-04T11:57:08Z (HEAD dcf1551)
**Binary:** none — library port, no binary; verified via `go test` from the module root
**Scenario:** no scenario file (spec declares none) — replayed each AC's named test command against this worktree, plus repeated full-suite and `-race` runs.
**Evidence:**
- `go build ./...` exit 0; `go vet ./...` exit 0; `gofmt -l .` prints nothing
- `go test ./... -count=1` 25/25 clean; `go test -race ./... -count=1` 10/10 clean; `make test` 5/5 exit 0
- `go test ./pkg/watcher/ -count=1` 40/40 clean and `-race` 30/30 clean (was 3/30 and 4/30 before dcf1551); no DATA RACE in any captured run
- `go test ./pkg/pane/ -run TestPaneResolver -v -count=1` 15/15 and `-race` 15/15 clean (was the flaky suite)
- named rows pass: TestClassifySessionState 34/34, TestActivityDate 14/14, TestResolveDisplayName 16/16, TestTerminateGuards 10/10, TestTerminateFailurePaths 4/4, TestLaunchRegistry 13/13, TestSessionLock 4/4, TestStatusCache 9/9, TestHierarchyFolders 6/6, TestConfigMerge 24/24, TestCleanupSweep 55/55
- `git diff --stat -- src/vault_ui/` empty; `uv run pytest -q` 751 passed
**Verdict:** PASS
