// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import "testing"

const oneNode = `apiVersion: v1
kind: Node
metadata: {name: n1}
status:
  allocatable: {cpu: "8", memory: 32Gi}
`

func codes(r Result) map[string]bool {
	m := map[string]bool{}
	for _, x := range r.Risks {
		m[x.Code] = true
	}
	return m
}

func TestPrivilegedWorkloadIsFlagged(t *testing.T) {
	desired := `apiVersion: apps/v1
kind: Deployment
metadata: {name: agent}
spec:
  replicas: 1
  template:
    spec:
      hostNetwork: true
      volumes:
        - name: h
          hostPath: {path: /var/run}
      containers:
        - name: a
          securityContext: {privileged: true}
`
	r, err := Simulate(Request{ClusterYAML: oneNode, DesiredYAML: desired})
	if err != nil {
		t.Fatal(err)
	}
	c := codes(r)
	for _, want := range []string{"HOST_NETWORK", "HOST_PATH", "PRIVILEGED_CONTAINER"} {
		if !c[want] {
			t.Errorf("missing %s in %v", want, c)
		}
	}
	if r.SecurityRisks != 3 || r.Verdict != "REVIEW" {
		t.Errorf("security=%d verdict=%s, want 3 REVIEW", r.SecurityRisks, r.Verdict)
	}
}

func TestExistingPrivilegeIsNotReflagged(t *testing.T) {
	cluster := oneNode + `---
apiVersion: apps/v1
kind: Deployment
metadata: {name: agent}
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: a
          securityContext: {privileged: true}
`
	desired := `apiVersion: apps/v1
kind: Deployment
metadata: {name: agent}
spec:
  replicas: 2
  template:
    spec:
      containers:
        - name: a
          securityContext: {privileged: true}
`
	r, err := Simulate(Request{ClusterYAML: cluster, DesiredYAML: desired})
	if err != nil {
		t.Fatal(err)
	}
	if r.SecurityRisks != 0 {
		t.Errorf("privilege already present, got %d new security risks", r.SecurityRisks)
	}
}

func TestExposureAndRBAC(t *testing.T) {
	desired := `apiVersion: v1
kind: Service
metadata: {name: web}
spec: {type: LoadBalancer}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: wide}
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: admin-bind}
roleRef: {kind: ClusterRole, name: cluster-admin}
subjects:
  - {kind: User, name: alice}
`
	r, err := Simulate(Request{ClusterYAML: oneNode, DesiredYAML: desired})
	if err != nil {
		t.Fatal(err)
	}
	c := codes(r)
	for _, want := range []string{"SERVICE_EXPOSED", "RBAC_WILDCARD", "RBAC_CLUSTER_ADMIN"} {
		if !c[want] {
			t.Errorf("missing %s in %v", want, c)
		}
	}
	if r.ExposureChanges != 1 || r.RBACRisks != 2 {
		t.Errorf("exposure=%d rbac=%d", r.ExposureChanges, r.RBACRisks)
	}
}

func TestUnchangedServiceTypeNotFlagged(t *testing.T) {
	svc := "apiVersion: v1\nkind: Service\nmetadata: {name: web}\nspec: {type: NodePort}\n"
	r, err := Simulate(Request{ClusterYAML: oneNode + "---\n" + svc, DesiredYAML: svc})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExposureChanges != 0 {
		t.Errorf("unchanged service flagged: %d", r.ExposureChanges)
	}
}

func TestMarkdownEscapesUntrustedText(t *testing.T) {
	r := Result{Verdict: "REVIEW", RiskLevel: "MEDIUM", ClusterName: "a|b", Risks: []Risk{{"high", "X", "t", "evil <script>|\n`x`", ResourceRef{"Kind", "", "n|ame"}}}}
	md := Markdown(r)
	for _, bad := range []string{"<script>", "a|b", "n|ame"} {
		if contains(md, bad) {
			t.Errorf("markdown contains unescaped %q:\n%s", bad, md)
		}
	}
	if Rank("BLOCK") <= Rank("REVIEW") || Rank("REVIEW") <= Rank("SAFE") {
		t.Error("rank order")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
