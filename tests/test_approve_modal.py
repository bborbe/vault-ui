"""Static-source regression tests for the approve modal.

Moving a task card from Todo into Planning is the operator's approval, not a
plain phase write: it opens a modal styled like the Abort close-out, renders one
labelled field per open question, and sends the answers with the phase change.
Cancel must leave the task file byte-identical, and every other transition must
keep the promptless path it has today.

This repo has no JS runtime, so these are the sanctioned in-container guards:
read the static sources via pathlib and assert on strings / brace-walked
function bodies, exactly as test_closeout_reason_modal.py does. The modal's
interactive behaviour is verified on the host with `make run`; these tests pin
the wiring so a future edit cannot silently drop the prompt, silently gate a
transition that should stay promptless, or silently send an empty answer.
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


# --- index.html: modal structure ---


def test_approve_modal_markup_present() -> None:
    """index.html contains every approve-modal element id the JS depends on."""
    for element_id in (
        'id="approve-modal"',
        'id="approve-form"',
        'id="approve-prompt"',
        'id="approve-confirm-btn"',
        'id="approve-cancel-btn"',
    ):
        assert element_id in INDEX_HTML, element_id


def test_approve_modal_reuses_existing_modal_classes() -> None:
    """The approve modal reuses .modal / .modal-content / .modal-buttons, so it
    inherits the Abort modal's styling rather than introducing a second look."""
    approve_modal = INDEX_HTML[INDEX_HTML.index('id="approve-modal"') :]
    assert 'class="modal hidden"' in approve_modal
    assert 'class="modal-content"' in approve_modal
    assert 'class="modal-buttons"' in approve_modal


def test_approve_modal_is_not_nested_inside_another_modal() -> None:
    """The modal is a sibling of the other modals, not nested in one of them.

    A nested modal is hidden whenever its parent is, so the prompt would never
    appear on a drag even though every string assertion above still passed.
    """
    start = INDEX_HTML.index('id="approve-modal"')
    # The next modal comment after the approve modal must appear before the
    # document ends, and the approve modal's own closing tag must precede it.
    rest = INDEX_HTML[start:]
    assert "</div>\n    </div>" in rest[: rest.index("<!-- Take-Over")]


# --- app.js: askApprove helper ---


def test_ask_approve_defined() -> None:
    """app.js defines the askApprove helper (sync function returning a Promise)."""
    assert re.search(r"(?:async\s+)?function\s+askApprove\s*\(", APP_JS)


def test_ask_approve_renders_one_field_per_open_question() -> None:
    """One labelled field per open question, built from the task's own list.

    The count is derived from `task.open_questions`, never a fixed number: a
    hardcoded single field would pass a single-question fixture and fail the
    two-question one, which is the shape this asserts against.
    """
    body = _function_body(APP_JS, "askApprove")
    assert "task.open_questions" in body
    assert "questions.map(" in body
    # Each field is labelled with its own question text and carries its index.
    assert "label.textContent = question.text;" in body
    assert "input.dataset.index = String(question.index);" in body


def test_ask_approve_contract() -> None:
    """askApprove resolves { answers } on Approve and null on Cancel."""
    body = _function_body(APP_JS, "askApprove")
    assert "approve-modal" in body
    assert "approve-form" in body
    # Confirm resolves the answers; Cancel resolves null.
    assert "resolvePromise({ answers })" in body
    assert "resolvePromise(null)" in body
    # Both paths hide the modal and detach their listeners.
    assert "modal.classList.add('hidden')" in body
    assert "removeEventListener" in body


def test_ask_approve_sends_only_filled_fields() -> None:
    """A blank field means 'no answer for this one', not 'answer with nothing'.

    The answer operation refuses an empty answer, so sending one would fail the
    whole approval; filtering here is what keeps a partly-filled modal valid.
    """
    body = _function_body(APP_JS, "askApprove")
    assert ".filter((input) => input.value.trim())" in body
    assert "answer: input.value.trim()" in body


# --- app.js: handleDrop gates the todo -> planning move ---


def test_handle_drop_asks_approve_for_todo_to_planning() -> None:
    """The todo -> planning drop awaits askApprove and returns on cancel."""
    body = _function_body(APP_JS, "handleDrop")
    assert "if (targetKey === 'planning' && task.phase === 'todo') {" in body
    assert "const approval = await askApprove(task);" in body
    assert "if (approval === null) {" in body
    assert "answers = approval.answers;" in body


def test_handle_drop_cancel_returns_before_any_request() -> None:
    """A cancel must leave the file untouched, so it returns before the fetch.

    Asserting the `return` exists is not enough — it has to come first. A
    cancel that fell through to the fetch would move the card anyway.
    """
    body = _function_body(APP_JS, "handleDrop")
    cancel = body.index("if (approval === null) {")
    fetch_at = body.index("await fetch(")
    assert cancel < fetch_at, "the cancel return must precede the phase request"


def test_handle_drop_sends_answers_only_when_present() -> None:
    """The body gains `answers` only when the operator filled something.

    A task with no open questions approves with the same body it always sent,
    so the existing phase path is unchanged for it.
    """
    body = _function_body(APP_JS, "handleDrop")
    assert "const body = { phase: targetKey };" in body
    assert "if (answers.length > 0) {" in body
    assert "body.answers = answers;" in body
    # The assignment is inside the guard, not before it.
    guard = body.index("if (answers.length > 0) {")
    assign = body.index("body.answers = answers;")
    assert guard < assign


def test_handle_drop_other_transitions_stay_promptless() -> None:
    """No transition other than todo -> planning opens the approve modal."""
    body = _function_body(APP_JS, "handleDrop")
    assert body.count("askApprove(") == 1
    # The goal path never prompts.
    goal_branch = body[body.index("if (goal) {") :]
    assert "askApprove(" not in goal_branch
