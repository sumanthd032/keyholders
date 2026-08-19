package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/lockfile"
	"github.com/sumanthd032/keyholders/internal/query"
)

func runPath(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("path", flag.ExitOnError)
	at := fs.String("at", "", "resolve as of this date (YYYY-MM-DD), default is every instant")
	depth := fs.Int("depth", 12, "maximum dependency hops to follow")
	all := fs.Bool("all", false, "show a chain to every package they hold, not just the first")
	asJSON := fs.Bool("json", false, "emit the chains as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: keyholders path [flags] <handle> <lockfile>")
	}
	handle := strings.TrimPrefix(fs.Arg(0), "@")
	path := fs.Arg(1)

	opts := query.Options{MaxDepth: *depth, Within: query.Always}
	if *at != "" {
		t, err := time.Parse("2006-01-02", *at)
		if err != nil {
			return fmt.Errorf("parse --at: %w", err)
		}
		opts.Within = query.At(t.UTC().Unix())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read lockfile: %w", err)
	}
	lf, err := lockfile.Parse(path, data)
	if err != nil {
		return err
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

	auditor := query.NewAuditor(db)
	audit, err := auditor.Audit(ctx, lf, opts)
	if err != nil {
		return err
	}

	held, ok := query.HeldPackages(audit, handle)
	if !ok {
		fmt.Printf("\n  %s holds no key to this project.\n", handle)
		if audit.Sources == 0 {
			fmt.Printf("  None of the %d locked packages are in the graph, so nothing was searched.\n", audit.Pins)
		}
		return nil
	}

	targets := held
	if !*all {
		targets = held[:1]
	}

	chains, err := auditor.HolderChains(ctx, audit, targets, *depth)
	if err != nil {
		return err
	}
	if *asJSON {
		return emitPathJSON(handle, held, chains)
	}
	reportPaths(handle, lf.Project, held, chains)
	return nil
}

func reportPaths(handle, project string, held []string, chains []query.PackageChain) {
	fmt.Printf("\n%s -> %s\n", handle, project)
	fmt.Printf("%s\n", strings.Repeat("-", 64))
	fmt.Printf("\n  %s can publish to %d of the packages this project reaches\n",
		handle, len(held))
	if len(chains) < len(held) {
		fmt.Printf("  showing the chain to %s, pass -all for the rest\n",
			strings.TrimPrefix(chains[0].Package, "pkg:npm/"))
	}

	for _, c := range chains {
		name := strings.TrimPrefix(c.Package, "pkg:npm/")
		if !c.Found {
			fmt.Printf("\n  %s\n    no coexistence path found within the depth bound\n", name)
			continue
		}
		fmt.Printf("\n  %s\n", name)
		fmt.Printf("    every edge below held from %s to %s\n\n",
			day(c.Chain.Valid.From), openOrDate(c.Chain.Valid.To))
		for i, node := range c.Chain.Nodes() {
			if i == 0 {
				fmt.Printf("      %s\n", strings.TrimPrefix(node, "pkg:npm/"))
				continue
			}
			fmt.Printf("        -> %s   (declared %s)\n",
				strings.TrimPrefix(node, "pkg:npm/"), c.Chain.Hops[i-1].Range)
		}
	}
}

func emitPathJSON(handle string, held []string, chains []query.PackageChain) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(query.NewPathView(handle, held, chains))
}
