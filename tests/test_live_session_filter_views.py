"""Static string-slice tests for the live-session filter on the Goals and Topics views.

Each loader is sliced out of app.js and asserted on directly. A file-wide grep
would pass on the unmodified file, which already carries several other
`session_live` occurrences.
"""

import re

# The filter must be guarded. An unconditional `params.set` would pin the board
# to always-filtered, so unticking the box would be a no-op — the same shape of
# defect as never sending the parameter at all.
_GUARDED_SET = re.compile(
    r"if \(currentLiveOnly\) \{\s*params\.set\('session_live', 'true'\);\s*\}"
)


def _slice_loader(app_js: str, name: str) -> str:
    match = re.search(rf"async function {name}\(\)\s*\{{.*?\n\}}", app_js, re.DOTALL)
    assert match is not None, f"{name} function not found"
    return match.group(0)


def _assert_guarded_filter(name: str) -> None:
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    body = _slice_loader(app_js, name)
    assert _GUARDED_SET.search(body), (
        f"{name} must set session_live inside an `if (currentLiveOnly)` guard, "
        "mirroring the task fetch — an unguarded call would filter the board "
        "permanently and break unticking"
    )


def test_load_goals_sends_session_live() -> None:
    """loadGoals() sends session_live inside the currentLiveOnly guard."""
    _assert_guarded_filter("loadGoals")


def test_load_topics_sends_session_live() -> None:
    """loadTopics() sends session_live inside the currentLiveOnly guard."""
    _assert_guarded_filter("loadTopics")
