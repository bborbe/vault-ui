"""Resolve a live Claude session to its WezTerm pane, and activate that pane.

The board can tell that a session is live but cannot take the operator to it:
the fleet-jump server that activates a pane requires a shared credential, and
that credential must never reach the browser. So the jump is proxied in the
server — the server reads the credential, hands it to the jump server, and
never returns it to the caller.

Two host facts this module encodes:

- The credential is a single line in ``~/.claude/secrets/jump-token`` (mode
  ``0600``, owned by the user the service runs as), so reading it needs no
  privilege change.
- Pane resolution belongs to the supervisor's own ``who-needs-me.py``. Its
  resolution order — a recorded pane validated against the live WezTerm list by
  existence *and* title, else the registry's current session name matched
  against pane titles, else refuse — is what stops a recycled pane id from
  handing over a confident link to the wrong tab. Re-implementing that here
  would duplicate the trap, so this module shells out instead.

Every function here returns ``None`` rather than raising for an expected
absence (no token, no pane); only ``perform_jump`` raises, so its caller can
map a jump-server failure to a status code.
"""

import asyncio
import logging
import os
import sys
import urllib.request
from contextlib import suppress
from pathlib import Path
from urllib.parse import quote

logger = logging.getLogger(__name__)

# The fleet-jump server: a loopback process that activates the WezTerm pane it
# is handed. Both bounds are 5s because both peers are local processes — a hang
# means the peer is wedged, and the caller is better served by a refusal than by
# a request that never returns.
_RESOLVE_TIMEOUT_SECONDS = 5.0
_JUMP_TIMEOUT_SECONDS = 5.0

_JUMP_SERVER_URL = "http://127.0.0.1:1337/jump"


def _jump_token_path() -> Path:
    """Path of the shared fleet-jump credential.

    A function, not a module constant, so a test can point it at a ``tmp_path``
    file by monkeypatching it — the same seam ``activity._claude_projects_root``
    provides for the transcript root.
    """
    return Path.home() / ".claude" / "secrets" / "jump-token"


def _who_needs_me_path() -> Path:
    """Path of the supervisor's pane-resolution script.

    ``CLAUDE_PLUGIN_ROOT`` wins when set, so a relocated or vendored plugin is
    found; otherwise the script is looked up in the default marketplace
    install. A function, not a module constant, for the same reason as
    ``_jump_token_path``.
    """
    plugin_root = os.environ.get("CLAUDE_PLUGIN_ROOT")
    if plugin_root:
        return Path(plugin_root) / "scripts" / "who-needs-me.py"
    return (
        Path.home()
        / ".claude"
        / "plugins"
        / "marketplaces"
        / "claude-supervisor"
        / "scripts"
        / "who-needs-me.py"
    )


def read_jump_token(path: Path | None = None) -> str | None:
    """The shared jump credential, stripped, or ``None`` when it is unavailable.

    ``None`` covers every way the credential can be absent — a missing file, one
    this process may not read, or one holding nothing but whitespace. The caller
    maps that to a refusal rather than a crash: an unreadable credential is an
    operator-fixable state, not a bug.

    The value is never logged, not even truncated, and not on the error path —
    only the path and the exception are. A token in a log file is a token that
    has left its ``0600`` file.

    Args:
        path: Credential file. Defaults to ``_jump_token_path()``; the parameter
            exists so tests can point it at a tmp file.
    """
    if path is None:
        path = _jump_token_path()
    try:
        content = path.read_text()
    except OSError as e:
        logger.debug("[PaneResolver] Cannot read jump token %s: %s", path, e)
        return None
    token = content.strip()
    return token or None


async def resolve_pane_id(session_id: str) -> str | None:
    """The WezTerm pane id the given session runs in, or ``None``.

    Delegates to the supervisor's ``who-needs-me.py --pane-for <prefix>`` rather
    than re-implementing its resolution order — see the module docstring. Only
    the first 8 characters of the session id are passed, because that is the
    prefix the script accepts.

    ``sys.executable`` is argv[0] and the script path argv[1], so the script's
    own shebang and import path are never relied on.

    Returns ``None`` for every failure — no session id, a script that cannot be
    executed, a non-zero exit (which is how the script reports "no pane"), an
    empty result, or a script that hangs past ``_RESOLVE_TIMEOUT_SECONDS``. A
    bounded call matters: an unbounded subprocess would hold the HTTP request
    that is waiting on it open indefinitely.
    """
    if not session_id:
        return None

    script = _who_needs_me_path()
    argv = [sys.executable, str(script), "--pane-for", session_id[:8]]
    try:
        proc = await asyncio.create_subprocess_exec(
            *argv,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
    except OSError as e:
        logger.debug("[PaneResolver] Cannot run %s: %s", script, e)
        return None

    try:
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(), timeout=_RESOLVE_TIMEOUT_SECONDS
        )
    except TimeoutError:
        # Kill it: a wedged script must not outlive the request that gave up on
        # it, and the return code of a process nobody waited for is not a fact
        # the caller can use.
        logger.debug("[PaneResolver] %s timed out for session %s", script, session_id)
        proc.kill()
        with suppress(ProcessLookupError):
            await proc.wait()
        return None

    if proc.returncode != 0:
        logger.debug(
            "[PaneResolver] %s exited %s for session %s: %s",
            script,
            proc.returncode,
            session_id,
            stderr.decode(errors="replace").strip(),
        )
        return None

    pane_id = stdout.decode(errors="replace").strip()
    return pane_id or None


async def perform_jump(pane_id: str, token: str) -> None:
    """Ask the fleet-jump server to activate ``pane_id``.

    Both query values are percent-encoded: a token containing ``&`` or ``=``
    would otherwise silently truncate the query and the jump server would see a
    different token than the one on disk.

    ``urllib.request`` from the standard library, not ``httpx`` — ``httpx`` is a
    dev-only dependency in ``pyproject.toml`` and must not become a runtime one.
    Its ``urlopen`` blocks, so the call runs through ``asyncio.to_thread`` to
    keep the event loop free for every other request the board is serving.

    Raises whatever ``urlopen`` raises — an ``HTTPError`` for a non-2xx status,
    a ``URLError``/``TimeoutError`` for an unreachable or wedged server — so the
    caller can map the failure to a status code.
    """
    url = f"{_JUMP_SERVER_URL}?pane={quote(pane_id)}&t={quote(token)}"

    def _blocking_jump() -> None:
        with urllib.request.urlopen(url, timeout=_JUMP_TIMEOUT_SECONDS) as response:
            status = response.status
        if not 200 <= status < 300:
            raise RuntimeError(f"jump server returned HTTP {status}")

    await asyncio.to_thread(_blocking_jump)
