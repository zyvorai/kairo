// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"strings"
	"testing"
)

func nodeYAML(name, cpu, mem string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: Node\nmetadata: {name: %s}\nstatus:\n  allocatable: {cpu: \"%s\", memory: %s}\n---\n", name, cpu, mem)
}

func deployYAML(replicas int, image, cpu, strategy string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec:
  replicas: %d
%s  template:
    spec:
      containers:
        - name: c
          image: %s
          resources:
            requests: {cpu: "%s", memory: 1Gi}
`, replicas, strategy, image, cpu)
}

func TestRolloutSurge(t *testing.T) {
	tests := []struct {
		name     string
		nodes    string
		live     string
		desired  string
		wantCode string
		wantSev  string
		verdict  string
	}{
		{"fits at steady state, no room to surge, maxUnavailable 0 stalls", nodeYAML("a", "4", "32Gi"),
			deployYAML(4, "web:1", "1", ""), deployYAML(4, "web:2", "1", "  strategy:\n    rollingUpdate: {maxSurge: 1, maxUnavailable: 0}\n"),
			"ROLLOUT_SURGE_UNSCHEDULABLE", "high", "REVIEW"},
		{"default 25% surge rounds up to 1, still no room: degraded", nodeYAML("a", "4", "32Gi"),
			deployYAML(4, "web:1", "1", ""), deployYAML(4, "web:2", "1", ""),
			"ROLLOUT_SURGE_UNSCHEDULABLE", "medium", "REVIEW"},
		{"headroom exists", nodeYAML("a", "5", "32Gi"),
			deployYAML(4, "web:1", "1", ""), deployYAML(4, "web:2", "1", ""), "", "", "SAFE"},
		{"Recreate never surges", nodeYAML("a", "4", "32Gi"),
			deployYAML(4, "web:1", "1", ""), deployYAML(4, "web:2", "1", "  strategy: {type: Recreate}\n"), "", "", "SAFE"},
		{"replica-only change is not a rollout", nodeYAML("a", "4", "32Gi"),
			deployYAML(2, "web:1", "1", ""), deployYAML(4, "web:1", "1", ""), "", "", "SAFE"},
		{"percent surge rounds up (10% of 4 = 1)", nodeYAML("a", "4", "32Gi"),
			deployYAML(4, "web:1", "1", ""), deployYAML(4, "web:2", "1", "  strategy:\n    rollingUpdate: {maxSurge: 10%, maxUnavailable: 0}\n"),
			"ROLLOUT_SURGE_UNSCHEDULABLE", "high", "REVIEW"},
		{"maxSurge 0 means no surge", nodeYAML("a", "4", "32Gi"),
			deployYAML(4, "web:1", "1", ""), deployYAML(4, "web:2", "1", "  strategy:\n    rollingUpdate: {maxSurge: 0, maxUnavailable: 1}\n"), "", "", "SAFE"},
		{"new deployment has no rollout", nodeYAML("a", "4", "32Gi"),
			"", deployYAML(4, "web:1", "1", ""), "", "", "SAFE"},
		{"steady-state failure is reported once, not as surge", nodeYAML("a", "2", "32Gi"),
			deployYAML(2, "web:1", "1", ""), deployYAML(4, "web:2", "1", ""), "UNSCHEDULABLE", "high", "BLOCK"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := tt.nodes + tt.live
			if tt.live == "" {
				cluster = strings.TrimSuffix(tt.nodes, "---\n")
			}
			r, err := Simulate(Request{ClusterYAML: cluster, DesiredYAML: tt.desired})
			if err != nil {
				t.Fatal(err)
			}
			if r.Verdict != tt.verdict {
				t.Errorf("verdict %s, want %s (%+v)", r.Verdict, tt.verdict, r.Risks)
			}
			c := codes(r)
			if tt.wantCode == "" && (c["ROLLOUT_SURGE_UNSCHEDULABLE"] || r.RolloutRisks != 0) {
				t.Errorf("unexpected rollout risk: %+v", r.Risks)
			}
			if tt.wantCode != "" {
				if !c[tt.wantCode] {
					t.Fatalf("missing %s in %+v", tt.wantCode, r.Risks)
				}
				if tt.wantCode == "ROLLOUT_SURGE_UNSCHEDULABLE" {
					for _, x := range r.Risks {
						if x.Code == tt.wantCode && x.Severity != tt.wantSev {
							t.Errorf("severity %s, want %s", x.Severity, tt.wantSev)
						}
					}
				} else if c["ROLLOUT_SURGE_UNSCHEDULABLE"] {
					t.Errorf("surge reported on top of steady-state failure")
				}
			}
		})
	}
}

func TestRolloutDoesNotConsumeCapacity(t *testing.T) {
	// The surge is simulated on a copy: the second Deployment must still fit.
	two := strings.Replace(deployYAML(1, "a:2", "1", ""), "name: web", "name: one", 1) + "---\n" + strings.Replace(deployYAML(1, "b:2", "1", ""), "name: web", "name: two", 1)
	live := strings.Replace(deployYAML(1, "a:1", "1", ""), "name: web", "name: one", 1) + "---\n" + strings.Replace(deployYAML(1, "b:1", "1", ""), "name: web", "name: two", 1)
	r, err := Simulate(Request{ClusterYAML: nodeYAML("a", "3", "8Gi") + live, DesiredYAML: two})
	if err != nil {
		t.Fatal(err)
	}
	if r.UnschedulablePods != 0 || r.Verdict != "SAFE" {
		t.Errorf("got %s uns=%d %+v", r.Verdict, r.UnschedulablePods, r.Risks)
	}
}

func TestResolveIntOrPercent(t *testing.T) {
	tests := []struct {
		v     any
		total int
		up    bool
		want  int
		ok    bool
	}{
		{nil, 4, true, 0, false}, {2, 4, true, 2, true}, {"25%", 6, true, 2, true}, {"25%", 6, false, 1, true},
		{"100%", 3, true, 3, true}, {"0%", 3, true, 0, true}, {"abc", 3, true, 0, false}, {"-5%", 3, true, 0, false},
		{-1, 3, true, 0, false}, {"3", 9, true, 3, true},
	}
	for _, tt := range tests {
		got, ok := resolveIntOrPercent(tt.v, tt.total, tt.up)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%v/%d up=%v: got %d,%v want %d,%v", tt.v, tt.total, tt.up, got, ok, tt.want, tt.ok)
		}
	}
}

func TestTerminatedPodsHoldNoCapacity(t *testing.T) {
	podOf := func(phase string) string {
		return fmt.Sprintf("---\napiVersion: v1\nkind: Pod\nmetadata: {name: p-%s}\nspec:\n  nodeName: a\n  containers:\n    - name: c\n      resources:\n        requests: {cpu: \"3\", memory: 1Gi}\nstatus: {phase: %s}\n", phase, phase)
	}
	for phase, wantUns := range map[string]int{"Succeeded": 0, "Failed": 0, "Running": 1} {
		r, err := Simulate(Request{ClusterYAML: nodeYAML("a", "4", "8Gi") + strings.TrimPrefix(podOf(phase), "---\n"), DesiredYAML: deployYAML(1, "x", "2", "")})
		if err != nil {
			t.Fatal(err)
		}
		if r.UnschedulablePods != wantUns {
			t.Errorf("%s: unschedulable %d, want %d", phase, r.UnschedulablePods, wantUns)
		}
	}
}

func TestParseTargetVersion(t *testing.T) {
	tests := []struct {
		in   string
		want k8sVersion
		err  bool
	}{
		{"", latestKnown, false}, {"1.29", 29, false}, {"v1.25.3", 25, false}, {" v1.22 ", 22, false},
		{"2.0", 0, true}, {"1", 0, true}, {"1.x", 0, true}, {"1.-3", 0, true}, {"1.2.3.4", 0, true}, {"latest", 0, true},
	}
	for _, tt := range tests {
		got, err := parseTargetVersion(tt.in)
		if (err != nil) != tt.err || (!tt.err && got != tt.want) {
			t.Errorf("%q: got %v,%v want %v err=%v", tt.in, got, err, tt.want, tt.err)
		}
	}
}

func TestAPIVersionRisks(t *testing.T) {
	obj := func(av, kind string) string {
		return fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata: {name: x}\n", av, kind)
	}
	tests := []struct {
		name, av, kind, target, wantCode, wantVerdict string
	}{
		{"removed ingress at 1.22", "networking.k8s.io/v1beta1", "Ingress", "1.22", "API_REMOVED", "BLOCK"},
		{"ingress ok at 1.21 but deprecated", "networking.k8s.io/v1beta1", "Ingress", "1.21", "API_DEPRECATED", "SAFE"},
		{"ingress fine before deprecation", "networking.k8s.io/v1beta1", "Ingress", "1.18", "", "SAFE"},
		{"cronjob removed at 1.25", "batch/v1beta1", "CronJob", "v1.25.0", "API_REMOVED", "BLOCK"},
		{"cronjob deprecated at 1.24", "batch/v1beta1", "CronJob", "1.24", "API_DEPRECATED", "SAFE"},
		{"hpa v2beta2 removed 1.26", "autoscaling/v2beta2", "HorizontalPodAutoscaler", "1.26", "API_REMOVED", "BLOCK"},
		{"hpa v2beta2 deprecated 1.25", "autoscaling/v2beta2", "HorizontalPodAutoscaler", "1.25", "API_DEPRECATED", "SAFE"},
		{"group-wide entry: apps/v1beta1", "apps/v1beta1", "StatefulSet", "1.16", "API_REMOVED", "BLOCK"},
		{"kind not in entry is ignored", "batch/v1beta1", "Job", "1.30", "", "SAFE"},
		{"stable api never flagged", "apps/v1", "Deployment", "1.33", "", "SAFE"},
		{"default target is latest", "policy/v1beta1", "PodDisruptionBudget", "", "API_REMOVED", "BLOCK"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Simulate(Request{ClusterYAML: nodeYAML("a", "1", "1Gi"), DesiredYAML: obj(tt.av, tt.kind), TargetVersion: tt.target})
			if err != nil {
				t.Fatal(err)
			}
			c := codes(r)
			for _, code := range []string{"API_REMOVED", "API_DEPRECATED"} {
				if c[code] != (code == tt.wantCode) {
					t.Errorf("%s present=%v, want code %q (%+v)", code, c[code], tt.wantCode, r.Risks)
				}
			}
			if r.Verdict != tt.wantVerdict {
				t.Errorf("verdict %s, want %s", r.Verdict, tt.wantVerdict)
			}
		})
	}
}

func TestInvalidTargetVersionIsAnError(t *testing.T) {
	if _, err := Simulate(Request{ClusterYAML: nodeYAML("a", "1", "1Gi"), DesiredYAML: "kind: Pod\n", TargetVersion: "banana"}); err == nil {
		t.Fatal("want error")
	}
}

func TestAPITableIsSane(t *testing.T) {
	for _, e := range removedAPIs {
		if e.removed == 0 || e.removed > latestKnown || e.deprecated > e.removed || e.replacement == "" {
			t.Errorf("bad entry %+v", e)
		}
	}
}

// ---- drain ----

func podYAML(name, nodeName, owner, labels, extra string) string {
	own := ""
	if owner != "" {
		k, n, _ := strings.Cut(owner, "/")
		own = fmt.Sprintf("  ownerReferences: [{kind: %s, name: %s}]\n", k, n)
	}
	return fmt.Sprintf(`---
apiVersion: v1
kind: Pod
metadata:
  name: %s
  labels: {%s}
%sspec:
  nodeName: %s
%s  containers:
    - name: c
      resources:
        requests: {cpu: "1", memory: 1Gi}
`, name, labels, own, nodeName, extra)
}

func pdbYAML(name, sel, spec string) string {
	return fmt.Sprintf("---\napiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata: {name: %s}\nspec:\n  %s\n  selector:\n    matchLabels: {app: %s}\n", name, spec, sel)
}

func TestDrain(t *testing.T) {
	nodes := nodeYAML("a", "4", "16Gi") + nodeYAML("b", "4", "16Gi") + strings.TrimSuffix(nodeYAML("c", "1", "16Gi"), "---\n")
	tests := []struct {
		name       string
		cluster    string
		drain      []string
		verdict    string
		wantCodes  []string
		noCodes    []string
		evicted    int
		ignored    int
		unsched    int
		pdbBlocked int
	}{
		{"safe: replicas spread, fits elsewhere", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + podYAML("w2", "b", "ReplicaSet/w", "app: w", ""),
			[]string{"a"}, "SAFE", nil, []string{"DRAIN_PDB_BLOCKED", "DRAIN_UNSCHEDULABLE"}, 1, 0, 0, 0},
		{"pdb with no disruptions blocks", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + podYAML("w2", "b", "ReplicaSet/w", "app: w", "") + pdbYAML("w", "w", "minAvailable: 2"),
			[]string{"a"}, "BLOCK", []string{"DRAIN_PDB_BLOCKED"}, nil, 1, 0, 0, 1},
		{"pdb with slack allows", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + podYAML("w2", "b", "ReplicaSet/w", "app: w", "") + pdbYAML("w", "w", "minAvailable: 1"),
			[]string{"a"}, "SAFE", nil, []string{"DRAIN_PDB_BLOCKED", "DRAIN_PDB_SERIALIZED"}, 1, 0, 0, 0},
		{"maxUnavailable 0 blocks", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + podYAML("w2", "b", "ReplicaSet/w", "app: w", "") + pdbYAML("w", "w", "maxUnavailable: 0"),
			[]string{"a"}, "BLOCK", []string{"DRAIN_PDB_BLOCKED"}, nil, 1, 0, 0, 1},
		{"pdb in other namespace does not apply", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + podYAML("w2", "b", "ReplicaSet/w", "app: w", "") +
			strings.Replace(pdbYAML("w", "w", "minAvailable: 2"), "metadata: {name: w}", "metadata: {name: w, namespace: other}", 1),
			[]string{"a"}, "SAFE", nil, []string{"DRAIN_PDB_BLOCKED"}, 1, 0, 0, 0},
		{"pdb serialized when more pods than allowed", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + podYAML("w2", "a", "ReplicaSet/w", "app: w", "") + podYAML("w3", "b", "ReplicaSet/w", "app: w", "") + pdbYAML("w", "w", "minAvailable: 2"),
			[]string{"a"}, "REVIEW", []string{"DRAIN_PDB_SERIALIZED"}, []string{"DRAIN_PDB_BLOCKED"}, 2, 0, 0, 0},
		{"daemonset and static pods are ignored", nodes +
			podYAML("d1", "a", "DaemonSet/d", "app: d", "") + strings.Replace(podYAML("s1", "a", "", "app: s", ""), "labels: {app: s}", "labels: {app: s}\n  annotations: {kubernetes.io/config.mirror: x}", 1),
			[]string{"a"}, "SAFE", nil, []string{"DRAIN_UNMANAGED_POD"}, 0, 2, 0, 0},
		{"unmanaged pod blocks", nodes + podYAML("lone", "a", "", "app: l", ""),
			[]string{"a"}, "BLOCK", []string{"DRAIN_UNMANAGED_POD"}, nil, 0, 0, 0, 0},
		{"no capacity left for big pod", nodes +
			strings.Replace(podYAML("big", "a", "ReplicaSet/big", "app: big", ""), "cpu: \"1\"", "cpu: \"6\"", 1) + podYAML("big2", "b", "ReplicaSet/big", "app: big", ""),
			[]string{"a"}, "BLOCK", []string{"DRAIN_UNSCHEDULABLE"}, nil, 1, 0, 1, 0},
		{"node selector pins pod to drained node", nodes +
			podYAML("p", "a", "ReplicaSet/p", "app: p", "  nodeSelector: {zone: z1}\n") + podYAML("p2", "b", "ReplicaSet/p", "app: p", ""),
			[]string{"a"}, "BLOCK", []string{"DRAIN_UNSCHEDULABLE"}, nil, 1, 0, 1, 0},
		{"all replicas on node warns", nodes + podYAML("only", "a", "ReplicaSet/only", "app: o", ""),
			[]string{"a"}, "REVIEW", []string{"DRAIN_ALL_REPLICAS"}, []string{"DRAIN_PDB_BLOCKED"}, 1, 0, 0, 0},
		{"emptyDir is a low note", nodes + podYAML("e", "a", "ReplicaSet/e", "app: e", "  volumes: [{name: v, emptyDir: {}}]\n") + podYAML("e2", "b", "ReplicaSet/e", "app: e", ""),
			[]string{"a"}, "REVIEW", []string{"DRAIN_LOCAL_DATA"}, nil, 1, 0, 0, 0},
		{"draining two nodes together leaves too little room", nodes +
			podYAML("x1", "a", "ReplicaSet/x", "app: x", "") + podYAML("x2", "b", "ReplicaSet/x", "app: x", "") +
			strings.Replace(podYAML("x3", "c", "ReplicaSet/x", "app: x", ""), "cpu: \"1\"", "cpu: \"1\"", 1),
			[]string{"a", "b"}, "BLOCK", []string{"DRAIN_UNSCHEDULABLE"}, nil, 2, 0, 2, 0},
		{"draining one of the same nodes still fits", nodes +
			podYAML("x1", "a", "ReplicaSet/x", "app: x", "") + podYAML("x2", "b", "ReplicaSet/x", "app: x", ""),
			[]string{"a"}, "SAFE", nil, []string{"DRAIN_UNSCHEDULABLE"}, 1, 0, 0, 0},
		{"completed pods are not evicted", nodes +
			strings.Replace(podYAML("done", "a", "Job/j", "app: j", ""), "spec:", "status: {phase: Succeeded}\nspec:", 1),
			[]string{"a"}, "SAFE", nil, nil, 0, 0, 0, 0},
		{"unready pod uses up the budget", nodes +
			podYAML("w1", "a", "ReplicaSet/w", "app: w", "") + strings.Replace(podYAML("w2", "b", "ReplicaSet/w", "app: w", ""), "spec:", "status: {conditions: [{type: Ready, status: \"False\"}]}\nspec:", 1) + pdbYAML("w", "w", "minAvailable: 1"),
			[]string{"a"}, "BLOCK", []string{"DRAIN_PDB_BLOCKED"}, nil, 1, 0, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// labels for the node selector case
			cluster := strings.Replace(tt.cluster, "metadata: {name: a}", "metadata: {name: a, labels: {zone: z1}}", 1)
			r, err := Drain(DrainRequest{ClusterYAML: cluster, Nodes: tt.drain})
			if err != nil {
				t.Fatal(err)
			}
			if r.Verdict != tt.verdict {
				t.Errorf("verdict %s, want %s: %+v", r.Verdict, tt.verdict, r.Risks)
			}
			c := map[string]bool{}
			for _, x := range r.Risks {
				c[x.Code] = true
			}
			for _, w := range tt.wantCodes {
				if !c[w] {
					t.Errorf("missing %s in %+v", w, r.Risks)
				}
			}
			for _, w := range tt.noCodes {
				if c[w] {
					t.Errorf("unexpected %s", w)
				}
			}
			if r.Evicted != tt.evicted || r.Ignored != tt.ignored || r.Unschedulable != tt.unsched || r.PDBBlocked != tt.pdbBlocked {
				t.Errorf("evicted=%d ignored=%d uns=%d pdb=%d; want %d %d %d %d", r.Evicted, r.Ignored, r.Unschedulable, r.PDBBlocked, tt.evicted, tt.ignored, tt.unsched, tt.pdbBlocked)
			}
		})
	}
}

func TestDrainErrors(t *testing.T) {
	cl := nodeYAML("a", "1", "1Gi")
	for name, req := range map[string]DrainRequest{
		"no nodes":     {ClusterYAML: cl},
		"blank node":   {ClusterYAML: cl, Nodes: []string{" "}},
		"unknown node": {ClusterYAML: cl, Nodes: []string{"zzz"}},
		"bad yaml":     {ClusterYAML: "a: [", Nodes: []string{"a"}},
	} {
		if _, err := Drain(req); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestDrainDeterministicAndDedupes(t *testing.T) {
	cl := nodeYAML("a", "4", "16Gi") + nodeYAML("b", "4", "16Gi") + strings.TrimSuffix(podYAML("w1", "a", "ReplicaSet/w", "app: w", ""), "")[4:]
	var first string
	for i := 0; i < 5; i++ {
		r, err := Drain(DrainRequest{ClusterYAML: cl, Nodes: []string{"a", "a", "a"}})
		if err != nil {
			t.Fatal(err)
		}
		s := fmt.Sprint(r)
		if i == 0 {
			first = s
		} else if s != first {
			t.Fatal("non-deterministic")
		}
		if len(r.Nodes) != 1 {
			t.Fatalf("nodes %v", r.Nodes)
		}
	}
}

func TestSelectorMatches(t *testing.T) {
	labels := map[string]any{"app": "w", "tier": "be"}
	tests := []struct {
		name string
		sel  string
		want bool
	}{
		{"empty selects all", "{}", true},
		{"matchLabels hit", "{matchLabels: {app: w}}", true},
		{"matchLabels miss", "{matchLabels: {app: z}}", false},
		{"In", "{matchExpressions: [{key: tier, operator: In, values: [be, fe]}]}", true},
		{"NotIn", "{matchExpressions: [{key: tier, operator: NotIn, values: [be]}]}", false},
		{"Exists", "{matchExpressions: [{key: app, operator: Exists}]}", true},
		{"DoesNotExist", "{matchExpressions: [{key: app, operator: DoesNotExist}]}", false},
		{"unknown operator fails closed", "{matchExpressions: [{key: app, operator: Weird}]}", false},
	}
	for _, tt := range tests {
		docs, err := ParseDocuments("s: " + tt.sel + "\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := selectorMatches(asMap(docs[0]["s"]), labels); got != tt.want {
			t.Errorf("%s: got %v", tt.name, got)
		}
	}
}
