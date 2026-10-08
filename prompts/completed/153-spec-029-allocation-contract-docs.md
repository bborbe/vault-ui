---
status: completed
spec: [029-rescan-allocates-only-what-changed]
summary: 'Added the incremental-rescan allocation contract to the Incremental updates section of docs/page-index.md; confirmed the existing ## Unreleased changelog bullet and make precommit passing.'
execution_id: vault-ui-exec-153-spec-029-allocation-contract-docs
dark-factory-version: v0.196.0
created: "2026-10-08T09:07:08Z"
queued: "2026-10-08T18:57:22Z"
started: "2026-10-08T18:58:20Z"
completed: "2026-10-08T19:00:28Z"
pr-url: https://github.com/bborbe/vault-ui/pull/166
branch: dark-factory/rescan-allocates-only-what-changed
---

# Document the page-index rescan allocation contract

<summary>
- The page-index design doc states the allocation contract of an incremental rescan.
- It says an unchanged pass allocates no snapshot state and keeps the published snapshot and the recorded fingerprints in place.
- It says the recorded fingerprint set is index-private and updated in place, so a pass that changes K files allocates in proportion to K, not to the folder's size.
- It says a new snapshot is published only when the page set or its order moved.
- Nothing else in the document changes meaning, and no production code changes.
- The `## Unreleased` changelog bullet from the implementation prompt stays as it is.
</summary>

<objective>
Record the allocation contract of an incremental rescan in `docs/page-index.md`, so the reason a pass over an unchanged folder is cheap — and the guarantee that it publishes nothing and replaces nothing — is written down next to the staleness and incremental-update rules it belongs to.
</objective>

<context>
Read `docs/page-index.md` in full. The `## Incremental updates` section already describes the stat-diff, the folder-level and per-file write marks, and `ForceReload`; this prompt adds the allocation contract to that section.

Read `specs/in-progress/029-rescan-allocates-only-what-changed.md` — its `## Goal`, `## Desired Behavior` items 1, 3 and 4, `## Constraints` and Acceptance Criterion AC8 are the source of the wording.

Read `CLAUDE.md` at the repo root and `docs/dod.md` for the project's Definition of Done.

The sibling implementation prompt for spec 029 — the one dark-factory numbered 152 and moved to `prompts/in-progress/` — adds the `## Unreleased` bullet to `CHANGELOG.md`, and it runs before this one. Do not add a second changelog entry here; only verify the existing one is present.

Coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>

1. In `docs/page-index.md`, inside the `## Incremental updates` section, add the allocation contract. Extend the existing stat-diff bullet or add one new bullet immediately after it. The added text must state, in plain prose:
   - an unchanged stat-diff — the key already has a published snapshot, every listed entry's fingerprint is present in the recorded set and equals the one recorded for it, the listed name set equals the recorded name set, and the pass consumed no file to re-read (no pending write mark forced a name) — **allocates** nothing beyond the one folder listing it performs to detect the change: it builds no new pages slice, no per-name order structure and no folder-sized lookup map, it keeps the published snapshot and the recorded fingerprint set in place, it still resolves the mark that triggered it, and it publishes nothing;
   - the recorded fingerprint set is index-private and updated in place for the K names a pass changes, so a pass's allocation is proportional to K and not to the folder's file count; a name the listing no longer holds has its entry deleted from the set in that same pass, so the recorded name set never outgrows a listing;
   - a new published snapshot is built only when the page set or its order actually moved; a read that reproduces the same pages in the same order publishes nothing and leaves a reader's snapshot identity unchanged.

   The word `allocate` or `allocates` must appear in the added text — AC8 greps for the stem `allocate`, which matches `allocates` but **not** `allocation`, and the section-scoped `awk` below uses the same stem. Writing only "the allocation contract" satisfies the prose but fails both checks.

2. Match the section's existing voice: short, factual bullets, present tense, no file paths and no function names beyond the ones the section already names (`Refresh`, `MarkDirty`, `MarkFileDirty`, `ForceReload`).

3. Do not change the meaning of any other bullet or section. Do not touch the document title, the `## Task-list snapshot`, `## Staleness bounds`, `## Frame ordering` or `## Key derivation` sections, and do not edit `README.md` or any other doc.

4. Do not modify `CHANGELOG.md` in this prompt. The `## Unreleased` bullet was added by the implementation prompt; confirm it is present.

5. Self-check: before finishing, re-run every `<verification>` command and confirm each passes. Re-read the added paragraph and confirm it says exactly the three things in requirement 1 and nothing that contradicts the `## Staleness bounds` section.

</requirements>

<constraints>
- Docs-only change: do NOT touch any Go file, the `Makefile`, or any config.
- Do NOT change `RescanInterval`, the read surface, the write-mark rules, the frame contract, the response bodies or vault-cli.
- Do NOT add a second `## Unreleased` changelog bullet in this prompt.
- bborbe conventions per `docs/dod.md` apply; documentation is updated when behaviour described in `docs/` changes.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
`go` lives at `/usr/local/go/bin` in this container and is not always on `PATH`; run `export PATH=/usr/local/go/bin:$PATH` first.

```
grep -n 'allocate' docs/page-index.md
```
Must print at least one line (AC8).

```
awk '/^## Incremental updates/{s=1; next} /^## /{s=0} s && /allocate/ {print; found=1} END { exit !found }' docs/page-index.md
```
Must exit 0 (the word "allocate" appears inside the `## Incremental updates` section).

```
awk '/^## /{sec=$0} /allocat/{found=1; print "allocation bullet under: " sec} END{exit !found}' CHANGELOG.md
```
Must exit 0 and print the section that holds the bullet. That section is `## Unreleased` only in the window between the implementation prompt landing the bullet and the next release — the release bot then cuts it into a `## vX.Y.Z` section, and **that is the better outcome, not a failure**. So this check asserts only that the changelog records the change, and prints where. Do not require `## Unreleased` specifically: that condition expires on the first release, so it false-fails on a correctly-released bullet. Do not use `grep -A10 '^## Unreleased' CHANGELOG.md | grep -q '^- fix:'` either — it passes on any other `fix:` bullet in the section when this one never landed, and prints nothing at all once a release has renamed the section. And not a bare `awk '/^## /{sec=$0} /allocat/{print sec}'`: an `awk` that only prints exits 0 whether or not it printed anything, so it cannot fail on the condition it asserts.

```
export PATH=/usr/local/go/bin:$PATH
ROOTDIR=/workspace make precommit
```
Must exit 0.
</verification>
