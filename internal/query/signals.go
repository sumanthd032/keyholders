package query

import (
	"context"
	"fmt"
	"sort"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// VersionAttrs are the properties of a Version node that say something about how it was published.
//
// Annotated is the one that governs the others. Only versions sampled during ingest have their
// registry document fetched, so provenance, install scripts and the publishing account exist for a
// minority of versions. A version with Annotated false is not a version without provenance; it is a
// version nobody looked at. Conflating the two would report absence of evidence as evidence of
// absence, on exactly the signal a reader would act on.
type VersionAttrs struct {
	URN              string
	PublishedAt      int64
	PublishedBy      string
	HasProvenance    bool
	HasInstallScript bool
	Deprecated       bool
	Annotated        bool
}

func versionAttrs(props map[string]any) VersionAttrs {
	// The annotation pass is the only writer of has_provenance, and it writes the key whatever the
	// value, so a real boolean is what separates "no provenance" from "not looked at". Testing
	// presence alone would not do: a node's property map omits what was never set, but a RETURN
	// projection carries the name with a nil value.
	_, annotated := props["has_provenance"].(bool)
	return VersionAttrs{
		URN:              stringProp(props, "urn"),
		PublishedAt:      intProp(props, "published_at"),
		PublishedBy:      stringProp(props, "published_by"),
		HasProvenance:    boolProp(props, "has_provenance"),
		HasInstallScript: boolProp(props, "has_install_script"),
		Deprecated:       boolProp(props, "deprecated"),
		Annotated:        annotated,
	}
}

func boolProp(props map[string]any, key string) bool {
	v, _ := props[key].(bool)
	return v
}

// Fraction is a proportion together with the sample it was taken over, because a fraction with no
// denominator cannot be told apart from a fraction over nothing. Terms with no observations are
// dropped from the risk score rather than counted as zero.
type Fraction struct {
	Count int
	Of    int
}

func (f Fraction) Known() bool { return f.Of > 0 }

func (f Fraction) Value() float64 {
	if f.Of == 0 {
		return 0
	}
	return float64(f.Count) / float64(f.Of)
}

// Signals are the per-account properties the risk score reads.
type Signals struct {
	// Solo counts the packages this account holds where it is the only maintainer. Sole control
	// means no second pair of eyes on a publish, which is the condition every recent npm compromise
	// has had in common.
	Solo Fraction

	// NoProvenance and InstallScript are taken over the sampled versions of the packages this
	// account holds. An install script runs on install, so it needs no import to execute; provenance
	// is what makes a publish checkable against the repository it claims to come from.
	NoProvenance  Fraction
	InstallScript Fraction

	// LastPublish is the most recent publish observed from this account, zero when none was. It
	// comes from published_by on sampled versions, so it is a lower bound on how recently the
	// account was active, never an upper one.
	LastPublish int64

	// LastRelease is the most recent publish of any version of the packages this account holds,
	// whoever made it. Every version node carries a publish time, so this has complete coverage over
	// the sampled set and answers the neighbouring question: whether the code is still moving.
	LastRelease int64
}

// signalsFor derives one account's signals.
//
// held is how many accounts hold each package, which the roster pass already read. sampled maps a
// package to the versions of it whose registry document was fetched, which is where the publisher
// evidence lives.
func signalsFor(k Keyholder, held map[string]int, sampled map[string][]VersionAttrs) Signals {
	var s Signals
	for pkg := range k.Through {
		s.Solo.Of++
		if held[pkg] == 1 {
			s.Solo.Count++
		}

		for _, a := range sampled[pkg] {
			if a.PublishedAt > s.LastRelease {
				s.LastRelease = a.PublishedAt
			}
			if a.PublishedBy == k.Handle && a.PublishedAt > s.LastPublish {
				s.LastPublish = a.PublishedAt
			}
			if !a.Annotated {
				continue
			}
			s.NoProvenance.Of++
			if !a.HasProvenance {
				s.NoProvenance.Count++
			}
			s.InstallScript.Of++
			if a.HasInstallScript {
				s.InstallScript.Count++
			}
		}
	}
	return s
}

// sampledVersions reads the annotated versions of a set of packages.
//
// The versions the search walked to arrive with their properties already attached, but they are
// almost never the annotated ones: annotation covers the versions current at each ingest epoch,
// three to eight per package, against a hundred thousand version nodes in total. Taking the
// publisher signals off the reached versions alone would put the provenance denominator at zero for
// nearly every account.
//
// The selection runs over SAMPLED rather than HAS_VERSION with a predicate. Both reach the same
// nodes, but a predicate on the version walks the package's whole timeline to evaluate it, which
// measured 1.7 seconds for typescript's 3,795 versions against 6 ms for a small package. A separate
// edge type gets its own traversal index, so the same answer becomes one batched call whose cost
// tracks the sampled versions rather than the total.
func (a *Auditor) sampledVersions(ctx context.Context, packages []string) (map[string][]VersionAttrs, error) {
	valid := make([]string, 0, len(packages))
	for _, p := range packages {
		if npmPackageURN.MatchString(p) {
			valid = append(valid, p)
		}
	}

	out := make(map[string][]VersionAttrs, len(valid))
	for start := 0; start < len(valid); start += sourceChunk {
		end := min(start+sourceChunk, len(valid))
		if err := a.readSampled(ctx, valid[start:end], out); err != nil {
			return nil, err
		}
	}
	for _, attrs := range out {
		sort.Slice(attrs, func(i, j int) bool { return attrs[i].PublishedAt < attrs[j].PublishedAt })
	}
	return out, nil
}

func (a *Auditor) readSampled(ctx context.Context, packages []string, out map[string][]VersionAttrs) error {
	stmt := fmt.Sprintf(`CALL algo.MSpaths({sourceLabel: 'Package', sourceProperty: 'urn',
      sourceValues: [%s], relTypes: ['SAMPLED'], relDirection: 'outgoing',
      maxLen: 1, pathCount: %d, resultLimit: %d}) YIELD path RETURN path`,
		quoteAll(packages), resultLimit, resultLimit)

	a.exp.Reads++
	recs, err := a.db.Query(ctx, stmt, nil)
	if err != nil {
		return fmt.Errorf("read sampled versions of %d packages: %w", len(packages), err)
	}
	if len(recs) >= resultLimit && len(packages) > 1 {
		half := len(packages) / 2
		if err := a.readSampled(ctx, packages[:half], out); err != nil {
			return err
		}
		return a.readSampled(ctx, packages[half:], out)
	}

	for _, rec := range recs {
		value, _ := rec.Get("path")
		path, ok := value.(neo4j.Path)
		if !ok || len(path.Nodes) != 2 {
			continue
		}
		pkg := stringProp(path.Nodes[0].Props, "urn")
		if pkg == "" {
			continue
		}
		attrs := versionAttrs(path.Nodes[1].Props)
		a.exp.Attrs[attrs.URN] = attrs
		out[pkg] = append(out[pkg], attrs)
	}
	return nil
}
