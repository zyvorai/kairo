// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"sort"
	"strings"
)

// FieldChange is one attribute that differs between the live and desired object.
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// Change describes what happens to one object. Action is created, changed or deleted.
type Change struct {
	Kind      string        `json:"kind"`
	Namespace string        `json:"namespace,omitempty"`
	Name      string        `json:"name"`
	Action    string        `json:"action"`
	Fields    []FieldChange `json:"fields"`
}

// summarize flattens the attributes reviewers care about: replicas, container
// images and container resource requests/limits.
func summarize(o map[string]any) map[string]string {
	out := map[string]string{}
	if o == nil {
		return out
	}
	switch str(o["kind"]) {
	case "Deployment", "StatefulSet", "ReplicaSet":
		out["replicas"] = fmt.Sprint(intValue(get(o, "spec", "replicas"), 1))
	}
	if str(o["kind"]) == "PersistentVolumeClaim" {
		if v := str(get(o, "spec", "resources", "requests", "storage")); v != "" {
			out["storage"] = v
		}
	}
	spec := podSpecFor(o)
	if str(o["kind"]) == "VirtualMachine" {
		spec = nil // synthetic spec; VM sizing is diffed through the raw object fallback
	}
	for _, key := range []string{"containers", "initContainers"} {
		for i, c := range asSlice(get(spec, key)) {
			cm := asMap(c)
			cname := str(cm["name"])
			if cname == "" {
				cname = fmt.Sprintf("#%d", i)
			}
			prefix := cname
			if key == "initContainers" {
				prefix = "init:" + cname
			}
			if img := str(cm["image"]); img != "" {
				out["image "+prefix] = img
			}
			for _, kind := range []string{"requests", "limits"} {
				for rk, rv := range asMap(get(cm, "resources", kind)) {
					out[kind+" "+prefix+" "+rk] = str(rv)
				}
			}
		}
	}
	return out
}

func diffSummaries(oldS, newS map[string]string) []FieldChange {
	keys := map[string]bool{}
	for k := range oldS {
		keys[k] = true
	}
	for k := range newS {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []FieldChange
	for _, k := range names {
		if oldS[k] != newS[k] {
			out = append(out, FieldChange{k, oldS[k], newS[k]})
		}
	}
	return out
}

// diffObjects compares the live objects with the desired ones and returns a
// deterministic, sorted list of per-object changes. Unmanaged live kinds that
// are absent from the desired set are not reported (they are not deleted).
func diffObjects(live, desired map[string]map[string]any) []Change {
	var out []Change
	add := func(o map[string]any, action string, fields []FieldChange) {
		kind, ns, name := metadata(o)
		if fields == nil {
			fields = []FieldChange{}
		}
		out = append(out, Change{kind, ns, name, action, fields})
	}
	for k, o := range desired {
		old, ok := live[k]
		switch {
		case !ok:
			add(o, "created", diffSummaries(nil, summarize(o)))
		case canonical(old) != canonical(o):
			f := diffSummaries(summarize(old), summarize(o))
			if len(f) == 0 {
				f = []FieldChange{{"spec", "", "modified"}}
			}
			add(o, "changed", f)
		}
	}
	for k, o := range live {
		if _, ok := desired[k]; !ok && isManagedKind(o) {
			add(o, "deleted", diffSummaries(summarize(o), nil))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	return out
}

// ExplainText renders the per-object changes for terminal output.
func ExplainText(changes []Change) string {
	if len(changes) == 0 {
		return "No object changes."
	}
	sym := map[string]string{"created": "+", "changed": "~", "deleted": "-"}
	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "%s %s %s/%s (%s)\n", sym[c.Action], c.Kind, c.Namespace, c.Name, c.Action)
		for _, f := range c.Fields {
			switch {
			case c.Action == "created":
				fmt.Fprintf(&b, "    %s: %s\n", f.Field, f.New)
			case c.Action == "deleted":
				fmt.Fprintf(&b, "    %s: %s\n", f.Field, f.Old)
			default:
				fmt.Fprintf(&b, "    %s: %s -> %s\n", f.Field, orNone(f.Old), orNone(f.New))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
