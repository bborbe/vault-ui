# Cleanup retention invariant

## What this document is

This is the durable contract for the five-minute cleanup sweep that keeps stale
Claude session ids from stranding a card. It is a sibling of
[`starting-marker-lifecycle.md`](starting-marker-lifecycle.md), which covers the
`claude_session_started` marker, and of
[`liveness-classification.md`](liveness-classification.md), which covers how a
session is classified `live` / `quiet` / `indeterminate`. The sweep is ported to
Go in `pkg/cleanup`; this document describes that contract so it survives the
Python backend's removal.

The sweep never reads or writes a vault file itself. Every vault operation goes
through an injected `VaultOps` surface (vault-cli as a library), one instance per
vault, and every awaited operation is bounded so one stuck helper cannot freeze
the pass.

## The retention invariant

A valid UUID `claude_session_id` is cleared only when **both** hold:

1. **This instance launched it** — the process-local launch registry has a record
   for the item (in flight or finished), and
2. **its transcript is gone** — `<uuid>.jsonl` does not exist in the vault's
   Claude project directory.

Either condition alone is not enough:

- **A missing transcript with no launch record is retained.** Without a registry
  record the sweep cannot prove THIS instance launched the session; the missing
  transcript may belong to a session started from a terminal or another tool.
- **A present transcript is retained** even when the registry holds the launch: a
  session file on disk is a running or resumable session regardless of the
  registry.
- **A foreign assignee is retained FIRST.** The assignee check comes before the
  transcript and registry checks, so a registry record never rescues a
  foreign-assignee binding — never write a field on a card owned by someone else.
  A peer machine's session must never be published as deleted.

A session id that contains a path separator (`/` or `\`) is skipped entirely: a
session id must not carry a path component before any file lookup.

## Non-UUID display names

A `claude_session_id` that is not a UUID is a display name, not an id.

| Item kind | Resolvable display name | Unresolvable display name |
|---|---|---|
| Task | Repaired to the resolved UUID (`SetTaskField`); never cleared. | **Retained** on disk untouched — being unresolvable right now is not evidence the binding is wrong. |
| Goal | Repaired to the resolved UUID; never cleared. | **Cleared** — the one deliberate divergence from the task path. |

Resolution consults the live process table first, then scans the transcripts in
the vault's project directory for a session whose current custom title matches.
An ambiguous match (two or more sessions share the title) is refused, not
guessed.

## Re-binding an empty session id

A card whose `claude_session_id` is EMPTY is re-bound from the task title only
when **exactly one live session carries that title** AND **its transcript lives
in this vault's Claude project directory** — the live map is keyed on the bare
title and carries no vault component, so the transcript's location is what tells
two same-titled vaults apart.

The live-process gate is the whole safety property. An empty binding is not
always a loss: the sanctioned session reset and vault-cli's failed-turn
compensating clear both empty the field DELIBERATELY, and the released session's
transcript keeps its custom title forever. A transcript-scan re-bind would
resurrect exactly those releases; a running process cannot be resurrected from a
stale file, so the live map is the honest evidence of "work already running".

Nothing is written when:

- the title is absent from the live map (no live session, or an ambiguous title
  the live map omits);
- the resolved session's transcript is not in this vault's project dir;
- the card is owned by another user, mid-launch (any launch-registry record), or
  already `completed`/`aborted`;
- the title is empty.

The write joins the API's own per-`(vault, item)` critical section, re-reads the
card under that lock, and abandons the write when the binding or the assignee
changed since the list was snapshotted. A re-bind is not a clear and does not
count toward the cleared total.

## The marker passes

The `claude_session_started` marker is handled by three registry-aware passes;
the set and clear paths and the concurrent-writer model are the frozen contract
in [`starting-marker-lifecycle.md`](starting-marker-lifecycle.md).

- **Marker TTL (post-restart orphan).** A marker with NO registry record is
  cleared once it is older than the marker TTL. An IN_FLIGHT record is never
  cleared regardless of age; a FINISHED record is handled by the re-clear pass.
  An unparseable marker (the legacy literal `true`) has an unknown age and is
  treated as expired. The marker is read from the status cache, not from the
  listed item: `vault-cli task list` does not emit `claude_session_started`.
- **Resurrected-marker re-clear.** A finished launch's marker that a concurrent
  writer (e.g. an obsidian-git merge) restored is re-cleared from disk. Fires at
  most once per finished record: the record is evicted once the clear succeeds or
  the marker is already gone, and eviction is conditional — the record is dropped
  only while it is still FINISHED, so a relaunch that begins while the clear is
  awaited keeps its fresh record. A failed re-clear keeps the record and is
  retried on the next pass.
- **Startup orphan reconciliation.** A marker whose launch this host no longer
  has is cleared once, at startup, when the registry has no record for the item,
  the marker is past the orphan grace, and no `--session-id` launch process for
  the item runs here. This closes the window a restart opens (the launch
  coroutines die with the process, so the cards would otherwise sit on
  "Starting…" until the TTL sweep).

## Frozen values

| Value | Setting | Meaning |
|---|---|---|
| 45 minutes | `cleanup.DefaultMarkerTTL` | How long a marker may sit without a session id before the TTL sweep clears it. Not the 15 that shipped in July: vault-cli's headless branch blocks until the turn finishes (its own 30m `sessionTurnTimeout`), so a shorter TTL would bounce a legitimately running card back to "Start". |
| 120 seconds | `cleanup.DefaultOrphanGrace` | A marker younger than this is never reconciled at startup: the launch may still be booting (vault-cli mints the uuid, writes the marker, then spawns claude). |
| 10 seconds | `cleanup.DefaultSetFieldTimeout` | Bound on each awaited vault operation, so a stuck helper leaves the field untouched and the next sweep retries. |
| 10 seconds | `cleanup.DefaultLockAcquireTimeout` | Bound on the wait to ACQUIRE the per-task lock in the re-bind pass (separate from the bound on the calls made while holding it). |
| 5 minutes | `cleanup.DefaultCleanupInterval` | The sweep interval. |

The Go identifiers may differ from the Python names; the values are the
contract.
