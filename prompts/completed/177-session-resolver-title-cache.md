---
status: completed
summary: Added a mutex-guarded per-transcript title cache that reads only appended bytes, with full-scan-equivalent results and comprehensive Ginkgo coverage.
execution_id: vault-ui-session-title-cache-exec-177-session-resolver-title-cache
dark-factory-version: v0.198.7
created: "2026-11-02T20:33:54Z"
queued: "2026-11-02T20:33:54Z"
started: "2026-11-02T20:34:22Z"
completed: "2026-11-02T20:39:32Z"
branch: dark-factory/177-session-resolver-title-cache
---

# Cache transcript titles in the session resolver so the cleanup sweep stops re-reading every transcript

<summary>
- The background cleanup sweep no longer re-reads every Claude transcript on every pass
- A transcript's current title is remembered and reused while the file is unchanged
- A transcript that grew since the last pass is read only from where the previous read stopped
- A transcript that shrank, was replaced, or was rewritten in place is read again from the start
- Lines that cannot be a title record are skipped without being decoded
- Over-long lines are skipped without being copied into memory
- Resolution results (single match, ambiguous tie, no match, missing directory) are unchanged for every input
</summary>

<objective>
`sessionresolver.ResolveSessionID` must return what it returns today for every transcript changed the way Claude changes them (append, truncate, replace, delete; see the accepted limitations in <constraints>), while doing work proportional to the bytes appended since the previous call instead of the total size of the project directory. Profiling the deployed board showed the cleanup sweep re-reading a 58 GB / 2,717-file transcript directory for every unresolvable display name on every 5-minute pass — ~480 MB/s of allocation and 80–97% CPU for ~6 of every 11 minutes — almost all of it in `lastCustomTitle`'s per-line `ReadBytes` + `json.Unmarshal`.
</objective>

<context>
Read `docs/dod.md` for the project's definition of done.
Read `pkg/sessionresolver/resolver.go` — `ResolveSessionID`, `lastCustomTitle`, and `maxLineBytes` (4096). Today's rules, which must be preserved bit-for-bit:
- a raw line (`raw` from `ReadBytes('\n')`, INCLUDING its trailing `\n` when present) is considered only if `0 < len(raw) <= maxLineBytes`;
- the trimmed line is decoded with `json.Unmarshal` into `map[string]any`; a decode error logs `Malformed JSON in %s, skipping line` and contributes nothing;
- a decoded line whose `"type"` equals `"custom-title"` and whose `"customTitle"` key is present and non-nil replaces the remembered value (`current any`), whatever its JSON type;
- after the last line, the function returns `(current.(string), ok)` — so a non-string last remembered value yields `"", false`.
Read `pkg/sessionresolver/resolver_test.go` — the existing Ginkgo `DescribeTable` and the `writeJSONL` / `customTitle` helpers; new tests follow this style.
Callers: `pkg/cleanup/cleanup.go` `repairDisplayName` (every `DefaultCleanupInterval`, once per task/goal with a non-UUID `claude_session_id`) AND `pkg/mutations/tasks.go` (`ResolveSessionID` on the HTTP request path). Both run concurrently and share the cache — the mutex is load-bearing. Neither caller changes.
`src/vault_ui/session_resolver.py` (the superseded Python backend) has the same logic and is intentionally NOT changed: the deployed board is the Go binary.
Transcripts (`*.jsonl`) are append-only in normal operation; a file can still be truncated, rewritten in place (same inode) or replaced, or deleted.
</context>

<requirements>
1. In `pkg/sessionresolver/resolver.go`, add a process-wide per-file cache keyed by transcript path, guarded by a `sync.Mutex`. Each entry stores:
   - the `os.FileInfo` (for `os.SameFile`), size and mtime observed at the last read;
   - `offset`: the byte offset just past the last COMPLETE (`\n`-terminated or skipped-over-long) line consumed;
   - `fingerprint`: up to the 64 bytes immediately before `offset`;
   - `committed`: the remembered value (`any`) after applying only complete lines up to `offset`;
   - `result` / `resultOK`: the function's return value as today's full scan would compute it, i.e. `committed` plus any trailing partial (unterminated) line applied.
   Hold the mutex only for lookup and store, never across file IO. When storing, if the stored entry and the new one are `os.SameFile` and the stored `offset` is larger, keep the stored entry (stops a stale concurrent scan clobbering newer state); otherwise store the new one.
2. Replace the body of `lastCustomTitle` (keep its name and `(ctx, path) (string, bool)` signature):
   - `os.Stat` fails → warn as today, drop the cache entry, return `"", false`.
   - Entry exists, `os.SameFile`, size and mtime unchanged → return the stored `result, resultOK` WITHOUT opening the file.
   - Reset (scan from 0 with `committed = nil`) when: no entry, `!os.SameFile`, size < stored `offset`, or the bytes before `offset` no longer match `fingerprint` (read them with `ReadAt` after opening).
   - Otherwise resume: seek to `offset`, start from `committed`.
   - Open failure after a successful stat → warn, drop the entry, return `"", false`.
   - Context cancellation mid-scan → return `"", false` and leave the cache entry untouched.
   - A non-EOF read error → warn as today, store state through the last complete line, and return the value computed so far (as today).
   - After a successful scan, store offset, fingerprint, size, mtime, FileInfo, `committed`, and `result`/`resultOK`.
3. Line scanning: `bufio.NewReaderSize(fh, maxLineBytes+1)` and `ReadSlice('\n')`. Keep today's rule verbatim: a slice is considered only if `len(raw) <= maxLineBytes`, where `raw` includes the trailing `\n` when present. On `bufio.ErrBufferFull` the line is over-long — keep reading `ReadSlice` until its `\n` (or EOF) without retaining anything, skip it, and advance `offset` past it once its `\n` is reached. An unterminated final slice is applied to `result` only (never to `committed`) and does not advance `offset`.
4. Before decoding a considered line, require it to contain the bytes `"custom-title"`; lines without them are skipped without the JSON decoder. This intentionally stops the `Malformed JSON` warning for malformed lines that do not contain `"custom-title"` — that reduction in log noise is expected, not a regression. Lines that pass the filter are decoded into `map[string]any` exactly as today (do NOT switch to a struct — struct decoding matches keys case-insensitively and would change results).
5. In `ResolveSessionID`, after listing the directory, evict cache entries whose path is inside `projectDir` (match with `filepath.Dir(key) == filepath.Clean(projectDir)`) but absent from the listing.
6. Keep `ResolveSessionID`'s signature, return values, logging and ambiguity handling unchanged.
7. Tests in `pkg/sessionresolver/resolver_test.go` (Ginkgo/Gomega, same style); all existing tests keep passing unchanged:
   - append a `custom-title` line after a first resolve → the second resolve sees the new title (incremental)
   - rewrite a transcript to a SHORTER body with a different title → new title (reset)
   - rewrite a transcript in place to a LONGER body with a different title → new title (fingerprint reset)
   - delete and recreate a transcript with a different title → new title
   - unterminated last line carrying a title → found; then append bytes to that same line making it invalid JSON plus `\n` → the earlier committed title (or none) is returned exactly as a fresh full scan would
   - boundary: a qualifying line of 4095 bytes + `\n` is parsed; 4096 bytes + `\n` is skipped; an unterminated final line of exactly 4096 bytes is parsed
   - an over-long line containing `"custom-title"` is skipped and the next line's title counts
   - a malformed JSON line containing `"custom-title"` is skipped
   - a non-string `customTitle` as the last title line → the earlier string title no longer resolves
   - a deleted transcript is evicted: a title only it carried no longer resolves
   - cache hit without reopening: write a title, resolve it, overwrite the file with a different title of identical byte length, restore the original mtime with `os.Chtimes`, assert the OLD title still resolves (proves the stored result was returned without reading; no test-only counter)
   - for every case above EXCEPT the cache-hit case, also assert the cached answer equals a fresh-process answer by clearing the cache through `ResetCacheForTest()` and resolving again. Define `ResetCacheForTest()` in `pkg/sessionresolver/export_test.go` declared as `package sessionresolver`, so the external `sessionresolver_test` package can call it. The cache-hit case documents an accepted limitation and must NOT be compared against a fresh scan.
8. Add a bullet under `## Unreleased` in `CHANGELOG.md` (create the heading above the first `## ` heading only if absent; afterwards `grep -c '^## Unreleased$' CHANGELOG.md` must print `1`): `- fix: session resolver caches transcript titles per file and reads only appended bytes, so the cleanup sweep no longer re-reads every transcript each pass`.
9. Before you finish, re-run `<verification>` and confirm it passes; walk each requirement above against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- No behaviour change in what `ResolveSessionID` returns for any transcript that is appended to, truncated, replaced, deleted, or rewritten so that its inode, size, mtime, or the 64 bytes before the cached offset change. Document these accepted limitations in a comment above the cache: (a) a rewrite that keeps inode, size and mtime returns the cached result; (b) an in-place rewrite that only alters bytes before the 64-byte fingerprint window is not detected; (c) a `custom-title` type written with JSON escapes is no longer recognised by the byte prefilter. Also document there why the cache is package-level rather than injected (the resolver's function signature and both callers are frozen by this change).
- Do not change `pkg/cleanup`, `pkg/mutations`, the sweep interval, or the Python backend
- Use the project's existing logging style (`glog`) as in `resolver.go`
- Never run `go mod vendor`; use `-mod=mod` for any direct `go test` call
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must pass.
</verification>
