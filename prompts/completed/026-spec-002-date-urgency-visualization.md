---
status: completed
spec: [002-date-urgency-visualization]
summary: Added date-urgency colored left-border indicators and urgency-first sorting to Kanban task cards via getUrgencyTier() function in app.js and corresponding CSS classes in style.css.
container: task-orchestrator-026-spec-002-date-urgency-visualization
dark-factory-version: v0.55.1
created: "2026-03-16T14:00:00Z"
queued: "2026-03-16T13:50:42Z"
started: "2026-03-16T13:50:44Z"
completed: "2026-03-16T13:51:55Z"
branch: dark-factory/date-urgency-visualization
---

<summary>
- Task cards gain a colored left border that immediately signals temporal urgency without reading the date
- Overdue tasks (due_date before today) show a red left border
- Tasks due today (due_date equals today) show a yellow/amber left border
- Scheduled tasks (planned_date on or before today, not already red/yellow) show a blue left border
- Tasks with no dates, or only future dates, show no colored border (default appearance unchanged)
- Within each board column, tasks are sorted urgency-first (red → yellow → blue → none), then by existing priority within each tier (high → medium → low)
- Drag-and-drop continues to work on cards with urgency borders
- All colors are legible against the existing dark theme background
</summary>

<objective>
Add date-urgency visualization to the Kanban board: a colored left-edge band on task cards that encodes overdue/today/scheduled urgency, plus urgency-first sorting within each column. No backend or API changes are needed — `due_date` and `planned_date` are already in the API response.
</objective>

<context>
Read CLAUDE.md for project conventions.

Read these files before making any changes:
- `src/task_orchestrator/static/app.js` — find `createTaskCard(task)` (line 314), the sort block in `loadTasks()` (lines 263–267), and `normalizePriority(priority)` (line 600). The `task` object has `due_date` and `planned_date` fields (strings in YYYY-MM-DD format, or null/empty when absent).
- `src/task_orchestrator/static/style.css` — find `.task-card` (line 169) to understand existing card styling. The dark theme background is `#3a3a3a` for cards and `#1a1a1a` for the page. Existing column header border colors use `#94a3b8` (slate), `#60a5fa` (blue), `#a78bfa` (purple), `#fbbf24` (amber), `#f97316` (orange), `#34d399` (green) — urgency colors must not clash with these.

The `Task` API response already contains `due_date` and `planned_date` (confirmed in spec assumptions). Dates are YYYY-MM-DD strings when present, or null/undefined/empty when absent.
</context>

<requirements>
1. Add a pure function `getUrgencyTier(task)` to `app.js` (place it near `normalizePriority`, after line 622):

```javascript
/**
 * Returns the urgency tier for a task based on due_date and planned_date.
 * Tier values (lower = more urgent):
 *   0 = overdue (due_date before today, red)
 *   1 = due today (due_date equals today, yellow)
 *   2 = scheduled (planned_date <= today, but not overdue/due-today, blue)
 *   3 = no urgency (no applicable dates)
 */
function getUrgencyTier(task) {
    const today = new Date().toISOString().slice(0, 10); // YYYY-MM-DD

    const dueDate = task.due_date && /^\d{4}-\d{2}-\d{2}$/.test(task.due_date)
        ? task.due_date : null;
    const plannedDate = task.planned_date && /^\d{4}-\d{2}-\d{2}$/.test(task.planned_date)
        ? task.planned_date : null;

    if (dueDate && dueDate < today) return 0;   // overdue
    if (dueDate && dueDate === today) return 1;  // due today
    if (plannedDate && plannedDate <= today) return 2; // scheduled/actionable
    return 3; // no urgency
}
```

The regex `/^\d{4}-\d{2}-\d{2}$/` validates the date format — any malformed string falls back to `null` (treated as no date, tier 3). Both fields use the same validation rule.

2. Modify the sort in `loadTasks()` (lines 263–267) to sort by urgency tier first, then by priority within each tier:

Replace:
```javascript
        // Sort tasks by priority (high=1, medium=2, low=3, null=999)
        tasks.sort((a, b) => {
            const priorityA = normalizePriority(a.priority);
            const priorityB = normalizePriority(b.priority);
            return priorityA - priorityB;
        });
```

With:
```javascript
        // Sort tasks by urgency tier first (0=overdue, 1=due-today, 2=scheduled, 3=none),
        // then by priority within each tier (high=1, medium=2, low=3, null=999)
        tasks.sort((a, b) => {
            const urgencyA = getUrgencyTier(a);
            const urgencyB = getUrgencyTier(b);
            if (urgencyA !== urgencyB) return urgencyA - urgencyB;
            return normalizePriority(a.priority) - normalizePriority(b.priority);
        });
```

3. Modify `createTaskCard(task)` to apply a CSS class based on urgency tier. At the top of `createTaskCard`, after `card.dataset.taskId = task.id;`, add:

```javascript
    // Apply urgency border class
    const tier = getUrgencyTier(task);
    if (tier === 0) card.classList.add('urgency-overdue');
    else if (tier === 1) card.classList.add('urgency-today');
    else if (tier === 2) card.classList.add('urgency-scheduled');
    // tier === 3: no class, default appearance
```

4. Add CSS urgency styles to `style.css`. Append after the last rule in the file (after the `@keyframes spin` block):

```css
/* Urgency left-border indicators */
.task-card.urgency-overdue {
    border-left: 4px solid #f87171; /* red-400 — overdue */
}

.task-card.urgency-today {
    border-left: 4px solid #fbbf24; /* amber-400 — due today */
}

.task-card.urgency-scheduled {
    border-left: 4px solid #60a5fa; /* blue-400 — planned/scheduled */
}
```

Colors chosen for contrast on the dark theme (`#3a3a3a` card background, `#1a1a1a` page):
- Red `#f87171`: overdue, visually distinct from column header colors
- Amber `#fbbf24`: due today, matches the `ai_review` column header which is acceptable — these are different semantic elements (column vs card edge)
- Blue `#60a5fa`: scheduled, matches the `planning` column header — same rationale, different element types

5. Run `make precommit` and verify all tests pass. There are no Python backend changes, so test impact is limited to existing frontend-related tests (if any).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Existing card appearance (shape, spacing, background) must not change — the urgency indicator is additive; only `border-left` is modified, and only when a class is present
- Existing priority sorting behavior must not change — urgency sorting wraps around it (urgency tier first, then priority within tier)
- Existing drag-and-drop behavior must continue to work with urgency indicators — the CSS class is on the card element which already has `draggable` and drag handlers; adding a class does not affect drag behavior
- Dates are compared as YYYY-MM-DD strings using lexicographic comparison, which is correct for ISO date strings; no date parsing with `new Date()` is needed for comparison (avoids timezone issues)
- A task with both `due_date` and `planned_date` set: `due_date` takes precedence — if `due_date` matches today, the card is yellow (not blue), per the priority rule red > yellow > blue
- Malformed date strings (not matching `/^\d{4}-\d{2}-\d{2}$/`) must be treated as absent — no border applied, sorts into tier 3
- Null, undefined, and empty string date values must all be treated as absent — no border applied
- `getUrgencyTier` must be a pure function with no side effects
- The "today" string is computed once per call as `new Date().toISOString().slice(0, 10)` — this uses UTC which may differ from local date by up to one day; this is acceptable per the spec assumption that "browser clock is wrong" is a user issue, not an application bug
</constraints>

<verification>
Run `make precommit` — must pass.

Manual smoke test (no automated browser tests required):
1. Load the board with tasks that have: past `due_date`, today's `due_date`, today's `planned_date`, future dates only, and no dates.
2. Verify border colors: past due_date → red left border, today's due_date → yellow/amber left border, today's planned_date → blue left border, others → no colored border.
3. Verify sort order within a column: overdue task appears above due-today task, due-today appears above scheduled, scheduled appears above undated. Within the same tier, high-priority tasks appear above low-priority.
4. Drag a card with an urgency border to another column — confirm the drag works and the border persists after the reload.
</verification>
