// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

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

func TestParseIndentlessSequence(t *testing.T) {
	src := `kind: Deployment
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
            cpu: 500m
      - name: side
        image: x
  paused: false
`
	d, err := ParseDocuments(src)
	if err != nil {
		t.Fatal(err)
	}
	cs := asSlice(get(d[0], "spec", "template", "spec", "containers"))
	if len(cs) != 2 {
		t.Fatalf("got %d containers", len(cs))
	}
	if get(d[0], "spec", "paused") != false {
		t.Fatal("key after indentless sequence lost")
	}
	if r := resourceReq(podSpecFor(d[0])); r.cpu != 0.5 {
		t.Fatalf("resources %#v", r)
	}
}

func TestFlowCollectionsWithPlainScalars(t *testing.T) {
	docs, err := ParseDocuments("kind: Service\nmetadata: {name: web, labels: {app: a, tier: \"x y\"}}\nspec: {type: LoadBalancer, ports: [80, 443], on: true}\nlist:\n  - {kind: User, name: alice}\n")
	if err != nil {
		t.Fatal(err)
	}
	d := docs[0]
	if str(get(d, "metadata", "name")) != "web" || str(get(d, "metadata", "labels", "tier")) != "x y" {
		t.Fatalf("metadata: %#v", d["metadata"])
	}
	if str(get(d, "spec", "type")) != "LoadBalancer" || get(d, "spec", "on") != true {
		t.Fatalf("spec: %#v", d["spec"])
	}
	if ports := asSlice(get(d, "spec", "ports")); len(ports) != 2 || intValue(ports[1], 0) != 443 {
		t.Fatalf("ports: %#v", get(d, "spec", "ports"))
	}
	if li := asSlice(d["list"]); len(li) != 1 || str(asMap(li[0])["name"]) != "alice" {
		t.Fatalf("list: %#v", d["list"])
	}
}

func TestFlowMalformedAndDeepInputStaysAString(t *testing.T) {
	for _, bad := range []string{"{a: 1", "{a 1}", "[1, 2", "{a: }", "{: 1}"} {
		if v, ok := parseFlow(bad); ok {
			t.Errorf("%q parsed as %#v", bad, v)
		}
	}
	deep := ""
	for i := 0; i < 5000; i++ {
		deep += "["
	}
	if _, ok := parseFlow(deep); ok { // must terminate, not overflow the stack
		t.Error("deeply nested input accepted")
	}
}
