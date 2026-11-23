# Changelog

All notable changes to Kairo are documented here.

## Unreleased

### Added

- `kairo consolidate` and `POST /api/v1/consolidate`: which nodes could be emptied by repacking existing pods (requests, nodeSelector, taints, cordons, PDBs, unmanaged and do-not-disrupt pods), the pods that move, node count before and after, CPU/memory/GPU reclaimed and requested utilisation. Results always list what is not modelled (affinity, topology spread, PV topology, observed usage, and more).
- Optional cost estimates: `-node-hour-price`, `-gib-hour-price`, `-currency` on `plan` and `consolidate`, and `prices` in the API. A monthly delta over a 730-hour month from user-supplied prices, labelled an estimate; no cloud API is called.
- ResourceQuota and LimitRange admission checks for changed workloads and PVCs: `ADMISSION_QUOTA_EXCEEDED`, `ADMISSION_QUOTA_NO_REQUESTS`, `ADMISSION_LIMITRANGE_MAX`, `ADMISSION_LIMITRANGE_MIN`, `ADMISSION_LIMITRANGE_RATIO`; new `limitRangeRisks` counter. All BLOCK.
- Web console views for node drain, consolidation, rollout surge, API removal (target-version selector) and the `plan -explain` change list, and price inputs.
- `docs/openapi.yaml` and tests that keep it in sync with the registered routes and the JSON types.
- `examples/consolidate-cluster.yaml`; e2e coverage for consolidation, prices and the UI assets.

### Changed

- `QUOTA_PRESSURE` is replaced by namespace-aware `ADMISSION_QUOTA_EXCEEDED`.

### Fixed

- `QUOTA_PRESSURE` compared cluster-wide request totals with one namespace's quota, producing false BLOCK verdicts for unrelated namespaces.
- `cpuDeltaCores`, `memoryDeltaBytes` and `gpuDelta` subtracted the whole cluster's requests from the proposed objects' requests; they now compare the proposed objects with the live objects they replace.
- The web console no longer interpolates the risk severity into markup unescaped.
