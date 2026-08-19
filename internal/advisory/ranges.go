package advisory

import (
	"sort"

	"github.com/sumanthd032/keyholders/internal/semver"
)

// boundary is one Range event reduced to a comparable version and what it does to affected status.
// "0" is OSV's sentinel for "from the beginning", which sorts before every real version without
// needing a special case in the walk below: zero-value semver.Version is already less than anything
// with a positive major, minor, or patch component.
type boundary struct {
	version semver.Version
	kind    eventKind
}

type eventKind uint8

const (
	introduced eventKind = iota
	fixed
	lastAffected
)

// affectedVersions returns every release whose version falls inside one Range, evaluated against the
// project's own semver comparator regardless of whether the range is typed SEMVER or ECOSYSTEM: real
// npm advisory data uses ordinary semver strings under both labels. A GIT range, keyed on commit
// hashes rather than versions, has nothing here to compare against and returns no versions.
//
// OSV's events describe transitions in increasing version order: introduced flips a package
// affected, fixed flips it back, and last_affected caps how far introduced reaches when no fixed
// version is known yet. A release's status is whatever the last transition at or before it left
// behind, which is exactly a sorted walk, applying introduced and fixed as they are reached and
// last_affected as a ceiling that revokes affected status past that point.
func affectedVersions(r Range, releases []Release) []Release {
	if r.Type == "GIT" {
		return nil
	}

	bounds := make([]boundary, 0, len(r.Events))
	for _, e := range r.Events {
		switch {
		case e.Introduced != "":
			v, ok := parseBoundary(e.Introduced)
			if !ok {
				continue
			}
			bounds = append(bounds, boundary{version: v, kind: introduced})
		case e.Fixed != "":
			v, err := semver.Parse(e.Fixed)
			if err != nil {
				continue
			}
			bounds = append(bounds, boundary{version: v, kind: fixed})
		case e.LastAffected != "":
			v, err := semver.Parse(e.LastAffected)
			if err != nil {
				continue
			}
			bounds = append(bounds, boundary{version: v, kind: lastAffected})
		}
	}
	if len(bounds) == 0 {
		return nil
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i].version.Compare(bounds[j].version) < 0 })

	var out []Release
	for _, rel := range releases {
		v, err := semver.Parse(rel.Version)
		if err != nil {
			continue
		}
		if isAffected(v, bounds) {
			out = append(out, rel)
		}
	}
	return out
}

// parseBoundary handles OSV's "0" sentinel for introduced, meaning affected from the first published
// version onward, alongside an ordinary version string.
func parseBoundary(s string) (semver.Version, bool) {
	if s == "0" {
		return semver.Version{}, true
	}
	v, err := semver.Parse(s)
	if err != nil {
		return semver.Version{}, false
	}
	return v, true
}

func isAffected(v semver.Version, bounds []boundary) bool {
	affected := false
	for _, b := range bounds {
		if v.Compare(b.version) < 0 {
			break
		}
		switch b.kind {
		case introduced:
			affected = true
		case fixed:
			affected = false
		case lastAffected:
			if v.Compare(b.version) > 0 {
				affected = false
			}
		}
	}
	return affected
}
