---
status: cancelled
created: "2026-10-05T00:00:00Z"
queued: "2026-10-04T22:13:56Z"
cancelled: "2026-10-04T22:20:43Z"
---

# Fix Timestamp Precision Divergence in Board JSON and Guard the Parity Harness

<summary>
- The Go backend renders date-time fields in its JSON with a variable number of sub-second digits.
- The Python backend renders the same fields with exactly six sub-second digits, or none at all when the sub-second part is zero.
- The two therefore disagree on the literal text of those fields whenever a value carries more precision than a microsecond.
- After this change both backends render those fields identically, so the comparison can be literal.
- The comparison harness gains a case that deliberately exercises a timestamp with sub-microsecond precision.
- That new case fails against the current backend and passes once the rendering is aligned.
- Timestamps that fall exactly on a whole second keep rendering exactly as they do today.
- No field is renamed, added, removed or reordered, and no status code changes.
- The Python backend and the browser frontend are not modified.
- The harness comparison is not loosened to tolerate the difference.
</summary>

<objective>
Make the Go backend render every date-time field in its JSON byte-identically to the Python backend, and add a harness case that would catch a regression. The end state is a parity run that compares these fields literally and passes, with the divergence impossible to reintroduce silently.
</objective>

<context>
Read these before changing anything:

- `CLAUDE.md` — note that its Development and Architecture sections still describe the superseded Python backend under `src/task_orchestrator/`; the live Python package is `src/vault_ui/`.
- `docs/dod.md` — the validation prompt this run is judged against.
- `docs/go-cutover.md` — the cutover to the Go binary is already complete and must not be repeated or modified. Its "Known limits" section explains why `make parity` can die on `PATH` inside the container.
- `src/vault_ui/api/models.py`, `src/vault_ui/activity.py` and `src/vault_ui/vault_cli_client.py` — the Python behaviour contract for these fields.
- `pkg/board/tasks.go`, `pkg/board/goals.go` and `pkg/activity/activity.go` — the Go renderers, their call sites, and where the underlying timestamp comes from.
- `scripts/parity/parity.sh` — read `normalize()`, `normalize_mutation()`, `write_vault()`, and the read-case loop that consumes `scripts/parity/cases.txt`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega conventions.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-test-types-guide.md` — which test type belongs at which level.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md` — time handling rules.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — error wrapping rules.
</context>

<requirements>

1. **Establish the exact Python rendering empirically, inside the container.** There is no host-installed uv tool and no real Obsidian vault here. Use the repo venv that `make precommit`'s `uv sync` creates, from the repo root: `./.venv/bin/python -m uvicorn vault_ui.__main__:app --host 127.0.0.1 --port 8001`. Read `activity_date` from the parity fixture vault the harness builds — run `PARITY_KEEP=1 bash scripts/parity/parity.sh` to keep the temp workdir, then start uvicorn with `HOME=<kept-workdir>/fixture` so it reads that fixture's config and transcript root — not from a host vault. Record the observed rule in your report. Do not infer the rule from reading Python source alone.

2. **Fix the renderer.** In `pkg/board/tasks.go`, `dateTimeString` currently formats with `time.RFC3339Nano`, which strips trailing zeros and so emits between 0 and 9 fractional digits. Change it so the rendered string is:
   - `null` when the input is nil (unchanged), and otherwise
   - UTC, always with a literal `Z` suffix (unchanged), and
   - with **no fractional part at all** when the microsecond component is zero, and
   - with **exactly six** fractional digits otherwise, the nanosecond value truncated to microseconds.

   The zero test must be on the microsecond component, not the nanosecond component: a value of 500 nanoseconds has a zero microsecond component and must render with no fractional part. Truncate; do not round.

3. **Cover every call site.** `dateTimeString` is reached from `pkg/board/tasks.go` (both `ModifiedDate` and `ActivityDate`) and `pkg/board/goals.go` (`ActivityDate`). Confirm all three are covered by the single change and state in your report that you verified it.

4. **Audit, but do not change, the two sibling formatting sites.** `pkg/statuscache/statuscache.go` and `pkg/mutations/helpers.go` also use `time.RFC3339Nano`. The operative reason neither is changed here is that `pkg/mutations/helpers.go` writes task frontmatter rather than an API response body, so it is outside this prompt's contract; `pkg/statuscache/statuscache.go` must be assessed for whether it can reach a response body at all. Change neither. File whatever you find as a new spec file under `specs/ideas/` and name it in your report, so the follow-up is not lost.

5. **Add a boundary-crossing test.** Add a Ginkgo `DescribeTable` with `Entry` rows asserting the exact rendered string for at least these inputs: a whole second, a value with 500 nanoseconds (zero microseconds), a value with 3 fractional digits' worth of nanoseconds, one with 8, and one with 9. Assert through the public entry point (`ListTasks` / `ListGoals`), not by exporting or re-implementing `dateTimeString`. Follow the existing convention in `pkg/board` — an external `package board_test` file; do NOT add an internal `package board` test file.

6. **Add the harness case.** Add a case to the parity harness that exercises `activity_date` with a sub-second part whose precision exceeds microseconds, so the fixture can no longer be blind to this class of difference. The case must be a **read** case — compared by `normalize()` in `scripts/parity/parity.sh`, which compares timestamps literally. Do NOT add it as a mutation case: `normalize_mutation()` rewrites timestamps to `TIMESTAMP` and would make the case pass regardless. The fixture currently produces only second-precision timestamps, because its task files carry no session transcript and `activity_date` therefore collapses to vault-cli's second-precision `modified_date`. So you must inject a sub-second source — the reliable one is a session transcript file whose mtime you set to a value with more than six fractional digits (e.g. `123456789` nanoseconds) and **newer than the task file's mtime**: `activity_date` is the newer of the two, so a transcript mtime in the past leaves the case blind. The transcript must sit where both backends look — `${FIXTURE}/.claude/projects/<dir>/<session-id>.jsonl`, since `$HOME/.claude/projects` is the projects root and `HOME=$FIXTURE` for the run — and must be attached to a fixture task that already carries a `claude_session_id` (TaskTwo), or add one. Injecting that transcript into `write_vault()` is itself enough to make the existing `tasks-all` read case diverge on the unfixed backend; add a dedicated `cases.txt` row only if it makes the intent clearer. The case must satisfy both properties: (a) it fails against the unfixed Go backend, and (b) it passes once the rendering is aligned. Demonstrate both.

7. **Demonstrate the case fails before the fix.** With the harness case in place and the Go change temporarily reverted by editing `pkg/board/tasks.go` back to `time.RFC3339Nano` (not via git — the container masks `.git` because `hideGit: true`), run `make parity` and capture the failure output. Re-apply the fix and capture the pass. Include both outputs in your report. A case that passes on the unfixed backend is not a case.

8. **Do not loosen the comparison.** `normalize()` in `scripts/parity/parity.sh` must keep comparing these fields literally. Making the case pass by normalising timestamps in the harness instead of aligning the Go renderer is a failure of this prompt, not a solution.

9. **CHANGELOG.** Add a bullet under `## Unreleased` in `CHANGELOG.md`; the file currently has no `## Unreleased` heading (its top section is a released version), so create it. One bullet: `- fix: Render Go board JSON date-time fields with Python's microsecond precision (six fractional digits, omitted when the microsecond component is zero) so the parity harness compares them literally.` Do NOT modify any existing section.

10. **Self-check.** Re-run the `<verification>` commands. Walk each acceptance criterion in `<summary>` and state, for each, the exact command output or test name that establishes it.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT modify anything under `src/vault_ui/` — the Python backend is the behaviour contract and is retained for one rollback window.
- Do NOT modify anything under `src/vault_ui/static/` — the frontend is frozen and out of scope.
- Do NOT modify `pkg/mutations/helpers.go` or `pkg/statuscache/statuscache.go` in this prompt; requirement 4 asks only for an assessment and a follow-up spec.
- Do NOT re-run, edit or reference the cutover procedure as a step to execute; the service already runs the Go binary.
- Keep `github.com/bborbe/vault-cli` pinned at v0.159.0 with no `replace` directive.
- Do NOT run `go mod vendor`; use `go get` plus `go mod tidy` if a dependency changes.
- Do NOT weaken `normalize()` or any other comparison in `scripts/parity/parity.sh` to make a case pass.
- Preserve the `Z` UTC suffix, the `null` for absent values, and every field name, field order and status code.
- Do NOT add configuration flags, thresholds or "future-proofing" options; this is a rendering alignment only.
- Do NOT edit any `status:` frontmatter field.
</constraints>

<verification>
Run `make precommit` -- must pass.

Run `make parity` -- must pass. If it dies before the comparison output is printed (for example `Error 127` inside `build_vault_cli`), that is an environment failure, not a pass: export `PATH=/usr/local/go/bin:$PATH` and re-run. The revert demonstration is requirement 7 — capture its output in your report and leave the change applied.

Run `make parity-selftest` -- must pass.
</verification>
