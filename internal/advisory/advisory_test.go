package advisory

import "testing"

func TestMatchAffectedPrefersRangesOverExplicitVersions(t *testing.T) {
	aff := Affected{
		Ranges:   []Range{{Type: "SEMVER", Events: []Event{{Introduced: "0"}, {Fixed: "1.1.0"}}}},
		Versions: []string{"9.9.9"}, // would match nothing real; proves this list is ignored when ranges exist
	}
	got := matchAffected(aff, releases("1.0.0", "1.1.0"))
	if len(got) != 1 || got[0].Version != "1.0.0" {
		t.Fatalf("matchAffected() = %v, want just 1.0.0", got)
	}
	if got[0].introduced != "0" || got[0].fixed != "1.1.0" {
		t.Errorf("boundary strings = (%q, %q), want (\"0\", \"1.1.0\")", got[0].introduced, got[0].fixed)
	}
}

func TestMatchAffectedFallsBackToExplicitVersions(t *testing.T) {
	aff := Affected{Versions: []string{"1.0.0", "1.2.0"}}
	got := matchAffected(aff, releases("1.0.0", "1.1.0", "1.2.0"))
	if len(got) != 2 {
		t.Fatalf("matchAffected() = %v, want exactly the two listed versions", got)
	}
	for _, m := range got {
		if m.Version != "1.0.0" && m.Version != "1.2.0" {
			t.Errorf("unexpected match %q", m.Version)
		}
	}
}

func TestSeverityPrefersDatabaseSpecific(t *testing.T) {
	r := Record{Severity: []Severity{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}}
	r.DatabaseSpecific.Severity = "HIGH"
	if got := severity(r); got != "HIGH" {
		t.Errorf("severity() = %q, want HIGH", got)
	}
}

func TestSeverityFallsBackToScoringMethodName(t *testing.T) {
	r := Record{Severity: []Severity{{Type: "CVSS_V3", Score: "CVSS:3.1/..."}}}
	if got := severity(r); got != "CVSS_V3" {
		t.Errorf("severity() = %q, want CVSS_V3", got)
	}
}

func TestCvssScoreRejectsVectorStrings(t *testing.T) {
	r := Record{Severity: []Severity{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}}
	if _, ok := cvssScore(r); ok {
		t.Error("cvssScore() should not extract a number from a CVSS vector string")
	}
}

func TestCvssScoreAcceptsPlainNumbers(t *testing.T) {
	r := Record{Severity: []Severity{{Type: "Ubuntu", Score: "7.5"}}}
	got, ok := cvssScore(r)
	if !ok || got != 7.5 {
		t.Errorf("cvssScore() = (%v, %v), want (7.5, true)", got, ok)
	}
}

func TestPublishedAtParsesRFC3339(t *testing.T) {
	got := publishedAt("2024-03-15T00:00:00Z")
	if got <= 0 {
		t.Errorf("publishedAt() = %d, want a positive unix timestamp", got)
	}
}

func TestPublishedAtRejectsGarbage(t *testing.T) {
	if got := publishedAt("not a date"); got != 0 {
		t.Errorf("publishedAt(garbage) = %d, want 0", got)
	}
}
