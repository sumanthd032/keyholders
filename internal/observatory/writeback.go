package observatory

import (
	"context"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/sketch"
)

// writeKRI sets kri and kri_at on an existing node, batched through UNWIND. Vertex upsert is MERGE
// by id followed by SET, and an UNWIND vertex upsert is rejected outright, "requires exactly one SET
// label", unless the SET clause names a label, even when the node already carries it and nothing
// about its labelling is actually changing. Every other write in this project happened to satisfy
// this by coincidence, either creating a node, which needs a label anyway, or re-asserting one
// alongside new properties; this is the first write that only ever adds a property, which is what
// surfaced the rule. See finding 21. label cannot be a query parameter, Cypher labels are never
// parameterizable, so it is interpolated into the statement text; the two callers below pass a
// literal, never anything derived from data.
func writeKRIStmt(label string) string {
	return fmt.Sprintf(`UNWIND $rows AS row
  MERGE (n {id: row.id})
  SET n:%s, n.kri = row.kri, n.kri_at = row.at`, label)
}

// WriteBack persists computed reach as kri and kri_at properties on existing graph nodes. kri_at
// records the epoch instant the value was computed for, because kri is a snapshot of one moment, not
// a live figure, and a reader who finds it on a node six months from now needs to know what it is a
// snapshot of rather than mistaking it for something that stays current on its own.
//
// label is the node's existing label, "Package" or "Maintainer", and urn turns a NodeID into the URN
// the node was created under. A package and a maintainer are constructed from different URN schemes,
// so the caller supplies the mapping rather than this function guessing one from the id alone.
func WriteBack(ctx context.Context, db *graph.Client, results map[sketch.NodeID]sketch.Sketch, at int64, label string, urn func(sketch.NodeID) string, batch int) (int, error) {
	rows := make([]map[string]any, 0, len(results))
	for id, sk := range results {
		rows = append(rows, map[string]any{
			"id":  graph.ID(urn(id)),
			"kri": sk.Count(),
			"at":  at,
		})
	}
	return db.WriteBatch(ctx, writeKRIStmt(label), rows, batch)
}
