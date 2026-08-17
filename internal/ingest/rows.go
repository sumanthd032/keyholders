package ingest

import (
	"sort"
	"strconv"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/registry"
)

// ecosystem is fixed for now. It is carried in every URN so that adding a second registry later is
// a data change rather than a migration.
const ecosystem = "npm"

// The write statements. Each one is a single UNWIND form, which is the only shape HydraDB accepts a
// list of maps for, and each edge statement touches exactly one relationship type because patterns
// are limited to one type and one hop.
//
// Vertex upsert is MERGE by id followed by SET. Folding the other properties into the MERGE pattern
// is rejected: the pattern is the identity being matched, not a description of the node.
//
// Ranked packages and packages seen only as dependency targets are written by two statements rather
// than one. Within a single UNWIND batch a vertex property may not be assigned two different values:
// HydraDB rejects the whole statement with "conflicting metadata values for vertex N property rank".
// Identical duplicates are accepted, and the same property may differ between statements, so keeping
// rank out of the reference statement is what makes the two paths compose.
const (
	upsertPackage = `UNWIND $rows AS row
  MERGE (n {id: row.vertex})
  SET n:Package, n.urn = row.urn, n.ecosystem = row.ecosystem, n.name = row.name, n.rank = row.rank`

	// A package reached only as a dependency of something in the ranked set. It gets a node so that
	// DEPENDS_ON has somewhere to point, but no rank, because it has no place in the ranking.
	upsertPackageRef = `UNWIND $rows AS row
  MERGE (n {id: row.vertex})
  SET n:Package, n.urn = row.urn, n.ecosystem = row.ecosystem, n.name = row.name`

	upsertVersion = `UNWIND $rows AS row
  MERGE (n {id: row.vertex})
  SET n:Version, n.urn = row.urn, n.name = row.name, n.version = row.version,
      n.published_at = row.published_at, n.deprecated = row.deprecated`

	// A second pass over the sampled versions, which are the only ones whose registry document is
	// fetched. Kept separate from upsertVersion so that the timeline pass stays one request per
	// package: every version gets a node, and the sampled ones get the publisher signals.
	annotateVersion = `UNWIND $rows AS row
  MERGE (n {id: row.vertex})
  SET n:Version, n.published_by = row.published_by, n.has_provenance = row.has_provenance,
      n.has_install_script = row.has_install_script`

	upsertMaintainer = `UNWIND $rows AS row
  MERGE (n {id: row.vertex})
  SET n:Maintainer, n.urn = row.urn, n.handle = row.handle`

	linkHasVersion = `UNWIND $rows AS row
  MATCH (s:Package {id: row.src}), (d:Version {id: row.dst})
  MERGE (s)-[r:HAS_VERSION {id: row.id}]->(d)
  SET r.published_at = row.published_at`

	linkDependsOn = `UNWIND $rows AS row
  MATCH (s:Version {id: row.src}), (d:Package {id: row.dst})
  MERGE (s)-[r:DEPENDS_ON {id: row.id}]->(d)
  SET r.range = row.range, r.direct = row.direct`

	linkMaintains = `UNWIND $rows AS row
  MATCH (s:Maintainer {id: row.src}), (d:Package {id: row.dst})
  MERGE (s)-[r:MAINTAINS {id: row.id}]->(d)
  SET r.valid_from = row.valid_from, r.valid_to = row.valid_to, r.observed = row.observed`

	// SAMPLED duplicates a subset of HAS_VERSION, pointing only at the versions whose registry
	// document was fetched. That is redundant as data and necessary as physical design: traversal
	// indexes are built per edge type, so this is the only way to select the sampled versions in a
	// batched call. The alternative, filtering HAS_VERSION on a version property, walks every
	// version of every package and measured 1.7 seconds for one package with 3,795 of them, against
	// 6 ms for a package with a handful.
	linkSampled = `UNWIND $rows AS row
  MATCH (s:Package {id: row.src}), (d:Version {id: row.dst})
  MERGE (s)-[r:SAMPLED {id: row.id}]->(d)
  SET r.published_at = row.published_at`
)

// rows is one flush unit: everything derived from one package, grouped by the statement that writes
// it. Grouping by statement rather than by package is what lets batches fill to the measured
// optimum of 128 rows instead of being cut short at each package boundary.
type rows struct {
	packages    []map[string]any
	references  []map[string]any
	versions    []map[string]any
	annotations []map[string]any
	maintainers []map[string]any
	hasVersion  []map[string]any
	sampled     []map[string]any
	dependsOn   []map[string]any
	maintains   []map[string]any
}

func (r *rows) append(other *rows) {
	r.packages = append(r.packages, other.packages...)
	r.references = append(r.references, other.references...)
	r.versions = append(r.versions, other.versions...)
	r.annotations = append(r.annotations, other.annotations...)
	r.maintainers = append(r.maintainers, other.maintainers...)
	r.hasVersion = append(r.hasVersion, other.hasVersion...)
	r.sampled = append(r.sampled, other.sampled...)
	r.dependsOn = append(r.dependsOn, other.dependsOn...)
	r.maintains = append(r.maintains, other.maintains...)
}

func (r *rows) nodeCount() int {
	return len(r.packages) + len(r.references) + len(r.versions) + len(r.maintainers)
}

func (r *rows) edgeCount() int {
	return len(r.hasVersion) + len(r.sampled) + len(r.dependsOn) + len(r.maintains)
}

func (r *rows) reset() {
	*r = rows{}
}

// addPackage records a package from the ranked list. rank is its position by download count, 1
// being the most downloaded. It is carried on the node so popularity is available to the typosquat
// ratio and the risk score without a second data source at query time.
func (r *rows) addPackage(name string, rank int) {
	urn := graph.PackageURN(ecosystem, name)
	r.packages = append(r.packages, map[string]any{
		"vertex":    graph.ID(urn),
		"urn":       urn,
		"ecosystem": ecosystem,
		"name":      name,
		"rank":      int64(rank),
	})
}

// addPackageRef records a package reached as a dependency target, which may or may not also be in
// the ranked list.
func (r *rows) addPackageRef(name string) {
	urn := graph.PackageURN(ecosystem, name)
	r.references = append(r.references, map[string]any{
		"vertex":    graph.ID(urn),
		"urn":       urn,
		"ecosystem": ecosystem,
		"name":      name,
	})
}

func (r *rows) addVersion(name string, rel registry.Release) {
	pkgURN := graph.PackageURN(ecosystem, name)
	verURN := graph.VersionURN(ecosystem, name, rel.Version)
	published := graph.Timestamp(rel.PublishedAt)

	r.versions = append(r.versions, map[string]any{
		"vertex":       graph.ID(verURN),
		"urn":          verURN,
		"name":         name,
		"version":      rel.Version,
		"published_at": published,
		"deprecated":   rel.Deprecated,
	})
	r.hasVersion = append(r.hasVersion, map[string]any{
		"id":           graph.EdgeID(pkgURN, "HAS_VERSION", verURN, ""),
		"src":          graph.ID(pkgURN),
		"dst":          graph.ID(verURN),
		"published_at": published,
	})
}

func (r *rows) addVersionDoc(doc registry.VersionDoc, publishedAt int64) {
	pkgURN := graph.PackageURN(ecosystem, doc.Name)
	verURN := graph.VersionURN(ecosystem, doc.Name, doc.Version)
	r.annotations = append(r.annotations, map[string]any{
		"vertex":             graph.ID(verURN),
		"published_by":       doc.PublishedBy,
		"has_provenance":     doc.HasProvenance,
		"has_install_script": doc.HasInstallScript,
	})
	r.sampled = append(r.sampled, map[string]any{
		"id":           graph.EdgeID(pkgURN, "SAMPLED", verURN, ""),
		"src":          graph.ID(pkgURN),
		"dst":          graph.ID(verURN),
		"published_at": publishedAt,
	})
}

func (r *rows) addDependency(dep registry.Dependency) {
	fromURN := graph.VersionURN(ecosystem, dep.FromName, dep.FromVersion)
	toURN := graph.PackageURN(ecosystem, dep.ToName)
	r.dependsOn = append(r.dependsOn, map[string]any{
		"id":     graph.EdgeID(fromURN, "DEPENDS_ON", toURN, ""),
		"src":    graph.ID(fromURN),
		"dst":    graph.ID(toURN),
		"range":  dep.Requirement,
		"direct": dep.Direct,
	})
}

func (r *rows) addMaintainer(handle string) {
	urn := graph.MaintainerURN(ecosystem, handle)
	r.maintainers = append(r.maintainers, map[string]any{
		"vertex": graph.ID(urn),
		"urn":    urn,
		"handle": handle,
	})
}

func (r *rows) addMaintains(handle, pkg string, s spell) {
	mntURN := graph.MaintainerURN(ecosystem, handle)
	pkgURN := graph.PackageURN(ecosystem, pkg)
	r.maintains = append(r.maintains, map[string]any{
		// The interval start discriminates the edge id, so a maintainer who left and returned gets
		// two edges rather than one edge covering the gap they were absent for.
		"id":         graph.EdgeID(mntURN, "MAINTAINS", pkgURN, s.discriminator()),
		"src":        graph.ID(mntURN),
		"dst":        graph.ID(pkgURN),
		"valid_from": s.from,
		"valid_to":   s.to,
		"observed":   int64(s.observed),
	})
}

// observation is one sampled version document, placed in time.
type observation struct {
	at          int64
	maintainers []string
}

// spell is a contiguous run during which one account appeared in a package's maintainer set.
type spell struct {
	from     int64
	to       int64
	observed int
}

// discriminator separates two spells of the same account on the same package. Distinct spells
// necessarily start at distinct observations, so the start instant is enough.
func (s spell) discriminator() string {
	return strconv.FormatInt(s.from, 10)
}

// spells derives each account's holding intervals from sampled maintainer sets.
//
// The sampling is what limits this: maintainer sets are read at the versions current at each epoch,
// so a change is placed at the observation that revealed it, not at the moment it happened. An
// account that joined and left entirely between two samples is invisible. Widening the sample costs
// one request per package per epoch, which is the ingest's binding constraint, so the resolution is
// deliberately coarse and the interval endpoints should be read as "no later than".
func spells(obs []observation) map[string][]spell {
	if len(obs) == 0 {
		return nil
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].at < obs[j].at })

	// present[handle] is the open spell for that handle, if it currently holds one.
	open := map[string]*spell{}
	out := map[string][]spell{}

	for _, o := range obs {
		here := make(map[string]bool, len(o.maintainers))
		for _, h := range o.maintainers {
			here[h] = true
		}

		for h, s := range open {
			if here[h] {
				continue
			}
			// Last seen at the previous observation, gone by this one. The interval ends here,
			// which is the earliest instant we can prove they no longer held the key.
			s.to = o.at
			out[h] = append(out[h], *s)
			delete(open, h)
		}

		for h := range here {
			if s, ok := open[h]; ok {
				s.observed++
				continue
			}
			// The first observation cannot tell us when the account joined, only that it was
			// already there, so the spell starts at the earliest evidence we have.
			open[h] = &spell{from: o.at, to: graph.OpenInterval, observed: 1}
		}
	}

	for h, s := range open {
		out[h] = append(out[h], *s)
	}
	return out
}

// dedupe drops repeated rows for the same vertex or relationship, keeping the first. Every producer
// emits identical values for a repeated key, so which one survives does not matter; what matters is
// that a batch never carries the same key twice, and that a popular dependency referenced by two
// hundred packages is written once rather than two hundred times.
func dedupe(in []map[string]any, key string) []map[string]any {
	if len(in) < 2 {
		return in
	}
	seen := make(map[int64]bool, len(in))
	out := in[:0]
	for _, row := range in {
		id, ok := row[key].(int64)
		if !ok {
			out = append(out, row)
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, row)
	}
	return out
}
