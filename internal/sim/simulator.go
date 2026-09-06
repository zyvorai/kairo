// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type nodeState struct {
	name string
	cap  resources
	free resources
}

func Simulate(req Request) (Result, error) {
	cluster, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return Result{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	desired, err := ParseDocuments(req.DesiredYAML)
	if err != nil {
		return Result{}, fmt.Errorf("parse desired manifests: %w", err)
	}
	res := Result{ClusterName: "kubernetes", ObjectsAnalyzed: len(cluster) + len(desired), Risks: []Risk{}, Recommendations: []string{}}
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

	nodes := buildNodes(cluster)
	accountExistingPods(nodes, cluster)
	baseCPU, baseMem, baseGPU := desiredResourceTotals(cluster, len(nodes))
	newCPU, newMem, newGPU := desiredResourceTotals(desired, len(nodes))
	res.CPUDeltaCores = round2(newCPU - baseCPU)
	res.MemoryDeltaBytes = newMem - baseMem
	res.GPUDelta = newGPU - baseGPU

	for _, obj := range desired {
		kind, ns, name := metadata(obj)
		old := live[keyFor(obj)]
		changed := old == nil || canonical(old) != canonical(obj)
		if !changed {
			continue
		}
		replicas := replicasFor(obj, len(nodes))
		spec := podSpecFor(obj)
		if spec != nil && replicas > 0 {
			if old != nil {
				res.PodsRestarted += replicas
			} else {
				res.PodsCreated += replicas
			}
			rq := resourceReq(spec)
			unscheduled := schedule(nodes, rq, replicas)
			if unscheduled > 0 {
				res.UnschedulablePods += unscheduled
				res.Risks = append(res.Risks, Risk{"high", "UNSCHEDULABLE", "Workload cannot fully schedule", fmt.Sprintf("%d of %d replicas cannot fit on the current node capacity.", unscheduled, replicas), ResourceRef{kind, ns, name}})
			}
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
		case "ResourceQuota":
			if quotaLikelyTight(obj, newCPU, newMem) {
				res.QuotaRisks++
				res.Risks = append(res.Risks, Risk{"high", "QUOTA_PRESSURE", "Resource quota may reject workloads", "Requested aggregate resources are close to or above the proposed namespace quota.", ResourceRef{kind, ns, name}})
			}
		}
	}

	res.ServicesAffected = estimateServiceImpact(cluster, desired, res.PodsRestarted, res.UnschedulablePods)
	score := 0
	score += minInt(45, res.UnschedulablePods*12)
	score += res.PVCDestructiveRisks * 40
	score += res.QuotaRisks * 25
	score += res.PDBRisks * 12
	score += minInt(15, res.NetworkPolicyChanges*8)
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
	if res.PVCDestructiveRisks > 0 || res.UnschedulablePods > 0 || res.QuotaRisks > 0 {
		res.Verdict = "BLOCK"
	} else if score >= 30 {
		res.Verdict = "REVIEW"
	} else {
		res.Verdict = "SAFE"
	}
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
		out = append(out, &nodeState{name, cap, cap})
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
		if node == nil {
			continue
		}
		r := resourceReq(asMap(o["spec"]))
		node.free.cpu -= r.cpu
		node.free.mem -= r.mem
		node.free.gpu -= r.gpu
	}
}
func schedule(nodes []*nodeState, r resources, replicas int) int {
	uns := 0
	for i := 0; i < replicas; i++ {
		best := -1
		for j, n := range nodes {
			if n.free.cpu+1e-9 >= r.cpu && n.free.mem >= r.mem && n.free.gpu >= r.gpu {
				if best < 0 || n.free.mem > nodes[best].free.mem {
					best = j
				}
			}
		}
		if best < 0 {
			uns++
			continue
		}
		nodes[best].free.cpu -= r.cpu
		nodes[best].free.mem -= r.mem
		nodes[best].free.gpu -= r.gpu
	}
	return uns
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
func quotaLikelyTight(obj map[string]any, cpu float64, mem int64) bool {
	hard := asMap(get(obj, "spec", "hard"))
	if hard == nil {
		return false
	}
	qc := parseCPU(hard["requests.cpu"])
	qm := parseBytes(hard["requests.memory"])
	return (qc > 0 && cpu > qc) || (qm > 0 && mem > qm)
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
		x = append(x, "Increase ResourceQuota or reduce aggregate requests before applying the change.")
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
CPU delta: %.2f cores · Memory delta: %s · GPU delta: %d`, r.ClusterName, r.Verdict, r.RiskLevel, r.BlastRadius, r.ObjectsAnalyzed, r.ChangedObjects, r.CreatedObjects, r.PodsRestarted, r.PodsCreated, r.UnschedulablePods, r.ServicesAffected, r.NetworkPolicyChanges, r.PVCDestructiveRisks, r.CPUDeltaCores, humanBytes(abs64(r.MemoryDeltaBytes)), r.GPUDelta))
}
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
