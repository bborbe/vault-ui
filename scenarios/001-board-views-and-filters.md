---
status: active
---

# Scenario 001: the three views, their filters, and the URL that encodes them

Validates that the board's Tasks / Goals / Topics views render the right cards, that every filter applies to the views it claims to, and that the URL is a faithful encoding of the state — so a deep-link and a reload land where the operator left off.

⚠️ **This scenario is walked by hand in a browser.** vault-ui has no browser harness yet (unlike attention-controller's `e2e/` Playwright suite), so nothing here is scripted. The harness is the follow-up; until it exists this is the only regression gate over the served page, which is exactly the gap that let the attention board ship a defect its suite could not see.

## Setup

- [ ] Repo checked out, `make build` run — the binary is at `~/Documents/workspaces/go/bin/vault-ui`
- [ ] A vault with at least two tasks in different statuses, at least one goal, and at least one topic — so every view has something to render and the status filter can actually discriminate
- [ ] Start the server with `make run`, then open `http://127.0.0.1:8000`
- [ ] Browser console open — a render-time exception in the poll loop reports itself as a connection problem, so the console is the only place the difference is visible

## Action

- [ ] Load the board with no query parameters
- [ ] Click the **Goals** toggle, then the **Topics** toggle, then back to **Tasks**
- [ ] Apply the **Vault** filter, then the **Status** filter, then the **Assignee** filter, clearing each before the next
- [ ] Reload the page while a non-default view and two filters are active
- [ ] Open `http://127.0.0.1:8000/?view=goals` directly in a fresh tab
- [ ] Toggle the **live** filter and wait for a vault file change to arrive

## Expected

- [ ] The default load renders the **Tasks** view, and the URL is either bare or carries `?view=tasks`
- [ ] Each toggle swaps the card set and writes the view into the URL as `?view=tasks` / `?view=goals` / `?view=topics` — the URL never keeps a stale view
- [ ] Goal cards carry a **⋮ menu**, not a read-only view: it offers Complete / Defer / Abort and Hold or Resume Goal, plus Reset Session when one is live, and each action posts to the goal route and re-renders the card — so the Goals view **can** mutate a goal, and that is what must be observed rather than denied (`app.js:2665-2671`). Flag and assign-to-me are separate card controls, not menu entries
- [ ] The **vault**, **status**, **assignee** and **live-session** filters apply to all three views — tasks and goals filter server-side, topics client-side (`matchesAssigneeSelection`, `app.js:1029-1032`), so the assignee filter **does** narrow the Topics view and its absence there would be a rendering fault
- [ ] A reload with a non-default view and filters active restores exactly that state — same view, same filter values, same card set
- [ ] `?view=goals` lands directly in the Goals view with **no flash through Tasks** first
- [ ] A vault change reaches the open board without a manual reload, and the card's column moves with it
- [ ] The browser console is clean: no uncaught exception, and no failed `/api/` request logged as a 5xx

## Cleanup

Stop the server. No temp directory is created by this scenario; the board only reads the vault.
