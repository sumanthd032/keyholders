// Package lockfile reads the three npm lockfile formats into the one shape the graph needs: a
// project name and the exact set of package versions it pins.
//
// A lockfile is the only artefact that says what a specific machine actually installed, as opposed
// to what a range would resolve to. That is what makes it the root of an audit.
package lockfile

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Pin is one package at one exact version, as recorded by the lockfile.
type Pin struct {
	Name    string
	Version string

	// Direct reports whether the root project declares this dependency itself, rather than
	// receiving it through somebody else. The distinction drives the risk report: a direct
	// dependency is one you chose, and the rest arrived with it.
	Direct bool

	// Dev reports a dependency of the development tree only. Still a keyholder risk, because dev
	// dependencies execute on developer machines and in CI, but worth separating in the output.
	Dev bool
}

// Lockfile is a parsed lockfile.
type Lockfile struct {
	Project string
	Format  string
	Pins    []Pin
}

// Format names, used in output and in the Project node so a result can say what it read.
const (
	FormatNpm  = "package-lock.json"
	FormatPnpm = "pnpm-lock.yaml"
	FormatYarn = "yarn.lock"
)

// Parse reads a lockfile, choosing the parser by filename.
func Parse(path string, data []byte) (Lockfile, error) {
	switch base := filepath.Base(path); {
	case base == FormatNpm || strings.HasSuffix(base, "package-lock.json"):
		return parseNpm(data)
	case base == FormatPnpm || strings.HasSuffix(base, "pnpm-lock.yaml"):
		return parsePnpm(data)
	case base == FormatYarn || strings.HasSuffix(base, "yarn.lock"):
		return parseYarn(data)
	default:
		return Lockfile{}, fmt.Errorf("unrecognised lockfile %s: expected %s, %s or %s",
			base, FormatNpm, FormatPnpm, FormatYarn)
	}
}

// normalise deduplicates pins and puts them in a stable order, so two runs over the same lockfile
// produce the same edges and the same output.
//
// A lockfile legitimately contains the same package at several versions, installed at different
// depths, so deduplication is by name and version together rather than by name.
func normalise(l Lockfile) Lockfile {
	seen := make(map[string]int, len(l.Pins))
	out := make([]Pin, 0, len(l.Pins))

	for _, p := range l.Pins {
		if p.Name == "" || p.Version == "" {
			continue
		}
		key := p.Name + "@" + p.Version
		if at, ok := seen[key]; ok {
			// The same pin can be reached both directly and transitively. Direct wins, because it
			// is the stronger claim, and a dependency that is anywhere in the production tree is
			// not a dev dependency.
			out[at].Direct = out[at].Direct || p.Direct
			out[at].Dev = out[at].Dev && p.Dev
			continue
		}
		seen[key] = len(out)
		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	l.Pins = out
	return l
}
