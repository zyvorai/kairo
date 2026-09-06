#!/usr/bin/env bash
# Copyright 2026 Zyvor AI Labs · https://zyvor.dev
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
PORT="${KAIRO_TEST_PORT:-18080}"
LOG="${TMPDIR:-/tmp}/kairo-e2e.log"
./bin/kairo serve -addr "127.0.0.1:${PORT}" >"$LOG" 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null; then break; fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:${PORT}/" | grep -q "Know before"
python3 - <<PY
import json, urllib.request
base='http://127.0.0.1:${PORT}'
ex=json.load(urllib.request.urlopen(base+'/api/v1/examples'))
req=urllib.request.Request(base+'/api/v1/simulate', data=json.dumps(ex).encode(), headers={'Content-Type':'application/json'}, method='POST')
r=json.load(urllib.request.urlopen(req))
assert r['verdict']=='BLOCK', r
assert r['unschedulablePods'] > 0, r
assert r['pvcDestructiveRisks'] == 1, r
assert 0 <= r['blastRadius'] <= 100, r
print('e2e ok:', r['clusterName'], r['verdict'], r['blastRadius'])
PY
