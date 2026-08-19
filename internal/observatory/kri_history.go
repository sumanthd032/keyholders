package observatory

import (
	"context"
	"fmt"
	"strconv"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/sketch"
)

// writeKRIHistoryStmt persists one epoch's kri as a self loop edge rather than a node property: a
// node property holds one value, and the reach-over-time curve needs one value per epoch. The two
// MATCH clauses bind the same node under separate variables because a MERGE whose two endpoints
// reuse a single MATCH variable is rejected outright, "requires exactly two endpoint nodes"; matching
// the same id under two variables passes and behaves as a genuine self loop on read back. See
// finding 23. Edge id keyed on (node, epoch), the same triple keying PKG_RESOLVES uses for
// (src, dst, epoch) per D46, so a rerun updates that epoch's value in place instead of accumulating
// duplicates.
func writeKRIHistoryStmt(label string) string {
	return fmt.Sprintf(`UNWIND $rows AS row
  MATCH (n1:%s {id: row.id}), (n2:%s {id: row.id})
  MERGE (n1)-[r:KRI_AT {id: row.edge_id}]->(n2)
  SET r.epoch = row.epoch, r.kri = row.kri, r.orphaned = row.orphaned`, label, label)
}

// WriteKRIHistory persists one epoch's kri for every node in results, so the reach-over-time curve
// and the orphaned-at-this-epoch flag both survive past the run that computed them, rather than only
// the latest epoch's value the way WriteBack's node properties do.
//
// orphaned is nil for maintainers, which have no such concept. Every package id present in orphaned
// is written with orphaned true; every other id is written with orphaned false, so "not orphaned at
// this epoch" is a fact the graph carries rather than the absence of one.
func WriteKRIHistory(ctx context.Context, db *graph.Client, results map[sketch.NodeID]sketch.Sketch, orphaned []sketch.NodeID, at int64, label string, urn func(sketch.NodeID) string, batch int) (int, error) {
	isOrphaned := make(map[sketch.NodeID]bool, len(orphaned))
	for _, id := range orphaned {
		isOrphaned[id] = true
	}

	rows := make([]map[string]any, 0, len(results))
	for id, sk := range results {
		u := urn(id)
		rows = append(rows, map[string]any{
			"id":       graph.ID(u),
			"edge_id":  graph.EdgeID(u, "KRI_AT", u, strconv.FormatInt(at, 10)),
			"epoch":    at,
			"kri":      sk.Count(),
			"orphaned": isOrphaned[id],
		})
	}
	return db.WriteBatch(ctx, writeKRIHistoryStmt(label), rows, batch)
}
