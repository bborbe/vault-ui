# SUPERSEDED — Python backend

> **This Python backend is SUPERSEDED by the Go backend
> (`github.com/bborbe/vault-ui`). Do not add features, routes, or behavior
> changes here.**

It is kept in-tree for exactly **one rollback window** — the period after the
operator cutover during which the launchd service can be repointed back to the
Python entry point if the Go service misbehaves. Nothing here is deleted yet.

The service that serves the HTTP and WebSocket surface is the Go backend. Its
entry points are [`main.go`](../../main.go) at the repo root and the packages
under [`pkg/`](../../pkg/) — start with `pkg/factory` and `pkg/handler`. The
parity harness under `scripts/parity/` measures the Go backend against this
Python one and must stay green.

The eventual removal of this tree is tracked in
[`specs/ideas/remove-superseded-python-backend.md`](../../specs/ideas/remove-superseded-python-backend.md).

Two parts of this directory are **not** retired with the rest:

- `src/vault_ui/static/` is frozen and shared. The Go backend embeds and serves
  it byte-identically (see `static_embed.go`), so the frontend must not be
  edited here either — a change here would diverge from what the Go service
  serves.
- `static_embed.go` (package `staticui`) is Go, not Python: it is the
  `//go:embed` shim the Go backend imports. It stays with the Go backend.

The Python test suite under [`tests/`](../../tests/) is retained and must stay
green for the duration of the rollback window, so `make precommit` keeps
passing while the backend is superseded.
