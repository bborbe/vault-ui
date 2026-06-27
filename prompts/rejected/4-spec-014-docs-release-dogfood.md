---
status: rejected
originalStatus: draft
spec: [014-goals-view-ux-hardening]
created: "2026-06-27T12:05:00Z"
branch: dark-factory/goals-view-ux-hardening
rejected: "2026-06-27T12:31:48Z"
rejectedReason: 'Same operator/container mix pattern as rejected spec 013 prompt 4: includes git tag + git push + uv sync + launchctl kickstart + browser DevTools + screenshots + PR creation — operator-only steps the dark-factory container cannot perform. CHANGELOG/README edits will land via prompts 1-3 each owning their own entries; release tagging + dogfood + PR handled manually post-merge.'
---

<summary>
- All four fixes are documented in `CHANGELOG.md` under a new `## v0.X.Y` section (v0.41.0 if prompt 2's `feat:` makes it a minor bump; v0.40.1 if only `fix:` bullets land — verifier picks based on the existing `## Unreleased` content).
- `README.md` gains a "Group columns by phase or status" section (added by prompt 2; verified still present) and the existing "Goals view" section is checked for accuracy post-prompt 2 changes (the `?view=tasks` default no longer applies to status columns if `?view=tasks&groupBy=status` is explicit).
- A new release tag matching `v[0-9]+\.[0-9]+\.[0-9]+` is pushed to the remote.
- Operator dogfoods: `uv sync` resolves to the new tag, `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator` restarts the launchd service, the operator exercises all four fixes by hand and posts before/after screenshots in the PR.
- The PR description includes the red→green regression test transcript from prompt 1, the column-header snapshot from prompt 2's kind-aware defaults, and the operator's screenshot pair.
- No Go or JS code changes — this prompt is docs + release + verification only.
</summary>

<objective>
Ship the umbrella spec 014: README + CHANGELOG + release tag + operator dogfood. Single PR for all three preceding prompts. The operator installs the resulting release via `uv sync` + `launchctl kickstart`, exercises all four fixes by hand, and approves.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists. Conventions for release + changelog follow `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read these files in full before editing (paths are absolute, host-side):
- `/workspace/CHANGELOG.md` — current state has `## Unreleased` (added by prompts 1 + 3) with three `fix:` bullets:
  - "Eliminate cross-view leak on Tasks/Goals toggle..." (from prompt 1)
  - "Remove redundant 'Open in Obsidian →' link from goal cards..." (from prompt 3)
  - "Drop silently-ignored `goal=` query param from `loadGoals()` requests..." (from prompt 3)
  Plus `## v0.40.0` at line 5 (from spec 013 prompt 3) and earlier versions below.

  Per the changelog-guide.md:
  - **Always** preserve the preamble (lines 1–3: `# Changelog`, `All notable changes...`, SemVer link). The `## Unreleased` block is removed on release — its bullets move to a `## vX.Y.Z` section. The SemVer preamble explains the bump rule.
  - Conventional prefix on every bullet (`feat:`, `fix:`, etc.). All three entries in `## Unreleased` are `fix:` — that means a **patch** bump: `v0.40.0 → v0.40.1`. (Prompt 2's `groupBy` selector and the `## Unreleased` entry it added — wait, prompt 2 did NOT add a `## Unreleased` entry, it added a README section. Re-verify before editing: the prompt 2 body says "prompt 4 does the CHANGELOG work", confirming prompt 2 did NOT modify CHANGELOG. So all `## Unreleased` bullets are `fix:` and the bump is `v0.40.0 → v0.40.1`.)

  Concretely: the new section is `## v0.40.1` with the three `fix:` bullets moved under it. The `## Unreleased` section is REMOVED (no more pending changes).

- `/workspace/README.md` — current state (after prompt 2):
  - Line 51–66: "## Goals view" section (added by spec 013 prompt 2).
  - Line 68: `## Group columns by phase or status` section (added by prompt 2).
  Verify both are present. The "Goals view" section may need a small clarification: the selector default depends on view (added by prompt 2). No content edit required if prompt 2's text is accurate.

- `/workspace/pyproject.toml` — line 31: `task-orchestrator = "task_orchestrator.__main__:main"`. The hatch-vcs build backend reads version from VCS tags (`source = "vcs"` at line 41). `uv sync` after a new tag will resolve to the new version automatically — no manual version bump in `pyproject.toml` required.

- `/workspace/Makefile` — `make sync` runs `uv sync --all-extras`. `make precommit` runs the full check suite (sync, format, test, lint, typecheck).

- `/workspace/docs/launchd-service.md` — the operator's launchd reference. Lines 121–145 cover the `LOG_LEVEL` env var via `launchctl setenv` and `launchctl kickstart -k gui/$UID/<label>`. The operator's dogfood command from the spec (`launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator`) matches the pattern at line 141 with `$(id -u)` replacing `$UID` (functionally equivalent).

- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — full file. Rules: preamble frozen, conventional prefix required, newest version first, no `### Added`/`### Fixed` categories.

- `/home/node/.claude/plugins/marketplaces/coding/docs/git-workflow.md` — branch + commit + push workflow. This prompt does NOT commit (dark-factory handles git); the prompt produces the CHANGELOG + README + tag. The operator (or management session) handles the actual commit + push + PR creation.

**Verified assumptions** (READ before writing any code):
- The tag format is `vX.Y.Z` per spec AC#14. Existing tags are `v0.40.0` (spec 013 prompt 3), `v0.39.0` (spec 013 prompt 2), `v0.38.0` (spec 013 prompt 1).
- `uv sync` resolves to the VCS tag automatically. The operator's existing plist (`~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist`) calls `uv run --directory <repo> task-orchestrator`. After `uv sync` against the new tag and `launchctl kickstart`, the service restarts on the new code.
- The PR description must include:
  - Link to the spec (specs/in-progress/014-goals-view-ux-hardening.md → once merged, specs/completed/)
  - Reference to the four umbrella task pages
  - Red→green regression test transcript (from prompt 1)
  - Column-header snapshot for both `?view=tasks` and `?view=goals` defaults (from prompt 2)
  - Operator before/after screenshots (dogfood)
  - AC checklist with checkmarks per spec lines 86–106
- The operator's dogfood is **NOT** automated. The prompt writes the verification commands; the human operator runs them and attaches evidence to the PR.

**No-goal of this prompt**: do NOT modify any Go/JS/Python code. do NOT add a new tag without operator approval (the operator manually creates the tag after verifying `make precommit` passes). do NOT commit or push (dark-factory handles git). do NOT modify the launchd plist (the operator owns that file).
</context>

<requirements>

### 1. Finalize CHANGELOG: move `## Unreleased` → `## v0.40.1`

In `/workspace/CHANGELOG.md`, the current `## Unreleased` block (added by prompts 1 + 3) holds three `fix:` bullets. Move them to a new `## v0.40.1` section directly above `## v0.40.0`. Delete the `## Unreleased` section entirely (no pending changes remain after this release).

The final shape:

```markdown
# Changelog

All notable changes to this project will be documented in this file.

Please choose versions by [Semantic Versioning](http://semver.org/).

* MAJOR version when you make incompatible API changes,
* MINOR version when you add functionality in a backwards-compatible manner,
* PATCH version when you make backwards-compatible bug fixes.

## v0.40.1

- fix: Eliminate cross-view leak on Tasks/Goals toggle — every sidebar interaction (vault switch, status filter, assignee filter, refresh button, periodic poll, WebSocket task event, drag-drop, slash command, clear session, assign-to-me) now routes through the `loadCurrentView()` dispatcher that fires only the active view's fetch. `handleTaskUpdate`'s task-event branch early-returns when `currentView === 'goals'` so a task event arriving while on Goals view does NOT mutate the goals DOM (spec AC#3). Regression test `tests/test_cross_view_leak.py` covers all migrated call sites.
- fix: Remove redundant 'Open in Obsidian →' link from goal cards below the title — the title `<a>` is the only link on the card now (spec AC#9). Clicking the title still opens the goal file in Obsidian (spec AC#10). No new `innerHTML` write site introduced (spec Security row 1).
- fix: Drop silently-ignored `goal=` query param from `loadGoals()` requests to `/api/goals` — the endpoint accepts only `vault`, `status`, `assignee`; the param was a no-op. Network panel now shows a clean URL. The `goal=` append in `loadTasks()` is untouched (`/api/tasks` accepts it as a valid filter).

## v0.40.0

... (preamble above stays unchanged; the v0.40.0 entry is from spec 013 prompt 3)
```

Bump rationale: all three bullets are `fix:` → patch bump → `v0.40.0 → v0.40.1`. Per `changelog-guide.md` rule: "any `feat:` entry → minor bump; everything else → patch bump". Prompt 2 added a `feat:` capability (`groupBy` selector) but did NOT add a CHANGELOG bullet — the editor of this prompt must verify whether prompt 2 added a `## Unreleased` entry. Re-read prompt 2 body — it does NOT add a CHANGELOG entry (prompt 4 owns CHANGELOG work). Therefore the bump is patch.

If the prompt 2 executor (YOLO container) DID add a `## Unreleased` entry despite the instruction, the editor must merge it into the `## v0.40.1` block with the correct prefix. The most likely entry prompt 2 would have added is:

```markdown
- feat: Add `groupBy` selector to kanban header — switches columns between phase (TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE) and status (IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED) views. URL-encoded as `?groupBy=phase` / `?groupBy=status`; kind-aware default (tasks→phase, goals→status). Unknown `?groupBy=` values fall back to the kind default. Under `?view=goals&groupBy=phase`, goals without a `phase` field land in a single `—` column.
```

If THAT bullet is present in `## Unreleased`, the bump is MINOR (`v0.40.0 → v0.41.0`) — change the section header accordingly.

### 2. Verify README sections are accurate post-prompts

Re-read `/workspace/README.md` lines 51–66 (Goals view) and the "Group columns by phase or status" section. Both should be present from prompts 2 + 3.

The Goals view section (spec 013 prompt 2) ends at line 66 with: "The active view is encoded in the URL as `?view=tasks` or `?view=goals` and survives reload." Add one sentence at the end acknowledging the groupBy dependency:

```markdown
The active view is encoded in the URL as `?view=tasks` or `?view=goals` and survives reload. See the "Group columns by phase or status" section below for how column dimensions switch with the view.
```

If prompt 2's editor already added this clarification, the editor of this prompt must not duplicate. Read the file and adjust accordingly.

The "Group columns by phase or status" section was added by prompt 2 with the canonical text. Verify it is byte-identical to the prompt 2 body. If absent, paste it from prompt 2 requirement 6.

### 3. Write the PR description template

The operator (or management session) opens a PR. Create `/workspace/docs/pr-014-description.md` with the PR body the operator pastes verbatim. The body includes:

```markdown
## Summary

Spec 014 — Goals view UX hardening. Closes the parent goal `Task Orchestrator Display Tasks and Goals`.

Four fixes in one PR:

1. **Cross-view leak fixed** — every sidebar interaction on `?view=goals` no longer fires `/api/tasks`. The `loadCurrentView()` dispatcher is the single entry point. Regression test `tests/test_cross_view_leak.py` covers all migrated call sites.
2. **`groupBy` selector added** — header carries a Phase/Status selector that switches the columns. URL-encoded, kind-aware defaults, "—" fallback column for goals without `phase`.
3. **Redundant "Open in Obsidian →" link removed** from goal cards below the title — the title `<a>` is the only link now.
4. **`goal=` query param dropped** from `loadGoals()` — the endpoint never accepted it; the silent ignore is gone.

## Test plan

- [ ] `make precommit` exits 0 (CI).
- [ ] `git revert HEAD --no-commit` → `pytest tests/test_cross_view_leak.py -v` → expect 4 tests FAIL → `git revert --abort` → expect 4 tests PASS. (Spec verification block; transcript in this PR's comments.)
- [ ] Manual: load `/?view=goals` → toggle `groupBy` to phase → observe `—` column appears → switch back to status → column disappears.
- [ ] Manual: load `/?view=goals` → click refresh button → Network panel shows ZERO `/api/tasks` requests.
- [ ] Manual: load `/?view=goals` → edit a task file in the vault → goals DOM is byte-equal before/after (WebSocket event for task is ignored).
- [ ] Manual: load a goal card → only ONE `<a>` element on the card (the title link); Network panel for `/api/goals` shows no `goal=` param.

## Acceptance criteria

- [ ] AC#1 (a-d): vault/status/assignee/refresh interactions on `?view=goals` leave columns containing only goal cards. `document.querySelectorAll('[data-card-kind="task"]').length` returns 0 after each interaction.
- [ ] AC#2: Network panel across the full interaction sequence shows zero `/api/tasks` requests on `?view=goals`.
- [ ] AC#3: WebSocket task event on `?view=goals` does NOT mutate goals DOM. Server access log shows no `/api/tasks` call. DOM hash before/after is equal.
- [ ] AC#4: Regression test `tests/test_cross_view_leak.py` red→green transcript captured in PR comments.
- [ ] AC#5: `document.querySelector('[data-testid="groupby-select"] option').length === 2` with values `phase` and `status`.
- [ ] AC#6: Changing the selector mutates `window.location.search` to include the new `?groupBy=` value AND re-renders columns.
- [ ] AC#7: `/?view=tasks` (no `groupBy=`) renders phase columns; `/?view=goals` (no `groupBy=`) renders status columns.
- [ ] AC#8: `/?view=goals&groupBy=phase` with a goal lacking `phase` renders in a single `—` column with no JS console error.
- [ ] AC#9: `document.querySelectorAll('[data-card-kind="goal"] a').length` is 1 (the title link).
- [ ] AC#10: Clicking the goal card title opens the goal file in Obsidian (`href` matches `^obsidian://open\?vault=.+&file=.+`).
- [ ] AC#11: `loadGoals()` request URL contains no `goal=` parameter (verified via `grep`).
- [ ] AC#12: README documents `?groupBy=` and the selector (`grep -n 'groupBy' README.md` returns ≥1 line).
- [ ] AC#13: CHANGELOG names all four fixes (`grep -n -i -E 'leak|groupby|obsidian link|goal= param' CHANGELOG.md` returns ≥4 lines).
- [ ] AC#14: New release tag pushed; `uv sync` exits 0.
- [ ] AC#15: Operator dogfood — before/after screenshots attached; both views toggle the selector cleanly.
- [ ] AC#16: `make precommit` exits 0 in the changed module.

## Dogfood evidence

[Operator attaches screenshots here after running `launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator`.]

- Screenshot 1: `?view=goals` BEFORE this PR — task cards bleed in after sidebar interaction (spec 013 state).
- Screenshot 2: `?view=goals` AFTER this PR — columns stay clean across all four interactions.

## Related

- Spec: specs/in-progress/014-goals-view-ux-hardening.md
- Precedent: specs/completed/013-task-orchestrator-goals-view.md (PR #14)
- Task pages: `Fix Task Cards Leaking into Goals View`, `Add GroupBy Selector to Task Orchestrator Kanban`, `Remove Redundant Open in Obsidian Link from Goal Cards`, `Remove Ignored Goal Filter Param from loadGoals`
```

The PR description is staged in the repo (`/workspace/docs/pr-014-description.md`) so the operator can copy-paste without re-typing.

### 4. Verification commands for the operator

This prompt does NOT run the operator's commands (those happen post-tag). It documents them so the prompt's `<verification>` block can run them in CI / precommit context:

```bash
# CHANGELOG preamble preserved
head -n 5 /workspace/CHANGELOG.md
# Expected: # Changelog\nAll notable changes...\nPlease choose versions by [Semantic Versioning]...\n* MAJOR version...\n* MINOR version...\n* PATCH version...

# CHANGELOG bump detection — count feat: vs fix: in the new section
sed -n '/^## v0\.40\.1/,/^## v0\.40\.0/p' /workspace/CHANGELOG.md | grep -c '^- fix:'
# Expected: 3 (or 4 if prompt 2 added a feat:)

# README mentions groupBy
grep -n 'groupBy' /workspace/README.md
# Expected: ≥1 line

# CHANGELOG mentions all four fixes
grep -c -i -E 'leak|groupby|obsidian link|goal= param' /workspace/CHANGELOG.md
# Expected: ≥4 distinct lines

# Precommit green
make precommit
# Expected: exit code 0
```

After `make precommit` exits 0, the operator (or management session) creates the tag:

```bash
git tag v0.40.1
git push origin v0.40.1
uv sync                                                       # against the new tag
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator
# Operator visits http://127.0.0.1:8000, exercises all 4 fixes, attaches screenshots to PR.
```

### 5. Operator dogfood checklist (added as PR comment template)

Create `/workspace/docs/dogfood-checklist-014.md`:

```markdown
# Operator dogfood checklist — spec 014

Run these steps after `uv sync` resolves to the new tag. Mark each item with a screenshot or note.

## Step 1: Restart the launchd service
```bash
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/
# Expected: 200
launchctl list | grep task-orchestrator
# Expected: status column = 0 (healthy)
```

## Step 2: Goals view — no cross-view leak
```bash
# Open http://127.0.0.1:8000/?view=goals in a browser with DevTools open.
# In DevTools console, run:
document.querySelectorAll('[data-card-kind="task"]').length
# Expected: 0
performance.getEntriesByType('resource').filter(r => r.name.includes('/api/tasks')).length
# Expected: 0
```
Then exercise each interaction and re-run the two assertions:
- [ ] (a) vault selector changed — both assertions still 0
- [ ] (b) status filter changed — both still 0
- [ ] (c) assignee filter changed — both still 0
- [ ] (d) refresh button clicked — both still 0

## Step 3: groupBy selector
- [ ] Open `/?view=tasks` (no `groupBy=`). Columns: TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE.
- [ ] Open `/?view=goals` (no `groupBy=`). Columns: IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED.
- [ ] Toggle `groupBy` to phase on Goals view. URL becomes `?view=goals&groupBy=phase`. A `—` column appears. Goal cards (which lack a `phase` field) land in `—`.
- [ ] Toggle `groupBy` to status on Tasks view. URL becomes `?view=tasks&groupBy=status`. Columns switch to status taxonomy.

## Step 4: Goal card cleanup
- [ ] Goal card has exactly ONE `<a>` element (the title link). Run:
      `document.querySelectorAll('[data-card-kind="goal"] a').length` → expect 1.
- [ ] Click the title → Obsidian opens the goal file.
- [ ] Network panel for `/api/goals` shows NO `goal=` query param.

## Step 5: WebSocket task event isolation
- [ ] On `?view=goals`, edit a task file's `status:` in the vault.
- [ ] Goals DOM does NOT change. No `/api/tasks` request fires.

## Step 6: Attach screenshots to PR
- [ ] Screenshot 1: Goals view BEFORE this PR (spec 013 state — task cards bleeding in). Find the spec 013 commit, check it out, exercise one sidebar interaction, screenshot.
- [ ] Screenshot 2: Goals view AFTER this PR — columns clean after all four interactions.

## Acceptance gate
All boxes ticked + both screenshots attached → PR ready for review.
```

This file is staged for the operator; the executor of this prompt does not run it.
</requirements>

<constraints>
- This prompt is docs + release + verification only. Do NOT modify any Python, JavaScript, HTML, CSS, or YAML code. Do NOT add new tests (the preceding prompts own testing). Do NOT modify `pyproject.toml` (the version comes from VCS tags via hatch-vcs).
- The CHANGELOG preamble (`# Changelog` + SemVer bullets) is FROZEN. Do NOT move, delete, or insert above it (per `changelog-guide.md` rule `changelog/preamble-frozen`). Insert `## v0.40.1` (or `## v0.41.0`) directly after the last preamble line.
- The `## Unreleased` section is REMOVED on release (no pending changes remain after this spec ships). If a future spec lands new work, the executor must create a fresh `## Unreleased`.
- The PR description file (`/workspace/docs/pr-014-description.md`) and the dogfood checklist (`/workspace/docs/dogfood-checklist-014.md`) are NEW docs files. They are the operator's reference — not user-facing documentation. They may stay in `docs/` or be deleted after the PR merges (operator's choice; out of scope for this prompt).
- The version bump follows the changelog-guide.md rule: any `feat:` → minor; everything else → patch. With three `fix:` bullets, the bump is `v0.40.0 → v0.40.1`. If prompt 2's editor added a `feat:` bullet, the bump is `v0.40.0 → v0.41.0` and the section header adjusts.
- Do NOT commit, push, or create the tag in this prompt. The dark-factory pipeline handles git. The operator creates the tag after `make precommit` exits 0.
- `make precommit` MUST exit 0. The CHANGELOG edit and the PR description file are markdown — they don't affect test/coverage targets.
- This prompt depends on prompts 1, 2, and 3 having shipped. The `## Unreleased` block must contain the expected three `fix:` bullets before this prompt can finalize the release.
</constraints>

<verification>
```bash
# Precommit at the very end (this is the only prompt that runs precommit
# at the prompt level — prompts 1-3 ran it during their own execution).
make precommit
# Expected: exit code 0

# CHANGELOG preamble preserved
head -n 7 /workspace/CHANGELOG.md
# Expected: # Changelog\n\nAll notable changes...\n\nPlease choose versions by [Semantic Versioning](http://semver.org/).\n\n* MAJOR version when you make incompatible API changes,\n* MINOR version when you add functionality in a backwards-compatible manner,\n* PATCH version when you make backwards-compatible bug fixes.

# New version section present and named per the bump rule
grep -E '^## v0\.(40\.1|41\.0)$' /workspace/CHANGELOG.md
# Expected: one match

# No leftover ## Unreleased
grep -c '^## Unreleased' /workspace/CHANGELOG.md
# Expected: 0

# README still has groupBy section
grep -n 'groupBy' /workspace/README.md
# Expected: ≥1 line

# All four fixes named in CHANGELOG
grep -c -i -E 'leak|groupby|obsidian link|goal= param' /workspace/CHANGELOG.md
# Expected: ≥4 distinct lines

# PR description file exists
test -f /workspace/docs/pr-014-description.md && echo OK

# Dogfood checklist exists
test -f /workspace/docs/dogfood-checklist-014.md && echo OK

# Quick spec AC evidence grep
grep -n -i -E 'leak' /workspace/CHANGELOG.md      # ≥1 line
grep -n -i 'groupby' /workspace/CHANGELOG.md      # ≥1 line
grep -n -i 'obsidian link' /workspace/CHANGELOG.md # ≥1 line
grep -n "goal= param\|goal='" /workspace/CHANGELOG.md # ≥1 line
```

The operator runs the post-tag steps manually:

```bash
git tag v0.40.1   # or v0.41.0 if the bump is minor
git push origin v0.40.1
uv sync
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.task-orchestrator
# ... exercise all 4 fixes, attach screenshots to PR.
```
</verification>

<success_criteria>
- [ ] AC#12: README documents `?groupBy=` URL param and the selector — verified by `grep -n 'groupBy' README.md` returning ≥1 line.
- [ ] AC#13: CHANGELOG `## Unreleased` (now `## v0.40.1` or `## v0.41.0`) names all four fixes — verified by `grep -n -i -E 'leak|groupby|obsidian link|goal= param' CHANGELOG.md` returning ≥4 distinct lines.
- [ ] AC#14: A new release tag matching `v[0-9]+\.[0-9]+\.[0-9]+` is pushed and `uv sync` exits 0 — verified by `git tag --list 'v*' | tail -n1` showing the new tag and the operator-reported `uv sync` exit code 0.
- [ ] AC#15: Operator dogfood — before/after screenshots attached to PR; both views toggle the selector cleanly — verified by PR review.
- [ ] AC#16: `make precommit` exits 0 in the changed module.
</success_criteria>

<depends_on>
- Prompt 1 (`1-spec-014-fix-cross-view-leak.md`): must have shipped. The `## Unreleased` block's first bullet is from prompt 1.
- Prompt 2 (`2-spec-014-add-groupby-selector.md`): must have shipped. The README's "Group columns by phase or status" section is from prompt 2; this prompt verifies it is still present.
- Prompt 3 (`3-spec-014-cleanups.md`): must have shipped. The `## Unreleased` block's second + third bullets are from prompt 3.
- Verify before editing:
  ```bash
  # All three preceding prompts' tests must pass
  uv run pytest tests/test_cross_view_leak.py tests/test_groupby_selector.py tests/test_goal_card_cleanup.py -v
  # Expected: all tests pass (4 + 10 + 5 = 19 tests)
  
  # CHANGELOG has the expected Unreleased content
  grep -A 10 '^## Unreleased' /workspace/CHANGELOG.md
  # Expected: three fix: bullets (or four if prompt 2 added a feat: bullet — re-verify)
  
  # README has the new section
  grep -A 3 'Group columns' /workspace/README.md
  ```
</depends_on>

<cross_references>
- Spec: `/workspace/specs/in-progress/014-goals-view-ux-hardening.md`
- Precedent: `specs/in-progress/013-task-orchestrator-goals-view.md` (merged via PR #14, commit `37bcf16`)
- Siblings: prompts 1 (`1-spec-014-fix-cross-view-leak.md`), 2 (`2-spec-014-add-groupby-selector.md`), 3 (`3-spec-014-cleanups.md`)
- Operator reference: `/workspace/docs/launchd-service.md` (launchd management), `/workspace/docs/definition-of-done.md` (DoD)
- Coding plugin docs: `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`, `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`
</cross_references>
