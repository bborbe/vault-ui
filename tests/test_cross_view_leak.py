"""Regression tests for spec 014 prompt 1 — fix cross-view leak.

Spec AC#4 requires a regression test that fails against spec 013's code
and passes after the leak fix. The leak was that sidebar interactions
on the Goals view unconditionally called loadTasks(), clobbering the
goal columns with task cards.

The fix migrates every unconditional loadTasks() call site (other than
the one inside loadCurrentView) to loadCurrentView(). The test asserts
that no bare loadTasks() invocation remains outside the dispatcher.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "task_orchestrator" / "static" / "app.js").read_text()


def _slice_outside_function(source: str, fn_name: str) -> str:
    """Return the source with the named function's body removed (replaced
    by an empty block). The dispatcher function is the one allowed to
    call loadTasks directly — every other call site must use loadCurrentView.
    """
    # Match `function NAME(...) { ... }` at the start of a line, greedy body.
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    if not m:
        raise AssertionError(f"function {fn_name} not found in app.js")
    # Walk braces from the opening brace to find the matching close.
    i = m.end()  # position just after the `{`
    depth = 1
    while i < len(source) and depth > 0:
        c = source[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        i += 1
    # Replace body with empty (keep function signature).
    head_end = source.index("{", m.start()) + 1
    return source[:head_end] + source[i:]


def test_no_unconditional_load_tasks_outside_dispatcher() -> None:
    """No call to loadTasks() outside the loadCurrentView dispatcher.

    Spec 013 had bare loadTasks() calls in startPolling, refresh-btn,
    setupUpcomingWindow, handleAllStatusCheckbox, handleStatusCheckboxChange,
    handleAllAssigneeCheckbox, handleAssigneeCheckboxChange, vault-only-btn,
    handleAllVaultCheckbox, handleVaultCheckboxChange, filterByAssignee,
    handleDrop, handleMenuAction, executeSlashCommand, clearTaskSession,
    handleTaskUpdate (task branch), assignToMe. Spec 014 prompt 1
    migrates ALL of these to loadCurrentView().
    """
    stripped = _slice_outside_function(APP_JS, "loadCurrentView")
    # The dispatcher body is removed; only references inside loadCurrentView
    # are excluded. Any remaining "loadTasks()" call site is a regression.
    #
    # Walk line by line to filter out:
    # - Lines starting with `//` (comments that mention loadTasks)
    # - The function declaration `async function loadTasks() {`
    bare_calls = []
    for line in stripped.splitlines():
        stripped_line = line.strip()
        # Skip comment lines.
        if stripped_line.startswith("//"):
            continue
        # Skip the function declaration.
        if "function loadTasks(" in line:
            continue
        # Match the call: loadTasks() possibly preceded by `await`.
        if re.search(r"(?:^|[^a-zA-Z_])loadTasks\s*\(\s*\)", line):
            bare_calls.append(line)
    assert bare_calls == [], (
        f"Found {len(bare_calls)} unconditional loadTasks() call site(s) outside "
        f"the loadCurrentView dispatcher. Migrate each to loadCurrentView() so "
        f"Goals view is not clobbered. First 3 occurrences:\n" + "\n".join(bare_calls[:3])
    )


def test_handle_task_update_does_not_fetch_on_cross_view() -> None:
    """handleTaskUpdate's task branch must early-return when currentView
    is 'goals' (spec AC#3 — DOM hash unchanged on cross-view WS event)."""
    handler_idx = APP_JS.find("function handleTaskUpdate")
    assert handler_idx != -1
    handler_body = APP_JS[handler_idx : handler_idx + 3000]
    # The cross-view guard: when kind === 'task' and currentView === 'goals',
    # the function must return without calling any loader.
    assert "currentView === 'goals'" in handler_body
    # Target the SECOND `if (currentView === 'goals')` — that one is inside
    # the kind === 'task' (else) block and must early-return without fetch.
    # The first occurrence is inside the kind === 'goal' branch (which
    # correctly fetches goals when the user is on goals view).
    all_goals_branches = list(
        re.finditer(
            r"if\s*\(\s*currentView\s*===\s*'goals'\s*\)\s*\{",
            handler_body,
        )
    )
    assert len(all_goals_branches) >= 2, (
        f"Expected at least two `if (currentView === 'goals')` guards in "
        f"handleTaskUpdate (one per kind), found {len(all_goals_branches)}"
    )
    # Use the second one (the kind === 'task' guard).
    goals_branch_match = all_goals_branches[1]
    open_brace_idx = goals_branch_match.end() - 1
    depth = 1
    i = open_brace_idx + 1
    while i < len(handler_body) and depth > 0:
        c = handler_body[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        i += 1
    inner = handler_body[open_brace_idx + 1 : i - 1]
    # No fetch may happen on the goals branch in the task-event path.
    assert "loadTasks" not in inner, "Goals-view branch in handleTaskUpdate must not call loadTasks"
    assert "loadGoals" not in inner, (
        "Goals-view branch must not re-fetch goals either (vault check at top handles that)"
    )
    assert "return" in inner, "Goals-view branch must early-return (spec AC#3 invariant)"


def test_refresh_button_uses_load_current_view() -> None:
    """The #refresh-btn click handler is wired to loadCurrentView, not
    loadTasks (spec AC#1 (d))."""
    setup_idx = APP_JS.find("function setupEventListeners")
    assert setup_idx != -1
    setup_body = APP_JS[setup_idx : APP_JS.find("function ", setup_idx + 1)]
    # The refresh-btn wiring is `addEventListener('click', loadTasks)`.
    # After the fix it is `addEventListener('click', loadCurrentView)`.
    assert "getElementById('refresh-btn')" in setup_body
    # Find the line with refresh-btn and assert loadTasks is NOT the handler.
    refresh_section = re.search(
        r"getElementById\('refresh-btn'\)[^;]*",
        setup_body,
        re.DOTALL,
    )
    assert refresh_section is not None
    assert "loadTasks" not in refresh_section.group(0), (
        "refresh-btn still wired to loadTasks — switch to loadCurrentView"
    )
    assert "loadCurrentView" in refresh_section.group(0)


def test_start_polling_uses_load_current_view() -> None:
    """startPolling() (60s fallback interval) calls loadCurrentView, not
    loadTasks (spec AC#1 covers periodic poll)."""
    poll_idx = APP_JS.find("function startPolling")
    assert poll_idx != -1
    poll_body = APP_JS[poll_idx : poll_idx + 500]
    assert "loadCurrentView" in poll_body
    # No bare loadTasks() in the poll body.
    assert "loadTasks()" not in poll_body, (
        "startPolling still calls loadTasks — periodic poll will clobber Goals view"
    )
