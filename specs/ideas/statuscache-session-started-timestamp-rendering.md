---
tags:
  - dark-factory
  - spec
status: draft
---

## Summary

- `pkg/statuscache/statuscache.go` renders a `claude_session_started` frontmatter marker through `time.RFC3339Nano` (`toString`, statuscache.go:256) after `go.yaml.in/yaml/v3` decodes a bare ISO-8601 scalar into a `time.Time`.
- That rendered value does reach a response body: `Cache.GetSessionStarted` → `board.sessionStarted` → `TaskResponse.ClaudeSessionStarted` / `GoalResponse.ClaudeSessionStarted`.
- Python's `status_cache.get_session_started` returns the raw frontmatter string verbatim (`src/vault_ui/status_cache.py:116`), so the two backends can disagree on the literal text of `claude_session_started`.
- This was **not** fixed in the board date-time precision change; it is a separate surface with a different contract (raw string passthrough vs. structured date-time rendering) and needs its own decision.
- This spec exists so the finding is not lost.

## Problem

The marker is a string in frontmatter, not a typed field. Python preserves it byte-for-byte; Go parses it into a `time.Time` and re-serializes it. The two agree only when the marker text is RFC3339Nano-idempotent.

Concrete divergence paths:

| Marker written by | Marker text | Python response | Go response |
|---|---|---|---|
| Python (`src/vault_ui/api/tasks.py:212`, `datetime.now(UTC).isoformat()`) | `2026-10-05T10:00:00.123456+00:00` | `…+00:00` (raw) | `2026-10-05T10:00:00.123456Z` (re-formatted) |
| Go (`pkg/mutations/helpers.go:62`, `time.RFC3339Nano`) | `2026-10-05T10:00:00.123456789Z` | `…123456789Z` (raw) | `…123456789Z` (idempotent) |
| Legacy boolean | `true` | `"true"` | `"true"` (yaml string/bool, unaffected) |

The Python writer's `+00:00` offset is the clearest divergence: a marker written by Python, read back by Go, renders as `Z`; read back by Python it stays `+00:00`.

## Why it is currently invisible

- The parity fixture sets no `claude_session_started` marker, so no read case exercises the field.
- Mutation-case bodies are compared after `normalize_mutation()` rewrites every timestamp to `TIMESTAMP` (`scripts/parity/parity.sh`), which masks any text difference.
- Each backend writes its own marker in the mutation flow, so the cross-backend read path (Python writes → Go reads) is never exercised.

## Verified facts

- `go.yaml.in/yaml/v3` decodes an unquoted ISO-8601 frontmatter scalar into `time.Time` for an `any` target; quoted strings and `true` stay strings/bools. Verified with a throwaway Go probe.
- `toString`'s `time.Time` branch formats with `time.RFC3339Nano` (`pkg/statuscache/statuscache.go:255-256`).
- Python's `get_session_started` returns the stored raw value (`src/vault_ui/status_cache.py:116-128`).

## Open questions

1. Should Go preserve the raw marker string (match Python) or normalize it? Preserving the raw string is the parity-preserving choice and the smaller change; normalizing would require Python to change too, which the frozen-Python constraint forbids.
2. If Go preserves the raw string, does the frontend's elapsed-time parse still work for both `+00:00` and `Z` forms? (It parses ISO-8601 either way, but confirm.)
3. Does any consumer depend on the marker being a normalized `Z` form?

## Non-goals

- No change to the board date-time rendering (`dateTimeString`) — that is the separate change this finding was filed alongside.
- No change to `pkg/mutations/helpers.go`'s writer format in this spec; only the read/render path is in question.

## Acceptance Criteria

- [ ] A fixture task/goal carries a `claude_session_started` marker written in Python's `isoformat()` form.
- [ ] The parity harness compares `claude_session_started` literally (no timestamp normalization) for at least one read case.
- [ ] Go's rendered `claude_session_started` is byte-identical to Python's for that case.
- [ ] `make parity` and `make precommit` pass.
