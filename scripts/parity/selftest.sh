#!/usr/bin/env bash
#
# Proves the parity harness actually compares. It builds throwaway binaries with
# deliberate divergences and asserts the harness rejects each:
#
#   1. a normal build passes the read-only route set (the harness can pass);
#   2. a build with one route renamed fails and the diagnostic names it;
#   3. a build with one response field renamed fails and the diagnostic names it.
#
# A harness that emitted constants would pass 2 and 3 and fail the self-test.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PARITY="$REPO_ROOT/scripts/parity/parity.sh"
READ_ROUTES="$REPO_ROOT/scripts/parity/routes-read.txt"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/vault-ui-parity-selftest.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

build() {
  local tags="$1" out="$2"
  if [[ -n "$tags" ]]; then
    (cd "$REPO_ROOT" && GOFLAGS=-buildvcs=false go build -tags "$tags" -o "$out" .)
  else
    (cd "$REPO_ROOT" && GOFLAGS=-buildvcs=false go build -o "$out" .)
  fi
}

run_parity() {
  local binary="$1" output="$2"
  PARITY_ROUTES="$READ_ROUTES" PARITY_GO_BINARY="$binary" \
    bash "$PARITY" >"$output" 2>&1
}

echo "selftest: baseline (normal build must pass the read-only route set)"
build "" "$WORK/vault-ui-ok"
if ! run_parity "$WORK/vault-ui-ok" "$WORK/ok.out"; then
  echo "FAIL: the baseline parity run did not pass — the harness or the backend is broken"
  cat "$WORK/ok.out"
  exit 1
fi
grep -q "routes: 6/6 matched" "$WORK/ok.out" || {
  echo "FAIL: baseline did not report routes: 6/6 matched"
  cat "$WORK/ok.out"
  exit 1
}

echo "selftest: renamed route must fail and be named"
build "parity_selftest_rename_route" "$WORK/vault-ui-rename"
if run_parity "$WORK/vault-ui-rename" "$WORK/rename.out"; then
  echo "FAIL: the harness accepted a renamed route (it is not comparing the route set)"
  cat "$WORK/rename.out"
  exit 1
fi
grep -q "/api/vaults" "$WORK/rename.out" || {
  echo "FAIL: the diagnostic did not name the renamed route"
  cat "$WORK/rename.out"
  exit 1
}

echo "selftest: divergent response field must fail and be named"
build "parity_selftest_body_diverge" "$WORK/vault-ui-body"
if run_parity "$WORK/vault-ui-body" "$WORK/body.out"; then
  echo "FAIL: the harness accepted a divergent response body (it is not comparing bodies)"
  cat "$WORK/body.out"
  exit 1
fi
grep -q "/api/vaults" "$WORK/body.out" || {
  echo "FAIL: the diagnostic did not name the divergent case"
  cat "$WORK/body.out"
  exit 1
}

echo "selftest: OK — the harness rejects both injected divergences"
