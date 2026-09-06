#!/usr/bin/env bash
# Copyright 2026 Zyvor AI Labs
# SPDX-License-Identifier: Apache-2.0
# ============================================================================
# smoke-remote.sh — Verify a running Kairo instance (local or remote)
# ============================================================================
# Checks healthz, dashboard HTML, examples API, and a simulate round-trip.
#
# Usage:
#   KAIRO_URL=http://212.8.248.187:19615 ./scripts/smoke-remote.sh
#   ./scripts/smoke-remote.sh --port 19615
#   KAIRO_PORT=19615 ./scripts/smoke-remote.sh
#   ./scripts/smoke-remote.sh   # uses HOST:PORT from .deploy-last
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT_FROM_CLI=""
while [ $# -gt 0 ]; do
  case "$1" in
    --port) [ $# -ge 2 ] || { echo "--port requires a value" >&2; exit 2; }; PORT_FROM_CLI="$2"; shift 2 ;;
    --port=*) PORT_FROM_CLI="${1#*=}"; shift ;;
    --help|-h)
      sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

BASE="${KAIRO_URL:-}"
HOST_FROM_LAST=""
PORT_FROM_LAST=""
if [ -f "$ROOT/.deploy-last" ]; then
  # shellcheck disable=SC1091
  source "$ROOT/.deploy-last"
  HOST_FROM_LAST="${HOST:-}"
  PORT_FROM_LAST="${PORT:-}"
fi

if [ -z "$BASE" ]; then
  PORT_RESOLVED="${PORT_FROM_CLI:-${KAIRO_PORT:-$PORT_FROM_LAST}}"
  HOST_RESOLVED="${KAIRO_HOST:-$HOST_FROM_LAST}"
  if [ -n "$HOST_RESOLVED" ] && [ -n "$PORT_RESOLVED" ]; then
    BASE="http://${HOST_RESOLVED}:${PORT_RESOLVED}"
  fi
fi
[ -n "$BASE" ] || {
  echo "Set KAIRO_URL=http://host:port, or --port / KAIRO_PORT with host from .deploy-last" >&2
  exit 2
}
BASE="${BASE%/}"
TMPDIR_SMOKE="${TMPDIR:-/tmp}"

pass() { printf '  ✅ %s\n' "$*"; }
fail() { printf '  ❌ %s\n' "$*" >&2; exit 1; }

echo "Kairo smoke → ${BASE}"

code="$(curl -sS -o "${TMPDIR_SMOKE}/kairo-smoke-health.json" -w '%{http_code}' "${BASE}/healthz")"
[ "$code" = "200" ] || fail "healthz HTTP ${code}"
grep -q '"ok":true\|"ok": true' "${TMPDIR_SMOKE}/kairo-smoke-health.json" || fail "healthz body missing ok=true"
pass "healthz"

code="$(curl -sS -o "${TMPDIR_SMOKE}/kairo-smoke-dash.html" -w '%{http_code}' "${BASE}/")"
[ "$code" = "200" ] || fail "dashboard HTTP ${code}"
grep -qi 'Know before\|kairo\|html' "${TMPDIR_SMOKE}/kairo-smoke-dash.html" || fail "dashboard body unexpected"
pass "dashboard"

code="$(curl -sS -o "${TMPDIR_SMOKE}/kairo-smoke-examples.json" -w '%{http_code}' "${BASE}/api/v1/examples")"
[ "$code" = "200" ] || fail "examples HTTP ${code}"
grep -q 'clusterYaml\|desiredYaml' "${TMPDIR_SMOKE}/kairo-smoke-examples.json" || fail "examples missing YAML fields"
pass "examples"

python3 - <<PY
import json, urllib.request, sys
base = "${BASE}"
ex = json.load(urllib.request.urlopen(base + "/api/v1/examples"))
if not ex.get("clusterYaml") or not ex.get("desiredYaml"):
    print("examples payload empty — demo YAMLs not shipped?", file=sys.stderr)
    sys.exit(1)
req = urllib.request.Request(
    base + "/api/v1/simulate",
    data=json.dumps(ex).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)
r = json.load(urllib.request.urlopen(req))
assert r.get("verdict") in ("SAFE", "REVIEW", "BLOCK"), r
assert 0 <= float(r.get("blastRadius", -1)) <= 100, r
print("  ✅ simulate →", r.get("clusterName"), r.get("verdict"), "blast", r.get("blastRadius"))
PY

echo "  ✨ smoke OK"
