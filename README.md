# Task Orchestrator

[![CI](https://github.com/bborbe/task-orchestrator/actions/workflows/ci.yml/badge.svg)](https://github.com/bborbe/task-orchestrator/actions/workflows/ci.yml)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/bborbe/task-orchestrator)

Orchestrate Claude Code sessions from Obsidian tasks.

## Where this fits in the bigger picture

task-orchestrator is the **human operator's Kanban view** into the bborbe task / agent system. It wraps [vault-cli](https://github.com/bborbe/vault-cli) as a FastAPI + Kanban web UI, watching vault file changes and offering a one-click launch of a Claude Code session per task.

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
```

## Configuration

Copy the example config and edit vault paths:
```bash
cp config.yaml.example config.yaml
```

**Top-level fields:**
- `claude_cli` - Claude CLI command (default: `claude`)
- `host` - Server host (default: `127.0.0.1`)
- `port` - Server port (default: `8000`)

**Per-vault fields** (under `vaults:`):
- `name` - Display name for the vault
- `vault_path` - Absolute path to the Obsidian vault
- `vault_name` - Vault name for `obsidian://` URLs
- `tasks_folder` - Folder containing task files (e.g., `"24 Tasks"`)
- `claude_script` - Script to run Claude sessions (default: `claude`)
- `vault_cli_path` - Path to vault-cli binary (default: `vault-cli`)

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
