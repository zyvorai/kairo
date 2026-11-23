# Research: comparable tools and what Kairo adds

Kairo's identity is a deterministic, offline "what happens if I apply this?" simulation of a cluster snapshot plus a proposed change. This survey picks features that extend that identity rather than turning Kairo into a linter.

Method note: web search results (Pluto, kubent, Karpenter docs) were read; the rest of the table is from the projects' public READMEs and docs as known to the author and were not all re-fetched during this pass. The API-removal table in `internal/sim/apis.go` should be re-verified against the official deprecation guide on each release.

## Survey

| Tool | What it does | Static / live / simulation | Gap for Kairo |
|---|---|---|---|
| kube-score, Polaris, Datree, Popeye | Lint manifests or sanitize a live cluster against best-practice rules (limits, probes, security) | Static or live; no simulation | Kairo already has security/exposure/RBAC checks; a bigger rule catalogue is a commodity and not differentiating |
| Kubescape | Security posture scanning against NSA/CIS frameworks | Static/live | Out of scope (compliance frameworks) |
| Kyverno / OPA (Gatekeeper, conftest) | Policy-as-code admission and CI checks | Rule evaluation | Kairo has fixed rules; user-defined policy is a large surface (needs a policy language) |
| Pluto, kubent | Find deprecated and removed API versions in manifests, Helm releases or a live cluster, against a target version | Static table lookup | Kairo had no API-lifecycle check, and cannot say "this fails on 1.25" |
| Nova | Finds outdated Helm charts and container images | Registry lookup | Needs network; not deterministic |
| Argo Rollouts, Flagger | Progressive delivery: canary/blue-green with metric analysis at runtime | Live runtime | They act after deploy; Kairo's job is pre-deploy. Rollout strategy parameters are still modelled |
| kube-capacity | Prints requests/limits/utilization per node | Live read-only | Kairo's node-fit table covers the capacity view |
| Karpenter consolidation / disruption | Simulates removing nodes: pods must reschedule elsewhere and PDBs are honoured through the Eviction API | Live simulation inside the controller | Not available pre-hoc as a CLI; Kairo has the node and pod model needed |
| Cluster-autoscaler simulator | Scale-down simulation: can pods on a node move to other nodes (checks PDBs, selectors, taints, unmanaged pods) | Live simulation | Same gap |
| Kairo (before this change) | Change-set simulation: scheduling fit, HPA headroom, PVC shrink, quota, PDB, security | Offline simulation | No rollout-time view, no drain/upgrade view, no API lifecycle |

## Ranked ideas

1. **Rolling-update peak-capacity simulation (built).** Kubernetes Deployments by default surge 25% extra pods during a rollout. A change that fits at steady state can still leave the rollout stuck (`maxUnavailable: 0`) or degraded. Argo Rollouts/Flagger only discover this at runtime; none of the linters model it. It reuses Kairo's existing scheduler model, is fully deterministic, and is a natural "change-risk" signal. Finding code: `ROLLOUT_SURGE_UNSCHEDULABLE`.
2. **Node drain / upgrade-wave simulation (built).** The cluster-autoscaler and Karpenter both embed this logic (reschedule evicted pods, honour PDBs through the Eviction API, skip DaemonSet and static pods, refuse pods without a controller), but neither exposes it as a pre-flight CLI or API. Cluster upgrades are the most common reason PDBs block work, so it is high-value and fits Kairo's snapshot model exactly. Supports draining several nodes together (one upgrade wave).
3. **Removed/deprecated API detection with `-target-version` (built).** Pluto and kubent prove demand. Including it in Kairo means one verdict covers capacity, risk and upgrade-readiness, and the target version makes it a simulation ("on 1.25 this is rejected") rather than a lint.

Rejected or deferred:

- **Policy-as-code engine (Kyverno/OPA style):** needs a rule language and evaluator; large and overlaps mature tools. Deferred.
- **Rollout risk timeline (step-by-step surge/unavailable schedule):** needs a controller model of ReplicaSet scaling and readiness timing; the peak check captures most of the value at a fraction of the complexity.
- **Image/chart freshness (Nova):** requires network access and is not deterministic.
- **More lint rules (kube-score/Polaris):** commodity, not differentiating.
- **Karpenter-style consolidation (which nodes could be removed to save cost):** deferred at first; now built as `kairo consolidate` on the drain engine (see Follow-up below).

## Follow-up: consolidation, cost and admission

- **Consolidation (built).** The deferred idea from above. It reuses drain's placement and PDB logic and adds a greedy emptiest-first, best-fit-decreasing packer. Rejected for now: optimal bin packing (NP-hard, unneeded for a preflight; the plan is labelled a heuristic), affinity/topology spread/PV topology (listed as not modelled in every result), and per-pod request right-sizing (needs observed usage, which a snapshot does not carry).
- **Cost awareness (built, narrowly).** Only user-supplied node-hour and GiB-hour prices, a fixed 730-hour month and an explicit estimate label. Rejected: pulling cloud price lists (network, non-deterministic, stale) and reserved/spot discount models.
- **Quota and LimitRange admission (built).** Fills the gap the earlier `QUOTA_PRESSURE` check left (it ignored namespaces). Still rejected: a general policy-as-code engine; these are two fixed, well-specified admission plugins.
- **Rollout timeline** stays rejected, as before.

## Sources

- Pluto: https://github.com/FairwindsOps/pluto and https://www.cncf.io/blog/2021/10/26/welcome-to-pluto-the-place-to-start-with-open-source-development/
- kubent (kube-no-trouble): https://github.com/doitintl/kube-no-trouble
- Karpenter disruption (Eviction API, PDBs, retry on blocked nodes): https://karpenter.sh/docs/concepts/disruption/
- Karpenter PDB blocking drains, example report: https://github.com/kubernetes-sigs/karpenter/issues/1776
- Kubernetes deprecated API migration guide (basis for the table): https://kubernetes.io/docs/reference/using-api/deprecation-guide/
- Kubernetes Deployment rolling update (maxSurge/maxUnavailable defaults): https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#rolling-update-deployment
- Kubernetes PDB semantics: https://kubernetes.io/docs/tasks/run-application/configure-pdb/
- Safely draining a node: https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/
- kube-score: https://github.com/zegl/kube-score; Polaris: https://github.com/FairwindsOps/polaris; Kubescape: https://github.com/kubescape/kubescape; Popeye: https://github.com/derailed/popeye; kube-capacity: https://github.com/robscott/kube-capacity; Nova: https://github.com/FairwindsOps/nova; Datree: https://github.com/datreeio/datree; Argo Rollouts: https://argo-rollouts.readthedocs.io; Flagger: https://flagger.app; Kyverno: https://kyverno.io; cluster-autoscaler: https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler
