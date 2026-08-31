"""Activity-age resolution: newer of task file mtime and session transcript mtime."""

import os
import signal
from datetime import UTC, datetime, timedelta
from pathlib import Path
from unittest.mock import patch

from vault_ui.activity import (
    LIVE_WINDOW,
    _parse_resume_processes,
    _parse_resume_session_ids,
    classify_session_state,
    compute_activity_date,
    terminate_resumed_session,
    transcript_mtime,
)

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()

SESSION_ID = "e0930886-0843-4ca9-adfa-58819443c032"


def _write_transcript(directory: Path, session_id: str, age: timedelta) -> Path:
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"{session_id}.jsonl"
    path.write_text('{"type":"mode"}\n')
    when = (datetime.now(tz=UTC) - age).timestamp()
    os.utime(path, (when, when))
    return path


def test_transcript_found_in_project_dir(tmp_path: Path) -> None:
    """The vault's own encoded-cwd directory is checked first."""
    project_dir = tmp_path / "projects" / "-Users-someone-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(minutes=3))

    result = transcript_mtime(SESSION_ID, project_dir, tmp_path / "projects")

    assert result is not None
    assert timedelta(minutes=2) < datetime.now(tz=UTC) - result < timedelta(minutes=4)


def test_transcript_found_via_glob_fallback(tmp_path: Path) -> None:
    """A session started from another cwd still resolves — it lives under a
    different project directory, so the whole projects root is scanned."""
    projects_root = tmp_path / "projects"
    vault_dir = projects_root / "-Users-someone-vault"
    vault_dir.mkdir(parents=True)
    _write_transcript(projects_root / "-Users-someone-code-repo", SESSION_ID, timedelta(minutes=5))

    result = transcript_mtime(SESSION_ID, vault_dir, projects_root)

    assert result is not None


def test_transcript_missing_returns_none(tmp_path: Path) -> None:
    """A session that ran elsewhere (cloud/container) has no local transcript."""
    projects_root = tmp_path / "projects"
    projects_root.mkdir()

    assert transcript_mtime(SESSION_ID, projects_root / "nope", projects_root) is None


def test_no_session_id_returns_none(tmp_path: Path) -> None:
    """Human tasks carry no claude_session_id at all."""
    assert transcript_mtime(None, tmp_path, tmp_path) is None
    assert transcript_mtime("", tmp_path, tmp_path) is None


def test_activity_prefers_live_session_over_stale_file(tmp_path: Path) -> None:
    """The false-stale case: file untouched for hours while an agent works."""
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(seconds=30))
    stale_file = datetime.now(tz=UTC) - timedelta(hours=4)

    result = compute_activity_date(stale_file, SESSION_ID, project_dir, projects_root)

    assert result is not None
    assert datetime.now(tz=UTC) - result < timedelta(minutes=1)


def test_activity_prefers_fresh_file_over_dead_session(tmp_path: Path) -> None:
    """The false-active case inverted: session died, but the file was just written."""
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(hours=3))
    fresh_file = datetime.now(tz=UTC) - timedelta(minutes=2)

    result = compute_activity_date(fresh_file, SESSION_ID, project_dir, projects_root)

    assert result is not None
    assert datetime.now(tz=UTC) - result < timedelta(minutes=3)


def test_activity_falls_back_to_file_mtime(tmp_path: Path) -> None:
    """No session id — the file mtime is the only signal, and must not error."""
    projects_root = tmp_path / "projects"
    projects_root.mkdir()
    modified = datetime.now(tz=UTC) - timedelta(days=2)

    result = compute_activity_date(modified, None, projects_root / "-vault", projects_root)

    assert result == modified


def test_activity_accepts_naive_datetime(tmp_path: Path) -> None:
    """vault-cli timestamps may arrive without tzinfo; treat them as UTC."""
    projects_root = tmp_path / "projects"
    projects_root.mkdir()
    naive = datetime.now(tz=UTC).replace(tzinfo=None) - timedelta(hours=1)

    result = compute_activity_date(naive, None, projects_root / "-vault", projects_root)

    assert result is not None
    assert result.tzinfo is not None


def test_activity_none_when_no_signal(tmp_path: Path) -> None:
    projects_root = tmp_path / "projects"
    projects_root.mkdir()

    assert compute_activity_date(None, None, projects_root / "-vault", projects_root) is None


def test_goal_parse_carries_modified_date() -> None:
    """Goals need a file mtime to fall back on, same as tasks."""
    from vault_ui.vault_cli_client import VaultCLIClient

    client = VaultCLIClient("vault-cli", "personal")
    goal = client._parse_goal(
        {"name": "G1", "title": "Ship It", "modified_date": "2026-08-09T08:58:32Z"}
    )

    assert goal.modified_date is not None
    assert goal.modified_date.year == 2026


def test_goal_parse_tolerates_missing_modified_date() -> None:
    from vault_ui.vault_cli_client import VaultCLIClient

    client = VaultCLIClient("vault-cli", "personal")

    assert client._parse_goal({"name": "G1", "title": "Ship It"}).modified_date is None


def test_formatter_uses_single_largest_unit() -> None:
    """Static assertion: the JS formatter covers every unit with no decimals."""
    start = APP_JS.find("function formatActivityAge")
    assert start != -1, "formatActivityAge not found in app.js"
    body = APP_JS[start : start + 900]

    for token in ("'<1m'", "}m`", "}h`", "}d`", "}w`"):
        assert token in body, f"missing unit in formatActivityAge: {token}"


def test_task_card_renders_activity_age() -> None:
    """The badge is wired into the task card footer and carries a hover timestamp."""
    assert "function activityAgeHtml" in APP_JS
    assert "activityAgeHtml(task.activity_date)" in APP_JS
    assert 'class="activity-age"' in APP_JS
    assert "Last activity:" in APP_JS


def test_goal_card_renders_activity_age() -> None:
    """Goal cards carry the same badge — staleness matters there too."""
    assert "activityAgeHtml(goal.activity_date)" in APP_JS


def test_activity_age_styled_small_and_grey() -> None:
    start = STYLE_CSS.find(".activity-age")
    assert start != -1, ".activity-age rule not found in style.css"
    rule = STYLE_CSS[start : start + 300]

    assert "font-size: 0.7rem" in rule
    assert "#64748b" in rule


# --- session_state classification ---


def test_classify_none_when_no_session_id(tmp_path: Path) -> None:
    projects_root = tmp_path / "projects"
    projects_root.mkdir()

    assert classify_session_state(None, projects_root / "-vault", projects_root) is None
    assert classify_session_state("", projects_root / "-vault", projects_root) is None


def test_classify_live_within_window(tmp_path: Path) -> None:
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(seconds=30))

    assert classify_session_state(SESSION_ID, project_dir, projects_root) == "live"


def test_classify_quiet_older_than_window(tmp_path: Path) -> None:
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(hours=3))

    # Hermetic: no running process (injected empty set), so the stale transcript
    # reads quiet rather than hitting the real ps scan.
    assert (
        classify_session_state(SESSION_ID, project_dir, projects_root, resume_session_ids=set())
        == "quiet"
    )


def test_classify_boundary_exactly_live_window(tmp_path: Path) -> None:
    """A transcript exactly LIVE_WINDOW old is still live; just past it is quiet."""
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    path = _write_transcript(project_dir, SESSION_ID, timedelta(hours=1))
    mtime = datetime.fromtimestamp(path.stat().st_mtime, tz=UTC)

    assert (
        classify_session_state(SESSION_ID, project_dir, projects_root, now=mtime + LIVE_WINDOW)
        == "live"
    )
    assert (
        classify_session_state(
            SESSION_ID,
            project_dir,
            projects_root,
            now=mtime + LIVE_WINDOW + timedelta(seconds=1),
            resume_session_ids=set(),
        )
        == "quiet"
    )


def test_classify_indeterminate_when_no_transcript(tmp_path: Path) -> None:
    projects_root = tmp_path / "projects"
    projects_root.mkdir()

    assert (
        classify_session_state(SESSION_ID, projects_root / "-vault", projects_root)
        == "indeterminate"
    )


# --- ps --resume cross-check ---


def test_parse_resume_session_ids_extracts_exact_resume_match() -> None:
    ps = (
        "  PID TTY STAT TIME COMMAND\n"
        '13862 ?? S 0:00.01 claude --settings {"theme":"x"} --model claude-opus-5[1m] '
        "--resume 7cbde4f8-239c-4f3d-92d7-1e550b0afa88 /vault-cli:work-on-task foo\n"
        " 94284 ?? S 0:00.02 claude --settings {} --model deepseek-v4-flash-max[1m] "
        "--resume c20647e6-ef96-47b8-866b-220f8dca685d\n"
        " 23478 ?? S 0:00.03 some other process --resume a55b44d0-cc04-4740-a5d9-df0a3e462cf4\n"
        " 28430 ?? S 0:00.04 claude --settings {} --print -p 'no resume here'\n"
    )
    assert _parse_resume_session_ids(ps) == {
        "7cbde4f8-239c-4f3d-92d7-1e550b0afa88",
        "c20647e6-ef96-47b8-866b-220f8dca685d",
    }


def test_parse_resume_session_ids_ignores_non_claude_and_prints() -> None:
    ps = (
        " 94282 bash cc-personal --resume c20647e6-ef96-47b8-866b-220f8dca685d\n"
        " 94284 claude --model claude-opus-5[1m] --print -p hi\n"
        " 40075 claude --settings {} --model deepseek[1m] --resume "
        "5df6f0a9-927d-4a99-84f8-ce9ff2350ec5\n"
    )
    # Non-claude `--resume` (the launcher wrapper) and `--print` (headless, no
    # resume) are not provable liveness — only the exact claude --model --resume
    # process counts, exactly like fleet-sessions.py.
    assert _parse_resume_session_ids(ps) == {"5df6f0a9-927d-4a99-84f8-ce9ff2350ec5"}


def test_classify_open_but_idle_session_stays_live(tmp_path: Path) -> None:
    """A stale transcript with a live `--resume` process is still live — the
    open-but-idle session (e.g. cc-personal launcher) the wall must not offer
    Resume on."""
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(hours=3))

    assert (
        classify_session_state(
            SESSION_ID,
            project_dir,
            projects_root,
            resume_session_ids={SESSION_ID},
        )
        == "live"
    )


def test_classify_stale_transcript_without_process_is_quiet(tmp_path: Path) -> None:
    """No process match → the stale transcript reads quiet, so Resume returns
    (flock is free — process death released it)."""
    projects_root = tmp_path / "projects"
    project_dir = projects_root / "-vault"
    _write_transcript(project_dir, SESSION_ID, timedelta(hours=3))

    assert (
        classify_session_state(
            SESSION_ID,
            project_dir,
            projects_root,
            resume_session_ids={"some-other-session"},
        )
        == "quiet"
    )


def test_classify_liveness_is_transcript_only(tmp_path: Path) -> None:
    """A fresh task file must NOT make a session live — liveness is transcript-only."""
    projects_root = tmp_path / "projects"
    projects_root.mkdir()
    modified = datetime.now(tz=UTC) - timedelta(seconds=10)

    # compute_activity_date would report <1m via the file mtime, but the session
    # itself has no transcript: the card must read indeterminate, not live.
    result = compute_activity_date(modified, SESSION_ID, projects_root / "-vault", projects_root)
    assert result is not None

    state = classify_session_state(SESSION_ID, projects_root / "-vault", projects_root)
    assert state == "indeterminate"


def test_session_button_gates_on_session_state() -> None:
    """Static assertion: the shared button hides Resume on 'live' and disables
    it on 'indeterminate' — the two states SC1/SC4 require."""
    start = APP_JS.find("function sessionButtonHtml")
    assert start != -1, "sessionButtonHtml not found in app.js"
    body = APP_JS[start : start + 2000]

    assert "item.session_state === 'live'" in body
    assert 'class="live-badge"' in body
    assert "item.session_state === 'indeterminate'" in body
    assert "resume-btn indeterminate" in body


def test_live_badge_styled(tmp_path: Path) -> None:
    start = STYLE_CSS.find(".live-badge")
    assert start != -1, ".live-badge rule not found in style.css"
    rule = STYLE_CSS[start : start + 400]

    assert "border-radius: 999px" in rule
    assert "#15803d" in rule  # green-700 background


def test_indeterminate_resume_styled(tmp_path: Path) -> None:
    assert ".resume-btn.indeterminate" in STYLE_CSS
    assert "cursor: not-allowed" in STYLE_CSS


# --- ps --resume process termination (take-over path) ---


def test_parse_resume_processes_maps_session_id_to_pid() -> None:
    """``ps -o pid=,args=`` output maps session id → pid for claude --resume matches."""
    ps = (
        " 12345 claude --settings {} --model claude-opus-5[1m] --resume "
        "7cbde4f8-239c-4f3d-92d7-1e550b0afa88 /vault-cli:work-on-task foo\n"
        " 67890 claude --settings {} --model deepseek-v4-flash-max[1m] --resume "
        "c20647e6-ef96-47b8-866b-220f8dca685d\n"
        " 23478 bash cc-personal --resume a55b44d0-cc04-4740-a5d9-df0a3e462cf4\n"
        " 28430 claude --settings {} --print -p 'no resume here'\n"
    )
    assert _parse_resume_processes(ps) == {
        "7cbde4f8-239c-4f3d-92d7-1e550b0afa88": 12345,
        "c20647e6-ef96-47b8-866b-220f8dca685d": 67890,
    }


def test_parse_resume_processes_skips_non_numeric_pid() -> None:
    """A malformed ps line (no numeric pid prefix) is skipped, not fatal."""
    ps = (
        "claude --settings {} --model claude-opus-5[1m] --resume "
        "7cbde4f8-239c-4f3d-92d7-1e550b0afa88\n"
    )
    assert _parse_resume_processes(ps) == {}


def test_terminate_resumed_session_kills_matching_process() -> None:
    """A matched pid receives SIGTERM; returns True."""
    with (
        patch("vault_ui.activity._current_resume_processes") as procs,
        patch("vault_ui.activity.os.kill") as kill,
    ):
        procs.return_value = {SESSION_ID: 4242}
        assert terminate_resumed_session(SESSION_ID) is True
        kill.assert_called_once_with(4242, signal.SIGTERM)


def test_terminate_resumed_session_no_match_returns_false() -> None:
    """No matching --resume process → no kill, returns False."""
    with (
        patch("vault_ui.activity._current_resume_processes") as procs,
        patch("vault_ui.activity.os.kill") as kill,
    ):
        procs.return_value = {"some-other-session": 4242}
        assert terminate_resumed_session(SESSION_ID) is False
        kill.assert_not_called()


def test_terminate_resumed_session_swallows_oserror() -> None:
    """SIGTERM failure (OSError) is logged and reported as False, not raised."""
    with (
        patch("vault_ui.activity._current_resume_processes") as procs,
        patch("vault_ui.activity.os.kill", side_effect=OSError("nope")) as kill,
    ):
        procs.return_value = {SESSION_ID: 4242}
        assert terminate_resumed_session(SESSION_ID) is False
        kill.assert_called_once_with(4242, signal.SIGTERM)


def test_take_over_badge_wired_into_live_branch() -> None:
    """Static assertion: the live branch renders the take-over affordance on the
    badge itself (discreet — no separate button), and the quiet branch keeps
    Resume — SC1/SC5 from the task."""
    start = APP_JS.find("function sessionButtonHtml")
    assert start != -1, "sessionButtonHtml not found in app.js"
    body = APP_JS[start : start + 2200]

    assert 'class="live-badge"' in body
    assert 'onclick="takeOverSession' in body
    assert "take-over-btn" not in body  # button removed — badge is the affordance
    # quiet (hasSession, not indeterminate) still gets the normal Resume button
    assert "buttonLabel = '▶ Resume'" in body


def test_take_over_modal_markup_present() -> None:
    """index.html carries every take-over modal element id the JS depends on."""
    from vault_ui import activity as _a  # noqa: F401  (module import sanity)

    INDEX_HTML = (
        Path(__file__).resolve().parent.parent / "src" / "vault_ui" / "static" / "index.html"
    ).read_text()
    for element_id in (
        'id="takeover-modal"',
        'id="takeover-title"',
        'id="takeover-prompt"',
        'id="takeover-confirm-btn"',
        'id="takeover-cancel-btn"',
    ):
        assert element_id in INDEX_HTML, element_id


def test_take_over_badge_styled() -> None:
    assert ".live-badge" in STYLE_CSS
    assert "cursor: pointer" in STYLE_CSS
    # keyboard-focusable (role=button, tabindex=0) → needs a visible focus ring
    assert ".live-badge:focus-visible" in STYLE_CSS
