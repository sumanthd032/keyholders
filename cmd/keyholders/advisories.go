package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sumanthd032/keyholders/internal/advisory"
	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
)

func runAdvisories(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("advisories", flag.ExitOnError)
	maxAge := fs.Duration("max-age", 6*time.Hour, "reuse a cached copy of the OSV feed younger than this")
	batch := fs.Int("batch", graph.DefaultBatch, "UNWIND rows per statement")
	if err := fs.Parse(args); err != nil {
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

	fmt.Fprintln(os.Stderr, "fetching OSV's npm advisory feed")
	records, err := advisory.Fetch(ctx, cfg.CacheDir, *maxAge)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "parsed %d advisories, matching against ingested timelines\n", len(records))

	ing := advisory.New(db, advisory.GraphSource{DB: db}, advisory.Options{Batch: *batch})
	stats, err := ing.Run(ctx, records)
	reportAdvisories(stats)
	return err
}

func reportAdvisories(s advisory.Stats) {
	fmt.Printf(`
advisories  %d written
affected    %d package mentions, %d with no ingested timeline to attach to
edges       %d AFFECTS
elapsed     %s
`, s.Advisories, s.Affected, s.NoTimeline, s.Edges, s.Elapsed.Round(time.Second))
}
