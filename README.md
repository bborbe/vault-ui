# Vault UI

[![CI](https://github.com/bborbe/vault-ui/actions/workflows/ci.yml/badge.svg)](https://github.com/bborbe/vault-ui/actions/workflows/ci.yml)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/bborbe/vault-ui)

Orchestrate Claude Code sessions from Obsidian tasks.

## Where this fits in the bigger picture

vault-ui is the **human operator's Kanban view** into the bborbe task / agent system. It wraps [vault-cli](https://github.com/bborbe/vault-cli) as a FastAPI + Kanban web UI, watching vault file changes and offering a one-click launch of a Claude Code session per task.

It only reads / mutates the local vault — it never talks to Kafka, Kubernetes, or the upstream task pipeline. The tasks shown on the board are materialized into the vault by [agent](https://github.com/bborbe/agent)'s `task/controller`, fed by producers like [recurring-task-creator](https://github.com/bborbe/recurring-task-creator) and [maintainer](https://github.com/bborbe/maintainer)'s watchers.

Full system map: [recurring-task-creator/docs/system-map.md](https://github.com/bborbe/recurring-task-creator/blob/master/docs/system-map.md).

## Features

- Kanban board UI showing Obsidian tasks
- Clickable Obsidian links to open tasks in vault
- Start Claude Code sessions in project directories
- Session handoff via session ID
- Dark theme interface

## Prerequisites

- Python 3.12+
- [uv](https://docs.astral.sh/uv/) package manager
- [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) (`claude` command)
- `vault-cli >= 0.156.0` on `PATH` (the flag toggle passes `--by operator`; older versions reject it with `unknown flag: --by`)
- An Obsidian vault with tasks in frontmatter format

## Installation

```bash
uv sync --all-extras
```

## Usage

Start server:
```bash
make run
```

Or start with auto-reload on code changes:
```bash
make watch
```

Then open http://127.0.0.1:8000

## Goals view

The board has a top-of-board toggle that switches between the **Tasks** view (default) and the **Goals** view. Both views share the same status columns and live-update plumbing.

- Click the toggle to switch views — the URL is updated to `?view=tasks` or `?view=goals` and the new view's data is fetched.
- Deep-link to a specific view: open `http://127.0.0.1:8000/?view=goals` to land directly in the Goals view (no flash through the Tasks view).
- Goal cards are read-only — they link back to the goal file in Obsidian. To edit a goal, click the title (or the "Open in Obsidian →" link) and edit in the vault.
- Vault, status, and assignee filters apply to both views.

Toggle sits above the columns:

```
[ Tasks | Goals ]  [Vault ▾]  [Status ▾]  [Assignee ▾]  [Upcoming: 8h ▾]
```

The active view is encoded in the URL as `?view=tasks` or `?view=goals` and survives reload.

## API

The HTTP API is served under `/api`. The task endpoint is documented below; the
goal endpoints exist alongside it, and two topic endpoints serve the vault
hierarchy.

### `GET /api/tasks`

Lists tasks from one or more vaults.

| Parameter | Required | Description |
|---|---|---|
| `vault` | no | Vault name; repeatable, or comma-separated. Omit for every configured vault. |
| `status` | no | Comma-separated statuses to keep (e.g. `in_progress,todo`). Defaults to every status except `aborted`. |
| `phase` | no | Comma-separated phases to keep (e.g. `planning,execution`). A task with no phase counts as `todo`. |
| `assignee` | no | Comma-separated assignee names to keep. An empty value matches unassigned tasks. |
| `goal` | no | Comma-separated goal names; keeps tasks declaring any of them. |
| `upcoming_hours` | no | Width of the "upcoming" window for deferred tasks, 0–168 hours (default 8). `0` hides deferred tasks entirely. |
| `session_live` | no | `true` keeps only tasks whose `session_state` is `live` — a Claude session the board can prove is running right now. Default `false`. |

Each task carries `session_state`: `live` when the harness's session registry
lists the session or its transcript was written within the last five minutes or
a matching `claude` process is alive; `indeterminate` when a session id is set
but no transcript can be found; `quiet` when a transcript exists but is stale
and no process matches; and `null` for a task with no `claude_session_id`.

Each task also carries `open_questions`: the items of its `Open Questions`
section in section order, each `{index, text}`. A task with no such section
carries `[]`, never `null`. Answered items are **included**, not filtered out —
the section is the source of truth for what the task is waiting on, and the UI
is better placed to decide what an already-answered item means; only the item's
answer is withheld, so `text` is the question alone. `open_questions` is a
Go-only field: the superseded Python backend does not emit it, and the parity
harness strips it from both sides before comparing.

### `GET /api/topics`

Lists a vault's topics.

| Parameter | Required | Description |
|---|---|---|
| `vault` | no | Vault name; repeatable, or comma-separated. Omit for every configured vault. |

Every topic is returned, completed ones included — the endpoint always passes
`--all` to `vault-cli topic list`, whose bare form filters to `in_progress`.
Each topic carries the status read from that topic's own file, and a vault with
no topics folder contributes an empty list rather than an error.

```json
[
  {
    "id": "Manager Layer",
    "title": "Manager Layer",
    "status": "in_progress",
    "vault": "personal",
    "obsidian_url": "obsidian://open?vault=personal&file=23%20Topics/Manager%20Layer.md"
  }
]
```

### `GET /api/topics/{topic_id}`

Returns one topic plus the work it tracks.

| Parameter | Required | Description |
|---|---|---|
| `vault` | **yes** | Vault name. A topic name is a filename inside a per-vault `23 Topics/` folder, so it is not unique across vaults and cannot be inferred from the id. |

The response carries the topic's own fields plus three lists classifying the
entries of the page's `## Goals` section — which holds the topic's whole tracked
set, not just goals. An entry is the leading `[[wikilink]]` of a top-level `- `
bullet (alias stripped); a bullet with no leading wikilink contributes nothing.
Each entry is resolved against the vault's goals and tasks: a name found among
goals lands in `goals`, among tasks in `tasks`, and an unknown name in
`unresolved`. A topic with no tracked work returns empty lists.

```json
{
  "id": "Manager Layer",
  "title": "Manager Layer",
  "status": "in_progress",
  "vault": "personal",
  "obsidian_url": "obsidian://open?vault=personal&file=23%20Topics/Manager%20Layer.md",
  "goals": ["Manager Layer Rollout"],
  "tasks": ["Wire the topic endpoint"],
  "unresolved": ["Renamed Task"]
}
```

An unknown `vault` and a topic id that `vault-cli topic show` cannot resolve
both return HTTP 404.

## Group columns by phase or status

The kanban header has a `groupBy` selector that switches the columns between two dimensions:

- **Phase** (default for Tasks view): TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE — the task-phase workflow.
- **Status**: IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED — the canonical status taxonomy.

The active value is encoded in the URL as `?groupBy=phase` or `?groupBy=status` and survives reload. The default depends on the view: `?view=tasks` opens with `groupBy=phase`; `?view=goals` opens with `groupBy=status`. Unknown values (e.g. `?groupBy=bogus`) fall back to the kind default and the URL is rewritten to the resolved value.

Under `?view=goals&groupBy=phase`, goals without a `phase` field land in a single `—` column.

## Development

```bash
make sync        # Install dependencies
make format      # Format code
make lint        # Lint code
make typecheck   # Type check
make test        # Run tests
make precommit   # Run all checks
make build       # Build the Go binary
```

## Configuration

Copy the example config to the XDG config directory (preferred):
```bash
mkdir -p ~/.config/vault-ui
cp config.yaml.example ~/.config/vault-ui/config.yaml
```

vault-ui looks for config in this order:
1. `~/.config/vault-ui/config.yaml` (preferred)
2. `./config.yaml` in the repo root (legacy fallback — still supported for existing installs)

**Top-level fields:**
- `claude_cli` - Claude CLI command (default: `claude`)
- `host` - Server host (default: `127.0.0.1`)
- `port` - Server port (default: `8000`)
- `max_concurrent_sessions` - Cap on simultaneous Start-button launches in flight — cards showing "Starting…" (default: `20`); excess Starts are refused with HTTP 429. Open sessions do not count toward the cap

**Per-vault fields** (under `vaults:`):
- `name` - Display name for the vault
- `vault_path` - Absolute path to the Obsidian vault
- `vault_name` - Vault name for `obsidian://` URLs
- `tasks_folder` - Folder containing task files (e.g., `"24 Tasks"`)
- `claude_script` - Script to run Claude sessions (default: `claude`)
- `vault_cli_path` - Path to vault-cli binary (default: `vault-cli`)

**The `vaults:` block is optional.** An absent or empty block serves every
vault-cli vault that has a tasks folder, so vault-ui needs no second copy of
vault-cli's vault list to keep in sync. A vault-cli vault with no `tasks_dir`,
or whose tasks folder is missing on disk, is skipped with a warning naming the
vault rather than stopping startup.

vault-cli's optional per-vault `topics_dir` (e.g. `"23 Topics"`) is picked up as
`topics_folder`. It is **not** part of that skip gate — most vaults have none,
and a vault without one still serves its tasks and goals, contributing an empty
topic list to `GET /api/topics`.

An explicit non-empty `vaults:` block still filters — a vault-cli vault not
named there stays off the board — and still overrides `vault_name` for the
vaults it names.

## Task Format

Tasks must have frontmatter with:
```yaml
---
status: todo  # or in_progress, completed
project: /path/to/project  # Required for running
---
```

## License

BSD-2-Clause license. See [LICENSE](LICENSE) file for details.
