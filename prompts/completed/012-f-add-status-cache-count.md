---
status: completed
summary: Added StatusCache.count() public method and replaced both cache._cache private accesses in reload_cache() with it
container: vault-ui-012-f-add-status-cache-count
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:31:24Z"
completed: "2026-03-11T21:32:05Z"
---
<summary>
- StatusCache exposes a public count method for querying cached item counts
- The reload_cache API endpoint no longer accesses StatusCache private internals
- The encapsulation pattern matches the existing get_status public method
- No test changes needed — existing tests pass unchanged
- No API response format changes
</summary>

<objective>
`StatusCache` has a public `count()` method and no external code accesses `cache._cache` directly.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `src/vault_ui/status_cache.py` — the `StatusCache` class.
Read `src/vault_ui/api/tasks.py` — the `reload_cache()` function (~line 511).

`reload_cache()` accesses `cache._cache.get(vault, {})` directly at ~lines 539 and 548 to get the count of cached items. This breaks encapsulation.
</context>

<requirements>
1. Add a `count()` method to `StatusCache` (after `get_status`, ~line 89):
   ```python
   def count(self, vault_name: str) -> int:
       """Get number of cached items for a vault.

       Args:
           vault_name: Name of the vault

       Returns:
           Number of cached items, 0 if vault not loaded
       """
       return len(self._cache.get(vault_name, {}))
   ```

2. In `reload_cache()` in `tasks.py`, replace both private access sites:
   ```python
   # OLD (~line 539, single vault reload)
   count = len(cache._cache.get(vault, {}))
   # NEW
   count = cache.count(vault)

   # OLD (~line 548, all vaults reload)
   count = len(cache._cache.get(vault_config.name, {}))
   # NEW
   count = cache.count(vault_config.name)
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the internal `_cache` data structure
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
