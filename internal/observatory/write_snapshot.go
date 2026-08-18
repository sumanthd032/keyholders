package observatory

import (
	"context"
	"strconv"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/sketch"
)

// linkPkgResolves persists one epoch's package level resolution snapshot. PKG_RESOLVES is one
// relationship type carrying an epoch property, not one type per epoch: measured against a live
// HydraDB instance, the shared type with a filtered read was 10 to 37 percent faster on the
// single-epoch traversal this package issues, opposite the a priori argument that a dedicated
// compiled index per type should win. See D46 and finding 22.
//
// The edge id is keyed on (src, dst, epoch), not just (src, dst): a dependency the same pair carries
// in two different epochs is two distinct facts, not one interval, since PKG_RESOLVES is not a
// validity window the way RESOLVES_TO is, it is a snapshot taken fresh from each package's current
// version at that instant. Keying on the triple is what makes a rerun update the epoch's edges in
// place instead of accumulating duplicates.
const linkPkgResolves = `UNWIND $rows AS row
  MATCH (s:Package {id: row.src}), (d:Package {id: row.dst})
  MERGE (s)-[r:PKG_RESOLVES {id: row.id}]->(d)
  SET r.epoch = row.epoch`

// WriteSnapshots writes every epoch's PKG_RESOLVES edges from Snapshots' output into the graph.
// Returns the number of edges written.
func WriteSnapshots(ctx context.Context, db *graph.Client, snapshots map[int64][]sketch.Edge, batch int) (int, error) {
	var rows []map[string]any
	for epoch, edges := range snapshots {
		for _, e := range edges {
			srcURN := graph.PackageURN(ecosystem, string(e.From))
			dstURN := graph.PackageURN(ecosystem, string(e.To))
			rows = append(rows, map[string]any{
				"src":   graph.ID(srcURN),
				"dst":   graph.ID(dstURN),
				"id":    graph.EdgeID(srcURN, "PKG_RESOLVES", dstURN, strconv.FormatInt(epoch, 10)),
				"epoch": epoch,
			})
		}
	}
	return db.WriteBatch(ctx, linkPkgResolves, rows, batch)
}
