---
status: approved
spec: [038-topic-card-data-parity]
created: "2026-11-10T07:10:00+01:00"
queued: "2026-11-11T07:04:38Z"
---

# Render the six data slots on topic cards

<summary>

- The topic card enables the six data slots the task and goal cards already render
- The topic card keeps the four slots it already has
- The topic mapper reads the payload fields the previous prompt added
- An unassigned topic shows no assign control, and task and goal cards keep theirs
- A blocked badge on a topic selects the blocker's own topic card
- The served page references a new asset token, so browsers pick up the change
- The card-render doc, the README example and the changelog record the new contract

</summary>

<objective>

Turn the six data slots on for the topic card and make the mapper supply them, so a topic card carries the same badges as a task or goal card wherever its page has the data — without changing what a task or goal card renders.

</objective>

<context>

Read `docs/frontend-card-render.md` — it is the authoritative description of the slot map, the view-model shape and the procedure for adding an affordance. Follow it.

Read `src/vault_ui/static/app.js` — `const CARD_CONFIG` (the `topic` entry), `mapTopicCard`, `createCard`, `blockedBadgeHtml`, `navigateToBlocker` and `extractJiraIssue`.

Read `pkg/board/jsasset` (added by the previous prompt) — the shared parser the new tests use to locate the card config and mapper object literals.

Read `pkg/api/api.go` — `TopicResponse`, which the previous prompt extended.

</context>

<requirements>

1. In the `topic` entry of `CARD_CONFIG`, set `hold`, `assignee`, `priority`, `activityAge`, `blocked` and `jira` to `true`. Leave `noteLink`, `flag`, `live` and `detail` as they are — they must stay enabled. `session` and `menu` stay `false`: the spec's non-goals exclude them because they need topic run and status routes that do not exist. `assigneeClickable` stays `false` and `assignCall` stays `null`.

2. Extend `mapTopicCard` so each view-model key reads its payload field instead of returning a constant: `assignee` from the payload's assignee, `priority` from its priority, `activityDate` from its activity date, `blockers` from its blocker list, `jiraKey` from its jira value, and `jiraUrl` derived from that jira value through the same helper a task card uses. `hold` compares the payload's status to `hold` — not a constant, not a truthiness test. Keep the mapper free of markup: no `innerHTML`, no tags, no class strings.

3. In `createCard`, the assign control is emitted in the empty-assignee branch. Guard it on the kind supplying an assign function: a kind whose assign function is null emits no assign control at all. Do not move the control into the non-empty branch — that would remove it from every unassigned task and goal card.

4. In `navigateToBlocker`, a topic resolves its blocker against the topic id dataset, not the task id dataset. Today every non-goal kind matches the task id, and a topic card carries a topic id, so the click always falls through to the "blocker not on the board" report.

5. Bump the cache-buster token in `src/vault_ui/static/index.html` to `2026-11-09-topic-card-data-parity`.

6. Add Go tests using the shared parser from `pkg/board/jsasset`. Locate `createCard` and `navigateToBlocker` through it; if it has no function-body or branch locator, extend the helper — do not add an independent regex in the test file.
   - the `topic` entry's enabled slot set is a superset of the six data slots and still contains `noteLink`, `flag`, `live` and `detail`;
   - each key of `mapTopicCard`'s returned object literal references its payload field, and the jira URL expression derives from the jira key;
   - the assign control is emitted only inside the empty-assignee branch and only when the assign function is non-null;
   - the jira slot's markup is an anchor whose href is the mapper's derived URL (this holds today — it pins the shape, it is not new coverage);
   - the topic mapper's `jiraUrl` expression is the empty-string fallback of the URL derived from the jira key, that URL comes from `extractJiraIssue`, and that helper's domain lookup stays gated on the fixed project-to-domain map — so a key whose project is not in the map yields an empty URL and renders no badge;
   - the blocker resolver branches on the topic kind to the topic id dataset;
   - the topic mapper's hold expression compares the status to `hold`.

7. Update `docs/frontend-card-render.md`: the slot table's topic column, the `assignCall` field-table row (it is now also read only when non-null), the paragraph stating the topic mapper reads exactly eight wire keys, the affordance procedure, and the test-coverage paragraph that currently says no automated frontend lane remains. Also correct the comment above `mapTopicCard` that states the topic response carries exactly those eight keys.

8. Update the `GET /api/topics` example in `README.md` to include the six new keys.

9. Add an entry under `## Unreleased` in `CHANGELOG.md` describing the change. If this prompt and the previous one both add a heading, keep a single `## Unreleased` section.

10. Before finishing, re-run `make precommit` and confirm it passes; walk each requirement above against the change.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- The card template stays shared: a kind contributes data — one slot-map entry and one mapper — never markup, and never a branch on the kind inside the card builder.
- Every value interpolated into markup goes through the existing escaping helpers. A value placed inside an inline handler must use the JS-attribute escape, not the HTML one.
- The task and goal payloads and slot maps are unchanged.
- No new dependency.
- Every frontend assertion is a source-structure assertion over the embedded asset — this repo has no JavaScript runtime and no browser test lane.

</constraints>

<verification>

`make precommit` — must pass.

</verification>
