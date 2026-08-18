package typosquat

import "testing"

func findPair(cands []Candidate, a, b string) (Candidate, bool) {
	for _, c := range cands {
		if (c.Popular == a && c.Lookalike == b) || (c.Popular == b && c.Lookalike == a) {
			return c, true
		}
	}
	return Candidate{}, false
}

func TestFindCatchesCrossEnvIncident(t *testing.T) {
	names := []string{"cross-env", "crossenv", "lodash", "express", "react"}
	ranks := map[string]int{"cross-env": 40, "crossenv": 8000, "lodash": 1, "express": 5, "react": 2}

	cands := Find(names, ranks, 2)
	c, ok := findPair(cands, "cross-env", "crossenv")
	if !ok {
		t.Fatalf("Find() did not surface cross-env/crossenv, got %+v", cands)
	}
	if c.Popular != "cross-env" || c.Lookalike != "crossenv" {
		t.Errorf("oriented wrong way: Popular=%q Lookalike=%q, want Popular=cross-env", c.Popular, c.Lookalike)
	}
	if c.PopularityRatio != 200 { // 8000 / 40
		t.Errorf("PopularityRatio = %v, want 200", c.PopularityRatio)
	}
}

func TestFindExcludesUnrelatedNames(t *testing.T) {
	names := []string{"lodash", "express", "webpack"}
	ranks := map[string]int{"lodash": 1, "express": 2, "webpack": 3}
	if cands := Find(names, ranks, 2); len(cands) != 0 {
		t.Errorf("Find() = %v, want no candidates among unrelated names", cands)
	}
}

func TestFindTreatsAnUnrankedNameAsMaximallyUnpopular(t *testing.T) {
	names := []string{"lodash", "1odash"}
	ranks := map[string]int{"lodash": 1} // "1odash" has no entry: never appeared in the ranked list

	cands := Find(names, ranks, 2)
	c, ok := findPair(cands, "lodash", "1odash")
	if !ok {
		t.Fatalf("Find() did not surface lodash/1odash, got %+v", cands)
	}
	if c.Popular != "lodash" {
		t.Errorf("Popular = %q, want lodash (the ranked package)", c.Popular)
	}
	if c.PopularityRatio <= 1000 {
		t.Errorf("PopularityRatio = %v, want a very large ratio for an unranked lookalike", c.PopularityRatio)
	}
}

func TestFindDeduplicatesPairsAcrossWindowOverlap(t *testing.T) {
	names := []string{"aaaa", "aaab", "aaac"}
	ranks := map[string]int{"aaaa": 1, "aaab": 2, "aaac": 3}

	cands := Find(names, ranks, 1)
	seen := map[[2]string]int{}
	for _, c := range cands {
		seen[pairKey(c.Popular, c.Lookalike)]++
	}
	for pair, n := range seen {
		if n > 1 {
			t.Errorf("pair %v reported %d times, want at most once", pair, n)
		}
	}
}
