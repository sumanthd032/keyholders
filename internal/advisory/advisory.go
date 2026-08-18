package advisory

import (
	"context"
	"fmt"
	"time"

	"github.com/sumanthd032/keyholders/internal/graph"
)

const ecosystem = "npm"

// Release is one published version, read from a package's HAS_VERSION timeline. Every package that
// reads this off the graph defines its own copy rather than sharing one across packages; see
// resolve.Release and observatory.Release for the same shape used the same way.
type Release struct {
	Version     string
	PublishedAt int64
}

// Source supplies what this package needs from the graph: one package's published version timeline.
// The graph is one implementation; tests use a table.
type Source interface {
	Timeline(ctx context.Context, name string) ([]Release, error)
}

// upsertAdvisory writes an Advisory node. Every UNWIND vertex upsert must SET a label even when the
// node already carries it, finding 21, so the label is always present here regardless of whether the
// node is new.
const upsertAdvisory = `UNWIND $rows AS row
  MERGE (a {id: row.id})
  SET a:Advisory, a.osv_id = row.osv_id, a.summary = row.summary,
      a.severity = row.severity, a.cvss = row.cvss, a.published_at = row.published_at`

// linkAffects writes one AFFECTS edge per advisory and concretely affected version, fanned out from
// the advisory's range rather than left as one edge per range: introduction bisection, task 4, walks
// HAS_VERSION by publish order looking for the first version carrying an AFFECTS edge, which only
// works if every affected version actually has one. introduced and fixed are the boundary strings
// from whichever range produced the match, constant across every edge that range fans out to.
const linkAffects = `UNWIND $rows AS row
  MATCH (a:Advisory {id: row.src}), (v:Version {id: row.dst})
  MERGE (a)-[r:AFFECTS {id: row.id}]->(v)
  SET r.introduced = row.introduced, r.fixed = row.fixed`

type Options struct {
	Batch int
}

func (o *Options) setDefaults() {
	if o.Batch <= 0 {
		o.Batch = graph.DefaultBatch
	}
}

// Stats is what one run produced, since the graph cannot be asked how big it is after the fact.
type Stats struct {
	Advisories int
	Affected   int // affected blocks naming an npm package, whether or not it was in the graph
	NoTimeline int // named package has no HAS_VERSION timeline, so nothing to attach to
	Edges      int
	Elapsed    time.Duration
}

// Ingester writes OSV records into Advisory nodes and AFFECTS edges against timelines already in the
// graph.
type Ingester struct {
	db   *graph.Client
	src  Source
	opts Options
}

func New(db *graph.Client, src Source, opts Options) *Ingester {
	opts.setDefaults()
	return &Ingester{db: db, src: src, opts: opts}
}

// Run writes every record's Advisory node, then resolves each one's affected ranges against the
// named packages' timelines and writes the resulting AFFECTS edges. Nodes are written first because
// the edge write anchors on an Advisory id already existing.
func (i *Ingester) Run(ctx context.Context, records []Record) (Stats, error) {
	started := time.Now()
	var stats Stats

	nodeRows := make([]map[string]any, 0, len(records))
	for _, r := range records {
		if r.ID == "" {
			continue
		}
		cvss, _ := cvssScore(r)
		nodeRows = append(nodeRows, map[string]any{
			"id":           graph.ID(graph.AdvisoryURN(r.ID)),
			"osv_id":       r.ID,
			"summary":      r.Summary,
			"severity":     severity(r),
			"cvss":         cvss,
			"published_at": publishedAt(r.Published),
		})
	}
	written, err := i.db.WriteBatch(ctx, upsertAdvisory, nodeRows, i.opts.Batch)
	if err != nil {
		return stats, fmt.Errorf("write advisories: %w", err)
	}
	stats.Advisories = written

	// Several advisories can name the same package, and a timeline read is not anchored any
	// cheaper the second time, so it is cached for the run rather than reread per advisory.
	timelines := map[string][]Release{}
	timelineOf := func(name string) ([]Release, error) {
		if rel, ok := timelines[name]; ok {
			return rel, nil
		}
		rel, err := i.src.Timeline(ctx, name)
		if err != nil {
			return nil, err
		}
		timelines[name] = rel
		return rel, nil
	}

	// Keyed on the edge id rather than left to the batch write: a small number of records in OSV's
	// npm feed carry more than one Affected block for the same package, and when their ranges
	// disagree on the same concrete version, the second row has the same (advisory, version) pair
	// with different introduced/fixed strings. HydraDB rejects a batch carrying one relationship id
	// twice with different properties outright, so the first match found wins rather than the write
	// failing partway through a 226,000-advisory run. Iteration order is stable within one run, so
	// this is deterministic even if it is not the only defensible tie-break.
	seenEdge := map[int64]bool{}

	var edgeRows []map[string]any
	for _, r := range records {
		advisoryURN := graph.AdvisoryURN(r.ID)
		for _, aff := range r.Affected {
			if aff.Package.Ecosystem != "npm" || aff.Package.Name == "" {
				continue
			}
			stats.Affected++

			releases, err := timelineOf(aff.Package.Name)
			if err != nil {
				return stats, fmt.Errorf("timeline of %s: %w", aff.Package.Name, err)
			}
			if len(releases) == 0 {
				// Named by an advisory but never ingested: the edge of the ingested set, not a
				// failure, the same distinction resolve.go draws for a target with no timeline.
				stats.NoTimeline++
				continue
			}

			for _, m := range matchAffected(aff, releases) {
				versionURN := graph.VersionURN(ecosystem, aff.Package.Name, m.Version)
				id := graph.EdgeID(advisoryURN, "AFFECTS", versionURN, "")
				if seenEdge[id] {
					continue
				}
				seenEdge[id] = true
				edgeRows = append(edgeRows, map[string]any{
					"id":         id,
					"src":        graph.ID(advisoryURN),
					"dst":        graph.ID(versionURN),
					"introduced": m.introduced,
					"fixed":      m.fixed,
				})
			}
		}
	}
	edgesWritten, err := i.db.WriteBatch(ctx, linkAffects, edgeRows, i.opts.Batch)
	if err != nil {
		return stats, fmt.Errorf("write affects: %w", err)
	}
	stats.Edges = edgesWritten
	stats.Elapsed = time.Since(started)
	return stats, nil
}

// match is one release an Affected block covers, carrying the boundary strings from whichever range
// matched it, for display on the edge.
type match struct {
	Release
	introduced, fixed string
}

// matchAffected resolves one Affected block against the package's real releases: through its ranges
// when it has any, since a range is resolved against whatever actually published rather than trusted
// to enumerate versions itself, or through its explicit Versions list when ranges are absent, which
// OSV allows for ecosystems that cannot express a range at all. A release named by more than one
// range keeps the first match, so the boundary shown is not arbitrary between rebuilds.
func matchAffected(aff Affected, releases []Release) []match {
	seen := map[string]match{}
	for _, rng := range aff.Ranges {
		intro, fix := rangeBounds(rng)
		for _, rel := range affectedVersions(rng, releases) {
			if _, ok := seen[rel.Version]; !ok {
				seen[rel.Version] = match{Release: rel, introduced: intro, fixed: fix}
			}
		}
	}
	if len(aff.Ranges) == 0 {
		for _, want := range aff.Versions {
			for _, rel := range releases {
				if rel.Version == want {
					if _, ok := seen[rel.Version]; !ok {
						seen[rel.Version] = match{Release: rel}
					}
				}
			}
		}
	}

	out := make([]match, 0, len(seen))
	for _, m := range seen {
		out = append(out, m)
	}
	return out
}

// rangeBounds picks the boundary strings shown on an edge: the first introduced event, since a
// range's affected status always starts there, and the last fixed event, which is the most recent
// patch in a range stitched from more than one introduce/fix cycle.
func rangeBounds(r Range) (introducedAt, fixedAt string) {
	for _, e := range r.Events {
		if e.Introduced != "" && introducedAt == "" {
			introducedAt = e.Introduced
		}
		if e.Fixed != "" {
			fixedAt = e.Fixed
		}
	}
	return introducedAt, fixedAt
}

// GraphSource reads timelines from HydraDB, anchored at the package id already known from its URN.
type GraphSource struct{ DB *graph.Client }

func (g GraphSource) Timeline(ctx context.Context, name string) ([]Release, error) {
	id := graph.ID(graph.PackageURN(ecosystem, name))
	recs, err := g.DB.Query(ctx, fmt.Sprintf(
		`MATCH (p:Package {id: %d})-[:HAS_VERSION]->(v:Version)
     RETURN v.version AS version, v.published_at AS published`, id), nil)
	if err != nil {
		return nil, fmt.Errorf("timeline of %s: %w", name, err)
	}

	releases := make([]Release, 0, len(recs))
	for _, rec := range recs {
		version, _ := rec.Get("version")
		published, _ := rec.Get("published")
		v, ok := version.(string)
		if !ok {
			continue
		}
		p, _ := published.(int64)
		releases = append(releases, Release{Version: v, PublishedAt: p})
	}
	return releases, nil
}
