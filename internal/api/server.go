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

func (s Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "service": "kairo", "time": time.Now().UTC()})
	})
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ready": true, "service": "kairo"})
	})
	m.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		v := s.Version
		if v == "" {
			v = "dev"
		}
		writeJSON(w, 200, map[string]string{"service": "kairo", "version": v})
	})
	m.HandleFunc("GET /api/v1/examples", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"clusterYaml": s.ExampleCluster, "desiredYaml": s.ExampleDesired})
	})
	m.HandleFunc("POST /api/v1/simulate", s.simulate)
	m.HandleFunc("POST /api/v1/drain", s.drain)
	m.Handle("/", s.Web)
	return logging(s.Log, m)
}
func (s Server) simulate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, 413, map[string]string{"error": "request too large"})
		} else {
			writeJSON(w, 400, map[string]string{"error": "could not read request body"})
		}
		return
	}
	var req sim.Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, 413, map[string]string{"error": "request too large"})
		} else {
			writeJSON(w, 400, map[string]string{"error": "could not read request body"})
		}
		return
	}
	var req sim.DrainRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
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
