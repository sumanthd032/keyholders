// Package graph holds the HydraDB-facing primitives: identity mapping and the Bolt client.
package graph

import "fmt"

// HydraDB node ids are non-negative integers, so every domain key is mapped through a
// deterministic 64-bit hash of its URN and masked into the non-negative range. The URN itself is
// stored as a node property so results can be hydrated back into human-readable names.
//
// FNV-1a is used rather than a cryptographic hash because this is an identity mapping, not a
// security boundary: it is fast, dependency-free, and stable across processes and machines, which
// is what matters for an ingest that must be resumable and idempotent.
const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211

	// nonNegMask clears the sign bit. Bolt carries ints as signed 64-bit, and HydraDB rejects
	// negative node ids, so the space is 2^63 rather than 2^64.
	nonNegMask = 0x7FFFFFFFFFFFFFFF
)

// ID maps a URN to a stable non-negative HydraDB node id.
func ID(urn string) int64 {
	h := uint64(fnvOffset64)
	for i := range len(urn) {
		h ^= uint64(urn[i])
		h *= fnvPrime64
	}
	return int64(h & nonNegMask)
}

// URN builders. Every node type has exactly one canonical URN form; these are the only places
// that form is constructed, so the id space cannot drift between ingest and query.

func PackageURN(ecosystem, name string) string {
	return fmt.Sprintf("pkg:%s/%s", ecosystem, name)
}

func VersionURN(ecosystem, name, version string) string {
	return fmt.Sprintf("pkg:%s/%s@%s", ecosystem, name, version)
}

func MaintainerURN(ecosystem, handle string) string {
	return fmt.Sprintf("mnt:%s/%s", ecosystem, handle)
}

func AdvisoryURN(osvID string) string {
	return fmt.Sprintf("osv:%s", osvID)
}

func ProjectURN(name string) string {
	return fmt.Sprintf("prj:local/%s", name)
}
