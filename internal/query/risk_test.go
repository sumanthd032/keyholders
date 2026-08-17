package query

import (
	"math"
	"testing"
)

// year is a plain number of seconds, because every staleness figure in these tests is stated in years
// and reading one off a timestamp is harder than it needs to be.
const year int64 = 365 * 24 * 60 * 60

// holder builds a keyholder reaching n packages. The spans do not matter to the score, only the
// count, so they are all Always.
func holder(handle string, n int) Keyholder {
	through := make(map[string]Set, n)
	for i := 0; i < n; i++ {
		through[string(rune('a'+i))] = Set{Always}
	}
	return Keyholder{Handle: handle, Through: through}
}

// clean is an account every signal was measured on and none of them flagged.
func clean(of int, now int64) Signals {
	return Signals{
		Solo:          Fraction{Count: 0, Of: of},
		NoProvenance:  Fraction{Count: 0, Of: of},
		InstallScript: Fraction{Count: 0, Of: of},
		LastPublish:   now,
	}
}

// TestUnmeasuredDoesNotOutrankMeasuredClean is the invariant the score is built around, and the one
// that a plausible simplification breaks: scoring an unknown term as zero would rank an account
// nobody has any evidence about as safer than one that was checked and came back clean. On a security
// ranking that inverts the meaning of missing data, so it is pinned here rather than left to the
// comment on Score.
func TestUnmeasuredDoesNotOutrankMeasuredClean(t *testing.T) {
	const now = 40 * year

	unknown := scoreOf(holder("unknown", 10), Signals{}, DefaultWeights, 50, now)
	measured := scoreOf(holder("measured", 10), clean(20, now), DefaultWeights, 50, now)

	if unknown.Total <= measured.Total {
		t.Errorf("unmeasured account scored %.3f, measured-clean scored %.3f: an account with no "+
			"evidence must not rank as safer than one checked and found clean",
			unknown.Total, measured.Total)
	}
	if unknown.Known() != 1 {
		t.Errorf("only reach is measurable here, got %d known terms", unknown.Known())
	}
	if measured.Known() != 5 {
		t.Errorf("every term is measurable here, got %d known terms", measured.Known())
	}
}

// TestUnknownWeightIsRedistributed pins that the surviving terms carry the whole scale and keep their
// relative importance. Without redistribution a score built from three terms would top out at the sum
// of their weights, so it could never be compared against one built from five.
func TestUnknownWeightIsRedistributed(t *testing.T) {
	const now = 40 * year

	// Reach and solo only: no publish observed, and no annotated versions to judge provenance or
	// install scripts on.
	s := Signals{Solo: Fraction{Count: 4, Of: 4}}
	got := scoreOf(holder("partial", 4), s, DefaultWeights, 4, now)

	var known float64
	for _, term := range got.Terms {
		if term.Known {
			known += term.Weight
		}
	}
	if math.Abs(known-1) > 1e-9 {
		t.Errorf("known weights sum to %.6f, want 1: an unknown term's weight has to go somewhere", known)
	}

	// Reach is 0.40 and solo 0.20 before redistribution, so reach must stay twice solo after it.
	byName := map[string]Term{}
	for _, term := range got.Terms {
		byName[term.Name] = term
	}
	if r, s := byName["reach"].Weight, byName["solo"].Weight; math.Abs(r-2*s) > 1e-9 {
		t.Errorf("reach weight %.4f against solo %.4f: redistribution must be proportional", r, s)
	}
	// Both terms are at their maximum, so the total is the whole scale.
	if math.Abs(got.Total-1) > 1e-9 {
		t.Errorf("Total = %.6f, want 1 when every measured term is at its maximum", got.Total)
	}
}

func TestNothingMeasuredScoresZero(t *testing.T) {
	// reachDenominator of zero is a real case: an audit whose lockfile pins nothing in the graph.
	got := scoreOf(holder("nobody", 0), Signals{}, DefaultWeights, 0, 40*year)
	if got.Total != 0 {
		t.Errorf("Total = %v, want 0 when no term was measured", got.Total)
	}
	if got.Known() != 0 {
		t.Errorf("Known = %d, want 0", got.Known())
	}
	for _, term := range got.Terms {
		if term.Contribution() != 0 {
			t.Errorf("term %q contributed %v with nothing measured", term.Name, term.Contribution())
		}
	}
}

func TestStaleness(t *testing.T) {
	const now = 40 * year

	cases := []struct {
		name  string
		last  int64
		want  float64
		known bool
	}{
		{"never observed", 0, 0, false},
		{"published today", now, 0, true},
		{"idle one year", now - year, 0.2, true},
		{"idle five years, at the cap", now - 5*year, 1, true},
		// Past the cap the term saturates. Letting it keep growing would let age alone dominate a
		// ranking that is supposed to be about reach.
		{"idle twenty years", now - 20*year, 1, true},
		// A publish stamped after the instant being asked about is not negative staleness. This
		// happens on a point-in-time audit, where the account went on publishing after the date in
		// the query.
		{"published after the query instant", now + 2*year, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := staleness(Signals{LastPublish: tc.last}, DefaultWeights, now)
			if got.Known != tc.known {
				t.Fatalf("Known = %v, want %v", got.Known, tc.known)
			}
			if math.Abs(got.Value-tc.want) > 1e-9 {
				t.Errorf("Value = %.6f, want %.6f", got.Value, tc.want)
			}
		})
	}
}

// TestReachIsLogScaled pins the shape of the reach term rather than its exact values: going from one
// package to two says far more about an account than going from forty to forty-one, and a linear term
// would rank a long tail of single-package accounts as harmless.
func TestReachIsLogScaled(t *testing.T) {
	const (
		now   = 40 * year
		total = 100
	)

	value := func(n int) float64 {
		got := scoreOf(holder("h", n), Signals{}, DefaultWeights, total, now)
		for _, term := range got.Terms {
			if term.Name == "reach" {
				return term.Value
			}
		}
		t.Fatalf("no reach term in %v", got.Terms)
		return 0
	}

	first := value(2) - value(1)
	later := value(41) - value(40)
	if first <= later {
		t.Errorf("one to two packages moved the term by %.4f, forty to forty-one by %.4f: "+
			"the first step must count for more", first, later)
	}
	if v := value(0); v != 0 {
		t.Errorf("reach term for no packages = %v, want 0", v)
	}
	// An account holding everything the audit reached is the worst case, and the term is a fraction
	// of the worst case rather than an open-ended number.
	if v := value(total); math.Abs(v-1) > 1e-9 {
		t.Errorf("reach term for all %d packages = %.6f, want 1", total, v)
	}
}

func TestRankedOrdersByRiskThenHandle(t *testing.T) {
	const now = 40 * year

	// Same signals throughout, so reach alone separates them and the two single-package accounts tie.
	keyholders := []Keyholder{holder("zoe", 1), holder("wide", 8), holder("amy", 1)}
	signals := map[string]Signals{}
	for _, k := range keyholders {
		signals[k.Handle] = clean(3, now)
	}

	ranked, scores := Ranked(keyholders, signals, DefaultWeights, 10, now)

	want := []string{"wide", "amy", "zoe"}
	for i, handle := range want {
		if ranked[i].Handle != handle {
			t.Fatalf("ranked %d is %q, want %q (order: %v)", i, ranked[i].Handle, handle, handles(ranked))
		}
	}
	if len(scores) != len(keyholders) {
		t.Errorf("scored %d accounts, want %d", len(scores), len(keyholders))
	}
	if scores["amy"].Total != scores["zoe"].Total {
		t.Errorf("identical accounts scored %.6f and %.6f", scores["amy"].Total, scores["zoe"].Total)
	}
}

// TestRankedDoesNotReorderInPlace matters because the audit keeps the reach-ordered roster and hands
// the same slice to the ranker. Sorting the caller's slice would silently change the other order.
func TestRankedDoesNotReorderInPlace(t *testing.T) {
	keyholders := []Keyholder{holder("wide", 8), holder("narrow", 1)}
	before := handles(keyholders)

	Ranked(keyholders, map[string]Signals{}, DefaultWeights, 10, 40*year)

	after := handles(keyholders)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("Ranked reordered the caller's slice: %v became %v", before, after)
		}
	}
}

func handles(ks []Keyholder) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.Handle)
	}
	return out
}
