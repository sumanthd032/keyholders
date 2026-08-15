package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

const depsDevBase = "https://api.deps.dev/v3alpha/systems/npm/packages/"

// timelineMaxAge is how long a stored version timeline is trusted without revalidation. A day is
// short enough that a package published this morning is picked up by tomorrow's run, and long
// enough that resuming an interrupted ingest costs no requests at all.
const timelineMaxAge = 24 * time.Hour

// Release is one published version of a package. publishedAt is the field the whole temporal layer
// rests on: without it there are no validity windows and no coexistence reasoning.
type Release struct {
	Version     string
	PublishedAt time.Time
	IsDefault   bool
	Deprecated  bool
}

// Timeline returns every published version of a package in the order deps.dev reports it.
func (c *Client) Timeline(ctx context.Context, name string) ([]Release, error) {
	body, err := c.Get(ctx, depsDevBase+escapePackage(name), timelineMaxAge)
	if err != nil {
		return nil, err
	}
	return parseTimeline(name, body)
}

func parseTimeline(name string, body []byte) ([]Release, error) {
	var doc struct {
		Versions []struct {
			VersionKey struct {
				Version string `json:"version"`
			} `json:"versionKey"`
			PublishedAt  string `json:"publishedAt"`
			IsDefault    bool   `json:"isDefault"`
			IsDeprecated bool   `json:"isDeprecated"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse deps.dev timeline for %s: %w", name, err)
	}

	releases := make([]Release, 0, len(doc.Versions))
	for _, v := range doc.Versions {
		r := Release{
			Version:    v.VersionKey.Version,
			IsDefault:  v.IsDefault,
			Deprecated: v.IsDeprecated,
		}
		// A version with no publish time cannot be placed on the timeline. These exist, so they are
		// carried with a zero time and dropped by the caller rather than failing the package.
		if v.PublishedAt != "" {
			t, err := time.Parse(time.RFC3339, v.PublishedAt)
			if err != nil {
				return nil, fmt.Errorf("parse publishedAt %q for %s@%s: %w", v.PublishedAt, name, r.Version, err)
			}
			r.PublishedAt = t
		}
		releases = append(releases, r)
	}
	return releases, nil
}

// Dependency is one declared dependency edge: a specific version of one package declaring a range
// against another package, together with the version deps.dev resolved that range to.
//
// The resolved version is not used to build RESOLVES_TO, which is materialized from our own semver
// matcher against the version timeline so that it carries validity windows. It is kept because it
// is an independent implementation's answer for the same range at the same instant, which makes it
// a free differential test for the matcher.
type Dependency struct {
	FromName    string
	FromVersion string
	ToName      string
	ToVersion   string
	Requirement string
	Direct      bool
}

// Dependencies returns the resolved dependency graph for one version. The response is the whole
// transitive closure, not just the direct dependencies, and every edge carries the declared
// requirement string. One call for express is 15 KB and yields 128 edges across 71 packages, so a
// closure fetched for a popular package supplies declared ranges for many packages at once.
func (c *Client) Dependencies(ctx context.Context, name, version string) ([]Dependency, error) {
	u := depsDevBase + escapePackage(name) + "/versions/" + url.PathEscape(version) + ":dependencies"
	body, err := c.Get(ctx, u, timelineMaxAge)
	if err != nil {
		return nil, err
	}
	return parseDependencies(name, version, body)
}

func parseDependencies(name, version string, body []byte) ([]Dependency, error) {
	var doc struct {
		Nodes []struct {
			VersionKey struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"versionKey"`
			Relation string `json:"relation"`
		} `json:"nodes"`
		Edges []struct {
			FromNode    int    `json:"fromNode"`
			ToNode      int    `json:"toNode"`
			Requirement string `json:"requirement"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse deps.dev dependencies for %s@%s: %w", name, version, err)
	}

	deps := make([]Dependency, 0, len(doc.Edges))
	for _, e := range doc.Edges {
		if e.FromNode < 0 || e.FromNode >= len(doc.Nodes) || e.ToNode < 0 || e.ToNode >= len(doc.Nodes) {
			return nil, fmt.Errorf("deps.dev edge for %s@%s references node out of range", name, version)
		}
		from, to := doc.Nodes[e.FromNode], doc.Nodes[e.ToNode]
		deps = append(deps, Dependency{
			FromName:    from.VersionKey.Name,
			FromVersion: from.VersionKey.Version,
			ToName:      to.VersionKey.Name,
			ToVersion:   to.VersionKey.Version,
			Requirement: e.Requirement,
			Direct:      from.Relation == "SELF",
		})
	}
	return deps, nil
}

// escapePackage encodes a package name for a deps.dev path segment. Scoped names must be fully
// percent encoded: `@types/node` as a literal path returns 404, `%40types%2Fnode` returns the
// package. The npm registry accepts either form, so this is the stricter of the two.
func escapePackage(name string) string {
	return url.QueryEscape(name)
}
