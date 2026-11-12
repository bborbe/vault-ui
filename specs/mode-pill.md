---
status: draft
created: "2026-11-12"
---

## Summary

- A card never shows whether opening that row starts a headless worker or an interactive session
- `mode:` is task frontmatter written at creation and never read back — the board parser ignores it and the API carries no such key
- A `mode` slot joins the shared card config for all three kinds, rendering a pill only when the page declares a mode
- The pill reads `⚙ headless` / `🖥 interactive`, and any other declared value renders verbatim with the title `Execution mode`
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

## Non-goals

- Adding `mode:` to goal or topic pages, or backfilling it across any folder — they carry none today (0 of 396 goals, 0 of 22 topics) and whether they should is a separate decision.
- Showing `role:` alongside `mode` on the card — a second field with its own question about what it means to a reader.
- Changing which mode a row opens with, or the write path — `mode:` stays creation-time and read-only here.
- Validating, normalising or enumerating the mode values. The field is free-form on disk and this spec renders what it finds.
- Filtering or sorting the board by mode.
- Defaulting the pill for the 8132 task pages that declare no `mode:` — the slot stays silent on them rather than showing a placeholder.
- Any new endpoint, CLI flag, or query parameter.
- Any change to the task, goal or topic payload keys other than `mode`.

**Recorded decision — the goal and topic slots are enabled structurally, and they render nothing today.** Only task pages carry a mode (0 of 396 goals, 0 of 22 topics), so enabling the slot on all three kinds is dead code until those kinds carry the field. It is kept deliberately: one slot and one renderer with no `kind` branch is cheaper than a task-only slot that forks the template, and the slot costs nothing because every badge is value-gated.

## Acceptance Criteria

- [ ] **AC1 — the task payload carries `mode`, and a page without it serves null.** Evidence: a Go test builds a task page carrying `mode: headless` and one carrying none, and asserts the served rows carry `"mode": "headless"` and `"mode": null` — the key always present, the value null when undeclared. A field populated from a default would print a value for every row and fail the null half. `make test` exits 0.
- [ ] **AC2 — the goal and topic payloads carry the page's own mode.** Evidence: a Go test builds a goal page and a topic page each carrying `mode: headless` and asserts both served responses carry `"mode": "headless"`, plus a page of each kind carrying none serving `null`. The criterion is the **value**, not merely the key's presence: goals have no page-field read today, so a key that is always `null` would satisfy a presence check while the goal half of the Goal is never delivered. A missing key is a different shape from a null one — the shared badge builder distinguishes them.
- [ ] **AC3 — the two pinned payload field lists move with the structs.** Evidence: `pkg/board/parity_test.go` asserts the task key set including `mode`, and `pkg/board/board_test.go` asserts the topic key set including `mode`; `go test ./pkg/board/` exits 0. A field added to `api.TaskResponse` without its list updated fails here rather than shipping silently, and a list edited by deleting a key rather than adding one also fails, because each list is asserted exactly.
- [ ] **AC4 — the slot map enables `mode` for all three kinds and keeps every existing slot.** Evidence: a Go test walks `CARD_CONFIG` → kind entry → its `slots` map, reusing the `cardEntry` / `ParseObjectLiteral` walk in `card_render_test.go:15` and `jsasset.go:61` — `jsasset.ObjectLiteral(source, "CARD_CONFIG")` alone returns only the top-level `{task, goal, topic}` map and would assert nothing. It asserts each kind's enabled-key set is a superset of its pre-change set **plus** `mode`, and the pre-change set is asserted explicitly, because a superset test alone would permit silently dropping an existing affordance. Task and goal pre-change sets come from the fixture; the topic set has no fixture entry (deliberate, see Assumptions) and is hardcoded in the test. A slot name inside a comment cannot satisfy it. `make test` exits 0.
- [ ] **AC5 — the mappers read the page's own value, and the badge is value-gated.** Evidence: a Go test parses each kind's mapper object literal and asserts the `mode` entry references the payload field rather than a constant, and asserts the badge builder gates on the value (`config.slots.mode && view.mode`). A mapper returning a constant, or a builder emitting the pill unconditionally, fails — an always-on badge proves nothing about the field.
- [ ] **AC6 — the pill names the mode, survives an unknown value, and escapes for text and attribute.** Evidence: a Go test asserts the pill's markup for `headless` carries `⚙ headless` and a `title` naming the headless consequence, for `interactive` carries `🖥 interactive` and its consequence title, and that `non_interactive` renders its raw value with the title `Execution mode` rather than being dropped or mislabelled. Two adversarial payloads: `<img src=x onerror=alert(1)>` must render as escaped text and not as an element, and `x" onmouseover="alert(1)` must not split the `title` attribute — the rendered attribute carries `&quot;` or the title is assigned through the DOM, never raw `escapeHtml` output. `escapeHtml` does not escape `"`, so the second payload is the one that catches an attribute built by string interpolation.
- [ ] **AC7 — the baseline fixture extends by exactly `mode`, and the guard still covers both kinds.** Evidence: `pkg/board/testdata/card-vectors-pre-change.json` gains the `mode` slot and the `mode` mapper entry for `task` and `goal` and nothing else; `pkg/board/jsasset/card_baseline_test.go` still names both kinds (`grep -c '{kind: "task"'` = 1 **and** `grep -c '{kind: "goal"'` = 1, so deleting a kind from the guard fails rather than passes), and additionally asserts each kind's **pre-change** slot set is a subset of the fixture's — so extending the fixture cannot silently drop an existing affordance. `go test ./pkg/board/jsasset/ -run Card` exits 0.
- [ ] **AC8 — the docs name the pill.** Evidence: `grep -c 'mode badge' "50 Knowledge Base/vault-ui.md"` returns ≥ 1 **and** `grep -A 12 'Activity-age badge' "50 Knowledge Base/vault-ui.md"` shows the new subsection under the same `### Kanban Board` list, shaped like *Activity-age badge* and *Blocked-by badge*. Operator-host step after merge; the container does not mount the vault, so the verifier skips this AC rather than failing it.

## Verification

### Container-executable

- `make precommit` — format, vet and `go test -race ./...` all exit 0
- `go test ./pkg/board/` — the whole board suite: the Ginkgo `TestBoard` entrypoint carries the parity, topic and goal specs, so `-run 'Parity|Topic|Goal'` selects nothing and exits 0 vacuously
- `go test ./pkg/board/jsasset/ -run Card -v` — the baseline guard and the pre-change subset assertion

### Operator-executable

- `make build` from the production clone, then `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui` in a quiet window (the restart kills every live session the board tracks)
- Load `http://127.0.0.1:8000/` and confirm the browser console is clean, then confirm the pill on a card declaring `mode:` and its absence on one declaring none — see Post-Deploy Verification

## Desired Behavior

1. A task card whose page declares `mode:` shows a pill in `card-footer-left`; a task card whose page declares none shows nothing there. **DB1**
2. The same holds on the goal board and the topic board — one slot, one renderer, three kinds. **DB2**
3. The pill shows `⚙ headless` for `headless` and `🖥 interactive` for `interactive`, each with a `title` naming the consequence. Any other non-empty value renders verbatim with the title `Execution mode`. **DB3**
4. A page that declares `mode:` with an empty value shows nothing — empty reads as absent, never as a blank pill. **DB4**
5. The task payload carries `mode` (`null` when the page declares none); the goal and topic payloads carry the key too, and serve the page's value when one declares it. **DB5**
6. Every other card affordance, every other payload key, and the baseline guard's coverage of both `task` and `goal` are unchanged. **DB6**
7. The pill is escaped for both element text and its `title` attribute, so a `mode:` containing HTML or a double quote renders as text and cannot break out of the attribute. **DB7**

## Constraints

- `Mode` is `*string` with `json:"mode"` and **no** `omitempty` on `api.TaskResponse`, `api.GoalResponse` and `api.TopicResponse` — the key is always emitted, `null` when the page declares none. A plain `string` with a default would print a value on every row and break AC1.
- The other 32 task keys and 14 topic keys are unchanged (see Non-goals); the goal payload gains exactly one key.
- The pill is a new slot. Every existing slot in `CARD_CONFIG.task`, `.goal` and `.topic` keeps its current value — AC4's pre-change set is the lock.
- Page-derived fields come from **one `pageIndex.ListPages` per vault**, never one per row. Extend the existing task page-fields struct rather than adding a second walk; topics already have `topicPageFieldsByTopic` (`pkg/board/topics.go:122`) and goals have no page read at all today, so the goal path is new plumbing mirroring the topic one.
- The pill must not change `card-footer-left`'s layout contract: a long or unrecognised value ellipsises within a `max-width` and never pushes the action button off the card. `app.js:1832–1842` records the 2026-09-11 incident where exactly that happened.
- `docs/frontend-card-render.md` is the written contract for `CARD_CONFIG` and the mappers (its § `CARD_CONFIG` fields and § `slots.*`, plus the add-a-slot procedure); prompt 2 updates it alongside the code.
- The repo has no JS runtime: frontend evidence is over the embedded source, never execution.
- The container does not mount the operator's vault, so the knowledge-base documentation step is host-side after merge.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| An `app.js` syntax error ships — a broken asset blanks the board | every board renders empty; no Go test catches it | operator runs `make build` + reloads; the browser console shows the parse error and its line; revert and re-release |
| A card's `mode:` holds an unrecognised value | the pill renders the raw value with the title `Execution mode`; the card is otherwise normal | no action — free-form by design |
| `mode:` is an empty string | no pill; the card is unchanged | no action |
| A long `mode:` value joins `card-footer-left` | the pill ellipsises and the action button stays on-card | no action — CSS backstop, asserted via the pill's class |
| A payload field lands as `""` instead of `null` | AC1 fails; the frontend still gates correctly, so no user-visible break | prompt 1 fixes the struct to `*string` |
| The baseline fixture is regenerated from the changed `CARD_CONFIG` | AC7's subset assertion still passes, but a dropped slot goes unnoticed | re-extend the fixture by hand; the file is never regenerated |
| A `mode:` value contains `"` or HTML | rendered as escaped text; the `title` attribute is not split | none — AC6's adversarial payload |

## Security / Abuse

`mode:` is untrusted file content — anyone or anything that writes a task page controls it — and it is rendered into the board's DOM on every page load.

- **Element text.** Escaped with `escapeHtml` (`app.js:2486`), the codebase's standard treatment for a page-derived string in element position.
- **Attribute position is the gap.** `escapeHtml` is `div.textContent` → `div.innerHTML`, which escapes `&`, `<` and `>` but **not** `"`. A value of `x" onmouseover="alert(1)` interpolated into `title="${escapeHtml(value)}"` breaks out of the attribute and installs an event handler. The `title` must therefore be assigned through the DOM (`el.title = value`, which the browser escapes on serialisation) or escaped with the `&quot;`-emitting half of `escapeJsAttr` (`app.js:2500`).
- **Precedent, and why it does not license the defect.** `app.js:1525` and `:1593` build `title` attributes with `escapeHtml` and carry the same latent hole. They are out of scope here — this spec must not extend it, and AC6's quote payload is the check that it did not.
- **No new surface.** No endpoint, no query parameter, no write path, and no value is ever used as a selector, URL or shell argument.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Payload: parse `mode` in `pkg/board/` (tasks, plus the new goal and topic page-field reads), add `Mode *string` to the three API structs, move the two pinned field lists | DB5, DB6 | AC1, AC2, AC3 | — |
| 2 | Frontend: `CARD_CONFIG` slot for all three kinds, mapper keys, escaped pill + `title` + ellipsis CSS, cache-buster bump, **the baseline fixture extension**, `docs/frontend-card-render.md`, README, CHANGELOG entry | DB1, DB2, DB3, DB4, DB7 | AC4, AC5, AC6, AC7 (fixture) | prompt 1 |
| 3 | Non-regression assertion: the pre-change subset check plus the both-kinds guard hardening | DB6 | AC7 (assert) | prompt 2 |

Rationale: prompt 1 establishes the payload the frontend reads; prompt 2 is the only layer that changes what a user sees, and it **must** carry the fixture extension in the same prompt — `card_baseline_test.go` asserts exact slot and mapper sets, so a prompt 2 that adds the slot without the fixture leaves `make precommit` red between prompts. Prompt 3 adds only assertions, so it is green on its own. Each prompt owns the tests for its own layer. The vault documentation step (AC8) is host-side after merge, not a prompt.

The spec sits at the upper end of the size budget (7 behaviors × 8 ACs across four layers). If generation research runs long, prompt 2 can split into config/mapper versus pill-markup/CSS — but the fixture extension must stay with whichever prompt first changes `CARD_CONFIG`.

Execution route: `bborbe/vault-ui` is a governed repo and this spec carries a business-level why, so it goes **spec → prompts → cluster agent**. After `dark-factory spec approve`, open the draft PR and let the cluster agent pick it up — start no local daemon. The frontend half cannot be verified in the container (no JS runtime, no browser), so AC4–AC6 assert the embedded source expressions and the rendering is proven against the running board after deploy.

## Post-Deploy Verification

Not an AC — a rung, because it cannot run in the container or in CI. After merge and release, the operator deploys per the `vault-ui` KB page in a quiet window, then records evidence that is observable rather than narrated:

- the board at `http://127.0.0.1:8000/` loads with **zero** browser-console errors;
- the card for a task whose page declares `mode: headless` shows a pill reading `⚙ headless`, and the card for a task whose page declares none shows no pill;
- both readings captured as a screenshot on the task page.

The served asset is itself a runtime: a broken `app.js` blanks the board rather than failing a test, so the console-clean page load is the pre-deploy check, not a green suite.

## Do-Nothing Option

The board keeps showing every row identically, and the operator keeps opening a card to find out what it will do — or opens the wrong kind. The decision is already on disk and already correct; the cost of doing nothing is that it stays unreadable at the exact moment it matters.

The rejected alternative was to show `role:` instead, on the theory that it implies the mode. It was rejected because the role × mode matrix does not make role a proxy for mode (the two are chosen separately), and a card that shows role while the operator asked for mode would answer a neighbouring question.

The second rejected alternative was to add `mode:` to goal and topic pages so all three kinds could show a pill immediately. It was rejected as scope: whether goals and topics should carry a mode is a separate decision about what those kinds mean, and this spec must not pre-empt it by backfilling 418 pages.
