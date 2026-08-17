package query

import (
	"fmt"
	"math"
	"sort"
)

// Weights configure the risk score. They are data rather than constants because the ranking they
// produce is an opinion, and an opinion a reader cannot see and change is a black box. Every weight
// is printed alongside the ranking it produced.
type Weights struct {
	Reach         float64 `json:"reach"`
	Staleness     float64 `json:"staleness"`
	Solo          float64 `json:"solo"`
	NoProvenance  float64 `json:"no_provenance"`
	InstallScript float64 `json:"install_script"`
}

// DefaultWeights leans on reach, because everything else is a modifier on how much damage the reach
// would do. The five sum to one so a score reads as a fraction of the worst case rather than as an
// unbounded number whose scale means nothing.
var DefaultWeights = Weights{
	Reach:         0.40,
	Staleness:     0.15,
	Solo:          0.20,
	NoProvenance:  0.15,
	InstallScript: 0.10,
}

// stalenessCap is where the staleness term saturates. An account silent for five years is treated
// as no worse than one silent for ten: past a point the distinction stops carrying information, and
// letting the term keep growing would let age alone dominate the ranking.
const stalenessCap = 5 * 365 * 24 * 60 * 60

// Term is one component of a score: what it measured, what it contributed, and whether there was
// anything to measure at all.
type Term struct {
	Name string

	// Value is the normalised measurement in [0, 1], and Weight is its share of the total. Known is
	// false when nothing was observed, in which case the term is excluded and its weight
	// redistributed over the rest.
	Value  float64
	Weight float64
	Known  bool

	// Detail is the raw measurement in the units a reader thinks in, for printing next to the term.
	Detail string
}

func (t Term) Contribution() float64 {
	if !t.Known {
		return 0
	}
	return t.Value * t.Weight
}

// Score is a keyholder's risk, decomposed.
//
// An unknown term is dropped and its weight spread across the known ones rather than scored as
// zero. Scoring it zero would rank an account we know nothing about as safer than one we have
// measured and found clean, which inverts the meaning of missing data on a security ranking.
type Score struct {
	Total float64
	Terms []Term
}

// Known reports how many terms carried a measurement, so output can say how much of the score is
// actually evidence.
func (s Score) Known() int {
	n := 0
	for _, t := range s.Terms {
		if t.Known {
			n++
		}
	}
	return n
}

// scoreOf ranks one account.
//
// reachDenominator is the number of packages the whole audit reached, so the reach term reads as
// "how much of your tree does this one account hold" and stays comparable between projects rather
// than being normalised against whoever happens to hold the most here. now is passed in because a
// score that changes with the wall clock cannot be tested.
func scoreOf(k Keyholder, s Signals, w Weights, reachDenominator int, now int64) Score {
	// Log scaled, because the step from one package to two says far more about an account than the
	// step from forty to forty-one.
	reach := 0.0
	if reachDenominator > 0 && k.Packages() > 0 {
		reach = math.Log1p(float64(k.Packages())) / math.Log1p(float64(reachDenominator))
	}

	terms := []Term{
		{
			Name: "reach", Value: math.Min(reach, 1), Weight: w.Reach, Known: reachDenominator > 0,
			Detail: fmt.Sprintf("%d of the %d packages reached", k.Packages(), reachDenominator),
		},
		staleness(s, w, now),
		{
			Name: "solo", Value: s.Solo.Value(), Weight: w.Solo, Known: s.Solo.Known(),
			Detail: fmt.Sprintf("sole maintainer of %d of %d", s.Solo.Count, s.Solo.Of),
		},
		{
			Name: "no provenance", Value: s.NoProvenance.Value(), Weight: w.NoProvenance,
			Known:  s.NoProvenance.Known(),
			Detail: fmt.Sprintf("%d of %d sampled versions", s.NoProvenance.Count, s.NoProvenance.Of),
		},
		{
			Name: "install script", Value: s.InstallScript.Value(), Weight: w.InstallScript,
			Known:  s.InstallScript.Known(),
			Detail: fmt.Sprintf("%d of %d sampled versions", s.InstallScript.Count, s.InstallScript.Of),
		},
	}

	var known float64
	for _, t := range terms {
		if t.Known {
			known += t.Weight
		}
	}
	if known == 0 {
		return Score{Terms: terms}
	}

	// Redistribute proportionally, so the surviving terms keep their relative importance.
	scale := 1 / known
	total := 0.0
	for i := range terms {
		if !terms[i].Known {
			terms[i].Weight = 0
			continue
		}
		terms[i].Weight *= scale
		total += terms[i].Contribution()
	}
	return Score{Total: total, Terms: terms}
}

func staleness(s Signals, w Weights, now int64) Term {
	t := Term{Name: "staleness", Weight: w.Staleness, Detail: "no observed publish"}
	if s.LastPublish <= 0 {
		return t
	}
	idle := float64(now - s.LastPublish)
	t.Known = true
	t.Value = math.Max(0, math.Min(idle/stalenessCap, 1))
	t.Detail = fmt.Sprintf("%.1f years since their last observed publish", idle/(365*24*60*60))
	return t
}

// Ranked orders keyholders by risk, highest first, and returns the scores alongside. Ties break on
// handle so a re-run prints the same order.
func Ranked(keyholders []Keyholder, signals map[string]Signals, w Weights, reachDenominator int, now int64) ([]Keyholder, map[string]Score) {
	scores := make(map[string]Score, len(keyholders))
	for _, k := range keyholders {
		scores[k.Handle] = scoreOf(k, signals[k.Handle], w, reachDenominator, now)
	}

	ranked := make([]Keyholder, len(keyholders))
	copy(ranked, keyholders)
	sort.Slice(ranked, func(i, j int) bool {
		a, b := scores[ranked[i].Handle].Total, scores[ranked[j].Handle].Total
		if a != b {
			return a > b
		}
		return ranked[i].Handle < ranked[j].Handle
	})
	return ranked, scores
}
