package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/query"
)

func runWho(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("who", flag.ExitOnError)
	at := fs.String("at", "", "resolve as of this date (YYYY-MM-DD), default is every instant")
	depth := fs.Int("depth", 12, "maximum dependency hops to follow")
	top := fs.Int("top", 20, "how many packages to list")
	asJSON := fs.Bool("json", false, "emit the full result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: keyholders who [flags] <handle>")
	}
	handle := strings.TrimPrefix(fs.Arg(0), "@")

	opts := query.Options{MaxDepth: *depth, Within: query.Always}
	if *at != "" {
		t, err := time.Parse("2006-01-02", *at)
		if err != nil {
			return fmt.Errorf("parse --at: %w", err)
		}
		opts.Within = query.At(t.UTC().Unix())
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
	started := time.Now()
	who, err := auditor.WhoIs(ctx, handle, opts)
	if err != nil {
		return err
	}
	elapsed := time.Since(started)

	if *asJSON {
		return emitWhoJSON(who, elapsed)
	}
	reportWho(who, elapsed, auditor.Reads(), *top, *at)
	return nil
}

func reportWho(w query.Who, elapsed time.Duration, reads, top int, at string) {
	when := "at any instant"
	if at != "" {
		when = "as of " + at
	}

	fmt.Printf("\n%s  (%s)\n", w.Handle, when)
	fmt.Printf("%s\n", strings.Repeat("-", 64))

	if len(w.Holds) == 0 {
		fmt.Printf("\n  %s holds no packages in the ingested graph.\n", w.Handle)
		return
	}

	versions := 0
	for _, h := range w.Holds {
		versions += h.Versions
	}

	fmt.Printf("\n  %s can publish to %d packages, %d versions deep\n",
		w.Handle, len(w.Holds), versions)
	fmt.Printf("  and reaches %d further packages through them\n\n", len(w.Dependents))

	fmt.Printf("  %-13s %10s %10s\n", "", "packages", "versions")
	fmt.Printf("  coexistence   %10d %10d\n", len(w.Dependents), len(w.Reach.Coexistence))
	fmt.Printf("  union graph   %10d %10d\n", w.UnionDependents, len(w.Reach.Union))
	fmt.Printf("  phantom       %10d %10d\n",
		w.UnionDependents-len(w.Dependents), len(w.Reach.Union)-len(w.Reach.Coexistence))

	if w.Reach.Truncated {
		fmt.Printf("\n  traversal stopped at the depth bound, so these are lower bounds\n")
	}
	fmt.Printf("\n  search        depth %d, kappa %.2f intervals per node, %d graph reads, %s\n",
		w.Reach.Depth, w.Reach.Kappa, reads, elapsed.Round(time.Millisecond))

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\n  packages %s can publish to\n\n", w.Handle)
	fmt.Fprintln(tw, "  package\tversions\theld\t")
	for i, h := range w.Holds {
		if i >= top {
			fmt.Fprintf(tw, "  ... and %d more\t\t\t\n", len(w.Holds)-top)
			break
		}
		fmt.Fprintf(tw, "  %s\t%d\t%s\t\n",
			strings.TrimPrefix(h.Package, "pkg:npm/"), h.Versions, spells(h.Spans))
	}
	tw.Flush()

	if len(w.Dependents) == 0 {
		return
	}
	fmt.Printf("\n  packages that would be running their code\n\n")
	for i, pkg := range w.Dependents {
		if i >= top {
			fmt.Printf("    ... and %d more\n", len(w.Dependents)-top)
			break
		}
		fmt.Printf("    %s\n", strings.TrimPrefix(pkg, "pkg:npm/"))
	}
}

// spells prints the periods an account could publish to a package. Disjoint spells are printed
// separately rather than as one range, because the gap is time they held no key and closing it would
// credit them with reach they did not have.
func spells(s query.Set) string {
	if len(s) == 0 {
		return "-"
	}
	out := make([]string, 0, len(s))
	for _, i := range s {
		out = append(out, fmt.Sprintf("%s to %s", day(i.From), openOrDate(i.To)))
	}
	return strings.Join(out, ", ")
}

func emitWhoJSON(w query.Who, elapsed time.Duration) error {
	type spell struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	}
	type held struct {
		Package  string  `json:"package"`
		Versions int     `json:"versions"`
		Spells   []spell `json:"spells"`
	}
	out := struct {
		Handle          string   `json:"handle"`
		Holds           []held   `json:"holds"`
		Dependents      []string `json:"dependents"`
		DependentCount  int      `json:"dependent_count"`
		UnionDependents int      `json:"union_dependent_count"`
		VersionsReached int      `json:"versions_reached"`
		UnionVersions   int      `json:"union_versions_reached"`
		Depth           int      `json:"depth"`
		Truncated       bool     `json:"truncated"`
		Kappa           float64  `json:"kappa"`
		ElapsedMS       int64    `json:"elapsed_ms"`
	}{
		Handle:     w.Handle,
		Dependents: w.Dependents, DependentCount: len(w.Dependents),
		UnionDependents: w.UnionDependents,
		VersionsReached: len(w.Reach.Coexistence), UnionVersions: len(w.Reach.Union),
		Depth: w.Reach.Depth, Truncated: w.Reach.Truncated,
		Kappa: w.Reach.Kappa, ElapsedMS: elapsed.Milliseconds(),
	}
	for _, h := range w.Holds {
		e := held{Package: h.Package, Versions: h.Versions}
		for _, i := range h.Spans {
			e.Spells = append(e.Spells, spell{From: i.From, To: i.To})
		}
		out.Holds = append(out.Holds, e)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
