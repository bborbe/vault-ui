# Changelog

All notable changes to this project will be documented in this file.

## v0.84.2

- chore: Skip counterfeiter-generated `mocks/` in the PR reviewer's size gate, so machine-written fakes stop counting toward the park threshold — 50 of the 58 sibling repos already exclude them.

## v0.84.1

- fix: Run WezTerm by its absolute app-bundle path when the pane resolver lists panes, so a jump finds the session's pane when the board runs under launchd — Go's exec looks a bare `wezterm` up on the board's own PATH, which launchd leaves without the bundle dir, so prepending the bundle to the child's PATH alone never found the binary and every jump from the deployed board answered 409 "no pane resolves for this session".

## v0.84.0

- feat: Turn the page index's folder-level work into stat-diff work — `PageIndex.Refresh` and the rescan loop list each folder, compare every file's size, modification-time and status-change-time fingerprint with the one recorded at its last read, and re-read only the files that changed, appeared or disappeared, so a rescan of an unchanged folder reads nothing, keeps the identical snapshot slice and warns about an unchanged unparsable file once instead of on every pass (the status-change time still catches a rewrite whose size and modification time were restored); `MarkDirty` now marks a folder stale whose next read resolves by that same stat-diff rather than a full re-read, the new `MarkFileDirty(key, id)` marks a single file when vault-cli's lookup could only have written that exact file (the id stripped of its `[[`/`]]` wrapper, a plain base name with no path separator and no `..`, a byte-for-byte match in the current snapshot and an existing `<name>.md`) and otherwise widens to a folder-level mark, a per-file mark makes the next list read re-read exactly those files and blocks every concurrent reader until it has while `RefreshFile` events and `Refresh` rescans never block a reader, and `MarkAllDirty` is replaced by `ForceReload` — the only path that ignores fingerprints, behind `POST /api/cache/reload`, so the next read of every key re-reads every file; `pkg/pageindex/equivalence_test.go` now walks a create/modify/delete/rename sequence including a symlink target rewritten with no event through per-file updates and stat-diffs, asserting the snapshot `reflect.DeepEqual`s vault-cli's own `storage.PageStorage.ListPages` after every step.
- refactor: Read the page index's pages one file at a time through new injectable `pageindex.PageReader` and `pageindex.DirectoryLister` seams composed from vault-cli's exported `storage.ParseFrontmatterMap` and `domain.NewPage`, recording a size/modification-time/status-change-time fingerprint per file (platform stat fields, linux and darwin) that follows symlinks; the cold build now lists the folder and reads each entry through those seams instead of calling a folder-wide `storage.PageStorage`, still skipping a symlink out of the vault, a file without frontmatter and a file with invalid YAML with the same capped per-file warnings as vault-cli, and a new `pkg/pageindex/equivalence_test.go` proves the built snapshot `reflect.DeepEqual`s vault-cli's own `storage.PageStorage.ListPages` over a fixture holding awkward files (bare wikilink, no frontmatter, invalid YAML, non-markdown file, subdirectory, outside symlink, broken symlink, inside symlink, non-ASCII filename).
- feat: Update the page index one file at a time through a new `pageindex.PageIndex.RefreshFile(ctx, key, filename)`, which re-reads exactly one file and splices its page into a new copy-on-write snapshot at its filename-ordered position — the untouched pages keep the previous snapshot's pointers, a published snapshot is never mutated, and the file's current on-disk state decides the outcome (present and readable inserts or replaces, absent or unreadable removes) rather than the caller's reason for calling; every single-file read takes a per-index sequence at its start, so the most recently started read of a file wins and a read an older one already superseded is discarded, while reads of different files never drop each other's results, a `ListPages` on the key is never blocked by a file read, and the folder build merges its per-file reads into the snapshot in one pass so it never publishes a page a later-started read has replaced; a new `vault_ui_page_index_files_read_total{reason}` counter counts single-file reads with `reason` in `build`, `event`, `write`, `rescan` and `reload`, of which `build` (the folder build) and `event` (`RefreshFile`) are incremented today.
- feat: Update the board's page index per-file end to end — `pkg/watchrefresh` hands each task/goal watcher event's own file to the new `PageIndex.RefreshFile` (falling back to the folder stat-diff when the event's path is not a single plain filename directly inside the key's folder) and still broadcasts the event's frame only after a snapshot containing a read that started after that event has been published, while `pkg/mutations` applies the spec's write-mark table: the three queued post-write callbacks (task publishing, goal publishing, task session silent) mark only the written item's own file through the new `IndexInvalidator.MarkFileDirty` — which the index widens to a folder-level mark whenever the id does not name an existing file byte-for-byte, so vault-cli's case-insensitive substring fallback is still picked up — and the eight synchronous sites (`Run*`, `TakeOver*` and both `execute-command` routes) keep their folder-level marks of the vault's tasks and goals keys, so a queued single-item write costs one file read instead of a folder rebuild; `docs/page-index.md` gains an `Incremental updates` section and rewrites its staleness and frame-ordering rules for the stat-diff rescan, per-file write marks and the forced `POST /api/cache/reload`, and `docs/optimistic-writes.md` records the per-file frame timing.

## v0.83.1

- fix: Consume the board's own write's watcher echo once per item instead of suppressing watcher frames for a fixed 3 s window, so an echo arriving later than 3 s (~3.9 s observed on v0.83.0) no longer triggers an extra refetch; the next frame for the item is dispatched normally, and the expectation lapses after a 30 s ceiling when no echo arrives.

## v0.83.0

- feat: Apply the board's frontmatter writes optimistically through an in-memory per-vault queue (`pkg/queue`): the nine frontmatter-writing routes (task phase/status/flag/assign-to-me/session set/session clear, goal status/assign-to-me/session clear) validate synchronously, enqueue one write and answer 202 with today's body; each vault's consumer applies its writes one at a time in submission order without blocking other vaults, and only after the file is written invalidates the status cache, marks the page index dirty and publishes the `task_updated`/`goal_updated` frame; a failed write publishes a new `write_failed` frame (`task_id`, `item_kind`, `vault`, `reason`) and nothing else. The process-spawning, jump and reload routes are unchanged.

## v0.82.0

- feat: Keep the board's live-session state current from harness session-registry file events in a new `pkg/sessionstate` package — the registry ids are read once at startup, re-read on every fsnotify event under `~/.claude/sessions`, and re-read every `DefaultRescanInterval` (60 s) as the safety net for a missed event — so the Live badge and the jump control's availability follow file events instead of a timer; a real change pushes the existing watcher frame (two per configured vault, `task` and `goal`) to connected browsers so an idle board updates in under a second instead of waiting for its 60 s poll, `sessionSignals` reads the ids from that state instead of opening the registry directory per request while the per-request `ps` scan stays on the request path, and a failing or panicking event source is logged at `glog.V(2)` and swallowed so the rescan keeps the state current and the board keeps serving; `factory.CreateSessionStateWatcher` wires it into `main.go` and `docs/pane-resolution.md` records when a pane is resolved and how fresh session state is.
- feat: Resolve a live Claude session's WezTerm pane in Go in a new `pkg/pane.Resolver`, which reads the harness session registry's current name for the session (`~/.claude/sessions/<pid>.json`) and matches it against the `wezterm cli list --format json` pane titles after stripping each side's leading status glyph, replacing the `python3 who-needs-me.py --pane-for <sid8>` shell-out and deleting `pane.ResolvePaneID` and `pane.WhoNeedsMePath`; the process boundary is an injected `ExecFunc` so no test spawns a subprocess, and an ambiguous, missing or unparsable match answers "no pane" rather than guessing. `factory.CreatePaneResolver` wires it into both the jump route and the pane refresher, so the seam's `Resolve(ctx, sessionID) (string, bool)` shape is unchanged for `pkg/board`, `pkg/panecache` and `pkg/mutations`.
- refactor: Delete `pkg/panecache` and the three-second background pane refresher, removing the ~95 % CPU burn it caused and with it every Go-to-Python shell-out; `factory.CreatePaneCache`, `factory.CreatePaneRefresher` and `liveSessionIDs` are gone, and `CreateAPIHandler` now takes a `mutations.PaneResolver` in place of the pane cache and hands it to `CreateMutationService`.
- refactor: Drop the pane id from the board's read model — `api.TaskResponse.JumpPane` (the `jump_pane` JSON field) is deleted rather than emitted empty, `board.Deps.Pane`, the board's `PaneResolver` interface and its `resolvePanes` pass are removed, so a task list read carries no pane id and makes zero pane-resolver calls; `POST /api/tasks/{task_id}/jump` resolves the pane on demand through the Go resolver at the moment it is clicked, keeping its 409/503/502 error contract.
- feat: Offer a task card's jump control from live session state alone instead of the removed `jump_pane` field, so a live task whose pane cannot currently be resolved still shows the control and the failure is reported by the toast when it is clicked; a goal card never renders the control because `/api/goals/{id}/jump` does not exist, the badge keeps its take-over onclick, the stale `style.css` comment is reconciled, and both `index.html` cache-bust tokens move to `2026-10-06-lazy-pane-jump` so no warm browser keeps the pre-change `app.js`.

- feat: Re-baseline the Go↔Python parity harness for lazy pane resolution, in which a task's WezTerm pane is resolved in Go when the card's jump control is clicked instead of by a three-second background refresher that shelled out to `who-needs-me.py` per live session — `pkg/panecache` and every Go→Python shell-out are deleted and the pane id is gone from the task list response, the live badge and the jump control follow session state kept current by the new `pkg/sessionstate` watcher over `~/.claude/sessions/*.json` (an initial read, file events, and a 60 s rescan as the safety net) so neither depends on a timer or on pane data while the `docs/liveness-classification.md` contract is unchanged, and the harness itself is re-baselined for the intentionally dropped field: `normalize()` in `scripts/parity/parity.sh` removes `jump_pane` from both sides of the `/api/tasks` body comparison behind a type guard that leaves the single-key error bodies and the non-object mutation fallback intact, the static-asset check stays a live byte comparison of the two backends' served `src/vault_ui/static/` tree, and `docs/go-cutover.md`'s § 6 regression guard derives its baseline from the commit that removed the frontend's `jump_pane` read instead of the pinned `798d901`, so `make parity` passes with every ratio full while every other route's body, status codes and WebSocket frames stay byte-identical to the Python reference.

## v0.81.0

- feat: Add `pkg/pageindex`, a process-wide in-memory snapshot store of each vault folder's parsed pages that implements vault-cli's `storage.PageStorage`, with one immutable snapshot per `(vaultPath, pagesDir)` key, per-key shared builds so concurrent cold or dirty readers wait on a single `ListPages` call, dirty marks for write invalidation, event-triggered rebuilds that coalesce into one in-flight build plus one follow-up and signal completion only after a post-event swap, a clock-driven rescan loop bounded by `RescanInterval` (50 s), and retention of the previous snapshot on a failed rebuild; the staleness, frame-ordering and key-derivation rules are written down in `docs/page-index.md`.
- feat: Serve the board's list reads (`/api/tasks`, `/api/assignees`, `/api/goals`, `/api/topics`, and the goal/task lists inside `/api/topics/{id}`) from a single process-wide `pageindex.PageIndex` built concurrently over every configured vault's tasks, goals and topics folders at startup and rescanned at least every 50 s, so a warm request answers from memory instead of re-parsing every vault file; `ShowTopic`'s own topic-file read and the mutation paths still read disk.
- feat: Refresh the page index from watcher events in a new `pkg/watchrefresh` handler wired through an injectable watch operation in `factory.CreateWatcher`, so a task or goal edit made outside vault-ui rebuilds only that event's `(vault path, folder)` key and its WebSocket frame is broadcast only after a rebuild that started after the event has been swapped in — a client that re-fetches on the frame sees fresh data; theme and objective frames, and frames for an unknown vault, are broadcast unchanged without touching the index.
- feat: Mark the page index dirty on every vault-ui write and on `POST /api/cache/reload`, so the board reads its own writes: the mutation service takes a narrow `IndexInvalidator` dependency, every task and goal mutation marks its vault's tasks and goals keys before returning (and before any `Publish*Updated` frame, so a client reacting to the frame never re-fetches stale data), and the cache reload marks every key dirty; `JumpTask` and `ReloadConfig` write nothing and mark nothing.

## v0.80.0

- feat: Resolve WezTerm pane links in a new `pkg/panecache` background refresher instead of on the request path, so `GET /api/tasks` reads pane ids from an in-memory cache and no longer spawns one `who-needs-me.py` helper subprocess per live session per request (measured 7.7s on 2026-10-05); the board's `Deps.Pane` is now the cache, which a `run.Func` refreshes every three seconds, leaving the jump route's on-demand resolution unchanged.

## v0.79.5

- fix: Render an empty goal status as an empty string rather than null, matching the Python backend's JSON type for that field.

## v0.79.4

- docs: Point `docs/go-cutover.md` and `docs/launchd-service.md` at the log path the service actually writes (`~/Library/Logs/vault-ui.log`, or `/Users/YOUR_USER/…` in the plist template) instead of `/tmp/vault-ui.log`, which nothing writes. The cost landed at the worst moment: the runbook's own STOP messages, read when a cutover or rollback has *already* failed, named a file that does not exist. Adds the `mkdir -p ~/Library/Logs` step the doc was missing — launchd does not create the parent directory of `StandardOutPath`, and `/tmp` always existed.

## v0.79.3

- fix: Render Go board JSON date-time fields with Python's microsecond precision (six fractional digits, omitted when the microsecond component is zero) so the parity harness compares them literally.

## v0.79.2

- fix: Bind the factory test suite's HTTP server to a run-time free port instead of the fixed admin port, so the suite passes on a machine where the shipped service already holds that port.

## v0.79.1

- docs: Record in `docs/go-cutover.md` § Known limits that `go` is not on `PATH` inside `bborbe/claude-yolo:v0.15.1` — it lives at `/usr/local/go/bin`, so the runbook's `make parity` step dies in `build_vault_cli` with `make: *** Error 127` before any comparison runs, which reads as a harness bug and is not one. The section now shows the invocation that works, and notes that `-e PATH=…` on `docker run` does not survive `bash -lc`.

## v0.79.0

- feat: Raise the process's own file-descriptor limit at startup via the new `pkg/fdlimit` package (`TargetLimit`/`Raise`), so the in-process vault watcher can hold roughly one descriptor per watched file across every configured vault regardless of how the service was launched; a limit that cannot be raised is logged as a warning naming the applied limit and the possible incomplete vault watching, and never aborts startup.

## v0.78.2

- docs: Add `docs/go-cutover.md`, the operator runbook for the final cutover — repointing the launchd LaunchAgent from the uv-installed Python tool to the Go binary, covering the pre-flight in-flight-launch check, `make build`, the plist edit, restart, the verification probes (including the running-process check that distinguishes a real cutover from an unapplied plist edit), board exercise, the static-tree regression guard, and rollback.

## v0.78.1

- docs: Mark the Python backend superseded in `src/vault_ui/README.md` (kept in-tree for one rollback window, frontend and tests frozen) and file its removal as `specs/ideas/remove-superseded-python-backend.md`.

## v0.78.0

- feat: Serve the Go `:8000` API surface — the six read routes (vaults, assignees, tasks, goals, topics, topic detail) with byte-identical embedded static serving, and the eighteen mutating routes (task/goal session lifecycle, execute-command, phase/flag/status/assign-to-me, session set/clear, the jump proxy, and cache/config reload) — reproducing the Python backend's status codes, bodies, guards, and vault-file side effects.
- feat: Call vault-cli in-process through its exported Go library for every read and write; no vault-cli subprocess is spawned on any route.
- feat: Inject a board-event publisher into the mutating handlers and publish exactly the mutations the Python backend broadcasts, so the WebSocket layer can plug in without touching the handlers.
- test: Extend the parity harness with a mutation-parity section that resets the fixture vault, runs each write route against the Python backend and then against the Go backend, and diffs the status, the normalized body, and the resulting vault-file tree; add a third injected-divergence build tag that skips a vault write and prove the harness rejects it.
- feat: Serve the live-update WebSocket at `/ws` from the Go backend — a bounded, non-blocking connection manager (concurrent-client cap, per-client buffered send queue that drops a slow client rather than stalling the broadcast) fed by vault-cli's in-process file watcher and by the mutating routes' event publisher, answering a client `ping` with `pong` and emitting the same watcher and mutation frames as the Python backend.
- test: Extend the parity harness with a ws-parity section that connects a client to each backend, drives the same watcher change and route-originated broadcast, and compares the normalized frame sequences (asserting a non-empty result); add a fourth injected-divergence build tag that corrupts a WebSocket frame and prove the harness rejects it.

## v0.77.0

- feat: Serve the six read-only vault-ui routes (`/api/vaults`, `/api/assignees`, `/api/tasks`, `/api/goals`, `/api/topics`, `/api/topics/{topic_id}`) from the Go backend at the frozen paths and query-parameter names, reproducing the Python response shapes, filters, and derived fields (blocked, upcoming, recently_completed, session_state, activity_date, obsidian_url).
- feat: Serve the frozen frontend from `src/vault_ui/static/` byte-identically at `/`, with explicit path canonicalization that refuses traversal with the same 404 the Python backend returns.
- feat: Add the `make parity` harness, which boots the Go and Python backends against a disposable fixture vault and compares route sets, normalized bodies, error shapes, static hashes, and a traversal probe; `make parity-selftest` proves the harness rejects an injected route rename and an injected response-body divergence.
- feat: Retarget `make build` to write the binary to `~/Documents/workspaces/go/bin/vault-ui` and make the API listen address settable via `VAULT_UI_LISTEN` (default `:8000`), leaving the fixed `:9090` admin block unchanged.

## v0.76.0

- feat: Port the session liveness, activity-date, and display-name resolution logic to Go packages, reproducing the Python classifications and refusals.
- feat: Port the process-termination guards to Go, issuing SIGTERM only for a matched claude launch row and treating a vanished process and a permission failure as non-fatal.
- feat: Port the process-local launch registry and session-lock registry to Go, preserving their state machines, holder-count eviction, and single-process assumption.
- feat: Port vault hierarchy discovery, the vault-ui/vault-cli config merge, and the status cache to Go, preserving folder ordering, the absent-tasks-folder skip, and YAML marker normalisation.
- feat: Port the vault-cli watcher supervisor and the WezTerm pane resolver to Go, with the restart/stop lifecycle, the bounded helper, and the jump-credential handling preserved.
- feat: Port the cleanup sweep policy to Go — the session-id retention invariant plus the empty-id re-bind, orphaned-marker TTL, and resurrected-marker re-clear passes — and capture the retention invariant in docs/.
- fix: Repair the pane resolver and watcher supervisor tests — realistic subprocess timeouts, and arming the watcher helper's SIGTERM trap before it signals readiness — removing load-dependent flakes that made `go test ./...` and `make test` fail intermittently.
- fix: Guard the watcher supervisor's subprocess handle with the existing mutex, removing a data race between Stop and the subprocess start that surfaced under `make test`'s -race run.

## v0.75.1

- fix: The board's Start button now approves a task still in the `todo` phase before opening its session, so starting a todo card no longer fails with `vault-cli work-on ... task is at phase "todo"`. The Start click is the operator's own approval surface; cards past approval take the unchanged path with no extra write.

## v0.75.0

- feat: Add the Go module foundation for vault-ui — composition root serving the canonical admin block on :9090 (healthz, readiness, metrics, setloglevel, gc).
- feat: Depend on vault-cli as a pinned library and discover configured vaults through its config loader; readiness now reports ready only after discovery succeeds.
- feat: Wire every vault-cli operation (list, show, set/clear field, work-on, defer, complete, and the goal/topic variants) through its exported ops constructors, assembled into a single op set by the composition root.
- chore: Run the Go suite (build, format, vet, test) from the Makefile alongside the Python suite; add a make build target producing the Go binary on the host toolchain.

## v0.74.2

- fix: The `↗` jump on a live task card reaches the running session again when the board runs as a launchd service. launchd starts the service with no `WEZTERM_UNIX_SOCKET`, so the pane-resolution helper's `wezterm cli` talked to `~/.local/share/wezterm/sock` — a separate mux server holding almost none of the operator's panes — and the route answered 409 "no pane resolves for this session". The helper is now spawned with the newest `~/.local/share/wezterm/gui-sock-<pid>` whose process is still alive (the GUI socket name changes on every WezTerm restart, so it cannot be pinned in the plist); a stale socket from an exited GUI is never chosen, and an explicitly set `WEZTERM_UNIX_SOCKET` still wins.

- fix: The WezTerm GUI-socket scan now ignores a `gui-sock-<suffix>` name whose suffix is not made only of ASCII digits, instead of raising. `str.isdigit()` accepts Unicode digits such as `²`, which `int()` then rejects with `ValueError`, so a stray file like `gui-sock-²` in `~/.local/share/wezterm` crashed pane resolution rather than being skipped like every other non-matching name; such names are now filtered out before the pid is parsed.

## v0.74.1

- fix: The `↗` jump control now renders on live task cards on the deployed board. The launchd service runs with a fixed, minimal `PATH` that does not include the WezTerm application bundle, so the pane-resolution helper could not list panes and every live task resolved to no pane — the board showed no jump control anywhere, while the identical code run from a terminal resolved a pane for every live task. The helper is now spawned with an environment whose `PATH` begins at the WezTerm bundle, so the board carries its own dependency resolution rather than depending on the service's `PATH`.

## v0.74.0

- feat: A live task card now carries a `↗` jump control beside its `● Live` badge, so the operator can move from the board to the running session in one click. The control is deliberately a separate element rather than a second action on the badge: the badge's click takes the session over and ends the running turn, so folding navigation into it would make a click meant to jump kill the session. It renders if and only if the payload carries a `jump_pane`, so a live card whose session resolves to no reachable pane shows no control at all rather than a dead one; the pane id itself is never written into the DOM, since the client does not need it and the server owns it. Clicking it POSTs to the jump route and does not navigate, reload or re-render — the operator's focus has already moved and the board must stay exactly where it was. Both `app.js` and `style.css` cachebust tokens are bumped with it.

## v0.73.0

- feat: A live task can now hand the operator straight to the session it belongs to. The board already said a session was running but gave no way to reach it, because reaching it needs the fleet-jump credential and that credential cannot be published in the served page. The jump is therefore proxied by the server, which reads the credential and never returns it: `POST /api/tasks/{task_id}/jump` activates the session's WezTerm pane and answers `204` with no body, and every task response now carries a derived `jump_pane` for live sessions so a card whose session resolves to no pane offers no control rather than a dead link. The pane is resolved fresh on each request, never cached, because a pane id is recycled across tab moves and WezTerm restarts. The route accepts only same-origin requests, so a page the operator merely visits cannot POST to it and move their focus.

## v0.72.0

- fix: The session chip is removed, and the `app.js` cachebust token bumped with it. The chip duplicated what the card's action area already says — `● Live`, `⏳ Starting…`, `▶ Resume` and `▶ Start` encode the same four states — and the one state it alone conveyed (`indeterminate`, 2 cards) did not justify a chip on the rest. `quiet` was the weakest case: it appeared on roughly half the board and does not mean "orphan", since a task whose session ended because the work finished is `quiet` too, so it could not be scanned for. The liveness work this series was for is unaffected — the `● Live` badge now correctly marks a session the registry lists, which is what the board was missing.

## v0.71.3

- fix: The `app.js` cachebust token is bumped again, because v0.71.2 changed `app.js` and left it byte-identical to v0.71.1 — so the chip fixes would not have reached a browser that had the board open across the previous deploy, exactly as happened with v0.71.0. This is the second occurrence; nothing in the repo currently enforces the bump, so it stays a thing a human has to remember.

## v0.71.2

- fix: A card whose launch turn is in flight no longer shows a session chip beside its `⏳ Starting…` badge. A session that started seconds ago has no transcript yet, so it classified `indeterminate` — leaving the card asserting both that it was starting and that its liveness could not be determined. The chip now defers to the Starting badge on exactly the condition the button helper uses, so the two cannot disagree.
- fix: The session chip no longer renders for `none`. That state is the default — the absence of a session rather than a finding about one — and it is the majority of the board, so the chip was appearing on most cards to say nothing. The chip now covers only `quiet` (a session id is set but nothing is running, i.e. an orphan) and `indeterminate`: the two states the board cannot otherwise show.

## v0.71.1

- fix: The board's `app.js` cachebust token is bumped, so the session chip and "Live session" filter added in v0.71.0 actually reach a browser that had the board open across the deploy. That release changed `app.js` but left the token byte-identical, and the token is what the browser keys its cache on — so a normal reload kept serving the pre-change script and only a hard refresh picked the change up.

## v0.71.0

- feat: A task or goal whose Claude session is alive but idle now renders `● Live` instead of `quiet`. Liveness was derived from transcript recency (a five-minute window) plus a `--resume`/`--session-id` process scan, so a worker that had not written its transcript in five minutes and had not been launched with a resume flag looked identical to an orphan whose session had exited — measured against the deployed board on 2026-09-30, of 147 open tasks carrying a `claude_session_id` only 14 were present in `~/.claude/sessions/` and just 6 rendered live. The harness's own session registry is now merged in as a third, authoritative signal: a session the registry lists is running, so it reads live even when its transcript has gone stale, and even when no transcript exists on this host at all (previously `indeterminate`). The four states, the `● Live` badge and every other endpoint are unchanged — this adds a signal to `session_state`, never a field beside it. `GET /api/tasks` also gains a `session_live` query parameter that restricts the list to live rows, composing with the existing `vault`/`status`/`phase`/`assignee`/`goal` filters.
- feat: The board surfaces that signal. Every task card now carries a session chip for the states the board could not previously show at all — `quiet` (a session id is set but nothing is running, i.e. an orphan), `indeterminate`, and `none` — with `live` left to the existing `● Live` badge so that a card never carries the state twice. The toolbar gains a "Live session" filter that narrows the board to tasks with a running session. The filter is applied **server-side** through the same `session_live` parameter, so the rows the board renders and the rows the API returns come from one query and cannot drift — the control narrows the list rather than merely rendering.

## v0.70.0

- fix: A topic card's **title** now opens the topic in Obsidian, and a footer button opens the tracked-work detail — the same two affordances a task or goal card carries. Shipped the other way round first: the title opened the detail and the 📝 icon opened Obsidian, the reverse of the board's established pattern (a task card's title in `cardShellHtml` and a goal card's title both carry the Obsidian URL). Operator-reported in review, on a topic in a different vault, after the view had already been deployed.

## v0.69.1

- fix: Opening a topic now shows that topic's own status in the detail view, not only the work it tracks. The Topics card carried the status, but the modal — where the operator lands after clicking through — did not, so the one screen dedicated to a single topic was the one screen that omitted its status. Rendered from the topic's own frontmatter, the same value the card uses; a topic declaring no status renders no badge rather than an empty pill. Found by driving the deployed build with Playwright, which the hermetic suite structurally cannot do: it mocks `vault-cli` and asserts against a fixture, so it proves the view renders given data but never what the live board actually shows.

## v0.69.0

- feat: Topics are now served over the API — `GET /api/topics` lists a vault's topics and `GET /api/topics/{topic_id}?vault=<v>` returns one topic plus the work it tracks, so the board can render the vault hierarchy's one remaining unserved layer. `list_topics` always passes `--all` (the bare `vault-cli topic list` filters to `in_progress` and would silently hide every completed topic) and each topic carries the status read from its own file. The detail endpoint parses the `## Goals` section of the `content` `topic show` already returns — never vault markdown from disk, and never the tasks' `goals:` frontmatter, which both floods and empties (measured 2026-09-30 on private-personal, the 12 topic pages hold 178 entries of which 28 are goals and 150 are tasks, while Manager Layer's 5 member goals are named in 111 tasks' frontmatter and Work Approval's 10 entries carry `goals: []`) — and classifies each entry as a goal, a task, or unresolved, resolving against the vault's *unfiltered* goals and tasks so a deferred or filtered-out goal is not misreported as unresolved. `vault` is a required parameter on the detail route because a topic name is a filename inside a per-vault `23 Topics/` folder; an unknown vault or an unresolvable topic id is a 404, not a 500. The new `VaultConfig.topics_folder` comes from vault-cli's optional `topics_dir` and never gates the vault: 12 of 14 vaults have none and still serve their tasks and goals, contributing an empty topic list. No cache — `vault-cli watch` emits no `topic` kind, so no watcher event could ever invalidate one. `HIERARCHY_SUFFIXES` is unchanged; the topics folder is read from config, not discovered by suffix.
- feat: A **Topics** view joins Tasks and Goals on the board. Topics are the containers every manager loop is scoped to, and they were the last layer of the vault hierarchy with no view — reaching one meant opening Obsidian and reading its `## Goals` section by hand. Each topic card renders the status from that topic's own file, so a topic reads `in_progress` or `completed` as it actually is and never as a placeholder; opening one lists the work it tracks, split into goals, tasks and unresolved names, each linking straight into the existing Goals or Tasks view rather than duplicating it. Topics group by status like Goals, and a vault with no topics folder renders an empty view instead of an error. Verified in a real browser by `tests/test_topics_view.py` — five Playwright cases run by `make test-integration`, covering the differing per-topic statuses, the goal/task/unresolved split, the entry links, and the empty-vault case.

## v0.68.0

- feat: A flag set from the board now records the operator as its writer. The flag toggle's `PATCH /tasks/{id}/flag` set path passes `--by operator` to `vault-cli task set`, so the flag carries `flag_set_by: operator` instead of reading as an unattributed write; clearing the flag is unchanged (`task clear` takes no `--by`), as is every other `set_field` caller. Requires vault-cli >= 0.156.0 — below that floor the toggle returns HTTP 500 on `unknown flag: --by`.

## v0.67.6

- fix: The `vaults:` block in config.yaml is now optional. An absent or empty block serves every vault-cli vault that has a tasks folder — meaning `tasks_dir` is set *and* that folder exists on disk — so vault-ui no longer needs a hand-maintained second copy of vault-cli's vault list, which is what took the board down on 2026-09-18 when vault-cli's config was cleaned up and vault-ui's stale copy still named the removed vaults (`KeyError: 'tasks_dir'` at startup, crash-looping the launchd service). A vault-cli entry with no `tasks_dir`, no `path`, or whose tasks folder is missing on disk is now skipped with a warning naming the vault instead of raising, on the explicit path as well as the fallback, so a block naming a task-less vault degrades rather than crashes. An explicit non-empty block keeps its role unchanged: it still filters (a vault-cli vault not named there stays off the board) and still overrides `vault_name`, and a vault it names that vault-cli does not know is still skipped with the existing warning. The "No vaults configured after merging with vault-cli output" startup failure is preserved for the misconfiguration it was written for — nothing survived the merge — with a message that now names the two real skip reasons. Note that removing the block *widens* the board: vaults that were never listed (e.g. `boss`, `starcitzen`) now appear, and the resulting vault list follows vault-cli's order rather than the block's YAML order.

## v0.67.5

- fix: Renaming a vault in vault-cli no longer takes the whole board down until someone restarts vault-ui. A vault that vault-cli no longer knows is now skipped with a `Vault '%s' not found in vault-cli output, skipping` warning on `/api/tasks`, `/api/goals` and `/api/assignees` — each renders the surviving vaults instead of returning HTTP 500 — while every other vault-cli failure (timeout, disk error, non-JSON output) still fails loudly, because the new `vault_cli_client.VaultNotFoundError` is raised only when vault-cli's stderr carries the `vault not found` marker. The vault list is also re-read automatically every 30 seconds by a new `factory.run_config_reload_loop`, so a rename is picked up without a restart; the loop calls `load_config()` off the event loop via `asyncio.to_thread`, survives a `load_config()` that raises, and calls `reload_config()` only when the vault set actually changed so the `vault-cli watch` subprocess is not churned (and live updates not dropped) on every tick. `↻ Refresh` and `POST /api/config/reload` are unchanged.

## v0.67.4

- fix: The 5-minute cleanup sweep can no longer be stalled indefinitely by a single task, and a task is never re-bound to a session belonging to a different vault that happens to share its title. The re-bind pass's per-task lock acquisition is now bounded by a new `_LOCK_ACQUIRE_TIMEOUT_SECONDS` (10s, separate from the `_SET_FIELD_TIMEOUT_SECONDS` that bounds the calls made while holding the lock), so a stuck acquisition skips that task and the sweep moves on instead of halting every later pass in every vault; the candidate uuid must additionally have its `<uuid>.jsonl` transcript in *this* vault's Claude project dir, since `-n <name>` carries no vault component and the live map is host-global; and the under-lock re-read now also abandons the write when the task's assignee changed since the list was snapshotted, which is the only place that invariant can be closed because `set_task_session` performs no assignee check. The pass is extracted to `_rebind_empty_session_ids` so these properties are testable directly, a non-zero `task set` return code is logged at WARNING like the sibling display-name repair, and an unexpected failure there now reports a traceback instead of a bare one-line warning. `vault_ui.activity._cached_live_session_names` is promoted to `cached_live_session_names` — it already had two foreign consumers.
- fix: A task whose `claude_session_id` was wiped — e.g. by a peer's cleanup on a git-synced shared vault — is now re-bound from its title by the 5-minute cleanup sweep, so the board offers `▶ Resume` for work already in progress instead of `▶ Start`. The pass only fires when exactly one session is running right now under that title: an ambiguous title (two live sessions share it) or an absent one writes nothing, a non-empty binding is never overwritten, a task owned by another user or mid-launch is skipped, and a deliberately released binding is not resurrected because a released session is no longer running. The write re-reads the task under the same per-task lock the API's `set_task_session` uses, so a binding that landed since the sweep listed the vault is kept.
- test: The three re-bind timeout tests now pin each assertion to the lead phrase of the clause it means to exercise, so a timeout attributed to the wrong clause fails the suite instead of passing. `test_rebind_lock_acquire_timeout_skips_the_task_and_continues`, `test_rebind_reread_timeout_logs_warning_and_writes_nothing` and the subprocess-timeout test previously matched on substrings (`task id` + `timed out`, `task id` + vault) that the re-read, write and lock-acquisition warnings all satisfy; each now additionally requires its own clause's lead phrase. The write clause's message in `vault_ui.cleanup._rebind_empty_session_ids` gains the `Re-bind write` lead phrase so the three timeout warnings are mutually distinguishable — wording only, no change to the clauses, their ordering, their timeouts, or the kill-and-reap.

## v0.67.3

- refactor: Vault UI now runs a single `vault-cli watch` subprocess covering every configured vault (one comma-joined `--vault` value) instead of one subprocess per vault, so the watcher process count no longer scales with the number of displayed vaults. `VaultCLIWatcher` takes `vault_names: list[str]`, `start_task_watchers` builds one watcher and logs one `Started vault-cli watcher for vaults: …` line, and the watcher callback resolves each event's `vault` back to its `VaultConfig` via the new `factory.resolve_vault_for_event` (an event naming an unconfigured vault is logged at debug and ignored rather than raising). Cache invalidation, WebSocket payload shape and session resolution are unchanged.

## v0.67.2

- fix(ui): Task and goal card footers no longer shred into four ragged rows. `.card-footer` was a single non-wrapping flex row, so a wide action (e.g. `⏳ Starting... 0:00`) claimed its intrinsic width first and squeezed `.card-footer-left` into a narrow column — the flag, Jira badge, assignee+priority and activity age each wrapped onto a row of their own while the action sat vertically centred against all four (observed on a 258px card: badges needed 245px, 238px available). Four changes: `.card-footer` now wraps with `.card-footer-left` on `flex: 1 1 auto` + `min-width: 0` and `.card-actions` on `margin-left: auto`, so the action drops to its own right-aligned row instead of squeezing the badges; the activity age moved into `.card-actions` so it wraps with the button rather than being orphaned as the smallest trailing badge; the Jira key moved out of the badge row onto its own `.card-jira` line above it, since at ~84px it was the single widest badge and the reason every Jira-backed card overflowed; and the width-critical row was tightened (flag padding `0.25rem`→`0.125rem`, priority chip `7px`→`6px`, actions gap `0.5rem`→`0.375rem`). Measured across 296 task cards and 292 goal cards at a 258px card width: single-row footers 90 → 220, and cards showing three or more footer rows 208 → 0.
- fix(ui): The Obsidian `↗` arrow can no longer be orphaned onto a line of its own below the card title. The template placed it after a whitespace-producing newline, so a title that filled its last line let the browser break before the arrow (19 of 298 live cards). New `titleWithIconHtml()` splits the title and wraps the final word plus the arrow in a `white-space: nowrap` span, so the two wrap together or not at all; both halves of the split still go through `escapeHtml`, guarded by a new test. Measured 19 → 0 orphaned arrows.

## v0.67.1

- fix(ui): The "blocked by" badge no longer pushes a card's ▶ Start button past the card edge. It rendered inside `.card-footer-left`, which shares a `flex` + `nowrap` row with `.card-actions` and had no `min-width: 0`, so a long blocker name pinned the row at its max-content width and the button was clipped (observed on a 298px card: badge 246px + actions 90px, button 22px outside the card). The badge now takes its own `.card-blocked` row between the title and the footer, so the full blocker name fits on a normal-width card and the action button stays inside; `max-width: 100%` with `nowrap` + ellipsis is the backstop for labels naming several blockers, with the full text kept in the `title`.

## v0.67.0

- feat: A task or goal whose `blocked_by` blocker is still open now stays visible on the board instead of silently disappearing, and the API reports which blockers are still open — `GET /api/tasks` and `GET /api/goals` derive a `blocked` flag and a `blockers` list naming the not-completed blockers (in `blocked_by` order) from the status cache, treating an unknown or unreadable blocker status as blocked; goals now parse `blocked_by` from frontmatter with the same normalization tasks already had.
- feat(ui): Blocked task and goal cards now carry a clickable "blocked by X" badge naming their still-open blockers (rendered as escaped plain text), and clicking it scrolls to and marks the blocker's own card on the same board — with an error toast instead when the blocker is not shown by the current filters; unblocked cards are unchanged.

## v0.66.3

- fix: A take-over no longer answers the abandoned Start request with a 500 carrying vault-cli's raw `exit status 143`. v0.66.2's bind watch takes seconds and ran *before* `_clear_starting_marker` — the call that sets the taken-over flag the still-pending Start's handler checks. The launcher exits ~1s after the SIGTERM, so that handler found the flag unset and the neutral 409 regressed to a 500. The marker clear (and with it the flag) now runs first, then the watch. Caught by driving the take-over on the deployed board, which is the only place the pending Start and the take-over are both live.

## v0.66.2

- fix: A take-over now keeps the session id for the whole window the killed launcher can clear it. v0.66.1 bound only when the ps-resolved uuid differed from the frontmatter, and verified once after a 0.4s settle — both wrong, and a driven take-over on the deployed board showed it: `work-on` persists the id *before* spawning, so the ids usually already match and no bind ran at all (that take-over returned in 0.099s having written nothing), while the launcher's compensating clear lands ~1s after the SIGTERM (measured live: the field was intact at t+0.5s and gone at t+1.0s), so a verify that finishes sooner reads the field as fine and returns before the clobber. The bind now runs unconditionally whenever a session id is known and watches the field for ~3s, re-writing it whenever it goes missing; exhaustion is logged at WARNING rather than failing the take-over.

## v0.66.1

- fix: A take-over no longer loses the session id to the launcher it just killed. The take-over binds the ps-resolved uuid into `claude_session_id` so the card can resume it, but its own SIGTERM makes `vault-cli work-on` answer the failed turn with a compensating clear — re-read the task, delete `claude_session_id` plus that run's metrics entry, "a failed turn must not leave a resumable-looking id on disk" — and that write can land after ours. When it did, the card fell back to `▶ Start` and the id survived only in the modal the operator had just closed (observed live 2026-09-11 12:30:46: the take-over returned 200 with `terminated=True`, yet the file kept no id and its uncommitted diff was exactly `metrics_sessions: []`). The bind now waits for the launcher to settle, re-reads, and re-binds if the id was clobbered — bounded at 3 attempts, then logged at WARNING rather than failing the take-over.

## v0.66.0

- fix: Taking over a Starting card no longer leaves the card on `⏳ Starting...`. The badge renders when *either* the server marker or the browser-side `startingTasks` / `startingGoals` set says starting, and only the Start path ever removed the id from that set — so a take-over cleared the marker server-side while the stale client flag put the badge straight back on the next render, making the click read as a no-op (observed live 2026-09-11: three take-overs returned 200 and cleared their markers, yet the cards stayed on Starting). `takeOverSession` now clears the client flag and the cached marker on both the success and the error path, whose "refresh so the stale badge does not stay" intent the same flag had been defeating.

## v0.65.2

- fix: A restart no longer strands cards on `⏳ Starting...` for 45 minutes. Restarting the service kills the launches vault-ui spawned — they are its subprocesses — and the coroutines that would have cleared their `claude_session_started` markers die with it, so nothing flipped the cards back and they waited for the TTL sweep (observed live 2026-09-11: a deploy killed two in-flight launches). The server now reconciles those markers at startup: a marker is cleared when the registry has no record for the item, it is past a 2-minute grace period, and no `--session-id` launch process for the item exists on this host — a restart recovers the board in seconds. A marker written by a peer machine in a shared vault also has no local launch process and can be cleared early (the TTL sweep would clear it at 45 minutes); only the display is affected, `claude_session_id` is untouched.
- fix: Taking over a Starting card can no longer end a session someone is working in. The take-over resolved its target from every live claude process, so a card whose session id was pinned by an *interactive* `--resume` — the shape a card takes after its launch died and the operator reopened the session in a terminal — would have SIGTERMed that session. Only a launch (`--session-id`) process is a take-over target now: when the pinned process is an interactive resume, take-over clears the marker and hands back the resume command without killing anything.

## v0.65.1

- fix: Taking over a Starting card no longer paints a red "vault-cli work-on failed … exit status 143" error across the board. The take-over SIGTERMs the launch while the original Start request is still pending, so vault-cli exits 143 — which is the take-over working, not a launch failure. The launch registry now flags a taken-over launch, and the launch endpoints answer that abandoned request with HTTP 409 "Launch ended by take-over from the wall — resume the session from the take-over modal"; the wall renders a 409 on the Start path as a neutral toast (no error styling) and reloads the card. Task and goal cards alike; the marker is still cleared, and the resume command still comes from the take-over modal.

## v0.65.0

- feat: `↻ Refresh` now re-reads the server config (`~/.config/vault-ui/config.yaml` + `vault-cli config list`) and reconciles the per-vault watchers (`POST /api/config/reload`) before reloading the view, the vault selector and the assignee options — registering or removing a vault is a config edit plus this click, with no launchd restart. A config that fails to load raises before anything is torn down, so a broken edit leaves the running board untouched.
- fix: The WebSocket live-update channel now works under the uvicorn CLI entry point (`uvicorn vault_ui.__main__:app` — what `make watch` and the documented worktree-on-:8001 recipe run): the connection manager is wired in `create_app()` instead of only in `main()`, so those invocations no longer reject every `/ws` connection with "Connection manager not initialized" and silently fall back to the 60s poll.

## v0.64.0

- feat: A card stuck on `⏳ Starting...` can be taken over from the wall — the badge itself is the affordance (the same discreet model as the live `● Live` badge): click → confirm → the in-flight launch process is SIGTERMed, the `claude_session_started` marker is cleared (so the card leaves "Starting…" at once instead of waiting for the 45-minute TTL sweep), and the resume command for the ended session is shown. The launch is resolved from `ps`: the card's own session id when a live process pins it, else the `-n <title>` launch row — which is what catches a relaunch whose fresh uuid the frontmatter never caught up with (observed live 2026-09-11: frontmatter `769563ff…` vs launch `--session-id 7e486b43…`); that uuid is written back so the card and the resumed session agree. Take-over reports whether a process was actually found (`terminated`), and the badge tooltip carries the elapsed time plus the card's last-activity age, because a launch's transcript goes quiet for minutes while one of its subagents works. Task and goal cards share the flow; live and quiet cards stay unchanged.

## v0.63.7

- fix: Task and goal card buttons (Resume, take-over, ⋮ menu, assignee filter, assign-to-me, flag) now escape titles/ids/vaults/assignees for the single-quoted-JS-string-inside-HTML-attribute context (`escapeJsAttr`). A title containing an apostrophe — e.g. "…Peer Machines' Session Bindings…" — previously terminated the inline onclick handler, producing a silent SyntaxError on click: the Resume button and card menu did nothing (no modal, no toast). `escapeHtml` alone was insufficient because it decodes `&#39;` back to `'` before the JS parser runs.

## v0.63.6

- fix: A task or goal whose session runs on another machine no longer loses its `claude_session_id` to a peer's cleanup sweep — the sweep now clears a valid UUID only when THIS instance launched the session (a `LaunchRegistry` record exists for the item) AND its transcript file is gone, so a binding assigned to another user or one with no local transcript and no registry record (a peer's session) is retained instead of cleared and published by the vault autocommit, and the board stops offering "Start" for work already running elsewhere.

## v0.63.5

- fix: A session whose `-n <name>` is the final command-line argument now binds to its session id — the `cc-*` launcher scripts put `-n <task name>` last (after `--resume <uuid>`), and the ps-row name matcher's terminator required a flag after the name, so such a session never mapped and the board kept the task's display-name `claude_session_id`, unable to resolve it to a UUID or show Live. The matcher now also terminates at the end of the line (trailing whitespace tolerated), so launcher-started sessions resolve instead of keeping a display name forever; mid-argv `-n` shapes are unchanged, and a bare `-n` or a `-n` directly followed by a flag still produces no mapping.

## v0.63.4

- fix: The background cleanup pass can no longer be frozen by a stuck `vault-cli task set` helper — the display-name repair now times out after 10s, kills and reaps the helper, logs a warning naming the task and vault, and leaves `claude_session_id` on disk untouched for the next sweep to retry — and two concurrent `PATCH /api/tasks/{id}/session` requests for the same task can no longer both slip past the UUID-overwrite guard: the read-check-write is serialised by a per-task lock, so exactly one write lands and the other is refused with HTTP 409 (a best-effort guard against vault-ui's own handlers, since the launched Claude session, obsidian-git and git-rest also write the field — see `docs/starting-marker-lifecycle.md`).
- fix: A task's recorded session is no longer discarded when its name cannot be resolved — the cleanup sweep now repairs a resolvable display-name `claude_session_id` to its UUID and leaves an unresolvable one on disk untouched (previously it cleared every non-UUID within five minutes), and `PATCH /api/tasks/{id}/session` refuses with HTTP 409 to overwrite a task's existing valid session UUID, naming both ids and pointing the caller at `DELETE /api/tasks/{id}/session` to release it first — so a task whose session name collides with another keeps its binding and the board no longer offers "Start" for a task that already has a session running.

## v0.63.3

- fix: The board now recognises sessions it started itself and binds a task to the right session even when several sessions share its name — a headless `--session-id <uuid>` launch counts as live (previously read as idle and offered a duplicate Start), and a task whose display name is shared by 2+ transcripts resolves to the running process's uuid from the process table instead of refusing as ambiguous; take-over can signal those headless processes too.

## v0.63.2

- fix: The Start-button admission gate now counts only launches in flight (cards showing "Starting…"), not open sessions — a task counts only when its durable `claude_session_started` marker is set and the `LaunchRegistry` does not record the launch as finished (a resurrected marker no longer counts), so `max_concurrent_sessions` limits simultaneous Start-button launches instead of the total number of running sessions

## v0.63.1

- fix: A relaunch that starts while the cleanup sweep is clearing a resurrected `claude_session_started` marker for the same `(vault, item_id)` keeps its `LaunchRegistry` record — the post-clear eviction now drops the record only if it is still FINISHED (`evict_if_finished`), so a `begin()` that lands during the awaited `task clear`/`goal clear` subprocess is not undone by the sweep and the relaunch stays protected against a resurrected marker.
- fix: The cleanup sweep now re-clears a `claude_session_started` marker that a concurrent git merge (obsidian-git pulling a `git-rest` commit) restored after the launch's own clear — at most once per finished `LaunchRegistry` record, re-clearing via the existing `task clear`/`goal clear` subprocess form, evicting the record only once the marker is confirmed gone from the file (successful clear or already absent), and logging a failed re-clear at WARNING with vault + id + error so the next pass retries — so a finished launch can never re-surface "Starting…" and the in-memory registry stays bounded.
- fix: `GET /api/tasks` and `GET /api/goals` now suppress `claude_session_started` for any task/goal whose launch the server's in-memory `LaunchRegistry` records as finished, so a concurrent writer restoring the marker (e.g. an obsidian-git merge) can no longer leave a card stuck on "Starting…"; the frontmatter marker remains the fallback across a server restart (and an in-flight launch still reports it), and the list endpoints never re-clear — disk convergence stays with the cleanup sweep.
- fix: `run_task`/`run_goal` now record in-flight/finished launches in a process-local `LaunchRegistry` — the server-authoritative "Starting…" signal for the follow-up list-endpoint fix — begun before the durable `claude_session_started` marker is written and finished exactly once per launch on success or failure; a marker clear that fails is logged at WARNING with vault + id + error instead of being swallowed by `suppress(Exception)`.

## v0.63.0

- feat: Cap Start-button session launches at the configurable `max_concurrent_sessions` limit (default 20, set in config.yaml); excess clicks are refused with HTTP 429 (hard refuse, no queue) naming the current count and cap, surfaced as a toast by the existing error path

## v0.61.1
- Fix two ruff errors in `api/tasks.py` flag endpoint (unused `vault_config` assignment, `raise` in `except` without `from`) that broke `make lint` on master and failed CI for three consecutive runs including the v0.61.0 release

## v0.61.0

- feat: tasks carry a frontmatter `flag` field ("picked for today"). Flagged cards sort to the top of every column under all sort modes (default / priority / last modified) and render with an amber ring; the card-footer toggle sets/clears the flag via `PATCH /api/tasks/{id}/flag`, writing through `vault-cli task set/clear flag`. Needs a vault-cli release exposing `flag` in `task list`/`show` JSON.

## v0.60.1

- fix(ui): A page reload during a session launch no longer shows the `● Live` take-over badge on a task/goal that is still booting. `isStarting` is now gated on the durable `claude_session_started` marker alone (not `!claude_session_id`), because the assistant's session-connect writes `claude_session_id` mid-turn — before the headless turn finishes — so an id present under a set marker is the launch, not a resumable session. To keep the marker meaning exactly "launch turn in flight", `run_task`/`run_goal` now also clear it on success (previously only on failure/reset), and the stale-marker cleanup sweep now clears expired markers on id-bearing tasks too (migrates launches that predate the clear-on-success change). Cache-bust token bumped to `2026-09-01-starting-marker-wins` so the fixed gate reaches the wall.

## v0.60.0

- fix: `resolve_session_id` now matches a display name only against a session's **current** title (the last `custom-title` entry in its transcript), never a title the session used to have, and an ambiguous name — two or more sessions currently sharing the same title — resolves to `None` instead of whichever file the filesystem listed first, so Resume can no longer be pointed at a conversation that worked a different task. The ambiguity is logged at warning level with all tied session ids; callers keep the display name in frontmatter for a human to resolve.

## v0.59.0

- feat(ui): Make the live card's `● Live` badge itself the take-over affordance — the separate `⚡ Take Over` button is removed for a more discreet card. The badge (now `role="button"`, pointer cursor, hover outline) opens the same confirm dialog ("End the running turn and resume this session? In-flight work is lost unless already saved"); Cancel still performs no action, confirming still SIGTERMs the matched `claude --resume <uuid>` process and returns the resume command. Cache-bust token bumped to `2026-08-31-live-badge-click` so the new badge reaches the wall.

## v0.58.1

- fix(ui): Bump the `app.js`/`style.css` cache-bust token to `2026-08-31-goal-take-over`. v0.58.0 removed the task-only gate on the goal take-over button without bumping the token, so browsers that cached the v0.57.0 `app.js` (which carried the gate) under the unchanged `?v=2026-08-31-take-over` URL never refetched and kept hiding the goal button. This token change forces the refetch so the goal take-over affordance actually reaches the wall.

## v0.58.0

- feat(ui): Live **goal** cards now carry the same `⚡ Take Over` affordance as task cards — the follow-up half of the take-over feature (v0.57.0 shipped the task side). A live goal's `● Live` badge previously had no button; now clicking it shows the same confirm dialog ("End the running turn and resume this session? In-flight work is lost unless already saved"), Cancel performs no action, and confirming calls the goal take-over endpoint that SIGTERMs the matched `claude --resume <uuid>` process and returns the normal resume command. Covered by API tests (terminate, no-session, 404) and Playwright integration tests (goal affordance + confirm flow).

## v0.57.0

- feat(ui): A live task card now offers a `⚡ Take Over` affordance instead of being untouchable — previously the `● Live` badge had no button and a live session could not be resumed from the wall at all (a plain resume is flock-refused on the vault-cli path, or starts a second claude on the same transcript on the launcher path). Clicking it shows a confirm dialog ("End the running turn and resume this session? In-flight work is lost unless already saved") whose Cancel performs no action; confirming calls a new take-over endpoint that SIGTERMs the matched `claude --resume <uuid>` process (reusing v0.56.1's narrow `ps` matcher, now also resolving the PID), releasing the per-session flock, and returns the normal resume command. A quiet session keeps its `▶ Resume` unchanged; live goal cards keep the bare badge (goal take-over ships in a follow-up). Covered by unit tests for the PID parser + terminate helper, API tests for the task take-over endpoint (terminate, no-match, no-session, 404, leading-dash), and Playwright integration tests for the affordance + cancel path + confirm flow.

## v0.56.1

- fix(liveness): The wall no longer wrongly offers Resume on an open-but-idle session. `classify_session_state` cross-checks a stale transcript against `ps` for an exact `claude --resume <uuid>` process (the narrow matcher from `~/.claude/scripts/fleet-sessions.py`): a session whose process is alive but paused (e.g. launched via `cc-personal --resume <id>`) stays `live` instead of flipping to `quiet` after 5 min, so the wall keeps Resume hidden and is not invited to start a second claude on the same transcript. Recency remains the signal for everything else (fresh headless turns, remote/container sessions); the `ps` scan is cached ~30s, not per-request-per-card. Covered by unit tests for the ps matcher.

## v0.56.0

- feat(ui): The wall now hides Resume on a card whose session is live (transcript written within ~5 min) — a green `● Live` badge holds the spot — and disables it when the session state is indeterminate (session id set but no transcript found, e.g. a manual terminal `/resume` in another cwd or a cloud/container session). A quiet session keeps the normal Resume. The classification is transcript-recency-only, surfaced as a new `session_state` field on task/goal responses (see `activity.py`), and agrees with vault-cli's per-session flock (v0.118.1): a session the wall shows as live is the same one the launch path refuses to start. Covered by unit tests for the classifier plus a new Playwright integration test (`make test-integration`).

## v0.55.2

- fix(cleanup): Sweep orphaned "Starting" markers on **goals** too. The goal loop filtered on `claude_session_id`, so a goal whose marker was orphaned by a mid-launch server restart was never examined — the same defect the task sweep fixed in v0.55.1, left in the sibling code path. `run_goal` writes the marker via `set_goal_field`, so goals orphan exactly like tasks do. Reads from `StatusCache` for the same reason the task sweep does: `vault-cli goal list --output json` does not emit the field. Its cache handle is resolved independently of the task block's, which is a separate `try`, so an early failure there cannot turn the goal sweep into a `NameError`.

## v0.55.1

- fix(cleanup): The orphaned-"Starting"-marker sweep added in v0.55.0 never matched anything. It read `claude_session_started` off the `Task` objects returned by `VaultCLIClient.list_tasks()`, but `vault-cli task list --output json` does not emit that field at all — the key is absent, so every task carried `None` and the sweep skipped all of them. The API endpoint only sees the field because it enriches from the `StatusCache` after listing; the sweep now reads the same source. Verified against the live vault: v0.55.0 logged `cleared 0` with 6 orphaned markers present.
- test: Lock the sweep's data source. The v0.55.0 tests exercised `_marker_age_seconds` in isolation and never ran `cleanup_stale_sessions`, so a sweep that matched nothing still passed them. The new tests build the task the way the CLI really does (marker `None`) with the marker only in the cache, and assert the clear happens — mutation-checked to fail against the v0.55.0 code.

## v0.55.0

- fix(ui): A card no longer sits on `⏳ Starting...` after its `claude_session_id` has landed. The websocket reconnects after a drop but never replayed the events missed during the gap, and `ws.onopen` did not re-fetch — so the tab rendered its stale copy until manually refreshed. It now calls `loadCurrentView()` on reconnect (guarded so the first connect does not duplicate the page load's fetch). The window this exposes grew from ~10s to the full 2-5 min turn when vault-cli v0.117.1 began persisting the session id only after the headless turn finishes, which is what made it visible.
- fix(cleanup): Restore crash-recovery for orphaned "Starting" markers. The launch endpoint clears `claude_session_started` in its own `except` when a launch fails, but cannot cover the server restarting mid-launch; the existing sweep only inspected tasks that already had a `claude_session_id`, so an orphan was never examined and the card stuck on `⏳ Starting...` indefinitely (17 such tasks were live when this was found, several 11h and 6d old). A new sweep clears a marker with no session id once it exceeds a 45-minute TTL. The TTL is deliberately **not** the 15 minutes that shipped in July: vault-cli now blocks up to its own 30m `sessionTurnTimeout`, so any TTL at or below 30m would clear the marker out from under a live turn and bounce the card to Start mid-work. A test locks the TTL above 30m.
- feat(ui): `⏳ Starting...` now shows elapsed time (`⏳ Starting... 1:46`), so a running turn is distinguishable from a dead one at a glance. `claude_session_started` carries an ISO-8601 launch instant instead of the literal `"true"` — any non-empty value stays truthy, so the Start/Starting/Resume gate is unchanged, while the sweep gains an age to expire and the badge a number to render. Legacy `"true"` markers degrade to the bare label and are treated as expired by the sweep, which clears the already-stuck cards on the first pass. Bumps the `app.js` cache-bust token.
- test: Assert the `app.js` cache-bust token by shape rather than by literal value in all three tests that checked it. Pinning the literal made every legitimate bump fail, training the bump to be skipped — and an un-bumped token is how this repo previously shipped a fix that browsers never received.

## v0.54.0

- feat(ui): Stop prompting for a close-out reason on Complete — completing a task or goal (Complete menu action, and dragging a card into Done / Completed) now closes out directly with no reason modal, and the request bodies carry no `reason` / `gate_successor` fields. This matches the abort-only backend contract: the API accepts a reason-free completion and drops any supplied `reason`/`gate_successor`, no `--reason`/`--gate-successor` flag reaches vault-cli, and completed task/goal files stay free of `aborted_reason`/`gate_successor`. Abort is unchanged — it still opens the reason modal (reason mandatory, HTTP 400 on blank, gate-successor defaulting to `none`) and sends both fields through `patchStatus`. Frontend half of the abort-only close-out change, matching the sibling vault-cli fix; bumps the `app.js` cache-buster token so already-open boards fetch the new script.
- feat(api): Restrict the close-out reason gate to `aborted` — `completed` task/goal close-outs (complete-task, complete-goal, status → completed, phase done auto-status) now proceed without a reason and pass no `--reason`/`--gate-successor` flags to vault-cli, which no longer requires `aborted_reason`/`gate_successor` for completion (sibling vault-cli fix). Supplied close-out fields on a completed request are dropped deterministically. The `aborted` contract is unchanged: a missing/blank/whitespace reason still fails fast with HTTP 400 naming `reason` before any write starts, and the abort argv still carries both flag pairs. Backend half of the abort-only close-out change; the frontend (stop prompting on Complete) ships in a follow-up.

## v0.53.0

- feat(ui): Closing out a task or goal — Abort and Complete via the card menu, and dragging a card into Done (tasks) / Completed (goals) — now prompts for a free-text reason before the UI sends the change, and asks where any trigger / gate / threshold / recurring check the item owns moves (gate successor, `none` if nothing is inherited). Both fields are passed to vault-cli as `--reason` and `--gate-successor`, restoring close-outs against vault-cli v0.116.0+, which rejects aborted/completed writes without `aborted_reason` and `gate_successor` frontmatter. A blank or whitespace-only reason is blocked in the browser (Confirm stays disabled) and by the API (HTTP 400 naming the missing field); a missing gate-successor defaults to `none` on both sides. Non-close-out actions (Hold/Resume/defer and phase moves other than done) are unchanged. Bumps the `app.js` and `style.css` cache-bust tokens.

## v0.52.1

- fix(api): Add a 30-second time-to-live to the per-vault task and goal list caches. The cache key is the directory (or vault-root) mtime, which POSIX does not bump on in-place frontmatter edits, so a task or goal flipped to `status: next` could keep surfacing under stale in_progress/hold/completed filters until the vault-cli watcher callback happened to fire (or the server restarted). Cache entries are now `(dir_mtime, cached_at_epoch, items)` and any entry older than `_CACHE_TTL_SECONDS` (30s) is treated as a miss and re-fetched from vault-cli on the next request — a missed or delayed watcher event self-heals instead of persisting indefinitely. The watcher-callback invalidation and the synchronous cache pops on status/execute-command writes are preserved unchanged. Both `/api/tasks` and `/api/goals` share the TTL bound.

## v0.52.0

- feat(ui): Add a Sort control to the board header with three within-column orderings — Default (unchanged: urgency tier → priority for tasks, priority → id for goals), Priority (highest first), and Last modified (most-recent activity first, via the card's `activity_date` — the small grey duration each card shows). The choice persists in the URL as `?sort=` so reloads and shared links keep the order; an unknown value falls back to Default. Applies to both Tasks and Goals views through a shared comparator, with the active → upcoming → hold bucketing and recently-completed pinning preserved. Shipped as a frontend-only direct-flow change (no dark-factory — the YOLO container cannot run a browser) and verified by a new integration-marked Playwright E2E suite (`make test-integration`, `tests/test_board_sort.py`) that starts the app in-process on a random port with a mocked vault-cli and asserts reorder + URL persistence in a real browser. Adds `pytest-playwright` as a dev dependency; the decision tables (`vault-ui/CLAUDE.md`, `dark-factory/docs/choosing-a-flow.md`, vault KB) gain a frontend-only → direct carve-out. Bumps the `app.js` and `style.css` cache-bust tokens.
- feat(ui): Task and goal cards show how long it has been since anything last happened on them — a small grey duration (`<1m`, `47m`, `2h`, `4d`, `3w`) in the card footer, with the full timestamp on hover. The timestamp is the newer of the file's mtime and the mtime of the Claude session transcript named by the card's `claude_session_id` (new `activity.py`, surfaced as `activity_date` on `TaskResponse` and `GoalResponse`). Neither signal works alone: the file only changes when something is written to it, so an agent mid-turn looks stale for hours, while the transcript keeps a dead session's timestamp once that session ends. Cards with no `claude_session_id` (human tasks) and sessions whose transcript is not on this machine (cloud/container) fall back to the file mtime. `Goal` gained a `modified_date` field to make that fallback possible. Bumps the `app.js` and `style.css` cache-bust tokens.

## v0.51.2

- fix(goals): Deferred goals now disappear from the board like deferred tasks. `GET /api/goals` gained the `defer_date` filter + `upcoming_hours` window the tasks endpoint already had (future-deferred goals are hidden; in-window ones return `upcoming: true`), `loadGoals()` sends the upcoming-window value, and goal cards grey out when upcoming. In-window (upcoming) and hold goals now also sink to the bottom of their column (`active → upcoming → hold` bucketing) instead of holding their priority slot — mirroring the task ordering. Previously deferring a goal wrote `defer_date` but left the card on the board in place.

## v0.51.1

- fix(ui): Goal drag-and-drop survives a Tasks → Goals → back navigation. The Goals view rebuilds its status columns from scratch on every view switch (`renderColumnHeaders` in `app.js` removes and recreates them), but drop-target listeners were only attached once at page load, so the freshly-built columns had no `drop`/`dragover`/`dragleave` handlers and goal cards could no longer be moved between statuses. `renderColumnHeaders` now re-wires drop handlers on all columns after every rebuild (idempotent for the surviving static Tasks phase columns). Direct `?view=goals` loads and Tasks-view drag-and-drop were and remain unaffected. Bumps the `app.js` cache-bust token so already-open boards fetch the fixed script.

## v0.51.0

- feat(ui): Declutter the header on narrow viewports (laptops). Below a 1500px viewport width, hide the "Vault UI" title (pure branding) and the "Upcoming" window `<select>` (a rarely-toggled setting) via a CSS media query, so the vault/status/assignee selectors and the board columns get the reclaimed width. Full-width monitors are unchanged; the elements stay in the DOM (hidden by CSS only). Bumps the `style.css` cache-bust token.

## v0.50.1

- refactor(ui): Collapse the forked task/goal card code paths in `app.js` into one kind-parameterized path. The run (Start/Resume), dropdown-build, dropdown-action dispatch, and clear-session functions each become a single function taking `kind` and routing to `/api/${base}/…` (`task`→`tasks`, `goal`→`goals`), extending the existing `patchStatus(kind, …)` precedent. Card render is extracted into shared helpers (`sessionButtonHtml`, `cardShellHtml`) with thin kind wrappers that keep the genuinely divergent parts (task urgency tiers + Jira badge; goal on-hold styling + goal-kind dataset) — not a monolithic `createCard`. The id arg-injection guard now lives once on the merged run and clear paths and covers both kinds. Pure refactor: both boards, drag-and-drop routing, every dropdown action, Start/Resume/Reset, and the durable `claude_session_started` starting-state behave identically to before. Bumps the `app.js` cache-bust token so already-open boards fetch the collapsed script.

## v0.50.0

- feat(api): Surface durable `claude_session_started` flag for goals end-to-end: `GET /api/goals` reads it from the status cache (mirroring the task path), `POST /goals/{id}/run` sets it before mint and clears it on mint failure, `DELETE /goals/{id}/session` clears it in lockstep with `claude_session_id`, and stale-session cleanup clears it alongside the id. The flag is an invariant of the goal session lifecycle, mirroring the existing task behavior exactly. No new config or tunable — the frontend already consumes `goal.claude_session_started` (shipped v0.49.0).

## v0.49.0

- fix(ui): Goal ▶ Start button holds "⏳ Starting…" through the multi-second Claude mint instead of flashing back to Start. Adds a `startingGoals` set (mirroring `startingTasks`) that survives mid-mint re-renders (WebSocket/poll) within the tab; `createGoalCard` computes an `isStarting` state like `createTaskCard`. (Full durable/cross-tab `claude_session_started` flag for goals is a follow-up.) Bumps the `app.js` cache-bust token.
- feat(ui): Include `hold` in the default status filter (no `?status=` param) for both tasks and goals, so parked/blocked work isn't silently hidden. Goals default is now `backlog, next, in_progress, hold, completed` (adds the Hold column by default); the Tasks view-toggle default is `in_progress, hold, completed` (previously dropped `hold`, inconsistent with initial load).

## v0.48.0

- feat(ui): Render card priority as a compact `P<N>` chip in the footer meta row (next to the assignee) on both task and goal cards, replacing the standalone `Priority: N` line that wasted a full row on goal cards. No chip when priority is unset. Muted slate pill styling (`.priority-chip`), distinct from the violet hold badge. Bumps the `app.js` cache-bust token.

## v0.47.0

- feat(ui): Add Start / Resume / Reset session controls to goal cards, reaching session parity with task cards. A session-less goal shows ▶ Start; clicking it mints a real Claude session via `POST /api/goals/{id}/run` (`vault-cli goal work-on <goal> --mode headless --output json`), stores `claude_session_id` on the goal, and opens the existing Session Ready modal with a resume command — the card then flips to ▶ Resume. A goal that already has a session shows ▶ Resume and short-circuits to the modal without minting a new session. The goal dropdown lists **Reset Session** only when the goal has a session; choosing it clears `claude_session_id` via `DELETE /api/goals/{id}/session` and the card reverts to ▶ Start. Reuses the existing `showModal` / `positionAndBindMenu` / `patchStatus` / `parseErrorResponse` helpers and the existing stale-session cleanup. Bumps the `app.js` cache-bust token so already-open boards fetch the new script.
- feat(api): Add `POST /api/goals/{goal_id}/run` endpoint that mints a Claude session for a goal via `vault-cli goal work-on`, stores `claude_session_id` on the goal, and returns a `SessionResponse` with a ready-to-run resume command. Goal IDs beginning with `-` are rejected before any subprocess (argument-injection guard). Failures surface as HTTP 500 with diagnostics naming the goal id and vault.
- feat(api): Add `DELETE /api/goals/{goal_id}/session` endpoint that clears `claude_session_id` from goal frontmatter via `vault-cli goal clear` with a bounded 10s timeout. A wedged process is killed and returns HTTP 504; non-zero exit returns HTTP 500. Goal IDs beginning with `-` are rejected before any subprocess.
- test(api): Add regression tests for both goal-session endpoints covering: happy-path mint + store + resume command, goal-not-found 404, dash-prefix rejection, three diagnosable-500 cases (non-zero exit, non-JSON, no session), clear-session happy path, timeout → 504 + kill, non-zero → 500, and `GET /api/goals` continuing to surface each goal's `claude_session_id`.

## v0.46.0

- feat(ui): Add a lifecycle dropdown to goal cards and a Hold/Resume toggle to both goal and task cards. Goal cards gain a `⋮` menu (Complete / Defer / Abort / Hold Goal) mirroring the task-card menu — Complete/Defer route to a new `POST /api/goals/{id}/execute-command` fast path (`vault-cli goal complete` / `goal defer <tomorrow>`, no AI session); Abort/Hold go through the existing `PATCH /api/goals/{id}/status`. Hold is a toggle: a held item's menu instead reads **Resume** and returns it to `in_progress`. Held goals show a `⏸ HOLD` badge like held tasks. Shared `positionAndBindMenu` + `patchStatus` frontend helpers back both menus (abort refactored onto them).
- fix(ui): Render the Hold (and Aborted) status column on the Goals board whenever that status is in the active filter. Previously the board built only four fixed columns (Backlog/Next/In Progress/Completed), so a held goal was fetched but had no column to render into and silently vanished — its card and Resume action unreachable. Columns now rebuild on status-filter changes too, not just on view toggle.
- fix(api): Invalidate the per-vault goal/task list cache synchronously on a status or goal execute-command write. The cache key is the vault-root mtime, which does not change on an in-place frontmatter edit (POSIX), so an operator's own Hold/Resume/Abort change did not surface until the async watcher happened to fire — forcing a manual reload. `update_goal_status`, `execute_goal_command`, and `update_task_status` now pop their cache entry before returning.

- fix(ui): Surface `claude_session_started` to the board so the durable "Starting" state actually reaches the UI. Root cause of the flag never showing: `vault-cli task list` emits a fixed schema (status, phase, `claude_session_id`, …) and drops the custom `claude_session_started` field, so `/api/tasks` always returned `null` for it — the flag was written to frontmatter but invisible to the frontend. The `StatusCache` (the sanctioned direct-frontmatter-read path) now also caches `claude_session_started`, and `/api/tasks` enriches each task from it. For a real multi-minute task (`work-on --mode headless` only returns the session id when the skill finishes — minutes, not the ~1s a trivial task takes), the card now shows ⏳ Starting for the whole run and survives reload / second tab, flipping to Resume only when `claude_session_id` lands.

- fix(ui): Replace the transient `claude_session_starting` timestamp marker with a durable `claude_session_started` boolean flag. `vault-cli task work-on --mode headless` returns the session id in ~1s (it does not block for the whole session), so the old marker was set and cleared within the same second and never actually showed "Starting". The flag is now set to `true` before launch and left set — it is cleared only when `claude_session_id` is cleared (the clear-session endpoint and the stale-session cleanup sweep both clear it), or if the launch itself fails. Button state: `claude_session_id` → Resume; else flag set → Starting; else Start. This makes the indicator durable across reload, modal dismiss, and phase change, and stops the card reverting to "Start" while a session exists. The `claude_session_starting` TTL sweep is removed. Also bumps the `app.js` cache-bust token.

- fix(ui): Bump the `app.js` cache-bust token (`?v=`) so the v0.45.1 durable-"Starting" fix actually reaches browsers. The static mount sends no `Cache-Control`, so with the token unchanged an already-open board kept serving the pre-fix cached `app.js` and the Start button still reverted — the fix shipped server-side but never loaded client-side. Bumping the token forces a fresh fetch on the next normal page load (no hard-refresh needed).

## v0.45.1

- fix(ui): Make task card's "Starting…" state durable and server-owned. The v0.34.4 fix deleted the browser-side starting marker on modal close, so the card silently reverted to "Start" during the 30s–5min window before `claude_session_id` landed. Now the backend writes a `claude_session_starting` ISO-8601 timestamp frontmatter field before launching; the frontend derives "Starting…" from this durable field instead of ephemeral browser state. The marker is cleared in `finally` when the session starts successfully or on failure. A background cleanup sweep clears orphaned markers older than ~15 min (TTL) so crashed mid-launch processes do not leave stale indicators. Existing Start → Starting → Resume behaviour and all current tests keep working.
- fix(ui): Preserve `hold` status when dragging a card between phase columns. `update_task_phase` hardcoded `status = in_progress` for any non-`done` phase move, so dragging a held card to Execution silently cleared its hold status (regression against the v0.45.0 hold feature). Now the handler reads the current status first and leaves `hold` untouched on non-`done` moves (`done` → `completed` unchanged, everything else → `in_progress` as before). New `test_update_task_phase_preserves_hold_status` guards it.

## v0.45.0

- feat(ui): Surface `hold` tasks on the Kanban board. Held cards now render with a violet left border, 60% opacity ("parked"), and a `⏸ HOLD` chip so they're distinguishable from active work while sitting in their own phase column. `hold` is included in the default status filter (frontend `currentStatuses` + backend `/api/tasks` default) so blocked/parked work stays visible without opting in via the dropdown, and held cards sink to the bottom of each column (order: active → upcoming → hold) since they're not actionable now. New `test_list_tasks_default_filter_includes_hold` covers the backend default including `hold` and still excluding `aborted`.

## v0.44.2

- fix: Start/Run button was broken for **every** task — `vault-cli task work-on --output json` emits its result as a **pretty-printed multi-line** JSON object, but `start_vault_cli_session` parsed only the last non-empty line (`}`), so `json.loads("}")` raised `Expecting value: line 1 column 1 (char 0)` (the recurring red toast; 0 successful sessions in the logs). Replace the last-line heuristic with `_last_json_value`, which parses the whole output as one value (pretty or single-line object) and falls back to last-parseable-line for JSONL. The v0.44.1 diagnostic wrapper made this legible; this restores the button. New `tests/test_workon_json_parse.py` covers the pretty-printed regression, JSONL fallback, and empty/blank/`null` inputs.

## v0.44.1

- fix: Replace the opaque `Expecting value: line 1 column 1 (char 0)` toast with a diagnosable error. Every `vault-cli` JSON-parse site (`vault_cli_client.list_tasks`/`show_task`/`list_goals`, `tasks.start_vault_cli_session` work-on, `config.discover_vaults_from_cli`) now wraps `json.loads` — on empty/blank subprocess output it raises a `RuntimeError` naming the command, return code, stdout length + snippet, and stderr, instead of the context-free `json.JSONDecodeError`. The new `RuntimeError` is deliberately not a `ValueError` subclass, so `/api/tasks` and `/api/goals` no longer silently swallow the failure (previously a transiently-empty vault vanished from the board with no signal). Root cause was unguarded `json.loads(stdout.decode())` — guarded only `returncode != 0`, never empty output. Object-expecting parse sites (`show_task`, work-on) also guard against `null`/non-object JSON (which would otherwise crash with a context-free `AttributeError` on `result.get(...)`); list sites keep `null → []` since empty vaults legitimately emit `null`.

## v0.44.0

- feat: Resolve config file XDG-first (`~/.config/vault-ui/config.yaml`), falling back to legacy repo-root `config.yaml` — matches vault-cli convention and enables `uv tool install`-based bare `vault-ui` invocations.

## v0.43.0

- feat(ui): Replace the task-card menu's 5 "Move to" phase shortcuts with a single "Abort Task" action — drag-and-drop already covers phase moves; abort sets task status to `aborted` via new `PATCH /api/tasks/{id}/status` (mirrors the goal status endpoint). Complete/Defer unchanged.

## v0.42.1

- perf: parallelize `/api/assignees` endpoint with `asyncio.gather` — 4 vaults drop from 2.65s to 0.17s (15× faster)

## v0.42.0

- refactor: Rename project end-to-end `task-orchestrator` → `vault-ui` — pairs with `vault-cli` (two surfaces over the same vault). Python package `src/task_orchestrator/` → `src/vault_ui/`, all imports + tests + `pyproject.toml` (`[project.scripts] vault-ui = "vault_ui.__main__:main"`, `[tool.hatch.build.targets.wheel] packages = ["src/vault_ui"]`) updated. GitHub repo `bborbe/task-orchestrator` → `bborbe/vault-ui` (redirect intact). Mechanical diff: 116 files renamed (933 ins / 933 del), no behavior changes. Operators must update launchd plist `ProgramArguments` from `uv run task-orchestrator` to `uv run vault-ui` after pulling — old entry-point name no longer registered.

## v0.41.1

- fix(goals): match goal card layout to task cards — Jira badge + assignee badge render side-by-side in `card-footer-left` (was: Jira badge inline with title; assignee alone in footer). Priority meta text now uses `.goal-meta` rule (12px / `#96999e`) matching the assignee-badge visual weight instead of inheriting paragraph-default size. Goal cards with frontmatter `jira: BRO-NNNN` (or BRO-NNNN prefix in title) now render the same 🔖 issue-key link tasks already had — `extractJiraIssue()` destructure was dropping `issueKey` / `issueUrl` on the goal-card path.

## v0.41.0

- feat: Add `groupBy` selector to the kanban header — switches columns between the phase taxonomy (TODO / PLANNING / EXECUTION / AI_REVIEW / HUMAN_REVIEW / DONE, default for Tasks view) and the canonical status taxonomy (IN_PROGRESS / NEXT / BACKLOG / COMPLETED / HOLD / ABORTED, default for Goals view). Active value round-trips through the URL as `?groupBy=phase` / `?groupBy=status` and survives reload; defaults are kind-aware (tasks→phase, goals→status) and unknown values fall back to the kind default with the URL rewritten. The hard-coded `statusToColumn` aliasing map in `loadGoals` is removed in favor of a `currentGroupBy` dispatch; the `in_progress → execution` aliasing rule in `loadTasks` now applies only under `groupBy=phase`. Under `?view=goals&groupBy=phase`, goals without a `phase` field land in a single `—` column. New `tests/test_groupby_selector.py` covers selector markup, URL plumbing, kind-aware default, and the unknown-fallback rewrite.
- fix: Eliminate cross-view leak on Tasks/Goals toggle — every sidebar interaction (vault switch, status filter, assignee filter, refresh button, periodic poll, WebSocket task event, drag-drop, slash command, clear session, assign-to-me) now routes through the `loadCurrentView()` dispatcher that fires only the active view's fetch. `handleTaskUpdate`'s task-event branch early-returns when `currentView === 'goals'` so a task event arriving while on Goals view does NOT mutate the goals DOM (spec AC#3). Regression test `tests/test_cross_view_leak.py` covers all migrated call sites.
- fix: Remove redundant 'Open in Obsidian →' link from goal cards below the title — the title `<a>` is the only link on the card now (spec AC#9). Clicking the title still opens the goal file in Obsidian (spec AC#10). No new `innerHTML` write site introduced (spec Security row 1).
- fix: Drop silently-ignored `goal=` query param from `loadGoals()` requests to `/api/goals` — the endpoint accepts only `vault`, `status`, `assignee`; the param was a no-op. Network panel now shows a clean URL. The `goal=` append in `loadTasks()` is untouched (`/api/tasks` accepts it as a valid filter).

## v0.40.0

- feat: WebSocket payload now carries `item_kind: "task" | "goal"` on every broadcast — vault-cli watcher callback (which already received the kind from `vault_cli watch --types`) propagates it into the message dict, and the three explicit `broadcast` call sites in `api/tasks.py` (defer/complete fast path, assign-to-me, update phase) carry `"task"`. The frontend `handleTaskUpdate` reads the field and routes to the active view's cache only. Cache invalidation in the watcher callback is now kind-scoped: task events touch only `vault_task_cache`; goal events touch only `vault_goal_cache` — the inactive view does NOT re-fetch on every event (spec AC#9 invariant).

## v0.39.0

- feat: Add Tasks/Goals view toggle to the board — top-of-board control switches between the existing Tasks view and a new Goals view that renders goal cards in the same status columns. Active view encoded in URL as `?view=tasks` / `?view=goals`; deep-linking to `?view=goals` lands in the Goals view without first firing `/api/tasks` (single in-flight fetch). Goal cards are read-only (no Start/Resume button, no drag), reusing the existing task-card rendering path and the same `obsidian://` URL encoding. Per-view caches ensure editing a goal does NOT re-fetch tasks and vice versa.

## v0.38.0

- feat: Add `GET /api/goals` endpoint mirroring `/api/tasks` (same `vault` / `status` / `assignee` query params; new `GoalResponse` shape with `status`, `priority`, `defer_date`, `target_date`, `completed_date`, `obsidian_url`, `vault`, `claude_session_id`, `assignee`; missing frontmatter fields surface as `null` per spec Failure Mode row 1). Per-vault mtime-keyed goal cache on `app.state.vault_goal_cache`, invalidated alongside the existing task cache by the vault-cli watcher. `Goal` dataclass gains the new fields with `None` defaults — backwards-compatible. `/api/tasks` and `TaskResponse` byte-identical to pre-spec.

## v0.37.0

- feat: Add `LOG_LEVEL` env var (`DEBUG | INFO | WARNING | ERROR`, case-insensitive; default `INFO`) read at startup and applied to both Python's root logger and uvicorn — bump to `DEBUG` to trace HTTP requests and the long-running headless `vault-cli task work-on` subprocess live. Stream the headless subprocess's stdout/stderr line-by-line at DEBUG (1 MiB per-line buffer, non-UTF8 tolerated) instead of buffering in `communicate()` — operator no longer waits 60–180s in the dark when starting a session from the UI. Other short-running vault-cli call sites are unchanged.

## v0.36.0

- feat: Embed task title as `-n <title>` in the resume command emitted by the orchestrator, so the launched Claude Code session shows the task title in its prompt box, `/resume` picker, and terminal title from the first turn — eliminates the per-session manual `/rename`. Empty / missing titles omit the flag, leaving the command byte-identical to before. Affects both the Start button (`POST /api/tasks/{id}/run`) and the `work-on-task` / `create-task` slash commands; fast-path `defer-task` / `complete-task` are unchanged.

## v0.35.0

- feat: Add upcoming-window dropdown to kanban header (Off · 2h · 4h · 8h · 12h · 24h) so the operator picks how far ahead deferred tasks should appear as greyed-out upcoming cards. Setting persists in localStorage. New `upcoming_hours` query param on `/api/tasks` (int, 0–168, default 8 preserves current behavior). `upcoming_hours=0` hides all deferred tasks regardless of how soon they are due — fixes the cross-midnight asymmetry where deferring to tomorrow only hid the card if you did it before 16:00 local

## v0.34.6

- fix: Click on the loading-modal or session-modal backdrop now closes the modal — previously only the small × button worked

## v0.34.5

- fix: Wrap Session Ready modal's task title in `<code>` so it gets the same dark-box styling as the other code boxes (visual consistency)
- fix: Add `user-select: all` to Session Ready modal's code boxes so a single click selects only the boxed content (task title, session ID, executed command, handoff command) — previously double-click extended selection into surrounding labels like "Session ID:"

## v0.34.4

- fix: Clear stale `startingTasks` Set entry on Executing-Command modal close + treat the Set as a hint rather than ground truth in the render guard so the Start button transitions to Resume once the backend's `claude_session_id` lands, even when the user dismisses the modal early

## v0.34.3

- fix: Invalidate per-vault task cache from the vault-cli watcher callback so in-place frontmatter edits (drag-and-drop phase/status changes) appear in the UI on the next refresh; directory mtime alone does not detect such writes under POSIX semantics

## v0.34.2

- perf: Replace serial per-vault loop in GET /api/tasks with asyncio.gather concurrent fan-out; warm p50 drops from 270-330 ms to single-vault dominated latency
- perf: Add per-vault mtime-keyed in-process cache to GET /api/tasks; cache hit skips the vault-cli subprocess and invalidates automatically when a task file is created, modified, or deleted
- refactor: Move per-vault task cache from module global to FastAPI app.state for constructor-injection; tests no longer reach into module private names
- fix: Drop status_filter kwarg from cache-miss list_tasks call to make the cache contract explicit (stores unfiltered raw list); closes pr-reviewer cache-key-missing-status-filter finding on PR #6
- fix: Narrow asyncio.gather result re-raise from BaseException to RuntimeError so KeyboardInterrupt / SystemExit / CancelledError do not accidentally surface through GET /api/tasks

## v0.34.1

- fix: `derive_claude_project_dir` now encodes `session_project_dir` to `~/.claude/projects/<encoded>` instead of returning it as-is. Previous behavior treated the obsidian vault path (e.g. `~/Documents/Obsidian/Personal`) as the claude project dir, so every cleanup pass cleared valid UUIDs in family/openclaw/trading tasks and the watcher resolver could never find a matching session.
- fix: vault-cli `work-on` success-without-session now surfaces the underlying warnings (e.g. "claude session starter unavailable — claude script not found in PATH") instead of the opaque "returned no session_id" UI toast.

## v0.34.0

- feat: Flip Kanban board to canonical vocabulary — status dropdown shows `next`/`backlog` in place of `todo`; EXECUTION column replaces "In Progress"; right-click "Move to" emits `phase=execution`; old on-disk `in_progress` phase aliases to EXECUTION on display; status filter URL always emits explicit `?status=` params

## v0.33.0

- feat: Accept status alias `next` alongside `todo` in default filter and `?status=next` queries; accept phase alias `execution` alongside `in_progress` in `?phase=execution` queries and valid-phase list — both old and new canonical values are first-class forever

## v0.32.0

- fix: Assignee dropdown now lists all assignees from the selected vault(s), not just those visible in the current filter — new GET /api/assignees endpoint sources the option set independently of `/api/tasks`. Fixes collapse to "All + Unassigned" when the Unassigned filter was active.

## v0.31.0

- feat: Assignee filter dropdown in the Kanban header — multi-select with one row per distinct assignee in the loaded task set, plus an "Unassigned" row for the empty-token filter; fixes the UX dead-end where `?assignee=` could not be cleared from the UI

## v0.30.0

- feat: Migrate vault-cli watcher subprocess from `task watch` to `watch` — dispatches events on the new `type` field; goal frontmatter changes now resolve display-name `claude_session_id` to UUID instantly via the watcher path instead of waiting up to 5 minutes for the cleanup loop. The cleanup loop stays as a backstop for events that arrive while the watcher is offline.

## v0.29.0

- feat: Frontend reads goal filter from URL — ?goal= param round-trips end-to-end (parse on load, forward to /api/tasks, preserve through updateURL writebacks); URL-driven only, no new UI controls

## v0.28.0

- feat: Add goal filter to GET /tasks — new goals field on TaskResponse (wiki-link brackets stripped at parse time), goal query param accepts repeated and comma-separated forms, filters by set membership with OR semantics

## v0.27.0

- feat: Status filter dropdown in the Kanban header — mirrors the vault dropdown UX, multi-select checkboxes for todo/in_progress/completed/hold/aborted, no need to hand-edit URL

## v0.26.0

- feat: Frontend reads multi-value status from URL — supports `?status=todo,in_progress` and `?status=todo&status=in_progress`; default behavior (`in_progress,completed`) unchanged when no status param present

## v0.25.0

- fix: Replace blocking alert() dialogs with non-blocking error toasts; drop redundant "Failed to X:" prefixes — backend stderr is now surfaced directly via showToast(message, true)

## v0.24.0

- fix: Surface real backend error messages in UI alerts — adds `parseErrorResponse()` helper, replaces generic "Failed to execute command" with actual stderr (e.g. "Error: incomplete subtasks: 11 pending" from vault-cli refusals); also replaces raw `{"detail": "..."}` JSON envelopes shown verbatim at four other fetch callsites

## v0.23.0

- feat: One-click "Assign to me" on unassigned task cards — adds `PATCH /tasks/{id}/assign-to-me` endpoint and inline link rendered in the assignee badge slot when a card has no assignee; clicking sets `assignee` to the configured `current_user` via vault-cli and re-renders the board

## v0.22.0

- feat: Support multi-value assignee URL params in Kanban board — repeated `?assignee=a&assignee=b` form is now read, stored, forwarded to the API, and written back to the URL; empty-token (`?assignee=`) unassigned marker round-trips correctly

## v0.21.0

- feat: Unify GET /tasks filter syntax — status, phase, and assignee now accept both repeated (?x=a&x=b) and comma-separated (?x=a,b) forms; assignee empty-string token matches unassigned tasks; vault gains comma-split support alongside existing repeated-param support

## v0.20.1

- fix: Suppress noisy traceback when a vault has no `Goals/` directory; downgraded to a debug log per cleanup cycle. Other `vault-cli goal list` failures still log at error level with traceback.

## v0.20.0

- feat: Extend cleanup loop to resolve and clear stale `claude_session_id` values on goals, matching task parity — display names are resolved to UUIDs on each cleanup pass (up to one cleanup-cycle latency); unresolved names and stale UUIDs are cleared

## v0.19.0

- feat: Add `Goal` dataclass to models and extend `VaultCLIClient` with `list_goals`, `set_goal_field`, `clear_goal_field` methods for vault-cli goal subcommand integration

## v0.18.6

- fix(test): replace hardcoded `defer_date="2026-05-01"` in `test_list_tasks_filters_deferred` with a dynamic future date (`date.today() + 30 days`). The hardcoded date was in the past as of 2026-05-05, so the deferred task was correctly returned by the API and the assertion failed.

## v0.18.5
- fix: Handle null response from vault-cli task list for vaults with no tasks
- chore: Add uv cache mount to dark-factory config
- chore: Use hatch-vcs for dynamic versioning from git tags
- chore: Add autoRelease to dark-factory config

## v0.18.4
- fix: Prefix resume command with `cd <session_project_dir>` when set so Claude finds the session file

## v0.18.3
- fix: Use `session_project_dir` from vault-cli config to resolve Claude session files when the vault's sessions land in a non-default project directory

## v0.18.2
- fix: Default `/api/tasks` status filter to include completed tasks so the Done column is populated when no `?status=` param is given
- fix: Use `completed_date` field (with `modified_date` fallback) for the 8-hour recency cutoff on completed tasks
- fix: Replace multi-status `--all`+Python-filter approach with repeated `--status` flags so vault-cli handles filtering natively

## v0.18.1
- fix: Update status when dragging tasks — moving to done sets status=completed, moving elsewhere sets status=in_progress

## v0.18.0
- feat: Show recently completed tasks (completed within last 8h) at bottom of Done lane with green border and reduced opacity

## v0.17.0
- feat: Add "Only" button to vault selector items that selects a single vault on hover-click, and make "All" checkbox a true toggle that unchecks all vaults when all are selected
- feat: Show tasks deferred within the next 8 hours at the bottom of their Kanban lane with grey border and reduced opacity; tasks deferred beyond 8 hours remain hidden

## v0.16.0
- feat: Replace single-select vault dropdown with multi-select checkbox dropdown supporting multiple vault filtering, URL persistence via repeated `?vault=` params, and localStorage migration from old `selectedVault` key

## v0.15.0
- feat: Add `PATCH /tasks/{task_id}/session` endpoint that stores a `claude_session_id`, resolving display names to UUIDs via `session_resolver` before persisting
- feat: Wire eager session ID resolution into vault-cli watcher callback so display-name session IDs are resolved to UUIDs after each file change event

## v0.14.1
- fix: Clear non-UUID (display-name) `claude_session_id` values immediately in cleanup loop without checking file existence

## v0.14.0
- feat: Add `session_resolver` module with `is_uuid()` and `resolve_session_id()` for resolving Claude session display names to UUIDs by scanning `.jsonl` files in the project directory

## v0.13.0
- feat: Add date-urgency colored left-border indicators on Kanban task cards (red=overdue, amber=due today, blue=scheduled) with urgency-first sort within each column

## v0.12.6
- fix: Show loading spinner modal during session creation in Start button flow
- fix: Keep Start button in "Starting..." state across card re-renders

## v0.12.5
- fix: Map vault-cli `name` field to task `id` and `title` in VaultCLIClient parser

## v0.12.4
- refactor: Replace watchdog-based `TaskWatcher` with `VaultCLIWatcher` subprocess wrapper around `vault-cli task watch`; remove `watchdog` dependency and `obsidian/` package

## v0.12.3
- refactor: Replace `ObsidianTaskReader` direct file access with `VaultCLIClient` async subprocess wrapper for all task list/read/update operations; remove `task_reader.py`

Please choose versions by [Semantic Versioning](http://semver.org/).

* MAJOR version when you make incompatible API changes,
* MINOR version when you add functionality in a backwards-compatible manner, and
* PATCH version when you make backwards-compatible bug fixes.

## v0.12.2
- refactor: Replace `claude-agent-sdk` session management with `vault-cli task work-on --mode headless` subprocess calls; remove `SessionManager`, `claude/` package, and `claude-agent-sdk` dependency

## v0.12.1
- refactor: Inherit `claude_script` from vault-cli registry instead of duplicating it in `config.yaml`; the key is now read from `vault-cli config list` JSON output with `"claude"` as fallback

## v0.12.0
- feat: Add assignee-aware stale session cleanup — sessions belonging to other users are always cleared; current user's sessions are only cleared when the `.jsonl` file is missing
- feat: Add `discover_current_user` to config and populate `Config.current_user` from `vault-cli config current-user` at startup

## v0.11.0
- feat: Discover vault `path`, `tasks_dir` from `vault-cli config list --output json` at startup instead of duplicating them in `config.yaml`; task-orch config now only holds orch-specific overrides (`claude_script`, `vault_name`)
- refactor: Remove dead `claude_cli` field from `Config` dataclass

## v0.10.1
- refactor: Remove duplicate `stale_session_cleaner.py` module and its wiring from `factory.py`
- fix: Use exact vault name (no `.lower()`) in `cleanup.py` vault-cli args to support mixed-case vault names like "Family"

## v0.10.0
- feat: Wire `StaleSessionCleaner` into `lifespan` context manager in `factory.py` as a tracked `asyncio.Task` that runs on startup and is cancelled gracefully on shutdown

## v0.9.0
- feat: Add `StaleSessionCleaner` class with `run_once()` and `run_loop()` async methods to detect and clear stale `claude_session_id` values whose `.jsonl` session files no longer exist under `~/.claude/projects/`

## v0.8.0
- feat: Add background cleanup loop that detects and clears stale `claude_session_id` values from task frontmatter when the corresponding Claude session `.jsonl` file no longer exists

## v0.7.12
- chore: Migrate from deprecated `claude-code-sdk` to `claude-agent-sdk`, rename `ClaudeCodeOptions` to `ClaudeAgentOptions`, replace direct `__aenter__`/`__aexit__` calls with `AsyncExitStack` in `Session` and `SessionManager`, and update model alias `"sonnet"` to explicit `"claude-sonnet-4-5"`

## v0.7.11
- docs: Update README with Prerequisites section, correct `make sync` target, and config.yaml-based configuration documentation

## v0.7.10
- refactor: Extract `_read_file` helper in `ObsidianTaskReader` and delegate `update_task_phase` to `_update_task_frontmatter`, eliminating duplicated UTF-8/latin-1 fallback read logic

## v0.7.9
- refactor: Add `StatusCache.count()` public method and replace `cache._cache` private access in `reload_cache()` with it

## v0.7.8
- refactor: Remove dead `create_claude_client_factory()` from `factory.py` and update integration tests to construct `ClaudeSDKClient` directly
- fix: Add `exc_info=True` to `stop_task_watchers()` error log for full stack traces on watcher shutdown failures

## v0.7.7
- refactor: Delete dead `executor.py` module (`ClaudeExecutor`, `ClaudeCodeExecutor`) and remove `get_executor()` from factory; `SessionManager` is the sole session management mechanism

## v0.7.6
- fix: Snapshot `active_connections` before iteration in `broadcast()` to prevent `RuntimeError` on concurrent mutation; add `exc_info=True` to failed-send warnings in `broadcast()` and `send_personal()`

## v0.7.5
- refactor: Replace `sys.exit(1)` in `load_config()` with `FileNotFoundError`, move wiring into `main()` so the error is catchable at the composition root

## v0.7.4
- refactor: Move all function-body imports in `tasks.py` to module level and add `get_status_cache` to factory import block

## v0.7.3
- refactor: Add command routing comment and 400 guard for unknown commands in `execute_slash_command`; replace generic else prompt with explicit `work-on-task` branch

## v0.7.2
- refactor: Replace `reader.update_task_phase()` in PATCH `/tasks/{id}/phase` with `vault-cli task set <task> phase <value>` subprocess call, making vault-cli the single source of truth for all task mutations

## v0.7.1
- refactor: Remove dead defer-task and complete-task branches from Claude session path in execute_slash_command

## v0.7.0
- feat: Show success toast and refresh task list instead of session modal when vault-cli fast path returns empty session_id

## v0.6.0
- feat: Replace Claude Code session path for defer-task and complete-task with direct vault-cli subprocess calls for millisecond-speed execution
- feat: Add `vault_cli_path` field to `VaultConfig` (default `"vault-cli"`) for configurable binary path
- feat: Broadcast `task_updated` WebSocket event after successful vault-cli defer/complete so the UI refreshes automatically

## v0.5.4
- fix: Accept full ISO datetime strings in defer_date frontmatter field (e.g. `2026-03-08T21:35:32.742132+01:00`)

## v0.5.3
- Add external config.yaml support with hard exit and helpful error if missing
- Add config.yaml.example with all configurable fields documented
- Remove hardcoded vault defaults in favour of config.yaml
- Add tests for config loading, vault parsing, and missing file error

## v0.5.2
- Fix slow task session creation by returning session_id immediately without waiting for Claude response
- Add session status tracking (initializing/ready) to task frontmatter for better UI feedback
- Fix resource leak by properly cleaning up Claude SDK client in background tasks
- Fix race condition by combining session_id + status updates into single frontmatter write
- Make all file I/O operations async using asyncio.to_thread() to prevent event loop blocking

## v0.5.1
- Add priority-based sorting for tasks within each Kanban column
- Fix mypy type annotation for cache reload endpoint

## v0.5.0
- Add in-memory status cache for fast blocker resolution across all hierarchy levels
- Extend file watchers to monitor 21-24 folders (Themes, Objectives, Goals, Tasks)
- Add POST /api/cache/reload endpoint for manual cache refresh
- Replace disk I/O with O(1) cache lookups for blocked_by field validation

## v0.4.4
- Fix task menu dropdown positioning to flip upward when near viewport bottom
- Fix menu positioning to stay within viewport bounds horizontally

## v0.4.3
- Use `--tool` flag for slash commands (machine-readable JSON output)
- Set phase to `human_review` on command failure
- Add create-task command support
- Migrate from deprecated on_event to lifespan context manager
- Fix loading modal dismiss not preventing session modal popup

## v0.4.2
- Add fallback polling every 60 seconds in case WebSocket misses updates

## v0.4.1
- Fix phase filtering to include tasks with invalid phase values (defaults to todo)
- Add test for tasks with defer_date=today inclusion
- Add test for invalid phase handling (phase: banana)
- Add test documenting status/phase mismatch behavior

## v0.4.0
- Add multi-vault support with "All" option in dropdown
- Add URL parameter filtering for vault (supports multiple `?vault=X&vault=Y`)
- Add assignee URL parameter filtering (`?assignee=name`)
- Add clickable assignee badges to filter tasks by assignee
- Add vault field to TaskResponse model for proper task identification
- Add 5 comprehensive tests for vault and assignee filtering
- Fix phase filtering to only show tasks without phase in todo column
- Improve WebSocket updates to handle multi-vault filtering

## v0.3.0
- Add slash command execution API endpoint with success/failure parsing
- Add loading modal with spinner and close button for command execution
- Add status message display in session modal (success/failure feedback)
- Add absolute date calculation for defer-task (tomorrow = YYYY-MM-DD)
- Fix defer_date field reading in task reader
- Improve slash command UX (non-blocking close, background execution)

## v0.2.0

- Add assignee display with 👤 icon badge in task cards
- Add Jira issue extraction from task titles with clickable 🔖 badges
- Add project domain mapping (BRO→seibertgroup.atlassian.net, TRADE→borbe.atlassian.net)
- Add configurable claude_script per vault (defaults to "claude")
- Add clickable task titles that link to Obsidian (entire title, not just icon)
- Add "Complete Task" and "Defer Task" slash command actions to dropdown menu
- Add status normalization (in-progress/inprogress/current → in_progress)
- Add executed command display in session modal
- Improve UI spacing for compact Jira-style layout
- Move menu button (⋮) to top-right corner of cards
- Replace 📝 icon with subtle ↗ arrow icon

## v0.1.0

- Add FastAPI web UI for viewing and managing Obsidian tasks
- Add vault configuration with support for multiple Obsidian vaults
- Add task filtering by status, phase, and defer dates
- Add Obsidian task reader with frontmatter parsing (status, phase, priority, dates)
- Add "Run Task" button to launch Claude Code sessions via Claude SDK
- Add persistent session UUIDs in task frontmatter (claude_session_id field)
- Add "Resume" buttons for continuing existing Claude sessions
- Add file watching with watchdog for real-time task directory monitoring
- Add WebSocket support for live UI updates without manual refresh
- Add connection status indicator (green/red dot) for WebSocket
- Add asyncio.run_coroutine_threadsafe for thread-safe event broadcasting
- Add comprehensive type hints and mypy type checking
- Add pytest test suite with task reader and API tests
- Add GitHub Actions workflow for CI/CD
