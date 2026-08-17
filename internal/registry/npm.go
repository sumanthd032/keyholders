package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const npmBase = "https://registry.npmjs.org/"

// versionDocMaxAge is long because a published version document is immutable in the fields we read.
// Maintainer sets are recorded as they were at publish time and are not rewritten afterwards, so a
// stored copy does not go stale the way a packument does.
const versionDocMaxAge = 30 * 24 * time.Hour

// VersionDoc is the per-version registry document, reduced to the fields the graph needs.
//
// The maintainer set is the reason this endpoint is fetched rather than /latest. npm preserves the
// maintainer list as it stood when each version was published, so express 4.0.0, 4.17.1 and 5.2.1
// carry three disjoint sets. That makes the human layer historical rather than current-only, and it
// is what lets an audit say who could have pushed at a past instant instead of who can push today.
type VersionDoc struct {
	Name        string
	Version     string
	Maintainers []string

	// PublishedBy is the human account behind the publish. It is a strictly stronger signal than
	// membership of the maintainer set, because it is an action rather than a capability.
	//
	// It is not simply `_npmUser.name`. That field carries the publishing *identity*, which for a
	// release made through a trusted publisher is the CI system: cookie@2.0.1 reports "GitHub
	// Actions", with the person who authorised it in `_npmUser.approver`. Taking the name at face
	// value credits nobody for those publishes, which would make an account that moved to trusted
	// publishing look dormant, and dormancy raises its risk score. The safest publishing practice
	// available would then be scored as a liability, so the approver wins where there is one.
	PublishedBy string

	HasProvenance    bool
	HasInstallScript bool
}

// VersionDoc fetches one version's registry document. Sizes measured between 1.6 and 8.5 KB, against
// a 4.2 MB average for the full packument that carries the same information for every version.
func (c *Client) VersionDoc(ctx context.Context, name, version string) (VersionDoc, error) {
	body, err := c.Get(ctx, npmBase+escapePackage(name)+"/"+version, versionDocMaxAge)
	if err != nil {
		return VersionDoc{}, err
	}
	return parseVersionDoc(name, version, body)
}

func parseVersionDoc(name, version string, body []byte) (VersionDoc, error) {
	var doc struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Maintainers []struct {
			Name string `json:"name"`
		} `json:"maintainers"`
		NpmUser struct {
			Name     string `json:"name"`
			Approver *struct {
				Name string `json:"name"`
			} `json:"approver"`
		} `json:"_npmUser"`
		Scripts map[string]string `json:"scripts"`
		Dist    struct {
			Attestations *struct {
				Provenance struct {
					PredicateType string `json:"predicateType"`
				} `json:"provenance"`
			} `json:"attestations"`
		} `json:"dist"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return VersionDoc{}, fmt.Errorf("parse npm document for %s@%s: %w", name, version, err)
	}

	publishedBy := doc.NpmUser.Name
	if doc.NpmUser.Approver != nil && doc.NpmUser.Approver.Name != "" {
		publishedBy = doc.NpmUser.Approver.Name
	}

	v := VersionDoc{
		Name:        doc.Name,
		Version:     doc.Version,
		PublishedBy: publishedBy,
		// dist.attestations is present only when the version was published with a provenance
		// attestation, so its presence is the signal. Verified against sigstore and vite, which
		// carry it, and tinybench, which does not.
		HasProvenance: doc.Dist.Attestations != nil &&
			doc.Dist.Attestations.Provenance.PredicateType != "",
	}
	for _, m := range doc.Maintainers {
		if m.Name != "" {
			v.Maintainers = append(v.Maintainers, m.Name)
		}
	}
	// These three run automatically on install, which is the difference between a dependency that
	// sits on disk and one that executes on every machine that installs it.
	for _, hook := range []string{"preinstall", "install", "postinstall"} {
		if doc.Scripts[hook] != "" {
			v.HasInstallScript = true
			break
		}
	}
	return v, nil
}
