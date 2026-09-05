package sim

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func intValue(v any, fallback int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if n, e := strconv.Atoi(x); e == nil {
			return n
		}
	}
	return fallback
}
func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm := asMap(cur)
		if mm == nil {
			return nil
		}
		cur = mm[p]
	}
	return cur
}
func metadata(m map[string]any) (kind, ns, name string) {
	kind = str(m["kind"])
	ns = str(get(m, "metadata", "namespace"))
	if ns == "" {
		ns = "default"
	}
	name = str(get(m, "metadata", "name"))
	return
}
func keyFor(m map[string]any) string {
	k, n, s := metadata(m)
	return strings.ToLower(k) + "/" + n + "/" + s
}
func canonical(v any) string { b, _ := json.Marshal(v); return string(b) }

func parseCPU(v any) float64 {
	s := strings.TrimSpace(str(v))
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "m") {
		f, _ := strconv.ParseFloat(strings.TrimSuffix(s, "m"), 64)
		return f / 1000
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
func parseBytes(v any) int64 {
	s := strings.TrimSpace(str(v))
	if s == "" {
		return 0
	}
	units := []struct {
		suffix string
		mult   float64
	}{{"Ki", 1024}, {"Mi", math.Pow(1024, 2)}, {"Gi", math.Pow(1024, 3)}, {"Ti", math.Pow(1024, 4)}, {"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			return int64(f * u.mult)
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f)
}

type resources struct {
	cpu float64
	mem int64
	gpu int64
}

func resourceReq(containerSpec map[string]any) resources {
	r := resources{}
	for _, c := range asSlice(containerSpec["containers"]) {
		cm := asMap(c)
		req := asMap(get(cm, "resources", "requests"))
		if req == nil {
			continue
		}
		r.cpu += parseCPU(req["cpu"])
		r.mem += parseBytes(req["memory"])
		r.gpu += int64(intValue(req["nvidia.com/gpu"], 0))
	}
	for _, c := range asSlice(containerSpec["initContainers"]) {
		cm := asMap(c)
		req := asMap(get(cm, "resources", "requests"))
		if req == nil {
			continue
		}
		r.cpu = math.Max(r.cpu, parseCPU(req["cpu"]))
		r.mem = max64(r.mem, parseBytes(req["memory"]))
		r.gpu = max64(r.gpu, int64(intValue(req["nvidia.com/gpu"], 0)))
	}
	return r
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func podSpecFor(obj map[string]any) map[string]any {
	switch str(obj["kind"]) {
	case "Pod":
		return asMap(obj["spec"])
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		return asMap(get(obj, "spec", "template", "spec"))
	case "CronJob":
		return asMap(get(obj, "spec", "jobTemplate", "spec", "template", "spec"))
	case "VirtualMachine":
		return vmAsPodSpec(obj)
	}
	return nil
}
func vmAsPodSpec(obj map[string]any) map[string]any {
	vm := asMap(get(obj, "spec", "template", "spec"))
	if vm == nil {
		return nil
	}
	domain := asMap(vm["domain"])
	if domain == nil {
		return nil
	}
	res := asMap(domain["resources"])
	requests := map[string]any{}
	if rr := asMap(res["requests"]); rr != nil {
		for k, v := range rr {
			requests[k] = v
		}
	}
	if cpu := asMap(domain["cpu"]); cpu != nil {
		sockets := intValue(cpu["sockets"], 1)
		cores := intValue(cpu["cores"], 1)
		threads := intValue(cpu["threads"], 1)
		requests["cpu"] = fmt.Sprint(sockets * cores * threads)
	}
	if mem := asMap(domain["memory"]); mem != nil && requests["memory"] == nil {
		requests["memory"] = mem["guest"]
	}
	gpuCount := 0
	for range asSlice(get(domain, "devices", "gpus")) {
		gpuCount++
	}
	if gpuCount > 0 {
		requests["nvidia.com/gpu"] = gpuCount
	}
	return map[string]any{"containers": []any{map[string]any{"name": "virtual-machine", "resources": map[string]any{"requests": requests}}}}
}
func replicasFor(obj map[string]any, nodeCount int) int {
	switch str(obj["kind"]) {
	case "Deployment", "StatefulSet", "ReplicaSet":
		return intValue(get(obj, "spec", "replicas"), 1)
	case "DaemonSet":
		return nodeCount
	case "Pod", "Job", "VirtualMachine":
		return 1
	case "CronJob":
		return 0
	}
	return 0
}
