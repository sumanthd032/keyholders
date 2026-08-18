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

import (
	"runtime"
	"sync"
)

// NodeID identifies anything this package or the observatory built on top of it tracks a sketch for:
// a package during propagation, a maintainer handle after aggregation. Nothing here depends on what
// a node id actually is, which is what lets the same map type flow through propagation and
// aggregation without a conversion at the boundary.
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

	// Workers is how many goroutines share each round's edge list. Zero means GOMAXPROCS.
	Workers int
}

func (e *Engine) workers() int {
	if e.Workers > 0 {
		return e.Workers
	}
	return runtime.GOMAXPROCS(0)
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
//
// Each round is a full recompute from the previous round's result rather than an in-place update of
// one shared map: workers only ever read the previous round's sketches, never mutate them, and each
// worker builds its own private map for the edges it was handed, so two goroutines never call Merge
// on the same object and never write the same map at the same time. That is what "workers need no
// coordination" means in practice; splitting edges by target node and mutating one shared map in
// place would still race, because a source read by one worker can be a target another worker is
// concurrently merging into. Folding the round's partial results together, and seeding every node at
// its cold start floor of {v}, happens single threaded between rounds, which costs little next to
// the merge work the round just parallelised.
func (e *Engine) RunEpoch(nodes []NodeID, edges []Edge) map[NodeID]Sketch {
	cur := make(map[NodeID]Sketch, len(nodes))
	seed := func(n NodeID) {
		if _, ok := cur[n]; ok {
			return
		}
		sk := e.New()
		sk.Add(n)
		cur[n] = sk
	}
	for _, n := range nodes {
		seed(n)
	}
	for _, edge := range edges {
		seed(edge.From)
		seed(edge.To)
	}

	chunks := splitEdges(edges, e.workers())

	for changed := true; changed; {
		partials := make([]map[NodeID]Sketch, len(chunks))
		var wg sync.WaitGroup
		for i, chunk := range chunks {
			i, chunk := i, chunk
			wg.Add(1)
			go func() {
				defer wg.Done()
				partials[i] = mergeChunk(e.New, cur, chunk)
			}()
		}
		wg.Wait()

		next := make(map[NodeID]Sketch, len(cur))
		for n := range cur {
			sk := e.New()
			sk.Add(n)
			next[n] = sk
		}
		for _, part := range partials {
			for n, sk := range part {
				next[n].Merge(sk)
			}
		}

		changed = false
		for n, sk := range next {
			if sk.Count() != cur[n].Count() {
				changed = true
			}
		}
		cur = next
	}
	return cur
}

// mergeChunk computes, for one worker's slice of edges, a fresh sketch per target holding the merge
// of every source that edge slice points at into it. It only reads cur, never writes to it, which is
// what makes it safe to run concurrently with the same call over a different chunk.
func mergeChunk(newSketch NewSketch, cur map[NodeID]Sketch, chunk []Edge) map[NodeID]Sketch {
	local := make(map[NodeID]Sketch)
	for _, edge := range chunk {
		dst, ok := local[edge.To]
		if !ok {
			dst = newSketch()
			local[edge.To] = dst
		}
		dst.Merge(cur[edge.From])
	}
	return local
}

// splitEdges divides edges into at most n contiguous, roughly equal chunks, fewer if there are not
// enough edges to give every worker something to do.
func splitEdges(edges []Edge, n int) [][]Edge {
	if n > len(edges) {
		n = len(edges)
	}
	if n <= 1 {
		return [][]Edge{edges}
	}

	chunks := make([][]Edge, 0, n)
	size := (len(edges) + n - 1) / n
	for start := 0; start < len(edges); start += size {
		end := start + size
		if end > len(edges) {
			end = len(edges)
		}
		chunks = append(chunks, edges[start:end])
	}
	return chunks
}
