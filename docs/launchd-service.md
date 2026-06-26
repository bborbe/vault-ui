# Run task-orchestrator as a macOS launchd service

Use this setup when you want `task-orchestrator` running continuously so the Kanban UI is always reachable at `http://127.0.0.1:8000` without manually running `make run` in a terminal.

## Why use a launchd service?

- automatic startup after login
- automatic restart if the process exits
- one warm process with all vault watchers running
- shared between any browser tab, Raycast, or shortcut

## Prerequisites

- `uv` installed at `~/.local/bin/uv` (or adjust the plist)
- `vault-cli` on `PATH` — typically `~/Documents/workspaces/go/bin/vault-cli`
- A populated `config.yaml` in the repo root (`cp config.yaml.example config.yaml`)
- Repo cloned at `~/Documents/workspaces/task-orchestrator`

Verify:

```bash
command -v uv
command -v vault-cli
ls ~/Documents/workspaces/task-orchestrator/config.yaml
```

## 1. Create the launch agent

Create `~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.bborbe.task-orchestrator</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Users/YOUR_USER/.local/bin/uv</string>
        <string>run</string>
        <string>--directory</string>
        <string>/Users/YOUR_USER/Documents/workspaces/task-orchestrator</string>
        <string>task-orchestrator</string>
    </array>
    <key>WorkingDirectory</key>
    <string>/Users/YOUR_USER/Documents/workspaces/task-orchestrator</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/Users/YOUR_USER/.local/bin:/Users/YOUR_USER/Documents/workspaces/go/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/task-orchestrator.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/task-orchestrator.log</string>
</dict>
</plist>
```

**Important:**

- launchd does **not** expand `~` — use absolute paths everywhere.
- `uv run --directory <repo>` is required because `config.yaml` is loaded relative to the source tree (`src/task_orchestrator/../../../config.yaml`). A bare `task-orchestrator` invocation from a `uv tool install` would not find the config.
- The `PATH` env var must include the directory holding `vault-cli`, otherwise the watchers fail with `[Errno 2] No such file or directory: 'vault-cli'`.

Load and start:

```bash
launchctl load ~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist
```

## 2. Manage the service

Stop:

```bash
launchctl unload ~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist
```

Restart (stop + start, required after editing the plist or `config.yaml`):

```bash
launchctl unload ~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist
launchctl load ~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist
```

## 3. Verify

```bash
launchctl list | grep task-orchestrator         # status column 0 = healthy
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/   # expect 200
tail -f /tmp/task-orchestrator.log
```

A healthy startup logs `Uvicorn running on http://127.0.0.1:8000` and one `Started vault-cli watcher for vault: <name>` line per configured vault.

## 4. Upgrade flow

Code changes are picked up on restart (no install step — `uv run` resolves the local source):

```bash
cd ~/Documents/workspaces/task-orchestrator
git pull
launchctl unload ~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist
launchctl load ~/Library/LaunchAgents/com.github.bborbe.task-orchestrator.plist
```

If dependencies changed (`pyproject.toml` / `uv.lock`), `uv run` resyncs on next start automatically.

## 5. Log verbosity

task-orchestrator reads `LOG_LEVEL` from the environment at startup. Valid values (case-insensitive): `DEBUG`, `INFO`, `WARNING`, `ERROR`. Unset → `INFO` (default; same as before this knob existed). Invalid → falls back to `INFO` and logs a one-line WARN at startup.

The same level drives both Python's root logger and uvicorn's logger — bumping to `DEBUG` surfaces router internals, every HTTP request, AND the per-line streaming output of the long-running `vault-cli task work-on` subprocess (so you can watch the headless claude's tool calls arrive live instead of waiting 60–180s for the buffered exit).

**Set persistently via the plist** (preferred — survives `launchctl kickstart`):

```xml
<key>EnvironmentVariables</key>
<dict>
    <key>PATH</key>
    <string>/Users/YOUR_USER/.local/bin:/Users/YOUR_USER/Documents/workspaces/go/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    <!-- Uncomment to enable verbose logging:
    <key>LOG_LEVEL</key>
    <string>DEBUG</string>
    -->
</dict>
```

Apply the plist edit by restarting the service (section 2).

**Set transiently for a single restart** (clears on reboot or `launchctl unsetenv`):

```bash
launchctl setenv LOG_LEVEL DEBUG
launchctl kickstart -k gui/$UID/com.github.bborbe.task-orchestrator
# ... investigate ...
launchctl unsetenv LOG_LEVEL
launchctl kickstart -k gui/$UID/com.github.bborbe.task-orchestrator
```

Verify the level took effect:

```bash
tail -f /tmp/task-orchestrator.log
# At DEBUG you should see per-request lines and "vault-cli stdout [<task_id>]: ..." lines while a Start is in flight.
```

## Troubleshooting

### `launchctl list` shows non-zero exit / service keeps restarting

Check `/tmp/task-orchestrator.log`. Common causes:

- `uv` path wrong in the plist (`command -v uv` should match)
- `vault-cli` not in the `PATH` env var → `[Errno 2] No such file or directory: 'vault-cli'`
- Port 8000 already in use → `lsof -i :8000`
- `config.yaml` missing or malformed

### Changed `config.yaml` but vaults didn't update

launchd does not reread anything on its own — restart the service (see section 2).

### Port 8000 already in use

Edit `config.yaml` (`port: 8001`) and restart the service. The plist itself does not pin the port; the app reads it from `config.yaml`.

## Related

- `README.md` — overview and manual `make run` usage
- `config.yaml.example` — configuration reference
