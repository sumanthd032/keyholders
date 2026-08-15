package query

import (
	"context"
	"fmt"
)

// Edge is one traversable relationship with the span over which it was valid.
type Edge struct {
	From  string
	To    string
	Valid Interval
}

// Expander supplies the outgoing edges of a set of nodes. The search does not know or care whether
// they come from HydraDB or from a table in a test.
type Expander interface {
	Out(ctx context.Context, urns []string) ([]Edge, error)
}

// Frontier is one wavefront of the search, handed to the observer as it is discovered.
//
// The search reports frontiers as it finds them rather than returning only a finished result,
// because the interface draws traversal as it actually happens. A view that animates a completed
// answer is theatre; this is the real thing, and designing it in from the start is cheaper than
// retrofitting it around a batch API.
type Frontier struct {
	Depth   int
	Entries []Entry
}

// Entry is a node paired with the span over which it was reached by the path that got here.
type Entry struct {
	URN   string
	Valid Interval
}

// Result is what a search found.
type Result struct {
	// Coexistence holds, per node, the maximal spans over which it was genuinely reachable: every
	// edge on some path to it shared a common instant.
	Coexistence map[string]Set

	// Union holds every node reachable when intervals are ignored, which is what a tool without
	// interval semantics would report. The difference between the two is Phantom Reach.
	Union map[string]bool

	// Depth reached, and whether the search stopped because of the depth bound rather than because
	// it ran out of frontier. A truncated search undercounts, so callers must be able to say so.
	Depth     int
	Truncated bool

	// Edges is every edge the search read, keyed by source. It is retained so the union and
	// coexistence passes share one set of graph reads.
	Edges map[string][]Edge

	// Kappa is the average number of maximal intervals held per reached node. The complexity of the
	// search is O(E * kappa), and kappa being small is a hypothesis about npm rather than a
	// guarantee, so it is measured on every run and reported.
	Kappa float64
}

// Sources builds source entries that are treated as existing at every instant. Callers that know
// when a node came into existence should pass that span instead.
func Sources(urns ...string) []Entry {
	out := make([]Entry, 0, len(urns))
	for _, u := range urns {
		out = append(out, Entry{URN: u, Valid: Always})
	}
	return out
}

// Options configures a search.
type Options struct {
	// Within restricts the whole search to a span. Pass Always for "ever", or At(t) for one instant.
	Within Interval

	// MaxDepth bounds the traversal. Variable length paths must be bounded in this engine anyway,
	// and an unbounded dependency walk is not a thing anyone wants to wait for.
	MaxDepth int

	// OnFrontier, when set, receives each wavefront as it is discovered.
	OnFrontier func(Frontier)
}

const defaultMaxDepth = 12

// Reach computes coexistence-constrained reachability from the sources.
//
// It runs in two passes over one set of graph reads. The first pass walks the union graph, ignoring
// intervals, which visits a superset of what any interval-respecting walk could visit and caches
// every edge it reads. The second pass replays that cache with interval semantics. Doing it this way
// means the expensive half happens once, and the comparison between the two answers, which is the
// finding this project exists to report, costs nothing extra.
func Reach(ctx context.Context, exp Expander, sources []Entry, opts Options) (Result, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = defaultMaxDepth
	}
	if opts.Within == (Interval{}) {
		opts.Within = Always
	}

	res := Result{
		Coexistence: map[string]Set{},
		Union:       map[string]bool{},
		Edges:       map[string][]Edge{},
	}

	if err := unionPass(ctx, exp, sources, opts, &res); err != nil {
		return Result{}, err
	}
	coexistencePass(sources, opts, &res)

	var intervals int
	for _, set := range res.Coexistence {
		intervals += len(set)
	}
	if n := len(res.Coexistence); n > 0 {
		res.Kappa = float64(intervals) / float64(n)
	}
	return res, nil
}

// unionPass walks the graph ignoring intervals, caching every edge it reads.
func unionPass(ctx context.Context, exp Expander, sources []Entry, opts Options, res *Result) error {
	frontier := make([]string, 0, len(sources))
	for _, s := range sources {
		if !res.Union[s.URN] {
			res.Union[s.URN] = true
			frontier = append(frontier, s.URN)
		}
	}

	for depth := range opts.MaxDepth {
		if len(frontier) == 0 {
			return nil
		}
		res.Depth = depth + 1

		edges, err := exp.Out(ctx, frontier)
		if err != nil {
			return fmt.Errorf("expand depth %d: %w", depth, err)
		}

		var next []string
		for _, e := range edges {
			res.Edges[e.From] = append(res.Edges[e.From], e)
			if !res.Union[e.To] {
				res.Union[e.To] = true
				next = append(next, e.To)
			}
		}
		frontier = next
	}

	// Reaching the bound with frontier still to expand means the answer is a lower bound.
	res.Truncated = len(frontier) > 0
	return nil
}

// coexistencePass replays the cached edges with interval semantics.
//
// This is the algorithm proper: frontier entries carry the running intersection, an edge whose
// interval does not overlap it prunes the branch entirely, and a node already covered over the
// candidate span is not re-expanded.
func coexistencePass(sources []Entry, opts Options, res *Result) {
	var frontier []Entry
	for _, s := range sources {
		// A source is reachable from itself only while it existed. Without this a query about a
		// past instant reports versions published after it as though they were already installed,
		// which is the same class of error as counting a path whose edges never coexisted.
		start := s.Valid.Intersect(opts.Within)
		if start.Empty() {
			continue
		}
		set, added := res.Coexistence[s.URN].Insert(start)
		if !added {
			continue
		}
		res.Coexistence[s.URN] = set
		frontier = append(frontier, Entry{URN: s.URN, Valid: start})
	}
	if opts.OnFrontier != nil && len(frontier) > 0 {
		opts.OnFrontier(Frontier{Depth: 0, Entries: frontier})
	}

	for depth := 1; depth <= opts.MaxDepth && len(frontier) > 0; depth++ {
		var next []Entry
		for _, entry := range frontier {
			for _, e := range res.Edges[entry.URN] {
				j := entry.Valid.Intersect(e.Valid)
				if j.Empty() {
					// The path to here and this edge were never valid at the same instant, so this
					// chain never existed. Not for one second. This single test is the whole idea.
					continue
				}
				set, added := res.Coexistence[e.To].Insert(j)
				if !added {
					continue
				}
				res.Coexistence[e.To] = set
				next = append(next, Entry{URN: e.To, Valid: j})
			}
		}
		frontier = next
		if opts.OnFrontier != nil && len(frontier) > 0 {
			opts.OnFrontier(Frontier{Depth: depth, Entries: frontier})
		}
	}
}

// PhantomReach is the count of nodes the union graph claims are reachable but which no coexistence
// path ever reached. It is the overcount every tool without interval semantics reports.
func (r Result) PhantomReach() int {
	phantom := 0
	for urn := range r.Union {
		if len(r.Coexistence[urn]) == 0 {
			phantom++
		}
	}
	return phantom
}

// ReachedAt lists the nodes reachable at one instant.
func (r Result) ReachedAt(t int64) []string {
	var out []string
	for urn, set := range r.Coexistence {
		for _, i := range set {
			if i.Contains(t) {
				out = append(out, urn)
				break
			}
		}
	}
	return out
}
