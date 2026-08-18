// Command keyholders answers how many people can execute code on your machine, and who they are,
// by building the npm package, version and maintainer graph in HydraDB and computing reachability
// over it.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/ingest"
	"github.com/sumanthd032/keyholders/internal/registry"
	"github.com/sumanthd032/keyholders/internal/resolve"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	// Interrupt cancels rather than kills, so the ingest finishes the batch it is writing and the
	// checkpoint stays honest about what reached the graph.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "packages":
		err = runPackages(ctx, os.Args[2:])
	case "ingest":
		err = runIngest(ctx, os.Args[2:])
	case "scan":
		err = runScan(ctx, os.Args[2:])
	case "who":
		err = runWho(ctx, os.Args[2:])
	case "path":
		err = runPath(ctx, os.Args[2:])
	case "resolve":
		err = runResolve(ctx, os.Args[2:])
	case "verify":
		err = runVerify(ctx, os.Args[2:])
	case "observatory":
		err = runObservatory(ctx, os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "keyholders: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `keyholders builds and queries the npm keyholder graph.

Commands:
  scan         audit a lockfile: who can execute code in this project
  who          what one account reaches, and through which packages
  path         the concrete chain from a project to what an account controls
  packages     write the ranked package list the ingest reads
  ingest       build the package, version and maintainer graph in HydraDB
  resolve      materialize RESOLVES_TO edges with their validity windows
  verify       cross-check the graph against deps.dev and rank maintainers by reach
  observatory  reachability for every maintainer across the whole graph, per epoch

Run a command with -h for its flags.
`)
}

func runPackages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("packages", flag.ExitOnError)
	top := fs.Int("top", 50_000, "how many packages to take, by download count")
	out := fs.String("out", "packages.txt", "file to write the ranked names to")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	reg, err := registry.New(cfg.CacheDir, registry.DefaultRate)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "fetching the top %d npm packages by download count\n", *top)
	names, err := reg.TopPackages(ctx, *top)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil && filepath.Dir(*out) != "." {
		return fmt.Errorf("create output dir: %w", err)
	}
	f, err := os.Create(*out)
	if err != nil {
		return fmt.Errorf("create %s: %w", *out, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, name := range names {
		fmt.Fprintln(w, name)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d names to %s\n", len(names), *out)
	return nil
}

func runIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	list := fs.String("packages", "packages.txt", "file of package names, most downloaded first")
	limit := fs.Int("limit", 0, "ingest only the first N names, 0 for all")
	epochs := fs.Int("epochs", 8, "quarterly sample points per package")
	batch := fs.Int("batch", graph.DefaultBatch, "UNWIND rows per statement")
	fetchers := fs.Int("fetchers", 8, "concurrent package fetches")
	deps := fs.Bool("deps", true, "fetch resolved dependency closures")
	rate := fs.Float64("rate", registry.DefaultRate, "sustained HTTP requests per second")
	if err := fs.Parse(args); err != nil {
		return err
	}

	names, err := readNames(*list)
	if err != nil {
		return err
	}
	if *limit > 0 && *limit < len(names) {
		names = names[:*limit]
	}
	if len(names) == 0 {
		return fmt.Errorf("%s holds no package names", *list)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	reg, err := registry.New(cfg.CacheDir, *rate)
	if err != nil {
		return err
	}
	db, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	opts := ingest.Options{
		Epochs:   *epochs,
		Fetchers: *fetchers,
		Batch:    *batch,
		Deps:     *deps,
		Now:      time.Now(),
	}
	fmt.Fprintf(os.Stderr, "ingesting %d packages at %d epochs, batch %d, %g req/s\n",
		len(names), opts.Epochs, opts.Batch, *rate)

	stats, err := ingest.New(reg, db, opts, os.Stderr).Run(ctx, names, cfg.StateDir)
	reportIngest(stats)
	return err
}

func reportIngest(s ingest.Stats) {
	el := s.Elapsed.Seconds()
	if el <= 0 {
		el = 1
	}
	fmt.Printf(`
packages   %d ingested, %d already done, %d unavailable
graph      %d nodes, %d edges
requests   %d at %.1f/s
elapsed    %s at %.0f edges/s
`, s.Packages, s.Skipped, s.Missing, s.Nodes, s.Edges, s.Requests,
		float64(s.Requests)/el, s.Elapsed.Round(time.Second), float64(s.Edges)/el)
}

func readNames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open package list: %w", err)
	}
	defer f.Close()

	var names []string
	seen := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		name := strings.TrimSpace(scan.Text())
		if name == "" || strings.HasPrefix(name, "#") || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("read package list: %w", err)
	}
	return names, nil
}

func runResolve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	list := fs.String("packages", "packages.txt", "file of package names to resolve against")
	limit := fs.Int("limit", 0, "resolve only the first N names, 0 for all")
	batch := fs.Int("batch", graph.DefaultBatch, "UNWIND rows per statement")
	readers := fs.Int("readers", 4, "concurrent target package reads")
	if err := fs.Parse(args); err != nil {
		return err
	}

	names, err := readNames(*list)
	if err != nil {
		return err
	}
	if *limit > 0 && *limit < len(names) {
		names = names[:*limit]
	}
	if len(names) == 0 {
		return fmt.Errorf("%s holds no package names", *list)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	fmt.Fprintf(os.Stderr, "resolving declarations against %d packages, batch %d\n", len(names), *batch)
	opts := resolve.Options{Readers: *readers, Batch: *batch}
	stats, err := resolve.New(db, opts, os.Stderr).Run(ctx, names, cfg.StateDir)
	reportResolve(stats)
	return err
}

func reportResolve(s resolve.Stats) {
	el := s.Elapsed.Seconds()
	if el <= 0 {
		el = 1
	}
	perEdge := 0.0
	if s.Declarations > 0 {
		perEdge = float64(s.Edges) / float64(s.Declarations)
	}
	fmt.Printf(`
packages     %d resolved, %d already done
declarations %d, of which %d never satisfiable, %d unparseable,
             %d pointing outside the ingested set
edges        %d RESOLVES_TO, %.2f windows per declaration
elapsed      %s at %.0f edges/s
`, s.Packages, s.Skipped, s.Declarations, s.Unsatisfied, s.Unparseable, s.NoTimeline,
		s.Edges, perEdge, s.Elapsed.Round(time.Second), float64(s.Edges)/el)
}
