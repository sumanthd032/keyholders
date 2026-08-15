package query

import "testing"

func TestSetInsertKeepsIntervalsMaximal(t *testing.T) {
	cases := []struct {
		name  string
		start Set
		add   Interval
		want  Set
		added bool
	}{
		{"into an empty set", nil, span(1, 5), Set{span(1, 5)}, true},
		{"disjoint stays separate", Set{span(1, 5)}, span(8, 10), Set{span(1, 5), span(8, 10)}, true},
		{"overlapping coalesces", Set{span(1, 5)}, span(3, 8), Set{span(1, 8)}, true},
		{"touching coalesces", Set{span(1, 5)}, span(5, 8), Set{span(1, 8)}, true},
		{"already covered adds nothing", Set{span(1, 10)}, span(3, 7), Set{span(1, 10)}, false},
		{"identical adds nothing", Set{span(1, 5)}, span(1, 5), Set{span(1, 5)}, false},
		{"empty interval adds nothing", Set{span(1, 5)}, span(7, 7), Set{span(1, 5)}, false},
		{"bridging merges three into one", Set{span(1, 3), span(7, 9)}, span(2, 8), Set{span(1, 9)}, true},
		{"extends to the left", Set{span(5, 9)}, span(1, 6), Set{span(1, 9)}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, added := tc.start.Insert(tc.add)
			if added != tc.added {
				t.Errorf("added = %v, want %v", added, tc.added)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestSetCovers(t *testing.T) {
	cases := []struct {
		name string
		set  Set
		ask  Interval
		want bool
	}{
		{"empty set covers nothing", nil, span(1, 2), false},
		{"exact match", Set{span(1, 5)}, span(1, 5), true},
		{"strictly inside", Set{span(1, 10)}, span(3, 7), true},
		{"overhanging right", Set{span(1, 5)}, span(3, 8), false},
		{"overhanging left", Set{span(5, 10)}, span(1, 7), false},
		{"entirely outside", Set{span(1, 5)}, span(8, 10), false},
		// The set is kept maximal, so two members are never adjacent. A query spanning the gap
		// between them is genuinely not covered, and treating it as covered would drop a real path.
		{"spanning a gap between members", Set{span(1, 3), span(7, 9)}, span(2, 8), false},
		{"inside the second member", Set{span(1, 3), span(7, 9)}, span(7, 8), true},
		{"an empty interval is trivially covered", Set{span(1, 3)}, span(5, 5), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.Covers(tc.ask); got != tc.want {
				t.Errorf("Covers(%v) over %v = %v, want %v", tc.ask, tc.set, got, tc.want)
			}
		})
	}
}

func TestIntersect(t *testing.T) {
	cases := []struct {
		name  string
		a, b  Interval
		want  Interval
		empty bool
	}{
		{"overlapping", span(1, 8), span(5, 12), span(5, 8), false},
		{"nested", span(1, 20), span(5, 8), span(5, 8), false},
		{"disjoint", span(1, 5), span(8, 12), Interval{}, true},
		// Half-open, so a span ending where the next begins shares no instant.
		{"touching shares no instant", span(1, 5), span(5, 9), Interval{}, true},
		{"with Always is the identity", span(3, 9), Always, span(3, 9), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.a.Intersect(tc.b)
			if got.Empty() != tc.empty {
				t.Fatalf("Intersect(%v, %v) = %v, empty=%v, want empty=%v", tc.a, tc.b, got, got.Empty(), tc.empty)
			}
			if !tc.empty && got != tc.want {
				t.Errorf("Intersect(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
