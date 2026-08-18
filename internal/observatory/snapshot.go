// Package observatory builds the package level view of the graph the sketch engine runs on: one
// PKG_RESOLVES edge per (package, dependency), snapshotted at each epoch from the dependent's
// version current at that instant. A version's declared dependencies never change once it is
// published, so "current at an epoch" is the only moving part; DEPENDS_ON already carries the rest.
package observatory

import (
	"context"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/sketch"
)

const ecosystem = "npm"

// Release is one published version, read from a package's HAS_VERSION timeline.
type Release struct {
	Version     string
	PublishedAt int64
}

// Source supplies what a snapshot needs from the graph: one package's version timeline, and one of
// its versions' declared dependencies. The graph is one implementation; tests use a table, the same
// split the audit search uses to stay testable without a live database.
type Source interface {
	Timeline(ctx context.Context, name string) ([]Release, error)
	Dependencies(ctx context.Context, name, version string) ([]string, error)
}

// Snapshots builds the PKG_RESOLVES edges live at every given epoch, for the given packages.
//
// The pass is driven entirely from names, never from a scan, because the packages this project
// knows about are the only universe a snapshot can honestly cover, and an unanchored pattern over a
// label this size is materialized in full before a filter gets a chance to run. Each package's
// timeline is read once regardless of how many epochs are asked for, and its dependencies are read
// once per distinct version that timeline resolves to across those epochs, not once per epoch: a
// package that has not published in years is the same current version at every epoch, and costs one
// read rather than one per epoch.
func Snapshots(ctx context.Context, src Source, names []string, epochs []int64) (map[int64][]sketch.Edge, error) {
	out := make(map[int64][]sketch.Edge, len(epochs))
	for _, e := range epochs {
		out[e] = nil
	}

	for _, name := range names {
		releases, err := src.Timeline(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("timeline of %s: %w", name, err)
		}

		epochsByVersion := map[string][]int64{}
		for _, e := range epochs {
			rel, ok := currentVersion(releases, e)
			if !ok {
				// Nothing of this package had been published yet at this epoch, so it contributes
				// no edges here. The same rule the audit applies to a lockfile pin applies to a
				// dependent: it can only be counted while it existed.
				continue
			}
			epochsByVersion[rel.Version] = append(epochsByVersion[rel.Version], e)
		}

		for version, atEpochs := range epochsByVersion {
			deps, err := src.Dependencies(ctx, name, version)
			if err != nil {
				return nil, fmt.Errorf("dependencies of %s@%s: %w", name, version, err)
			}
			for _, e := range atEpochs {
				for _, dep := range deps {
					out[e] = append(out[e], sketch.Edge{From: sketch.NodeID(name), To: sketch.NodeID(dep)})
				}
			}
		}
	}
	return out, nil
}

// currentVersion returns the version current at instant t: the newest non-prerelease version
// published at or before t. This is the same canonical reconstruction the rest of the project
// resolves against, measured at 89.6% agreement with real dated lockfiles.
//
// Prereleases are excluded because npm does not install one unless a range explicitly asks for it,
// so treating a prerelease as current would misreport what an install actually produced.
func currentVersion(releases []Release, t int64) (Release, bool) {
	var best Release
	found := false
	for _, r := range releases {
		if r.PublishedAt <= 0 || r.PublishedAt > t || isPrerelease(r.Version) {
			continue
		}
		if !found || r.PublishedAt > best.PublishedAt {
			best, found = r, true
		}
	}
	return best, found
}

// isPrerelease reports whether a version string carries a prerelease tag. Build metadata after `+`
// is not a prerelease, so the `-` has to be found before any `+`.
func isPrerelease(version string) bool {
	for i := range len(version) {
		switch version[i] {
		case '-':
			return true
		case '+':
			return false
		}
	}
	return false
}

// GraphSource reads timelines and dependencies from HydraDB. Both reads anchor at an id already
// known, a package for the timeline and a specific resolved version for its dependencies, which is
// what keeps them fast: an anchored pattern returns immediately on this graph, where the equivalent
// unanchored form exceeds the query timeout before WHERE or LIMIT ever applies.
type GraphSource struct{ DB *graph.Client }

func (g GraphSource) Timeline(ctx context.Context, name string) ([]Release, error) {
	id := graph.ID(graph.PackageURN(ecosystem, name))
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

func (g GraphSource) Dependencies(ctx context.Context, name, version string) ([]string, error) {
	id := graph.ID(graph.VersionURN(ecosystem, name, version))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (v:Version {id: %d})-[:DEPENDS_ON]->(p:Package)
     RETURN p.name AS name`, id), nil)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		if s, ok := n.(string); ok {
			names = append(names, s)
		}
	}
	return names, nil
}
