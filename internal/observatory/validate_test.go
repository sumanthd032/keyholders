package observatory

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/sumanthd032/keyholders/internal/sketch"
)

func TestRelativeError(t *testing.T) {
	cases := []struct {
		name         string
		exact, guess int
		want         float64
	}{
		{"exact match", 100, 100, 0},
		{"overestimate", 100, 110, 0.1},
		{"underestimate", 100, 90, 0.1},
		{"both zero, isolated package", 0, 0, 0},
		{"estimator invents dependents for an isolated package", 0, 5, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ValidationResult{Exact: c.exact, Estimate: c.guess}
			if got := r.RelativeError(); got != c.want {
				t.Errorf("RelativeError() = %v, want %v", got, c.want)
			}
		})
	}
}

// buildLayeredGraph mirrors the generator internal/sketch uses for its own benchmark: each node
// depends on a handful of nodes ranked after it, acyclic, with real fan out rather than a worst case
// shape, so a validation run here exercises the same kind of graph the observatory actually
// propagates over.
func buildLayeredGraph(nodeCount, fanout int) ([]sketch.NodeID, []sketch.Edge) {
	nodes := make([]sketch.NodeID, nodeCount)
	for i := range nodes {
		nodes[i] = sketch.NodeID(fmt.Sprintf("n%d", i))
	}

	rng := rand.New(rand.NewSource(2))
	edges := make([]sketch.Edge, 0, nodeCount*fanout)
	for i := 0; i < nodeCount-1; i++ {
		for k := 0; k < fanout; k++ {
			j := i + 1 + rng.Intn(nodeCount-i-1)
			edges = append(edges, sketch.Edge{From: nodes[i], To: nodes[j]})
		}
	}
	return nodes, edges
}

// TestValidateAgreesWithHLLWithinNominalError runs a moderate synthetic graph through Validate and
// checks that HyperLogLog's estimate, checked against the exact fixpoint over the identical edges
// rather than a synthetic cardinality, still lands within the error band internal/sketch's own tests
// measured. That is the actual claim task 6 exists to check: not that HLL is accurate in isolation,
// which task 3 already established, but that nothing about running it inside this package's
// propagation and edge construction reintroduces error HLL's own tests would not have caught.
func TestValidateAgreesWithHLLWithinNominalError(t *testing.T) {
	nodes, edges := buildLayeredGraph(2000, 5)

	// Edges only ever point toward a higher index, so the last few nodes accumulate almost every
	// other node's reach, where the estimate has room to be wrong, while the first few have no
	// incoming edges at all, where both engines have to agree on exactly one trivially.
	sample := []sketch.NodeID{nodes[0], nodes[1], nodes[2], nodes[len(nodes)-1], nodes[len(nodes)-2]}

	results := Validate(nodes, edges, sample, func() sketch.Sketch { return sketch.NewHLL(8) })
	if len(results) != len(sample) {
		t.Fatalf("got %d results, want %d", len(results), len(sample))
	}

	const nominalStderr = 1.04 / 16 // precision 8, m = 256
	for _, r := range results {
		t.Logf("%s: exact=%d estimate=%d relative_error=%.4f", r.Package, r.Exact, r.Estimate, r.RelativeError())
		if r.Exact > 20 && r.RelativeError() > 3*nominalStderr {
			t.Errorf("%s: relative error %.4f exceeds 3x nominal standard error %.4f (exact=%d, estimate=%d)",
				r.Package, r.RelativeError(), 3*nominalStderr, r.Exact, r.Estimate)
		}
	}
}
