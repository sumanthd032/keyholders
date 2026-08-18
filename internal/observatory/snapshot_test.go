package observatory

import (
	"context"
	"sort"
	"testing"

	"github.com/sumanthd032/keyholders/internal/sketch"
)

// table is a Source backed by literal timelines and dependency lists, so Snapshots can be tested
// without a graph. It also counts calls, which is what proves the deduplication claim in the doc
// comment on Snapshots rather than just trusting it.
type table struct {
	timelines map[string][]Release
	deps      map[string][]string // keyed "name@version"
	depCalls  int
}

func (t *table) Timeline(_ context.Context, name string) ([]Release, error) {
	return t.timelines[name], nil
}

func (t *table) Dependencies(_ context.Context, name, version string) ([]string, error) {
	t.depCalls++
	return t.deps[name+"@"+version], nil
}

func sortedEdges(edges []sketch.Edge) []sketch.Edge {
	out := append([]sketch.Edge(nil), edges...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// TestSnapshotsFollowsCurrentVersion is what package level resolution means: app depends on a
// package whose current version changes between two epochs, and the dependency it declares changes
// with it. The snapshot at each epoch must reflect the version that was actually current then, not
// the package's latest version overall.
func TestSnapshotsFollowsCurrentVersion(t *testing.T) {
	const (
		epoch1 int64 = 100
		epoch2 int64 = 200
	)
	tab := &table{
		timelines: map[string][]Release{
			"app": {{Version: "1.0.0", PublishedAt: 1}},
			"lib": {
				{Version: "1.0.0", PublishedAt: 1},
				{Version: "2.0.0", PublishedAt: 150}, // published between the two epochs
			},
		},
		deps: map[string][]string{
			"app@1.0.0": {"lib"},
			"lib@1.0.0": {"left-pad"},
			"lib@2.0.0": {"right-pad"}, // lib swapped its dependency in 2.0.0
		},
	}

	got, err := Snapshots(context.Background(), tab, []string{"app", "lib"}, []int64{epoch1, epoch2})
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}

	want1 := []sketch.Edge{{From: "app", To: "lib"}, {From: "lib", To: "left-pad"}}
	if got1 := sortedEdges(got[epoch1]); !equalEdges(got1, sortedEdges(want1)) {
		t.Errorf("epoch1 edges = %v, want %v", got1, want1)
	}

	want2 := []sketch.Edge{{From: "app", To: "lib"}, {From: "lib", To: "right-pad"}}
	if got2 := sortedEdges(got[epoch2]); !equalEdges(got2, sortedEdges(want2)) {
		t.Errorf("epoch2 edges = %v, want %v", got2, want2)
	}
}

// TestSnapshotsSkipsUnpublishedPackages is D34's rule applied at package granularity: a package
// cannot contribute a dependency edge at an epoch before it existed.
func TestSnapshotsSkipsUnpublishedPackages(t *testing.T) {
	tab := &table{
		timelines: map[string][]Release{
			"new-lib": {{Version: "1.0.0", PublishedAt: 500}},
		},
		deps: map[string][]string{
			"new-lib@1.0.0": {"left-pad"},
		},
	}

	got, err := Snapshots(context.Background(), tab, []string{"new-lib"}, []int64{100, 600})
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	if len(got[100]) != 0 {
		t.Errorf("epoch before publication should contribute no edges, got %v", got[100])
	}
	if len(got[600]) != 1 {
		t.Errorf("epoch after publication should contribute the declared edge, got %v", got[600])
	}
}

// TestSnapshotsDedupesDependencyReadsByVersion is the efficiency claim in the doc comment on
// Snapshots: a package whose current version is the same across several epochs must have its
// dependencies read once, not once per epoch, because the read is anchored at the resolved version
// and nothing about it changes between those epochs.
func TestSnapshotsDedupesDependencyReadsByVersion(t *testing.T) {
	tab := &table{
		timelines: map[string][]Release{
			"stable": {{Version: "1.0.0", PublishedAt: 1}},
		},
		deps: map[string][]string{
			"stable@1.0.0": {"left-pad"},
		},
	}

	epochs := []int64{10, 20, 30, 40, 50}
	if _, err := Snapshots(context.Background(), tab, []string{"stable"}, epochs); err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	if tab.depCalls != 1 {
		t.Errorf("Dependencies called %d times across %d identical epochs, want 1", tab.depCalls, len(epochs))
	}
}

func equalEdges(a, b []sketch.Edge) bool {
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
