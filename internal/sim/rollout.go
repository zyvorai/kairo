// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// resolveIntOrPercent resolves a Kubernetes IntOrString against total.
// Percentages round up or down as the caller requires. ok is false when the
// value is absent or malformed so callers can apply the API default.
func resolveIntOrPercent(v any, total int, roundUp bool) (int, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int:
		return max(x, 0), x >= 0
	case int64:
		return max(int(x), 0), x >= 0
	case float64:
		return max(int(x), 0), x >= 0
	case string:
		s := strings.TrimSpace(x)
		if strings.HasSuffix(s, "%") {
			p, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
			if err != nil || p < 0 {
				return 0, false
			}
			f := p * float64(total) / 100
			if roundUp {
				return int(math.Ceil(f - 1e-9)), true
			}
			return int(math.Floor(f + 1e-9)), true
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

// rolloutParams returns the effective maxSurge and maxUnavailable of a
// Deployment (Kubernetes defaults: 25% each, surge rounded up, unavailable
// down). rolling is false for the Recreate strategy, which never surges.
func rolloutParams(obj map[string]any, replicas int) (surge, unavailable int, rolling bool) {
	if str(get(obj, "spec", "strategy", "type")) == "Recreate" {
		return 0, replicas, false
	}
	ru := asMap(get(obj, "spec", "strategy", "rollingUpdate"))
	surge, ok := resolveIntOrPercent(ru["maxSurge"], replicas, true)
	if !ok {
		surge = int(math.Ceil(float64(replicas)*0.25 - 1e-9))
	}
	unavailable, ok = resolveIntOrPercent(ru["maxUnavailable"], replicas, false)
	if !ok {
		unavailable = int(math.Floor(float64(replicas)*0.25 + 1e-9))
	}
	return surge, unavailable, true
}

// rolloutSurgeRisk simulates the peak of a rolling update. nodes must hold
// the steady-state placement of the new replicas; the surge pods are placed
// on a copy on top of it, because while the rollout runs, up to maxSurge pods
// exist beyond the desired replica count. It returns nil when the change does
// not start a rollout or the surge fits. The caller must only invoke it when
// the steady state itself schedules, so the two findings never overlap.
func rolloutSurgeRisk(old, obj map[string]any, replicas int, nodes []*nodeState, rq resources, pl placement) *Risk {
	kind, ns, name := metadata(obj)
	if kind != "Deployment" || old == nil || replicas <= 0 {
		return nil
	}
	if canonical(get(old, "spec", "template")) == canonical(get(obj, "spec", "template")) {
		return nil // replica-only or metadata change: no new ReplicaSet, no rollout
	}
	surge, unavail, rolling := rolloutParams(obj, replicas)
	if !rolling || surge <= 0 {
		return nil
	}
	uns := schedule(cloneNodes(nodes), rq, surge, pl)
	if uns == 0 {
		return nil
	}
	ref := ResourceRef{Kind: kind, Namespace: ns, Name: name}
	if unavail == 0 {
		return &Risk{"high", "ROLLOUT_SURGE_UNSCHEDULABLE", "Rolling update will stall",
			fmt.Sprintf("The workload fits at steady state, but %d of %d surge pods (maxSurge %d, maxUnavailable 0) cannot be scheduled during the rollout, so it can never complete. Free capacity, lower maxSurge or allow maxUnavailable.", uns, surge, surge), ref}
	}
	return &Risk{"medium", "ROLLOUT_SURGE_UNSCHEDULABLE", "Rolling update runs with reduced capacity",
		fmt.Sprintf("The workload fits at steady state, but %d of %d surge pods (maxSurge %d) cannot be scheduled during the rollout; it will proceed only by taking up to %d pods down at a time.", uns, surge, surge, unavail), ref}
}
