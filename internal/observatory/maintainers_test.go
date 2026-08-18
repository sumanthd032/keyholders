package observatory

import (
	"context"
	"testing"

	"github.com/sumanthd032/keyholders/internal/sketch"
)

func sketchOf(members ...string) sketch.Sketch {
	sk := sketch.NewExactSet()
	for _, m := range members {
		sk.Add(sketch.NodeID(m))
	}
	return sk
}

// holding is one account's tenure over a package, mirroring the validity window a MAINTAINS edge
// carries: [from, to), open ended tenures use a to far past anything a test asks about.
type holding struct {
	handle             string
	validFrom, validTo int64
}

type maintainerTable struct {
	holdings map[string][]holding // keyed by package name
}

func (t *maintainerTable) Maintainers(_ context.Context, name string, at int64) ([]string, error) {
	var out []string
	for _, h := range t.holdings[name] {
		if h.validFrom <= at && at < h.validTo {
			out = append(out, h.handle)
		}
	}
	return out, nil
}

// TestAggregateUnionsControlledPackages is the definition task 5 implements directly: a maintainer's
// reach is the union of every package they control, not a sum and not just one of them. "left-pad"
// and "chalk" share no members; an account controlling both must reach the union of both, and an
// account controlling only one must not pick up the other's members.
func TestAggregateUnionsControlledPackages(t *testing.T) {
	packages := map[sketch.NodeID]sketch.Sketch{
		"left-pad": sketchOf("left-pad", "some-cli"),
		"chalk":    sketchOf("chalk", "some-cli", "some-logger"),
	}
	table := &maintainerTable{holdings: map[string][]holding{
		"left-pad": {{handle: "azer", validFrom: 0, validTo: 1 << 40}},
		"chalk":    {{handle: "azer", validFrom: 0, validTo: 1 << 40}, {handle: "sindresorhus", validFrom: 0, validTo: 1 << 40}},
	}}

	agg := Aggregator{Src: table, New: sketch.NewExactSet}
	got, orphaned, err := agg.Aggregate(context.Background(), packages, 500)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(orphaned) != 0 {
		t.Errorf("both packages have a maintainer, want no orphans, got %v", orphaned)
	}

	// azer controls both packages: union of {left-pad, some-cli} and {chalk, some-cli, some-logger}.
	if want := 4; got[sketch.NodeID("azer")].Count() != want {
		t.Errorf("azer's reach = %d, want %d (left-pad, chalk, some-cli, some-logger)", got[sketch.NodeID("azer")].Count(), want)
	}
	// sindresorhus controls only chalk.
	if want := 3; got[sketch.NodeID("sindresorhus")].Count() != want {
		t.Errorf("sindresorhus's reach = %d, want %d (chalk, some-cli, some-logger)", got[sketch.NodeID("sindresorhus")].Count(), want)
	}
}

// TestAggregateRespectsHandoverWindow checks that an account which no longer holds a package at the
// queried epoch is not credited with its reach, the same rule D34 applies to a lockfile's pinned
// versions applied here to the observatory's per epoch snapshot.
func TestAggregateRespectsHandoverWindow(t *testing.T) {
	packages := map[sketch.NodeID]sketch.Sketch{
		"event-stream": sketchOf("event-stream", "downstream-app"),
	}
	table := &maintainerTable{holdings: map[string][]holding{
		// dominictarr held it early, handed it off at t=100; right9ctrl held it after.
		"event-stream": {
			{handle: "dominictarr", validFrom: 0, validTo: 100},
			{handle: "right9ctrl", validFrom: 100, validTo: 1 << 40},
		},
	}}

	agg := Aggregator{Src: table, New: sketch.NewExactSet}

	before, _, err := agg.Aggregate(context.Background(), packages, 50)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if _, ok := before[sketch.NodeID("right9ctrl")]; ok {
		t.Error("right9ctrl should not be credited before the handover")
	}
	if got := before[sketch.NodeID("dominictarr")].Count(); got != 2 {
		t.Errorf("dominictarr's reach before handover = %d, want 2", got)
	}

	after, _, err := agg.Aggregate(context.Background(), packages, 500)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if _, ok := after[sketch.NodeID("dominictarr")]; ok {
		t.Error("dominictarr should not be credited after handing the package off")
	}
	if got := after[sketch.NodeID("right9ctrl")].Count(); got != 2 {
		t.Errorf("right9ctrl's reach after handover = %d, want 2", got)
	}
}

// TestAggregateReportsOrphans checks the other half of what one pass over the package list buys:
// a package whose only maintainer's tenure already ended, and nobody has taken it over, must show up
// as orphaned rather than silently vanishing from the maintainer leaderboard with no trace of why.
func TestAggregateReportsOrphans(t *testing.T) {
	packages := map[sketch.NodeID]sketch.Sketch{
		"left-pad":  sketchOf("left-pad", "some-cli"),
		"abandoned": sketchOf("abandoned", "big-app", "bigger-app"),
	}
	table := &maintainerTable{holdings: map[string][]holding{
		"left-pad": {{handle: "azer", validFrom: 0, validTo: 1 << 40}},
		// abandoned had a maintainer once, but their tenure ended and nobody replaced them.
		"abandoned": {{handle: "someone", validFrom: 0, validTo: 100}},
	}}

	agg := Aggregator{Src: table, New: sketch.NewExactSet}
	maintainers, orphaned, err := agg.Aggregate(context.Background(), packages, 500)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(orphaned) != 1 || orphaned[0] != "abandoned" {
		t.Errorf("orphaned = %v, want [abandoned]", orphaned)
	}
	if _, ok := maintainers[sketch.NodeID("someone")]; ok {
		t.Error("someone's tenure ended before the queried instant and should not be credited")
	}
}
