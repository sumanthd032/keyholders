// Package sketch computes reachability across the whole graph the observatory covers, the way the
// audit computes it for one lockfile: from scratch, not carried forward. The observatory calls it
// once per epoch, at package granularity, cold started every time.
//
// Cold starting is a correctness requirement, not an inefficiency worth removing: sketches merge by
// register-wise max, which only ever grows a register, so a sketch carried across epochs cannot
// reflect an edge that died in between. Package rather than version granularity is a size decision:
// a version-level resolution edge set runs to on the order of a hundred million edges across the
// registry, which does not fit a write path or a memory budget that have never been asked to move
// that much. Package granularity is single-digit millions of edges per epoch, comfortably affordable,
// at the cost of reporting reach between packages rather than between exact versions.
package sketch

// NodeID identifies a package for the purposes of propagation. The observatory keys it by the
// package's graph id; nothing in this package depends on what a node id actually is.
type NodeID string

// Edge is one PKG_RESOLVES relationship live during an epoch: From depends on To. Propagation walks
// this direction, so a target's sketch absorbs its dependent's sketch, which is what makes S[v] the
// set of nodes that can reach v rather than the set v can reach.
type Edge struct {
	From NodeID
	To   NodeID
}

// Sketch is anything that can absorb a member and merge with another sketch of the same concrete
// type. HyperLogLog implements it for the observatory. The tests in this package use an exact,
// uncompressed implementation instead, because the property under test is the propagation loop's
// cold start, not sketch error.
type Sketch interface {
	Add(NodeID)
	Merge(Sketch)
	Count() int
}

// NewSketch constructs an empty sketch of whatever concrete kind an Engine has been configured to
// use.
type NewSketch func() Sketch

// Engine runs a per epoch fixpoint: seed every node's sketch with itself, then repeatedly merge each
// edge's dependent sketch into its dependency's until nothing changes. It is a struct rather than a
// free function because a full run reuses one engine across every epoch to stay inside the memory
// budget, and that reuse is exactly where a warm start bug would hide: RunEpoch has to leave nothing
// behind for the next call to inherit. TestDeletionAcrossEpochs is the guard on that property.
type Engine struct {
	New NewSketch
}

// RunEpoch computes a sketch for every node in nodes, cold started at S[v] = {v}, then propagates
// along edges until no sketch changes. An edge endpoint outside nodes is seeded the same way, so a
// caller does not have to prove nodes is already a superset of every edge's endpoints.
//
// This never reads anything produced by a previous call. That is not an optimisation left out; it is
// the correctness property this package exists to enforce: sketches merge by register-wise max,
// which only ever grows a register, so a sketch seeded from a prior epoch cannot shrink when an edge
// dies between epochs. Carrying state forward would make the KRI curve monotonically increasing and
// entirely plausible for a growing ecosystem, which is exactly what would let the bug survive to a
// demo instead of getting caught by one.
func (e *Engine) RunEpoch(nodes []NodeID, edges []Edge) map[NodeID]Sketch {
	s := make(map[NodeID]Sketch, len(nodes))
	seed := func(n NodeID) {
		if _, ok := s[n]; ok {
			return
		}
		sk := e.New()
		sk.Add(n)
		s[n] = sk
	}
	for _, n := range nodes {
		seed(n)
	}
	for _, edge := range edges {
		seed(edge.From)
		seed(edge.To)
	}

	for changed := true; changed; {
		changed = false
		for _, edge := range edges {
			before := s[edge.To].Count()
			s[edge.To].Merge(s[edge.From])
			if s[edge.To].Count() != before {
				changed = true
			}
		}
	}
	return s
}
