// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := Server{Web: http.NotFoundHandler()}.Handler()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHealthz(t *testing.T) {
	if w := serve(t, "GET", "/healthz", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestSimulateValidation(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"invalid json", `{`, 400},
		{"missing fields", `{"clusterYaml":"a: 1"}`, 400},
		{"too large", `{"clusterYaml":"` + strings.Repeat("a", 5<<20) + `"}`, 413},
		{"ok", `{"clusterYaml":"kind: Node\nmetadata: {name: n}\nstatus: {allocatable: {cpu: \"1\", memory: 1Gi}}","desiredYaml":"kind: Service\nmetadata: {name: s}"}`, 200},
	}
	for _, c := range cases {
		if w := serve(t, "POST", "/api/v1/simulate", c.body); w.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, w.Code, c.want, w.Body)
		}
	}
}

func TestSimulateRejectsWrongMethod(t *testing.T) {
	if w := serve(t, "GET", "/api/v1/simulate", ""); w.Code != 405 && w.Code != 404 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestVersionAndReadyz(t *testing.T) {
	h := Server{Version: "1.2.3", Web: http.NotFoundHandler()}.Handler()
	for path, want := range map[string]string{"/api/v1/version": `"version":"1.2.3"`, "/readyz": `"ready":true`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	w := serve(t, "GET", "/api/v1/version", "")
	if !strings.Contains(w.Body.String(), `"version":"dev"`) {
		t.Errorf("default version: %s", w.Body)
	}
}

func TestBodyLimitBoundary(t *testing.T) {
	base := `{"clusterYaml":"kind: Node\nmetadata: {name: n}","desiredYaml":"kind: Service\nmetadata: {name: s}"}`
	exact := base + strings.Repeat(" ", MaxBodyBytes-len(base))
	if w := serve(t, "POST", "/api/v1/simulate", exact); w.Code != 200 {
		t.Errorf("body at the limit: got %d", w.Code)
	}
	if w := serve(t, "POST", "/api/v1/simulate", exact+" "); w.Code != 413 {
		t.Errorf("body over the limit: got %d", w.Code)
	}
}

func TestServerTimeouts(t *testing.T) {
	s := NewHTTPServer(":0", http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 || s.MaxHeaderBytes <= 0 {
		t.Fatalf("missing limits: %+v", s)
	}
}

func TestStalledClientIsDropped(t *testing.T) {
	s := NewHTTPServer("", Server{Web: http.NotFoundHandler()}.Handler())
	s.ReadHeaderTimeout = 100 * time.Millisecond
	ts := httptest.NewUnstartedServer(s.Handler)
	ts.Config.ReadHeaderTimeout = s.ReadHeaderTimeout
	ts.Start()
	defer ts.Close()
	c, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("POST /api/v1/simulate HTTP/1.1\r\nHost: x\r\n")) // never finishes headers
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("server did not close the stalled connection: %v", err)
	}
}

func TestSimulateRisksDeterministicOrder(t *testing.T) {
	desired := "kind: Deployment\nmetadata: {name: a}\nspec:\n  template:\n    spec:\n      hostNetwork: true\n      containers:\n        - {name: c, securityContext: {privileged: true}}\n---\nkind: ClusterRoleBinding\nmetadata: {name: b}\nroleRef: {name: cluster-admin}\n"
	body, _ := json.Marshal(map[string]string{"clusterYaml": "kind: Node\nmetadata: {name: n}\nstatus: {allocatable: {cpu: \"8\", memory: 8Gi}}", "desiredYaml": desired})
	var first string
	for i := 0; i < 5; i++ {
		w := serve(t, "POST", "/api/v1/simulate", string(body))
		var out struct {
			Risks []struct{ Severity, Code string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Risks) < 2 || out.Risks[0].Severity != "critical" {
			t.Fatalf("%v %s", err, w.Body)
		}
		var codes []string
		for _, r := range out.Risks {
			codes = append(codes, r.Severity+":"+r.Code)
		}
		if got := strings.Join(codes, ","); i == 0 {
			first = got
		} else if got != first {
			t.Fatalf("order changed: %s vs %s", got, first)
		}
	}
}
