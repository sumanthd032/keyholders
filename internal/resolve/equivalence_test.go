package resolve

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/semver"
)

// pointResolve is the definition the whole project is built on: an install at instant t takes the
// highest version satisfying the range among those published at or before t. Step 2 measured this
// against 32,571 triples from real dated lockfiles and found 89.6% agreement, with the residual
// almost entirely lockfiles that had gone stale rather than resolution error.
//
// Windows is an optimisation of exactly this function: instead of answering one instant, it returns
// the spans over which the answer is constant. If the two ever disagree, the windows are wrong and
// step 2's measured accuracy does not carry over to anything the graph reports.
func pointResolve(releases []Release, r semver.Range, at int64) (string, bool) {
	var best semver.Version
	bestRaw := ""
	found := false
	for _, rel := range releases {
		if rel.PublishedAt <= 0 || rel.PublishedAt > at {
			continue
		}
		v, err := semver.Parse(rel.Version)
		if err != nil || !r.Satisfies(v) {
			continue
		}
		if !found || v.Compare(best) > 0 {
			best, bestRaw, found = v, rel.Version, true
		}
	}
	return bestRaw, found
}

// TestWindowsAgreeWithPointResolution is the load-bearing test for step 4. It generates timelines
// and ranges, then checks every instant of interest against both implementations.
func TestWindowsAgreeWithPointResolution(t *testing.T) {
	ranges := []string{
		"^1.0.0", "~1.2.0", ">=1.0.0 <2.0.0", "1.x", "*", "1.2.3",
		"^0.1.0", ">=1.5.0", "1.0.0 - 2.0.0", "^1.0.0 || ^3.0.0", "~0.0.1",
	}
	versions := []string{
		"0.9.0", "1.0.0", "1.0.1", "1.1.0", "1.1.1-beta.1", "1.1.1", "1.2.0",
		"1.2.3", "1.5.0", "1.9.9", "2.0.0-rc.1", "2.0.0", "2.1.0", "3.0.0", "0.1.5",
	}

	rng := rand.New(rand.NewSource(20260815))

	for _, rangeText := range ranges {
		r, err := semver.ParseRange(rangeText)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", rangeText, err)
		}

		for trial := range 200 {
			// A timeline in a deliberately arbitrary publish order, because real registries publish
			// backports and republishes out of precedence order and that is where the step function
			// is easiest to get wrong.
			perm := rng.Perm(len(versions))
			releases := make([]Release, 0, len(versions))
			for i, p := range perm {
				releases = append(releases, Release{
					Version:     versions[p],
					PublishedAt: int64(1000 + i*100),
				})
			}

			declaredAt := int64(0)
			if trial%3 == 1 {
				declaredAt = int64(1000 + rng.Intn(len(versions))*100)
			}

			windows := Windows(releases, r, declaredAt)
			if err := checkAgreement(releases, windows, r, declaredAt); err != nil {
				t.Fatalf("range %q trial %d: %v\nreleases %+v\nwindows %+v",
					rangeText, trial, err, releases, windows)
			}
		}
	}
}

// checkAgreement compares the two implementations at every instant where either could change: each
// publish time, the instants either side of it, and every window boundary.
func checkAgreement(releases []Release, windows []Window, r semver.Range, declaredAt int64) error {
	instants := map[int64]bool{}
	for _, rel := range releases {
		for _, d := range []int64{-1, 0, 1} {
			if rel.PublishedAt+d > 0 {
				instants[rel.PublishedAt+d] = true
			}
		}
	}
	for _, w := range windows {
		instants[w.From] = true
		if w.To != graph.OpenInterval {
			instants[w.To] = true
			instants[w.To-1] = true
		}
	}

	for at := range instants {
		if at < declaredAt {
			// Before the dependent existed there is nothing to agree about: no install could have
			// happened, which is exactly why those windows are clamped away.
			continue
		}

		wantVersion, wantFound := pointResolve(releases, r, at)
		gotVersion, gotFound := windowAt(windows, at)

		if wantFound != gotFound {
			return fmt.Errorf("at %d: point resolution found=%v (%q), windows found=%v (%q)",
				at, wantFound, wantVersion, gotFound, gotVersion)
		}
		if wantFound && wantVersion != gotVersion {
			return fmt.Errorf("at %d: point resolution says %q, windows say %q", at, wantVersion, gotVersion)
		}
	}
	return nil
}

// windowAt is what a reader of the graph does: find the window covering an instant. Half-open, so
// the instant a window closes belongs to the next one and no instant is covered twice.
func windowAt(windows []Window, at int64) (string, bool) {
	for _, w := range windows {
		if at >= w.From && at < w.To {
			return w.Version, true
		}
	}
	return "", false
}
