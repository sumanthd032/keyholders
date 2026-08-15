package graph_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/sumanthd032/keyholders/internal/config"
	"github.com/sumanthd032/keyholders/internal/graph"
)

// open connects to the local deployment, skipping when it is not running. The write path cannot be
// tested against a fake: the behaviour worth testing is HydraDB's, not ours.
func open(t *testing.T) *graph.Client {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	c, err := graph.Open(context.Background(), cfg)
	if err != nil {
		t.Skipf("HydraDB unreachable at %s, run `make up`: %v", cfg.BoltURI, err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func TestWriteBatchRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := open(t)

	// A label of its own keeps the test from colliding with ingested data or with a previous run,
	// which matters because MERGE by id makes reruns overwrite rather than fail.
	const label = "BatchTest"

	urns := []string{"pkg:test/alpha", "pkg:test/beta", "pkg:test/gamma"}
	nodes := make([]map[string]any, 0, len(urns))
	for _, urn := range urns {
		nodes = append(nodes, map[string]any{"id": graph.ID(urn), "urn": urn})
	}

	upsert := fmt.Sprintf(`UNWIND $rows AS row MERGE (n {id: row.id}) SET n:%s, n.urn = row.urn`, label)
	if n, err := c.WriteBatch(ctx, upsert, nodes, 2); err != nil {
		t.Fatalf("upsert nodes: %v", err)
	} else if n != len(nodes) {
		t.Fatalf("wrote %d nodes, want %d", n, len(nodes))
	}

	edges := []map[string]any{
		{"id": graph.EdgeID(urns[0], "TESTS", urns[1], ""), "src": graph.ID(urns[0]), "dst": graph.ID(urns[1]), "weight": int64(1)},
		{"id": graph.EdgeID(urns[1], "TESTS", urns[2], ""), "src": graph.ID(urns[1]), "dst": graph.ID(urns[2]), "weight": int64(2)},
	}
	link := fmt.Sprintf(`UNWIND $rows AS row
  MATCH (s:%s {id: row.src}), (d:%s {id: row.dst})
  MERGE (s)-[r:TESTS {id: row.id}]->(d)
  SET r.weight = row.weight`, label, label)
	if _, err := c.WriteBatch(ctx, link, edges, 2); err != nil {
		t.Fatalf("write edges: %v", err)
	}

	// Writing the same rows again must not duplicate, since resuming an interrupted ingest replays
	// whatever the checkpoint could not confirm.
	if _, err := c.WriteBatch(ctx, link, edges, 2); err != nil {
		t.Fatalf("rewrite edges: %v", err)
	}

	recs, err := c.Query(ctx, fmt.Sprintf(
		`MATCH (s:%s {id: %d})-[r:TESTS]->(d) RETURN d.urn AS urn, r.weight AS weight`,
		label, graph.ID(urns[0])), nil)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("read back %d edges from alpha, want 1 (a repeated MERGE duplicated the edge)", len(recs))
	}
	urn, _ := recs[0].Get("urn")
	weight, _ := recs[0].Get("weight")
	if urn != urns[1] || weight != int64(1) {
		t.Fatalf("read back urn=%v weight=%v, want %v and 1", urn, weight, urns[1])
	}
}

// TestParallelEdgesByID pins the behaviour the MAINTAINS model depends on: two edges of the same
// type between the same pair, distinguished only by relationship id, must both survive. Interval
// edges need this, because a maintainer can hold two disjoint spells on one package.
func TestParallelEdgesByID(t *testing.T) {
	ctx := context.Background()
	c := open(t)

	const label = "ParallelTest"
	src, dst := "mnt:test/holder", "pkg:test/held"

	nodes := []map[string]any{
		{"id": graph.ID(src), "urn": src},
		{"id": graph.ID(dst), "urn": dst},
	}
	upsert := fmt.Sprintf(`UNWIND $rows AS row MERGE (n {id: row.id}) SET n:%s, n.urn = row.urn`, label)
	if _, err := c.WriteBatch(ctx, upsert, nodes, 8); err != nil {
		t.Fatalf("upsert nodes: %v", err)
	}

	spells := []struct{ from, to int64 }{{1400000000, 1500000000}, {1700000000, 1800000000}}
	edges := make([]map[string]any, 0, len(spells))
	for _, s := range spells {
		edges = append(edges, map[string]any{
			"id":         graph.EdgeID(src, "HOLDS", dst, fmt.Sprint(s.from)),
			"src":        graph.ID(src),
			"dst":        graph.ID(dst),
			"valid_from": s.from,
			"valid_to":   s.to,
		})
	}
	link := fmt.Sprintf(`UNWIND $rows AS row
  MATCH (s:%s {id: row.src}), (d:%s {id: row.dst})
  MERGE (s)-[r:HOLDS {id: row.id}]->(d)
  SET r.valid_from = row.valid_from, r.valid_to = row.valid_to`, label, label)
	if _, err := c.WriteBatch(ctx, link, edges, 8); err != nil {
		t.Fatalf("write interval edges: %v", err)
	}

	recs, err := c.Query(ctx, fmt.Sprintf(
		`MATCH (s:%s {id: %d})-[r:HOLDS]->(d) RETURN r.valid_from AS from, r.valid_to AS to`,
		label, graph.ID(src)), nil)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(recs) != len(spells) {
		t.Fatalf("read back %d interval edges, want %d: distinct relationship ids did not "+
			"produce parallel edges, so MAINTAINS cannot carry disjoint spells", len(recs), len(spells))
	}
}
