# Kairo architecture

Kairo is split into four surfaces around one deterministic simulation package.

```text
              Git / Helm / GitOps / CI
                       |
                       v
+--------------------------------------------------+
|                 Kairo simulation                 |
|                                                  |
|  parse -> index live objects -> model capacity   |
|       -> evaluate desired objects -> score       |
|                                                  |
|  scheduling  PVC  PDB  quota  policy  KubeVirt  |
+---------------------+----------------------------+
                      |
            +---------+---------+
            |         |         |
           CLI       REST      Web
```

## Simulation lifecycle

1. Parse the cluster snapshot and proposed object stream.
2. Index live objects by kind/namespace/name.
3. Build allocatable node state from Node status, including cordon (`spec.unschedulable`), taints and labels.
4. Subtract requests from currently bound Pods.
5. For each changed/new workload, extract pod requests and simulate placement using a deterministic best-free-memory greedy strategy on nodes the pod may use (tolerations, `nodeSelector`). If an HPA targets the workload, the extra replicas up to `maxReplicas` are placed on a copy of the node state to detect `HPA_MAX_UNSCHEDULABLE`.
   For a Deployment whose pod template changed, `maxSurge` extra pods are then placed on a copy of the resulting state (`internal/sim/rollout.go`) to detect `ROLLOUT_SURGE_UNSCHEDULABLE`. Steady-state failures are reported alone to avoid duplicate findings.
6. Check each desired object's `apiVersion` against the removal table for the target version (`internal/sim/apis.go`).
7. Evaluate object-specific risks (PVC shrink, policy change, strict PDB and quota pressure).
8. Calculate operational deltas and a bounded blast-radius score.
9. Sort risks by severity, code and resource; compute a per-object `changes[]` diff (replicas, images, requests/limits).
10. Return `SAFE`, `REVIEW` or `BLOCK` plus findings and recommendations.

## Drain simulation

`sim.Drain` (`internal/sim/drain.go`) is a separate entry point over the same snapshot and node model. It classifies pods on the target nodes (ignore DaemonSet/static, flag unmanaged, evict the rest), evaluates PodDisruptionBudgets (selector match incl. `matchExpressions`, healthy/expected counts, `minAvailable`/`maxUnavailable` as int or percent), then reschedules evictees largest-first onto the remaining nodes with the shared `schedule` function. Completed (Succeeded/Failed) pods hold no capacity and are skipped everywhere. Limits: affinity/anti-affinity, topology spread and PV topology are not modelled; PDB `unhealthyPodEvictionPolicy` is ignored; budgets are evaluated against the snapshot, not as replacements become ready.

## Why the engine does not pretend to be kube-scheduler

This version intentionally provides an understandable, deterministic preflight model. It does not yet execute scheduler framework plugins, topology spread, affinity/anti-affinity, node affinity expressions, CSI topology, device plugins, webhooks or controller reconciliation. Those are extension points for a production digital twin.

The public result schema is designed so those richer simulators can replace or augment individual evaluators without changing the CLI/API contract.

## Suggested production packages

```text
internal/collector/      Kubernetes discovery + watch snapshots
internal/scheduler/      scheduler-framework adapter
internal/network/        observed-flow + policy evaluator
internal/storage/        CSI/PVC topology evaluator
internal/gpu/            GPU/MIG/NVLink topology evaluator
internal/kubevirt/       VMI migration evaluator
internal/history/        snapshot persistence + replay
internal/integrations/   GitHub/GitLab/Argo/Flux/ServiceNow
```
