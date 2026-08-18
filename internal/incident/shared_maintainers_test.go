package incident

import (
	"context"
	"sort"
	"testing"
)

// table is a MaintainerSource backed by literal holdings, keyed the way MAINTAINS validity windows
// are: a holder is only returned for an instant inside its window, so tests can exercise the temporal
// behaviour without a graph.
type table struct {
	// holders maps package name to the accounts holding it, each with the window they held it in.
	holders map[string][]holding
}

type holding struct {
	handle             string
	validFrom, validTo int64
}

func (t *table) MaintainersOf(_ context.Context, name string, at int64) ([]string, error) {
	var out []string
	for _, h := range t.holders[name] {
		if h.validFrom <= at && at < h.validTo {
			out = append(out, h.handle)
		}
	}
	return out, nil
}

func (t *table) PackagesOf(_ context.Context, handle string, at int64) ([]string, error) {
	var out []string
	for pkg, holders := range t.holders {
		for _, h := range holders {
			if h.handle == handle && h.validFrom <= at && at < h.validTo {
				out = append(out, pkg)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func sortedSharedMaintainers(s []SharedMaintainer) []SharedMaintainer {
	out := append([]SharedMaintainer(nil), s...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Maintainer != out[j].Maintainer {
			return out[i].Maintainer < out[j].Maintainer
		}
		return out[i].Package < out[j].Package
	})
	return out
}

func TestSharedMaintainersFindsOtherPackagesTheSameAccountHolds(t *testing.T) {
	tab := &table{holders: map[string][]holding{
		"left-pad":  {{handle: "alice", validFrom: 0, validTo: 1000}},
		"right-pad": {{handle: "alice", validFrom: 0, validTo: 1000}},
		"unrelated": {{handle: "bob", validFrom: 0, validTo: 1000}},
	}}

	got, err := SharedMaintainers(context.Background(), tab, "left-pad", 500)
	if err != nil {
		t.Fatalf("SharedMaintainers: %v", err)
	}
	want := []SharedMaintainer{{Maintainer: "alice", Package: "right-pad"}}
	if !equalShared(sortedSharedMaintainers(got), want) {
		t.Errorf("SharedMaintainers() = %v, want %v", got, want)
	}
}

// TestSharedMaintainersExcludesTheQueriedPackageItself is the trivial two-hop loop back to the
// starting node that a naive walk would otherwise report as its own neighbour.
func TestSharedMaintainersExcludesTheQueriedPackageItself(t *testing.T) {
	tab := &table{holders: map[string][]holding{
		"left-pad": {{handle: "alice", validFrom: 0, validTo: 1000}},
	}}

	got, err := SharedMaintainers(context.Background(), tab, "left-pad", 500)
	if err != nil {
		t.Fatalf("SharedMaintainers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("SharedMaintainers() = %v, want none: alice's only package is the one queried", got)
	}
}

// TestSharedMaintainersRespectsTheValidityWindow is the coexistence rule this package inherits from
// the rest of the project: an account credited before it ever held the package, or after it left,
// must not be reported as a live adjacency at that instant.
func TestSharedMaintainersRespectsTheValidityWindow(t *testing.T) {
	tab := &table{holders: map[string][]holding{
		"left-pad":  {{handle: "alice", validFrom: 100, validTo: 200}},
		"right-pad": {{handle: "alice", validFrom: 100, validTo: 200}},
	}}

	before, err := SharedMaintainers(context.Background(), tab, "left-pad", 50)
	if err != nil {
		t.Fatalf("SharedMaintainers: %v", err)
	}
	if len(before) != 0 {
		t.Errorf("before the window opened, got %v, want none", before)
	}

	after, err := SharedMaintainers(context.Background(), tab, "left-pad", 250)
	if err != nil {
		t.Fatalf("SharedMaintainers: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("after the window closed, got %v, want none", after)
	}
}

func equalShared(a, b []SharedMaintainer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
