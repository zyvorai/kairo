#!/usr/bin/env bash
# Copyright 2026 Zyvor AI Labs · https://zyvor.dev
# SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0
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
curl -fsS "http://127.0.0.1:${PORT}/readyz" | grep -q '"ready":true'
curl -fsS "http://127.0.0.1:${PORT}/api/v1/version" | grep -q '"service":"kairo"'
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
sev={'critical':0,'high':1,'medium':2,'low':3,'info':4}
keys=[(sev[x['severity']],x['code']) for x in r['risks']]
assert keys==sorted(keys), keys
assert r['changes'], r
print('e2e ok:', r['clusterName'], r['verdict'], r['blastRadius'])
PY

# --- CLI: drain simulation, rollout surge, target-version ---
set +e
./bin/kairo drain -cluster examples/drain-cluster.yaml -node worker-a >"${TMPDIR:-/tmp}/kairo-drain.out"
rc=$?
set -e
[ "$rc" -eq 3 ] || { echo "drain: expected exit 3 (PDB blocked), got $rc"; exit 1; }
grep -q DRAIN_PDB_BLOCKED "${TMPDIR:-/tmp}/kairo-drain.out"
./bin/kairo drain -cluster examples/drain-cluster.yaml -node worker-a -fail-on never -format json | grep -q '"pdbBlocked": 1'
./bin/kairo drain -cluster examples/drain-cluster.yaml -node nope >/dev/null 2>&1 && { echo "drain: unknown node should fail"; exit 1; }

E2E_DIR="$(mktemp -d)"
cat >"$E2E_DIR/cluster.yaml" <<'YAML'
apiVersion: v1
kind: Node
metadata: {name: n1}
status:
  allocatable: {cpu: "4", memory: 16Gi}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec:
  replicas: 4
  template:
    spec:
      containers:
        - name: c
          image: web:1
          resources:
            requests: {cpu: "1", memory: 1Gi}
YAML
cat >"$E2E_DIR/desired.yaml" <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec:
  replicas: 4
  template:
    spec:
      containers:
        - name: c
          image: web:2
          resources:
            requests: {cpu: "1", memory: 1Gi}
YAML
./bin/kairo plan -cluster "$E2E_DIR/cluster.yaml" -f "$E2E_DIR/desired.yaml" -json -fail-on never | grep -q ROLLOUT_SURGE_UNSCHEDULABLE
printf 'apiVersion: batch/v1beta1\nkind: CronJob\nmetadata: {name: j}\n' >"$E2E_DIR/old.yaml"
set +e
./bin/kairo plan -cluster "$E2E_DIR/cluster.yaml" -f "$E2E_DIR/old.yaml" -target-version 1.25 >/dev/null
[ $? -eq 3 ] || { echo "target 1.25 should BLOCK"; exit 1; }
./bin/kairo plan -cluster "$E2E_DIR/cluster.yaml" -f "$E2E_DIR/old.yaml" -target-version 1.24 >/dev/null
[ $? -eq 0 ] || { echo "target 1.24 should pass"; exit 1; }
set -e
rm -rf "$E2E_DIR"
echo "e2e cli ok"
