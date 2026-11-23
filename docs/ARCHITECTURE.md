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
7. Evaluate object-specific risks (PVC shrink, policy change, strict PDB), then ResourceQuota/LimitRange admission (`internal/sim/admission.go`).
8. Calculate operational deltas and a bounded blast-radius score.
9. Sort risks by severity, code and resource; compute a per-object `changes[]` diff (replicas, images, requests/limits).
10. Return `SAFE`, `REVIEW` or `BLOCK` plus findings and recommendations.

## Drain simulation

`sim.Drain` (`internal/sim/drain.go`) is a separate entry point over the same snapshot and node model. It classifies pods on the target nodes (ignore DaemonSet/static, flag unmanaged, evict the rest), evaluates PodDisruptionBudgets (selector match incl. `matchExpressions`, healthy/expected counts, `minAvailable`/`maxUnavailable` as int or percent), then reschedules evictees largest-first onto the remaining nodes with the shared `schedule` function. Completed (Succeeded/Failed) pods hold no capacity and are skipped everywhere. Limits: affinity/anti-affinity, topology spread and PV topology are not modelled; PDB `unhealthyPodEvictionPolicy` is ignored; budgets are evaluated against the snapshot, not as replacements become ready.

## Consolidation

`sim.Consolidate` (`internal/sim/consolidate.go`) reuses the node model and placement (`eligible`, `fits`, `take`) from drain. Nodes are tried emptiest first (name breaks ties). A node is a candidate only if it has no unmanaged or do-not-disrupt pod; its controller-owned, non-DaemonSet pods are placed largest first, best-fit (least free memory), on nodes not yet removed; PDB budget is consumed per eviction across the whole plan. A failed trial is rolled back; a successful one is committed, so pods placed earlier on a later-removed node move again. The last node is never removed. Output is deterministic. Not modelled (and returned in `notModelled`): affinity, topology spread, PV topology, observed usage, node pools, autoscaler policy.

## Cost estimates

`internal/sim/cost.go` converts user-supplied unit prices into a monthly delta (730 h). Consolidation prices removed nodes and reclaimed memory capacity; plan prices the requested CPU/memory delta as a share of an average node. Prices are validated (0 to 1e9) and never fetched from a cloud API. The result is flagged `estimate: true` with a basis and note.

## Disruption risk

`sim.DisruptionRisk` (`internal/sim/disruption.go`) scores each node from the pods bound to it. The weights and saturation points live in one table (`disruptionWeights`) that is returned in every result; points are `max x min(1, observed / saturation)`. Inputs: PDB `allowed` disruptions against what the node needs (shared with drain), owner replica counts, volumes, `topologySpreadConstraints` and required anti-affinity (detected only), and declared probes. Missing inputs are returned in `unknowns`, never defaulted. `removable` comes from `consolidateObjs`.

## Right-sizing

`sim.Rightsize` (`internal/sim/rightsize.go`) validates the supplied usage (finite, non-negative, bounded counts), matches each series to pods by namespace, workload (pod name, controller name, or Deployment name derived from a ReplicaSet) and container, and computes a nearest-rank percentile with a margin and floor. `quantileCI` gives a distribution-free confidence interval from the binomial distribution of order statistics; sides that the sample count cannot bound are flagged open. Four scenarios (current, optimistic, expected, conservative) re-parse the snapshot, overwrite the matched containers' requests, and run `consolidateObjs`, which is the consolidation engine without the YAML parse, so input objects are never shared between scenarios.

## Resilience

`sim.Resilience` (`internal/sim/resilience.go`) groups nodes by capacity type, instance type and zone labels and runs `drainObjs` on each spot or unknown pool and each zone with PodDisruptionBudget objects removed from the object list (involuntary loss ignores them). Scenario count is capped at 500 and the cap is reported.

## Admission checks

`internal/sim/admission.go` overlays the proposed ResourceQuota/LimitRange objects on the snapshot, applies the API server's defaulting (request = explicit limit, then LimitRange `defaultRequest`/`default`), checks Container/Pod/PVC min, max and limit-request ratio, and compares projected namespace usage with each unscoped quota. Only increases are flagged, so a namespace already over quota is not blamed for an unrelated change. Usage counts workload objects (not their owned Pods/ReplicaSets/Jobs) to avoid double counting.

## API contract

`docs/openapi.yaml` describes every route. `internal/api` keeps routes in one table (`Server.Routes`) used by both the mux and `openapi_test.go`, which fails on undocumented or stale routes and on JSON fields missing from (or extra in) the schemas.

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
