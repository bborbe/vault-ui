# Pane resolution

`pkg/pane` resolves a card's Claude session id to the WezTerm pane that session
runs in, so the board's jump control can focus it. Resolution is a Go
implementation: it enumerates the WezTerm panes and matches one to the session,
with no Python interpreter and no `who-needs-me.py` in the path.

There is no pane cache and no background pane work of any interval. A list build
resolves the panes of the rows it is about to publish, and a click resolves
fresh; both happen on demand, never on a timer.

## When a pane is resolved

- A list response now carries pane availability, so the board offers a jump
  control only on a card whose session actually has a terminal. The list path
  resolves the panes of its live sessions once per snapshot build (and once per
  session-generation refresh) with a single `wezterm cli list`; it never runs
  one listing per session. `/api/tasks` carries the resulting `jump_pane` on a
  live row and `null` otherwise.
- The click path still resolves fresh and validates at click time, and that is
  what keeps a recycled pane id from jumping to the wrong tab: a pane id is
  reused across tab moves and WezTerm restarts, so the id rendered on the card
  is a hint, never a credential.
- The resolver's seam is:

  ```go
  Resolve(ctx context.Context, sessionID string) (string, bool)
  ResolveAll(ctx context.Context, sessionIDs []string) map[string]string
  ```

  `Resolve` returns `("", false)` for every failure and `(paneID, true)` only on
  exactly one unambiguous match. `ResolveAll` is the batch form the list path
  uses: one registry read and one `wezterm cli list` for the whole batch, keyed
  by the caller's own session id, with an unresolved id simply absent.
- Resolution works in three steps. The harness session registry
  (`~/.claude/sessions/*.json`) supplies the session's current name — the entry
  whose session id carries the requested id as a case-insensitive prefix. Then
  `wezterm cli list --format json` supplies the panes. Finally the registry name
  has its leading status glyph stripped and is matched against the pane titles:
  exactly one match resolves, zero or several do not.
- **This registry read is an identity lookup, not a liveness decision.** The
  resolver needs the session's *name* to match a WezTerm pane title, and the
  attention store that supplies liveness carries no name. The Live/Resume
  decision belongs to the board and reads the store
  ([`liveness-classification.md`](liveness-classification.md)); the resolver is
  only reached for a session the board already rendered, and answers "which pane",
  never "is it alive".
- The composition root wires it in `pkg/factory`: `factory.CreatePaneResolver`
  builds the resolver from the home directory, the WezTerm bundle directory, the
  registry directory and a resolve timeout. It is handed to `CreateAPIHandler`
  as the `paneResolver` parameter, which passes it on to
  `CreateMutationService` as the mutation service's `Pane`. The `ps`-based
  liveness scan is a separate dependency and is unaffected.
- Failure contract: when no pane can be resolved, the jump answers non-2xx with
  a body naming the failure, and the frontend surfaces it as a visible error on
  the card. The jump control follows pane availability, not session state, so a
  live session whose pane cannot currently be resolved shows no control until
  the next build. The 409 remains for any caller that asks for a pane that does
  not exist; it is no longer the operator's first encounter with the condition.

## Session state freshness

- The live session ids are held in memory in `pkg/sessionstate`. They come from
  the attention store (`pkg/heartbeat`, `GET /api/1.0/session-heartbeat`, base
  URL from `ATTENTION_STORE_URL`), **not** from the harness registry directory
  under `~/.claude/sessions`: the store is the single source every liveness
  reader shares and also carries the heartbeats a cluster session posts. The
  state is read once at startup and re-read every 60 seconds
  (`DefaultRescanInterval`). Nothing is persisted to disk.
- Staleness bounds:
  - A change in the store is visible after at most one poll interval
    (`DefaultRescanInterval`, 60 s). An HTTP source cannot beat a poll, so the
    watcher polls rather than watching a directory.
  - A read that cannot reach the store is not fatal and never clears the ids:
    the state keeps the last known set, marks it non-authoritative, and the next
    poll retries. The board reads that flag so an unreachable store renders
    "cannot tell" rather than a Resume
    ([`liveness-classification.md`](liveness-classification.md) § An unreachable
    store is "cannot tell", not "dead").
  - A store that is unreachable at startup starts the state empty-but-unknown,
    and a later successful poll fills it.
- The Live badge reads from this state. The jump control's availability follows
  pane availability instead (see § When a pane is resolved); losing a pane never
  changes the badge. When the live set — or its authoritative flag — actually changes, the watcher
  pushes a refresh frame to connected browsers — two frames per configured vault
  (`task` and `goal`), each of which triggers one list reload in a browser
  viewing that kind. Both kinds are needed because the frontend dispatches a
  frame by `item_kind` and ignores the other kind. The frame reuses the existing
  watcher-frame shape, so no new protocol is introduced. The initial read pushes
  nothing: only a later change is worth a frame.
- The classification contract is unchanged and is documented in
  [`liveness-classification.md`](liveness-classification.md): the same four
  outcomes, the same signal order, the same five-minute window. Only where the
  live ids are read from moved — `ClassifySessionState` takes them as a
  parameter either way, so it is unaffected.
- The per-request `ps` scan stays on the request path; it is not part of this
  state.
