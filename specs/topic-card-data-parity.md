---
status: draft
created: "2026-11-09T22:10:00+01:00"
---

## Summary

- The topics board renders the same card template as the task and goal boards
- Six data slots are off for topics because the topic payload carries only one of the fields they need
- The topic payload gains assignee, priority, activity date, jira and blockers
- The slot map enables all six, so a badge appears wherever a page carries the data
- The task and goal boards render exactly as they do today

## Problem

The three boards share one renderer, and the only differences between them come from a per-kind slot map. A task or goal card carries an assignee badge, a priority chip, an activity age, a Jira badge, a blocked badge and a hold badge. A topic card carries a flag, a note icon and a live pill.

The operator has raised this three times, most recently on 2026-11-01 after seeing the deployed boards side by side: *"topics still look total different — can we use the same view for all 3?"*

The slots are off because the data does not exist, not because the design says so. The topic payload returns eight keys, and topic pages carry none of assignee, jira, blockers or activity date. Flipping the slots without the data would render empty chrome on all 22 `private-personal` topic cards, which is worse than the current bare card.

## Goal

The topics board renders the six data slots task and goal cards already render, backed by real per-page data, with no change to the task and goal boards.

## Non-goals

- The `session` slot on topic cards. It is not a config flip: the session runner routes every non-goal kind to the task endpoints, the card menu has no topic branch, and topics expose no run, take-over, resume or status route. Filed as its own task.
- The `menu` slot on topic cards. Same reason — it needs topic status mutations and a topic branch in the menu builder. Filed as its own task.
- Changing the task or goal slot sets to match topics. The direction is topics → task parity, not the reverse.
- The `↗` jump control on live topic cards, owned by a separate open task.
- Topic board column semantics (phase vs status grouping).
- New topic page types, or a topic frontmatter migration beyond the fields these slots need.
- Writing to topic pages. The 22-page backfill that gives these slots data is an operator step on the host, not a prompt — the container mounts this repo only, not the vault.

## Assumptions

- The vault-cli list item already carries `assignee` and `priority` for a page, so two of the five payload fields need no upstream change. Verified: the board already reads both from the list item on the goal and task paths.
- `jira` is not on the list item; it comes from a separate page-fields read that exists for tasks only. Topics need that path extended.
- No topic page currently carries `jira:`, `blocked_by:` or `status: hold`, so those three slots render nothing until the operator adds the data. Enabling them is still correct: each badge gates on data presence, so the slot costs nothing and starts rendering the day a page carries the field.
- The Jira badge can only render for a key whose project maps to a known Atlassian domain. The frontend derives the issue URL from a fixed project-to-domain map covering two projects, and renders no badge at all for any other key. This spec inherits that behaviour; it does not extend the map.
- **The blocked-badge resolver branch lands ahead of any data by design.** No topic page carries `blocked_by:` today, so that branch renders nothing until the operator adds it. It ships now because the slot is on and the resolver would otherwise mis-target; it is not re-litigated later.
- **The frontend half is verified by source structure, not execution.** The repo is Go-only since the Python harness was removed, and this change adds no dependency, so no container test can run the card builder. The Go tests assert the slot map, the mapper's source expressions and the guards; the rendering itself is proven at the Post-Deploy rung against the running board.

## Acceptance Criteria

- [ ] **AC1 — the slot map enables the six data slots for topics and keeps the four it has.** Evidence: a Go test reads the embedded frontend asset, locates the topic entry of the card config, and asserts its enabled-key set is a superset of `{hold, assignee, priority, activityAge, blocked, jira}` **and** still carries `{noteLink, flag, live, detail}`. It parses the object literal rather than grepping for names, so a slot name inside a comment cannot satisfy it. A superset test alone would permit silently dropping the topic's existing affordances. `make test` exits 0.
- [ ] **AC2 — the topic payload carries the six new keys.** Evidence: a Go test decodes a topic list response and asserts the key set is exactly `id`, `title`, `status`, `phase`, `session_state`, `flag`, `vault`, `obsidian_url` plus `assignee`, `priority`, `activity_date`, `jira`, `blockers` and `blocked`. `blocked` is required, not cosmetic: the shared badge builder gates on it, so without the key every topic renders an empty blocked container and the badge never appears. `make test` exits 0.
- [ ] **AC3 — the four stored values round-trip from page frontmatter.** Evidence: a Go test builds a topic page carrying `assignee`, `priority`, `jira` and `blocked_by`, and asserts the served payload returns each value. `activity_date` is excluded here because it is derived, not stored — AC5 covers it. A field parsed but never populated reads as `null`, so this AC is not satisfied by a struct change alone.
- [ ] **AC4 — a page without the data serves null, never a fabricated default.** Evidence: a Go test asserts a topic page carrying no `assignee:` serves `"assignee": null` and `"priority": null` — the keys are always present, their values are null. This is the negative half of AC3 and the only check that catches a default added later.
- [ ] **AC5 — activity date is derived, not stored.** Evidence: a Go table test asserts a page whose file mtime is older than its session transcript returns the transcript's timestamp, and a page with no session returns the file mtime. The transcript is located by the page's `ManagerSessionID` — named explicitly, because the list item also carries `ClaudeSessionID` and the existing topic code classifies that one, so an implementation reaching for the field already in use falls back to the file mtime and passes the second half while proving nothing. `make test` exits 0.
- [ ] **AC6 — the topic mapper's source maps every payload field.** Evidence: a Go test reads the embedded frontend asset, locates the topic mapper's returned object literal, and asserts each key's source expression references its payload field — assignee, priority, activity date, blocker list, and a Jira URL derived from the Jira key. The repo has no JS runtime, so the test asserts the expression, not the execution. A mapper returning a constant or an empty Jira URL fails. `make test` exits 0.
- [ ] **AC7 — the assign control is guarded by the kind's assign function.** Evidence: a Go test reads the embedded frontend asset and asserts the assign control is emitted only inside the **empty-assignee** branch — where it belongs, and where it already sits for tasks and goals — and only when the kind's assign function is non-null. Today that function is interpolated unconditionally in that branch, and a topic's function is null, so the emitted handler calls nothing. The guard must not require a non-empty assignee: that would remove the control from every unassigned task and goal card. The runtime half — a topic card showing no assign control while a task card shows one — is asserted at the Post-Deploy rung, which does run the frontend. `make test` exits 0.
- [ ] **AC8 — the Jira badge markup is an anchor on the derived URL.** Evidence: a Go test asserts the jira slot's markup is an anchor whose href is the mapper's derived Jira URL, not the raw key, and that the mapper derives an empty URL for a key outside the known project map — the same treatment a task card gets today. A plain-text span, or an href carrying the bare key, fails.
- [ ] **AC9 — the blocked badge names its still-open blockers.** Evidence: a Go test builds a topic page with three `blocked_by:` entries, one of which is completed, and asserts the served payload's `blockers` list carries the two open names and excludes the completed one, and that `blocked` is true. The badge label itself is emitted by the shared badge builder and is covered at the Post-Deploy rung. A list that includes the completed blocker, or a false `blocked`, fails.
- [ ] **AC10 — the hold badge is exactly `status: hold`.** Evidence: a Go test reads the embedded frontend asset and asserts the topic mapper's hold key expression compares the topic's status to `hold` — not a constant and not a truthiness test. The repo has no JS runtime, so the assertion is over the expression; the three-state behaviour follows from that one comparison and needs no separate table.
- [ ] **AC11 — a topic blocked badge navigates.** Evidence: a Go test reads the embedded frontend asset and asserts the blocker resolver branches on the topic kind to the topic id dataset. The resolver currently matches every non-goal kind against the task id dataset, and topic cards carry a topic id dataset instead, so the click falls through to a "blocker not on the board" report.
- [ ] **AC12 — the task and goal boards do not regress.** Evidence: the existing board test suite exits 0, and the task and goal card config slot sets plus the task and goal mapper object literals match a fixture captured with `git show <base-sha>:src/vault_ui/static/app.js` at the pre-change commit and committed at `pkg/board/testdata/card-vectors-pre-change.json` (create the directory). The comparison deliberately excludes the topic mapper, whose key expressions this change is required to alter — including it would make the AC unsatisfiable. Capture happens in the first prompt, before any behaviour change; a fixture authored after the change compares itself to itself and does not satisfy this.
- [ ] **Post-Deploy (Rung-2):** the deployed local service serves the new payload and the backfilled pages render — evidence: the served `/api/topics` response carries the new keys, the served page references a changed asset token, every rendered `private-personal` topic card carries a badge whose text equals that page's own `assignee:` value and a chip equal to `P` plus its `priority`, a page carrying `jira:` renders an anchor whose href is the issue URL, a page carrying `blocked_by:` with one open entry renders a badge naming it, no card from an un-backfilled vault renders an empty badge shell, and no topic card shows an assign-to-me control while at least one task or goal card does. The Jira and blocked clauses need one backfilled page each; the backfill step below covers them.
  - `deploy_check:` `curl -s http://127.0.0.1:8000/ | grep -o 'app.js?v=[^"]*'`
  - `deploy_target:` `app.js?v=2026-11-09-topic-card-data-parity`

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — format, vet and test all pass
- `make test` — the single `go test -race ./...` run this repo has; there is no separate integration lane

### Operator-executable (runs on the host after PR merge)

- `curl -s 'http://127.0.0.1:8000/api/tasks?vault=private-personal&session_live=true' | jq 'length'`, the same for `/api/goals` and `/api/topics` — all three must print `0`. The restart below ends every session the board tracks as live, across all three kinds, so checking topics alone is not enough.
- `make build` then `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui` — redeploy the local service.
- Backfill the vault: add `assignee` and `priority` to each of the 22 `private-personal` topic pages that lack them, and `jira` only where a topic tracks one. This is the step that makes the change visible; without it every card renders bare. It runs on the host because the vault is not mounted in the container.
- `curl -s 'http://127.0.0.1:8000/api/topics' | jq '.[0] | keys'` — the new keys are served
- Playwright against the running board: for each rendered `private-personal` topic card, assert `.assignee-badge > span:last-child` text equals that page's `assignee:` frontmatter value and `.priority-chip` text equals `P` + its `priority`. A card whose badge text differs from its own page fails.
- Update `50 Knowledge Base/vault-ui.md` so the topic payload's new keys are documented there. That page lives in the operator's vault, outside this repo.

## Desired Behavior

1. The topics board renders an assignee badge on every topic that has an assignee, and no assign control on any that does not.
2. The topics board renders a priority chip on every topic that has a priority.
3. The topics board renders an activity age derived from the newer of the page's file mtime and its manager session's transcript mtime, matching how a task card's age is derived.
4. The topics board renders a Jira badge, linked to the issue, on every topic whose page carries a Jira key that maps to a known Atlassian project domain. A key outside that map renders no badge — the same as a task card does today.
5. The topics board renders a blocked badge naming the still-open blockers, and clicking it selects the blocker's own card on the same board.
6. The topics board renders a hold badge on a topic at `status: hold` and on no other topic.
7. The task and goal boards render exactly as they do today: same badges, same values, same controls.

## Constraints

- The card template stays shared. A kind contributes data — one slot-map entry and one mapper — never markup and never a branch on the kind inside the card builder.
- Every value interpolated into markup goes through the existing escaping helpers. A value placed inside an inline handler must use the JS-attribute escape, not the HTML one.
- The topic payload's existing eight keys keep their names and meanings. This is additive.
- The task and goal payloads and slot maps are unchanged.
- No new dependency and no change to the vault-cli module; every field is derived from page data the board already reads.
- `CHANGELOG.md` carries an entry under `## Unreleased`; create that heading if it is absent, since the file's top section is currently a released version. `make precommit` does not check this, so it is a separate obligation.
- The frontend asset's cache-buster token in the served page is bumped in the same change, to the value the Post-Deploy acceptance criterion asserts. Without the bump a browser keeps the old asset and the change is invisible even when it shipped.
- `docs/frontend-card-render.md` is the authoritative description of the slot map, the mapper shape and the procedure for adding an affordance. It is updated in the same change: its slot table, its "topic reads exactly eight wire keys" paragraph, its affordance procedure, and its "no automated frontend lane remains" test-coverage paragraph all become wrong otherwise. The same correction applies to the mapper's own source comment that states the topic response carries exactly those eight keys, and to `README.md`'s `GET /api/topics` example, which gains the new keys.
- The five source-structure assertions read the frontend asset through one shared helper that parses the card config and the mapper object literals, rather than five independent regexes — one reformat of the asset would otherwise break all five separately.
- **This supersedes spec 036's non-goal "do not add `assignee` or `priority` to topic pages, or render them on the topic card — that is a schema decision for the operator."** The operator made that schema decision on 2026-11-01 by asking for card parity, and again on 2026-11-09 by approving the plan. Spec 036's line 76 ("do not backfill `phase`, `assignee`, `priority` or `manager_session_id`") is narrowed accordingly: the backfill here writes `assignee`, `priority` and `jira` only, and still never writes `phase` or `manager_session_id`.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| A topic page carries no assignee | The card renders no assignee badge and no assign control | None needed; this is the expected empty state |
| A topic page's frontmatter holds a malformed priority | The payload omits the value and serves `"priority": null` rather than failing the whole topic list | The card renders no chip; re-run the topics request and confirm the key is present with a null value, not missing |
| A topic's session transcript is missing | The activity date falls back to the page's file mtime | None needed; the age is still rendered |
| A blocked-by name matches no topic on the board | The click reports "blocker not on the board" rather than silently doing nothing | The toast names the blocker; confirm it is a topic, not a task, by checking the topic folder for that name |
| A topic page carries a Jira key outside the known project map | The card renders no Jira badge, exactly as a task card does today | Add the project to the frontend's project-to-domain map, or leave the key off the page |
| The cache-buster is not bumped | The deployed page serves the old asset and the change is invisible | Confirm the served token differs from the pre-change value; a matching token means the bump was missed |
| The backfill runs while the board is serving | A page caught mid-write is read as its previous state or with the field absent; the next request sees the new value | Re-request the board; no action needed |
| The backfill stops partway through the 22 pages | Some cards carry badges and others do not; nothing fails | Re-run the backfill over the pages still missing the fields |
| A clock or timezone difference skews the activity age | No skew: the shared helper compares the page mtime and the transcript mtime in UTC | None needed; the helper already normalizes both to UTC |
| The topic-page backfill is skipped | Every card renders bare and the change delivers nothing visible | The Post-Deploy criterion fails and the work does not close |

## Security / Abuse Cases

The change reads operator-written frontmatter and adds no write surface. The endpoints stay read-only. The Jira href is never interpolated from raw frontmatter: it is derived by regex from a fixed project-to-domain map, so only a constructed `https://<known-domain>/browse/<KEY>` string can reach the attribute. A blocker name is interpolated into an inline handler and goes through the JS-attribute escape, not the HTML one. Both properties must hold if the derivation is ever loosened.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Payload fields, and capture the pre-change card vectors fixture before anything else changes | 1, 2, 3, 4, 5, 6 | AC2, AC3, AC4, AC5, AC12 (capture) | — |
| 2 | Slot map, mapper, assign guard, blocker-resolver topic branch, cache-buster bump, card-render doc, README, CHANGELOG entry | 1, 2, 4, 5, 6, 7 | AC1, AC6, AC7, AC8, AC9, AC10, AC11 | prompt 1 (needs the payload fields) |
| 3 | Non-regression assertion against the fixture captured in prompt 1 | 7 | AC12 (assert) | prompts 1, 2 |

Rationale: prompt 1 establishes the payload the frontend reads and takes the baseline snapshot while the tree is still pre-change; prompt 2 is the only layer that changes what a user sees, and depends on it; prompt 3 asserts the snapshot still matches once both are in place. Each prompt owns the tests for its own layer. The vault backfill and the knowledge-base documentation step are host-side after merge, not prompts — both touch the operator's vault, which the container does not mount.

## Do-Nothing Option

The topics board keeps its four slots and continues to look nothing like the other two boards. The operator has raised this three times; a fourth filing would carry none of the three prior filings' history. The cost of doing nothing is that the shared card template stays half-used, and every future card affordance has to be built twice — once for the kinds that carry data and once for the kind that does not.

The rejected alternative was a topic-specific card design: a bespoke topic card could show the fields a topic actually has without touching the shared renderer. It was rejected because it re-forks the template the three boards were just unified onto, and the operator's ask was explicitly for the same view on all three.
