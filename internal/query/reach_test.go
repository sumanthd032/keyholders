package query

import (
	"context"
	"sort"
	"testing"
)

// table is an Expander backed by a literal edge list, so the search can be tested without a graph.
type table struct {
	edges map[string][]Edge
	calls int
}

func newTable(edges ...Edge) *table {
	t := &table{edges: map[string][]Edge{}}
	for _, e := range edges {
		t.edges[e.From] = append(t.edges[e.From], e)
	}
	return t
}

func (t *table) Out(_ context.Context, urns []string) ([]Edge, error) {
	t.calls++
	var out []Edge
	for _, u := range urns {
		out = append(out, t.edges[u]...)
	}
	return out, nil
}

func span(from, to int64) Interval { return Interval{From: from, To: to} }

// Months as small integers. Nothing in the search depends on the scale, and reading "Jan to Jun"
// off the test is worth more than reading a Unix timestamp.
const (
	jan int64 = 1
	feb int64 = 2
	jun int64 = 6
	aug int64 = 8
	oct int64 = 10
	dec int64 = 12
)

// TestPhantomPath is the case the whole project exists for, taken from the worked example.
//
//	your-app -> A  valid [Jan, Jun)
//	A        -> B  valid [Aug, Dec)
//
// The union graph says your-app reaches B, so B's maintainer is counted as a keyholder. But the two
// edges were never valid at the same time. That path never existed, not for one second, and the
// maintainer of B never held a key. Any change that makes this test pass by reporting B as reachable
// has removed the reason this project is different from every other tool.
func TestPhantomPath(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: span(jan, jun)},
		Edge{From: "A", To: "B", Valid: span(aug, dec)},
	)

	res, err := Reach(context.Background(), exp, Sources("app"), Options{Within: Always})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}

	if !res.Union["B"] {
		t.Fatal("the union graph must reach B, or the comparison being drawn is meaningless")
	}
	if len(res.Coexistence["B"]) != 0 {
		t.Errorf("B is reachable only through a path that never coexisted, got %v", res.Coexistence["B"])
	}
	if got := res.Coexistence["A"]; len(got) != 1 || got[0] != span(jan, jun) {
		t.Errorf("A should be reachable exactly over [Jan, Jun), got %v", got)
	}
	if got, want := res.PhantomReach(), 1; got != want {
		t.Errorf("PhantomReach = %d, want %d", got, want)
	}
}

// TestCoexistingPath is the same shape with overlapping windows, so the path is real.
func TestCoexistingPath(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: span(jan, aug)},
		Edge{From: "A", To: "B", Valid: span(jun, dec)},
	)

	res, err := Reach(context.Background(), exp, Sources("app"), Options{Within: Always})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}

	// The intersection of [Jan, Aug) and [Jun, Dec) is [Jun, Aug): B was reachable then and only
	// then, even though each edge individually was valid for longer.
	got := res.Coexistence["B"]
	if len(got) != 1 || got[0] != span(jun, aug) {
		t.Errorf("B reachable over %v, want [Jun, Aug)", got)
	}
	if res.PhantomReach() != 0 {
		t.Errorf("no node should be phantom here, got %d", res.PhantomReach())
	}
}

// TestSecondPathRescues checks that a node unreachable by one chain is still found by another. A
// search that stopped at the first pruned branch would undercount, which for a security tool is the
// dangerous direction.
func TestSecondPathRescues(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: span(jan, feb)},
		Edge{From: "A", To: "target", Valid: span(oct, dec)}, // never coexists
		Edge{From: "app", To: "C", Valid: span(jan, dec)},
		Edge{From: "C", To: "target", Valid: span(feb, jun)}, // does coexist
	)

	res, err := Reach(context.Background(), exp, Sources("app"), Options{Within: Always})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	got := res.Coexistence["target"]
	if len(got) != 1 || got[0] != span(feb, jun) {
		t.Errorf("target reachable over %v, want [Feb, Jun) via the second path", got)
	}
}

func TestReachAtAnInstant(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: span(jan, jun)},
		Edge{From: "A", To: "B", Valid: span(feb, dec)},
	)

	cases := []struct {
		name string
		at   int64
		want []string
	}{
		// [Jan,Jun) intersect [Feb,Dec) is [Feb,Jun), so B is reachable only inside that.
		{"before anything", 0, nil},
		{"A only", jan, []string{"A"}},
		{"both", feb, []string{"A", "B"}},
		{"after the first edge lapsed", aug, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Reach(context.Background(), exp, Sources("app"), Options{Within: At(tc.at)})
			if err != nil {
				t.Fatalf("Reach: %v", err)
			}
			got := res.ReachedAt(tc.at)
			// The source is always reachable from itself; the question is what else.
			got = without(got, "app")
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("reached %v at %d, want %v", got, tc.at, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("reached %v at %d, want %v", got, tc.at, tc.want)
				}
			}
		})
	}
}

// TestCycleTerminates guards the obvious way a dependency walk hangs. npm graphs contain cycles.
func TestCycleTerminates(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: Always},
		Edge{From: "A", To: "B", Valid: Always},
		Edge{From: "B", To: "A", Valid: Always},
	)

	res, err := Reach(context.Background(), exp, Sources("app"), Options{Within: Always, MaxDepth: 50})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	if len(res.Coexistence) != 3 {
		t.Errorf("reached %d nodes, want 3", len(res.Coexistence))
	}
}

// TestDepthBoundIsReported pins that a search stopped by its depth bound says so. A truncated
// traversal is a lower bound, and reporting it as an answer would understate exposure.
func TestDepthBoundIsReported(t *testing.T) {
	exp := newTable(
		Edge{From: "a", To: "b", Valid: Always},
		Edge{From: "b", To: "c", Valid: Always},
		Edge{From: "c", To: "d", Valid: Always},
	)

	res, err := Reach(context.Background(), exp, Sources("a"), Options{Within: Always, MaxDepth: 2})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	if !res.Truncated {
		t.Error("a search that hit its depth bound with frontier remaining must report Truncated")
	}
	if res.Union["d"] {
		t.Error("d is three hops away and must not be reported at MaxDepth 2")
	}

	full, err := Reach(context.Background(), exp, Sources("a"), Options{Within: Always, MaxDepth: 10})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	if full.Truncated {
		t.Error("a search that exhausted its frontier must not report Truncated")
	}
}

// TestOneReadPerDepth pins the property that makes the search affordable: the frontier is expanded
// in one batched call per level, not one call per node.
func TestOneReadPerDepth(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: Always},
		Edge{From: "app", To: "B", Valid: Always},
		Edge{From: "A", To: "C", Valid: Always},
		Edge{From: "B", To: "D", Valid: Always},
	)

	if _, err := Reach(context.Background(), exp, Sources("app"), Options{Within: Always}); err != nil {
		t.Fatalf("Reach: %v", err)
	}
	// Two levels have edges, and the third call discovers the frontier is exhausted.
	if exp.calls > 3 {
		t.Errorf("expanded in %d calls, want one per depth", exp.calls)
	}
}

func TestFrontiersAreStreamed(t *testing.T) {
	exp := newTable(
		Edge{From: "app", To: "A", Valid: Always},
		Edge{From: "A", To: "B", Valid: Always},
	)

	var depths []int
	_, err := Reach(context.Background(), exp, Sources("app"), Options{
		Within:     Always,
		OnFrontier: func(f Frontier) { depths = append(depths, f.Depth) },
	})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	if len(depths) != 3 {
		t.Fatalf("observed frontiers at depths %v, want three wavefronts", depths)
	}
	for i, d := range depths {
		if d != i {
			t.Errorf("frontier %d reported depth %d", i, d)
		}
	}
}

func without(s []string, drop string) []string {
	out := s[:0]
	for _, v := range s {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}
