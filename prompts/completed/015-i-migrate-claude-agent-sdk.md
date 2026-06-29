---
status: completed
summary: Migrated from claude-code-sdk to claude-agent-sdk, renamed ClaudeCodeOptions to ClaudeAgentOptions, replaced direct __aenter__/__aexit__ calls with AsyncExitStack, and updated model alias to explicit claude-sonnet-4-5
container: vault-ui-015-i-migrate-claude-agent-sdk
dark-factory-version: v0.44.0
created: "2026-03-11T22:00:00Z"
queued: "2026-03-11T21:25:02Z"
started: "2026-03-11T21:33:33Z"
completed: "2026-03-11T21:35:26Z"
---
<summary>
- Project migrates from deprecated claude-code-sdk to claude-agent-sdk
- ClaudeCodeOptions is renamed to ClaudeAgentOptions throughout
- Direct __aenter__/__aexit__ calls in start_session are replaced with AsyncExitStack
- Session.start/Session.close use AsyncExitStack instead of direct dunder calls
- send_prompt already uses async-with and is left unchanged
- Model references use explicit model IDs instead of short aliases
</summary>

<objective>
The project uses `claude-agent-sdk` (the supported replacement for the deprecated `claude-code-sdk`) with safe async context management via `AsyncExitStack` where manual lifecycle control is needed.
</objective>

<context>
Read CLAUDE.md for project conventions.
Read `pyproject.toml` — dependency declaration (~line 12).
Read `src/vault_ui/claude/session_manager.py` — all SDK usage.
Read `src/vault_ui/factory.py` — check if any `claude_code_sdk` imports remain (prior prompts may have removed them; if so, skip factory.py).
Read `tests/test_session_manager_integration.py` — uses `claude_code_sdk` imports that need updating.

Migration mapping:
- Package: `claude-code-sdk` → `claude-agent-sdk`
- Import: `claude_code_sdk` → `claude_agent_sdk`
- `ClaudeCodeOptions` → `ClaudeAgentOptions`
- `ClaudeSDKClient`, `AssistantMessage`, `SystemMessage`, `TextBlock` → unchanged
</context>

<requirements>
1. In `pyproject.toml`, replace the dependency:
   ```toml
   # OLD
   "claude-code-sdk>=0.0.25",
   # NEW
   "claude-agent-sdk>=0.1.0",
   ```

2. In `session_manager.py`, update the module-level import (~line 7):
   ```python
   # OLD
   from claude_code_sdk import ClaudeCodeOptions, ClaudeSDKClient
   # NEW
   from claude_agent_sdk import ClaudeAgentOptions, ClaudeSDKClient
   ```

3. Update the function-body imports in `start_session()` (~line 138) and `send_prompt()` (~line 227):
   ```python
   # OLD
   from claude_code_sdk import AssistantMessage, SystemMessage
   from claude_code_sdk import AssistantMessage, SystemMessage, TextBlock
   # NEW
   from claude_agent_sdk import AssistantMessage, SystemMessage
   from claude_agent_sdk import AssistantMessage, SystemMessage, TextBlock
   ```
   Note: if a prior prompt already moved these to module level, update there instead.

4. Replace all `ClaudeCodeOptions` with `ClaudeAgentOptions` in `session_manager.py`:
   - In `start_session()` (~lines 140-144)
   - In `send_prompt()` (~lines 229-232)

5. Replace model short alias `"sonnet"` with explicit model ID `"claude-sonnet-4-5"` in both:
   - `start_session()`: `ClaudeAgentOptions(model="claude-sonnet-4-5", ...)`
   - `send_prompt()`: `ClaudeAgentOptions(model="claude-sonnet-4-5", ...)`

6. In `start_session()`, replace direct `__aenter__`/`__aexit__` with `contextlib.AsyncExitStack`:
   ```python
   from contextlib import AsyncExitStack

   # OLD (~line 152)
   await client.__aenter__()

   # NEW
   stack = AsyncExitStack()
   await stack.enter_async_context(client)
   ```
   Pass `stack` (instead of `client`) to `_consume_session_messages` for cleanup. In the except block (~line 210-212):
   ```python
   # OLD
   except Exception:
       await client.__aexit__(None, None, None)
       raise
   # NEW
   except Exception:
       await stack.aclose()
       raise
   ```

7. Update `_consume_session_messages` signature to accept `stack: AsyncExitStack` as a parameter. In the `finally` block (~lines 106-110):
   ```python
   # OLD
   finally:
       try:
           await client.__aexit__(None, None, None)
   # NEW
   finally:
       try:
           await stack.aclose()
   ```

8. Update the `Session` class (~lines 15-29). `Session.start()` and `Session.close()` currently call `__aenter__`/`__aexit__` directly:
   ```python
   # OLD
   class Session:
       def __init__(self, client: ClaudeSDKClient) -> None:
           self.client = client
           self.messages: list[dict[str, str]] = []
       async def start(self) -> None:
           await self.client.__aenter__()
       async def close(self) -> None:
           await self.client.__aexit__(None, None, None)

   # NEW
   class Session:
       def __init__(self, client: ClaudeSDKClient) -> None:
           self.client = client
           self.messages: list[dict[str, str]] = []
           self._stack = AsyncExitStack()
       async def start(self) -> None:
           await self._stack.enter_async_context(self.client)
       async def close(self) -> None:
           await self._stack.aclose()
   ```

9. Do NOT modify `send_prompt()` lifecycle — it already uses `async with client:` (~line 240), which is the correct idiomatic pattern. Only update its imports and `ClaudeCodeOptions` → `ClaudeAgentOptions`.

10. In `factory.py`, if any `claude_code_sdk` imports remain, update them to `claude_agent_sdk`. If prior prompts already removed all SDK imports from factory.py, skip this step.

11. In `tests/test_session_manager_integration.py`, update any `claude_code_sdk` imports to `claude_agent_sdk` and `ClaudeCodeOptions` to `ClaudeAgentOptions`.

12. Run `uv sync --all-extras` to install the new dependency.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Do NOT change the session lifecycle behavior (start_session must still return session_id quickly and consume messages in background)
- Do NOT change the API endpoints or response models
- Do NOT modify the send_prompt lifecycle — it already uses async-with correctly
</constraints>

<verification>
Run `make precommit` -- must pass.
</verification>
