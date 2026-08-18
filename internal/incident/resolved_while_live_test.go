package incident

import (
	"context"
	"testing"
)

type liveTable struct {
	windows map[string][]ResolutionWindow // "name@version" of the compromised version -> windows
	locks   map[string][]LockedAt         // "name@version" of the dependent -> who locked it, and when
}

func (l *liveTable) ResolversInto(_ context.Context, name, version string) ([]ResolutionWindow, error) {
	return l.windows[name+"@"+version], nil
}

func (l *liveTable) ProjectsLocking(_ context.Context, name, version string) ([]LockedAt, error) {
	return l.locks[name+"@"+version], nil
}

func TestResolvedWhileLiveFlagsAProjectLockedInsideTheWindow(t *testing.T) {
	tab := &liveTable{
		windows: map[string][]ResolutionWindow{
			"bad@1.2.3": {{DependentName: "app-lib", DependentVersion: "2.0.0", ValidFrom: 100, ValidTo: 200}},
		},
		locks: map[string][]LockedAt{
			"app-lib@2.0.0": {{Project: "victim", At: 150}},
		},
	}

	got, err := ResolvedWhileLive(context.Background(), tab, "bad", "1.2.3")
	if err != nil {
		t.Fatalf("ResolvedWhileLive: %v", err)
	}
	if len(got) != 1 || got[0].Project != "victim" {
		t.Fatalf("got %v, want one exposure for victim", got)
	}
	if got[0].Dependent != "app-lib@2.0.0" {
		t.Errorf("Dependent = %q, want app-lib@2.0.0", got[0].Dependent)
	}
}

func TestResolvedWhileLiveExcludesAProjectLockedBeforeTheWindowOpened(t *testing.T) {
	tab := &liveTable{
		windows: map[string][]ResolutionWindow{
			"bad@1.2.3": {{DependentName: "app-lib", DependentVersion: "2.0.0", ValidFrom: 100, ValidTo: 200}},
		},
		locks: map[string][]LockedAt{
			"app-lib@2.0.0": {{Project: "safe", At: 50}},
		},
	}
	got, err := ResolvedWhileLive(context.Background(), tab, "bad", "1.2.3")
	if err != nil {
		t.Fatalf("ResolvedWhileLive: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none: locked before the resolution existed", got)
	}
}

func TestResolvedWhileLiveExcludesAProjectLockedAfterTheWindowClosed(t *testing.T) {
	tab := &liveTable{
		windows: map[string][]ResolutionWindow{
			"bad@1.2.3": {{DependentName: "app-lib", DependentVersion: "2.0.0", ValidFrom: 100, ValidTo: 200}},
		},
		locks: map[string][]LockedAt{
			// The dependent was later upgraded past the compromised range, so a lockfile written
			// after the fix landed must not be reported as exposed.
			"app-lib@2.0.0": {{Project: "upgraded", At: 200}},
		},
	}
	got, err := ResolvedWhileLive(context.Background(), tab, "bad", "1.2.3")
	if err != nil {
		t.Fatalf("ResolvedWhileLive: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none: valid_to is exclusive and the lock lands exactly on it", got)
	}
}

func TestResolvedWhileLiveCoversEveryDependentThatEverResolvedIntoIt(t *testing.T) {
	tab := &liveTable{
		windows: map[string][]ResolutionWindow{
			"bad@1.2.3": {
				{DependentName: "app-lib", DependentVersion: "2.0.0", ValidFrom: 100, ValidTo: 200},
				{DependentName: "other-lib", DependentVersion: "5.0.0", ValidFrom: 300, ValidTo: 400},
			},
		},
		locks: map[string][]LockedAt{
			"app-lib@2.0.0":   {{Project: "victim-one", At: 150}},
			"other-lib@5.0.0": {{Project: "victim-two", At: 350}},
		},
	}
	got, err := ResolvedWhileLive(context.Background(), tab, "bad", "1.2.3")
	if err != nil {
		t.Fatalf("ResolvedWhileLive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want exposures through both dependents", got)
	}
}
