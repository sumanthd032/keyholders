// Package typosquat finds npm package name pairs close enough to be mistaken for one another, edit
// distance and homoglyph confusion alike, and ranks them by how lopsided the two packages' download
// counts are: a near-identical name to something ninety times more popular is the shape of an actual
// typosquat, where two similarly popular names are more likely a coincidence or a legitimate family
// (eslint, eslint-config, eslint-plugin).
package typosquat

// EditDistance is the Levenshtein distance between a and b: the minimum number of single character
// insertions, deletions, or substitutions turning one into the other. Computed with two rolling rows
// rather than a full matrix, since nothing here needs the alignment itself, only its cost.
func EditDistance(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}

	prev := make([]int, len(ra)+1)
	for i := range prev {
		prev[i] = i
	}
	curr := make([]int, len(ra)+1)

	for i := 1; i <= len(rb); i++ {
		curr[0] = i
		for j := 1; j <= len(ra); j++ {
			cost := 1
			if rb[i-1] == ra[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(ra)]
}
