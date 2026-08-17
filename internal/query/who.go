package query

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// Held is one package an account can publish to, and the spans over which it could.
type Held struct {
	Package string

	// Spans are the spells the account held the package over, merged. There is one entry per
	// uninterrupted spell, so an account that left and came back has two and the gap between them is
	// preserved.
	Spans Set

	// Versions is how many versions of it the graph holds, which is the size of the surface the
	// reverse search starts from rather than a property of the account.
	Versions int
}

// Who is what one account reaches: every package that would be running their code if they published
// to any of the packages they hold.
//
// It is the audit question turned around. An audit walks outward from a project through its
// dependencies; this walks inward from an account's packages through their dependents, over the same
// edges with the same interval rule. Reachability is not symmetric but the coexistence constraint is,
// so the search is the same one with the direction reversed.
type Who struct {
	Handle string
	Holds  []Held
	Reach  Result

	// Dependents are the packages that reach one of this account's packages, most recently exposed
	// first. Sources are excluded: an account trivially reaches its own packages.
	Dependents []string

	// UnionDependents is the same count without the interval rule, so the two can be compared the
	// way the audit compares them.
	UnionDependents int
}

var npmHandle = regexp.MustCompile(`^[a-z0-9._~-]+$`)

// WhoIs walks inward from everything one account can publish to.
func (a *Auditor) WhoIs(ctx context.Context, handle string, opts Options) (Who, error) {
	if !npmHandle.MatchString(handle) {
		return Who{}, fmt.Errorf("not an npm handle: %q", handle)
	}

	held, err := a.holdings(ctx, handle)
	if err != nil {
		return Who{}, err
	}
	out := Who{Handle: handle, Holds: held}
	if len(held) == 0 {
		return out, nil
	}

	packages := make([]string, 0, len(held))
	spans := make(map[string]Set, len(held))
	for _, h := range held {
		packages = append(packages, h.Package)
		spans[h.Package] = h.Spans
	}

	sources, counts, err := a.versionsOf(ctx, packages, spans, opts.Within)
	if err != nil {
		return Who{}, err
	}
	for i := range out.Holds {
		out.Holds[i].Versions = counts[out.Holds[i].Package]
	}
	if len(sources) == 0 {
		return out, nil
	}

	// Inbound, because RESOLVES_TO points from the dependent to the dependency. Walking it backwards
	// is what turns "what do I depend on" into "who depends on me".
	inbound := NewGraphExpander(a.db, "RESOLVES_TO")
	inbound.Direction = "incoming"
	inbound.Attrs = a.exp.Attrs
	out.Reach, err = Reach(ctx, inbound, sources, opts)
	a.exp.Reads += inbound.Reads
	if err != nil {
		return Who{}, err
	}

	own := map[string]bool{}
	for _, p := range packages {
		own[p] = true
	}
	dependents := map[string]bool{}
	for versionURN := range out.Reach.Coexistence {
		if pkg := PackageURNOf(versionURN); pkg != "" && !own[pkg] {
			dependents[pkg] = true
		}
	}
	unionDependents := map[string]bool{}
	for versionURN := range out.Reach.Union {
		if pkg := PackageURNOf(versionURN); pkg != "" && !own[pkg] {
			unionDependents[pkg] = true
		}
	}

	out.Dependents = make([]string, 0, len(dependents))
	for pkg := range dependents {
		out.Dependents = append(out.Dependents, pkg)
	}
	sort.Strings(out.Dependents)
	out.UnionDependents = len(unionDependents)
	return out, nil
}

// holdings reads the packages an account can publish to, with the spans it held them over.
//
// One pair can carry several MAINTAINS edges, and they are merged here rather than reported one per
// edge. Two of them can genuinely be disjoint, which is an account that left and returned, and
// Set.Insert keeps that gap. They can also overlap, which is an artefact of how the spells were
// observed: each ingest run samples the maintainer list at its own epoch boundaries, so a run over a
// shifted window starts the same unbroken spell at a different instant and writes it as a second
// edge. Counted per edge, that reports one package twice and doubles its version count.
func (a *Auditor) holdings(ctx context.Context, handle string) ([]Held, error) {
	urn := graph.MaintainerURN("npm", handle)
	stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Maintainer', sourceProperty: 'urn',
      sourceValues: ['%s'], relTypes: ['MAINTAINS'], relDirection: 'outgoing',
      maxLen: 1, pathCount: %d, resultLimit: %d}) YIELD path RETURN path`,
		urn, resultLimit, resultLimit)

	a.exp.Reads++
	recs, err := a.db.Query(ctx, stmt, nil)
	if err != nil {
		return nil, fmt.Errorf("read what %s maintains: %w", handle, err)
	}

	spans := map[string]Set{}
	for _, rec := range recs {
		value, _ := rec.Get("path")
		path, ok := value.(neo4j.Path)
		if !ok || len(path.Nodes) != 2 || len(path.Relationships) != 1 {
			continue
		}
		pkg := stringProp(path.Nodes[1].Props, "urn")
		if pkg == "" {
			continue
		}
		spans[pkg], _ = spans[pkg].Insert(intervalOf(path.Relationships[0].Props))
	}

	held := make([]Held, 0, len(spans))
	for pkg, set := range spans {
		held = append(held, Held{Package: pkg, Spans: set})
	}
	sort.Slice(held, func(i, j int) bool { return held[i].Package < held[j].Package })
	return held, nil
}

// versionsOf enumerates the versions of a set of packages as search sources.
//
// Each version is seeded over the span it existed *and* the account held the package, intersected
// with the query window. Both halves matter. Seeding a version before it was published reports a
// dependency that could not have existed, and seeding it outside the holding span credits an account
// with reach over a package it had already left.
//
// A package held over two disjoint spells yields two entries for the same version, one per spell.
// The search takes that: entries are merged into a per-node interval set, so two spells of the same
// account are indistinguishable from two paths arriving over different spans.
func (a *Auditor) versionsOf(ctx context.Context, packages []string, spans map[string]Set, within Interval) ([]Entry, map[string]int, error) {
	var (
		sources []Entry
		counts  = map[string]int{}
	)

	valid := make([]string, 0, len(packages))
	for _, p := range packages {
		if npmPackageURN.MatchString(p) {
			valid = append(valid, p)
		}
	}

	for start := 0; start < len(valid); start += sourceChunk {
		end := min(start+sourceChunk, len(valid))
		got, err := a.readVersions(ctx, valid[start:end])
		if err != nil {
			return nil, nil, err
		}
		for pkg, versions := range got {
			counts[pkg] += len(versions)
			for _, v := range versions {
				existed := Interval{From: v.PublishedAt, To: graph.OpenInterval}
				if v.PublishedAt <= 0 {
					existed = Always
				}
				for _, spell := range spans[pkg] {
					live := existed.Intersect(spell).Intersect(within)
					if live.Empty() {
						continue
					}
					sources = append(sources, Entry{URN: v.URN, Valid: live})
				}
			}
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].URN != sources[j].URN {
			return sources[i].URN < sources[j].URN
		}
		return sources[i].Valid.From < sources[j].Valid.From
	})
	return sources, counts, nil
}

func (a *Auditor) readVersions(ctx context.Context, packages []string) (map[string][]VersionAttrs, error) {
	stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Package', sourceProperty: 'urn',
      sourceValues: [%s], relTypes: ['HAS_VERSION'], relDirection: 'outgoing',
      maxLen: 1, pathCount: %d, resultLimit: %d}) YIELD path RETURN path`,
		quoteAll(packages), resultLimit, resultLimit)

	a.exp.Reads++
	recs, err := a.db.Query(ctx, stmt, nil)
	if err != nil {
		return nil, fmt.Errorf("read versions of %d packages: %w", len(packages), err)
	}
	if len(recs) >= resultLimit && len(packages) > 1 {
		half := len(packages) / 2
		left, err := a.readVersions(ctx, packages[:half])
		if err != nil {
			return nil, err
		}
		right, err := a.readVersions(ctx, packages[half:])
		if err != nil {
			return nil, err
		}
		for pkg, versions := range right {
			left[pkg] = append(left[pkg], versions...)
		}
		return left, nil
	}

	out := map[string][]VersionAttrs{}
	for _, rec := range recs {
		value, _ := rec.Get("path")
		path, ok := value.(neo4j.Path)
		if !ok || len(path.Nodes) != 2 {
			continue
		}
		pkg := stringProp(path.Nodes[0].Props, "urn")
		attrs := versionAttrs(path.Nodes[1].Props)
		if pkg == "" || attrs.URN == "" {
			continue
		}
		out[pkg] = append(out[pkg], attrs)
	}
	return out, nil
}
