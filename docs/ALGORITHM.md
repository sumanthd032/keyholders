# Algorithm

This is how Keyholders answers "who can execute code in this project," and why the answer is not a
plain graph traversal.

## The problem with a plain reachability query

Ask HydraDB "which accounts can reach this project's dependencies" over the union of every
`DEPENDS_ON` edge ever recorded, and the graph will answer honestly: it will find every account that
ever maintained a package anywhere in the transitive dependency tree, at any point in that package's
history. That answer is wrong for what this project needs, and wrong in a specific, checkable way.

Take a two hop example. Project P depends on package A at some version. A's `package.json` once
declared a dependency on package B, but that declaration was removed two years before P's lockfile was
written; P has never actually resolved to a version of A that depends on B. Account M maintained B for
exactly one month, three years ago, well before A dropped the dependency and well before P existed.

A plain reachability query over the union graph says M can reach P: there is a path P -> A -> B, and M
maintains B. But M's tenure on B and A's dependency on B never coexisted in time, so this path never
existed as a fact about the real world. Reporting M as a keyholder is not a lower bound or a
conservative estimate; it is a fabricated relationship, indistinguishable in a plain query from a real
one. This is the two-hop case where the union graph says reachable and coexistence says not, and it is
also the reason none of this project's counts are read directly off an unconstrained traversal.

## Coexistence-constrained reachability

Every edge that matters to this question, `MAINTAINS`, `RESOLVES_TO`, `LOCKS`, carries a validity
interval, `[valid_from, valid_to)`, the span during which the fact the edge represents was actually
true. An account maintains a package during some interval. A dependency range resolves to a concrete
version during some interval, because the target's own version history changes what the range's
maximum satisfying version is over time. A lockfile locks a project's dependency graph at one instant.

A coexistence path is a sequence of edges whose intervals have a non-empty intersection: there exists
at least one instant at which every edge on the path held simultaneously. The reachability search is
an interval-constrained breadth first search: each frontier entry is a (node, interval) pair, edges are
followed only when the edge's own interval intersects the running interval, and an empty intersection
prunes that branch rather than continuing with a wrong or undefined interval.

Two counts come out of the same search: the coexistence count, following only intersecting intervals,
and the union count, following every edge regardless of interval, the answer a tool without this
distinction would report. The gap between them, the phantom keyholder count, is not a rounding error;
it is the size of the class of relationships that look real in a naive query and never actually held.
Every view in this project shows both numbers side by side rather than only the corrected one, because
the size of the correction is itself the argument for doing the correction at all.

## Materialized resolution, not resolution at query time

The interval on a `RESOLVES_TO` edge is not computed while the search runs. Each `DEPENDS_ON` edge
names a semver range against a target package; a dedicated resolver walks that target's full version
timeline once and emits one `RESOLVES_TO` edge per interval during which the range's resolution was
stable, an edge whose properties already carry the concrete version, the declared range, and the span
it held for. The search then only ever intersects intervals it reads directly off edges, rather than
re-deriving a resolution for an arbitrary instant on every query. This also makes the resolver testable
on its own: a semver matcher is differentially tested against a large generated corpus of range and
version pairs entirely separately from the graph search that later depends on its output being correct.

## Proof paths, not just counts

A keyholder roster that only states a count and a name is asking to be trusted. For any named account
and any package they are reported to hold, the engine can produce the concrete chain: the exact
sequence of packages and versions connecting what the project locked to the version that account can
publish to, together with the span during which every edge on that chain held at once. The chain comes
from the same underlying path-finding call the search itself is built on, filtered in the client for
coexistence, since the graph's own query language cannot express interval intersection directly. A
chain is small enough that this filtering costs nothing, and it means every claim in the roster is
checkable by hand against the public registry, not merely asserted.

## Sketch-based reachability for the whole ecosystem

Interval-constrained BFS answers a question about one project. The observatory asks a different
question: across the whole ingested graph, at a given point in time, how much of the ecosystem does
each maintainer's access ultimately reach. Running an exact BFS from every maintainer, across every
package, at every historical epoch, does not fit in the memory or time this project has to spend; the
number of (maintainer, epoch) pairs alone rules it out well before the per-query cost does.

Reach is instead estimated with HyperLogLog sketches, one per package per epoch, merged bottom-up
along the dependency graph until every package's sketch represents the set of packages that
transitively depend on it, then merged again per maintainer across every package that maintainer
controls at that epoch. A HyperLogLog sketch estimates the cardinality of a set from a small fixed-size
summary, with a known, boundable error rate that gets better with more registers and worse as fewer are
used; the estimate is cheap to compute and cheap to merge, which is what makes an ecosystem-wide answer
affordable at all.

The one property this estimation must not trade away is soundness across time: an epoch's answer must
depend only on that epoch's edges, not on any prior epoch's computation. A sketch engine that carries
state from one epoch into the next will silently produce a curve that looks plausible, monotonically
non-decreasing reach over time, purely because the estimator's internal state can only grow, regardless
of whether packages or dependencies were actually removed in between. Every epoch here is therefore
cold started from scratch: each package's sketch begins as a singleton set containing only itself, and
propagation runs to a fixpoint using only that epoch's own dependency edges. A dedicated regression
test asserts that an edge live in one epoch and gone in the next produces a measurably smaller reach
estimate at the second epoch; an implementation that carries sketch state forward fails this test
immediately, which is deliberate: it is the guard against the exact failure mode described above.

Because the estimate is an estimate, every reach number the observatory reports is shown with its
nominal error band alongside it, computed from the register count in use, never presented as an exact
count. Accuracy is checked directly: the same propagation engine runs twice over identical input, once
using the HyperLogLog sketch, once using an exact, uncompressed set representation with no estimation
error at all, and the two are compared. That comparison, not a claimed theoretical error rate, is what
this project reports as the estimator's actual accuracy on this data.

## Removal analysis

Given the coexistence search's own output, the graph already implies what happens if one account is
removed: every package that account controls, and every package downstream of those that has no other
surviving path to the project once that account's edges are gone. This is computed by replaying the
search's own recorded reads rather than a second graph traversal, and the result distinguishes an
account whose removal changes nothing, because some other path already covers the same packages, from
one whose removal is irreplaceable, the sole route to part of what the project depends on. This is not
the minimum covering set, the smallest number of accounts whose removal would eliminate all reach,
which is a harder combinatorial question this project does not attempt to answer; it is a per-account
question, answerable directly from data already in hand.
