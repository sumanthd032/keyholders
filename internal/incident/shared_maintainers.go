// Package incident answers the track's mode 2 questions: blast radius, introduction, the temporal
// "resolved it while it was live" query, and adjacency through a shared maintainer.
package incident

import (
	"context"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// MaintainerSource supplies what SharedMaintainers needs from the graph: who holds a package, and
// what else an account holds, both at a given instant. The graph is one implementation; tests use a
// table, the same split every other query package in this project uses.
type MaintainerSource interface {
	MaintainersOf(ctx context.Context, name string, at int64) ([]string, error)
	PackagesOf(ctx context.Context, handle string, at int64) ([]string, error)
}

// SharedMaintainer is one other package held, at the queried instant, by an account that also holds
// the package the incident is about.
type SharedMaintainer struct {
	Maintainer string
	Package    string
}

// SharedMaintainers answers track question four, "which packages share maintainers or
// infrastructure": every other package held at instant at by an account that also holds name.
//
// This is two anchored single-hop reads composed in Go, not one two-hop pattern in one statement:
// HydraDB's Cypher subset takes one relationship type per pattern, one hop per pattern, so a chain
// belongs in Go rather than in the query text. The walk starts from the package, not the maintainer
// side, for the reason PROJECT.md gives for aggregation in the observatory: the set of maintainers to
// start from has no natural anchor, while the package this incident is about does.
func SharedMaintainers(ctx context.Context, src MaintainerSource, name string, at int64) ([]SharedMaintainer, error) {
	holders, err := src.MaintainersOf(ctx, name, at)
	if err != nil {
		return nil, fmt.Errorf("maintainers of %s: %w", name, err)
	}

	var out []SharedMaintainer
	for _, handle := range holders {
		held, err := src.PackagesOf(ctx, handle, at)
		if err != nil {
			return nil, fmt.Errorf("packages held by %s: %w", handle, err)
		}
		for _, pkg := range held {
			if pkg == name {
				continue
			}
			out = append(out, SharedMaintainer{Maintainer: handle, Package: pkg})
		}
	}
	return out, nil
}

// GraphSource reads maintainer holdings from HydraDB, filtered by MAINTAINS' validity window rather
// than credited to whoever holds a package today: an incident evaluated at a past instant should see
// the roster as it stood then, the same coexistence rule the audit applies everywhere else.
type GraphSource struct {
	DB        *graph.Client
	Ecosystem string
}

func (g GraphSource) MaintainersOf(ctx context.Context, name string, at int64) ([]string, error) {
	id := graph.ID(graph.PackageURN(g.Ecosystem, name))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (p:Package {id: %d})<-[r:MAINTAINS]-(m:Maintainer)
     WHERE r.valid_from <= %d AND r.valid_to > %d
     RETURN m.handle AS handle`, id, at, at), nil)
	if err != nil {
		return nil, err
	}
	return stringColumn(recs, "handle"), nil
}

func (g GraphSource) PackagesOf(ctx context.Context, handle string, at int64) ([]string, error) {
	id := graph.ID(graph.MaintainerURN(g.Ecosystem, handle))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (m:Maintainer {id: %d})-[r:MAINTAINS]->(p:Package)
     WHERE r.valid_from <= %d AND r.valid_to > %d
     RETURN p.name AS name`, id, at, at), nil)
	if err != nil {
		return nil, err
	}
	return stringColumn(recs, "name"), nil
}
