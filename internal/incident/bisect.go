package incident

import (
	"context"
	"fmt"
	"sort"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// Release is one published version, read from a package's HAS_VERSION timeline. Every package that
// reads this off the graph defines its own copy; see resolve.Release, observatory.Release, and
// advisory.Release for the same shape used the same way.
type Release struct {
	Version     string
	PublishedAt int64
}

// Dependency is one DEPENDS_ON declaration: a name and the range a version declared for it.
type Dependency struct {
	Name  string
	Range string
}

// BisectSource supplies what Bisect needs from the graph: a package's publish timeline, which of its
// versions one advisory's AFFECTS edges cover, and one version's declared dependencies.
type BisectSource interface {
	Timeline(ctx context.Context, name string) ([]Release, error)
	AffectedVersions(ctx context.Context, advisoryID, name string) (map[string]bool, error)
	Dependencies(ctx context.Context, name, version string) ([]Dependency, error)
}

// DependencyChange is one declaration whose range moved between two versions of the same package.
type DependencyChange struct {
	Name     string
	From, To string
}

// DependencyDiff is what changed in a version's declared dependencies relative to the version
// published immediately before it.
type DependencyDiff struct {
	Added   []Dependency
	Removed []Dependency
	Changed []DependencyChange
}

// Introduction is the result of bisecting one advisory's affected versions against a package's
// publish timeline.
type Introduction struct {
	Package       string
	FirstAffected string
	PublishedAt   int64
	Previous      string // empty when FirstAffected is the package's first ever release
	Diff          DependencyDiff
}

// Bisect answers track question two, "which version introduced the vulnerability": the earliest
// published version an advisory's AFFECTS edges cover, found by walking the timeline in publish
// order rather than trusting a range's own introduced boundary, since a range string and the
// registry's actual publish order are not guaranteed to agree once prereleases or backports are
// involved. The dependency diff against the immediately preceding version is context, not proof: a
// new dependency landing in the same release an advisory starts at is often, not always, the change
// that introduced it.
func Bisect(ctx context.Context, src BisectSource, advisoryID, name string) (Introduction, error) {
	timeline, err := src.Timeline(ctx, name)
	if err != nil {
		return Introduction{}, fmt.Errorf("timeline of %s: %w", name, err)
	}
	affected, err := src.AffectedVersions(ctx, advisoryID, name)
	if err != nil {
		return Introduction{}, fmt.Errorf("affected versions of %s: %w", name, err)
	}

	sort.Slice(timeline, func(i, j int) bool { return timeline[i].PublishedAt < timeline[j].PublishedAt })

	var first, previous *Release
	for i, rel := range timeline {
		if affected[rel.Version] {
			first = &timeline[i]
			if i > 0 {
				previous = &timeline[i-1]
			}
			break
		}
	}
	if first == nil {
		return Introduction{}, fmt.Errorf("%s: advisory %s affects no version in this package's timeline", name, advisoryID)
	}

	intro := Introduction{Package: name, FirstAffected: first.Version, PublishedAt: first.PublishedAt}
	if previous != nil {
		intro.Previous = previous.Version
		diff, err := diffDependencies(ctx, src, name, previous.Version, first.Version)
		if err != nil {
			return Introduction{}, err
		}
		intro.Diff = diff
	}
	return intro, nil
}

func diffDependencies(ctx context.Context, src BisectSource, name, from, to string) (DependencyDiff, error) {
	before, err := src.Dependencies(ctx, name, from)
	if err != nil {
		return DependencyDiff{}, fmt.Errorf("dependencies of %s@%s: %w", name, from, err)
	}
	after, err := src.Dependencies(ctx, name, to)
	if err != nil {
		return DependencyDiff{}, fmt.Errorf("dependencies of %s@%s: %w", name, to, err)
	}

	beforeRange := make(map[string]string, len(before))
	for _, d := range before {
		beforeRange[d.Name] = d.Range
	}
	afterRange := make(map[string]string, len(after))
	for _, d := range after {
		afterRange[d.Name] = d.Range
	}

	var diff DependencyDiff
	for _, d := range after {
		prevRange, existed := beforeRange[d.Name]
		switch {
		case !existed:
			diff.Added = append(diff.Added, d)
		case prevRange != d.Range:
			diff.Changed = append(diff.Changed, DependencyChange{Name: d.Name, From: prevRange, To: d.Range})
		}
	}
	for _, d := range before {
		if _, stillThere := afterRange[d.Name]; !stillThere {
			diff.Removed = append(diff.Removed, d)
		}
	}
	return diff, nil
}

func (g GraphSource) Timeline(ctx context.Context, name string) ([]Release, error) {
	id := graph.ID(graph.PackageURN(g.Ecosystem, name))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (p:Package {id: %d})-[:HAS_VERSION]->(v:Version)
     RETURN v.version AS version, v.published_at AS published`, id), nil)
	if err != nil {
		return nil, err
	}

	releases := make([]Release, 0, len(recs))
	for _, rec := range recs {
		version, _ := rec.Get("version")
		published, _ := rec.Get("published")
		v, ok := version.(string)
		if !ok {
			continue
		}
		p, _ := published.(int64)
		releases = append(releases, Release{Version: v, PublishedAt: p})
	}
	return releases, nil
}

// AffectedVersions reads every version of name that advisoryID's AFFECTS edges cover, anchored at the
// advisory since that id is already known. An advisory can affect more than one package, so the
// result is filtered to name's own versions by the Version node's name property rather than by
// walking from the package side, which would need a second anchored read per version instead of one
// read for the whole advisory.
func (g GraphSource) AffectedVersions(ctx context.Context, advisoryID, name string) (map[string]bool, error) {
	id := graph.ID(graph.AdvisoryURN(advisoryID))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (a:Advisory {id: %d})-[:AFFECTS]->(v:Version)
     WHERE v.name = '%s'
     RETURN v.version AS version`, id, name), nil)
	if err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(recs))
	for _, rec := range recs {
		v, _ := rec.Get("version")
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out, nil
}

func (g GraphSource) Dependencies(ctx context.Context, name, version string) ([]Dependency, error) {
	id := graph.ID(graph.VersionURN(g.Ecosystem, name, version))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (v:Version {id: %d})-[r:DEPENDS_ON]->(p:Package)
     RETURN p.name AS name, r.range AS range`, id), nil)
	if err != nil {
		return nil, err
	}

	deps := make([]Dependency, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		r, _ := rec.Get("range")
		name, ok := n.(string)
		if !ok {
			continue
		}
		rng, _ := r.(string)
		deps = append(deps, Dependency{Name: name, Range: rng})
	}
	return deps, nil
}
