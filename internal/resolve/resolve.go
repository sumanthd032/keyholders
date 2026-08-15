package resolve

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/sumanthd032/keyholders/internal/checkpoint"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/semver"
)

const ecosystem = "npm"

// linkResolvesTo writes the edge the whole product reads.
//
// RESOLVES_TO is deliberately its own relationship type. Traversal indexes are built per edge type,
// so the hot path gets its own compiled adjacency generation rather than sharing one with
// DEPENDS_ON and filtering on every hop.
//
// The edge needs no discriminator in its id: a resolution moves only upward through the target's
// versions, so each resolved version owns exactly one contiguous window. Re-running therefore
// updates windows in place instead of accumulating duplicates.
const linkResolvesTo = `UNWIND $rows AS row
  MATCH (s:Version {id: row.src}), (d:Version {id: row.dst})
  MERGE (s)-[r:RESOLVES_TO {id: row.id}]->(d)
  SET r.valid_from = row.valid_from, r.valid_to = row.valid_to, r.range = row.range`

// Options configures one resolve run.
type Options struct {
	// Readers is how many target packages are read from the graph concurrently. Reads are not
	// serialised the way writes are, so this does raise throughput.
	Readers int
	Batch   int
}

func (o *Options) setDefaults() {
	if o.Readers <= 0 {
		o.Readers = 4
	}
	if o.Batch <= 0 {
		o.Batch = graph.DefaultBatch
	}
}

// Stats is what the run produced. As with the ingest, the graph cannot be asked how big it is, so
// these counters are the only source.
type Stats struct {
	Packages     int
	Skipped      int
	Declarations int
	Edges        int
	Unsatisfied  int
	Unparseable  int
	NoTimeline   int
	Elapsed      time.Duration
}

// Resolver materializes RESOLVES_TO from the DEPENDS_ON edges already in the graph.
type Resolver struct {
	db    *graph.Client
	opts  Options
	log   io.Writer
	mu    sync.Mutex
	stats Stats
}

func New(db *graph.Client, opts Options, log io.Writer) *Resolver {
	opts.setDefaults()
	return &Resolver{db: db, opts: opts, log: log}
}

// Run resolves every declaration pointing at one of the named packages.
//
// The work is organised by target rather than by dependent, which is what makes it affordable. A
// target's version timeline is what a range is resolved against, so fetching it once and then
// answering every declaration that points at it turns an N-declaration problem into one read per
// package. It also plays to the engine's shape: HydraDB keeps inbound topology records, so asking
// "who depends on this" is a native reverse traversal rather than a scan.
func (r *Resolver) Run(ctx context.Context, names []string, stateDir string) (Stats, error) {
	cp, err := checkpoint.Open(filepath.Join(stateDir, "resolve.checkpoint"))
	if err != nil {
		return Stats{}, err
	}
	defer cp.Close()

	started := time.Now()
	targets := make(chan string)
	results := make(chan *targetRows)

	var wg sync.WaitGroup
	for range r.opts.Readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range targets {
				tr, err := r.resolveTarget(ctx, name)
				if err != nil {
					fmt.Fprintf(r.log, "  skip %s: %v\n", name, err)
					continue
				}
				select {
				case results <- tr:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(targets)
		for _, name := range names {
			if cp.Has(name) {
				r.mu.Lock()
				r.stats.Skipped++
				r.mu.Unlock()
				continue
			}
			select {
			case targets <- name:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	err = r.writeLoop(ctx, results, cp)

	r.mu.Lock()
	r.stats.Elapsed = time.Since(started)
	stats := r.stats
	r.mu.Unlock()
	return stats, err
}

type targetRows struct {
	name string
	rows []map[string]any
}

const flushAt = 2048

func (r *Resolver) writeLoop(ctx context.Context, results <-chan *targetRows, cp *checkpoint.File) error {
	var pending []map[string]any
	var names []string
	lastLog := time.Now()

	flush := func() error {
		if len(names) == 0 {
			return nil
		}
		if len(pending) > 0 {
			if _, err := r.db.WriteBatch(ctx, linkResolvesTo, pending, r.opts.Batch); err != nil {
				return fmt.Errorf("write resolves_to: %w", err)
			}
			r.mu.Lock()
			r.stats.Edges += len(pending)
			r.mu.Unlock()
		}
		if err := cp.Record(names); err != nil {
			return err
		}
		pending, names = pending[:0], names[:0]
		return nil
	}

	for tr := range results {
		pending = append(pending, tr.rows...)
		names = append(names, tr.name)

		r.mu.Lock()
		r.stats.Packages++
		done := r.stats.Packages
		r.mu.Unlock()

		if len(pending) >= flushAt {
			if err := flush(); err != nil {
				return err
			}
		}
		if time.Since(lastLog) > 5*time.Second {
			r.progress(done)
			lastLog = time.Now()
		}
	}
	return flush()
}

// resolveTarget reads one package's timeline and every declaration pointing at it, then turns each
// declaration into its stable resolution windows.
func (r *Resolver) resolveTarget(ctx context.Context, name string) (*targetRows, error) {
	releases, err := r.timeline(ctx, name)
	if err != nil {
		return nil, err
	}
	tr := &targetRows{name: name}

	declarations, err := r.dependents(ctx, name)
	if err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		// The package was reached as somebody's dependency but its own versions were never
		// ingested, so there is no timeline to resolve against. Counting these separately is how
		// the run reports its coverage: they are the edge of the ingested set, not a failure.
		r.mu.Lock()
		r.stats.Declarations += len(declarations)
		r.stats.NoTimeline += len(declarations)
		r.mu.Unlock()
		return tr, nil
	}

	var unsatisfied, unparseable int
	for _, d := range declarations {
		rng, err := semver.ParseRange(d.rangeText)
		if err != nil {
			unparseable++
			continue
		}
		windows := Windows(releases, rng, d.publishedAt)
		if len(windows) == 0 {
			// Either nothing was ever published that satisfies the range, or everything that did
			// predates the dependent. Both are real states of the ecosystem, not errors.
			unsatisfied++
			continue
		}
		for _, w := range windows {
			targetURN := graph.VersionURN(ecosystem, name, w.Version)
			tr.rows = append(tr.rows, map[string]any{
				"id":         graph.EdgeID(d.versionURN, "RESOLVES_TO", targetURN, ""),
				"src":        graph.ID(d.versionURN),
				"dst":        graph.ID(targetURN),
				"valid_from": w.From,
				"valid_to":   w.To,
				"range":      d.rangeText,
			})
		}
	}

	r.mu.Lock()
	r.stats.Declarations += len(declarations)
	r.stats.Unsatisfied += unsatisfied
	r.stats.Unparseable += unparseable
	r.mu.Unlock()
	return tr, nil
}

// timeline reads every published version of a package, anchored at the package node.
//
// Anchoring is not optional. An unanchored pattern such as MATCH (v:Version)-[:DEPENDS_ON]->(p)
// exceeds the query timeout even with LIMIT 3, because the match is materialized before LIMIT or
// WHERE is applied. Every read here starts from an id we already know.
func (r *Resolver) timeline(ctx context.Context, name string) ([]Release, error) {
	id := graph.ID(graph.PackageURN(ecosystem, name))
	recs, err := r.db.Query(ctx, fmt.Sprintf(
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

type declaration struct {
	versionURN  string
	rangeText   string
	publishedAt int64
}

// dependents reads every declared dependency pointing at a package, anchored at the target.
func (r *Resolver) dependents(ctx context.Context, name string) ([]declaration, error) {
	id := graph.ID(graph.PackageURN(ecosystem, name))
	recs, err := r.db.Query(ctx, fmt.Sprintf(
		`MATCH (v:Version)-[e:DEPENDS_ON]->(p:Package {id: %d})
     RETURN v.urn AS urn, v.published_at AS published, e.range AS range`, id), nil)
	if err != nil {
		return nil, fmt.Errorf("dependents of %s: %w", name, err)
	}

	out := make([]declaration, 0, len(recs))
	for _, rec := range recs {
		urn, _ := rec.Get("urn")
		published, _ := rec.Get("published")
		rangeText, _ := rec.Get("range")

		u, ok := urn.(string)
		if !ok {
			continue
		}
		rt, _ := rangeText.(string)
		p, _ := published.(int64)
		out = append(out, declaration{versionURN: u, rangeText: rt, publishedAt: p})
	}
	return out, nil
}

func (r *Resolver) progress(done int) {
	r.mu.Lock()
	s := r.stats
	r.mu.Unlock()
	fmt.Fprintf(r.log, "  %d packages, %d declarations, %d edges\n", done, s.Declarations, s.Edges)
}
