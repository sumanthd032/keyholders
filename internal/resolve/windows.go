// Package resolve materializes RESOLVES_TO edges: what each declared dependency range actually
// resolved to, and for which span of time that answer held.
//
// This is the edge the rest of the project reads. Semver ranges cannot be evaluated inside Cypher,
// which has no IN and only STARTS WITH for strings, so resolution is computed once here and written
// as concrete version-to-version edges carrying validity windows. That turns a query nobody can
// express into pure traversal, which is what the engine is fast at.
package resolve

import (
	"sort"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/semver"
)

// Release is one published version of the package a range points at.
type Release struct {
	Version     string
	PublishedAt int64
}

// Window is one span during which a range resolved to a single stable version.
type Window struct {
	Version string
	From    int64
	To      int64
}

// Windows computes the spans during which r resolved to each version of the target.
//
// Resolution is a step function of time: an install at instant t takes the highest version
// satisfying r among those published at or before t, so the answer changes exactly at the publishes
// that raise that maximum. Each step becomes one edge.
//
// declaredAt clamps the result to the moment the dependent version itself was published. A window
// that closed before the dependent existed describes an install nobody could have performed, and
// the window containing that instant starts there rather than earlier.
//
// The answer only ever moves upward, because the set of versions published so far only grows and
// adding to a set cannot lower its maximum. Each resolved version therefore owns exactly one
// contiguous window, which is why a RESOLVES_TO edge needs no discriminator to stay unique.
func Windows(releases []Release, r semver.Range, declaredAt int64) []Window {
	type candidate struct {
		version   semver.Version
		raw       string
		published int64
	}

	admitted := make([]candidate, 0, len(releases))
	for _, rel := range releases {
		// A version with no publish time cannot be placed on the timeline, and a version the
		// registry accepted but semver cannot parse is not something npm would resolve to either.
		if rel.PublishedAt <= 0 {
			continue
		}
		v, err := semver.Parse(rel.Version)
		if err != nil || !r.Satisfies(v) {
			continue
		}
		admitted = append(admitted, candidate{version: v, raw: rel.Version, published: rel.PublishedAt})
	}
	if len(admitted) == 0 {
		return nil
	}

	// Publish order decides when the answer changes. Two versions can share a publish second, in
	// which case the higher precedence one is the answer for that instant, so it must sort last.
	sort.Slice(admitted, func(i, j int) bool {
		if admitted[i].published != admitted[j].published {
			return admitted[i].published < admitted[j].published
		}
		return admitted[i].version.Compare(admitted[j].version) < 0
	})

	var windows []Window
	best := admitted[0]
	windows = append(windows, Window{Version: best.raw, From: best.published, To: graph.OpenInterval})

	for _, c := range admitted[1:] {
		if c.version.Compare(best.version) <= 0 {
			// A backport or a republish below the current maximum. It changes nothing about what an
			// install would pick, so it is not a step.
			continue
		}
		windows[len(windows)-1].To = c.published
		windows = append(windows, Window{Version: c.raw, From: c.published, To: graph.OpenInterval})
		best = c
	}
	return clamp(windows, declaredAt)
}

// clamp drops windows that closed before the dependent was published, truncates the one that spans
// that instant, and discards any window covering no time at all.
//
// Empty windows are produced by two versions sharing a publish second: the lower precedence one is
// opened and closed at the same instant, and no install could ever have seen it. Dropping them
// preserves contiguity, because the window it collapses into starts exactly where it ended.
func clamp(windows []Window, declaredAt int64) []Window {
	out := windows[:0]
	for _, w := range windows {
		if declaredAt > 0 {
			if w.To <= declaredAt {
				continue
			}
			if w.From < declaredAt {
				w.From = declaredAt
			}
		}
		if w.From >= w.To {
			continue
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
