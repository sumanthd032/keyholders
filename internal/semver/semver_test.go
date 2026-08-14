package semver

import (
	"encoding/json"
	"os"
	"testing"
)

// goldenCase is one range from testdata/golden.json, whose answers come from npm's own semver
// package. Resolution correctness is the foundation every downstream number rests on, and agreeing
// with the reference implementation is the only meaningful definition of correct here, so this is
// the test that matters most in the package.
//
// Regenerate with: node internal/semver/testdata/generate_golden.js
type goldenCase struct {
	Pkg           string          `json:"pkg"`
	Range         string          `json:"range"`
	Satisfies     map[string]bool `json:"satisfies"`
	MaxSatisfying *string         `json:"maxSatisfying"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("golden file is empty")
	}
	return cases
}

func TestSatisfiesMatchesNpm(t *testing.T) {
	cases := loadGolden(t)

	var pairs, mismatches int
	for _, c := range cases {
		r, err := ParseRange(c.Range)
		if err != nil {
			t.Errorf("ParseRange(%q) from %s: %v", c.Range, c.Pkg, err)
			continue
		}
		for verStr, want := range c.Satisfies {
			v, err := Parse(verStr)
			if err != nil {
				t.Errorf("Parse(%q): %v", verStr, err)
				continue
			}
			pairs++
			if got := r.Satisfies(v); got != want {
				mismatches++
				if mismatches <= 25 {
					t.Errorf("range %q version %q: got %v, npm says %v (pkg %s)",
						c.Range, verStr, got, want, c.Pkg)
				}
			}
		}
	}
	t.Logf("compared %d (range, version) pairs across %d ranges, %d mismatches",
		pairs, len(cases), mismatches)
}

func TestMaxSatisfyingMatchesNpm(t *testing.T) {
	cases := loadGolden(t)

	var checked, mismatches int
	for _, c := range cases {
		r, err := ParseRange(c.Range)
		if err != nil {
			continue // reported by TestSatisfiesMatchesNpm
		}
		versions := make([]Version, 0, len(c.Satisfies))
		for verStr := range c.Satisfies {
			if v, err := Parse(verStr); err == nil {
				versions = append(versions, v)
			}
		}

		got, found := MaxSatisfying(versions, r)
		checked++

		switch {
		case c.MaxSatisfying == nil:
			if found {
				mismatches++
				t.Errorf("range %q: got %q, npm says nothing satisfies", c.Range, got)
			}
		case !found:
			mismatches++
			t.Errorf("range %q: got nothing, npm says %q", c.Range, *c.MaxSatisfying)
		default:
			want, err := Parse(*c.MaxSatisfying)
			if err != nil {
				t.Errorf("golden maxSatisfying %q unparseable: %v", *c.MaxSatisfying, err)
				continue
			}
			if got.Compare(want) != 0 {
				mismatches++
				t.Errorf("range %q: got %q, npm says %q", c.Range, got, want)
			}
		}
	}
	t.Logf("compared maxSatisfying across %d ranges, %d mismatches", checked, mismatches)
}

func TestVersionOrdering(t *testing.T) {
	// Ordering has to be right before anything built on MaxSatisfying can be trusted, and the
	// prerelease cases are where naive implementations go wrong.
	ordered := []string{
		"1.0.0-0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
		"1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.1.0", "2.0.0",
	}
	for i := range len(ordered) - 1 {
		a, err := Parse(ordered[i])
		if err != nil {
			t.Fatalf("Parse(%q): %v", ordered[i], err)
		}
		b, err := Parse(ordered[i+1])
		if err != nil {
			t.Fatalf("Parse(%q): %v", ordered[i+1], err)
		}
		if a.Compare(b) >= 0 {
			t.Errorf("expected %s < %s", ordered[i], ordered[i+1])
		}
		if b.Compare(a) <= 0 {
			t.Errorf("expected %s > %s reversed", ordered[i+1], ordered[i])
		}
	}
}

func TestBuildMetadataIgnoredInComparison(t *testing.T) {
	a, _ := Parse("1.2.3+build.1")
	b, _ := Parse("1.2.3+build.99")
	if a.Compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
}

func TestRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "1", "1.2", "1.2.3.4", "a.b.c", "01.2.3", "1.02.3", "1.2.3-"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should have failed", s)
		}
	}
}

func TestIsAnyDetectsUnresolvableRanges(t *testing.T) {
	// A range carrying no version information cannot be resolved for a past instant, because doing
	// so needs dist-tag history that the registry does not retain. Callers depend on spotting these.
	for _, s := range []string{"*", "x", "", "latest"} {
		r, err := ParseRange(s)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", s, err)
		}
		if !r.IsAny() {
			t.Errorf("ParseRange(%q).IsAny() = false, want true", s)
		}
	}
	for _, s := range []string{"^1.2.3", ">=1.0.0", "1.x"} {
		r, _ := ParseRange(s)
		if r.IsAny() {
			t.Errorf("ParseRange(%q).IsAny() = true, want false", s)
		}
	}
}
