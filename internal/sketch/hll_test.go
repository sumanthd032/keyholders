package sketch

import (
	"fmt"
	"math"
	"testing"
)

func buildHLL(precision uint, members ...string) *HLL {
	h := NewHLL(precision)
	for _, m := range members {
		h.Add(NodeID(m))
	}
	return h
}

// TestHLLMergeIsIdempotentCommutativeAndOrderIndependent checks the algebraic properties the
// propagation fixpoint depends on. Engine.RunEpoch merges the same edge repeatedly until nothing
// changes and arrives at a node through however many paths reach it, in whatever order the edge list
// puts them in. If merge were not idempotent, repeated passes over an unchanged edge would keep
// inflating a count that should have stabilised. If it were not order independent, a diamond, two
// paths arriving at the same node, could converge to different registers depending on which arrived
// first, and the loop would either never reach a fixpoint or reach the wrong one.
func TestHLLMergeIsIdempotentCommutativeAndOrderIndependent(t *testing.T) {
	a := buildHLL(8, "a", "b", "c")

	// Idempotent: merging in a sketch with identical content must not change the registers. Real
	// members hash deterministically under the fixed process seed, so two independently built
	// sketches over the same set are bit for bit identical, which makes this a genuine test of the
	// merge operation rather than a tautology about merging an object with itself.
	aCopy := buildHLL(8, "a", "b", "c")
	aCopy.Merge(buildHLL(8, "a", "b", "c"))
	if aCopy.Count() != a.Count() {
		t.Errorf("idempotent merge changed the count: %d -> %d", a.Count(), aCopy.Count())
	}

	// Commutative: {a,b,c} merged with {c,d,e} must estimate the same cardinality as the reverse,
	// since both represent the same five member union.
	b := buildHLL(8, "c", "d", "e")
	ab := buildHLL(8, "a", "b", "c")
	ab.Merge(b)
	ba := buildHLL(8, "c", "d", "e")
	ba.Merge(buildHLL(8, "a", "b", "c"))
	if ab.Count() != ba.Count() {
		t.Errorf("merge is not commutative: forward = %d, reverse = %d", ab.Count(), ba.Count())
	}

	// Order independent across three sketches: two different merge orders reaching the same final
	// union must leave identical registers, not merely the same estimate, because the propagation
	// loop compares registers directly to decide whether anything changed.
	c := buildHLL(8, "e", "f")
	left := buildHLL(8, "a", "b", "c")
	left.Merge(b)
	left.Merge(c)
	right := buildHLL(8, "a", "b", "c")
	right.Merge(c)
	right.Merge(buildHLL(8, "c", "d", "e"))
	for i := range left.registers {
		if left.registers[i] != right.registers[i] {
			t.Fatalf("merge order changed register %d: %d vs %d", i, left.registers[i], right.registers[i])
		}
	}
}

// TestHLLMergeRejectsMismatchedPrecision guards against a silent wrong answer: merging sketches of
// different sizes without checking would still run, iterating only over the smaller register array
// and quietly ignoring part of the larger one, which produces an undercount with no signal that
// anything went wrong.
func TestHLLMergeRejectsMismatchedPrecision(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Merge across different precisions should panic rather than silently under merge")
		}
	}()
	a := NewHLL(8)
	b := NewHLL(6)
	a.Merge(b)
}

// TestHLLErrorCurve measures actual estimation error rather than citing the nominal standard error
// formula. It inserts known, exact cardinalities at several precisions and checks the measured
// relative error against a multiple of the formula's prediction, so a regression that breaks the
// hash distribution or the estimator's bias correction fails a concrete number instead of passing on
// faith that the textbook formula still applies to this implementation.
func TestHLLErrorCurve(t *testing.T) {
	precisions := []uint{6, 8, 10}
	cardinalities := []int{100, 1_000, 10_000, 100_000}

	for _, p := range precisions {
		m := float64(uint(1) << p)
		nominal := 1.04 / math.Sqrt(m)
		for _, n := range cardinalities {
			h := NewHLL(p)
			for i := 0; i < n; i++ {
				h.Add(NodeID(fmt.Sprintf("member-%d", i)))
			}
			got := h.Count()
			errRate := math.Abs(float64(got-n)) / float64(n)
			t.Logf("precision=%d (m=%d) n=%d estimate=%d relative_error=%.4f nominal_stderr=%.4f",
				p, uint(1)<<p, n, got, errRate, nominal)

			// Three standard errors under the normal approximation HLL error is usually modelled
			// with covers 99.7% of trials; failing this margin on a single run is a real defect,
			// not sampling noise, and the small range regime (n well under 2.5m) is measured here
			// too rather than assumed to inherit the large range formula's behaviour.
			if errRate > 3*nominal+0.02 {
				t.Errorf("precision=%d n=%d: relative error %.4f exceeds 3x nominal standard error %.4f",
					p, n, errRate, nominal)
			}
		}
	}
}
