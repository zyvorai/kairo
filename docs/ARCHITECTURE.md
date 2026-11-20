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
3. Build allocatable node state from Node status.
4. Subtract requests from currently bound Pods.
5. For each changed/new workload, extract pod requests and simulate placement using a deterministic best-free-memory greedy strategy.
6. Evaluate object-specific risks (PVC shrink, policy change, strict PDB and quota pressure).
7. Calculate operational deltas and a bounded blast-radius score.
8. Return `SAFE`, `REVIEW` or `BLOCK` plus findings and recommendations.

## Why the engine does not pretend to be kube-scheduler

This version intentionally provides an understandable, deterministic preflight model. It does not yet execute scheduler framework plugins, topology spread, affinity/anti-affinity, taints/tolerations, CSI topology, device plugins, webhooks or controller reconciliation. Those are extension points for a production digital twin.

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
