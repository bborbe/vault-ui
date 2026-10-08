---
status: approved
created: "2026-10-08T16:08:40Z"
queued: "2026-10-08T16:16:01Z"
---

# Redirect both cache-dir environment variables in the page-index store spec

<summary>
- The page index store's default-path spec passes on macOS again, so `make precommit` is green on the operator's machine and not only in the Linux container.
- The spec stops writing into, and reading from, the developer's real user cache directory when it runs on macOS.
- The spec redirects the user cache directory the way each platform actually honours, instead of assuming one environment variable is enough.
- The expected path is derived from the same live lookup the production code uses, so the expectation and the implementation cannot disagree.
- The suite still fails if the redirect stops taking effect, because the spec asserts the resolved cache directory sits inside the temp directory it set up.
- Production behaviour is unchanged: no non-test Go file changes and the store location is resolved at runtime exactly as before. The one non-Go edit is a single `## Unreleased` changelog bullet.
</summary>

<objective>
Make the page index store's "opens the default store at the cache path" spec OS-agnostic, so `make precommit` is green on darwin as well as on Linux. `os.UserCacheDir()` honours `XDG_CACHE_HOME` on Linux but ignores it on darwin, where it returns `$HOME/Library/Caches` — so the spec's redirect takes effect inside the Linux container and is silently ineffective on the operator's macOS machine, which is why CI stayed green while `make precommit` failed locally with `TestPageIndex`. The fix lives in the test only: production path resolution is correct on both platforms.
</objective>

<context>
Read `docs/dod.md` (this repo's Definition of Done) before starting. Do not rely on `CLAUDE.md` — it is gitignored and not part of the committed tree.

Read these files before implementing:

- `pkg/pageindex/store_test.go` — the file under change, package `pageindex_test`. Inside `Describe("Page index store", ...)` → `Describe("location", ...)`, the spec `It("AC6 opens the default store at the cache path", ...)` redirects only `XDG_CACHE_HOME` and then compares the opened store's `Path()` against a path joined from the temp directory. The sibling spec directly above it, `It("AC6 resolves the fixed cache path", ...)`, is already correct: it compares against a live `os.UserCacheDir()` call. Read both, plus the package helpers near the top of the file (`vlogSink`, `currentIdentity`, `newStoreAt`, `newStore`, `closeStore`, `simpleEntry`) so the new helper lands among them.
- `pkg/pageindex/store.go` — `DefaultStorePath()` and `OpenDefaultStore(ctx, warnf, vlogf)`. Read them and confirm the store path is resolved through `os.UserCacheDir()`. Do not change either.
- `pkg/factory/pageindex_store_test.go` — the exemplar for this exact problem. Its `cacheDirEnv(dir string)` helper sets both `XDG_CACHE_HOME` and `HOME` and carries the comment "linux reads XDG_CACHE_HOME, darwin reads HOME"; its specs then derive the expected path from the production lookup. The same shape belongs in `pkg/pageindex/store_test.go`.
- `docs/page-index.md` — the `Page index store` section documents the fixed cache location. Read it to confirm the documented location is unchanged and needs no edit.
- `specs/in-progress/030-page-index-snapshot.md` — this spec is acceptance criterion AC6 of that spec; read AC6 for the intent behind the store's fixed location. Do not edit that spec.
- Coding plugin guides (in-container paths): `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` (Ginkgo/Gomega conventions) and `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` (`## Unreleased` format and conventional prefixes).

The container is Linux, so the darwin branch of `os.UserCacheDir()` cannot be executed there. That is exactly why the spec must redirect both variables instead of being proven by a run.
</context>

<requirements>
1. In `pkg/pageindex/store_test.go`, add a package-level test helper alongside the existing helpers, mirroring the exemplar in `pkg/factory/pageindex_store_test.go`:

   ```go
   // cacheDirEnv points the user cache directory at dir so the real cache is
   // never touched. linux reads XDG_CACHE_HOME, darwin reads HOME.
   func cacheDirEnv(dir string) {
       GinkgoT().Setenv("XDG_CACHE_HOME", dir)
       GinkgoT().Setenv("HOME", dir)
   }
   ```

2. Rewrite the spec `It("AC6 opens the default store at the cache path", ...)` in the `Describe("location", ...)` block so its expected path comes from a live `os.UserCacheDir()` call taken **after** the environment is redirected. The full spec body after the change:

   ```go
   It("AC6 opens the default store at the cache path", func() {
       cacheDir := GinkgoT().TempDir()
       cacheDirEnv(cacheDir)

       expectedCacheDir, err := os.UserCacheDir()
       Expect(err).NotTo(HaveOccurred())
       Expect(strings.HasPrefix(expectedCacheDir, cacheDir)).To(BeTrue())

       key := pageindex.NewKey("/vault-a", "24 Tasks")
       warns := &warnSink{}
       store := pageindex.OpenDefaultStore(ctx, warns.warnf, (&vlogSink{}).vlogf)
       defer closeStore(store)

       Expect(store.Path()).To(Equal(
           filepath.Join(expectedCacheDir, "vault-ui", "page-index.bolt"),
       ))
       Expect(store.Write(ctx, key, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
           To(Succeed())
       loaded, ok, err := store.Load(ctx, key)
       Expect(err).NotTo(HaveOccurred())
       Expect(ok).To(BeTrue())
       Expect(loaded).To(HaveLen(1))
   })
   ```

   Three details are load-bearing:
   - `expectedCacheDir` is read **after** `cacheDirEnv(cacheDir)`. Read before, it resolves the developer's real cache directory and the spec fails on macOS for the opposite reason. This is the only place in this spec where the expected path may be built — never join the temp directory directly again.
   - The `strings.HasPrefix` assertion is the guard that the redirect took effect. On Linux `expectedCacheDir` equals `cacheDir`; on darwin it is `<cacheDir>/Library/Caches`. Both satisfy the prefix. If the redirect ever stops working, the spec fails here instead of silently writing into the real user cache directory.
   - The `simpleEntry("A.md", "A")` fixture and the `Write` / `Load` assertions stay as they are. `os`, `strings` and `path/filepath` are already imported — add no import.

   The two lines being replaced are:

   ```go
   GinkgoT().Setenv("XDG_CACHE_HOME", cacheDir)
   ```
   and
   ```go
   filepath.Join(cacheDir, "vault-ui", "page-index.bolt"),
   ```

3. Leave the other three specs in `Describe("location", ...)` untouched: `It("AC6 resolves the fixed cache path", ...)` already derives the path from a live `os.UserCacheDir()` call, and `It("AC6 creates a missing store directory", ...)` and `It("AC6 never writes the store under a vault directory", ...)` use explicit temp paths. No other spec in the file changes.

4. Add an `## Unreleased` section to `CHANGELOG.md` directly above the top `## vX.Y.Z` heading, with exactly one bullet:

   ```
   - test: Redirect both the user-cache environment variables in the page-index store spec that opens the default store, so the spec no longer assumes Linux's `XDG_CACHE_HOME` redirect and stops failing `make precommit` on darwin, where `os.UserCacheDir()` reads `HOME` and the spec compared the store path against the temp directory it had set; the expected path now comes from a fresh `os.UserCacheDir()` call taken after the redirect.
   ```

   Leave everything above the new section untouched — the `# Changelog` title and the "All notable changes" line stay exactly where they are. The bullet is one line, flat, with a conventional prefix.

5. Before finishing, re-run `<verification>` and confirm it passes, then walk each `<summary>` bullet against the change and confirm it still holds.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- **Test-only change. Do NOT change `DefaultStorePath()`, `OpenDefaultStore(...)`, `NewBoltStore(...)`, `storeOpenTimeout`, or any other non-test code.** Production path resolution is correct on both operating systems; only the spec's assumption about how the redirect works is wrong. `pkg/pageindex/store.go` must be unchanged after this prompt — do not "fix" the production path.
- Scope: `pkg/pageindex/store_test.go` and `CHANGELOG.md`. No other file, and no edit to `docs/page-index.md` or to `specs/in-progress/030-page-index-snapshot.md`.
- Ginkgo/Gomega tests. Counterfeiter mocks are regenerated into `mocks/` by precommit — never hand-write or edit a mock.
- Errors are wrapped with `github.com/bborbe/errors`, never `fmt.Errorf`.
- At least 80% coverage on the changed package.
- `make precommit` runs on Linux inside the container and must exit 0 there as well as on darwin, so the fix must not depend on the platform it happens to run on.
- Repo-relative paths only — no absolute or home-relative paths.
- No new dependency and no `go.mod` change.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must pass (`sync`, `format`, `go-vet`, `test`, `check`; `test` runs `go test -race ./...`).

Run `go test -race ./pkg/pageindex/...` — must pass.

Then confirm the redirect and the derivation are in the file. The container is Linux and cannot execute the darwin branch, so these are the only in-container proof that the spec now covers both platforms:

- `grep -n 'Setenv("HOME"' pkg/pageindex/store_test.go` — must print at least one line.
- `test "$(grep -c 'os.UserCacheDir()' pkg/pageindex/store_test.go)" -ge 2` — must succeed (the pre-existing spec's call plus the new one).

Then confirm the production resolver was not rewritten:

- `test "$(grep -c 'os.UserCacheDir()' pkg/pageindex/store.go)" -eq 1` — must succeed, so `DefaultStorePath` still resolves the path through the one live lookup and was not restructured.

Then confirm the changelog entry sits under `## Unreleased` rather than folding into a released section:

- `awk '/^## /{sec=$0} /Redirect both the user-cache environment variables/{print "sits under: " sec}' CHANGELOG.md | grep -q 'sits under: ## Unreleased'` — must succeed. The pattern is this bullet's own opening words on purpose: a bare `/^- test:/` also matches the five `test:` bullets already sitting in released sections, so it could pass on the wrong bullet.
</verification>
