package typosquat

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"lodash", "lodash"},
		{"Lodash", "lodash"},
		{"cross-env", "crossenv"},
		{"crossenv", "crossenv"},
		{"cross_env", "crossenv"},
		{"l0dash", "lodash"},
		{"1odash", "lodash"},
		{"@babel/core", "@babel/core"},
		{"@Babel/Core", "@babel/core"},
		{"moderne", "modeme"}, // the rn->m fold is intentionally aggressive; see the doc comment
	}
	for _, c := range cases {
		if got := Normalize(c.name); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestNormalizeCatchesCrossEnvIncident is the case the doc comment on Normalize names directly: the
// 2017 crossenv package squatted cross-env by dropping its separator. If this stops passing, the
// homoglyph and separator folding regressed on the one incident this package was built to catch.
func TestNormalizeCatchesCrossEnvIncident(t *testing.T) {
	if Normalize("cross-env") != Normalize("crossenv") {
		t.Errorf("Normalize(cross-env)=%q and Normalize(crossenv)=%q should match",
			Normalize("cross-env"), Normalize("crossenv"))
	}
}
