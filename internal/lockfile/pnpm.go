package lockfile

import (
	"bufio"
	"bytes"
	"strings"
)

// parsePnpm reads pnpm-lock.yaml.
//
// Only the `packages:` section is read, and it is read as lines rather than as YAML. That is a
// deliberate trade: the section is a flat list of keys that encode name and version directly, the
// key format is the one thing pnpm has kept stable across lockfile versions, and reading two line
// shapes is less to get wrong than mapping three different document schemas.
//
//	v5:      /lodash/4.17.21:
//	v6:      /lodash@4.17.21:
//	v9:      lodash@4.17.21:
//
// Direct and Dev are left false. Those live in the `importers:` section, whose shape does differ
// between versions, and a wrong flag is worse than an absent one.
func parsePnpm(data []byte) (Lockfile, error) {
	out := Lockfile{Format: FormatPnpm}

	inPackages := false
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scan.Scan() {
		line := scan.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// A top level key ends the section. `snapshots:` follows `packages:` in v9 and repeats the
		// same keys with their dependency lists, so stopping here avoids reading them twice.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inPackages = strings.HasPrefix(line, "packages:")
			continue
		}
		if !inPackages {
			continue
		}

		// Entry keys sit one level in. Anything deeper is a property of the entry above it.
		trimmed := strings.TrimSpace(line)
		if indent(line) > 2 || !strings.HasSuffix(trimmed, ":") {
			continue
		}
		if name, version, ok := pnpmKey(trimmed); ok {
			out.Pins = append(out.Pins, Pin{Name: name, Version: version})
		}
	}
	if err := scan.Err(); err != nil {
		return Lockfile{}, err
	}
	return normalise(out), nil
}

// pnpmKey splits an entry key into name and version.
func pnpmKey(key string) (name, version string, ok bool) {
	key = strings.TrimSuffix(key, ":")
	key = strings.Trim(strings.TrimSpace(key), `'"`)
	key = strings.TrimPrefix(key, "/")
	if key == "" {
		return "", "", false
	}

	// Peer dependency suffixes are appended in brackets, as in
	// `@babel/plugin-syntax-jsx@7.24.7(@babel/core@7.24.7)`. They describe the resolution context,
	// not the package, so the identity is everything before them.
	if open := strings.IndexByte(key, '('); open >= 0 {
		key = key[:open]
	}

	at := strings.LastIndex(key, "@")
	switch {
	case at > 0:
		name, version = key[:at], key[at+1:]
	default:
		// v5 separates with a slash instead: /lodash/4.17.21
		slash := strings.LastIndex(key, "/")
		if slash <= 0 {
			return "", "", false
		}
		name, version = key[:slash], key[slash+1:]
	}

	if name == "" || version == "" || !startsWithDigit(version) {
		// Anything whose trailing segment is not a version is a link, a tarball URL, or a schema
		// key that happens to sit at this indent.
		return "", "", false
	}
	return name, version, true
}

func indent(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}

func startsWithDigit(s string) bool {
	return s != "" && s[0] >= '0' && s[0] <= '9'
}
