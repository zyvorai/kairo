// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Limits that bound hostile or accidental input.
const (
	maxUsageSeries       = 2000
	maxSamplesPerSeries  = 50000
	maxUsageSamplesTotal = 1000000
	maxNoDataListed      = 50
	defaultCPUPercentile = 0.95
	defaultMemPercentile = 0.99
	defaultCPUMargin     = 0.15
	defaultMemoryMargin  = 0.20
	defaultMinSamples    = 30
	defaultConfidence    = 0.90
	minRecommendedCPU    = 0.010
	minRecommendedMemory = 32 << 20
	memoryRoundingBytes  = 1 << 20
)

// UsageSeries is observed usage of one container of one workload: one value
// per sampling interval, per replica or aggregated per replica, as exported
// from your metrics system. CPU is in cores, memory in bytes (working set).
type UsageSeries struct {
	Namespace   string    `json:"namespace,omitempty"`
	Workload    string    `json:"workload"`
	Container   string    `json:"container"`
	CPUCores    []float64 `json:"cpuCores,omitempty"`
	MemoryBytes []float64 `json:"memoryBytes,omitempty"`
}

// UsageData is the supplied usage history. Kairo never queries a metrics
// system; it only reads what it is given.
type UsageData struct {
	Series []UsageSeries `json:"series"`
}

// RightsizeOptions is the safety-margin model. Unset fields take defaults,
// which are echoed in the result. Pointers distinguish "unset" from 0.
type RightsizeOptions struct {
	CPUPercentile    *float64 `json:"cpuPercentile,omitempty"`
	MemoryPercentile *float64 `json:"memoryPercentile,omitempty"`
	CPUMargin        *float64 `json:"cpuMargin,omitempty"`
	MemoryMargin     *float64 `json:"memoryMargin,omitempty"`
	MinSamples       int      `json:"minSamples,omitempty"`
	Confidence       *float64 `json:"confidence,omitempty"`
}

// EffectiveOptions are the options actually used.
type EffectiveOptions struct {
	CPUPercentile    float64 `json:"cpuPercentile"`
	MemoryPercentile float64 `json:"memoryPercentile"`
	CPUMargin        float64 `json:"cpuMargin"`
	MemoryMargin     float64 `json:"memoryMargin"`
	MinSamples       int     `json:"minSamples"`
	Confidence       float64 `json:"confidence"`
	MinCPUCores      float64 `json:"minCpuCores"`
	MinMemoryBytes   int64   `json:"minMemoryBytes"`
}

// RightsizeRequest asks what would happen to node count and cost if the
// requests of the snapshot's pods were set from the supplied usage.
type RightsizeRequest struct {
	ClusterYAML string            `json:"clusterYaml"`
	Usage       UsageData         `json:"usage"`
	Options     *RightsizeOptions `json:"options,omitempty"`
	Prices      *Prices           `json:"prices,omitempty"`
}

// ResourceRec is the recommendation for one resource of one container, with
// every input that produced it. Units are cores (CPU) or bytes (memory).
type ResourceRec struct {
	Samples    int     `json:"samples"`
	Percentile float64 `json:"percentile"`
	// Observed is the sample at Percentile (nearest rank); Max is the largest sample.
	Observed float64 `json:"observed"`
	Max      float64 `json:"max"`
	// Lower and Upper are a distribution-free confidence interval for the
	// true percentile from the order statistics; an open end means the
	// samples cannot bound the percentile on that side (the end is clamped to a sample).
	Lower     float64 `json:"lower"`
	Upper     float64 `json:"upper"`
	LowerOpen bool    `json:"lowerOpen"`
	UpperOpen bool    `json:"upperOpen"`
	Margin    float64 `json:"margin"`
	// Current is today's request per pod (0 when none is set).
	Current float64 `json:"current"`
	// Optimistic, Expected and Conservative are requests after margin and floor,
	// from the lower bound, the observed percentile and the upper bound.
	Optimistic   float64 `json:"optimistic"`
	Expected     float64 `json:"expected"`
	Conservative float64 `json:"conservative"`
	// AboveExpected counts samples above the expected request.
	AboveExpected int    `json:"aboveExpected"`
	Basis         string `json:"basis"`
}

// RightsizeContainer is the outcome for one supplied series.
type RightsizeContainer struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Container string `json:"container"`
	// Status is recommended, insufficient-samples or unmatched.
	Status string       `json:"status"`
	Pods   int          `json:"pods"`
	CPU    *ResourceRec `json:"cpu,omitempty"`
	Memory *ResourceRec `json:"memory,omitempty"`
	Notes  []string     `json:"notes"`
}

// RightsizeScenario is the snapshot consolidated after applying one set of requests.
type RightsizeScenario struct {
	Name                 string        `json:"name"`
	Description          string        `json:"description"`
	CPURequestedCores    float64       `json:"cpuRequestedCores"`
	MemoryRequestedBytes int64         `json:"memoryRequestedBytes"`
	NodesBefore          int           `json:"nodesBefore"`
	NodesAfter           int           `json:"nodesAfter"`
	Removable            []string      `json:"removable"`
	AdditionalNodes      int           `json:"additionalNodesRemovable"`
	Cost                 *CostEstimate `json:"cost,omitempty"`
	// AdditionalMonthlyDelta is this scenario's cost change minus the current-requests scenario's.
	AdditionalMonthlyDelta *float64 `json:"additionalMonthlyDelta,omitempty"`
}

type RightsizeResult struct {
	Options    EffectiveOptions     `json:"options"`
	Containers []RightsizeContainer `json:"containers"`
	Scenarios  []RightsizeScenario  `json:"scenarios"`
	// NoUsageData lists pod containers in the snapshot that the usage does not
	// cover; their requests are left unchanged in every scenario.
	NoUsageData      []string `json:"noUsageData"`
	NoUsageDataCount int      `json:"noUsageDataCount"`
	Summary          string   `json:"summary"`
	NotModelled      []string `json:"notModelled"`
}

var rightsizeNotModelled = []string{
	"Recommendations are advisory: Kairo never changes requests, limits or pods. Apply them yourself (for example through in-place resize or your manifests) after review.",
	"Only the supplied samples are used. Seasonality, deploys, incidents and traffic growth outside the sampled window are not modelled; short windows understate peaks.",
	"Samples are treated as independent draws. Autocorrelated metrics make the confidence interval narrower than it should be.",
	"An open interval end means the samples cannot bound the percentile; peak-based memory (percentile 100) always has an open upper end because unseen peaks are unknowable.",
	"Scenario node counts reuse the consolidation heuristic and its limits (no affinity, topology spread, volumes or autoscaler policy); only CPU and memory requests of matched pods change.",
	"Limits, QoS class, VPA/HPA interaction (an HPA on CPU utilisation reacts to changed requests) and init-container requests are not modelled.",
}

func (o *RightsizeOptions) effective() (EffectiveOptions, error) {
	e := EffectiveOptions{CPUPercentile: defaultCPUPercentile, MemoryPercentile: defaultMemPercentile, CPUMargin: defaultCPUMargin, MemoryMargin: defaultMemoryMargin, MinSamples: defaultMinSamples, Confidence: defaultConfidence, MinCPUCores: minRecommendedCPU, MinMemoryBytes: minRecommendedMemory}
	if o == nil {
		return e, nil
	}
	check := func(name string, p *float64, lo, hi float64, loOpen bool, dst *float64) error {
		if p == nil {
			return nil
		}
		v := *p
		if math.IsNaN(v) || math.IsInf(v, 0) || v < lo || v > hi || (loOpen && v == lo) {
			br := "["
			if loOpen {
				br = "("
			}
			return fmt.Errorf("%s must be in %s%g, %g]", name, br, lo, hi)
		}
		*dst = v
		return nil
	}
	if err := check("cpuPercentile", o.CPUPercentile, 0, 1, true, &e.CPUPercentile); err != nil {
		return e, err
	}
	if err := check("memoryPercentile", o.MemoryPercentile, 0, 1, true, &e.MemoryPercentile); err != nil {
		return e, err
	}
	if err := check("cpuMargin", o.CPUMargin, 0, 5, false, &e.CPUMargin); err != nil {
		return e, err
	}
	if err := check("memoryMargin", o.MemoryMargin, 0, 5, false, &e.MemoryMargin); err != nil {
		return e, err
	}
	if o.Confidence != nil {
		c := *o.Confidence
		if math.IsNaN(c) || c < 0.5 || c > 0.999 {
			return e, fmt.Errorf("confidence must be between 0.5 and 0.999")
		}
		e.Confidence = c
	}
	if o.MinSamples != 0 {
		if o.MinSamples < 1 || o.MinSamples > maxSamplesPerSeries {
			return e, fmt.Errorf("minSamples must be between 1 and %d", maxSamplesPerSeries)
		}
		e.MinSamples = o.MinSamples
	}
	return e, nil
}

func validateUsage(u UsageData) error {
	if len(u.Series) == 0 {
		return fmt.Errorf("usage.series is empty: supply at least one series of CPU/memory samples")
	}
	if len(u.Series) > maxUsageSeries {
		return fmt.Errorf("usage.series has %d entries; the limit is %d", len(u.Series), maxUsageSeries)
	}
	total := 0
	seen := map[string]bool{}
	for i, s := range u.Series {
		if strings.TrimSpace(s.Workload) == "" || strings.TrimSpace(s.Container) == "" {
			return fmt.Errorf("usage.series[%d]: workload and container are required", i)
		}
		if len(s.Workload) > 253 || len(s.Container) > 253 || len(s.Namespace) > 253 {
			return fmt.Errorf("usage.series[%d]: names are limited to 253 characters", i)
		}
		if len(s.CPUCores) == 0 && len(s.MemoryBytes) == 0 {
			return fmt.Errorf("usage.series[%d] (%s/%s): has no cpuCores or memoryBytes samples", i, s.Workload, s.Container)
		}
		if len(s.CPUCores) > maxSamplesPerSeries || len(s.MemoryBytes) > maxSamplesPerSeries {
			return fmt.Errorf("usage.series[%d]: more than %d samples", i, maxSamplesPerSeries)
		}
		total += len(s.CPUCores) + len(s.MemoryBytes)
		if total > maxUsageSamplesTotal {
			return fmt.Errorf("usage has more than %d samples in total", maxUsageSamplesTotal)
		}
		for name, vals := range map[string][]float64{"cpuCores": s.CPUCores, "memoryBytes": s.MemoryBytes} {
			for j, v := range vals {
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e18 {
					return fmt.Errorf("usage.series[%d].%s[%d] = %v is not a finite non-negative number", i, name, j, v)
				}
			}
		}
		k := seriesNS(s) + "/" + s.Workload + "/" + s.Container
		if seen[k] {
			return fmt.Errorf("usage.series[%d]: duplicate series for %s", i, k)
		}
		seen[k] = true
	}
	return nil
}

func seriesNS(s UsageSeries) string {
	if s.Namespace == "" {
		return "default"
	}
	return s.Namespace
}

// workloadNames lists the names a pod answers to in a usage series: its own
// name, its controller's name, and for a ReplicaSet the Deployment name
// (the ReplicaSet name minus its pod-template hash).
func workloadNames(p map[string]any) []string {
	_, _, name := metadata(p)
	out := []string{name}
	for _, o := range asSlice(get(p, "metadata", "ownerReferences")) {
		om := asMap(o)
		on := str(om["name"])
		if on == "" {
			continue
		}
		out = append(out, on)
		if str(om["kind"]) == "ReplicaSet" {
			if i := strings.LastIndex(on, "-"); i > 0 {
				out = append(out, on[:i])
			}
		}
	}
	return out
}

func podMatches(p map[string]any, ns, workload string) bool {
	if _, pns, _ := metadata(p); pns != ns {
		return false
	}
	for _, n := range workloadNames(p) {
		if n == workload {
			return true
		}
	}
	return false
}

func containerSpec(p map[string]any, name string) map[string]any {
	for _, c := range asSlice(get(p, "spec", "containers")) {
		if cm := asMap(c); str(cm["name"]) == name {
			return cm
		}
	}
	return nil
}

// quantileRank is the 1-indexed nearest-rank position of quantile q in n samples.
func quantileRank(n int, q float64) int {
	r := int(math.Ceil(q*float64(n) - 1e-9))
	if r < 1 {
		r = 1
	}
	if r > n {
		r = n
	}
	return r
}

// quantileCI returns 1-indexed order-statistic ranks (lo, hi) such that
// [X(lo), X(hi)] covers the true q-quantile with probability >= conf, with no
// distributional assumption. loOpen/hiOpen report that n is too small to
// bound that side, in which case the rank is clamped to 1 or n.
func quantileCI(n int, q, conf float64) (lo, hi int, loOpen, hiOpen bool) {
	if q >= 1 {
		return n, n, false, true
	}
	alpha2 := (1 - conf) / 2
	lg := func(x float64) float64 { v, _ := math.Lgamma(x); return v }
	lq, l1q := math.Log(q), math.Log(1-q)
	cdf, acc := make([]float64, n+1), 0.0 // cdf[k] = P(Bin(n,q) <= k)
	for k := 0; k <= n; k++ {
		acc += math.Exp(lg(float64(n+1)) - lg(float64(k+1)) - lg(float64(n-k+1)) + float64(k)*lq + float64(n-k)*l1q)
		cdf[k] = math.Min(acc, 1)
	}
	lo, loOpen = 1, true
	for l := n; l >= 1; l-- {
		if cdf[l-1] <= alpha2 {
			lo, loOpen = l, false
			break
		}
	}
	hi, hiOpen = n, true
	for u := 1; u <= n; u++ {
		if cdf[u-1] >= 1-alpha2 {
			hi, hiOpen = u, false
			break
		}
	}
	if lo > hi {
		lo = hi
	}
	return
}

func roundUpTo(v, step float64) float64 { return math.Ceil(v/step-1e-9) * step }

func recommend(samples []float64, q, margin, conf float64, current, floor, step float64, unit func(float64) string, what string) *ResourceRec {
	n := len(samples)
	s := append([]float64{}, samples...)
	sort.Float64s(s)
	lo, hi, loOpen, hiOpen := quantileCI(n, q, conf)
	r := &ResourceRec{Samples: n, Percentile: q, Observed: s[quantileRank(n, q)-1], Max: s[n-1], Lower: s[lo-1], Upper: s[hi-1], LowerOpen: loOpen, UpperOpen: hiOpen, Margin: margin, Current: current}
	rec := func(v float64) float64 { return roundUpTo(math.Max(v*(1+margin), floor), step) }
	r.Optimistic, r.Expected, r.Conservative = rec(r.Lower), rec(r.Observed), rec(r.Upper)
	for _, v := range s {
		if v > r.Expected {
			r.AboveExpected++
		}
	}
	r.Basis = fmt.Sprintf("%s: p%g of %d samples = %s (%.0f%% interval %s..%s%s) x (1+%g margin), floor %s, rounded up -> %s", what, q*100, n, unit(r.Observed), conf*100, unit(r.Lower), unit(r.Upper), openNote(loOpen, hiOpen), margin, unit(floor), unit(r.Expected))
	return r
}

func openNote(lo, hi bool) string {
	switch {
	case lo && hi:
		return ", open at both ends"
	case lo:
		return ", open at the low end"
	case hi:
		return ", open at the high end"
	}
	return ""
}

func cpuUnit(v float64) string { return fmt.Sprintf("%.0fm", v*1000) }
func memUnit(v float64) string { return humanBytes(int64(v)) }

// Rightsize recommends requests from supplied usage samples and reports what
// the recommendations would do to the consolidation outcome and cost, as a
// band (optimistic / expected / conservative) with every input shown.
func Rightsize(req RightsizeRequest) (RightsizeResult, error) {
	if err := req.Prices.validate(); err != nil {
		return RightsizeResult{}, err
	}
	opt, err := req.Options.effective()
	if err != nil {
		return RightsizeResult{}, err
	}
	if err := validateUsage(req.Usage); err != nil {
		return RightsizeResult{}, err
	}
	objs, err := ParseDocuments(req.ClusterYAML)
	if err != nil {
		return RightsizeResult{}, fmt.Errorf("parse cluster snapshot: %w", err)
	}
	if len(buildNodes(objs)) == 0 {
		return RightsizeResult{}, fmt.Errorf("the cluster snapshot has no Node objects")
	}

	res := RightsizeResult{Options: opt, Containers: []RightsizeContainer{}, Scenarios: []RightsizeScenario{}, NoUsageData: []string{}, NotModelled: rightsizeNotModelled}
	covered := map[string]bool{} // namespace/pod/container
	for _, s := range req.Usage.Series {
		ns := seriesNS(s)
		c := RightsizeContainer{Namespace: ns, Workload: s.Workload, Container: s.Container, Notes: []string{}}
		var matched []map[string]any
		for _, o := range objs {
			if str(o["kind"]) == "Pod" && !podTerminated(o) && podMatches(o, ns, s.Workload) && containerSpec(o, s.Container) != nil {
				matched = append(matched, o)
			}
		}
		c.Pods = len(matched)
		if len(matched) == 0 {
			c.Status = "unmatched"
			c.Notes = append(c.Notes, "no running pod in the snapshot has this namespace, workload (pod or controller name) and container; the series was not used")
			res.Containers = append(res.Containers, c)
			continue
		}
		var curCPU, curMem float64
		varies := false
		for k, p := range matched {
			_, _, pn := metadata(p)
			covered[ns+"/"+pn+"/"+s.Container] = true
			rq := asMap(get(containerSpec(p, s.Container), "resources", "requests"))
			cpu, mem := parseCPU(rq["cpu"]), float64(parseBytes(rq["memory"]))
			if k == 0 {
				curCPU, curMem = cpu, mem
			} else if cpu != curCPU || mem != curMem {
				varies = true
			}
		}
		if varies {
			c.Notes = append(c.Notes, "pods of this workload have different requests today; the first pod's values are shown as current")
		}
		if len(s.CPUCores) >= opt.MinSamples {
			c.CPU = recommend(s.CPUCores, opt.CPUPercentile, opt.CPUMargin, opt.Confidence, curCPU, opt.MinCPUCores, 0.001, cpuUnit, "CPU")
		} else if len(s.CPUCores) > 0 {
			c.Notes = append(c.Notes, fmt.Sprintf("CPU has %d sample(s); at least %d are required, so CPU is left unchanged", len(s.CPUCores), opt.MinSamples))
		} else {
			c.Notes = append(c.Notes, "no CPU samples supplied; CPU is left unchanged")
		}
		if len(s.MemoryBytes) >= opt.MinSamples {
			c.Memory = recommend(s.MemoryBytes, opt.MemoryPercentile, opt.MemoryMargin, opt.Confidence, curMem, float64(opt.MinMemoryBytes), memoryRoundingBytes, memUnit, "memory")
		} else if len(s.MemoryBytes) > 0 {
			c.Notes = append(c.Notes, fmt.Sprintf("memory has %d sample(s); at least %d are required, so memory is left unchanged", len(s.MemoryBytes), opt.MinSamples))
		} else {
			c.Notes = append(c.Notes, "no memory samples supplied; memory is left unchanged")
		}
		c.Status = "recommended"
		if c.CPU == nil && c.Memory == nil {
			c.Status = "insufficient-samples"
		}
		for _, r := range []*ResourceRec{c.CPU, c.Memory} {
			if r != nil && r.Max == 0 {
				c.Notes = append(c.Notes, "every sample is zero: the container looks idle or its metric is missing; the recommendation is the floor")
			}
		}
		if r := c.Memory; r != nil {
			if r.Current == 0 {
				c.Notes = append(c.Notes, "no memory request is set today; applying the recommendation adds one")
			}
			if r.AboveExpected > 0 {
				c.Notes = append(c.Notes, fmt.Sprintf("%d of %d memory samples (%.1f%%) are above the recommended request; with a limit near the request that is an OOM-kill risk (the margin is %g)", r.AboveExpected, r.Samples, 100*float64(r.AboveExpected)/float64(r.Samples), r.Margin))
			}
			if r.Expected > r.Current && r.Current > 0 {
				c.Notes = append(c.Notes, "memory recommendation is above today's request: the container is under-requested")
			}
		}
		if r := c.CPU; r != nil {
			if r.Current == 0 {
				c.Notes = append(c.Notes, "no CPU request is set today; applying the recommendation adds one")
			}
			if r.Expected > r.Current && r.Current > 0 {
				c.Notes = append(c.Notes, "CPU recommendation is above today's request: the container is under-requested")
			}
		}
		lim := asMap(get(containerSpec(matched[0], s.Container), "resources", "limits"))
		if l := parseCPU(lim["cpu"]); c.CPU != nil && l > 0 && c.CPU.Conservative > l {
			c.Notes = append(c.Notes, fmt.Sprintf("the conservative CPU request (%s) exceeds the container's CPU limit (%s); raise the limit or the API server rejects it", cpuUnit(c.CPU.Conservative), cpuUnit(l)))
		}
		if l := float64(parseBytes(lim["memory"])); c.Memory != nil && l > 0 && c.Memory.Conservative > l {
			c.Notes = append(c.Notes, fmt.Sprintf("the conservative memory request (%s) exceeds the container's memory limit (%s); raise the limit or the API server rejects it", memUnit(c.Memory.Conservative), memUnit(l)))
		}
		res.Containers = append(res.Containers, c)
	}
	sort.SliceStable(res.Containers, func(i, j int) bool {
		a, b := res.Containers[i], res.Containers[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		return a.Container < b.Container
	})

	// Containers the usage does not cover: reported, never defaulted.
	var missing []string
	for _, o := range objs {
		if str(o["kind"]) != "Pod" || podTerminated(o) || ownerKind(o) == "DaemonSet" {
			continue
		}
		_, ns, name := metadata(o)
		for _, c := range asSlice(get(o, "spec", "containers")) {
			if cn := str(asMap(c)["name"]); !covered[ns+"/"+name+"/"+cn] {
				missing = append(missing, ns+"/"+name+"/"+cn)
			}
		}
	}
	sort.Strings(missing)
	res.NoUsageDataCount = len(missing)
	if len(missing) > maxNoDataListed {
		missing = missing[:maxNoDataListed]
	}
	res.NoUsageData = append(res.NoUsageData, missing...)

	scenarios := []struct{ name, desc string }{
		{"current", "today's requests"},
		{"optimistic", "requests from the lower bound of each percentile interval, plus margin: the most savings the data supports"},
		{"expected", "requests from the observed percentile, plus margin"},
		{"conservative", "requests from the upper bound of each percentile interval, plus margin: the safest requests the data supports"},
	}
	baseRemovable := 0
	var baseCost *CostEstimate
	for k, sc := range scenarios {
		so, err := ParseDocuments(req.ClusterYAML)
		if err != nil {
			return RightsizeResult{}, err
		}
		if k > 0 {
			applyRequests(so, res.Containers, sc.name)
		}
		var cpuSum float64
		var memSum int64
		for _, o := range so {
			if str(o["kind"]) == "Pod" && !podTerminated(o) {
				r := resourceReq(asMap(o["spec"]))
				cpuSum += r.cpu
				memSum += r.mem
			}
		}
		cr, err := consolidateObjs(so, req.Prices)
		if err != nil {
			return RightsizeResult{}, err
		}
		s := RightsizeScenario{Name: sc.name, Description: sc.desc, CPURequestedCores: round2(cpuSum), MemoryRequestedBytes: memSum, NodesBefore: cr.NodesBefore, NodesAfter: cr.NodesAfter, Removable: cr.Removable, Cost: cr.Cost}
		if k == 0 {
			baseRemovable, baseCost = len(cr.Removable), cr.Cost
		}
		s.AdditionalNodes = len(cr.Removable) - baseRemovable
		if cr.Cost != nil && baseCost != nil {
			d := round2(cr.Cost.MonthlyDelta-baseCost.MonthlyDelta) + 0
			s.AdditionalMonthlyDelta = &d
		}
		res.Scenarios = append(res.Scenarios, s)
	}
	cur, exp := res.Scenarios[0], res.Scenarios[2]
	used := 0
	for _, c := range res.Containers {
		if c.Status == "recommended" {
			used++
		}
	}
	res.Summary = fmt.Sprintf("%d of %d series produced recommendations. Requested CPU %.2f -> %.2f cores, memory %s -> %s (expected). Removable nodes: %d now, %d / %d / %d (optimistic / expected / conservative).",
		used, len(res.Containers), cur.CPURequestedCores, exp.CPURequestedCores, humanBytes(cur.MemoryRequestedBytes), humanBytes(exp.MemoryRequestedBytes),
		baseRemovable, len(res.Scenarios[1].Removable), len(exp.Removable), len(res.Scenarios[3].Removable))
	return res, nil
}

// applyRequests rewrites the matched pods' requests in objs for a scenario.
func applyRequests(objs []map[string]any, cs []RightsizeContainer, scenario string) {
	pick := func(r *ResourceRec) float64 {
		switch scenario {
		case "optimistic":
			return r.Optimistic
		case "conservative":
			return r.Conservative
		}
		return r.Expected
	}
	for _, c := range cs {
		if c.Status != "recommended" {
			continue
		}
		for _, o := range objs {
			if str(o["kind"]) != "Pod" || podTerminated(o) || !podMatches(o, c.Namespace, c.Workload) {
				continue
			}
			cm := containerSpec(o, c.Container)
			if cm == nil {
				continue
			}
			res := asMap(cm["resources"])
			if res == nil {
				res = map[string]any{}
				cm["resources"] = res
			}
			rq := asMap(res["requests"])
			if rq == nil {
				rq = map[string]any{}
				res["requests"] = rq
			}
			if c.CPU != nil {
				rq["cpu"] = fmt.Sprintf("%dm", int64(math.Round(pick(c.CPU)*1000)))
			}
			if c.Memory != nil {
				rq["memory"] = fmt.Sprintf("%d", int64(math.Round(pick(c.Memory))))
			}
		}
	}
}

// RightsizeText renders the result for the terminal, inputs included.
func RightsizeText(r RightsizeResult) string {
	var b strings.Builder
	o := r.Options
	fmt.Fprintf(&b, "KAIRO rightsize\n%s\n", r.Summary)
	fmt.Fprintf(&b, "Model: CPU p%g + %g%% margin, memory p%g + %g%% margin, %g%% interval, min %d samples, floors %s / %s\n", o.CPUPercentile*100, o.CPUMargin*100, o.MemoryPercentile*100, o.MemoryMargin*100, o.Confidence*100, o.MinSamples, cpuUnit(o.MinCPUCores), humanBytes(o.MinMemoryBytes))
	for _, c := range r.Containers {
		fmt.Fprintf(&b, "\n%s/%s container %s: %s (%d pod(s))\n", c.Namespace, c.Workload, c.Container, c.Status, c.Pods)
		for _, rec := range []struct {
			n string
			r *ResourceRec
			u func(float64) string
		}{{"cpu", c.CPU, cpuUnit}, {"memory", c.Memory, memUnit}} {
			if rec.r == nil {
				continue
			}
			x := rec.r
			cur := "none set"
			if x.Current > 0 {
				cur = rec.u(x.Current)
			}
			fmt.Fprintf(&b, "  %s: now %s -> %s / %s / %s (optimistic / expected / conservative)\n    %s\n", rec.n, cur, rec.u(x.Optimistic), rec.u(x.Expected), rec.u(x.Conservative), x.Basis)
		}
		for _, n := range c.Notes {
			fmt.Fprintf(&b, "  note: %s\n", n)
		}
	}
	if r.NoUsageDataCount > 0 {
		fmt.Fprintf(&b, "\nNo usage data for %d pod container(s), requests unchanged: %s", r.NoUsageDataCount, strings.Join(r.NoUsageData, ", "))
		if r.NoUsageDataCount > len(r.NoUsageData) {
			b.WriteString(", ...")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nScenarios (consolidation after applying each set of requests):\n")
	for _, s := range r.Scenarios {
		fmt.Fprintf(&b, "  %-12s CPU %.2f cores, memory %s requested; %d -> %d node(s), removable: %s", s.Name, s.CPURequestedCores, humanBytes(s.MemoryRequestedBytes), s.NodesBefore, s.NodesAfter, nodeList(s.Removable))
		if s.Cost != nil {
			fmt.Fprintf(&b, "; monthly %+.2f %s", s.Cost.MonthlyDelta, s.Cost.Currency)
			if s.AdditionalMonthlyDelta != nil && s.Name != "current" {
				fmt.Fprintf(&b, " (%+.2f vs today)", *s.AdditionalMonthlyDelta)
			}
		}
		b.WriteString("\n")
	}
	if r.Scenarios[0].Cost != nil {
		b.WriteString("  " + costNote + "\n")
	}
	b.WriteString("\nNot modelled:\n")
	for _, x := range r.NotModelled {
		fmt.Fprintf(&b, "  - %s\n", x)
	}
	return b.String()
}

func nodeList(n []string) string {
	if len(n) == 0 {
		return "none"
	}
	return strings.Join(n, ", ")
}
