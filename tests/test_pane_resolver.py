"""Tests for the pane_resolver module.

Every test is hermetic: the token path, the supervisor script path and the
subprocess call are all pointed at tmp paths or replaced with fakes, so nothing
here reads the real ``~/.claude/``, spawns ``who-needs-me.py``, or reaches the
fleet-jump server.
"""

import asyncio
import os
import sys
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlsplit

import pytest

from vault_ui import pane_resolver
from vault_ui.pane_resolver import (
    _jump_token_path,
    _subprocess_env,
    _wezterm_bin_dir,
    _who_needs_me_path,
    perform_jump,
    read_jump_token,
    resolve_pane_id,
)

# ---------------------------------------------------------------------------
# read_jump_token
# ---------------------------------------------------------------------------


def test_read_jump_token_returns_stripped_value(tmp_path: Path) -> None:
    path = tmp_path / "jump-token"
    path.write_text("  s3cr3t-token\n")
    assert read_jump_token(path) == "s3cr3t-token"


def test_read_jump_token_missing_path_returns_none(tmp_path: Path) -> None:
    assert read_jump_token(tmp_path / "does-not-exist") is None


def test_read_jump_token_whitespace_only_returns_none(tmp_path: Path) -> None:
    path = tmp_path / "jump-token"
    path.write_text("   \n\t\n")
    assert read_jump_token(path) is None


def test_read_jump_token_defaults_to_home_secrets_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The no-argument call reads the documented host location."""
    path = tmp_path / "jump-token"
    path.write_text("token\n")
    monkeypatch.setattr("vault_ui.pane_resolver._jump_token_path", lambda: path)
    assert read_jump_token() == "token"


def test_read_jump_token_never_logs_the_value(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    """Neither the success path nor the error path may emit the token."""
    good = tmp_path / "jump-token"
    good.write_text("super-secret-value\n")

    with caplog.at_level("DEBUG", logger="vault_ui.pane_resolver"):
        assert read_jump_token(good) == "super-secret-value"
        assert read_jump_token(tmp_path / "missing") is None

    assert "super-secret-value" not in caplog.text


# ---------------------------------------------------------------------------
# path helpers
# ---------------------------------------------------------------------------


def test_jump_token_path_is_under_home_secrets() -> None:
    path = _jump_token_path()
    assert path == Path.home() / ".claude" / "secrets" / "jump-token"


def test_who_needs_me_path_prefers_plugin_root(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("CLAUDE_PLUGIN_ROOT", "/opt/supervisor")
    assert _who_needs_me_path() == Path("/opt/supervisor/scripts/who-needs-me.py")


def test_who_needs_me_path_falls_back_to_marketplace(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.delenv("CLAUDE_PLUGIN_ROOT", raising=False)
    assert _who_needs_me_path() == (
        Path.home()
        / ".claude"
        / "plugins"
        / "marketplaces"
        / "claude-supervisor"
        / "scripts"
        / "who-needs-me.py"
    )


def test_wezterm_bin_dir_returns_bundle_when_binary_present(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The directory is returned only when ``wezterm`` exists inside it."""
    (tmp_path / "wezterm").write_text("")
    monkeypatch.setattr("vault_ui.pane_resolver._WEZTERM_BUNDLE_DIR", tmp_path)
    assert _wezterm_bin_dir() == tmp_path


def test_wezterm_bin_dir_returns_none_when_binary_absent(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """An empty bundle directory — or a machine without WezTerm — is ``None``."""
    monkeypatch.setattr("vault_ui.pane_resolver._WEZTERM_BUNDLE_DIR", tmp_path)
    assert _wezterm_bin_dir() is None


# ---------------------------------------------------------------------------
# _subprocess_env
# ---------------------------------------------------------------------------


def test_subprocess_env_prepends_bundle_and_preserves_rest_of_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr("vault_ui.pane_resolver._wezterm_bin_dir", lambda: tmp_path)
    monkeypatch.setenv("PATH", "/usr/bin:/bin")

    env = _subprocess_env()

    assert env["PATH"] == f"{tmp_path}{os.pathsep}/usr/bin:/bin"
    assert env["PATH"].split(os.pathsep)[1:] == ["/usr/bin", "/bin"]


def test_subprocess_env_is_unchanged_when_bundle_absent(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr("vault_ui.pane_resolver._wezterm_bin_dir", lambda: None)
    monkeypatch.setenv("PATH", "/usr/bin:/bin")

    env = _subprocess_env()

    assert env == dict(os.environ)
    assert env["PATH"] == os.environ["PATH"]


def test_subprocess_env_never_mutates_os_environ(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Both outcomes leave the process environment untouched."""
    monkeypatch.setenv("PATH", "/sentinel/bin")

    monkeypatch.setattr("vault_ui.pane_resolver._wezterm_bin_dir", lambda: tmp_path)
    _subprocess_env()
    assert os.environ["PATH"] == "/sentinel/bin"

    monkeypatch.setattr("vault_ui.pane_resolver._wezterm_bin_dir", lambda: None)
    _subprocess_env()
    assert os.environ["PATH"] == "/sentinel/bin"


# ---------------------------------------------------------------------------
# resolve_pane_id
# ---------------------------------------------------------------------------


class _FakeProc:
    """Stand-in for an ``asyncio`` subprocess with fixed output."""

    def __init__(self, stdout: bytes = b"", stderr: bytes = b"", returncode: int = 0) -> None:
        self._stdout = stdout
        self._stderr = stderr
        self.returncode: int | None = returncode
        self.killed = False

    async def communicate(self) -> tuple[bytes, bytes]:
        return self._stdout, self._stderr

    def kill(self) -> None:
        self.killed = True
        self.returncode = -9

    async def wait(self) -> int:
        return self.returncode or 0


def _patch_subprocess(
    monkeypatch: pytest.MonkeyPatch,
    proc: _FakeProc,
    kwargs_record: list[dict[str, Any]] | None = None,
) -> list[list[str]]:
    """Replace ``create_subprocess_exec`` and record every argv it is handed.

    Pass ``kwargs_record`` to also capture the keyword arguments (notably
    ``env``) each spawn is given. The return value stays argv-only so the
    existing exact-argv assertion is unaffected.
    """
    recorded: list[list[str]] = []

    async def _fake(*argv: str, **kwargs: Any) -> _FakeProc:
        recorded.append(list(argv))
        if kwargs_record is not None:
            kwargs_record.append(kwargs)
        return proc

    monkeypatch.setattr("vault_ui.pane_resolver.asyncio.create_subprocess_exec", _fake)
    return recorded


async def test_resolve_pane_id_empty_session_never_spawns(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """An empty session id returns None without shelling out.

    The subprocess call is patched to raise, so a regression that spawns on an
    empty id fails loudly here rather than silently succeeding.
    """

    async def _explode(*_argv: str, **_kwargs: Any) -> None:
        raise AssertionError("resolve_pane_id must not spawn for an empty session id")

    monkeypatch.setattr("vault_ui.pane_resolver.asyncio.create_subprocess_exec", _explode)

    assert await resolve_pane_id("") is None


async def test_resolve_pane_id_returns_stripped_pane_on_success(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _patch_subprocess(monkeypatch, _FakeProc(stdout=b"  42\n"))
    assert await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032") == "42"


async def test_resolve_pane_id_nonzero_exit_returns_none(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _patch_subprocess(monkeypatch, _FakeProc(stderr=b"no pane\n", returncode=1))
    assert await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032") is None


async def test_resolve_pane_id_empty_stdout_returns_none(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Exit 0 with nothing on stdout is an absence, not a pane id."""
    _patch_subprocess(monkeypatch, _FakeProc(stdout=b"  \n"))
    assert await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032") is None


async def test_resolve_pane_id_missing_script_returns_none(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    async def _missing(*_argv: str, **_kwargs: Any) -> None:
        raise FileNotFoundError("no such file")

    monkeypatch.setattr("vault_ui.pane_resolver.asyncio.create_subprocess_exec", _missing)
    assert await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032") is None


async def test_resolve_pane_id_timeout_returns_none_and_kills(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A wedged script is refused, not waited on forever."""
    proc = _FakeProc(stdout=b"42\n")

    async def _hang() -> tuple[bytes, bytes]:
        await asyncio.sleep(30)
        raise AssertionError("unreachable")

    monkeypatch.setattr(proc, "communicate", _hang)
    _patch_subprocess(monkeypatch, proc)
    monkeypatch.setattr("vault_ui.pane_resolver._RESOLVE_TIMEOUT_SECONDS", 0.01)

    assert await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032") is None
    assert proc.killed is True


async def test_resolve_pane_id_argv_is_exact(monkeypatch: pytest.MonkeyPatch) -> None:
    """argv is [sys.executable, <script>, "--pane-for", <first 8 chars>].

    This is the one boundary whose failure is silent — a wrong flag name, or the
    whole session id where the script expects the 8-character prefix, exits
    non-zero at run time with nothing else to catch it. The id below is longer
    than 8 characters so forwarding the whole id is distinguishable.
    """
    script = Path("/opt/supervisor/scripts/who-needs-me.py")
    monkeypatch.setattr("vault_ui.pane_resolver._who_needs_me_path", lambda: script)
    recorded = _patch_subprocess(monkeypatch, _FakeProc(stdout=b"7\n"))

    session_id = "e0930886-0843-4ca9-adfa-58819443c032"
    await resolve_pane_id(session_id)

    assert recorded == [[sys.executable, str(script), "--pane-for", "e0930886"]]


async def test_resolve_pane_id_spawn_env_prepends_wezterm_dir(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The spawned helper's PATH starts at the WezTerm bundle.

    Captured from the spawn's kwargs, not from ``_subprocess_env`` directly: a
    correct helper whose result never reaches ``create_subprocess_exec`` is
    exactly the silent regression this guards against.
    """
    (tmp_path / "wezterm").write_text("")
    monkeypatch.setattr("vault_ui.pane_resolver._wezterm_bin_dir", lambda: tmp_path)
    monkeypatch.setenv("PATH", "/usr/bin:/bin")
    kwargs_record: list[dict[str, Any]] = []
    _patch_subprocess(monkeypatch, _FakeProc(stdout=b"7\n"), kwargs_record)

    await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032")

    assert len(kwargs_record) == 1
    assert kwargs_record[0]["env"]["PATH"] == f"{tmp_path}{os.pathsep}/usr/bin:/bin"
    assert os.environ["PATH"] == "/usr/bin:/bin"


async def test_resolve_pane_id_spawn_env_unchanged_without_wezterm(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """With no bundle present the helper inherits the environment verbatim."""
    monkeypatch.setattr("vault_ui.pane_resolver._wezterm_bin_dir", lambda: None)
    monkeypatch.setenv("PATH", "/usr/bin:/bin")
    kwargs_record: list[dict[str, Any]] = []
    _patch_subprocess(monkeypatch, _FakeProc(stdout=b"7\n"), kwargs_record)

    await resolve_pane_id("e0930886-0843-4ca9-adfa-58819443c032")

    assert kwargs_record[0]["env"] == dict(os.environ)


# ---------------------------------------------------------------------------
# perform_jump
# ---------------------------------------------------------------------------


async def test_perform_jump_percent_encodes_query_values(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A token holding '&' or '=' must survive the query string intact.

    Driven through a monkeypatched ``urllib.request.urlopen`` that records the
    URL it was handed; the recorded query is decoded back and compared with the
    originals, which is what a raw '&' reaching the server would break.
    """
    recorded: list[str] = []

    class _FakeResponse:
        status = 200

        def __enter__(self) -> "_FakeResponse":
            return self

        def __exit__(self, *_exc: object) -> None:
            return None

    def _fake_urlopen(url: str, timeout: float | None = None) -> _FakeResponse:
        recorded.append(url)
        return _FakeResponse()

    monkeypatch.setattr("urllib.request.urlopen", _fake_urlopen)

    pane_id = "w1:p7"
    token = "a&b=c d/e?f"
    await perform_jump(pane_id, token)

    assert len(recorded) == 1
    query = parse_qs(urlsplit(recorded[0]).query)
    assert query["pane"] == [pane_id]
    assert query["t"] == [token]


async def test_perform_jump_raises_on_non_2xx(monkeypatch: pytest.MonkeyPatch) -> None:
    """A refusal from the jump server reaches the caller as an exception."""

    class _FakeResponse:
        status = 500

        def __enter__(self) -> "_FakeResponse":
            return self

        def __exit__(self, *_exc: object) -> None:
            return None

    monkeypatch.setattr("urllib.request.urlopen", lambda url, timeout=None: _FakeResponse())

    with pytest.raises(RuntimeError):
        await perform_jump("42", "token")


async def test_perform_jump_uses_standard_library_not_a_new_dependency() -> None:
    """The runtime HTTP path is ``urllib.request`` — httpx is dev-only."""
    assert pane_resolver.urllib.request is not None
