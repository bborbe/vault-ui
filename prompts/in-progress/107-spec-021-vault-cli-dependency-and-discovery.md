---
status: approved
spec: [021-go-backend-foundation-vault-ops]
created: "2026-10-03T21:40:00Z"
queued: "2026-10-03T21:29:32Z"
branch: dark-factory/go-backend-foundation-vault-ops
---

# Pin vault-cli as a library and wire config.Loader vault discovery

<summary>
- vault-cli becomes a pinned library dependency of vault-ui — no subprocess, no local override.
- The Go backend discovers the configured vaults through vault-cli's own config loader.
- The discovered vault path is the root every vault operation will be invoked against — vault-ui never resolves a vault path itself.
- Readiness now flips to ready only after vault discovery succeeds, instead of at startup.
- Negative guards prove the dependency is a real pinned library, not a local override or a spawned subprocess.
- The Python backend is untouched and keeps building and testing exactly as before.
- No vault operation is wired yet — that is the next prompt.
</summary>

<objective>
Make vault-cli an importable, pinned library of vault-ui and drive vault discovery through vault-cli's `config.Loader`, so the vault operations wired in the next prompt are invoked against the operator's real vault path without a subprocess and without vault-ui resolving paths itself.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mod-replace-guide.md` — cross-repo `replace` is forbidden; consume released tags via `require`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — `Create*` factories are pure wiring.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega conventions.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the previous prompt `prompts/1-spec-021-go-module-skeleton-admin-block.md` so you know the module layout already in place (`main.go`, `pkg/readiness.go`, `pkg/metrics.go`, `pkg/handler/`, `pkg/factory/factory.go`).

Read the spec `specs/in-progress/021-go-backend-foundation-vault-ops.md` (Desired Behavior 3, 6; Acceptance Criteria 6, 10; Failure Modes; Constraints).

After adding the dependency, the vault-cli source is readable in the module cache at
`$(go env GOPATH)/pkg/mod/github.com/bborbe/vault-cli@v0.159.0/` — read `pkg/config/config.go` there to confirm the `Loader` interface before you use it.
</context>

<requirements>

### 1. Add the vault-cli library dependency

1. Write the code in steps 2-4 that imports `github.com/bborbe/vault-cli/pkg/config` FIRST.
2. Then run `go get github.com/bborbe/vault-cli@v0.159.0` and `go mod tidy`.

Result: `go.mod` contains a direct `require github.com/bborbe/vault-cli v0.159.0` line and NO `replace` directive for vault-cli. If `go mod tidy` demotes the dependency, it means no code imports it — fix the import first.

Do NOT add a `replace` directive — not even a local `../vault-cli` path. Do NOT invoke vault-cli as a subprocess (no `exec.Command`, no `os/exec`).

Failure modes this pinning covers (spec 021 Failure Modes table):
- If the pinned tag `v0.159.0` becomes unresolvable, `go build ./...` fails at module resolution naming the missing module version — no partial artifact. Do NOT work around it with a `replace`.
- If a later vault-cli release changes an `ops.*` signature, the pin is NOT bumped implicitly; `go build` fails at compile time naming the changed symbol. Do NOT bump the pin in this prompt.

Security: `config.Loader` reading vault-cli's own config file is the ONLY source of a vault path in this spec. Do NOT accept a vault path from an env var, a flag, vault-ui's own `config.yaml`, or any other source.

Note: `GetAllVaults` does not validate that a vault path exists — discovery success means the config parsed, not that the vault is reachable. Do NOT add a path-existence check; that would violate the "vault-ui never resolves vault paths itself" invariant.

### 2. `pkg/discovery.go` (package `vaultui`)

```go
package vaultui

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/config"
)

// DiscoverVaults loads every vault from vault-cli's config and, on success,
// marks readiness ready. It is the service's startup discovery step and the
// only place vault paths are resolved — vault-ui never resolves them itself.
func DiscoverVaults(ctx context.Context, loader config.Loader, readiness Readiness) error {
	if _, err := loader.GetAllVaults(ctx); err != nil {
		return errors.Wrap(ctx, err, "discover vaults")
	}
	readiness.SetReady()
	return nil
}
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
func NewLoader(configPath string) Loader
```
`Vault` has exported field `Path string`; `GetVault`/`GetAllVaults` return the vault with `Path` expanded (a leading `~` is resolved to the home directory).

### 3. Extend `pkg/factory/factory.go`

Add these two factories (pure wiring — no `if`/`switch`/`for`, no `error` return):

```go
// CreateConfigLoader returns a vault-cli config loader. An empty configPath
// makes the loader use vault-cli's default config location.
func CreateConfigLoader(configPath string) config.Loader {
	return config.NewLoader(configPath)
}

// CreateVaultDiscovery returns a run.Func that discovers the configured vaults
// once and marks readiness ready.
func CreateVaultDiscovery(loader config.Loader, readiness vaultui.Readiness) run.Func {
	return func(ctx context.Context) error {
		return vaultui.DiscoverVaults(ctx, loader, readiness)
	}
}
```

Add the imports `"github.com/bborbe/vault-cli/pkg/config"` and `"github.com/bborbe/vault-ui/pkg"` (already aliased `vaultui`).

### 4. Update `main.go` — discovery replaces the placeholder readiness

Replace the body of `execute` so readiness is set by discovery, not unconditionally:

```go
func execute(ctx context.Context) error {
	readiness := factory.CreateReadiness()
	loader := factory.CreateConfigLoader("")
	if err := run.CancelOnFirstErrorWait(ctx,
		factory.CreateVaultDiscovery(loader, readiness),
		factory.CreateHTTPServer(adminListen, readiness),
	); err != nil {
		return errors.Wrap(ctx, err, "run failed")
	}
	return nil
}
```

Delete the `// Vault discovery arrives in the next prompt` comment and the unconditional `readiness.SetReady()` line. `CreateConfigLoader("")` resolves vault-cli's default config location; do NOT add a config-path flag or env var.

Behavioral note (verified): `run.CancelOnFirstErrorWait` cancels the server only when a function returns an error. The discovery function returning `nil` after a successful pass does NOT cancel the server; a discovery error DOES, so a failed discovery fails the process fast.

### 5. Tests — add to `pkg/factory/factory_test.go` (package `factory_test`)

The existing suite file (`pkg/factory/factory_suite_test.go`) is reused — do NOT create a second suite.

Add a helper that writes a vault-cli config file pointing at a temp vault dir, and cover:

1. **Discovery returns the configured vault path.** Create `vaultDir` with `os.MkdirTemp`, write a config file:
   ```yaml
   current_user: alice
   default_vault: test
   vaults:
     test:
       path: <vaultDir>
       name: test
   ```
   Then `loader := factory.CreateConfigLoader(configPath)` and assert `loader.GetVaultPath(ctx, "test")` equals `vaultDir` (the discovered path IS the fixture path).

2. **Readiness flips 503 -> 200 on discovery.** Using the suite-level `readiness` var created in `BeforeEach` and the already-running admin server from the suite `BeforeEach` (which uses that fresh not-ready readiness):
   - `GET /readiness` returns 503.
   - Run `factory.CreateVaultDiscovery(loader, readiness)(ctx)` — must return nil.
   - `GET /readiness` returns 200.

3. **Discovery error leaves readiness not-ready.** Point the loader at a **malformed YAML** config file (e.g. an unterminated flow sequence `vaults: [`); assert `factory.CreateVaultDiscovery(...)(ctx)` returns an error and `readiness.IsReady()` stays false. (Verified in the module cache: `DiscoverVaults` calls `GetAllVaults`, which calls `Load` then `expandVaultPaths`; `expandVaultPaths` only expands a leading `~` and resolves template paths and never stats `Path`, so a config naming a nonexistent vault path does NOT error, and `GetAllVaults` never resolves a vault name, so an unknown name does not error either. A malformed YAML file is the only reliable error trigger through this path.)

Use `DeferCleanup` to remove the temp dirs. Do NOT bind a second port; reuse the suite server on `:9090`.

### 6. CHANGELOG entry

Append to the existing `## Unreleased` section (created by the previous prompt) one bullet:
`- feat: Depend on vault-cli as a pinned library and discover configured vaults through its config loader; readiness now reports ready only after discovery succeeds.`
Do NOT modify any existing section.

### 7. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.
</requirements>

<constraints>
- Copy of spec 021 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline.
  - The existing Python backend must keep working: `make test` and `make precommit` must continue to pass the Python steps.
  - vault-cli is pinned by `require` to a tagged release; a `replace ../vault-cli` directive is a MUST-violation (see `go-mod-replace-guide`).
  - vault-cli stays the sole vault interface — the Go backend reads and writes no vault file directly.
  - `config.Loader` reads the vault-cli config file; vault-ui must not accept a vault path from any other source in this spec.
  - Do NOT add a config knob for the admin port — the canonical `:9090` block is fixed.
  - Test framework is Ginkgo/Gomega per `go-testing-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`.
- Do NOT import `github.com/bborbe/vault-cli/pkg/ops` yet — the operation wiring lands in the next prompt.
- Do NOT touch `src/`, `tests/`, `Makefile`, or `docs/dod.md`.
- Existing Go tests from the previous prompt must still pass.
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test ./...` — must exit 0.
4. `! grep -nE 'replace.*vault-cli' go.mod` — must succeed (no replace directive for vault-cli).
5. `grep -nE '^require github.com/bborbe/vault-cli v[0-9]' go.mod` — must print a line pinning a tagged release.
6. `! grep -rn --include='*.go' 'exec.Command' .` — must succeed (repo-wide; the layout has no `cmd/` directory, so the repo-wide form is deliberate).
7. `make test` — the existing pytest suite must still pass.
</verification>
