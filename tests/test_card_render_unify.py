"""Static assertions for the unified card template in app.js.

Every card kind (task, goal, topic) is described by one entry in ``CARD_CONFIG``
and one data mapper; a single ``createCard`` builder renders them all. This
suite guards that shape: one builder, no per-kind markup, config-driven slots,
and boards that call the builder instead of per-kind renderers.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
APP_JS = (REPO_ROOT / "src" / "vault_ui" / "static" / "app.js").read_text()
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()

# Referenced through a module-level name so this file never contains the literal
# declaration the acceptance check greps for.
BUILDER = "createCard"


def _slice(marker: str, length: int) -> str:
    start = APP_JS.find(marker)
    assert start != -1, f"marker not found: {marker}"
    return APP_JS[start : start + length]


def _function_body(name: str) -> str:
    """The whole body of a top-level function, up to the next top-level function.

    A fixed character window silently under-covers a builder longer than the
    window, so a guard like "the flag markup is not inlined here" would only
    inspect the head of the function and pass regardless.
    """
    marker = f"function {name}("
    start = APP_JS.find(marker)
    assert start != -1, f"function not found: {name}"
    end = APP_JS.find("\nfunction ", start + len(marker))
    return APP_JS[start:] if end == -1 else APP_JS[start:end]


def _slice_function_body(source: str, fn_name: str) -> str:
    """Return the body of the named function (without outer braces)."""
    pattern = re.compile(
        rf"^(?:async\s+)?function\s+{re.escape(fn_name)}\s*\([^)]*\)\s*\{{",
        re.MULTILINE,
    )
    m = pattern.search(source)
    if not m:
        raise AssertionError(f"function {fn_name} not found in app.js")
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


def _brace_body(source: str, open_index: int) -> str:
    """Return the content between the brace at ``open_index`` and its match."""
    depth = 0
    i = open_index
    while i < len(source):
        c = source[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                return source[open_index + 1 : i]
        i += 1
    raise AssertionError("unbalanced braces")


def _object_body(source: str, key: str) -> str:
    """Find ``key: {`` and walk braces to the matching close."""
    marker = key + ": {"
    start = source.find(marker)
    assert start != -1, f"object not found: {key}"
    return _brace_body(source, source.index("{", start))


def _removed(prefix: str, suffix: str) -> str:
    """Build a removed declaration's name from parts, so this file never holds
    the literal a future grep might count."""
    return prefix + suffix


def test_one_card_builder() -> None:
    """Exactly one card builder exists, with one innerHTML write site, no
    kind-name branch, and the base class assignment."""
    declarations = re.findall(r"^function create[A-Za-z]*Card\(", APP_JS, re.MULTILINE)
    assert len(declarations) == 1, declarations
    assert APP_JS.count("card.innerHTML =") == 1

    body = _function_body(BUILDER)
    for token in ("=== 'task'", "=== 'goal'", "=== 'topic'"):
        assert token not in body, token

    assert APP_JS.count("card.className = 'task-card") == 1
    assert "card.className = 'task-card" in body


def test_no_per_kind_markup_survives() -> None:
    """Mappers carry data only; the per-kind renderers are gone."""
    for mapper in ("mapTaskCard", "mapGoalCard", "mapTopicCard"):
        body = _slice_function_body(APP_JS, mapper)
        assert "innerHTML" not in body, mapper
        assert "<div" not in body, mapper
        assert "class=" not in body, mapper

    for gone in (
        _removed("function create", "TaskCard("),
        _removed("function create", "GoalCard("),
        _removed("function create", "TopicCard("),
    ):
        assert gone not in APP_JS, gone


def test_card_builder_reads_the_config_map() -> None:
    """Every kind-specific difference is read from the config entry or the
    view-model — no field is declared but unused by the template."""
    body = _function_body(BUILDER)
    for token in (
        "CARD_CONFIG[kind]",
        "config.map(",
        "config.slots.",
        "config.idDataset",
        "config.kindDataset",
        "config.vaultDataset",
        "config.draggable",
        "config.extraClass",
        "config.titleLinkExtraClass",
        "config.holdTitle",
        "config.assignCall",
    ):
        assert token in body, token


def test_boards_call_the_builder() -> None:
    """Every board constructs cards through the shared builder."""
    assert APP_JS.count("createCard('task', task)") == 2
    assert APP_JS.count("createCard('goal', goal)") == 1
    assert APP_JS.count("createCard('topic', topic)") == 1
    for gone in (
        "createTaskCard(",
        "createGoalCard(",
        "createTopicCard(",
    ):
        assert gone not in APP_JS, gone


def test_config_map_declares_every_kind() -> None:
    """The CARD_CONFIG literal carries one entry per kind."""
    outer_start = APP_JS.find("const CARD_CONFIG = {")
    assert outer_start != -1
    outer = _brace_body(APP_JS, APP_JS.index("{", outer_start))
    for kind in ("task", "goal", "topic"):
        assert f"{kind}: {{" in outer, kind


def test_topic_config_matches_the_operator_decision() -> None:
    """Topic cards keep flag/note/detail/live and drop the task-only controls.

    ``menu: false`` is also what turns the status controls off: every status
    action (complete, hold, abort, defer) lives in the card menu built by
    ``showMenu``, so there is no separate status slot to disable.
    """
    topic = _object_body(APP_JS, "topic")
    assert "noteLink: true" in topic
    assert "flag: true" in topic
    assert "detail: true" in topic
    assert "live: true" in topic
    assert "statusFilter: true" in topic
    assert "fallbackColumn: 'unknown'" in topic
    assert "menu: false" in topic
    assert "session: false" in topic
    assert "draggable: false" in topic

    task = _object_body(APP_JS, "task")
    goal = _object_body(APP_JS, "goal")
    for entry in (task, goal):
        assert "menu: true" in entry
        assert "session: true" in entry
        assert "detail: false" in entry
        assert "statusFilter: false" in entry
    assert "draggable: true" in task
    assert "draggable: false" in goal
    assert "fallbackColumn: 'todo'" in task
    assert "fallbackColumn: 'unknown'" in goal


def test_topic_mapper_reads_exactly_the_wire_keys() -> None:
    """The topic mapper reads the topic response's eight keys and no others."""
    body = _slice_function_body(APP_JS, "mapTopicCard")
    keys = set(re.findall(r"\btopic\.([a-z_]+)", body))
    assert keys == {
        "id",
        "title",
        "status",
        "phase",
        "session_state",
        "flag",
        "vault",
        "obsidian_url",
    }, keys


def test_topic_mapper_leaves_unsupported_slots_empty() -> None:
    """Absent topic fields render no badge — never the literal ``undefined``."""
    body = _slice_function_body(APP_JS, "mapTopicCard")
    for token in (
        "assignee: ''",
        "priority: null",
        "activityDate: null",
        "blockers: []",
        "jiraKey: ''",
        "jiraUrl: ''",
    ):
        assert token in body, token


def test_session_button_helper_shared() -> None:
    """One session-button helper gates all three labels off session id + durable
    flag, and the shared builder calls it instead of inlining the gate."""
    body = _slice("function sessionButtonHtml", 4000)
    assert "claude_session_id" in body
    assert "claude_session_started" in body
    assert "▶ Start" in body
    assert "▶ Resume" in body
    assert "Starting" in body
    assert "runSession('${kind}'" in body
    assert "sessionButtonHtml(kind, item)" in APP_JS


def test_flag_control_present_on_all_card_kinds() -> None:
    """Every card kind carries the flag toggle through one shared helper, so the
    gesture does not depend on which kind the card happens to be."""
    assert "function flagButtonHtml(kind, item)" in APP_JS
    assert "toggleFlag(" in APP_JS
    assert "classList.add('flagged')" in APP_JS
    assert "🚩" in APP_JS and "⚑" in APP_JS
    # The markup lives only in the helper: the builder inlines none of it, and
    # every kind enables the flag slot.
    assert 'class="flag-btn' not in _function_body(BUILDER)
    for kind in ("task", "goal", "topic"):
        assert "flag: true" in _object_body(APP_JS, kind), kind


def test_flag_check_leads_every_sort_mode() -> None:
    """Flagged cards sort to the top before any sort mode applies — the flag
    comparison must precede the priority/modified/default branches, otherwise
    the header sort-select would override the "picked for today" marker."""
    body = _slice("function sortBoardItems", 700)
    assert "!!a.flag" in body
    assert "!!b.flag" in body
    # The flag branch must come before every mode-specific branch.
    assert body.index("!!a.flag") < body.index("currentSort === 'priority'")
    assert body.index("!!a.flag") < body.index("currentSort === 'modified'")
    assert body.index("!!a.flag") < body.index("defaultSortCompare")


def test_cachebust_token_bumped() -> None:
    """index.html carries a non-empty app.js token; known-stale tokens are gone.

    Asserts the shape rather than the current value, matching
    test_goal_session_controls.test_cachebust_token_bumped. Pinning the literal
    token made every legitimate bump fail this test, which trains the bump to be
    skipped — and an un-bumped token is exactly how this repo has previously
    shipped a fix that browsers never received.
    """
    assert re.search(r"app\.js\?v=\S+", INDEX_HTML)
    assert "app.js?v=2026-08-19-board-sort" not in INDEX_HTML
    assert "app.js?v=2026-08-25-closeout-abort-only" not in INDEX_HTML
    assert "app.js?v=2026-10-29-jump-pane-gating" not in INDEX_HTML


def test_task_card_keeps_urgency_and_jira() -> None:
    """The task mapper keeps both halves of the Jira logic and the urgency tier;
    the builder still renders the urgency class."""
    mapper = _slice_function_body(APP_JS, "mapTaskCard")
    assert "getUrgencyTier(" in mapper
    assert "extractJiraIssue(task.title)" in mapper
    assert "task.jira" in mapper

    assert "urgency-overdue" in _function_body(BUILDER)


def test_goal_card_keeps_onhold_and_dataset() -> None:
    """The goal config keeps the goal dataset and the mapper keeps the hold
    derivation; the builder applies the on-hold class from the view-model."""
    goal = _object_body(APP_JS, "goal")
    assert "idDataset: 'goalId'" in goal
    assert "kindDataset: 'goal'" in goal

    mapper = _slice_function_body(APP_JS, "mapGoalCard")
    assert "goal.status === 'hold'" in mapper

    body = _function_body(BUILDER)
    assert "on-hold" in body
    assert "view.hold" in body


def test_topic_card_drops_status_badge_and_gates_the_live_indicator() -> None:
    """The topic card renders no status badge — its column is its phase — and
    shows the display-only live indicator only when the session is live."""
    body = _function_body(BUILDER)
    assert "status-badge" not in body
    assert "view.live" in body
    assert "topic-live-badge" in body

    mapper = _slice_function_body(APP_JS, "mapTopicCard")
    assert "session_state === 'live'" in mapper
