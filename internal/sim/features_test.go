// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"strconv"
	"strings"
	"testing"
)

func node(name, extraSpec, labels string) string {
	return "apiVersion: v1\nkind: Node\nmetadata:\n  name: " + name + "\n" + labels + extraSpec +
		"status:\n  allocatable: {cpu: \"4\", memory: 16Gi}\n---\n"
}

func deploy(name string, replicas int, podExtra string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec:\n  replicas: " + itoa(replicas) + "\n  template:\n    spec:\n" + podExtra +
		"      containers:\n        - name: c\n          image: img:1\n          resources:\n            requests: {cpu: \"1\", memory: 1Gi}\n---\n"
}

func itoa(n int) string { return strconv.Itoa(n) }

func hpa(target string, minR, maxR int) string {
	return "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata: {name: h}\nspec:\n  scaleTargetRef: {kind: Deployment, name: " + target + "}\n  minReplicas: " + itoa(minR) + "\n  maxReplicas: " + itoa(maxR) + "\n---\n"
}

func run(t *testing.T, cluster, desired string) Result {
	t.Helper()
	r, err := Simulate(Request{ClusterYAML: cluster, DesiredYAML: desired})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHPAMaxUnschedulable(t *testing.T) {
	cl := node("n1", "", "")
	r := run(t, cl, deploy("web", 2, "")+hpa("web", 1, 10))
	if !codes(r)["HPA_MAX_UNSCHEDULABLE"] || r.HPARisks != 1 {
		t.Fatalf("want HPA_MAX_UNSCHEDULABLE, got %+v", r.Risks)
	}
	if r.UnschedulablePods != 0 || r.Verdict != "REVIEW" {
		t.Errorf("uns=%d verdict=%s", r.UnschedulablePods, r.Verdict)
	}
	// max fits: no risk
	r = run(t, cl, deploy("web", 2, "")+hpa("web", 1, 4))
	if r.HPARisks != 0 {
		t.Errorf("unexpected %+v", r.Risks)
	}
}

func TestHPAMinExceedsReplicas(t *testing.T) {
	r := run(t, node("n1", "", ""), deploy("web", 1, "")+hpa("web", 3, 4))
	if !codes(r)["HPA_MIN_EXCEEDS_REPLICAS"] {
		t.Fatalf("got %+v", r.Risks)
	}
	r = run(t, node("n1", "", ""), deploy("web", 1, "")+hpa("web", 4, 2))
	if !codes(r)["HPA_INVALID_BOUNDS"] {
		t.Fatalf("got %+v", r.Risks)
	}
}

func TestHPAWorstCaseDoesNotConsumeCapacity(t *testing.T) {
	// 4 CPUs: web scales to 4 in the worst case, but the second workload still fits at baseline.
	r := run(t, node("n1", "", ""), deploy("web", 1, "")+hpa("web", 1, 3)+deploy("other", 1, ""))
	if r.UnschedulablePods != 0 || r.HPARisks != 0 {
		t.Fatalf("%+v", r.Risks)
	}
}

func TestHPAOnlyForMatchingTarget(t *testing.T) {
	r := run(t, node("n1", "", ""), deploy("web", 1, "")+hpa("other", 1, 50))
	if r.HPARisks != 0 {
		t.Fatalf("%+v", r.Risks)
	}
}

func TestCordonedNodeExcluded(t *testing.T) {
	cl := node("n1", "spec:\n  unschedulable: true\n", "")
	r := run(t, cl, deploy("web", 1, ""))
	if r.UnschedulablePods != 1 {
		t.Fatalf("cordoned node must not schedule: %+v", r)
	}
	// Tolerating the cordon taint overrides it.
	tol := "      tolerations:\n        - {key: node.kubernetes.io/unschedulable, operator: Exists, effect: NoSchedule}\n"
	if r := run(t, cl, deploy("web", 1, tol)); r.UnschedulablePods != 0 {
		t.Fatalf("toleration ignored: %+v", r)
	}
}

func TestTaints(t *testing.T) {
	cl := node("n1", "spec:\n  taints:\n    - {key: gpu, value: \"true\", effect: NoSchedule}\n    - {key: soft, effect: PreferNoSchedule}\n", "")
	if r := run(t, cl, deploy("web", 1, "")); r.UnschedulablePods != 1 {
		t.Fatalf("taint ignored")
	}
	tol := "      tolerations:\n        - {key: gpu, operator: Equal, value: \"true\", effect: NoSchedule}\n"
	if r := run(t, cl, deploy("web", 1, tol)); r.UnschedulablePods != 0 {
		t.Fatalf("Equal toleration ignored")
	}
	wrong := "      tolerations:\n        - {key: gpu, value: \"false\"}\n"
	if r := run(t, cl, deploy("web", 1, wrong)); r.UnschedulablePods != 1 {
		t.Fatalf("wrong value should not tolerate")
	}
	all := "      tolerations:\n        - {operator: Exists}\n"
	if r := run(t, cl, deploy("web", 1, all)); r.UnschedulablePods != 0 {
		t.Fatalf("wildcard toleration ignored")
	}
}

func TestNodeSelector(t *testing.T) {
	cl := node("a", "", "  labels: {disk: hdd}\n") + node("b", "", "  labels: {disk: ssd}\n")
	sel := "      nodeSelector: {disk: ssd}\n"
	// 4 CPUs on the only ssd node: 4 fit, a 5th does not even though node a is empty.
	if r := run(t, cl, deploy("web", 4, sel)); r.UnschedulablePods != 0 {
		t.Fatalf("%+v", r.Risks)
	}
	if r := run(t, cl, deploy("web", 5, sel)); r.UnschedulablePods != 1 {
		t.Fatalf("selector not enforced: %d", r.UnschedulablePods)
	}
	if r := run(t, cl, deploy("web", 1, "      nodeSelector: {disk: nvme}\n")); r.UnschedulablePods != 1 {
		t.Fatalf("unmatched selector must be unschedulable")
	}
}

func TestDaemonSetSkipsCordonedNode(t *testing.T) {
	cl := node("a", "", "") + node("b", "spec:\n  unschedulable: true\n  taints:\n    - {key: x, effect: NoSchedule}\n", "")
	ds := "apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: agent}\nspec:\n  template:\n    spec:\n      containers:\n        - name: c\n          resources:\n            requests: {cpu: \"1\"}\n"
	r := run(t, cl, ds)
	// cordon is tolerated by DaemonSets, the x taint is not: only node a is targeted.
	if r.PodsCreated != 1 || r.UnschedulablePods != 0 {
		t.Fatalf("created=%d uns=%d", r.PodsCreated, r.UnschedulablePods)
	}
}

func TestChangesDiff(t *testing.T) {
	live := node("n1", "", "") + "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  replicas: 2\n  template:\n    spec:\n      containers:\n        - name: c\n          image: img:1\n          resources:\n            requests: {cpu: 500m}\n---\napiVersion: v1\nkind: Service\nmetadata: {name: gone}\n"
	desired := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  replicas: 4\n  template:\n    spec:\n      containers:\n        - name: c\n          image: img:2\n          resources:\n            requests: {cpu: 1}\n---\n" + deploy("new", 1, "")
	r := run(t, live, desired)
	byName := map[string]Change{}
	for _, c := range r.Changes {
		byName[c.Name] = c
	}
	web := byName["web"]
	if web.Action != "changed" {
		t.Fatalf("%+v", r.Changes)
	}
	got := map[string]FieldChange{}
	for _, f := range web.Fields {
		got[f.Field] = f
	}
	if got["replicas"].Old != "2" || got["replicas"].New != "4" || got["image c"].New != "img:2" || got["requests c cpu"].Old != "500m" {
		t.Errorf("%+v", web.Fields)
	}
	if byName["new"].Action != "created" || byName["gone"].Action != "deleted" {
		t.Errorf("%+v", r.Changes)
	}
	txt := ExplainText(r.Changes)
	if !strings.Contains(txt, "replicas: 2 -> 4") || !strings.Contains(txt, "image c: img:1 -> img:2") {
		t.Errorf("text:\n%s", txt)
	}
	for i := 1; i < len(r.Changes); i++ {
		if r.Changes[i-1].Kind > r.Changes[i].Kind {
			t.Errorf("unsorted changes")
		}
	}
}

func TestRisksSortedBySeverityThenCode(t *testing.T) {
	desired := `apiVersion: apps/v1
kind: Deployment
metadata: {name: agent}
spec:
  replicas: 1
  template:
    spec:
      hostNetwork: true
      containers:
        - name: a
          securityContext: {privileged: true}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: b}
roleRef: {name: cluster-admin}
subjects: [{kind: User, name: x}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: np}
`
	for i := 0; i < 5; i++ {
		r := run(t, oneNode, desired)
		for j := 1; j < len(r.Risks); j++ {
			a, b := r.Risks[j-1], r.Risks[j]
			if rankOf(a.Severity) > rankOf(b.Severity) || (a.Severity == b.Severity && a.Code > b.Code) {
				t.Fatalf("unsorted: %+v", r.Risks)
			}
		}
		if r.Risks[0].Severity != "critical" {
			t.Fatalf("critical first: %+v", r.Risks)
		}
	}
}
