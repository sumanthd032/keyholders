package observatory

import (
	"context"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/sketch"
)

// MaintainerSource supplies which accounts held a package at a given instant. The graph is one
// implementation; tests use a table, the same split Source uses for Snapshots.
type MaintainerSource interface {
	Maintainers(ctx context.Context, name string, at int64) ([]string, error)
}

// Aggregator merges package sketches into maintainer sketches for one epoch.
type Aggregator struct {
	Src MaintainerSource
	New sketch.NewSketch
}

// Aggregate merges each package's sketch into every account that held it at the given epoch, so a
// maintainer's reach is the union of what every package they control can reach on its own, HLL union
// being exact set union under estimation. This reads no further than the packages the caller already
// has sketches for: for each one, a single anchored lookup answers who held it then, which is what
// keeps this a merge over results already in hand rather than a walk starting from the maintainer
// side of the graph, where the set of accounts to start from is unbounded and the direction back to
// a package's reach is not a single traversal.
//
// It also returns every package that lookup found holds no maintainer at all, the orphaned set
// task 8's derived view ranks by reach. Reporting it here rather than with a second pass is not an
// optimisation bolted on afterward: finding an orphan and crediting a holder are the same read, one
// per package, and asking the graph the same question twice to answer two different views of it
// would be exactly the kind of avoidable cost this project has spent every prior task measuring
// against.
func (a Aggregator) Aggregate(ctx context.Context, packages map[sketch.NodeID]sketch.Sketch, at int64) (maintainers map[sketch.NodeID]sketch.Sketch, orphaned []sketch.NodeID, err error) {
	maintainers = make(map[sketch.NodeID]sketch.Sketch)
	for name, pkgSketch := range packages {
		holders, err := a.Src.Maintainers(ctx, string(name), at)
		if err != nil {
			return nil, nil, fmt.Errorf("maintainers of %s: %w", name, err)
		}
		if len(holders) == 0 {
			orphaned = append(orphaned, name)
			continue
		}
		for _, handle := range holders {
			id := sketch.NodeID(handle)
			acc, ok := maintainers[id]
			if !ok {
				acc = a.New()
				maintainers[id] = acc
			}
			acc.Merge(pkgSketch)
		}
	}
	return maintainers, orphaned, nil
}

// Maintainers reads accounts that held a package at instant t, anchored at the package id and
// filtered to the validity window a MAINTAINS edge carries. An account is credited here only for the
// packages it actually held then, the same coexistence rule the audit applies to a lockfile pin
// rather than crediting whoever holds the package today with reach it did not have at the time being
// asked about.
func (g GraphSource) Maintainers(ctx context.Context, name string, at int64) ([]string, error) {
	id := graph.ID(graph.PackageURN(ecosystem, name))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (m:Maintainer)-[r:MAINTAINS]->(p:Package {id: %d})
     WHERE r.valid_from <= %d AND r.valid_to > %d
     RETURN m.handle AS handle`, id, at, at), nil)
	if err != nil {
		return nil, err
	}

	handles := make([]string, 0, len(recs))
	for _, rec := range recs {
		h, _ := rec.Get("handle")
		if s, ok := h.(string); ok {
			handles = append(handles, s)
		}
	}
	return handles, nil
}
