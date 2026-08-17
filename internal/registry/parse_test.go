package registry

import (
	"testing"
	"time"
)

func TestParseTimeline(t *testing.T) {
	// Trimmed from a real deps.dev response. The third entry has no publishedAt, which the API does
	// return and which every temporal claim downstream has to survive.
	const body = `{"packageKey":{"system":"NPM","name":"express"},"versions":[
      {"versionKey":{"version":"4.0.0"},"publishedAt":"2014-04-09T20:07:15Z","isDefault":false,"isDeprecated":true},
      {"versionKey":{"version":"5.2.1"},"publishedAt":"2025-12-01T20:49:43Z","isDefault":true,"isDeprecated":false},
      {"versionKey":{"version":"0.0.1"},"publishedAt":"","isDefault":false,"isDeprecated":false}]}`

	got, err := parseTimeline("express", []byte(body))
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d releases, want 3", len(got))
	}

	want := time.Date(2014, 4, 9, 20, 7, 15, 0, time.UTC)
	if !got[0].PublishedAt.Equal(want) {
		t.Errorf("4.0.0 published at %v, want %v", got[0].PublishedAt, want)
	}
	if !got[0].Deprecated {
		t.Error("4.0.0 should be deprecated")
	}
	if !got[1].IsDefault {
		t.Error("5.2.1 should be the default version")
	}
	if !got[2].PublishedAt.IsZero() {
		t.Errorf("a missing publishedAt must parse to the zero time, got %v", got[2].PublishedAt)
	}
}

func TestParseDependencies(t *testing.T) {
	// Node 0 is SELF. The edge from node 1 to node 2 is a declaration by a package other than the
	// root, which is what makes one closure fetch worth more than the root's own dependencies.
	const body = `{"nodes":[
      {"versionKey":{"name":"express","version":"4.18.2"},"relation":"SELF"},
      {"versionKey":{"name":"accepts","version":"1.3.8"},"relation":"DIRECT"},
      {"versionKey":{"name":"mime-types","version":"2.1.35"},"relation":"INDIRECT"}],
    "edges":[
      {"fromNode":0,"toNode":1,"requirement":"~1.3.8"},
      {"fromNode":1,"toNode":2,"requirement":"~2.1.34"}]}`

	got, err := parseDependencies("express", "4.18.2", []byte(body))
	if err != nil {
		t.Fatalf("parseDependencies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d dependencies, want 2", len(got))
	}

	if got[0].FromName != "express" || got[0].ToName != "accepts" ||
		got[0].Requirement != "~1.3.8" || !got[0].Direct {
		t.Errorf("first edge resolved to %+v", got[0])
	}
	if got[1].FromName != "accepts" || got[1].FromVersion != "1.3.8" ||
		got[1].ToName != "mime-types" || got[1].Direct {
		t.Errorf("second edge resolved to %+v", got[1])
	}
}

// TestParseDependenciesBadIndex guards the one way this parse can silently corrupt the graph: an
// out-of-range node index would otherwise panic or, worse, attach a range to the wrong package.
func TestParseDependenciesBadIndex(t *testing.T) {
	const body = `{"nodes":[{"versionKey":{"name":"a","version":"1.0.0"},"relation":"SELF"}],
    "edges":[{"fromNode":0,"toNode":7,"requirement":"^1.0.0"}]}`

	if _, err := parseDependencies("a", "1.0.0", []byte(body)); err == nil {
		t.Fatal("an edge referencing a node that does not exist must be an error")
	}
}

func TestParseVersionDoc(t *testing.T) {
	cases := []struct {
		name             string
		body             string
		wantMaintainers  []string
		wantPublishedBy  string
		wantProvenance   bool
		wantInstallHooks bool
	}{
		{
			name: "provenance and postinstall",
			body: `{"name":"vite","version":"6.0.0",
              "maintainers":[{"name":"patak"},{"name":"antfu"}],
              "_npmUser":{"name":"patak"},
              "scripts":{"build":"tsc","postinstall":"node ./scripts/post.js"},
              "dist":{"attestations":{"url":"https://example","provenance":{"predicateType":"https://slsa.dev/provenance/v1"}}}}`,
			wantMaintainers:  []string{"patak", "antfu"},
			wantPublishedBy:  "patak",
			wantProvenance:   true,
			wantInstallHooks: true,
		},
		{
			name: "no attestations block at all",
			body: `{"name":"tinybench","version":"2.9.0",
              "maintainers":[{"name":"aslemammad"}],
              "_npmUser":{"name":"aslemammad"},
              "scripts":{"publish":"npm publish"},
              "dist":{"shasum":"abc"}}`,
			wantMaintainers: []string{"aslemammad"},
			wantPublishedBy: "aslemammad",
		},
		{
			// A build script named "install-deps" must not count: only the three hooks npm runs
			// automatically mean code executes on install.
			name: "script that merely looks like a hook",
			body: `{"name":"x","version":"1.0.0","maintainers":[{"name":"m"}],
              "_npmUser":{"name":"m"},"scripts":{"install-deps":"pnpm i"},"dist":{}}`,
			wantMaintainers: []string{"m"},
			wantPublishedBy: "m",
		},
		{
			name: "preinstall counts",
			body: `{"name":"y","version":"1.0.0","maintainers":[{"name":"m"}],
              "_npmUser":{"name":"m"},"scripts":{"preinstall":"./configure"},"dist":{}}`,
			wantMaintainers:  []string{"m"},
			wantPublishedBy:  "m",
			wantInstallHooks: true,
		},
		{
			// Trimmed from the real cookie@2.0.1 document. _npmUser.name is the publishing identity,
			// which here is the CI system, and the person is in approver. Attributing the publish to
			// "GitHub Actions" would credit nobody for it, and an account credited with no publishes
			// reads as dormant, which raises its risk score. Trusted publishing would then be scored
			// as a liability.
			name: "trusted publisher credits the approver",
			body: `{"name":"cookie","version":"2.0.1",
              "maintainers":[{"name":"blakeembrey"},{"name":"dougwilson"}],
              "_npmUser":{"name":"GitHub Actions","email":"npm-oidc-no-reply@github.com",
                "approver":{"name":"blakeembrey","email":"hello@blakeembrey.com"},
                "trustedPublisher":{"id":"github","oidcConfigId":"oidc:2e6871c3"}},
              "scripts":{"build":"tsc"},
              "dist":{"attestations":{"url":"https://example","provenance":{"predicateType":"https://slsa.dev/provenance/v1"}}}}`,
			wantMaintainers: []string{"blakeembrey", "dougwilson"},
			wantPublishedBy: "blakeembrey",
			wantProvenance:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVersionDoc("x", "1.0.0", []byte(tc.body))
			if err != nil {
				t.Fatalf("parseVersionDoc: %v", err)
			}
			if len(got.Maintainers) != len(tc.wantMaintainers) {
				t.Fatalf("maintainers %v, want %v", got.Maintainers, tc.wantMaintainers)
			}
			for i, h := range tc.wantMaintainers {
				if got.Maintainers[i] != h {
					t.Errorf("maintainer %d is %q, want %q", i, got.Maintainers[i], h)
				}
			}
			if got.PublishedBy != tc.wantPublishedBy {
				t.Errorf("published by %q, want %q", got.PublishedBy, tc.wantPublishedBy)
			}
			if got.HasProvenance != tc.wantProvenance {
				t.Errorf("provenance %v, want %v", got.HasProvenance, tc.wantProvenance)
			}
			if got.HasInstallScript != tc.wantInstallHooks {
				t.Errorf("install script %v, want %v", got.HasInstallScript, tc.wantInstallHooks)
			}
		})
	}
}

func TestEscapePackage(t *testing.T) {
	cases := []struct{ in, want string }{
		{"express", "express"},
		// deps.dev returns 404 for a literal `@types/node` path and 200 for the encoded form.
		{"@types/node", "%40types%2Fnode"},
		{"lodash.merge", "lodash.merge"},
	}
	for _, tc := range cases {
		if got := escapePackage(tc.in); got != tc.want {
			t.Errorf("escapePackage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
