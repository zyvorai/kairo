// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"math"
)

// HoursPerMonth is the fixed month length used for every cost estimate
// (365 days * 24 h / 12).
const HoursPerMonth = 730

// maxPrice bounds user-supplied prices so absurd input cannot overflow.
const maxPrice = 1e9

// Prices are optional, user-supplied unit prices. Kairo never calls a cloud
// pricing API; with no prices there is no cost estimate.
type Prices struct {
	// Currency is a free-form label shown next to amounts (default "price units").
	Currency string `json:"currency,omitempty"`
	// NodeHour is the price of one node for one hour.
	NodeHour float64 `json:"nodeHourPrice,omitempty"`
	// GiBHour is the price of one GiB of memory for one hour, for setups where
	// memory is billed separately from the node. Leave it 0 when NodeHour
	// already covers the whole node, or memory is counted twice.
	GiBHour float64 `json:"gibHourPrice,omitempty"`
}

// CostEstimate is a monthly cost change derived from Prices. Negative
// amounts are savings. It is an estimate from user-supplied prices, never a quote.
type CostEstimate struct {
	Estimate           bool    `json:"estimate"`
	Currency           string  `json:"currency"`
	HoursPerMonth      int     `json:"hoursPerMonth"`
	NodeHourPrice      float64 `json:"nodeHourPrice"`
	GiBHourPrice       float64 `json:"gibHourPrice"`
	NodeMonthlyDelta   float64 `json:"nodeMonthlyDelta"`
	MemoryMonthlyDelta float64 `json:"memoryMonthlyDelta"`
	MonthlyDelta       float64 `json:"monthlyDelta"`
	Basis              string  `json:"basis"`
	Note               string  `json:"note"`
}

const costNote = "Estimate from user-supplied prices over a 730-hour month; no cloud pricing API was called. Not a quote."

func (p *Prices) validate() error {
	if p == nil {
		return nil
	}
	for name, v := range map[string]float64{"nodeHourPrice": p.NodeHour, "gibHourPrice": p.GiBHour} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > maxPrice {
			return fmt.Errorf("%s must be between 0 and %g", name, maxPrice)
		}
	}
	if len(p.Currency) > 16 {
		return fmt.Errorf("currency label is limited to 16 characters")
	}
	return nil
}

func (p *Prices) active() bool { return p != nil && (p.NodeHour > 0 || p.GiBHour > 0) }

func newCost(p *Prices, nodeDelta, gibDelta float64, basis string) *CostEstimate {
	cur := p.Currency
	if cur == "" {
		cur = "price units"
	}
	c := &CostEstimate{Estimate: true, Currency: cur, HoursPerMonth: HoursPerMonth, NodeHourPrice: p.NodeHour, GiBHourPrice: p.GiBHour, Basis: basis, Note: costNote}
	c.NodeMonthlyDelta = round2(nodeDelta*p.NodeHour*HoursPerMonth) + 0 // + 0 turns -0 into 0
	c.MemoryMonthlyDelta = round2(gibDelta*p.GiBHour*HoursPerMonth) + 0
	c.MonthlyDelta = round2(c.NodeMonthlyDelta+c.MemoryMonthlyDelta) + 0
	return c
}

// planCost estimates the monthly effect of a change. The node component is
// the proportional share of an average node the requested CPU/memory delta
// occupies (the larger of the two fractions); the memory component prices the
// requested-memory delta. Requests are demand, not provisioned capacity, so
// this is a share-of-cost view, not a prediction of nodes the autoscaler adds.
func planCost(p *Prices, cpuDelta float64, memDelta int64, nodes []*nodeState) *CostEstimate {
	if !p.active() {
		return nil
	}
	var cpu float64
	var mem int64
	for _, n := range nodes {
		cpu += n.cap.cpu
		mem += n.cap.mem
	}
	eq := 0.0
	if len(nodes) > 0 && cpu > 0 && mem > 0 {
		avgCPU, avgMem := cpu/float64(len(nodes)), float64(mem)/float64(len(nodes))
		eq = cpuDelta / avgCPU
		if f := float64(memDelta) / avgMem; math.Abs(f) > math.Abs(eq) {
			eq = f
		}
	}
	return newCost(p, eq, float64(memDelta)/(1<<30), fmt.Sprintf("requested CPU/memory delta (%.2f cores, %s) as a share of an average node (%d nodes in snapshot)", cpuDelta, humanBytes(abs64(memDelta)), len(nodes)))
}

// CostText renders a cost estimate for the terminal.
func CostText(c *CostEstimate) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("Estimated monthly cost change: %+.2f %s (nodes %+.2f, memory %+.2f)\n  Basis: %s\n  %s", c.MonthlyDelta, c.Currency, c.NodeMonthlyDelta, c.MemoryMonthlyDelta, c.Basis, c.Note)
}
