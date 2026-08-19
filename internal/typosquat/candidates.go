package typosquat

import "sort"

// Candidate is one pair of package names close enough to flag. Popular is always the more downloaded
// of the two, by rank, so PopularityRatio is oriented the same way for every candidate: how many
// times further down the download ranking the lookalike sits.
type Candidate struct {
	Popular         string
	Lookalike       string
	Distance        int
	PopularityRatio float64
}

// window is how many lexicographic neighbours, after sorting by normalized name, each name is
// compared against. A true match at or under a small edit distance almost always leaves a long
// shared prefix, so it sorts close by; window bounds the comparison to O(n * window) instead of
// O(n^2), which matters once the ranked list reaches the tens of thousands this project targets.
const window = 20

// unranked stands in for a package with no position in the ranked list this comparison runs over: a
// node the graph only knows about as someone's dependency. Treating it as maximally unpopular, rather
// than excluding it, is what lets a genuinely unranked squat target still surface, at the cost of a
// popularity ratio that is a comparison against "unranked" rather than a real number. See D48.
const unranked = int(^uint(0) >> 1)

// Find returns every distinct pair of names whose normalized forms are within maxDistance edits of
// each other. ranks is rank by download count, 1 most downloaded; a name absent from it is scored as
// unranked. See D48 for why rank, not a raw download count, is the popularity signal available here.
func Find(names []string, ranks map[string]int, maxDistance int) []Candidate {
	type entry struct {
		name       string
		normalized string
	}
	entries := make([]entry, len(names))
	for i, n := range names {
		entries[i] = entry{name: n, normalized: Normalize(n)}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].normalized < entries[j].normalized })

	seen := map[[2]string]bool{}
	var out []Candidate
	for i := range entries {
		for j := i + 1; j < len(entries) && j <= i+window; j++ {
			a, b := entries[i], entries[j]
			if a.name == b.name {
				continue
			}
			// Comparing normalized forms is what makes this catch both plain typos and homoglyph or
			// separator substitutions: a distance of 0 here, two different literal names folding to
			// the same normalized form, is the strongest signal this function can produce, not a
			// no-op to be filtered out, since `crossenv` vs `cross-env` is exactly that case.
			d := EditDistance(a.normalized, b.normalized)
			if d > maxDistance {
				continue
			}
			pair := pairKey(a.name, b.name)
			if seen[pair] {
				continue
			}
			seen[pair] = true
			out = append(out, orient(a.name, b.name, d, ranks))
		}
	}
	return out
}

func pairKey(a, b string) [2]string {
	if a < b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

func rankOf(ranks map[string]int, name string) int {
	if r, ok := ranks[name]; ok {
		return r
	}
	return unranked
}

func orient(a, b string, d int, ranks map[string]int) Candidate {
	ra, rb := rankOf(ranks, a), rankOf(ranks, b)
	popular, lookalike, popRank, lookRank := a, b, ra, rb
	if rb < ra {
		popular, lookalike, popRank, lookRank = b, a, rb, ra
	}
	return Candidate{
		Popular:         popular,
		Lookalike:       lookalike,
		Distance:        d,
		PopularityRatio: float64(lookRank) / float64(popRank),
	}
}
