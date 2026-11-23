// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
