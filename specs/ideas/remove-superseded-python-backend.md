---
status: idea
tags:
    - dark-factory
    - spec
---

## Idea

Remove the superseded Python backend once the rollback window closes.

The Go backend (`github.com/bborbe/vault-ui`) is now the service behind
`http://127.0.0.1:8000`: it serves the identical HTTP and WebSocket surface as
the Python backend and serves the frozen frontend byte-identically. The Python
tree under `src/vault_ui/` is marked SUPERSEDED (see its README) and kept
in-tree for exactly one rollback window. This follow-up records the intent to
delete it once that window has passed, and the preconditions that must hold
first. It is not part of spec 023 — removal is a separate, later piece of work.

## Why now

Keeping two implementations of the same surface is a maintenance split: only
one of them is exercised, and the retired one drifts silently until someone
tries to finish the job against it. The rollback window is what makes the
cutover reversible; once it has elapsed with no rollback, the Python tree is
dead weight — and its presence makes every reader wonder which backend is
live. Removing it deliberately, with the checks below, is safer than deleting
it ad hoc later.

## What must be verified before removal

- The Go backend serves all 25 routes and the parity harness is green —
  `make parity` prints `routes: 25/25 matched`, full body/error/mutation/ws
  parity, and `static-parity: 3/3 byte-identical`, and exits 0.
- The operator has run the cutover and the live board is served by the Go
  binary — the process listening on `:8000` is the Go binary, not the Python
  service.
- The rollback window has elapsed with no rollback.
- `src/vault_ui/static/` is still required by the Go backend — it is served
  from there (embedded via `static_embed.go`) and must **not** be deleted with
  the rest of the tree.
- `src/vault_ui/static_embed.go` (package `staticui`) is likewise part of the
  Go backend, not the Python one — the Go build imports it. It must be kept or
  relocated, not deleted.
- The Python test suite (`tests/`) and the packaged Python entry point
  (`pyproject.toml`, the `vault-ui` script) can be retired together, and
  nothing else references them — no script, CI job, or doc invokes
  `vault-ui` as a Python entry point or imports a `vault_ui` module.

## What the removal touches

- `src/vault_ui/` **except** `static/` and `static_embed.go` (both belong to
  the Go backend) — i.e. the Python modules: `__main__.py`, `factory.py`,
  `api/`, `websocket/`, and the rest.
- The Python-only tests under `tests/`.
- The `pyproject.toml` `[project.scripts]` `vault-ui` entry point.
- The Makefile targets that still run Python: `sync`, `format`, `lint`,
  `typecheck`, `check`, `test`'s pytest step, `run`, and `watch`.

## Open questions

- How long is the rollback window, and what marks its end — a fixed date, or a
  period of stable operation?
- Does `static/` stay where it is under `src/vault_ui/`, or move to a
  Go-owned path once the Python package around it is gone?
- Does the `vault-ui` console-script name get retired outright, or kept as an
  alias for the Go binary?

## Why not

- **Not yet.** The rollback window is the whole point of keeping the Python
  tree; deleting it now removes the fallback and turns a reversible cutover
  into a one-way door.
- **Not as part of spec 023.** Spec 023's scope is parity and the marker; a
  deletion of this size (Python modules, tests, packaging, Makefile targets)
  deserves its own prompt and its own review against the preconditions above.
