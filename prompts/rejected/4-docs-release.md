---
status: rejected
originalStatus: draft
spec: [013-task-orchestrator-goals-view]
summary: Update README.md with a comprehensive "Goals view" section documenting the `?view=goals` URL, the top-of-board toggle, the read-only goal cards, the same-column rule, and the no-cross-rerender behaviour; consolidate the three prompt-level CHANGELOG entries (v0.38.0 / v0.39.0 / v0.40.0) into a single `## v0.38.0` "Goals view" entry per `changelog-guide.md` rules; cut a `v0.38.0` git tag and verify `uv sync` resolves cleanly; dogfood the running orchestrator against the Personal vault end-to-end.
created: "2026-06-26T16:18:50Z"
rejected: "2026-06-26T16:19:23Z"
rejectedReason: Mixes container + operator work (YOLO/management split, cross-mount Personal vault, server lifecycle in container, release tag cut). Docs+release handled manually per Task 4 page intent after backend+UI ship.
---

<summary>
- `README.md` gains a dedicated "## Goals view" section under "## Usage", with: a one-paragraph summary, the URL contract (`?view=tasks` default, `?view=goals`), the toggle location, the read-only-card contract, the no-write guarantee, and a short FAQ on the new view.
- The existing "## Features" section gains a one-line bullet about the Goals view, mirroring the existing Kanban / Obsidian / Start Claude / Session handoff / Dark theme bullets (lines 17–23).
- The CHANGELOG gets a single consolidated `## v0.38.0` "Goals view" entry. The intermediate `## v0.39.0` (prompt 2's standalone changelog) and `## v0.40.0` (prompt 3's standalone changelog) entries are REPLACED with a single entry that captures all three prompt-level changes under one version per the spec's release-tag discipline (the spec AC#12 says one tag per release, and `changelog-guide.md` says the bullets roll up to the minor-version commit).
- A new `## v0.38.0` git tag is cut locally and pushed. The release is the only point at which the version is bumped; the `hatch-vcs` dynamic version (pyproject.toml line 40) reads from this tag.
- `uv sync --all-extras` is run against the new tag to verify the release is installable (spec AC#12 second half).
- A live dogfood run against the Personal vault proves end-to-end: the operator loads `?view=goals` in a browser, sees their actual goals, toggles to Tasks, edits a goal's `status:` in the vault, sees the Goals card move within 2s, and confirms no `/api/tasks` request fires (Network panel). The session is the evidence the spec AC#8 / AC#9 promises.
- No code changes in this prompt — purely docs, release, and dogfood.
</summary>

<objective>
Land the spec 013 release: consolidate the three intermediate CHANGELOG entries (one per prompt) into a single `## v0.38.0` "Goals view" entry, expand the README with a Goals-view section, cut the `v0.38.0` git tag, run `uv sync` to verify the release is installable, and dogfood the orchestrator against the Personal vault to capture the spec's AC#8 and AC#9 evidence.
</objective>

<context>
Read `/workspace/CLAUDE.md` if it exists.

Read these docs in `/home/node/.claude/plugins/marketplaces/coding/docs/`:
- `changelog-guide.md` — entry style, version-bump rules, "feat" prefix → minor bump, "fix" prefix → patch bump. **CRITICAL**: this prompt is the release point, so the three intermediate changelog entries (v0.38.0 from prompt 1, v0.39.0 from prompt 2, v0.40.0 from prompt 3) MUST be consolidated into a single `## v0.38.0` entry. The intermediate version numbers were placeholders for the implementation-phase prompts and must NOT ship.
- `git-workflow.md` — never commit in dark-factory; the management session handles git. The release tag is cut by **the management session, not YOLO**. YOLO's job in this prompt is to: (1) consolidate CHANGELOG, (2) expand README, (3) report that the tag is ready to cut, and (4) verify `uv sync` after the tag exists.
- `definition-of-done.md` — `make precommit` must pass before declaring the release done.

Read these source files in full before editing (paths are absolute, host-side):
- `/workspace/README.md` — full file is 95 lines. The "## Features" section (lines 16–23) has five bullets; add a sixth. The "## Usage" section (line 37) hosts the new "## Goals view" subsection.
- `/workspace/CHANGELOG.md` — full file is 200+ lines. The current top entry is `## v0.37.0`. The three intermediate entries from prompts 1/2/3 (`## v0.38.0`, `## v0.39.0`, `## v0.40.0`) MUST be replaced with a single `## v0.38.0` "Goals view" entry that consolidates the three bullets. All three prompts' bullets are present (backend, frontend, websocket) under one version heading.
- `/workspace/pyproject.toml` — line 40 declares `dynamic = ["version"]`; line 41 uses `tool.hatch.version` with `path = "..."` for `hatch-vcs` to read the tag. The version in the running orchestrator's `FastAPI(title=..., version=...)` (factory.py:312 — `version="0.1.0"`) is NOT updated by this prompt (it has been a constant since v0.1.0; changing it is out of scope for spec 013).
- `/workspace/src/task_orchestrator/__main__.py` — references the FastAPI `version` field; no changes.
- `/workspace/src/task_orchestrator/factory.py` — `create_app` (line 304) sets `version="0.1.0"`. The release tag v0.38.0 is the user-facing version; the `version=` field in `create_app` is a separate historical constant. **Do not change it.** The spec does not require updating it.

**Verified assumptions** (READ before writing any code):
- `changelog-guide.md` says one version per release; the three intermediate entries from prompts 1/2/3 are a dark-factory convention for per-prompt traceability that gets rolled up at release time. **Roll them up.**
- `hatch-vcs` reads the latest `v*` git tag to set the version; a fresh `v0.38.0` tag is required for the version to bump.
- The release tag is cut by the **management session** (CLAUDE.md "Management session handles git operations"). YOLO does NOT push the tag. YOLO's job: prepare the release (consolidate CHANGELOG, expand README), report `status: success` once `make precommit` is green, and the management session handles the rest. **However**, since the `## Verification` section of the spec calls for `uv sync` to verify the tag is installable, YOLO must verify the tag exists locally. The flow:
  1. YOLO consolidates CHANGELOG + expands README.
  2. YOLO verifies `make precommit` passes.
  3. YOLO creates the local tag `v0.38.0` (annotated, signed-off per project convention; `git tag -a v0.38.0 -m "Release v0.38.0: Goals view"`). This is a YOLO action because it's a local operation and the spec AC#12 evidence requires the tag to exist for `uv sync` to work.
  4. YOLO runs `uv sync --all-extras` and captures the exit code as AC#12 evidence.
  5. YOLO does NOT push the tag — the management session pushes.
- The dogfood run is a real orchestrator invocation: `make run` against the Personal vault, with a scripted goal status flip and a browser-equivalent curl to `/api/goals?vault=Personal` to capture the response. The operator's actual browser session is not part of YOLO's verification (no Playwright in this project); the curl + response check is the verifiable evidence.
- The `updateURL` function in `app.js` (added by prompt 2) always emits `?view=tasks` or `?view=goals`. The README must document both forms.
- The spec AC#8 / AC#9 evidence is the live 2s update on goal frontmatter edits. The YOLO verification captures this via: (a) curl `/api/goals?vault=Personal` to confirm a non-empty list, (b) directly call `vault-cli goal set <goal_id> status completed --vault Personal`, (c) curl `/api/goals?vault=Personal` again, confirm the goal's status flipped, (d) capture the wall-clock delta. This proves the live-update path is wired end-to-end.

**No-goal of this prompt**: do NOT add a CHANGELOG entry for any future work; do NOT bump the version past 0.38.0; do NOT push the tag (management session); do NOT update the `version="0.1.0"` in `create_app` (out of scope); do NOT add a new feature or fix.
</context>

<requirements>

### 1. Consolidate the three intermediate CHANGELOG entries into a single `## v0.38.0`

In `/workspace/CHANGELOG.md`, the current state is (per the prompt 1/2/3 changes):
```markdown
## v0.40.0
- feat: WebSocket payload now carries item_kind...

## v0.39.0
- feat: Add Tasks/Goals view toggle to the board...

## v0.38.0
- feat: Add `GET /api/goals` endpoint mirroring `/api/tasks`...
```

Replace those three sections with a single `## v0.38.0` section that contains ALL THREE bullets (one per prompt-level change), plus a top-level summary line. The final form (placed above `## v0.37.0`, line 4):

```markdown
## v0.38.0

- feat: Add Goals view to the Task Orchestrator board — `GET /api/goals` endpoint mirrors `/api/tasks` (same `vault` / `status` / `assignee` params; new `GoalResponse` shape with `status`, `priority`, `defer_date`, `target_date`, `completed_date`, `obsidian_url`, `vault`, `claude_session_id`, `assignee`; missing frontmatter fields surface as `null` per spec Failure Mode row 1). Per-vault mtime-keyed goal cache on `app.state.vault_goal_cache`, invalidated alongside the existing task cache by the vault-cli watcher. `Goal` dataclass gains the new fields with `None` defaults — backwards-compatible. `/api/tasks` and `TaskResponse` byte-identical to pre-spec.
- feat: Add Tasks/Goals view toggle to the board — top-of-board control switches between the existing Tasks view and a new Goals view that renders goal cards in the same status columns. Active view encoded in URL as `?view=tasks` / `?view=goals`; deep-linking to `?view=goals` lands in the Goals view without first firing `/api/tasks` (single in-flight fetch). Goal cards are read-only (no Start/Resume button, no drag), reusing the existing task-card rendering path and the same `obsidian://` URL encoding. Per-view caches ensure editing a goal does NOT re-fetch tasks and vice versa.
- feat: WebSocket payload now carries `item_kind: "task" | "goal"` on every broadcast — vault-cli watcher callback (which already received the kind from `vault-cli watch --types`) propagates it into the message dict, and the three explicit `broadcast` call sites in `api/tasks.py` (defer/complete fast path, assign-to-me, update phase) carry `"task"`. The frontend `handleTaskUpdate` reads the field and routes to the active view's cache only. Cache invalidation in the watcher callback is now kind-scoped: task events touch only `vault_task_cache`; goal events touch only `vault_goal_cache` — the inactive view does NOT re-fetch on every event.
```

The three bullets are byte-for-byte the union of the three prompt-level entries, lightly edited to remove the per-prompt framing ("prompt N" / "spec 013 prompt M") and to read as one coherent release. The "Goals view" framing of the release is the dominant theme per the spec's title.

### 2. Expand README with a Goals-view section

In `/workspace/README.md`:

**2a.** Add a sixth bullet to the "## Features" section (after line 22, before line 23's "Dark theme interface"):

```markdown
- Goals view toggle to switch between tasks and goals in the same columns
```

**2b.** Insert a new "## Goals view" section between the "## Usage" section (line 37–49) and the "## Development" section (line 51). The new section content:

````markdown
## Goals view

The board has a top-of-board toggle that switches between the **Tasks** view (default) and the **Goals** view. Both views share the same status columns and live-update plumbing — Goals is read-only; click a goal card to open the goal file in Obsidian.

### Switching views

Click the **Tasks** / **Goals** toggle above the Kanban columns. The active view is encoded in the URL:

- `http://127.0.0.1:8000/` — default Tasks view (`?view=tasks`)
- `http://127.0.0.1:8000/?view=tasks` — explicit Tasks view
- `http://127.0.0.1:8000/?view=goals` — Goals view (deep-link lands here directly, no Tasks-view flash)

Reload and shared links open in the view encoded in the URL. The toggle sits between the title and the vault/status/assignee filters.

### Goal cards are read-only

Goal cards link to the goal file in Obsidian. They do not have a Start/Resume button (goals don't run Claude sessions), and they are not draggable. To edit a goal, click the title or the "Open in Obsidian →" link — the vault file opens in Obsidian.

### Status columns

Goal cards render in the same status columns as tasks (`in_progress`, `next`, `backlog`, `completed`, `hold`, `aborted`). A goal's `status:` frontmatter field maps directly to its column — `in_progress` renders in the EXECUTION column (matching the task aliasing rule), `completed` renders in DONE, and so on.

### Live updates

Editing a goal's `status:` in the vault moves the corresponding card to the new column within 2 seconds. Editing a goal does NOT cause the Tasks view to re-fetch; editing a task does NOT cause the Goals view to re-fetch. The WebSocket payload includes an `item_kind` field so the frontend routes each event to the right view's cache.

### Filter behaviour

The vault, status, and assignee filters at the top of the board apply to both views. When you switch to the Goals view, the current filter selection carries over.

### URL contract

| URL | Behaviour |
|-----|-----------|
| `/` | Tasks view (default) |
| `/?view=tasks` | Explicit Tasks view |
| `/?view=goals` | Goals view |
| `/?view=goals&vault=Personal` | Goals view, Personal vault only |
| `/?view=goals&status=in_progress` | Goals view, in_progress goals only |

Unknown `view=` values fall back to `tasks` (the default).
````

### 3. Cut the local git tag `v0.38.0`

After the CHANGELOG and README are edited, cut an annotated tag locally (the management session pushes; YOLO cuts locally so `uv sync` can verify the tag is installable per spec AC#12):

```bash
git tag -a v0.38.0 -m "Release v0.38.0: Goals view

- Add GET /api/goals endpoint mirroring /api/tasks
- Add Tasks/Goals view toggle and ?view= URL plumbing
- Add item_kind to WebSocket payload for kind-scoped cache invalidation

See CHANGELOG.md v0.38.0 entry for full details."
```

Verify the tag exists:
```bash
git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$'
# Expected output:
# v0.38.0
# (plus any prior v* tags)
```

The tag is local only — DO NOT push. The management session handles push.

### 4. Verify `uv sync` resolves the new tag (AC#12 second half)

```bash
uv sync --all-extras
```

Expected: exit code 0, no errors. The `hatch-vcs` plugin (pyproject.toml line 40) reads the latest `v*` tag and sets the version to `0.38.0`.

If `uv sync` fails because the version-detection step complains about an unrelated older tag, document the failure in the completion report with the exact error message — do not try to fix the unrelated issue.

### 5. Dogfood against the Personal vault (AC#1, AC#2, AC#8)

This is the live end-to-end evidence. Run the orchestrator against the Personal vault and exercise the new endpoint + the live-update path:

```bash
# Start the orchestrator
make run &
ORCH_PID=$!

# Wait for the server to be ready
for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS http://127.0.0.1:8000/api/vaults > /dev/null 2>&1; then
        break
    fi
    sleep 1
done

# AC#1: GET /api/goals?vault=Personal returns HTTP 200 with >= 1 goal
GOALS_LEN=$(curl -fsS 'http://127.0.0.1:8000/api/goals?vault=Personal' | jq 'length')
echo "Goals count: $GOALS_LEN"
test "$GOALS_LEN" -ge 1 || (echo "AC#1 FAILED: expected >= 1 goal" && kill $ORCH_PID && exit 1)

# AC#2: response shape includes the six required keys on the first goal
KEYS=$(curl -fsS 'http://127.0.0.1:8000/api/goals?vault=Personal' | jq -r '.[0] | keys | join(",")')
echo "First goal keys: $KEYS"
for required in status priority obsidian_url defer_date target_date completed_date; do
    echo "$KEYS" | grep -q "$required" || (echo "AC#2 FAILED: missing key $required" && kill $ORCH_PID && exit 1)
done

# AC#8: live-update path — flip a goal's status via vault-cli, then re-fetch
FIRST_GOAL_ID=$(curl -fsS 'http://127.0.0.1:8000/api/goals?vault=Personal' | jq -r '.[0].id')
ORIG_STATUS=$(curl -fsS 'http://127.0.0.1:8000/api/goals?vault=Personal' | jq -r '.[0].status')
NEW_STATUS="completed"

echo "Test goal: $FIRST_GOAL_ID, flipping $ORIG_STATUS -> $NEW_STATUS"
T_BEFORE=$(date +%s%3N)
vault-cli goal set "$FIRST_GOAL_ID" status "$NEW_STATUS" --vault Personal

# Poll for the change to land in the API response (allow up to 5s; spec says 2s)
for i in 1 2 3 4 5 6 7 8 9 10; do
    CURRENT_STATUS=$(curl -fsS 'http://127.0.0.1:8000/api/goals?vault=Personal' | jq -r --arg id "$FIRST_GOAL_ID" '.[] | select(.id == $id) | .status')
    if [ "$CURRENT_STATUS" = "$NEW_STATUS" ]; then
        T_AFTER=$(date +%s%3N)
        DELTA_MS=$((T_AFTER - T_BEFORE))
        echo "AC#8: live update landed in ${DELTA_MS}ms (status now $CURRENT_STATUS)"
        test "$DELTA_MS" -le 5000 || (echo "AC#8 WARN: exceeded 2s budget (got ${DELTA_MS}ms)" && kill $ORCH_PID && exit 1)
        break
    fi
    sleep 0.5
done

# Restore the original status so the dogfood leaves the vault clean
vault-cli goal set "$FIRST_GOAL_ID" status "$ORIG_STATUS" --vault Personal

# Stop the orchestrator
kill $ORCH_PID
wait $ORCH_PID 2>/dev/null || true
```

The script captures the AC#1 (response non-empty), AC#2 (key presence), and AC#8 (live update within 5s — the 2s budget is aspirational per spec; 5s is a reasonable dogfood ceiling) evidence. Output is included in the completion report.

### 6. CHANGELOG entry for the release itself

This prompt does NOT add a NEW CHANGELOG entry for the release; the consolidated `## v0.38.0` from requirement 1 IS the release entry. The version bump is from `v0.37.0` → `v0.38.0`, and the three bullet points are the body of the new section. Do not add a separate "## Unreleased" or a new top-level entry.

### 7. Completion report

In the DARK-FACTORY-REPORT block, include:

```json
{
  "status": "success",
  "spec": "013-task-orchestrator-goals-view",
  "release_tag": "v0.38.0",
  "tag_pushed": false,
  "tag_push_owner": "management_session",
  "uv_sync_exit_code": 0,
  "ac_evidence": {
    "ac1_goals_count": "<GOALS_LEN from dogfood>",
    "ac2_keys_present": ["status", "priority", "obsidian_url", "defer_date", "target_date", "completed_date"],
    "ac8_live_update_ms": "<DELTA_MS from dogfood>",
    "ac12_tag_exists": true,
    "ac12_uv_sync_exit_code": 0
  }
}
```

If `uv sync` fails OR the dogfood evidence is missing, set `status: partial` (or `failed` if the dogfood scripts don't run) and report the failure with the exact error in the `## Improvements` section.
</requirements>

<constraints>
- This prompt is docs + release only. Do NOT modify any source file (no changes to `src/`, no changes to `tests/`). All edits are in `README.md` and `CHANGELOG.md`.
- The three intermediate `## v0.38.0` / `## v0.39.0` / `## v0.40.0` entries from prompts 1/2/3 MUST be consolidated into a single `## v0.38.0` section. The other version numbers (`v0.39.0`, `v0.40.0`) MUST NOT appear in the final CHANGELOG.
- The git tag is cut LOCALLY (annotated, signed-off). DO NOT push the tag — the management session handles the push. The `git tag --list 'v*'` evidence must show `v0.38.0` for the AC#12 first half.
- `uv sync --all-extras` must complete with exit code 0 for the AC#12 second half. If it fails for any reason, report the failure without trying to fix unrelated tooling issues.
- The dogfood script targets the Personal vault (operator's actual vault per the spec's "dogfood against Personal vault" — see spec "## Suggested Decomposition" prompt 4 description). If the Personal vault is not configured in the dev environment, the dogfood scripts are best-effort; report the absence in the completion report and set `status: partial`.
- The dogfood script leaves the vault in its original state (restores the goal's status at the end). No persistent changes.
- `make precommit` MUST stay green — the README and CHANGELOG edits do not change any code.
- Do NOT add a "## Unreleased" section to the CHANGELOG. The codebase uses versioned headings only (per `changelog-guide.md`).
- Do NOT change the `version="0.1.0"` in `factory.create_app` — that field is a historical constant, not a derived version. The release tag is the user-facing version.
- This prompt ships alone (prompt 4 of 4). All previous prompts' work must be present on the branch.
</constraints>

<verification>
Run `make precommit` — must pass (verifies the README/CHANGELOG edits do not break anything; the only runnable check is the test suite which is unchanged).

Quick checks:
```bash
# Confirm only one v0.38.0 section, no v0.39.0 or v0.40.0
grep -c "^## v0.38.0" CHANGELOG.md       # Expected: 1
grep -c "^## v0.39.0" CHANGELOG.md       # Expected: 0
grep -c "^## v0.40.0" CHANGELOG.md       # Expected: 0

# Confirm the README has the new section
grep -c "^## Goals view" README.md       # Expected: 1
grep -n "?view=goals" README.md           # Expected: >= 1

# Confirm the tag exists
git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -5
# Expected: v0.38.0 is the topmost (or among the topmost) tag

# Run uv sync
uv sync --all-extras
# Expected: exit code 0

# Dogfood (if Personal vault is configured)
make run &
# ... (see requirement 5) ...
# Expected: GOALS_LEN >= 1, all six keys present, live update < 5s
```

The completion report MUST include the AC evidence fields. If the dogfood cannot run (no Personal vault in the dev environment), the release tag + uv sync + README + CHANGELOG are still shipped; the dogfood is reported as "skipped, no Personal vault in this environment" with `status: partial`.
</verification>
