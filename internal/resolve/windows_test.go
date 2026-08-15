package resolve

import (
	"testing"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/semver"
)

// Publish instants are small integers so the expected windows read as a sequence rather than as
// timestamps. Nothing in the computation depends on their scale.
const (
	t1 = 1000
	t2 = 2000
	t3 = 3000
	t4 = 4000
	t5 = 5000
)

func mustRange(t *testing.T, s string) semver.Range {
	t.Helper()
	r, err := semver.ParseRange(s)
	if err != nil {
		t.Fatalf("ParseRange(%q): %v", s, err)
	}
	return r
}

func TestWindows(t *testing.T) {
	cases := []struct {
		name       string
		releases   []Release
		rng        string
		declaredAt int64
		want       []Window
	}{
		{
			name:     "one satisfying version stays the answer",
			releases: []Release{{"1.0.0", t1}},
			rng:      "^1.0.0",
			want:     []Window{{"1.0.0", t1, graph.OpenInterval}},
		},
		{
			// The central case: each publish inside the range moves the answer, and each step is
			// one edge with a closed window behind it.
			name:     "each publish inside the range is a step",
			releases: []Release{{"1.0.0", t1}, {"1.1.0", t2}, {"1.2.0", t3}},
			rng:      "^1.0.0",
			want: []Window{
				{"1.0.0", t1, t2},
				{"1.1.0", t2, t3},
				{"1.2.0", t3, graph.OpenInterval},
			},
		},
		{
			// A major release outside the range must not close the window. This is the whole point
			// of a caret range, and getting it wrong would silently retarget every edge.
			name:     "a publish outside the range is not a step",
			releases: []Release{{"1.0.0", t1}, {"2.0.0", t2}, {"1.1.0", t3}},
			rng:      "^1.0.0",
			want: []Window{
				{"1.0.0", t1, t3},
				{"1.1.0", t3, graph.OpenInterval},
			},
		},
		{
			// A backport published after a higher version does not change what an install picks,
			// so it is not a boundary even though it is inside the range.
			name:     "a backport below the running maximum is not a step",
			releases: []Release{{"1.0.0", t1}, {"1.5.0", t2}, {"1.1.0", t3}},
			rng:      "^1.0.0",
			want: []Window{
				{"1.0.0", t1, t2},
				{"1.5.0", t2, graph.OpenInterval},
			},
		},
		{
			// npm does not install a prerelease unless the range names one at the same
			// major.minor.patch, so 2.0.0-beta.1 must not become the answer for ^1.0.0.
			name:     "prereleases are not admitted by a plain range",
			releases: []Release{{"1.0.0", t1}, {"1.1.0-beta.1", t2}, {"1.1.0", t3}},
			rng:      "^1.0.0",
			want: []Window{
				{"1.0.0", t1, t3},
				{"1.1.0", t3, graph.OpenInterval},
			},
		},
		{
			name:     "an exact range pins one version for all time",
			releases: []Release{{"1.0.0", t1}, {"1.1.0", t2}},
			rng:      "1.0.0",
			want:     []Window{{"1.0.0", t1, graph.OpenInterval}},
		},
		{
			name:     "nothing satisfies the range",
			releases: []Release{{"1.0.0", t1}, {"1.1.0", t2}},
			rng:      "^3.0.0",
			want:     nil,
		},
		{
			// Windows that closed before the dependent was published describe installs nobody could
			// have performed, and the window spanning that instant starts when the dependent did.
			name:       "windows are clamped to the dependent's publish time",
			releases:   []Release{{"1.0.0", t1}, {"1.1.0", t2}, {"1.2.0", t4}},
			rng:        "^1.0.0",
			declaredAt: t3,
			want: []Window{
				{"1.1.0", t3, t4},
				{"1.2.0", t4, graph.OpenInterval},
			},
		},
		{
			name:       "the dependent is newer than every release",
			releases:   []Release{{"1.0.0", t1}, {"1.1.0", t2}},
			rng:        "^1.0.0",
			declaredAt: t5,
			want:       []Window{{"1.1.0", t5, graph.OpenInterval}},
		},
		{
			name:     "versions with no publish time are dropped",
			releases: []Release{{"1.0.0", 0}, {"1.1.0", t2}},
			rng:      "^1.0.0",
			want:     []Window{{"1.1.0", t2, graph.OpenInterval}},
		},
		{
			// The registry holds versions node-semver cannot parse. They are skipped rather than
			// failing the package, because one bad version should not cost every edge to it.
			name:     "unparseable versions are skipped",
			releases: []Release{{"not-a-version", t1}, {"1.0.0", t2}},
			rng:      "*",
			want:     []Window{{"1.0.0", t2, graph.OpenInterval}},
		},
		{
			// Same publish second: the higher precedence version is what an install at that instant
			// picks, so it must win rather than depending on input order.
			name:     "a tie on publish time resolves by precedence",
			releases: []Release{{"1.2.0", t1}, {"1.1.0", t1}},
			rng:      "^1.0.0",
			want:     []Window{{"1.2.0", t1, graph.OpenInterval}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Windows(tc.releases, mustRange(t, tc.rng), tc.declaredAt)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d windows %+v, want %d %+v", len(got), got, len(tc.want), tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("window %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestWindowsAreContiguous pins the invariant the interval-constrained search in step 5 depends on:
// windows for one edge tile the timeline without gaps or overlaps, and exactly one is open-ended.
func TestWindowsAreContiguous(t *testing.T) {
	releases := []Release{
		{"1.0.0", t1}, {"2.0.0", t2}, {"1.1.0", t3}, {"1.1.1", t4}, {"1.2.0", t5},
	}
	got := Windows(releases, mustRange(t, "^1.0.0"), 0)
	if len(got) < 3 {
		t.Fatalf("expected several windows, got %+v", got)
	}

	for i, w := range got {
		if w.From >= w.To {
			t.Errorf("window %d is empty or inverted: %+v", i, w)
		}
		if i > 0 && got[i-1].To != w.From {
			t.Errorf("gap or overlap between %+v and %+v", got[i-1], w)
		}
	}
	if last := got[len(got)-1]; last.To != graph.OpenInterval {
		t.Errorf("the final window must stay open, got %d", last.To)
	}
	for i, w := range got[:len(got)-1] {
		if w.To == graph.OpenInterval {
			t.Errorf("window %d is open-ended but is not the last: %+v", i, w)
		}
	}
}
