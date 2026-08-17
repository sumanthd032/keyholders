package query

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/sumanthd032/keyholders/internal/graph"
	"github.com/sumanthd032/keyholders/internal/lockfile"
)

// Keyholder is one account that can execute code inside the audited tree, together with the
// packages that give them that reach and the spans over which they had it.
type Keyholder struct {
	Handle string

	// Through maps a package URN to the spans over which this account held publish rights *and* a
	// coexistence path from the project reached that package. The intersection of the two is the
	// point: someone who left a project before you ever depended on it never held your key.
	Through map[string]Set

	// Since is when this account's publish rights over any of those packages begin, read off the
	// MAINTAINS edge rather than off Through. Through is already clipped to the query window, so at
	// a single instant every span in it starts at that instant, and reading tenure from it would
	// report the date of the question instead of the date of the access.
	Since int64
}

// Packages is the number of packages in your tree this account can publish to.
func (k Keyholder) Packages() int { return len(k.Through) }

// Audit is the answer for one lockfile.
type Audit struct {
	Project string
	Format  string

	// Pins is what the lockfile listed, Sources is how many of those exist in the graph. The gap is
	// the ingested set's coverage of this project, and it has to be reported: an audit over half a
	// tree is a lower bound, not an answer.
	Pins    int
	Sources int

	Reach Result

	// Keyholders counts only coexistence paths, ordered by risk. UnionKeyholders is what a tool
	// without interval semantics would report, and the difference between them is the overcount
	// nobody has measured.
	Keyholders      []Keyholder
	UnionKeyholders int

	// Signals, Scores and Cuts are keyed by handle. Weights are carried alongside the scores they
	// produced, because a ranking whose weights are not on the page is a ranking nobody can check.
	Signals map[string]Signals
	Scores  map[string]Score
	Weights Weights
	Cuts    []Cut
}

// PhantomKeyholders is the number of accounts the union graph would name as keyholders but which no
// coexistence path ever reached.
func (a Audit) PhantomKeyholders() int { return a.UnionKeyholders - len(a.Keyholders) }

// Auditor answers audit questions against the graph.
type Auditor struct {
	db  *graph.Client
	exp *GraphExpander

	// Weights configure the risk ranking. Exported so a caller can override them and print what it
	// used, which is the whole point of the score being a formula rather than a model.
	Weights Weights

	// Now is the instant a point-in-time audit is scored against when the query itself names none.
	Now int64
}

func NewAuditor(db *graph.Client) *Auditor {
	return &Auditor{
		db:      db,
		exp:     NewGraphExpander(db, "RESOLVES_TO"),
		Weights: DefaultWeights,
		Now:     time.Now().Unix(),
	}
}

// Reads is how many graph round trips the last audit made.
func (a *Auditor) Reads() int { return a.exp.Reads }

// Audit walks out from a lockfile and reports who holds a key.
func (a *Auditor) Audit(ctx context.Context, lf lockfile.Lockfile, opts Options) (Audit, error) {
	out := Audit{Project: lf.Project, Format: lf.Format, Pins: len(lf.Pins), Weights: a.Weights}

	sources, err := a.knownVersions(ctx, lf.Pins)
	if err != nil {
		return Audit{}, err
	}
	out.Sources = len(sources)
	if len(sources) == 0 {
		return out, nil
	}

	out.Reach, err = Reach(ctx, a.exp, sources, opts)
	if err != nil {
		return Audit{}, err
	}

	roster, held, err := a.keyholders(ctx, out.Reach)
	if err != nil {
		return Audit{}, err
	}
	out.UnionKeyholders = held.union

	sampled, err := a.sampledVersions(ctx, reachedPackageList(out.Reach.Coexistence))
	if err != nil {
		return Audit{}, err
	}
	out.Signals = make(map[string]Signals, len(roster))
	for _, k := range roster {
		out.Signals[k.Handle] = signalsFor(k, held.count, sampled)
	}

	// Staleness is measured from the instant being asked about, so a query about last October does
	// not call an account dormant for time that had not yet passed.
	at := a.Now
	if opts.Within != Always && opts.Within.From > 0 {
		at = opts.Within.From
	}

	out.Keyholders, out.Scores = Ranked(roster, out.Signals, a.Weights, len(held.count), at)
	out.Cuts = Cuts(out.Reach, out.Keyholders)
	return out, nil
}

func reachedPackageList(coexistence map[string]Set) []string {
	set := reachedPackages(coexistence)
	out := make([]string, 0, len(set))
	for pkg := range set {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}

// knownVersions filters the lockfile's pins down to the versions the graph actually holds, and
// pairs each with the span over which it existed.
//
// Existence is checked by walking the inbound HAS_VERSION edge, which every Version node has
// exactly one of. That makes the check a batched traversal rather than one anchored lookup per pin,
// and it yields the publish time in the same response.
//
// The span matters as much as the membership. A version cannot be in anybody's tree before it was
// published, so a query about a past instant must not start from versions that did not exist yet.
func (a *Auditor) knownVersions(ctx context.Context, pins []lockfile.Pin) ([]Entry, error) {
	wanted := make([]string, 0, len(pins))
	for _, p := range pins {
		urn := graph.VersionURN("npm", p.Name, p.Version)
		if npmURN.MatchString(urn) {
			wanted = append(wanted, urn)
		}
	}

	found := map[string]Interval{}
	for start := 0; start < len(wanted); start += sourceChunk {
		end := min(start+sourceChunk, len(wanted))
		stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Version', sourceProperty: 'urn',
        sourceValues: [%s], relTypes: ['HAS_VERSION'], relDirection: 'incoming',
        maxLen: 1, pathCount: 4, resultLimit: %d}) YIELD path RETURN path`,
			quoteAll(wanted[start:end]), resultLimit)

		recs, err := a.db.Query(ctx, stmt, nil)
		if err != nil {
			return nil, fmt.Errorf("check %d pins against the graph: %w", end-start, err)
		}
		for _, rec := range recs {
			value, _ := rec.Get("path")
			path, ok := value.(neo4j.Path)
			if !ok || len(path.Nodes) == 0 {
				continue
			}
			urn := stringProp(path.Nodes[0].Props, "urn")
			// The source node of the inbound walk is the version itself.
			found[urn] = Interval{From: intProp(path.Nodes[0].Props, "published_at"), To: graph.OpenInterval}
		}
	}

	sources := make([]Entry, 0, len(found))
	for urn, existed := range found {
		if existed.From <= 0 {
			// No publish time recorded, so nothing can be said about when it existed. Treating it
			// as always present would overstate exposure at past instants.
			existed = Always
		}
		sources = append(sources, Entry{URN: urn, Valid: existed})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].URN < sources[j].URN })
	return sources, nil
}

// roster is the by-product of building the keyholder list that the signals need: how many accounts
// hold each reached package, and how many the union graph would have named.
type roster struct {
	count map[string]int
	union int
}

// keyholders maps reached versions to the accounts that can publish them.
//
// A version is reached over a set of spans. The package owning it is publishable by whoever held
// MAINTAINS over an overlapping span. Intersecting the two is what makes this an answer rather than
// a list of everyone who has ever touched the tree.
func (a *Auditor) keyholders(ctx context.Context, reach Result) ([]Keyholder, roster, error) {
	// Reached packages, with the spans over which any of their versions was reachable.
	coexisting := map[string]Set{}
	for versionURN, set := range reach.Coexistence {
		pkg := PackageURNOf(versionURN)
		if pkg == "" {
			continue
		}
		merged := coexisting[pkg]
		for _, i := range set {
			merged, _ = merged.Insert(i)
		}
		coexisting[pkg] = merged
	}

	union := map[string]bool{}
	for versionURN := range reach.Union {
		if pkg := PackageURNOf(versionURN); pkg != "" {
			union[pkg] = true
		}
	}

	packages := make([]string, 0, len(union))
	for pkg := range union {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)

	byHandle := map[string]map[string]Set{}
	unionHandles := map[string]bool{}

	// holders is the accounts per package that held a key over a span the package was reached in.
	// It is the denominator of the solo-maintainer signal, and it has to be counted here rather than
	// derived from the roster afterwards: the roster is keyed by account, so an account holding two
	// packages is one entry, and the per-package count cannot be recovered from it.
	holders := map[string]map[string]bool{}

	// since is when each account's rights begin, taken from the MAINTAINS edge before the query
	// window clips anything.
	since := map[string]int64{}

	for start := 0; start < len(packages); start += sourceChunk {
		end := min(start+sourceChunk, len(packages))
		edges, err := a.maintains(ctx, packages[start:end])
		if err != nil {
			return nil, roster{}, err
		}
		for _, e := range edges {
			unionHandles[e.To] = true

			// e.From is the package, e.To the maintainer, e.Valid the span they held rights over.
			for _, reached := range coexisting[e.From] {
				held := reached.Intersect(e.Valid)
				if held.Empty() {
					continue
				}
				if byHandle[e.To] == nil {
					byHandle[e.To] = map[string]Set{}
				}
				byHandle[e.To][e.From], _ = byHandle[e.To][e.From].Insert(held)

				if holders[e.From] == nil {
					holders[e.From] = map[string]bool{}
				}
				holders[e.From][e.To] = true

				if prev, seen := since[e.To]; !seen || e.Valid.From < prev {
					since[e.To] = e.Valid.From
				}
			}
		}
	}

	out := make([]Keyholder, 0, len(byHandle))
	for handle, through := range byHandle {
		out = append(out, Keyholder{Handle: handle, Through: through, Since: since[handle]})
	}
	// Most packages first, then by handle so the order is stable across runs. The risk ranking
	// reorders this, but the reach order is what a caller sees if it does not.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Packages() != out[j].Packages() {
			return out[i].Packages() > out[j].Packages()
		}
		return out[i].Handle < out[j].Handle
	})

	// Every coexistence-reached package counts, including ones with no maintainer edge at all: a
	// package nobody is recorded as holding still sits in the denominator of "how much of your tree
	// does this account hold".
	count := make(map[string]int, len(coexisting))
	for pkg := range coexisting {
		count[pkg] = len(holders[pkg])
	}
	return out, roster{count: count, union: len(unionHandles)}, nil
}

// maintains reads the MAINTAINS edges pointing at a set of packages, batched.
func (a *Auditor) maintains(ctx context.Context, packages []string) ([]Edge, error) {
	valid := make([]string, 0, len(packages))
	for _, p := range packages {
		if npmPackageURN.MatchString(p) {
			valid = append(valid, p)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}

	stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Package', sourceProperty: 'urn',
      sourceValues: [%s], relTypes: ['MAINTAINS'], relDirection: 'incoming',
      maxLen: 1, pathCount: %d, resultLimit: %d}) YIELD path RETURN path`,
		quoteAll(valid), resultLimit, resultLimit)

	recs, err := a.db.Query(ctx, stmt, nil)
	if err != nil {
		return nil, fmt.Errorf("read maintainers of %d packages: %w", len(valid), err)
	}
	if len(recs) >= resultLimit && len(valid) > 1 {
		half := len(valid) / 2
		left, err := a.maintains(ctx, valid[:half])
		if err != nil {
			return nil, err
		}
		right, err := a.maintains(ctx, valid[half:])
		if err != nil {
			return nil, err
		}
		return append(left, right...), nil
	}

	edges := make([]Edge, 0, len(recs))
	for _, rec := range recs {
		value, _ := rec.Get("path")
		path, ok := value.(neo4j.Path)
		if !ok || len(path.Nodes) != 2 || len(path.Relationships) != 1 {
			continue
		}
		edges = append(edges, Edge{
			From:  stringProp(path.Nodes[0].Props, "urn"),
			To:    stringProp(path.Nodes[1].Props, "handle"),
			Valid: intervalOf(path.Relationships[0].Props),
		})
	}
	return edges, nil
}

var npmPackageURN = regexp.MustCompile(`^pkg:npm/(@[a-z0-9._~-]+/)?[a-z0-9._~-]+$`)

// PackageURNOf strips the version from a version URN. The separator is the last `@`, because a
// scoped name carries one of its own at the front: pkg:npm/@types/node@22.0.0 is the package
// pkg:npm/@types/node.
func PackageURNOf(versionURN string) string {
	at := strings.LastIndex(versionURN, "@")
	if at <= 0 {
		return ""
	}
	return versionURN[:at]
}
