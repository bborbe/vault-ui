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
| `assignCall` | Function called by the "+ Assign to me" link. Only read when the assignee slot is enabled. |
| `assigneeClickable` | Whether the assignee badge is a filter control (clickable, `active` state, `filterByAssignee` onclick) or plain text. |
| `holdTitle` | `title` attribute of the hold badge. |

## `slots.*`

The template reads `config.slots` to decide what to emit. `live` is a
display-only indicator; everything else is an affordance the kind supports.

| Slot | Markup it gates | task | goal | topic |
| --- | --- | --- | --- | --- |
| `noteLink` | The title anchor to `obsidianUrl` (reserved; the template always renders the title link) | ✓ | ✓ | ✓ |
| `flag` | `flagButtonHtml(kind, item)` in the footer | ✓ | ✓ | ✓ |
| `hold` | The hold badge (`config.holdTitle`) | ✓ | ✓ | — |
| `assignee` | The assignee badge, or the "+ Assign to me" link | ✓ | ✓ | — |
| `priority` | The priority chip (`P<n>`) | ✓ | ✓ | — |
| `activityAge` | `activityAgeHtml(view.activityDate)` in the actions | ✓ | ✓ | — |
| `blocked` | The `card-blocked` row (`blockedBadgeHtml`) | ✓ | ✓ | — |
| `jira` | The `card-jira` row | ✓ | ✓ | — |
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
    activityDate,      // string | null
    blockers,          // string[] — [] when not blocked
    jiraKey,           // string — '' when none
    jiraUrl,           // string — '' when none
}
```

`mapTopicCard` reads exactly the topic response's eight wire keys — `id`,
`title`, `status`, `phase`, `session_state`, `flag`, `vault`, `obsidian_url` —
and returns the unsupported slots empty (`''`, `null`, `[]`). An absent field
renders no badge, never the literal `undefined`.

`blockers` is always an array: the builder's `view.blockers.length` gate depends
on it.

## Escaping

DOM text goes through `escapeHtml`; values interpolated into an `onclick`
string go through `escapeJsAttr`. The shared builder escapes the title link's
`obsidianUrl` (`${escapeHtml(view.obsidianUrl)}`). This is a deliberate
hardening over the previous per-kind code, where both the goal card and the
shared shell interpolated the URL raw.

## Layout note

The topic card's DOM wrappers changed with the unification: it used to nest
`.card-header > .card-title` with a bare `.card-footer`; it now emits
`.card-content > h3.task-title` and splits the footer into `.card-footer-left` /
`.card-actions`, like the other kinds. `.card-actions { margin-left: auto }`
(`style.css`) pushes the detail button to the right edge of the footer.

## Static-assertion lane

`tests/test_card_render_unify.py` and the neighbouring static suites read
`app.js` as text and slice function bodies with `_function_body` (up to the next
top-level `function`) or `_slice_function_body` (brace walking). A helper must
stay a top-level `function` declaration to remain assertable — a `const`
arrow-function assigned to a variable is invisible to these lanes.

## Adding an affordance

1. Edit the template once in `createCard`, gated on `config.slots.<name>`.
2. Add the slot to each `CARD_CONFIG` entry that wants it.
3. Fill the mapper with the data the affordance reads.
4. Extend the static test (a new slot assertion, and a mapper field if the
   view-model grew).
5. Add the browser-level proof to `tests/test_card_parity.py`.

## Browser lane

The static lane cannot see rendering. `tests/test_card_parity.py` is the
integration proof that all three boards actually render the shared template —
the topic card's slot set, the display-only live indicator, flag-first and
hold-sink ordering, the topic-only status filter, and the unchanged task/goal
slots.

It is integration-marked, so `make test` deselects it (and the YOLO container
has no Chromium). Run it on the host:

```
uv run playwright install chromium   # once
make test-integration
```
