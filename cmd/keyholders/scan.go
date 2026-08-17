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

	reportRoster(a, top)
	reportIrreplaceable(a)
	reportWeights(a.Weights)
}

func reportRoster(a query.Audit, top int) {
	fmt.Printf("\n  keyholders by risk\n\n")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  account\trisk\tpackages\tsolo\tno prov\tinstall\tlast publish\tsince\t")

	for i, k := range a.Keyholders {
		if i >= top {
			fmt.Fprintf(w, "  ... and %d more\t\t\t\t\t\t\t\t\n", len(a.Keyholders)-top)
			break
		}
		s := a.Signals[k.Handle]
		fmt.Fprintf(w, "  %s\t%.2f\t%d\t%s\t%s\t%s\t%s\t%s\t\n",
			k.Handle, a.Scores[k.Handle].Total, k.Packages(),
			fraction(s.Solo), fraction(s.NoProvenance), fraction(s.InstallScript),
			day(s.LastPublish), day(k.Since))
	}
	w.Flush()
}

// fraction prints a proportion as its raw counts, never as a bare percentage. Three of five and
// three hundred of five hundred are both sixty percent and are not equally worth acting on, and a
// term with nothing behind it has to be visibly empty rather than look like zero.
func fraction(f query.Fraction) string {
	if !f.Known() {
		return "-"
	}
	return fmt.Sprintf("%d/%d", f.Count, f.Of)
}

func day(t int64) string {
	if t <= 0 {
		return "-"
	}
	return time.Unix(t, 0).UTC().Format("2006-01-02")
}

// reportIrreplaceable lists the accounts you cannot route around. Every account is the only route to
// its own packages, so the list is the ones that also take something downstream with them.
func reportIrreplaceable(a query.Audit) {
	var chokepoints []query.Cut
	for _, c := range a.Cuts {
		if c.Irreplaceable() {
			chokepoints = append(chokepoints, c)
		}
	}
	if len(chokepoints) == 0 {
		fmt.Printf("\n  no account is the only route to a package it does not already control\n")
		return
	}

	fmt.Printf("\n  irreplaceable: removing these costs reach you cannot get back another way\n\n")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  account\tcontrols\tpackages lost\tof which downstream\t")
	for i, c := range chokepoints {
		if i >= 10 {
			fmt.Fprintf(w, "  ... and %d more\t\t\t\t\n", len(chokepoints)-10)
			break
		}
		fmt.Fprintf(w, "  %s\t%d\t%d\t%s\t\n", c.Handle, c.Controls, c.Packages,
			strings.TrimPrefix(strings.Join(names(c.Orphaned, 3), ", "), "pkg:npm/"))
	}
	w.Flush()
}

// names shortens a URN list for printing, keeping the count honest when it is cut.
func names(urns []string, keep int) []string {
	out := make([]string, 0, keep+1)
	for i, u := range urns {
		if i == keep {
			out = append(out, fmt.Sprintf("and %d more", len(urns)-keep))
			break
		}
		out = append(out, strings.TrimPrefix(u, "pkg:npm/"))
	}
	return out
}

// reportWeights prints the formula that produced the ranking. A security tool whose ranking cannot
// be audited is a security tool nobody should use, so this is not optional output.
func reportWeights(w query.Weights) {
	fmt.Printf("\n  risk = %.2f reach + %.2f staleness + %.2f solo + %.2f no-provenance + %.2f install-script\n",
		w.Reach, w.Staleness, w.Solo, w.NoProvenance, w.InstallScript)
	fmt.Printf("  terms with nothing measured are dropped and their weight shared out, so a score\n")
	fmt.Printf("  built from fewer terms is not the same as a low score\n")
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

// emitJSON writes the whole audit, including the decomposed score and the removal analysis.
//
// The terms are emitted individually rather than as the total alone, and every fraction keeps its
// denominator, so a consumer can re-derive the ranking or re-weight it. A score handed over as one
// number is not auditable by whoever receives it, which defeats the reason it is a formula.
func emitJSON(a query.Audit, elapsed time.Duration) error {
	r := counts(a)
	type fractionJSON struct {
		Count int  `json:"count"`
		Of    int  `json:"of"`
		Known bool `json:"known"`
	}
	type termJSON struct {
		Name         string  `json:"name"`
		Value        float64 `json:"value"`
		Weight       float64 `json:"weight"`
		Known        bool    `json:"known"`
		Contribution float64 `json:"contribution"`
		Detail       string  `json:"detail"`
	}
	type keyholder struct {
		Handle        string       `json:"handle"`
		Packages      int          `json:"packages"`
		Holds         []string     `json:"holds"`
		Since         int64        `json:"since,omitempty"`
		Risk          float64      `json:"risk"`
		Terms         []termJSON   `json:"risk_terms"`
		Solo          fractionJSON `json:"solo"`
		NoProvenance  fractionJSON `json:"no_provenance"`
		InstallScript fractionJSON `json:"install_script"`
		LastPublish   int64        `json:"last_publish,omitempty"`
		LastRelease   int64        `json:"last_release,omitempty"`
	}
	type cutJSON struct {
		Handle        string   `json:"handle"`
		Controls      int      `json:"controls"`
		PackagesLost  int      `json:"packages_lost"`
		VersionsLost  int      `json:"versions_lost"`
		Downstream    int      `json:"downstream_lost"`
		Orphaned      []string `json:"orphaned"`
		Irreplaceable bool     `json:"irreplaceable"`
	}
	out := struct {
		Project              string         `json:"project"`
		Format               string         `json:"format"`
		Pins                 int            `json:"pins"`
		Sources              int            `json:"sources_in_graph"`
		Keyholders           []keyholder    `json:"keyholders"`
		KeyholderCount       int            `json:"keyholder_count"`
		UnionKeyholders      int            `json:"union_keyholder_count"`
		PhantomKeyholders    int            `json:"phantom_keyholder_count"`
		PackagesReached      int            `json:"packages_reached"`
		UnionPackagesReached int            `json:"union_packages_reached"`
		VersionsReached      int            `json:"versions_reached"`
		UnionVersionsReached int            `json:"union_versions_reached"`
		Cuts                 []cutJSON      `json:"cuts"`
		Weights              query.Weights  `json:"weights"`
		Depth                int            `json:"depth"`
		Truncated            bool           `json:"truncated"`
		Kappa                float64        `json:"kappa"`
		ElapsedMS            int64          `json:"elapsed_ms"`
	}{
		Project: a.Project, Format: a.Format, Pins: a.Pins, Sources: a.Sources,
		KeyholderCount: len(a.Keyholders), UnionKeyholders: a.UnionKeyholders,
		PhantomKeyholders: a.PhantomKeyholders(),
		PackagesReached:   r.coPackages, UnionPackagesReached: r.unionPackages,
		VersionsReached: r.coVersions, UnionVersionsReached: r.unionVersions,
		Weights: a.Weights,
		Depth:   a.Reach.Depth, Truncated: a.Reach.Truncated,
		Kappa: a.Reach.Kappa, ElapsedMS: elapsed.Milliseconds(),
	}

	frac := func(f query.Fraction) fractionJSON {
		return fractionJSON{Count: f.Count, Of: f.Of, Known: f.Known()}
	}
	for _, k := range a.Keyholders {
		s, score := a.Signals[k.Handle], a.Scores[k.Handle]
		e := keyholder{
			Handle: k.Handle, Packages: k.Packages(), Since: k.Since, Risk: score.Total,
			Solo: frac(s.Solo), NoProvenance: frac(s.NoProvenance),
			InstallScript: frac(s.InstallScript),
			LastPublish:   s.LastPublish, LastRelease: s.LastRelease,
		}
		for pkg := range k.Through {
			e.Holds = append(e.Holds, pkg)
		}
		sort.Strings(e.Holds)
		for _, t := range score.Terms {
			e.Terms = append(e.Terms, termJSON{
				Name: t.Name, Value: t.Value, Weight: t.Weight, Known: t.Known,
				Contribution: t.Contribution(), Detail: t.Detail,
			})
		}
		out.Keyholders = append(out.Keyholders, e)
	}
	for _, c := range a.Cuts {
		out.Cuts = append(out.Cuts, cutJSON{
			Handle: c.Handle, Controls: c.Controls, PackagesLost: c.Packages,
			VersionsLost: c.Versions, Downstream: c.Beyond, Orphaned: c.Orphaned,
			Irreplaceable: c.Irreplaceable(),
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
