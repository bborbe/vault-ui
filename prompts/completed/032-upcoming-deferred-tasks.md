---
status: completed
summary: 'Added upcoming task support: tasks deferred within 8h shown at bottom of Kanban lanes with grey border and reduced opacity; tasks deferred beyond 8h remain hidden'
container: vault-ui-032-upcoming-deferred-tasks
dark-factory-version: v0.57.5
created: "2026-03-18T14:11:34Z"
queued: "2026-03-18T14:11:34Z"
started: "2026-03-18T14:11:38Z"
completed: "2026-03-18T14:15:44Z"
---

<summary>
- Tasks deferred to a time within the next 8 hours are shown at the bottom of each lane, greyed out
- Tasks deferred beyond 8 hours from now remain hidden as before
- Upcoming tasks have a grey left-border indicator and reduced opacity so they are clearly distinct from active tasks
- The sort order within each lane is: active tasks first (by urgency/priority), upcoming tasks last
- Backward compatible: date-only defer_date values (YYYY-MM-DD) continue to work — they are treated as available at start of that day
</summary>

<objective>
Show tasks that will become available within the next 8 hours at the bottom of their Kanban lane, visually distinguished with a grey border and reduced opacity. Tasks deferred further than 8 hours remain hidden. This gives users visibility of upcoming work without cluttering the active task view.
</objective>

<context>
Read CLAUDE.md for project conventions and dark-factory workflow.

Key files:
- `src/vault_ui/api/models.py` — Task dataclass and TaskResponse Pydantic model; both need an `upcoming` bool field
- `src/vault_ui/api/tasks.py` — `_parse_defer_date` at line ~116 returns `date`; defer filtering at line ~183; `_task_to_response` at line ~545
- `src/vault_ui/static/app.js` — `loadTasks` renders cards per lane (~line 393); `createTaskCard` applies urgency classes (~line 461); `getUrgencyTier` returns 0-3 (~line 779)
- `src/vault_ui/static/style.css` — urgency left-border classes at line ~656: `urgency-overdue` (red), `urgency-today` (amber), `urgency-scheduled` (blue)

Existing imports in `tasks.py` line 8: `from datetime import date, datetime, timedelta` — needs `timezone` added.

The defer_date frontmatter field can contain either `YYYY-MM-DD` (date-only) or `YYYY-MM-DDTHH:MM:SS+HH:MM` (RFC3339 with timezone).
</context>

<requirements>
1. In `src/vault_ui/api/models.py`:
   - Add `upcoming: bool = False` to the `Task` dataclass after `blocked_by`
   - Add `upcoming: bool = False` to the `TaskResponse` Pydantic model between `blocked_by` and `vault`:
     ```python
     blocked_by: list[str] | None
     upcoming: bool = False
     vault: str
     ```

2. In `src/vault_ui/api/tasks.py`:
   - Add `timezone` to the existing datetime import: `from datetime import date, datetime, timedelta, timezone`
   - Replace `_parse_defer_date` with a new version that returns a timezone-aware `datetime`; update the signature from `def _parse_defer_date(defer_date: str) -> date:` to `def _parse_defer_date(defer_date: str) -> datetime:`:
     - If the string is date-only (`YYYY-MM-DD`): return `datetime(year, month, day, tzinfo=timezone.utc)` (start of day UTC)
     - If the string contains a time component: parse with `datetime.fromisoformat(defer_date)` and ensure timezone-aware (if no tzinfo, assume UTC)
   - Replace the defer filtering block (currently at ~line 183):
     ```python
     # Old:
     today = date.today()
     tasks = [t for t in tasks if t.defer_date is None or _parse_defer_date(t.defer_date) <= today]

     # New:
     now = datetime.now(timezone.utc)
     cutoff = now + timedelta(hours=8)
     visible_tasks = []
     for t in tasks:
         if t.defer_date is None:
             visible_tasks.append(t)
         else:
             defer_dt = _parse_defer_date(t.defer_date)
             if defer_dt <= now:
                 visible_tasks.append(t)           # available now
             elif defer_dt <= cutoff:
                 t.upcoming = True
                 visible_tasks.append(t)           # upcoming within 8h
             # else: hidden (defer > 8h away)
     tasks = visible_tasks
     ```
   - In `_task_to_response`, add `upcoming=task.upcoming` to the `TaskResponse(...)` constructor

3. In `src/vault_ui/static/app.js`:
   - In `loadTasks`, after sorting, split tasks into two groups before rendering:
     ```js
     const activeTasks = tasks.filter(t => !t.upcoming);
     const upcomingTasks = tasks.filter(t => t.upcoming);
     ```
   - Render `activeTasks` first into each lane container, then `upcomingTasks` after — so upcoming always appear at the bottom of each lane regardless of phase
   - In `createTaskCard`, after the existing urgency class block (after `tier === 3: no class`), add:
     ```js
     if (task.upcoming) card.classList.add('upcoming');
     ```

4. In `src/vault_ui/static/style.css`, add after the existing urgency block (~line 666):
   ```css
   .task-card.upcoming {
       border-left: 4px solid #9ca3af; /* gray-400 — upcoming/deferred */
       opacity: 0.55;
   }
   ```

5. Add or update tests in `tests/test_api.py` for the backend changes:
   - `_parse_defer_date` with a date-only string (`"2026-03-19"`) returns a timezone-aware datetime at midnight UTC
   - `_parse_defer_date` with a RFC3339 string (`"2026-03-19T16:00:00+01:00"`) returns a timezone-aware datetime
   - Filtering: task with `defer_date` in the past → active (upcoming=False)
   - Filtering: task with `defer_date` within 8h from now → upcoming=True, included
   - Filtering: task with `defer_date` more than 8h from now → excluded entirely
   - Filtering: task with no `defer_date` → active, unaffected
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Tasks with no defer_date are unaffected
- Date-only defer_date values (YYYY-MM-DD) must continue to work: they are treated as available at midnight UTC on that date
- Tasks deferred to a past date/datetime must still appear as active (not upcoming)
- The blocked_by filtering that follows defer filtering must still apply to both active and upcoming tasks
- All paths are repo-relative
</constraints>

<verification>
Run `make precommit` — must pass with no failures.
</verification>
