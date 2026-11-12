# Release process

vault-ui releases through the maintainer fleet: `.maintainer.yaml` sets `release.autoRelease: true`, so once a PR merges to `master` with a non-empty `## Unreleased` section, the `github-releaser-agent` rewrites the heading, commits `release vX.Y.Z`, tags and pushes. There is no manual release step and no install step to gate.

## The scenario gate

**Before a PR is merged — not before a release is cut — `make e2e` must be green.**

The gate's placement follows from the release driver. vault-cli can gate `make install` because a human runs that step; dark-factory can gate prompt completion because the auto-release hangs off it. Here nothing human sits between merge and tag, so the last point at which a human is in the loop is the PR. A gate placed any later is not a gate.

```bash
make e2e                         # the gate — must exit 0
make run                         # serve it on :8000 for a manual look
```

`make e2e` builds the binary, serves it on a free port against a throwaway vault, and drives both scenarios in a real browser. **It needs the live board stopped:** the binary binds the admin port `:9090` unconditionally (spec 021 forbids a port knob), so the target refuses to start while `com.github.bborbe.vault-ui` holds it and prints the `launchctl bootout` / `bootstrap` pair to use.

A scenario that fails stops the merge: fix the regression, re-run `make e2e`. Do not merge with the suite red and do not narrow it to the files the diff touched — the board is one page, and a change to a filter or a column renders through code the diff did not name.

### Why a browser and not `make test`

`make precommit` runs `format vet test`, and none of it renders the page. The failure class this gate exists for is a render-time one: a caught render error reaches `showToast` plus a console line and nothing else, and an uncaught one leaves the page showing its last render — the `#ws-status` indicator reports WebSocket state, never render state, so a broken page reads as stale or dead. Both read as clean to a unit test. This is the same rule the development guide states for any served UI asset — precommit-green is not the page renders.

The suite lives in `e2e/` and is deliberately outside `precommit` and outside CI: it needs Chromium and a free `:9090`, so it is a PR-time command, not a pre-commit hook. The browser-free half (`freePort`, the fixture seeder, the config writer) does run under `precommit`, so a broken harness fails fast instead of silently skipping.

## What a scenario must carry

- `status: active` in frontmatter, and `Setup` / `Action` / `Expected` / `Cleanup` sections
- An `Expected` list whose every item is observable from outside — a rendered element, an API status, a console line — never an internal state
- A `⚠️` note when the scenario's coverage has a known hole, so the next reader does not mistake a green walk for full coverage

## Change history

**2026-11-13** — The gate became executable. `make e2e` now runs both scenarios in a real browser against a throwaway vault, so the walk is no longer manual; the caveat that said *"the walk is manual because vault-ui has no browser harness"* is gone, and the mechanism sentence in § Why a browser was corrected to what `app.js` actually does.

**2026-11-12** — Created. The repo had `docs/dod.md` and no release guide, and no `scenarios/` at all, so nothing in the tree told anyone to exercise the served page before shipping it. The two scenarios added with this page are the first ones; the harness that would make them scripted is filed separately.
