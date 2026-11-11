---
status: approved
spec: [038-topic-card-data-parity]
created: "2026-11-10T07:10:00+01:00"
queued: "2026-11-11T07:04:38Z"
---

# Assert the task and goal cards did not change

<summary>

- A test compares the task and goal card definitions against the baseline captured before this change
- Both slot sets must match exactly — no slot added, none removed
- Both mapper expression maps must match exactly, so a changed expression fails, not just a changed key
- The topic card is excluded from the comparison, because the change is required to alter it
- The comparison is structural, over the embedded asset, and needs no JavaScript runtime
- A regression on the task or goal boards fails the suite rather than shipping

</summary>

<objective>

Lock the task and goal card definitions to the pre-change baseline, so the work that enabled six topic slots cannot quietly change what a task or goal card renders.

</objective>

<context>

Read `pkg/board/testdata/card-vectors-pre-change.json` — the baseline captured by the first prompt, before the frontend changed.

Read `pkg/board/jsasset` — the shared parser that reads the embedded asset into card-config and mapper structures.

Read `src/vault_ui/static/app.js` — `const CARD_CONFIG` and the three mapper functions, whose current state this test compares against the baseline.

</context>

<requirements>

1. Add a Go test that loads the baseline fixture and the current embedded asset through the shared parser.

2. Assert the `task` and `goal` entries of `CARD_CONFIG` have exactly the enabled slot set recorded in the baseline — no slot added, none removed.

3. Assert the `mapTaskCard` and `mapGoalCard` object literals have exactly the key-to-source-expression map recorded in the baseline. A changed expression is a failure, not just a changed key.

4. Exclude the `topic` entry and `mapTopicCard` from the comparison. The preceding prompt changes both by design; including them would make this test unsatisfiable. Add a short comment in the test stating why they are excluded, so a later reader does not "fix" the omission.

5. If the comparison fails, the failure message must name which entry and which key diverged, so the cause is readable without re-running the parser by hand.

6. Before finishing: run `make precommit` and confirm it passes. Then confirm the test is load-bearing: flip one enabled `task` slot in `CARD_CONFIG` to `false`, run the new test, and confirm it fails naming that entry and slot; then flip it back to `true` by editing the file — this repo has no git inside the container, so never rely on `git checkout` to revert — and re-run `make precommit` to confirm green again. Do the same for one mapper expression, since requirement 3 compares expressions and a slot flip alone does not exercise it. Finally, walk each requirement above against the change.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- The baseline fixture is read, never rewritten by this prompt.
- No new dependency.

</constraints>

<verification>

`make precommit` — must pass.

</verification>
