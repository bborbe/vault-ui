---
status: completed
summary: Moved all function-body imports in tasks.py to module level and added get_status_cache to the factory import block
container: vault-ui-007-a-fix-lint-and-move-imports
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:25:59Z"
completed: "2026-03-11T21:27:07Z"
---
<summary>
- The RUF059 lint error (unused variable in tuple unpacking) is resolved
- All function-body imports in tasks.py are consolidated at module level
- A duplicate factory import inside reload_cache is removed
- The module-level factory import block gains get_status_cache
- No behavior changes — import locations only
</summary>

<objective>
All imports in tasks.py are at module level and `make precommit` passes with no lint errors.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/api/tasks.py` — the file with all issues.
</context>

<requirements>
1. In `update_task_phase()` (~line 467), rename `stdout` to `_stdout` in the tuple unpacking:
   ```python
   # OLD
   stdout, stderr = await proc.communicate()
   # NEW
   _stdout, stderr = await proc.communicate()
   ```

2. Move `import json` and `import re` from inside `execute_slash_command()` (~line 376-377) to module-level imports at the top of the file. `re` is NOT currently imported at module level — add it.

3. Move `from pathlib import Path` from inside `reload_cache()` (~line 524) to module-level imports.

4. Add `get_status_cache` to the existing module-level factory import. Change:
   ```python
   # OLD
   from vault_ui.factory import (
       get_config,
       get_task_reader_for_vault,
       get_vault_config,
   )
   # NEW
   from vault_ui.factory import (
       get_config,
       get_status_cache,
       get_task_reader_for_vault,
       get_vault_config,
   )
   ```

5. Remove the inner import `from vault_ui.factory import get_status_cache` inside `list_tasks()` (~line 163).

6. Remove the inner import `from vault_ui.factory import get_config, get_status_cache` inside `reload_cache()` (~line 526). Both are now available from the module-level import.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change any behavior — imports only
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
