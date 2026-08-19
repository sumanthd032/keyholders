package typosquat

import "testing"

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"lodash", "lodash", 0},
		{"", "abc", 3},
		{"lodash", "lodahs", 2}, // transposition costs two single-character edits, not one
		{"lodash", "lodashs", 1},
		{"kitten", "sitting", 3}, // the textbook case
		{"cross-env", "crossenv", 1},
	}
	for _, c := range cases {
		if got := EditDistance(c.a, c.b); got != c.want {
			t.Errorf("EditDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestEditDistanceIsSymmetric(t *testing.T) {
	pairs := [][2]string{{"lodash", "1odash"}, {"react", "reactt"}, {"", "x"}}
	for _, p := range pairs {
		if EditDistance(p[0], p[1]) != EditDistance(p[1], p[0]) {
			t.Errorf("EditDistance(%q, %q) != EditDistance(%q, %q)", p[0], p[1], p[1], p[0])
		}
	}
}
