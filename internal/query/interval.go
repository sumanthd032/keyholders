// Package query answers reachability over the bitemporal graph: who can execute code on your
// machine, counting only paths whose edges were all valid at one common instant.
package query

import (
	"sort"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// Interval is a half-open span [From, To). Half-open is what makes adjacent spans tile without
// double counting the instant where one ends and the next begins.
type Interval struct {
	From int64
	To   int64
}

// Always spans every instant a real edge can carry. Edge windows are Unix seconds bounded above by
// graph.OpenInterval, so this is the identity for intersection rather than a notional infinity.
var Always = Interval{From: 0, To: graph.OpenInterval}

// At is the degenerate interval containing exactly one instant, which is how a query for a single
// point in time enters the search.
func At(t int64) Interval { return Interval{From: t, To: t + 1} }

func (i Interval) Empty() bool { return i.From >= i.To }

// Intersect is the coexistence operator. A path is a coexistence path when the intersection of its
// edge intervals is non-empty, so this single function is what separates this project's answer from
// the union-graph answer everything else computes.
func (i Interval) Intersect(o Interval) Interval {
	return Interval{From: max(i.From, o.From), To: min(i.To, o.To)}
}

func (i Interval) Contains(t int64) bool { return t >= i.From && t < i.To }

// Set is a disjoint, sorted collection of maximal intervals: the set of spans over which a node has
// already been reached. Keeping it maximal and merged is what bounds the search state, since
// intersection only ever shrinks intervals and a covered span can never yield anything new.
type Set []Interval

// Covers reports whether every instant of i has already been reached.
//
// This is the prune from the algorithm, and its soundness has a limit worth stating where it is
// implemented. The check is sound for the pointwise question "was v reachable at instant t", which
// is what every mode asks. It is not sound for reporting the validity width of a single path,
// because a Set merges spans contributed by different paths: {[1,5)} from one path and {[4,10)} from
// another merge into {[1,10)}, which then covers [3,7) even though no one path spans [3,7).
//
// Pointwise reachability is safe to report. Per-path interval widths must never be read off a Set.
func (s Set) Covers(i Interval) bool {
	if i.Empty() {
		return true
	}
	// Members are sorted and disjoint, so this is one ordered walk consuming i from the left.
	for _, have := range s {
		if have.To <= i.From {
			// Entirely before the part still needing cover. Skipping without advancing i matters:
			// treating it as progress would let a member far to the left "cover" a later span.
			continue
		}
		if have.From > i.From {
			// A gap at the start of what remains. Members were merged on insert, so this is a real
			// gap rather than an artefact of how the set was built.
			return false
		}
		if have.To >= i.To {
			return true
		}
		i.From = have.To
	}
	return false
}

// Insert merges i into the set, returning the new set and whether anything was actually added.
// Intervals that touch or overlap are coalesced, so the set stays maximal.
func (s Set) Insert(i Interval) (Set, bool) {
	if i.Empty() || s.Covers(i) {
		return s, false
	}

	out := make(Set, 0, len(s)+1)
	merged := i
	for _, have := range s {
		if have.To < merged.From || have.From > merged.To {
			out = append(out, have)
			continue
		}
		merged = Interval{From: min(merged.From, have.From), To: max(merged.To, have.To)}
	}
	out = append(out, merged)
	sort.Slice(out, func(a, b int) bool { return out[a].From < out[b].From })
	return out, true
}

// Width is the total time covered, used to report exposure duration rather than a path width.
func (s Set) Width() int64 {
	var total int64
	for _, i := range s {
		total += i.To - i.From
	}
	return total
}

// Earliest is the first instant covered by the set. There is no min aggregate in the query layer
// either, but here the set is already sorted.
func (s Set) Earliest() int64 {
	if len(s) == 0 {
		return 0
	}
	return s[0].From
}
