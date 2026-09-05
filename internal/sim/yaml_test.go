package sim

import "testing"

func TestParseKubernetesYAML(t *testing.T) {
	src := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: api
          image: example/api:v2
          resources:
            requests:
              cpu: 500m
              memory: 1Gi
`
	d, err := ParseDocuments(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 {
		t.Fatalf("got %d docs", len(d))
	}
	if intValue(get(d[0], "spec", "replicas"), 0) != 3 {
		t.Fatal("replicas not parsed")
	}
	spec := podSpecFor(d[0])
	r := resourceReq(spec)
	if r.cpu != 0.5 || r.mem != 1073741824 {
		t.Fatalf("resources %#v", r)
	}
}
