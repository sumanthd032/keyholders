package query

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Hop is one edge of a proof path.
type Hop struct {
	From  string
	To    string
	Range string
	Valid Interval
}

// Chain is a concrete path from something the project locked to something it reaches, together with
// the span over which every edge on it was simultaneously valid.
//
// This is evidence rather than a claim. A keyholder roster that cannot show the chain is asking to
// be trusted; a chain can be checked against the registry by hand.
type Chain struct {
	Hops  []Hop
	Valid Interval
}

func (c Chain) Nodes() []string {
	if len(c.Hops) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.Hops)+1)
	out = append(out, c.Hops[0].From)
	for _, h := range c.Hops {
		out = append(out, h.To)
	}
	return out
}

// ProofPath finds a coexistence path from any of the sources to the target version.
//
// The procedure does the search and returns whole paths with their edge properties, so coexistence
// is checked here rather than expressed in the query: intervals cannot be intersected in this Cypher
// subset, and a path is small enough that filtering it in Go costs nothing.
//
// The shortest valid chain is returned, because a proof is more convincing when it is short.
//
// fairRelationshipVariants would be useful here, since it spreads the result budget across
// structural paths so one hyper-connected pair cannot consume the whole response. It is rejected
// outside pairwise mode, and pairwise pairs sources to targets by position rather than searching
// every source for the one target, so it is the wrong shape for this question.
func (a *Auditor) ProofPath(ctx context.Context, sources []string, target string, maxLen int) (Chain, bool, error) {
	if maxLen <= 0 {
		maxLen = defaultMaxDepth
	}
	if !npmURN.MatchString(target) {
		return Chain{}, false, fmt.Errorf("not a version URN: %q", target)
	}

	valid := make([]string, 0, len(sources))
	for _, s := range sources {
		if npmURN.MatchString(s) {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return Chain{}, false, nil
	}

	var best Chain
	found := false

	for start := 0; start < len(valid); start += sourceChunk {
		end := min(start+sourceChunk, len(valid))

		// targetValues is how set membership is expressed here, since the Cypher subset has no IN.
		stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Version', sourceProperty: 'urn',
        sourceValues: [%s], targetLabel: 'Version', targetProperty: 'urn', targetValues: ['%s'],
        relTypes: ['RESOLVES_TO'], relDirection: 'outgoing', maxLen: %d,
        pathCount: 64, resultLimit: %d}) YIELD path RETURN path`,
			quoteAll(valid[start:end]), target, maxLen, resultLimit)

		recs, err := a.db.Query(ctx, stmt, nil)
		if err != nil {
			return Chain{}, false, fmt.Errorf("proof path to %s: %w", target, err)
		}

		for _, rec := range recs {
			value, _ := rec.Get("path")
			path, ok := value.(neo4j.Path)
			if !ok {
				continue
			}
			chain, ok := coexistenceChain(path)
			if !ok {
				// The procedure found a path through the union graph whose edges never held at the
				// same instant. Reporting it would be exactly the error this project exists to fix.
				continue
			}
			if !found || len(chain.Hops) < len(best.Hops) {
				best, found = chain, true
			}
		}
	}
	return best, found, nil
}

// coexistenceChain converts a returned path into a chain, reporting whether its edges share an
// instant. The intersection is taken across every edge, which is the definition of a coexistence
// path rather than a merely time-ordered one.
func coexistenceChain(path neo4j.Path) (Chain, bool) {
	if len(path.Relationships) == 0 || len(path.Nodes) != len(path.Relationships)+1 {
		return Chain{}, false
	}

	chain := Chain{Valid: Always, Hops: make([]Hop, 0, len(path.Relationships))}
	for i, rel := range path.Relationships {
		hop := Hop{
			From:  stringProp(path.Nodes[i].Props, "urn"),
			To:    stringProp(path.Nodes[i+1].Props, "urn"),
			Range: stringProp(rel.Props, "range"),
			Valid: intervalOf(rel.Props),
		}
		chain.Hops = append(chain.Hops, hop)
		chain.Valid = chain.Valid.Intersect(hop.Valid)
		if chain.Valid.Empty() {
			return Chain{}, false
		}
	}
	return chain, true
}
