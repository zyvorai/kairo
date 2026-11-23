# Research: disruption risk, right-sizing and capacity resilience

This extends [RESEARCH.md](RESEARCH.md) (which covers the lint, policy and change-simulation landscape and why consolidation, drain and rollout simulation were built). It looks at the cost-optimisation and node-consolidation field, picks what a read-only snapshot tool can add, and records what was built.

Method and honesty note. Claims marked **[U]** were not read from a fetched page: they come from memory, from search-result snippets, or from vendor marketing that was not corroborated. Unmarked claims come from a page that was fetched and read in this pass (listed under Sources as "fetched"). Pages that were truncated by the fetch tool say so. Dates: this pass was done in November 2026; "new in the last year" statements are only as good as the page that states them.

## Hard boundaries Kairo keeps

Kairo is advisory and offline: it reads a snapshot you supply, simulates, and reports. It never calls the Kubernetes API, a cloud API, a metrics system or a pricing API, and never changes a cluster. Every idea below was filtered through that: usage data and prices must be supplied by the user, and the output is a recommendation with its inputs, not an action.

## What the field does, and what users complain about

**Karpenter** (fetched). Consolidation policies are `WhenEmpty`, `WhenEmptyOrUnderutilized` (default) and `Balanced`, which scores cost savings against pod disruption and skips actions where savings are small relative to disruption. `consolidateAfter` (default 0s) is a stability timer. Mechanisms run in order: empty nodes, multi-node, single-node. Disruption budgets limit voluntary disruption by node count or percentage (default 10%), per reason (`Drifted`, `Underutilized`, `Empty`, `Unhealthy`), with cron `schedule` and `duration` windows in UTC; the most restrictive active budget wins. Candidates are ordered by fewer pods, expiring soon, lower-priority pods and lower total `karpenter.sh/disruption-cost`; each pod's cost contribution is capped. `karpenter.sh/do-not-disrupt` can be `"true"` or a duration such as `"30m"` (protection after the pod starts). Preferred anti-affinity and topology spread are honoured to avoid churn. Spot-to-spot single-node replacement needs at least 15 instance types in the launch request; deletion consolidation of spot nodes is on by default.

**Cluster Autoscaler** (fetched; the FAQ page was truncated at 100,000 of 111,553 characters, so later sections were not read). A node is unneeded when requested CPU and memory are below `--scale-down-utilization-threshold` (default 0.5) and all non-DaemonSet pods can move; it must stay unneeded for `--scale-down-unneeded-time` (default 10m). Blockers: restrictive PDBs, pods without a controller, local storage (`hostPath` or non-memory `emptyDir`, overridable with `safe-to-evict-local-volumes`), pods that cannot be rescheduled elsewhere, and `safe-to-evict: "false"`. Expanders: random, most-pods, least-waste (default), price (GCE/GKE/Equinix Metal only per the page), priority; chainable since 1.23. Overprovisioning uses low-priority pause pods that are preempted when real pods need room.

**Descheduler** (fetched). `HighNodeUtilization` evicts pods from underutilised nodes so they pack (paired with `MostAllocated` scoring); `LowNodeUtilization` and `RemovePodsViolatingTopologySpreadConstraint` rebalance. It does not evict bare pods, DaemonSet pods, critical pods or (by default) pods with local storage, and it evicts through the Eviction API so PDBs hold. Utilisation defaults to requests, not live usage; metrics-based utilisation is optional. Newer releases add `podProtections` (for example `PodsWithPVC`, `PodsWithoutPDB`) and eviction caps.

**In-place pod resize** (fetched; the page was truncated at 100,000 of 115,218 characters, so `resizePolicy` and the memory-decrease constraints were not read). The page states CPU/memory in-place resize is stable since Kubernetes v1.35 (first available v1.27). Resize outcomes surface as `PodResizePending` (`Infeasible`, `Deferred`) and `PodResizeInProgress`. The scheduler uses the maximum of desired, allocated and actual requests while a resize is pending. Consequence for Kairo: a right-sizing recommendation can now be applied without a restart, which makes "what happens to packing if requests drop" a live question, but a Deferred resize can also sit unapplied when a node is full.

**Dynamic Resource Allocation** (fetched). The page states DRA is GA since v1.35, with `ResourceSlice`, `DeviceClass` and `ResourceClaim` objects, and no scheduler preemption for DRA devices. Device accounting moves out of `requests` into claims, so a requests-only bin-packer such as Kairo's cannot see DRA-allocated devices. Not modelled; listed as a limitation.

**Cast AI** (fetched blog; vendor marketing). It describes native consolidation limits (topology spread and tight PDBs block consolidation, mixed memory-heavy and CPU-heavy pods pack poorly, stateful pods accumulate do-not-disrupt) and sells an evictor, a rebalancer that provisions replacements before draining, and CRIU-based live migration in early access. Its benchmark figure (43% more savings than native Karpenter over 7 days) has no published methodology on the page; treat it as unverified.

**What users complain about** (search results only, secondary blogs; **[U]** for the specifics): the default 10% budget causing consolidation waves during deploys, very short `consolidateAfter` causing churn, consolidation without PDBs on stateful sets, and do-not-disrupt not protecting against spot reclaim. These are consistent with the Karpenter page, which says budgets limit only voluntary disruption.

**Right-sizing (VPA, Goldilocks, Kubecost/OpenCost, StormForge).** A search surfaced secondary blogs describing VPA's recommender as percentile-based (reported defaults target percentile 0.9 and a 15% margin, with an OOM bump of 1.2x and 100 MiB), and Goldilocks as a dashboard over recommendation-only VPA objects **[U]**. The official VPA components page that was fetched does not state any of those parameters. A FinOps guide suggested 15-25% extra memory headroom **[U]**. Kubecost/OpenCost, StormForge and Spot Elastigroup were not fetched in this pass **[U]**; from memory they provide usage-based request recommendations and spot-aware node provisioning as live services, which a snapshot tool cannot replicate. kube-green's page returned 404, so its claims are **[U]**: from memory it schedules workloads to sleep outside working hours.

**Spot diversification** (search snippets, **[U]** for details). AWS guidance is to diversify across instance types, sizes and zones, because a capacity pool is one instance type in one zone; Karpenter's spot-to-spot guidance in the fetched Karpenter page uses 15 types as a flexibility signal for single-node replacement.

## Gap analysis against Kairo

Before this round Kairo could answer "which nodes could be emptied" (consolidate) and "what happens if I drain these" (drain), but:

1. Consolidation ranked nothing by how disruptive removal is. Karpenter orders by pod count and disruption cost; users complain about churn and stateful casualties. A snapshot has the inputs to explain that risk (PDBs, replicas, volumes, spread constraints, probes).
2. Consolidation used requests only and said so ("not request right-sizing"). The most common source of waste is requests far above usage, and the field's answer (VPA, Kubecost, StormForge) is usage-based recommendations. Kairo could not say "if you set requests from this usage, you would free N nodes".
3. Nothing looked at concentration. Spot reclaim is involuntary: PDBs do not apply, and the question is what a lost pool or zone strands.

## Ranked ideas

| # | Idea | Rationale | Source | Fit with Kairo's boundaries | Effort | Status |
|---|---|---|---|---|---|---|
| 1 | Right-sizing what-if with an explicit margin model and a confidence band, fed through consolidation to show nodes and cost as a band | Waste is mostly over-requesting; recommenders exist but none show the downstream packing effect offline; uncertainty is usually hidden | Karpenter disruption docs (consolidation inputs); VPA percentile design (secondary, **[U]**): https://github.com/kubernetes/autoscaler/tree/master/vertical-pod-autoscaler | Good: user supplies samples and prices; advisory; deterministic | M | Built: `kairo rightsize` |
| 2 | Per-node disruption-risk score from PDBs, single-replica, all-replicas-here, local state, placement constraints and declared probe startup time | Karpenter orders candidates by pod count/cost; CA and descheduler list the same blockers; users want to know which node is safest to remove first | https://karpenter.sh/docs/concepts/disruption/ ; https://github.com/kubernetes-sigs/descheduler | Good: pure function of the snapshot; unknowns reported | M | Built: `kairo disruption` |
| 3 | Capacity-pool and zone loss simulation plus diversification report | Spot reclaim ignores PDBs; AWS guidance centres on pool diversity **[U]**; Karpenter's 15-type signal | https://karpenter.sh/docs/concepts/disruption/ (15 types); AWS spot guidance **[U]** | Good: node labels are in the snapshot; reuses the drain engine without PDBs | M | Built: `kairo resilience` |
| 4 | Disruption-budget window simulator: given a Karpenter budget (percent, schedule, reasons), how many rounds/hours would the consolidation plan take | Budgets are the main user control and a frequent surprise | https://karpenter.sh/docs/concepts/disruption/ | Good, but needs the NodePool object in the snapshot and cron evaluation | S-M | Next |
| 5 | Savings forecast over time with uncertainty from sample windows (seasonality) | Short windows understate peaks | **[U]** | Needs long, timestamped series; the current model treats samples as independent | M | Deferred |
| 6 | Karpenter `disruption-cost` / `Balanced` scoring replica | Compare Kairo's score with the controller's own ordering | https://karpenter.sh/docs/concepts/disruption/ | Fine, but the controller's cost model is version-specific; risk of drift | M | Deferred |
| 7 | Time-bound `do-not-disrupt` ("30m") handling in consolidation | Page states durations are valid | https://karpenter.sh/docs/concepts/disruption/ | Needs pod start times; currently reported as unknown in `disruption`, treated as non-blocking | S | Partly (reported) |
| 8 | DRA-aware GPU packing (ResourceSlice/ResourceClaim) | DRA is GA per the fetched page; devices leave `requests` | https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/ | Requires modelling claims and device attributes; large | L | Rejected for now |
| 9 | Carbon-aware scheduling / kube-green style sleep schedules | Popular topic | kube-green page 404, **[U]** | Needs external carbon data or acts on the cluster; conflicts with offline/read-only | L | Rejected |
| 10 | Live metrics collection (Prometheus) for right-sizing | What Kubecost/StormForge do | **[U]** | Violates the no-network, snapshot-only boundary; users export samples instead | L | Rejected |
| 11 | Expander (random, least-waste, price) comparison | CA expanders decide which node group scales up | https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md | Scale-up needs node-group definitions not in a snapshot | M | Rejected |

## What was built and how it works

**`kairo rightsize`** (`internal/sim/rightsize.go`). Input: a snapshot plus per-container CPU (cores) and memory (bytes) samples. For each series: nearest-rank percentile (default CPU p95, memory p99), a margin (15% CPU, 20% memory), a floor (10m, 32 MiB), rounded up. The uncertainty band is a distribution-free confidence interval for the true percentile from order statistics and the binomial distribution (default 90%), so "optimistic" uses the lower bound, "expected" the observed percentile and "conservative" the upper bound. When too few samples exist to bound a side, the interval is reported as open and clamped to a sample; for p99 that happens below roughly 300 samples, and for a peak (percentile 1) the upper side is always open. Series below `minSamples` (30) leave that resource unchanged and say so. Each scenario rewrites the matched pods' requests and re-runs the consolidation engine, giving removable nodes and a cost band (when prices are supplied). Containers without a series are listed as missing data and keep today's requests. Warnings: samples above the recommendation (OOM-kill risk), recommendations above the container limit, under-requested containers, all-zero series. Weak points are listed in `notModelled`: no seasonality, independence assumption, no limits/QoS/HPA interaction.

**`kairo disruption`** (`internal/sim/disruption.go`). A 100-point weighted sum with the weights and saturation points published in every result: PDB pressure 25, single-replica workloads 20, all replicas on this node 15, local state (emptyDir disk, hostPath, PVC) 15, placement constraints (topology spread, required anti-affinity; detected, not evaluated) 10, declared probe startup time 15. Hard blockers (unmanaged pod, do-not-disrupt, node annotation) set the level to BLOCKED. Startup time is read only from probes; pods without probes are listed under `unknowns`, never defaulted. The weights are judgement calls and are labelled as such.

**`kairo resilience`** (`internal/sim/resilience.go`). Groups nodes by capacity type, instance type and zone labels (Karpenter, EKS, GKE spot and AKS spot label names; the non-Karpenter label names are from memory **[U]**), reports diversity, and simulates losing each spot or unknown-capacity pool and each zone at once using the drain engine with PDBs removed. Findings: pool-loss strands pods, one pool dominates spot CPU (over 50%, a judgement call), fewer than 3 spot instance types, a workload entirely inside one pool. Missing labels are grouped as "unknown".

## Rejected and why

See rows 8 to 11. In short: DRA and carbon-aware ideas need data a snapshot does not carry or an external feed; live metrics collection and price lookups break the offline, no-network boundary; expander comparison needs node-group definitions.

## Sources

Fetched and read:

- Karpenter disruption: https://karpenter.sh/docs/concepts/disruption/
- Cluster Autoscaler FAQ (first 100,000 of 111,553 characters): https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md
- In-place pod resize (first 100,000 of 115,218 characters): https://kubernetes.io/docs/tasks/configure-pod-container/resize-container-resources/
- Descheduler README: https://github.com/kubernetes-sigs/descheduler
- Dynamic Resource Allocation: https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/
- Cast AI on Karpenter consolidation (vendor marketing): https://cast.ai/blog/karpenter-consolidation/
- VPA components page (did not contain recommender parameters): https://github.com/kubernetes/autoscaler/blob/master/vertical-pod-autoscaler/docs/components.md

Search results only, not fetched **[U]**: Karpenter churn write-ups (https://zesty.co/finops-academy/kubernetes/how-to-avoid-surprise-node-churn-in-karpenter/, https://dev.to/muskan_8abedcc7e12/karpenter-consolidation-6-settings-worth-tuning-in-2026-4bo6), VPA/Goldilocks blogs (https://oneuptime.com/blog/post/2026-02-09-goldilocks-vpa-rightsize-pods/view, https://oneuptime.com/blog/post/2026-08-25-vpa-startup-spikes-oom-events/view), AWS spot guidance (https://aws.amazon.com/blogs/compute/applying-spot-to-spot-consolidation-best-practices-with-karpenter, https://aws.amazon.com/blogs/containers/using-amazon-ec2-spot-instances-with-karpenter).

Not fetched **[U]**: Kubecost/OpenCost, StormForge, Spot Elastigroup (Spot by NetApp), Goldilocks docs; kube-green (https://kube-green.dev/docs/what-is-kube-green/ returned 404).
