// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// maxReplicas bounds per-object replicas so untrusted input cannot pin the CPU.
const maxReplicas = 100000

type nodeState struct {
	name          string
	cap           resources
	free          resources
	unschedulable bool
	taints        []taint
	labels        map[string]string
}

func Simulate(req Request) (Result, error) {
	target, err := parseTargetVersion(req.TargetVersion)
	if err != nil {
		return Result{}, err
	}
	cluster, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return Result{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	desired, err := ParseDocuments(req.DesiredYAML)
	if err != nil {
		return Result{}, fmt.Errorf("parse desired manifests: %w", err)
	}
	res := Result{TargetVersion: target.String(), ClusterName: "kubernetes", ObjectsAnalyzed: len(cluster) + len(desired), Risks: []Risk{}, Recommendations: []string{}}
	live := map[string]map[string]any{}
	for _, o := range cluster {
		live[keyFor(o)] = o
		if str(o["kind"]) == "ConfigMap" && str(get(o, "metadata", "name")) == "kairo-cluster-info" {
			if n := str(get(o, "data", "clusterName")); n != "" {
				res.ClusterName = n
			}
		}
	}
	desiredMap := map[string]map[string]any{}
	for _, o := range desired {
		desiredMap[keyFor(o)] = o
	}
	// A repeated object replaces the earlier one (as kubectl apply would), so
	// it must not be scheduled twice.
	deduped := make([]map[string]any, 0, len(desiredMap))
	seen := map[string]bool{}
	for i := len(desired) - 1; i >= 0; i-- {
		k := keyFor(desired[i])
		if !seen[k] {
			seen[k] = true
			deduped = append(deduped, desired[i])
		}
	}
	for i, j := 0, len(deduped)-1; i < j; i, j = i+1, j-1 {
		deduped[i], deduped[j] = deduped[j], deduped[i]
	}
	desired = deduped
	for _, o := range desired {
		if r := replicasFor(o, 0); r > maxReplicas {
			_, ns, name := metadata(o)
			return Result{}, fmt.Errorf("%s %s/%s: replicas %d exceeds the supported maximum of %d", str(o["kind"]), ns, name, r, maxReplicas)
		}
	}
	for k, o := range desiredMap {
		if old, ok := live[k]; !ok {
			res.CreatedObjects++
		} else if canonical(old) != canonical(o) {
			res.ChangedObjects++
		}
	}
	for k := range live {
		if _, ok := desiredMap[k]; !ok && isManagedKind(live[k]) {
			res.DeletedObjects++
		}
	}

	for _, o := range desired {
		if rs := apiVersionRisks(o, target); len(rs) > 0 {
			for _, r := range rs {
				if r.Code == "API_REMOVED" {
					res.APIRemoved++
				} else {
					res.APIDeprecated++
				}
			}
			res.Risks = append(res.Risks, rs...)
		}
	}

	nodes := buildNodes(cluster)
	hpas := collectHPAs(cluster, desired)
	accountExistingPods(nodes, cluster)
	// Compare like with like: only live objects the change replaces count as "before".
	var replaced []map[string]any
	for _, o := range cluster {
		if _, ok := desiredMap[keyFor(o)]; ok {
			replaced = append(replaced, o)
		}
	}
	baseCPU, baseMem, baseGPU := desiredResourceTotals(replaced, len(nodes))
	newCPU, newMem, newGPU := desiredResourceTotals(desired, len(nodes))
	res.CPUDeltaCores = round2(newCPU - baseCPU)
	res.MemoryDeltaBytes = newMem - baseMem
	res.GPUDelta = newGPU - baseGPU
	if err := req.Prices.validate(); err != nil {
		return Result{}, err
	}
	res.Cost = planCost(req.Prices, res.CPUDeltaCores, res.MemoryDeltaBytes, nodes)

	for _, obj := range desired {
		kind, ns, name := metadata(obj)
		old := live[keyFor(obj)]
		changed := old == nil || canonical(old) != canonical(obj)
		if !changed {
			continue
		}
		replicas := replicasFor(obj, len(nodes))
		spec := podSpecFor(obj)
		if spec != nil && (replicas > 0 || kind == "DaemonSet") {
			rq := resourceReq(spec)
			pl := placementFor(spec, kind == "DaemonSet")
			unscheduled := 0
			if kind == "DaemonSet" {
				replicas, unscheduled = scheduleDaemonSet(nodes, rq, pl)
			} else {
				unscheduled = schedule(nodes, rq, replicas, pl)
			}
			if old != nil {
				res.PodsRestarted += replicas
			} else {
				res.PodsCreated += replicas
			}
			if unscheduled > 0 {
				res.UnschedulablePods += unscheduled
				res.Risks = append(res.Risks, Risk{"high", "UNSCHEDULABLE", "Workload cannot fully schedule", fmt.Sprintf("%d of %d replicas cannot fit on the current node capacity.", unscheduled, replicas), ResourceRef{kind, ns, name}})
			}
			if unscheduled == 0 {
				if rr := rolloutSurgeRisk(old, obj, replicas, nodes, rq, pl); rr != nil {
					res.RolloutRisks++
					if rr.Severity == "high" {
						res.RolloutStalls++
					}
					res.Risks = append(res.Risks, *rr)
				}
			}
			if hpa := findHPA(hpas, obj); hpa != nil {
				hr := hpaRisks(hpa, obj, replicas, nodes, rq, pl)
				res.HPARisks += len(hr)
				res.Risks = append(res.Risks, hr...)
			}
		}
		if rs, c := extraRisks(obj, old); len(rs) > 0 {
			res.Risks = append(res.Risks, rs...)
			res.SecurityRisks += c.security
			res.ExposureChanges += c.exposure
			res.RBACRisks += c.rbac
		}
		switch kind {
		case "NetworkPolicy", "CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy":
			res.NetworkPolicyChanges++
			res.Risks = append(res.Risks, Risk{"medium", "NETWORK_POLICY_CHANGE", "Network reachability may change", "A network policy is being created or modified. Validate observed production flows before rollout.", ResourceRef{kind, ns, name}})
		case "PersistentVolumeClaim":
			if old != nil {
				oldSize := parseBytes(get(old, "spec", "resources", "requests", "storage"))
				newSize := parseBytes(get(obj, "spec", "resources", "requests", "storage"))
				if oldSize > 0 && newSize > 0 && newSize < oldSize {
					res.PVCDestructiveRisks++
					res.Risks = append(res.Risks, Risk{"critical", "PVC_SHRINK", "PVC shrink is destructive or unsupported", fmt.Sprintf("Requested storage decreases from %s to %s.", humanBytes(oldSize), humanBytes(newSize)), ResourceRef{kind, ns, name}})
				}
			}
		case "PodDisruptionBudget":
			min := get(obj, "spec", "minAvailable")
			maxUn := get(obj, "spec", "maxUnavailable")
			if min != nil && str(min) == "100%" || maxUn != nil && (str(maxUn) == "0" || str(maxUn) == "0%") {
				res.PDBRisks++
				res.Risks = append(res.Risks, Risk{"medium", "PDB_STRICT", "PDB may block voluntary disruptions", "The proposed PDB allows no voluntary unavailability and can block drains or upgrades.", ResourceRef{kind, ns, name}})
			}
		}
	}

	if rs, c := admissionRisks(cluster, desired, len(nodes)); len(rs) > 0 {
		res.Risks = append(res.Risks, rs...)
		res.QuotaRisks += c.quota
		res.LimitRangeRisks += c.limitRange
	}

	res.ServicesAffected = estimateServiceImpact(cluster, desired, res.PodsRestarted, res.UnschedulablePods)
	score := 0
	score += minInt(45, res.UnschedulablePods*12)
	score += res.PVCDestructiveRisks * 40
	score += minInt(50, res.QuotaRisks*25)
	score += minInt(50, res.LimitRangeRisks*25)
	score += res.PDBRisks * 12
	score += minInt(15, res.NetworkPolicyChanges*8)
	score += minInt(30, res.SecurityRisks*15)
	score += minInt(16, res.ExposureChanges*8)
	score += minInt(45, res.RBACRisks*20)
	score += minInt(20, res.HPARisks*10)
	score += minInt(24, res.RolloutStalls*20+(res.RolloutRisks-res.RolloutStalls)*8)
	score += minInt(60, res.APIRemoved*40+res.APIDeprecated*5)
	if res.PodsRestarted > 0 {
		score += minInt(12, int(math.Ceil(float64(res.PodsRestarted)/10)))
	}
	if res.ServicesAffected > 0 {
		score += minInt(10, res.ServicesAffected*3)
	}
	if score > 100 {
		score = 100
	}
	res.BlastRadius = score
	switch {
	case score >= 60:
		res.RiskLevel = "HIGH"
	case score >= 30:
		res.RiskLevel = "MEDIUM"
	default:
		res.RiskLevel = "LOW"
	}
	if res.PVCDestructiveRisks > 0 || res.UnschedulablePods > 0 || res.QuotaRisks > 0 || res.LimitRangeRisks > 0 || res.APIRemoved > 0 {
		res.Verdict = "BLOCK"
	} else if score >= 30 || res.HPARisks > 0 || res.RolloutRisks > 0 {
		res.Verdict = "REVIEW"
	} else {
		res.Verdict = "SAFE"
	}
	res.Changes = diffObjects(live, desiredMap)
	sortRisks(res.Risks)
	res.Recommendations = recommendations(res)
	for _, n := range nodes {
		res.Nodes = append(res.Nodes, NodeResult{n.name, round2(n.cap.cpu), round2(n.free.cpu), n.cap.mem, n.free.mem, n.cap.gpu, n.free.gpu})
	}
	sort.Slice(res.Nodes, func(i, j int) bool { return res.Nodes[i].Name < res.Nodes[j].Name })
	return res, nil
}

func isManagedKind(o map[string]any) bool {
	switch str(o["kind"]) {
	case "Deployment", "StatefulSet", "DaemonSet", "Service", "Ingress", "NetworkPolicy", "CiliumNetworkPolicy", "PersistentVolumeClaim", "PodDisruptionBudget", "VirtualMachine":
		return true
	}
	return false
}
func buildNodes(objs []map[string]any) []*nodeState {
	var out []*nodeState
	for _, o := range objs {
		if str(o["kind"]) != "Node" {
			continue
		}
		name := str(get(o, "metadata", "name"))
		alloc := asMap(get(o, "status", "allocatable"))
		cap := resources{parseCPU(alloc["cpu"]), parseBytes(alloc["memory"]), int64(intValue(alloc["nvidia.com/gpu"], 0))}
		n := &nodeState{name: name, cap: cap, free: cap, labels: map[string]string{}}
		if b, ok := get(o, "spec", "unschedulable").(bool); ok {
			n.unschedulable = b
		} else {
			n.unschedulable = str(get(o, "spec", "unschedulable")) == "true"
		}
		for _, t := range asSlice(get(o, "spec", "taints")) {
			tm := asMap(t)
			n.taints = append(n.taints, taint{str(tm["key"]), str(tm["value"]), str(tm["effect"])})
		}
		for k, v := range asMap(get(o, "metadata", "labels")) {
			n.labels[k] = str(v)
		}
		out = append(out, n)
	}
	return out
}
func accountExistingPods(nodes []*nodeState, objs []map[string]any) {
	by := map[string]*nodeState{}
	for _, n := range nodes {
		by[n.name] = n
	}
	for _, o := range objs {
		if str(o["kind"]) != "Pod" {
			continue
		}
		node := by[str(get(o, "spec", "nodeName"))]
		if node == nil || podTerminated(o) {
			continue
		}
		r := resourceReq(asMap(o["spec"]))
		node.free.cpu -= r.cpu
		node.free.mem -= r.mem
		node.free.gpu -= r.gpu
	}
}
func desiredResourceTotals(objs []map[string]any, nodeCount int) (float64, int64, int64) {
	var c float64
	var m, g int64
	for _, o := range objs {
		sp := podSpecFor(o)
		if sp == nil {
			continue
		}
		r := resourceReq(sp)
		rep := replicasFor(o, nodeCount)
		if str(o["kind"]) == "Pod" && str(get(o, "spec", "nodeName")) != "" {
			rep = 1
		}
		c += r.cpu * float64(rep)
		m += r.mem * int64(rep)
		g += r.gpu * int64(rep)
	}
	return c, m, g
}
func estimateServiceImpact(cluster, desired []map[string]any, restarts, uns int) int {
	if restarts == 0 && uns == 0 {
		return 0
	}
	services := 0
	for _, o := range cluster {
		if str(o["kind"]) == "Service" {
			services++
		}
	}
	if services == 0 {
		for _, o := range desired {
			if str(o["kind"]) == "Service" {
				services++
			}
		}
	}
	if services == 0 {
		return 0
	}
	impacted := 1 + uns/3
	if impacted > services {
		impacted = services
	}
	return impacted
}
func recommendations(r Result) []string {
	var x []string
	if r.UnschedulablePods > 0 {
		x = append(x, "Add node capacity or reduce workload requests before deployment; inspect the node-fit table.")
	}
	if r.PVCDestructiveRisks > 0 {
		x = append(x, "Do not shrink PVCs in place. Create a migration/restore plan to a new volume.")
	}
	if r.NetworkPolicyChanges > 0 {
		x = append(x, "Replay observed service flows against the proposed network policy before enforcing it.")
	}
	if r.PDBRisks > 0 {
		x = append(x, "Relax the PDB during controlled maintenance or ensure spare replicas exist.")
	}
	if r.QuotaRisks > 0 {
		x = append(x, "Raise the namespace ResourceQuota, reduce replicas/requests, or set requests and limits so quota admission accepts the pods.")
	}
	if r.LimitRangeRisks > 0 {
		x = append(x, "Bring container requests/limits inside the namespace LimitRange min/max (or change the LimitRange) before applying.")
	}
	if r.SecurityRisks > 0 {
		x = append(x, "Remove privileged, host-namespace and hostPath settings, or document why the workload needs them.")
	}
	if r.ExposureChanges > 0 {
		x = append(x, "Confirm every new Service or Ingress exposure is intended and restricted (source ranges, TLS, auth).")
	}
	if r.RBACRisks > 0 {
		x = append(x, "Replace wildcard or cluster-admin grants with least-privilege roles.")
	}
	if r.HPARisks > 0 {
		x = append(x, "Raise node capacity for the HPA maxReplicas, lower maxReplicas, or align minReplicas with the workload replicas.")
	}
	if r.RolloutRisks > 0 {
		x = append(x, "Add node headroom for the rollout surge, lower maxSurge, or allow maxUnavailable so the rolling update can complete.")
	}
	if r.APIRemoved > 0 {
		x = append(x, "Migrate manifests off removed API versions (see each finding for the replacement) before applying; the API server will reject them.")
	}
	if r.APIDeprecated > 0 {
		x = append(x, "Plan a migration off deprecated API versions before they are removed.")
	}
	if len(x) == 0 {
		x = append(x, "No blocking capacity or destructive-storage issue was detected. Continue with normal progressive delivery safeguards.")
	}
	return x
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func humanBytes(v int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(v)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func SummaryText(r Result) string {
	return strings.TrimSpace(fmt.Sprintf(`KAIRO · %s
Verdict: %s   Risk: %s   Blast radius: %d/100
Objects: %d analyzed · %d changed · %d created
Pods: %d restarted · %d created · %d unschedulable
Services affected: %d · Network-policy changes: %d · PVC risks: %d
Rollout risks: %d · API removed: %d · API deprecated: %d (target %s)
CPU delta: %.2f cores · Memory delta: %s · GPU delta: %d`, r.ClusterName, r.Verdict, r.RiskLevel, r.BlastRadius, r.ObjectsAnalyzed, r.ChangedObjects, r.CreatedObjects, r.PodsRestarted, r.PodsCreated, r.UnschedulablePods, r.ServicesAffected, r.NetworkPolicyChanges, r.PVCDestructiveRisks, r.RolloutRisks, r.APIRemoved, r.APIDeprecated, r.TargetVersion, r.CPUDeltaCores, humanBytes(abs64(r.MemoryDeltaBytes)), r.GPUDelta))
}
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// podTerminated reports whether a pod finished (Succeeded or Failed); such
// pods no longer hold their resource requests on the node.
func podTerminated(o map[string]any) bool {
	p := str(get(o, "status", "phase"))
	return p == "Succeeded" || p == "Failed"
}
