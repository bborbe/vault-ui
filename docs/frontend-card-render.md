# Card rendering

Every card on the board — task, goal and topic — is produced by one template,
`createCard(kind, item)` in `src/vault_ui/static/app.js`. The template contains
no per-kind markup and no branch on a kind name. A kind contributes only data,
through two extension points:

1. **Its entry in `CARD_CONFIG`** — which affordances the kind carries and how
   its board places it.
2. **Its data mapper** (`mapTaskCard`, `mapGoalCard`, `mapTopicCard`) — which
   maps the kind's API response onto the common card view-model.

This replaces the three forked renderers (`createTaskCard`, `createGoalCard`,
`createTopicCard`) and the shared shell helper (`cardShellHtml`). Spec 017's
non-goal "no monolithic card builder" was reversed by the operator on
2026-10-31: a card-level change is now written once and every board inherits it.

## `CARD_CONFIG` fields

| Field | Meaning |
| --- | --- |
| `map` | The kind's data mapper. Called as `config.map(item)`. |
| `fallbackColumn` | Column id when the item's `phase` is absent/unrecognised (`todo` for tasks, `unknown` for goals and topics). Read by the renderer. |
| `statusFilter` | Whether the renderer applies the client-side status filter to this kind. Read by the renderer. |
| `draggable` | Whether the card is draggable and gets the drag listeners. |
| `idDataset` | `dataset` key carrying the item id (`taskId`, `goalId`, `topicId`). |
| `kindDataset` | Value for `dataset.kind`, or `null` to omit it. |
| `vaultDataset` | Whether `dataset.vault` is set. |
| `extraClass` | Extra class appended to the base `task-card` class (`''`, `goal-card`, `topic-card`). |
| `titleLinkExtraClass` | Extra class on the title anchor (`''`, `topic-title-link`). |
| `assignCall` | Function called by the "+ Assign to me" link. Only read when the assignee slot is enabled and the value is non-null; a kind with a null `assignCall` emits no assign control. |
| `assigneeClickable` | Whether the assignee badge is a filter control (clickable, `active` state, `filterByAssignee` onclick) or plain text. |
| `holdTitle` | `title` attribute of the hold badge. |

## `slots.*`

The template reads `config.slots` to decide what to emit. `live` is a
display-only indicator; everything else is an affordance the kind supports.

| Slot | Markup it gates | task | goal | topic |
| --- | --- | --- | --- | --- |
| `noteLink` | The title anchor to `obsidianUrl` (reserved; the template always renders the title link) | ✓ | ✓ | ✓ |
| `flag` | `flagButtonHtml(kind, item)` in the footer | ✓ | ✓ | ✓ |
| `hold` | The hold badge (`config.holdTitle`) | ✓ | ✓ | ✓ |
| `assignee` | The assignee badge, or the "+ Assign to me" link | ✓ | ✓ | ✓ |
| `priority` | The priority chip (`P<n>`) | ✓ | ✓ | ✓ |
| `mode` | The execution-mode pill (`⚙ headless` / `🖥 interactive` / the raw value) | ✓ | ✓ | ✓ |
| `activityAge` | `activityAgeHtml(view.activityDate)` in the actions | ✓ | ✓ | ✓ |
| `blocked` | The `card-blocked` row (`blockedBadgeHtml`) | ✓ | ✓ | ✓ |
| `jira` | The `card-jira` row | ✓ | ✓ | ✓ |
| `live` | The display-only `topic-live-badge` (no onclick/role/tabindex) | — | — | ✓ |
| `session` | `sessionButtonHtml(kind, item)` in the actions | ✓ | ✓ | — |
| `menu` | The `⋮` menu button wired to `showMenu` | ✓ | ✓ | — |
| `detail` | The `topic-detail-btn` and its `openTopicDetail(item)` listener | — | — | ✓ |

`menu: false` is also what turns the status controls off for a kind: every
status action (complete, hold, abort, defer) lives in the card menu built by
`showMenu`, so there is no separate status slot to disable. If a status
affordance outside the menu is ever wanted, it becomes its own slot then.

No card kind renders a `.status-badge`: the column is the phase.

## View-model

Each mapper returns exactly this shape and contains **no markup** — no
`innerHTML`, no `<div`, no `class=`. A mapper that emits markup is the fork
coming back under a new name.

```
{
    id,                // string
    vault,             // string
    title,             // string — heading text; task/goal Jira-stripped; topic falls back to id
    obsidianUrl,       // string
    status,            // string | null
    phase,             // string | null
    flag,              // boolean — item.flag === true
    live,              // boolean — the kind's own liveness signal
    hold,              // boolean — status === 'hold'
    upcoming,          // boolean
    recentlyCompleted, // boolean
    urgencyTier,       // number 0..3 for tasks, null for goal and topic
    assignee,          // string — '' when none
    priority,          // number | string | null
    mode,              // string | null — the page's own `mode:` frontmatter value
    activityDate,      // string | null
    blockers,          // string[] — [] when not blocked
    jiraKey,           // string — '' when none
    jiraUrl,           // string — '' when none
}
```

`mapTopicCard` reads the topic response's own eight wire keys — `id`, `title`,
`status`, `phase`, `session_state`, `flag`, `vault`, `obsidian_url` — plus the
seven data keys the topic endpoint now serves: `assignee`, `priority`,
`activity_date`, `jira`, `blockers`, `blocked` and `mode`. It returns the slots the
topic card does not carry (`upcoming`, `recentlyCompleted`, `urgencyTier`) as
their neutral values. An absent field renders no badge, never the literal
`undefined`.

`blockers` is always an array: the builder's `view.blockers.length` gate depends
on it.

## Escaping

DOM text goes through `escapeHtml`; values interpolated into an `onclick`
string go through `escapeJsAttr`.

`escapeHtml` is safe for **element text** and unsafe inside a **double-quoted
attribute**: it escapes `&`, `<` and `>` but not `"`. A page-derived value used
as a `title` is therefore assigned through the DOM (`el.title = value`, which
the browser escapes on serialisation) and never interpolated into markup.
`escapeJsAttr` is not a drop-in for a plain attribute value — it also rewrites
`'` to `\'`, which would render a literal backslash. The shared builder escapes the title link's
`obsidianUrl` (`${escapeHtml(view.obsidianUrl)}`). This is a deliberate
hardening over the previous per-kind code, where both the goal card and the
shared shell interpolated the URL raw.

## Layout note

The topic card's DOM wrappers changed with the unification: it used to nest
`.card-header > .card-title` with a bare `.card-footer`; it now emits
`.card-content > h3.task-title` and splits the footer into `.card-footer-left` /
`.card-actions`, like the other kinds. `.card-actions { margin-left: auto }`
(`style.css`) pushes the detail button to the right edge of the footer.

## Test coverage

The frontend's automated lanes were once Python: `tests/test_card_render_unify.py`
(static assertions over `app.js`) plus the Playwright suites
`tests/test_card_parity.py` and `tests/test_board_sort.py`, run by
`make test-integration`. All three were removed with the Python backend. A Go
replacement lane now lives in `pkg/board/jsasset`: `card_render_test.go` uses
the package's structural parser (`ObjectLiteral`, `ParseObjectLiteral`,
`ReturnedObject`, `FunctionBody`, `Branch`) to assert the card contract over the
embedded asset — slot sets, mapper payload reads, the guarded assign control,
the jira URL chain and the blocker resolver. It parses structure rather than
grepping, so a name in a comment or string literal cannot satisfy an assertion.

Those tests pin source structure, not rendering. A template change is still
verified by loading the board and exercising the affected card in a browser —
for a served asset, precommit-green is not proof that the page renders.

## Adding an affordance

1. Edit the template once in `createCard`, gated on `config.slots.<name>`.
2. Add the slot to each `CARD_CONFIG` entry that wants it. If the affordance
   needs a per-kind call, add it to `CARD_CONFIG` too and read it behind a
   non-null guard so a kind without one emits nothing.
3. Fill each enabled kind's mapper with the payload data the affordance reads.
   A kind that lacks the data keeps the slot off; never branch on the kind in
   the template.
4. Extend `pkg/board/jsasset/card_render_test.go` with a structural assertion
   for the new slot, mapper expression or guard.
5. Extend `pkg/board/testdata/card-vectors-pre-change.json` **by hand** for
   every kind the baseline guard covers (`task` and `goal`; never `topic`),
   adding the slot to `enabled_slots` and the mapper key to `mapper`. Never
   regenerate that file from the changed source — the guard asserts the sets
   exactly, and a regenerated fixture would make the pre-change subset check
   vacuous.
6. Load the board and confirm every card kind that carries the slot renders it.

