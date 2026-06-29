---
status: completed
summary: Extracted _read_file helper and delegated update_task_phase to _update_task_frontmatter, eliminating duplicated UTF-8/latin-1 fallback read logic in ObsidianTaskReader
container: vault-ui-013-g-deduplicate-task-reader
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:32:07Z"
completed: "2026-03-11T21:32:45Z"
---
<summary>
- Duplicate frontmatter update code in update_task_phase is eliminated
- File reading with encoding fallback is extracted into a reusable helper
- update_task_phase delegates to the generic _update_task_frontmatter method
- _parse_task uses the same helper but discards the encoding (read-only)
- No changes to public method signatures or the TaskReader protocol
</summary>

<objective>
`ObsidianTaskReader` has one canonical `_read_file` helper for encoding-safe reads and one canonical `_update_task_frontmatter` method for all frontmatter writes. No duplicated file I/O patterns remain.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/obsidian/task_reader.py` — the `ObsidianTaskReader` class.

`update_task_phase()` (~lines 64-101) duplicates all file read/parse/write logic from `_update_task_frontmatter()` (~lines 103-149). The UTF-8/latin-1 fallback read pattern appears in both methods and in `_parse_task()` (~lines 189-193).
</context>

<requirements>
1. Add a private helper method `_read_file` to `ObsidianTaskReader`:
   ```python
   def _read_file(self, file_path: Path) -> tuple[str, str]:
       """Read file with UTF-8/latin-1 fallback.

       Args:
           file_path: Path to file

       Returns:
           Tuple of (content, encoding used)
       """
       try:
           return file_path.read_text(encoding="utf-8"), "utf-8"
       except UnicodeDecodeError:
           return file_path.read_text(encoding="latin-1"), "latin-1"
   ```

2. In `_update_task_frontmatter()`, replace the try/except encoding block (~lines 114-120) with:
   ```python
   content, encoding = self._read_file(file_path)
   ```

3. In `_parse_task()`, replace the try/except encoding block (~lines 189-193) with:
   ```python
   content, _ = self._read_file(file_path)
   ```
   Note: `_parse_task` is read-only — it never writes back, so the encoding is discarded.

4. Replace the body of `update_task_phase()` with a one-liner delegation:
   ```python
   def update_task_phase(self, task_id: str, new_phase: str) -> None:
       """Update the phase field in task frontmatter."""
       self._update_task_frontmatter(task_id, {"phase": new_phase})
   ```

5. Keep `update_task_phase` in the `TaskReader` protocol (line 33-34) — callers still use it.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the TaskReader protocol interface
- Do NOT change the behavior of any public method
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
