---
status: completed
summary: Factory test suite now binds its HTTP server to a run-time free loopback port instead of the fixed admin port; assertions unchanged, CHANGELOG entry added.
execution_id: vault-ui-exec-123-bind-factory-test-server-to-a-free-port
dark-factory-version: v0.196.0
created: "2026-10-05T00:00:00Z"
queued: "2026-10-04T22:40:44Z"
started: "2026-10-04T22:40:56Z"
completed: "2026-10-04T22:42:55Z"
branch: dark-factory/123-bind-factory-test-server-to-a-free-port
---

# Bind the Factory Test Server to a Free Port Instead of a Fixed One

<summary>
- The factory test suite starts a real HTTP server on a hard-coded port.
- Any other process already using that port makes the whole suite fail.
- That is not hypothetical: the service this repo ships now runs on that port, so the suite fails on a normal machine.
- The failure is in the test, not in the product.
- After this change the suite picks a free port at run time and uses it.
- The suite then passes whether or not something else holds the old fixed port.
- No production Go code changes; the only non-test file touched is the changelog.
- The test's own assertions keep testing exactly what they test today.
- The old fixed port no longer appears anywhere in the suite.
- The dark-factory pipeline can run again on a machine where the service is up.
</summary>

<objective>
Stop the factory test suite from depending on a fixed port, so it passes regardless of what else is listening. The end state is a suite that chooses its own free port and runs green on a machine where the shipped service already occupies the old one.
</objective>

<context>
Read these before changing anything:

- `pkg/factory/factory_test.go` — the suite. Note `baseURL` is a `const` at the top and is used by every assertion in the file, and that `BeforeEach` starts the server.
- `pkg/factory/api_test.go` — `CreateAPIServer` is already called with `"127.0.0.1:0"` (~line 237). That is the closest precedent in this package and the shape to follow.
- `pkg/factory/factory.go` — `CreateHTTPServer(listen string, readiness vaultui.Readiness) run.Func`. It takes a listen address and returns a closure; it exposes no way to read back the address it actually bound, which is why the port must be chosen before the call rather than discovered after it.
- `scripts/parity/parity.sh` — its `free_port()` helper is another precedent for picking a free port from a shell script.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega conventions.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-test-types-guide.md` — which test type belongs at which level.
- `CHANGELOG.md` — the entry format used by existing releases.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — the CHANGELOG entry conventions.

Background you need: `main.go` defines the admin listen address as a constant and deliberately does not read it from the environment. The test mirrors that constant. On a machine where the shipped service is running, the suite fails with `listen tcp :9090: bind: address already in use`, and because the suite's `Eventually` waits are 10 seconds each the failure surfaces as a 60-second suite timeout rather than a fast error. That causal chain is true of a machine running the service; it is not universal, and you do not need to reproduce it here — see requirement 5.
</context>

<requirements>

1. **Remove the fixed port from the suite.** `pkg/factory/factory_test.go` currently hard-codes the port in two places — a `const baseURL` near the top and the argument to `factory.CreateHTTPServer` inside `BeforeEach`. Both must go, and the literal port must not appear anywhere in that file afterwards, including in comments and string literals.

2. **Choose a free port at run time.** Add a small helper in the test file that binds `127.0.0.1:0`, reads the port the operating system assigned, closes the listener, and returns the address as a `127.0.0.1:<port>` string. Follow the shape of `free_port()` in `scripts/parity/parity.sh` and of the `"127.0.0.1:0"` usage in `pkg/factory/api_test.go`. The bind-then-close-then-rebind sequence has an inherent race, and `CreateHTTPServer` offers no way to hold the listener across the gap; so retry the whole helper a small number of times if the later bind fails, and note the residual race in a comment. If the helper cannot obtain a port at all, fail the test rather than falling back to any fixed port.

3. **Keep the suite's shape.** `baseURL` becomes a variable assigned in `BeforeEach` from the chosen address, so every existing assertion that uses it keeps working unchanged. Do not rewrite the assertions, do not weaken them, and do not change what any of them tests. Two `//nolint:gosec` comments in the file justify themselves with the URL being a fixed loopback address; once the port is dynamic that rationale is no longer true, so update those comments.

4. **Confine the code change to the test file.** Do not modify `pkg/factory/factory.go`, `main.go`, or any other non-test file except `CHANGELOG.md` (requirement 7). Do not add an address accessor, a new constructor, or an environment variable — the production listen address stays exactly as it is.

5. **Assert the fixed port is gone.** After the change, `grep -n '9090' pkg/factory/factory_test.go` must print nothing. That is the check this prompt owns. The end-to-end proof — that the suite passes while another process holds the fixed port — is a host-side observation that happens outside this prompt, when the pipeline's own preflight next runs; do not attempt it here and do not claim it in your report.

6. **Boundary-crossing test.** The suite already drives the real server through its real HTTP surface, so the post-change run is the contract test. Report the spec count of that run and confirm that no `It` was removed, skipped, or left pending relative to before.

7. **CHANGELOG.** Add a bullet under `## Unreleased` in `CHANGELOG.md`; the file currently has no `## Unreleased` heading (its top section is a released version), so create it. One bullet: `- fix: Bind the factory test suite's HTTP server to a run-time free port instead of the fixed admin port, so the suite passes on a machine where the shipped service already holds that port.` Do NOT modify any existing section.

8. **Self-check.** Re-run the `<verification>` commands. Walk each acceptance criterion in `<summary>` and state, for each, the exact command output or test name that establishes it; for the two criteria that only a host-side run can establish, say plainly that they are not establishable in-container and do not claim them.

</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT modify any non-test file except `CHANGELOG.md`; the code change is confined to `pkg/factory/factory_test.go`.
- Do NOT modify anything under `src/vault_ui/` — the Python backend is out of scope.
- Do NOT add an environment variable, a configuration field, or an accessor to read the bound address.
- Do NOT introduce a fixed fallback port; if no free port can be obtained, the test must fail.
- Do NOT weaken, skip, or delete any existing assertion to make the suite pass.
- Do NOT attempt to observe, start, or stop any host process, and do not claim an end-to-end host-side proof.
- Keep `github.com/bborbe/vault-cli` pinned at v0.159.0 with no `replace` directive.
- Do NOT run `go mod vendor`; use `go get` plus `go mod tidy` if a dependency changes.
</constraints>

<verification>
Run `make precommit` -- must pass.

Assert the fixed port is gone from the suite:
`! grep -q '9090' pkg/factory/factory_test.go`
</verification>
