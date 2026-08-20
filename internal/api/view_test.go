package api

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sumanthd032/keyholders/internal/query"
)

// A view field declared as a JSON array must never serialize as null. The clients index these
// directly (report.exposed.length, view.chains[0]), so a null is not a tolerable empty value there,
// it is a thrown TypeError that takes the whole render down before any emptiness check runs.
func TestViewsNeverMarshalNullArrays(t *testing.T) {
	tests := []struct {
		name string
		view any
	}{
		{"who, handle holds nothing", newWhoView(query.Who{Handle: "nobody"})},
		{"who, nil dependents with holds", newWhoView(query.Who{
			Handle: "somebody",
			Holds:  []query.Held{{Package: "pkg:npm/left-pad", Versions: 3}},
		})},
		{"path, handle not in audit", query.NewPathView("nobody", nil, nil)},
		{"path, held but unproven", query.NewPathView("somebody", []string{"pkg:npm/left-pad"}, nil)},
	}
	// handleIncident assembles its view inline against a concrete graph source, so there is no seam
	// to build one here without a live HydraDB. Its six arrays are covered by orEmpty below and by
	// hitting the endpoint on a package nothing depends on, which is what found this in the first
	// place.

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.view)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if bytes.Contains(b, []byte(":null")) {
				t.Errorf("a declared array marshalled as null:\n%s", b)
			}
		})
	}
}

func TestOrEmpty(t *testing.T) {
	if got := orEmpty[string](nil); got == nil {
		t.Error("nil slice must become an empty slice, not stay nil")
	}
	held := []string{"a", "b"}
	got := orEmpty(held)
	if len(got) != 2 || got[0] != "a" {
		t.Errorf("a populated slice must pass through unchanged, got %v", got)
	}
}
