#!/usr/bin/env bash
# Copyright 2026 Zyvor AI Labs
# SPDX-License-Identifier: Apache-2.0
# ============================================================================
# smoke-remote.sh — Verify a running Kairo instance (local or remote)
# ============================================================================
# Checks healthz, dashboard HTML, examples API, and a simulate round-trip.
#
# Usage:
#   ./scripts/smoke-remote.sh
#   KAIRO_URL=http://212.8.248.187:19615 ./scripts/smoke-remote.sh
#
set -euo pipefail

BASE="${KAIRO_URL:-http://127.0.0.1:8080}"
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
