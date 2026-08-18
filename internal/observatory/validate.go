package observatory

import (
	"math"
	"sort"

	"github.com/sumanthd032/keyholders/internal/sketch"
)

// ValidationResult is one package's estimated reach checked against its exact count, both computed
// from the identical PKG_RESOLVES edges for one epoch.
type ValidationResult struct {
	Package  string
	Exact    int
	Estimate int
}

// RelativeError is |Estimate-Exact| / Exact. A package with no dependents at all is defined as zero
// error only when the estimate agrees at zero too; an estimator that invents dependents for an
// isolated package is not a rounding artefact and reporting it as undefined would hide exactly the
// kind of failure this validation exists to catch.
func (r ValidationResult) RelativeError() float64 {
	if r.Exact == 0 {
		if r.Estimate == 0 {
			return 0
		}
		return 1
	}
	return math.Abs(float64(r.Estimate-r.Exact)) / float64(r.Exact)
}

// Validate runs one epoch's edges through two engines built from the identical input, one backed by
// ExactSet and one by whatever estimator the caller is validating, and reports both counts for every
// package in sample. Running the exact engine over the same nodes and edges is not a smaller version
// of the estimate, it is the fixpoint the estimate is approximating, computed exactly because at this
// scale, a sample rather than the whole registry, an exact set is affordable. Comparing the two this
// way validates the propagation and estimation this package actually runs in production, rather than
// a synthetic cardinality test standing in for it.
func Validate(nodes []sketch.NodeID, edges []sketch.Edge, sample []sketch.NodeID, newEstimator sketch.NewSketch) []ValidationResult {
	exact := (&sketch.Engine{New: sketch.NewExactSet}).RunEpoch(nodes, edges)
	estimate := (&sketch.Engine{New: newEstimator}).RunEpoch(nodes, edges)

	results := make([]ValidationResult, 0, len(sample))
	for _, p := range sample {
		results = append(results, ValidationResult{
			Package:  string(p),
			Exact:    exact[p].Count(),
			Estimate: estimate[p].Count(),
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Exact > results[j].Exact })
	return results
}
