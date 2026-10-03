---
status: approved
spec: [021-go-backend-foundation-vault-ops]
created: "2026-10-03T21:40:00Z"
queued: "2026-10-03T21:29:32Z"
branch: dark-factory/go-backend-foundation-vault-ops
---

# Add Go Makefile targets and the Go DoD line

<summary>
- The repo's Makefile gains Go build, format, vet, and test targets alongside the existing Python ones.
- `make test` now runs the Go suite and the pytest suite, and fails if either fails.
- `make precommit` keeps the Python format/lint/typecheck/test steps unchanged and adds the Go format/test/vet steps.
- A new `make build` target produces the Go binary on the host toolchain (not a Docker image).
- The Definition of Done gains a Go code-quality line next to the existing Python/JavaScript line.
- The Python backend's commands and behavior are unchanged.
</summary>

<objective>
Extend the repo's Makefile so `make test` and `make precommit` exercise both the Go and Python suites, add a `make build` target that produces the Go binary on the host toolchain, and record the Go code-quality expectation in `docs/dod.md` — so the Go foundation is part of the repo's standard quality gate rather than a side directory.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-makefile-commands.md` — the standard Go targets (`test`, `vet`, `format`) and what they run.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`.

Read the spec `specs/in-progress/021-go-backend-foundation-vault-ops.md` (Desired Behavior 8; Acceptance Criteria 11, 12; Verification — note `make build` is on the operator rung).

Read the current `Makefile` in full before editing. It currently defines: `sync`, `format`, `lint`, `typecheck`, `check`, `test`, `test-integration`, `precommit`, `run`, `watch`. The Python steps MUST stay byte-for-byte in behavior.

Read `docs/dod.md` in full before editing.
</context>

<requirements>

### 1. Makefile — add the Go toolchain variable and Go targets

Keep every existing target and its recipe behavior unchanged. Add `GO ?= go` near the top, then add these targets (recipes MUST use a literal TAB indent):

```makefile
.PHONY: build
build:
	$(GO) build -o bin/vault-ui .

.PHONY: go-format
go-format:
	$(GO) fmt ./...

.PHONY: go-vet
go-vet:
	$(GO) vet ./...

.PHONY: go-test
go-test:
	$(GO) test -race ./...
```

- `build` produces the binary at `bin/vault-ui` on the host toolchain — a plain `go build`, NOT a Docker image. (`bin/` is already gitignored.)
- `go-test` runs `$(GO) test -race ./...` — the race detector matches what the cited `go-makefile-commands.md` attributes to `make test`.
- Do NOT make `build` a prerequisite of `precommit` — it is a separate, operator-invoked target.

### 2. Makefile — wire the Go steps into `format`, `test`, and `precommit`

Change the three existing targets to:

```makefile
.PHONY: format
format: go-format
	uv run ruff format .
	uv run ruff check --fix . || true

.PHONY: test
test: sync go-test
	uv run pytest || test $$? -eq 5

.PHONY: precommit
precommit: sync format go-vet test check
	@echo "✓ All precommit checks passed"
```

The Python recipe lines are unchanged (`uv run ruff format .`, `uv run ruff check --fix . || true`, `uv run pytest || test $$? -eq 5`, `@echo "✓ All precommit checks passed"`). Only prerequisites and the added Go targets change:
- `format` runs `go-format` first, then the existing ruff steps.
- `test` runs `go-test` (via the prerequisite) and then the existing pytest step.
- `precommit` runs `sync format go-vet test check` — Python steps unchanged, Go format/test/vet added.

Leave `lint`, `typecheck`, `check`, `test-integration`, `run`, `watch`, and `sync` exactly as they are.

### 3. `docs/dod.md` — add the Go code-quality line

In the `## Code Quality` section, add a bullet after the existing first bullet (which names Python and JavaScript). It MUST contain the word `Go`:

```markdown
- Go code follows the bborbe conventions (errors wrapped with github.com/bborbe/errors, no silently ignored error returns, factory `Create*` functions contain no business logic)
```

Do NOT modify any other line of `docs/dod.md`.

### 4. CHANGELOG entry

Append to `## Unreleased` one bullet:
`- chore: Run the Go suite (build, format, vet, test) from the Makefile alongside the Python suite; add a make build target producing the Go binary on the host toolchain.`
Do NOT modify any existing section.

### 5. Document the new target

Add `make build` to the `## Development` command block in `README.md` (after `make precommit`) with the comment `# Build the Go binary`, and add `- `make build` — build the Go binary` to the `### Build and test` list in `CLAUDE.md`.

### 6. Self-check

Before finishing, re-run the `<verification>` commands and confirm each passes; walk every acceptance criterion named in the objective against the change.
</requirements>

<constraints>
- Copy of spec 021 constraints that bind this prompt (the agent has no memory between prompts):
  - **Never code directly** — this repo mandates the dark-factory pipeline.
  - The existing Python backend must keep working: `make precommit` and `make test` continue to pass the Python format/lint/typecheck/pytest steps.
  - `make build` produces the Go binary on the host toolchain — it is NOT a Docker build and is verified on the operator rung, not inside the container.
  - The repo's `.dark-factory.yaml` sets `hideGit: true`, `workflow: direct`, `autoRelease: false` — this prompt commits nothing and releases nothing.
- Do NOT commit — dark-factory handles git.
- Do NOT change the Python recipe lines in any target.
- Do NOT add a `make build` invocation to `precommit` — the spec places `make build` on the operator verification rung (spec §Verification), so it stays out of the container's `precommit`.
- Do NOT touch `src/`, `tests/`, or any Go source file.
- Existing Python tests must still pass.
</constraints>

<verification>
Run from the repo root:

1. `make test` — must exit 0 (runs the Go suite and the pytest suite).
2. `make precommit` — must exit 0 (Python format/lint/typecheck/test unchanged, Go format/test/vet added).
3. `go build ./... && go build -o bin/vault-ui . && test -x bin/vault-ui` — must exit 0 and produce the binary (exercises the real recipe without invoking the operator-only `make build`).
4. `grep -nE '^build:' Makefile` — must print a line.
5. `grep -nF '$(GO) build -o bin/vault-ui' Makefile` — must print a line.
6. `grep -nF '$(GO) test ./...' Makefile` — must print a line.
7. `grep -nF '$(GO) vet ./...' Makefile` — must print a line.
8. `grep -n 'Go' docs/dod.md` — must print the new Go code-quality line.
</verification>
