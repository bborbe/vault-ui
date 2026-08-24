"""Static-source regression tests for the close-out reason modal (spec 077).

Every close-out write (Abort/Complete via the card menu, drag-to-Done) must
prompt for a free-text reason + optional gate successor before sending the
request. This repo has no JS runtime, so these are the sanctioned in-container
guards: read the static sources via pathlib and assert on strings / brace-walked
function bodies. The modal's interactive behavior is verified by the operator on
the host with `make run`; these tests pin the wiring so a future edit cannot
silently drop the reason prompt.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
STATIC_DIR = REPO_ROOT / "src" / "vault_ui" / "static"
APP_JS = (STATIC_DIR / "app.js").read_text()
INDEX_HTML = (STATIC_DIR / "index.html").read_text()

NEW_TOKEN = "2026-08-24-closeout-reason"


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
    """index.html loads app.js and style.css with the NEW close-out-reason token."""
    assert f"app.js?v={NEW_TOKEN}" in INDEX_HTML
    assert f"style.css?v={NEW_TOKEN}" in INDEX_HTML
    assert "2026-08-19-board-sort" not in INDEX_HTML


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


# --- app.js: patchStatus carries the close-out fields ---


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


# --- app.js: dispatchMenuAction gates every close-out branch on a non-null result ---


def test_dispatch_menu_action_calls_ask_close_out() -> None:
    """dispatchMenuAction prompts via askCloseOut for every close-out action."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "askCloseOut('goal', 'complete')" in body
    assert "askCloseOut('goal', 'abort')" in body
    assert "askCloseOut('task', 'complete')" in body
    assert "askCloseOut('task', 'abort')" in body


def test_dispatch_menu_action_complete_task_gated() -> None:
    """The complete_task branch awaits askCloseOut and returns on cancel."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    # complete_task is dispatched through executeSlashCommand(id, action, closeOut).
    assert "action === 'complete_task' ? await askCloseOut('task', 'complete') : null" in body
    assert "if (action === 'complete_task' && closeOut === null) return;" in body
    assert "executeSlashCommand(id, action, closeOut)" in body


def test_dispatch_menu_action_abort_task_gated() -> None:
    """The abort_task branch awaits askCloseOut and returns on cancel."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "const closeOut = await askCloseOut('task', 'abort');" in body
    assert "if (closeOut === null) return;" in body
    assert "patchStatus('task', id, item.vault, 'aborted', 'Task aborted', closeOut)" in body


def test_dispatch_menu_action_complete_goal_gated() -> None:
    """The complete_goal branch awaits askCloseOut and returns on cancel."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "action === 'complete_goal' ? await askCloseOut('goal', 'complete') : null" in body
    assert "if (action === 'complete_goal' && closeOut === null) return;" in body
    # The inline /goals/{id}/execute-command POST body includes the close-out fields.
    assert "body.reason = closeOut.reason;" in body


def test_dispatch_menu_action_abort_goal_gated() -> None:
    """The abort_goal branch awaits askCloseOut and returns on cancel."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    assert "const closeOut = await askCloseOut('goal', 'abort');" in body
    assert "if (closeOut === null) return;" in body
    assert "patchStatus('goal', id, item.vault, 'aborted', 'Goal aborted', closeOut)" in body


def test_dispatch_menu_action_defer_not_gated() -> None:
    """Defer actions must NOT open the modal (no askCloseOut in their branch)."""
    body = _function_body(APP_JS, "dispatchMenuAction")
    # defer_goal / defer_task dispatch through the same command branches but only
    # askCloseOut for the close-out (complete) variant.
    assert "action === 'complete_goal' ? await askCloseOut('goal', 'complete') : null" in body
    assert "action === 'complete_task' ? await askCloseOut('task', 'complete') : null" in body


# --- app.js: handleDrop prompts before a close-out drop ---


def test_handle_drop_task_done_guards_ask_close_out() -> None:
    """Task drops into the 'done' column prompt first; cancel aborts the drop."""
    body = _function_body(APP_JS, "handleDrop")
    done_block = _if_block(body, "targetKey === 'done'")
    assert "askCloseOut('task', 'complete')" in done_block
    assert "if (closeOut === null) return;" in done_block
    # The close-out fields ride on the PATCH body (inside the subsequent
    # `if (closeOut)` block) for the done drop.
    assert "body.reason = closeOut.reason;" in body
    assert "body.gate_successor = closeOut.gate_successor;" in body


def test_handle_drop_goal_completed_guards_ask_close_out() -> None:
    """Goal drops into the 'completed' column prompt first; cancel aborts the drop."""
    body = _function_body(APP_JS, "handleDrop")
    completed_block = _if_block(body, "targetKey === 'completed'")
    assert "askCloseOut('goal', 'complete')" in completed_block
    assert "if (closeOut === null) return;" in completed_block
    assert "body.reason = closeOut.reason;" in body
    assert "body.gate_successor = closeOut.gate_successor;" in body
