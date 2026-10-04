---
status: approved
spec: [023-go-backend-api-and-cutover]
created: "2026-10-04T00:00:00Z"
queued: "2026-10-04T13:10:34Z"
---

# Mark the Python backend superseded in-tree and file its removal as a follow-up

<summary>
- Anyone who opens the Python service's directory now sees, in the first line of its own README, that it has been superseded by the Go backend and must not receive new work.
- The marker explains why it is still present: it is kept for one rollback window, and nothing is deleted yet.
- The work to actually remove it is written down as its own follow-up document, so the removal happens deliberately later rather than being forgotten or done ad hoc.
- The follow-up names what must be checked before the Python tree can go — the retained test suite, the packaged entry point, and the frontend's assumptions.
- The Python source, its tests, and the frozen frontend are otherwise untouched; the existing Python suite still passes.
- No service is restarted and no deployment is performed — that remains an operator action documented outside this pipeline.
</summary>

<objective>
Mark the Python backend as superseded in-tree with a README marker, and file its removal as a named follow-up document, so the one-rollback-window decision is durable and the eventual deletion is tracked rather than implied.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read the spec `specs/in-progress/023-go-backend-api-and-cutover.md` — this prompt covers Acceptance Criterion 10 and confirms Criterion 12. Read the spec's Constraints on the Python backend and its "Do-Nothing Option" for the rollback-window rationale.

Read prompts 1–3 for context on what the Go backend now serves:
- `prompts/1-spec-023-http-router-read-routes-and-parity-harness.md`
- `prompts/2-spec-023-mutating-routes-and-write-parity.md`
- `prompts/3-spec-023-websocket-watcher-and-frame-parity.md`

Read the Python backend tree so the README describes it accurately: `src/vault_ui/` (`__main__.py`, `factory.py`, `api/`, `websocket/`, `static/`), and `tests/` (the retained Python suite). There is **no** `README` under `src/vault_ui/` today — you are creating it.

Read `specs/ideas/` for the existing follow-up-document style (`migrate-frontend-to-typescript.md`, `rename-to-vault-dashboard.md`) and mirror it.

Read the changelog guide: `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.
</context>

<requirements>

### 1. Create `src/vault_ui/README.md`

Create the file with a marker that satisfies the spec's evidence check — `grep -rn 'SUPERSEDED' src/vault_ui/README*` must return at least one line. Make the marker the first thing a reader sees, and make it unambiguous:

- State that this Python backend is **SUPERSEDED** by the Go backend (`github.com/bborbe/vault-ui`).
- State that it is kept in-tree for exactly one rollback window and must not receive new features, routes, or behavior changes.
- State that the Go backend is the service that serves the HTTP and WebSocket surface, and point at the Go entry points (`main.go`, `pkg/`) so a reader can find the successor.
- Point at the removal follow-up document created in requirement 2 by its repo-relative path.
- Note that `src/vault_ui/static/` is frozen and shared: the Go backend serves it byte-identically, so the frontend must not be edited here either.
- Note that the Python test suite under `tests/` is retained and must stay green for the duration of the rollback window.

Keep it short — a banner and a few paragraphs. Do not document the Python internals; that is what the follow-up will retire.

### 2. File the removal follow-up as a named document

Create `specs/ideas/remove-superseded-python-backend.md`, following the style of the existing files in `specs/ideas/`. It must:

- Name the goal: remove the superseded Python backend once the rollback window closes.
- List what must be verified before removal, at minimum:
  - the Go backend serves all 25 routes and the parity harness is green;
  - the operator has run the cutover and the live board is served by the Go binary;
  - the rollback window has elapsed with no rollback;
  - `src/vault_ui/static/` is still required by the Go backend (it is served from there) and must **not** be deleted with the rest of the tree;
  - the Python test suite (`tests/`) and the packaged Python entry point (`pyproject.toml`, the `vault-ui` script) can be retired together, and nothing else references them.
- List what the removal touches: `src/vault_ui/` except `static/`, the Python-only tests, the `pyproject.toml` entry point, and the Makefile targets that still run Python (`sync`, `format`, `lint`, `typecheck`, `check`, `test`'s pytest step, `run`, `watch`).
- Explicitly state that removal is a separate, later piece of work — not part of this spec.

Keep it an idea document, not an implementation plan: it records intent and the preconditions, not a step-by-step script.

### 3. Do not touch the Python source, the tests, or the frontend

This prompt adds documentation only. Do not delete, rename, or edit any Python module, any test, `src/vault_ui/static/`, `pyproject.toml`, or the Makefile. The only files you create are the README marker, the follow-up document, and the changelog entry.

### 4. Reference the follow-up from the completion note

In your completion report for this prompt, reference `specs/ideas/remove-superseded-python-backend.md` by path as the filed removal follow-up, so the spec's completion note can point at it.

### 5. Update the changelog

Add an `## Unreleased` entry recording that the Python backend is marked superseded and its removal filed, following `changelog-guide.md` (prefix + specific).

### 6. Confirm the negative cutover criterion

The spec requires that no dark-factory prompt performs the service switchover. Confirm — do not implement — that this prompt and its three siblings contain no service-restart, plist-edit, or tool-reinstall command, and report the confirmation.

**Open question for the human reviewer (do not resolve it here):** the spec's evidence check for that criterion greps `prompts/ --include='spec-<NNN>-*'`. This spec's prompts are named `1-spec-023-…` through `4-spec-023-…` (the repo's established ordering-prefix convention, matching the spec-022 prompts), so that include-glob matches **none** of them and the check passes vacuously rather than by inspection. The spec's intended scope was the generated prompts for this spec. Flag this in your completion report so the reviewer can decide whether to widen the glob (e.g. to `*-spec-023-*`) when verifying.

### Self-check before finishing

Re-run the `<verification>` commands. Confirm the marker grep returns ≥1 line and that the change is documentation-only.

</requirements>

<constraints>
- **Never code directly** — this repo's `CLAUDE.md` mandates the dark-factory pipeline (spec → prompts → daemon) for all code changes.
- **Documentation only** — do not delete or edit the Python source, the Python tests, `src/vault_ui/static/`, `pyproject.toml`, or the Makefile.
- **Frozen static tree** — `src/vault_ui/static/` (index.html, app.js, style.css) is out of scope and must not change; the Go backend serves it byte-identically.
- **Do NOT delete the Python source in this spec** — it is marked superseded and its removal is filed as a follow-up so one rollback window exists.
- **The cutover runbook is a direct doc change** — authored outside the dark-factory pipeline. Nothing in this prompt authors it, references its commands, or performs the service switch.
- **Do NOT deploy to a cluster** — single host; no dev/prod cluster exists or is invented.
- **Do NOT add automatic rollback tooling, a `backend: go|python` switch flag, or a runtime fallback to Python.** The cutover is a manual operator step.
- **Production-touching, operator-gated cutover** — the cutover stops and replaces a running service and must be an explicit operator step, never an unattended prompt action.
- **Do NOT commit** — dark-factory handles git.
- **Existing tests must still pass** — the retained Python suite stays green while the backend is superseded.
</constraints>

<verification>
Run inside the container:

```
make precommit
```

Expected: exit 0 — the retained Python suite still green and the Go checks unchanged. Adding two markdown files must not alter any test outcome.

```
grep -rn 'SUPERSEDED' src/vault_ui/README.md
```

Expected: ≥1 matching line.

```
ls specs/ideas/remove-superseded-python-backend.md
```

Expected: the follow-up document exists.

```
sha256sum src/vault_ui/static/index.html src/vault_ui/static/app.js src/vault_ui/static/style.css
```

Expected: the three hashes are unchanged from the start of this prompt — record them before you begin and compare after. The frozen static tree is the asset most easily damaged by a broad edit; if a hash moved, revert the change.

Do NOT try to prove this change is documentation-only with a version-control diff. This container runs with `hideGit: true`, so `.git` is masked and any VCS invocation dies with `fatal: not a git repository` — which would silently pass as a "check that ran". Verify by inspecting the files you touched instead.
</verification>
