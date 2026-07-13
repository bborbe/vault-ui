"""Regression test: renderColumnHeaders must re-wire drop handlers on every call.

Goal drag-and-drop stops working after a Tasks → Goals → Tasks round-trip.
The Goals view rebuilds its status columns from scratch every time
renderColumnHeaders() is called (in status mode it removes and recreates all
status columns), but drop-target listeners (dragover/drop/dragleave) were only
attached once at page load inside the DOMContentLoaded handler.  After a view
switch the freshly-built columns had no handlers, so goal cards could not be
dragged between status columns.

The fix: renderColumnHeaders() calls setupDragAndDrop() at the END of its
body (outside both the status-mode and phase-mode branches) so every column
rebuild wires drop handlers for both Goals (status columns) and Tasks (phase
columns, which survive across view switches).

This is a pure-static-source audit — no JS runtime, no Playwright.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()


def _slice_function_body(source: str, fn_name: str) -> str:
    """Return the body of the named function (without outer braces)."""
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    if not m:
        raise AssertionError(f"function {fn_name} not found in app.js")
    # Walk from the opening brace to the matching close.
    i = m.end()  # position just after the `{`
    depth = 1
    while i < len(source) and depth > 0:
        c = source[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        i += 1
    # Return just the body (skip the outer braces).
    return source[m.end() : i - 1]


def test_render_column_headers_calls_setup_drag_and_drop_at_top_level() -> None:
    """renderColumnHeaders must call setupDragAndDrop() at depth 0 (not nested
    inside any if/else/loop block), so both status-mode and phase-mode column
    layouts get drop handlers wired on every rebuild.

    Regression: goal drag-drop dies after a Tasks→Goals→Tasks round-trip.
    """
    body = _slice_function_body(APP_JS, "renderColumnHeaders")

    # Walk the body tracking nesting depth.  setupDragAndDrop( must appear at depth 0.
    depth = 0
    found_at_depth_0 = False
    i = 0
    while i < len(body):
        c = body[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
        elif c == "(" and depth == 0:
            # Look back for setupDragAndDrop identifier (the chars just before this '(').
            end = i
            start = end - 17  # len("setupDragAndDrop") == 17
            if start >= 0 and body[start:end].strip() == "setupDragAndDrop":
                found_at_depth_0 = True
                break
        i += 1

    assert found_at_depth_0, (
        "renderColumnHeaders must call setupDragAndDrop() at the end of its "
        "body (outside both the status and phase branches) so every rebuilt "
        "column layout gets drop handlers.  Regression: goal drag-drop dies "
        "after a Tasks→Goals→Tasks round-trip."
    )
