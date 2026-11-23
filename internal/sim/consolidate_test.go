// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func cpod(name, node, owner string, cpu, mem string, extra string) string {
	own := ""
	if owner != "" {
		own = fmt.Sprintf("  ownerReferences: [{kind: ReplicaSet, name: %s}]\n", owner)
	}
	return fmt.Sprintf("---\napiVersion: v1\nkind: Pod\nmetadata:\n  name: %s\n  labels: {app: %s}\n%s%sspec:\n  nodeName: %s\n  containers:\n    - name: c\n      resources:\n        requests: {cpu: \"%s\", memory: %s}\n", name, owner, own, extra, node, cpu, mem)
}

func consolidate(t *testing.T, y string) ConsolidateResult {
	t.Helper()
	r, err := Consolidate(ConsolidateRequest{ClusterYAML: y})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func nodeNamed(r ConsolidateResult, n string) ConsolidateNode {
	for _, x := range r.Nodes {
		if x.Name == n {
			return x
		}
	}
	return ConsolidateNode{}
}

func TestConsolidateEmptiesUnderusedNodes(t *testing.T) {
	y := nodeYAML("a", "8", "32Gi") + nodeYAML("b", "8", "32Gi") + nodeYAML("c", "8", "32Gi") +
		cpod("p1", "a", "web", "2", "4Gi", "") + cpod("p2", "b", "web", "2", "4Gi", "") + cpod("p3", "c", "web", "1", "2Gi", "")
	r := consolidate(t, y)
	if r.NodesBefore != 3 || r.NodesAfter != 1 || len(r.Removable) != 2 {
		t.Fatalf("%+v", r)
	}
	if len(r.Moves) != 2 {
		t.Errorf("moves = %+v", r.Moves)
	}
	for _, m := range r.Moves {
		if m.To != "a" && m.To != "b" && m.To != "c" {
			t.Errorf("bad destination %+v", m)
		}
		if nodeNamed(r, m.To).Removable {
			t.Errorf("pod moved onto a removed node: %+v", m)
		}
	}
	if r.CPUReclaimCores != 16 || r.MemoryReclaimBytes != 64<<30 {
		t.Errorf("reclaim = %v cores, %d bytes", r.CPUReclaimCores, r.MemoryReclaimBytes)
	}
	if r.Utilization.CPUBefore >= r.Utilization.CPUAfter {
		t.Errorf("utilisation should rise: %+v", r.Utilization)
	}
	if len(r.NotModelled) < 3 || !strings.Contains(strings.Join(r.NotModelled, " "), "affinity") || !strings.Contains(strings.Join(r.NotModelled, " "), "Topology spread") || !strings.Contains(strings.Join(r.NotModelled, " "), "PersistentVolume topology") {
		t.Errorf("notModelled = %v", r.NotModelled)
	}
}

func TestConsolidateConstraints(t *testing.T) {
	two := nodeYAML("a", "4", "16Gi") + nodeYAML("b", "4", "16Gi")
	cases := []struct {
		name, yaml string
		removable  int
		reasonOn   string // node whose Reason must contain reasonHas
		reasonHas  string
	}{
		{"does not fit", two + cpod("p1", "a", "x", "3", "1Gi", "") + cpod("p2", "b", "y", "3", "1Gi", ""), 0, "a", "fits on"},
		{"unmanaged pod pins its node", two + cpod("lone", "a", "", "1", "1Gi", "") + cpod("p2", "b", "y", "1", "1Gi", ""), 1, "a", "no controller"},
		{"do-not-disrupt pins its node", two + cpod("p1", "a", "x", "1", "1Gi", "  annotations: {karpenter.sh/do-not-disrupt: \"true\"}\n") + cpod("p2", "b", "y", "1", "1Gi", ""), 1, "a", "opts out"},
		{"safe-to-evict false pins its node", two + cpod("p1", "a", "x", "1", "1Gi", "  annotations: {cluster-autoscaler.kubernetes.io/safe-to-evict: \"false\"}\n") + cpod("p2", "b", "y", "1", "1Gi", ""), 1, "a", "opts out"},
		{"strict PDB blocks", two + cpod("p1", "a", "x", "1", "1Gi", "") + cpod("p2", "b", "y", "1", "1Gi", "") + pdbYAML("x", "x", "minAvailable: 1") + pdbYAML("y", "y", "minAvailable: 1"), 0, "a", "PodDisruptionBudget"},
		{"PDB with room allows it", two + cpod("p1", "a", "x", "1", "1Gi", "") + cpod("p3", "b", "x", "1", "1Gi", "") + pdbYAML("x", "x", "maxUnavailable: 1"), 1, "", ""},
		{"nodeSelector blocks a move", strings.Replace(nodeYAML("a", "4", "16Gi"), "{name: a}", "{name: a, labels: {pool: g}}", 1) + nodeYAML("b", "4", "16Gi") +
			strings.Replace(cpod("p1", "a", "x", "1", "1Gi", ""), "spec:\n", "spec:\n  nodeSelector: {pool: g}\n", 1) + cpod("p2", "b", "y", "1", "1Gi", ""), 1, "a", "fits on"},
		{"cordoned node cannot receive", strings.Replace(nodeYAML("a", "4", "16Gi"), "status:", "spec: {unschedulable: true}\nstatus:", 1) + nodeYAML("b", "2", "16Gi") + cpod("p1", "a", "x", "1", "1Gi", "") + cpod("p2", "b", "y", "1.5", "1Gi", ""), 0, "b", "fits on"},
		{"tainted node is skipped unless tolerated", strings.Replace(nodeYAML("a", "4", "16Gi"), "status:", "spec: {taints: [{key: gpu, effect: NoSchedule}]}\nstatus:", 1) + nodeYAML("b", "2", "16Gi") + cpod("p1", "a", "x", "1", "1Gi", "") + cpod("p2", "b", "y", "1.5", "1Gi", ""), 0, "b", "fits on"},
		{"toleration allows a tainted target", strings.Replace(nodeYAML("a", "4", "16Gi"), "status:", "spec: {taints: [{key: gpu, effect: NoSchedule}]}\nstatus:", 1) + nodeYAML("b", "2", "16Gi") + cpod("p1", "b", "x", "1", "1Gi", "") + strings.Replace(cpod("p2", "b", "y", "0.5", "1Gi", ""), "spec:\n", "spec:\n  tolerations: [{key: gpu, operator: Exists}]\n", 1), 1, "", ""},
		{"daemonset and static pods do not block", two + cpod("ds", "a", "", "1", "1Gi", "  ownerReferences: [{kind: DaemonSet, name: ds}]\n") + cpod("p2", "b", "y", "1", "1Gi", ""), 1, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := consolidate(t, c.yaml)
			if len(r.Removable) != c.removable {
				t.Fatalf("removable = %v, want %d\n%s", r.Removable, c.removable, ConsolidateText(r))
			}
			if c.reasonOn != "" {
				if got := nodeNamed(r, c.reasonOn).Reason; !strings.Contains(got, c.reasonHas) {
					t.Errorf("node %s reason = %q, want it to contain %q", c.reasonOn, got, c.reasonHas)
				}
			}
			if r.NodesAfter < 1 {
				t.Error("must keep at least one node")
			}
		})
	}
}

func TestConsolidateRepackedPodsCanMoveAgain(t *testing.T) {
	// c's pod lands on b first (emptiest-first), then b is also emptied onto a.
	y := nodeYAML("a", "8", "32Gi") + nodeYAML("b", "8", "32Gi") + nodeYAML("c", "8", "32Gi") +
		cpod("pa", "a", "x", "5", "1Gi", "") + cpod("pb", "b", "y", "2", "1Gi", "") + cpod("pc", "c", "z", "1", "1Gi", "")
	r := consolidate(t, y)
	if r.NodesAfter != 1 || len(r.Moves) != 2 {
		t.Fatalf("%s", ConsolidateText(r))
	}
	for _, m := range r.Moves {
		if m.To != "a" || m.From == "a" {
			t.Errorf("move %+v: everything should end on a", m)
		}
	}
}

func TestConsolidateSingleNodeAndErrors(t *testing.T) {
	r := consolidate(t, nodeYAML("a", "4", "16Gi")+cpod("p", "a", "x", "1", "1Gi", ""))
	if len(r.Removable) != 0 || r.NodesAfter != 1 || !strings.Contains(nodeNamed(r, "a").Reason, "last remaining") {
		t.Errorf("%+v", r)
	}
	if _, err := Consolidate(ConsolidateRequest{ClusterYAML: "kind: Service\nmetadata: {name: s}"}); err == nil {
		t.Error("no nodes should be an error")
	}
	if _, err := Consolidate(ConsolidateRequest{ClusterYAML: "a: [1"}); err == nil {
		t.Error("bad yaml should be an error")
	}
	// Empty nodes are removable; with all empty one stays.
	r = consolidate(t, nodeYAML("a", "4", "16Gi")+nodeYAML("b", "4", "16Gi")+nodeYAML("c", "4", "16Gi"))
	if r.NodesAfter != 1 || len(r.Moves) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestConsolidateDeterministicAndCost(t *testing.T) {
	y := nodeYAML("a", "8", "32Gi") + nodeYAML("b", "8", "32Gi") + nodeYAML("c", "8", "32Gi") +
		cpod("p1", "a", "web", "1", "1Gi", "") + cpod("p2", "b", "web", "1", "1Gi", "") + cpod("p3", "c", "web", "1", "1Gi", "")
	req := ConsolidateRequest{ClusterYAML: y, Prices: &Prices{NodeHour: 0.5, GiBHour: 0.01, Currency: "USD"}}
	a, _ := Consolidate(req)
	ja, _ := json.Marshal(a)
	for i := 0; i < 5; i++ {
		b, _ := Consolidate(req)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatal("not deterministic")
		}
	}
	c := a.Cost
	if c == nil || len(a.Removable) != 2 {
		t.Fatalf("%+v", a)
	}
	if want := -2 * 0.5 * 730; math.Abs(c.NodeMonthlyDelta-want) > 0.01 {
		t.Errorf("node = %v want %v", c.NodeMonthlyDelta, want)
	}
	if want := -64 * 0.01 * 730; math.Abs(c.MemoryMonthlyDelta-want) > 0.01 {
		t.Errorf("memory = %v want %v", c.MemoryMonthlyDelta, want)
	}
	if !c.Estimate || !strings.Contains(c.Note, "Estimate") {
		t.Errorf("not labelled an estimate: %+v", c)
	}
	if r, _ := Consolidate(ConsolidateRequest{ClusterYAML: y}); r.Cost != nil {
		t.Error("no prices, no cost")
	}
	if !strings.Contains(ConsolidateText(a), "Estimated monthly cost change: -") {
		t.Error(ConsolidateText(a))
	}
}

func TestConsolidateRespectsNodeDoNotDisrupt(t *testing.T) {
	pinned := strings.Replace(nodeYAML("a", "4", "16Gi"), "metadata: {name: a}", "metadata: {name: a, annotations: {karpenter.sh/do-not-disrupt: \"true\"}}", 1)
	r := consolidate(t, pinned+nodeYAML("b", "4", "16Gi")+cpod("p1", "a", "x", "1", "1Gi", "")+cpod("p2", "b", "y", "1", "1Gi", ""))
	if len(r.Removable) != 1 || r.Removable[0] != "b" || !strings.Contains(nodeNamed(r, "a").Reason, "do-not-disrupt") {
		t.Errorf("annotated node must stay: %+v", r)
	}
}
