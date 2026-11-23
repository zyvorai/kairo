// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"sort"
	"strings"
)

// ConsolidateRequest asks which nodes of a snapshot could be emptied by
// repacking the existing pods onto fewer nodes.
type ConsolidateRequest struct {
	ClusterYAML string  `json:"clusterYaml"`
	Prices      *Prices `json:"prices,omitempty"`
}

// ConsolidateNode reports one node of the snapshot.
type ConsolidateNode struct {
	Name                 string  `json:"name"`
	Removable            bool    `json:"removable"`
	Reason               string  `json:"reason,omitempty"`
	MovablePods          int     `json:"movablePods"`
	CPUCapacity          float64 `json:"cpuCapacity"`
	CPURequested         float64 `json:"cpuRequested"`
	MemoryCapacity       int64   `json:"memoryCapacityBytes"`
	MemoryRequestedBytes int64   `json:"memoryRequestedBytes"`
}

// ConsolidateMove is one pod that has to be rescheduled by the plan.
type ConsolidateMove struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// Utilization is requested/allocatable, 0..1 (can exceed 1 on overcommitted nodes).
type Utilization struct {
	CPUBefore    float64 `json:"cpuBefore"`
	CPUAfter     float64 `json:"cpuAfter"`
	MemoryBefore float64 `json:"memoryBefore"`
	MemoryAfter  float64 `json:"memoryAfter"`
}

type ConsolidateResult struct {
	NodesBefore        int               `json:"nodesBefore"`
	NodesAfter         int               `json:"nodesAfter"`
	Removable          []string          `json:"removable"`
	CPUReclaimCores    float64           `json:"cpuReclaimCores"`
	MemoryReclaimBytes int64             `json:"memoryReclaimBytes"`
	GPUReclaim         int64             `json:"gpuReclaim"`
	Utilization        Utilization       `json:"utilization"`
	Nodes              []ConsolidateNode `json:"nodes"`
	Moves              []ConsolidateMove `json:"moves"`
	Cost               *CostEstimate     `json:"cost,omitempty"`
	Summary            string            `json:"summary"`
	NotModelled        []string          `json:"notModelled"`
}

// consolidationNotModelled is returned with every result so the plan is never
// read as a guarantee.
var consolidationNotModelled = []string{
	"Pod affinity/anti-affinity and node affinity are ignored (only nodeSelector, taints, cordons and requests are checked).",
	"Topology spread constraints and zone/rack balance are ignored.",
	"PersistentVolume topology and volume attach limits are ignored; pods with zonal volumes may not be movable.",
	"Only resource requests are used, not observed usage, so this is not request right-sizing; per-node max-pods, system reservations beyond allocatable, priority and preemption are ignored.",
	"The plan is a deterministic greedy heuristic (emptiest node first, best-fit decreasing); it is achievable but not guaranteed to be the minimum node count.",
	"PodDisruptionBudgets are checked as a one-shot budget against the snapshot, not as replacements become ready over time.",
	"Node pools, instance-type minimums, and autoscaler/Karpenter policies (consolidateAfter, budgets) are not modelled.",
}

type movablePod struct {
	ns, name string
	pod      map[string]any
	rq       resources
	pl       placement
	origin   string
	at       *nodeState
	pdbs     []int
}

// Consolidate bin-packs the existing controller-managed pods onto as few
// nodes as possible, using the same placement rules as Drain, and reports
// which nodes could be removed and the capacity they free.
func Consolidate(req ConsolidateRequest) (ConsolidateResult, error) {
	if err := req.Prices.validate(); err != nil {
		return ConsolidateResult{}, err
	}
	objs, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return ConsolidateResult{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	return consolidateObjs(objs, req.Prices)
}

// consolidateObjs is Consolidate on already-parsed objects. It does not
// modify objs, so callers can run what-if variants of the same snapshot.
func consolidateObjs(objs []map[string]any, prices *Prices) (ConsolidateResult, error) {
	nodes := buildNodes(objs)
	if len(nodes) == 0 {
		return ConsolidateResult{}, fmt.Errorf("the cluster snapshot has no Node objects")
	}
	accountExistingPods(nodes, objs)
	byName := map[string]*nodeState{}
	for _, n := range nodes {
		byName[n.name] = n
	}
	var pods []map[string]any
	for _, o := range objs {
		if str(o["kind"]) == "Pod" && !podTerminated(o) {
			pods = append(pods, o)
		}
	}
	pdbs := collectPDBs(objs)
	pdbAllowed := make([]int, len(pdbs))
	for i, b := range pdbs {
		pdbAllowed[i], _, _ = b.allowed(pods)
	}

	// Classify pods per node.
	hardBlock := map[string]string{} // node -> reason it can never be emptied
	for _, o := range objs {
		if str(o["kind"]) == "Node" && str(get(o, "metadata", "annotations", "karpenter.sh/do-not-disrupt")) == "true" {
			hardBlock[str(get(o, "metadata", "name"))] = "node is annotated karpenter.sh/do-not-disrupt=true"
		}
	}
	movable := map[*nodeState][]*movablePod{}
	var all []*movablePod
	for _, p := range pods {
		n := byName[str(get(p, "spec", "nodeName"))]
		if n == nil {
			continue
		}
		_, ns, name := metadata(p)
		switch {
		case str(get(p, "metadata", "annotations", "kubernetes.io/config.mirror")) != "", ownerKind(p) == "DaemonSet":
			continue // dies with the node and is recreated by the node, never moved
		case ownerKey(p) == "":
			setFirst(hardBlock, n.name, fmt.Sprintf("pod %s/%s has no controller and would be lost", ns, name))
		case noDisrupt(p):
			setFirst(hardBlock, n.name, fmt.Sprintf("pod %s/%s opts out of disruption (do-not-disrupt / safe-to-evict=false)", ns, name))
		default:
			m := &movablePod{ns: ns, name: name, pod: p, rq: resourceReq(asMap(p["spec"])), pl: placementFor(asMap(p["spec"]), false), origin: n.name, at: n}
			for i, b := range pdbs {
				if b.matches(p) {
					m.pdbs = append(m.pdbs, i)
				}
			}
			movable[n] = append(movable[n], m)
			all = append(all, m)
		}
	}

	// Candidates: emptiest first; name breaks ties.
	order := append([]*nodeState{}, nodes...)
	util := func(n *nodeState) float64 {
		u := 1.0
		if n.cap.cpu > 0 && n.cap.mem > 0 {
			u = (n.cap.cpu - n.free.cpu) / n.cap.cpu
			if m := float64(n.cap.mem-n.free.mem) / float64(n.cap.mem); m > u {
				u = m
			}
		}
		return u
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := util(order[i]), util(order[j])
		if a != b {
			return a < b
		}
		return order[i].name < order[j].name
	})

	removed := map[*nodeState]bool{}
	reason := map[string]string{}
	pdbUsed := make([]int, len(pdbs))
	for _, cand := range order {
		if why, bad := hardBlock[cand.name]; bad {
			reason[cand.name] = why
			continue
		}
		if len(nodes)-len(removed) <= 1 {
			reason[cand.name] = "last remaining node is kept"
			continue
		}
		list := append([]*movablePod{}, movable[cand]...)
		sort.SliceStable(list, func(i, j int) bool {
			x, y := list[i], list[j]
			if x.rq.mem != y.rq.mem {
				return x.rq.mem > y.rq.mem
			}
			if x.rq.cpu != y.rq.cpu {
				return x.rq.cpu > y.rq.cpu
			}
			return x.ns+"/"+x.name < y.ns+"/"+y.name
		})
		// PDBs: each eviction consumes budget; refuse when it runs out.
		need := map[int]int{}
		for _, m := range list {
			for _, i := range m.pdbs {
				need[i]++
			}
		}
		pdbWhy := ""
		var ids []int
		for i := range need {
			ids = append(ids, i)
		}
		sort.Ints(ids)
		for _, i := range ids {
			if pdbUsed[i]+need[i] > pdbAllowed[i] {
				pdbWhy = fmt.Sprintf("PodDisruptionBudget %s/%s allows %d disruption(s), %d needed", pdbs[i].ns, pdbs[i].name, pdbAllowed[i]-pdbUsed[i], need[i])
				break
			}
		}
		if pdbWhy != "" {
			reason[cand.name] = pdbWhy
			continue
		}
		// Trial placement on a copy; commit only if every pod fits.
		saved := map[*nodeState]resources{}
		for _, n := range nodes {
			saved[n] = n.free
		}
		placed := make([]*nodeState, len(list))
		fail := ""
		for k, m := range list {
			dst := bestFit(nodes, removed, cand, m.rq, m.pl)
			if dst == nil {
				fail = fmt.Sprintf("pod %s/%s (%.2f CPU, %s) has no other node it fits on", m.ns, m.name, m.rq.cpu, humanBytes(m.rq.mem))
				break
			}
			dst.take(m.rq)
			placed[k] = dst
		}
		if fail != "" {
			for n, f := range saved {
				n.free = f
			}
			reason[cand.name] = fail
			continue
		}
		for k, m := range list {
			cand.free.cpu += m.rq.cpu
			cand.free.mem += m.rq.mem
			cand.free.gpu += m.rq.gpu
			movable[placed[k]] = append(movable[placed[k]], m)
			m.at = placed[k]
		}
		delete(movable, cand)
		for i, c := range need {
			pdbUsed[i] += c
		}
		removed[cand] = true
	}

	res := ConsolidateResult{NodesBefore: len(nodes), NodesAfter: len(nodes) - len(removed), Removable: []string{}, Nodes: []ConsolidateNode{}, Moves: []ConsolidateMove{}, NotModelled: consolidationNotModelled}
	var capCPUAll, usedCPUAll, capCPUKept, usedCPUKept float64
	var capMemAll, usedMemAll, capMemKept, usedMemKept int64
	for _, n := range nodes {
		cpuUsed, memUsed := n.cap.cpu-n.free.cpu, n.cap.mem-n.free.mem
		if removed[n] {
			res.Removable = append(res.Removable, n.name)
			res.CPUReclaimCores += n.cap.cpu
			res.MemoryReclaimBytes += n.cap.mem
			res.GPUReclaim += n.cap.gpu
		} else {
			capCPUKept += n.cap.cpu
			usedCPUKept += cpuUsed
			capMemKept += n.cap.mem
			usedMemKept += memUsed
		}
		cn := ConsolidateNode{Name: n.name, Removable: removed[n], Reason: reason[n.name], MovablePods: 0, CPUCapacity: round2(n.cap.cpu), MemoryCapacity: n.cap.mem}
		res.Nodes = append(res.Nodes, cn)
	}
	// "Before" figures come from the snapshot placement; recompute them cleanly.
	before := buildNodes(objs)
	accountExistingPods(before, objs)
	idx := map[string]int{}
	for i, n := range res.Nodes {
		idx[n.Name] = i
	}
	for _, n := range before {
		capCPUAll += n.cap.cpu
		usedCPUAll += n.cap.cpu - n.free.cpu
		capMemAll += n.cap.mem
		usedMemAll += n.cap.mem - n.free.mem
		i := idx[n.name]
		res.Nodes[i].CPURequested = round2(n.cap.cpu - n.free.cpu)
		res.Nodes[i].MemoryRequestedBytes = n.cap.mem - n.free.mem
	}
	for _, m := range all {
		res.Nodes[idx[m.origin]].MovablePods++
		if m.at.name != m.origin {
			res.Moves = append(res.Moves, ConsolidateMove{m.ns, m.name, m.origin, m.at.name})
		}
	}
	sort.Strings(res.Removable)
	sort.Slice(res.Nodes, func(i, j int) bool { return res.Nodes[i].Name < res.Nodes[j].Name })
	sort.Slice(res.Moves, func(i, j int) bool {
		a, b := res.Moves[i], res.Moves[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	res.CPUReclaimCores = round2(res.CPUReclaimCores)
	res.Utilization = Utilization{
		CPUBefore: ratio(usedCPUAll, capCPUAll), CPUAfter: ratio(usedCPUKept, capCPUKept),
		MemoryBefore: ratio(float64(usedMemAll), float64(capMemAll)), MemoryAfter: ratio(float64(usedMemKept), float64(capMemKept)),
	}
	if prices.active() {
		res.Cost = newCost(prices, -float64(len(removed)), -float64(res.MemoryReclaimBytes)/(1<<30),
			fmt.Sprintf("%d removable node(s) and %s of reclaimed memory capacity", len(removed), humanBytes(res.MemoryReclaimBytes)))
	}
	switch len(removed) {
	case 0:
		res.Summary = fmt.Sprintf("No node can be emptied: %d node(s) stay.", res.NodesAfter)
	default:
		res.Summary = fmt.Sprintf("%d of %d node(s) could be emptied (%s), freeing %.2f CPU cores and %s of allocatable memory.", len(removed), res.NodesBefore, strings.Join(res.Removable, ", "), res.CPUReclaimCores, humanBytes(res.MemoryReclaimBytes))
	}
	return res, nil
}

func setFirst(m map[string]string, k, v string) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

func ratio(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return round2(a/b*100) / 100
}

func noDisrupt(p map[string]any) bool {
	return str(get(p, "metadata", "annotations", "karpenter.sh/do-not-disrupt")) == "true" ||
		str(get(p, "metadata", "annotations", "cluster-autoscaler.kubernetes.io/safe-to-evict")) == "false"
}

// bestFit picks the eligible node with the least free memory that still
// fits the pod, so pods pack tightly. Removed nodes and the candidate are skipped.
func bestFit(nodes []*nodeState, removed map[*nodeState]bool, skip *nodeState, r resources, p placement) *nodeState {
	var best *nodeState
	for _, n := range nodes {
		if n == skip || removed[n] || !n.eligible(p) || !n.fits(r) {
			continue
		}
		if best == nil || n.free.mem < best.free.mem || (n.free.mem == best.free.mem && (n.free.cpu < best.free.cpu || (n.free.cpu == best.free.cpu && n.name < best.name))) {
			best = n
		}
	}
	return best
}

// ConsolidateText renders a consolidation plan for the terminal.
func ConsolidateText(r ConsolidateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "KAIRO consolidate · %d -> %d node(s)\n%s\n", r.NodesBefore, r.NodesAfter, r.Summary)
	fmt.Fprintf(&b, "Requested utilisation: CPU %.0f%% -> %.0f%% · memory %.0f%% -> %.0f%%\n", r.Utilization.CPUBefore*100, r.Utilization.CPUAfter*100, r.Utilization.MemoryBefore*100, r.Utilization.MemoryAfter*100)
	b.WriteString("\nNodes:\n")
	for _, n := range r.Nodes {
		state := "keep"
		if n.Removable {
			state = "REMOVE"
		}
		note := ""
		if n.Reason != "" {
			note = " (" + n.Reason + ")"
		}
		fmt.Fprintf(&b, "  %s: %s%s\n", n.Name, state, note)
	}
	if len(r.Moves) > 0 {
		b.WriteString("\nPods to reschedule:\n")
		for _, m := range r.Moves {
			fmt.Fprintf(&b, "  %s/%s: %s -> %s\n", m.Namespace, m.Name, m.From, m.To)
		}
	}
	if r.Cost != nil {
		b.WriteString("\n" + CostText(r.Cost) + "\n")
	}
	b.WriteString("\nNot modelled:\n")
	for _, x := range r.NotModelled {
		fmt.Fprintf(&b, "  - %s\n", x)
	}
	return b.String()
}
