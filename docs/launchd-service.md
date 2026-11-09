# Run vault-ui as a macOS launchd service

Use this setup when you want `vault-ui` running continuously so the Kanban UI is always reachable at `http://127.0.0.1:8000` without manually running `make run` in a terminal.

## Why use a launchd service?

- automatic startup after login
- automatic restart if the process exits
- one warm process with all vault watchers running
- shared between any browser tab, Raycast, or shortcut

## Prerequisites

- Go toolchain (`go` on `PATH`) to build the binary
- `vault-cli` on `PATH` — typically `~/Documents/workspaces/go/bin/vault-cli`
- A populated `config.yaml` (XDG location preferred: `~/.config/vault-ui/config.yaml`; legacy repo-root fallback: `cp config.yaml.example config.yaml`)
- Repo cloned at `~/Documents/workspaces/vault-ui`

Verify:

```bash
command -v go
command -v vault-cli
ls ~/.config/vault-ui/config.yaml
```

## 1. Build the binary

```bash
cd ~/Documents/workspaces/vault-ui
make build     # writes ~/Documents/workspaces/go/bin/vault-ui
```

## 2. Create the launch agent

Create the log directory first. launchd does not create the parent directory of
`StandardOutPath`, and `~/Library/Logs` does not exist on a fresh macOS account:

```bash
mkdir -p ~/Library/Logs
```

Create `~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.bborbe.vault-ui</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Users/YOUR_USER/Documents/workspaces/go/bin/vault-ui</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>/Users/YOUR_USER</string>
        <key>PATH</key>
        <string>/Users/YOUR_USER/Documents/workspaces/go/bin:/opt/homebrew/bin:/usr/local/bin:/Users/YOUR_USER/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>VAULT_UI_LISTEN</key>
        <string>127.0.0.1:8000</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/Users/YOUR_USER/Library/Logs/vault-ui.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/YOUR_USER/Library/Logs/vault-ui.log</string>
</dict>
</plist>
```

**Important:**

- launchd does **not** expand `~` — use absolute paths everywhere.
- **`VAULT_UI_LISTEN` pins the bind address.** The binary defaults to `:8000`, which listens on **every** interface and serves every vault page to the LAN. `host:`/`port:` in `config.yaml` do not set the API listener, so a plist without this key silently exposes the board. `127.0.0.1:8000` keeps it loopback-only.
- The `PATH` env var must include the directory holding `vault-cli`, otherwise the watchers fail with `exec: "vault-cli": executable file not found`.
- WezTerm does **not** need to be on the `PATH`. The board resolves the `wezterm` executable itself, from its macOS application bundle (`/Applications/WezTerm.app/Contents/MacOS`), when it launches the pane-resolution helper behind the card's `↗` jump control.
- `WEZTERM_UNIX_SOCKET` must **not** be set in the plist. launchd starts the service without it, and the board then picks the newest `~/.local/share/wezterm/gui-sock-<pid>` whose process is still alive — the GUI socket name carries the WezTerm process id and changes on every restart, so it cannot be pinned in the plist. Without this, `wezterm cli` falls back to `~/.local/share/wezterm/sock`, a separate mux server that holds almost none of the operator's panes, and the card's `↗` jump answers 409.

Load and start:

```bash
launchctl bootstrap gui/$UID ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist
```

## 3. Manage the service

Stop:

```bash
launchctl bootout gui/$UID/com.github.bborbe.vault-ui
```

Restart (required after editing the plist; a `config.yaml` vault change is picked up by `↻ Refresh` on the board, or automatically within 30 seconds, instead):

```bash
launchctl bootout gui/$UID/com.github.bborbe.vault-ui
launchctl bootstrap gui/$UID ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist
```

## 4. Verify

```bash
launchctl list | grep vault-ui                     # status column 0 = healthy
lsof -nP -iTCP:8000 -sTCP:LISTEN                   # expect 127.0.0.1:8000, not *:8000
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/   # expect 200
tail -f ~/Library/Logs/vault-ui.log
```

The `lsof` line is the check that catches a bind-address regression: a `*:8000` entry means `VAULT_UI_LISTEN` is missing from the plist and the board is reachable from the LAN.

## 5. Upgrade flow

The plist runs the **built binary** at `~/Documents/workspaces/go/bin/vault-ui`, not the repo checkout — the frontend is embedded in the binary at build time. A `git pull` alone leaves the live board on the old code, so rebuild and restart after every pull:

```bash
cd ~/Documents/workspaces/vault-ui
git pull
make build
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
```

After a static-file change the browser also needs a hard refresh: the `app.js` URL is version-pinned (`?v=…`), so an old tab keeps the cached copy.

Verify the rebuild actually landed — compare the binary's mtime against the pull, and the served token against the tree:

```bash
ls -l ~/Documents/workspaces/go/bin/vault-ui          # mtime must be after the pull
curl -s http://127.0.0.1:8000/ | grep -o 'app.js?v=[^"]*'
```

## 6. Logging

The binary logs through `glog`. Warnings and errors always land in
`~/Library/Logs/vault-ui.log` in the form `2026/11/09 16:31:01 WARN <message>`.
There is no `LOG_LEVEL` environment knob — that belonged to the retired Python
backend and is not read here.

Common startup warnings, both benign:

- `skipping unreadable page file="…" error="no frontmatter found"` — a note under a watched vault has no YAML frontmatter and is not a board page.
- `WARN watch failed dir="…" error="too many open files"` — the process ran out of file descriptors; see Troubleshooting.

## Troubleshooting

### `launchctl list` shows non-zero exit / service keeps restarting

Check `~/Library/Logs/vault-ui.log`. Common causes:

- binary path wrong in the plist (`ls -l ~/Documents/workspaces/go/bin/vault-ui` should exist and be executable)
- `vault-cli` not in the `PATH` env var → `exec: "vault-cli": executable file not found`
- Port 8000 already in use → `lsof -i :8000`
- `config.yaml` missing or malformed

### `too many open files`

The watcher holds one file descriptor per watched page. Across every Obsidian
vault that is tens of thousands of descriptors, so a low per-process limit fails
the watch for whole directories and those vaults silently stop updating. The
binary raises its own soft limit at startup (`pkg/fdlimit`); check the actual
limit and the current descriptor count:

```bash
launchctl procinfo $(pgrep -f 'go/bin/vault-ui') | grep -i maxfiles
lsof -p $(pgrep -f 'go/bin/vault-ui') | wc -l
```

If the count approaches the limit, the fix is a code change (directory-level
watching), not a plist tweak.

### Changed `config.yaml` but vaults didn't update

Click `↻ Refresh` on the board: it re-reads the config file and `vault-cli
config list`, reconciles the single vault watcher, and rebuilds the vault selector
(`POST /api/config/reload`) — no restart needed. The `claude_script` and vault
paths come from vault-cli, which is re-read per invocation, so those changes are
picked up the same way.

`↻ Refresh` is no longer the only trigger: the vault set is also re-read
automatically every 30 seconds, so a vault added, removed or renamed in
`config.yaml`/vault-cli shows up on its own within that window. The automatic
reload only restarts the watcher when the vault set actually changed, so live
updates keep flowing on every other tick. A vault name vault-cli no longer knows
is skipped with a warning in the log — the board, the assignee dropdown and the
goals view render the remaining vaults instead of returning HTTP 500.

A restart (section 3) is still the fallback when the reload fails — e.g. a
config that does not parse. The reload reads the new config before it tears
anything down, so a broken edit leaves the running service untouched and the
toast names the error.

### Port 8000 already in use

Change `VAULT_UI_LISTEN` in the plist to `127.0.0.1:8001` and restart the
service. `config.yaml`'s `port` does not move the API listener.

## Related

- `README.md` — overview and manual `make run` usage
- `config.yaml.example` — configuration reference
