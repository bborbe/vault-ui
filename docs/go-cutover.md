# Cut over vault-ui from the Python backend to the Go binary

The final step of the Go rewrite: repoint the launchd LaunchAgent from the
uv-installed Python tool to the Go binary built from this repo. Until this runs,
the board on `http://127.0.0.1:8000` is still served by Python.

**This is an operator-run procedure. No dark-factory prompt performs it.** It stops
and replaces a running service, so it sits deliberately outside the pipeline: spec
`023-go-backend-api-and-cutover.md` declares the cutover operator-gated and keeps
every service restart, plist edit and tool reinstall out of `prompts/`.

Parity is already proven before you start — `make parity` compares the Go and Python
backends across all 25 routes, the error shapes, the write path and the WebSocket
frames. This runbook is about the switch, not about re-establishing that.

## What changes

| | Before | After |
|---|---|---|
| `ProgramArguments[0]` | `~/.local/bin/vault-ui` (uv-installed Python) | `~/Documents/workspaces/go/bin/vault-ui` |
| Backing code | `~/.local/share/uv/tools/vault-ui/` (installed snapshot) | the repo checkout, compiled |
| Upgrade step | `git pull` + `uv tool install --force --no-cache .` | `git pull` + `make build` + restart |

The `PATH` contract, `WorkingDirectory`, `KeepAlive`, `RunAtLoad` and the log paths
do not change. `docs/launchd-service.md` documents the plist shape and why the PATH
ordering matters.

## Before you start

**Wait for in-flight launches.** A restart kills the subprocesses vault-ui itself
spawned. If the board shows any `⏳ Starting...` card, let it finish before you
restart — the restart table in `docs/starting-marker-lifecycle.md` explains what is
lost otherwise.

## 1. Build the Go binary

```bash
cd ~/Documents/workspaces/vault-ui
git pull
make build
```

`make build` writes to `~/Documents/workspaces/go/bin/vault-ui`. Confirm it is a real
executable and not a leftover script:

```bash
file ~/Documents/workspaces/go/bin/vault-ui     # → Mach-O 64-bit executable arm64
```

## 2. Repoint the LaunchAgent

Edit `~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist` so that
`ProgramArguments` is the single Go binary path:

```xml
<key>ProgramArguments</key>
<array>
    <string>/Users/YOUR_USER/Documents/workspaces/go/bin/vault-ui</string>
</array>
```

Leave `PATH`, `WorkingDirectory`, `KeepAlive`, `RunAtLoad` and the log paths as they
are.

If your plist currently spells the Python service as
`uv run --directory <repo> vault-ui`, replace the **whole array** with the single
element above — leaving the `uv`/`run`/`--directory` arguments in place would invoke
the Go binary with arguments it does not accept.

## 3. Restart

```bash
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist
```

## 4. Verify

```bash
plutil -extract ProgramArguments.0 raw ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist   # → the Go binary path
launchctl list | grep vault-ui                                                                     # exit code 0
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/                                    # → 200
lsof -p "$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)" -a -d txt | grep -q 'workspaces/go/bin/vault-ui' && echo "running binary: go"
curl -s 'http://127.0.0.1:8000/api/tasks?vault=Personal&status=in_progress' | jq 'length'          # → > 0
ps eww "$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)" | tr ' ' '\n' | grep '^PATH='            # vault-cli dir before homebrew
```

The fourth line is the one that distinguishes a real cutover from a plist edit that
never took effect: it reads the **running** process's executable, not the file on
disk. Before the cutover it names the Python service even when the plist has already
been edited.

If `launchctl list` shows a non-zero status, read `/tmp/vault-ui.log`. A Go binary
invoked with leftover `uv run` arguments, or a `PATH` missing the vault-cli
directory, both fail loudly there.

## 5. Exercise the board

Open `http://127.0.0.1:8000` and drive it: the tasks, goals and topics views render,
a card's actions work, and live updates arrive as vault files change.

For a scripted check, use Playwright MCP through `browser_evaluate` with scoped
selectors. **Do not call `browser_snapshot` or `browser_find` on the board** — the
DOM contract and the reason are in the vault's `[[vault-ui]]` page.

## 6. Regression guard

```bash
cd ~/Documents/workspaces/vault-ui
git diff --stat origin/master -- src/vault_ui/static/
```

Prints nothing. The frontend is frozen; a non-empty result means something touched it.

## Rollback

The Python backend stays in the tree and stays installable for one rollback window.
Repoint `ProgramArguments[0]` back to `~/.local/bin/vault-ui`, then:

```bash
cd ~/Documents/workspaces/vault-ui
uv tool install --force --no-cache .
launchctl kickstart -k gui/$(id -u)/com.github.bborbe.vault-ui
```

`--no-cache` is load-bearing: uv keys its build cache on the version string, so
`--force` alone can reinstall the cached old wheel and still report success. See
`docs/launchd-service.md` § Upgrade flow.

The Python backend remains in-tree until its removal executes — filed as
`specs/ideas/remove-superseded-python-backend.md`.

## Related

- `docs/launchd-service.md` — plist shape, PATH contract, upgrade flow
- `docs/starting-marker-lifecycle.md` — what a restart does to in-flight launches
- `src/vault_ui/README.md` — the superseded marker on the Python backend
