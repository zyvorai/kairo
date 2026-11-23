// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func lrYAML(ns, limits string) string {
	return fmt.Sprintf("---\napiVersion: v1\nkind: LimitRange\nmetadata: {name: lr, namespace: %s}\nspec:\n  limits: %s\n", ns, limits)
}

func quotaYAML(ns, hard string) string {
	return fmt.Sprintf("---\napiVersion: v1\nkind: ResourceQuota\nmetadata: {name: q, namespace: %s}\nspec:\n  hard: %s\n", ns, hard)
}

func teamDeploy(ns string, replicas int, resources string) string {
	return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata: {name: app, namespace: %s}
spec:
  replicas: %d
  template:
    spec:
      containers:
        - name: c
          image: app:1
          resources: %s
`, ns, replicas, resources)
}

func admCodes(r Result) string {
	var c []string
	for _, x := range r.Risks {
		if strings.HasPrefix(x.Code, "ADMISSION_") {
			c = append(c, x.Code)
		}
	}
	sort.Strings(c)
	return strings.Join(c, ",")
}

func TestAdmissionChecks(t *testing.T) {
	nodes := nodeYAML("n1", "32", "128Gi")
	cases := []struct {
		name            string
		cluster, wanted string
		desired         string
		want            string // sorted ADMISSION_* codes
		detail          string // substring expected in some admission risk detail
	}{
		{"limit above LimitRange max", lrYAML("team", "[{type: Container, max: {cpu: \"2\"}}]"), "", teamDeploy("team", 1, "{requests: {cpu: \"1\"}, limits: {cpu: \"4\"}}"), "ADMISSION_LIMITRANGE_MAX", "limit 4cpu exceeds"},
		{"limit within max", lrYAML("team", "[{type: Container, max: {cpu: \"2\"}}]"), "", teamDeploy("team", 1, "{requests: {cpu: \"1\"}, limits: {cpu: \"2\"}}"), "", ""},
		{"no limit with max and no default", lrYAML("team", "[{type: Container, max: {memory: 1Gi}}]"), "", teamDeploy("team", 1, "{requests: {memory: 256Mi}}"), "ADMISSION_LIMITRANGE_MAX", "no memory limit"},
		{"default limit satisfies max", lrYAML("team", "[{type: Container, max: {memory: 1Gi}, default: {memory: 512Mi}}]"), "", teamDeploy("team", 1, "{requests: {memory: 256Mi}}"), "", ""},
		{"default limit above max is rejected", lrYAML("team", "[{type: Container, max: {memory: 1Gi}, default: {memory: 2Gi}}]"), "", teamDeploy("team", 1, "{}"), "ADMISSION_LIMITRANGE_MAX", "limit 2.0 GiB exceeds"},
		{"request below min", lrYAML("team", "[{type: Container, min: {cpu: 500m}}]"), "", teamDeploy("team", 1, "{requests: {cpu: 100m}}"), "ADMISSION_LIMITRANGE_MIN", "below the LimitRange"},
		{"limit/request ratio", lrYAML("team", "[{type: Container, maxLimitRequestRatio: {cpu: \"2\"}}]"), "", teamDeploy("team", 1, "{requests: {cpu: 100m}, limits: {cpu: \"1\"}}"), "ADMISSION_LIMITRANGE_RATIO", "ratio 10.00"},
		{"pod-level max sums containers", lrYAML("team", "[{type: Pod, max: {cpu: \"1\"}}]"), "", `---
apiVersion: apps/v1
kind: Deployment
metadata: {name: app, namespace: team}
spec:
  replicas: 1
  template:
    spec:
      containers:
        - {name: a, resources: {requests: {cpu: 600m}, limits: {cpu: 600m}}}
        - {name: b, resources: {requests: {cpu: 600m}, limits: {cpu: 600m}}}
`, "ADMISSION_LIMITRANGE_MAX", "pod cpu limit 1.2cpu"},
		{"LimitRange in other namespace ignored", lrYAML("other", "[{type: Container, max: {cpu: \"1\"}}]"), "", teamDeploy("team", 1, "{limits: {cpu: \"4\"}}"), "", ""},
		{"PVC above max", lrYAML("team", "[{type: PersistentVolumeClaim, max: {storage: 10Gi}}]"), "", "---\napiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: d, namespace: team}\nspec:\n  resources:\n    requests: {storage: 50Gi}\n", "ADMISSION_LIMITRANGE_MAX", "storage 50.0 GiB"},
		{"quota exceeded", quotaYAML("team", "{requests.cpu: \"4\"}"), "", teamDeploy("team", 5, "{requests: {cpu: \"1\"}}"), "ADMISSION_QUOTA_EXCEEDED", "requests.cpu in namespace team is 5cpu"},
		{"quota fits", quotaYAML("team", "{requests.cpu: \"4\"}"), "", teamDeploy("team", 4, "{requests: {cpu: \"1\"}}"), "", ""},
		{"bare cpu key aliases requests.cpu", quotaYAML("team", "{cpu: \"2\"}"), "", teamDeploy("team", 3, "{requests: {cpu: \"1\"}}"), "ADMISSION_QUOTA_EXCEEDED", ""},
		{"pods count quota", quotaYAML("team", "{pods: \"2\"}"), "", teamDeploy("team", 3, "{requests: {cpu: 10m}}"), "ADMISSION_QUOTA_EXCEEDED", "pods in namespace team is 3"},
		{"limits.memory quota", quotaYAML("team", "{limits.memory: 1Gi}"), "", teamDeploy("team", 2, "{requests: {memory: 256Mi}, limits: {memory: 768Mi}}"), "ADMISSION_QUOTA_EXCEEDED", "limits.memory"},
		{"quota in other namespace ignored", quotaYAML("other", "{requests.cpu: \"1\"}"), "", teamDeploy("team", 5, "{requests: {cpu: \"1\"}}"), "", ""},
		{"quota without requests", quotaYAML("team", "{requests.cpu: \"4\"}"), "", teamDeploy("team", 1, "{}"), "ADMISSION_QUOTA_NO_REQUESTS", "requests.cpu"},
		{"LimitRange defaultRequest satisfies quota", quotaYAML("team", "{requests.cpu: \"4\"}") + lrYAML("team", "[{type: Container, defaultRequest: {cpu: 500m}}]"), "", teamDeploy("team", 2, "{}"), "", ""},
		{"LimitRange defaultRequest counts toward quota", quotaYAML("team", "{requests.cpu: \"4\"}") + lrYAML("team", "[{type: Container, defaultRequest: {cpu: \"2\"}}]"), "", teamDeploy("team", 3, "{}"), "ADMISSION_QUOTA_EXCEEDED", "6cpu"},
		{"explicit limit defaults the request", quotaYAML("team", "{requests.cpu: \"1\"}"), "", teamDeploy("team", 2, "{limits: {cpu: \"1\"}}"), "ADMISSION_QUOTA_EXCEEDED", "2cpu"},
		{"scoped quota is not modelled", "---\napiVersion: v1\nkind: ResourceQuota\nmetadata: {name: q, namespace: team}\nspec:\n  scopes: [BestEffort]\n  hard: {pods: \"1\"}\n", "", teamDeploy("team", 3, "{}"), "", ""},
		{"already over quota and not growing is not flagged", quotaYAML("team", "{requests.cpu: \"2\"}") + teamDeploy("team", 5, "{requests: {cpu: \"1\"}}"), "", teamDeploy("team", 4, "{requests: {cpu: \"1\"}}"), "", ""},
		{"growing while over quota is flagged", quotaYAML("team", "{requests.cpu: \"2\"}") + teamDeploy("team", 5, "{requests: {cpu: \"1\"}}"), "", teamDeploy("team", 6, "{requests: {cpu: \"1\"}}"), "ADMISSION_QUOTA_EXCEEDED", ""},
		{"quota in desired replaces live and is checked", quotaYAML("team", "{requests.cpu: \"10\"}") + teamDeploy("team", 3, "{requests: {cpu: \"1\"}}"), "", quotaYAML("team", "{requests.cpu: \"2\"}") + teamDeploy("team", 3, "{requests: {cpu: \"1\"}}"), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Simulate(Request{ClusterYAML: nodes + c.cluster, DesiredYAML: c.desired})
			if err != nil {
				t.Fatal(err)
			}
			if got := admCodes(r); got != c.want {
				t.Fatalf("codes = %q, want %q\n%+v", got, c.want, r.Risks)
			}
			if c.want != "" && r.Verdict != "BLOCK" {
				t.Errorf("verdict = %s, want BLOCK", r.Verdict)
			}
			if c.detail != "" {
				found := false
				for _, x := range r.Risks {
					found = found || strings.Contains(x.Detail, c.detail)
				}
				if !found {
					t.Errorf("no risk detail contains %q: %+v", c.detail, r.Risks)
				}
			}
		})
	}
}

func TestAdmissionCountsAndDeterminism(t *testing.T) {
	cluster := nodeYAML("n1", "32", "128Gi") + quotaYAML("team", "{requests.cpu: \"2\"}") + lrYAML("team", "[{type: Container, max: {cpu: \"1\"}}]")
	desired := teamDeploy("team", 5, "{requests: {cpu: \"1\"}, limits: {cpu: \"2\"}}")
	a, _ := Simulate(Request{ClusterYAML: cluster, DesiredYAML: desired})
	b, _ := Simulate(Request{ClusterYAML: cluster, DesiredYAML: desired})
	if a.QuotaRisks != 1 || a.LimitRangeRisks != 1 {
		t.Errorf("quotaRisks=%d limitRangeRisks=%d", a.QuotaRisks, a.LimitRangeRisks)
	}
	if fmt.Sprint(a.Risks) != fmt.Sprint(b.Risks) {
		t.Error("risks not deterministic")
	}
}

// The old QUOTA_PRESSURE check compared cluster-wide totals with one quota.
func TestQuotaIsNamespaceScoped(t *testing.T) {
	cluster := nodeYAML("n1", "64", "256Gi")
	desired := quotaYAML("small", "{requests.cpu: \"2\"}") + teamDeploy("big", 10, "{requests: {cpu: \"1\"}}")
	r, _ := Simulate(Request{ClusterYAML: cluster, DesiredYAML: desired})
	if admCodes(r) != "" || r.QuotaRisks != 0 {
		t.Fatalf("unrelated namespace flagged: %+v", r.Risks)
	}
}

// The resource delta compares the changed objects with what they replace, not
// with the whole cluster.
func TestResourceDeltaOnlyCountsReplacedObjects(t *testing.T) {
	cluster := nodeYAML("n1", "32", "128Gi") + teamDeploy("a", 10, "{requests: {cpu: \"1\", memory: 1Gi}}") + teamDeploy("b", 4, "{requests: {cpu: \"1\", memory: 1Gi}}")
	r, _ := Simulate(Request{ClusterYAML: cluster, DesiredYAML: teamDeploy("b", 6, "{requests: {cpu: \"1\", memory: 1Gi}}")})
	if r.CPUDeltaCores != 2 || r.MemoryDeltaBytes != 2<<30 {
		t.Fatalf("delta = %v cores %d bytes, want 2 cores and 2GiB", r.CPUDeltaCores, r.MemoryDeltaBytes)
	}
}
