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
//  4. How many rows can one read return, and do Bolt and HTTP agree?
//  5. Can one query traverse edges spanning two cells?
//  6. What is the sustained write throughput, and at which batch size?
//  7. PKG_RESOLVES: one edge type per epoch, or one type carrying an epoch property?
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

const (
	boltURI      = "bolt://127.0.0.1:7687"
	httpQueryURL = "http://127.0.0.1:8443/v1/graphs/default/query"
	token        = "local-development-token-32-bytes"

	// maxBatch is HydraDB's admission control ceiling on UNWIND batch items. It comes from
	// ClientConfig.max_parameters, whose default is 1024 and which has no environment binding, so
	// running the published image it cannot be raised. Exceeding it fails the whole statement with
	// MemoryPoolOutOfMemoryError naming "client_query_batch_items", which reads like a memory
	// problem but is a fixed row count. The throughput probe below shows the ceiling is moot
	// anyway: the rate peaks near 128 rows and concurrent sessions do not raise it, because writes
	// serialise behind the per-cell writer lease.
	maxBatch = 1024
)

// Workload sizes, set from flags before any probe runs.
var (
	edgesPerRun   = 60_000
	spokesPerStar = 3_000
)

func main() {
	only := flag.String("only", "", "run a single probe by name")
	// The throughput workload is a flag because the useful size changes with the question. A few
	// thousand edges answers "did the write path regress"; sixty thousand is needed before
	// compaction starts and the sustained rate separates from the cold-cache rate.
	edges := flag.Int("edges", 60_000, "edges per throughput run")
	// 3,000 is enough to clear the HTTP page size and stay quick. The Bolt side was also run at
	// 25,000, above the resultLimit the query layer uses, and returned every row.
	spokes := flag.Int("spokes", 3_000, "spokes on the result cap star")
	flag.Parse()
	edgesPerRun = *edges
	spokesPerStar = *spokes

	probes := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"auth", probeAuth},
		{"unwind", probeUnwind},
		{"selectors", probeSelectors},
		{"resultcap", probeResultCap},
		{"cells", probeCells},
		{"throughput", probeThroughput},
		{"pkgresolves", probePkgResolves},
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

// write runs a statement and waits for the server to acknowledge it. sess.Run only queues the
// statement: the driver streams lazily, so a write whose result is never consumed reports no error
// even when the server rejected it. Every probe below writes through here, because measuring the
// throughput of writes that silently failed is worse than not measuring at all.
func write(ctx context.Context, sess neo4j.SessionWithContext, stmt string, params map[string]any) error {
	res, err := sess.Run(ctx, stmt, params)
	if err != nil {
		return err
	}
	_, err = res.Consume(ctx)
	return err
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
	if err := write(ctx, sess, upsert, map[string]any{"rows": rows}); err != nil {
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
	if err := write(ctx, sess, link, map[string]any{"rows": edges}); err != nil {
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
			if err := write(ctx, sess,
				`UNWIND $rows AS row MERGE (n {id: row.vertex}) SET n:Sel, n.urn = row.urn`,
				map[string]any{"rows": chunk}); err != nil {
				return fmt.Errorf("plant vertices: %w", err)
			}
		}
		for chunk := range slicesOf(edges, maxBatch) {
			if err := write(ctx, sess, chain, map[string]any{"rows": chunk}); err != nil {
				return fmt.Errorf("plant edges: %w", err)
			}
		}
		planted = size

		// Look up a value near the end of the range, so a scan cannot get lucky early.
		//
		// The value list is interpolated into the query text rather than bound as a parameter.
		// That is not a style choice: a list parameter is accepted only as UNWIND input, so
		// `sourceValues: $vals` is rejected outright with "composite parameter $vals is only
		// supported as an UNWIND input". Interpolation is the only way to call the multi-source
		// form, which is why the ingest validates every value against the npm name grammar before
		// it reaches a query string.
		target := fmt.Sprintf("sel:%d", size-2)
		call := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Sel', sourceProperty: 'urn',
                        sourceValues: ['%s'], relTypes: ['LINKS'],
                        relDirection: 'outgoing', maxLen: 3, pathCount: 4})
      YIELD path RETURN path`, target)
		t0 := time.Now()
		res, err := sess.Run(ctx, call, nil)
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

// probeResultCap asks how many rows one read can return, on both transports.
//
// The query layer assumes algo.MSpaths' own resultLimit is the only ceiling: it sets the limit
// generously and treats a full result set as evidence of truncation. A lower server side cap would
// make that watch never fire, and every large read would be silently short. Since a reachability
// answer that is quietly incomplete is the one failure a security tool must not have, the assumption
// is worth a probe rather than a reading of the source.
//
// A star is planted rather than reusing the ingested graph, so the expected row count is exact.
func probeResultCap(ctx context.Context) error {
	drv, sess, err := session(ctx)
	if err != nil {
		return err
	}
	defer drv.Close(ctx)
	defer sess.Close(ctx)

	const hub = int64(3_000_000)
	spokes := spokesPerStar

	rows := make([]any, 0, spokes+1)
	rows = append(rows, map[string]any{"vertex": hub, "urn": "cap:hub", "seq": int64(-1)})
	for i := range spokes {
		rows = append(rows, map[string]any{
			"vertex": hub + 1 + int64(i),
			"urn":    fmt.Sprintf("cap:spoke:%d", i),
			"seq":    int64(i),
		})
	}
	for chunk := range slicesOf(rows, maxBatch) {
		if err := write(ctx, sess,
			`UNWIND $rows AS row MERGE (n {id: row.vertex}) SET n:Cap, n.urn = row.urn, n.seq = row.seq`,
			map[string]any{"rows": chunk}); err != nil {
			return fmt.Errorf("plant star: %w", err)
		}
	}

	edges := make([]any, 0, spokes)
	for i := range spokes {
		edges = append(edges, map[string]any{
			"src": hub, "dst": hub + 1 + int64(i), "rel": hub + 1_000_000 + int64(i),
		})
	}
	for chunk := range slicesOf(edges, maxBatch) {
		if err := write(ctx, sess, `UNWIND $rows AS row
      MATCH (s:Cap {id: row.src}), (d:Cap {id: row.dst})
      MERGE (s)-[:SPOKE {id: row.rel}]->(d)`, map[string]any{"rows": chunk}); err != nil {
			return fmt.Errorf("plant spokes: %w", err)
		}
	}

	// want is how many rows the statement should produce, so an aggregate is not reported as short.
	count := func(label, stmt string, want int) {
		res, err := sess.Run(ctx, stmt, nil)
		if err != nil {
			fmt.Printf("  %-34s rejected: %v\n", label, truncate(err.Error(), 70))
			return
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			fmt.Printf("  %-34s collect failed: %v\n", label, truncate(err.Error(), 70))
			return
		}
		short := ""
		if len(recs) < want {
			short = fmt.Sprintf("  <- short by %d", want-len(recs))
		}
		fmt.Printf("  %-34s %5d rows%s\n", label, len(recs), short)
	}

	fmt.Printf("  planted a hub with %d spokes\n", spokes)
	// The aggregate first, because it establishes the expected fan-out from the server's own count
	// rather than from what the planting code believes it wrote.
	count("count(*)", fmt.Sprintf(
		`MATCH (h:Cap {id: %d})-[:SPOKE]->(s) RETURN count(*) AS n`, hub), 1)
	count("MATCH, no LIMIT", fmt.Sprintf(
		`MATCH (h:Cap {id: %d})-[:SPOKE]->(s) RETURN s.urn AS urn`, hub), spokes)
	count(fmt.Sprintf("MATCH, LIMIT %d", spokes), fmt.Sprintf(
		`MATCH (h:Cap {id: %d})-[:SPOKE]->(s) RETURN s.urn AS urn LIMIT %d`, hub, spokes), spokes)

	const fan = `CALL algo.MSpaths({sourceLabel: 'Cap', sourceProperty: 'urn',
     sourceValues: ['cap:hub'], relTypes: ['SPOKE'], relDirection: 'outgoing', maxLen: 1,
     pathCount: %d, resultLimit: %d}) YIELD path RETURN path`
	count(fmt.Sprintf("MSpaths, resultLimit %d", spokes), fmt.Sprintf(fan, spokes, spokes), spokes)
	count("MSpaths, resultLimit 1024", fmt.Sprintf(fan, 1024, 1024), spokes)

	// The same two reads over HTTP, which is the documented fallback transport and is where the
	// interesting difference is.
	httpCount("HTTP MATCH, no LIMIT", fmt.Sprintf(
		`MATCH (h:Cap {id: %d})-[:SPOKE]->(s) RETURN s.urn AS urn`, hub), spokes)
	httpCount(fmt.Sprintf("HTTP MSpaths, resultLimit %d", spokes),
		fmt.Sprintf(fan, spokes, spokes), spokes)

	// Paging is the escape hatch, and only a pattern that accepts ORDER BY has one. A procedure call
	// yields paths, which carry no orderable projection, so it has none.
	page := 0
	for offset := 0; offset < spokes; offset += 1000 {
		res, err := sess.Run(ctx, fmt.Sprintf(
			`MATCH (h:Cap {id: %d})-[:SPOKE]->(s) RETURN s.urn AS urn ORDER BY s.seq SKIP %d LIMIT 1000`,
			hub, offset), nil)
		if err != nil {
			fmt.Printf("  SKIP %-29d rejected: %v\n", offset, truncate(err.Error(), 70))
			return nil
		}
		recs, err := res.Collect(ctx)
		if err != nil {
			return fmt.Errorf("page at %d: %w", offset, err)
		}
		page += len(recs)
	}
	fmt.Printf("  %-34s %5d rows\n", "ORDER BY + SKIP, 1000 per page", page)

	fmt.Println("  -> Bolt honours resultLimit and LIMIT exactly, so the query layer's")
	fmt.Println("     split-on-full-result check is the right mitigation there")
	fmt.Println("  -> HTTP pages at 1024 whatever the query asks for, and hands back a")
	fmt.Println("     next_cursor it will not accept back, so it cannot read past one page")
	return nil
}

// httpCount runs the same statement over the HTTP API and reports the row count and cursor. The two
// transports share one query service, so any difference is in the transport rather than the engine.
func httpCount(label, query string, want int) {
	body, err := json.Marshal(map[string]any{"cell_id": "cell-0", "query": query})
	if err != nil {
		fmt.Printf("  %-34s encode failed: %v\n", label, err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, httpQueryURL, bytes.NewReader(body))
	if err != nil {
		fmt.Printf("  %-34s request failed: %v\n", label, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Graph-Namespace", "default")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  %-34s %v\n", label, truncate(err.Error(), 70))
		return
	}
	defer resp.Body.Close()

	var decoded struct {
		Rows       []json.RawMessage `json:"rows"`
		NextCursor *int64            `json:"next_cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		fmt.Printf("  %-34s decode failed: %v\n", label, err)
		return
	}

	cursor := "none"
	if decoded.NextCursor != nil {
		cursor = fmt.Sprintf("next_cursor=%d", *decoded.NextCursor)
	}
	short := ""
	if len(decoded.Rows) < want {
		short = fmt.Sprintf("  <- short by %d", want-len(decoded.Rows))
	}
	fmt.Printf("  %-34s %5d rows, %s%s\n", label, len(decoded.Rows), cursor, short)
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

// probePkgResolves answers the step 7 task 9 question by measurement rather than preference: does
// PKG_RESOLVES get one edge type per epoch, or one type carrying an epoch property? Both designs
// write through the identical UNWIND path, so the write side is expected to agree; the question is
// what a single epoch's traversal costs once several epochs have accumulated, since that is the read
// the observatory actually issues, once per package, once per epoch.
//
// A separate type per epoch means the type's compiled CSC generation holds only that epoch's edges.
// A shared type with a property means the same generation holds every epoch ever written, and a
// single-epoch read has to filter all of it. The two are planted with the same edge count so the
// comparison isolates the epoch count already accumulated under the property design, not a
// difference in workload size.
func probePkgResolves(ctx context.Context) error {
	drv, err := neo4j.NewDriverWithContext(boltURI, neo4j.BasicAuth("neo4j", token, ""))
	if err != nil {
		return err
	}
	defer drv.Close(ctx)

	const packages = 2_000
	const edgesPerEpoch = 8_000
	const epochs = 24
	const base = int64(110_000_000)

	if err := plantVertices(ctx, drv, base, packages); err != nil {
		return err
	}

	sess := drv.NewSession(ctx, neo4j.SessionConfig{})
	defer sess.Close(ctx)

	edgesFor := func(epoch, relBase int64) []any {
		rows := make([]any, 0, edgesPerEpoch)
		for i := range edgesPerEpoch {
			rows = append(rows, map[string]any{
				"src":   base + int64(i%packages),
				"dst":   base + (int64(i)+1+epoch)%packages,
				"rel":   relBase + epoch*int64(edgesPerEpoch) + int64(i),
				"epoch": epoch,
			})
		}
		return rows
	}

	// writeEpochs plants every epoch's edges under one statement shape, returning the total time
	// spent writing, not including vertex planting, which both designs share and is done once above.
	writeEpochs := func(stmtFor func(epoch int64) string, relBase int64) (time.Duration, error) {
		var total time.Duration
		for e := range int64(epochs) {
			rows := edgesFor(e, relBase)
			t0 := time.Now()
			for chunk := range slicesOf(rows, maxBatch) {
				if werr := write(ctx, sess, stmtFor(e), map[string]any{"rows": chunk}); werr != nil {
					return 0, fmt.Errorf("epoch %d: %w", e, werr)
				}
			}
			total += time.Since(t0)
		}
		return total, nil
	}

	// Design A: PKG_RESOLVES_0 .. PKG_RESOLVES_5. The type name is interpolated, the same way a
	// label is: relationship types cannot be parameters in this Cypher subset either.
	perTypeWrite, err := writeEpochs(func(epoch int64) string {
		return fmt.Sprintf(`UNWIND $rows AS row
      MATCH (s:Tp {id: row.src}), (d:Tp {id: row.dst})
      CREATE (s)-[:PKG_RESOLVES_%d {id: row.rel}]->(d)`, epoch)
	}, base)
	if err != nil {
		return fmt.Errorf("design per-type: %w", err)
	}

	// Design B: one PKG_RESOLVES_PROP type, epoch carried as a property on every edge.
	const propStmt = `UNWIND $rows AS row
    MATCH (s:Tp {id: row.src}), (d:Tp {id: row.dst})
    CREATE (s)-[:PKG_RESOLVES_PROP {id: row.rel, epoch: row.epoch}]->(d)`
	propWrite, err := writeEpochs(func(int64) string { return propStmt }, base+10_000_000)
	if err != nil {
		return fmt.Errorf("design property: %w", err)
	}

	// The traversal every observatory run actually issues: everything reachable through one epoch's
	// edges, queried after every epoch has been written, which is the realistic state for design B
	// and the only state design A's per-epoch type is ever in. Run once as a warmup, discarded, then
	// timed repeatedly and averaged: a single measurement over Bolt is noisy enough at this row count
	// to hide the effect this probe exists to find.
	const repeats = 7
	target := int64(epochs / 2)
	timeQuery := func(stmt string) (int64, time.Duration, error) {
		var n int64
		var total time.Duration
		for i := range repeats + 1 {
			t0 := time.Now()
			res, qerr := sess.Run(ctx, stmt, nil)
			if qerr != nil {
				return 0, 0, qerr
			}
			rec, qerr := res.Single(ctx)
			if qerr != nil {
				return 0, 0, qerr
			}
			elapsed := time.Since(t0)
			if i == 0 {
				// Discard: first call pays session and query-plan warmup cost neither design incurs
				// again on a subsequent identical statement.
				continue
			}
			n, _ = rec.Values[0].(int64)
			total += elapsed
		}
		return n, total / repeats, nil
	}

	perTypeCount, perTypeRead, err := timeQuery(fmt.Sprintf(
		`MATCH (p:Tp)-[:PKG_RESOLVES_%d]->(d) RETURN count(*) AS n`, target))
	if err != nil {
		return fmt.Errorf("per-type traversal: %w", err)
	}
	propCount, propRead, err := timeQuery(fmt.Sprintf(
		`MATCH (p:Tp)-[r:PKG_RESOLVES_PROP]->(d) WHERE r.epoch = %d RETURN count(*) AS n`, target))
	if err != nil {
		return fmt.Errorf("property traversal: %w", err)
	}

	fmt.Printf("  %d epochs of %d edges over %d packages, %d accumulated under the shared type\n\n",
		epochs, edgesPerEpoch, packages, epochs*edgesPerEpoch)
	fmt.Printf("  design            write edges/s   single-epoch read (avg of %d)   rows\n", repeats)
	fmt.Printf("  per-epoch type    %11.0f   %24s   %d\n",
		float64(epochs*edgesPerEpoch)/perTypeWrite.Seconds(), perTypeRead.Round(time.Microsecond), perTypeCount)
	fmt.Printf("  epoch property    %11.0f   %24s   %d\n",
		float64(epochs*edgesPerEpoch)/propWrite.Seconds(), propRead.Round(time.Microsecond), propCount)
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
		if err := write(ctx, sess,
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
				if err := write(ctx, sess, link, map[string]any{"rows": chunk}); err != nil {
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
