package query

import "testing"

// TestVersionAttrsAnnotated pins how a version is told apart from one nobody looked at. Only versions
// sampled during ingest have their registry document fetched, so a missing has_provenance means no
// evidence rather than no provenance.
func TestVersionAttrsAnnotated(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		annotated  bool
		provenance bool
	}{
		{
			name:  "never sampled",
			props: map[string]any{"urn": v("a")},
		},
		{
			// A RETURN projection carries the property name with a nil value even where the node never
			// had it, so presence of the key is not enough to tell the two cases apart.
			name:  "projected but unset",
			props: map[string]any{"urn": v("a"), "has_provenance": nil},
		},
		{
			name:      "sampled, no provenance",
			props:     map[string]any{"urn": v("a"), "has_provenance": false},
			annotated: true,
		},
		{
			name:       "sampled, provenance present",
			props:      map[string]any{"urn": v("a"), "has_provenance": true},
			annotated:  true,
			provenance: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionAttrs(tc.props)
			if got.Annotated != tc.annotated {
				t.Errorf("Annotated = %v, want %v", got.Annotated, tc.annotated)
			}
			if got.HasProvenance != tc.provenance {
				t.Errorf("HasProvenance = %v, want %v", got.HasProvenance, tc.provenance)
			}
		})
	}
}

// TestUnsampledVersionsStayOutOfTheDenominator is the reason Fraction carries its denominator at all.
// Counting a version nobody fetched as one without provenance would report absence of evidence as
// evidence of absence, on precisely the signal a reader would act on.
func TestUnsampledVersionsStayOutOfTheDenominator(t *testing.T) {
	sampled := map[string][]VersionAttrs{
		pkg("A"): {
			{URN: v("A"), PublishedAt: 100, Annotated: true, HasProvenance: false},
			{URN: v("A"), PublishedAt: 200, Annotated: true, HasProvenance: true},
			{URN: v("A"), PublishedAt: 300}, // walked to by the search, never fetched
		},
	}

	got := signalsFor(keyholderOf("alice", "A"), map[string]int{pkg("A"): 2}, sampled)

	if got.NoProvenance.Of != 2 {
		t.Errorf("provenance denominator = %d, want 2: only annotated versions are evidence",
			got.NoProvenance.Of)
	}
	if got.NoProvenance.Count != 1 {
		t.Errorf("provenance count = %d, want 1", got.NoProvenance.Count)
	}
	// The unannotated version still dates the package: every version node carries a publish time.
	if got.LastRelease != 300 {
		t.Errorf("LastRelease = %d, want 300", got.LastRelease)
	}
}

func TestSoloCountsOnlyPackagesHeldAlone(t *testing.T) {
	held := map[string]int{pkg("A"): 1, pkg("B"): 4, pkg("C"): 1}
	got := signalsFor(keyholderOf("alice", "A", "B", "C"), held, nil)

	if got.Solo.Of != 3 {
		t.Errorf("solo denominator = %d, want 3", got.Solo.Of)
	}
	if got.Solo.Count != 2 {
		t.Errorf("solo count = %d, want 2 (A and C)", got.Solo.Count)
	}
}

// TestLastPublishIsThisAccountOnly separates "this account is still active" from "this package is
// still moving". Reading the package's latest release as the account's own publish would make a
// dormant maintainer on a busy package look active, which is the wrong way round for a staleness
// signal.
func TestLastPublishIsThisAccountOnly(t *testing.T) {
	sampled := map[string][]VersionAttrs{
		pkg("A"): {
			{URN: v("A"), PublishedAt: 100, PublishedBy: "alice", Annotated: true},
			{URN: v("A"), PublishedAt: 900, PublishedBy: "bob", Annotated: true},
		},
	}

	got := signalsFor(keyholderOf("alice", "A"), map[string]int{pkg("A"): 2}, sampled)

	if got.LastPublish != 100 {
		t.Errorf("LastPublish = %d, want 100: bob's release is not alice's activity", got.LastPublish)
	}
	if got.LastRelease != 900 {
		t.Errorf("LastRelease = %d, want 900", got.LastRelease)
	}
}

func TestInstallScriptFraction(t *testing.T) {
	sampled := map[string][]VersionAttrs{
		pkg("A"): {
			{URN: v("A"), PublishedAt: 1, Annotated: true, HasInstallScript: true},
			{URN: v("A"), PublishedAt: 2, Annotated: true},
		},
	}

	got := signalsFor(keyholderOf("alice", "A"), map[string]int{pkg("A"): 1}, sampled)

	if got.InstallScript.Count != 1 || got.InstallScript.Of != 2 {
		t.Errorf("install script = %d/%d, want 1/2", got.InstallScript.Count, got.InstallScript.Of)
	}
	if !got.InstallScript.Known() {
		t.Error("a fraction over two sampled versions is known")
	}
}

func TestFractionWithNoSampleIsUnknown(t *testing.T) {
	got := signalsFor(keyholderOf("alice", "A"), map[string]int{pkg("A"): 1}, nil)

	if got.NoProvenance.Known() || got.InstallScript.Known() {
		t.Error("no sampled versions means the publisher signals are unknown, not zero")
	}
	if got.LastPublish != 0 {
		t.Errorf("LastPublish = %d, want 0 with nothing observed", got.LastPublish)
	}
	// Solo needs no sample: it comes from the maintainer edges the roster already read.
	if !got.Solo.Known() {
		t.Error("solo is measurable without any sampled version")
	}
}
