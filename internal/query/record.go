package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/lockfile"
)

const (
	upsertProject = `UNWIND $rows AS row
  MERGE (n {id: row.vertex})
  SET n:Project, n.urn = row.urn, n.name = row.name,
      n.lockfile_sha = row.sha, n.locked_at = row.locked_at, n.format = row.format`

	// LOCKS carries locked_at as a point rather than a span, because a lockfile records one
	// decision made at one moment. That point is what the incident question compares against a
	// RESOLVES_TO window: an application resolved a compromised version if its locked_at falls
	// inside the span over which that resolution was in force.
	linkLocks = `UNWIND $rows AS row
  MATCH (s:Project {id: row.src}), (d:Version {id: row.dst})
  MERGE (s)-[r:LOCKS {id: row.id}]->(d)
  SET r.locked_at = row.locked_at, r.direct = row.direct, r.dev = row.dev`
)

// Record writes a project and its lockfile pins into the graph, so later queries can ask which
// applications were exposed without re-reading files from disk.
//
// lockedAt is when the lockfile was written, not when it was read. Using the read time would make
// every historical query answer "now", which is the one answer that is never interesting.
func Record(ctx context.Context, db *graph.Client, lf lockfile.Lockfile, raw []byte, lockedAt int64) (int, error) {
	if lf.Project == "" {
		return 0, fmt.Errorf("cannot record a lockfile with no project name")
	}

	sum := sha256.Sum256(raw)
	urn := graph.ProjectURN(lf.Project)

	project := []map[string]any{{
		"vertex":    graph.ID(urn),
		"urn":       urn,
		"name":      lf.Project,
		"sha":       hex.EncodeToString(sum[:]),
		"locked_at": lockedAt,
		"format":    lf.Format,
	}}
	if _, err := db.WriteBatch(ctx, upsertProject, project, graph.DefaultBatch); err != nil {
		return 0, fmt.Errorf("write project %s: %w", lf.Project, err)
	}

	rows := make([]map[string]any, 0, len(lf.Pins))
	for _, p := range lf.Pins {
		versionURN := graph.VersionURN("npm", p.Name, p.Version)
		rows = append(rows, map[string]any{
			// A project can lock the same package at two versions in one tree, so the version is
			// part of the edge identity through the URN it is derived from.
			"id":        graph.EdgeID(urn, "LOCKS", versionURN, ""),
			"src":       graph.ID(urn),
			"dst":       graph.ID(versionURN),
			"locked_at": lockedAt,
			"direct":    p.Direct,
			"dev":       p.Dev,
		})
	}

	// An edge statement is UNWIND MATCH, and a MATCH that finds neither endpoint writes nothing and
	// reports no error, so pins whose Version node was never ingested are dropped silently here.
	// The caller already knows how many pins the graph holds and reports that as coverage.
	written, err := db.WriteBatch(ctx, linkLocks, rows, graph.DefaultBatch)
	if err != nil {
		return 0, fmt.Errorf("write locks edges for %s: %w", lf.Project, err)
	}
	return written, nil
}
