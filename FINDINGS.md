# HydraDB findings

Behaviour we measured while building Keyholders against HydraDB `ghcr.io/hydra-db/hydradb:latest`
(digest `sha256:db78309a`), single node, `CLOUD_PROVIDER=local`, one cell.

Several of these behaviours are not documented, and a few are surprising enough that we would have
designed around them incorrectly if we had assumed rather than measured. Every entry below is
reproducible with `go run ./cmd/probe`, which is kept in the tree for exactly that reason.

Nothing here is a complaint. HydraDB is at `0.1.0` and the limits are mostly deliberate admission
control doing its job. What follows is the shape of the envelope as we found it.

---

## 1. Bolt authentication accepts any non-empty username

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

**Suggestion.** An environment override for `max_parameters` would be valuable, since bulk load
throughput is bounded by it. A distinct error class such as `LimitExceeded` would also be clearer than
one naming a memory pool.

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

It is indexed. Measured over 25 calls each, looking up a value deliberately placed near the end of the
range:

| Query | Label size | Latency |
|---|---|---|
| `MSpaths` by `urn` property | 40,000 nodes | **0.40 ms** |
| `MSpaths` by `urn` property | 1,000 nodes | **0.36 ms** |
| `SSpaths` by integer node id | 40,000 nodes | 0.35 ms |

Forty times the nodes for eleven percent more time. Property selectors cost effectively the same as a
direct id lookup, with no index declaration required.

## 6. `algo.MSpaths` amortises across sources as documented

The claim that `MSpaths` shares selector hydration, topology and adjacency across source and target
pairs holds up:

| Inline `sourceValues` | Total | Per source |
|---|---|---|
| 1 | 0.40 ms | 0.40 ms |
| 50 | 3.60 ms | 0.072 ms |
| 500 | 7.26 ms | **0.015 ms** |

Ten times the sources for twice the wall clock. This is the single most useful performance property we
found, and it is what makes returning a proof path per keyholder affordable.

## 7. Writes are serialised, so concurrency does not raise throughput

60,000 edges written as `UNWIND MATCH ... CREATE`, one cell:

| Batch | Workers | Edges/sec |
|---|---|---|
| 256 | 1 | 4,178 |
| 1024 | 1 | 3,213 |
| 1024 | 4 | 2,830 |
| 1024 | 8 | 2,888 |
| 1024 | 16 | 3,155 |

Four, eight and sixteen concurrent sessions perform no better than one. This is consistent with the
documented design, where a durable writer lease selects one active writer per cell, so all sessions
against a single cell contend for the same writer. Worth stating explicitly for anyone planning a bulk
load: **parallel client sessions are not a throughput lever.**

Batch size sweep at a larger graph size, 24,000 edges each:

| Batch | Edges/sec | us/edge |
|---|---|---|
| 32 | 2,024 | 494 |
| 64 | 2,154 | 464 |
| 128 | **2,191** | **456** |
| 256 | 2,124 | 471 |
| 512 | 1,970 | 508 |
| 1024 | 1,840 | 544 |

The optimum is broad and sits around 128 rows. Larger batches are consistently worse, which combined
with finding 2 means the useful range is narrow.

## 8. Write throughput degrades as the graph grows

The same batch size of 256 measured 4,178 edges/sec early and 2,124 edges/sec after roughly 600,000
more edges existed. Planning on a number taken from an empty graph would have been optimistic by about
two times.

We plan on **2,000 edges/sec sustained**, and treat further degradation as expected rather than
surprising.

## 9. Label scans are capped at 250,000 candidates

```cypher
MATCH (n:Tp) RETURN count(*)
-> Neo.TransientError.General.MemoryPoolOutOfMemoryError
   cypher_vertex_label_index_candidates rejected by admission control: actual 250001 exceeds limit 250000
```

A label with more than 250,000 nodes cannot be scanned or counted by a plain `MATCH`. This is the
limit with the largest design consequence for us, because any whole graph pass has to be expressed as
bounded pages rather than a scan.

## 10. A full edge type count exceeds the query timeout

```cypher
MATCH ()-[r:TP]->() RETURN count(*)
-> Neo.ClientError.Transaction.Terminated
   cypher_relationship_edge_records exceeded query timeout after 29999 ms; limit is 29999 ms
```

Roughly 300,000 edges of one type could not be counted inside the 29,999 ms budget. A second attempt
failed in `cypher_relationship_metadata_hydration` instead, so the cost is in hydration rather than in
enumeration.

Findings 9 and 10 together mean **there is no cheap way to ask how big the graph is**, which is mildly
awkward for progress reporting during a bulk load. We maintain our own counters instead.

## 11. Bolt cannot select a cell

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

## 12. Storage footprint

981 MB on disk for roughly 500,000 edges and 400,000 nodes, before compaction settled. Container
resident memory grew from 43 MB at idle to 1.4 GB after the write benchmarks.

## 13. Two operational notes that cost time elsewhere

Both are documented in the HydraDB README and both are easy to skip past.

- `RUST_MIN_STACK=33554432` is required. Without it the node builds, answers `/readyz`, then aborts on
  the first query. The failure looks like a crash bug rather than a missing setting.
- The container runs as UID 10001, so a bind mounted data directory owned by the host user needs
  `--user "$(id -u):$(id -g)"` or the first storage operation fails.

Startup is quick: `/readyz` returned 200 two seconds after container start, at 43 MB resident.

---

## What we changed in our design because of these

| Finding | Design consequence |
|---|---|
| 2, 7, 8 | Ingest is a single writer at about 2,000 edges/sec. Batches of 128. Scale target set from this number rather than from an estimate |
| 3, 4 | `algo.MSpaths` value lists are interpolated into query text, with values validated against the npm name grammar first |
| 5, 6 | Proof paths are affordable, and a single batched call serves many keyholders. No index declaration needed |
| 9, 10 | Every whole graph pass is paged by integer id range. No full label scans, no unbounded aggregates. We keep our own counters |
| 11 | One cell. Sharding is not pursued |
