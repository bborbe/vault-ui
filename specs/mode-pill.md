---
status: draft
created: "2026-11-12"
---

## Summary

- A card never shows whether opening that row starts a headless worker or an interactive session
- `mode:` is task frontmatter written at creation and never read back — the board parser ignores it and the API carries no such key
- A `mode` slot joins the shared card config for all three kinds, rendering a pill only when the page declares a mode
- The pill reads `⚙ headless` / `🖥 interactive` with a `title` naming the consequence
- The task, goal and topic boards are otherwise unchanged

## Problem

The operator makes a per-row choice — open a headless worker or an interactive session — and the board shows nothing that distinguishes the two. The decision is already recorded on disk at creation from the role × mode matrix (`claude-supervisor/docs/role-mode-matrix.md`); it is simply invisible where the operator makes the choice.

Measured 2026-11-12 in `private-personal`:

| kind | pages declaring `mode:` | total pages |
|---|---|---|
| task (`25 Tasks`) | 3252 | 11384 |
| goal (`24 Goals`) | 0 | 396 |
| topic (`23 Topics`) | 0 | 22 |

The values are free-form, not a two-value enum: `headless` 2375, `interactive` 870, `non_interactive` 7, one empty. Empty must read as absent, and an unrecognised value must still render rather than being silently dropped.

The invisibility is end to end, not a rendering gap:

- The running board's API carries no `mode` key on any kind — `/api/tasks` 32 keys, `/api/goals` 21 keys, `/api/topics` 14 keys, none of them `mode`.
- `grep -rn '"mode"\|"role"' pkg/` returns no hits: the Go board parser does not read the field at all.

The three boards already share one renderer, so this is one config entry plus one mapper key — not a third renderer. `grep -cE 'function create[A-Za-z]*Card\('` on `src/vault_ui/static/app.js` is 1, and `createCard`'s body contains 0 `kind` branches; per-kind differences live only in `CARD_CONFIG` (defined at `app.js:1612`) and the mapper.

## Goal

Every card whose page declares `mode:` shows a small pill naming that mode, on all three boards, and every card whose page declares none is unchanged.

## Desired Behavior

1. A task card whose page declares `mode:` shows a pill in `card-footer-left`; a task card whose page declares none shows nothing there. **DB1**
2. The same holds on the goal board and the topic board — one slot, one renderer, three kinds. **DB2**
3. The pill's text is the page's own value. `headless` and `interactive` carry a `title` naming the consequence; any other non-empty value renders verbatim with a generic `title`. **DB3**
4. A page that declares `mode:` with an empty value shows nothing — empty reads as absent, never as a blank pill. **DB4**
5. The task payload carries `mode` (`null` when the page declares none); the goal and topic payloads carry the key too, serving `null` on every current page. **DB5**
6. Every other card affordance, every other payload key, and the baseline guard's coverage of both `task` and `goal` are unchanged. **DB6**
7. The pill escapes its value the way every other badge does, so a `mode:` containing HTML renders as text. **DB7**

## Constraints

- `Mode` is `*string` with `json:"mode"` and **no** `omitempty` on `api.TaskResponse`, `api.GoalResponse` and `api.TopicResponse` — the key is always emitted, `null` when the page declares none. A plain `string` with a default would print a value on every row and break AC1.
- The other 31 task keys and 13 topic keys are unchanged (see Non-goals); the goal payload gains exactly one key.
- The pill is a new slot. Every existing slot in `CARD_CONFIG.task`, `.goal` and `.topic` keeps its current value — AC4's pre-change set is the lock.
- The mode value is untrusted file content rendered into element text **and** a `title` attribute, so both go through `escapeHtml`, as every comparable badge does (`app.js:1525`, `1593`).
- The repo has no JS runtime: frontend evidence is over the embedded source, never execution.
- The container does not mount the operator's vault, so the knowledge-base documentation step is host-side after merge.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| A `app.js` syntax error ships — a broken asset blanks the board | every board renders empty; no Go test catches it | operator runs `make build` + reloads; the browser console shows the parse error and its line; revert and re-release |
| A card's `mode:` holds an unrecognised value | the pill renders the raw value with the generic `title`; the card is otherwise normal | no action — free-form by design |
| `mode:` is an empty string | no pill; the card is unchanged | no action |
| A payload field lands as `""` instead of `null` | AC1 fails; the frontend still gates correctly, so no user-visible break | prompt 1 fixes the struct to `*string` |
| The baseline fixture is edited by deleting keys rather than adding `mode` | AC7 fails on the pre-change subset assertion | restore the deleted keys and add only `mode` |
| A future slot change re-captures the fixture wholesale | AC7's subset assertion still passes, but AC4's explicit pre-change set is what catches a dropped affordance | no action — AC4 is the lock, AC7 the fixture guard |

## Assumptions

- **`mode:` is a free-form string, not an enum.** Disk carries `headless`, `interactive`, `non_interactive` and one empty value. The mapper renders whatever the page carries; empty reads as absent. A two-case switch that drops the third value is a defect, not a simplification.
- **The three boards already share one card template and one config map.** Verified on master: one `createCard`, no `kind` branches in its body, three one-line board entry points. This spec is one `CARD_CONFIG` entry plus one mapper key per kind.
- **The card baseline guard covers `task` and `goal` only and deliberately excludes `topic`** (`pkg/board/jsasset/card_baseline_test.go`; the exclusion is commented in the file). Enabling the slot breaks both covered entries, so the fixture moves in the same change and the guard must keep covering both.
- **Two pinned payload field lists exist and break when `Mode` lands on the API structs** — `pkg/board/parity_test.go:42` (task, 32 keys) and `pkg/board/board_test.go:1278` (topic, 14 keys). No equivalent list exists for goals; AC2's new test is the goal lock.
- **`pkg/board/testdata/card-vectors-pre-change.json` is a living fixture, not frozen evidence.** It was introduced by spec 038, which is still in `specs/in-progress/`; it pins the current card definitions and is re-captured by any slot change. This spec extends it in the same commit and adds a subset assertion so an extension cannot silently drop an existing slot.
- **The container does not mount the operator's vault**, so the knowledge-base documentation step is host-side after merge, not a prompt.

## Acceptance Criteria

- [ ] **AC1 — the task payload carries `mode`, and a page without it serves null.** Evidence: a Go test builds a task page carrying `mode: headless` and one carrying none, and asserts the served rows carry `"mode": "headless"` and `"mode": null` — the key always present, the value null when undeclared. A field populated from a default would print a value for every row and fail the null half. `make test` exits 0.
- [ ] **AC2 — the goal and topic payloads carry `mode` too.** Evidence: a Go test decodes a goal list response and a topic list response and asserts both carry a `mode` key serving null on the current pages. The criterion is the key's presence on all three kinds, because the slot is on for all three and a missing key is a different shape from a null one — the shared badge builder distinguishes them. `make test` exits 0.
- [ ] **AC3 — the two pinned payload field lists move with the structs.** Evidence: `pkg/board/parity_test.go` asserts the task key set including `mode`, and `pkg/board/board_test.go` asserts the topic key set including `mode`; `go test ./pkg/board/` exits 0. A field added to `api.TaskResponse` without its list updated fails here rather than shipping silently, and a list edited by deleting a key rather than adding one also fails, because each list is asserted exactly.
- [ ] **AC4 — the slot map enables `mode` for all three kinds and keeps every existing slot.** Evidence: a Go test walks `CARD_CONFIG` → kind entry → its `slots` map, reusing the `cardEntry` / `ParseObjectLiteral` walk in `card_baseline_test.go` — `jsasset.ObjectLiteral(source, "CARD_CONFIG")` alone returns only the top-level `{task, goal, topic}` map and would assert nothing. It asserts each kind's enabled-key set is a superset of its pre-change set **plus** `mode`, and the pre-change set is asserted explicitly, because a superset test alone would permit silently dropping an existing affordance. A slot name inside a comment cannot satisfy it. `make test` exits 0.
- [ ] **AC5 — the mappers read the page's own value, and the badge is value-gated.** Evidence: a Go test parses each kind's mapper object literal and asserts the `mode` entry references the payload field rather than a constant, and asserts the badge builder gates on the value (`config.slots.mode && view.mode`). A mapper returning a constant, or a builder emitting the pill unconditionally, fails — an always-on badge proves nothing about the field.
- [ ] **AC6 — the pill names the mode, survives an unknown value, and escapes.** Evidence: a Go test asserts the pill's markup carries the raw value as escaped element text, carries a `title` attribute naming the consequence for `headless` (`opens a headless worker`) and for `interactive` (`opens an interactive session`), and that `non_interactive` renders its raw value with the generic title rather than being dropped or mislabelled. A page whose `mode:` is `<img src=x onerror=alert(1)>` must render that as escaped text in both the element and the `title`, not as an element — `mode:` is free-form file content and every comparable badge escapes (`app.js:1525`, `1593`).
- [ ] **AC7 — the baseline fixture extends by exactly `mode`, and the guard still covers both kinds.** Evidence: `pkg/board/testdata/card-vectors-pre-change.json` gains the `mode` slot and the `mode` mapper entry for `task` and `goal` and nothing else; `pkg/board/jsasset/card_baseline_test.go` still names both kinds (`grep -c '{kind: "task"'` = 1 **and** `grep -c '{kind: "goal"'` = 1, so deleting a kind from the guard fails rather than passes), and additionally asserts each kind's **pre-change** slot set is a subset of the fixture's — so extending the fixture cannot silently drop an existing affordance. `go test ./pkg/board/jsasset/ -run Card` exits 0.
- [ ] **AC8 — the docs name the pill.** Evidence: `grep -c 'mode badge' "50 Knowledge Base/vault-ui.md"` returns ≥ 1, in a subsection under `### Kanban Board` shaped like *Activity-age badge* and *Blocked-by badge*. Operator-host step after merge; the container does not mount the vault.

## Verification

### Container-executable

- `make precommit` — format, vet and `go test -race ./...` all exit 0
- `go test ./pkg/board/jsasset/ -run Card -v` — the baseline guard and the pre-change subset assertion are green
- `go test ./pkg/board/ -run 'Parity|Topic|Goal'` — both pinned key lists and the new payload tests are green

### Operator-executable

- `make build` from the production clone, then `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui` in a quiet window (the restart kills every live session the board tracks)
- Load `http://127.0.0.1:8000/` and confirm the browser console is clean, then confirm the pill on a card declaring `mode:` and its absence on one declaring none — see Post-Deploy Verification

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Payload: parse `mode` in `pkg/board/`, add `Mode *string` to the three API structs, move the two pinned field lists | DB5, DB6 | AC1, AC2, AC3 | — |
| 2 | Frontend: `CARD_CONFIG` slot for all three kinds, mapper keys, escaped pill + `title` + CSS, cache-buster bump, `docs/frontend-card-render.md`, README, CHANGELOG entry | DB1, DB2, DB3, DB4, DB7 | AC4, AC5, AC6 | prompt 1 (needs the payload field) |
| 3 | Baseline fixture extension plus the pre-change subset assertion | DB6 | AC7 | prompts 1, 2 |

Rationale: prompt 1 establishes the payload the frontend reads; prompt 2 is the only layer that changes what a user sees, and depends on it; prompt 3 extends the fixture and asserts no existing slot was dropped. Each prompt owns the tests for its own layer. The vault documentation step (AC8) is host-side after merge, not a prompt.

Execution route: `bborbe/vault-ui` is a governed repo and this spec carries a business-level why, so it goes **spec → prompts → cluster agent**. After `dark-factory spec approve`, open the draft PR and let the cluster agent pick it up — start no local daemon. The frontend half cannot be verified in the container (no JS runtime, no browser), so AC4–AC6 assert the embedded source expressions and the rendering is proven against the running board after deploy.

## Post-Deploy Verification

Not an AC — a rung, because it cannot run in the container or in CI. After merge and release, the operator deploys per the `vault-ui` KB page in a quiet window, then records evidence that is observable rather than narrated:

- the board at `http://127.0.0.1:8000/` loads with **zero** browser-console errors;
- the card for a task whose page declares `mode: headless` shows a pill reading `headless`, and the card for a task whose page declares none shows no pill;
- both readings captured as a screenshot on the task page.

The served asset is itself a runtime: a broken `app.js` blanks the board rather than failing a test, so the console-clean page load is the pre-deploy check, not a green suite.

## Do-Nothing Option

The board keeps showing every row identically, and the operator keeps opening a card to find out what it will do — or opens the wrong kind. The decision is already on disk and already correct; the cost of doing nothing is that it stays unreadable at the exact moment it matters.

The rejected alternative was to show `role:` instead, on the theory that it implies the mode. It was rejected because the role × mode matrix does not make role a proxy for mode (the two are chosen separately), and a card that shows role while the operator asked for mode would answer a neighbouring question.

The second rejected alternative was to add `mode:` to goal and topic pages so all three kinds could show a pill immediately. It was rejected as scope: whether goals and topics should carry a mode is a separate decision about what those kinds mean, and this spec must not pre-empt it by backfilling 418 pages.
