# Pane resolution

`pkg/pane` resolves a card's Claude session id to the WezTerm pane that session
runs in, so the board's jump control can focus it. Resolution is a Go
implementation: it enumerates the WezTerm panes and matches one to the session,
with no Python interpreter and no `who-needs-me.py` in the path.

There is no pane cache and no background pane work of any interval. The pane id
exists for exactly one action, so it is computed at that action.

## When a pane is resolved

- A pane is resolved only when the operator clicks a card's jump control —
  never on a list read, never on a timer. `/api/tasks` and every other list
  response carry no pane id at all, so a list read spawns no subprocess and
  depends on no pane data.
- The resolver's seam is:

  ```go
  Resolve(ctx context.Context, sessionID string) (string, bool)
  ```

  `("", false)` covers every failure; `(paneID, true)` is returned only on
  exactly one unambiguous match.
- Resolution works in three steps. The harness session registry
  (`~/.claude/sessions/*.json`) supplies the session's current name — the entry
  whose session id carries the requested id as a case-insensitive prefix. Then
  `wezterm cli list --format json` supplies the panes. Finally the registry name
  has its leading status glyph stripped and is matched against the pane titles:
  exactly one match resolves, zero or several do not.
- The composition root wires it in `pkg/factory`: `factory.CreatePaneResolver`
  builds the resolver from the home directory, the WezTerm bundle directory, the
  registry directory and a resolve timeout. It is handed to `CreateAPIHandler`
  as the `paneResolver` parameter, which passes it on to
  `CreateMutationService` as the mutation service's `Pane`. The `ps`-based
  liveness scan is a separate dependency and is unaffected.
- Failure contract: when no pane can be resolved, the jump answers non-2xx with
  a body naming the failure, and the frontend surfaces it as a visible error on
  the card. The jump control stays offered in that case, because it follows live
  session state rather than pane data — a live session whose pane cannot
  currently be resolved still shows the control, and the failure is reported on
  the click.

## Session state freshness

- The live session registry ids are held in memory in `pkg/sessionstate`. The
  state is read once at startup, re-read on every file event on the registry
  directory (`~/.claude/sessions`), and re-read every 60 seconds as the safety
  net for a missed event. Nothing is persisted to disk.
- Staleness bounds:
  - A change under the registry directory is visible after the event path, which
    is debounce-free and sub-second.
  - A missed event is repaired by the rescan within `DefaultRescanInterval`
    (60 s). A `Source` that fails or panics is logged at `glog.V(2)` naming the
    directory and the error, and the rescan keeps the state current: the board
    keeps serving, and the degradation is visible only in that log line.
  - If the registry directory is deleted and recreated while the service runs,
    the fsnotify watch is lost and freshness falls back to the 60 s rescan until
    the service restarts. A directory that is missing at startup starts the
    state empty and is picked up by the next rescan once it reappears.
- The Live badge and the jump control's availability both read from this state.
  When the live set actually changes, the watcher pushes a refresh frame to
  connected browsers — two frames per configured vault (`task` and `goal`),
  each of which triggers one list reload in a browser viewing that kind. Both
  kinds are needed because the frontend dispatches a frame by `item_kind` and
  ignores the other kind. The frame reuses the existing watcher-frame shape, so
  no new protocol is introduced. The initial read pushes nothing: only a later
  change is worth a frame.
- The classification contract is unchanged and is documented in
  [`liveness-classification.md`](liveness-classification.md): the same four
  outcomes, the same signal order, the same five-minute window. Only where the
  registry ids are read from moved — `ClassifySessionState` takes them as a
  parameter either way, so it is unaffected.
- The per-request `ps` scan stays on the request path; it is not part of this
  state.
