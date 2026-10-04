---
status: completed
spec: [022-go-backend-private-logic]
summary: Ported vault hierarchy discovery, the vault-ui/vault-cli config merge, and the status cache to Go (pkg/hierarchy, pkg/vaultconfig, pkg/statuscache) with Ginkgo/Gomega suites, promoted go.yaml.in/yaml/v3 to a direct dependency, and added a CHANGELOG entry; make precommit exits 0.
execution_id: vault-ui-exec-113-spec-022-topology-and-status-cache
dark-factory-version: v0.196.0
created: "2026-10-03T23:45:00Z"
queued: "2026-10-03T23:06:15Z"
started: "2026-10-04T09:50:11Z"
completed: "2026-10-04T09:58:23Z"
branch: dark-factory/go-backend-private-logic
---

# Add vault topology discovery, the config merge, and the status cache

<summary>
- The Go backend can discover a vault's hierarchy folders the same way the Python backend does, matching the Themes/Objectives/Goals/Tasks suffix and ordering them by category then numeric prefix.
- A vault's configured tasks folder is preferred and other `*Tasks` folders in the same vault are excluded; if the configured folder is absent the discovered set is used unchanged.
- The Go backend merges vault-ui's own config file with vault-cli's configuration, skipping a vault whose tasks folder is absent on disk rather than failing the whole board.
- An explicit vaults block still filters and can override a display name; an absent or empty block serves every vault-cli vault that has a tasks folder.
- An in-memory status cache loads each discovered folder's items and extracts each item's status and its raw `claude_session_started` marker.
- A legacy YAML boolean `true` marker is normalised to the string `"true"`, while a modern ISO-timestamp marker is preserved verbatim.
- A single item can be invalidated on a watcher event: the status and the marker are updated together, and an item whose status field was removed is dropped from the cache.
- An unreadable or malformed file contributes nothing and never crashes the load.
- The Python backend is untouched and keeps serving traffic.
</summary>

<objective>
Port `hierarchy.py`, `config.py`, and `status_cache.py` into `pkg/hierarchy`, `pkg/vaultconfig`, and `pkg/statuscache` — reproducing folder discovery and ordering, the vault-ui config.yaml + vault-cli config merge, and the in-memory status/started cache — with Ginkgo/Gomega tests.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` plus subpackages on a real split trigger.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry-point, `DescribeTable`/`Entry`, suite timeout.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; a vault whose tasks folder is absent is an expected skip, not an error.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the spec `specs/in-progress/022-go-backend-private-logic.md` (Desired Behavior 4, 5; Acceptance Criteria 9, 10; Failure Modes rows for config discovery and a malformed file; Constraints — the `config.yaml` and vault-cli `config list` contracts are frozen).

Read the Python source that is the contract — reproduce it exactly:
- `src/vault_ui/hierarchy.py` — `HIERARCHY_SUFFIXES`, `discover_hierarchy_folders`, `discover_hierarchy_folders_for_vault`.
- `src/vault_ui/config.py` — `VaultConfig`, `Config`, `resolve_default_config_path`, `discover_current_user`, `discover_vaults_from_cli`, `_build_vault_config`, `load_config`.
- `src/vault_ui/status_cache.py` — `StatusCache` (`load_vault`, `_extract_fields`, `get_status`, `get_session_started`, `count`, `invalidate`).
- The matching pytest suites are the regression lock: `tests/test_hierarchy.py`, `tests/test_config.py`, `tests/test_status_cache.py`.

Read the previous spec-021 prompt `prompts/completed/107-spec-021-vault-cli-dependency-and-discovery.md` — this prompt reuses vault-cli's `config.Loader` for the vault-cli side of the merge (no subprocess).

The module is `github.com/bborbe/vault-ui` at the repo root (established by spec 021). `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` are already direct dependencies.

**Package-per-AC-name rule.** Ginkgo allows exactly ONE `RunSpecs` per test binary, so one Go package exposes exactly one suite entry-point. The spec pins `go test -run 'TestHierarchyFolders|TestConfigMerge'` and `go test -run TestStatusCache`, so the three packages below have suite entry-points named exactly `TestHierarchyFolders`, `TestConfigMerge`, and `TestStatusCache`. Do NOT merge them and do NOT rename the entry-points.
</context>

<requirements>

### 1. Create `pkg/hierarchy/` — hierarchy folder discovery

Package name `hierarchy`. Files: `hierarchy.go`, `hierarchy_suite_test.go`, `hierarchy_test.go`.

Port `hierarchy.py` exactly.

Exported contract:

```go
package hierarchy

// Suffixes are the recognized hierarchy folder suffixes, in category order.
var Suffixes = []string{"Themes", "Objectives", "Goals", "Tasks"}

// DiscoverHierarchyFolders returns the top-level directories under vaultPath whose
// name ends with one of Suffixes, ordered by category (Suffixes order) then by
// numeric prefix, then case-insensitively by name. A missing vault path returns an
// empty slice and no error.
func DiscoverHierarchyFolders(vaultPath string) ([]string, error)

// DiscoverHierarchyFoldersForVault keeps all Themes/Objectives/Goals folders and
// prefers exactly the configured tasksFolder among the *Tasks folders (excluding
// the others). If the configured folder is absent from the discovered set, the
// discovered set is returned unchanged. Returns full paths.
func DiscoverHierarchyFoldersForVault(vaultPath, tasksFolder string) ([]string, error)
```

Behavior notes to reproduce exactly: a folder's numeric prefix is the integer before the first space of the name minus its suffix (e.g. `24 Tasks` → 24); a non-numeric or missing prefix sorts last (9999); the category index is the `Suffixes` position; the final tiebreak is the lower-cased name.

### 2. Create `pkg/vaultconfig/` — the vault-ui config merge

Package name `vaultconfig`. Files: `vaultconfig.go`, `vaultconfig_suite_test.go`, `vaultconfig_test.go`.

Port `config.py`. The vault-cli side comes from the injected `config.Loader` (never a subprocess); vault-ui's own `config.yaml` supplies host/port/max_concurrent_sessions, the `vault_cli_path`, and the optional `vaults:` override block. Use `go.yaml.in/yaml/v3` for the YAML parse (already an indirect dependency of the module; `go mod tidy` will promote it).

Exported contract:

```go
package vaultconfig

import (
    "context"

    "github.com/bborbe/vault-cli/pkg/config"
)

// Vault is one resolved vault.
type Vault struct {
    Name              string
    Path              string
    TasksFolder       string
    VaultName         string
    ClaudeScript      string
    VaultCLIPath      string
    SessionProjectDir string
    TopicsFolder      string
}

// Config is the merged application configuration.
type Config struct {
    Vaults                []Vault
    Host                  string
    Port                  int
    MaxConcurrentSessions int
    CurrentUser           string
}

// BuildVaultConfig builds a Vault from a vault-cli entry, or (Vault{}, false)
// when the entry has no path, has no tasks dir, or its tasks folder is absent on
// disk. It never returns an error for a skip.
func BuildVaultConfig(name string, cliVault *config.Vault, vaultName, vaultCLIPath string) (Vault, bool)

// ResolveDefaultConfigPath resolves the default config.yaml path XDG-first,
// falling back to the legacy repo-root path when only that exists.
func ResolveDefaultConfigPath(xdgPath, legacyPath string) string

// Load reads configPath, merges it with the injected vault-cli loader's vaults and
// current user, and returns the resolved Config. An explicit non-empty `vaults:`
// block filters (a vault-cli vault not named stays off) and overrides the display
// name; an absent or empty block serves every vault-cli vault with a tasks folder
// that exists on disk. When nothing survives the merge it returns an error naming
// the reason.
func Load(ctx context.Context, loader config.Loader, configPath string) (*Config, error)
```

Verified vault-cli contract (do not deviate) — `github.com/bborbe/vault-cli/pkg/config`:
```go
type Loader interface {
    Load(ctx context.Context) (*Config, error)
    GetVaultPath(ctx context.Context, vaultName string) (string, error)
    GetVault(ctx context.Context, vaultName string) (*Vault, error)
    GetAllVaults(ctx context.Context) ([]*Vault, error)
    GetCurrentUser(ctx context.Context) (string, error)
}
```
`config.Vault` exported fields used here (verbatim): `Path`, `Name`, `TasksDir`, `GoalsDir`, `ThemesDir`, `ObjectivesDir`, `TopicsDir`, `ClaudeScript`, `SessionProjectDir`; plus method `GetTasksDir() string` (defaults to `"Tasks"`). Gate on the RAW `TasksDir` field: an entry whose `TasksDir` is empty is skipped (mirrors `config.py`'s `cli_vault.get("tasks_dir")`). Do NOT use `GetTasksDir()` for the presence gate — it defaults to `"Tasks"` and would silently defeat the skip.

Behavior notes to reproduce exactly:
- vault-cli entries are indexed case-insensitively by `Name` (lower-cased).
- `BuildVaultConfig` gates ONLY on path presence, raw `TasksDir` presence, and the tasks folder existing on disk. `topics_dir` is deliberately NOT a gate — a vault without it is still returned with an empty `TopicsFolder`.
- `claude_script` falls back to `"claude"` when empty/absent; `session_project_dir` defaults to `""`; `topics_folder` is empty when absent.
- The `vault_name` override wins over `strings.Title`-style title-casing of the key.
- `Load` returns an error when configPath does not exist, when the YAML is malformed, and when every candidate vault was skipped.
- Use `os.Stat` + `IsDir` for the tasks-folder existence check (the test uses a temp vault dir).

Do NOT spawn `vault-cli`; the vault-cli side is the injected `config.Loader`.

### 3. Create `pkg/statuscache/` — the status + started cache

Package name `statuscache`. Files: `statuscache.go`, `statuscache_suite_test.go`, `statuscache_test.go`. Imports `pkg/hierarchy`.

Port `status_cache.py`.

Exported contract:

```go
package statuscache

// Cache is the in-memory status + claude_session_started cache.
type Cache interface {
    LoadVault(vaultName, vaultPath, tasksFolder string) error
    GetStatus(vaultName, itemID string) (string, bool)
    GetSessionStarted(vaultName, itemID string) (string, bool)
    Count(vaultName string) int
    Invalidate(vaultName, itemID string)
}

func NewCache() Cache
```

Behavior notes to reproduce exactly:
- `LoadVault` scans every discovered hierarchy folder (via `hierarchy.DiscoverHierarchyFoldersForVault`, with a default tasks folder of `"24 Tasks"` when empty) recursively for `*.md`; each file's stem is the item id; the status and the raw `claude_session_started` value are extracted. The vault's maps are replaced atomically (start fresh each time).
- `_extract_fields` equivalent: match the frontmatter block with the same `^---\s*\n(.*?)\n---` regex, parse it as YAML, and read `status` and `claude_session_started`. A truthy `claude_session_started` yields a string: a legacy YAML boolean `true` normalises to the literal `"true"`, any other value is preserved verbatim (an ISO-8601 timestamp since 2026-08-29). A cleared/absent/false field yields no marker. A non-dict frontmatter, a missing frontmatter block, and any read/parse failure each contribute nothing (no crash).
- `Invalidate(vaultName, itemID)` searches the discovered folders for `<itemID>.md`: when found it updates the status and the marker together (dropping the status when the field is gone, dropping the marker when it is gone); when the file is not found it removes the item from both maps. An unknown vault is a no-op.
- `GetStatus`/`GetSessionStarted` return `(value, true)` when present, `(\"\", false)` otherwise.

### 4. Tests — three Ginkgo suites

Each package's `<pkg>_suite_test.go` uses the standard suite body with the entry-point named exactly as the AC, NOT `TestSuite`.

**`pkg/hierarchy` — suite entry `TestHierarchyFolders`.** Mirror `tests/test_hierarchy.py`. Rows (use `DescribeTable`/`Entry` with these EXACT entry descriptions):
- `suffix-and-numeric-order` — folders are returned category-then-numeric-prefix ordered (e.g. `21 Themes`, `22 Objectives`, `23 Goals`, `24 Tasks`, `40 Tasks`).
Also cover: a missing vault path returns empty; folders with no numeric prefix order by category then name (`Themes`, `Objectives`, `Goals`, `Tasks`); the configured tasks folder is preferred and other `*Tasks` folders are excluded; the discovered set is returned unchanged when the configured tasks folder is absent.

**`pkg/vaultconfig` — suite entry `TestConfigMerge`.** Mirror `tests/test_config.py`. Build a temp vault fixture and an injected `config.Loader` (a fake implementing the interface, or vault-cli's real loader pointed at a temp config). Rows (exact entry descriptions):
- `absent-tasks-folder-skips-vault` — a vault-cli vault whose tasks folder is absent on disk is omitted, and the remaining vaults still load.
- `no-tasks-dir-skips-vault` — a vault-cli vault with no `tasks_dir` is skipped even when a `Tasks/` folder exists on disk.
Also cover: `Load` reads a healthy vault (name, path, vault_name title-case, tasks folder, claude_script); the claude_script empty/absent fallback; multiple vaults; host/port/max_concurrent_sessions defaults and overrides; `current_user` from the loader; `session_project_dir` present/absent; `topics_dir` is kept even when its folder is absent; an explicit block filters and overrides `vault_name`; an absent or empty block serves every vault-cli vault; a vault without a `path` is skipped; `Load` errors when every vault is skipped; `ResolveDefaultConfigPath` is XDG-first with a legacy fallback.

**`pkg/statuscache` — suite entry `TestStatusCache`.** Mirror `tests/test_status_cache.py`. Rows (exact entry descriptions):
- `yaml-true-normalised` — an unquoted YAML `true` marker is cached as the string `"true"`.
- `status-field-removed-drops-item` — after the status field is removed from the file, an `Invalidate` drops the item from the cache.
Also cover: `LoadVault` populates status and the marker; an item without the marker returns `(\"\", false)`; `Invalidate` sets then clears the marker in lockstep; an unknown vault/item returns `(\"\", false)`.

Do NOT use stdlib `func TestX(t *testing.T)` tables. Do NOT name any suite entry-point `TestSuite`.

### 5. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- feat: Port vault hierarchy discovery, the vault-ui/vault-cli config merge, and the status cache to Go, preserving folder ordering, the absent-tasks-folder skip, and YAML marker normalisation.`
Do NOT modify any existing section.

### 6. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.

</requirements>

<constraints>
- Copy of spec 022 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - **Behavior parity is the contract** — reproduce `src/vault_ui/hierarchy.py`, `src/vault_ui/config.py`, and `src/vault_ui/status_cache.py` exactly; the pytest suites are the reference. No behavior added, removed, or "improved".
  - **The Python backend keeps working until spec 3's cutover.** Do NOT change anything under `src/`.
  - `config.yaml` format and the vault-cli `config list` JSON contract are frozen — the port consumes them, it does not change them.
  - **Errors are wrapped and classified** so a caller can distinguish an expected absence (a skipped vault, an unreadable file) from an unexpected failure (`go-error-wrapping-guide`).
  - **Security:** the status cache is the sanctioned direct-read path for the `claude_session_started` marker; it reads vault files only for frontmatter extraction and never writes them. A vault name/item id must never be used to build a path outside the discovered folders without the same guard the Python uses.
  - Coding guides to follow (do not inline): `go-testing-guide`, `go-error-wrapping-guide`, `go-package-layout-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: branch`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT touch `src/`, `tests/`, or `Makefile`.
- Do NOT wire these packages into `pkg/factory` or `main.go` — that is spec 3's job.
- Do NOT spawn `vault-cli` as a subprocess; the vault-cli side of the merge is the injected `config.Loader`.
- Write the code that imports `go.yaml.in/yaml/v3` BEFORE running `go mod tidy`, so tidy promotes it to a direct dependency.
- The container masks `.git` (`hideGit: true`). If `go build`/`go test` fails with a VCS-stamping error, add `-buildvcs=false`. Do NOT change the Makefile.
- Existing Go tests from spec 021 and prompts 1-3 must still pass.

<!-- OPEN QUESTION for the human auditor: the spec's Desired Behavior 4 names one "vault topology discovery" concern but the AC pins two test names (`TestHierarchyFolders`, `TestConfigMerge`); combined with Ginkgo's one-suite-per-package rule this yields three packages (hierarchy, vaultconfig, statuscache). If a coarser split is preferred, the AC test names would need to be relaxed. -->
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test -run TestHierarchyFolders ./pkg/hierarchy/` — must exit 0.
4. `go test -run TestConfigMerge ./pkg/vaultconfig/` — must exit 0.
5. `go test -run TestStatusCache ./pkg/statuscache/` — must exit 0.
6. `go test ./...` — must exit 0.
7. `go test -race ./...` — must exit 0.
8. `gofmt -l .` — must print nothing.
9. `grep -nE 'go\.yaml\.in/yaml/v3 v[0-9][0-9.]*$' go.mod` — must print a line (the YAML dependency is a direct requirement).
10. `make test` — the existing pytest suite must still pass.
</verification>
