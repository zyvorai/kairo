// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"math"
	"strings"
	"testing"
)

func ramp(n int, from, to float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = from + (to-from)*float64(i)/float64(n-1)
	}
	return out
}

func rsCluster() string {
	return nodeYAML("a", "4", "16Gi") + nodeYAML("b", "4", "16Gi") + nodeYAML("c", "4", "16Gi") +
		cpod("web-1", "a", "web-5d8", "1500m", "3Gi", "") + cpod("web-2", "b", "web-5d8", "1500m", "3Gi", "") + cpod("web-3", "c", "web-5d8", "1500m", "3Gi", "")
}

func TestQuantileCIKnownValues(t *testing.T) {
	// Classic binomial interval for the median of 100 samples at 95%.
	if lo, hi, lop, hop := quantileCI(100, 0.5, 0.95); lo != 40 || hi != 61 || lop || hop {
		t.Errorf("median n=100: %d %d %v %v", lo, hi, lop, hop)
	}
	// Peak: never bounded above.
	if lo, hi, _, hop := quantileCI(50, 1, 0.9); lo != 50 || hi != 50 || !hop {
		t.Errorf("max: %d %d %v", lo, hi, hop)
	}
	// Too few samples for p95 at 90%: upper end is open.
	if _, hi, _, hop := quantileCI(5, 0.95, 0.9); hi != 5 || !hop {
		t.Errorf("n=5 p95: %d %v", hi, hop)
	}
	if got := quantileRank(100, 0.95); got != 95 {
		t.Errorf("rank = %d", got)
	}
	if got := quantileRank(3, 0.0001); got != 1 {
		t.Errorf("rank = %d", got)
	}
}

func TestRightsizeRecommendationShowsItsInputs(t *testing.T) {
	cpu := ramp(100, 0.10, 0.30) // p95 -> 0.2906..., sorted ascending
	mem := ramp(100, 200<<20, 400<<20)
	r, err := Rightsize(RightsizeRequest{ClusterYAML: rsCluster(), Usage: UsageData{Series: []UsageSeries{{Namespace: "default", Workload: "web", Container: "c", CPUCores: cpu, MemoryBytes: mem}}}})
	if err != nil {
		t.Fatal(err)
	}
	c := r.Containers[0]
	if c.Status != "recommended" || c.Pods != 3 || c.CPU == nil || c.Memory == nil {
		t.Fatalf("%+v", c)
	}
	want := math.Ceil(cpu[94]*1.15*1000-1e-9) / 1000
	if math.Abs(c.CPU.Expected-want) > 1e-9 || c.CPU.Observed != cpu[94] {
		t.Errorf("cpu expected %v observed %v, want %v / %v", c.CPU.Expected, c.CPU.Observed, want, cpu[94])
	}
	if !(c.CPU.Optimistic <= c.CPU.Expected && c.CPU.Expected <= c.CPU.Conservative) {
		t.Errorf("band not ordered: %+v", c.CPU)
	}
	if c.CPU.Current != 1.5 || c.Memory.Current != float64(3<<30) {
		t.Errorf("current requests not read: %v %v", c.CPU.Current, c.Memory.Current)
	}
	for _, s := range []string{"p95", "100 samples", "interval", "margin", "floor"} {
		if !strings.Contains(c.CPU.Basis, s) {
			t.Errorf("basis %q lacks %q", c.CPU.Basis, s)
		}
	}
	// Memory p99 of 100 samples cannot be bounded above: open end, reported.
	if !c.Memory.UpperOpen || !strings.Contains(c.Memory.Basis, "open at the high end") {
		t.Errorf("memory interval should be open: %+v", c.Memory)
	}
	if len(r.Scenarios) != 4 || r.Scenarios[0].Name != "current" {
		t.Fatalf("scenarios %+v", r.Scenarios)
	}
	cur, exp := r.Scenarios[0], r.Scenarios[2]
	if exp.CPURequestedCores >= cur.CPURequestedCores || exp.MemoryRequestedBytes >= cur.MemoryRequestedBytes {
		t.Errorf("expected scenario should request less: %+v vs %+v", exp, cur)
	}
	if len(exp.Removable) <= len(cur.Removable) || exp.AdditionalNodes != len(exp.Removable)-len(cur.Removable) {
		t.Errorf("right-sizing 3 pods of 1.5 CPU should free more nodes: cur %v exp %v", cur.Removable, exp.Removable)
	}
	if len(r.NoUsageData) != 0 || r.NotModelled == nil {
		t.Errorf("%v", r.NoUsageData)
	}
}

func TestRightsizeMissingDataIsReportedNotDefaulted(t *testing.T) {
	y := rsCluster() + cpod("other-1", "a", "other-1x", "500m", "1Gi", "")
	r, err := Rightsize(RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{
		{Workload: "web", Container: "c", CPUCores: ramp(10, 0.1, 0.2), MemoryBytes: ramp(40, 1<<28, 1<<29)}, // cpu below MinSamples, memory ok
		{Workload: "ghost", Container: "c", CPUCores: ramp(40, 0.1, 0.2)},                                    // no such pod
		{Workload: "web", Namespace: "elsewhere", Container: "c", CPUCores: ramp(40, 0.1, 0.2)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]RightsizeContainer{}
	for _, c := range r.Containers {
		by[c.Namespace+"/"+c.Workload] = c
	}
	web := by["default/web"]
	if web.CPU != nil || web.Memory == nil || !strings.Contains(strings.Join(web.Notes, " "), "10 sample(s); at least 30") {
		t.Errorf("web = %+v", web)
	}
	if by["default/ghost"].Status != "unmatched" || by["elsewhere/web"].Status != "unmatched" {
		t.Errorf("unmatched series must be flagged: %+v", r.Containers)
	}
	if r.NoUsageDataCount != 1 || r.NoUsageData[0] != "default/other-1/c" {
		t.Errorf("uncovered containers = %v (%d)", r.NoUsageData, r.NoUsageDataCount)
	}
	// CPU stays at today's value in every scenario where it had too few samples.
	if r.Scenarios[1].CPURequestedCores != r.Scenarios[0].CPURequestedCores {
		t.Errorf("cpu changed without enough samples: %+v", r.Scenarios)
	}
}

func TestRightsizeFlagsRiskyRecommendations(t *testing.T) {
	// Idle container (all zeros) and a memory series with a spike above a peak-based request.
	zeros := make([]float64, 40)
	y := nodeYAML("a", "4", "16Gi") + nodeYAML("b", "4", "16Gi") + cpod("idle-1", "a", "idle-abc", "1", "1Gi", "") + cpod("hot-1", "b", "hot-abc", "1", "1Gi", "")
	p := 0.5
	opts := &RightsizeOptions{MemoryPercentile: &p}
	spiky := ramp(40, 1<<28, 1<<28+1)
	spiky[39] = 8 << 30
	r, err := Rightsize(RightsizeRequest{ClusterYAML: y, Options: opts, Usage: UsageData{Series: []UsageSeries{
		{Workload: "idle", Container: "c", CPUCores: zeros, MemoryBytes: zeros},
		{Workload: "hot", Container: "c", MemoryBytes: spiky},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	idle, hot := r.Containers[1], r.Containers[0]
	if idle.Workload == "hot" {
		idle, hot = hot, idle
	}
	if !strings.Contains(strings.Join(idle.Notes, " "), "every sample is zero") || idle.CPU.Expected != 0.010 || idle.Memory.Expected != float64(32<<20) {
		t.Errorf("idle must show the floor and a warning: %+v %v", idle.CPU, idle.Notes)
	}
	if hot.Memory.AboveExpected != 1 || !strings.Contains(strings.Join(hot.Notes, " "), "OOM-kill risk") {
		t.Errorf("spike above the recommendation must be flagged: %+v %v", hot.Memory, hot.Notes)
	}
}

func TestRightsizeLimitConflictAndPrices(t *testing.T) {
	y := nodeYAML("a", "4", "16Gi") + nodeYAML("b", "4", "16Gi") + strings.Replace(cpod("w-1", "a", "w-abc", "1", "1Gi", ""), "requests:", "limits: {cpu: 100m}\n        requests:", 1)
	r, err := Rightsize(RightsizeRequest{ClusterYAML: y, Prices: &Prices{NodeHour: 1, Currency: "EUR"}, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ramp(40, 0.5, 0.9)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.Containers[0].Notes, " "), "exceeds the container's CPU limit") {
		t.Errorf("notes = %v", r.Containers[0].Notes)
	}
	for _, s := range r.Scenarios {
		if s.Cost == nil || !s.Cost.Estimate || s.Cost.Currency != "EUR" {
			t.Errorf("scenario %s cost = %+v", s.Name, s.Cost)
		}
	}
	if r.Scenarios[0].AdditionalMonthlyDelta == nil || *r.Scenarios[0].AdditionalMonthlyDelta != 0 {
		t.Errorf("baseline additional = %v", r.Scenarios[0].AdditionalMonthlyDelta)
	}
	r2, _ := Rightsize(RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ramp(40, 0.5, 0.9)}}}})
	if r2.Scenarios[0].Cost != nil || r2.Scenarios[0].AdditionalMonthlyDelta != nil {
		t.Error("no prices means no cost")
	}
}

func TestRightsizeHostileInput(t *testing.T) {
	y := rsCluster()
	ok := []float64{0.1, 0.2}
	nan, inf, neg := math.NaN(), math.Inf(1), -1.0
	big := make([]float64, maxSamplesPerSeries+1)
	many := make([]UsageSeries, maxUsageSeries+1)
	for i := range many {
		many[i] = UsageSeries{Workload: "w", Container: string(rune('a'+i%26)) + strings.Repeat("x", i), CPUCores: ok}
	}
	bad := 2.0
	zero := 0.0
	cases := []struct {
		name string
		req  RightsizeRequest
		want string
	}{
		{"no series", RightsizeRequest{ClusterYAML: y}, "usage.series is empty"},
		{"nan", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: []float64{nan}}}}}, "finite non-negative"},
		{"inf", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", MemoryBytes: []float64{inf}}}}}, "finite non-negative"},
		{"negative", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: []float64{neg}}}}}, "finite non-negative"},
		{"no name", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Container: "c", CPUCores: ok}}}}, "required"},
		{"no samples", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c"}}}}, "no cpuCores or memoryBytes"},
		{"duplicate", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}, {Workload: "w", Container: "c", CPUCores: ok}}}}, "duplicate"},
		{"too long", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: big}}}}, "more than"},
		{"too many series", RightsizeRequest{ClusterYAML: y, Usage: UsageData{Series: many}}, "limit is"},
		{"bad percentile", RightsizeRequest{ClusterYAML: y, Options: &RightsizeOptions{CPUPercentile: &bad}, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}}}}, "cpuPercentile"},
		{"zero percentile", RightsizeRequest{ClusterYAML: y, Options: &RightsizeOptions{MemoryPercentile: &zero}, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}}}}, "memoryPercentile"},
		{"nan margin", RightsizeRequest{ClusterYAML: y, Options: &RightsizeOptions{CPUMargin: &nan}, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}}}}, "cpuMargin"},
		{"negative min samples", RightsizeRequest{ClusterYAML: y, Options: &RightsizeOptions{MinSamples: -1}, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}}}}, "minSamples"},
		{"bad price", RightsizeRequest{ClusterYAML: y, Prices: &Prices{NodeHour: -1}, Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}}}}, "nodeHourPrice"},
		{"no nodes", RightsizeRequest{ClusterYAML: "kind: Service\nmetadata: {name: s}", Usage: UsageData{Series: []UsageSeries{{Workload: "w", Container: "c", CPUCores: ok}}}}, "no Node"},
	}
	for _, c := range cases {
		_, err := Rightsize(c.req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestRightsizeDeterministicAndDoesNotMutateInput(t *testing.T) {
	req := RightsizeRequest{ClusterYAML: rsCluster(), Usage: UsageData{Series: []UsageSeries{{Workload: "web", Container: "c", CPUCores: ramp(60, 0.1, 0.4), MemoryBytes: ramp(60, 1<<28, 1<<29)}}}}
	a, _ := Rightsize(req)
	b, _ := Rightsize(req)
	if RightsizeText(a) != RightsizeText(b) {
		t.Error("not deterministic")
	}
	if req.Usage.Series[0].CPUCores[0] != 0.1 || req.Usage.Series[0].CPUCores[59] != 0.4 {
		t.Error("input samples were reordered")
	}
	if !strings.Contains(RightsizeText(a), "Not modelled") {
		t.Error("text must list what is not modelled")
	}
}
