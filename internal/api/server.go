// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"encoding/json"
	"errors"
	"github.com/zyvorai/kairo/internal/sim"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// MaxBodyBytes caps a simulate request body.
const MaxBodyBytes = 4 << 20

// NewHTTPServer returns an http.Server with the read/write/idle limits Kairo
// serves with, so slow or stalled clients cannot hold connections open.
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
}

type Server struct {
	Version        string
	Log            *slog.Logger
	Web            http.Handler
	ExampleCluster string
	ExampleDesired string
}

// Route is one registered HTTP endpoint. Routes() is the single source of
// truth for the mux and for the OpenAPI consistency test.
type Route struct {
	Method, Path string
}

type route struct {
	Route
	h http.HandlerFunc
}

func (s Server) routes() []route {
	return []route{
		{Route{"GET", "/healthz"}, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"ok": true, "service": "kairo", "time": time.Now().UTC()})
		}},
		{Route{"GET", "/readyz"}, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"ready": true, "service": "kairo"})
		}},
		{Route{"GET", "/api/v1/version"}, func(w http.ResponseWriter, r *http.Request) {
			v := s.Version
			if v == "" {
				v = "dev"
			}
			writeJSON(w, 200, map[string]string{"service": "kairo", "version": v})
		}},
		{Route{"GET", "/api/v1/examples"}, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]string{"clusterYaml": s.ExampleCluster, "desiredYaml": s.ExampleDesired})
		}},
		{Route{"POST", "/api/v1/simulate"}, s.simulate},
		{Route{"POST", "/api/v1/drain"}, s.drain},
		{Route{"POST", "/api/v1/consolidate"}, s.consolidate},
	}
}

// Routes lists the API endpoints (the web UI catch-all is not included).
func (s Server) Routes() []Route {
	var out []Route
	for _, r := range s.routes() {
		out = append(out, r.Route)
	}
	return out
}

func (s Server) Handler() http.Handler {
	m := http.NewServeMux()
	for _, r := range s.routes() {
		m.HandleFunc(r.Method+" "+r.Path, r.h)
	}
	m.Handle("/", s.Web)
	return logging(s.Log, m)
}

// decode reads a size-limited JSON body into v, writing the error response
// itself and returning false on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, 413, map[string]string{"error": "request too large"})
		} else {
			writeJSON(w, 400, map[string]string{"error": "could not read request body"})
		}
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}

func (s Server) simulate(w http.ResponseWriter, r *http.Request) {
	var req sim.Request
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ClusterYAML) == "" || strings.TrimSpace(req.DesiredYAML) == "" {
		writeJSON(w, 400, map[string]string{"error": "clusterYaml and desiredYaml are required"})
		return
	}
	out, err := sim.Simulate(req)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (s Server) drain(w http.ResponseWriter, r *http.Request) {
	var req sim.DrainRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ClusterYAML) == "" || len(req.Nodes) == 0 {
		writeJSON(w, 400, map[string]string{"error": "clusterYaml and nodes are required"})
		return
	}
	out, err := sim.Drain(req)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (s Server) consolidate(w http.ResponseWriter, r *http.Request) {
	var req sim.ConsolidateRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ClusterYAML) == "" {
		writeJSON(w, 400, map[string]string{"error": "clusterYaml is required"})
		return
	}
	out, err := sim.Consolidate(req)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func logging(log *slog.Logger, next http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}
