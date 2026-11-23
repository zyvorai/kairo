// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zyvorai/kairo/internal/sim"
)

const specPath = "../../docs/openapi.yaml"

func readSpec(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("docs/openapi.yaml is required: %v", err)
	}
	return string(b)
}

var (
	pathLine   = regexp.MustCompile(`^  (/[^:\s]*):\s*$`)
	methodLine = regexp.MustCompile(`^    (get|post|put|patch|delete|head|options):\s*$`)
)

// documentedOperations returns "METHOD /path" for every operation under paths:.
func documentedOperations(spec string) map[string]bool {
	ops := map[string]bool{}
	inPaths, cur := false, ""
	for _, ln := range strings.Split(spec, "\n") {
		if strings.HasPrefix(ln, "paths:") {
			inPaths = true
			continue
		}
		if inPaths && ln != "" && !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "#") {
			break // next top-level key
		}
		if m := pathLine.FindStringSubmatch(ln); m != nil {
			cur = m[1]
		} else if m := methodLine.FindStringSubmatch(ln); m != nil && cur != "" {
			ops[strings.ToUpper(m[1])+" "+cur] = true
		}
	}
	return ops
}

// schemaProperties returns property names per schema under components.schemas.
func schemaProperties(spec string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	inSchemas, name, inProps := false, "", false
	nameLine := regexp.MustCompile(`^    ([A-Za-z0-9]+):\s*$`)
	propLine := regexp.MustCompile(`^        ([A-Za-z0-9_]+):`)
	for _, ln := range strings.Split(spec, "\n") {
		switch {
		case ln == "  schemas:":
			inSchemas = true
		case !inSchemas:
		case nameLine.MatchString(ln):
			name = nameLine.FindStringSubmatch(ln)[1]
			out[name] = map[string]bool{}
			inProps = false
		case ln == "      properties:":
			inProps = true
		case strings.HasPrefix(ln, "      ") && !strings.HasPrefix(ln, "       "):
			inProps = false // another key at schema-body level
		case inProps && propLine.MatchString(ln):
			out[name][propLine.FindStringSubmatch(ln)[1]] = true
		}
	}
	return out
}

func TestEveryRegisteredRouteIsDocumented(t *testing.T) {
	ops := documentedOperations(readSpec(t))
	routes := Server{Web: nil}.Routes()
	if len(routes) < 10 {
		t.Fatalf("expected at least 10 routes, got %v", routes)
	}
	registered := map[string]bool{}
	for _, r := range routes {
		k := r.Method + " " + r.Path
		registered[k] = true
		if !ops[k] {
			t.Errorf("route %s is registered but missing from docs/openapi.yaml", k)
		}
	}
	for k := range ops {
		if !registered[k] {
			t.Errorf("docs/openapi.yaml documents %s but no such route is registered", k)
		}
	}
}

func TestOpenAPIParserSelfCheck(t *testing.T) {
	ops := documentedOperations("paths:\n  /a:\n    get:\n    post:\n  /b/c:\n    delete:\ncomponents:\n  /x:\n    get:\n")
	got := []string{}
	for k := range ops {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "DELETE /b/c,GET /a,POST /a" {
		t.Fatalf("got %v", got)
	}
}

func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}

func TestSchemasMatchGoTypes(t *testing.T) {
	props := schemaProperties(readSpec(t))
	types := map[string]reflect.Type{
		"SimulateRequest":    reflect.TypeOf(sim.Request{}),
		"Result":             reflect.TypeOf(sim.Result{}),
		"Risk":               reflect.TypeOf(sim.Risk{}),
		"ResourceRef":        reflect.TypeOf(sim.ResourceRef{}),
		"NodeResult":         reflect.TypeOf(sim.NodeResult{}),
		"Change":             reflect.TypeOf(sim.Change{}),
		"FieldChange":        reflect.TypeOf(sim.FieldChange{}),
		"DrainRequest":       reflect.TypeOf(sim.DrainRequest{}),
		"DrainResult":        reflect.TypeOf(sim.DrainResult{}),
		"DrainPod":           reflect.TypeOf(sim.DrainPod{}),
		"ConsolidateRequest": reflect.TypeOf(sim.ConsolidateRequest{}),
		"ConsolidateResult":  reflect.TypeOf(sim.ConsolidateResult{}),
		"ConsolidateNode":    reflect.TypeOf(sim.ConsolidateNode{}),
		"ConsolidateMove":    reflect.TypeOf(sim.ConsolidateMove{}),
		"Utilization":        reflect.TypeOf(sim.Utilization{}),
		"Prices":             reflect.TypeOf(sim.Prices{}),
		"CostEstimate":       reflect.TypeOf(sim.CostEstimate{}),
		"DisruptionRequest":  reflect.TypeOf(sim.DisruptionRequest{}),
		"DisruptionResult":   reflect.TypeOf(sim.DisruptionResult{}),
		"DisruptionNode":     reflect.TypeOf(sim.DisruptionNode{}),
		"DisruptionFactor":   reflect.TypeOf(sim.DisruptionFactor{}),
		"DisruptionWeight":   reflect.TypeOf(sim.DisruptionWeight{}),
		"UsageSeries":        reflect.TypeOf(sim.UsageSeries{}),
		"UsageData":          reflect.TypeOf(sim.UsageData{}),
		"RightsizeOptions":   reflect.TypeOf(sim.RightsizeOptions{}),
		"RightsizeRequest":   reflect.TypeOf(sim.RightsizeRequest{}),
		"EffectiveOptions":   reflect.TypeOf(sim.EffectiveOptions{}),
		"ResourceRec":        reflect.TypeOf(sim.ResourceRec{}),
		"RightsizeContainer": reflect.TypeOf(sim.RightsizeContainer{}),
		"RightsizeScenario":  reflect.TypeOf(sim.RightsizeScenario{}),
		"RightsizeResult":    reflect.TypeOf(sim.RightsizeResult{}),
		"ResilienceRequest":  reflect.TypeOf(sim.ResilienceRequest{}),
		"ResilienceResult":   reflect.TypeOf(sim.ResilienceResult{}),
		"ResilienceScenario": reflect.TypeOf(sim.ResilienceScenario{}),
		"CapacityDiversity":  reflect.TypeOf(sim.CapacityDiversity{}),
	}
	for name, typ := range types {
		doc, ok := props[name]
		if !ok {
			t.Errorf("schema %s is missing from docs/openapi.yaml", name)
			continue
		}
		fields := jsonFields(typ)
		for f := range fields {
			if !doc[f] {
				t.Errorf("schema %s lacks property %q present in %s", name, f, typ)
			}
		}
		for f := range doc {
			if !fields[f] {
				t.Errorf("schema %s documents property %q that %s does not have", name, f, typ)
			}
		}
	}
}

func TestOpenAPIRefsResolve(t *testing.T) {
	spec := readSpec(t)
	props := schemaProperties(spec)
	refs := regexp.MustCompile(`\$ref: "#/components/(schemas|responses)/([A-Za-z0-9]+)"`).FindAllStringSubmatch(spec, -1)
	if len(refs) == 0 {
		t.Fatal("no $ref found")
	}
	for _, m := range refs {
		switch m[1] {
		case "schemas":
			if _, ok := props[m[2]]; !ok {
				t.Errorf("dangling $ref to schema %s", m[2])
			}
		case "responses":
			if !strings.Contains(spec, "\n    "+m[2]+":\n      description:") {
				t.Errorf("dangling $ref to response %s", m[2])
			}
		}
	}
	if !strings.Contains(spec, "\nopenapi: 3.0.3\n") {
		t.Error("missing openapi version")
	}
}

func TestConsolidateEndpoint(t *testing.T) {
	cluster := `kind: Node\nmetadata: {name: a}\nstatus: {allocatable: {cpu: \"4\", memory: 8Gi}}\n---\nkind: Node\nmetadata: {name: b}\nstatus: {allocatable: {cpu: \"4\", memory: 8Gi}}`
	cases := []struct {
		name, body string
		want       int
		contains   string
	}{
		{"invalid json", `{`, 400, ""},
		{"missing cluster", `{}`, 400, "clusterYaml is required"},
		{"no nodes", `{"clusterYaml":"kind: Service\nmetadata: {name: s}"}`, 422, "no Node"},
		{"negative price", `{"clusterYaml":"` + cluster + `","prices":{"nodeHourPrice":-1}}`, 422, "nodeHourPrice"},
		{"ok", `{"clusterYaml":"` + cluster + `"}`, 200, `"nodesAfter":1`},
		{"ok with prices", `{"clusterYaml":"` + cluster + `","prices":{"nodeHourPrice":1,"currency":"EUR"}}`, 200, `"monthlyDelta":-730`},
		{"too large", `{"clusterYaml":"` + strings.Repeat("a", 5<<20) + `"}`, 413, ""},
	}
	for _, c := range cases {
		w := serve(t, "POST", "/api/v1/consolidate", c.body)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.contains) {
			t.Errorf("%s: got %d %s", c.name, w.Code, w.Body)
		}
	}
	if w := serve(t, "GET", "/api/v1/consolidate", ""); w.Code != 405 && w.Code != 404 {
		t.Errorf("GET: %d", w.Code)
	}
}

func TestSimulateAcceptsPrices(t *testing.T) {
	body := `{"clusterYaml":"kind: Node\nmetadata: {name: n}\nstatus: {allocatable: {cpu: \"8\", memory: 8Gi}}","desiredYaml":"kind: Service\nmetadata: {name: s}","prices":{"nodeHourPrice":0.2}}`
	if w := serve(t, "POST", "/api/v1/simulate", body); w.Code != 200 || !strings.Contains(w.Body.String(), `"cost":{"estimate":true`) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := serve(t, "POST", "/api/v1/simulate", strings.Replace(body, "0.2", "-3", 1)); w.Code != 422 {
		t.Errorf("negative price: %d", w.Code)
	}
}

func TestDisruptionEndpoint(t *testing.T) {
	cluster := `kind: Node\nmetadata: {name: a}\nstatus: {allocatable: {cpu: \"4\", memory: 8Gi}}`
	cases := []struct {
		name, body string
		want       int
		contains   string
	}{
		{"invalid json", `{`, 400, ""},
		{"missing cluster", `{}`, 400, "clusterYaml is required"},
		{"no nodes", `{"clusterYaml":"kind: Service\nmetadata: {name: s}"}`, 422, "no Node"},
		{"ok", `{"clusterYaml":"` + cluster + `"}`, 200, `"weights"`},
		{"too large", `{"clusterYaml":"` + strings.Repeat("a", 5<<20) + `"}`, 413, ""},
	}
	for _, c := range cases {
		w := serve(t, "POST", "/api/v1/disruption", c.body)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.contains) {
			t.Errorf("%s: got %d %s", c.name, w.Code, w.Body)
		}
	}
}

func TestRightsizeEndpoint(t *testing.T) {
	cluster := `kind: Node\nmetadata: {name: a}\nstatus: {allocatable: {cpu: \"4\", memory: 8Gi}}\n---\nkind: Pod\nmetadata: {name: w-1, ownerReferences: [{kind: ReplicaSet, name: w-abc}]}\nspec: {nodeName: a, containers: [{name: c, resources: {requests: {cpu: \"1\", memory: 1Gi}}}]}`
	samples := strings.TrimSuffix(strings.Repeat("0.2,", 40), ",")
	usage := `"usage":{"series":[{"workload":"w","container":"c","cpuCores":[` + samples + `]}]}`
	cases := []struct {
		name, body string
		want       int
		contains   string
	}{
		{"invalid json", `{`, 400, ""},
		{"missing usage", `{"clusterYaml":"` + cluster + `"}`, 400, "usage.series are required"},
		{"nan-like", `{"clusterYaml":"` + cluster + `","usage":{"series":[{"workload":"w","container":"c","cpuCores":[-1]}]}}`, 422, "finite non-negative"},
		{"bad option", `{"clusterYaml":"` + cluster + `",` + usage + `,"options":{"cpuPercentile":7}}`, 422, "cpuPercentile"},
		{"wrong type", `{"clusterYaml":"` + cluster + `","usage":{"series":[{"workload":"w","container":"c","cpuCores":"x"}]}}`, 400, "invalid JSON"},
		{"ok", `{"clusterYaml":"` + cluster + `",` + usage + `,"prices":{"nodeHourPrice":1}}`, 200, `"scenarios"`},
		{"too large", `{"clusterYaml":"` + strings.Repeat("a", 5<<20) + `"}`, 413, ""},
	}
	for _, c := range cases {
		w := serve(t, "POST", "/api/v1/rightsize", c.body)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.contains) {
			t.Errorf("%s: got %d %s", c.name, w.Code, w.Body)
		}
	}
}

func TestResilienceEndpoint(t *testing.T) {
	cluster := `kind: Node\nmetadata: {name: a, labels: {karpenter.sh/capacity-type: spot}}\nstatus: {allocatable: {cpu: \"4\", memory: 8Gi}}`
	cases := []struct {
		name, body string
		want       int
		contains   string
	}{
		{"invalid json", `{`, 400, ""},
		{"missing cluster", `{}`, 400, "clusterYaml is required"},
		{"no nodes", `{"clusterYaml":"kind: Service\nmetadata: {name: s}"}`, 422, "no Node"},
		{"ok", `{"clusterYaml":"` + cluster + `"}`, 200, `"scenarios"`},
		{"too large", `{"clusterYaml":"` + strings.Repeat("a", 5<<20) + `"}`, 413, ""},
	}
	for _, c := range cases {
		w := serve(t, "POST", "/api/v1/resilience", c.body)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.contains) {
			t.Errorf("%s: got %d %s", c.name, w.Code, w.Body)
		}
	}
}
