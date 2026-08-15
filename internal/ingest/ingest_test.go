package ingest

import (
	"testing"
	"time"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/registry"
)

func ts(year int, month time.Month, day int) int64 {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Unix()
}

func TestSpells(t *testing.T) {
	cases := []struct {
		name string
		obs  []observation
		want map[string][]spell
	}{
		{
			name: "one account throughout",
			obs: []observation{
				{at: ts(2020, time.January, 1), maintainers: []string{"alice"}},
				{at: ts(2021, time.January, 1), maintainers: []string{"alice"}},
			},
			want: map[string][]spell{
				"alice": {{from: ts(2020, time.January, 1), to: graph.OpenInterval, observed: 2}},
			},
		},
		{
			// The case the whole interval model exists for. A maintainer who left in 2019 was never
			// a keyholder for a 2023 install, and reporting them as one is the error the current
			// maintainer set makes.
			name: "account leaves",
			obs: []observation{
				{at: ts(2018, time.January, 1), maintainers: []string{"alice", "bob"}},
				{at: ts(2019, time.January, 1), maintainers: []string{"alice", "bob"}},
				{at: ts(2020, time.January, 1), maintainers: []string{"alice"}},
			},
			want: map[string][]spell{
				"alice": {{from: ts(2018, time.January, 1), to: graph.OpenInterval, observed: 3}},
				"bob":   {{from: ts(2018, time.January, 1), to: ts(2020, time.January, 1), observed: 2}},
			},
		},
		{
			// Two disjoint spells must stay disjoint. Collapsing them to one edge would claim bob
			// held a key through 2020 and 2021, when the evidence says he did not.
			name: "account leaves and returns",
			obs: []observation{
				{at: ts(2018, time.January, 1), maintainers: []string{"alice", "bob"}},
				{at: ts(2020, time.January, 1), maintainers: []string{"alice"}},
				{at: ts(2022, time.January, 1), maintainers: []string{"alice", "bob"}},
			},
			want: map[string][]spell{
				"alice": {{from: ts(2018, time.January, 1), to: graph.OpenInterval, observed: 3}},
				"bob": {
					{from: ts(2018, time.January, 1), to: ts(2020, time.January, 1), observed: 1},
					{from: ts(2022, time.January, 1), to: graph.OpenInterval, observed: 1},
				},
			},
		},
		{
			name: "observations arrive out of order",
			obs: []observation{
				{at: ts(2021, time.January, 1), maintainers: []string{"alice"}},
				{at: ts(2019, time.January, 1), maintainers: []string{"bob"}},
			},
			want: map[string][]spell{
				"alice": {{from: ts(2021, time.January, 1), to: graph.OpenInterval, observed: 1}},
				"bob":   {{from: ts(2019, time.January, 1), to: ts(2021, time.January, 1), observed: 1}},
			},
		},
		{name: "no observations", obs: nil, want: map[string][]spell{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := spells(tc.obs)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d accounts %v, want %d", len(got), got, len(tc.want))
			}
			for handle, wantSpells := range tc.want {
				gotSpells := got[handle]
				if len(gotSpells) != len(wantSpells) {
					t.Fatalf("%s: got %d spells %v, want %d", handle, len(gotSpells), gotSpells, len(wantSpells))
				}
				for i, w := range wantSpells {
					if gotSpells[i] != w {
						t.Errorf("%s spell %d: got %+v, want %+v", handle, i, gotSpells[i], w)
					}
				}
			}
		})
	}
}

// TestSpellDiscriminatorSeparatesEdges pins the reason spells carry a discriminator: two spells of
// the same account on the same package must produce two distinct relationship ids, or the second
// MERGE overwrites the first and the gap disappears.
func TestSpellDiscriminatorSeparatesEdges(t *testing.T) {
	var r rows
	first := spell{from: ts(2018, time.January, 1), to: ts(2020, time.January, 1), observed: 1}
	second := spell{from: ts(2022, time.January, 1), to: graph.OpenInterval, observed: 1}

	r.addMaintains("bob", "express", first)
	r.addMaintains("bob", "express", second)

	if len(r.maintains) != 2 {
		t.Fatalf("got %d maintains rows, want 2", len(r.maintains))
	}
	if r.maintains[0]["id"] == r.maintains[1]["id"] {
		t.Fatal("two spells produced the same relationship id, so one would overwrite the other")
	}
}

func TestVersionAt(t *testing.T) {
	rel := func(v string, y int, m time.Month, d int) registry.Release {
		return registry.Release{Version: v, PublishedAt: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
	}
	releases := []registry.Release{
		rel("1.0.0", 2019, time.March, 1),
		rel("2.0.0", 2021, time.June, 1),
		rel("3.0.0-beta.1", 2022, time.January, 1),
		rel("2.1.0", 2022, time.May, 1),
		{Version: "9.9.9"}, // no publish time, so it cannot be placed
	}

	cases := []struct {
		name  string
		at    time.Time
		want  string
		found bool
	}{
		{"before anything was published", time.Date(2018, time.January, 1, 0, 0, 0, 0, time.UTC), "", false},
		{"between releases", time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC), "1.0.0", true},
		{"exactly on a publish", time.Date(2021, time.June, 1, 0, 0, 0, 0, time.UTC), "2.0.0", true},
		// npm does not install a prerelease unless a range asks for it, so 3.0.0-beta.1 must not be
		// reported as the version current in early 2022.
		{"prerelease is not current", time.Date(2022, time.March, 1, 0, 0, 0, 0, time.UTC), "2.0.0", true},
		{"after everything", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), "2.1.0", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := versionAt(releases, tc.at)
			if ok != tc.found {
				t.Fatalf("found = %v, want %v", ok, tc.found)
			}
			if ok && got.Version != tc.want {
				t.Errorf("got %s, want %s", got.Version, tc.want)
			}
		})
	}
}

func TestEpochs(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	got := Epochs(4, now)

	want := []time.Time{
		time.Date(2025, time.October, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d epochs, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("epoch %d is %v, want %v", i, got[i], want[i])
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"1.2.3", false},
		{"1.2.3-beta.1", true},
		{"1.2.3-0", true},
		// Build metadata is not a prerelease, and the hyphen inside it must not be mistaken for one.
		{"1.2.3+build-7", false},
		{"1.2.3-rc.1+build", true},
	}
	for _, tc := range cases {
		if got := isPrerelease(tc.version); got != tc.want {
			t.Errorf("isPrerelease(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}
