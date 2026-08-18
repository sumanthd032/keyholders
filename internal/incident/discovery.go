package incident

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// AdvisoryInfo is enough of an Advisory node to name it and show why it matters, without the caller
// needing a second read to get a human readable label.
type AdvisoryInfo struct {
	ID       string
	Summary  string
	Severity string
}

// AdvisoriesAffecting reads every advisory whose AFFECTS edges cover the named version, anchored at
// the version's own id: the incident command starts from a package and version, not an advisory id,
// so this is what lets `keyholders incident <package>@<version>` discover which advisories apply
// without the caller naming one.
func (g GraphSource) AdvisoriesAffecting(ctx context.Context, name, version string) ([]AdvisoryInfo, error) {
	id := graph.ID(graph.VersionURN(g.Ecosystem, name, version))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (a:Advisory)-[:AFFECTS]->(v:Version {id: %d})
     RETURN a.osv_id AS id, a.summary AS summary, a.severity AS severity`, id), nil)
	if err != nil {
		return nil, err
	}

	out := make([]AdvisoryInfo, 0, len(recs))
	for _, rec := range recs {
		i, _ := rec.Get("id")
		osvID, ok := i.(string)
		if !ok {
			continue
		}
		s, _ := rec.Get("summary")
		sev, _ := rec.Get("severity")
		summary, _ := s.(string)
		severity, _ := sev.(string)
		out = append(out, AdvisoryInfo{ID: osvID, Summary: summary, Severity: severity})
	}
	return out, nil
}

// TyposquatNeighbor is one package materially close to the queried one by name, in either direction:
// something the queried package resembles, or something that resembles the queried package.
type TyposquatNeighbor struct {
	Name            string
	Direction       string // "impersonates" or "impersonated by"
	Distance        int
	PopularityRatio float64
}

// TyposquatsNear reads every TYPOSQUAT_OF edge touching the named package, in both directions: two
// anchored single-hop reads rather than one, since the edge is directed from lookalike to the name it
// resembles and a package can appear on either side.
func (g GraphSource) TyposquatsNear(ctx context.Context, name string) ([]TyposquatNeighbor, error) {
	id := graph.ID(graph.PackageURN(g.Ecosystem, name))

	outgoing, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (s:Package {id: %d})-[r:TYPOSQUAT_OF]->(d:Package)
     RETURN d.name AS name, r.distance AS distance, r.popularity_ratio AS ratio`, id), nil)
	if err != nil {
		return nil, err
	}
	incoming, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (s:Package)-[r:TYPOSQUAT_OF]->(d:Package {id: %d})
     RETURN s.name AS name, r.distance AS distance, r.popularity_ratio AS ratio`, id), nil)
	if err != nil {
		return nil, err
	}

	out := make([]TyposquatNeighbor, 0, len(outgoing)+len(incoming))
	out = append(out, typosquatRows(outgoing, "impersonates")...)
	out = append(out, typosquatRows(incoming, "impersonated by")...)
	return out, nil
}

func typosquatRows(recs []*neo4j.Record, direction string) []TyposquatNeighbor {
	out := make([]TyposquatNeighbor, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		name, ok := n.(string)
		if !ok {
			continue
		}
		d, _ := rec.Get("distance")
		r, _ := rec.Get("ratio")
		distance, _ := d.(int64)
		ratio, _ := r.(float64)
		out = append(out, TyposquatNeighbor{Name: name, Direction: direction, Distance: int(distance), PopularityRatio: ratio})
	}
	return out
}
