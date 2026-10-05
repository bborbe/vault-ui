---
status: completed
summary: Rendered an empty goal status as an empty string in the Go backend by assigning &item.Status instead of strPtr(item.Status), pinned with a ListGoals table test and a parity fixture goal, matching the Python backend's JSON type.
execution_id: vault-ui-exec-125-render-empty-goal-status-as-an-empty-string
dark-factory-version: v0.196.0
created: "2026-10-05T00:00:00Z"
queued: "2026-10-05T07:19:39Z"
started: "2026-10-05T07:20:28Z"
completed: "2026-10-05T07:24:32Z"
branch: dark-factory/125-render-empty-goal-status-as-an-empty-string
---

# Render an Empty Goal Status as an Empty String, Not Null

<summary>
- The Go backend returns `null` for a goal whose status is empty.
- The Python backend returns an empty string for the same goal.
- The two therefore disagree on the JSON type of that field, which breaks the parity contract.
- After this change both return an empty string, and the field's type matches.
- Task status is already correct on both sides and must not change.
- The shared helper that causes this must not change, because other fields rely on it.
- A test pins the empty-status case so it cannot regress.
- The parity harness gains a fixture goal that exercises an unrecognised status.
- No other field's rendering changes, and no status value is reinterpreted.
- No API shape other than that one field's empty case changes.
</summary>

<objective>
Make the Go backend render an empty goal status as an empty string, matching the Python backend, without altering how any other field renders. The end state is that a goal whose status vault-cli reports as empty serialises identically on both backends.
</objective>

<context>
Read these before changing anything:

- `pkg/board/goals.go` — the goal renderer; the `Status` field is built with `strPtr(item.Status)`.
- `pkg/board/tasks.go` — the task renderer. Its `Status` field is assigned the plain string, which is already the correct shape; read it as the model to follow.
- `pkg/board/helpers.go` — `strPtr` returns nil for an empty string. That behaviour is correct for the date and optional fields it is also used for, so it is not what changes here.
- `src/vault_ui/api/models.py` — the Python goal model declares `status: str | None`, and the Python backend passes vault-cli's value through, so an empty status serialises as `""`.
- `scripts/parity/parity.sh` — `write_vault()`, where a goal can be added to the fixture.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega conventions.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-test-types-guide.md` — which test type belongs at which level.

Background you need: vault-cli reports a goal status it does not recognise as an empty string. The real vault contains at least one such goal, so this is reachable in production, not hypothetical. Confirmed against the running service: `/api/goals` returns `"status": null` from the Go backend and `"status": ""` from the Python backend for the same goal.
</context>

<requirements>

1. **Make the goal status render as an empty string.** In `pkg/board/goals.go`, the goal's `Status` is currently produced by `strPtr(item.Status)`, which turns an empty string into a nil pointer and serialises as JSON `null`. Replace that single expression with `&item.Status` so the field always points at a string; an empty `item.Status` then serialises as `""`. `item` is the local copy inside `goalResponse`, so taking its address is safe. `GoalResponse.Status` is declared `*string` in `pkg/api/api.go` — do NOT change that declared type. Only this one assignment is in scope; `TaskResponse.Status` and the topic status fields are separate declarations and must not be touched. Keep the field's JSON name and position unchanged.

2. **Do not change `strPtr`.** It is used for date and other optional fields across `pkg/board` where a nil pointer — and therefore JSON `null` — is the correct rendering that already matches Python. Changing the helper would silently alter those fields. Scope the change to the goal `Status` assignment.

3. **Do not reinterpret any status value.** A non-empty status must serialise exactly as it does today. This change concerns only the empty case.

4. **Confirm the task path needs no change.** `pkg/board/tasks.go` assigns the task status as a plain string and is already correct. Verify this against the code and state in your report that you checked it, rather than assuming it.

5. **Add a boundary-crossing test.** Add a Ginkgo `DescribeTable` entry (or extend the existing goal table) asserting that a goal whose status is empty renders `""` and not `null`, exercised through the public entry point (`ListGoals`), not by calling an unexported helper. Follow the existing convention in `pkg/board` — an external `package board_test` file; do NOT add an internal `package board` test file. Include an entry with a non-empty status alongside it so the change cannot be satisfied by ignoring the field. With `Status` declared `*string`, assert the empty case as `Expect(responses[i].Status).NotTo(BeNil())` plus `Expect(*responses[i].Status).To(Equal(""))` — a nil pointer is exactly what renders `null` — and assert the non-empty entry as `Expect(*responses[i].Status).To(Equal(<value>))`. Extend the existing `Describe("ListGoals")` block in `pkg/board/board_test.go`, which already seeds `h.list.items["23 Goals"]` and calls `ListGoals`; do not invent a new harness.

6. **Add the harness case.** In `write_vault()` in `scripts/parity/parity.sh`, add a third goal file `"$VAULT/23 Goals/GoalThree.md"` with frontmatter `status: draft` — an unrecognised goal status, which vault-cli reports as empty. No new case entry is needed: `scripts/parity/routes.txt` already sends `GET /api/goals` with SEND `yes` and diffs the full body, and `normalize()` is only `jq -S`, which does not collapse `null` and `""`, so adding the goal to the fixture is sufficient. Do not add it to any mutation case in `mutations.txt`. Demonstrate that the case fails before the fix and passes after.

7. **CHANGELOG.** Add a bullet under `## Unreleased` in `CHANGELOG.md`. One bullet: `- fix: Render an empty goal status as an empty string rather than null, matching the Python backend's JSON type for that field.` Do NOT modify any existing section.

8. **Self-check.** Re-run the `<verification>` commands. Walk each acceptance criterion in `<summary>` and state, for each, the exact command output or test name that establishes it.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT modify `strPtr` in `pkg/board/helpers.go`, and do not change how any other field that uses it renders.
- Do NOT modify anything under `src/vault_ui/` — the Python backend is the behaviour contract.
- Do NOT modify anything under `src/vault_ui/static/` — the frontend is frozen and out of scope.
- Do NOT reinterpret, normalise, or validate status values; pass them through exactly as today.
- Do NOT change any other field's JSON name, type, position, or empty-case rendering.
- Do NOT weaken `normalize()` or any other comparison in `scripts/parity/parity.sh`.
- Keep `github.com/bborbe/vault-cli` pinned at v0.159.0 with no `replace` directive.
- Do NOT run `go mod vendor`; use `go get` plus `go mod tidy` if a dependency changes.
- Do NOT edit any `status:` frontmatter field.
</constraints>

<verification>
Run `make precommit` -- must pass.

Run `make parity` -- must pass. Run `make precommit` (which performs `uv sync`) first, so the container has a Linux `.venv` rather than a host-architecture one. If `make parity` dies before the comparison output is printed (for example `Error 127` inside `build_vault_cli`), that is an environment failure, not a pass: export `PATH=/usr/local/go/bin:$PATH` and re-run. Capture the before-fix failure and the after-fix pass for requirement 6 in your report, and leave the change applied.
</verification>
