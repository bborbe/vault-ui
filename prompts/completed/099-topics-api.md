---
status: completed
summary: Added GET /api/topics and GET /api/topics/{topic_id} backed by new vault-cli topic client methods, a Topic/TopicDetail model pair with extra-forbid responses, and an optional VaultConfig.topics_folder that never gates a vault
execution_id: vault-ui-topics-exec-099-topics-api
dark-factory-version: v0.196.0
created: "2026-09-30T13:05:22Z"
queued: "2026-09-30T13:05:22Z"
started: "2026-09-30T13:17:10Z"
completed: "2026-09-30T13:24:32Z"
---

# Add the Topics API (List and Detail)

<summary>
- The API can list a vault's topics, alongside its existing task and goal endpoints
- Each topic carries the status read from that topic's own file — not a default, not a derived value
- Topics whose status is `completed` are included; the list is not limited to active work
- A topic's detail response carries the work that topic tracks, each entry classified as a goal, a task, or unresolved
- A topic with no tracked work returns empty lists rather than an error
- A vault that has no topics folder returns an empty list instead of an error
- Existing task and goal endpoints are unchanged
</summary>

<objective>
Expose the vault's topics over the API so the board can render them. Topics are the containers every manager loop is scoped to, and they are the one layer of the vault hierarchy the API does not currently serve — so a topic and the work it tracks cannot be seen outside Obsidian.
</objective>

<context>
Read `README.md` for project conventions and `docs/dod.md` for the definition of done the daemon validates against.

The goals view is the pattern to follow. Read these before writing anything:

- `src/vault_ui/vault_cli_client.py` — `list_goals` (~line 215) and `show_task` (~line 142) are the two client methods to mirror.
- `src/vault_ui/api/tasks.py` — `_goal_to_response` (~line 837), `_process_goal_vault` (~line 896) and `list_goals` (~line 1004) are the response-builder, per-vault-fetch and list-endpoint shapes to mirror. `run_task` (~line 1124) is the item-scoped route shape — it takes a plain required `vault: str` alongside the id, and its error handling (~lines 1252-1262) is the pattern for a 404 rather than a bare 500.
- `src/vault_ui/config.py` — `VaultConfig` and `_build_vault_config` (~line 115), which returns `None` for an unusable vault.
- `src/vault_ui/status_cache.py` — the existing in-memory status map. Note: watcher-driven invalidation lives in `factory.py` (`start_task_watchers.on_change`), not here.
- `src/vault_ui/hierarchy.py` — `HIERARCHY_SUFFIXES` is `("Themes", "Objectives", "Goals", "Tasks")` and has **no** `Topics` entry, so `discover_hierarchy_folders` cannot find a topics folder. Use the configured `topics_folder` instead; do not add a suffix.

Facts verified against `vault-cli` v0.156.0 on 2026-09-30 — do not re-derive them:

- `vault-cli topic list --all --vault <v> --output json` returns every topic, each with exactly `name`, `status`, `vault`, `category`, `modified_date` — there is **no** `title` key. **The `--all` flag is mandatory**: the bare `vault-cli topic list` filters to `in_progress` and returns 9 of the private-personal vault's 12 topics, silently hiding the 3 `completed` ones.
- `vault-cli topic show "<name>" --vault <v> --output json` is the detail command. It returns exactly `name`, `file_path`, `vault`, `fields` (a map including `status`), `field_order`, `content` (the full markdown) — and **no** `modified_date`.
- `vault-cli topic get` is **not** the detail command — it reads a single frontmatter field and takes two arguments (`topic get <name> <key>`).
- `vault-cli config list --output json` carries an optional `topics_dir` key per vault (e.g. `"topics_dir": "23 Topics"`). Only 2 of 14 vaults have it.

**The shape of a topic page's `## Goals` section — measured 2026-09-30 across all 12 pages of private-personal.** It is named "Goals" but holds the topic's *whole tracked set*, overwhelmingly tasks:

| Topic | entries | resolve to a goal | resolve to a task |
|---|---|---|---|
| Attention Routing | 116 | 1 | 115 |
| Manager Layer | 15 | 5 | 10 |
| Work Approval | 10 | **0** | 10 |
| (9 others) | 37 | 22 | 15 |
| **Total** | **178** | **28** | **150** |

Note the totals: the pages hold **192** top-level `- ` bullets but only **178** entries — 14 bullets carry no leading wikilink and are not entries. Do not assume the section holds goals, and do not derive the task half from tasks' `goals:` frontmatter — see requirement 5.
</context>

<requirements>
1. **Client methods** in `src/vault_ui/vault_cli_client.py`: add `async def list_topics() -> list[Topic]` and `async def show_topic(topic_id: str) -> TopicDetail`. Mirror `list_goals` (~line 215) for the subprocess/parse shape and `show_task` (~line 142) for the single-item shape. `list_topics` always passes `--all` — the bare command's `in_progress` filter is never wanted, so the method exposes no flag for it. It must copy `list_goals`'s `_VAULT_NOT_FOUND_MARKER` branch (~lines 236-240) so an unknown vault raises `VaultNotFoundError` — requirement 4's degrade path depends on it. Mirror the existing methods' unbounded `await proc.communicate()`; `vault-cli` is a local binary and no existing client method sets a timeout, so adding one here would be the deviation, not the norm.

2. **Models** in `src/vault_ui/api/models.py`: add a `Topic` dataclass (`id`, `title`, `status`, `vault`) — `title` falls back to the topic `name`, since vault-cli emits no `title` (mirror how `_parse_goal` uses `data.get("title", goal_id)`); a `TopicDetail` dataclass for the `show` payload; and the response models `TopicResponse` (`id`, `title`, `status`, `vault`, `obsidian_url`) and `TopicDetailResponse` (exactly `TopicResponse`'s five keys plus `goals`, `tasks`, `unresolved`). Give every response model `model_config = {"extra": "forbid"}` — see `GoalResponse` (~line 106).

3. **Config** in `src/vault_ui/config.py`: add `topics_folder: str | None = None` to `VaultConfig` — **with that default**, so the keyword constructions at `config.py:145` and in the tests keep working. Populate it from `cli_vault.get("topics_dir")`. ⚠️ This field is **optional and must never cause a vault to be skipped.** `_build_vault_config` returns `None` when `tasks_dir` is missing or its folder is absent on disk; `topics_dir` must not join that condition — 12 of 14 vaults have no `topics_dir` at all and must still serve their tasks and goals.

4. **List endpoint** `GET /api/topics` in `src/vault_ui/api/tasks.py`: mirror `list_goals` (~line 1004) — same `vault` query parameter, same per-vault `asyncio.gather` fan-out, same `VaultNotFoundError` / `ValueError` degrade path. A vault whose `topics_folder` is `None` contributes an empty list and no error.

   **No cache.** `vault-cli watch` emits only `task`, `goal`, `theme` and `objective` kinds, so no watcher event can ever invalidate a topic cache, and a single `vault-cli topic list --all` over ~12 files is cheap enough to run per request. Do **not** add `app.state.vault_topic_cache` and do **not** thread a new parameter through `start_task_watchers`, `reload_config`, `run_config_reload_loop` or `reload_config_endpoint`. `_CACHE_TTL_SECONDS` is not used by this feature.

5. **Detail endpoint** `GET /api/topics/{topic_id}` in `src/vault_ui/api/tasks.py`: take a **required** `vault: str` parameter, mirroring `run_task` (~line 1124) — a topic name is a filename inside a per-vault `23 Topics/` folder and is not unique across vaults, so the vault cannot be inferred from the id. Resolve the per-vault client via `get_vault_cli_client_for_vault(vault)`. Return the topic's own fields plus its tracked work, parsed from the `content` field that `topic show` already returns. Do not read vault markdown from disk.

   Define an entry as the leading `[[wikilink]]` of each top-level `- ` bullet in the `## Goals` section (strip any `|alias`), stopping at the next `## ` heading. A bullet with **no** leading wikilink contributes no entry — skip it; do not count it as unresolved. Resolve each entry name against the vault's **unfiltered** goal names and the existing task cache: a name found among goals is a goal entry, a name found among tasks is a task entry, an unknown name is an unresolved entry. Return the three lists.

   ⚠️ Resolve against the unfiltered goal set, not `_process_goal_vault`'s return value — that helper applies `status_filter`, `assignee_filter` and a `defer_date` cutoff (~lines 934-958), so using it for name resolution would misclassify a deferred or filtered-out goal entry as `unresolved`. Use `_process_goal_vault` only for the goal list you return.

   ⚠️ **Do not derive the task list from the tasks' `goals:` frontmatter.** Measured 2026-09-30 on private-personal, the 12 topic pages hold 178 `## Goals` entries of which only 28 are goals and 150 are tasks, and the frontmatter derivation both floods and empties: Manager Layer's 5 member goals are named in the `goals:` frontmatter of **111** tasks while the page declares only 15 entries, and Work Approval's 10 entries yield **0** (its members are topic-direct, `goals: []`) — so the endpoint would report ten task names as goals and an empty task list.

   Error paths: an unknown `vault` returns 404, and a topic id that `topic show` cannot resolve returns 404 — never a bare 500. Follow `run_task`'s handling (~lines 1252-1262).

6. **Tests**, in `tests/test_api.py` (endpoints) and `tests/test_vault_cli_client.py` (client argv — create this file if absent; it does not exist today). Cover, at minimum:
   - the client builds the right argv for `list_topics` — assert `--all` is present;
   - the client builds the right argv for `show_topic` — assert the command is `topic show <id> --vault <v> --output json`;
   - a vault with no `topics_dir` is still returned by `_build_vault_config` with `topics_folder is None`, and is **not** skipped;
   - `GET /api/topics` on a vault with no topics folder returns 200 and `[]`;
   - `GET /api/topics` on a vault **with** topics returns 200 and entries carrying exactly `id`, `title`, `status`, `vault`, `obsidian_url` — this asserts the serialized key set; `extra: forbid` on `TopicResponse` separately guards construction;
   - `GET /api/topics/{topic_id}?vault=<v>` against a populated topic, exercising `TopicDetailResponse` and asserting a section entry that resolves to a goal lands in `goals`, one that resolves to a task lands in `tasks`, and an unknown name lands in `unresolved`;
   - the `## Goals` parser against a **trimmed** real body (frontmatter plus a `## Goals` section with 2–3 bullets — do not inline a full page; the real ones run 300 KB+), including a `- ` bullet with no leading wikilink, which contributes no entry; and against a body with **no** `## Goals` section, which must yield empty lists rather than raise.
   These are boundary tests: the parser, the config gate and the response models are where a value crosses into a stricter contract. Mock all subprocesses; the suite makes no real subprocess or network calls.

7. **CHANGELOG.md**: the file currently has no `## Unreleased` section (its head is `## v0.68.0`). Add one above the released headings with a single `feat:` bullet.

8. **README.md**: it documents the views and the config today, not the HTTP endpoints — add a new `## API` section documenting `GET /api/topics` and `GET /api/topics/{topic_id}`, placed after `## Goals view`.

9. Before finishing, re-run `<verification>`, confirm it passes, and walk each numbered requirement above against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- All vault access in **new** code goes through `vault-cli` subprocess calls. Never read or parse vault markdown from disk directly in code you add; the topic body arrives via `topic show`'s `content` field. (Existing direct-read paths such as `status_cache.py:_extract_fields` are sanctioned and out of scope.)
- `--all` on `vault-cli topic list` is mandatory. A bare `topic list` silently returns only `in_progress` topics and would make the completed-topic case untestable.
- Do not add a `Topics` entry to `HIERARCHY_SUFFIXES` — it would change goal and task discovery. Use `topics_folder`.
- Existing tests must still pass.
- Every new response model uses `model_config = {"extra": "forbid"}`.
- This prompt is backend-only. Do not touch `src/vault_ui/static/` — the frontend view is a separate prompt, not yet written.
- A prompt (rather than a spec) is a deliberate call here: the change is API plumbing against an existing, well-established view pattern, and it carries no new business rationale beyond the objective above.
- All paths in this prompt are repo-relative.
</constraints>

<verification>
Run `make precommit` -- must pass.
Then confirm the CHANGELOG heading exists: `grep -q '^## Unreleased' CHANGELOG.md`.
</verification>
