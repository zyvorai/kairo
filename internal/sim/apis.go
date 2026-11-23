// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"strconv"
	"strings"
)

// k8sVersion is a Kubernetes minor version (major is always 1).
type k8sVersion int

func (v k8sVersion) String() string { return "1." + strconv.Itoa(int(v)) }

// latestKnown is the newest minor version the deprecation table covers; it
// is the default target so un-pinned runs check against "latest", like Pluto.
const latestKnown k8sVersion = 33

// parseTargetVersion accepts "", "1.29", "v1.29" or "v1.29.3".
func parseTargetVersion(s string) (k8sVersion, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" {
		return latestKnown, nil
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "1" {
		return 0, fmt.Errorf("invalid target version %q: want 1.<minor>, e.g. 1.29", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 999 {
		return 0, fmt.Errorf("invalid target version %q: want 1.<minor>, e.g. 1.29", s)
	}
	return k8sVersion(m), nil
}

// apiEntry records the lifecycle of one apiVersion for a set of kinds.
// deprecated or removed of 0 means "not applicable".
type apiEntry struct {
	apiVersion  string
	kinds       []string // empty: every kind in the group version
	deprecated  k8sVersion
	removed     k8sVersion
	replacement string
}

// removedAPIs is curated from the Kubernetes deprecation guide
// (https://kubernetes.io/docs/reference/using-api/deprecation-guide/).
var removedAPIs = []apiEntry{
	{"extensions/v1beta1", []string{"Deployment", "DaemonSet", "ReplicaSet"}, 9, 16, "apps/v1"},
	{"extensions/v1beta1", []string{"NetworkPolicy"}, 9, 16, "networking.k8s.io/v1"},
	{"extensions/v1beta1", []string{"PodSecurityPolicy"}, 10, 16, "policy/v1beta1 (itself removed in 1.25; use Pod Security Admission)"},
	{"extensions/v1beta1", []string{"Ingress"}, 14, 22, "networking.k8s.io/v1"},
	{"apps/v1beta1", nil, 9, 16, "apps/v1"},
	{"apps/v1beta2", nil, 9, 16, "apps/v1"},
	{"networking.k8s.io/v1beta1", []string{"Ingress", "IngressClass"}, 19, 22, "networking.k8s.io/v1"},
	{"rbac.authorization.k8s.io/v1beta1", nil, 17, 22, "rbac.authorization.k8s.io/v1"},
	{"rbac.authorization.k8s.io/v1alpha1", nil, 8, 22, "rbac.authorization.k8s.io/v1"},
	{"admissionregistration.k8s.io/v1beta1", nil, 16, 22, "admissionregistration.k8s.io/v1"},
	{"apiextensions.k8s.io/v1beta1", nil, 16, 22, "apiextensions.k8s.io/v1"},
	{"apiregistration.k8s.io/v1beta1", nil, 10, 22, "apiregistration.k8s.io/v1"},
	{"authentication.k8s.io/v1beta1", nil, 19, 22, "authentication.k8s.io/v1"},
	{"authorization.k8s.io/v1beta1", nil, 19, 22, "authorization.k8s.io/v1"},
	{"certificates.k8s.io/v1beta1", nil, 19, 22, "certificates.k8s.io/v1"},
	{"coordination.k8s.io/v1beta1", nil, 19, 22, "coordination.k8s.io/v1"},
	{"scheduling.k8s.io/v1beta1", nil, 14, 22, "scheduling.k8s.io/v1"},
	{"scheduling.k8s.io/v1alpha1", nil, 14, 22, "scheduling.k8s.io/v1"},
	{"storage.k8s.io/v1beta1", []string{"CSIDriver", "CSINode", "StorageClass", "VolumeAttachment"}, 19, 22, "storage.k8s.io/v1"},
	{"storage.k8s.io/v1beta1", []string{"CSIStorageCapacity"}, 24, 27, "storage.k8s.io/v1"},
	{"batch/v1beta1", []string{"CronJob"}, 21, 25, "batch/v1"},
	{"policy/v1beta1", []string{"PodDisruptionBudget"}, 21, 25, "policy/v1"},
	{"policy/v1beta1", []string{"PodSecurityPolicy"}, 21, 25, "Pod Security Admission or a policy engine (no direct replacement)"},
	{"discovery.k8s.io/v1beta1", []string{"EndpointSlice"}, 21, 25, "discovery.k8s.io/v1"},
	{"events.k8s.io/v1beta1", []string{"Event"}, 19, 25, "events.k8s.io/v1"},
	{"node.k8s.io/v1beta1", []string{"RuntimeClass"}, 20, 25, "node.k8s.io/v1"},
	{"autoscaling/v2beta1", []string{"HorizontalPodAutoscaler"}, 22, 25, "autoscaling/v2"},
	{"autoscaling/v2beta2", []string{"HorizontalPodAutoscaler"}, 23, 26, "autoscaling/v2"},
	{"flowcontrol.apiserver.k8s.io/v1beta1", nil, 23, 26, "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io/v1beta2", nil, 26, 29, "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io/v1beta3", nil, 29, 32, "flowcontrol.apiserver.k8s.io/v1"},
}

func (e apiEntry) matches(apiVersion, kind string) bool {
	if e.apiVersion != apiVersion {
		return false
	}
	if len(e.kinds) == 0 {
		return true
	}
	for _, k := range e.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// apiVersionRisks reports a removed or deprecated apiVersion on one desired
// object relative to the target Kubernetes version. A removed API is a hard
// failure (the API server rejects the manifest); a deprecated one is advice.
func apiVersionRisks(obj map[string]any, target k8sVersion) []Risk {
	kind, ns, name := metadata(obj)
	av := str(obj["apiVersion"])
	ref := ResourceRef{Kind: kind, Namespace: ns, Name: name}
	for _, e := range removedAPIs {
		if !e.matches(av, kind) {
			continue
		}
		switch {
		case e.removed > 0 && e.removed <= target:
			return []Risk{{"critical", "API_REMOVED", "API version removed in Kubernetes " + e.removed.String(),
				fmt.Sprintf("%s %s was removed in %s (target %s). Migrate to %s.", av, kind, e.removed, target, e.replacement), ref}}
		case e.deprecated > 0 && e.deprecated <= target:
			return []Risk{{"medium", "API_DEPRECATED", "API version deprecated since Kubernetes " + e.deprecated.String(),
				fmt.Sprintf("%s %s is deprecated since %s and will be removed in %s. Migrate to %s.", av, kind, e.deprecated, e.removed, e.replacement), ref}}
		}
	}
	return nil
}
