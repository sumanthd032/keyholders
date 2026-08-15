package lockfile

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseNpm reads package-lock.json, which has had three incompatible shapes.
//
// Version 1 carries a nested `dependencies` tree. Versions 2 and 3 carry a flat `packages` map keyed
// by install path, and version 2 additionally repeats the v1 tree for older clients. The flat map is
// preferred wherever present: it is the authoritative one in v2, and the nested tree it duplicates
// loses the distinction between two copies of a package installed at different depths.
func parseNpm(data []byte) (Lockfile, error) {
	var doc struct {
		Name            string `json:"name"`
		LockfileVersion int    `json:"lockfileVersion"`
		Packages        map[string]struct {
			Name     string          `json:"name"`
			Version  string          `json:"version"`
			Dev      bool            `json:"dev"`
			Link     bool            `json:"link"`
			Deps     json.RawMessage `json:"dependencies"`
			DevDeps  json.RawMessage `json:"devDependencies"`
			Optional bool            `json:"optional"`
		} `json:"packages"`
		Dependencies map[string]npmV1Entry `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Lockfile{}, fmt.Errorf("parse package-lock.json: %w", err)
	}

	out := Lockfile{Project: doc.Name, Format: FormatNpm}

	if root, ok := doc.Packages[""]; ok {
		if out.Project == "" {
			out.Project = root.Name
		}
		direct := directNames(root.Deps, root.DevDeps)

		for path, entry := range doc.Packages {
			if path == "" || entry.Link {
				// The root itself is the project, and a link entry is a workspace symlink rather
				// than something fetched from the registry.
				continue
			}
			name := packageNameFromPath(path)
			if name == "" || entry.Version == "" {
				continue
			}
			out.Pins = append(out.Pins, Pin{
				Name:    name,
				Version: entry.Version,
				// Only a top-level node_modules entry can be what the root declared. The same
				// package nested under another is somebody else's copy.
				Direct: direct[name] && !strings.Contains(strings.TrimPrefix(path, "node_modules/"), "node_modules/"),
				Dev:    entry.Dev,
			})
		}
		return normalise(out), nil
	}

	// Version 1, or a v2 file whose packages map was stripped.
	collectNpmV1(doc.Dependencies, true, &out)
	return normalise(out), nil
}

type npmV1Entry struct {
	Version      string                `json:"version"`
	Dev          bool                  `json:"dev"`
	Dependencies map[string]npmV1Entry `json:"dependencies"`
}

func collectNpmV1(deps map[string]npmV1Entry, top bool, out *Lockfile) {
	for name, entry := range deps {
		if entry.Version != "" {
			out.Pins = append(out.Pins, Pin{Name: name, Version: entry.Version, Direct: top, Dev: entry.Dev})
		}
		collectNpmV1(entry.Dependencies, false, out)
	}
}

// directNames is the set the root project declares itself.
func directNames(sections ...json.RawMessage) map[string]bool {
	names := map[string]bool{}
	for _, raw := range sections {
		if len(raw) == 0 {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			// The root's dependencies are a name to range map. Anything else is not something we
			// can read, and getting the direct flag wrong is not worth failing the parse over.
			continue
		}
		for name := range m {
			names[name] = true
		}
	}
	return names
}

// packageNameFromPath turns an install path into a package name.
//
//	node_modules/express                                   -> express
//	node_modules/@babel/core                               -> @babel/core
//	node_modules/@commitlint/is-ignored/node_modules/semver -> semver
//
// The name is whatever follows the last node_modules segment, which is what makes nesting work: a
// package installed under another package is still itself.
func packageNameFromPath(path string) string {
	const marker = "node_modules/"
	i := strings.LastIndex(path, marker)
	if i < 0 {
		return ""
	}
	return path[i+len(marker):]
}
