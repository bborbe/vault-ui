---
status: completed
spec: [025-lazy-pane-resolution-at-jump-time]
summary: 'Re-baselined the parity harness for the intentionally dropped jump_pane field, moved the cutover runbook''s static-tree regression guard off the pinned 798d901 SHA, and recorded the change under ## Unreleased.'
execution_id: vault-ui-lazy-pane-exec-134-spec-025-parity-rebaseline-and-changelog
dark-factory-version: dev
created: "2026-10-06T06:16:43Z"
queued: "2026-10-06T06:48:20Z"
started: "2026-10-06T08:09:36Z"
completed: "2026-10-06T08:15:29Z"
pr-url: https://github.com/bborbe/vault-ui/pull/127
branch: dark-factory/134-spec-025-parity-rebaseline-and-changelog
---

# Re-baseline the parity harness and record the change

<summary>
- The Go-to-Python parity harness passes again after the intentional removal of the pane field from the task list response.
- The re-baseline is narrow: only the one intentionally dropped field is filtered out of the comparison, and every other key of every other route stays compared.
- The harness's own self-test still proves it rejects an injected divergence, so the new filter cannot have turned it vacuous.
- The static-asset section records that the frontend changed on purpose with this work.
- The cutover runbook's static-tree regression guard moves to the new parity baseline.
- The changelog carries one entry describing the on-demand pane resolution.
</summary>

<objective>
Make `make parity` exit 0 again by re-baselining exactly the two surfaces this spec intentionally moves — the `/api/tasks` body comparison (the Go backend no longer emits `jump_pane`; the Python reference still does) and the static-tree baseline in the cutover runbook — and record the change under `## Unreleased`.
</objective>

<context>
Read `CLAUDE.md` at the repo root if present (it may be absent in this checkout) and `docs/dod.md`.

Read the spec `specs/in-progress/025-lazy-pane-resolution-at-jump-time.md`. This prompt covers Desired Behavior 8 and Acceptance Criterion AC9. It must run LAST: it re-pins the surfaces the earlier prompts move.

Prerequisites: prompts 1–4 changed the Go backend's task response (no `jump_pane`), the frontend (`src/vault_ui/static/app.js` no longer reads `jump_pane`), and added `pkg/sessionstate`. Read `scripts/parity/parity.sh`, `scripts/parity/cases.txt`, `scripts/parity/routes.txt` and `docs/go-cutover.md` before editing.

Read these before writing (current shapes verified):

- `scripts/parity/parity.sh` — `normalize()` is the shared body normalizer: `jq -S . "$1" 2>/dev/null || cat "$1"`. It is used by the route table loop, the extra-read-cases loop, the error-cases loop, and as `normalize_mutation`'s non-object fallback. The static-asset section compares `py_hash` against `go_hash` for `index.html`, `app.js?v=parity` and `style.css?v=parity`, both served from `src/vault_ui/static/` (the Go binary embeds that tree; the Python backend mounts it from `Path(__file__).parent / "static"`, which resolves to the same files in an editable install).
- `scripts/parity/cases.txt` — every `tasks-*` case is a `/api/tasks?...` request, so all of them carry task objects.
- `scripts/parity/routes.txt` — the `/api/tasks` route row. `/api/goals`, `/api/topics` and `/api/topics/{id}` carry no pane field: `api.TopicDetailResponse.Tasks` is `[]string` of task ids, not nested task responses.
- `scripts/parity/selftest.sh` — builds throwaway binaries with `-tags parity_selftest_*` and asserts the harness rejects each injected divergence. It runs with `PARITY_ROUTES=routes-read.txt` and `PARITY_MUTATIONS=mutations-selftest.txt`, so `/api/tasks` IS in its read set.
- `docs/go-cutover.md` — § 6 "Regression guard" pins the static-tree comparison to `798d901`, and the "Before you start" section repeats that SHA in the parity-claim sentence. Both are stale after this change.
- `CHANGELOG.md` — its newest heading is `## v0.81.0`; there is no `## Unreleased` section. The file's style is one long `feat:` bullet per change, semicolon-separated, naming the mechanism and the measured effect.

Coding guides (in-container paths):

- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`

Resolved note on AC9's evidence (verified against the code, not an open question): the harness holds no stored static hash. `scripts/parity/parity.sh` hashes both backends' served static bytes live, and both serve the same `src/vault_ui/static/` tree (Go embeds it, Python mounts it), so a frontend edit moves both sides together and the static comparison stays green with no harness edit. `cases.txt` and the other case tables are deliberately untouched — requirement 1 changes only `normalize()`. The narrowness AC9 asks for is therefore established by `make parity`'s summary reporting every ratio full (a route that lost its comparison drops below full), and the static-tree re-baseline is the `docs/go-cutover.md` § 6 guard that requirement 3 moves. The spec's AC9 already reads this way.
</context>

<requirements>

### 1. Re-baseline the `/api/tasks` body comparison (`scripts/parity/parity.sh`)

The Go backend intentionally no longer emits `jump_pane` in a task response (spec 025: the pane is resolved when the jump control is clicked, never on a list read). The Python reference still emits it. Teach `normalize()` to drop that one field from both sides, so every other key stays compared:

```bash
normalize() {
  # `jump_pane` is intentionally absent from the Go backend's task responses
  # (spec 025 — a session's pane is resolved when the jump control is clicked,
  # never on a list read). The Python reference still emits it, so it is removed
  # from BOTH sides before comparison. Every other key stays compared: widening
  # this filter would silently drop the safety net for the routes this change
  # does not touch.
  jq -S 'if type == "array"
         then map(if type == "object" then del(.jump_pane) else . end)
         elif type == "object" then del(.jump_pane)
         else . end' "$1" 2>/dev/null || cat "$1"
}
```

The type guards are load-bearing: `del(.jump_pane)` on a non-object is a `jq` error, and `normalize()` is also used for error bodies (single-key objects), empty bodies and `normalize_mutation`'s non-object fallback. The `|| cat "$1"` fallback must survive unchanged.

Do **not** change `routes.txt`, `cases.txt`, `errors.txt`, `mutations.txt` or `selftest.sh`. The re-baseline is one field in one function.

### 2. Record the static-tree reality (`scripts/parity/parity.sh`)

The comment above the static-asset loop currently reads `# Static assets: byte identity (query strings ignored for path resolution).` Extend it so it records that `app.js` changed on purpose with spec 025 (the frontend no longer reads `jump_pane`) and that the check remains a live comparison of the two backends' served bytes — the Python backend mounts `src/vault_ui/static/` and the Go binary embeds the same tree, so both sides move together and no stored baseline hash exists to re-pin. Change no code in that loop.

### 3. Move the cutover runbook's regression guard (`docs/go-cutover.md`)

The static tree is no longer frozen at `798d901`. Remove every occurrence of that SHA — four lines: the two in the intro parity-claim paragraph (~lines 14–15), the § 6 command (~line 228), and the "since `798d901`" prose (~line 232):

- In the intro paragraph above "Before you start", the parity-claim sentence ("last passed at **`798d901`**") and the next sentence that names the same SHA. Drop the pinned-SHA claim: say parity was last re-established when spec 025 changed the frontend and the task-list body, and keep the instruction to re-run `make parity` (in the container) when deployed code has moved past what parity last covered.
- In § 6 "Regression guard", the pinned `git diff --stat 798d901 origin/master -- src/vault_ui/static/` command.

Replace the pinned SHA with a baseline the operator resolves at run time, and rewrite the surrounding prose:

```bash
cd ~/Documents/workspaces/vault-ui
git fetch origin
# The static tree intentionally changed with lazy pane resolution (spec 025), so the
# parity baseline for the frontend moved with it. Resolve that baseline as the commit
# that removed the frontend's `jump_pane` read — a fixed point: `-S` only matches
# commits that change the token's count in app.js, and it stays at zero afterwards
# (tests/test_jump_control.py forbids it anywhere in app.js):
BASELINE=$(git log --format=%h -1 -S'jump_pane' origin/master -- src/vault_ui/static/app.js)
git diff --stat "$BASELINE" origin/master -- src/vault_ui/static/
```

- The sentence "The frontend is frozen, and this compares the parity baseline against what you are about to deploy" must stop claiming the frontend is frozen. Say that the frontend changed once, deliberately, with lazy pane resolution, and that the guard now derives its baseline from the commit that removed the frontend's `jump_pane` read, so it keeps meaning "nothing has changed the static tree since parity was last re-established". Say why it is that commit and not simply the last commit touching `app.js`: the latter would re-anchor on every later frontend change, so the guard could never report one.
- Keep the existing paragraph explaining why a *working-tree* diff (`git diff origin/master -- …`) is the wrong form — it is still correct and still the reason this guard is written the way it is.
- Do not touch any other step of the runbook.

The block above is content to write into `docs/go-cutover.md` — do not execute it. The container has no working `.git` (`.dark-factory.yaml` sets `hideGit: true`), so the SHA cannot be resolved here — that is why the baseline is written as the derivation above rather than as a literal.

### 4. CHANGELOG

Create `## Unreleased` immediately above `## v0.81.0` and add one `feat:` bullet in the file's existing style — a single long sentence naming the mechanism and the effect. It must cover:

- A task's WezTerm pane is now resolved in Go when the card's jump control is clicked, instead of by a three-second background refresher that shelled out to `who-needs-me.py` per live session.
- `pkg/panecache` and every Go→Python shell-out are deleted; the pane id is gone from the task list response.
- The live badge and the jump control follow session state kept current by a new `pkg/sessionstate` watcher over `~/.claude/sessions/*.json` — an initial read, file events, and a 60-second rescan as the safety net — so neither depends on a timer or on pane data; the classification contract in `docs/liveness-classification.md` is unchanged.
- The parity harness is re-baselined for the intentionally dropped field.

Do not add a version heading, do not bump the version, and do not touch any released section.

### 5. Self-check

Before finishing, re-run every `<verification>` command and confirm each passes. Then walk only the AC9 parts this prompt owns — `make precommit` exit 0, `make parity` exit 0 with every ratio full (this full-ratio output is the narrowness evidence), the `docs/go-cutover.md` guard moved, and the `## Unreleased` CHANGELOG bullet — and name the command output that establishes each. The `docs/pane-resolution.md` headings and the `Resolve(ctx` signature are prompt 4's AC9 contribution, not this prompt's; do not claim them.

</requirements>

<constraints>
- Every route other than the one field named in requirement 1 keeps its response body, status codes, query parameters and WebSocket frame content byte-identical to the Python reference (spec 023 parity contract). The re-baseline is narrow by design — widening it would silently drop the safety net for routes this change does not touch.
- Do NOT weaken `scripts/parity/parity.sh` in any other way: no route removed from a table, no comparison skipped, no diagnostic suppressed, no exit path softened. `make parity-selftest` must still prove the harness rejects every injected divergence.
- The spec `024-serve-list-reads-from-page-index` also touched `/api/tasks`; do not re-pin a body that spec is still changing.
- `docs/go-cutover.md` is an operator-run runbook — edit its prose and commands only; never add a step that a dark-factory prompt is expected to perform.
- No new HTTP route, no new query parameter, no opt-out flag.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- Every path this prompt has the agent resolve is repo-relative. The `~/Documents/workspaces/vault-ui` paths in requirement 3 are runbook content written into `docs/go-cutover.md`, not paths the agent resolves.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run every command below with `export PATH=/usr/local/go/bin:$PATH` first.

```
make precommit
```
Must exit 0. Run this FIRST: `make parity` needs the Linux `.venv` that `make precommit` builds.

```
make parity
```
Must exit 0 and print the full summary with every parity line at its full ratio (`routes: 25/25 matched`, `body-parity: N/N`, `error-parity: N/N`, `mutation-parity: N/N`, `ws-parity: frames identical`, `static-parity: 3/3 byte-identical`). If it dies before printing the comparison (e.g. `Error 127` in `build_vault_cli`), that is an environment failure, not a pass — fix `PATH` and re-run.

```
make parity-selftest
```
Must exit 0. This is the guard against a vacuous re-baseline: the self-test injects four deliberate divergences and asserts the harness rejects each. A normalization that swallowed too much would make one of them pass and this target would fail.

```
awk '/^## /{sec=$0} sec=="## Unreleased" && /pane/{print; exit}' CHANGELOG.md
```
Must print a line — a bullet mentioning the pane resolution sits under `## Unreleased`. Use this section-walking form, never a line-scoped `grep -A<n>` window, which can swallow the neighbouring section.

```
! grep -q '798d901' docs/go-cutover.md
```
Must exit 0 — no stale parity baseline is left in the cutover runbook.

```
grep -qF -- "-S'jump_pane'" docs/go-cutover.md
```
Must exit 0 — the guard is anchored on the spec-025 frontend commit, not on the latest `app.js` commit.

```
grep -q 'jump_pane' scripts/parity/parity.sh
```
Must exit 0 — the re-baseline is present in the harness.

```
! grep -q 'jump_pane' scripts/parity/routes.txt scripts/parity/cases.txt scripts/parity/errors.txt scripts/parity/mutations.txt scripts/parity/selftest.sh
```
Must exit 0 — the re-baseline did not spread into the case tables.

```
grep -A6 '^# Static assets' scripts/parity/parity.sh | grep -q 'spec 025'
```
Must exit 0 — requirement 2's static-asset comment records the intentional frontend change.
</verification>
