# Keyholders

Keyholders answers one question: **how many people can execute code on your machine, and who are
they.** It builds the npm package, version, and maintainer graph in
[HydraDB](https://github.com/hydra-db/hydradb) and computes reachability over it, constrained to the
actual points in time at which a dependency and a maintainer's access to it coexisted, not the union
of every relationship either of them ever had.

Three modes, one graph:

- **Audit** a lockfile: `keyholders scan <lockfile>` — the roster of every account that can publish
  code into this exact project, ranked by risk, with a printed proof for every claim.
- **Respond to an incident**: `keyholders incident <package>@<version>` — the full blast radius of a
  compromised release: who is exposed, since when, and through what chain.
- **Survey the ecosystem**: `keyholders observatory` — estimated reach for every maintainer across the
  whole ingested graph, at multiple points in time, with its error band shown alongside every number.

A web interface (`make web`) serves all three as `/`, `/incident`, and `/observatory`.

## Why the union graph gives the wrong answer

A plain reachability query, "every account that ever maintained a package this project's dependency
tree ever touched," reports relationships that never actually existed. If a package once declared a
dependency it has since dropped, and an account's tenure on that dependency began only after it was
dropped, a query without a notion of time reports that account as a keyholder anyway: there is a path
in the union of every edge, even though the two facts never held at the same instant.

Every edge that matters here, who maintained what, which version a range resolved to, when a project
locked its dependencies, carries a validity interval. Keyholders runs an interval-constrained search:
an edge is only followed when its interval intersects the interval the search is already carrying, and
the search reports two numbers side by side everywhere, coexistence and union, so the size of the
correction is visible rather than hidden behind a single, silently-cleaned-up count. See
[`docs/ALGORITHM.md`](docs/ALGORITHM.md) for the full mechanism, and
[`docs/VALIDATION.md`](docs/VALIDATION.md) for what was checked against independent ground truth.

## Setup

Requires Go 1.24 or later, Docker, and Node.js 20 or later for the web interface.

```
git clone https://github.com/sumanthd032/keyholders.git
cd keyholders
make up      # starts HydraDB (and its MinIO backing store) and waits for readiness
make build   # builds the CLI binaries into bin/
make test    # runs the Go test suite
```

Populate the graph before any query mode is useful:

```
bin/keyholders packages -top 2000 -out packages.txt   # a ranked package name list
bin/keyholders ingest -packages packages.txt          # packages, versions, maintainers
bin/keyholders resolve -packages packages.txt         # materialize RESOLVES_TO validity windows
bin/keyholders advisories                             # OSV's npm advisory feed, for incident mode
bin/keyholders typosquats -packages packages.txt      # typosquat candidate edges, for incident mode
```

Then:

```
bin/keyholders scan path/to/package-lock.json
bin/keyholders incident minimist@1.2.0
bin/keyholders observatory -packages packages.txt -epochs 8
```

Or run the web interface, which serves all three modes against the same graph:

```
make web     # runs the query API and the Next.js dev server together, http://localhost:3000
```

`make down` stops HydraDB without deleting its data; `make clean` removes build output and the local
HydraDB store entirely.

### CLI reference

| Command | What it does |
|---|---|
| `scan <lockfile>` | Audit a lockfile: the keyholder count, the roster, the risk ranking, proof paths. |
| `who <handle>` | What one account reaches, and through which packages. |
| `path <handle> <lockfile>` | The concrete chain from a project to what an account controls. |
| `packages` | Write the ranked package list ingest and resolve read. |
| `ingest` | Build the package, version, and maintainer graph in HydraDB. |
| `resolve` | Materialize `RESOLVES_TO` edges with their validity windows. |
| `verify` | Cross-check the graph against deps.dev and rank maintainers by reach. |
| `observatory` | Reachability for every maintainer across the whole graph, per epoch. |
| `advisories` | Ingest OSV's npm feed into `Advisory` nodes and `AFFECTS` edges. |
| `typosquats` | Materialize `TYPOSQUAT_OF` edges from name similarity and rank. |
| `incident <package>@<version>` | The full blast radius report. |
| `serve` | Serve the web API the Next.js interface talks to. |
| `ci-check <lockfile>` | Exit non-zero if a reached version carries a live advisory, or (with `-base`) if the keyholder set grew. |

Every command takes `-h` for its full flag list.

## How HydraDB is used, and what would be lost without it

HydraDB is not a data store this project happens to use; the questions Keyholders answers are graph
questions, and every one of them is a HydraDB query:

- **The keyholder search itself** is an interval-constrained breadth first search over `MAINTAINS`,
  `RESOLVES_TO`, and `LOCKS` edges, run as a native, server-side path-finding call
  (`algo.MSpaths`) rather than pulled client-side edge by edge. Streamed frontiers, so the web
  interface can draw the search as it actually happens, come directly from that same call.
- **Proof paths** are the same path-finding procedure narrowed to one account and one target, so every
  claim in a roster is a real chain read out of the graph, not a client-side reconstruction.
- **Incident mode's six questions** (transitive exposure, version of introduction, resolved-while-live
  exposure, shared-maintainer adjacency, typosquat proximity, and the combined blast radius) are each a
  small number of anchored graph reads, composed in Go rather than expressed as one large query,
  because this Cypher subset has no multi-hop pattern and no set-membership operator; the graph is
  still what is being asked, at every step.
- **The observatory's ecosystem-wide reach** propagates HyperLogLog sketches along `DEPENDS_ON` edges
  read directly from the graph, epoch by epoch, and writes the result back onto `Package` and
  `Maintainer` nodes as properties and onto self-loop edges for the reach-over-time curve, so a
  second run can read a prior run's answer without recomputing it.

Without HydraDB, this project would need to either materialize the entire multi-million-edge npm
dependency graph in application memory for every query (infeasible at the scale a real audit needs),
or hand-roll graph storage, indexing, and traversal in Go, which is the graph database's actual job.
The interval-constrained search specifically depends on the engine's native path-finding call
supporting a bounded, batched, multi-source search; without it, the search would be a client-driven
loop issuing one query per hop, which the observatory's own scale would make impractical.

## Dependencies and attribution

**Go**

| Dependency | Why |
|---|---|
| [`neo4j-go-driver`](https://github.com/neo4j/neo4j-go-driver) | HydraDB speaks the Bolt 5.x protocol; this is the client used to speak it, since HydraDB itself ships no Go driver. |

Everything else on the Go side is the standard library, by design: dependencies are added only when
they earn their place over what the standard library already provides.

**Web** (`web/`)

| Dependency | Why |
|---|---|
| [Next.js](https://nextjs.org/) (App Router) | The web framework. |
| [React](https://react.dev/) | What Next.js is built on. |
| [Reagraph](https://reagraph.dev/) | WebGL graph rendering, chosen because it ships path finding and clustering rather than needing them hand built, and neither an SVG nor a canvas-2D renderer survives the tens of thousands of nodes a deep dependency tree audit can reach. |
| [Tailwind CSS](https://tailwindcss.com/) | Styling, driven entirely through CSS custom properties so the design tokens have one source. |
| [Geist](https://vercel.com/font) (Sans, Mono) | Typography. |

**External services and datasets**

| Source | What it provides |
|---|---|
| [npm registry](https://registry.npmjs.org/) | Per-version package documents, including `_npmUser`, the historical publishing identity behind every version. |
| [deps.dev](https://deps.dev/) (Google, Open Source Insights) | Version timelines with publish times, and resolved dependency graphs with the declared range on every edge. |
| [OSV](https://osv.dev/) | The bulk npm vulnerability feed `keyholders advisories` ingests, per the [OSV schema](https://ossf.github.io/osv-schema/). |
| [ecosyste.ms](https://ecosyste.ms/) | Ranked package listings by download count, for `keyholders packages`. |
| [HydraDB](https://github.com/hydra-db/hydradb) | The graph database every mode of this project queries. |

## Benchmarks

Full tables, with the exact conditions each number was measured under, live in
[`docs/BENCHMARKS.md`](docs/BENCHMARKS.md); `scripts/bench/` reproduces every one of them against a
running HydraDB instance. Headline figures, measured against a 2,000 package live ingest:

| | |
|---|---|
| Ingest throughput, steady state | ~4,400 edges/sec |
| Query latency, warm, real 527-package lockfile | p50 100 ms, p90 130 ms |
| Observatory runtime, 2,000 packages, 8 epochs | 2m 15s (writing every epoch's history) |
| CLI memory high water mark | 23 MB (scan), 46 MB (observatory) |

## Research this builds on

The reachability model is a direct application of interval-constrained graph traversal: reachability
computed not over a static graph but over a graph whose edges are only sometimes true, which is the
established way to ask "was X reachable from Y at time T" rather than "is X ever reachable from Y."
Ecosystem-wide reach estimation uses HyperLogLog (Flajolet, Fusy, Gandouet, Meunier, 2007), a
cardinality estimator with well-characterized, boundable error, chosen and implemented rather than a
novel estimator, because the goal here is applying published, well-understood techniques to a new
domain, not contributing a new algorithm. Version resolution follows the [semver](https://semver.org/)
specification, validated by differential testing against the reference implementation the npm
ecosystem itself uses.

## License

Keyholders is licensed under the [Apache License, Version 2.0](LICENSE).

HydraDB itself is licensed AGPL 3.0. This project runs it as a separate service and communicates with
it over Bolt and HTTP; nothing in this repository links against or embeds HydraDB's source, so this
codebase is not a derivative work of it and Apache 2.0 applies to all of it. Any future contribution
back to HydraDB's own engine (a native procedure, for instance) would be a derivative work of that
project and would carry HydraDB's own AGPL 3.0 license, kept in its own clearly separated location
rather than folded into this Apache-licensed tree.

## Incident mode: the six questions

`keyholders incident <package>@<version>` answers all six from the graph, in this order:

| # | Track question | How Keyholders answers it |
|---|---|---|
| 1 | Which internal services are transitively exposed? | Coexistence constrained reachability from every recorded project (`scan -record`) to the compromised version, reusing the same interval BFS the audit mode runs for one lockfile. `internal/incident.TransitivelyExposed`. |
| 2 | Which version introduced the vulnerability? | Walk `HAS_VERSION` by publish order for the earliest version an advisory's `AFFECTS` edges cover, and diff `DEPENDS_ON` against the version published immediately before it. `internal/incident.Bisect`. |
| 3 | Which applications resolved the compromised version while it was live? | `LOCKS.locked_at` compared against the `[valid_from, valid_to)` window of the `RESOLVES_TO` edge that produced the compromised version. `internal/incident.ResolvedWhileLive`. |
| 4 | Which packages share maintainers or infrastructure? | Every other package held by an account that also holds the named package, read as two anchored single-hop `MAINTAINS` traversals. `internal/incident.SharedMaintainers`. |
| 5 | Are there likely typosquats nearby? | Precomputed `TYPOSQUAT_OF` edges, edit distance and homoglyph folding weighted by download rank, read in both directions. `internal/typosquat`, `GraphSource.TyposquatsNear`. |
| 6 | What is the complete blast radius? | The union of the above, printed worst severity first. `keyholders incident`'s report. |

Verified by hand against a real published incident: `keyholders incident minimist@1.2.0` correctly
surfaces all three of minimist's real advisories with correct severities, bisects each to minimist's
actual 2013 first release, and distinguishes a direct lockfile pin (unconditionally exposed) from an
indirect one resolved through a real, time-bounded `RESOLVES_TO` edge.

## Continuous integration

`.github/workflows/lockfile-audit.yml` runs `keyholders ci-check` against this repository's own
`web/package-lock.json` on every pull request that touches it: it fails the build if the updated
lockfile reaches a version carrying a live advisory, or if it introduces a keyholder the base branch's
lockfile did not already have.
