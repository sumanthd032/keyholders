package semver

import (
	"fmt"
	"strings"
)

// Range is a disjunction of comparator sets, matching node-semver's "||" of space separated
// comparators. A version satisfies the range when it satisfies every comparator in at least one set.
type Range struct {
	sets []comparatorSet
	raw  string
}

type comparatorSet []comparator

type comparator struct {
	op  op
	ver Version
}

type op uint8

const (
	opLT op = iota
	opLTE
	opGT
	opGTE
	opEQ
)

// ParseRange desugars a range expression into comparator sets.
//
// An empty range, "*", "x" and "latest" all mean "any version". Note that "latest" is not a semver
// range at all: it is a dist-tag, and resolving it requires dist-tag history that the registry does
// not retain. It parses as "any" here so callers can detect it, and callers that care about
// historical accuracy must treat it separately.
func ParseRange(s string) (Range, error) {
	raw := s
	s = strings.TrimSpace(s)
	if s == "" || s == "latest" || s == "*" {
		return Range{sets: []comparatorSet{{{op: opGTE, ver: Version{}}}}, raw: raw}, nil
	}

	var sets []comparatorSet
	for _, part := range strings.Split(s, "||") {
		set, err := parseComparatorSet(strings.TrimSpace(part))
		if err != nil {
			return Range{}, fmt.Errorf("range %q: %w", raw, err)
		}
		sets = append(sets, set)
	}
	return Range{sets: sets, raw: raw}, nil
}

func (r Range) String() string { return r.raw }

// IsAny reports whether the range admits every non-prerelease version. Ranges like "*" and "" carry
// no version information, which matters because they cannot be resolved for a past instant.
func (r Range) IsAny() bool {
	if len(r.sets) != 1 || len(r.sets[0]) != 1 {
		return false
	}
	c := r.sets[0][0]
	return c.op == opGTE && c.ver.Compare(Version{}) == 0 && !c.ver.IsPrerelease()
}

func parseComparatorSet(s string) (comparatorSet, error) {
	fields := joinLooseOperators(strings.Fields(s))
	if len(fields) == 0 {
		return comparatorSet{{op: opGTE, ver: Version{}}}, nil
	}

	var set comparatorSet
	for i := 0; i < len(fields); i++ {
		// A hyphen range spans three tokens and has to be recognised before the tokens are treated
		// as individual comparators.
		if i+2 < len(fields) && fields[i+1] == "-" {
			cs, err := hyphenRange(fields[i], fields[i+2])
			if err != nil {
				return nil, err
			}
			set = append(set, cs...)
			i += 2
			continue
		}
		cs, err := parseComparator(fields[i])
		if err != nil {
			return nil, err
		}
		set = append(set, cs...)
	}
	return set, nil
}

// joinLooseOperators reattaches an operator to the version that follows it. Ranges in the wild are
// written with a space after the operator often enough to matter: safer-buffer publishes
// ">= 2.1.2 < 3.0.0", which node-semver accepts. Splitting on whitespace alone turns that into four
// meaningless tokens.
func joinLooseOperators(fields []string) []string {
	out := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case ">", ">=", "<", "<=", "=", "~", "~>", "^":
			if i+1 < len(fields) {
				out = append(out, fields[i]+fields[i+1])
				i++
				continue
			}
		}
		out = append(out, fields[i])
	}
	return out
}

// partial is a version with possibly absent minor and patch, which is how x-ranges are written.
type partial struct {
	major, minor, patch uint64
	hasMinor, hasPatch  bool
	pre                 []string
}

func parsePartial(s string) (partial, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return partial{}, ErrInvalidVersion
	}

	var p partial
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if pre != "" {
			p.pre = strings.Split(pre, ".")
		}
	}

	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return partial{}, ErrInvalidVersion
	}
	for i, seg := range parts {
		if isWildcard(seg) {
			break
		}
		n, err := parseNumeric(seg)
		if err != nil {
			return partial{}, err
		}
		switch i {
		case 0:
			p.major = n
		case 1:
			p.minor, p.hasMinor = n, true
		case 2:
			p.patch, p.hasPatch = n, true
		}
	}
	return p, nil
}

func isWildcard(s string) bool {
	return s == "" || s == "x" || s == "X" || s == "*"
}

// parseComparator desugars one token into the comparators it implies. The -0 prerelease on upper
// bounds is deliberate: without it, "^1.2.3" would admit "2.0.0-alpha", which node-semver excludes.
func parseComparator(tok string) (comparatorSet, error) {
	if isWildcard(tok) {
		return comparatorSet{{op: opGTE, ver: Version{}}}, nil
	}

	switch {
	case strings.HasPrefix(tok, "^"):
		return caretRange(tok[1:])
	case strings.HasPrefix(tok, "~>"):
		return tildeRange(tok[2:])
	case strings.HasPrefix(tok, "~"):
		return tildeRange(tok[1:])
	case strings.HasPrefix(tok, ">="):
		return boundComparator(opGTE, tok[2:])
	case strings.HasPrefix(tok, "<="):
		return boundComparator(opLTE, tok[2:])
	case strings.HasPrefix(tok, ">"):
		return boundComparator(opGT, tok[1:])
	case strings.HasPrefix(tok, "<"):
		return boundComparator(opLT, tok[1:])
	case strings.HasPrefix(tok, "="):
		return exactRange(tok[1:])
	default:
		return exactRange(tok)
	}
}

// boundComparator handles an inequality against a possibly partial version. ">1.2" means
// ">=1.3.0", because every 1.2.x is not greater than 1.2, while "<1.2" means "<1.2.0".
func boundComparator(o op, s string) (comparatorSet, error) {
	p, err := parsePartial(s)
	if err != nil {
		return nil, err
	}
	if p.hasPatch {
		return comparatorSet{{op: o, ver: versionOf(p)}}, nil
	}
	switch o {
	case opGT:
		if p.hasMinor {
			return comparatorSet{{op: opGTE, ver: Version{Major: p.major, Minor: p.minor + 1}}}, nil
		}
		return comparatorSet{{op: opGTE, ver: Version{Major: p.major + 1}}}, nil
	case opLTE:
		if p.hasMinor {
			return comparatorSet{{op: opLT, ver: zeroPre(Version{Major: p.major, Minor: p.minor + 1})}}, nil
		}
		return comparatorSet{{op: opLT, ver: zeroPre(Version{Major: p.major + 1})}}, nil
	default:
		return comparatorSet{{op: o, ver: versionOf(p)}}, nil
	}
}

// exactRange turns a bare token into either an equality or, when it is partial, the range that the
// missing components imply. "1.2" means ">=1.2.0 <1.3.0-0".
func exactRange(s string) (comparatorSet, error) {
	p, err := parsePartial(s)
	if err != nil {
		return nil, err
	}
	if p.hasPatch {
		return comparatorSet{{op: opEQ, ver: versionOf(p)}}, nil
	}
	lo := versionOf(p)
	var hi Version
	if p.hasMinor {
		hi = zeroPre(Version{Major: p.major, Minor: p.minor + 1})
	} else {
		hi = zeroPre(Version{Major: p.major + 1})
	}
	return comparatorSet{{op: opGTE, ver: lo}, {op: opLT, ver: hi}}, nil
}

// tildeRange allows patch level changes when a minor version is given, and minor level changes
// otherwise.
func tildeRange(s string) (comparatorSet, error) {
	p, err := parsePartial(s)
	if err != nil {
		return nil, err
	}
	lo := versionOf(p)
	var hi Version
	if p.hasMinor {
		hi = zeroPre(Version{Major: p.major, Minor: p.minor + 1})
	} else {
		hi = zeroPre(Version{Major: p.major + 1})
	}
	return comparatorSet{{op: opGTE, ver: lo}, {op: opLT, ver: hi}}, nil
}

// caretRange allows changes that do not modify the left-most non-zero component. The zero-major
// cases are what make caret ranges narrow for unstable packages, and they are a common source of
// resolution surprises.
func caretRange(s string) (comparatorSet, error) {
	p, err := parsePartial(s)
	if err != nil {
		return nil, err
	}
	lo := versionOf(p)

	var hi Version
	switch {
	case p.major > 0 || !p.hasMinor:
		hi = zeroPre(Version{Major: p.major + 1})
	case p.minor > 0 || !p.hasPatch:
		hi = zeroPre(Version{Major: p.major, Minor: p.minor + 1})
	default:
		hi = zeroPre(Version{Major: p.major, Minor: p.minor, Patch: p.patch + 1})
	}
	return comparatorSet{{op: opGTE, ver: lo}, {op: opLT, ver: hi}}, nil
}

func hyphenRange(loTok, hiTok string) (comparatorSet, error) {
	lp, err := parsePartial(loTok)
	if err != nil {
		return nil, err
	}
	hp, err := parsePartial(hiTok)
	if err != nil {
		return nil, err
	}

	set := comparatorSet{{op: opGTE, ver: versionOf(lp)}}
	switch {
	case hp.hasPatch:
		set = append(set, comparator{op: opLTE, ver: versionOf(hp)})
	case hp.hasMinor:
		set = append(set, comparator{op: opLT, ver: zeroPre(Version{Major: hp.major, Minor: hp.minor + 1})})
	default:
		set = append(set, comparator{op: opLT, ver: zeroPre(Version{Major: hp.major + 1})})
	}
	return set, nil
}

func versionOf(p partial) Version {
	return Version{Major: p.major, Minor: p.minor, Patch: p.patch, Pre: p.pre}
}

// zeroPre marks a desugared upper bound with the lowest possible prerelease, so that prereleases of
// the excluded version do not satisfy the range.
func zeroPre(v Version) Version {
	v.Pre = []string{"0"}
	return v
}

// Satisfies reports whether v is admitted by the range.
//
// The prerelease rule is the part that most implementations get wrong: a prerelease version is only
// ever admitted by a comparator set that itself mentions a prerelease at the same major, minor and
// patch. Without that rule, "^1.2.3" would match "1.9.0-beta", which npm would never install.
func (r Range) Satisfies(v Version) bool {
	for _, set := range r.sets {
		if set.satisfies(v) {
			return true
		}
	}
	return false
}

func (s comparatorSet) satisfies(v Version) bool {
	for _, c := range s {
		if !c.satisfies(v) {
			return false
		}
	}
	if !v.IsPrerelease() {
		return true
	}
	for _, c := range s {
		if !c.ver.IsPrerelease() {
			continue
		}
		if c.ver.Major == v.Major && c.ver.Minor == v.Minor && c.ver.Patch == v.Patch {
			return true
		}
	}
	return false
}

func (c comparator) satisfies(v Version) bool {
	cmp := v.Compare(c.ver)
	switch c.op {
	case opLT:
		return cmp < 0
	case opLTE:
		return cmp <= 0
	case opGT:
		return cmp > 0
	case opGTE:
		return cmp >= 0
	default:
		return cmp == 0
	}
}

// MaxSatisfying returns the highest version in the list admitted by the range, which is how npm
// picks a version when nothing is already installed. The boolean reports whether anything matched.
func MaxSatisfying(versions []Version, r Range) (Version, bool) {
	var best Version
	found := false
	for _, v := range versions {
		if !r.Satisfies(v) {
			continue
		}
		if !found || v.Compare(best) > 0 {
			best, found = v, true
		}
	}
	return best, found
}
