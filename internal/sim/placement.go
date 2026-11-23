// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

// unschedulableTaint is the taint Kubernetes puts on cordoned nodes.
const unschedulableTaint = "node.kubernetes.io/unschedulable"

type taint struct{ key, value, effect string }

type toleration struct{ key, op, value, effect string }

// placement carries the scheduling constraints of one pod template.
type placement struct {
	selector    map[string]string
	tolerations []toleration
}

func placementFor(spec map[string]any, daemonSet bool) placement {
	p := placement{selector: map[string]string{}}
	for k, v := range asMap(spec["nodeSelector"]) {
		p.selector[k] = str(v)
	}
	for _, t := range asSlice(spec["tolerations"]) {
		tm := asMap(t)
		op := str(tm["operator"])
		if op == "" {
			op = "Equal"
		}
		p.tolerations = append(p.tolerations, toleration{str(tm["key"]), op, str(tm["value"]), str(tm["effect"])})
	}
	if daemonSet {
		// The DaemonSet controller adds this toleration automatically.
		p.tolerations = append(p.tolerations, toleration{unschedulableTaint, "Exists", "", "NoSchedule"})
	}
	return p
}

func (t toleration) tolerates(x taint) bool {
	if t.effect != "" && t.effect != x.effect {
		return false
	}
	if t.key == "" { // empty key with Exists matches every taint
		return t.op == "Exists"
	}
	if t.key != x.key {
		return false
	}
	if t.op == "Exists" {
		return true
	}
	return t.value == x.value
}

// eligible reports whether a pod with these constraints may land on the node,
// ignoring free capacity. Cordoned nodes and NoSchedule/NoExecute taints
// exclude the node unless tolerated; PreferNoSchedule is only a preference.
func (n *nodeState) eligible(p placement) bool {
	for k, v := range p.selector {
		if got, ok := n.labels[k]; !ok || got != v {
			return false
		}
	}
	taints := n.taints
	if n.unschedulable {
		taints = append(append([]taint{}, taints...), taint{unschedulableTaint, "", "NoSchedule"})
	}
	for _, x := range taints {
		if x.effect != "NoSchedule" && x.effect != "NoExecute" {
			continue
		}
		ok := false
		for _, t := range p.tolerations {
			if t.tolerates(x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (n *nodeState) fits(r resources) bool {
	return n.free.cpu+1e-9 >= r.cpu && n.free.mem >= r.mem && n.free.gpu >= r.gpu
}

func (n *nodeState) take(r resources) {
	n.free.cpu -= r.cpu
	n.free.mem -= r.mem
	n.free.gpu -= r.gpu
}

func cloneNodes(nodes []*nodeState) []*nodeState {
	out := make([]*nodeState, len(nodes))
	for i, n := range nodes {
		c := *n
		out[i] = &c
	}
	return out
}

// schedule places replicas greedily (most free memory first) on eligible
// nodes and returns how many could not be placed.
func schedule(nodes []*nodeState, r resources, replicas int, p placement) int {
	uns := 0
	for i := 0; i < replicas; i++ {
		best := -1
		for j, n := range nodes {
			if n.eligible(p) && n.fits(r) && (best < 0 || n.free.mem > nodes[best].free.mem) {
				best = j
			}
		}
		if best < 0 {
			uns++
			continue
		}
		nodes[best].take(r)
	}
	return uns
}

// scheduleDaemonSet places exactly one pod on every eligible node; nodes the
// DaemonSet does not target are skipped. It returns pods placed-or-failed
// (targets) and how many of those do not fit.
func scheduleDaemonSet(nodes []*nodeState, r resources, p placement) (targets, unscheduled int) {
	for _, n := range nodes {
		if !n.eligible(p) {
			continue
		}
		targets++
		if n.fits(r) {
			n.take(r)
		} else {
			unscheduled++
		}
	}
	return
}
