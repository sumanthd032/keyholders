package query

import "sort"

// Cut is what a project stops reaching when one account is taken out of the graph.
//
// Taking an account out means removing every version of every package it can publish to. If a
// dependency is still reachable afterwards there is a route around this account; if it is not, every
// path to that dependency runs through a package this account controls, which is the definition of a
// dominator on the lockfile's flowgraph. A lockfile supplies the single entry that makes dominance
// well defined, which is why this is an audit-scope answer and has no ecosystem-wide counterpart.
//
// It is computed by replaying the search with those nodes removed rather than by a dominator
// algorithm. The edges are already in memory from the audit, a replay is a few thousand map
// operations, and brute force gives the exact answer for every account at once without a second
// implementation of the traversal to keep in agreement with the first.
type Cut struct {
	Handle string

	// Controls is how many reached packages this account can publish to.
	Controls int

	// Packages and Versions are what becomes unreachable, which always includes what the account
	// controls directly.
	Packages int
	Versions int

	// Beyond is the packages lost that this account does not itself control: dependencies that were
	// only ever reachable through one of its packages. An account with Beyond above zero is a
	// chokepoint rather than merely a widely trusted publisher.
	Beyond int

	// Orphaned names the packages counted in Beyond, most useful part of the answer and short enough
	// to print.
	Orphaned []string
}

// Irreplaceable reports whether removing this account costs the project reach it cannot get back by
// any other route. Every account is trivially irreplaceable for its own packages, so the test is
// whether anything downstream falls with them.
func (c Cut) Irreplaceable() bool { return c.Beyond > 0 }

// Cuts computes the removal analysis for every keyholder on the roster.
func Cuts(reach Result, keyholders []Keyholder) []Cut {
	// Which reached versions belong to which package, so an account's packages map back to the nodes
	// that have to come out.
	versionsOf := map[string][]string{}
	for versionURN := range reach.Coexistence {
		if pkg := PackageURNOf(versionURN); pkg != "" {
			versionsOf[pkg] = append(versionsOf[pkg], versionURN)
		}
	}

	before := reachedPackages(reach.Coexistence)

	out := make([]Cut, 0, len(keyholders))
	for _, k := range keyholders {
		blocked := map[string]bool{}
		for pkg := range k.Through {
			for _, urn := range versionsOf[pkg] {
				blocked[urn] = true
			}
		}
		if len(blocked) == 0 {
			continue
		}

		after := replay(reach.Sources, reach.Opts, reach.Edges, blocked, nil)
		lost := reachedPackages(after)

		c := Cut{
			Handle:   k.Handle,
			Controls: k.Packages(),
			Versions: len(reach.Coexistence) - len(after),
		}
		for pkg := range before {
			if lost[pkg] {
				continue
			}
			c.Packages++
			if _, held := k.Through[pkg]; !held {
				c.Beyond++
				c.Orphaned = append(c.Orphaned, pkg)
			}
		}
		sort.Strings(c.Orphaned)
		out = append(out, c)
	}

	// Widest cut first, then by handle so a re-run prints the same order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Packages != out[j].Packages {
			return out[i].Packages > out[j].Packages
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

func reachedPackages(coexistence map[string]Set) map[string]bool {
	out := map[string]bool{}
	for versionURN := range coexistence {
		if pkg := PackageURNOf(versionURN); pkg != "" {
			out[pkg] = true
		}
	}
	return out
}
