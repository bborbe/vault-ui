"""Contract tests for the responsive header declutter (narrow viewports)."""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
INDEX_HTML = (REPO_ROOT / "src" / "vault_ui" / "static" / "index.html").read_text()
STYLE_CSS = (REPO_ROOT / "src" / "vault_ui" / "static" / "style.css").read_text()


def _narrow_media_block() -> str:
    """Return the body of the max-width:1500px media query, or '' if absent."""
    match = re.search(
        r"@media\s*\(max-width:\s*1500px\)\s*\{(.*?)\n\}",
        STYLE_CSS,
        re.DOTALL,
    )
    return match.group(1) if match else ""


def test_style_css_has_narrow_media_query() -> None:
    assert "@media (max-width: 1500px)" in STYLE_CSS


def test_narrow_media_hides_title() -> None:
    block = _narrow_media_block()
    assert "header h1" in block
    # the title rule must set display:none within the narrow block
    assert re.search(r"header h1\s*\{\s*display:\s*none", block)


def test_narrow_media_hides_upcoming_window() -> None:
    block = _narrow_media_block()
    assert "#upcoming-window" in block
    # Combined with #sort-select since both header controls share the rule.
    assert re.search(r"#upcoming-window\s*,\s*#sort-select\s*\{\s*display:\s*none", block)


def test_title_and_upcoming_still_present_by_default() -> None:
    # only hidden inside the media query — the elements themselves stay in the DOM
    assert "<h1>Vault UI</h1>" in INDEX_HTML
    assert 'id="upcoming-window"' in INDEX_HTML


def test_style_css_cache_bust_bumped() -> None:
    """index.html carries a non-empty style.css token; known-stale tokens are gone.

    Asserts the shape rather than the current value (same rationale as
    test_card_render_unify.test_cachebust_token_bumped): pinning the literal
    made every legitimate bump fail, training the bump to be skipped — and an
    un-bumped token is how this repo previously shipped a fix browsers never got.
    """
    assert re.search(r"style\.css\?v=\S+", INDEX_HTML)
    assert "style.css?v=2026-08-19-board-sort" not in INDEX_HTML
    assert "style.css?v=2026-08-24-closeout-reason" not in INDEX_HTML
