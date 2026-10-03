---
status: draft
spec: [022-go-backend-private-logic]
created: "2026-10-03T23:45:00Z"
branch: dark-factory/go-backend-private-logic
---

# Add the session liveness, activity-date, and display-name resolution packages

<summary>
- The Go backend can now answer "is this card's Claude session running right now?" with the same four answers the Python backend gives: live, quiet, indeterminate, or none.
- Liveness is decided from three signals in a fixed order — the harness session registry first, transcript recency within a five-minute window second, and a live resume/launch process cross-check third — so an alive-but-idle worker reads live instead of quiet.
- A card with a session id but no transcript on this host reads indeterminate, never quiet, so the board never offers a Resume it cannot honor.
- A card's activity date is the newer of its task-file modification time and its session transcript's modification time, and is absent only when neither signal exists.
- A non-UUID session display name resolves to its real id: the live process table wins, the last custom-title line of each transcript is the fallback, and an ambiguous tie refuses to resolve rather than guessing.
- The process table is parsed once per 30-second window for the board's read paths, so a board full of cards does not shell out once per card.
- Every window, timeout, and clock read is injected, so the classification is deterministic under test.
- The liveness classification is captured in a durable document beside the existing marker-lifecycle doc, so the contract survives the Python backend's removal.
- The Python backend is untouched and keeps serving traffic; these packages are proven in isolation.
</summary>

<objective>
Port the Python session-liveness, activity-date, and display-name resolution logic into three new Go packages (`pkg/session`, `pkg/activity`, `pkg/sessionresolver`) with Ginkgo/Gomega tests that reproduce the exact classifications, refusals, and ordering the Python modules and their pytest suites assert — so spec 3 can wire them behind the HTTP surface with a proven contract.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container (they are the contract for this change):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` by default; subpackages only on a real split trigger (this spec adds enough files to fire the "too many files to navigate" trigger, and each package below owns one cohesive domain).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package (`package X_test`), a `<pkg>_suite_test.go` entry-point, `DescribeTable`/`Entry` (never stdlib `t.Run` tables), UTC time, suite timeout.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md` — inject time; `github.com/bborbe/time` (`libtime`), never `time.Now()` in production code.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; expected absences (no match, no transcript, no registry entry) are NOT errors and must not be wrapped as failures.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — long loops/filesystem scans carry a non-blocking context check.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.

Read the spec `specs/in-progress/022-go-backend-private-logic.md` (Desired Behavior 1; Acceptance Criteria 2, 3, 4; Failure Modes rows for the process table and clock skew; Constraints; Security / Abuse Cases).

Read the Python source that is the behavior contract — reproduce it exactly, do not "improve" it:
- `src/vault_ui/session_resolver.py` — `is_uuid`, `resolve_session_id`, `_UUID_RE`, `_MAX_LINE_BYTES`.
- `src/vault_ui/activity.py` — `LIVE_WINDOW`, `_PS_CACHE_TTL_SECONDS`, `_SESSION_ID_FLAG_RE`, `_SESSION_NAME_RE`, `read_registry_session_ids`, `transcript_mtime`, `compute_activity_date`, `_parse_live_session_ids`, `_parse_live_session_names`, `_parse_live_processes`, `_parse_launch_processes`, `_parse_launch_names`, `classify_session_state`.
- The matching pytest suites are the regression lock: `tests/test_session_resolver.py`, `tests/test_activity.py`. Every assertion there must have a Go counterpart.

Read the already-generated spec-021 prompts `prompts/1-spec-021-go-module-skeleton-admin-block.md` and `prompts/2-spec-021-vault-cli-dependency-and-discovery.md` for the module layout and prompt style.

The module is `github.com/bborbe/vault-ui` (Go 1.27.1) at the repo root, established by spec 021: `main.go` at the root, a flat `pkg/` package named `vaultui`, plus `pkg/factory/` and `pkg/handler/`. The new packages below are added alongside those. `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` are already direct dependencies of the module.

**Package-per-AC-name rule (read this before choosing a layout).** Ginkgo allows exactly ONE `RunSpecs` per test binary, so one Go package can expose exactly one suite entry-point. The spec pins a distinct `go test -run <Name>` per behavior, so each AC test name below becomes its OWN package whose suite entry-point function is named exactly after that AC (`TestClassifySessionState`, `TestActivityDate`, `TestResolveDisplayName`) — not the guide's default `TestSuite`. Do NOT merge these three into one package and do NOT rename the entry-points.
</context>

<requirements>

### 1. Create `pkg/session/` — session liveness classification and process-table parsing

Package name `session`. Files: `session.go` (state type), `classify.go` (`ClassifySessionState`), `process.go` (ps parsing + cached table), `session_suite_test.go`, `classify_test.go`, `process_test.go`.

Port `classify_session_state` and the `ps`-parsing helpers from `src/vault_ui/activity.py`. Reproduce the three-signal order exactly (registry → transcript recency → live-process cross-check).

Exported contract:

```go
package session

import (
    "context"
    "time"

    libtime "github.com/bborbe/time"
)

// SessionState is the classification of a card's Claude session.
// The empty value is the Python None: the card carries no claude_session_id.
type SessionState string

const (
    SessionStateNone          SessionState = ""
    SessionStateLive          SessionState = "live"
    SessionStateQuiet         SessionState = "quiet"
    SessionStateIndeterminate SessionState = "indeterminate"
)

// DefaultLiveWindow is the transcript-recency window from the Python LIVE_WINDOW
// (five minutes). It is the default; the value is always passed explicitly to
// ClassifySessionState so tests and callers can vary it.
const DefaultLiveWindow = 5 * time.Minute

// ClassifyParams carries one classification request. ResumeSessionIDs and
// RegistrySessionIDs are computed by the caller (from ProcessTable and
// activity.ReadRegistrySessionIDs); ClassifySessionState performs no ps scan and
// no registry read itself.
type ClassifyParams struct {
    SessionID          string
    ProjectDir         string
    ProjectsRoot       string
    Now                libtime.DateTime
    LiveWindow         time.Duration
    ResumeSessionIDs   []string
    RegistrySessionIDs []string
}

// ClassifySessionState reproduces src/vault_ui/activity.py classify_session_state.
func ClassifySessionState(ctx context.Context, params ClassifyParams) SessionState

// ProcessScanner returns fresh `ps` output. The production scanner runs ps;
// tests inject a fixed string.
type ProcessScanner func(ctx context.Context) (string, error)

func ParseLiveSessionIDs(psOutput string) []string
func ParseLiveSessionNames(psOutput string) map[string]string
func ParseLiveProcesses(psOutput string) map[string]int
func ParseLaunchProcesses(psOutput string) map[string]int
func ParseLaunchNames(psOutput string) map[string]string

// ProcessTable is the raw-ps cache (one TTL for the whole board, not per card),
// from which both derived views are parsed.
type ProcessTable interface {
    LiveSessionIDs(ctx context.Context) []string
    LiveSessionNames(ctx context.Context) map[string]string
}

// NewProcessTable caches one ps scan for cacheTTL. The scanner is injectable; the
// clock is injected (never time.Now()).
func NewProcessTable(scanner ProcessScanner, cacheTTL time.Duration, currentDateTime libtime.CurrentDateTimeGetter) ProcessTable

// NewPSScanner returns a ProcessScanner that runs `ps` with the given args
// (production read paths use `-axww`, `-o`, `args=`).
func NewPSScanner(args ...string) ProcessScanner
```

Behavior notes to reproduce exactly (from `activity.py`):
- `ClassifySessionState`: empty `SessionID` → `SessionStateNone`. If `SessionID` is in `RegistrySessionIDs` → `live` (registry is authoritative, checked FIRST, before any transcript lookup). Else look up the transcript mtime (project dir first, then a glob under `ProjectsRoot`); no transcript → `indeterminate`. Else if `Now - mtime <= LiveWindow` → `live`. Else `live` if `SessionID` is in `ResumeSessionIDs`, else `quiet`. Normalise a zero-location `Now` to UTC.
- `_SESSION_ID_FLAG_RE` = either `--resume` or `--session-id` followed by an exact UUID; a `ps` row counts only when it contains the `claude` token. `_SESSION_NAME_RE` captures `-n <name>` where the name runs to the next flag or end of line.
- `ParseLiveSessionNames` omits any name bound to two different ids in one scan (ambiguous).
- `ParseLiveProcesses` reads `ps -o pid=,args=` output (first whitespace field is the PID); a non-numeric PID prefix is skipped, not fatal.
- `ParseLaunchProcesses`/`ParseLaunchNames` use ONLY `--session-id` rows (a `--resume` row is an interactive resume, never a launch).
- `ProcessTable.LiveSessionIDs`/`LiveSessionNames` re-scan only when the cached scan is older than `cacheTTL`.

Do NOT run `ps` from `ClassifySessionState`; it receives the derived sets. Do NOT call `time.Now()`; use the injected getter.

### 2. Create `pkg/activity/` — transcript mtime, activity date, session registry

Package name `activity`. Files: `activity.go`, `activity_suite_test.go`, `activity_test.go`.

Port `transcript_mtime`, `compute_activity_date`, and `read_registry_session_ids` from `src/vault_ui/activity.py`.

Exported contract:

```go
package activity

import (
    "context"

    libtime "github.com/bborbe/time"
)

// TranscriptMtime returns the mtime of the session transcript, checking
// projectDir first and then every project directory under projectsRoot.
// It returns nil for a missing/blank session id and for a transcript that
// cannot be found or read (an expected absence, never an error).
func TranscriptMtime(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime

// ComputeActivityDate returns the newer of the task-file mtime and the transcript
// mtime; nil only when both signals are absent. A nil modifiedDate is an absent
// task-file mtime.
func ComputeActivityDate(ctx context.Context, modifiedDate *libtime.DateTime, sessionID, projectDir, projectsRoot string) *libtime.DateTime

// ReadRegistrySessionIDs returns the sessionId of every entry in the harness
// session registry directory. Never raises: a missing/unreadable directory, an
// unreadable file, a non-JSON file, and a file with no sessionId each contribute
// nothing.
func ReadRegistrySessionIDs(root string) []string

// DefaultRegistryRoot returns the harness registry root (`~/.claude/sessions`).
func DefaultRegistryRoot() string

// DefaultProjectsRoot returns the transcript projects root (`~/.claude/projects`).
func DefaultProjectsRoot() string
```

Behavior notes: `ComputeActivityDate` treats a location-less (`naive`) `modifiedDate` as UTC before comparing; `TranscriptMtime` scans the fallback root only after the direct project-dir lookup misses. Deliberately uncached, exactly as the Python docstring says.

### 3. Create `pkg/sessionresolver/` — display-name to UUID resolution

Package name `sessionresolver`. Files: `resolver.go`, `resolver_suite_test.go`, `resolver_test.go`.

Port `is_uuid` and `resolve_session_id` from `src/vault_ui/session_resolver.py`.

Exported contract:

```go
package sessionresolver

import "context"

// IsUUID reports whether value matches the 8-4-4-4-12 hex UUID format.
func IsUUID(value string) bool

// ResolveSessionID maps a non-UUID display name to its real UUID. The live
// process table is consulted first; otherwise each `.jsonl` transcript in
// projectDir is scanned and a session's current title is the customTitle of the
// LAST "custom-title" line carrying a customTitle key. Returns (uuid, true) only
// for exactly one current match; (\"\", false) for no match, for an ambiguous tie,
// and for a missing project directory. Malformed JSON lines and over-long lines
// are skipped.
func ResolveSessionID(ctx context.Context, displayName, projectDir string, liveSessionNames map[string]string) (string, bool)
```

Behavior notes to reproduce exactly: a `custom-title` line WITHOUT a `customTitle` key must not erase the previous title; a line longer than 4096 bytes is skipped; the returned UUID is always the filename stem, so a `customTitle` containing path separators is only ever string-compared and never used to build a path.

### 4. Tests — three Ginkgo suites

Every package with `*_test.go` needs its own `<pkg>_suite_test.go` with the suite entry-point named exactly as the AC below (not `TestSuite`). Use the standard suite body (`time.Local = time.UTC`; `format.TruncatedDiff = false`; `RegisterFailHandler(Fail)`; `suiteConfig, reporterConfig := GinkgoConfiguration()`; `suiteConfig.Timeout = 60 * time.Second`; `RunSpecs(t, "<Name>", suiteConfig, reporterConfig)`).

**`pkg/session` — suite entry `TestClassifySessionState`.** Mirror `tests/test_activity.py`'s classification and parsing cases. Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions — they are the spec's contract):
- `empty-id-is-None-despite-registry` — an empty session id classifies `SessionStateNone` even when `RegistrySessionIDs` contains an entry.
- `stale-transcript-without-process-is-quiet` — a stale transcript and no matching resume id classifies `SessionStateQuiet`.
Also cover: live within window; quiet older than window; transcript exactly `LIVE_WINDOW` old is live, one second past is quiet; indeterminate when no transcript; registry-live beats a stale transcript; registry-live without a transcript reads live; a fresh task-file mtime does NOT make a session live (liveness is transcript-only); missing registry equals the transcript/ps model; and the five `Parse*` helpers against the real `ps` rows quoted in `tests/test_activity.py` (headless `--session-id`, interactive `--resume`, `--resume` by name, no-flag, and the launcher row whose `-n <name>` is the final argument).

**`pkg/activity` — suite entry `TestActivityDate`.** Mirror `tests/test_activity.py`'s activity-date and registry cases. Rows (exact entry descriptions):
- `newer-signal-wins` — the task-file mtime is returned when it is the newer of the two signals.
- `neither-signal-is-none` — `ComputeActivityDate` returns nil when both mtimes are absent.
Also cover: transcript found in the project dir; transcript found via the projects-root glob fallback; transcript missing returns nil; no/blank session id returns nil; a stale file with a fresh transcript returns the transcript time; a fresh file with a dead transcript returns the file time; a nil modified date falls back to the file mtime; a naive (location-less) modified date is accepted and normalised to UTC; `ReadRegistrySessionIDs` returns the ids, is empty for a missing directory, skips a malformed/unreadable/sessionless entry, and still returns the valid ones.

**`pkg/sessionresolver` — suite entry `TestResolveDisplayName`.** Mirror `tests/test_session_resolver.py`. Rows (exact entry descriptions):
- `last-custom-title-wins` — the UUID comes from the final `custom-title` line (a session renamed away from an old title resolves only under the new title).
- `ambiguous-match-is-none` — two transcripts sharing one current title return `(\"\", false)`.
Also cover: exact match; no match; missing project dir; malformed JSON line skipped; unreadable file skipped; path-traversal title only string-compared; keyless trailing `custom-title` does not erase; line too long tolerated; extra JSON fields tolerated; missing `customTitle` key yields no match; the live process table beats an ambiguous transcript; `IsUUID` valid/invalid.

Do NOT use stdlib `func TestX(t *testing.T)` tables — Ginkgo `DescribeTable`/`Entry` only. Do NOT name any suite entry-point `TestSuite`.

### 5. `docs/liveness-classification.md` — the durable liveness table

Create `docs/liveness-classification.md`, a sibling of the frozen `docs/starting-marker-lifecycle.md`. It MUST contain the liveness-classification table: the four outcomes (`live`, `quiet`, `indeterminate`, none) and the signal behind each, in the checked-first order the Python uses (registry, then transcript recency within five minutes, then a live `--resume`/`--session-id` process). State that the task-file mtime is never a liveness signal, and that an empty session id is "none" regardless of the registry. Reference `docs/starting-marker-lifecycle.md` and note the values (five-minute window) are frozen. Do NOT invent new behavior in the doc — describe the ported contract only.

### 6. CHANGELOG entry

Add a `## Unreleased` section at the top of `CHANGELOG.md` if it does not exist (append to it if it does) and add one bullet:
`- feat: Port the session liveness, activity-date, and display-name resolution logic to Go packages, reproducing the Python classifications and refusals.`
Do NOT modify any existing section.

### 7. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.

</requirements>

<constraints>
- Copy of spec 022 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - **Behavior parity is the contract.** Each ported package reproduces its Python module's behavior; the Python source and its pytest suite are the reference. No behavior may be added, removed, or "improved".
  - **The Python backend keeps working until spec 3's cutover.** Do NOT change anything under `src/`; the pytest suite stays green.
  - **Single-process assumption** — the registries (later prompts) are sound only because the server runs one worker; do not introduce multi-worker sharing.
  - **Time is injectable** — every window, TTL, grace period, and timeout is a parameter, not a wall-clock read inside the logic (`go-time-injection`).
  - **Errors are wrapped and classified** so a caller can distinguish an expected absence (no transcript, no registry entry, no match) from an unexpected failure (`go-error-wrapping-guide`). Absences are not errors.
  - **Constants carry values, not Python names.** The five-minute liveness window is a frozen value; the Go identifier may be idiomatic (`DefaultLiveWindow`), so the port is verified by value, never by a name-grep.
  - Coding guides to follow (do not inline): `go-testing-guide`, `go-context-cancellation-in-loops`, `go-time-injection`, `go-package-layout-guide`, `go-error-wrapping-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`; `vendor/` is not committed in this repo.
- Do NOT touch `src/`, `tests/`, or `Makefile`.
- Do NOT wire these packages into `pkg/factory` or `main.go` — the HTTP surface and composition are spec 3's job; these packages are proven in isolation.
- Do NOT call `time.Now()` in production code; inject `libtime.CurrentDateTimeGetter`.
- The container masks `.git` (`hideGit: true`). If `go build`/`go test` fails with a VCS-stamping error, add `-buildvcs=false`. Do NOT change the Makefile.
- Existing Go tests from spec 021 must still pass.

<!-- OPEN QUESTION for the human auditor: spec 022 Desired Behavior 1 says classification, display-name resolution, and activity-date computation live in "the same package", but the spec's Acceptance Criteria pin three distinct `go test -run <Name>` commands (TestClassifySessionState, TestActivityDate, TestResolveDisplayName). Ginkgo permits only one RunSpecs per package, so three real suite entry-points require three packages. This prompt follows the ACs (the load-bearing contract) and splits into three packages; if a single package is preferred, the AC test names must be relaxed to Ginkgo Describe labels. -->
<!-- OPEN QUESTION for the human auditor: the spec constraint "reference them from the spec" for the new durable docs cannot be satisfied from inside this prompt (the spec file is approved and will be archived). The operator should add a link to `docs/liveness-classification.md` from `specs/in-progress/022-go-backend-private-logic.md` (and/or `docs/starting-marker-lifecycle.md`). -->
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test ./...` — must exit 0 (runs the new suites plus the spec-021 suites).
4. `go test -run TestClassifySessionState ./pkg/session/` — must exit 0.
5. `go test -run TestActivityDate ./pkg/activity/` — must exit 0.
6. `go test -run TestResolveDisplayName ./pkg/sessionresolver/` — must exit 0.
7. `go test -race ./...` — must exit 0.
8. `gofmt -l .` — must print nothing.
9. `grep -rnE '5[[:space:]]*\*[[:space:]]*time\.Minute' pkg/session/` — must print at least one line (the five-minute liveness window is carried by value, not invented).
10. `! grep -rn 'time.Now()' pkg/session pkg/activity pkg/sessionresolver` — must succeed (no direct wall-clock read in the ported logic).
11. `test -f docs/liveness-classification.md` — must exit 0.
12. `make test` — the existing pytest suite must still pass (Python backend unchanged).
</verification>
