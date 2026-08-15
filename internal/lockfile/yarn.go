package lockfile

import (
	"bufio"
	"bytes"
	"strings"
)

// parseYarn reads both yarn lockfile dialects with one scanner.
//
// Classic yarn writes its own format, and Berry writes YAML, but the two agree on the shape that
// matters: an entry header at column zero naming one or more descriptors, then an indented block
// containing the resolved `version`. Reading just those two things works for both.
//
//	classic:  lodash@^4.17.20:
//	            version "4.17.21"
//	berry:    "@actions/github@npm:9.1.1":
//	            version: 9.1.1
//
// Neither dialect records which dependencies the project declared itself, so Direct is left false.
// Yarn keys entries by the requested descriptor, and a descriptor appears whether it was requested
// by the root or by a dependency, so claiming otherwise would be a guess.
func parseYarn(data []byte) (Lockfile, error) {
	out := Lockfile{Format: FormatYarn}

	var pending string
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scan.Scan() {
		line := scan.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			pending = yarnEntryName(trimmed)
			continue
		}
		if pending == "" {
			continue
		}
		if version, ok := yarnVersion(trimmed); ok {
			out.Pins = append(out.Pins, Pin{Name: pending, Version: version})
			pending = ""
		}
	}
	if err := scan.Err(); err != nil {
		return Lockfile{}, err
	}
	return normalise(out), nil
}

// yarnEntryName extracts the installed package name from an entry header.
//
// A header lists one or more comma separated descriptors, each of which is a name, an `@`, and what
// was asked for. Splitting at the first `@` past a leading scope marker separates the two:
//
//	lodash@^4.17.20              -> lodash
//	@babel/core@^7.0.0           -> @babel/core
//	"@actions/github@npm:9.1.1"  -> @actions/github
//
// Aliases are the case that makes this more than a split. Yarn lets a project install one package
// under another name, and the descriptor then names the real one:
//
//	"@babel-baseline/cli@npm:@babel/cli@7.27.1" -> @babel/cli
//
// Reading the left side there would attribute the code to a package that does not exist, and so to
// no maintainer at all, which silently drops a real keyholder.
func yarnEntryName(header string) string {
	header = strings.TrimSuffix(header, ":")
	if comma := strings.Index(header, ","); comma >= 0 {
		header = header[:comma]
	}
	header = strings.Trim(strings.TrimSpace(header), `"'`)

	name, descriptor, ok := splitDescriptor(header)
	if !ok || name == "" || strings.HasPrefix(name, "__") {
		return ""
	}

	switch {
	// Local paths rather than registry packages. Their version is the placeholder 0.0.0-use.local.
	case strings.HasPrefix(descriptor, "workspace:"),
		strings.HasPrefix(descriptor, "link:"),
		strings.HasPrefix(descriptor, "portal:"),
		strings.HasPrefix(descriptor, "file:"):
		return ""

	case strings.HasPrefix(descriptor, "npm:"):
		// Either `npm:<range>`, which is an ordinary dependency, or `npm:<name>@<range>`, which is
		// an alias naming the package actually installed.
		if aliased, _, ok := splitDescriptor(strings.TrimPrefix(descriptor, "npm:")); ok {
			return aliased
		}
	}
	return name
}

// splitDescriptor separates `name@rest`, treating a leading `@` as a scope marker rather than a
// separator. The boolean reports whether a separator was found at all.
func splitDescriptor(s string) (name, rest string, ok bool) {
	search := s
	offset := 0
	if strings.HasPrefix(s, "@") {
		search, offset = s[1:], 1
	}
	at := strings.Index(search, "@")
	if at < 0 {
		return s, "", false
	}
	return s[:at+offset], s[at+offset+1:], true
}

// yarnVersion reads the resolved version from an entry body, in either dialect.
func yarnVersion(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "version")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	rest = strings.Trim(rest, `"'`)
	if rest == "" || rest == "0.0.0-use.local" {
		return "", false
	}
	return rest, true
}
