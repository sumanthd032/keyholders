package incident

import (
	"context"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
)

// ResolutionWindow is one RESOLVES_TO edge pointing at the compromised version: a dependent version
// whose declared range resolved to it, and the span that resolution held.
type ResolutionWindow struct {
	DependentName, DependentVersion string
	ValidFrom, ValidTo              int64
}

// LockedAt is one recorded project's pin of a version, and the instant it was locked.
type LockedAt struct {
	Project string
	At      int64
}

// LiveSource supplies what ResolvedWhileLive needs from the graph: every RESOLVES_TO edge into the
// compromised version, and every recorded project that locked a given dependent version.
type LiveSource interface {
	ResolversInto(ctx context.Context, name, version string) ([]ResolutionWindow, error)
	ProjectsLocking(ctx context.Context, name, version string) ([]LockedAt, error)
}

// Exposure is one project that locked a dependent version whose resolution into the compromised
// version was live at the moment that lockfile was recorded.
type Exposure struct {
	Project   string
	LockedAt  int64
	Dependent string
	ValidFrom int64
	ValidTo   int64
}

// ResolvedWhileLive answers track question three, "which applications resolved the compromised
// version while it was live": `LOCKS.locked_at` compared against the `[valid_from, valid_to)` window
// of the RESOLVES_TO edge that produced the compromised version, PROJECT.md's own formula for this
// question, the bitemporal design's reason for existing rather than an afterthought bolted onto it.
//
// This does not require the compromised version to appear as its own LOCKS pin: a project that
// locked a dependent version whose declared range resolved to the compromised one at the time its
// lockfile was written is exposed through that resolution, whether or not the transitive pin was
// separately recorded. A project that did lock the compromised version directly is found the same
// way, since a RESOLVES_TO edge exists from every dependent whose range ever matched it.
func ResolvedWhileLive(ctx context.Context, src LiveSource, name, version string) ([]Exposure, error) {
	windows, err := src.ResolversInto(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("resolvers into %s@%s: %w", name, version, err)
	}

	var out []Exposure
	for _, w := range windows {
		locks, err := src.ProjectsLocking(ctx, w.DependentName, w.DependentVersion)
		if err != nil {
			return nil, fmt.Errorf("projects locking %s@%s: %w", w.DependentName, w.DependentVersion, err)
		}
		for _, l := range locks {
			if l.At < w.ValidFrom || l.At >= w.ValidTo {
				continue
			}
			out = append(out, Exposure{
				Project:   l.Project,
				LockedAt:  l.At,
				Dependent: w.DependentName + "@" + w.DependentVersion,
				ValidFrom: w.ValidFrom,
				ValidTo:   w.ValidTo,
			})
		}
	}
	return out, nil
}

func (g GraphSource) ResolversInto(ctx context.Context, name, version string) ([]ResolutionWindow, error) {
	id := graph.ID(graph.VersionURN(g.Ecosystem, name, version))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (d:Version)-[r:RESOLVES_TO]->(v:Version {id: %d})
     RETURN d.name AS name, d.version AS version, r.valid_from AS valid_from, r.valid_to AS valid_to`, id), nil)
	if err != nil {
		return nil, err
	}

	out := make([]ResolutionWindow, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		v, _ := rec.Get("version")
		name, ok := n.(string)
		if !ok {
			continue
		}
		ver, ok := v.(string)
		if !ok {
			continue
		}
		from, _ := rec.Get("valid_from")
		to, _ := rec.Get("valid_to")
		vf, _ := from.(int64)
		vt, _ := to.(int64)
		out = append(out, ResolutionWindow{DependentName: name, DependentVersion: ver, ValidFrom: vf, ValidTo: vt})
	}
	return out, nil
}

func (g GraphSource) ProjectsLocking(ctx context.Context, name, version string) ([]LockedAt, error) {
	id := graph.ID(graph.VersionURN(g.Ecosystem, name, version))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (p:Project)-[r:LOCKS]->(v:Version {id: %d})
     RETURN p.name AS name, r.locked_at AS locked_at`, id), nil)
	if err != nil {
		return nil, err
	}

	out := make([]LockedAt, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		project, ok := n.(string)
		if !ok {
			continue
		}
		a, _ := rec.Get("locked_at")
		at, _ := a.(int64)
		out = append(out, LockedAt{Project: project, At: at})
	}
	return out, nil
}
