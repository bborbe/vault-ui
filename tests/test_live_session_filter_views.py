"""Static string-slice tests for the live-session filter on the Goals and Topics views.

Each loader is sliced out of app.js and asserted on directly. A file-wide grep
would pass on the unmodified file, which already carries several other
`session_live` occurrences.
"""

import re


def _slice_loader(app_js: str, name: str) -> str:
    match = re.search(rf"async function {name}\(\)\s*\{{.*?\n\}}", app_js, re.DOTALL)
    assert match is not None, f"{name} function not found"
    return match.group(0)


def test_load_goals_sends_session_live() -> None:
    """loadGoals() sends session_live in the API request, mirroring loadTasks()."""
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    body = _slice_loader(app_js, "loadGoals")
    assert "params.set('session_live'" in body, (
        "loadGoals must set session_live on params when currentLiveOnly is set, "
        "mirroring the task fetch"
    )


def test_load_topics_sends_session_live() -> None:
    """loadTopics() sends session_live in the API request, mirroring loadTasks()."""
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    body = _slice_loader(app_js, "loadTopics")
    assert "params.set('session_live'" in body, (
        "loadTopics must set session_live on params when currentLiveOnly is set, "
        "mirroring the task fetch"
    )
