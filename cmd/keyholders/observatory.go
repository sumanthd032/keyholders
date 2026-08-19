package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/ingest"
	"github.com/sumanthd032/keyholders/internal/observatory"
	"github.com/sumanthd032/keyholders/internal/sketch"
)

// epochRun holds one epoch's cold started propagation and aggregation, kept only long enough to
// build the reach curve and to feed the latest epoch's leaderboard, validation and write back. Every
// entry here is a fresh Engine.RunEpoch, never one seeded from the entry before it: that is the whole
// correctness property this step exists to enforce.
type epochRun struct {
	epoch       int64
	packages    map[sketch.NodeID]sketch.Sketch
	maintainers map[sketch.NodeID]sketch.Sketch
	orphaned    []sketch.NodeID
}

func runObservatory(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("observatory", flag.ExitOnError)
	list := fs.String("packages", "packages.txt", "file of package names to cover")
	limit := fs.Int("limit", 0, "cover only the first N names, 0 for all")
	epochCount := fs.Int("epochs", 8, "quarterly sample points")
	precision := fs.Uint("precision", 8, "HyperLogLog register precision")
	batch := fs.Int("batch", graph.DefaultBatch, "UNWIND rows per statement")
	top := fs.Int("top", 20, "leaderboard rows to print")
	sample := fs.Int("validate", 5, "exact-check this many top packages against the estimate, 0 to skip")
	writeGraph := fs.Bool("write", true, "persist PKG_RESOLVES edges and kri back onto the graph")
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

	epochs := make([]int64, 0, *epochCount)
	for _, t := range ingest.Epochs(*epochCount, time.Now()) {
		epochs = append(epochs, t.Unix())
	}

	src := observatory.GraphSource{DB: db}
	fmt.Fprintf(os.Stderr, "building package level snapshots for %d packages across %d epochs\n",
		len(names), len(epochs))
	started := time.Now()
	snapshots, err := observatory.Snapshots(ctx, src, names, epochs)
	if err != nil {
		return fmt.Errorf("snapshots: %w", err)
	}

	if *writeGraph {
		written, err := observatory.WriteSnapshots(ctx, db, snapshots, *batch)
		if err != nil {
			return fmt.Errorf("write PKG_RESOLVES: %w", err)
		}
		fmt.Fprintf(os.Stderr, "wrote %d PKG_RESOLVES edges\n", written)
	}

	newHLL := func() sketch.Sketch { return sketch.NewHLL(*precision) }
	nodes := make([]sketch.NodeID, len(names))
	for i, n := range names {
		nodes[i] = sketch.NodeID(n)
	}
	engine := &sketch.Engine{New: newHLL}
	agg := observatory.Aggregator{Src: src, New: newHLL}

	runs := make([]epochRun, 0, len(epochs))
	for _, e := range epochs {
		// Cold started every call: the engine carries no state from the previous epoch's run, which
		// is the property TestDeletionAcrossEpochs exists to guard. Reusing edges already read into
		// memory, never re-querying the graph per epoch, is what keeps this affordable.
		packages := engine.RunEpoch(nodes, snapshots[e])
		maintainers, orphaned, err := agg.Aggregate(ctx, packages, e)
		if err != nil {
			return fmt.Errorf("aggregate epoch %d: %w", e, err)
		}
		runs = append(runs, epochRun{epoch: e, packages: packages, maintainers: maintainers, orphaned: orphaned})
	}
	elapsed := time.Since(started)

	latest := runs[len(runs)-1]
	pkgURN := func(id sketch.NodeID) string { return graph.PackageURN("npm", string(id)) }
	mntURN := func(id sketch.NodeID) string { return graph.MaintainerURN("npm", string(id)) }

	if *writeGraph {
		pkgWritten, err := observatory.WriteBack(ctx, db, latest.packages, latest.epoch, "Package", pkgURN, *batch)
		if err != nil {
			return fmt.Errorf("write back package kri: %w", err)
		}
		mntWritten, err := observatory.WriteBack(ctx, db, latest.maintainers, latest.epoch, "Maintainer", mntURN, *batch)
		if err != nil {
			return fmt.Errorf("write back maintainer kri: %w", err)
		}
		fmt.Fprintf(os.Stderr, "wrote kri to %d packages and %d maintainers, epoch %s\n",
			pkgWritten, mntWritten, epochDate(latest.epoch))

		// Every epoch's value, not only the latest, so the curve and the orphaned flag both survive
		// past this run rather than being recomputed from scratch each time something wants to read
		// them (see finding 23 for why a self loop is the shape, and D44's note that this was left
		// open until the per-node scalar property stopped being enough).
		for _, run := range runs {
			if _, err := observatory.WriteKRIHistory(ctx, db, run.packages, run.orphaned, run.epoch, "Package", pkgURN, *batch); err != nil {
				return fmt.Errorf("write kri history for packages, epoch %d: %w", run.epoch, err)
			}
			if _, err := observatory.WriteKRIHistory(ctx, db, run.maintainers, nil, run.epoch, "Maintainer", mntURN, *batch); err != nil {
				return fmt.Errorf("write kri history for maintainers, epoch %d: %w", run.epoch, err)
			}
		}
		fmt.Fprintf(os.Stderr, "wrote kri history across %d epochs\n", len(runs))
	}

	reportObservatory(runs, elapsed, len(names), *top)

	if *sample > 0 {
		reportValidation(nodes, snapshots[latest.epoch], latest, *sample, newHLL)
	}
	return nil
}

func epochDate(t int64) string {
	return time.Unix(t, 0).UTC().Format("2006-01-02")
}

func reportObservatory(runs []epochRun, elapsed time.Duration, packages, top int) {
	latest := runs[len(runs)-1]

	fmt.Printf("\nobservatory  %d packages, %d epochs, %s\n", packages, len(runs), elapsed.Round(time.Millisecond))
	fmt.Printf("%s\n", strings.Repeat("-", 64))

	mntBoard := observatory.Leaderboard(latest.maintainers, top)
	fmt.Printf("\n  maintainer reach, as of %s\n\n", epochDate(latest.epoch))
	printLeaderboard(mntBoard)
	if len(runs) > 1 {
		printCurve("maintainer", mntBoard, runs, func(r epochRun) map[sketch.NodeID]sketch.Sketch { return r.maintainers })
	}

	pkgBoard := observatory.Leaderboard(latest.packages, top)
	fmt.Printf("\n  package reach, as of %s\n\n", epochDate(latest.epoch))
	printLeaderboard(pkgBoard)

	orphanBoard := observatory.Orphaned(latest.packages, latest.orphaned, top)
	fmt.Printf("\n  orphaned but load bearing: no maintainer at all, as of %s\n\n", epochDate(latest.epoch))
	if len(latest.orphaned) == 0 {
		fmt.Printf("  every package in this run has at least one maintainer\n")
	} else {
		fmt.Printf("  %d of %d packages, %s, have no maintainer\n\n",
			len(latest.orphaned), packages, percent(len(latest.orphaned), packages))
		printLeaderboard(orphanBoard)
	}
}

func printLeaderboard(entries []observatory.LeaderboardEntry) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  rank\tname\tkri\t")
	for i, e := range entries {
		fmt.Fprintf(w, "  %d\t%s\t%d\t\n", i+1, e.Name, e.KRI)
	}
	w.Flush()
}

// printCurve shows the top entries' reach across every epoch computed this run, not just the latest,
// which is the "as a curve over time" framing the observatory exists to answer rather than a single
// instant's snapshot.
func printCurve(label string, top []observatory.LeaderboardEntry, runs []epochRun,
	pick func(epochRun) map[sketch.NodeID]sketch.Sketch,
) {
	if len(top) == 0 {
		return
	}
	names := make([]string, 0, len(top))
	for _, e := range top {
		if len(names) == 8 {
			break
		}
		names = append(names, e.Name)
	}

	fmt.Printf("\n  %s reach over time\n\n", label)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(w, "  name\t")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t", epochDate(r.epoch))
	}
	fmt.Fprintln(w)
	for _, name := range names {
		fmt.Fprintf(w, "  %s\t", name)
		for _, r := range runs {
			sk, ok := pick(r)[sketch.NodeID(name)]
			if !ok {
				fmt.Fprint(w, "-\t")
				continue
			}
			fmt.Fprintf(w, "%d\t", sk.Count())
		}
		fmt.Fprintln(w)
	}
	w.Flush()
}

func reportValidation(nodes []sketch.NodeID, edges []sketch.Edge, latest epochRun, n int, newEstimator sketch.NewSketch) {
	board := observatory.Leaderboard(latest.packages, n)
	sample := make([]sketch.NodeID, 0, len(board))
	for _, e := range board {
		sample = append(sample, sketch.NodeID(e.Name))
	}

	results := observatory.Validate(nodes, edges, sample, newEstimator)
	sort.Slice(results, func(i, j int) bool { return results[i].Exact > results[j].Exact })

	fmt.Printf("\n  validation: exact fixpoint vs the estimate, top %d packages\n\n", len(results))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  package\texact\testimate\trelative error\t")
	for _, r := range results {
		fmt.Fprintf(w, "  %s\t%d\t%d\t%.1f%%\t\n", r.Package, r.Exact, r.Estimate, 100*r.RelativeError())
	}
	w.Flush()
}
