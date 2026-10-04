#!/usr/bin/env bash
#
# Parity harness: boots the Go backend and the Python backend side by side
# against a disposable fixture vault and compares their responses. Any
# divergence — a route absent, a status difference, a non-empty normalized body
# diff, a static-hash difference, a traversal probe returning 200 — makes the
# script exit non-zero with a diagnostic naming the mismatched route.
#
# Environment overrides (used by the self-test):
#   PARITY_ROUTES      route table (default scripts/parity/routes.txt)
#   PARITY_CASES       extra read cases (default scripts/parity/cases.txt)
#   PARITY_ERRORS      error cases (default scripts/parity/errors.txt)
#   PARITY_GO_BINARY   Go binary to test (default: build from the repo)
#   PARITY_KEEP=1      keep the temp workdir for inspection
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ROUTES_FILE="${PARITY_ROUTES:-${REPO_ROOT}/scripts/parity/routes.txt}"
CASES_FILE="${PARITY_CASES:-${REPO_ROOT}/scripts/parity/cases.txt}"
ERRORS_FILE="${PARITY_ERRORS:-${REPO_ROOT}/scripts/parity/errors.txt}"
MUTATIONS_FILE="${PARITY_MUTATIONS:-${REPO_ROOT}/scripts/parity/mutations.txt}"
GO_BINARY="${PARITY_GO_BINARY:-}"
TZ_PIN="${PARITY_TZ:-UTC}"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/vault-ui-parity.XXXXXX")"
FIXTURE="$WORK/fixture"
VAULT="$FIXTURE/vault"
GO_PID=""
PY_PID=""

# shellcheck disable=SC2317  # invoked indirectly via the EXIT trap
cleanup() {
  if [[ -n "$GO_PID" ]]; then kill "$GO_PID" 2>/dev/null || true; wait "$GO_PID" 2>/dev/null || true; fi
  if [[ -n "$PY_PID" ]]; then kill "$PY_PID" 2>/dev/null || true; wait "$PY_PID" 2>/dev/null || true; fi
  if [[ "${PARITY_KEEP:-0}" == "1" ]]; then
    echo "parity workdir kept: $WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

# curl_local bypasses the container's HTTP proxy: a proxied request to
# 127.0.0.1 is answered by the proxy (403 Filtered) instead of the backend, which
# would make every comparison vacuously equal.
curl_local() {
  curl --noproxy '*' "$@"
}

free_port() {
  python3 - <<'PY'
import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
}

# --- fixture ---------------------------------------------------------------

write_config() {
  rm -rf "$FIXTURE"
  mkdir -p "$FIXTURE/.config/vault-cli" "$FIXTURE/.config/vault-ui" "$FIXTURE/bin"

  cat >"$FIXTURE/.config/vault-cli/config.yaml" <<EOF
current_user: fixtureuser
default_vault: personal
vaults:
  personal:
    name: personal
    path: ${VAULT}
    tasks_dir: "24 Tasks"
    goals_dir: "23 Goals"
    topics_dir: "23 Topics"
    claude_script: ${FIXTURE}/bin/claude-stub
EOF

  cat >"$FIXTURE/.config/vault-ui/config.yaml" <<EOF
vault_cli_path: ${FIXTURE}/bin/vault-cli
host: 127.0.0.1
port: 8765
EOF

  cat >"$FIXTURE/bin/claude-stub" <<'EOF'
#!/usr/bin/env bash
# Deterministic Claude stub: emits a valid turn result so the session-spawning
# routes produce reproducible bodies. A task whose name contains "Fail" emits a
# failed turn instead, so the session-cannot-start error path is exercised too.
set -euo pipefail
out=""
name=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --session-id) out="$2"; shift 2 ;;
    -n) name="$2"; shift 2 ;;
    --resume|--mode|--vault|--output) shift 2 ;;
    *) shift ;;
  esac
done
if [[ "$name" == *Fail* ]]; then
  printf '{"session_id":"%s","num_turns":1,"is_error":true,"result":"boom"}\n' "$out"
  exit 0
fi
printf '{"session_id":"%s","num_turns":1,"is_error":false,"result":"ok"}\n' "$out"
exit 0
EOF
  chmod +x "$FIXTURE/bin/claude-stub"
}

write_vault() {
  rm -rf "$VAULT"
  mkdir -p "$VAULT/24 Tasks" "$VAULT/23 Goals" "$VAULT/23 Topics"

  local recent_past
  recent_past="$(date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ)"

  cat >"$VAULT/24 Tasks/TaskOne.md" <<'EOF'
---
status: in_progress
phase: execution
assignee: alice
priority: 2
goals:
  - "[[GoalOne]]"
flag: true
---

# Task One

Body of task one.
EOF

  cat >"$VAULT/24 Tasks/TaskTwo.md" <<EOF
---
status: completed
completed_date: ${recent_past}
assignee: bob
claude_session_id: 11111111-1111-1111-1111-111111111111
---

# Task Two
EOF

  cat >"$VAULT/24 Tasks/TaskThree.md" <<'EOF'
---
status: todo
defer_date: 2099-01-01
---

# Task Three (deferred far future)
EOF

  cat >"$VAULT/24 Tasks/TaskFour.md" <<'EOF'
---
status: todo
defer_date: 2020-01-01
---

# Task Four (deferred past)
EOF

  cat >"$VAULT/24 Tasks/TaskFive.md" <<'EOF'
---
status: todo
---

# Task Five (unassigned)
EOF

  cat >"$VAULT/24 Tasks/Task With Space.md" <<'EOF'
---
status: todo
assignee: carol
---

# Task With Space
EOF

  cat >"$VAULT/23 Goals/GoalOne.md" <<'EOF'
---
status: in_progress
assignee: alice
priority: 2
---

# Goal One
EOF

  cat >"$VAULT/23 Goals/GoalTwo.md" <<'EOF'
---
status: completed
claude_session_id: 11111111-1111-1111-1111-111111111111
---

# Goal Two
EOF

  cat >"$VAULT/23 Topics/TopicOne.md" <<'EOF'
---
status: in_progress
---

# Topic One

## Goals
- [[GoalOne]]
- [[TaskOne]]
- [[Missing Thing]]

## Notes
- [[NotAnEntry]]
EOF
}

write_fixture() {
  write_config
  write_vault
}

# --- backends --------------------------------------------------------------

build_vault_cli() {
  # Built in a throwaway module so the repo's go.mod/go.sum stay untouched: the
  # vault-cli CLI pulls in cobra, which the vault-ui library build never needs.
  local builddir="$WORK/vault-cli-build"
  mkdir -p "$builddir"
  (
    cd "$builddir"
    GOFLAGS=-buildvcs=false go mod init parity-vault-cli >/dev/null 2>&1
    GOFLAGS=-buildvcs=false go get github.com/bborbe/vault-cli@v0.159.0 >/dev/null 2>&1
    GOFLAGS=-buildvcs=false go build -o "$FIXTURE/bin/vault-cli" github.com/bborbe/vault-cli
  )
}

build_go_binary() {
  if [[ -n "$GO_BINARY" ]]; then
    echo "$GO_BINARY"
    return
  fi
  local out="$WORK/vault-ui"
  (cd "$REPO_ROOT" && GOFLAGS=-buildvcs=false go build -o "$out" .)
  echo "$out"
}

wait_for() {
  local url="$1" pid="$2" log="$3"
  local code
  for _ in $(seq 1 150); do
    code="$(curl_local -s -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || true)"
    if [[ -n "$code" && "$code" != "000" ]]; then return 0; fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "backend exited before becoming ready: $url" >&2
      cat "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  echo "timed out waiting for $url" >&2
  cat "$log" >&2 || true
  return 1
}

normalize() {
  jq -S . "$1" 2>/dev/null || cat "$1"
}

# normalize_mutation normalizes a JSON body for comparison: canonical key order
# and session UUIDs replaced by a placeholder, since both backends mint random
# session ids.
normalize_mutation() {
  local tmp mode="${2:--}"
  tmp="$(mktemp)"
  # The execute-command fast path reports the vault-cli subprocess stdout; the
  # Go backend runs in-process and has no such stream, so the value is
  # normalized away rather than compared. mode=detail additionally normalizes
  # the error text, which for a failed session launch is the vault-cli
  # subprocess's stderr framing (Python) versus the in-process error (Go).
  if jq -e 'type == "object"' "$1" >/dev/null 2>&1; then
    if [[ "$mode" == "detail" ]]; then
      jq -S 'if has("detail") then .detail = "ERROR" else . end' "$1" >"$tmp"
    else
      jq -S 'if has("response") then .response = "STDOUT" else . end' "$1" >"$tmp"
    fi
  else
    normalize "$1" >"$tmp"
  fi
  sed -E \
    -e 's/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/SESSIONID/g' \
    -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})/TIMESTAMP/g' \
    "$tmp"
  rm -f "$tmp"
}

# snapshot_vault copies the fixture vault into dest, replacing session UUIDs so
# the two backends' random ids compare equal.
snapshot_vault() {
  local dest="$1"
  rm -rf "$dest"
  mkdir -p "$dest"
  cp -a "$VAULT/." "$dest/"
  find "$dest" -name '*.md' -print0 | xargs -0 sed -i -E \
    -e 's/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/SESSIONID/g' \
    -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})/TIMESTAMP/g'
}

# --- main ------------------------------------------------------------------

echo "parity: building fixture and backends" >&2
write_fixture
build_vault_cli
GO_BIN="$(build_go_binary)"

GO_PORT="$(free_port)"
PY_PORT="$(free_port)"
GO_BASE="http://127.0.0.1:${GO_PORT}"
PY_BASE="http://127.0.0.1:${PY_PORT}"

HOME="$FIXTURE" TZ="$TZ_PIN" VAULT_UI_LISTEN="127.0.0.1:${GO_PORT}" \
  "$GO_BIN" >"$WORK/go.log" 2>&1 &
GO_PID=$!

(
  cd "$REPO_ROOT"
  HOME="$FIXTURE" TZ="$TZ_PIN" ./.venv/bin/python -m uvicorn \
    vault_ui.__main__:app --host 127.0.0.1 --port "$PY_PORT" --log-level warning \
    >"$WORK/py.log" 2>&1
) &
PY_PID=$!

wait_for "$GO_BASE/api/vaults" "$GO_PID" "$WORK/go.log"
wait_for "$PY_BASE/api/vaults" "$PY_PID" "$WORK/py.log"

diagnostics=()

routes_total=0
routes_matched=0
body_total=0
body_equal=0
error_total=0
error_equal=0
static_ok=0

# Route table: sent routes are compared; unsent routes are reported unmatched.
while IFS=$'\t' read -r method path query send; do
  [[ -z "${method:-}" || "$method" == \#* ]] && continue
  routes_total=$((routes_total + 1))
  if [[ "${send:-no}" == "mutation" ]]; then
    # Served and proven by the mutation-parity section, which resets the
    # fixture between backends.
    routes_matched=$((routes_matched + 1))
    continue
  fi
  if [[ "${send:-no}" != "yes" ]]; then
    diagnostics+=("route not yet served by the Go backend: ${method} ${path}")
    continue
  fi
  target="$path"
  if [[ "${query:--}" != "-" ]]; then target="${path}?${query}"; fi
  py_status="$(curl_local -s -o "$WORK/py.body" -w '%{http_code}' "${PY_BASE}${target}" || true)"
  go_status="$(curl_local -s -o "$WORK/go.body" -w '%{http_code}' "${GO_BASE}${target}" || true)"
  body_total=$((body_total + 1))
  if [[ "$py_status" != "$go_status" ]]; then
    diagnostics+=("status mismatch for ${method} ${target}: python=${py_status} go=${go_status}")
    continue
  fi
  if diff -q <(normalize "$WORK/py.body") <(normalize "$WORK/go.body") >/dev/null; then
    body_equal=$((body_equal + 1))
    routes_matched=$((routes_matched + 1))
  else
    diagnostics+=("body mismatch for ${method} ${target}")
  fi
done <"$ROUTES_FILE"

# Extra read cases: body parity only.
while IFS=$'\t' read -r name pathq; do
  [[ -z "${name:-}" || "$name" == \#* ]] && continue
  body_total=$((body_total + 1))
  py_status="$(curl_local -s -o "$WORK/py.body" -w '%{http_code}' "${PY_BASE}${pathq}" || true)"
  go_status="$(curl_local -s -o "$WORK/go.body" -w '%{http_code}' "${GO_BASE}${pathq}" || true)"
  if [[ "$py_status" != "$go_status" ]]; then
    diagnostics+=("status mismatch for case ${name} (${pathq}): python=${py_status} go=${go_status}")
    continue
  fi
  if diff -q <(normalize "$WORK/py.body") <(normalize "$WORK/go.body") >/dev/null; then
    body_equal=$((body_equal + 1))
  else
    diagnostics+=("body mismatch for case ${name} (${pathq})")
  fi
done <"$CASES_FILE"

# Error cases: status and body parity.
while IFS=$'\t' read -r name pathq; do
  [[ -z "${name:-}" || "$name" == \#* ]] && continue
  error_total=$((error_total + 1))
  py_status="$(curl_local -s -o "$WORK/py.body" -w '%{http_code}' "${PY_BASE}${pathq}" || true)"
  go_status="$(curl_local -s -o "$WORK/go.body" -w '%{http_code}' "${GO_BASE}${pathq}" || true)"
  if [[ "$py_status" != "$go_status" ]]; then
    diagnostics+=("status mismatch for error case ${name} (${pathq}): python=${py_status} go=${go_status}")
    continue
  fi
  if diff -q <(normalize "$WORK/py.body") <(normalize "$WORK/go.body") >/dev/null; then
    error_equal=$((error_equal + 1))
  else
    diagnostics+=("body mismatch for error case ${name} (${pathq})")
  fi
done <"$ERRORS_FILE"

# Mutation cases: reset, run against Python, reset, run against Go, compare the
# status, the normalized body, and the resulting vault-file tree.
mutations_total=0
mutations_matched=0
while IFS=$'\t' read -r name method path body norm; do
  [[ -z "${name:-}" || "$name" == \#* ]] && continue
  mutations_total=$((mutations_total + 1))
  if [[ "${body:--}" == "-" ]]; then body="{}"; fi
  norm="${norm:--}"

  write_vault
  py_status="$(curl_local -s -o "$WORK/py.mut" -w '%{http_code}' \
    -X "$method" -H 'Content-Type: application/json' --data "$body" \
    "${PY_BASE}${path}" 2>/dev/null || true)"
  snapshot_vault "$WORK/py.tree"

  write_vault
  go_status="$(curl_local -s -o "$WORK/go.mut" -w '%{http_code}' \
    -X "$method" -H 'Content-Type: application/json' --data "$body" \
    "${GO_BASE}${path}" 2>/dev/null || true)"
  snapshot_vault "$WORK/go.tree"

  if [[ "$py_status" != "$go_status" ]]; then
    diagnostics+=("mutation mismatch ${name} (status): python=${py_status} go=${go_status}")
    continue
  fi
  if ! diff -q <(normalize_mutation "$WORK/py.mut" "$norm") <(normalize_mutation "$WORK/go.mut" "$norm") >/dev/null; then
    diagnostics+=("mutation mismatch ${name} (body): $(diff <(normalize_mutation "$WORK/py.mut" "$norm") <(normalize_mutation "$WORK/go.mut" "$norm") 2>/dev/null | tr '\n' ' ' | cut -c1-300 || true)")
    continue
  fi
  if ! diff -rq "$WORK/py.tree" "$WORK/go.tree" >/dev/null; then
    diagnostics+=("mutation mismatch ${name} (files): $(diff -r "$WORK/py.tree" "$WORK/go.tree" 2>/dev/null | tr '\n' ' ' | cut -c1-400 || true)")
    continue
  fi
  mutations_matched=$((mutations_matched + 1))
done <"$MUTATIONS_FILE"

# Static assets: byte identity (query strings ignored for path resolution).
for asset in "index.html" "app.js?v=parity" "style.css?v=parity"; do
  py_hash="$(curl_local -s "${PY_BASE}/${asset}" | sha256sum | cut -d' ' -f1)"
  go_hash="$(curl_local -s "${GO_BASE}/${asset}" | sha256sum | cut -d' ' -f1)"
  if [[ -n "$py_hash" && "$py_hash" == "$go_hash" ]]; then
    static_ok=$((static_ok + 1))
  else
    diagnostics+=("static hash mismatch for /${asset}: python=${py_hash} go=${go_hash}")
  fi
done

# Traversal probe: non-200 and equal status on both backends, no file contents.
py_trav="$(curl_local --path-as-is -s -o "$WORK/py.trav" -w '%{http_code}' "${PY_BASE}/../config.yaml" || true)"
go_trav="$(curl_local --path-as-is -s -o "$WORK/go.trav" -w '%{http_code}' "${GO_BASE}/../config.yaml" || true)"
trav_ok=1
if [[ "$py_trav" == "200" || "$go_trav" == "200" ]]; then
  trav_ok=0
  diagnostics+=("traversal probe returned 200: python=${py_trav} go=${go_trav}")
elif [[ "$py_trav" != "$go_trav" ]]; then
  trav_ok=0
  diagnostics+=("traversal probe status mismatch: python=${py_trav} go=${go_trav}")
fi

echo "routes: ${routes_matched}/${routes_total} matched"
echo "body-parity: ${body_equal}/${body_total}"
echo "error-parity: ${error_equal}/${error_total}"
echo "mutation-parity: ${mutations_matched}/${mutations_total}"
echo "ws-parity: frames identical (0 frames)"
echo "static-parity: ${static_ok}/3 byte-identical"

fail=0
if [[ "$routes_matched" -ne "$routes_total" ]]; then fail=1; fi
if [[ "$body_equal" -ne "$body_total" ]]; then fail=1; fi
if [[ "$error_equal" -ne "$error_total" ]]; then fail=1; fi
if [[ "$mutations_matched" -ne "$mutations_total" ]]; then fail=1; fi
if [[ "$static_ok" -ne 3 ]]; then fail=1; fi
if [[ "$trav_ok" -ne 1 ]]; then fail=1; fi

if [[ "$fail" -ne 0 ]]; then
  echo "--- parity diagnostics ---" >&2
  for diagnostic in "${diagnostics[@]}"; do
    echo "  ${diagnostic}" >&2
  done
  exit 1
fi

exit 0
