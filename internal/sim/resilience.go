// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"sort"
	"strings"
)

// ResilienceRequest asks what happens when a whole capacity pool (spot
// instances of one type in one zone) or a whole zone disappears at once.
type ResilienceRequest struct {
	ClusterYAML string `json:"clusterYaml"`
}

// Labels Kairo reads from Node objects. Nothing is inferred from instance
// names or prices; a node without the label is reported as unknown.
const (
	labelInstanceType = "node.kubernetes.io/instance-type"
	labelInstanceBeta = "beta.kubernetes.io/instance-type"
	labelZone         = "topology.kubernetes.io/zone"
	labelCapacity     = "karpenter.sh/capacity-type"
	labelEKSCapacity  = "eks.amazonaws.com/capacityType"
	labelGKESpot      = "cloud.google.com/gke-spot"
	labelAKSPriority  = "kubernetes.azure.com/scalesetpriority"
)

const (
	// concentrationShare is the share of a capacity type's CPU in one pool above
	// which the report warns. It is a judgement call, published in the result.
	concentrationShare = 0.5
	// fewTypes is the number of distinct instance types among spot nodes below
	// which the report warns.
	fewTypes       = 3
	maxResilienceN = 500
)

// ResilienceScenario is the loss of one pool or zone.
type ResilienceScenario struct {
	// Kind is "pool" (one capacity type + instance type + zone) or "zone".
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	CapacityType string   `json:"capacityType"`
	InstanceType string   `json:"instanceType"`
	Zone         string   `json:"zone"`
	Nodes        []string `json:"nodes"`
	// CPUShare and NodeShare are the fractions of the cluster's allocatable CPU and nodes.
	CPUShare      float64 `json:"cpuShare"`
	NodeShare     float64 `json:"nodeShare"`
	Verdict       string  `json:"verdict"`
	Evicted       int     `json:"evicted"`
	Unschedulable int     `json:"unschedulable"`
	Unmanaged     int     `json:"unmanaged"`
	Risks         []Risk  `json:"risks"`
}

// CapacityDiversity summarises one capacity type.
type CapacityDiversity struct {
	CapacityType      string  `json:"capacityType"`
	Nodes             int     `json:"nodes"`
	InstanceTypes     int     `json:"instanceTypes"`
	Zones             int     `json:"zones"`
	LargestPool       string  `json:"largestPool"`
	LargestPoolShare  float64 `json:"largestPoolShare"`
	CPUShareOfCluster float64 `json:"cpuShareOfCluster"`
}

type ResilienceResult struct {
	Diversity []CapacityDiversity  `json:"diversity"`
	Scenarios []ResilienceScenario `json:"scenarios"`
	Findings  []Risk               `json:"findings"`
	// Unknowns lists nodes missing labels; they are grouped as "unknown", never guessed.
	Unknowns    []string `json:"unknowns"`
	Thresholds  []string `json:"thresholds"`
	Summary     string   `json:"summary"`
	NotModelled []string `json:"notModelled"`
}

var resilienceNotModelled = []string{
	"A lost pool is modelled as every node in it vanishing at once and their controller-managed pods being rescheduled onto the surviving nodes (requests, nodeSelector, taints, cordons). PodDisruptionBudgets are ignored on purpose: a cloud reclaim does not honour them.",
	"Replacement capacity is not modelled: the autoscaler may launch new nodes, but not instantly, and in a stressed spot market not at all. Unschedulable counts are the gap until it does.",
	"Spot prices, interruption frequency and real pool availability are not known to a snapshot; the report is about concentration, not about probability. Pools are keyed by the node labels shown, so two instance types that share a capacity pool in the provider are counted as different here.",
	"Interruption warning time (for example the two minutes before a spot reclaim) and graceful shutdown are not modelled; affinity, topology spread and volume topology are ignored as in consolidate.",
}

func nodeCapacityType(labels map[string]string) string {
	switch {
	case labels[labelCapacity] != "":
		return strings.ToLower(labels[labelCapacity])
	case labels[labelEKSCapacity] != "":
		return strings.ToLower(strings.ReplaceAll(labels[labelEKSCapacity], "_", "-"))
	case labels[labelGKESpot] == "true", labels[labelAKSPriority] == "spot":
		return "spot"
	}
	return "unknown"
}

func nodeInstanceType(labels map[string]string) string {
	if v := labels[labelInstanceType]; v != "" {
		return v
	}
	if v := labels[labelInstanceBeta]; v != "" {
		return v
	}
	return "unknown"
}

func nodeZone(labels map[string]string) string {
	if v := labels[labelZone]; v != "" {
		return v
	}
	return "unknown"
}

// Resilience simulates the loss of each spot (or unknown-capacity) pool and
// each zone, and reports how concentrated the cluster's capacity is.
func Resilience(req ResilienceRequest) (ResilienceResult, error) {
	objs, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return ResilienceResult{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	nodes := buildNodes(objs)
	if len(nodes) == 0 {
		return ResilienceResult{}, fmt.Errorf("the cluster snapshot has no Node objects")
	}
	// PodDisruptionBudgets do not apply to involuntary loss.
	var noPDB []map[string]any
	for _, o := range objs {
		if str(o["kind"]) != "PodDisruptionBudget" {
			noPDB = append(noPDB, o)
		}
	}
	var totalCPU float64
	for _, n := range nodes {
		totalCPU += n.cap.cpu
	}

	res := ResilienceResult{Diversity: []CapacityDiversity{}, Scenarios: []ResilienceScenario{}, Findings: []Risk{}, Unknowns: []string{},
		Thresholds: []string{
			fmt.Sprintf("RESILIENCE_POOL_CONCENTRATION: one pool holds more than %.0f%% of a capacity type's CPU (judgement call)", concentrationShare*100),
			fmt.Sprintf("RESILIENCE_FEW_INSTANCE_TYPES: fewer than %d distinct instance types among spot nodes (judgement call)", fewTypes),
		},
		NotModelled: resilienceNotModelled}

	type pool struct {
		capacity, itype, zone string
		nodes                 []string
		cpu                   float64
	}
	pools := map[string]*pool{}
	zones := map[string]*pool{}
	var missing []string
	for _, n := range nodes {
		ct, it, z := nodeCapacityType(n.labels), nodeInstanceType(n.labels), nodeZone(n.labels)
		var lack []string
		if ct == "unknown" {
			lack = append(lack, "capacity type")
		}
		if it == "unknown" {
			lack = append(lack, "instance type")
		}
		if z == "unknown" {
			lack = append(lack, "zone")
		}
		if len(lack) > 0 {
			missing = append(missing, fmt.Sprintf("node %s has no %s label", n.name, strings.Join(lack, ", ")))
		}
		k := ct + "|" + it + "|" + z
		if pools[k] == nil {
			pools[k] = &pool{capacity: ct, itype: it, zone: z}
		}
		pools[k].nodes = append(pools[k].nodes, n.name)
		pools[k].cpu += n.cap.cpu
		if zones[z] == nil {
			zones[z] = &pool{capacity: "any", itype: "any", zone: z}
		}
		zones[z].nodes = append(zones[z].nodes, n.name)
		zones[z].cpu += n.cap.cpu
	}
	sort.Strings(missing)
	if len(missing) > 20 {
		missing = append(missing[:20], fmt.Sprintf("and %d more node(s) with missing labels", len(missing)-20))
	}
	res.Unknowns = append(res.Unknowns, missing...)

	// Diversity per capacity type.
	type agg struct {
		nodes, npools int
		cpu           float64
		types, zns    map[string]bool
		largest       *pool
	}
	byCap := map[string]*agg{}
	for _, p := range pools {
		a := byCap[p.capacity]
		if a == nil {
			a = &agg{types: map[string]bool{}, zns: map[string]bool{}}
			byCap[p.capacity] = a
		}
		a.nodes += len(p.nodes)
		a.npools++
		a.cpu += p.cpu
		a.types[p.itype] = true
		a.zns[p.zone] = true
		if a.largest == nil || p.cpu > a.largest.cpu || (p.cpu == a.largest.cpu && p.itype+p.zone < a.largest.itype+a.largest.zone) {
			a.largest = p
		}
	}
	var caps []string
	for c := range byCap {
		caps = append(caps, c)
	}
	sort.Strings(caps)
	for _, c := range caps {
		a := byCap[c]
		share := 0.0
		if a.cpu > 0 {
			share = a.largest.cpu / a.cpu
		}
		d := CapacityDiversity{CapacityType: c, Nodes: a.nodes, InstanceTypes: len(a.types), Zones: len(a.zns), LargestPool: a.largest.itype + " in " + a.largest.zone, LargestPoolShare: round2(share*100) / 100, CPUShareOfCluster: ratio(a.cpu, totalCPU)}
		res.Diversity = append(res.Diversity, d)
		ref := ResourceRef{"CapacityType", "", c}
		if c == "spot" && len(a.types) < fewTypes {
			res.Findings = append(res.Findings, Risk{"medium", "RESILIENCE_FEW_INSTANCE_TYPES", "Spot capacity uses few instance types",
				fmt.Sprintf("%d spot node(s) span %d distinct instance type(s) and %d zone(s); a shortage of one type has few alternatives in the snapshot.", a.nodes, len(a.types), len(a.zns)), ref})
		}
		if (c == "spot" || c == "unknown") && len(pools) > 1 && a.nodes > 1 && share > concentrationShare {
			res.Findings = append(res.Findings, Risk{"medium", "RESILIENCE_POOL_CONCENTRATION", "One pool holds most of the capacity",
				fmt.Sprintf("%s holds %.0f%% of the %s CPU (%d node(s) of that capacity type across %d pool(s)).", d.LargestPool, share*100, c, a.nodes, a.npools), ref})
		}
	}

	// Scenarios: each spot/unknown pool and each zone.
	var all []*pool
	var kinds []string
	var keys []string
	for k := range pools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p := pools[k]; p.capacity != "on-demand" && p.capacity != "reserved" {
			all = append(all, p)
			kinds = append(kinds, "pool")
		}
	}
	var zkeys []string
	for k := range zones {
		zkeys = append(zkeys, k)
	}
	sort.Strings(zkeys)
	for _, k := range zkeys {
		all = append(all, zones[k])
		kinds = append(kinds, "zone")
	}
	skipped := 0
	if len(all) > maxResilienceN {
		skipped = len(all) - maxResilienceN
		all, kinds = all[:maxResilienceN], kinds[:maxResilienceN]
	}
	for i, p := range all {
		names := append([]string{}, p.nodes...)
		sort.Strings(names)
		d, err := drainObjs(noPDB, names)
		if err != nil {
			return ResilienceResult{}, err
		}
		sc := ResilienceScenario{Kind: kinds[i], CapacityType: p.capacity, InstanceType: p.itype, Zone: p.zone, Nodes: names,
			CPUShare: ratio(p.cpu, totalCPU), NodeShare: ratio(float64(len(names)), float64(len(nodes))),
			Verdict: d.Verdict, Evicted: d.Evicted, Unschedulable: d.Unschedulable, Unmanaged: d.Unmanaged, Risks: d.Risks}
		if kinds[i] == "zone" {
			sc.Name = "zone " + p.zone
		} else {
			sc.Name = fmt.Sprintf("%s %s in %s", p.capacity, p.itype, p.zone)
		}
		res.Scenarios = append(res.Scenarios, sc)
		if kinds[i] == "pool" {
			for _, rk := range d.Risks {
				if rk.Code == "DRAIN_ALL_REPLICAS" && len(res.Findings) < 100 {
					res.Findings = append(res.Findings, Risk{"medium", "RESILIENCE_WORKLOAD_IN_ONE_POOL", "A workload lives entirely in one pool",
						fmt.Sprintf("Every pod of %s/%s runs in %s; one reclaim of that pool takes the whole workload down until replacements start.", rk.Resource.Namespace, rk.Resource.Name, sc.Name), rk.Resource})
				}
			}
		}
		if sc.Unschedulable > 0 || sc.Unmanaged > 0 {
			var parts []string
			if sc.Unschedulable > 0 {
				parts = append(parts, fmt.Sprintf("%d evicted pod(s) fit nowhere on the remaining nodes", sc.Unschedulable))
			}
			if sc.Unmanaged > 0 {
				parts = append(parts, fmt.Sprintf("%d pod(s) without a controller would be lost", sc.Unmanaged))
			}
			res.Findings = append(res.Findings, Risk{"high", "RESILIENCE_LOSS_UNSCHEDULABLE", "Losing this capacity strands pods",
				fmt.Sprintf("If %s (%d node(s), %.0f%% of CPU) vanished, %s; replacement capacity is not modelled.", sc.Name, len(names), sc.CPUShare*100, strings.Join(parts, " and ")),
				ResourceRef{"Scenario", "", sc.Name}})
		}
	}
	if skipped > 0 {
		res.Unknowns = append(res.Unknowns, fmt.Sprintf("%d pool/zone scenario(s) beyond the first %d were not simulated", skipped, maxResilienceN))
	}
	sortRisks(res.Findings)
	bad := 0
	for _, s := range res.Scenarios {
		if s.Unschedulable > 0 || s.Unmanaged > 0 {
			bad++
		}
	}
	res.Summary = fmt.Sprintf("%d scenario(s) simulated; %d would strand pods; %d finding(s); %d node(s) with missing labels.", len(res.Scenarios), bad, len(res.Findings), len(missing))
	return res, nil
}

// ResilienceText renders the report for the terminal.
func ResilienceText(r ResilienceResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "KAIRO resilience\n%s\n\nCapacity diversity:\n", r.Summary)
	for _, d := range r.Diversity {
		fmt.Fprintf(&b, "  %s: %d node(s), %d instance type(s), %d zone(s), %.0f%% of cluster CPU; largest pool %s (%.0f%%)\n", d.CapacityType, d.Nodes, d.InstanceTypes, d.Zones, d.CPUShareOfCluster*100, d.LargestPool, d.LargestPoolShare*100)
	}
	b.WriteString("\nLoss scenarios:\n")
	for _, s := range r.Scenarios {
		fmt.Fprintf(&b, "  %s: %s · %d node(s) (%.0f%% CPU) · %d evicted, %d unschedulable, %d unmanaged\n", s.Name, s.Verdict, len(s.Nodes), s.CPUShare*100, s.Evicted, s.Unschedulable, s.Unmanaged)
	}
	if len(r.Findings) > 0 {
		b.WriteString("\nFindings:\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", f.Severity, f.Code, f.Detail)
		}
	}
	for _, u := range r.Unknowns {
		fmt.Fprintf(&b, "unknown: %s\n", u)
	}
	b.WriteString("\nThresholds (judgement calls):\n")
	for _, t := range r.Thresholds {
		fmt.Fprintf(&b, "  - %s\n", t)
	}
	b.WriteString("\nNot modelled:\n")
	for _, x := range r.NotModelled {
		fmt.Fprintf(&b, "  - %s\n", x)
	}
	return b.String()
}
