// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"sort"
)

// collectHPAs indexes HorizontalPodAutoscalers by "kind/namespace/target".
// An HPA in the desired set replaces a live one that targets the same object.
func collectHPAs(cluster, desired []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, set := range [][]map[string]any{cluster, desired} {
		for _, o := range set {
			if str(o["kind"]) != "HorizontalPodAutoscaler" {
				continue
			}
			_, ns, _ := metadata(o)
			ref := asMap(get(o, "spec", "scaleTargetRef"))
			if ref == nil {
				continue
			}
			out[hpaKey(str(ref["kind"]), ns, str(ref["name"]))] = o
		}
	}
	return out
}

func hpaKey(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

func findHPA(hpas map[string]map[string]any, obj map[string]any) map[string]any {
	kind, ns, name := metadata(obj)
	if kind != "Deployment" && kind != "StatefulSet" {
		return nil
	}
	return hpas[hpaKey(kind, ns, name)]
}

// hpaRisks evaluates an autoscaler's bounds against the workload and the
// nodes left after the baseline replicas were placed. The scale-up headroom is
// simulated on a copy so it never consumes capacity for later objects.
func hpaRisks(hpa, obj map[string]any, replicas int, nodes []*nodeState, rq resources, pl placement) []Risk {
	kind, ns, name := metadata(obj)
	ref := ResourceRef{Kind: kind, Namespace: ns, Name: name}
	minR := intValue(get(hpa, "spec", "minReplicas"), 1)
	maxR := intValue(get(hpa, "spec", "maxReplicas"), 0)
	if maxR > maxReplicas {
		maxR = maxReplicas // bound the simulation; the cap is far beyond any real cluster
	}
	hname := str(get(hpa, "metadata", "name"))
	var out []Risk
	if maxR > 0 && minR > maxR {
		out = append(out, Risk{"medium", "HPA_INVALID_BOUNDS", "HPA minReplicas exceeds maxReplicas",
			fmt.Sprintf("HorizontalPodAutoscaler %s has minReplicas %d above maxReplicas %d.", hname, minR, maxR), ref})
	} else if minR > replicas {
		out = append(out, Risk{"medium", "HPA_MIN_EXCEEDS_REPLICAS", "HPA minReplicas exceeds workload replicas",
			fmt.Sprintf("HorizontalPodAutoscaler %s requires at least %d replicas but the workload declares %d; the HPA will scale it up immediately.", hname, minR, replicas), ref})
	}
	worst := maxR
	if minR > worst && minR <= maxReplicas {
		worst = minR
	}
	if extra := worst - replicas; extra > 0 {
		if uns := schedule(cloneNodes(nodes), rq, extra, pl); uns > 0 {
			out = append(out, Risk{"high", "HPA_MAX_UNSCHEDULABLE", "HPA maxReplicas cannot be scheduled",
				fmt.Sprintf("At the HPA maximum of %d replicas, %d pods cannot fit on the current node capacity.", worst, uns), ref})
		}
	}
	return out
}

var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

func rankOf(s string) int {
	if r, ok := severityRank[s]; ok {
		return r
	}
	return len(severityRank)
}

// sortRisks orders risks by severity (critical first), then code, then
// resource, so output is identical across runs.
func sortRisks(r []Risk) {
	sort.SliceStable(r, func(i, j int) bool {
		a, b := r[i], r[j]
		if ra, rb := rankOf(a.Severity), rankOf(b.Severity); ra != rb {
			return ra < rb
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Resource.Kind != b.Resource.Kind {
			return a.Resource.Kind < b.Resource.Kind
		}
		if a.Resource.Namespace != b.Resource.Namespace {
			return a.Resource.Namespace < b.Resource.Namespace
		}
		return a.Resource.Name < b.Resource.Name
	})
}
