// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"math"
	"strings"
	"testing"
)

func TestPlanCostEstimate(t *testing.T) {
	cluster := nodeYAML("n1", "8", "32Gi") + nodeYAML("n2", "8", "32Gi") + teamDeploy("a", 2, "{requests: {cpu: \"1\", memory: 1Gi}}")
	desired := teamDeploy("a", 6, "{requests: {cpu: \"1\", memory: 1Gi}}") // +4 cores, +4GiB
	r, err := Simulate(Request{ClusterYAML: cluster, DesiredYAML: desired, Prices: &Prices{NodeHour: 0.10, GiBHour: 0.01, Currency: "EUR"}})
	if err != nil {
		t.Fatal(err)
	}
	c := r.Cost
	if c == nil || !c.Estimate || c.Currency != "EUR" || c.HoursPerMonth != 730 {
		t.Fatalf("cost = %+v", c)
	}
	// 4 cores of an 8-core node = 0.5 node; 4GiB of 32GiB = 0.125 node; the larger wins.
	if want := 0.5 * 0.10 * 730; math.Abs(c.NodeMonthlyDelta-want) > 0.01 {
		t.Errorf("node delta = %v, want %v", c.NodeMonthlyDelta, want)
	}
	if want := 4 * 0.01 * 730; math.Abs(c.MemoryMonthlyDelta-want) > 0.01 {
		t.Errorf("memory delta = %v, want %v", c.MemoryMonthlyDelta, want)
	}
	if math.Abs(c.MonthlyDelta-(c.NodeMonthlyDelta+c.MemoryMonthlyDelta)) > 0.011 {
		t.Errorf("total %v != parts", c.MonthlyDelta)
	}
	if !strings.Contains(c.Note, "Estimate") || !strings.Contains(c.Note, "no cloud pricing API") {
		t.Errorf("note must label the figure an estimate: %q", c.Note)
	}
	if !strings.Contains(CostText(c), "Estimated monthly cost change: +") {
		t.Errorf("text: %s", CostText(c))
	}
	// A reduction is a saving (negative).
	r, _ = Simulate(Request{ClusterYAML: cluster, DesiredYAML: teamDeploy("a", 1, "{requests: {cpu: \"1\", memory: 1Gi}}"), Prices: &Prices{NodeHour: 1}})
	if r.Cost == nil || r.Cost.MonthlyDelta >= 0 {
		t.Errorf("shrinking should save money: %+v", r.Cost)
	}
}

func TestNoPricesNoCost(t *testing.T) {
	cluster := nodeYAML("n1", "8", "32Gi")
	for _, p := range []*Prices{nil, {}, {Currency: "USD"}} {
		r, _ := Simulate(Request{ClusterYAML: cluster, DesiredYAML: teamDeploy("a", 1, "{}"), Prices: p})
		if r.Cost != nil {
			t.Errorf("prices %+v produced cost %+v", p, r.Cost)
		}
	}
}

func TestPriceValidation(t *testing.T) {
	for _, p := range []Prices{{NodeHour: -1}, {GiBHour: -0.5}, {NodeHour: math.NaN()}, {NodeHour: math.Inf(1)}, {NodeHour: 2e9}, {Currency: strings.Repeat("x", 17), NodeHour: 1}} {
		p := p
		if _, err := Simulate(Request{ClusterYAML: nodeYAML("n", "1", "1Gi"), DesiredYAML: teamDeploy("a", 1, "{}"), Prices: &p}); err == nil {
			t.Errorf("%+v accepted", p)
		}
		if _, err := Consolidate(ConsolidateRequest{ClusterYAML: nodeYAML("n", "1", "1Gi"), Prices: &p}); err == nil {
			t.Errorf("consolidate accepted %+v", p)
		}
	}
}
