package sketch

import (
	"fmt"
	"math/rand"
	"testing"
)

// exactSketch is an uncompressed stand in for the HyperLogLog sketch task 3 builds. It is exact by
// construction, which is what lets the assertions below check specific counts instead of an error
// band. It lives only in this test file because the regression it guards is about the propagation
// loop's cold start, not about sketch accuracy.
type exactSketch struct{ members map[NodeID]bool }

func newExactSketch() Sketch { return &exactSketch{members: map[NodeID]bool{}} }

func (s *exactSketch) Add(n NodeID) { s.members[n] = true }

func (s *exactSketch) Merge(other Sketch) {
	o := other.(*exactSketch)
	for n := range o.members {
		s.members[n] = true
	}
}

func (s *exactSketch) Count() int { return len(s.members) }

// TestDeletionAcrossEpochs is the guard that has to exist before any other engine code: a
// PKG_RESOLVES edge live in epoch 1 and dead in epoch 2 must make KRI at the edge's target decrease.
// This is not a hypothetical failure mode: sketches merge by register-wise max, which only ever
// grows, so an engine that seeds an epoch from the previous epoch's sketches cannot lower a count
// that a dead edge should have removed, and the resulting KRI curve comes out monotonically
// increasing, indistinguishable from a healthy, growing ecosystem. That is the bug this test exists
// to catch before it reaches a demo. Any implementation that carries sketch state from one RunEpoch
// call to the next, instead of cold starting S[v] = {v} every time, fails the second assertion below.
func TestDeletionAcrossEpochs(t *testing.T) {
	nodes := []NodeID{"X", "Y", "Z"}
	// Epoch 1: X depends on Y, Y depends on Z. Z's reach is X, Y and itself.
	epoch1 := []Edge{
		{From: "X", To: "Y"},
		{From: "Y", To: "Z"},
	}
	// Epoch 2: Y stops depending on Z. The only edge into Z is gone.
	epoch2 := []Edge{
		{From: "X", To: "Y"},
	}

	eng := &Engine{New: newExactSketch}

	s1 := eng.RunEpoch(nodes, epoch1)
	if got, want := s1["Z"].Count(), 3; got != want {
		t.Fatalf("epoch 1 KRI(Z) = %d, want %d: X and Y both reach Z through Y, plus Z itself", got, want)
	}

	s2 := eng.RunEpoch(nodes, epoch2)
	if got, want := s2["Z"].Count(), 1; got != want {
		t.Errorf("epoch 2 KRI(Z) = %d, want %d: Y -> Z died, nothing reaches Z but Z itself", got, want)
	}
	if s2["Z"].Count() >= s1["Z"].Count() {
		t.Errorf("KRI(Z) must decrease when its only inbound edge dies between epochs: epoch1=%d epoch2=%d",
			s1["Z"].Count(), s2["Z"].Count())
	}

	// X -> Y still lives in epoch 2, so Y's own reach must be unaffected by Z's edge dying.
	if got, want := s2["Y"].Count(), 2; got != want {
		t.Errorf("epoch 2 KRI(Y) = %d, want %d: X -> Y still lives", got, want)
	}
}

// TestRunEpochIsWorkerCountInvariant checks that splitting the edge list across more workers never
// changes the answer. The fixpoint of a monotone merge over a finite set of nodes is unique, so this
// is not tolerance for parallel noise, it is a hard equality: any worker count that disagrees with
// running single threaded has a real bug in how a round is split or folded back together, not an
// acceptable variance in a parallel algorithm.
//
// The graph is a diamond, so two paths arrive at D, feeding into a cycle back to A, so convergence
// needs several rounds under either worker count rather than settling on the first pass.
func TestRunEpochIsWorkerCountInvariant(t *testing.T) {
	nodes := []NodeID{"A", "B", "C", "D", "E"}
	edges := []Edge{
		{From: "A", To: "B"},
		{From: "A", To: "C"},
		{From: "B", To: "D"},
		{From: "C", To: "D"},
		{From: "D", To: "E"},
		{From: "E", To: "A"},
	}

	results := map[int]map[NodeID]Sketch{}
	for _, workers := range []int{1, 3, 8} {
		eng := &Engine{New: newExactSketch, Workers: workers}
		results[workers] = eng.RunEpoch(nodes, edges)
	}

	for _, n := range nodes {
		want := results[1][n].Count()
		for _, workers := range []int{3, 8} {
			if got := results[workers][n].Count(); got != want {
				t.Errorf("node %s: 1 worker gives %d, %d workers gives %d", n, want, workers, got)
			}
		}
	}
}

// buildLayeredGraph makes a synthetic dependency graph shaped like a real one: each node depends on
// a handful of nodes ranked after it, which keeps it acyclic and gives BenchmarkRunEpoch something
// with real fan out to propagate through rather than a worst case adversarial shape.
func buildLayeredGraph(nodeCount, fanout int) ([]NodeID, []Edge) {
	nodes := make([]NodeID, nodeCount)
	for i := range nodes {
		nodes[i] = NodeID(fmt.Sprintf("n%d", i))
	}

	rng := rand.New(rand.NewSource(1))
	edges := make([]Edge, 0, nodeCount*fanout)
	for i := 0; i < nodeCount-1; i++ {
		for k := 0; k < fanout; k++ {
			j := i + 1 + rng.Intn(nodeCount-i-1)
			edges = append(edges, Edge{From: nodes[i], To: nodes[j]})
		}
	}
	return nodes, edges
}

// BenchmarkRunEpoch measures whether splitting work across workers actually helps, rather than
// assuming that adding goroutines to a merge loop pays for itself. Sub-benchmarks share one graph so
// the comparison isolates the worker count.
func BenchmarkRunEpoch(b *testing.B) {
	nodes, edges := buildLayeredGraph(20_000, 5)
	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			eng := &Engine{New: func() Sketch { return NewHLL(8) }, Workers: workers}
			for b.Loop() {
				eng.RunEpoch(nodes, edges)
			}
		})
	}
}
