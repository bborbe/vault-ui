---
status: approved
spec: [039-mode-pill]
created: "2026-11-12T12:35:00+01:00"
queued: "2026-11-12T11:30:28Z"
---

# Render the execution-mode pill on every card that declares a mode

<summary>

- A card now shows whether opening that row starts a headless worker or an interactive session
- The pill reads `⚙ headless` or `🖥 interactive`, with a tooltip explaining what that mode does
- Any other value a page declares renders verbatim under a generic "Execution mode" tooltip instead of being dropped
- A card whose page declares no mode, or an empty one, is unchanged — no placeholder, no blank pill
- All three boards show it from one shared template and one shared slot, not three renderers
- A long or unrecognised value truncates inside the card instead of pushing the action button off it
- The tooltip is set in a way a value containing a double quote cannot break out of, so a page's mode cannot inject markup
- The card regression baseline is extended by hand so the task and goal cards keep their full pinned contract
- The frontend render contract now documents the escaping rule and the fixture step it was missing
- Documentation and a changelog entry land with the code; nothing else the board serves changes

</summary>

<objective>

Make the execution mode the operator already chooses per row readable at the moment of the choice. The payloads already carry the page's own `mode` (previous prompt of this spec); this prompt puts it on the card — a small pill in the footer, present only when the page declares a mode, escaped so an untrusted value renders as text, and truncated so it cannot break the footer layout.

</objective>

<context>

Read these before writing code.

- `docs/frontend-card-render.md` — the written contract for `CARD_CONFIG`, the mappers, the view-model and the escaping rule. Its `## Adding an affordance` procedure is the checklist this change follows, and it currently has **no fixture step** — adding one is part of this prompt.
- `src/vault_ui/static/app.js` — the three extension points this change touches:
  - `const CARD_CONFIG` (`app.js:1612`) — one entry per kind (`task`, `goal`, `topic`), each with a `slots` object. Per-kind differences live here and nowhere else.
  - `mapTaskCard`, `mapGoalCard`, `mapTopicCard` — each returns the common view-model object. Data only: no `innerHTML`, no `<div`, no `class=`.
  - `createCard(kind, item)` — the one template. It reads `config.slots.<name>` and `view.<field>`, and never **branches** on the kind name (it does pass `kind` through to helpers like `showMenu` and `blockedBadgeHtml`). `card-footer-left` is assembled from `footerLeft`, and `card.innerHTML` is assigned once at the end.
- `src/vault_ui/static/app.js` — the exemplars to copy:
  - `priorityChip` and `liveIndicator` / `holdBadge` — value-gated footer spans built as one template literal.
  - `escapeHtml` — `div.textContent` → `div.innerHTML`; it escapes `&`, `<` and `>`, **not** `"`. Safe for element text, unsafe inside a double-quoted attribute.
  - `escapeJsAttr` — escapes for a JS string literal inside an HTML attribute and also rewrites `'` to `\'`. **Not** this change's tool.
  - `flagButtonHtml`, `blockedBadgeHtml`, `activityAgeHtml` — the house shape for a shared per-card helper: one small named function returning markup.
- `src/vault_ui/static/style.css` — `.priority-chip` (chip look), `.blocked-badge` (the ellipsis treatment for a free-form label; its own `max-width` is `100%`), and the comment above `.card-blocked` summarising the footer-clipping incident (the `2026-09-11` date lives in the `createCard` comment at `app.js:1836`).
- `pkg/board/jsasset/card_render_test.go` — the house style for a card assertion: `jsasset.FunctionBody` / `jsasset.ReturnedObject` / `jsasset.ObjectLiteral` plus exact `strings.Contains` checks. The structured parsers (`ObjectLiteral` / `ReturnedObject` / `Branch`) skip comments, so a name in a comment cannot satisfy a structural assertion; `FunctionBody` returns the raw body **including** comments, so its `strings.Contains` assertions hold only as long as the asserted strings do not also appear in a comment.
- `pkg/board/jsasset/card_baseline_test.go` and `pkg/board/testdata/card-vectors-pre-change.json` — the exact-set guard. `assertMapperMatches` fails on any mapper key absent from the fixture, so the fixture moves in this same change.
- `src/vault_ui/static/index.html` — the `?v=` cache-buster tokens (`style.css` and `app.js`).

Precondition: the payloads already carry `mode` — `grep -n 'json:"mode"' pkg/api/api.go` must find it on `TaskResponse`, `GoalResponse` and `TopicResponse`. If it does not, stop and report; do not re-implement the payload half in this prompt.

Conventions: `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` for the plain `testing` + `t.Fatalf` shape these tests use, and `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for the `## Unreleased` + conventional-prefix entry.

</context>

<requirements>

1. Add three small helpers to `src/vault_ui/static/app.js`, beside the other shared card helpers (`flagButtonHtml`, `activityAgeHtml`). They exist as separate functions because the escaping rule splits them: the label is element text and goes through `escapeHtml`, while the title must never be interpolated into markup at all.

```js
// The execution-mode pill's text for a page's `mode:` value. The field is
// free-form on disk, so the two known modes get a friendly label, any other
// non-empty value renders verbatim, and an empty value never reaches here —
// the caller gates on it.
function modeBadgeLabel(mode) {
    if (mode === 'headless') return '⚙ headless';
    if (mode === 'interactive') return '🖥 interactive';
    return mode;
}

// The pill's tooltip. Deliberately returns a constant for every case: the value
// never reaches a title attribute, so a `mode:` carrying a double quote cannot
// break out of one.
function modeBadgeTitle(mode) {
    if (mode === 'headless') return 'Starts a headless worker (no UI)';
    if (mode === 'interactive') return 'Opens an interactive session';
    return 'Execution mode';
}

// The pill's markup. It carries no `title` attribute on purpose: escapeHtml
// escapes &, < and > but not a double quote, so a value interpolated into
// title="..." could split the attribute and install an event handler. The
// title is assigned through the DOM after the card's innerHTML is set — see
// createCard.
function modeBadgeHtml(mode) {
    return `<span class="mode-badge">${escapeHtml(modeBadgeLabel(mode))}</span>`;
}
```

The three literal labels and three literal titles above are asserted verbatim by the tests in requirement 9 — do not reword, translate or re-case them.

2. Enable the slot on all three kinds. In each `CARD_CONFIG` entry's `slots` object (`task`, `goal`, `topic`), add `mode: true,` immediately after `priority: true,`. Every existing slot keeps its current value — `live: true` stays topic-only, `session`/`menu` stay off for topic, `detail` stays topic-only.

3. Add the mapper key to all three mappers. In the object literal each mapper returns, add one line immediately after its `priority` line:

```js
        mode: task.mode,
```

and the same shape for the other two — `mode: goal.mode,` in `mapGoalCard`, `mode: topic.mode,` in `mapTopicCard`. The expression must reference the kind's own payload field, verbatim, with no fallback (`|| ''`), no default and no constant — the tests and the fixture pin these three expressions exactly. A payload without the field yields `undefined`, which the gate in requirement 4 reads as absent.

4. Render the pill in `card-footer-left`, immediately after the priority chip, gated on the slot **and** the value. In `createCard`:

```js
    const modeChip = config.slots.mode && view.mode ? modeBadgeHtml(view.mode) : '';
```

and extend `footerLeft` so the pill follows `priorityChip` and precedes `detailButton`:

```js
    const footerLeft = `${liveIndicator}${flagButton}${holdBadge}${assigneeBlock}${priorityChip}${modeChip}${detailButton}`;
```

The `config.slots.mode && view.mode` conjunction is load-bearing in both directions: the slot alone would emit an empty pill on the 8132 task pages that declare no mode, and the value alone would bypass the slot contract. Empty string is falsy, so a page carrying `mode:` with no value renders nothing.

5. Assign the tooltip through the DOM, never through the markup. After `card.innerHTML = ...` in `createCard` and before the `if (config.slots.detail)` block, add:

```js
    // The pill's title is assigned through the DOM, never interpolated into the
    // markup: escapeHtml does not escape a double quote, so a `mode:` value
    // carrying `"` would break out of a title="..." attribute. The browser
    // escapes the value on serialisation.
    const modeBadge = card.querySelector('.mode-badge');
    if (modeBadge) modeBadge.title = modeBadgeTitle(view.mode);
```

Do not put `title="..."` inside `modeBadgeHtml`. Do not use `escapeJsAttr` for this — it also rewrites `'` to `\'`, which would print a literal backslash in the tooltip.

6. Add a `.mode-badge` rule to `src/vault_ui/static/style.css`, immediately after `.priority-chip`. Copy the chip look from `.priority-chip` (font-size `0.7rem`, `font-weight: 600`, `line-height: 1rem`, `padding: 1px 6px`, `border-radius: 999px`) and the truncation treatment `.blocked-badge` uses (`overflow: hidden`, `text-overflow: ellipsis`, `white-space: nowrap`), with `max-width: 50%` — deliberately narrower than `.blocked-badge`'s `max-width: 100%` because this pill shares `.card-footer-left` with the action button. Pick a background distinct from the priority chip's slate-700 so the two chips do not read as one. Carry a comment recording why the truncation is there, naming the 2026-09-11 clipping incident recorded in `createCard` (`app.js:1832–1838`) and summarised above `.card-blocked`: `mode:` is free-form, so an unrecognised value can be arbitrarily long, and without a `max-width` it pins `.card-footer-left` at its max-content width and pushes the action button past the card edge.

7. Bump both cache-buster tokens in `src/vault_ui/static/index.html` to the same new token `2026-11-12-mode-pill`: `style.css?v=2026-10-06-lazy-pane-jump` becomes `style.css?v=2026-11-12-mode-pill`, and `app.js?v=2026-11-09-topic-card-data-parity` becomes `app.js?v=2026-11-12-mode-pill`. Both assets change in this prompt, so both tokens move.

8. Extend `pkg/board/testdata/card-vectors-pre-change.json` by hand, for `task` and `goal` only. This is the same-change requirement that keeps `make precommit` green: `card_baseline_test.go` asserts the enabled slot set and the full mapper key set **exactly**, so the fixture must gain the slot in this same change.

   - In each of the `task` and `goal` `enabled_slots` arrays, insert `"mode"` between `"menu"` and `"noteLink"` (the arrays are sorted).
   - In each of the `task` and `goal` `mapper` objects, insert `"mode": "task.mode"` / `"mode": "goal.mode"` between `"live"` and `"obsidianUrl"` (the keys are sorted).
   - Add nothing else, and do **not** add a `topic` entry — the baseline guard deliberately excludes topic (`card_baseline_test.go` says so in its doc comment; do not "fix" that omission).
   - Do not regenerate the file from the changed source. Its contract is hand-maintained: a regenerated fixture would silently drop an existing affordance and make the next prompt's subset assertion vacuous.

9. Add the structural tests to `pkg/board/jsasset/card_render_test.go`, in the existing `jsasset_test` package and the existing style (`loadAsset`, `cardEntry`, `jsasset.ObjectLiteral` / `ReturnedObject` / `FunctionBody` / `CollapseWhitespace`, plain `t.Fatalf`). There is no JS runtime in this repo, so every assertion is over the embedded source expressions — never over a rendered string.

   a. `TestCardConfigEnablesModeSlot` (AC4) — for each of `task`, `goal`, `topic`: `cardSlots(t, source, kind)["mode"]` must be `"true"` (`cardSlots` is the existing helper in `card_baseline_test.go`; prompt 3 keeps it there)., and every name in that kind's **hardcoded pre-change enabled list** must still be `"true"`. Declare the three lists inside the test function, not read them from the fixture (reading them from the fixture would make the check circular, since the same change extends the fixture):

```go
	// preChangeEnabledSlots is the enabled-slot set each kind carried before the
	// mode slot. Hardcoded on purpose: the fixture is extended by this same
	// change, so reading it back would compare the change to itself.
	preChangeEnabledSlots := map[string][]string{
		"task":  {"activityAge", "assignee", "blocked", "flag", "hold", "jira", "menu", "noteLink", "priority", "session"},
		"goal":  {"activityAge", "assignee", "blocked", "flag", "hold", "jira", "menu", "noteLink", "priority", "session"},
		"topic": {"activityAge", "assignee", "blocked", "detail", "flag", "hold", "jira", "live", "noteLink", "priority"},
	}
```

   The failure message must name the kind and the missing slot.

   b. `TestCardMappersReadModeFromPayload` (AC5) — table over `{kind, mapperFn, field}`: `task`/`mapTaskCard`/`task.mode`, `goal`/`mapGoalCard`/`goal.mode`, `topic`/`mapTopicCard`/`topic.mode`. Assert `jsasset.CollapseWhitespace(view["mode"])` equals the field exactly, so a constant (`'headless'`), a fallback (`task.mode || ''`) or a coerced boolean all fail. Then assert `jsasset.FunctionBody(source, "createCard")` contains `config.slots.mode && view.mode`.

   c. `TestModeBadgeNamesModeAndEscapes` (AC6) — four assertions, each over one named helper body:
      - `FunctionBody(source, "modeBadgeLabel")` contains `'⚙ headless'` and `'🖥 interactive'`, and its verbatim fallback `return mode;` — so a two-case switch that drops `non_interactive` fails here rather than shipping.
      - `FunctionBody(source, "modeBadgeTitle")` contains `'Starts a headless worker (no UI)'`, `'Opens an interactive session'` and `'Execution mode'`, and does **not** contain `` `${ `` — the title is never built from the value.
      - `FunctionBody(source, "modeBadgeHtml")` contains `class="mode-badge"`, `escapeHtml(` and `modeBadgeLabel(mode)`, and does **not** contain `title=` — the pill's markup carries no attribute for a value to escape from, and its class is the hook the truncation CSS targets, so renaming it would silently drop the width backstop.
      - `FunctionBody(source, "createCard")` contains `querySelector('.mode-badge')` and `.title = modeBadgeTitle(view.mode)`.

   Add a comment on `TestModeBadgeNamesModeAndEscapes` recording how AC6's two adversarial payloads are covered: `<img src=x onerror=alert(1)>` cannot become an element because the label path goes through `escapeHtml`, and `x" onmouseover="alert(1)` cannot split an attribute because the markup carries no attribute and the title path interpolates nothing. Say plainly that the rendered proof is the post-deploy browser check, not this test.

10. Update `docs/frontend-card-render.md`:
    - `## slots.*` table — add a `mode` row: gates the execution-mode pill (`⚙ headless` / `🖥 interactive` / the raw value), enabled for `task`, `goal` and `topic`.
    - `## View-model` code block — add `mode,                // string | null` after `priority`.
    - The `mapTopicCard` paragraph — it currently says the mapper reads "the six data keys": add `mode` to that list and make it seven. Update the matching comment above `mapTopicCard` in `app.js` ("the six data fields …") to seven for the same reason.
    - `## Escaping` — record the rule this change exists to obey: `escapeHtml` is safe for **element text** and unsafe inside a **double-quoted attribute** (it does not escape `"`), so a page-derived value used as a `title` is assigned through the DOM (`el.title = value`) and never interpolated into markup; `escapeJsAttr` is for a JS string literal inside an attribute and is not a drop-in for a plain attribute value.
    - `## Adding an affordance` — add the missing fixture step: after adding the slot to `CARD_CONFIG` and the mapper key, extend `pkg/board/testdata/card-vectors-pre-change.json` **by hand** for every kind the baseline guard covers (`task` and `goal`; never `topic`), adding the slot to `enabled_slots` and the mapper key to `mapper`, and never regenerating the file. Renumber the following steps so the list stays ordered.

11. Update `README.md`:
    - `## Features` — one bullet: the card shows the page's execution mode as a `⚙ headless` / `🖥 interactive` pill, any other declared value verbatim, and no pill when the page declares none.
    - One sentence in each of the three API sections documenting the payload key the card reads: under `### GET /api/tasks` after the `jira` paragraph, under `### GET /api/goals` after the `session_state` paragraph, under `### GET /api/topics` after its `session_state` paragraph. Each says the row carries `mode` — the page's own `mode:` frontmatter value, verbatim and unparsed, or `null` when the page declares none — and that the key is always present.

12. Add a `CHANGELOG.md` entry. There is no `## Unreleased` section today: insert one directly above `## v0.106.1` (below the existing preamble, which stays untouched) with a single bullet beginning `- feat: `, describing the pill, the `mode` payload key it reads, the escaping shape (title via the DOM, text via `escapeHtml`) and the truncation backstop. Follow `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for the prefix.

13. Before finishing, re-run `<verification>` and confirm it exits 0; then walk each of AC4, AC5, AC6 and AC7's fixture half against the change, naming the test or file that proves each one.

</requirements>

<constraints>

- The pill renders only when the page declares a non-empty mode. No placeholder, no default, no "unknown" pill for the 8132 task pages that declare none, and an empty `mode:` renders nothing.
- `mode:` is free-form and untrusted: do not validate it, normalise it, lowercase it, enumerate it, or map an unrecognised value to a default. A two-case switch that drops `non_interactive` is a defect, not a simplification — the third value renders verbatim with the title `Execution mode`.
- Escaping: element text through `escapeHtml`; the `title` assigned through the DOM. Never `title="${escapeHtml(...)}"`, never `escapeJsAttr` for the title, and never the raw value in an attribute.
- The pill must not change `card-footer-left`'s layout contract: it ellipsises within a `max-width` and never pushes the action button off the card (the 2026-09-11 incident at `app.js:1832–1838`).
- Every existing `CARD_CONFIG` slot keeps its current value in all three entries; every existing mapper key keeps its exact expression. `pkg/board/testdata/card-vectors-pre-change.json` gains exactly one slot and one mapper key per covered kind, by hand, and never a `topic` entry.
- No new `CARD_CONFIG` field, no branch on the kind name in `createCard`, and no per-kind markup — one slot, one renderer, three kinds.
- Do not modify the Go payload code (`pkg/board/tasks.go`, `goals.go`, `topics.go`, `snapshot.go`, `pkg/api/api.go`) or the payload tests; that half is the previous prompt of this spec.
- Do not modify anything under `.github/workflows/`.
- Do not add a `mode:` to any goal or topic page and write no vault content.
- No new endpoint, query parameter, CLI flag, dependency, or JS library. No new Prometheus metric.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.

</constraints>

<verification>

`go` is not always on `PATH` in this container. `ROOTDIR` is **not** read by this repo's Makefile (no `Makefile.variables` / `default.env`), so a `ROOTDIR=/workspace` prefix is inert — the bare target is correct here.

```
export PATH=/usr/local/go/bin:$PATH
make precommit
```

Must exit 0 — it runs `go fmt ./...`, `go vet ./...` and `go test -race ./...`, which covers the three new card assertions, the extended baseline fixture, and every existing suite.

Targeted check of the card lane:

```
export PATH=/usr/local/go/bin:$PATH
go test ./pkg/board/jsasset/ -v
```

The fixture must have gained exactly four `"mode"` occurrences — one slot entry and one mapper entry for each of the two covered kinds — and no `topic` entry:

```
grep -c '"mode"' pkg/board/testdata/card-vectors-pre-change.json
```

must print `4`.

A syntax error in `app.js` blanks the whole board and no Go test can see it, so parse it:

```
node --check src/vault_ui/static/app.js
```

must exit 0. (`app.js` is a classic script with no `import`/`export`, so `--check` is a pure parse. If `node` is genuinely absent from the image, say so in your report rather than letting a `command not found` read as a pass.)

</verification>
