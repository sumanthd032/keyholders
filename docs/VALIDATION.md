# Validation

What was checked, against what ground truth, and what the result was. Every claim below is either a
comparison against an independent source or a test that fails on a specific, deliberately reintroduced
bug, not a claim taken on faith from the implementation being straightforward.

## Semver resolution

The production range matcher, covering caret, tilde, comparator sets, hyphen ranges, x-ranges, and
prerelease rules, is differentially tested against the reference `semver` npm package over a large
generated corpus of range and version pairs. A matcher that silently disagrees with the reference on
any input in that corpus fails the test suite; there is no tolerance for partial disagreement, since a
wrong resolution corrupts every downstream number the graph reports.

Independently, resolution windows are validated by equivalence: resolving a range against a target's
full version timeline should produce the same concrete version, at any instant, as resolving that same
range fresh against a point-in-time snapshot of the timeline. This checks the interval-splitting logic
itself, not just the underlying range grammar, since a correct grammar fed through incorrect interval
boundaries would still produce wrong `RESOLVES_TO` edges.

## Coexistence semantics

The two-hop case where the union graph reports a path and coexistence semantics say the path never
existed is a golden regression test, not only documentation: a project depending on a package whose
declared sub-dependency existed only before the account in question ever held maintainer rights on it,
constructed so a union-only reachability query gives the wrong answer and a coexistence-constrained
search gives the right one. This is checked directly against real, live data as well: `keyholders
incident minimist@1.2.0` correctly surfaces all three of minimist's real published advisories, bisects
each to minimist's actual first affected release from 2013, and distinguishes a direct lockfile pin
from an indirect one resolved through a real, time-bounded dependency chain, each classified correctly
and distinctly from the other.

## Sketch accuracy

The sketch propagation engine that computes ecosystem-wide reach is run twice over identical input for
a sample of packages: once using HyperLogLog sketches, once using an uncompressed exact set with no
estimation error, both driven by the same propagation code so the comparison isolates the estimator's
own error rather than any difference in the algorithm around it. Against the live ingested graph, the
large majority of sampled packages land at or under the nominal standard error the register count in
use predicts; every reach figure the observatory reports is shown with that error band directly beside
it rather than presented as an exact count.

A separate, deliberately adversarial check confirms the estimator's independence assumptions actually
hold on this project's real key shapes: hashing near-identical short keys with a weak finalizer left
the estimator's most significant bits correlated, undercounting cardinality by a large factor before
the hash was fixed. That regression is what the error-rate check above would have caught, had it
shipped; it is recorded because catching it required measuring the error curve directly rather than
trusting the nominal figure a HyperLogLog implementation is supposed to hit.

## The cold-start regression

The deletion regression test is the single most load-bearing test in the sketch engine: a synthetic
graph where an edge is live in one epoch and removed in the next, asserting that the reach estimate at
the second epoch is measurably smaller than at the first. An implementation that carries sketch state
forward from one epoch's computation into the next, rather than starting each epoch from scratch,
passes every other correctness check and still fails this one, because a carried-forward sketch can
only grow: removed edges never actually shrink its result. This test was written before any engine
code, and was verified against a deliberately reintroduced warm-start variant of the engine, which it
correctly failed.

## Typosquat detection

The candidate-generation logic (edit distance plus homoglyph folding, weighted by relative popularity)
is validated against a real, publicly documented incident: the 2017 `cross-env` / `crossenv` typosquat,
where a single character transposition on a popular package was registered separately and distributed
malicious code to anyone who mistyped the name. The detector correctly surfaces this pair as a
candidate from name similarity and popularity skew alone, with no incident-specific logic.

## Cross-checking ingest against an independent source

Node and edge counts produced by ingest are spot-checked against deps.dev for a sample of packages,
comparing this project's own resolved dependency graph and version timeline against an independently
maintained source for the same packages, rather than trusting the ingest pipeline's own accounting of
what it wrote.

## What is not independently validated

The risk score's specific weighting of reach, staleness, solo-maintainer status, provenance, and
install script presence is a stated, printed formula, not a claim calibrated against a labeled dataset
of real supply chain incidents; no such labeled dataset was available to validate against. Every weight
is printed alongside every score specifically so the ranking is auditable and disputable, rather than
presented as independently validated risk in the way the semver and coexistence numbers above are.
