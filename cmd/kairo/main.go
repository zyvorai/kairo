// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/zyvorai/kairo/internal/api"
	"github.com/zyvorai/kairo/internal/sim"
	webui "github.com/zyvorai/kairo/internal/web"
	"log/slog"
	"net/http"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "plan":
		plan(os.Args[2:])
	case "version":
		fmt.Println("kairo", version)
	default:
		usage()
		os.Exit(2)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "Kairo — know the blast radius before you deploy\n\nUsage:\n  kairo serve [-addr :8080]\n  kairo plan -cluster snapshot.yaml -f desired.yaml [-format text|json|markdown] [-fail-on BLOCK|REVIEW|never] [-explain]\n  kairo version")
}
func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("KAIRO_ADDR", ":8080"), "listen address")
	_ = fs.Parse(args)
	cluster := readOptional("examples/cluster.yaml")
	desired := readOptional("examples/desired.yaml")
	srv := api.Server{Version: version, Log: slog.Default(), Web: webui.Handler(), ExampleCluster: cluster, ExampleDesired: desired}
	httpSrv := api.NewHTTPServer(*addr, srv.Handler())
	slog.Info("kairo listening", "addr", *addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
func plan(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	clusterPath := fs.String("cluster", "", "cluster snapshot YAML/JSON")
	desiredPath := fs.String("f", "", "desired manifest YAML/JSON")
	asJSON := fs.Bool("json", false, "print JSON (same as -format json)")
	format := fs.String("format", "text", "output format: text, json or markdown")
	failOn := fs.String("fail-on", "BLOCK", "exit 3 when the verdict is at least this: BLOCK, REVIEW or never")
	explain := fs.Bool("explain", false, "show what changed per object (replicas, image, resources); adds changes[] to JSON")
	_ = fs.Parse(args)
	if *asJSON {
		*format = "json"
	}
	threshold, ok := map[string]int{"BLOCK": 2, "REVIEW": 1, "never": 3}[*failOn]
	if !ok || (*format != "text" && *format != "json" && *format != "markdown") {
		fmt.Fprintln(os.Stderr, "error: -format must be text, json or markdown; -fail-on must be BLOCK, REVIEW or never")
		os.Exit(2)
	}
	if *clusterPath == "" || *desiredPath == "" {
		fs.Usage()
		os.Exit(2)
	}
	cluster, must := os.ReadFile(*clusterPath)
	if must != nil {
		fatal(must)
	}
	desired, must := os.ReadFile(*desiredPath)
	if must != nil {
		fatal(must)
	}
	r, err := sim.Simulate(sim.Request{ClusterYAML: string(cluster), DesiredYAML: string(desired)})
	if err != nil {
		fatal(err)
	}
	if !*explain {
		r.Changes = nil
	}
	switch *format {
	case "json":
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	case "markdown":
		fmt.Print(sim.Markdown(r))
	default:
		fmt.Println(sim.SummaryText(r))
		if *explain {
			fmt.Println("\nChanges:")
			fmt.Println(sim.ExplainText(r.Changes))
		}
		fmt.Println("\nRecommendations:")
		for _, x := range r.Recommendations {
			fmt.Println("-", x)
		}
	}
	if sim.Rank(r.Verdict) >= threshold {
		os.Exit(3)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "error:", err); os.Exit(1) }
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func readOptional(p string) string { b, _ := os.ReadFile(p); return string(b) }
