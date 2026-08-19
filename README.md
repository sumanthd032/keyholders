# Keyholders

Keyholders answers "how many people can execute code on your machine, and who are they" by building
the npm package, version, and maintainer graph in [HydraDB](https://github.com/hydra-db/hydradb) and
computing reachability over it.

Full setup, run, and attribution documentation is still being written. This section covers how the
incident track's six questions map onto the graph.

## Incident mode: the six questions

`keyholders incident <package>@<version>` answers all six from the graph, in this order:

| # | Track question | How Keyholders answers it |
|---|---|---|
| 1 | Which internal services are transitively exposed? | Coexistence constrained reachability from every recorded project (`scan --record`) to the compromised version, reusing the same interval BFS the audit mode runs for one lockfile. `internal/incident.TransitivelyExposed`. |
| 2 | Which version introduced the vulnerability? | Walk `HAS_VERSION` by publish order for the earliest version an advisory's `AFFECTS` edges cover, and diff `DEPENDS_ON` against the version published immediately before it. `internal/incident.Bisect`. |
| 3 | Which applications resolved the compromised version while it was live? | `LOCKS.locked_at` compared against the `[valid_from, valid_to)` window of the `RESOLVES_TO` edge that produced the compromised version. `internal/incident.ResolvedWhileLive`. |
| 4 | Which packages share maintainers or infrastructure? | Every other package held by an account that also holds the named package, read as two anchored single-hop `MAINTAINS` traversals. `internal/incident.SharedMaintainers`. |
| 5 | Are there likely typosquats nearby? | Precomputed `TYPOSQUAT_OF` edges, edit distance and homoglyph folding weighted by download rank, read in both directions. `internal/typosquat`, `GraphSource.TyposquatsNear`. |
| 6 | What is the complete blast radius? | The union of the above, printed worst severity first. `keyholders incident`'s report. |

Verified by hand against a real published incident: `keyholders incident minimist@1.2.0` correctly
surfaces all three of minimist's real advisories with correct severities, bisects each to minimist's
actual 2013 first release, and distinguishes a direct lockfile pin (unconditionally exposed) from an
indirect one resolved through a real, time-bounded `RESOLVES_TO` edge.
