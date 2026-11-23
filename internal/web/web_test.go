// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func asset(t *testing.T, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d", path, w.Code)
	}
	return w.Body.String()
}

// The page has no browser test here, so check the wiring statically: every
// element the script looks up must exist, and every view must be present.
func TestScriptElementsExist(t *testing.T) {
	html, js := asset(t, "/"), asset(t, "/app.js")
	ids := regexp.MustCompile(`\$\('([a-z0-9-]+)'\)`).FindAllStringSubmatch(js, -1)
	if len(ids) < 20 {
		t.Fatalf("found only %d id lookups", len(ids))
	}
	for _, m := range ids {
		if !strings.Contains(html, `id="`+m[1]+`"`) {
			t.Errorf("app.js looks up #%s but index.html has no such element", m[1])
		}
	}
	for _, view := range []string{"drain", "consolidate", "rollout", "api", "explain"} {
		if !strings.Contains(html, `data-explore="`+view+`"`) || !strings.Contains(html, `data-pane="`+view+`"`) {
			t.Errorf("view %q lacks a tab or a pane", view)
		}
		if !strings.Contains(js, view+": {") {
			t.Errorf("view %q is not registered in app.js", view)
		}
	}
	for _, path := range []string{"/api/v1/drain", "/api/v1/consolidate", "/api/v1/simulate"} {
		if !strings.Contains(js, "'"+path+"'") {
			t.Errorf("app.js never calls %s", path)
		}
	}
	for _, want := range []string{"targetVersion", "id=\"api-target\"", "price-node", "price-gib", "estimate"} {
		if !strings.Contains(html+js, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// Server-provided text (names, details, YAML-derived values) must reach the
// DOM only through the escaping helpers. Every ${...} interpolated into an
// HTML template literal has to be on this allowlist.
func TestHTMLInterpolationsAreEscaped(t *testing.T) {
	js := asset(t, "/app.js")
	safe := []*regexp.Regexp{
		regexp.MustCompile(`^(esc|escapeHTML)\(`),                       // escaped text
		regexp.MustCompile(`^(num|pct|bytes|humanBytes|signed)\(`),      // numbers only
		regexp.MustCompile(`^(costHTML|metrics|riskHTML)\(`),            // helpers that escape internally
		regexp.MustCompile(`^(rows|moves)$`),                            // joined from escaped cells above
		regexp.MustCompile(`^[\w.]+(\.map\(.*\.join\(''\))$`),           // list built with esc()/helpers
		regexp.MustCompile(`^[\w.]+ \? '[A-Za-z -]*' : '[A-Za-z -]*'$`), // choice between literal class names
		regexp.MustCompile(`^verdictColor\(`),                           // returns a fixed colour literal
		regexp.MustCompile(`^(sign|rolling)$`),                          // '+'/'' and a count computed locally
	}
	lines := strings.Split(js, "\n")
	checked := 0
	for i, ln := range lines {
		if !strings.Contains(ln, "<") || !strings.Contains(ln, "`") {
			continue
		}
		for _, expr := range interpolations(ln) {
			// An expression that itself holds a template literal is checked
			// through the interpolations nested inside it.
			if strings.Contains(expr, "`") {
				continue
			}
			checked++
			ok := false
			for _, re := range safe {
				if re.MatchString(expr) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("app.js:%d interpolates %q into HTML without an escaping helper", i+1, expr)
			}
		}
	}
	if checked < 40 {
		t.Errorf("only %d interpolations checked; the pattern probably stopped matching", checked)
	}
	if !strings.Contains(js, "function escapeHTML") || !strings.Contains(js, "&#39;") {
		t.Error("escapeHTML must escape quotes")
	}
}

// interpolations returns every ${...} expression in s, including nested ones,
// matching braces by depth.
func interpolations(s string) []string {
	var out []string
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '$' || s[i+1] != '{' {
			continue
		}
		depth := 0
		for j := i + 1; j < len(s); j++ {
			if s[j] == '{' {
				depth++
			} else if s[j] == '}' {
				depth--
				if depth == 0 {
					expr := strings.TrimSpace(s[i+2 : j])
					out = append(out, expr)
					out = append(out, interpolations(expr)...)
					i = j
					break
				}
			}
		}
	}
	return out
}

func TestNoUnsafeDOMSinks(t *testing.T) {
	js := asset(t, "/app.js")
	for _, bad := range []string{"eval(", "document.write", "outerHTML", "insertAdjacentHTML", "new Function"} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js uses %s", bad)
		}
	}
}
