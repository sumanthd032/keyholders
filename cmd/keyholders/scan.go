package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/lockfile"
	"github.com/sumanthd032/keyholders/internal/query"
)

func runScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	at := fs.String("at", "", "resolve as of this date (YYYY-MM-DD), default is every instant")
	depth := fs.Int("depth", 12, "maximum dependency hops to follow")
	top := fs.Int("top", 20, "how many keyholders to list")
	asJSON := fs.Bool("json", false, "emit the full result as JSON")
	proof := fs.String("proof", "", "show a proof path to this package URN or name")
	record := fs.Bool("record", false, "write the project and its LOCKS edges into the graph")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: keyholders scan [flags] <lockfile>")
	}
	path := fs.Arg(0)

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
	if lf.Project == "" {
		// Neither yarn nor pnpm records the project name, so fall back to something recognisable
		// rather than printing an empty heading.
		lf.Project = strings.TrimSuffix(path, "/"+lf.Format)
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

	if *record {
		// The lockfile's modification time is when the decision was made. Reading it now does not
		// change when the project resolved these versions.
		lockedAt := time.Now().Unix()
		if info, err := os.Stat(path); err == nil {
			lockedAt = info.ModTime().Unix()
		}
		written, err := query.Record(ctx, db, lf, data, lockedAt)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "recorded %s with %d LOCKS edges, locked at %s\n",
			lf.Project, written, time.Unix(lockedAt, 0).UTC().Format("2006-01-02"))
	}

	auditor := query.NewAuditor(db)
	started := time.Now()
	audit, err := auditor.Audit(ctx, lf, opts)
	if err != nil {
		return err
	}
	elapsed := time.Since(started)

	if *asJSON {
		return emitJSON(audit, elapsed)
	}
	reportScan(audit, elapsed, auditor.Reads(), *top, *at)

	if *proof != "" {
		return showProof(ctx, auditor, audit, *proof, *depth)
	}
	return nil
}

func reportScan(a query.Audit, elapsed time.Duration, reads, top int, at string) {
	when := "at any instant"
	if at != "" {
		when = "as of " + at
	}

	fmt.Printf("\n%s  (%s, %s)\n", a.Project, a.Format, when)
	fmt.Printf("%s\n", strings.Repeat("-", 64))

	if a.Sources == 0 {
		fmt.Printf("None of the %d locked packages are in the graph, so there is nothing to report.\n"+
			"Ingest the packages this project depends on first.\n", a.Pins)
		return
	}

	fmt.Printf("\n  %d people can execute code in this project\n\n", len(a.Keyholders))

	// The two counts sit next to each other permanently. The gap is the whole argument, and hiding
	// it would make this look like every other tool's number.
	r := counts(a)
	fmt.Printf("  %-13s %8s %10s %9s\n", "", "keyholders", "packages", "versions")
	fmt.Printf("  coexistence   %8d %10d %9d\n", len(a.Keyholders), r.coPackages, r.coVersions)
	fmt.Printf("  union graph   %8d %10d %9d\n", a.UnionKeyholders, r.unionPackages, r.unionVersions)
	fmt.Printf("  phantom       %8d %10d %9d   (%s of versions)\n",
		a.PhantomKeyholders(), r.unionPackages-r.coPackages, r.unionVersions-r.coVersions,
		percent(r.unionVersions-r.coVersions, r.unionVersions))

	fmt.Printf("\n  coverage      %d of %d locked packages are in the graph\n", a.Sources, a.Pins)
	if a.Sources < a.Pins {
		fmt.Printf("                the rest were not ingested, so this is a lower bound\n")
	}
	if a.Reach.Truncated {
		fmt.Printf("                traversal stopped at the depth bound, so this is a lower bound\n")
	}
	fmt.Printf("  search        depth %d, kappa %.2f intervals per node, %d graph reads, %s\n",
		a.Reach.Depth, a.Reach.Kappa, reads, elapsed.Round(time.Millisecond))

	if len(a.Keyholders) == 0 {
		return
	}

	fmt.Printf("\n  keyholders by reach into this tree\n\n")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  account\tpackages\tsince\t")
	for i, k := range a.Keyholders {
		if i >= top {
			fmt.Fprintf(w, "  ... and %d more\t\t\t\n", len(a.Keyholders)-top)
			break
		}
		since := "unknown"
		if s := k.Since(); s > 0 {
			since = time.Unix(s, 0).UTC().Format("2006-01-02")
		}
		fmt.Fprintf(w, "  %s\t%d\t%s\t\n", k.Handle, k.Packages(), since)
	}
	w.Flush()
}

// reach counts what each semantics reached. Both sides are counted the same way at both
// granularities, because the difference between them shows up at version level first: two versions
// of one package can be reachable through paths that never coexisted while the package itself stays
// reachable through one that did.
type reach struct {
	coPackages, coVersions       int
	unionPackages, unionVersions int
}

func counts(a query.Audit) reach {
	co := map[string]bool{}
	for versionURN := range a.Reach.Coexistence {
		if p := query.PackageURNOf(versionURN); p != "" {
			co[p] = true
		}
	}
	un := map[string]bool{}
	for versionURN := range a.Reach.Union {
		if p := query.PackageURNOf(versionURN); p != "" {
			un[p] = true
		}
	}
	return reach{
		coPackages: len(co), coVersions: len(a.Reach.Coexistence),
		unionPackages: len(un), unionVersions: len(a.Reach.Union),
	}
}

func percent(part, whole int) string {
	if whole == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(part)/float64(whole))
}

func showProof(ctx context.Context, auditor *query.Auditor, a query.Audit, want string, depth int) error {
	target := want
	if !strings.HasPrefix(target, "pkg:") {
		// Accept a bare name and find a reached version of it, so the flag can be typed from the
		// roster without copying a URN.
		prefix := "pkg:npm/" + want + "@"
		for urn := range a.Reach.Coexistence {
			if strings.HasPrefix(urn, prefix) {
				target = urn
				break
			}
		}
	}
	if !strings.HasPrefix(target, "pkg:") {
		return fmt.Errorf("no reached version of %q to prove", want)
	}

	chain, ok, err := auditor.ProofPath(ctx, lockedVersions(a), target, depth)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Printf("\n  no coexistence path to %s\n", target)
		return nil
	}

	fmt.Printf("\n  proof path to %s\n", target)
	fmt.Printf("  every edge below was valid from %s to %s\n\n",
		time.Unix(chain.Valid.From, 0).UTC().Format("2006-01-02"), openOrDate(chain.Valid.To))
	for i, node := range chain.Nodes() {
		if i == 0 {
			fmt.Printf("    %s\n", strings.TrimPrefix(node, "pkg:npm/"))
			continue
		}
		fmt.Printf("      -> %s   (declared %s)\n",
			strings.TrimPrefix(node, "pkg:npm/"), chain.Hops[i-1].Range)
	}
	return nil
}

// lockedVersions is the pins the lockfile named, which are the only honest starting points for a
// proof. Starting from every reached version instead would satisfy the procedure with a single hop
// from whatever happens to sit next to the target, and a one-hop chain out of the middle of the tree
// proves nothing about what this project installs.
func lockedVersions(a query.Audit) []string {
	out := make([]string, 0, len(a.Reach.Sources))
	for _, s := range a.Reach.Sources {
		out = append(out, s.URN)
	}
	sort.Strings(out)
	return out
}

func openOrDate(t int64) string {
	if t >= graph.OpenInterval {
		return "now"
	}
	return time.Unix(t, 0).UTC().Format("2006-01-02")
}

func emitJSON(a query.Audit, elapsed time.Duration) error {
	r := counts(a)
	type keyholder struct {
		Handle   string `json:"handle"`
		Packages int    `json:"packages"`
		Since    int64  `json:"since,omitempty"`
	}
	out := struct {
		Project              string      `json:"project"`
		Format               string      `json:"format"`
		Pins                 int         `json:"pins"`
		Sources              int         `json:"sources_in_graph"`
		Keyholders           []keyholder `json:"keyholders"`
		KeyholderCount       int         `json:"keyholder_count"`
		UnionKeyholders      int         `json:"union_keyholder_count"`
		PhantomKeyholders    int         `json:"phantom_keyholder_count"`
		PackagesReached      int         `json:"packages_reached"`
		UnionPackagesReached int         `json:"union_packages_reached"`
		VersionsReached      int         `json:"versions_reached"`
		UnionVersionsReached int         `json:"union_versions_reached"`
		Depth                int         `json:"depth"`
		Truncated            bool        `json:"truncated"`
		Kappa                float64     `json:"kappa"`
		ElapsedMS            int64       `json:"elapsed_ms"`
	}{
		Project: a.Project, Format: a.Format, Pins: a.Pins, Sources: a.Sources,
		KeyholderCount: len(a.Keyholders), UnionKeyholders: a.UnionKeyholders,
		PhantomKeyholders: a.PhantomKeyholders(),
		PackagesReached:   r.coPackages, UnionPackagesReached: r.unionPackages,
		VersionsReached: r.coVersions, UnionVersionsReached: r.unionVersions,
		Depth: a.Reach.Depth, Truncated: a.Reach.Truncated,
		Kappa: a.Reach.Kappa, ElapsedMS: elapsed.Milliseconds(),
	}
	for _, k := range a.Keyholders {
		out.Keyholders = append(out.Keyholders, keyholder{
			Handle: k.Handle, Packages: k.Packages(), Since: k.Since(),
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
