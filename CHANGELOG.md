# Changelog

All notable changes to Kairo are documented here.

## Unreleased

### Added

- `kairo rightsize` and `POST /api/v1/rightsize`: request recommendations from supplied CPU/memory usage samples with an explicit percentile and safety-margin model, a distribution-free confidence interval, and a current/optimistic/expected/conservative scenario band for removable nodes and monthly cost through the consolidation engine. Missing data (too few samples, unmatched series, containers without usage) is reported, hostile samples (NaN, infinite, negative, oversized) are rejected.
- `kairo disruption` and `POST /api/v1/disruption`: a published 100-point disruption score per node from PDB pressure, single-replica and all-replicas-here workloads, local state, placement constraints and declared probe startup time, with factor inputs, hard blockers and `unknowns`.
- `kairo resilience` and `POST /api/v1/resilience`: capacity-type/instance-type/zone diversity, pool and zone loss simulation without PDBs, and the findings `RESILIENCE_LOSS_UNSCHEDULABLE`, `RESILIENCE_WORKLOAD_IN_ONE_POOL`, `RESILIENCE_POOL_CONCENTRATION`, `RESILIENCE_FEW_INSTANCE_TYPES`.
- Web console views for disruption risk, right-sizing (paste usage JSON) and pool loss.
- `docs/RESEARCH-disruption-rightsizing.md` (field research, ranked ideas, claims not read from a source marked), `examples/rightsize-cluster.yaml`, `examples/usage.json`, `examples/spot-cluster.yaml`, OpenAPI schemas and e2e coverage for all three.
- `kairo consolidate` and `POST /api/v1/consolidate`: which nodes could be emptied by repacking existing pods (requests, nodeSelector, taints, cordons, PDBs, unmanaged and do-not-disrupt pods), the pods that move, node count before and after, CPU/memory/GPU reclaimed and requested utilisation. Results always list what is not modelled (affinity, topology spread, PV topology, observed usage, and more).
- Optional cost estimates: `-node-hour-price`, `-gib-hour-price`, `-currency` on `plan` and `consolidate`, and `prices` in the API. A monthly delta over a 730-hour month from user-supplied prices, labelled an estimate; no cloud API is called.
- ResourceQuota and LimitRange admission checks for changed workloads and PVCs: `ADMISSION_QUOTA_EXCEEDED`, `ADMISSION_QUOTA_NO_REQUESTS`, `ADMISSION_LIMITRANGE_MAX`, `ADMISSION_LIMITRANGE_MIN`, `ADMISSION_LIMITRANGE_RATIO`; new `limitRangeRisks` counter. All BLOCK.
- Web console views for node drain, consolidation, rollout surge, API removal (target-version selector) and the `plan -explain` change list, and price inputs.
- `docs/openapi.yaml` and tests that keep it in sync with the registered routes and the JSON types.
- `examples/consolidate-cluster.yaml`; e2e coverage for consolidation, prices and the UI assets.

### Changed

- `QUOTA_PRESSURE` is replaced by namespace-aware `ADMISSION_QUOTA_EXCEEDED`.

### Fixed

- `consolidate` ignored a Node annotated `karpenter.sh/do-not-disrupt=true` and could report it removable.
- The README licence badge said Zyvor Production 1.0; the licence is v2.0.
- `QUOTA_PRESSURE` compared cluster-wide request totals with one namespace's quota, producing false BLOCK verdicts for unrelated namespaces.
- `cpuDeltaCores`, `memoryDeltaBytes` and `gpuDelta` subtracted the whole cluster's requests from the proposed objects' requests; they now compare the proposed objects with the live objects they replace.
- The web console no longer interpolates the risk severity into markup unescaped.
