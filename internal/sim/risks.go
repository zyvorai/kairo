// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import "fmt"

// extraRisks inspects one changed desired object for security, exposure and
// access-control risks that are independent of capacity. It returns the risks
// found and the counters they feed into the score.
type extraCounts struct {
	security, exposure, rbac int
}

func extraRisks(obj, old map[string]any) ([]Risk, extraCounts) {
	kind, ns, name := metadata(obj)
	ref := ResourceRef{Kind: kind, Namespace: ns, Name: name}
	var out []Risk
	var c extraCounts

	if spec := podSpecFor(obj); spec != nil {
		before := map[string]bool{}
		if old != nil {
			for _, f := range podSecurityFindings(podSpecFor(old)) {
				before[f.code] = true
			}
		}
		for _, f := range podSecurityFindings(spec) {
			if before[f.code] {
				continue // already present in the live object: not introduced by this change
			}
			c.security++
			out = append(out, Risk{"high", f.code, f.title, f.detail, ref})
		}
	}

	switch kind {
	case "Service":
		t := str(get(obj, "spec", "type"))
		if (t == "LoadBalancer" || t == "NodePort") && (old == nil || str(get(old, "spec", "type")) != t) {
			c.exposure++
			out = append(out, Risk{"medium", "SERVICE_EXPOSED", "Service becomes reachable from outside the cluster",
				fmt.Sprintf("Service type is %s; confirm the exposure and any source-range restrictions are intended.", t), ref})
		}
	case "Ingress":
		if old == nil || canonical(get(old, "spec")) != canonical(get(obj, "spec")) {
			c.exposure++
			out = append(out, Risk{"medium", "INGRESS_CHANGE", "External routing changes",
				"An Ingress is created or its rules change. Verify hosts, paths and TLS before rollout.", ref})
		}
	case "ClusterRole", "Role":
		if wildcardRule(obj) {
			c.rbac++
			out = append(out, Risk{"high", "RBAC_WILDCARD", "Role grants wildcard access",
				"A rule uses '*' for verbs or resources. Grant only the verbs and resources that are needed.", ref})
		}
	case "ClusterRoleBinding", "RoleBinding":
		if str(get(obj, "roleRef", "name")) == "cluster-admin" && (old == nil || canonical(get(old, "subjects")) != canonical(get(obj, "subjects"))) {
			c.rbac++
			out = append(out, Risk{"critical", "RBAC_CLUSTER_ADMIN", "Binding grants cluster-admin",
				"Subjects are bound to the cluster-admin role. Prefer a narrowly scoped role.", ref})
		}
	}
	return out, c
}

type podFinding struct{ code, title, detail string }

func podSecurityFindings(spec map[string]any) []podFinding {
	if spec == nil {
		return nil
	}
	var out []podFinding
	if b, _ := spec["hostNetwork"].(bool); b {
		out = append(out, podFinding{"HOST_NETWORK", "Pod uses the host network", "hostNetwork bypasses pod network isolation and network policies."})
	}
	if b, _ := spec["hostPID"].(bool); b {
		out = append(out, podFinding{"HOST_PID", "Pod shares the host PID namespace", "hostPID lets the workload see and signal host processes."})
	}
	for _, v := range asSlice(spec["volumes"]) {
		if asMap(v)["hostPath"] != nil {
			out = append(out, podFinding{"HOST_PATH", "Pod mounts a host path", "A hostPath volume exposes the node filesystem to the workload."})
			break
		}
	}
	for _, key := range []string{"containers", "initContainers"} {
		privileged, escalation := false, false
		for _, c := range asSlice(spec[key]) {
			sc := asMap(get(asMap(c), "securityContext"))
			if b, _ := sc["privileged"].(bool); b {
				privileged = true
			}
			if b, _ := sc["allowPrivilegeEscalation"].(bool); b {
				escalation = true
			}
		}
		if privileged {
			out = append(out, podFinding{"PRIVILEGED_CONTAINER", "Privileged container", "A container runs with privileged: true and has near-host access."})
			break
		}
		if escalation {
			out = append(out, podFinding{"PRIVILEGE_ESCALATION", "Privilege escalation allowed", "A container sets allowPrivilegeEscalation: true."})
			break
		}
	}
	return out
}

func wildcardRule(obj map[string]any) bool {
	for _, r := range asSlice(obj["rules"]) {
		rm := asMap(r)
		for _, k := range []string{"verbs", "resources"} {
			for _, v := range asSlice(rm[k]) {
				if str(v) == "*" {
					return true
				}
			}
		}
	}
	return false
}
