// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"strings"
	"testing"
)

func disruptionOf(t *testing.T, y string) map[string]DisruptionNode {
	t.Helper()
	r, err := DisruptionRisk(DisruptionRequest{ClusterYAML: y})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]DisruptionNode{}
	for _, n := range r.Nodes {
		out[n.Name] = n
	}
	return out
}

func factorOf(n DisruptionNode, code string) DisruptionFactor {
	for _, f := range n.Factors {
		if f.Code == code {
			return f
		}
	}
	return DisruptionFactor{}
}

func TestDisruptionScoresFactorsWithInputs(t *testing.T) {
	probe := "      readinessProbe: {initialDelaySeconds: 50, periodSeconds: 10}\n"
	y := nodeYAML("a", "8", "32Gi") + nodeYAML("b", "8", "32Gi") + nodeYAML("c", "8", "32Gi") +
		// a: a single-replica pod with a PVC, a slow probe, and an anti-affinity constraint.
		cpod("solo-1", "a", "solo", "1", "1Gi", "") +
		// b: both replicas of "pair" live here -> ALL_REPLICAS_HERE.
		cpod("pair-1", "b", "pair", "1", "1Gi", "") + cpod("pair-2", "b", "pair", "1", "1Gi", "") +
		// c: spread over nodes, nothing notable.
		cpod("web-1", "c", "web", "1", "1Gi", "") + cpod("web-2", "a", "web", "1", "1Gi", "")
	y = strings.Replace(y, "kind: Pod\nmetadata:\n  name: solo-1\n", "kind: Pod\nmetadata:\n  name: solo-1\n", 1)
	// add PVC, probe and anti-affinity to solo-1 by rewriting its spec
	y = strings.Replace(y, "  nodeName: a\n  containers:\n    - name: c\n"+"      resources:\n        requests: {cpu: \"1\", memory: 1Gi}\n---\napiVersion: v1\nkind: Pod\nmetadata:\n  name: pair-1",
		"  nodeName: a\n  volumes: [{name: d, persistentVolumeClaim: {claimName: data}}]\n  affinity: {podAntiAffinity: {requiredDuringSchedulingIgnoredDuringExecution: [{topologyKey: kubernetes.io/hostname}]}}\n  containers:\n    - name: c\n"+probe+"      resources:\n        requests: {cpu: \"1\", memory: 1Gi}\n---\napiVersion: v1\nkind: Pod\nmetadata:\n  name: pair-1", 1)
	got := disruptionOf(t, y)
	a, b, c := got["a"], got["b"], got["c"]
	if f := factorOf(a, "SINGLE_REPLICA"); f.Points <= 0 || len(f.Pods) != 1 || !strings.Contains(f.Pods[0], "solo") {
		t.Errorf("a SINGLE_REPLICA = %+v", f)
	}
	if f := factorOf(a, "LOCAL_STATE"); f.Points <= 0 {
		t.Errorf("a LOCAL_STATE = %+v (pvc must count)", f)
	}
	if f := factorOf(a, "PLACEMENT_CONSTRAINTS"); f.Points <= 0 {
		t.Errorf("a PLACEMENT_CONSTRAINTS = %+v", f)
	}
	if f := factorOf(a, "SLOW_START"); f.Points != 15*60.0/120 {
		t.Errorf("a SLOW_START = %+v, want 60s budget = 7.5 points", f)
	}
	if f := factorOf(b, "ALL_REPLICAS_HERE"); f.Points <= 0 {
		t.Errorf("b ALL_REPLICAS_HERE = %+v", f)
	}
	if !(c.Score < b.Score && b.Score < a.Score) {
		t.Errorf("scores a=%d b=%d c=%d, want c < b < a", a.Score, b.Score, c.Score)
	}
	if len(a.Unknowns) == 0 || !strings.Contains(strings.Join(c.Unknowns, " "), "no startup/readiness probe") {
		t.Errorf("missing probes must be reported as unknown, not scored: a=%v c=%v", a.Unknowns, c.Unknowns)
	}
	if f := factorOf(c, "SLOW_START"); f.Points != 0 || !strings.Contains(f.Detail, "unknown") {
		t.Errorf("no probes must not default a startup time: %+v", f)
	}
}

func TestDisruptionBlockersAndPDB(t *testing.T) {
	y := nodeYAML("a", "8", "32Gi") + nodeYAML("b", "8", "32Gi") +
		cpod("lone", "a", "", "1", "1Gi", "") +
		cpod("w1", "b", "web", "1", "1Gi", "") + cpod("w2", "b", "web", "1", "1Gi", "") +
		"---\napiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata: {name: web}\nspec:\n  minAvailable: 2\n  selector: {matchLabels: {app: web}}\n"
	got := disruptionOf(t, y)
	if got["a"].Level != "BLOCKED" || len(got["a"].Blockers) != 1 {
		t.Errorf("unmanaged pod must block: %+v", got["a"])
	}
	if f := factorOf(got["b"], "PDB_PRESSURE"); f.Points != 25 || !strings.Contains(f.Detail, "allows 0") {
		t.Errorf("PDB with no budget = %+v", f)
	}
	r, _ := DisruptionRisk(DisruptionRequest{ClusterYAML: y})
	if r.Nodes[len(r.Nodes)-1].Name != "a" {
		t.Errorf("blocked nodes sort last: %v", r.Nodes)
	}
	// Node-level do-not-disrupt annotation.
	ny := "apiVersion: v1\nkind: Node\nmetadata:\n  name: n\n  annotations: {karpenter.sh/do-not-disrupt: \"true\"}\nstatus:\n  allocatable: {cpu: \"4\", memory: 8Gi}\n"
	if n := disruptionOf(t, ny)["n"]; n.Level != "BLOCKED" || !strings.Contains(strings.Join(n.Blockers, " "), "node is annotated") {
		t.Errorf("node annotation must block: %+v", n)
	}
}

func TestDisruptionDeterministicAndPublishesModel(t *testing.T) {
	y := nodeYAML("a", "8", "32Gi") + nodeYAML("b", "8", "32Gi") + cpod("p", "a", "x", "1", "1Gi", "")
	r1, _ := DisruptionRisk(DisruptionRequest{ClusterYAML: y})
	r2, _ := DisruptionRisk(DisruptionRequest{ClusterYAML: y})
	if DisruptionText(r1) != DisruptionText(r2) {
		t.Error("not deterministic")
	}
	sum := 0.0
	for _, w := range r1.Weights {
		sum += w.MaxPoints
	}
	if sum != 100 || len(r1.NotModelled) == 0 {
		t.Errorf("weights sum %v, notModelled %v", sum, r1.NotModelled)
	}
	if !r1.Nodes[0].Removable && !r1.Nodes[1].Removable {
		t.Error("one of two nodes should be reported as a consolidation candidate")
	}
}

func TestDisruptionHostileInput(t *testing.T) {
	if _, err := DisruptionRisk(DisruptionRequest{ClusterYAML: "kind: Service\nmetadata: {name: s}"}); err == nil {
		t.Error("no nodes must be an error")
	}
	if _, err := DisruptionRisk(DisruptionRequest{ClusterYAML: "{{{{"}); err == nil {
		t.Error("garbage must be an error")
	}
	// Probes with absurd, negative and string values, a pod on an unknown node, a nil PDB selector.
	y := nodeYAML("a", "8", "32Gi") + cpod("p", "a", "x", "1", "1Gi", "") + cpod("q", "ghost", "x", "1", "1Gi", "") +
		"---\napiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata: {name: n}\nspec: {minAvailable: 1}\n"
	y = strings.Replace(y, "    - name: c\n", "    - name: c\n      startupProbe: {initialDelaySeconds: -5, periodSeconds: 999999999999, failureThreshold: abc}\n", 1)
	r, err := DisruptionRisk(DisruptionRequest{ClusterYAML: y})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "not in the snapshot") {
		t.Errorf("warnings = %v", r.Warnings)
	}
	if s := r.Nodes[0].Score; s < 0 || s > 100 {
		t.Errorf("score out of range: %d", s)
	}
}
