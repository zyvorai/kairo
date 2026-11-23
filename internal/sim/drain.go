// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"sort"
	"strings"
)

// DrainRequest asks what happens if the named nodes are cordoned and drained
// together, as in one wave of a node-pool upgrade.
type DrainRequest struct {
	ClusterYAML string   `json:"clusterYaml"`
	Nodes       []string `json:"nodes"`
}

// DrainPod is the fate of one pod on a drained node.
type DrainPod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Node      string `json:"node"`
	// Action is evict, ignore-daemonset, ignore-static or unmanaged.
	Action string `json:"action"`
	// Reschedulable is set for evicted pods only: a replacement fits on a node that stays.
	Reschedulable bool   `json:"reschedulable"`
	BlockedBy     string `json:"blockedBy,omitempty"`
}

type DrainResult struct {
	Nodes         []string   `json:"nodes"`
	Verdict       string     `json:"verdict"`
	Evicted       int        `json:"evicted"`
	Ignored       int        `json:"ignored"`
	Unmanaged     int        `json:"unmanaged"`
	Unschedulable int        `json:"unschedulable"`
	PDBBlocked    int        `json:"pdbBlocked"`
	Pods          []DrainPod `json:"pods"`
	Risks         []Risk     `json:"risks"`
}

// Drain simulates `kubectl drain` on one or more nodes. Pods are evicted
// through the Eviction API, so PodDisruptionBudgets are honoured: a PDB with
// no disruptions allowed blocks the drain. Evicted, controller-managed pods
// are rescheduled onto the nodes that remain (largest first, deterministic).
func Drain(req DrainRequest) (DrainResult, error) {
	objs, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return DrainResult{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	return drainObjs(objs, req.Nodes)
}

// drainObjs is Drain on already-parsed objects. It does not modify objs.
func drainObjs(objs []map[string]any, nodeNames []string) (DrainResult, error) {
	if len(nodeNames) == 0 {
		return DrainResult{}, fmt.Errorf("at least one node is required")
	}
	all := buildNodes(objs)
	known := map[string]bool{}
	for _, n := range all {
		known[n.name] = true
	}
	target := map[string]bool{}
	var names []string
	for _, n := range nodeNames {
		n = strings.TrimSpace(n)
		if n == "" || target[n] {
			continue
		}
		if !known[n] {
			return DrainResult{}, fmt.Errorf("node %q not found in the cluster snapshot", n)
		}
		target[n] = true
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return DrainResult{}, fmt.Errorf("at least one node is required")
	}
	var remaining []*nodeState
	for _, n := range all {
		if !target[n.name] {
			remaining = append(remaining, n)
		}
	}
	accountExistingPods(remaining, objs)

	res := DrainResult{Nodes: names, Pods: []DrainPod{}, Risks: []Risk{}}
	var pods []map[string]any
	for _, o := range objs {
		if str(o["kind"]) == "Pod" && !podTerminated(o) {
			pods = append(pods, o)
		}
	}
	pdbs := collectPDBs(objs)
	ownerTotal := map[string]int{}
	ownerOnDrain := map[string]int{}
	for _, p := range pods {
		if k := ownerKey(p); k != "" && ownerKind(p) != "DaemonSet" {
			ownerTotal[k]++
			if target[str(get(p, "spec", "nodeName"))] {
				ownerOnDrain[k]++
			}
		}
	}

	type evictee struct {
		idx int
		pod map[string]any
		rq  resources
	}
	var evictees []evictee
	pdbEvict := map[int]int{} // pdb index -> pods evicted
	pdbPods := map[int][]int{}
	for _, p := range pods {
		node := str(get(p, "spec", "nodeName"))
		if !target[node] {
			continue
		}
		kind, ns, name := metadata(p)
		_ = kind
		dp := DrainPod{Namespace: ns, Name: name, Node: node}
		ref := ResourceRef{"Pod", ns, name}
		switch {
		case str(get(p, "metadata", "annotations", "kubernetes.io/config.mirror")) != "":
			dp.Action = "ignore-static"
			res.Ignored++
		case ownerKind(p) == "DaemonSet":
			dp.Action = "ignore-daemonset"
			res.Ignored++
		case ownerKey(p) == "":
			dp.Action = "unmanaged"
			res.Unmanaged++
			res.Risks = append(res.Risks, Risk{"high", "DRAIN_UNMANAGED_POD", "Pod has no controller and will be lost",
				"kubectl drain refuses to evict a pod without a controller unless forced, and nothing recreates it.", ref})
		default:
			dp.Action = "evict"
			res.Evicted++
			if hasEmptyDir(p) {
				res.Risks = append(res.Risks, Risk{"low", "DRAIN_LOCAL_DATA", "Pod uses emptyDir data",
					"The pod's emptyDir contents are lost on eviction; kubectl drain needs --delete-emptydir-data.", ref})
			}
			evictees = append(evictees, evictee{len(res.Pods), p, resourceReq(asMap(p["spec"]))})
			for i, b := range pdbs {
				if b.matches(p) {
					pdbEvict[i]++
					pdbPods[i] = append(pdbPods[i], len(res.Pods))
				}
			}
		}
		res.Pods = append(res.Pods, dp)
	}

	// PDBs: an Eviction is refused while the budget allows no disruption.
	var pdbIdx []int
	for i := range pdbEvict {
		pdbIdx = append(pdbIdx, i)
	}
	sort.Ints(pdbIdx)
	for _, i := range pdbIdx {
		b := pdbs[i]
		allowed, healthy, expected := b.allowed(pods)
		if allowed >= 1 {
			if pdbEvict[i] > allowed {
				res.Risks = append(res.Risks, Risk{"medium", "DRAIN_PDB_SERIALIZED", "PDB forces a slow, serialized drain",
					fmt.Sprintf("PodDisruptionBudget %s allows %d disruption(s) but %d of its pods are on the drained nodes; evictions proceed only as replacements become ready.", b.name, allowed, pdbEvict[i]), ResourceRef{"PodDisruptionBudget", b.ns, b.name}})
			}
			continue
		}
		for _, di := range pdbPods[i] {
			res.Pods[di].BlockedBy = b.ns + "/" + b.name
		}
		res.PDBBlocked += pdbEvict[i]
		res.Risks = append(res.Risks, Risk{"high", "DRAIN_PDB_BLOCKED", "PDB blocks the drain",
			fmt.Sprintf("PodDisruptionBudget %s allows no disruptions (%d of %d expected pods healthy) but %d of its pods must be evicted; the drain will hang until the budget is relaxed or replicas are added.", b.name, healthy, expected, pdbEvict[i]), ResourceRef{"PodDisruptionBudget", b.ns, b.name}})
	}

	// Reschedule evicted pods: largest first, name as tie-break.
	sort.SliceStable(evictees, func(a, b int) bool {
		x, y := evictees[a], evictees[b]
		if x.rq.mem != y.rq.mem {
			return x.rq.mem > y.rq.mem
		}
		if x.rq.cpu != y.rq.cpu {
			return x.rq.cpu > y.rq.cpu
		}
		return res.Pods[x.idx].Namespace+"/"+res.Pods[x.idx].Name < res.Pods[y.idx].Namespace+"/"+res.Pods[y.idx].Name
	})
	for _, e := range evictees {
		pl := placementFor(asMap(e.pod["spec"]), false)
		if schedule(remaining, e.rq, 1, pl) == 0 {
			res.Pods[e.idx].Reschedulable = true
			continue
		}
		res.Unschedulable++
		dp := res.Pods[e.idx]
		res.Risks = append(res.Risks, Risk{"high", "DRAIN_UNSCHEDULABLE", "Evicted pod has nowhere to go",
			"No remaining node has the free capacity and matching selector/tolerations for this pod's replacement.", ResourceRef{"Pod", dp.Namespace, dp.Name}})
	}

	// Workloads whose every replica sits on the drained nodes go fully down.
	var owners []string
	for k, n := range ownerOnDrain {
		if n == ownerTotal[k] {
			owners = append(owners, k)
		}
	}
	sort.Strings(owners)
	for _, k := range owners {
		parts := strings.SplitN(k, "/", 3)
		res.Risks = append(res.Risks, Risk{"medium", "DRAIN_ALL_REPLICAS", "Every replica is on the drained nodes",
			fmt.Sprintf("All %d pod(s) owned by %s %s run on the drained nodes; the workload is down until replacements start.", ownerTotal[k], parts[1], parts[2]), ResourceRef{parts[1], parts[0], parts[2]}})
	}

	sortRisks(res.Risks)
	sort.SliceStable(res.Pods, func(i, j int) bool {
		a, b := res.Pods[i], res.Pods[j]
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	switch {
	case res.PDBBlocked > 0 || res.Unschedulable > 0 || res.Unmanaged > 0:
		res.Verdict = "BLOCK"
	case len(res.Risks) > 0:
		res.Verdict = "REVIEW"
	default:
		res.Verdict = "SAFE"
	}
	return res, nil
}

func hasEmptyDir(p map[string]any) bool {
	for _, v := range asSlice(get(p, "spec", "volumes")) {
		if asMap(v)["emptyDir"] != nil {
			return true
		}
	}
	return false
}

func ownerKind(p map[string]any) string {
	for _, o := range asSlice(get(p, "metadata", "ownerReferences")) {
		if k := str(asMap(o)["kind"]); k != "" {
			return k
		}
	}
	return ""
}

// ownerKey identifies a pod's controller as "namespace/Kind/name", or "" for
// a pod without one.
func ownerKey(p map[string]any) string {
	_, ns, _ := metadata(p)
	for _, o := range asSlice(get(p, "metadata", "ownerReferences")) {
		om := asMap(o)
		if k, n := str(om["kind"]), str(om["name"]); k != "" && n != "" {
			return ns + "/" + k + "/" + n
		}
	}
	return ""
}

type pdb struct {
	ns, name                     string
	selector                     map[string]any
	minAvailable, maxUnavailable any
}

func collectPDBs(objs []map[string]any) []pdb {
	var out []pdb
	for _, o := range objs {
		if str(o["kind"]) != "PodDisruptionBudget" {
			continue
		}
		_, ns, name := metadata(o)
		out = append(out, pdb{ns, name, asMap(get(o, "spec", "selector")), get(o, "spec", "minAvailable"), get(o, "spec", "maxUnavailable")})
	}
	return out
}

func (b pdb) matches(p map[string]any) bool {
	_, ns, _ := metadata(p)
	if ns != b.ns || b.selector == nil {
		return false // a nil selector selects nothing
	}
	return selectorMatches(b.selector, asMap(get(p, "metadata", "labels")))
}

// selectorMatches implements a LabelSelector: matchLabels plus
// matchExpressions (In, NotIn, Exists, DoesNotExist). Empty selects all.
func selectorMatches(sel, labels map[string]any) bool {
	for k, v := range asMap(sel["matchLabels"]) {
		if got, ok := labels[k]; !ok || str(got) != str(v) {
			return false
		}
	}
	for _, e := range asSlice(sel["matchExpressions"]) {
		em := asMap(e)
		key := str(em["key"])
		val, has := labels[key]
		in := false
		for _, v := range asSlice(em["values"]) {
			if has && str(v) == str(val) {
				in = true
			}
		}
		switch str(em["operator"]) {
		case "In":
			if !in {
				return false
			}
		case "NotIn":
			if in {
				return false
			}
		case "Exists":
			if !has {
				return false
			}
		case "DoesNotExist":
			if has {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// allowed returns the disruptions the budget currently permits, with the
// healthy and expected pod counts it derived them from. Pods count as healthy
// when Running (or of unknown phase) and not marked NotReady.
func (b pdb) allowed(pods []map[string]any) (allowed, healthy, expected int) {
	for _, p := range pods {
		if !b.matches(p) {
			continue
		}
		expected++
		if ph := str(get(p, "status", "phase")); (ph == "" || ph == "Running") && podReady(p) {
			healthy++
		}
	}
	switch {
	case b.maxUnavailable != nil:
		mu, ok := resolveIntOrPercent(b.maxUnavailable, expected, true)
		if !ok {
			return 0, healthy, expected
		}
		allowed = mu - (expected - healthy)
	case b.minAvailable != nil:
		ma, ok := resolveIntOrPercent(b.minAvailable, expected, true)
		if !ok {
			return 0, healthy, expected
		}
		allowed = healthy - ma
	default:
		return 0, healthy, expected
	}
	if allowed < 0 {
		allowed = 0
	}
	return
}

func podReady(p map[string]any) bool {
	for _, c := range asSlice(get(p, "status", "conditions")) {
		cm := asMap(c)
		if str(cm["type"]) == "Ready" {
			return str(cm["status"]) == "True"
		}
	}
	return true
}
