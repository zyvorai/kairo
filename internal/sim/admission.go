// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Admission checks: would the API server (LimitRanger and ResourceQuota
// admission plugins) reject the pods or claims a changed object produces?
//
// Not modelled: quota scopes/scopeSelector (such quotas are skipped), count/*
// object-count quotas, ephemeral-storage and extended resources other than
// nvidia.com/gpu, rollout surge pods counting against quota, and LimitRange
// types other than Container, Pod and PersistentVolumeClaim.

type admissionCounts struct{ quota, limitRange int }

// effContainer is a container's requests and limits after defaulting.
type effContainer struct {
	req, lim map[string]float64
}

type limitItem struct {
	typ                             string
	max, min, def, defReq, maxRatio map[string]float64
}

type quotaItem struct {
	ns, name string
	hard     map[string]float64
}

var admissionResources = []string{"cpu", "memory"}

func qtyFor(res string, v any) float64 {
	if res == "cpu" {
		return parseCPU(v)
	}
	return float64(parseBytes(v))
}

func qtyMap(v any, resources []string) map[string]float64 {
	m := asMap(v)
	out := map[string]float64{}
	for _, r := range resources {
		if x, ok := m[r]; ok {
			out[r] = qtyFor(r, x)
		}
	}
	return out
}

func fmtQty(res string, v float64) string {
	switch res {
	case "cpu", "requests.cpu", "limits.cpu":
		return fmt.Sprintf("%gcpu", math.Round(v*1000)/1000)
	case "pods", "requests.nvidia.com/gpu":
		return fmt.Sprintf("%g", v)
	}
	return humanBytes(int64(v))
}

func collectLimitRanges(objs map[string]map[string]any) map[string][]limitItem {
	keys := make([]string, 0, len(objs))
	for k, o := range objs {
		if str(o["kind"]) == "LimitRange" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := map[string][]limitItem{}
	for _, k := range keys {
		o := objs[k]
		_, ns, _ := metadata(o)
		for _, l := range asSlice(get(o, "spec", "limits")) {
			lm := asMap(l)
			res := admissionResources
			if str(lm["type"]) == "PersistentVolumeClaim" {
				res = []string{"storage"}
			}
			out[ns] = append(out[ns], limitItem{str(lm["type"]), qtyMap(lm["max"], res), qtyMap(lm["min"], res), qtyMap(lm["default"], res), qtyMap(lm["defaultRequest"], res), qtyMap(lm["maxLimitRequestRatio"], res)})
		}
	}
	return out
}

// effective applies request=limit defaulting and LimitRange defaults to one container.
func effective(c map[string]any, lrs []limitItem) effContainer {
	e := effContainer{qtyMap(get(c, "resources", "requests"), append(admissionResources, "nvidia.com/gpu")), qtyMap(get(c, "resources", "limits"), append(admissionResources, "nvidia.com/gpu"))}
	for r, v := range e.lim {
		if _, ok := e.req[r]; !ok {
			e.req[r] = v // the API server defaults a missing request to the explicit limit
		}
	}
	for _, l := range lrs {
		if l.typ != "Container" {
			continue
		}
		for _, r := range admissionResources {
			if v, ok := l.def[r]; ok {
				if _, has := e.lim[r]; !has {
					e.lim[r] = v
				}
			}
		}
	}
	for _, l := range lrs {
		if l.typ != "Container" {
			continue
		}
		for _, r := range admissionResources {
			if _, has := e.req[r]; has {
				continue
			}
			if v, ok := l.defReq[r]; ok {
				e.req[r] = v
			} else if v, ok := l.def[r]; ok {
				e.req[r] = v
			}
		}
	}
	return e
}

func podContainers(spec map[string]any, lrs []limitItem) (cs, inits []effContainer) {
	for _, c := range asSlice(spec["containers"]) {
		cs = append(cs, effective(asMap(c), lrs))
	}
	for _, c := range asSlice(spec["initContainers"]) {
		inits = append(inits, effective(asMap(c), lrs))
	}
	return
}

// podTotals sums containers and takes the larger of that and the biggest init container.
func podTotals(cs, inits []effContainer) (req, lim map[string]float64, missReq, missLim map[string]bool) {
	req, lim = map[string]float64{}, map[string]float64{}
	missReq, missLim = map[string]bool{}, map[string]bool{}
	for _, c := range cs {
		for _, r := range admissionResources {
			if v, ok := c.req[r]; ok {
				req[r] += v
			} else {
				missReq[r] = true
			}
			if v, ok := c.lim[r]; ok {
				lim[r] += v
			} else {
				missLim[r] = true
			}
		}
		req["nvidia.com/gpu"] += c.req["nvidia.com/gpu"]
	}
	for _, c := range inits {
		for _, r := range append(admissionResources, "nvidia.com/gpu") {
			if v := c.req[r]; v > req[r] {
				req[r] = v
			}
		}
		for _, r := range admissionResources {
			if v := c.lim[r]; v > lim[r] {
				lim[r] = v
			}
		}
	}
	return
}

// limitRangeViolations returns violations grouped by finding code.
func limitRangeViolations(spec map[string]any, lrs []limitItem) map[string][]string {
	out := map[string][]string{}
	if len(lrs) == 0 || spec == nil {
		return out
	}
	cs, inits := podContainers(spec, lrs)
	check := func(scope string, req, lim map[string]float64, l limitItem, label string) {
		for _, r := range admissionResources {
			if mx, ok := l.max[r]; ok {
				if v, has := lim[r]; !has {
					out["ADMISSION_LIMITRANGE_MAX"] = append(out["ADMISSION_LIMITRANGE_MAX"], fmt.Sprintf("%s has no %s limit but the LimitRange max is %s", label, r, fmtQty(r, mx)))
				} else if v > mx+1e-9 {
					out["ADMISSION_LIMITRANGE_MAX"] = append(out["ADMISSION_LIMITRANGE_MAX"], fmt.Sprintf("%s %s limit %s exceeds the LimitRange %s max %s", label, r, fmtQty(r, v), scope, fmtQty(r, mx)))
				}
				if v, has := req[r]; has && v > mx+1e-9 {
					out["ADMISSION_LIMITRANGE_MAX"] = append(out["ADMISSION_LIMITRANGE_MAX"], fmt.Sprintf("%s %s request %s exceeds the LimitRange %s max %s", label, r, fmtQty(r, v), scope, fmtQty(r, mx)))
				}
			}
			if mn, ok := l.min[r]; ok {
				if v, has := req[r]; has && v < mn-1e-9 {
					out["ADMISSION_LIMITRANGE_MIN"] = append(out["ADMISSION_LIMITRANGE_MIN"], fmt.Sprintf("%s %s request %s is below the LimitRange %s min %s", label, r, fmtQty(r, v), scope, fmtQty(r, mn)))
				} else if !has {
					out["ADMISSION_LIMITRANGE_MIN"] = append(out["ADMISSION_LIMITRANGE_MIN"], fmt.Sprintf("%s has no %s request but the LimitRange %s min is %s", label, r, scope, fmtQty(r, mn)))
				}
				if v, has := lim[r]; has && v < mn-1e-9 {
					out["ADMISSION_LIMITRANGE_MIN"] = append(out["ADMISSION_LIMITRANGE_MIN"], fmt.Sprintf("%s %s limit %s is below the LimitRange %s min %s", label, r, fmtQty(r, v), scope, fmtQty(r, mn)))
				}
			}
			if ratio, ok := l.maxRatio[r]; ok && ratio > 0 {
				rv, hr := req[r]
				lv, hl := lim[r]
				if hr && hl && rv > 0 && lv/rv > ratio+1e-9 {
					out["ADMISSION_LIMITRANGE_RATIO"] = append(out["ADMISSION_LIMITRANGE_RATIO"], fmt.Sprintf("%s %s limit/request ratio %.2f exceeds the LimitRange max ratio %g", label, r, lv/rv, ratio))
				}
			}
		}
	}
	names := func(spec map[string]any, key string) []string {
		var n []string
		for i, c := range asSlice(spec[key]) {
			nm := str(asMap(c)["name"])
			if nm == "" {
				nm = fmt.Sprint(i)
			}
			n = append(n, nm)
		}
		return n
	}
	cn, in := names(spec, "containers"), names(spec, "initContainers")
	for _, l := range lrs {
		switch l.typ {
		case "Container":
			for i, c := range cs {
				check("Container", c.req, c.lim, l, "container "+cn[i])
			}
			for i, c := range inits {
				check("Container", c.req, c.lim, l, "init container "+in[i])
			}
		case "Pod":
			req, lim, _, missLim := podTotals(cs, inits)
			// A pod total is only meaningful when every container has a value.
			for r := range missLim {
				delete(lim, r)
			}
			check("Pod", req, lim, l, "pod")
		}
	}
	for code, v := range out {
		sort.Strings(v)
		out[code] = dedupe(v)
	}
	return out
}

func dedupe(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func collectQuotas(objs map[string]map[string]any) []quotaItem {
	keys := make([]string, 0, len(objs))
	for k, o := range objs {
		if str(o["kind"]) == "ResourceQuota" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []quotaItem
	for _, k := range keys {
		o := objs[k]
		if len(asSlice(get(o, "spec", "scopes"))) > 0 || get(o, "spec", "scopeSelector") != nil {
			continue
		}
		_, ns, name := metadata(o)
		hard := map[string]float64{}
		for key, v := range asMap(get(o, "spec", "hard")) {
			switch key {
			case "cpu", "requests.cpu":
				hard["requests.cpu"] = parseCPU(v)
			case "memory", "requests.memory":
				hard["requests.memory"] = float64(parseBytes(v))
			case "limits.cpu":
				hard["limits.cpu"] = parseCPU(v)
			case "limits.memory":
				hard["limits.memory"] = float64(parseBytes(v))
			case "pods", "requests.nvidia.com/gpu":
				hard[key] = float64(intValue(v, 0))
			}
		}
		if len(hard) > 0 {
			out = append(out, quotaItem{ns, name, hard})
		}
	}
	return out
}

// quotaUsage totals the namespace's workloads' requests, limits and pod count.
// Controller-owned Pods/ReplicaSets/Jobs are skipped to avoid counting the
// same pods as their owner.
func quotaUsage(objs map[string]map[string]any, ns string, lrs []limitItem, nodeCount int) map[string]float64 {
	use := map[string]float64{}
	for _, o := range objs {
		kind, ons, _ := metadata(o)
		if ons != ns {
			continue
		}
		spec := podSpecFor(o)
		if spec == nil || kind == "VirtualMachine" || kind == "CronJob" {
			continue
		}
		if (kind == "Pod" || kind == "ReplicaSet" || kind == "Job") && len(asSlice(get(o, "metadata", "ownerReferences"))) > 0 {
			continue
		}
		rep := float64(replicasFor(o, nodeCount))
		if rep <= 0 {
			continue
		}
		cs, inits := podContainers(spec, lrs)
		req, lim, _, _ := podTotals(cs, inits)
		use["requests.cpu"] += req["cpu"] * rep
		use["requests.memory"] += req["memory"] * rep
		use["requests.nvidia.com/gpu"] += req["nvidia.com/gpu"] * rep
		use["limits.cpu"] += lim["cpu"] * rep
		use["limits.memory"] += lim["memory"] * rep
		use["pods"] += rep
	}
	return use
}

func overlay(live []map[string]any, desired []map[string]any) (liveMap, projected map[string]map[string]any) {
	liveMap, projected = map[string]map[string]any{}, map[string]map[string]any{}
	for _, o := range live {
		liveMap[keyFor(o)] = o
		projected[keyFor(o)] = o
	}
	for _, o := range desired {
		projected[keyFor(o)] = o
	}
	return
}

// admissionRisks reports changed objects the API server would reject because
// of a LimitRange or ResourceQuota in their namespace. Rules from desired
// manifests replace live ones of the same name.
func admissionRisks(live, desired []map[string]any, nodeCount int) ([]Risk, admissionCounts) {
	liveMap, projected := overlay(live, desired)
	lrAfter := collectLimitRanges(projected)
	lrBefore := collectLimitRanges(liveMap)
	var risks []Risk
	var c admissionCounts
	quotas := collectQuotas(projected)
	changedIn := map[string][]string{} // namespace -> changed workload names
	nsChanged := map[string]bool{}

	for _, o := range desired {
		kind, ns, name := metadata(o)
		old := liveMap[keyFor(o)]
		if old != nil && canonical(old) == canonical(o) {
			continue
		}
		nsChanged[ns] = true
		ref := ResourceRef{Kind: kind, Namespace: ns, Name: name}
		if kind == "PersistentVolumeClaim" {
			for _, l := range lrAfter[ns] {
				if l.typ != "PersistentVolumeClaim" {
					continue
				}
				v := float64(parseBytes(get(o, "spec", "resources", "requests", "storage")))
				if mx, ok := l.max["storage"]; ok && v > mx {
					c.limitRange++
					risks = append(risks, Risk{"high", "ADMISSION_LIMITRANGE_MAX", "LimitRange rejects this claim",
						fmt.Sprintf("Requested storage %s exceeds the LimitRange PersistentVolumeClaim max %s in namespace %s.", fmtQty("storage", v), fmtQty("storage", mx), ns), ref})
				}
				if mn, ok := l.min["storage"]; ok && v > 0 && v < mn {
					c.limitRange++
					risks = append(risks, Risk{"high", "ADMISSION_LIMITRANGE_MIN", "LimitRange rejects this claim",
						fmt.Sprintf("Requested storage %s is below the LimitRange PersistentVolumeClaim min %s in namespace %s.", fmtQty("storage", v), fmtQty("storage", mn), ns), ref})
				}
			}
			continue
		}
		spec := podSpecFor(o)
		if spec == nil || kind == "VirtualMachine" {
			continue
		}
		changedIn[ns] = append(changedIn[ns], kind+" "+name)
		v := limitRangeViolations(spec, lrAfter[ns])
		codes := make([]string, 0, len(v))
		for code := range v {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		for _, code := range codes {
			c.limitRange++
			title := map[string]string{"ADMISSION_LIMITRANGE_MAX": "Pods exceed the namespace LimitRange maximum", "ADMISSION_LIMITRANGE_MIN": "Pods fall below the namespace LimitRange minimum", "ADMISSION_LIMITRANGE_RATIO": "Pods exceed the LimitRange limit/request ratio"}[code]
			risks = append(risks, Risk{"high", code, title, "The API server will reject these pods: " + joinLimited(v[code], 3) + ".", ref})
		}
		// Quotas that need explicit requests/limits reject pods that have none.
		_, _, missReq, missLim := podTotals(podContainers(spec, lrAfter[ns]))
		for _, q := range quotas {
			if q.ns != ns {
				continue
			}
			var miss []string
			for _, r := range admissionResources {
				if _, ok := q.hard["requests."+r]; ok && missReq[r] {
					miss = append(miss, "requests."+r)
				}
				if _, ok := q.hard["limits."+r]; ok && missLim[r] {
					miss = append(miss, "limits."+r)
				}
			}
			if len(miss) > 0 {
				c.quota++
				risks = append(risks, Risk{"high", "ADMISSION_QUOTA_NO_REQUESTS", "Quota requires resources the pods do not set",
					fmt.Sprintf("ResourceQuota %s constrains %s, so pods must specify them (a LimitRange default would also satisfy it); the API server rejects pods that do not.", q.name, strings.Join(miss, ", ")), ref})
			}
		}
	}
	for _, o := range desired { // quota or LimitRange changes also make their namespace relevant
		switch str(o["kind"]) {
		case "ResourceQuota", "LimitRange":
			_, ns, _ := metadata(o)
			nsChanged[ns] = true
		}
	}
	for _, q := range quotas {
		if !nsChanged[q.ns] {
			continue
		}
		after := quotaUsage(projected, q.ns, lrAfter[q.ns], nodeCount)
		before := quotaUsage(liveMap, q.ns, lrBefore[q.ns], nodeCount)
		var keys []string
		for k := range q.hard {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if after[k] > q.hard[k]+1e-9 && after[k] > before[k]+1e-9 {
				c.quota++
				who := ""
				if w := changedIn[q.ns]; len(w) > 0 {
					sort.Strings(w)
					who = " Changed workloads: " + joinLimited(w, 3) + "."
				}
				risks = append(risks, Risk{"high", "ADMISSION_QUOTA_EXCEEDED", "Namespace quota would be exceeded",
					fmt.Sprintf("Projected %s in namespace %s is %s but ResourceQuota %s allows %s (live usage %s); the API server rejects pods beyond the quota.%s",
						k, q.ns, fmtQty(k, after[k]), q.name, fmtQty(k, q.hard[k]), fmtQty(k, before[k]), who), ResourceRef{"ResourceQuota", q.ns, q.name}})
			}
		}
	}
	return risks, c
}

func joinLimited(s []string, n int) string {
	if len(s) <= n {
		return strings.Join(s, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(s[:n], "; "), len(s)-n)
}
