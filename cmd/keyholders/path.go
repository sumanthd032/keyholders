package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
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

	held, ok := holdingsIn(audit, handle)
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

	chains, err := chainsTo(ctx, auditor, audit, targets, *depth)
	if err != nil {
		return err
	}
	if *asJSON {
		return emitPathJSON(handle, held, chains)
	}
	reportPaths(handle, lf.Project, held, chains)
	return nil
}

// holdingsIn lists the packages an account holds in one audited tree, widest reach first so the
// default single chain is to the package that matters most.
func holdingsIn(a query.Audit, handle string) ([]string, bool) {
	for _, k := range a.Keyholders {
		if k.Handle != handle {
			continue
		}
		held := make([]string, 0, len(k.Through))
		for pkg := range k.Through {
			held = append(held, pkg)
		}
		sort.Strings(held)
		return held, len(held) > 0
	}
	return nil, false
}

// chain pairs a package with the proof that the project reaches it, or with nothing when the
// procedure found no coexistence path. A missing chain is reported rather than skipped: the roster
// says this account holds the package, so a proof that cannot be produced is worth seeing.
type chain struct {
	Package string
	Chain   query.Chain
	Found   bool
}

func chainsTo(ctx context.Context, auditor *query.Auditor, a query.Audit, packages []string, depth int) ([]chain, error) {
	sources := lockedVersions(a)

	// The reached versions of each package, so a proof can be asked for against a concrete version
	// rather than against the package, which the procedure has no way to search for.
	reached := map[string][]string{}
	for urn := range a.Reach.Coexistence {
		if pkg := query.PackageURNOf(urn); pkg != "" {
			reached[pkg] = append(reached[pkg], urn)
		}
	}

	out := make([]chain, 0, len(packages))
	for _, pkg := range packages {
		versions := reached[pkg]
		sort.Strings(versions)

		c := chain{Package: pkg}
		for _, target := range versions {
			found, ok, err := auditor.ProofPath(ctx, sources, target, depth)
			if err != nil {
				return nil, err
			}
			if ok && (!c.Found || len(found.Hops) < len(c.Chain.Hops)) {
				c.Chain, c.Found = found, true
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func reportPaths(handle, project string, held []string, chains []chain) {
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

func emitPathJSON(handle string, held []string, chains []chain) error {
	type hop struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Range string `json:"range"`
		From_ int64  `json:"valid_from"`
		To_   int64  `json:"valid_to"`
	}
	type entry struct {
		Package string `json:"package"`
		Found   bool   `json:"found"`
		From    int64  `json:"valid_from,omitempty"`
		To      int64  `json:"valid_to,omitempty"`
		Hops    []hop  `json:"hops,omitempty"`
	}
	out := struct {
		Handle string   `json:"handle"`
		Holds  []string `json:"holds"`
		Paths  []entry  `json:"paths"`
	}{Handle: handle, Holds: held}

	for _, c := range chains {
		e := entry{Package: c.Package, Found: c.Found}
		if c.Found {
			e.From, e.To = c.Chain.Valid.From, c.Chain.Valid.To
			for _, h := range c.Chain.Hops {
				e.Hops = append(e.Hops, hop{
					From: h.From, To: h.To, Range: h.Range,
					From_: h.Valid.From, To_: h.Valid.To,
				})
			}
		}
		out.Paths = append(out.Paths, e)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
