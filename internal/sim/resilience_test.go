// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"strings"
	"testing"
)

func lnode(name, labels, cpu, mem string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: Node\nmetadata:\n  name: %s\n  labels: {%s}\nstatus:\n  allocatable: {cpu: \"%s\", memory: %s}\n---\n", name, labels, cpu, mem)
}

func spotLabels(ct, it, zone string) string {
	return fmt.Sprintf("karpenter.sh/capacity-type: %s, node.kubernetes.io/instance-type: %s, topology.kubernetes.io/zone: %s", ct, it, zone)
}

func scenarioNamed(r ResilienceResult, sub string) (ResilienceScenario, bool) {
	for _, s := range r.Scenarios {
		if strings.Contains(s.Name, sub) {
			return s, true
		}
	}
	return ResilienceScenario{}, false
}

func TestResilienceLosingAPoolStrandsPods(t *testing.T) {
	y := lnode("s1", spotLabels("spot", "m5.large", "a"), "4", "8Gi") + lnode("s2", spotLabels("spot", "m5.large", "b"), "4", "8Gi") + lnode("od", spotLabels("on-demand", "m5.large", "a"), "4", "8Gi") +
		cpod("w1", "s1", "web", "3", "1Gi", "") + cpod("w2", "s2", "web", "3", "1Gi", "") + cpod("w3", "od", "web", "3", "1Gi", "")
	r, err := Resilience(ResilienceRequest{ClusterYAML: y})
	if err != nil {
		t.Fatal(err)
	}
	// On-demand pools are not interruptible by the cloud and are not simulated as pools.
	if _, ok := scenarioNamed(r, "on-demand"); ok {
		t.Error("on-demand pool must not be a loss scenario")
	}
	s1, ok := scenarioNamed(r, "spot m5.large in a")
	if !ok || s1.Kind != "pool" || len(s1.Nodes) != 1 || s1.Unschedulable != 1 || s1.Verdict != "BLOCK" {
		t.Fatalf("pool a = %+v", s1)
	}
	if s1.CPUShare != 0.3333 || s1.NodeShare != 0.3333 {
		t.Errorf("shares = %v %v", s1.CPUShare, s1.NodeShare)
	}
	if z, ok := scenarioNamed(r, "zone a"); !ok || z.Kind != "zone" || len(z.Nodes) != 2 {
		t.Errorf("zone a = %+v", z)
	}
	found := false
	for _, f := range r.Findings {
		if f.Code == "RESILIENCE_LOSS_UNSCHEDULABLE" && strings.Contains(f.Detail, "fit nowhere") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %+v", r.Findings)
	}
	if len(r.Thresholds) != 2 || len(r.NotModelled) < 3 {
		t.Errorf("thresholds/notModelled missing: %v %v", r.Thresholds, r.NotModelled)
	}
}

func TestResilienceIgnoresPDBsAndSafeWhenCapacityExists(t *testing.T) {
	y := lnode("s1", spotLabels("spot", "m5.large", "a"), "8", "32Gi") + lnode("s2", spotLabels("spot", "c5.large", "b"), "8", "32Gi") + lnode("s3", spotLabels("spot", "r5.large", "c"), "8", "32Gi") +
		cpod("w1", "s1", "web", "1", "1Gi", "") + cpod("w2", "s2", "web", "1", "1Gi", "") +
		"---\napiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata: {name: web}\nspec:\n  minAvailable: 2\n  selector: {matchLabels: {app: web}}\n"
	r, err := Resilience(ResilienceRequest{ClusterYAML: y})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := scenarioNamed(r, "spot m5.large in a")
	if s.Verdict != "SAFE" || s.Unschedulable != 0 {
		t.Errorf("a PDB must not block an involuntary loss: %+v", s)
	}
	for _, f := range r.Findings {
		if f.Code == "RESILIENCE_FEW_INSTANCE_TYPES" || f.Code == "RESILIENCE_POOL_CONCENTRATION" {
			t.Errorf("well diversified spot capacity flagged: %+v", f)
		}
	}
	if r.Diversity[0].InstanceTypes != 3 || r.Diversity[0].Zones != 3 {
		t.Errorf("diversity = %+v", r.Diversity)
	}
}

func TestResilienceWholeWorkloadInOnePoolAndConcentration(t *testing.T) {
	y := lnode("s1", spotLabels("spot", "m5.large", "a"), "8", "32Gi") + lnode("s2", spotLabels("spot", "m5.large", "a"), "8", "32Gi") + lnode("s3", spotLabels("spot", "m5.large", "b"), "2", "8Gi") +
		cpod("c1", "s1", "cache", "1", "1Gi", "") + cpod("c2", "s2", "cache", "1", "1Gi", "")
	r, _ := Resilience(ResilienceRequest{ClusterYAML: y})
	codes := map[string]bool{}
	for _, f := range r.Findings {
		codes[f.Code] = true
	}
	for _, c := range []string{"RESILIENCE_WORKLOAD_IN_ONE_POOL", "RESILIENCE_POOL_CONCENTRATION", "RESILIENCE_FEW_INSTANCE_TYPES"} {
		if !codes[c] {
			t.Errorf("missing finding %s in %+v", c, r.Findings)
		}
	}
}

func TestResilienceMissingLabelsAreUnknownNotGuessed(t *testing.T) {
	y := nodeYAML("bare", "4", "8Gi") + lnode("eks", "eks.amazonaws.com/capacityType: SPOT, beta.kubernetes.io/instance-type: t3.large, topology.kubernetes.io/zone: z1", "4", "8Gi") +
		lnode("gke", "cloud.google.com/gke-spot: \"true\"", "4", "8Gi")
	r, err := Resilience(ResilienceRequest{ClusterYAML: y})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Unknowns) < 2 || !strings.Contains(strings.Join(r.Unknowns, " "), "node bare has no capacity type, instance type, zone label") {
		t.Errorf("unknowns = %v", r.Unknowns)
	}
	if _, ok := scenarioNamed(r, "spot t3.large in z1"); !ok {
		t.Errorf("EKS/beta labels not understood: %+v", r.Scenarios)
	}
	if _, ok := scenarioNamed(r, "spot unknown in unknown"); !ok {
		t.Errorf("GKE spot label not understood: %+v", r.Scenarios)
	}
	if _, ok := scenarioNamed(r, "unknown unknown in unknown"); !ok {
		t.Errorf("unlabelled node must be grouped as unknown, not on-demand or spot: %+v", r.Scenarios)
	}
}

func TestResilienceHostileInput(t *testing.T) {
	if _, err := Resilience(ResilienceRequest{ClusterYAML: "kind: Service\nmetadata: {name: s}"}); err == nil {
		t.Error("no nodes must be an error")
	}
	if _, err := Resilience(ResilienceRequest{ClusterYAML: "{{{"}); err == nil {
		t.Error("garbage must be an error")
	}
	// Many distinct pools: capped, and the cap is reported.
	var b strings.Builder
	for i := 0; i < maxResilienceN+20; i++ {
		b.WriteString(lnode(fmt.Sprintf("n%d", i), spotLabels("spot", fmt.Sprintf("t%d", i), "z"), "1", "1Gi"))
	}
	r, err := Resilience(ResilienceRequest{ClusterYAML: b.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Scenarios) != maxResilienceN || !strings.Contains(strings.Join(r.Unknowns, " "), "not simulated") {
		t.Errorf("scenarios %d, unknowns %v", len(r.Scenarios), r.Unknowns)
	}
	r2, _ := Resilience(ResilienceRequest{ClusterYAML: b.String()})
	if ResilienceText(r) != ResilienceText(r2) {
		t.Error("not deterministic")
	}
}
