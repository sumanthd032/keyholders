package query

import (
	"context"
	"testing"
)

// v names a version node the way the graph does, so PackageURNOf can split it back.
func v(pkg string) string { return "pkg:npm/" + pkg + "@1" }

func pkg(name string) string { return "pkg:npm/" + name }

// diamondReach is a tree where C sits under two routes and D under one:
//
//	app -> A -> C
//	app -> B -> C
//	A -> D
//
// Every edge is valid for all time, because coexistence is not what these tests are about: the
// question is whether removing an account's packages costs the project reach it cannot get back.
func diamondReach(t *testing.T) Result {
	t.Helper()

	exp := newTable(
		Edge{From: v("app"), To: v("A"), Valid: Always},
		Edge{From: v("app"), To: v("B"), Valid: Always},
		Edge{From: v("A"), To: v("C"), Valid: Always},
		Edge{From: v("B"), To: v("C"), Valid: Always},
		Edge{From: v("A"), To: v("D"), Valid: Always},
	)

	res, err := Reach(context.Background(), exp, Sources(v("app")), Options{Within: Always})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	if len(res.Coexistence) != 5 {
		t.Fatalf("reached %d versions, want 5", len(res.Coexistence))
	}
	return res
}

func keyholderOf(handle string, packages ...string) Keyholder {
	through := make(map[string]Set, len(packages))
	for _, p := range packages {
		through[pkg(p)] = Set{Always}
	}
	return Keyholder{Handle: handle, Through: through}
}

func cutOf(t *testing.T, cuts []Cut, handle string) Cut {
	t.Helper()
	for _, c := range cuts {
		if c.Handle == handle {
			return c
		}
	}
	t.Fatalf("no cut for %q in %v", handle, cuts)
	return Cut{}
}

// TestCutFindsTheRouteAround is the distinction the irreplaceable list exists to draw. Alice and Bob
// each hold one of two routes to C, so neither of them can take C away; only D, which hangs off
// Alice's package alone, actually falls.
func TestCutFindsTheRouteAround(t *testing.T) {
	reach := diamondReach(t)
	cuts := Cuts(reach, []Keyholder{
		keyholderOf("alice", "A"),
		keyholderOf("bob", "B"),
	})

	alice := cutOf(t, cuts, "alice")
	if alice.Packages != 2 {
		t.Errorf("removing alice cost %d packages, want 2 (A and D)", alice.Packages)
	}
	if alice.Beyond != 1 || len(alice.Orphaned) != 1 || alice.Orphaned[0] != pkg("D") {
		t.Errorf("alice orphaned %v (Beyond %d), want just D", alice.Orphaned, alice.Beyond)
	}
	if !alice.Irreplaceable() {
		t.Error("alice is the only route to D and must be reported irreplaceable")
	}

	bob := cutOf(t, cuts, "bob")
	if bob.Packages != 1 {
		t.Errorf("removing bob cost %d packages, want 1 (B alone: C is still reachable through A)",
			bob.Packages)
	}
	if bob.Beyond != 0 {
		t.Errorf("bob orphaned %v, want nothing: there is a route around him to C", bob.Orphaned)
	}
	if bob.Irreplaceable() {
		t.Error("bob holds a package with an alternative route and must not be irreplaceable")
	}
}

// TestCutOfBothRoutesTakesTheSharedDependency pins that dominance is computed over the account's whole
// holding rather than one package at a time. Neither A nor B alone can take C away, but an account
// holding both can, and an implementation that scored each package separately would miss it.
func TestCutOfBothRoutesTakesTheSharedDependency(t *testing.T) {
	reach := diamondReach(t)
	cuts := Cuts(reach, []Keyholder{keyholderOf("dave", "A", "B")})

	dave := cutOf(t, cuts, "dave")
	if dave.Controls != 2 {
		t.Errorf("Controls = %d, want 2", dave.Controls)
	}
	if dave.Packages != 4 {
		t.Errorf("removing dave cost %d packages, want 4 (A, B, C, D)", dave.Packages)
	}
	if dave.Beyond != 2 {
		t.Errorf("Beyond = %d, want 2 (C and D, neither of which he holds)", dave.Beyond)
	}
	want := map[string]bool{pkg("C"): true, pkg("D"): true}
	for _, p := range dave.Orphaned {
		if !want[p] {
			t.Errorf("orphaned %q, which dave does not hold and which has another route", p)
		}
	}
}

// TestCutOfARootTakesTheWholeTree is the case most likely to be read as a bug in real output: an
// account that can publish to a direct dependency takes everything beneath it, so a roster entry
// controlling three packages can be reported as costing fifty. That is dominance working, not
// double counting, and the number would be dishonest if it were smaller.
func TestCutOfARootTakesTheWholeTree(t *testing.T) {
	reach := diamondReach(t)
	cuts := Cuts(reach, []Keyholder{keyholderOf("root", "app")})

	root := cutOf(t, cuts, "root")
	if root.Controls != 1 {
		t.Errorf("Controls = %d, want 1", root.Controls)
	}
	if root.Packages != 5 {
		t.Errorf("removing the root cost %d packages, want all 5", root.Packages)
	}
	if root.Versions != len(reach.Coexistence) {
		t.Errorf("lost %d versions, want all %d", root.Versions, len(reach.Coexistence))
	}
	if root.Beyond != 4 {
		t.Errorf("Beyond = %d, want 4", root.Beyond)
	}
}

// TestCutSkipsAccountsWithNothingReached guards against a roster entry for a package the search never
// reached producing an empty cut that claims the project loses nothing, which would read as a
// harmless account rather than an irrelevant one.
func TestCutSkipsAccountsWithNothingReached(t *testing.T) {
	reach := diamondReach(t)
	cuts := Cuts(reach, []Keyholder{
		keyholderOf("alice", "A"),
		keyholderOf("stranger", "not-in-this-tree"),
	})

	if len(cuts) != 1 {
		t.Fatalf("got %d cuts, want 1: an account holding nothing reached has no cut", len(cuts))
	}
	if cuts[0].Handle != "alice" {
		t.Errorf("cut is for %q, want alice", cuts[0].Handle)
	}
}

func TestCutsOrderWidestFirstThenByHandle(t *testing.T) {
	reach := diamondReach(t)
	cuts := Cuts(reach, []Keyholder{
		keyholderOf("carol", "C"),
		keyholderOf("bob", "B"),
		keyholderOf("alice", "A"),
	})

	// Alice costs two packages, Bob and Carol one each, so the tie breaks on handle.
	want := []string{"alice", "bob", "carol"}
	for i, handle := range want {
		if cuts[i].Handle != handle {
			t.Fatalf("cut %d is %q, want %q", i, cuts[i].Handle, handle)
		}
	}
}

// TestCutRespectsCoexistence checks that removal analysis inherits the interval rule rather than
// falling back to the union graph. D is reachable only through a chain that never held at one
// instant, so it is not reach the project has and cutting Alice cannot cost it.
func TestCutRespectsCoexistence(t *testing.T) {
	exp := newTable(
		Edge{From: v("app"), To: v("A"), Valid: span(jan, jun)},
		Edge{From: v("A"), To: v("D"), Valid: span(aug, dec)},
	)
	reach, err := Reach(context.Background(), exp, Sources(v("app")), Options{Within: Always})
	if err != nil {
		t.Fatalf("Reach: %v", err)
	}
	if !reach.Union[v("D")] {
		t.Fatal("the union graph must reach D, or this test is not testing anything")
	}

	alice := cutOf(t, Cuts(reach, []Keyholder{keyholderOf("alice", "A")}), "alice")
	if alice.Packages != 1 {
		t.Errorf("removing alice cost %d packages, want 1: D was never coexistence-reachable", alice.Packages)
	}
	if alice.Beyond != 0 {
		t.Errorf("Beyond = %d, want 0: a phantom dependency is not reach that can be lost", alice.Beyond)
	}
}
