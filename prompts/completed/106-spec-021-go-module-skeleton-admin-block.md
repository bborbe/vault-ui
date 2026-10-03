---
status: completed
spec: [021-go-backend-foundation-vault-ops]
summary: Added the github.com/bborbe/vault-ui Go module at the repo root with main.go, flat pkg/ readiness+metrics, pkg/handler admin handlers, and pkg/factory composition root serving the canonical admin block on :9090, proven by a passing Ginkgo/Gomega suite; Python backend unchanged.
execution_id: vault-ui-exec-106-spec-021-go-module-skeleton-admin-block
dark-factory-version: v0.196.0
created: "2026-10-03T21:40:00Z"
queued: "2026-10-03T21:29:32Z"
started: "2026-10-03T21:29:33Z"
completed: "2026-10-03T21:32:59Z"
branch: dark-factory/go-backend-foundation-vault-ops
---

# Add the Go module skeleton and admin block on :9090

<summary>
- vault-ui gains its first Go code: a compilable Go module at the repo root.
- The module's composition root owns the canonical bborbe admin HTTP block on port 9090.
- The admin block exposes `/healthz`, `/readiness`, `/metrics`, `/setloglevel/{level}`, and `/gc`, all reachable and returning the expected status.
- `/readiness` reports "not ready" (503) until the service has finished starting, then "ready" (200).
- `/metrics` serves Prometheus text that includes a vault-ui-owned build-info metric plus the standard Go runtime metrics.
- The Python backend is untouched and keeps building and testing exactly as before.
- A Ginkgo/Gomega suite proves the admin block end to end, and a compile check proves the binary links.
- No vault-cli dependency yet — this prompt is pure skeleton; vault wiring lands in the next prompt.
</summary>

<objective>
Create the Go foundation for vault-ui: a `github.com/bborbe/vault-ui` module at the repo root whose composition root (`pkg/factory`) starts the canonical bborbe admin block on `:9090`, so the vault-cli library wiring (next prompt) and the HTTP surface (spec 3) have a compiling, testable module to live in.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container (they are the contract for this change):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md` — flat `pkg/` plus the two conventional exceptions `pkg/factory/` and `pkg/handler/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-http-service-guide.md` — the canonical admin block (five endpoints, port `:9090`), the `libhttp.NewServer(...).Run(ctx)` lifecycle.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — `Create*` factories are pure wiring; `pkg/factory/` holds no implementation types.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-prometheus-metrics-guide.md` — metric naming, `MustRegister`, help-string quality.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md` — glog verbosity levels.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega suite file, external test package, `main_test.go` Compiles check.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — use `run.*` combinators, never a raw `go func()` in production code.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — coverage and verification rules.

Read the spec `specs/in-progress/021-go-backend-foundation-vault-ops.md` (Desired Behavior 1-2, Failure Modes, Constraints).

There is no existing Go code in this repo — every Go file below is new. The repo root is the module root (do NOT create a `cmd/` directory; `main.go` sits at the root).
</context>

<requirements>

### 1. Create the Go module (`go.mod` at the repo root)

From the repo root run `go mod init github.com/bborbe/vault-ui`, then `go mod edit -go=1.27.1`.

The resulting `go.mod` MUST satisfy (verbatim lines):
- `module github.com/bborbe/vault-ui`
- `go 1.27.1`

Do NOT add a `replace` directive. Do NOT create `go.work`.

### 2. `main.go` (package main, repo root)

```go
package main

import (
	"context"
	"os"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/factory"
)

// adminListen is the canonical bborbe admin port. It is deliberately NOT a
// flag or env var — spec 021 Non-goals forbid a port knob.
const adminListen = ":9090"

func main() {
	defer glog.Flush()
	ctx := run.ContextWithSig(context.Background())
	if err := execute(ctx); err != nil {
		glog.Errorf("vault-ui failed: %v", err)
		glog.Flush()
		os.Exit(1)
	}
}

func execute(ctx context.Context) error {
	readiness := factory.CreateReadiness()
	// Vault discovery arrives in the next prompt; until then the service has
	// nothing to wait for, so it is ready immediately.
	readiness.SetReady()
	if err := run.CancelOnFirstErrorWait(ctx,
		factory.CreateHTTPServer(adminListen, readiness),
	); err != nil {
		return errors.Wrap(ctx, err, "run http server")
	}
	return nil
}
```

Do NOT add a `--listen` flag, a `Listen` struct field, or any argument parser. Do NOT add Sentry, Kafka, or a kv DB.

### 3. `pkg/readiness.go` (package `vaultui`, directory `pkg/`)

The flat `pkg/` directory declares package name `vaultui`.

```go
package vaultui

import "sync/atomic"

// Readiness reports whether the service has completed startup. The admin
// /readiness endpoint serves 503 until SetReady has been called, then 200.
type Readiness interface {
	SetReady()
	IsReady() bool
}

type readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a Readiness that starts not-ready.
func NewReadiness() Readiness {
	return &readiness{}
}

func (r *readiness) SetReady() {
	r.ready.Store(true)
}

func (r *readiness) IsReady() bool {
	return r.ready.Load()
}
```

### 4. `pkg/metrics.go` (package `vaultui`)

Define exactly ONE vault-ui-owned metric, named `vault_ui_build_info`, and register it on the default registry so `promhttp.Handler()` exposes it. Use `Namespace: "vault_ui"`, `Name: "build_info"` (the full metric name becomes `vault_ui_build_info`).

```go
package vaultui

import "github.com/prometheus/client_golang/prometheus"

var buildInfoGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "vault_ui",
	Name:      "build_info",
	Help:      "Build information for the running vault-ui binary; always 1.",
})

func init() {
	prometheus.MustRegister(buildInfoGauge)
	buildInfoGauge.Set(1)
}
```

Do NOT invent any other metric. Do NOT use `github.com/bborbe/metrics` — its build-info metric is named `build_info`, not `vault_ui_build_info`.

### 5. `pkg/handler/healthz.go` and `pkg/handler/readiness.go` (package `handler`)

```go
// pkg/handler/healthz.go
package handler

import (
	"net/http"

	libhttp "github.com/bborbe/http"
)

// NewHealthzHandler returns the canonical liveness handler: always HTTP 200.
func NewHealthzHandler() http.Handler {
	return libhttp.NewPrintHandler("OK")
}
```

```go
// pkg/handler/readiness.go
package handler

import (
	"net/http"

	vaultui "github.com/bborbe/vault-ui/pkg"
)

// NewReadinessHandler returns HTTP 200 once readiness.IsReady is true and
// HTTP 503 before.
func NewReadinessHandler(readiness vaultui.Readiness) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, _ *http.Request) {
		if !readiness.IsReady() {
			resp.WriteHeader(http.StatusServiceUnavailable)
			_, _ = resp.Write([]byte("not ready"))
			return
		}
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write([]byte("OK"))
	})
}
```

### 6. `pkg/factory/factory.go` (package `factory`)

The factory is pure wiring — no `if`, `switch`, `for`, and no `error` return in any `Create*` function. The router assembly lives inside the returned `run.Func` closure, which the factory guide (§4.3, §11) permits.

```go
package factory

import (
	"context"
	"net/http"
	"time"

	libhttp "github.com/bborbe/http"
	"github.com/bborbe/log"
	"github.com/bborbe/run"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/handler"
)

// CreateReadiness returns the service readiness gate, which starts not-ready.
func CreateReadiness() vaultui.Readiness {
	return vaultui.NewReadiness()
}

// CreateHealthzHandler returns the canonical liveness handler.
func CreateHealthzHandler() http.Handler {
	return handler.NewHealthzHandler()
}

// CreateReadinessHandler returns the readiness handler backed by readiness.
func CreateReadinessHandler(readiness vaultui.Readiness) http.Handler {
	return handler.NewReadinessHandler(readiness)
}

// CreateHTTPServer returns a run.Func serving the canonical bborbe admin block.
func CreateHTTPServer(listen string, readiness vaultui.Readiness) run.Func {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		router := mux.NewRouter()
		router.Path("/healthz").Handler(CreateHealthzHandler())
		router.Path("/readiness").Handler(CreateReadinessHandler(readiness))
		router.Path("/metrics").Handler(promhttp.Handler())
		router.Path("/setloglevel/{level}").
			Handler(log.NewSetLoglevelHandler(ctx, log.NewLogLevelSetter(2, 5*time.Minute)))
		router.Path("/gc").Handler(libhttp.NewGarbageCollectorHandler())

		glog.V(2).Infof("starting http server listen on %s", listen)
		return libhttp.NewServer(listen, router).Run(ctx)
	}
}
```

Notes on the verified library contracts (do not deviate):
- `libhttp "github.com/bborbe/http"`: `NewPrintHandler(format string, a ...any) http.Handler`, `NewGarbageCollectorHandler() http.Handler`, `NewServer(addr string, router http.Handler, optionFns ...func(*ServerOptions)) run.Func`.
- `"github.com/bborbe/log"`: `NewSetLoglevelHandler(ctx context.Context, setter LogLevelSetter) http.Handler`, `NewLogLevelSetter(defaultLoglevel glog.Level, autoResetDuration time.Duration) LogLevelSetter`. Pass baseline `2` and a 5-minute reset window.
- `run.Func` has a `Run(ctx) error` method, so `libhttp.NewServer(listen, router).Run(ctx)` is valid.

### 7. Add dependencies, then tidy

Write the imports first (steps 2-6), then run `go mod tidy`. Recommended explicit pins (they match what vault-cli v0.159.0 uses, and avoid surprises):
`github.com/bborbe/errors`, `github.com/bborbe/http`, `github.com/bborbe/log`, `github.com/bborbe/run`, `github.com/golang/glog`, `github.com/gorilla/mux`, `github.com/prometheus/client_golang`, `github.com/onsi/ginkgo/v2`, `github.com/onsi/gomega`.

Do NOT write `-mod=vendor` anywhere.

### 8. Tests

Every package that contains a `*_test.go` file MUST have a `*_suite_test.go` entry-point (Ginkgo specs are not discovered without it). Use the standard suite body:
`time.Local = time.UTC`; `format.TruncatedDiff = false`; `RegisterFailHandler(Fail)`; `suiteConfig, reporterConfig := GinkgoConfiguration()`; `suiteConfig.Timeout = 60 * time.Second`; `RunSpecs(t, "<Name> Suite", suiteConfig, reporterConfig)`.

**`main_test.go`** (package `main_test`, repo root) — the required `Compiles` check:

```go
package main_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Main", func() {
	It("Compiles", func() {
		var err error
		_, err = gexec.Build(".", "-mod=mod", "-buildvcs=false")
		Expect(err).NotTo(HaveOccurred())
	})
})

func TestSuite(t *testing.T) {
	time.Local = time.UTC
	format.TruncatedDiff = false
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.Timeout = 60 * time.Second
	RunSpecs(t, "Main Suite", suiteConfig, reporterConfig)
}
```

**`pkg/vaultui_suite_test.go`** (package `vaultui_test`) + **`pkg/readiness_test.go`** (package `vaultui_test`) — a small unit test for the readiness gate: a fresh `NewReadiness()` reports `IsReady() == false`; after `SetReady()` it reports `true`.

**`pkg/handler/handler_suite_test.go`** (package `handler_test`) + **`pkg/handler/readiness_test.go`** (package `handler_test`) — unit-test the handlers with `net/http/httptest`:
- `NewHealthzHandler()` returns HTTP 200.
- `NewReadinessHandler(fresh)` returns HTTP 503; after `fresh.SetReady()` it returns HTTP 200.

**`pkg/factory/factory_suite_test.go`** (package `factory_test`) + **`pkg/factory/factory_test.go`** (package `factory_test`) — the admin-block integration test. Start the real server via `factory.CreateHTTPServer(":9090", readiness)` in a goroutine under a cancelable context, poll `http://127.0.0.1:9090/healthz` with `Eventually` until it answers 200, and shut the server down in `AfterEach` (cancel the context and wait for the goroutine's error to be received). Cover:
1. Once `readiness.SetReady()` has been called, each of `GET /healthz`, `GET /readiness`, `GET /metrics`, `GET /setloglevel/info`, `GET /gc` returns HTTP 200. (`/setloglevel/info` matches the `{level}` route; the canonical handler returns 200 for any level value.)
2. `GET /readiness` returns 503 before `SetReady` and 200 after.
3. `GET /metrics` body matches `(?m)^vault_ui_build_info` AND `(?m)^go_` (read the whole body with `io.ReadAll`).

Only this package binds `:9090`, so `go test ./...` (which runs packages in parallel) never has two servers on the same port.

Do NOT use a stdlib `func TestX(t *testing.T)` table test — use Ginkgo `It`/`Describe`/`DescribeTable` per the testing guide.

### 9. CHANGELOG entry

Add a `## Unreleased` section at the top of `CHANGELOG.md` (above `## v0.74.2`) if it does not exist, and add one bullet:
`- feat: Add the Go module foundation for vault-ui — composition root serving the canonical admin block on :9090 (healthz, readiness, metrics, setloglevel, gc).`
Do NOT modify any existing section.

### 10. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.
</requirements>

<constraints>
- Copy of spec 021 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline; this prompt is that pipeline.
  - The existing Python backend must keep working: `make test` and `make precommit` must continue to pass the Python format/lint/typecheck/pytest steps.
  - vault-cli stays the sole vault interface — the Go backend reads and writes no vault file directly.
  - Do NOT add a config knob for the admin port — the canonical `:9090` block is fixed. No `--listen` flag, no `Listen` struct field, no env var.
  - Do NOT add a `replace` directive for vault-cli.
  - Package layout follows `go-package-layout-guide` (flat `pkg/` + `pkg/factory/` + `pkg/handler/`), composition follows `go-factory-pattern`, the admin block follows `go-http-service-guide`, metrics follow `go-prometheus-metrics-guide`, logging follows `go-glog-guide`.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT run `go mod vendor`; `vendor/` is not committed in this repo.
- Do NOT touch any file under `src/`, `tests/`, `Makefile`, or `docs/dod.md` in this prompt — the Makefile and DoD changes land in the build-tooling prompt.
- The container masks `.git` (`hideGit: true`). If `go build` / `go test` fails with a VCS-stamping error, add `-buildvcs=false` to the failing command (the `main_test.go` Compiles check already passes it). Do NOT change the Makefile for this.
- Existing Python tests must still pass.
</constraints>

<verification>
Run from the repo root:

1. `go build ./...` — must exit 0.
2. `go vet ./...` — must exit 0.
3. `go test ./...` — must exit 0 (runs the main Compiles check, the readiness/handler unit tests, and the factory admin-block suite).
4. `grep -n '^module github.com/bborbe/vault-ui$' go.mod` — must print a line.
5. `grep -n '^go 1.27.1$' go.mod` — must print a line.
6. `make test` — the existing pytest suite must still pass (Python backend unchanged).
</verification>
