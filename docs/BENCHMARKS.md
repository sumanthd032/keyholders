# Benchmarks

Every number below was measured against a live HydraDB instance on the development machine (Apple
Silicon, Docker Desktop, `deploy/docker-compose.yml`), not estimated. Where a number depends on graph
size, the graph size it was measured against is stated alongside it. `scripts/bench/` holds a
reproduction script for each section.

## Graph size

Measured on the 2,000 package ingest (`var/packages-2k.txt`) this project's live checks run against.

| Node / edge type | Count | How it was read |
|---|---|---|
| `Package` | 2,436 | `MATCH (n:Package) RETURN count(*)` |
| `Maintainer` | 1,391 | `MATCH (n:Maintainer) RETURN count(*)` |
| `Advisory` | 226,925 | `MATCH (n:Advisory) RETURN count(*)` |
| `Version` | not measurable this way | a full label scan exceeds this HydraDB build's 250,000 candidate cap (`FINDINGS.md` finding 10) |
| `AFFECTS` | 190,851 | recorded at ingest time (`internal/advisory`), not re-queried: a full edge type count exceeds the query timeout at this scale (finding 11) |
| `PKG_RESOLVES` | 17,680 across 4 epochs, 24,000+ across 8 | recorded at write time (`internal/observatory.WriteSnapshots`) |
| `TYPOSQUAT_OF` candidates | 122 | recorded at ingest time (`internal/typosquat`) |

The `Version` and full edge type rows are intentionally reported as "not measurable this way" rather
than a number: both hit real HydraDB limits at this scale (findings 10 and 11), and a benchmark table
that silently omits the reason reads as the number happening not to matter, rather than the engine
refusing to answer.

## Ingest throughput

From the batch size sweep and the live registry ingest:

| Condition | Edges/sec |
|---|---|
| Empty graph, cold | 4,178 |
| Graph large enough to be compacting | 2,000 |
| After the store and consumption fixes (see `FINDINGS.md` findings 1, 15) | 3,400 to 4,400, settling around 4,426 at steady state |
| `PKG_RESOLVES` snapshot writes (step 7, various runs) | 7,000 to 11,700 |

Batch size 256 rows per `UNWIND` is what these numbers were measured at (`internal/graph.DefaultBatch`,
the measured throughput optimum on a graph large enough to be compacting: 4,426 edges/sec at batch
256 against 3,354 at batch 1,024, no further gain from concurrent sessions).

## Query latency

`keyholders scan -json web/package-lock.json` against the live 2,000 package graph, 20 consecutive
runs, real wall clock time including process startup and the Bolt handshake:

| | Time |
|---|---|
| Run 1 (cold Bolt connection) | 1.85 s |
| min (warm) | 0.09 s |
| p50 (warm) | 0.10 s |
| p90 (warm) | 0.13 s |
| max (warm, excluding the cold run) | 0.15 s |

This lockfile locks 527 packages, 388 of which are in this ingest's graph, and the audit reaches a
depth of 6. The cold first run is the Bolt connection and session setup cost, paid once per process;
every CLI invocation pays it, the `serve` HTTP API pays it once at startup instead.

Reproduce: `scripts/bench/query_latency.sh`.

## Observatory runtime

`keyholders observatory -packages var/packages-2k.txt -epochs 8`, 2,000 packages, 8 quarterly epochs,
cold started every epoch (the correctness property `TestDeletionAcrossEpochs` guards):

| Condition | Wall time |
|---|---|
| Full run: snapshots, propagation, aggregation, `PKG_RESOLVES` write, `kri`/`kri_at` write, `KRI_AT` history write for all 8 epochs (`-validate 0`) | 2m 15s |
| Same run with `-write=false` (no graph writes, propagation and aggregation only) | 78 s |

The gap between the two, about 57 seconds, is entirely the graph write cost: 4 epochs of
`PKG_RESOLVES` (roughly 17,680 edges), the latest epoch's `kri`/`kri_at` node writes, and 16 epoch
writes of `KRI_AT` history (8 epochs times 2 labels). Reproduce: `scripts/bench/observatory_runtime.sh`.

## Memory high water mark

Peak resident set size, `/usr/bin/time -l` on macOS:

| Process | Peak RSS |
|---|---|
| `keyholders scan` (warm, 527-package lockfile) | 23.3 MB |
| `keyholders observatory` (2,000 packages, 8 epochs, `-write=false`) | 46.1 MB |
| `keyholders-hydradb` container, after the full 8-epoch write including `KRI_AT` history | 2.17 GiB |
| `keyholders-minio` container, same point | 440 MiB |

The HydraDB container's own figure grows with what has been written into it, not with any one query:
it was 861 MiB before a `KRI_AT` history write added 16 epochs' worth of self loop edges across 2,000
packages and 1,391 maintainers, and 2.17 GiB after. This is the container process's resident memory,
not this project's client-side footprint, which the CLI rows above measure.

The CLI's own memory footprint is small at this scale because the sketch engine holds one epoch's
package sketches at a time (HyperLogLog, precision 8, 256 registers per package), not the whole
history. A rough estimate at 700,000 nodes and 256 registers each puts that one-epoch working set
around 134 MB; this measurement, at 2,000 nodes, is well under that.

## Storage footprint

`du -sh` against `deploy/hydradb-data/`, the bind-mounted store:

| Directory | Size | What it holds |
|---|---|---|
| `minio/` | 1.3 GB | HydraDB's persisted graph store |
| `cache/` | 877 MB | this project's own on-disk registry response cache (`internal/registry`), not HydraDB state |
| `store/` | 0 B | unused; HydraDB's actual data lives in MinIO, not a local directory (finding 1) |

## What these numbers do not claim

These are single-machine, single-run measurements on a 2,000 package ingest, not a load test and not
a claim about HydraDB's behavior at ecosystem scale, tens of thousands of packages. Where a number
would change materially at that scale, this file says so rather than extrapolating.
