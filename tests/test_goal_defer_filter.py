"""Static string-slice tests for goal defer filter frontend wiring."""

import re


def test_load_goals_sends_upcoming_hours() -> None:
    """loadGoals() sends upcoming_hours in the API request, mirroring loadTasks()."""
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    # Find the loadGoals function body
    load_goals_match = re.search(
        r"async function loadGoals\(\)\s*\{.*?\n\}",
        app_js,
        re.DOTALL,
    )
    assert load_goals_match is not None, "loadGoals function not found"
    load_goals_body = load_goals_match.group(0)

    # Verify upcoming_hours is set on params
    assert "params.set('upcoming_hours'" in load_goals_body, (
        "loadGoals must set upcoming_hours on params, "
        "e.g. params.set('upcoming_hours', String(upcomingHours))"
    )


def _slice_function_body(source: str, fn_name: str) -> str:
    """Return the body of the named function (without outer braces)."""
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    assert m is not None, f"function {fn_name} not found"
    i = m.end()
    depth = 1
    while i < len(source) and depth > 0:
        c = source[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        i += 1
    return source[m.end() : i - 1]


def test_create_goal_card_greys_upcoming() -> None:
    """The goal mapper carries goal.upcoming and the shared builder adds the
    'upcoming' CSS class from it."""
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    mapper_body = _slice_function_body(app_js, "mapGoalCard")
    assert "goal.upcoming" in mapper_body, (
        "mapGoalCard must carry goal.upcoming into the view-model"
    )

    builder_body = _slice_function_body(app_js, "createCard")
    assert "if (view.upcoming) card.classList.add('upcoming')" in builder_body, (
        "createCard must add 'upcoming' class when view.upcoming is true, mirroring the task card"
    )


def test_load_goals_buckets_upcoming_to_bottom() -> None:
    """The shared renderer draws active goals first, then upcoming, then hold —
    so deferred (upcoming) goals sink to the bottom of their column, mirroring
    the task bucketing."""
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    body = _slice_function_body(app_js, "renderBoard")
    assert "const holdItems = rest.filter(item => item.status === 'hold')" in body
    assert (
        "const upcomingItems = rest.filter(item => item.status !== 'hold' && item.upcoming)" in body
    )
    assert (
        "const activeItems = rest.filter(item => item.status !== 'hold' && !item.upcoming)" in body
    )
    assert "[...activeItems, ...upcomingItems, ...holdItems].forEach(" in body
