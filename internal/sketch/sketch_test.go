package sketch

import "testing"

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
