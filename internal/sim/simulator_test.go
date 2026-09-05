package sim

import "testing"

func TestBlocksUnschedulableAndPVCShrink(t *testing.T) {
	cluster := `apiVersion: v1
kind: Node
metadata: {"name":"n1"}
status:
  allocatable:
    cpu: "2"
    memory: 4Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
spec:
  resources:
    requests:
      storage: 100Gi
`
	desired := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 2
  template:
    spec:
      containers:
        - name: api
          resources:
            requests:
              cpu: "2"
              memory: 3Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
spec:
  resources:
    requests:
      storage: 50Gi
`
	r, err := Simulate(Request{cluster, desired})
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "BLOCK" {
		t.Fatalf("verdict=%s", r.Verdict)
	}
	if r.UnschedulablePods < 1 {
		t.Fatal("expected unschedulable")
	}
	if r.PVCDestructiveRisks != 1 {
		t.Fatalf("pvc risks=%d", r.PVCDestructiveRisks)
	}
}
