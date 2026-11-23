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
	"strings"
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
	case "drain":
		drain(os.Args[2:])
	case "consolidate":
		consolidate(os.Args[2:])
	case "version":
		fmt.Println("kairo", version)
	default:
		usage()
		os.Exit(2)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "Kairo — know the blast radius before you deploy\n\nUsage:\n  kairo serve [-addr :8080]\n  kairo plan -cluster snapshot.yaml -f desired.yaml [-format text|json|markdown] [-fail-on BLOCK|REVIEW|never] [-explain] [-target-version 1.29] [-node-hour-price P] [-gib-hour-price P] [-currency USD]\n  kairo drain -cluster snapshot.yaml -node n1[,n2] [-format text|json] [-fail-on BLOCK|REVIEW|never]\n  kairo consolidate -cluster snapshot.yaml [-format text|json] [-node-hour-price P] [-gib-hour-price P] [-currency USD]\n  kairo version")
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
	target := fs.String("target-version", "", "Kubernetes version manifests must be valid on, e.g. 1.29 (default: newest known)")
	prices := priceFlags(fs)
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
	r, err := sim.Simulate(sim.Request{ClusterYAML: string(cluster), DesiredYAML: string(desired), TargetVersion: *target, Prices: prices()})
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
		if r.Cost != nil {
			fmt.Println(sim.CostText(r.Cost))
		}
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
func drain(args []string) {
	fs := flag.NewFlagSet("drain", flag.ExitOnError)
	clusterPath := fs.String("cluster", "", "cluster snapshot YAML/JSON")
	nodes := fs.String("node", "", "node(s) to drain, comma-separated")
	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "BLOCK", "exit 3 when the verdict is at least this: BLOCK, REVIEW or never")
	_ = fs.Parse(args)
	threshold, ok := map[string]int{"BLOCK": 2, "REVIEW": 1, "never": 3}[*failOn]
	if !ok || (*format != "text" && *format != "json") {
		fmt.Fprintln(os.Stderr, "error: -format must be text or json; -fail-on must be BLOCK, REVIEW or never")
		os.Exit(2)
	}
	if *clusterPath == "" || *nodes == "" {
		fs.Usage()
		os.Exit(2)
	}
	cluster, err := os.ReadFile(*clusterPath)
	if err != nil {
		fatal(err)
	}
	r, err := sim.Drain(sim.DrainRequest{ClusterYAML: string(cluster), Nodes: strings.Split(*nodes, ",")})
	if err != nil {
		fatal(err)
	}
	if *format == "json" {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Print(sim.DrainText(r))
	}
	if sim.Rank(r.Verdict) >= threshold {
		os.Exit(3)
	}
}
func consolidate(args []string) {
	fs := flag.NewFlagSet("consolidate", flag.ExitOnError)
	clusterPath := fs.String("cluster", "", "cluster snapshot YAML/JSON")
	format := fs.String("format", "text", "output format: text or json")
	prices := priceFlags(fs)
	_ = fs.Parse(args)
	if *format != "text" && *format != "json" {
		fmt.Fprintln(os.Stderr, "error: -format must be text or json")
		os.Exit(2)
	}
	if *clusterPath == "" {
		fs.Usage()
		os.Exit(2)
	}
	cluster, err := os.ReadFile(*clusterPath)
	if err != nil {
		fatal(err)
	}
	r, err := sim.Consolidate(sim.ConsolidateRequest{ClusterYAML: string(cluster), Prices: prices()})
	if err != nil {
		fatal(err)
	}
	if *format == "json" {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Print(sim.ConsolidateText(r))
	}
}

// priceFlags registers the optional cost-estimate flags and returns a
// function yielding the prices, or nil when none were given.
func priceFlags(fs *flag.FlagSet) func() *sim.Prices {
	node := fs.Float64("node-hour-price", 0, "optional price of one node-hour; enables a monthly cost estimate")
	gib := fs.Float64("gib-hour-price", 0, "optional price of one GiB-hour of memory (only if billed separately from nodes)")
	cur := fs.String("currency", "", "label for the prices, e.g. USD")
	return func() *sim.Prices {
		if *node == 0 && *gib == 0 {
			return nil
		}
		return &sim.Prices{Currency: *cur, NodeHour: *node, GiBHour: *gib}
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
