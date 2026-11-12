# Release process

vault-ui releases through the maintainer fleet: `.maintainer.yaml` sets `release.autoRelease: true`, so once a PR merges to `master` with a non-empty `## Unreleased` section, the `github-releaser-agent` rewrites the heading, commits `release vX.Y.Z`, tags and pushes. There is no manual release step and no install step to gate.

## The scenario gate

**Before a PR is merged — not before a release is cut — run every scenario in `scenarios/` against the built binary.**

The gate's placement follows from the release driver. vault-cli can gate `make install` because a human runs that step; dark-factory can gate prompt completion because the auto-release hangs off it. Here nothing human sits between merge and tag, so the last point at which a human is in the loop is the PR. A gate placed any later is not a gate.

```bash
make build                       # the binary under test
make run                         # serve it on :8000
# walk scenarios/*.md in a browser, top to bottom, against that binary
```

A scenario that fails stops the merge: fix the regression, rebuild, re-walk. Do not merge with a scenario red and do not narrow the walk to the files the diff touched — the board is one page, and a change to a filter or a column renders through code the diff did not name.

### Why a browser walk and not `make test`

`make precommit` runs `format vet test`, and none of it renders the page. The failure class this gate exists for is a render-time one: a JavaScript exception inside the poll loop reports itself as a connection problem, so a broken page reads as a dead server, and both read as clean to a unit test. This is the same rule the development guide states for any served UI asset — precommit-green is not the page renders.

⚠️ **The walk is manual because vault-ui has no browser harness.** attention-controller has one (`e2e/`, Playwright, `make e2e`); vault-ui does not, and its JS is already extracted into `src/vault_ui/static/app.js` so there is no extraction to piggyback on. Building that harness is the follow-up that retires the manual walk; until it exists, this gate is only as reliable as the operator's attention, and a skipped walk leaves no trace.

## What a scenario must carry

- `status: active` in frontmatter, and `Setup` / `Action` / `Expected` / `Cleanup` sections
- An `Expected` list whose every item is observable from outside — a rendered element, an API status, a console line — never an internal state
- A `⚠️` note when the scenario's coverage has a known hole, so the next reader does not mistake a green walk for full coverage

## Change history

**2026-11-12** — Created. The repo had `docs/dod.md` and no release guide, and no `scenarios/` at all, so nothing in the tree told anyone to exercise the served page before shipping it. The two scenarios added with this page are the first ones; the harness that would make them scripted is filed separately.
