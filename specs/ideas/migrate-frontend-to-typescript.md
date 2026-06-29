---
tags:
  - dark-factory
  - spec
status: draft
---

## Summary

- Convert the single-file frontend (`static/app.js`, ~1400 LOC of vanilla JS) into a typed TypeScript source bundled with esbuild.
- Add a build toolchain (`package.json`, `tsconfig.json` strict mode, esbuild) wired into `make precommit`, `make run`, and `make watch`.
- Hand-mirror the backend pydantic models (`api/models.py`) as TypeScript interfaces so payload field-name typos fail the build instead of shipping silently.
- No functional UI changes — every shipped feature must behave byte-identically before and after.
- Foundation only. Module split, component framework, JS test framework, and OpenAPI-driven type generation are explicit non-goals here and belong to later specs.

## Problem

Two real pain classes have already hit this codebase:

1. **Typo-class bugs.** Two recent regressions referenced `currentAssignee` (singular) where the runtime field was `currentAssignees` (plural). Both were caught only by manual audit; without that step they would have shipped silently broken. Vanilla JS has no compile-time defense against this.
2. **Refactor coupling.** Functions like `parseURLParams`, `updateURL`, and `loadTasks` share field names by convention only. Renaming a single field requires manually following call chains. Each new URL-passthrough feature (status, goal, soon theme) widens the blast radius.

The frontend is 1400 LOC and growing. Migrating now, while it is still single-file, is cheap. Migrating after widget code is smeared across multiple modules will be expensive, and the typo + refactor classes will already have cost more bugs.

## Goal

After this work, the frontend source is TypeScript with strict typing, compiled and bundled by esbuild into a single artifact that FastAPI serves at the existing static URL. `make precommit` fails on type errors. The operator's daily loop (`make run`, `make watch`) keeps working without a manual extra step. Every UI feature behaves the same as it did before the migration.

## Non-goals

- No component framework migration (Svelte / Lit / Preact / React) — separate decision.
- No splitting the TypeScript source into multiple modules — stays one file at first.
- No JavaScript / TypeScript unit-test framework — no JS test infra exists today; defer.
- No auto-generated TypeScript types from the FastAPI OpenAPI schema — future spec.
- No CSS preprocessor — `style.css` stays vanilla.
- No new runtime dependencies in the bundled output.
- No functional UI changes, no new features, no behavior tweaks.

## Desired Behavior

1. The frontend source lives in TypeScript. Editing the UI means editing a `.ts` file; the prior `.js` source is removed.
2. A build step produces a single bundled JavaScript file plus a source map. FastAPI serves both at the existing static URL pattern. The browser loads exactly one `<script>` tag, as today.
3. The TypeScript compiler runs in strict mode. Any field-name typo (e.g. `currentAssignee` vs `currentAssignees`), wrong argument type, or unhandled `null`/`undefined` at a typed seam fails the build.
4. Backend payload shapes (`Task`, `Goal`, `Vault`, WebSocket events) are declared as TypeScript interfaces that mirror the pydantic models in `api/models.py`. The mirroring is hand-maintained and documented as the source of truth for now.
5. `make precommit` runs the TypeScript type-check and the bundle build alongside the existing Python checks. Any failure in either fails the gate.
6. `make watch` runs Python auto-reload AND esbuild watch mode together so the operator's edit-save-refresh loop sees both backend and frontend changes without a second terminal.
7. Source maps map browser stack traces back to TypeScript line numbers in DevTools.
8. The bundled output is a build artifact, not a source artifact. CLAUDE.md states this so future contributors do not edit the bundle by hand.
9. Every UI feature shipped today continues to work without operator-visible regression: multi-vault selector, status dropdown, multi-value URL params (`vault`, `status`, `assignee`, `goal`), "Assign to me" link, drag-drop across phases, modals, toast notifications, WebSocket reconnection.

## Constraints

- UI behavior must be byte-identical after migration. No functional changes ride along.
- `make precommit` must pass — primary automated gate.
- `make run` and `make watch` must continue to work for the operator's daily loop without a separate manual build step.
- The bundled `<script>` must remain a single file with no new runtime dependencies.
- Source-map files (`.js.map`) must be served correctly by FastAPI's existing static-files mount. If the current mount does not pass `.map` through with the correct content type, the mount must be adjusted as part of this work.
- Strict mode is enabled from day one. Any `any` annotation requires an inline comment explaining why narrowing is not yet possible. There is no plan to relax strict mode later.
- Node engine pin in `package.json` is `>=22` to align with the claude-yolo Docker image (`FROM node:22`, verified). esbuild and typescript install as dev dependencies (`npm install --save-dev`).
- The build artifact is gitignored. The build runs as part of `make precommit` (which is the local gate), `make run`, and `make watch` — operators never need to commit the bundle, and CI / dark-factory verification runs the same `make` targets.
- No changes to Python source apart from (a) the static-files mount if it does not already serve `.map` files, and (b) any path the bundler writes to. No changes to API shapes.

## Assumptions

- **claude-yolo container has Node 22.** Verified by reading `~/Documents/workspaces/claude-yolo/Dockerfile` (`FROM node:22`). esbuild + typescript install cleanly via `npm install --save-dev`. No container changes needed.
- **Frontend is a single source file today.** `static/app.js` is ~1400 LOC of vanilla JS with no module imports; one source-file rename is sufficient.
- **Pydantic models in `api/models.py` are the canonical payload shapes.** Hand-mirroring them in TypeScript interfaces is correct as long as the Python side is the source of truth. Drift is possible but caught by manual review until a future spec automates this.
- **FastAPI's `StaticFiles` mount serves arbitrary file extensions.** If true, `.js.map` works without code change. Verify during implementation; if false, adjust the mount.
- **`make watch` can run two long-lived processes.** Use a standard backgrounding pattern (e.g. `&` plus `wait` or `concurrently`-equivalent shell construct). The exact mechanism is an implementation detail; the constraint is that Ctrl-C cleanly stops both.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Type error in `app.ts` | `make js-check` exits non-zero; `make precommit` fails with the type error printed | Operator reads error, fixes types, retries |
| Bundle build fails (esbuild error) | `make js-build` exits non-zero; `make precommit` fails | Operator reads esbuild output, fixes, retries |
| Bundled JS throws at runtime (logic bug) | Browser console shows the error; source map maps the stack trace to `.ts` line numbers | Operator opens DevTools, locates `.ts` source, fixes |
| Source map missing or wrong | DevTools shows bundled / minified line numbers; harder to debug but UI still works | Operator notices and re-investigates the build config; not a runtime failure |
| `make watch` foreground process is killed but background process keeps running | Both processes must terminate together on Ctrl-C; spec failure if not | Implementation must trap signals or use a wrapper; verify during acceptance |
| FastAPI serves `.map` with wrong content type | DevTools fails to load source map silently or with a console warning; functionality unaffected | Adjust static-files mount; covered by acceptance criterion |
| esbuild or typescript upgrade introduces a breaking change | Lockfile pins versions; upgrade is an explicit operator action; failure surfaces in `make js-check` / `make js-build` | Operator pins or updates as a separate change |
| Operator forgets to run the build before opening the browser | `make run` and `make watch` invoke the build, so this should not happen; bare `uv run vault-ui` would serve a stale or missing bundle | `make run` is the documented entry point |
| Bundled output not present in a fresh checkout | First `make` target that needs the bundle runs the build; no developer-visible breakage | None — this is the expected flow |

## Security / Abuse Cases

- The build pipeline runs locally and inside the claude-yolo container only. esbuild and typescript are dev-time dependencies; they do not ship to any user.
- The bundled output is the same trust boundary as the prior `app.js` — it runs in the operator's browser against a localhost server. No new attack surface.
- npm dependency supply-chain risk is real. Mitigated by (a) keeping the dependency set tiny — esbuild and typescript only — (b) committing `package-lock.json` for reproducible installs, and (c) no `postinstall` scripts from the dependency tree are needed for build to succeed; if any appear during install, treat as a signal to audit before accepting.
- Source maps are useful in production for debugging but expose source structure. Acceptable here because the deployment is a single-operator localhost tool, not a public web app.

## Acceptance Criteria

- [ ] `make js-build` produces `src/vault_ui/static/dist/app.js` and `src/vault_ui/static/dist/app.js.map`.
- [ ] `make js-check` exits 0 against the converted source.
- [ ] `make precommit` runs both `js-check` and `js-build` alongside the existing Python checks, and passes.
- [ ] `make watch` starts both Python auto-reload and esbuild watch mode; Ctrl-C stops both cleanly.
- [ ] `make run` ensures the bundle is built before the server starts.
- [ ] `index.html` loads exactly one `<script>` tag pointing at the bundled output. The browser console is free of errors on initial load.
- [ ] DevTools opens the original `.ts` source via the source map and a stack trace from a thrown error maps back to `.ts` line numbers.
- [ ] The prior `static/app.js` source file is removed; the source of truth is the new `.ts` file.
- [ ] `node_modules/` and `src/vault_ui/static/dist/` are gitignored.
- [ ] `package.json` declares Node engines `>=22` and lists `esbuild` and `typescript` as dev dependencies. `package-lock.json` is committed.
- [ ] `tsconfig.json` enables strict mode (`"strict": true`); any `any` in the converted source carries an inline comment justifying it.
- [ ] TypeScript interfaces exist for `Task`, `Goal`, `Vault`, and the WebSocket event payloads, mirroring the fields declared in `api/models.py`.
- [ ] FastAPI serves `.js.map` files with a content type that DevTools accepts (no console warnings about source-map load failure).
- [ ] CLAUDE.md is updated to state that `static/<source>.ts` is the source of truth and `static/dist/` is a build artifact (do not edit by hand).
- [ ] Manual smoke checklist (see Verification) passes end-to-end against a running server.
- [ ] No regression in any existing pytest test; `make test` passes unchanged.
- [ ] **Scenario coverage: NO new scenario test.** No JS test infrastructure exists today and adding one is an explicit non-goal. The manual smoke checklist plus `make precommit` (which now compiles and bundles) is the gate.

## Verification

```
make precommit
make run
```

Then manual smoke checklist against a running server with the browser open at the served page:

1. **Multi-vault selector** — open dropdown, toggle two vaults on/off, observe URL updates with comma-separated `vault=` parameter, observe board re-renders.
2. **Status dropdown** — toggle multiple statuses, observe URL `status=` parameter updates, observe board filters.
3. **Multi-value URL params on initial load** — open `?status=todo,in_progress`, `?assignee=,bborbe`, `?goal=<some-goal>` and confirm each filter is applied on first paint.
4. **"Assign to me" link** — click on an unassigned task; the task becomes assigned to the operator and the card updates.
5. **Drag-drop** — drag a card between two phases; backend call fires; card stays in the new phase after refresh.
6. **Toast on backend error** — trigger an error (e.g. stop the backend mid-action) and confirm a toast appears with stderr text.
7. **WebSocket reconnect** — restart the backend; the WS-status indicator goes red, then green again within a few seconds; live updates resume.
8. **DevTools source map** — open DevTools → Sources → confirm the original `.ts` file is present and a `console.trace()` (or any thrown error) maps to `.ts` line numbers, not bundled-JS line numbers.
9. **Single `<script>` tag** — view page source; confirm exactly one `<script>` tag is present and it points to the bundled artifact.

## Do-Nothing Option

Doing nothing keeps shipping the two pain classes. The `currentAssignee` / `currentAssignees` typo will recur as new fields are added. Refactoring URL parameters will keep being a manual call-chain trace. The frontend will keep growing — every additional LOC makes the migration more expensive, because it spreads typo-prone field references across more sites and risks letting people invent module boundaries that the migration would later have to retrofit. The migration is cheap exactly while the source is one file. Waiting is strictly worse.
