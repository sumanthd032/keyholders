package advisory

import (
	"testing"
)

func releases(versions ...string) []Release {
	out := make([]Release, len(versions))
	for i, v := range versions {
		out[i] = Release{Version: v}
	}
	return out
}

func versionsOf(rels []Release) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = r.Version
	}
	return out
}

func TestAffectedVersions(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		rels   []Release
		want   []string
	}{
		{
			name:   "introduced at zero with a fix covers everything before the fix",
			events: []Event{{Introduced: "0"}, {Fixed: "1.2.0"}},
			rels:   releases("1.0.0", "1.1.0", "1.2.0", "1.3.0"),
			want:   []string{"1.0.0", "1.1.0"},
		},
		{
			name:   "introduced at a specific version excludes everything before it",
			events: []Event{{Introduced: "1.1.0"}, {Fixed: "1.3.0"}},
			rels:   releases("1.0.0", "1.1.0", "1.2.0", "1.3.0"),
			want:   []string{"1.1.0", "1.2.0"},
		},
		{
			name:   "no fix means every version from introduced onward is affected",
			events: []Event{{Introduced: "1.1.0"}},
			rels:   releases("1.0.0", "1.1.0", "1.2.0"),
			want:   []string{"1.1.0", "1.2.0"},
		},
		{
			name:   "last_affected caps the range without a known fix",
			events: []Event{{Introduced: "0"}, {LastAffected: "1.1.0"}},
			rels:   releases("1.0.0", "1.1.0", "1.2.0"),
			want:   []string{"1.0.0", "1.1.0"},
		},
		{
			name: "reintroduced after a fix, the multi-hop case",
			events: []Event{
				{Introduced: "0"}, {Fixed: "1.0.0"},
				{Introduced: "2.0.0"}, {Fixed: "2.1.0"},
			},
			rels: releases("0.9.0", "1.0.0", "1.5.0", "2.0.0", "2.1.0"),
			want: []string{"0.9.0", "2.0.0"},
		},
		{
			name:   "a version that never satisfies any window is excluded",
			events: []Event{{Introduced: "1.5.0"}, {Fixed: "1.6.0"}},
			rels:   releases("1.0.0", "2.0.0"),
			want:   nil,
		},
		{
			name:   "an unparseable release version is skipped rather than failing the match",
			events: []Event{{Introduced: "0"}},
			rels:   releases("not-a-version", "1.0.0"),
			want:   []string{"1.0.0"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := versionsOf(affectedVersions(Range{Type: "SEMVER", Events: c.events}, c.rels))
			if !equalStrings(got, c.want) {
				t.Errorf("affectedVersions() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAffectedVersionsIgnoresGitRanges(t *testing.T) {
	r := Range{Type: "GIT", Events: []Event{{Introduced: "0"}}}
	if got := affectedVersions(r, releases("1.0.0")); got != nil {
		t.Errorf("GIT range should match nothing here, got %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
