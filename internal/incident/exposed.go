package incident

import (
	"context"
	"fmt"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/lockfile"
	"github.com/sumanthd032/keyholders/internal/query"
)

// RecordedProject is one project this graph already has LOCKS pins for, from a prior `scan --record`.
type RecordedProject struct {
	Name     string
	LockedAt int64
}

// ExposedProject is one recorded project whose coexistence-constrained reach, evaluated at the
// instant its lockfile was recorded, includes the compromised version.
type ExposedProject struct {
	Project  string `json:"project"`
	LockedAt int64  `json:"locked_at"`
}

// ExposureSource supplies what TransitivelyExposed needs beyond query.Auditor: every project this
// graph has recorded pins for, and each one's pins reconstructed from its LOCKS edges.
type ExposureSource interface {
	RecordedProjects(ctx context.Context) ([]RecordedProject, error)
	Pins(ctx context.Context, project string) ([]lockfile.Pin, error)
}

// TransitivelyExposed answers track question one, "which internal services are transitively
// exposed": every recorded project whose dependency tree, walked coexistence constrained and
// evaluated at the instant its own lockfile was locked, reaches the compromised version.
//
// This reuses Mode 1's interval BFS rather than a second traversal engine: exposure through an
// incident is the same question the audit answers for one lockfile, asked here against every
// lockfile this graph already knows about, each at its own recorded instant rather than at the
// instant the incident command happens to run.
func TransitivelyExposed(ctx context.Context, src ExposureSource, auditor *query.Auditor, name, version string) ([]ExposedProject, error) {
	target := graph.VersionURN("npm", name, version)

	projects, err := src.RecordedProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("recorded projects: %w", err)
	}

	var out []ExposedProject
	for _, p := range projects {
		pins, err := src.Pins(ctx, p.Name)
		if err != nil {
			return nil, fmt.Errorf("pins of %s: %w", p.Name, err)
		}
		if len(pins) == 0 {
			continue
		}

		lf := lockfile.Lockfile{Project: p.Name, Pins: pins}
		opts := query.Options{Within: query.At(p.LockedAt), MaxDepth: 12}
		audit, err := auditor.Audit(ctx, lf, opts)
		if err != nil {
			return nil, fmt.Errorf("audit %s: %w", p.Name, err)
		}
		if _, reached := audit.Reach.Coexistence[target]; reached {
			out = append(out, ExposedProject{Project: p.Name, LockedAt: p.LockedAt})
		}
	}
	return out, nil
}

// RecordedProjects lists every project this graph has pins for. Unanchored, unlike every other read
// in this package: there is no id to anchor on for "every project," and the label is expected to
// stay small, the local repositories one user or team has run `scan --record` against, not an
// ecosystem scale set the way Package is.
func (g GraphSource) RecordedProjects(ctx context.Context) ([]RecordedProject, error) {
	recs, err := g.DB.Query(ctx, `MATCH (p:Project) RETURN p.name AS name, p.locked_at AS locked_at`, nil)
	if err != nil {
		return nil, err
	}

	out := make([]RecordedProject, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		name, ok := n.(string)
		if !ok {
			continue
		}
		a, _ := rec.Get("locked_at")
		at, _ := a.(int64)
		out = append(out, RecordedProject{Name: name, LockedAt: at})
	}
	return out, nil
}

func (g GraphSource) Pins(ctx context.Context, project string) ([]lockfile.Pin, error) {
	id := graph.ID(graph.ProjectURN(project))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (p:Project {id: %d})-[r:LOCKS]->(v:Version)
     RETURN v.name AS name, v.version AS version, r.direct AS direct, r.dev AS dev`, id), nil)
	if err != nil {
		return nil, err
	}

	out := make([]lockfile.Pin, 0, len(recs))
	for _, rec := range recs {
		n, _ := rec.Get("name")
		v, _ := rec.Get("version")
		name, ok := n.(string)
		if !ok {
			continue
		}
		version, ok := v.(string)
		if !ok {
			continue
		}
		d, _ := rec.Get("direct")
		dv, _ := rec.Get("dev")
		direct, _ := d.(bool)
		dev, _ := dv.(bool)
		out = append(out, lockfile.Pin{Name: name, Version: version, Direct: direct, Dev: dev})
	}
	return out, nil
}
