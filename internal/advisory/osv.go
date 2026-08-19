// Package advisory ingests OSV's bulk npm vulnerability feed into Advisory nodes and AFFECTS edges
// against the version timelines already in the graph.
package advisory

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// npmFeedURL is OSV's per-ecosystem bulk export: a zip holding one JSON document per advisory,
// GCS virtual-hosted style. Documented at https://google.github.io/osv.dev/data/#zip-files.
const npmFeedURL = "https://osv-vulnerabilities.storage.googleapis.com/npm/all.zip"

// fetchTimeout is generous relative to the per-package registry client's 60 second timeout: this is
// one bulk export, measured at 209 MB and 143 seconds end to end, not many small rate-limited calls,
// so the request needs minutes, not seconds, to legitimately succeed rather than hang.
const fetchTimeout = 10 * time.Minute

const cacheFile = "osv-npm-all.zip"

// Record is the subset of the OSV schema (https://ossf.github.io/osv-schema/) this package reads.
// An advisory carries far more, credits, long form details, aliases, none of which the graph needs.
type Record struct {
	ID        string     `json:"id"`
	Summary   string     `json:"summary"`
	Published string     `json:"published"`
	Severity  []Severity `json:"severity"`
	Affected  []Affected `json:"affected"`
	// DatabaseSpecific carries the source's own fields. GHSA records, which is nearly everything in
	// OSV's npm feed, put a coarse category here: LOW, MODERATE, HIGH, CRITICAL. This project reads
	// that instead of computing a score off a CVSS vector string, which the standard schema's
	// severity array carries no parser for. See D47.
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

// Severity is one scoring method's result. Score is a vector string for CVSS types ("CVSS:3.1/AV:N/
// ...") and a plain number for a handful of others; this package only trusts the plain-number case,
// see cvssScore.
type Severity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

// Affected is one package's vulnerable window within one advisory. Ecosystem, name identify the
// package; Ranges or the explicit Versions list identify which published versions are in it.
type Affected struct {
	Package  Package  `json:"package"`
	Ranges   []Range  `json:"ranges"`
	Versions []string `json:"versions"`
}

type Package struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

// Range is one interval definition. Type is ECOSYSTEM, SEMVER, or GIT; this package treats the first
// two identically, comparing every version string with the project's own semver parser, since real
// npm advisory ranges use ordinary semver strings regardless of which type they are labelled. GIT
// ranges, keyed on commit hashes rather than versions, are not evaluated.
type Range struct {
	Type   string  `json:"type"`
	Events []Event `json:"events"`
}

// Event is one transition in a Range's timeline. Exactly one field is set per OSV's schema.
type Event struct {
	Introduced   string `json:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty"`
}

// Fetch downloads and parses OSV's npm bulk feed. A cached copy at cacheDir/osv-npm-all.zip younger
// than maxAge is reused rather than refetched: the feed is a ~200 MB download taking minutes, worth
// avoiding on every rerun during development, and it changes on the order of hours, not seconds.
//
// This does not go through registry.Client, which every other bulk document in this project reads
// through: that client's 60 second HTTP timeout is sized for many small, rate-limited per-package
// documents, and cuts off partway through a single download this large. Reusing it here produced a
// context deadline exceeded on the very first run.
func Fetch(ctx context.Context, cacheDir string, maxAge time.Duration) ([]Record, error) {
	body, err := fetchZip(ctx, cacheDir, maxAge)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("open OSV feed zip: %w", err)
	}

	records := make([]Record, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rec, err := readRecord(f)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f.Name, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func readRecord(f *zip.File) (Record, error) {
	rc, err := f.Open()
	if err != nil {
		return Record{}, err
	}
	defer rc.Close()

	var rec Record
	if err := json.NewDecoder(rc).Decode(&rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// fetchZip returns the feed's raw bytes, from the on-disk cache when it is fresh enough, otherwise
// downloaded and written back to the same path.
func fetchZip(ctx context.Context, cacheDir string, maxAge time.Duration) ([]byte, error) {
	path := filepath.Join(cacheDir, cacheFile)
	if info, err := os.Stat(path); err == nil && maxAge > 0 && time.Since(info.ModTime()) < maxAge {
		body, err := os.ReadFile(path)
		if err == nil {
			return body, nil
		}
		// A cache file that stat succeeds on but fails to read is treated as a miss rather than an
		// error: refetching costs minutes, not correctness.
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, npmFeedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for OSV npm feed: %w", err)
	}
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch OSV npm feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch OSV npm feed: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read OSV npm feed: %w", err)
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", cacheDir, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return nil, fmt.Errorf("cache OSV npm feed at %s: %w", path, err)
	}
	return body, nil
}

// publishedAt parses the RFC3339 published timestamp OSV requires. A record that fails to parse it,
// which has not happened in practice, is written with 0 rather than dropped: the advisory and its
// AFFECTS edges are still real even without a trustworthy publish instant.
func publishedAt(rfc3339 string) int64 {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// severity picks the category this project stores: the source's own label when present, since that
// is what a reader recognises, falling back to the first scoring method's raw type name so the field
// is never silently empty just because the record used CVSS instead of a database-specific category.
func severity(r Record) string {
	if r.DatabaseSpecific.Severity != "" {
		return r.DatabaseSpecific.Severity
	}
	if len(r.Severity) > 0 {
		return r.Severity[0].Type
	}
	return ""
}

// cvssScore returns a numeric score only when a severity entry's score is already a plain number.
// CVSS_V2 and CVSS_V3 report a vector string, "CVSS:3.1/AV:N/AC:L/...", which requires the CVSS base
// score formula to reduce to a single float; that formula is not implemented here, so a vector string
// is left unscored rather than approximated. See D47.
func cvssScore(r Record) (float64, bool) {
	for _, s := range r.Severity {
		// ParseFloat requires the whole string to be numeric, so a vector string, "CVSS:3.1/AV:N/
		// ...", is rejected outright rather than partially parsed.
		if f, err := strconv.ParseFloat(s.Score, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
