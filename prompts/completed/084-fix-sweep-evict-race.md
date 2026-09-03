---
status: completed
summary: 'Made the cleanup sweep''s record eviction conditional: added LaunchRegistry.evict_if_finished and used it at both post-await re-clear call sites (task and goal) so a concurrent relaunch that re-begins a record during the awaited clear subprocess keeps its fresh IN_FLIGHT record, with unit tests, race-reproducing regression tests (verified to fail pre-fix), docs, and a CHANGELOG entry.'
execution_id: vault-ui-starting-marker-exec-084-fix-sweep-evict-race
dark-factory-version: dev
created: "2026-09-03T20:14:39Z"
queued: "2026-09-03T20:14:39Z"
started: "2026-09-03T20:26:35Z"
completed: "2026-09-03T20:29:05Z"
---

<summary>
- The cleanup sweep can delete a launch record that a brand-new launch just created, undoing the protection this whole feature exists to provide.
- It happens because the sweep decides which records to drop before it waits on a slow external command, then acts on that stale decision after the wait.
- During the wait, a person clicking Start on the same item creates a fresh record; the sweep then deletes it.
- For that one relaunch the board can go back to showing "Starting…" forever, which is exactly the bug the feature was built to prevent.
- The fix is to re-check the record is still finished immediately before dropping it, in one step that cannot be interrupted.
- Found by local review of PR #54; no existing test covers a relaunch racing the sweep.
</summary>

<objective>
Make the cleanup sweep's record eviction safe against a concurrent relaunch, so a launch that begins while the sweep is awaiting a clear subprocess keeps its registry record and stays protected.
</objective>

<context>
Read `docs/dod.md` before writing code (the repo's Definition of Done: type annotations, no swallowed errors, >= 80% coverage on new code, CHANGELOG entry). Do NOT try to read `CLAUDE.md` — it is gitignored and absent from this worktree.

Read first:
- `src/vault_ui/launch_registry.py` — the whole file. `LaunchRegistry` holds `self._records: dict[tuple[str, str], tuple[str, str]]` mapping `(vault, item_id)` to `(state, kind)`, with `begin`, `finish`, `state`, `evict`, `finished`, `size`.
- `src/vault_ui/cleanup.py` — the two "re-clear resurrected marker" passes. Task side ~266-323, goal side ~552-605. Each iterates `launch_registry.finished(vault.name)`, awaits a `vault-cli ... clear` subprocess via `communicate()`, and calls `launch_registry.evict(...)` on success.
- `docs/starting-marker-lifecycle.md` — the marker/registry/sweep contract this change amends.
- `tests/test_launch_registry.py` and `tests/test_cleanup.py` — existing style, fixtures `_make_config` (builds vault name `"testvault"`, lowercase), `_make_task`, `_make_goal`.

The defect, in full:

`finished(vault)` returns a materialized snapshot of records taken BEFORE the loop awaits anything. vault-ui runs a single uvicorn worker, so other coroutines run during `await resurrected_proc.communicate()`. A concurrent `POST /api/tasks/{id}/run` for the SAME `(vault, item_id)` calls `begin()`, overwriting the record to `(IN_FLIGHT, kind)`. When the sweep resumes it calls `evict()` unconditionally, deleting that fresh IN_FLIGHT record. For the duration of that relaunch the registry can no longer suppress a resurrected marker — reintroducing the original bug — and the relaunch's own `finish()` becomes a silent no-op because the record is already gone.

The two "fast path" evictions (task side ~280-282, goal side ~561-563: `if not marker: evict(); continue`) are SAFE and must not change — no `await` occurs between reading the marker and evicting, so no interleaving is possible there.
</context>

<requirements>
1. Add a new method to `LaunchRegistry` in `src/vault_ui/launch_registry.py`:

```python
    def evict_if_finished(self, vault: str, item_id: str) -> bool:
        """Drop the record for ``(vault, item_id)`` only if it is still FINISHED.

        The cleanup sweep decides what to evict from a snapshot taken before it
        awaits a clear subprocess. During that await a concurrent launch can call
        ``begin()`` for the same id, flipping the record back to IN_FLIGHT; an
        unconditional evict would delete that fresh record and leave the relaunch
        unprotected. Re-checking and deleting in one synchronous step closes that
        window — there is no await between the check and the delete, so under the
        single-worker assumption no coroutine can interleave.

        Returns True when a FINISHED record was removed, False otherwise.
        """
        record = self._records.get((vault, item_id))
        if record is None or record[0] != FINISHED:
            return False
        self._records.pop((vault, item_id))
        return True
```

2. In `src/vault_ui/cleanup.py`, in the TASK-side re-clear pass, replace the post-await success eviction `launch_registry.evict(vault.name, finished_id)` with `launch_registry.evict_if_finished(vault.name, finished_id)`. Add a short comment: the record may have been re-begun by a concurrent launch while the clear subprocess was awaited, and that fresh record must survive.

3. Apply the identical change to the GOAL-side re-clear pass. Both sweeps must be fixed; fixing only the task side leaves the same race on goals.

4. Do NOT change the two fast-path evictions (`if not marker: evict(); continue`) on either side. They are already safe and an `evict_if_finished` there would be equivalent but noisier.

5. Do NOT change `evict()` itself — it stays unconditional and is still used by the fast paths.

6. Tests in `tests/test_launch_registry.py`:
   - `evict_if_finished` on a FINISHED record removes it and returns True.
   - `evict_if_finished` on an IN_FLIGHT record leaves it and returns False; `state(...)` still reports IN_FLIGHT.
   - `evict_if_finished` on an absent key returns False and does not raise.

7. A regression test in `tests/test_cleanup.py` that reproduces the race directly. Copy the patch block from the existing `test_registry_resurrected_task_marker_recleared_exactly_once` in `tests/test_cleanup.py` — all four patches are required, in particular `patch("vault_ui.factory.get_launch_registry", return_value=registry)`; without it the sweep reads the process-global singleton, `finished()` returns `[]`, no subprocess runs, and the test fails for the wrong reason. Use the `"testvault"` vault name from `_make_config` (lowercase). Arrange a FINISHED task record plus a cache marker so the re-clear pass runs, and replace only `mock_proc.communicate` with a plain `async def` that calls `registry.begin("testvault", "<id>", "task")` and then returns `(b"", b"")` — that is what simulates a relaunch landing during the await. Then assert after the sweep that `registry.state("testvault", "<id>") == IN_FLIGHT` (the fresh record survived) and `registry.size() == 1`. This test MUST fail against the current unconditional `evict()` and pass after the fix; state that expectation in the test's docstring.

8. Add the same regression test for the goal-side sweep.

9. Do NOT add `@pytest.mark.integration` to any test — that marker is deselected by `addopts = "-m 'not integration'"` in `pyproject.toml`, so a marked test would never run in `make test` / `make precommit`.

10. Update `docs/starting-marker-lifecycle.md` — it currently says the sweep "evicts the record once the marker is confirmed gone". Amend it to state that eviction is conditional: the record is dropped only if it is still FINISHED, so a relaunch that begins while the clear subprocess is running keeps its fresh record. One or two sentences; do not restructure the doc.

11. Add a `## Unreleased` CHANGELOG bullet describing the fix in behavioural terms (a relaunch starting while the sweep is clearing a resurrected marker keeps its registry record).

12. Before finishing, re-run every command in `<verification>` and confirm each passes, then walk requirements 1-11 against your diff.
</requirements>

<constraints>
- Do NOT commit; dark-factory handles the commit.
- Do NOT modify `src/vault_ui/static/app.js` — the frontend contract is frozen.
- Do NOT change `_STARTING_MARKER_TTL_SECONDS` or any existing sweep semantics beyond the two eviction call sites named above.
- Do NOT add a config flag, threshold, or opt-out.
- Do NOT change the `(vault, item_id)` key shape of the registry.
- Existing behaviour must not regress: all currently passing tests must still pass.
- The registry stays process-local and in-memory; do not persist it.
- Out of scope: the clear subprocess may still wipe a marker the relaunch wrote during the await. This prompt fixes only the registry record; do NOT attempt to also re-write or guard the on-disk marker.
</constraints>

<verification>
Run, in order, confirming each passes:
- `uv run pytest tests/test_launch_registry.py tests/test_cleanup.py -v`
- `grep -c 'launch_registry.evict_if_finished(' src/vault_ui/cleanup.py` — must print `2` (the two post-await success branches)
- `grep -c 'launch_registry.evict(' src/vault_ui/cleanup.py` — must print `2` (the two fast paths, unchanged)
- `set -o pipefail; make precommit 2>&1 | tee /tmp/precommit.log` from the repo root — must exit 0
- `! grep -q ERROR /tmp/precommit.log` — must exit 0 (note `grep -c` prints `0` but EXITS 1, so do not gate on its exit status)

Do not run any `git` command — the container has no usable `.git` (hideGit).

Do not grep for the ABSENCE of `launch_registry.evict(` — it is still correct there; the two fast paths must keep calling it.
</verification>
