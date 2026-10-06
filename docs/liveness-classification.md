# Session liveness classification

## What this document is

This is the durable contract for how a card's Claude session is classified as
`live`, `quiet`, or `indeterminate` (or has no state at all). It is a sibling of
[`starting-marker-lifecycle.md`](starting-marker-lifecycle.md), which covers the
`claude_session_started` marker. The classification is ported to Go in
`pkg/session` (`ClassifySessionState`); this document describes that contract so
it survives the Python backend's removal.

## The four outcomes

| Outcome | Meaning | Signal behind it |
|---|---|---|
| `live` | A Claude session for this card is running right now; the wall must not offer Resume. | The harness session registry lists the session id, **or** the transcript was written within the five-minute window, **or** a live `--resume` / `--session-id` process pins the session id. |
| `quiet` | The session ended and Resume is safe (vault-cli's per-session flock releases on process death). | A transcript exists and is older than the five-minute window, no registry entry, and no live `--resume` / `--session-id` process matches. |
| `indeterminate` | A session id is set but no transcript can be found, so the session cannot be proven dead. Do not offer a Resume that cannot be honored. | A session id is set, but no transcript exists on this host (a manual terminal `/resume` in another cwd, a cloud/container session, or an entity-name session the resolver cannot match). |
| none | The card carries no `claude_session_id`; a human task, nothing to classify. | An empty session id — regardless of what the session registry holds. |

## Signal order (checked first to last)

Classification evaluates the three signals in a fixed order. The first that
fires decides the outcome.

1. **Empty session id → none.** Checked before anything else; an empty id is
   `none` even when the registry contains entries.
2. **Harness session registry → live.** The harness writes one `<pid>.json` per
   live Claude Code session under `~/.claude/sessions`. Its presence is the
   harness's own record that a session is alive — the signal transcript recency
   cannot give, because an open-but-idle session stops writing its transcript
   while its process stays up. The registry is authoritative and is checked
   before the transcript lookup, so an alive-but-idle worker reads `live` rather
   than `quiet`, and a registry-live session whose transcript is not on this
   host reads `live` rather than `indeterminate`.
3. **Transcript recency within the five-minute window → live.** The transcript
   (`<session_id>.jsonl`) is looked up in the card's project directory first,
   then by scanning every project directory under `~/.claude/projects`. A
   transcript written within the window counts as live. A transcript that cannot
   be found makes the state `indeterminate` (never `quiet`).
4. **Live `--resume` / `--session-id` process cross-check → live, else quiet.**
   For a stale transcript, a live `claude --resume <uuid>` or
   `claude --session-id <uuid>` process on this host keeps the session `live`.
   The `ps` cross-check covers the gap where a session launched via a launcher
   wrapper or a headless launch keeps its process alive while its transcript
   stops being written, so recency alone would wrongly read `quiet` and the wall
   would offer a corrupting Resume. When no process matches, the state is
   `quiet`.

## Cached inputs

The classification inputs are now read from a process-wide, timer-refreshed
session snapshot (`pkg/sessionsnapshot`), not from the request path. The registry
ids and the live `--resume`/`--session-id` ids — the latter from one `ps` scan
per refresh — and the transcript mtimes, probed at most once per session per
refresh, are cached with a timestamp and refreshed on a fixed interval of at
least 60 s (`SessionRefreshInterval`). A cached verdict never outlives its
refresh window: once a later refresh has been published, the cached value is
discarded and probed again.

The `ps` cross-check stays signal #4 in the fixed order above, the four outcomes
and the five-minute window are unchanged. Only the source of the two inputs
changes; the signal-order and outcome tables are authoritative as written.

## The task-file mtime is never a liveness signal

The task file's mtime moves when a human edits the file and says nothing about
whether a Claude session runs. It is used only for the **activity date**
(`ComputeActivityDate`, the newer of the task-file mtime and the transcript
mtime), never for liveness. A fresh task-file mtime does not make a session
`live`.

## Frozen values

The five-minute liveness window is a frozen value carried from the Python
`LIVE_WINDOW`. The Go port exposes it as `session.DefaultLiveWindow`; the
identifier may differ from the Python name, but the value (five minutes) is the
contract. The transcript-recency window is the only tunable here, and no config
knob exists for it.
