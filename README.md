# Task Orchestrator

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
