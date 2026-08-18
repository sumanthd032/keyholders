package observatory

import (
	"sort"

	"github.com/sumanthd032/keyholders/internal/sketch"
)

// LeaderboardEntry is one ranked row: a package or maintainer identified by name, and its KRI at the
// epoch the ranking was computed for.
type LeaderboardEntry struct {
	Name string
	KRI  int
}

// Leaderboard ranks results by KRI, descending, ties broken by name so the order is stable across
// runs that produce identical counts. It works for a package map or a maintainer map alike: KRI is a
// magnitude, and what it is a magnitude of is the caller's business, not this function's. Not the
// covering set, which needs a fixed entry point to be well defined and is audit scope only, and not
// an ecosystem wide dominance or Phantom Reach ranking, which sketch estimates cannot support.
func Leaderboard(results map[sketch.NodeID]sketch.Sketch, n int) []LeaderboardEntry {
	return top(rank(results), n)
}

// Orphaned ranks, by KRI descending, every package Aggregate found held by nobody at all: no account
// to notice a compromise, no account to patch one, and every dependent relying on publish rights that
// belong to no one without any way to know it from the package alone. orphaned is the set Aggregate
// already produced while crediting maintainers, one read per package, not a second pass over the
// graph asking the same question again.
//
// Ranking by reach rather than merely listing every orphan is what turns this into "load bearing":
// an abandoned package nothing depends on is a fact about the ecosystem's history, not a risk anyone
// is carrying today. The packages worth a reader's attention are the ones at the top.
func Orphaned(packages map[sketch.NodeID]sketch.Sketch, orphaned []sketch.NodeID, n int) []LeaderboardEntry {
	subset := make(map[sketch.NodeID]sketch.Sketch, len(orphaned))
	for _, id := range orphaned {
		subset[id] = packages[id]
	}
	return top(rank(subset), n)
}

func rank(results map[sketch.NodeID]sketch.Sketch) []LeaderboardEntry {
	entries := make([]LeaderboardEntry, 0, len(results))
	for id, sk := range results {
		entries = append(entries, LeaderboardEntry{Name: string(id), KRI: sk.Count()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].KRI != entries[j].KRI {
			return entries[i].KRI > entries[j].KRI
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}

// top returns the first n entries, or every entry if n is not a smaller, positive bound.
func top(entries []LeaderboardEntry, n int) []LeaderboardEntry {
	if n > 0 && n < len(entries) {
		return entries[:n]
	}
	return entries
}
