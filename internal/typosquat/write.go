package typosquat

import (
	"context"

	"github.com/sumanthd032/keyholders/internal/graph"
)

const ecosystem = "npm"

// linkTyposquatOf writes one candidate as an edge from the lookalike to the package it resembles,
// TYPOSQUAT_OF pointing toward the name being impersonated: "crossenv" -[:TYPOSQUAT_OF]-> "cross-env".
const linkTyposquatOf = `UNWIND $rows AS row
  MATCH (s:Package {id: row.src}), (d:Package {id: row.dst})
  MERGE (s)-[r:TYPOSQUAT_OF {id: row.id}]->(d)
  SET r.distance = row.distance, r.popularity_ratio = row.popularity_ratio`

// Write persists every candidate as a TYPOSQUAT_OF edge, anchored on Package nodes already in the
// graph. A candidate naming a package this project never actually ingested, which can only happen if
// it was in the ranked list but failed ingestion for some other reason, matches nothing on one side
// of the MATCH and contributes no edge, the same tolerance every other edge writer in this project
// has for a missing anchor.
func Write(ctx context.Context, db *graph.Client, cands []Candidate, batch int) (int, error) {
	rows := make([]map[string]any, 0, len(cands))
	for _, c := range cands {
		srcURN := graph.PackageURN(ecosystem, c.Lookalike)
		dstURN := graph.PackageURN(ecosystem, c.Popular)
		rows = append(rows, map[string]any{
			"id":               graph.EdgeID(srcURN, "TYPOSQUAT_OF", dstURN, ""),
			"src":              graph.ID(srcURN),
			"dst":              graph.ID(dstURN),
			"distance":         c.Distance,
			"popularity_ratio": c.PopularityRatio,
		})
	}
	return db.WriteBatch(ctx, linkTyposquatOf, rows, batch)
}
