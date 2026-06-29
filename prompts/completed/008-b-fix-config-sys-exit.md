---
status: completed
summary: Replaced sys.exit(1) in load_config() with FileNotFoundError, moved wiring into main() for catchability, and updated the test to expect FileNotFoundError
container: vault-ui-008-b-fix-config-sys-exit
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:27:10Z"
completed: "2026-03-11T21:28:29Z"
---
<summary>
- Config loading raises FileNotFoundError instead of calling sys.exit, making it testable
- The main entry point catches the error and prints the user-friendly message
- Library code no longer has side effects (print + exit)
- The existing test for missing config is updated to expect FileNotFoundError
- Module-level wiring in __main__.py is moved inside main() to make the exception catchable
</summary>

<objective>
`load_config()` is a pure function that raises on error instead of exiting the process, and `__main__.py` handles the user-facing error message at the composition root.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/config.py` — the `load_config()` function (~line 42).
Read `src/vault_ui/__main__.py` — the entry point.
Read `tests/test_config.py` — contains `test_load_config_missing_file_exits` (~line 83) that currently asserts `SystemExit`.

**Important call chain**: `__main__.py` calls `create_app()` and `get_config()` at MODULE LEVEL (lines 19-31), not inside `main()`. This means a try/except inside `main()` will NOT catch a `FileNotFoundError` from `load_config()`. The module-level wiring must be moved inside `main()` for the exception to be catchable.
</context>

<requirements>
1. In `config.py`, replace the `print()` + `sys.exit(1)` block in `load_config()` with:
   ```python
   # OLD (lines 44-52)
   if not config_path.exists():
       print(
           f"ERROR: config.yaml not found at {config_path}\n"
           "\nCreate it by copying the example:\n"
           "  cp config.yaml.example config.yaml\n"
           "\nThen edit vault paths to match your system.",
           file=sys.stderr,
       )
       sys.exit(1)

   # NEW
   if not config_path.exists():
       raise FileNotFoundError(
           f"config.yaml not found at {config_path}\n"
           "\nCreate it by copying the example:\n"
           "  cp config.yaml.example config.yaml\n"
           "\nThen edit vault paths to match your system."
       )
   ```

2. Remove `import sys` from `config.py` (verify no other usage first).

3. In `__main__.py`, move ALL module-level wiring (lines 18-31) inside `main()`, after the logging setup. The module level should only have imports and the `app` variable assignment (needed for `uvicorn --reload`). Restructure like this:
   ```python
   # Module level: only imports
   # app variable set inside main() or via a get_app() function

   def main() -> int:
       logging.basicConfig(...)

       try:
           session_manager = SessionManager()
           app = create_app()
           set_session_manager(session_manager)
           set_connection_manager(get_connection_manager())
           tasks_set_connection_manager(get_connection_manager())

           config = get_config()
           uvicorn.run(app, host=config.host, port=config.port, log_level="info")
       except FileNotFoundError as e:
           print(str(e), file=sys.stderr)
           return 1

       return 0
   ```
   Note: the `make watch` target uses `vault_ui.__main__:app` for uvicorn reload. If `app` is no longer module-level, either keep a lazy `app` at module level or update the Makefile watch target to use `vault_ui.factory:create_app()` with `--factory` flag.

4. In `tests/test_config.py`, update the existing test at ~line 83:
   ```python
   # OLD
   def test_load_config_missing_file_exits(tmp_path: Path) -> None:
       """load_config exits with error when config.yaml is missing."""
       with pytest.raises(SystemExit):
           load_config(tmp_path / "nonexistent.yaml")

   # NEW
   def test_load_config_missing_file_raises(tmp_path: Path) -> None:
       """load_config raises FileNotFoundError when config.yaml is missing."""
       with pytest.raises(FileNotFoundError, match="config.yaml not found"):
           load_config(tmp_path / "nonexistent.yaml")
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass (after updating the test in req 4)
- Do NOT change config.yaml format or any other config behavior
- `make watch` (uvicorn --reload) must still work
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
