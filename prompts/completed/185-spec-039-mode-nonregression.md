---
status: completed
spec: [039-mode-pill]
created: "2026-11-12T12:22:00+01:00"
queued: "2026-11-12T11:30:28Z"
---

# Lock the pre-change card affordances against a regenerated baseline

<summary>

- The pre-change record of what each card offered is now written into the test itself, not read back from the fixture
- The fixture moved in the same change that added the new slot, so comparing the fixture to itself would prove nothing
- Each kind's previously enabled affordances must all still be enabled, with the new slot allowed on top
- The task and goal card guard can no longer lose a kind without a test failing
- The topic card, which the fixture deliberately omits, gets the same protection from its own hardcoded record
- A silently dropped badge fails the suite instead of shipping on a card the operator relies on
- Only test assertions change — the board's behaviour is untouched
- The whole change is green whether or not the new slot has landed yet, so it never blocks the prompts it guards

</summary>

<objective>

Give the pre-change card affordances an independent lock, because the baseline fixture is extended by the same change that adds the mode slot — so the exact-match test would compare the new card config against a fixture that moved with it, and a slot dropped in that move would pass. This prompt ends with the task, goal and topic cards each proven to still offer everything they offered before, and the both-kinds guard proven unable to silently lose a kind.

</objective>

<context>

Read these before writing code.

- `pkg/board/jsasset/card_baseline_test.go` — the guard this prompt hardens: `baselinePath`, `cardVector`, `loadBaseline`, `cardSlots`, `TestTaskAndGoalCardDefinitionsMatchBaseline` (whose inline table has the `{kind: "task", mapperFn: "mapTaskCard"}` and `{kind: "goal", mapperFn: "mapGoalCard"}` rows), and `assertEnabledSlotsMatch` (the missing/extra divergence report to imitate). `sort` is already imported in this file.
- `pkg/board/jsasset/card_render_test.go` — `cardEntry(t, source, kind)` parses one `CARD_CONFIG` entry; `topicSlots(t, source)` is the existing topic-slot helper. `cardSlots(t, source, kind)` in the baseline test is the kind-generic form and is the one to reuse.
- `pkg/board/jsasset/jsasset.go` — `ParseObjectLiteral` returns a key-to-source-expression map; an enabled slot is the entry whose value is exactly `true`.
- `pkg/board/jsasset/jsasset_test.go` — `loadAsset(t)` returns the embedded `app.js` source.
- `pkg/board/testdata/card-vectors-pre-change.json` — the fixture prompt 2 extends with one `mode` slot and one `mode` mapper entry per covered kind. Read it; do not edit it.
- `src/vault_ui/static/app.js` — `const CARD_CONFIG`, the current slot maps the assertions read. Read only; do not edit.
- `specs/in-progress/039-mode-pill.md` — AC7 and DB6 are the criteria this prompt covers (the assert half of AC7 only; the fixture half belongs to prompt 2).
- `prompts/2-spec-039-mode-frontend.md` — the preceding prompt in this spec's sequence. It owns the fixture extension, `CARD_CONFIG`, the mappers and the pill; this prompt owns none of those.

Conventions: `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` for the plain-`testing` table style this file uses (it is `testing.T`, not Ginkgo — match the file).

The pre-change enabled slot sets, read from the current `CARD_CONFIG` and the fixture, are the data this prompt hardcodes:

- task: `activityAge`, `assignee`, `blocked`, `flag`, `hold`, `jira`, `menu`, `noteLink`, `priority`, `session`
- goal: the same ten names
- topic: `activityAge`, `assignee`, `blocked`, `detail`, `flag`, `hold`, `jira`, `live`, `noteLink`, `priority`

These are the sets **before** the mode slot lands. `live` and `detail` are disabled on task and goal, and `session` and `menu` are disabled on topic — those four names are deliberately absent from the corresponding lists above.

</context>

<requirements>

1. Add `TestCardSlotsRetainPreChangeAffordances` to `pkg/board/jsasset/card_baseline_test.go`. The name must contain `Card` so `go test ./pkg/board/jsasset/ -run Card` selects it.

   The test carries the three pre-change lists as **hardcoded string literals** inside the test body — the ten task names, the ten goal names and the ten topic names from `<context>`, alphabetically ordered as written there. Do not derive any of these lists from the fixture, from `CARD_CONFIG`, or from any other parsed source; a comment on the literal block must say why: the same change extends the fixture, so an expectation read back from it makes the assertion `fixture ⊆ fixture` and the drop-detection vacuous.

   Do not add `mode` to any of the three lists. The assertion is a subset check, so an added slot is allowed by construction; the lists record only what existed before.

2. Assert per kind, against a different target for the two shapes:
   - `task` and `goal` — every hardcoded name must appear in the fixture entry's `enabled_slots` (`loadBaseline(t)[kind].EnabledSlots`).
   - `topic` — the fixture has no topic entry (deliberate, see the exclusion comment already in the file), so every hardcoded name must appear in the live topic slot map, `cardSlots(t, source, "topic")`, as an entry whose value is exactly `true`.

   Structure it as a table over the three kinds with the target resolved per kind, or as three explicit blocks — either is fine, but each failure must name the kind and every missing slot, sorted. Extract a small helper rather than repeating the divergence report three times:

```go
// assertSlotSubset fails when any pre-change slot is absent from the enabled
// set, naming the kind and each dropped slot. Extra slots are allowed: the
// change that extends the fixture adds one by design.
func assertSlotSubset(t *testing.T, kind string, preChange []string, enabled map[string]bool)
```

   Build `enabled` from the fixture list (a name is enabled when present in `enabled_slots`) or from the parsed slot map (a name is enabled when its value is `true`), at the call site. `assertEnabledSlotsMatch` in the same file is the shape to imitate, but do not call it — it asserts equality in both directions and would fail on the newly added `mode` slot.

   A slot name mentioned in a comment cannot satisfy this: the parsed maps come from `ParseObjectLiteral`, which skips comments and string literals, so the check is structural by construction. Do not add a raw `strings.Contains` over the source.

3. Harden the both-kinds guard in the same file. Lift the inline table out of `TestTaskAndGoalCardDefinitionsMatchBaseline` into a package-level variable and keep that test's behaviour identical:

```go
// baselineKinds is the set of kinds the baseline guard covers. It is a package
// variable so TestCardBaselineGuardCoversBothKinds can prove the guard still
// names both kinds: a deleted row would otherwise silently drop a kind from the
// comparison while every remaining assertion still passes.
var baselineKinds = []struct {
	kind     string
	mapperFn string
}{
	{kind: "task", mapperFn: "mapTaskCard"},
	{kind: "goal", mapperFn: "mapGoalCard"},
}
```

   Keep one composite-literal row per kind, in the exact `{kind: "task", mapperFn: "mapTaskCard"}` form, so the spec's AC7 evidence holds: `grep -c '{kind: "task"' pkg/board/jsasset/card_baseline_test.go` = 1 and `grep -c '{kind: "goal"' pkg/board/jsasset/card_baseline_test.go` = 1. Do not convert the rows to a map or a loop-generated slice.

   `TestTaskAndGoalCardDefinitionsMatchBaseline` iterates `baselineKinds` instead of the inline slice, with no other change — same `t.Run`, same `loadBaseline`, same `assertEnabledSlotsMatch` and `assertMapperMatches` calls.

4. Add `TestCardBaselineGuardCoversBothKinds` (name contains `Card`) to `pkg/board/jsasset/card_baseline_test.go`, or to a new file in the same `jsasset_test` package. If it lives in `card_baseline_test.go`, reference the kinds as bare string literals (`"task"`, `"goal"`) — never as `{kind: "task"}` composite-literal rows, which would break AC7's `grep -c '{kind: "task"'` = 1 contract. Assert the guard covers exactly the two kinds the spec requires:
   - `len(baselineKinds) == 2`;
   - the set of `kind` values equals `{"task", "goal"}` (order-independent; assert each of the two is present and that no third kind is).
   - each row's `mapperFn` is non-empty.

   A failure must name the missing or unexpected kind. This is what makes deleting the `task` row or the `goal` row fail instead of pass: without it, the remaining row runs, the fixture comparison is green, and the deleted kind is silently unguarded.

5. Run `<verification>` and confirm both commands exit 0. Then confirm the two new assertions are load-bearing by a temporary edit, reverting it by hand afterwards — this repo's container has no usable `.git`, so never rely on `git checkout`:
   - delete one name from the hardcoded `task` pre-change list, re-run the test, confirm it fails naming `task` and that slot; restore the name.
   - delete the `{kind: "goal", mapperFn: "mapGoalCard"}` row, re-run the test, confirm `TestCardBaselineGuardCoversBothKinds` fails naming `goal`; restore the row.
   Re-run `make precommit` after restoring and confirm green.

6. Finish by walking AC7 (assert half) and DB6 against the change: name, for each, the test and the assertion that proves it, and confirm no production file was touched.

</requirements>

<constraints>

- Only test files under `pkg/board/jsasset/` may change — `card_baseline_test.go`, and no other file unless a new test must live in its own file in the same package.
- Do **not** modify `pkg/board/testdata/card-vectors-pre-change.json`. Prompt 2 owns the fixture extension; this prompt reads it only. The fixture is extended by hand, never regenerated.
- Do **not** modify `src/vault_ui/static/app.js` — no change to `CARD_CONFIG`, `mapTaskCard`, `mapGoalCard` or `mapTopicCard`. This prompt asserts only.
- The pre-change slot lists are hardcoded literals. Never read the expectation back from the fixture or from the parsed asset — that makes the check circular and drop-detection vacuous.
- The assertion is a subset check, not equality: `mode` is added by prompt 2 and must not fail this test. Do not add `mode` to any pre-change list.
- The topic set is hardcoded because the fixture has no topic entry. Do not add a topic entry to the fixture to "fix" that — the exclusion is deliberate and commented in the guard.
- Keep `TestTaskAndGoalCardDefinitionsMatchBaseline`'s existing assertions intact; the table moves, its behaviour does not.
- No new dependency; `sort` and the other imports the file needs are already present.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`. Run every command below with `export PATH=/usr/local/go/bin:$PATH` first. (`ROOTDIR` is not read by this repo's Makefile, so a `ROOTDIR=/workspace` prefix is inert.)

Run `make precommit` — must exit 0. It runs `go fmt ./...`, `go vet ./...` and `go test -race ./...`, which covers the whole `jsasset` package.

Run `go test ./pkg/board/jsasset/ -run Card -v` — must exit 0 and list both new tests among the run.

The AC7 evidence depends on exactly one composite-literal row per kind in the baseline test file. Both must print `1`:

```
test "$(grep -c '{kind: "task"' pkg/board/jsasset/card_baseline_test.go)" = 1
test "$(grep -c '{kind: "goal"' pkg/board/jsasset/card_baseline_test.go)" = 1
```

No `git` command is available or needed: this repo sets `hideGit: true`.

</verification>
