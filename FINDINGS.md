# HydraDB findings

Behaviour we measured while building Keyholders against HydraDB `ghcr.io/hydra-db/hydradb:latest`
(digest `sha256:db78309a`), single node, one cell.

Several of these behaviours are not documented, and a few are surprising enough that we would have
designed around them incorrectly if we had assumed rather than measured. Every entry below is
reproducible with `go run ./cmd/probe`, which is kept in the tree for exactly that reason.

Nothing here is a complaint. HydraDB is at `0.1.0` and most of these limits are deliberate admission
control doing its job. What follows is the shape of the envelope as we found it.

**Storage backend.** These were first taken against `CLOUD_PROVIDER=local`. Finding 1
explains why that configuration cannot be used for anything durable, and every number here has since
been re-measured against MinIO, which is what `deploy/docker-compose.yml` now runs.

---

## 1. The local object store cannot do conditional puts, so writes die permanently once the store grows

This is the finding with the largest consequence, and the one that is hardest to diagnose from the
client, because **reads keep working perfectly while every write fails.**

With `CLOUD_PROVIDER=local`, SlateDB's object store is `object_store`'s `LocalFileSystem`, which
returns `NotImplemented` for a compare-and-swap put:

```
object store error: Operation `put_opts` with mode `PutMode::Update`
not yet implemented by LocalFileSystem(file:///data/store).
```

A fresh store works. It keeps working across restarts. Then, once enough data has been written that
SlateDB needs to rotate its manifest, every subsequent write fails with:

```
Neo.DatabaseError.General.UnknownError (internal query execution error)
```

That error names nothing. The real cause appears only in the container log, as a `WARN` reading
`Bolt suppressed internal graph error`. Background garbage collection announces the same problem
minutes earlier, once per minute, which is the only advance warning:

```
error collecting garbage [resource=Manifest,
  error=ObjectStoreError(NotImplemented { operation: "`put_opts` with mode `PutMode::Update`" })]
```

**Reproduction.** Start with `CLOUD_PROVIDER=local`, write a few hundred thousand edges, restart the
node, then attempt any write. Reads succeed, writes fail, and the store never recovers.

**Fix.** Run an S3-compatible object store. `deploy/docker-compose.yml` runs MinIO with
`AWS_CONDITIONAL_PUT=etag`, which is the configuration the engine is designed against. Storage is
selected by `CLOUD_PROVIDER`, accepting `local`, `memory`, `aws`, `azure` and `gcp`; the `aws` path
builds its client from the standard `AWS_*` environment, so any S3-compatible endpoint works.

**Suggestion.** Two things would each have saved a day here. First, surface the underlying object
store error to the client instead of replacing it with `UnknownError`, or at least log it at `ERROR`
rather than `WARN`. Second, refuse to start with `CLOUD_PROVIDER=local` unless the store advertises
conditional put, since a configuration that works until it silently stops is worse than one that
never starts. The README's Docker quickstart uses `CLOUD_PROVIDER=local`, which is the shortest path
to this failure.

## 2. UNWIND batches are capped at 1024 rows, and the error names memory

`UNWIND` with more than 1024 rows fails:

```
Neo.TransientError.General.MemoryPoolOutOfMemoryError
client_query_batch_items rejected by admission control: actual 2000 exceeds limit 1024
```

The limit is `ClientConfig.max_parameters`, defaulting to `DEFAULT_MAX_PARAMETERS = 1024`
(`src/client/service.rs:37`), enforced at `src/client/service.rs:1285` and `:1617`. It is settable
through the `with_max_parameters` builder, but **we could not find an environment binding**, so it is
fixed at 1024 when running the published image.

Two things make this costly to discover:

1. The error class is `MemoryPoolOutOfMemoryError`, which reads as a memory pressure problem. It is a
   fixed row count and is unrelated to available memory.
2. Ingest throughput is usually tuned by raising batch size, and here that direction is closed.

In practice the ceiling turned out not to bind, because throughput peaks well below it (finding 8).

**Suggestion.** A distinct error class such as `LimitExceeded` would be clearer than one naming a
memory pool.

## 3. Composite parameters are rejected outside UNWIND, which affects `algo.MSpaths`

Passing a list as a query parameter:

```cypher
CALL algo.MSpaths({sourceValues: $vals, ...})
```

fails with:

```
Neo.ClientError.Statement.InvalidSyntax
ClientProtocol query is not supported yet: composite parameter $vals is only supported as an UNWIND input
```

This is consistent with the documented rule that a list parameter is only accepted as `UNWIND` input.
The consequence for the path procedures is not obvious though: **`algo.MSpaths` multi source calls
must have their value list interpolated into the query text**, because the only way to supply a list
is literally.

That is workable but it moves an input validation burden onto the caller. We validate every value
against the npm package name grammar before interpolation rather than relying on quoting.

**Suggestion.** Allowing a list parameter specifically for `algo.*` config values would remove the
need for callers to build query strings.

## 4. `sourceValues` must be strings, so integer node ids cannot drive a multi source call

```cypher
CALL algo.MSpaths({sourceValues: [1000100, 1000200], ...})
-> Neo.ClientError.Statement.InvalidSyntax
   OpenCypher parse error: sourceValues must be a list of strings
```

The single source forms take an integer node id (`sourceNode`), and the multi source form takes string
property values. So a caller that already knows its integer ids, as we do because ours are derived
deterministically, cannot use them for a batched call and must go through a string property instead.

Both forms work well; they just do not compose.

## 5. Property selector resolution is genuinely indexed

This was our largest open question, because `CREATE INDEX` does not exist anywhere in the supported
Cypher surface, yet the path procedures resolve `sourceProperty` and `sourceValues` through what the
documentation calls indexed selectors. If that were a scan, selector latency would grow with label
size and our design would have needed rethinking.

It is indexed. `algo.MSpaths` by `urn`, looking up a value deliberately placed near the end of the
range:

| Label size | Latency |
|---|---|
| 2,000 nodes | 5.5 ms |
| 10,000 nodes | 3.9 ms |
| 40,000 nodes | **5.4 ms** |

Twenty times the nodes for no measurable change. Selector resolution costs effectively the same at
any size we tested, with no index declaration required.

## 6. `algo.MSpaths` amortises across sources as documented

The claim that `MSpaths` shares selector hydration, topology and adjacency across source and target
pairs holds up: ten times the sources costs roughly twice the wall clock, so the per-source cost
falls by about 5x between one source and five hundred. This is what makes returning a proof path per
keyholder affordable.

## 7. Writes are serialised, so concurrency does not raise throughput

60,000 edges written as `UNWIND MATCH ... MERGE`, one cell:

| Batch | Workers | Edges/sec |
|---|---|---|
| 256 | 1 | **4,426** |
| 1024 | 1 | 3,354 |
| 1024 | 4 | 3,159 |
| 1024 | 8 | 3,536 |
| 1024 | 16 | 2,911 |

Four, eight and sixteen concurrent sessions perform no better than one, and sixteen is measurably
worse. This is consistent with the documented design, where a durable writer lease selects one active
writer per cell, so all sessions against a single cell contend for the same writer. Worth stating
explicitly for anyone planning a bulk load: **parallel client sessions are not a throughput lever.**

## 8. Batch size has an optimum, and it moves with graph size

At 6,000 edges on a small graph, 1024 rows is fastest at 8,343 edges/sec against 5,844 at 256. At
60,000 edges on a graph large enough to be compacting, the order reverses: 4,426 at 256 against 3,354
at 1024.

Tuning batch size on a small graph therefore picks the wrong answer. We use 256, from the larger run.

Sustained throughput is about **3,400 to 4,400 edges/sec** on this hardware, degrading as the graph
grows. We plan on 3,500 and treat further degradation as expected rather than surprising.

## 9. An UNWIND batch cannot assign two different values to the same vertex property

```cypher
UNWIND $rows AS row MERGE (n {id: row.vertex}) SET n:Dup, n.rank = row.rank
```

with rows `[{vertex: 1, rank: 1}, {vertex: 1, rank: 0}]` fails:

```
Neo.ClientError.Statement.InvalidSyntax
GraphQuery query is not supported yet: conflicting metadata values for vertex 1 property rank
```

The exact rule, established by probing each case separately:

| Case | Result |
|---|---|
| Same vertex twice, identical value | accepted |
| Same vertex twice, different values | **rejected, whole statement fails** |
| Distinct vertices | accepted |
| Same vertex, different values, separate statements | accepted, last write wins |

This is easy to hit in a bulk load, because the natural way to write a graph is to emit a node row
wherever the node is mentioned, and two mentions can legitimately carry different detail. Ours did:
a package in the ranked input list carries a download rank, and the same package reached as somebody
else's dependency does not.

It also fails the whole batch rather than the offending row, so one conflicting pair loses the other
255 rows with it.

We fixed it by writing the two cases with separate statements, so the property that can differ
appears in only one of them, and by deduplicating rows by vertex id before each write.

**Suggestion.** Naming the two conflicting values in the error would make this a one minute fix
rather than a bisect. `InvalidSyntax` is also a slightly misleading class for a data condition.

## 10. Label scans are capped at 250,000 candidates

```cypher
MATCH (n:Tp) RETURN count(*)
-> Neo.TransientError.General.MemoryPoolOutOfMemoryError
   cypher_vertex_label_index_candidates rejected by admission control: actual 250001 exceeds limit 250000
```

A label with more than 250,000 nodes cannot be scanned or counted by a plain `MATCH`. This is the
limit with the largest design consequence for us, because any whole graph pass has to be expressed as
bounded pages rather than a scan.

## 11. A full edge type count exceeds the query timeout

```cypher
MATCH ()-[r:TP]->() RETURN count(*)
-> Neo.ClientError.Transaction.Terminated
   cypher_relationship_edge_records exceeded query timeout after 29999 ms; limit is 29999 ms
```

Roughly 300,000 edges of one type could not be counted inside the 29,999 ms budget. A second attempt
failed in `cypher_relationship_metadata_hydration` instead, so the cost is in hydration rather than in
enumeration.

Findings 10 and 11 together mean **there is no cheap way to ask how big the graph is**, which is
mildly awkward for progress reporting during a bulk load. The ingest maintains its own counters
instead.

## 12. LIMIT does not reduce the cost of an unanchored pattern

Findings 10 and 11 are usually described as limits on scans and aggregates. The practical form is
stronger, and it is what shapes every read in this project: **a pattern that is not anchored at a
known node id is materialized in full before `LIMIT` or `WHERE` is applied.**

All three of these exceed the 30 second timeout on a graph of 105,000 nodes:

```cypher
MATCH (v:Version)-[r:DEPENDS_ON]->(b:Package) RETURN v.id AS vid LIMIT 3
MATCH (v:Version)-[r:DEPENDS_ON]->(b:Package) WHERE v.id < 4611686018427387904 RETURN v.urn AS u LIMIT 3
MATCH (p:Package)-[r:HAS_VERSION]->(v:Version) WHERE r.published_at > 1700000000 RETURN v.urn AS u LIMIT 3
```

Anchoring one end at an id returns immediately on the same graph:

```cypher
MATCH (p:Package {id: 5579749480056184795})-[:HAS_VERSION]->(v:Version) RETURN v.version AS ver
MATCH (v:Version)-[e:DEPENDS_ON]->(p:Package {id: 5579749480056184795}) RETURN v.urn AS urn, e.range AS range
```

`WHERE` itself is fine once the pattern is anchored, including `AND`, `<=` and relationship
properties. It just cannot be used to make an unanchored pattern affordable.

The second query above is the one that matters most: **anchoring at the target and walking inbound is
as fast as walking outbound**, which is the documented reason inbound topology records exist. It let
the resolver organise its work by dependency target, reading each version timeline once instead of
once per dependent.

**Suggestion.** Pushing `LIMIT` into the match, or rejecting an unanchored pattern outright with a
message saying so, would both be better than a 30 second wait. As it stands the query looks like it
should work and simply never returns.

## 13. Bolt cannot select a cell

`SessionConfig.DatabaseName` selects the graph, not the cell:

| `DatabaseName` | Result |
|---|---|
| unset | accepted |
| `default` | accepted, same graph |
| `cell-0` | rejected, `ClientProtocol query is not supported` |
| `cell-1` | rejected, same |

Cell targeting is available over HTTP, where `cell_id` is a request body field, but not over Bolt.
Combined with finding 7, sharding a bulk load across cells would require several `graph-node`
processes, and queries would then be unable to traverse across them, so we did not pursue it.

## 14. Bolt authentication accepts any non-empty username

Not documented. The auth token is validated as the **password**, and the username is ignored provided
it is not empty.

| Credential | Result |
|---|---|
| `BasicAuth("neo4j", token, "")` | accepted |
| `BasicAuth("hydradb", token, "")` | accepted |
| `BasicAuth(token, token, "")` | accepted |
| `BearerAuth(token)` | accepted |
| `CustomAuth("bearer", "", token, "", nil)` | accepted |
| `BasicAuth("", token, "")` | rejected, `Neo.ClientError.Security.Unauthorized` |
| `NoAuth()` | rejected, `Neo.ClientError.Security.Unauthorized` |

We use `BasicAuth("neo4j", token, "")` because it is what third party Neo4j tooling expects.

**Note for anyone probing this.** `VerifyConnectivity` is not sufficient to test a credential. The
driver defers the handshake, so a bad credential is only reported at first statement execution. The
probe runs a real query per candidate.

**Suggestion.** Documenting the expected credential shape, or rejecting an obviously wrong username,
would save the next person an hour.

## 15. A write whose result is never consumed reports no error

This one is the Neo4j Go driver's behaviour rather than HydraDB's, but it interacts badly with
finding 1 and it cost us a day, so it is recorded here for the next person.

`session.Run` queues a statement and returns a lazy result. A write that the server rejects therefore
looks like a success unless the result is consumed:

```go
res, err := sess.Run(ctx, stmt, params)  // err is nil even for a rejected write
_, err = res.Consume(ctx)                // the rejection surfaces here
```

Our own probe made this mistake, which is why finding 1 went unnoticed while every write was failing,
and why an early throughput number was measuring statements the server never accepted. Every write in
this repository now goes through a helper that consumes.

## 16. Two operational notes that cost time elsewhere

Both are documented in the HydraDB README and both are easy to skip past.

- `RUST_MIN_STACK=33554432` is required. Without it the node builds, answers `/readyz`, then aborts on
  the first query. The failure looks like a crash bug rather than a missing setting.
- The container runs as UID 10001, so a bind mounted data directory owned by the host user needs
  `--user "$(id -u):$(id -g)"` or the first storage operation fails.

Startup is quick: `/readyz` returned 200 one to two seconds after container start.

## 17. `resultLimit` truncates silently, with no error and no cursor

`algo.MSpaths` returning fewer paths than exist is indistinguishable from it returning all of them:

```cypher
CALL algo.MSpaths({..., resultLimit: 5}) YIELD path RETURN path
```

on a source with 17 outgoing edges returns exactly 5 rows, `next_cursor: null`, and no error or
warning. The same call with a generous limit returns all 17.

For a traversal this is the dangerous direction. A frontier expansion that quietly loses edges
produces an undercount, and an undercounted answer to "who can execute code on your machine" looks
exactly like a good result.

We treat a full result set as evidence of possible truncation: when a call returns exactly
`resultLimit` rows and had more than one source, the source list is split in half and both halves are
re-run. That costs one extra query in the rare exact-fit case and removes the failure mode entirely.

**Suggestion.** Either populate `next_cursor` when results were cut, or return a flag saying so.
Pagination state already exists for this procedure, so the information is present; it just is not
surfaced on the truncating path.

## 18. Returned paths carry full relationship properties, which is what makes client-side interval logic possible

A positive finding, and the one that decided the query design.

`YIELD path` returns nodes with all their properties *and* relationships with all of theirs:

```json
{"id": 550393, "edge_type": "RESOLVES_TO",
 "src": 6033986735605758548, "dst": 289911684998391487,
 "properties": {"range": {"String": "^2.1.3"},
                "valid_from": {"Integer": 1743039553},
                "valid_to": {"Integer": 4102444800}}}
```

This matters because interval intersection cannot be expressed in this Cypher subset. Since the
procedure hands back every edge's validity window, the intersection is done in the client over data
the graph already returned, rather than needing a procedure that does not exist. The same property
makes `MSpaths` usable as a batched one-hop frontier expander with `maxLen: 1`, which is what turns a
frontier of hundreds of nodes into a single query.

One restriction worth knowing: `fairRelationshipVariants` is rejected outside `pairwise` mode
(`fairRelationshipVariants is only supported by pairwise algo.MSpaths`), and pairwise matches sources
to targets by position rather than searching every source for every target, so the two cannot be
combined for a many-sources-one-target proof path query.

## 19. The HTTP API stops at 1,024 rows and hands back a cursor it will not accept back

The two transports do not return the same answer to the same query. `go run ./cmd/probe -only
resultcap` plants a hub with 3,000 spokes and reads it back both ways:

| Read | Bolt | HTTP |
|---|---|---|
| `MATCH (h)-[:SPOKE]->(s) RETURN s.urn` | 3,000 | 1,024, `next_cursor: 22` |
| `MATCH … LIMIT 3000` | 3,000 | 1,024, `next_cursor` set |
| `algo.MSpaths(… resultLimit: 3000)` | 3,000 | 1,024, `next_cursor` set |
| `algo.MSpaths(… resultLimit: 1024)` | 1,024 | 1,024 |
| `ORDER BY … SKIP n LIMIT 1000`, paged | 3,000 | n/a |

Bolt honours `LIMIT` and `resultLimit` exactly. Re-run at 25,000 spokes it still returns every row,
which is above the 20,000 `resultLimit` the query layer uses, so nothing lower than the procedure's
own limit is in the way. HTTP stops at 1,024 whatever the query asked for.

Unlike finding 17 the HTTP truncation is at least signalled, since `next_cursor` is non-null. It is
not, however, usable. Sending the cursor back with the query is rejected:

```
{"code":"invalid_request",
 "message":"ClientProtocol query is not supported yet: result cursor does not belong to this query
 request"}
```

Omitting `query` and sending only the cursor fails deserialisation (`missing field query`), and there
is no cursor endpoint (`/v1/graphs/default/query/cursor/{id}` is a 404). So on this build the HTTP
API cannot read past its first page by any route we could find.

The practical consequence is that HTTP is not a drop-in fallback for Bolt on read paths, only on
writes and on reads known to be small. That is worth stating plainly, because a fallback that returns
a well-formed short answer is worse than one that fails: 1,024 rows of a 3,000 row traversal is a
plausible-looking undercount.

`SKIP` with `ORDER BY` does work, on both transports, so an ordered `MATCH` can be paged past any
cap. A procedure call cannot, because `YIELD path` has no orderable projection to page on.

**Suggestion.** Accept the returned `next_cursor` on a subsequent request, or document the page size
so callers know to page with `SKIP` instead.

---

## What we changed in our design because of these

| Finding | Design consequence |
|---|---|
| 1 | The deployment runs MinIO, not a local directory. This is not a preference: the local store cannot hold a graph of the size this project needs |
| 2, 7, 8 | Ingest is a single writer at about 3,500 edges/sec, batching 256 rows. The scale target is set from that number rather than from an estimate |
| 3, 4 | `algo.MSpaths` value lists are interpolated into query text, with values validated against the npm name grammar first |
| 5, 6 | Proof paths are affordable, and a single batched call serves many keyholders. No index declaration needed |
| 9 | Node rows are deduplicated by vertex id before every write, and a property that can legitimately differ between two sources of the same node is written by its own statement |
| 10, 11, 12 | Every read is anchored at a known node id, and whole-graph work is driven from our own lists rather than from a scan. The resolver is organised by dependency target so that inbound traversal does the work. We keep our own counters |
| 13 | One cell. Sharding is not pursued |
| 15 | Every write consumes its result. A benchmark that does not is measuring the client |
| 17 | A result set that exactly fills `resultLimit` is treated as truncated and the source list is split. Silent undercounting is the one failure a reachability query must not have |
| 18 | Interval intersection happens in the client, over the edge properties the path already carries. `MSpaths` with `maxLen: 1` is the batched frontier expander the interval search runs on |
| 19 | Every read goes over Bolt. HTTP is kept for writes and for reads bounded well under 1,024 rows, and is no longer described as a general fallback |
