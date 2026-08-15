// Package ingest builds the package, version and maintainer graph in HydraDB from public registry
// data. It is the only place that writes those node and edge types.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/sumanthd032/keyholders/internal/checkpoint"
	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/registry"
)

// Options configures one ingest run.
type Options struct {
	// Epochs is how many quarterly sample points each package is examined at. Every epoch costs one
	// npm document and one deps.dev closure per package, so this multiplies the request budget,
	// which at 30 requests/s is what bounds the run.
	Epochs int

	// Fetchers is how many packages are fetched concurrently. It does not change the sustained rate,
	// which the shared rate limiter fixes, but it keeps the limiter saturated while slow responses
	// are outstanding.
	Fetchers int

	// Batch is the UNWIND row count per statement.
	Batch int

	// Deps controls whether resolved dependency closures are fetched. Turning it off halves the
	// request count and produces a graph with no DEPENDS_ON edges, which is useful for measuring
	// the rest of the pipeline in isolation.
	Deps bool

	// Now is the instant epochs are counted back from. Injected so a run is reproducible.
	Now time.Time
}

func (o *Options) setDefaults() {
	if o.Epochs <= 0 {
		o.Epochs = 8
	}
	if o.Fetchers <= 0 {
		o.Fetchers = 8
	}
	if o.Batch <= 0 {
		o.Batch = graph.DefaultBatch
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
}

// Stats is what the run wrote. The graph cannot be asked how big it is (a label scan caps at
// 250,000 candidates and a full edge-type count exceeds the query timeout), so the ingest keeps its
// own counters and they are the only source for the progress line and the final report.
type Stats struct {
	Packages  int
	Skipped   int
	Missing   int
	Nodes     int
	Edges     int
	Requests  int
	Elapsed   time.Duration
	StartedAt time.Time
}

// Ingester writes the package graph.
type Ingester struct {
	reg   *registry.Client
	db    *graph.Client
	opts  Options
	log   io.Writer
	stats Stats
	mu    sync.Mutex
}

func New(reg *registry.Client, db *graph.Client, opts Options, log io.Writer) *Ingester {
	opts.setDefaults()
	return &Ingester{reg: reg, db: db, opts: opts, log: log}
}

// Run ingests every named package, resuming from the checkpoint in stateDir. Names are expected in
// rank order, most downloaded first, because the rank is written onto the node.
func (i *Ingester) Run(ctx context.Context, names []string, stateDir string) (Stats, error) {
	cp, err := checkpoint.Open(filepath.Join(stateDir, "ingest.checkpoint"))
	if err != nil {
		return Stats{}, err
	}
	defer cp.Close()

	i.stats.StartedAt = time.Now()
	epochs := Epochs(i.opts.Epochs, i.opts.Now)

	type job struct {
		name string
		rank int
	}
	jobs := make(chan job)
	results := make(chan *packageRows)

	var wg sync.WaitGroup
	for range i.opts.Fetchers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				pr, err := i.fetchPackage(ctx, j.name, j.rank, epochs)
				if err != nil {
					// A package that cannot be fetched is not a reason to abandon the run: names
					// come from a ranking that drifts, and unpublished packages are ordinary.
					// Anything else is reported and skipped so the run still finishes.
					if !errors.Is(err, registry.ErrNotFound) && !errors.Is(err, context.Canceled) {
						fmt.Fprintf(i.log, "  skip %s: %v\n", j.name, err)
					}
					i.countMissing()
					continue
				}
				select {
				case results <- pr:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for rank, name := range names {
			if cp.Has(name) {
				i.mu.Lock()
				i.stats.Skipped++
				i.mu.Unlock()
				continue
			}
			select {
			case jobs <- job{name: name, rank: rank + 1}:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	// A single writer, because writes serialise behind the per-cell writer lease: sixteen
	// concurrent sessions measured no faster than one, so extra writers would only add contention.
	err = i.writeLoop(ctx, results, cp)

	i.mu.Lock()
	i.stats.Elapsed = time.Since(i.stats.StartedAt)
	stats := i.stats
	i.mu.Unlock()
	return stats, err
}

// packageRows is one package's contribution, kept together so the checkpoint can only record a name
// after everything derived from it has been written.
type packageRows struct {
	name     string
	rows     rows
	requests int
}

// flushAt is the accumulated row count that triggers a write. Batches are filled across packages,
// so a package contributing three rows does not cause a three-row statement.
const flushAt = 2048

func (i *Ingester) writeLoop(ctx context.Context, results <-chan *packageRows, cp *checkpoint.File) error {
	var pending rows
	var names []string
	lastLog := time.Now()

	flush := func() error {
		if len(names) == 0 {
			return nil
		}
		if err := i.write(ctx, &pending); err != nil {
			return err
		}
		if err := cp.Record(names); err != nil {
			return err
		}
		pending.reset()
		names = names[:0]
		return nil
	}

	for pr := range results {
		pending.append(&pr.rows)
		names = append(names, pr.name)

		i.mu.Lock()
		i.stats.Packages++
		i.stats.Requests += pr.requests
		done := i.stats.Packages
		i.mu.Unlock()

		if pending.nodeCount()+pending.edgeCount() >= flushAt {
			if err := flush(); err != nil {
				return err
			}
		}
		if time.Since(lastLog) > 5*time.Second {
			i.progress(done, cp.Count())
			lastLog = time.Now()
		}
	}
	return flush()
}

// write sends one accumulated batch. Nodes go before the edges that reference them, because an edge
// statement is UNWIND MATCH and a MATCH that finds nothing writes nothing and reports no error.
func (i *Ingester) write(ctx context.Context, r *rows) error {
	// Nodes are keyed by "vertex" and edges by "id". Deduplicating here rather than at the point of
	// production is what keeps a popular dependency, referenced by most of the batch, from being
	// written once per referrer.
	r.packages = dedupe(r.packages, "vertex")
	r.references = dedupe(r.references, "vertex")
	r.versions = dedupe(r.versions, "vertex")
	r.annotations = dedupe(r.annotations, "vertex")
	r.maintainers = dedupe(r.maintainers, "vertex")
	r.hasVersion = dedupe(r.hasVersion, "id")
	r.dependsOn = dedupe(r.dependsOn, "id")
	r.maintains = dedupe(r.maintains, "id")

	steps := []struct {
		name string
		stmt string
		rows []map[string]any
	}{
		{"packages", upsertPackage, r.packages},
		{"package references", upsertPackageRef, r.references},
		{"versions", upsertVersion, r.versions},
		{"maintainers", upsertMaintainer, r.maintainers},
		{"version annotations", annotateVersion, r.annotations},
		{"has_version", linkHasVersion, r.hasVersion},
		{"depends_on", linkDependsOn, r.dependsOn},
		{"maintains", linkMaintains, r.maintains},
	}

	nodes, edges := r.nodeCount(), r.edgeCount()
	for _, s := range steps {
		if len(s.rows) == 0 {
			continue
		}
		if _, err := i.db.WriteBatch(ctx, s.stmt, s.rows, i.opts.Batch); err != nil {
			return fmt.Errorf("write %s: %w", s.name, err)
		}
	}

	i.mu.Lock()
	i.stats.Nodes += nodes
	i.stats.Edges += edges
	i.mu.Unlock()
	return nil
}

func (i *Ingester) fetchPackage(ctx context.Context, name string, rank int, epochs []time.Time) (*packageRows, error) {
	pr := &packageRows{name: name}
	pr.rows.addPackage(name, rank)

	releases, err := i.reg.Timeline(ctx, name)
	pr.requests++
	if err != nil {
		return nil, err
	}
	for _, rel := range releases {
		if rel.PublishedAt.IsZero() {
			// Without a publish time the version cannot be placed on the timeline, and every
			// temporal claim downstream depends on that placement.
			continue
		}
		pr.rows.addVersion(name, rel)
	}

	// The versions current at each epoch, deduplicated. A package that has not published in years
	// is the same version at every epoch and costs one document, not one per epoch.
	sampled := make([]registry.Release, 0, len(epochs))
	seen := map[string]bool{}
	for _, at := range epochs {
		rel, ok := versionAt(releases, at)
		if !ok || seen[rel.Version] {
			continue
		}
		seen[rel.Version] = true
		sampled = append(sampled, rel)
	}

	var obs []observation
	handles := map[string]bool{}
	for _, rel := range sampled {
		doc, err := i.reg.VersionDoc(ctx, name, rel.Version)
		pr.requests++
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("version document %s@%s: %w", name, rel.Version, err)
		}
		pr.rows.addVersionDoc(doc)
		obs = append(obs, observation{at: graph.Timestamp(rel.PublishedAt), maintainers: doc.Maintainers})
		for _, h := range doc.Maintainers {
			handles[h] = true
		}

		if i.opts.Deps {
			deps, err := i.reg.Dependencies(ctx, name, rel.Version)
			pr.requests++
			if err != nil && !errors.Is(err, registry.ErrNotFound) {
				return nil, fmt.Errorf("dependencies %s@%s: %w", name, rel.Version, err)
			}
			for _, d := range deps {
				// The closure carries edges between packages other than this one. They are real
				// declarations and worth keeping, but their endpoints are versions we have not
				// created nodes for, and an edge whose MATCH finds nothing is silently dropped.
				// Only the root's own declarations are written here; the rest arrive when their
				// own package is ingested.
				if !d.Direct {
					continue
				}
				pr.rows.addDependency(d)
				pr.rows.addPackageRef(d.ToName)
			}
		}
	}

	for h := range handles {
		pr.rows.addMaintainer(h)
	}
	for handle, spells := range spells(obs) {
		for _, s := range spells {
			pr.rows.addMaintains(handle, name, s)
		}
	}
	return pr, nil
}

func (i *Ingester) countMissing() {
	i.mu.Lock()
	i.stats.Missing++
	i.mu.Unlock()
}

func (i *Ingester) progress(done, committed int) {
	i.mu.Lock()
	s := i.stats
	i.mu.Unlock()

	el := time.Since(s.StartedAt).Seconds()
	fmt.Fprintf(i.log, "  %d packages, %d committed, %d nodes, %d edges, %.0f req/s, %.0f edges/s\n",
		done, committed, s.Nodes, s.Edges, float64(s.Requests)/el, float64(s.Edges)/el)
}
