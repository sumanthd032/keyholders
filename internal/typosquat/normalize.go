package typosquat

import "strings"

// homoglyphs maps characters attackers substitute for a visually similar one in a squatted name.
// Scoped to what shows up in real npm incidents rather than the full Unicode confusable table: npm
// names are lowercase ASCII plus digits, hyphens, dots, and underscores, so digit-for-letter
// leetspeak is the realistic threat, not cross-script homoglyphs.
var homoglyphs = strings.NewReplacer(
	"0", "o",
	"1", "l",
	"3", "e",
	"4", "a",
	"5", "s",
	"7", "t",
	"rn", "m",
	"vv", "w",
)

// Normalize folds a package name down to the form typosquat comparison runs against: lowercased,
// homoglyphs folded to the letter they impersonate, and separators removed. Separator removal is
// what catches the canonical case, `crossenv` squatting `cross-env` in 2017: the two names are edit
// distance 1 apart on their separators alone, and every other transform here is inert on that pair.
//
// The scope prefix, "@scope/", is left untouched: a squat that changes the scope but keeps the name
// identical is a different, and arguably worse, attack than a same-scope near-miss, and folding them
// together would conflate two distinct signals into one distance number.
func Normalize(name string) string {
	scope, rest, hasScope := strings.Cut(name, "/")
	if !hasScope {
		rest = scope
		scope = ""
	}

	rest = strings.ToLower(rest)
	rest = homoglyphs.Replace(rest)
	rest = strings.NewReplacer("-", "", "_", "", ".", "").Replace(rest)

	if scope == "" {
		return rest
	}
	return strings.ToLower(scope) + "/" + rest
}
