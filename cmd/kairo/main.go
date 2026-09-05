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
	"time"
)

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
		fmt.Println("kairo dev")
	default:
		usage()
		os.Exit(2)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "Kairo — know the blast radius before you deploy\n\nUsage:\n  kairo serve [-addr :8080]\n  kairo plan -cluster snapshot.yaml -f desired.yaml [-json]\n  kairo version")
}
func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("KAIRO_ADDR", ":8080"), "listen address")
	_ = fs.Parse(args)
	cluster := readOptional("examples/cluster.yaml")
	desired := readOptional("examples/desired.yaml")
	srv := api.Server{Log: slog.Default(), Web: webui.Handler(), ExampleCluster: cluster, ExampleDesired: desired}
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
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
	asJSON := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
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
	if *asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Println(sim.SummaryText(r))
		fmt.Println("\nRecommendations:")
		for _, x := range r.Recommendations {
			fmt.Println("-", x)
		}
	}
	if r.Verdict == "BLOCK" {
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
