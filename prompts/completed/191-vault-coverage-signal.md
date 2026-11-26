---
status: completed
summary: Carried vault-api's `vaults` coverage set through pkg/vaultapi into a per-vault nullable `source` on GET /api/vaults and rendered a no-source marker in the tasks view, with unit, boundary, and e2e coverage.
execution_id: vault-ui-vaultcoverage-exec-191-vault-coverage-signal
dark-factory-version: v0.198.17
created: "2026-11-26T21:15:00Z"
queued: "2026-11-26T20:15:01Z"
started: "2026-11-26T20:15:30Z"
completed: "2026-11-26T20:21:10Z"
branch: feat/vault-coverage
---

# Mark a listed vault the board has no source for

<summary>
- A vault the board lists but cannot serve is now marked as such, instead of rendering as a healthy empty board.
- An indexed vault that merely has no open rows still renders an ordinary empty table, so a closed-out vault is never mistaken for an unserved one.
- The mark is distinct from the existing "vault-api unavailable" banner, and both can be visible at once.
- When the board does not know which vaults are served, it marks nothing rather than claiming every vault is unserved.
- The coverage information arrives on the poll the board already makes, so no new request is added.
- The board's default view still opens on rows.
</summary>

<objective>
Consume the `vaults` array vault-api now returns on every list envelope (`GET /api/v1/tasks`, `/goals`, `/topics`; `bborbe/vault-api` PR #81) and surface it as a per-vault `source` field on `GET /api/vaults`, rendered as a no-source marker in the tasks view. A listed vault the board cannot serve must stop looking like a healthy empty board.
</objective>

<context>
Read `docs/release-process.md` for project conventions. There is no `CLAUDE.md` in this repo.

Read these coding guides in the container:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-package-layout-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external test package, `<pkg>_suite_test.go` entry-point.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, `ctx` first, never `fmt.Errorf`, never a bare `return err`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Read the code this change extends:
- `pkg/vaultapi/client.go` — the list envelope structs (`taskList` ~184, `goalList` ~192, `topicList` ~197) and the `Client` interface (~45-53). Note the comment above them: `warnings` is a COUNT, not a list, because vault-api answers `"warnings": 112` while an earlier build declared `[]string` and every poll failed. A field added to the envelope is exercised by the real response or it is not exercised at all.
- `pkg/vaultapi/source.go` — `Source.poll` (~146), which fetches all three lists every `DefaultPollInterval` and swaps them under `mu`; `NewSourceFromData` (~69), the test constructor; `Source.Unavailable()` (~196).
- `pkg/board/vaults.go` — `ListVaults`, the handler body for `GET /api/vaults`.
- `pkg/board/board.go` — the `board` struct carries `source *vaultapi.Source` (~166).
- `pkg/api/api.go` — `VaultResponse` (~16).
- `pkg/handler/selftest_body_on.go` — build tag `parity_selftest_body_diverge`; it renames `api.VaultResponse` fields by hand.
- `src/vault_ui/static/app.js` — `loadVaults` (~580) and `loadSourceStatus` (~564); `src/vault_ui/static/index.html` line 10 carries the existing `data-testid="vault-api-unavailable"` banner.
- `e2e/harness.go` — the fake vault-api (`stubHandler` ~86, `stubList` ~245) and the generated vault config (~654).

Why the field exists upstream: a list query cannot express coverage. A vault vault-api does not index and a vault it does index but whose rows are all filtered out both answer `items: []`. The board's own visibility rules make that concrete — a vault whose only tasks are `completed` outside `LookbackHours = 8` or `aborted` (`defaultStatuses` omits it) renders zero rows correctly, and must not be marked unserved.
</context>

<requirements>

### 1. Carry the coverage set from the wire into `pkg/vaultapi`

The server's `ListResponse[T]` is a single generic struct, so all three list envelopes carry a `vaults` array. Add `Vaults []string \`json:"vaults"\`` to `taskList`, `goalList` and `topicList` so this client stays a faithful mirror of the server's envelope, even though only the tasks envelope is read today.

`Client`'s list methods return only the decoded slices, so the envelope never reaches `poll`. Make it reach: add `ListTaskEnvelope(ctx context.Context) (taskList, error)` to `vaultapi.Client` and implement it on `*client` by decoding into `taskList`; leave `ListTasks` in place, returning `body.Items`. The two in-package fakes in `pkg/vaultapi/source_internal_test.go` (`fakeClient`, `failingClient`) also implement `Client` — add `ListTaskEnvelope` to both (returning `taskList{Items: f.tasks}` and the same unreachable error respectively), or `make precommit` fails to compile. `Source.poll` calls it once, stores the set under the existing `mu` alongside `tasks`/`goals`/`topics`, and stores nil when the envelope omits `vaults`. Add:

```go
// IndexedVaults returns the vaults vault-api reported serving on the last
// successful poll, or nil when no poll has landed or the server predates the
// field. Nil means "no coverage claim" — it must not read as "covers nothing".
func (s *Source) IndexedVaults() []string
```

Return `slices.Clone(vaults)` — **not** `append([]string(nil), vaults...)`, which collapses a known-empty set to nil and turns "serves nothing" into "unknown". A known-empty set is a real answer.

### 2. Expose it per vault on `GET /api/vaults`

`api.VaultResponse` gains:

```go
// Source reports whether vault-api serves this vault: true when it appears in
// the coverage set, false when the set is known and omits it, and null when the
// set is unknown (no poll yet, or a server that predates the field). It is a
// pointer precisely so "unknown" is not rendered as "unserved".
Source *bool `json:"source"`
```

`board.ListVaults` populates it by joining each configured vault's `Name` against `b.source.IndexedVaults()`:

- coverage set nil → every `Source` stays nil
- coverage set non-nil → present ⇒ `true`, absent ⇒ `false` (this includes the known-empty set: every configured vault is `false`)

Never collapse nil and false at any layer — wire, copy, join, or render.

Add `"source": item.Source` to the hand-enumerated fields in `pkg/handler/selftest_body_on.go`, so the selftest build's deliberately-renamed body stays a faithful rename instead of silently dropping the new field.

### 3. Render the no-source marker in the tasks view

Add a hidden element in `src/vault_ui/static/index.html` next to the `vault-api-unavailable` banner (line 10), carrying `data-testid="vault-no-source"` and initial class `banner hidden`. Its text names the vault and says the board has no source for it.

In `app.js`, hold the per-vault source in a module-level map populated by `loadVaults` from the `/api/vaults` payload, and toggle the element from the same place the existing banner is toggled — a sibling call in the same load path, not a new poll and not a new endpoint. Requirements:

- It must NOT reuse `vault-api-unavailable` or its text.
- A vault with `source: true` and zero rows renders the ordinary empty table with no marker.
- `source: null` renders no marker — unknown is not a claim.
- Both markers can be visible at once when vault-api is down and an unserved vault is selected.
- The default (all-vaults) view must not show the marker merely because some unselected vault is unserved — it reports on what is selected.

### 4. Exercise the field on both sides of the wire

- Unit: `pkg/vaultapi` — drive the poll through an `httptest` server whose body is real JSON containing `"vaults": [...]`, not a hand-built `taskList`, so the `json:"vaults"` tag itself is exercised (the `warnings`-count failure mode cited in `<context>` is exactly a tag that no real body ever reaches). Cover: a body carrying `vaults` populates `IndexedVaults()`; a body omitting it leaves it nil; a body carrying `[]` leaves it non-nil and empty; the returned slice is a copy.
- Unit: `pkg/board` — `ListVaults` yields `Source: true` / `false` / `nil` for the three cases. `NewSourceFromData` is the only test constructor and carries no coverage set, so extend it with a `vaults []string` parameter (nil = unknown) and update every call site (`pkg/board/board_test.go`, `pkg/board/snapshot_pending_internal_test.go`, `pkg/vaultapi/listop_internal_test.go`, `overlay_internal_test.go`, `source_internal_test.go`, `pkg/factory/api_test.go`, `pkg/factory/fixture_source_test.go`). Do not add a package-level `var` + `Set*` pair for this.
- Boundary: assert the HTTP payload itself for all three states — `GET /api/vaults` serializes `"source":null`, `"source":true` and `"source":false` — so the pointer contract is tested at the wire and not only on the struct.
- e2e: the fake vault-api in `e2e/harness.go` returns a `vaults` array, and **both** generated configs carry **two** vault entries — the vault-cli config (~638) with each vault's `path` and `tasks_dir`, and the vault-ui config (~654) naming both — one the stub serves, one it does not, each with its fixture directory and tasks folder present. The loader merges the two halves, so a vault named only in the vault-ui config fails the load with a message that reads as a harness bug; an explicit non-empty `vaults:` block also filters, and a vault whose tasks folder is missing is skipped with a warning. A scenario asserts the unserved vault renders `data-testid="vault-no-source"` while the served vault with no visible rows renders none.

Do not narrow `make e2e` to the new case — the board is one page and the marker renders through the same code path as every other view.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do not reference a `CLAUDE.md` — this repo has none.
- `precommit` stays `format vet test`. Do not add `e2e` to it, and do not add `e2e` to CI.
- No new endpoint, query parameter, CLI flag, env var, dependency, or JS library. No new Prometheus metric.
- `Source` is a pointer so "unknown" stays distinct from "unserved": never collapse nil and false at any layer — wire, copy, join, or render.
- Test coverage injection goes through a constructor parameter (`NewSourceFromData`), never a package-level `var` + `Set*` pair.
- Do not modify anything under `.github/workflows/`.
- Every existing test must still pass.
- **The upstream contract is pinned here and must not be guessed:** the key is `vaults`, the type is `[]string`, and it is carried on all three list envelopes (`/api/v1/tasks`, `/goals`, `/topics`) by the same generic `ListResponse`. This container cannot reach vault-api to confirm it; a wrong key name passes every test here and fails only in production, which is exactly the `warnings`-count failure mode cited in `<context>`.
- Add a `CHANGELOG.md` entry under a new `## Unreleased` section (none exists today — insert it above `## v0.116.0`), per `docs/dod.md`.
- No spec is filed for this change: it is a single-prompt change and the why lives in the CHANGELOG bullet and this prompt.
</constraints>

<verification>

`go` is not always on `PATH` in this container; export it first. `make e2e` needs a browser and a free admin port, so it is not runnable here — compile the tagged suite instead and leave the browser run to the operator.

```
export PATH=/usr/local/go/bin:$PATH
make precommit
go vet -tags e2e ./e2e/
go vet -tags parity_selftest_body_diverge ./pkg/handler/
test -z "$(gofmt -l e2e/)" || { gofmt -l e2e/; exit 1; }
node --check src/vault_ui/static/app.js
```

All five must exit 0. `make precommit` runs `go fmt ./...`, `go vet ./...` and `go test -race ./...`, which covers the `pkg/vaultapi` and `pkg/board` suites and the three `Source` states. The two `go vet -tags` lines prove the build-tagged files this change touches compile — `e2e/` for the new scenario, `pkg/handler/` for the `selftest_body_on.go` field enumeration, which no untagged build compiles. The `gofmt` line is required because `go fmt ./...` skips build-tagged files. `node --check` is a pure parse — a syntax error in `app.js` blanks the whole board and no Go test can see it.

</verification>
