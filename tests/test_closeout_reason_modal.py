"""Static-source regression tests for the close-out reason modal (spec 077).

Only the Abort close-out (card-menu abort_task / abort_goal) prompts for a
free-text reason + optional gate successor; Complete is reason-free — the UI
never prompts on Complete (menu actions and drag into Done/Completed) and the
request bodies carry no close-out fields, matching the abort-only backend
contract. This repo has no JS runtime, so these are the sanctioned in-container
guards: read the static sources via pathlib and assert on strings / brace-walked
function bodies. The modal's interactive behavior is verified by the operator on
the host with `make run`; these tests pin the wiring so a future edit cannot
silently drop the abort reason prompt or silently reintroduce a Complete prompt.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
STATIC_DIR = REPO_ROOT / "src" / "vault_ui" / "static"
APP_JS = (STATIC_DIR / "app.js").read_text()
INDEX_HTML = (STATIC_DIR / "index.html").read_text()


def _function_body(source: str, fn_name: str) -> str:
    """Return the brace-walked body of the named (possibly async) function.

    The body is everything between the opening `{` of the function signature
    and its matching close — no trailing brace, no signature. Raises
    AssertionError if the function is not found.
    """
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    assert m, f"function {fn_name} not found in app.js"
    open_brace = source.index("{", m.start())
    i = open_brace + 1
    depth = 1
    while i < len(source) and depth > 0:
        if source[i] == "{":
            depth += 1
        elif source[i] == "}":
            depth -= 1
        i += 1
    return source[open_brace + 1 : i - 1]


def _if_block(source: str, condition: str) -> str:
    """Return the brace-walked body of the first `if (<condition>) { ... }` block."""
    match = re.search(re.escape(condition) + r"\)\s*\{", source)
    assert match, f"no if-block for {condition!r} found in app.js"
    i = match.end()
    depth = 1
    while i < len(source) and depth > 0:
        if source[i] == "{":
            depth += 1
        elif source[i] == "}":
            depth -= 1
        i += 1
    return source[match.end() : i - 1]


# --- index.html: modal structure + cache-buster tokens ---


def test_reason_modal_markup_present() -> None:
    """index.html contains every reason-modal element id the JS depends on."""
    for element_id in (
        'id="reason-modal"',
        'id="reason-input"',
        'id="gate-successor-input"',
        'id="reason-confirm-btn"',
        'id="reason-cancel-btn"',
    ):
        assert element_id in INDEX_HTML, element_id


def test_modal_reuses_existing_modal_classes() -> None:
    """The reason modal reuses the existing .modal / .modal-content / .modal-buttons classes."""
    reason_modal = INDEX_HTML[INDEX_HTML.index('id="reason-modal"') :]
    assert 'class="modal hidden"' in reason_modal
    assert 'class="modal-content"' in reason_modal
    assert 'class="modal-buttons"' in reason_modal


def test_cache_busters_bumped() -> None:
    """index.html carries a non-empty app.js and style.css token; stale ones are gone.

    Asserts the shape, not the literal token: pinning it makes every legitimate
    cache-bust bump fail, which trains the bump to be skipped — and an un-bumped
    token is how this repo has previously shipped a fix browsers never received.
    """
    assert re.search(r"app\.js\?v=\S+", INDEX_HTML)
    assert re.search(r"style\.css\?v=\S+", INDEX_HTML)
    assert "2026-08-19-board-sort" not in INDEX_HTML
    assert "style.css?v=2026-08-24-closeout-reason" not in INDEX_HTML


# --- app.js: askCloseOut helper ---


def test_ask_close_out_defined() -> None:
    """app.js defines the askCloseOut helper (sync function returning a Promise)."""
    assert re.search(r"(?:async\s+)?function\s+askCloseOut\s*\(", APP_JS)


def test_ask_close_out_contract() -> None:
    """askCloseOut resolves { reason, gate_successor } and disables Confirm while blank."""
    body = _function_body(APP_JS, "askCloseOut")
    assert "reason-modal" in body
    assert "reason-input" in body
    assert "gate-successor-input" in body
    # Confirm is disabled when the reason is empty/whitespace-only, and the
    # confirm handler refuses a blank reason as a guard.
    assert "confirmBtn.disabled = !reasonInput.value.trim()" in body
    assert "if (!reason)" in body
    assert "return; // guard" in body
    # gate_successor defaults to the literal 'none' when the input is blank.
    assert "gateInput.value.trim() || 'none'" in body
    # Cancel resolves null; both buttons hide the modal and detach listeners.
    assert "resolvePromise(null)" in body
    assert "modal.classList.add('hidden')" in body
    assert "removeEventListener" in body


def test_risk_prompt_sentence_present() -> None:
    """The risk-prompt sentence appears verbatim in app.js (set from JS per kind)."""
    assert (
        "Does this task own a trigger, gate, threshold or recurring check? If so, "
        "name where it moves (gate successor), or 'none'." in APP_JS
    )
    assert (
        "Does this goal own a trigger, gate, threshold or recurring check? If so, "
        "name where it moves (gate successor), or 'none'." in APP_JS
    )


# --- app.js: patchStatus carries the close-out fields (abort path) ---


def test_patch_status_builds_close_out_body() -> None:
    """patchStatus adds reason/gate_successor to the request body when closeOut is set."""
    body = _function_body(APP_JS, "patchStatus")
    assert "reason" in body
    assert "gate_successor" in body
    # The body construction: `const body = { status };` then conditional adds.
    assert "const body = { status };" in body
    assert "if (closeOut)" in body
    assert "body.reason = closeOut.reason;" in body
    assert "body.gate_successor = closeOut.gate_successor;" in body


# --- app.js: dispatchMenuAction prompts via askCloseOut for abort only ---


def test_dispatch_menu_action_asks_close_out_only_for_abort() -> None:
    """dispatchMenuAction prompts via askCloseOut for abort only — zero 'complete' verbs."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('task', 'abort')" in body
    assert "askCloseOut('goal', 'abort')" in body
    assert "askCloseOut('task', 'complete')" not in body
    assert "askCloseOut('goal', 'complete')" not in body


def test_dispatch_menu_action_complete_task_reason_free() -> None:
    """complete_task dispatches directly with no close-out prompt or fields."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('task', 'complete')" not in body
    assert "executeSlashCommand(id, action)" in body


def test_dispatch_menu_action_complete_goal_reason_free() -> None:
    """complete_goal posts { command } with no close-out fields on the body."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('goal', 'complete')" not in body
    assert "body.reason" not in body
    assert "body.gate_successor" not in body


def test_dispatch_menu_action_abort_task_gated() -> None:
    """The abort_task branch awaits askCloseOut and returns on cancel."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "const closeOut = await askCloseOut('task', 'abort');" in body
    assert "if (closeOut === null) return;" in body
    assert "patchStatus('task', id, item.vault, 'aborted', 'Task aborted', closeOut)" in body


def test_dispatch_menu_action_abort_goal_gated() -> None:
    """The abort_goal branch awaits askCloseOut and returns on cancel."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "const closeOut = await askCloseOut('goal', 'abort');" in body
    assert "if (closeOut === null) return;" in body
    assert "patchStatus('goal', id, item.vault, 'aborted', 'Goal aborted', closeOut)" in body


def test_dispatch_menu_action_defer_not_gated() -> None:
    """defer_goal / defer_task dispatch without any askCloseOut prompt."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('task', 'complete')" not in body
    assert "askCloseOut('goal', 'complete')" not in body


# --- app.js: handleDrop completes are reason-free (no prompt, no fields) ---


def test_handle_drop_task_done_reason_free() -> None:
    """Task drops into the 'done' column are reason-free — no prompt, no close-out fields."""
    body = _function_body(APP_JS, "handleDrop")
    assert "askCloseOut('task', 'complete')" not in body
    assert "const body = { phase: targetKey };" in body
    assert "body.reason" not in body


def test_handle_drop_goal_completed_reason_free() -> None:
    """Goal drops into the 'completed' column are reason-free — no prompt, no close-out fields."""
    body = _function_body(APP_JS, "handleDrop")
    assert "askCloseOut('goal', 'complete')" not in body
    assert "const body = { status: targetKey };" in body
    assert "body.reason" not in body
