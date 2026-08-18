package observatory

import (
	"reflect"
	"testing"

	"github.com/sumanthd032/keyholders/internal/sketch"
)

func TestLeaderboardRanksByKRIDescendingWithStableTies(t *testing.T) {
	results := map[sketch.NodeID]sketch.Sketch{
		"dougwilson":  sketchOf("a", "b", "c"),
		"ljharb":      sketchOf("a", "b"),
		"jongleberry": sketchOf("a"),
		"bravado":     sketchOf("x", "y"), // ties ljharb at 2, must sort after it by name
	}

	got := Leaderboard(results, 0)
	want := []LeaderboardEntry{
		{Name: "dougwilson", KRI: 3},
		{Name: "bravado", KRI: 2},
		{Name: "ljharb", KRI: 2},
		{Name: "jongleberry", KRI: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Leaderboard() = %+v, want %+v", got, want)
	}
}

func TestLeaderboardTruncatesToN(t *testing.T) {
	results := map[sketch.NodeID]sketch.Sketch{
		"a": sketchOf("1", "2", "3"),
		"b": sketchOf("1", "2"),
		"c": sketchOf("1"),
	}
	got := Leaderboard(results, 2)
	if len(got) != 2 {
		t.Fatalf("Leaderboard(n=2) returned %d entries, want 2", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("Leaderboard(n=2) = %+v, want the top two by KRI", got)
	}
}

// TestOrphanedRanksOnlyTheOrphanedSubset checks that Orphaned reports load bearing orphans ranked by
// reach, and does not pick up a package that has a maintainer just because it also has high reach.
func TestOrphanedRanksOnlyTheOrphanedSubset(t *testing.T) {
	packages := map[sketch.NodeID]sketch.Sketch{
		"widely-maintained": sketchOf("a", "b", "c", "d"), // highest reach, but not orphaned
		"abandoned-popular": sketchOf("a", "b", "c"),
		"abandoned-obscure": sketchOf("a"),
	}
	orphaned := []sketch.NodeID{"abandoned-popular", "abandoned-obscure"}

	got := Orphaned(packages, orphaned, 0)
	want := []LeaderboardEntry{
		{Name: "abandoned-popular", KRI: 3},
		{Name: "abandoned-obscure", KRI: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Orphaned() = %+v, want %+v (widely-maintained must not appear)", got, want)
	}
}

func TestOrphanedWithNoOrphansIsEmpty(t *testing.T) {
	packages := map[sketch.NodeID]sketch.Sketch{"fine": sketchOf("a")}
	got := Orphaned(packages, nil, 10)
	if len(got) != 0 {
		t.Errorf("Orphaned() with no orphans = %+v, want empty", got)
	}
}
