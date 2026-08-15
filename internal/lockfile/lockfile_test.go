package lockfile

import "testing"

// find returns the pin for a name, so a test can assert on one entry without depending on ordering.
func find(t *testing.T, l Lockfile, name string) Pin {
	t.Helper()
	for _, p := range l.Pins {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no pin for %q in %d pins", name, len(l.Pins))
	return Pin{}
}

func TestParseNpmV3(t *testing.T) {
	const data = `{
      "name": "checkout-api",
      "lockfileVersion": 3,
      "packages": {
        "": {"name": "checkout-api", "dependencies": {"express": "^4.18.0"}, "devDependencies": {"mocha": "^10.0.0"}},
        "node_modules/express": {"version": "4.18.2"},
        "node_modules/@babel/core": {"version": "7.29.7", "dev": true},
        "node_modules/mocha": {"version": "10.2.0", "dev": true},
        "node_modules/@commitlint/is-ignored/node_modules/semver": {"version": "7.8.5", "dev": true},
        "node_modules/semver": {"version": "6.3.1", "dev": true},
        "node_modules/link-to-workspace": {"resolved": "packages/thing", "link": true}
      }
    }`

	got, err := Parse("package-lock.json", []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Project != "checkout-api" {
		t.Errorf("project = %q, want checkout-api", got.Project)
	}

	// A workspace link is a symlink into the repository, not something fetched from the registry,
	// so it has no maintainer and must not become a node.
	if len(got.Pins) != 5 {
		t.Fatalf("got %d pins, want 5 (the link entry must be dropped): %+v", len(got.Pins), got.Pins)
	}

	if p := find(t, got, "express"); !p.Direct || p.Dev {
		t.Errorf("express: direct=%v dev=%v, want direct and not dev", p.Direct, p.Dev)
	}
	if p := find(t, got, "mocha"); !p.Direct || !p.Dev {
		t.Errorf("mocha: direct=%v dev=%v, want direct and dev", p.Direct, p.Dev)
	}
	if p := find(t, got, "@babel/core"); p.Direct {
		t.Error("@babel/core is not declared by the root and must not be marked direct")
	}

	// Two copies of semver at different depths are two different installed artefacts, published by
	// possibly different people at different times. Collapsing them by name would lose one.
	versions := map[string]bool{}
	for _, p := range got.Pins {
		if p.Name == "semver" {
			versions[p.Version] = true
		}
	}
	if len(versions) != 2 || !versions["6.3.1"] || !versions["7.8.5"] {
		t.Errorf("semver versions = %v, want both 6.3.1 and 7.8.5", versions)
	}
}

func TestParseNpmV1(t *testing.T) {
	const data = `{
      "name": "legacy",
      "lockfileVersion": 1,
      "dependencies": {
        "express": {"version": "4.18.2", "dependencies": {
          "body-parser": {"version": "1.20.1"}
        }},
        "mocha": {"version": "10.2.0", "dev": true}
      }
    }`

	got, err := Parse("package-lock.json", []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Pins) != 3 {
		t.Fatalf("got %d pins, want 3: %+v", len(got.Pins), got.Pins)
	}
	if p := find(t, got, "body-parser"); p.Direct {
		t.Error("a nested v1 dependency is not direct")
	}
	if p := find(t, got, "express"); !p.Direct {
		t.Error("a top level v1 dependency is direct")
	}
}

func TestParseYarn(t *testing.T) {
	// Classic and Berry entries in one file, which does not happen in practice but proves the
	// scanner reads both dialects rather than sniffing a version header.
	const data = `# yarn lockfile v1

lodash@^4.17.20:
  version "4.17.21"
  resolved "https://registry.yarnpkg.com/lodash/-/lodash-4.17.21.tgz"

"@babel/core@^7.0.0", "@babel/core@^7.1.0":
  version "7.29.7"

"@actions/github@npm:9.1.1":
  version: 9.1.1
  linkType: hard

"@babel-baseline/cli@npm:@babel/cli@7.27.1":
  version: 7.27.1

"my-app@workspace:.":
  version: 0.0.0-use.local
`

	got, err := Parse("yarn.lock", []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := map[string]string{
		"lodash":          "4.17.21",
		"@babel/core":     "7.29.7",
		"@actions/github": "9.1.1",
		"@babel/cli":      "7.27.1", // the alias resolves to the package actually installed
	}
	if len(got.Pins) != len(want) {
		t.Fatalf("got %d pins, want %d (the workspace entry must be dropped): %+v",
			len(got.Pins), len(want), got.Pins)
	}
	for name, version := range want {
		if p := find(t, got, name); p.Version != version {
			t.Errorf("%s = %s, want %s", name, p.Version, version)
		}
	}
}

func TestParsePnpm(t *testing.T) {
	// The three key shapes pnpm has used, plus a peer suffix and a snapshots section that repeats
	// the same keys and must not be counted twice.
	const data = `lockfileVersion: '9.0'

importers:

  .:
    dependencies:
      vite:
        specifier: ^5.0.0
        version: 5.0.0

packages:

  '@11ty/gray-matter@2.1.0':
    resolution: {integrity: sha512-abc==}
    engines: {node: '>=11'}

  /lodash@4.17.21:
    resolution: {integrity: sha512-def==}

  /semver/6.3.1:
    resolution: {integrity: sha512-ghi==}

  '@babel/plugin-syntax-jsx@7.24.7(@babel/core@7.24.7)':
    resolution: {integrity: sha512-jkl==}

snapshots:

  '@11ty/gray-matter@2.1.0':
    dependencies:
      js-yaml: 4.3.0
`

	got, err := Parse("pnpm-lock.yaml", []byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := map[string]string{
		"@11ty/gray-matter":        "2.1.0",
		"lodash":                   "4.17.21",
		"semver":                   "6.3.1",
		"@babel/plugin-syntax-jsx": "7.24.7",
	}
	if len(got.Pins) != len(want) {
		t.Fatalf("got %d pins, want %d: %+v", len(got.Pins), len(want), got.Pins)
	}
	for name, version := range want {
		if p := find(t, got, name); p.Version != version {
			t.Errorf("%s = %s, want %s", name, p.Version, version)
		}
	}
}

func TestParseUnknownFormat(t *testing.T) {
	if _, err := Parse("Gemfile.lock", []byte("{}")); err == nil {
		t.Fatal("an unrecognised lockfile must be an error, not an empty result")
	}
}

func TestYarnEntryName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"lodash@^4.17.20:", "lodash"},
		{"@babel/core@^7.0.0:", "@babel/core"},
		{`"@actions/github@npm:9.1.1":`, "@actions/github"},
		{`"@babel-baseline/cli@npm:@babel/cli@7.27.1":`, "@babel/cli"},
		{`"string-width@npm:^4.2.0":`, "string-width"},
		{`"lodash@^4.0.0, lodash@^4.17.0":`, "lodash"},
		{`"my-app@workspace:.":`, ""},
		{`"thing@link:../thing":`, ""},
		{"__metadata:", ""},
	}
	for _, tc := range cases {
		if got := yarnEntryName(tc.in); got != tc.want {
			t.Errorf("yarnEntryName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
