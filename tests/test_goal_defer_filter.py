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


def test_create_goal_card_greys_upcoming() -> None:
    """createGoalCard() adds 'upcoming' CSS class when goal.upcoming is true."""
    with open("src/vault_ui/static/app.js") as f:
        app_js = f.read()

    # Find the createGoalCard function body
    create_goal_match = re.search(
        r"function createGoalCard\(goal\)\s*\{.*?\n\}",
        app_js,
        re.DOTALL,
    )
    assert create_goal_match is not None, "createGoalCard function not found"
    create_goal_body = create_goal_match.group(0)

    # Verify upcoming class is added
    assert "if (goal.upcoming) card.classList.add('upcoming')" in create_goal_body, (
        "createGoalCard must add 'upcoming' class when goal.upcoming is true, "
        "mirroring createTaskCard"
    )
