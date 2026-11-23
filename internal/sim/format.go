// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import (
	"fmt"
	"strings"
)

// Rank orders verdicts so callers can fail on a threshold.
func Rank(verdict string) int {
	switch verdict {
	case "BLOCK":
		return 2
	case "REVIEW":
		return 1
	}
	return 0
}

// Markdown renders a result as a pull-request comment.
func Markdown(r Result) string {
	var b strings.Builder
	icon := map[string]string{"SAFE": "✅", "REVIEW": "⚠️", "BLOCK": "⛔"}[r.Verdict]
	fmt.Fprintf(&b, "### %s Kairo verdict: %s\n\n", icon, r.Verdict)
	fmt.Fprintf(&b, "**Risk:** %s · **Blast radius:** %d/100 · **Cluster:** %s\n\n", r.RiskLevel, r.BlastRadius, mdEscape(r.ClusterName))
	fmt.Fprintf(&b, "| Changed | Created | Deleted | Pods restarted | Unschedulable |\n|---|---|---|---|---|\n| %d | %d | %d | %d | %d |\n\n",
		r.ChangedObjects, r.CreatedObjects, r.DeletedObjects, r.PodsRestarted, r.UnschedulablePods)
	if len(r.Risks) > 0 {
		b.WriteString("| Severity | Code | Resource | Detail |\n|---|---|---|---|\n")
		for _, x := range r.Risks {
			fmt.Fprintf(&b, "| %s | `%s` | %s/%s | %s |\n", x.Severity, x.Code, mdEscape(x.Resource.Kind), mdEscape(x.Resource.Name), mdEscape(x.Detail))
		}
		b.WriteString("\n")
	}
	for _, rec := range r.Recommendations {
		fmt.Fprintf(&b, "- %s\n", mdEscape(rec))
	}
	return b.String()
}

// mdEscape keeps untrusted manifest text from breaking out of a table cell
// or injecting markup into a PR comment.
func mdEscape(s string) string {
	s = strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ", "<", "&lt;", ">", "&gt;", "`", "'").Replace(s)
	return s
}
