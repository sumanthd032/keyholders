package incident

import (
	"context"
	"testing"
)

// bisectTable is a BisectSource backed by literal timelines, affected sets, and dependency lists.
type bisectTable struct {
	timeline map[string][]Release
	affected map[string]map[string]bool // advisoryID -> version -> affected
	deps     map[string][]Dependency    // "name@version" -> dependencies
}

func (b *bisectTable) Timeline(_ context.Context, name string) ([]Release, error) {
	return b.timeline[name], nil
}

func (b *bisectTable) AffectedVersions(_ context.Context, advisoryID, _ string) (map[string]bool, error) {
	return b.affected[advisoryID], nil
}

func (b *bisectTable) Dependencies(_ context.Context, name, version string) ([]Dependency, error) {
	return b.deps[name+"@"+version], nil
}

func TestBisectFindsEarliestAffectedVersionByPublishOrder(t *testing.T) {
	tab := &bisectTable{
		timeline: map[string][]Release{
			"lib": {
				{Version: "1.0.0", PublishedAt: 100},
				{Version: "1.1.0", PublishedAt: 200},
				{Version: "1.2.0", PublishedAt: 300},
			},
		},
		affected: map[string]map[string]bool{
			"GHSA-x": {"1.1.0": true, "1.2.0": true},
		},
	}

	got, err := Bisect(context.Background(), tab, "GHSA-x", "lib")
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if got.FirstAffected != "1.1.0" {
		t.Errorf("FirstAffected = %q, want 1.1.0", got.FirstAffected)
	}
	if got.Previous != "1.0.0" {
		t.Errorf("Previous = %q, want 1.0.0", got.Previous)
	}
}

func TestBisectHandlesTheFirstEverReleaseBeingAffected(t *testing.T) {
	tab := &bisectTable{
		timeline: map[string][]Release{"lib": {{Version: "1.0.0", PublishedAt: 100}}},
		affected: map[string]map[string]bool{"GHSA-x": {"1.0.0": true}},
	}

	got, err := Bisect(context.Background(), tab, "GHSA-x", "lib")
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if got.Previous != "" {
		t.Errorf("Previous = %q, want empty: there is nothing before the first release", got.Previous)
	}
	if len(got.Diff.Added) != 0 || len(got.Diff.Removed) != 0 || len(got.Diff.Changed) != 0 {
		t.Errorf("Diff = %+v, want empty with no previous version to diff against", got.Diff)
	}
}

func TestBisectSortsATimelineGivenOutOfOrder(t *testing.T) {
	// Registries do not guarantee HAS_VERSION comes back in publish order; Bisect must sort itself.
	tab := &bisectTable{
		timeline: map[string][]Release{
			"lib": {
				{Version: "2.0.0", PublishedAt: 300},
				{Version: "1.0.0", PublishedAt: 100},
				{Version: "1.5.0", PublishedAt: 200},
			},
		},
		affected: map[string]map[string]bool{"GHSA-x": {"1.5.0": true, "2.0.0": true}},
	}

	got, err := Bisect(context.Background(), tab, "GHSA-x", "lib")
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if got.FirstAffected != "1.5.0" {
		t.Errorf("FirstAffected = %q, want 1.5.0", got.FirstAffected)
	}
}

func TestBisectErrorsWhenAdvisoryAffectsNothingInTheTimeline(t *testing.T) {
	tab := &bisectTable{
		timeline: map[string][]Release{"lib": {{Version: "1.0.0", PublishedAt: 100}}},
		affected: map[string]map[string]bool{"GHSA-x": {"9.9.9": true}}, // not in this package's timeline
	}
	if _, err := Bisect(context.Background(), tab, "GHSA-x", "lib"); err == nil {
		t.Error("Bisect() should error when no timeline version is affected")
	}
}

func TestDependencyDiffReportsAddedRemovedAndChanged(t *testing.T) {
	tab := &bisectTable{
		timeline: map[string][]Release{
			"lib": {{Version: "1.0.0", PublishedAt: 100}, {Version: "2.0.0", PublishedAt: 200}},
		},
		affected: map[string]map[string]bool{"GHSA-x": {"2.0.0": true}},
		deps: map[string][]Dependency{
			"lib@1.0.0": {{Name: "left-pad", Range: "^1.0.0"}, {Name: "old-dep", Range: "^1.0.0"}},
			"lib@2.0.0": {{Name: "left-pad", Range: "^2.0.0"}, {Name: "new-dep", Range: "^1.0.0"}},
		},
	}

	got, err := Bisect(context.Background(), tab, "GHSA-x", "lib")
	if err != nil {
		t.Fatalf("Bisect: %v", err)
	}
	if len(got.Diff.Added) != 1 || got.Diff.Added[0].Name != "new-dep" {
		t.Errorf("Added = %v, want [new-dep]", got.Diff.Added)
	}
	if len(got.Diff.Removed) != 1 || got.Diff.Removed[0].Name != "old-dep" {
		t.Errorf("Removed = %v, want [old-dep]", got.Diff.Removed)
	}
	if len(got.Diff.Changed) != 1 || got.Diff.Changed[0] != (DependencyChange{Name: "left-pad", From: "^1.0.0", To: "^2.0.0"}) {
		t.Errorf("Changed = %v, want [{left-pad ^1.0.0 ^2.0.0}]", got.Diff.Changed)
	}
}
