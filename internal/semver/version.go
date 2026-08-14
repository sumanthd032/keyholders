// Package semver implements the subset of node-semver that dependency resolution needs.
//
// The npm registry is the source of truth for what a range resolved to, and npm uses node-semver,
// so this package has to agree with that implementation rather than with the SemVer specification
// where the two differ. The differences are small but they decide real resolutions, in particular
// the prerelease rules and the -0 suffix on desugared upper bounds. Every behaviour here is checked
// against the reference implementation by a differential test.
package semver

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

var ErrInvalidVersion = errors.New("invalid version")

// Version is a parsed semantic version. Build metadata is retained for round-tripping but ignored
// in comparisons, as the specification requires.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string
	Build               string
}

// Parse reads a version, tolerating the leading "v" and surrounding space that appear in real
// package metadata.
func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "=")
	if s == "" {
		return Version{}, ErrInvalidVersion
	}

	var v Version
	if i := strings.IndexByte(s, '+'); i >= 0 {
		v.Build = s[i+1:]
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if pre == "" {
			return Version{}, ErrInvalidVersion
		}
		v.Pre = strings.Split(pre, ".")
		if slices.Contains(v.Pre, "") {
			return Version{}, ErrInvalidVersion
		}
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, ErrInvalidVersion
	}
	nums := make([]uint64, 3)
	for i, p := range parts {
		n, err := parseNumeric(p)
		if err != nil {
			return Version{}, err
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// parseNumeric rejects leading zeroes, which node-semver treats as invalid rather than as a number.
func parseNumeric(s string) (uint64, error) {
	if s == "" {
		return 0, ErrInvalidVersion
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, ErrInvalidVersion
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, ErrInvalidVersion
	}
	return n, nil
}

func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare orders two versions, returning -1, 0 or 1. Build metadata is not considered.
func (v Version) Compare(o Version) int {
	if c := cmpUint(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpUint(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpUint(v.Patch, o.Patch); c != 0 {
		return c
	}
	return comparePre(v.Pre, o.Pre)
}

// comparePre implements the precedence rules for prerelease identifiers: a version with a
// prerelease sorts below the same version without one, numeric identifiers compare numerically and
// sort below alphanumeric ones, and a longer identifier list wins when all earlier fields are equal.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		an, aNum := numericIdent(a[i])
		bn, bNum := numericIdent(b[i])
		switch {
		case aNum && bNum:
			return cmpUint(an, bn)
		case aNum:
			return -1
		case bNum:
			return 1
		case a[i] < b[i]:
			return -1
		default:
			return 1
		}
	}
	return cmpInt(len(a), len(b))
}

func numericIdent(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func (v Version) String() string {
	s := strconv.FormatUint(v.Major, 10) + "." +
		strconv.FormatUint(v.Minor, 10) + "." +
		strconv.FormatUint(v.Patch, 10)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
