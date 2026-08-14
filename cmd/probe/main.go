// Command probe measures the HydraDB behaviour that the ingest and query design depend on.
//
// Several of these behaviours are not documented, so every claim in FINDINGS.md is backed by a probe
// here that anyone can rerun. Keeping it in the tree means the findings stay checkable against a
// future release rather than becoming folklore.
//
// The questions, in the order they block work:
//
//  1. Which Bolt authentication scheme does HydraDB accept? Undocumented.
//  2. Does UNWIND with a list-of-maps parameter work through the Neo4j Go driver?
//  3. How do property indexes come into existence, given that CREATE INDEX is not in the
//     supported Cypher surface, and does algo.MSpaths selector resolution scale?
//  4. Can one query traverse edges spanning two cells?
//  5. What is the sustained write throughput, and at which batch size?
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

const (
	boltURI = "bolt://127.0.0.1:7687"
	token   = "local-development-token-32-bytes"

	// maxBatch is HydraDB's admission control ceiling on UNWIND batch items. It comes from
	// ClientConfig.max_parameters, whose default is 1024 and which has no environment binding, so
	// running the published image it cannot be raised. Exceeding it fails the whole statement with
	// MemoryPoolOutOfMemoryError naming "client_query_batch_items", which reads like a memory
	// problem but is a fixed row count. Ingest throughput therefore has to come from concurrent
	// sessions rather than from larger batches.
	maxBatch = 1024
)

func main() {
	only := flag.String("only", "", "run a single probe by name")
	flag.Parse()

	probes := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"auth", probeAuth},
		{"unwind", probeUnwind},
		{"selectors", probeSelectors},
		{"cells", probeCells},
		{"throughput", probeThroughput},
	}

	ctx := context.Background()
	failed := 0
	for _, p := range probes {
		if *only != "" && *only != p.name {
			continue
		}
		fmt.Printf("=== probe: %s ===\n", p.name)
		if err := p.fn(ctx); err != nil {
			fmt.Printf("FAILED: %v\n\n", err)
			failed++
			continue
		}
		fmt.Print("\n")
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// probeAuth tries every plausible Bolt credential shape and reports which ones open a session that
// can actually run a query. Connecting is not proof: the driver defers the handshake, so each
// candidate has to round-trip a real statement.
func probeAuth(ctx context.Context) error {
	candidates := []struct {
		name string
		auth neo4j.AuthToken
	}{
		{`basic("neo4j", token)`, neo4j.BasicAuth("neo4j", token, "")},
		{`basic("", token)`, neo4j.BasicAuth("", token, "")},
		{`basic(token, token)`, neo4j.BasicAuth(token, token, "")},
		{`basic("hydradb", token)`, neo4j.BasicAuth("hydradb", token, "")},
		{`bearer(token)`, neo4j.BearerAuth(token)},
		{`none`, neo4j.NoAuth()},
		{`custom("bearer", token)`, neo4j.CustomAuth("bearer", "", token, "", nil)},
	}

	var working []string
	for _, c := range candidates {
		err := tryAuth(ctx, c.auth)
		switch {
		case err == nil:
			fmt.Printf("  OK      %s\n", c.name)
			working = append(working, c.name)
		default:
			fmt.Printf("  reject  %-24s %v\n", c.name, truncate(err.Error(), 110))
		}
	}
	if len(working) == 0 {
		return errors.New("no Bolt auth scheme accepted; fall back to the HTTP API")
	}
	fmt.Printf("  -> use: %s\n", working[0])
	return nil
}

func tryAuth(ctx context.Context, auth neo4j.AuthToken) error {
	drv, err := neo4j.NewDriverWithContext(boltURI, auth)
	if err != nil {
		return err
	}
	defer drv.Close(ctx)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// VerifyConnectivity alone is not enough: a server can accept the connection and reject the
	// credentials at first use, so run a statement that must produce a row.
	sess := drv.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer sess.Close(ctx)

	res, err := sess.Run(ctx, "MATCH (a {id: 1})-[:FOLLOWS]->(b) RETURN b.id AS id", nil)
	if err != nil {
		return err
	}
	if _, err := res.Collect(ctx); err != nil {
		return err
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// session opens a Bolt session using the credential shape the auth probe established: the token is
// the password, and the username is ignored as long as it is non-empty.
func session(ctx context.Context) (neo4j.DriverWithContext, neo4j.SessionWithContext, error) {
	drv, err := neo4j.NewDriverWithContext(boltURI, neo4j.BasicAuth("neo4j", token, ""))
	if err != nil {
		return nil, nil, err
	}
	return drv, drv.NewSession(ctx, neo4j.SessionConfig{}), nil
}

// probeUnwind checks that batch writes work through the Go driver. The compatibility notes say a
// parameter holding a list of maps is a transport level type accepted only by the client transport,
// so the question is whether the driver's map encoding is the shape HydraDB expects. If it is not,
// the entire ingest write path has to change.
func probeUnwind(ctx context.Context) error {
	drv, sess, err := session(ctx)
	if err != nil {
		return err
	}
	defer drv.Close(ctx)
	defer sess.Close(ctx)

	rows := []any{}
	for i := range 5 {
		rows = append(rows, map[string]any{
			"vertex": int64(9000 + i),
			"urn":    fmt.Sprintf("pkg:npm/probe-%d", i),
		})
	}

	// Vertex upsert must be MERGE by id followed by SET. Folding urn into the MERGE pattern is
	// rejected, because the pattern is the identity being matched on.
	const upsert = `UNWIND $rows AS row MERGE (n {id: row.vertex}) SET n:Probe, n.urn = row.urn`
	if _, err := sess.Run(ctx, upsert, map[string]any{"rows": rows}); err != nil {
		return fmt.Errorf("vertex batch: %w", err)
	}
	fmt.Println("  OK      vertex batch (MERGE by id, then SET)")

	edges := []any{}
	for i := range 4 {
		edges = append(edges, map[string]any{
			"src": int64(9000 + i),
			"dst": int64(9001 + i),
			"rel": int64(19000 + i),
		})
	}
	const link = `UNWIND $rows AS row
  MATCH (s:Probe {id: row.src}), (d:Probe {id: row.dst})
  CREATE (s)-[:DEPENDS_ON {id: row.rel}]->(d)`
	if _, err := sess.Run(ctx, link, map[string]any{"rows": edges}); err != nil {
		return fmt.Errorf("edge batch: %w", err)
	}
	fmt.Println("  OK      edge batch (UNWIND MATCH ... CREATE)")

	res, err := sess.Run(ctx, `MATCH (n:Probe)-[:DEPENDS_ON]->(m) RETURN count(*) AS n`, nil)
	if err != nil {
		return err
	}
	rec, err := res.Single(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  OK      readback: %v edges\n", rec.Values[0])
	return nil
}

// probeSelectors answers the question the Cypher surface leaves open: CREATE INDEX does not exist,
// yet algo.MSpaths resolves sourceValues through what the docs call indexed selectors. If resolution
// is really a scan, selector latency grows with graph size and the proof path design has to change.
// Growing the graph and watching the curve is the only way to tell from outside.
func probeSelectors(ctx context.Context) error {
	drv, sess, err := session(ctx)
	if err != nil {
		return err
	}
	defer drv.Close(ctx)
	defer sess.Close(ctx)

	const chain = `UNWIND $rows AS row
  MATCH (s:Sel {id: row.src}), (d:Sel {id: row.dst})
  CREATE (s)-[:LINKS {id: row.rel}]->(d)`

	base := int64(1_000_000)
	planted := 0
	for _, size := range []int{2_000, 10_000, 40_000} {
		var verts, edges []any
		for i := planted; i < size; i++ {
			verts = append(verts, map[string]any{
				"vertex": base + int64(i),
				"urn":    fmt.Sprintf("sel:%d", i),
			})
			if i > planted {
				edges = append(edges, map[string]any{
					"src": base + int64(i-1),
					"dst": base + int64(i),
					"rel": base + int64(2_000_000+i),
				})
			}
		}
		for chunk := range slicesOf(verts, maxBatch) {
			if _, err := sess.Run(ctx,
				`UNWIND $rows AS row MERGE (n {id: row.vertex}) SET n:Sel, n.urn = row.urn`,
				map[string]any{"rows": chunk}); err != nil {
				return fmt.Errorf("plant vertices: %w", err)
			}
		}
		for chunk := range slicesOf(edges, maxBatch) {
			if _, err := sess.Run(ctx, chain, map[string]any{"rows": chunk}); err != nil {
				return fmt.Errorf("plant edges: %w", err)
			}
		}
		planted = size

		// Look up a value near the end of the range, so a scan cannot get lucky early.
		target := fmt.Sprintf("sel:%d", size-2)
		const call = `CALL algo.SSpaths({sourceLabel: 'Sel', sourceProperty: 'urn',
                        sourceValues: $vals, relTypes: ['LINKS'],
                        relDirection: 'outgoing', maxLen: 3, pathCount: 4})
      YIELD path RETURN path`
		t0 := time.Now()
		res, err := sess.Run(ctx, call, map[string]any{"vals": []any{target}})
		if err != nil {
			fmt.Printf("  n=%-6d selector call rejected: %v\n", size, truncate(err.Error(), 90))
			continue
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			fmt.Printf("  n=%-6d selector collect failed: %v\n", size, truncate(err.Error(), 90))
			continue
		}
		fmt.Printf("  n=%-6d selector lookup %7.1f ms  (%d paths)\n",
			size, float64(time.Since(t0).Microseconds())/1000, len(recs))
	}
	fmt.Println("  -> flat latency means indexed; latency growing with n means a scan")
	return nil
}

// probeCells asks whether a Bolt session can target a cell, and whether one query can traverse edges
// that span two cells. The answer decides whether ingest can shard across cells for parallel writers,
// since writer leases are per cell.
func probeCells(ctx context.Context) error {
	drv, err := neo4j.NewDriverWithContext(boltURI, neo4j.BasicAuth("neo4j", token, ""))
	if err != nil {
		return err
	}
	defer drv.Close(ctx)

	for _, name := range []string{"", "cell-0", "cell-1", "default"} {
		label := name
		if label == "" {
			label = "(unset)"
		}
		s := drv.NewSession(ctx, neo4j.SessionConfig{DatabaseName: name})
		res, err := s.Run(ctx, `MATCH (n:Probe) RETURN count(*) AS n`, nil)
		if err == nil {
			var rec *neo4j.Record
			rec, err = res.Single(ctx)
			if err == nil {
				fmt.Printf("  DatabaseName=%-9s OK, count=%v\n", label, rec.Values[0])
			}
		}
		if err != nil {
			fmt.Printf("  DatabaseName=%-9s rejected: %v\n", label, truncate(err.Error(), 90))
		}
		s.Close(ctx)
	}
	fmt.Println("  -> only one cell is configured here (GRAPH_CELLS=cell-0), so cross-cell")
	fmt.Println("     traversal needs a two-cell config before it can be tested properly")
	return nil
}

// probeThroughput measures sustained edge writes per second. Batch size is capped at 1024 by
// admission control, so the only remaining lever is concurrent sessions, and the question is whether
// the writer serialises them. Writer leases are per cell and this deployment has one cell, so all
// sessions contend for the same writer. Every scale decision in the project divides by this number.
func probeThroughput(ctx context.Context) error {
	drv, err := neo4j.NewDriverWithContext(boltURI, neo4j.BasicAuth("neo4j", token, ""))
	if err != nil {
		return err
	}
	defer drv.Close(ctx)

	const edgesPerRun = 60_000
	base := int64(10_000_000)

	fmt.Printf("  %-8s %-8s %10s %12s %10s\n", "batch", "workers", "edges", "edges/sec", "ms/batch")
	for _, tc := range []struct{ batch, workers int }{
		{256, 1}, {1024, 1}, {1024, 4}, {1024, 8}, {1024, 16},
	} {
		lo := base
		if err := plantVertices(ctx, drv, lo, edgesPerRun+1); err != nil {
			return err
		}

		rows := make([]any, 0, edgesPerRun)
		for i := range edgesPerRun {
			rows = append(rows, map[string]any{
				"src": lo + int64(i),
				"dst": lo + int64(i+1),
				"rel": lo + int64(500_000_000) + int64(i),
			})
		}

		chunks := make([][]any, 0, edgesPerRun/tc.batch+1)
		for c := range slicesOf(rows, tc.batch) {
			chunks = append(chunks, c)
		}

		t0 := time.Now()
		if err := runChunks(ctx, drv, chunks, tc.workers); err != nil {
			return fmt.Errorf("batch=%d workers=%d: %w", tc.batch, tc.workers, err)
		}
		el := time.Since(t0)
		fmt.Printf("  %-8d %-8d %10d %12.0f %10.1f\n", tc.batch, tc.workers, edgesPerRun,
			float64(edgesPerRun)/el.Seconds(),
			float64(el.Milliseconds())/float64(len(chunks)))

		base += int64(edgesPerRun) + 10
	}
	return nil
}

func plantVertices(ctx context.Context, drv neo4j.DriverWithContext, lo int64, n int) error {
	sess := drv.NewSession(ctx, neo4j.SessionConfig{})
	defer sess.Close(ctx)
	rows := make([]any, 0, n)
	for i := range n {
		rows = append(rows, map[string]any{"vertex": lo + int64(i)})
	}
	for chunk := range slicesOf(rows, maxBatch) {
		if _, err := sess.Run(ctx,
			`UNWIND $rows AS row MERGE (n {id: row.vertex}) SET n:Tp`,
			map[string]any{"rows": chunk}); err != nil {
			return fmt.Errorf("vertex prep: %w", err)
		}
	}
	return nil
}

// runChunks writes chunks across n sessions. Each worker owns its session, because a session is not
// safe for concurrent use.
func runChunks(ctx context.Context, drv neo4j.DriverWithContext, chunks [][]any, workers int) error {
	const link = `UNWIND $rows AS row
  MATCH (s:Tp {id: row.src}), (d:Tp {id: row.dst})
  CREATE (s)-[:TP {id: row.rel}]->(d)`

	work := make(chan []any)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess := drv.NewSession(ctx, neo4j.SessionConfig{})
			defer sess.Close(ctx)
			for chunk := range work {
				if _, err := sess.Run(ctx, link, map[string]any{"rows": chunk}); err != nil {
					select {
					case errs <- err:
					default:
					}
					return
				}
			}
		}()
	}
	for _, c := range chunks {
		work <- c
	}
	close(work)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

// slicesOf yields fixed size chunks of s, with a short final chunk.
func slicesOf(s []any, n int) func(func([]any) bool) {
	return func(yield func([]any) bool) {
		for i := 0; i < len(s); i += n {
			end := min(i+n, len(s))
			if !yield(s[i:end]) {
				return
			}
		}
	}
}
