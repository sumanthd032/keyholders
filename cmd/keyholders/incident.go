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
	"github.com/sumanthd032/keyholders/internal/incident"
	"github.com/sumanthd032/keyholders/internal/query"
)

func runIncident(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("incident", flag.ExitOnError)
	at := fs.String("at", "", "evaluate shared-maintainer adjacency as of this date, default now")
	top := fs.Int("top", 20, "how many rows to print in the shared-maintainer and typosquat tables")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: keyholders incident [flags] <package>@<version>")
	}

	name, version, ok := strings.Cut(fs.Arg(0), "@")
	if !ok || name == "" || version == "" {
		return fmt.Errorf("expected <package>@<version>, got %q", fs.Arg(0))
	}

	when := time.Now().Unix()
	if *at != "" {
		t, err := time.Parse("2006-01-02", *at)
		if err != nil {
			return fmt.Errorf("parse --at: %w", err)
		}
		when = t.UTC().Unix()
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

	src := incident.GraphSource{DB: db, Ecosystem: "npm"}
	auditor := query.NewAuditor(db)

	advisories, err := src.AdvisoriesAffecting(ctx, name, version)
	if err != nil {
		return fmt.Errorf("advisories affecting %s@%s: %w", name, version, err)
	}

	var introductions []incident.Introduction
	for _, a := range advisories {
		intro, err := incident.Bisect(ctx, src, a.ID, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip bisecting %s: %v\n", a.ID, err)
			continue
		}
		introductions = append(introductions, intro)
	}

	exposed, err := incident.TransitivelyExposed(ctx, src, auditor, name, version)
	if err != nil {
		return fmt.Errorf("transitively exposed: %w", err)
	}

	live, err := incident.ResolvedWhileLive(ctx, src, name, version)
	if err != nil {
		return fmt.Errorf("resolved while live: %w", err)
	}

	shared, err := incident.SharedMaintainers(ctx, src, name, when)
	if err != nil {
		return fmt.Errorf("shared maintainers: %w", err)
	}

	nearby, err := src.TyposquatsNear(ctx, name)
	if err != nil {
		return fmt.Errorf("typosquats near %s: %w", name, err)
	}

	reportIncident(name, version, advisories, introductions, exposed, live, shared, nearby, *top)
	return nil
}

func reportIncident(
	name, version string,
	advisories []incident.AdvisoryInfo,
	introductions []incident.Introduction,
	exposed []incident.ExposedProject,
	live []incident.Exposure,
	shared []incident.SharedMaintainer,
	nearby []incident.TyposquatNeighbor,
	top int,
) {
	fmt.Printf("\nincident: %s@%s\n", name, version)
	fmt.Printf("%s\n", strings.Repeat("-", 64))

	reportIncidentAdvisories(advisories)
	reportIntroductions(introductions)
	reportExposed(exposed)
	reportResolvedWhileLive(live)
	reportSharedMaintainers(shared, top)
	reportTyposquatNeighbors(nearby, top)
}

// severityRank orders the coarse categories this project stores from most to least urgent, so the
// report reads worst-first. An unrecognised or absent category sorts last rather than erroring: OSV
// does not guarantee every record carries one.
func severityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 0
	case "HIGH":
		return 1
	case "MODERATE", "MEDIUM":
		return 2
	case "LOW":
		return 3
	default:
		return 4
	}
}

func reportIncidentAdvisories(advisories []incident.AdvisoryInfo) {
	fmt.Printf("\n  advisories\n\n")
	if len(advisories) == 0 {
		fmt.Printf("  none found affecting this exact version\n")
		return
	}
	sort.Slice(advisories, func(i, j int) bool {
		return severityRank(advisories[i].Severity) < severityRank(advisories[j].Severity)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  id\tseverity\tsummary\t")
	for _, a := range advisories {
		fmt.Fprintf(w, "  %s\t%s\t%s\t\n", a.ID, orDash(a.Severity), truncateSummary(a.Summary, 70))
	}
	w.Flush()
}

func reportIntroductions(introductions []incident.Introduction) {
	if len(introductions) == 0 {
		return
	}
	fmt.Printf("\n  introduced\n\n")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  version\tpublished\tprevious\tdependency changes\t")
	for _, intro := range introductions {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t\n",
			intro.FirstAffected, day(intro.PublishedAt), orDash(intro.Previous), summarizeDiff(intro.Diff))
	}
	w.Flush()
}

func summarizeDiff(d incident.DependencyDiff) string {
	if len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 {
		return "-"
	}
	return fmt.Sprintf("+%d -%d ~%d", len(d.Added), len(d.Removed), len(d.Changed))
}

func reportExposed(exposed []incident.ExposedProject) {
	fmt.Printf("\n  transitively exposed\n\n")
	if len(exposed) == 0 {
		fmt.Printf("  none of the recorded projects reach this version\n")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  project\tlocked at\t")
	for _, e := range exposed {
		fmt.Fprintf(w, "  %s\t%s\t\n", e.Project, day(e.LockedAt))
	}
	w.Flush()
}

func reportResolvedWhileLive(live []incident.Exposure) {
	fmt.Printf("\n  resolved the compromised version while it was live\n\n")
	if len(live) == 0 {
		fmt.Printf("  no recorded project locked a dependent whose resolution into this version was live at the time\n")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  project\tlocked at\tthrough\tlive from\tlive to\t")
	for _, l := range live {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t\n",
			l.Project, day(l.LockedAt), l.Dependent, day(l.ValidFrom), openOrDate(l.ValidTo))
	}
	w.Flush()
}

func reportSharedMaintainers(shared []incident.SharedMaintainer, top int) {
	fmt.Printf("\n  shares a maintainer with\n\n")
	if len(shared) == 0 {
		fmt.Printf("  no other package shares a maintainer with this one\n")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  package\tthrough\t")
	for i, s := range shared {
		if i >= top {
			fmt.Fprintf(w, "  ... and %d more\t\t\n", len(shared)-top)
			break
		}
		fmt.Fprintf(w, "  %s\t%s\t\n", s.Package, s.Maintainer)
	}
	w.Flush()
}

func reportTyposquatNeighbors(nearby []incident.TyposquatNeighbor, top int) {
	fmt.Printf("\n  likely typosquats nearby\n\n")
	if len(nearby) == 0 {
		fmt.Printf("  none materialized against this package\n")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  name\trelation\tdistance\tpopularity ratio\t")
	for i, n := range nearby {
		if i >= top {
			fmt.Fprintf(w, "  ... and %d more\t\t\t\n", len(nearby)-top)
			break
		}
		fmt.Fprintf(w, "  %s\t%s\t%d\t%.0fx\t\n", n.Name, n.Direction, n.Distance, n.PopularityRatio)
	}
	w.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncateSummary(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
