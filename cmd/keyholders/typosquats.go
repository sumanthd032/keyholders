package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/typosquat"
)

func runTyposquats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("typosquats", flag.ExitOnError)
	list := fs.String("packages", "packages.txt", "file of ranked package names, most downloaded first")
	limit := fs.Int("limit", 0, "consider only the first N names, 0 for all")
	maxDistance := fs.Int("distance", 2, "maximum edit distance between normalized names")
	batch := fs.Int("batch", graph.DefaultBatch, "UNWIND rows per statement")
	top := fs.Int("top", 20, "how many candidates to print")
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

	// The file is already ranked by download count, most downloaded first, the same ordering ingest
	// reads to set Package.rank, so position in the file is the rank without a second data source.
	ranks := make(map[string]int, len(names))
	for i, n := range names {
		ranks[n] = i + 1
	}

	fmt.Fprintf(os.Stderr, "comparing %d package names at edit distance %d or less\n", len(names), *maxDistance)
	cands := typosquat.Find(names, ranks, *maxDistance)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	written, err := typosquat.Write(ctx, db, cands, *batch)
	if err != nil {
		return err
	}

	reportTyposquats(cands, written, *top)
	return nil
}

func reportTyposquats(cands []typosquat.Candidate, written, top int) {
	sort.Slice(cands, func(i, j int) bool { return cands[i].PopularityRatio > cands[j].PopularityRatio })

	fmt.Printf("\n%d candidates found, %d written as TYPOSQUAT_OF edges\n\n", len(cands), written)
	if len(cands) == 0 {
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  lookalike\tresembles\tdistance\tpopularity ratio\t")
	for i, c := range cands {
		if i >= top {
			fmt.Fprintf(w, "  ... and %d more\t\t\t\t\n", len(cands)-top)
			break
		}
		fmt.Fprintf(w, "  %s\t%s\t%d\t%.0fx\t\n", c.Lookalike, c.Popular, c.Distance, c.PopularityRatio)
	}
	w.Flush()
}
