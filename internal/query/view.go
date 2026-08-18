package query

import (
	"sort"
	"time"
)

// Counts is the package and version tallies behind the dual counter: coexistence next to union, at
// both granularities. Computed once here rather than at each caller, since the CLI's human readable
// report and the API's JSON view both need the identical numbers.
type Counts struct {
	CoexistencePackages, CoexistenceVersions int
	UnionPackages, UnionVersions             int
}

// Counts derives package level tallies from the version level reach sets. Two versions of one
// package can both be reached without the package count doubling, which is why this exists rather
// than reading len(Reach.Coexistence) directly wherever a package count is wanted.
func (a Audit) Counts() Counts {
	co := map[string]bool{}
	for versionURN := range a.Reach.Coexistence {
		if p := PackageURNOf(versionURN); p != "" {
			co[p] = true
		}
	}
	un := map[string]bool{}
	for versionURN := range a.Reach.Union {
		if p := PackageURNOf(versionURN); p != "" {
			un[p] = true
		}
	}
	return Counts{
		CoexistencePackages: len(co), CoexistenceVersions: len(a.Reach.Coexistence),
		UnionPackages: len(un), UnionVersions: len(a.Reach.Union),
	}
}

// FractionView is Fraction shaped for a consumer that cannot call Known() itself.
type FractionView struct {
	Count int  `json:"count"`
	Of    int  `json:"of"`
	Known bool `json:"known"`
}

func viewFraction(f Fraction) FractionView {
	return FractionView{Count: f.Count, Of: f.Of, Known: f.Known()}
}

// TermView is one risk term, with its contribution already computed so a consumer can render or
// re-weight the formula without reimplementing Contribution().
type TermView struct {
	Name         string  `json:"name"`
	Value        float64 `json:"value"`
	Weight       float64 `json:"weight"`
	Known        bool    `json:"known"`
	Contribution float64 `json:"contribution"`
	Detail       string  `json:"detail"`
}

// KeyholderView is one ranked account, decomposed the way the risk score itself is: every term
// individually inspectable, per the project's own transparency requirement on the score.
type KeyholderView struct {
	Handle        string        `json:"handle"`
	Packages      int           `json:"packages"`
	Holds         []string      `json:"holds"`
	Since         int64         `json:"since,omitempty"`
	Risk          float64       `json:"risk"`
	Terms         []TermView    `json:"risk_terms"`
	Solo          FractionView  `json:"solo"`
	NoProvenance  FractionView  `json:"no_provenance"`
	InstallScript FractionView  `json:"install_script"`
	LastPublish   int64         `json:"last_publish,omitempty"`
	LastRelease   int64         `json:"last_release,omitempty"`
}

// CutView is one removal analysis result: what is lost if this account is taken out.
type CutView struct {
	Handle        string   `json:"handle"`
	Controls      int      `json:"controls"`
	PackagesLost  int      `json:"packages_lost"`
	VersionsLost  int      `json:"versions_lost"`
	Downstream    int      `json:"downstream_lost"`
	Orphaned      []string `json:"orphaned"`
	Irreplaceable bool     `json:"irreplaceable"`
}

// AuditView is an Audit shaped for serialization: every fraction keeps its denominator and every
// score term is broken out individually, so a consumer can re-derive the ranking or re-weight it
// rather than trusting a single opaque number.
type AuditView struct {
	Project              string          `json:"project"`
	Format               string          `json:"format"`
	Pins                 int             `json:"pins"`
	Sources              int             `json:"sources_in_graph"`
	Keyholders           []KeyholderView `json:"keyholders"`
	KeyholderCount       int             `json:"keyholder_count"`
	UnionKeyholders      int             `json:"union_keyholder_count"`
	PhantomKeyholders    int             `json:"phantom_keyholder_count"`
	PackagesReached      int             `json:"packages_reached"`
	UnionPackagesReached int             `json:"union_packages_reached"`
	VersionsReached      int             `json:"versions_reached"`
	UnionVersionsReached int             `json:"union_versions_reached"`
	Cuts                 []CutView       `json:"cuts"`
	Weights              Weights         `json:"weights"`
	Depth                int             `json:"depth"`
	Truncated            bool            `json:"truncated"`
	Kappa                float64         `json:"kappa"`
	ElapsedMS            int64           `json:"elapsed_ms"`
	Graph                GraphView       `json:"graph"`
}

// GraphNodeView is one package the search reached, collapsed from every version of it the search
// touched: a canvas rendering thousands of individual versions when the interesting unit is which
// packages are involved would show detail nobody asked for at the cost of the shape they did.
type GraphNodeView struct {
	Package    string `json:"package"`
	Coexistent bool   `json:"coexistent"`
}

// GraphEdgeView is one resolution edge between two packages, at least one of whose underlying
// version level edges the search read. Coexistent is true only when both endpoints are themselves
// coexistence reached; it is an approximation of "this edge lies on a coexistence path", not a
// per-edge replay of the interval intersection, since Result records maximal intervals per node,
// not a coexistence flag per traversed edge.
type GraphEdgeView struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Coexistent bool   `json:"coexistent"`
}

type GraphView struct {
	Nodes []GraphNodeView `json:"nodes"`
	Edges []GraphEdgeView `json:"edges"`
}

// newGraphView collapses a's version level reach graph to package granularity for the canvas.
func newGraphView(a Audit) GraphView {
	coexistent := map[string]bool{}
	for versionURN := range a.Reach.Coexistence {
		if pkg := PackageURNOf(versionURN); pkg != "" {
			coexistent[pkg] = true
		}
	}

	nodes := map[string]bool{}
	for versionURN := range a.Reach.Union {
		if pkg := PackageURNOf(versionURN); pkg != "" {
			nodes[pkg] = true
		}
	}

	type edgeKey struct{ from, to string }
	edges := map[edgeKey]bool{}
	for _, list := range a.Reach.Edges {
		for _, e := range list {
			from, to := PackageURNOf(e.From), PackageURNOf(e.To)
			if from == "" || to == "" || from == to {
				continue
			}
			edges[edgeKey{from, to}] = true
		}
	}

	view := GraphView{Nodes: make([]GraphNodeView, 0, len(nodes)), Edges: make([]GraphEdgeView, 0, len(edges))}
	for pkg := range nodes {
		view.Nodes = append(view.Nodes, GraphNodeView{Package: pkg, Coexistent: coexistent[pkg]})
	}
	for k := range edges {
		view.Edges = append(view.Edges, GraphEdgeView{From: k.from, To: k.to, Coexistent: coexistent[k.from] && coexistent[k.to]})
	}
	return view
}

// NewAuditView builds the serializable view of a, the single definition the CLI's --json flag and
// the web API both read from, so the two surfaces cannot silently drift into different shapes for
// the same computation.
func NewAuditView(a Audit, elapsed time.Duration) AuditView {
	r := a.Counts()
	view := AuditView{
		Project: a.Project, Format: a.Format, Pins: a.Pins, Sources: a.Sources,
		KeyholderCount: len(a.Keyholders), UnionKeyholders: a.UnionKeyholders,
		PhantomKeyholders: a.PhantomKeyholders(),
		PackagesReached:   r.CoexistencePackages, UnionPackagesReached: r.UnionPackages,
		VersionsReached: r.CoexistenceVersions, UnionVersionsReached: r.UnionVersions,
		Weights: a.Weights,
		Depth:   a.Reach.Depth, Truncated: a.Reach.Truncated,
		Kappa: a.Reach.Kappa, ElapsedMS: elapsed.Milliseconds(),
		Graph: newGraphView(a),
	}

	for _, k := range a.Keyholders {
		s, score := a.Signals[k.Handle], a.Scores[k.Handle]
		e := KeyholderView{
			Handle: k.Handle, Packages: k.Packages(), Since: k.Since, Risk: score.Total,
			Solo: viewFraction(s.Solo), NoProvenance: viewFraction(s.NoProvenance),
			InstallScript: viewFraction(s.InstallScript),
			LastPublish:   s.LastPublish, LastRelease: s.LastRelease,
		}
		for pkg := range k.Through {
			e.Holds = append(e.Holds, pkg)
		}
		sort.Strings(e.Holds)
		for _, t := range score.Terms {
			e.Terms = append(e.Terms, TermView{
				Name: t.Name, Value: t.Value, Weight: t.Weight, Known: t.Known,
				Contribution: t.Contribution(), Detail: t.Detail,
			})
		}
		view.Keyholders = append(view.Keyholders, e)
	}
	for _, c := range a.Cuts {
		view.Cuts = append(view.Cuts, CutView{
			Handle: c.Handle, Controls: c.Controls, PackagesLost: c.Packages,
			VersionsLost: c.Versions, Downstream: c.Beyond, Orphaned: c.Orphaned,
			Irreplaceable: c.Irreplaceable(),
		})
	}
	return view
}
