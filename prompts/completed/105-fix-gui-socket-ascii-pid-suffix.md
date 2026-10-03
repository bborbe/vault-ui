---
status: completed
summary: Filtered WezTerm GUI-socket candidates to ASCII-digit suffixes so non-ASCII-digit names like gui-sock-² are skipped instead of raising ValueError, with two new tests and a changelog entry.
execution_id: vault-ui-wezterm-socket-exec-105-fix-gui-socket-ascii-pid-suffix
dark-factory-version: dev
created: "2026-10-03T13:46:56Z"
queued: "2026-10-03T13:46:56Z"
started: "2026-10-03T13:47:28Z"
completed: "2026-10-03T13:48:21Z"
---

<summary>
- Finding the newest live WezTerm GUI socket must never crash the board, whatever files sit in the socket directory.
- Today a socket file whose name ends in a non-ASCII digit (such as a superscript two) makes that search crash.
- After this change only names ending in ordinary 0-9 digits count as socket candidates.
- Any other name is skipped silently, like every other non-matching file.
- Behaviour for normal socket names, liveness checks and newest-first ordering is unchanged.
- A test covers the case where only the odd name is present, and the search finds nothing.
- A second test covers the odd name sitting next to a live normal socket, and the normal one is chosen.
- The changelog records the fix.
</summary>

<objective>
`_wezterm_gui_socket()` in `src/vault_ui/pane_resolver.py` skips any `gui-sock-<suffix>` whose suffix is not made only of ASCII digits, so no filename in the socket directory can make it raise.
</objective>

<context>
Read `src/vault_ui/pane_resolver.py` (`_wezterm_gui_socket`, the candidate filter that calls `suffix.isdigit()` then `int(suffix)`) and `tests/test_pane_resolver.py` (the existing `_wezterm_gui_socket` tests and their fixtures — follow their patterns).

`str.isdigit()` is true for Unicode digits such as `²`, but `int("²")` raises `ValueError`. The function's docstring promises it never raises.
</context>

<requirements>
1. In `_wezterm_gui_socket()`, change the suffix check so a candidate is kept only when the suffix is non-empty and every character is an ASCII digit `0-9` (e.g. `suffix.isascii() and suffix.isdigit()`). Every other suffix is skipped exactly like a non-matching name. Change nothing else in the function.
2. Add a test in `tests/test_pane_resolver.py`: a directory holding `gui-sock-²` (and nothing live) makes `_wezterm_gui_socket()` return `None` without raising.
3. Add a test: a directory holding `gui-sock-²` alongside a live ASCII `gui-sock-<pid>` returns the ASCII one.
4. Add a `- fix:` bullet under the existing `## Unreleased` section of `CHANGELOG.md` saying the GUI-socket scan now ignores non-ASCII-digit suffixes instead of raising.
</requirements>

<constraints>
- Do not run any `git` command.
- Do not change behaviour for ASCII names, liveness, mtime ordering, or `_subprocess_env()`.
- Do not modify existing tests.
</constraints>

<verification>
set -o pipefail; make precommit
grep -q 'isascii' src/vault_ui/pane_resolver.py
grep -q 'gui-sock-²' tests/test_pane_resolver.py
</verification>
