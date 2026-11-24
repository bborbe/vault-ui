---
status: completed
summary: Bumped github.com/bborbe/vault-markdown to v0.1.2 and pinned frontmatter wikilink rendering (quoted scalar and unquoted multi-link forms) with tests through the pageview resolver, plus a CHANGELOG entry
execution_id: vault-ui-frontmatter-links-exec-190-vault-markdown-frontmatter-wikilinks
dark-factory-version: v0.198.17
created: "2026-11-24T16:20:00+01:00"
queued: "2026-11-24T15:37:55Z"
started: "2026-11-24T15:41:59Z"
completed: "2026-11-24T15:43:38Z"
branch: dark-factory/190-vault-markdown-frontmatter-wikilinks
---

# Bump `github.com/bborbe/vault-markdown` to v0.1.2

<summary>

- The page view's markdown dependency moves to the release that renders frontmatter wikilinks as anchors.
- The same release stops a page whose frontmatter carries an unquoted wikilink list (`tags: [[Go]] [[Trading]]`) from failing to render at all.
- The pin is verified against the module's own published tags before the bump, and the run stops rather than downgrading if the expected release is missing.
- Two new tests prove the bumped renderer emits an anchor for a frontmatter wikilink through this repo's own resolver, and that the unquoted-list form no longer fails the page.
- No board behaviour changes: the page body renderer and every other surface are untouched.
- `make precommit` continues to pass.

</summary>

<objective>

Bump `github.com/bborbe/vault-markdown` in `go.mod` from `v0.1.0` to `v0.1.2`, the published release that renders a frontmatter wikilink value as an anchor and stops an unquoted `[[A]] [[B]]` frontmatter value from failing the whole page render, and pin both behaviours with tests that drive this repo's own resolver through the page-view renderer.

</objective>

<context>

Read these before writing:

- `go.mod` — the current requirement is `github.com/bborbe/vault-markdown v0.1.0`.
- `pkg/pageview/pageview.go` — `newRenderer` builds the renderer with `markdown.WithResolver(...)`, resolving a wikilink target to `PageURL(vaultName, rel)`; `Render` is the entry point the page route uses.
- `pkg/pageview/pageview_test.go` — the existing suite, its `fixtureVaults` provider and its `fixtureSource` fake.
- `pkg/pageview/pageview_seam_test.go` — `recordingSource`, the fake that accepts a constructed `vaultapi.Page` (use this one; `fixtureSource`'s `Backlinks` read 404s for a page it does not know).
- `CHANGELOG.md` — there is no `## Unreleased` section today; the releaser rewrites it after each release (see `docs/release-process.md`). Insert a new `## Unreleased` section directly above `## v0.115.3`, below the existing preamble.
- `docs/dod.md` — the definition of done this prompt is verified against.

Verified facts you may rely on:

- `v0.1.1` renders a frontmatter wikilink value (quoted scalar, quoted list, and the single inline `[[X]]` form) through the configured resolver, emitting `<a class="wikilink" href="…">label</a>`; an unresolved target still emits `<span class="wikilink missing">`.
- `v0.1.2` additionally quotes a frontmatter value made only of bare `[[…]]` groups before parsing, so `tags: [[Go]] [[Trading]]` renders instead of failing the whole frontmatter with a YAML parse error.
- `pkg/pageview` delegates all rendering to this module; it emits no `<td>` itself (`grep '<td' pkg/pageview` returns nothing).
- `v0.1.2`'s rendered cell is `<td>key</td><td>…</td>` with only the label HTML-escaped; the anchor markup is emitted as-is.

</context>

<requirements>

1. Discover the version — do not guess it. Run:

```bash
export PATH=/usr/local/go/bin:$PATH
export GOPRIVATE=github.com/bborbe/vault-markdown
timeout 120 go list -m -versions github.com/bborbe/vault-markdown
```

The listing must contain `v0.1.2`. Take `v0.1.2`; refuse to proceed if it is absent or if the newest published version is lower than `v0.1.2` — report `status: failed` naming the versions seen rather than bumping to something older.

2. Bump the dependency and tidy:

```bash
export PATH=/usr/local/go/bin:$PATH
export GOPRIVATE=github.com/bborbe/vault-markdown
timeout 300 go get github.com/bborbe/vault-markdown@v0.1.2
go mod tidy
```

`go.mod` must carry exactly one `github.com/bborbe/vault-markdown v0.1.2` require entry (no duplicate direct/indirect pair). Accept any `go.sum` change `go mod tidy` produces; do not hand-edit `go.sum`.

3. Add two tests to `pkg/pageview/pageview_test.go`, both through the existing `pageview.NewService(fixtureVaults{}, source)` seam:

   a. A quoted-scalar frontmatter wikilink renders as an anchor. Build a `vaultapi.Page` whose `Content` is `---\ngoals: '[[Target Goal]]'\n---\n\nBody.` and whose `Links` maps `Target Goal` to `24 Goals/Target Goal.md`; assert the output contains `<td>goals</td><td><a class="wikilink" href="/v/private-personal/page/24%20Goals/Target%20Goal.md">Target Goal</a></td>` (the vault segment is whatever `fixtureVaults` returns — read it from the suite).
   b. An unquoted multi-link frontmatter value renders both links instead of failing the page. Build a `vaultapi.Page` named `Tags` whose `Content` is `---\ntags: [[Go]] [[Trading]]\n---\n\nBody.` and whose `Links` maps `Go` to `Go.md` and `Trading` to `Trading.md`; assert the render returns no error and the output contains both `<a class="wikilink" href="/v/private-personal/page/Go.md">Go</a>` and `<a class="wikilink" href="/v/private-personal/page/Trading.md">Trading</a>`.

   Assertion (a) alone passes on `v0.1.1`; assertion (b) is what pins `v0.1.2`. Assert on the `<a href>` — not on the absence of `[[`, which a bare target would also satisfy. If either assertion fails, do not weaken it; report `status: failed` with the observed `<td>` markup and the version.

4. Insert a new `## Unreleased` section (none exists today) directly above `## v0.115.3` in `CHANGELOG.md` with one `fix:` bullet in the repo's existing style — prose, no file list, naming that the page view's frontmatter metadata table now renders a wikilink value as a link to its target page, including the unquoted `[[A]] [[B]]` form that previously failed the whole page render.

5. Scope fence: change only `go.mod`, `go.sum`, `pkg/pageview/pageview_test.go` and `CHANGELOG.md`. Do not touch `pkg/pageview/pageview.go`, any handler, the e2e harness, `Makefile`, or `.github/workflows/`.

6. Before finishing, re-run `<verification>` and confirm it exits 0; confirm `go.mod` carries `v0.1.2` and the CHANGELOG bullet sits under `## Unreleased`.

</requirements>

<constraints>

- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- No new dependency beyond the vault-markdown bump.
- Do not edit `Makefile` or `.github/workflows/ci.yml`; they already carry `GOPRIVATE`.
- `go mod vendor` is never run; `vendor/` is a build artifact.

</constraints>

<verification>

`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run:

```
export PATH=/usr/local/go/bin:$PATH
make precommit
```

Must exit 0.

</verification>
