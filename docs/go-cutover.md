# Cut over vault-ui from the Python backend to the Go binary

The final step of the Go rewrite: repoint the launchd LaunchAgent from the
uv-installed Python tool to the Go binary built from this repo. Until this runs,
the board on `http://127.0.0.1:8000` is still served by Python.

**This is an operator-run procedure. No dark-factory prompt performs it.** It stops
and replaces a running service, so it sits deliberately outside the pipeline: spec
`023-go-backend-api-and-cutover.md` declares the cutover operator-gated and keeps
every service restart, plist edit and tool reinstall out of `prompts/`.

Parity is proven before you start — `make parity` compares the Go and Python backends
across all 25 routes, the error shapes, the write path and the WebSocket frames, and
last passed at **`798d901`**. This runbook is about the switch, not about
re-establishing that. If step 1 pulls code that moves the Go backend past `798d901`,
the claim no longer covers what you are deploying — re-run `make parity` (in the
container; see § Known limits).

## What changes

| | Before | After |
|---|---|---|
| `ProgramArguments` | `~/.local/bin/vault-ui` — **or** the five-element `uv run --directory <repo> vault-ui` form | the single Go binary path |
| Backing code | `~/.local/share/uv/tools/vault-ui/` (installed snapshot) | the repo checkout, compiled |
| Upgrade step | `git pull` + `uv tool install --force --no-cache .` | `git pull` + `make build` + restart |

**Which "Before" you have matters for rollback.** `docs/launchd-service.md` § 1
documents the five-element `uv run` form; the uv-tool form is a single element. Read
your plist before you edit it and record the exact array — rollback restores *that*,
not a guess.

The `PATH` contract, `WorkingDirectory`, `KeepAlive`, `RunAtLoad` and the log paths
do not change. `docs/launchd-service.md` documents the plist shape and why the PATH
ordering matters.

## Before you start

**Wait for in-flight launches.** A restart kills the subprocesses vault-ui itself
spawned. If the board shows any `⏳ Starting...` card, let it finish before you
restart — `docs/starting-marker-lifecycle.md` § Set and clear paths explains what a
restart leaves behind (a post-restart orphan whose launching coroutine no longer
exists, cleared by take-over or the TTL sweep).

**Check where your config lives.** The two backends do not resolve `config.yaml` the
same way, and this is the one difference that can leave you with no board at all:

- **Go** is XDG-first, then `~/config.yaml` — it does **not** look in the repo.
- **Python** additionally falls back to the repo root (`<repo>/config.yaml`).

If your Python service has been running off the repo-root file, the Go binary will
not find it and will exit at startup. Check and migrate before cutting over:

```bash
mkdir -p ~/.config/vault-ui
if [ ! -f ~/.config/vault-ui/config.yaml ]; then
  cp ~/Documents/workspaces/vault-ui/config.yaml ~/.config/vault-ui/config.yaml \
    || { echo "no config found in either location — stop and locate it"; exit 1; }
fi
```

If your config already lives at `~/config.yaml`, the Go binary finds it there and this
step is a no-op — that path is the Go-only fallback, not a failure.

## 1. Build the Go binary

```bash
cd ~/Documents/workspaces/vault-ui
git checkout master
git pull
make build
```

**Be on `master` first.** The parity claim above is pinned to a `master` commit and
step 6's guard assumes a `master` checkout — a build taken from a feature branch is
code nothing in this runbook has verified.

`make build` writes to `~/Documents/workspaces/go/bin/vault-ui`. Confirm it is a real,
runnable executable — `file` alone reports the format regardless of the mode bits, so
check the exec bit too:

```bash
file ~/Documents/workspaces/go/bin/vault-ui     # → Mach-O 64-bit executable arm64
test -x ~/Documents/workspaces/go/bin/vault-ui && echo "executable"
```

## 2. Back up the LaunchAgent, then repoint it

Keep the working configuration — a malformed `ProgramArguments` makes
`launchctl bootstrap` fail with the only copy of it gone:

```bash
cp ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist \
   ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist.bak
```

Now edit `~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist` so that
`ProgramArguments` is the single Go binary path:

```xml
<key>ProgramArguments</key>
<array>
    <string>/Users/YOUR_USER/Documents/workspaces/go/bin/vault-ui</string>
</array>
```

The value is written out in full (`/Users/YOUR_USER/…`, substituting your own user)
rather than as `~`: launchd does not tilde-expand `ProgramArguments`, so an absolute
path is required here — the only place in this document where `~` will not do.

Leave `PATH`, `WorkingDirectory`, `KeepAlive`, `RunAtLoad` and the log paths as they
are.

Replace the **whole array**. If your plist currently spells the Python service as
`uv run --directory <repo> vault-ui`, setting only the first element would invoke the
Go binary with `run --directory …` as arguments it does not accept.

## 3. Restart

```bash
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist || true
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist
```

`bootout` reports `Could not find specified service` if the job is not currently
loaded; `|| true` keeps that harmless first-run noise from looking like a failure.

The `bootout`/`bootstrap` pair is what makes launchd **re-read the plist**. A
`launchctl kickstart -k` would restart the job from launchd's in-memory definition and
silently ignore your edit — it is the right tool in `docs/launchd-service.md` § Upgrade
flow, where only the installed tool changes, and the wrong one here, where the plist
itself does.

## 4. Verify

```bash
plutil -extract ProgramArguments.0 raw ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist   # → the Go binary path
launchctl list | grep vault-ui                                                                     # exit code 0
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/                                    # → 200

# Gate: exactly one process must hold :8000. 0 = nothing listening; 2+ = a leftover
# listener is still up. Resolve either before reading the next two probes, which act
# on the first pid only.
lsof -nP -iTCP:8000 -sTCP:LISTEN -t | grep -c .
PID=$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)
lsof -p "$PID" -a -d txt | grep -q 'workspaces/go/bin/vault-ui' && echo "running binary: go"

curl -s 'http://127.0.0.1:8000/api/tasks?vault=private-personal&status=in_progress' | jq 'length'  # → > 0
curl -s 'http://127.0.0.1:8000/api/goals?vault=private-personal' | jq 'length'                     # → > 0
ps eww "$PID" | tr ' ' '\n' | grep '^PATH='                                                        # vault-cli dir before homebrew
```

Four things about these probes:

- **The listener count is the gate.** `1` is what you want. `0` means nothing is
  listening — read `/tmp/vault-ui.log`. `2` or more means a leftover listener is still
  up; resolve that first, because the two probes below take the first pid only and
  would otherwise tell you about whichever process `lsof` happened to list first.
- **`lsof -p … -a -d txt` is the probe that distinguishes a real cutover from a plist
  edit that never took effect.** `plutil` reads the file on disk and reports the Go path
  the moment you save the edit, whether or not the restart worked; this reads the
  *running* process. It either matches and prints `running binary: go`, or prints
  nothing — no output here means the listener is not the Go binary.
- **Both `jq 'length'` calls must return a non-zero count.** A vault that exists always
  has goals and in-progress tasks, so `0` here means the *vault name* is wrong — not that
  the board is empty. List the names the API actually serves with
  `curl -s http://127.0.0.1:8000/api/vaults | jq -r '.[].name'`. Vaults get renamed, and
  a retired name returns `0`, which reads exactly like a healthy empty vault. Spec
  `023-go-backend-api-and-cutover.md` still names `Personal` in this probe; that name no
  longer resolves against this config, so the spec's own annotation cannot pass as
  written.
- **The `PATH` probe prints only the `PATH` line.** The full environment is never
  displayed, and the plist sets nothing else (the Go binary reads one env var,
  `VAULT_UI_LISTEN`).

If `launchctl list` shows a non-zero status, read `/tmp/vault-ui.log`. A Go binary
invoked with leftover `uv run` arguments, a `PATH` missing the vault-cli directory,
and a config file it cannot find all fail loudly there.

## 5. Exercise the board

Open `http://127.0.0.1:8000` and drive it: the tasks, goals and topics views render,
a card's actions work, and live updates arrive as vault files change.

For a scripted check, use Playwright MCP through `browser_evaluate` with scoped
selectors. **Do not call `browser_snapshot` or `browser_find` on the board** — the
board re-renders continuously from live vault updates, so a full-tree snapshot either
times out or returns a tree that is stale before you can act on it. The DOM contract
is in the vault's `[[vault-ui]]` page.

## 6. Regression guard

Run this **from a checkout on `master`** — on a feature branch the diff also contains
that branch's work and reports a false alarm.

```bash
cd ~/Documents/workspaces/vault-ui
git fetch origin
git diff --stat origin/master -- src/vault_ui/static/
```

Prints nothing. The frontend is frozen; a non-empty result means something touched it.
The `git fetch` matters: a stale `origin/master` ref makes the guard report a
difference that is not there.

## Rollback

The Python backend stays in the tree and stays installable for one rollback window.
Restore the `.bak` you took in step 2 — it returns the exact prior invocation,
including the five-element `uv run` form if that is what you had:

```bash
cp ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist.bak \
   ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist

cd ~/Documents/workspaces/vault-ui
uv tool install --force --no-cache .

launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bborbe.vault-ui.plist

# Same shape as step 4, asserting the opposite result.
lsof -nP -iTCP:8000 -sTCP:LISTEN -t | grep -c .                                                    # → 1
PID=$(lsof -nP -iTCP:8000 -sTCP:LISTEN -t | head -1)
lsof -p "$PID" -a -d txt | grep -q 'workspaces/go/bin/vault-ui' \
  && echo "STILL RUNNING THE GO BINARY — the rollback did not take effect" \
  || echo "running binary: python (rollback applied)"
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/                                    # → 200
```

Verify the rollback, do not assume it. A rollback that leaves the Go binary running
while reporting success is exactly the failure the `kickstart -k` variant would have
caused — the check is what tells the two apart.

**The `bootout`/`bootstrap` pair is not interchangeable with `kickstart -k` here.**
`kickstart -k` restarts the job from launchd's in-memory definition without re-reading
the plist, so the restored Python path would be ignored and the Go binary would keep
running — a rollback that reports success and changes nothing.

`--no-cache` is load-bearing: uv keys its build cache on the version string, so
`--force` alone can reinstall the cached old wheel and still report success. See
`docs/launchd-service.md` § Upgrade flow.

If you edited the plist in place instead of restoring the backup, `uv tool install`
alone is **not** sufficient for the `uv run` form — it reinstalls the tool, but the
`uv run --directory` arguments are what make the legacy repo-root config reachable.

The Python backend remains in-tree until its removal executes — filed as
`specs/ideas/remove-superseded-python-backend.md`.

## Known limits

- **`make parity` does not run on the macOS host.** The harness uses GNU
  `date -u -d '-1 hour'`, which BSD `date` rejects, and the repo `.venv` is a
  container-built Linux venv. Run it in the spec's container
  (`docker.io/bborbe/claude-yolo:v0.15.1`, mounting the repo at `/workspace`). This is
  why the parity precondition above is worth re-checking if step 1 pulls new code.

## Related

- `docs/launchd-service.md` — plist shape, PATH contract, upgrade flow
- `docs/starting-marker-lifecycle.md` — what a restart leaves behind
- `src/vault_ui/README.md` — the superseded marker on the Python backend
