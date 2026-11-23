// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// DisruptionRequest asks how disruptive it would be to remove each node of a
// snapshot, so the cheapest nodes to consolidate or drain come first.
type DisruptionRequest struct {
	ClusterYAML string `json:"clusterYaml"`
}

// DisruptionFactor is one explained component of a node's score. Points are
// MaxPoints scaled by how far the observed count is towards the saturation
// point of the factor; both are published in DisruptionResult.Weights.
type DisruptionFactor struct {
	Code      string   `json:"code"`
	Points    float64  `json:"points"`
	MaxPoints float64  `json:"maxPoints"`
	Detail    string   `json:"detail"`
	Pods      []string `json:"pods,omitempty"`
}

// DisruptionNode is the disruption assessment of removing one node.
type DisruptionNode struct {
	Name string `json:"name"`
	// Score is 0 (nothing notable) to 100 (every factor saturated).
	Score int `json:"score"`
	// Level is LOW, MEDIUM, HIGH, or BLOCKED when a hard blocker exists.
	Level    string   `json:"level"`
	Blockers []string `json:"blockers"`
	// Removable is true when `consolidate` can empty this node on its own.
	Removable bool `json:"removable"`
	// Pods counts the controller-managed, non-DaemonSet pods that would move.
	Pods    int                `json:"pods"`
	Factors []DisruptionFactor `json:"factors"`
	// Unknowns lists inputs the snapshot does not carry; they are not scored.
	Unknowns []string `json:"unknowns"`
}

// DisruptionWeight documents one factor of the scoring model.
type DisruptionWeight struct {
	Code        string  `json:"code"`
	MaxPoints   float64 `json:"maxPoints"`
	SaturatesAt float64 `json:"saturatesAt"`
	Unit        string  `json:"unit"`
	Description string  `json:"description"`
}

type DisruptionResult struct {
	// Nodes are ordered least disruptive first (score, then name).
	Nodes       []DisruptionNode   `json:"nodes"`
	Weights     []DisruptionWeight `json:"weights"`
	Warnings    []string           `json:"warnings"`
	Summary     string             `json:"summary"`
	NotModelled []string           `json:"notModelled"`
}

// The model is a transparent weighted sum, not a prediction. The weights are
// judgement calls and are returned with every result so they can be argued with.
var disruptionWeights = []DisruptionWeight{
	{"PDB_PRESSURE", 25, 1, "worst budget (1 = a budget allows no disruption, 0.5 = allows fewer than the node needs)", "A PodDisruptionBudget limits how fast pods on this node can be evicted."},
	{"SINGLE_REPLICA", 20, 3, "workloads", "Workloads with exactly one running pod go down while the replacement starts."},
	{"ALL_REPLICAS_HERE", 15, 2, "workloads", "Multi-replica workloads whose every pod is on this node lose all capacity at once."},
	{"LOCAL_STATE", 15, 3, "pods", "Pods with emptyDir disk, hostPath or PersistentVolumeClaim volumes lose data or must reattach storage."},
	{"PLACEMENT_CONSTRAINTS", 10, 3, "pods", "Pods with topologySpreadConstraints or required pod anti-affinity may not be re-placeable; Kairo does not model them."},
	{"SLOW_START", 15, 120, "seconds", "Slowest declared startup/readiness probe budget among the pods; longer means a longer gap in capacity."},
}

var disruptionNotModelled = []string{
	"The score is a weighted heuristic over the snapshot, not a measurement; the weights and saturation points are listed in `weights`.",
	"Startup time comes from declared probes (startupProbe: initialDelaySeconds + failureThreshold x periodSeconds; else readinessProbe: initialDelaySeconds + periodSeconds). Pods without probes have unknown startup time and are reported in `unknowns`, not scored.",
	"Observed traffic, request rate and per-pod importance are not known to the snapshot.",
	"Topology spread and anti-affinity are only detected, not evaluated; PodDisruptionBudgets are read from the snapshot as a one-shot budget.",
	"Pod priority, terminationGracePeriodSeconds beyond the figure quoted in details, and Karpenter/Cluster Autoscaler timing policies are not scored.",
}

// DisruptionRisk scores every node by how disruptive removing it would be.
func DisruptionRisk(req DisruptionRequest) (DisruptionResult, error) {
	objs, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return DisruptionResult{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	nodes := buildNodes(objs)
	if len(nodes) == 0 {
		return DisruptionResult{}, fmt.Errorf("the cluster snapshot has no Node objects")
	}
	known := map[string]bool{}
	for _, n := range nodes {
		known[n.name] = true
	}
	nodeNoDisrupt := map[string]bool{}
	for _, o := range objs {
		if str(o["kind"]) == "Node" && str(get(o, "metadata", "annotations", "karpenter.sh/do-not-disrupt")) == "true" {
			nodeNoDisrupt[str(get(o, "metadata", "name"))] = true
		}
	}
	removable := map[string]bool{}
	if c, err := consolidateObjs(objs, nil); err == nil {
		for _, n := range c.Removable {
			removable[n] = true
		}
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
	ownerTotal := map[string]int{}
	for _, p := range pods {
		if k := ownerKey(p); k != "" && ownerKind(p) != "DaemonSet" {
			ownerTotal[k]++
		}
	}

	res := DisruptionResult{Nodes: []DisruptionNode{}, Weights: disruptionWeights, Warnings: []string{}, NotModelled: disruptionNotModelled}
	perNode := map[string][]map[string]any{}
	orphans := 0
	for _, p := range pods {
		nn := str(get(p, "spec", "nodeName"))
		if !known[nn] {
			if nn != "" {
				orphans++
			}
			continue
		}
		perNode[nn] = append(perNode[nn], p)
	}
	if orphans > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d pod(s) are bound to nodes that are not in the snapshot and were ignored.", orphans))
	}

	for _, n := range nodes {
		dn := DisruptionNode{Name: n.name, Blockers: []string{}, Factors: []DisruptionFactor{}, Unknowns: []string{}, Removable: removable[n.name]}
		if nodeNoDisrupt[n.name] {
			dn.Blockers = append(dn.Blockers, "node is annotated karpenter.sh/do-not-disrupt=true")
		}
		var movable []map[string]any
		for _, p := range perNode[n.name] {
			_, ns, name := metadata(p)
			switch {
			case str(get(p, "metadata", "annotations", "kubernetes.io/config.mirror")) != "", ownerKind(p) == "DaemonSet":
				continue
			case ownerKey(p) == "":
				dn.Blockers = append(dn.Blockers, fmt.Sprintf("pod %s/%s has no controller and would be lost", ns, name))
				continue
			case noDisrupt(p):
				dn.Blockers = append(dn.Blockers, fmt.Sprintf("pod %s/%s opts out of disruption", ns, name))
				continue
			}
			if v := str(get(p, "metadata", "annotations", "karpenter.sh/do-not-disrupt")); v != "" && v != "true" && v != "false" {
				dn.Unknowns = append(dn.Unknowns, fmt.Sprintf("pod %s/%s has do-not-disrupt=%q (a time-bound protection); treated as not blocking", ns, name, v))
			}
			movable = append(movable, p)
		}
		dn.Pods = len(movable)
		sort.Strings(dn.Blockers)

		// PDB pressure.
		need := map[int]int{}
		for _, p := range movable {
			for i, b := range pdbs {
				if b.matches(p) {
					need[i]++
				}
			}
		}
		var ids []int
		for i := range need {
			ids = append(ids, i)
		}
		sort.Ints(ids)
		sat, pdbDetail := 0.0, fmt.Sprintf("no PodDisruptionBudget covers the %d movable pod(s)", len(movable))
		if len(ids) > 0 {
			pdbDetail = "every covering budget allows the evictions this node needs"
		}
		for _, i := range ids {
			b := pdbs[i]
			s := 0.0
			switch {
			case pdbAllowed[i] == 0:
				s = 1
			case need[i] > pdbAllowed[i]:
				s = 0.5
			}
			if s > sat {
				sat = s
				pdbDetail = fmt.Sprintf("PodDisruptionBudget %s/%s allows %d disruption(s), the node needs %d", b.ns, b.name, pdbAllowed[i], need[i])
			}
		}
		dn.Factors = append(dn.Factors, factor("PDB_PRESSURE", sat, pdbDetail, nil))

		// Single-replica and all-replicas-here workloads.
		onNode := map[string]int{}
		for _, p := range movable {
			onNode[ownerKey(p)]++
		}
		var singles, wholes []string
		for k, c := range onNode {
			short := k
			if ownerTotal[k] == 1 {
				singles = append(singles, short)
			} else if c == ownerTotal[k] {
				wholes = append(wholes, short)
			}
		}
		sort.Strings(singles)
		sort.Strings(wholes)
		dn.Factors = append(dn.Factors, factor("SINGLE_REPLICA", float64(len(singles)), fmt.Sprintf("%d workload(s) have a single running pod, on this node", len(singles)), singles))
		dn.Factors = append(dn.Factors, factor("ALL_REPLICAS_HERE", float64(len(wholes)), fmt.Sprintf("%d multi-replica workload(s) have every pod on this node", len(wholes)), wholes))

		// Local state and placement constraints.
		var state, constrained []string
		for _, p := range movable {
			_, ns, name := metadata(p)
			if hasLocalState(p) {
				state = append(state, ns+"/"+name)
			}
			if hasPlacementConstraint(p) {
				constrained = append(constrained, ns+"/"+name)
			}
		}
		sort.Strings(state)
		sort.Strings(constrained)
		dn.Factors = append(dn.Factors, factor("LOCAL_STATE", float64(len(state)), fmt.Sprintf("%d pod(s) use emptyDir disk, hostPath or PersistentVolumeClaim volumes", len(state)), state))
		dn.Factors = append(dn.Factors, factor("PLACEMENT_CONSTRAINTS", float64(len(constrained)), fmt.Sprintf("%d pod(s) declare topologySpreadConstraints or required pod anti-affinity", len(constrained)), constrained))

		// Startup time from declared probes only.
		slowest, slowPod, unknown, grace := 0, "", 0, 0
		for _, p := range movable {
			if s, ok := probeStartupSeconds(p); ok {
				if s > slowest {
					slowest = s
					_, ns, name := metadata(p)
					slowPod = ns + "/" + name
				}
			} else {
				unknown++
			}
			if g := intValue(get(p, "spec", "terminationGracePeriodSeconds"), 30); g > grace && g <= 86400 {
				grace = g
			}
		}
		startDetail := "no pod declares a startup or readiness probe; startup time unknown"
		if slowest > 0 || unknown < len(movable) {
			startDetail = fmt.Sprintf("slowest declared probe budget is %ds (%s); longest terminationGracePeriodSeconds is %ds", slowest, slowPod, grace)
		}
		var slowPods []string
		if slowPod != "" {
			slowPods = []string{slowPod}
		}
		dn.Factors = append(dn.Factors, factor("SLOW_START", float64(slowest), startDetail, slowPods))
		if unknown > 0 {
			dn.Unknowns = append(dn.Unknowns, fmt.Sprintf("%d of %d movable pod(s) declare no startup/readiness probe; their startup time is unknown and not scored", unknown, len(movable)))
		}

		total := 0.0
		for _, f := range dn.Factors {
			total += f.Points
		}
		dn.Score = int(math.Round(total))
		switch {
		case len(dn.Blockers) > 0:
			dn.Level = "BLOCKED"
		case dn.Score >= 50:
			dn.Level = "HIGH"
		case dn.Score >= 25:
			dn.Level = "MEDIUM"
		default:
			dn.Level = "LOW"
		}
		res.Nodes = append(res.Nodes, dn)
	}
	sort.SliceStable(res.Nodes, func(i, j int) bool {
		a, b := res.Nodes[i], res.Nodes[j]
		if (a.Level == "BLOCKED") != (b.Level == "BLOCKED") {
			return b.Level == "BLOCKED"
		}
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		return a.Name < b.Name
	})
	low := 0
	for _, n := range res.Nodes {
		if n.Level == "LOW" {
			low++
		}
	}
	res.Summary = fmt.Sprintf("%d node(s) scored; %d LOW. Least disruptive first: %s (score %d, %s).", len(res.Nodes), low, res.Nodes[0].Name, res.Nodes[0].Score, res.Nodes[0].Level)
	return res, nil
}

// factor scales a raw observation to points using the published weights.
func factor(code string, raw float64, detail string, pods []string) DisruptionFactor {
	for _, w := range disruptionWeights {
		if w.Code == code {
			pts := w.MaxPoints * math.Min(1, math.Max(0, raw)/w.SaturatesAt)
			f := DisruptionFactor{Code: code, Points: round2(pts), MaxPoints: w.MaxPoints, Detail: detail}
			if len(pods) > 5 {
				f.Pods = append(append([]string{}, pods[:5]...), fmt.Sprintf("and %d more", len(pods)-5))
			} else {
				f.Pods = pods
			}
			return f
		}
	}
	return DisruptionFactor{Code: code, Detail: detail}
}

func hasLocalState(p map[string]any) bool {
	for _, v := range asSlice(get(p, "spec", "volumes")) {
		vm := asMap(v)
		if ed := asMap(vm["emptyDir"]); vm["emptyDir"] != nil && str(ed["medium"]) != "Memory" {
			return true
		}
		if vm["hostPath"] != nil || vm["persistentVolumeClaim"] != nil || vm["ephemeral"] != nil {
			return true
		}
	}
	return false
}

func hasPlacementConstraint(p map[string]any) bool {
	if len(asSlice(get(p, "spec", "topologySpreadConstraints"))) > 0 {
		return true
	}
	return len(asSlice(get(p, "spec", "affinity", "podAntiAffinity", "requiredDuringSchedulingIgnoredDuringExecution"))) > 0
}

// probeStartupSeconds returns the longest declared startup budget over the
// pod's containers, and false when no container declares a probe.
func probeStartupSeconds(p map[string]any) (int, bool) {
	best, found := 0, false
	clamp := func(v any, def int) int {
		n := intValue(v, def)
		if n < 0 || n > 86400 {
			return def
		}
		return n
	}
	for _, c := range asSlice(get(p, "spec", "containers")) {
		cm := asMap(c)
		var s int
		if sp := asMap(cm["startupProbe"]); sp != nil {
			s = clamp(sp["initialDelaySeconds"], 0) + clamp(sp["failureThreshold"], 3)*clamp(sp["periodSeconds"], 10)
		} else if rp := asMap(cm["readinessProbe"]); rp != nil {
			s = clamp(rp["initialDelaySeconds"], 0) + clamp(rp["periodSeconds"], 10)
		} else {
			continue
		}
		found = true
		if s > best {
			best = s
		}
	}
	return best, found
}

// DisruptionText renders the assessment for the terminal.
func DisruptionText(r DisruptionResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "KAIRO disruption risk · %d node(s)\n%s\n", len(r.Nodes), r.Summary)
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	for _, n := range r.Nodes {
		rem := ""
		if n.Removable {
			rem = " · consolidation candidate"
		}
		fmt.Fprintf(&b, "\n%s: %s, score %d/100, %d movable pod(s)%s\n", n.Name, n.Level, n.Score, n.Pods, rem)
		for _, x := range n.Blockers {
			fmt.Fprintf(&b, "  BLOCKER: %s\n", x)
		}
		for _, f := range n.Factors {
			if f.Points > 0 {
				fmt.Fprintf(&b, "  +%.1f/%.0f %s: %s\n", f.Points, f.MaxPoints, f.Code, f.Detail)
			}
		}
		for _, u := range n.Unknowns {
			fmt.Fprintf(&b, "  unknown: %s\n", u)
		}
	}
	b.WriteString("\nScoring model (points = max x min(1, observed/saturation)):\n")
	for _, w := range r.Weights {
		fmt.Fprintf(&b, "  %s: max %.0f, saturates at %g %s\n", w.Code, w.MaxPoints, w.SaturatesAt, w.Unit)
	}
	b.WriteString("\nNot modelled:\n")
	for _, x := range r.NotModelled {
		fmt.Fprintf(&b, "  - %s\n", x)
	}
	return b.String()
}
