package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/incident"
	"github.com/sumanthd032/keyholders/internal/lockfile"
	"github.com/sumanthd032/keyholders/internal/query"
)

// runCICheck is the gate a build pipeline runs against a lockfile: does any version the project
// actually reaches, right now, carry a live advisory, and, if a base lockfile is given, did this
// change introduce any keyholder the base lockfile did not already have. Exit code carries the
// verdict, since that is what a CI step checks; the report on stdout is for the human reading the
// failed job.
func runCICheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ci-check", flag.ExitOnError)
	base := fs.String("base", "", "a prior lockfile to diff the keyholder set against")
	at := fs.String("at", "", "resolve as of this date (YYYY-MM-DD), default is every instant")
	depth := fs.Int("depth", 12, "maximum dependency hops to follow")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: keyholders ci-check [flags] <lockfile>")
	}
	path := fs.Arg(0)

	opts := query.Options{MaxDepth: *depth, Within: query.Always}
	if *at != "" {
		t, err := time.Parse("2006-01-02", *at)
		if err != nil {
			return fmt.Errorf("parse -at: %w", err)
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

	audit, err := auditLockfile(ctx, auditor, path, opts)
	if err != nil {
		return err
	}

	src := incident.GraphSource{DB: db, Ecosystem: "npm"}
	exposures, err := advisoriesReached(ctx, src, audit)
	if err != nil {
		return fmt.Errorf("check advisories: %w", err)
	}

	var newKeyholders []string
	if *base != "" {
		baseAudit, err := auditLockfile(ctx, auditor, *base, opts)
		if err != nil {
			return fmt.Errorf("audit base lockfile: %w", err)
		}
		newKeyholders = diffKeyholders(baseAudit, audit)
	}

	failed := len(exposures) > 0 || len(newKeyholders) > 0
	reportCICheck(path, *base, exposures, newKeyholders)
	if failed {
		os.Exit(1)
	}
	return nil
}

func auditLockfile(ctx context.Context, auditor *query.Auditor, path string, opts query.Options) (query.Audit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return query.Audit{}, fmt.Errorf("read lockfile %s: %w", path, err)
	}
	lf, err := lockfile.Parse(path, data)
	if err != nil {
		return query.Audit{}, err
	}
	return auditor.Audit(ctx, lf, opts)
}

// exposure is one version this project reaches that carries a live advisory. Checked one reached
// version at a time, since AdvisoriesAffecting is anchored per version and this Cypher subset has
// no way to ask for every affected version in a reached set in one call.
type exposure struct {
	version    string
	advisories []incident.AdvisoryInfo
}

func advisoriesReached(ctx context.Context, src incident.GraphSource, audit query.Audit) ([]exposure, error) {
	versions := make([]string, 0, len(audit.Reach.Coexistence))
	for urn := range audit.Reach.Coexistence {
		versions = append(versions, urn)
	}
	sort.Strings(versions)

	var out []exposure
	for _, urn := range versions {
		name, version, ok := splitVersionURN(urn)
		if !ok {
			continue
		}
		advisories, err := src.AdvisoriesAffecting(ctx, name, version)
		if err != nil {
			return nil, err
		}
		if len(advisories) > 0 {
			out = append(out, exposure{version: urn, advisories: advisories})
		}
	}
	return out, nil
}

// splitVersionURN splits "pkg:npm/left-pad@1.0.0" into ("left-pad", "1.0.0"). A scoped name,
// "@scope/name@1.0.0", has an earlier "@" for the scope itself, so this splits on the last one.
func splitVersionURN(urn string) (name, version string, ok bool) {
	pkg := query.PackageURNOf(urn)
	if pkg == "" {
		return "", "", false
	}
	name = strings.TrimPrefix(pkg, "pkg:npm/")
	version = strings.TrimPrefix(urn, pkg+"@")
	if version == urn {
		return "", "", false
	}
	return name, version, true
}

// diffKeyholders lists every handle audit reaches that base did not, sorted for a stable report. A
// dependency bump that adds a new keyholder is exactly the accounts present in the new lockfile's
// roster and absent from the old one; an account already present is not "new" even if its risk score
// or package count changed.
func diffKeyholders(base, head query.Audit) []string {
	had := make(map[string]bool, len(base.Keyholders))
	for _, k := range base.Keyholders {
		had[k.Handle] = true
	}
	var added []string
	for _, k := range head.Keyholders {
		if !had[k.Handle] {
			added = append(added, k.Handle)
		}
	}
	sort.Strings(added)
	return added
}

func reportCICheck(path, base string, exposures []exposure, newKeyholders []string) {
	fmt.Printf("\nkeyholders ci-check  %s\n", path)
	fmt.Printf("%s\n", strings.Repeat("-", 64))

	if len(exposures) == 0 {
		fmt.Printf("\n  no reached version carries a live advisory\n")
	} else {
		fmt.Printf("\n  %d reached version(s) carry a live advisory:\n\n", len(exposures))
		for _, e := range exposures {
			name := strings.TrimPrefix(e.version, "pkg:npm/")
			fmt.Printf("    %s\n", name)
			for _, a := range e.advisories {
				fmt.Printf("      %s  %s  %s\n", a.ID, a.Severity, a.Summary)
			}
		}
	}

	if base != "" {
		if len(newKeyholders) == 0 {
			fmt.Printf("\n  no new keyholder relative to %s\n", base)
		} else {
			fmt.Printf("\n  %d new keyholder(s) relative to %s:\n\n", len(newKeyholders), base)
			for _, h := range newKeyholders {
				fmt.Printf("    %s\n", h)
			}
		}
	}
	fmt.Println()
}
