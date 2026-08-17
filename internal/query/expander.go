package query

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// GraphExpander expands frontiers through HydraDB.
type GraphExpander struct {
	db       *graph.Client
	edgeType string

	// Direction is the sense the edge type is walked in, "outgoing" by default. An audit walks
	// RESOLVES_TO outward from a project into its dependencies; asking what one account reaches
	// walks the same edges inward from their packages to their dependents.
	Direction string

	// Reads counts the round trips made, so a run can report how much of its time was the graph.
	Reads int

	// Attrs holds the node properties seen during expansion, keyed by version URN.
	//
	// A returned path carries every property of both its nodes, and the search needs only the URNs,
	// so the rest would otherwise be decoded and dropped. Keeping them turns the publisher signals
	// into a by-product of the traversal rather than a second pass over the same nodes.
	Attrs map[string]VersionAttrs
}

func NewGraphExpander(db *graph.Client, edgeType string) *GraphExpander {
	return &GraphExpander{
		db:        db,
		edgeType:  edgeType,
		Direction: "outgoing",
		Attrs:     map[string]VersionAttrs{},
	}
}

// sourceChunk is how many URNs go into one MSpaths call.
//
// The procedure amortises hard across sources, sharing selector hydration, topology and adjacency,
// so batching is the whole reason a frontier of five hundred nodes costs one query rather than five
// hundred. The chunk is bounded anyway because the value list is interpolated into the query text.
const sourceChunk = 256

// resultLimit bounds what one call returns. It has to be generous, because exceeding it truncates
// the answer with no error and no cursor, and an undercounted traversal is the dangerous direction
// for a security tool. See splitting in expand.
const resultLimit = 20000

// npmURN matches the URNs we construct, and nothing else.
//
// Every value is checked against this before it reaches a query string. That is not defensive
// habit: a list parameter is only accepted as UNWIND input, so algo.MSpaths value lists must be
// interpolated literally, which puts the validation burden on the caller.
var npmURN = regexp.MustCompile(`^pkg:npm/(@[a-z0-9._~-]+/)?[a-z0-9._~-]+@[A-Za-z0-9._+-]+$`)

// Out returns the outgoing edges of the given nodes, with their validity windows.
func (g *GraphExpander) Out(ctx context.Context, urns []string) ([]Edge, error) {
	var out []Edge
	for start := 0; start < len(urns); start += sourceChunk {
		end := min(start+sourceChunk, len(urns))
		edges, err := g.expand(ctx, urns[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, edges...)
	}
	return out, nil
}

// expand runs one MSpaths call, splitting the source set if the result may have been truncated.
func (g *GraphExpander) expand(ctx context.Context, urns []string) ([]Edge, error) {
	valid := make([]string, 0, len(urns))
	for _, u := range urns {
		if npmURN.MatchString(u) {
			valid = append(valid, u)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}

	stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Version', sourceProperty: 'urn',
      sourceValues: [%s], relTypes: ['%s'], relDirection: '%s',
      maxLen: 1, pathCount: %d, resultLimit: %d}) YIELD path RETURN path`,
		quoteAll(valid), g.edgeType, g.Direction, resultLimit, resultLimit)

	g.Reads++
	recs, err := g.db.Query(ctx, stmt, nil)
	if err != nil {
		return nil, fmt.Errorf("expand %d sources: %w", len(valid), err)
	}

	// A full result set is indistinguishable from a truncated one: the procedure returns exactly
	// resultLimit rows with no error and no cursor. Splitting on equality costs one extra query in
	// the rare exact-fit case and prevents a silently incomplete traversal in every other.
	if len(recs) >= resultLimit && len(valid) > 1 {
		half := len(valid) / 2
		left, err := g.expand(ctx, valid[:half])
		if err != nil {
			return nil, err
		}
		right, err := g.expand(ctx, valid[half:])
		if err != nil {
			return nil, err
		}
		return append(left, right...), nil
	}

	edges := make([]Edge, 0, len(recs))
	for _, rec := range recs {
		value, ok := rec.Get("path")
		if !ok {
			continue
		}
		path, ok := value.(neo4j.Path)
		if !ok {
			return nil, fmt.Errorf("expected a path, got %T", value)
		}
		if len(path.Nodes) != 2 || len(path.Relationships) != 1 {
			// maxLen is 1, so anything else is not a single hop and we do not know how to read it.
			continue
		}
		for _, n := range path.Nodes {
			if urn := stringProp(n.Props, "urn"); urn != "" {
				g.Attrs[urn] = versionAttrs(n.Props)
			}
		}
		edges = append(edges, Edge{
			From:  stringProp(path.Nodes[0].Props, "urn"),
			To:    stringProp(path.Nodes[1].Props, "urn"),
			Valid: intervalOf(path.Relationships[0].Props),
		})
	}
	return edges, nil
}

func quoteAll(values []string) string {
	var b strings.Builder
	for i, v := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('\'')
		b.WriteString(v)
		b.WriteByte('\'')
	}
	return b.String()
}

func intervalOf(props map[string]any) Interval {
	i := Interval{From: intProp(props, "valid_from"), To: intProp(props, "valid_to")}
	if i.To == 0 {
		// An edge type that carries no window, such as HAS_VERSION, is valid whenever anything is.
		return Always
	}
	return i
}

func intProp(props map[string]any, key string) int64 {
	v, _ := props[key].(int64)
	return v
}

func stringProp(props map[string]any, key string) string {
	v, _ := props[key].(string)
	return v
}
