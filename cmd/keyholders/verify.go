package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/registry"
)

// runVerify checks the ingested graph against its source and shows that it answers the question the
// ingest exists to make answerable.
//
// The version count check matters because the write path can fail quietly in one specific way: an
// edge statement is UNWIND MATCH, and a MATCH that finds neither endpoint writes nothing and reports
// no error. Counting HAS_VERSION edges per package against the source timeline is what catches that.
func runVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	list := fs.String("packages", "packages.txt", "file of package names to sample from")
	sample := fs.Int("sample", 20, "how many packages to cross-check against deps.dev")
	top := fs.Int("top", 15, "how many maintainers to show")
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
	db, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	names, err := readNames(*list)
	if err != nil {
		return err
	}
	if *sample < len(names) {
		names = names[:*sample]
	}

	if err := crossCheck(ctx, db, reg, names); err != nil {
		return err
	}
	return topMaintainers(ctx, db, *top)
}

func crossCheck(ctx context.Context, db *graph.Client, reg *registry.Client, names []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "package\tin graph\tat deps.dev\t")

	agree, checked := 0, 0
	for _, name := range names {
		urn := graph.PackageURN("npm", name)
		recs, err := db.Query(ctx, fmt.Sprintf(
			`MATCH (p:Package {id: %d})-[:HAS_VERSION]->(v) RETURN count(*) AS n`, graph.ID(urn)), nil)
		if err != nil {
			return fmt.Errorf("count versions of %s: %w", name, err)
		}
		if len(recs) == 0 {
			continue
		}
		inGraph, _ := recs[0].Get("n")

		releases, err := reg.Timeline(ctx, name)
		if err != nil {
			continue
		}
		// Versions with no publish time are not written, because nothing temporal can be said about
		// them, so the source count has to exclude them too or the comparison is not like for like.
		dated := 0
		for _, r := range releases {
			if !r.PublishedAt.IsZero() {
				dated++
			}
		}

		checked++
		mark := ""
		if n, ok := inGraph.(int64); ok && int(n) == dated {
			agree++
		} else {
			mark = "  <- differs"
		}
		fmt.Fprintf(w, "%s\t%v\t%d\t%s\n", name, inGraph, dated, mark)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\n%d of %d packages match the source version count\n\n", agree, checked)
	return nil
}

// topMaintainers is the question step 3 exists to make answerable: which accounts hold publish
// rights over the most of the ranked set. It is a plain traversal because the ingest materialized
// the human layer as edges.
func topMaintainers(ctx context.Context, db *graph.Client, top int) error {
	// There is no min or max aggregate, so a ranking is ORDER BY with LIMIT. Maintainer is well
	// under the 250,000 candidate ceiling on label scans, which a whole-graph pass over Version
	// would not be.
	recs, err := db.Query(ctx, fmt.Sprintf(
		`MATCH (m:Maintainer)-[:MAINTAINS]->(p:Package)
     RETURN m.handle AS handle, count(*) AS packages
     ORDER BY packages DESC LIMIT %d`, top), nil)
	if err != nil {
		return fmt.Errorf("rank maintainers: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "maintainer\tpackages held\t")
	for _, r := range recs {
		handle, _ := r.Get("handle")
		count, _ := r.Get("packages")
		fmt.Fprintf(w, "%v\t%v\t\n", handle, count)
	}
	return w.Flush()
}
