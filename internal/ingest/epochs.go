package ingest

import (
	"time"

	"github.com/sumanthd032/keyholders/internal/registry"
)

// Epochs returns n quarterly boundaries ending at the most recent completed quarter, oldest first.
//
// Quarterly is a compromise between resolution and cost. Every extra epoch costs one npm document
// and one deps.dev closure per package, so the epoch count multiplies the request budget directly,
// and at 30 requests/s that is the binding constraint on how many packages fit in a run.
func Epochs(n int, now time.Time) []time.Time {
	now = now.UTC()
	// Start of the quarter containing now, so the newest epoch is a boundary that has passed.
	q := time.Date(now.Year(), month(quarter(now)), 1, 0, 0, 0, 0, time.UTC)

	out := make([]time.Time, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, q.AddDate(0, -3*i, 0))
	}
	return out
}

func quarter(t time.Time) int { return (int(t.Month()) - 1) / 3 }

func month(q int) time.Month { return time.Month(q*3 + 1) }

// versionAt returns the release that was current at instant t: the newest version published at or
// before t. This is the canonical reconstruction the rest of the project uses, measured against
// real dated lockfiles at 89.6% agreement, with the residual almost entirely lockfiles that had
// gone stale rather than resolution error.
//
// Prereleases are excluded because npm does not install one unless a range explicitly asks for it,
// so treating a prerelease as the current version would misreport what an install produced.
func versionAt(releases []registry.Release, t time.Time) (registry.Release, bool) {
	var best registry.Release
	found := false
	for _, r := range releases {
		if r.PublishedAt.IsZero() || r.PublishedAt.After(t) || isPrerelease(r.Version) {
			continue
		}
		if !found || r.PublishedAt.After(best.PublishedAt) {
			best, found = r, true
		}
	}
	return best, found
}

// isPrerelease reports whether a version string carries a prerelease tag. Build metadata after `+`
// is not a prerelease, so the `-` has to be found before any `+`.
func isPrerelease(version string) bool {
	for i := range len(version) {
		switch version[i] {
		case '-':
			return true
		case '+':
			return false
		}
	}
	return false
}
