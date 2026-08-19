// Command bench-graph-size prints the node and edge counts docs/BENCHMARKS.md reports. A full label
// scan on Version, and a full edge type count on DEPENDS_ON or RESOLVES_TO, both exceed limits this
// HydraDB build enforces (FINDINGS.md findings 10 and 11), so those rows are reported as unmeasurable
// rather than guessed at.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bench-graph-size: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(ctx)

	labels := []string{"Package", "Maintainer", "Advisory"}
	for _, label := range labels {
		stmt := fmt.Sprintf("MATCH (n:%s) RETURN count(*) AS c", label)
		recs, err := db.Query(ctx, stmt, nil)
		if err != nil {
			fmt.Printf("%-12s error: %v\n", label, err)
			continue
		}
		c, _ := recs[0].Get("c")
		fmt.Printf("%-12s %v\n", label, c)
	}

	fmt.Println("Version      not measurable: full label scan exceeds the 250,000 candidate cap (finding 10)")
	fmt.Println("AFFECTS      not measurable: full edge type count exceeds the query timeout (finding 11); docs/BENCHMARKS.md reports the count recorded at ingest time instead")
	return nil
}
