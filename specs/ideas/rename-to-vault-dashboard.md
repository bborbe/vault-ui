---
status: idea
tags:
    - dark-factory
    - spec
---

## Idea

Rename the project from `vault-ui` to `VaultDashboard`.

- Display name: `VaultDashboard`
- Repo / package slug: `vault-dashboard`
- Python module: `vault_dashboard`

## Why now

The name is no longer accurate on two axes.

**It is not an orchestrator.** The orchestration machinery lives in `bborbe/agent` (`task/controller`, `task/executor`). This project is the operator-facing dashboard that surfaces what those orchestrators produce and lets a human steer them. Calling it `vault-ui` invites confusion with the actual controller/executor and misrepresents the layer.

**It is no longer just about tasks.** Recent and queued work has expanded the surface well beyond a thin task Kanban:

- Multi-vault filtering with multi-value URL params for status, phase, assignee, soon goal (spec 005)
- Status filter dropdown UI (status-filter-dropdown prompt shipped)
- "Assign to me" + claim-style operator actions
- Real-time WebSocket updates from vault-cli watchers
- Goal cleanup loop (spec 004)
- Goal filter pass-through (spec 006, frontend follow-up coming)
- Goal-board view foreshadowed in spec 006's do-nothing section
- Themes / objectives are the natural next hierarchy levels to surface

`VaultDashboard` is honest: an Obsidian-vault dashboard for an operator, with tasks as one of several first-class views.

## What changes

Rough inventory (not exhaustive — sizing only):

- GitHub repo: `bborbe/vault-ui` → `bborbe/vault-dashboard` (GitHub redirects clones / PRs / issue links)
- `pyproject.toml` `name`, `[project.scripts]` entry, hatch wheel target
- Package directory: `src/vault_ui/` → `src/vault_dashboard/`
- All in-tree Python imports
- `make` targets / scripts that reference the old name
- Cross-repo references in `~/Documents/workspaces/maintainer/CLAUDE.md`, `~/Documents/workspaces/agent/CLAUDE.md`, vault-cli, etc.
- Obsidian vault references under `~/Documents/Obsidian/Personal/` (daily notes, goal/task pages, hub pages)
- Operator browser bookmarks / shortcuts (manual)
- Note: existing prompts under `prompts/completed/` reference the old name in their context blocks — these are immutable history, no rewrite needed, but new prompts after the rename will use the new name

## Open questions

- Version bump: major (clear break) or minor (rename is cosmetic, behavior unchanged)?
- GitHub: in-place rename (relies on auto-redirect) vs new repo with archive of old?
- Migration timing: single big PR, or staged (rename internals first, repo last)?
- Do we batch the rename with the goal-board view so the "it's not just tasks" story lands in one release?
- Console script name: keep `vault-ui` as an alias for one release, or hard-cut to `vault-dashboard`?
- CLI service name in `vault-cli` integration / daemon configs — does anything outside this repo invoke `vault-ui` by name?

## Why not

- Rename costs hours of cross-repo cleanup (maintainer, agent, vault-cli docs, Obsidian vault, bookmarks). Defer until a feature ships that justifies announcing a "new" thing — most natural pair is the goal-board view, so the rename and the broader-scope story land together.
- The current name is wrong, but no user is confused yet — there is exactly one operator. Inertia is cheap.
- Auto-redirects from GitHub are reliable but not eternal; any external doc that hard-codes the old URL will eventually rot.
- dark-factory prompt history under `prompts/completed/` will be permanently inconsistent with the new name. Acceptable, but worth naming.

## Adjacent

Things the rename makes more natural / unblocks framing for:

- Goal-board view (spec 006 do-nothing) — fits cleanly under "dashboard", awkward under "task orchestrator"
- Theme-board / objective-board views — same argument, one level up the hierarchy
- Cross-vault dashboard (Personal + Trading + Octopus) — the name supports plural-vault scope without further renaming
- Operator-facing alerts / runbook surfacing in the same UI — "dashboard" is the right umbrella; "orchestrator" is not
- Splitting the read surface (dashboard) from the write surface (claim / assign actions) into clearer subsections, since "dashboard" reads as primarily observational
