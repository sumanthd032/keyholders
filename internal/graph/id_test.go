package graph

import "testing"

func TestIDIsNonNegative(t *testing.T) {
	// HydraDB rejects negative node ids, and the mask is the only thing standing between a
	// uint64 hash and a rejected write, so this is checked over a wide spread of inputs.
	for _, urn := range []string{
		"", "a", "pkg:npm/lodash", "pkg:npm/@types/node@20.1.0",
		"mnt:npm/jdalton", "osv:GHSA-1234-5678-9012", "prj:local/checkout-api",
	} {
		if got := ID(urn); got < 0 {
			t.Errorf("ID(%q) = %d, want non-negative", urn, got)
		}
	}
}

func TestIDIsDeterministic(t *testing.T) {
	const urn = "pkg:npm/express"
	first := ID(urn)
	for range 100 {
		if got := ID(urn); got != first {
			t.Fatalf("ID(%q) not stable: %d then %d", urn, first, got)
		}
	}
}

func TestIDDistinguishes(t *testing.T) {
	// Near-miss URNs must not collide, in particular a package and its version, and two versions
	// that differ only in a patch digit.
	urns := []string{
		PackageURN("npm", "lodash"),
		VersionURN("npm", "lodash", "4.17.20"),
		VersionURN("npm", "lodash", "4.17.21"),
		MaintainerURN("npm", "lodash"),
		PackageURN("pypi", "lodash"),
	}
	seen := map[int64]string{}
	for _, u := range urns {
		id := ID(u)
		if prev, dup := seen[id]; dup {
			t.Fatalf("collision: %q and %q both map to %d", prev, u, id)
		}
		seen[id] = u
	}
}

func TestURNForms(t *testing.T) {
	cases := []struct{ got, want string }{
		{PackageURN("npm", "lodash"), "pkg:npm/lodash"},
		{VersionURN("npm", "lodash", "4.17.21"), "pkg:npm/lodash@4.17.21"},
		{MaintainerURN("npm", "jdalton"), "mnt:npm/jdalton"},
		{AdvisoryURN("GHSA-abcd"), "osv:GHSA-abcd"},
		{ProjectURN("checkout-api"), "prj:local/checkout-api"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// BenchmarkID guards the ingest hot path: every node and edge row hashes at least one URN, so a
// slow mapping here would show up directly in edges/sec.
func BenchmarkID(b *testing.B) {
	for b.Loop() {
		ID("pkg:npm/@babel/plugin-transform-runtime@7.23.9")
	}
}
