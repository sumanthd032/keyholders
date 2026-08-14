// Command spike-resolve measures how accurately a dependency range can be resolved for a past
// instant using only public data.
//
// The method: take real lockfiles committed to public repositories at known dates. A lockfile at
// version 2 or 3 records both the declared range for every dependency and the version that was
// actually installed, so one file yields many (range, resolved version, date) triples with no
// cross-referencing. Canonical resolution is then the highest version satisfying the range that was
// published at or before that date, and the agreement rate between the two is the number this spike
// exists to produce.
//
// This is a measurement tool, not part of the product. It reports a number and the reasons behind
// the disagreements, which is what calibrates how strongly time based claims can be stated.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sumanthd032/keyholders/internal/semver"
)

var repos = []string{
	"eslint/eslint", "axios/axios", "expressjs/express", "npm/cli", "reduxjs/redux",
	"chartjs/Chart.js", "socketio/socket.io", "date-fns/date-fns", "winstonjs/winston",
	"validatorjs/validator.js", "moment/moment", "gulpjs/gulp", "jashkenas/underscore",
	"mochajs/mocha", "sinonjs/sinon",
}

func main() {
	perRepo := flag.Int("snapshots", 4, "lockfile snapshots to sample per repository")
	flag.Parse()

	token := githubToken()
	if token == "" {
		fmt.Fprintln(os.Stderr, "no GitHub token; run gh auth login")
		os.Exit(1)
	}
	gh := &github{token: token}
	dd := &depsDev{cache: map[string][]release{}}

	var (
		mu       sync.Mutex
		outcomes []outcome
	)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, repo := range repos {
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			snaps, err := gh.lockfileSnapshots(repo, *perRepo)
			if err != nil {
				fmt.Printf("  %-26s skipped: %v\n", repo, err)
				return
			}
			for _, sn := range snaps {
				res := evaluate(dd, repo, sn)
				mu.Lock()
				outcomes = append(outcomes, res...)
				mu.Unlock()
				fmt.Printf("  %-26s %s  %d triples\n", repo, sn.when.Format("2006-01-02"), len(res))
			}
		}(repo)
	}
	wg.Wait()

	report(outcomes)
}

// ---------- lockfile handling ----------

type snapshot struct {
	sha  string
	when time.Time
	body []byte
}

// triple is one declared dependency from a lockfile: the range its parent asked for, and the version
// npm actually installed, as of the snapshot date.
type triple struct {
	repo     string
	when     time.Time
	dep      string
	rangeStr string
	resolved string
}

type outcome struct {
	triple
	canonical string
	verdict   verdict
}

type verdict int

const (
	agree verdict = iota
	disagree
	skipUnresolvableRange // "*" or "latest": needs dist-tag history the registry does not keep
	skipNoTimeline        // deps.dev had no version list
	skipUnparseable
	skipNoCandidate // nothing satisfied the range at that date
)

func (v verdict) String() string {
	switch v {
	case agree:
		return "agree"
	case disagree:
		return "disagree"
	case skipUnresolvableRange:
		return "skipped: range carries no version (* or latest)"
	case skipNoTimeline:
		return "skipped: no version timeline"
	case skipUnparseable:
		return "skipped: unparseable range or version"
	default:
		return "skipped: nothing satisfied the range at that date"
	}
}

// lockfile covers version 2 and 3, which both carry the packages map. Version 1 stored a different
// shape and is skipped rather than half-supported.
type lockfile struct {
	Version  int `json:"lockfileVersion"`
	Packages map[string]struct {
		Version      string            `json:"version"`
		Dependencies map[string]string `json:"dependencies"`
	} `json:"packages"`
}

func extractTriples(repo string, sn snapshot) ([]triple, error) {
	var lf lockfile
	if err := json.Unmarshal(sn.body, &lf); err != nil {
		return nil, err
	}
	if lf.Version < 2 || len(lf.Packages) == 0 {
		return nil, fmt.Errorf("lockfileVersion %d not supported", lf.Version)
	}

	version := make(map[string]string, len(lf.Packages))
	for path, entry := range lf.Packages {
		if entry.Version != "" {
			version[path] = entry.Version
		}
	}

	var out []triple
	for path, entry := range lf.Packages {
		for dep, rng := range entry.Dependencies {
			got, ok := lookupFrom(version, path, dep)
			if !ok {
				continue
			}
			out = append(out, triple{
				repo: repo, when: sn.when, dep: dep, rangeStr: rng, resolved: got,
			})
		}
	}
	return out, nil
}

// lookupFrom finds the copy of dep that the package at parentPath actually resolves to, following
// Node's resolution order: the package's own node_modules first, then each enclosing scope, then the
// root.
//
// Flattening the tree by package name instead produces nonsense, because npm installs several
// versions of the same package at different depths. Doing that here matched declared ranges against
// unrelated copies and reported impossibilities such as "^10.0.1" resolving to 11.3.6.
func lookupFrom(version map[string]string, parentPath, dep string) (string, bool) {
	scope := parentPath
	for {
		candidate := dep
		if scope == "" {
			candidate = "node_modules/" + dep
		} else {
			candidate = scope + "/node_modules/" + dep
		}
		if v, ok := version[candidate]; ok {
			return v, true
		}
		if scope == "" {
			return "", false
		}
		// Step out one nesting level. Scoped names contain a slash, so cut at the last
		// "/node_modules/" rather than at the last path separator.
		i := strings.LastIndex(scope, "/node_modules/")
		if i < 0 {
			scope = ""
			continue
		}
		scope = scope[:i]
	}
}

// ---------- resolution ----------

func evaluate(dd *depsDev, repo string, sn snapshot) []outcome {
	triples, err := extractTriples(repo, sn)
	if err != nil {
		return nil
	}

	// One timeline fetch per distinct dependency rather than per triple, and fetched concurrently.
	// A large lockfile names over a thousand packages, so doing this serially at a third of a second
	// per request turns one snapshot into several minutes.
	names := map[string]bool{}
	for _, t := range triples {
		names[t.dep] = true
	}
	var wg sync.WaitGroup
	work := make(chan string)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range work {
				dd.timeline(name)
			}
		}()
	}
	for name := range names {
		work <- name
	}
	close(work)
	wg.Wait()

	out := make([]outcome, 0, len(triples))
	for _, t := range triples {
		out = append(out, resolveOne(dd, t))
	}
	return out
}

func resolveOne(dd *depsDev, t triple) outcome {
	o := outcome{triple: t}

	r, err := semver.ParseRange(t.rangeStr)
	if err != nil {
		o.verdict = skipUnparseable
		return o
	}
	if r.IsAny() {
		o.verdict = skipUnresolvableRange
		return o
	}

	releases := dd.timeline(t.dep)
	if len(releases) == 0 {
		o.verdict = skipNoTimeline
		return o
	}

	// Canonical resolution: the highest satisfying version that existed at the snapshot date.
	var candidates []semver.Version
	for _, rel := range releases {
		if rel.published.After(t.when) {
			continue
		}
		candidates = append(candidates, rel.version)
	}
	best, found := semver.MaxSatisfying(candidates, r)
	if !found {
		o.verdict = skipNoCandidate
		return o
	}
	o.canonical = best.String()

	want, err := semver.Parse(t.resolved)
	if err != nil {
		o.verdict = skipUnparseable
		return o
	}
	if best.Compare(want) == 0 {
		o.verdict = agree
	} else {
		o.verdict = disagree
	}
	return o
}

// ---------- reporting ----------

func report(all []outcome) {
	counts := map[verdict]int{}
	for _, o := range all {
		counts[o.verdict]++
	}
	decided := counts[agree] + counts[disagree]

	fmt.Printf("\n%s\n", strings.Repeat("-", 72))
	fmt.Printf("triples extracted        %d\n", len(all))
	for _, v := range []verdict{skipUnresolvableRange, skipNoTimeline, skipUnparseable, skipNoCandidate} {
		if counts[v] > 0 {
			fmt.Printf("  %-56s %d\n", v, counts[v])
		}
	}
	fmt.Printf("comparable triples       %d\n", decided)
	if decided == 0 {
		fmt.Println("no comparable triples; nothing to conclude")
		return
	}
	rate := float64(counts[agree]) / float64(decided) * 100
	fmt.Printf("agree                    %d\n", counts[agree])
	fmt.Printf("disagree                 %d\n", counts[disagree])
	fmt.Printf("AGREEMENT RATE           %.1f%%\n", rate)

	// The shape of the disagreements matters more than the rate. Canonical resolution assumes a
	// fresh install, so a lockfile that pinned an older version because it was already installed
	// shows up as canonical being newer.
	var newer, older int
	for _, o := range all {
		if o.verdict != disagree {
			continue
		}
		c, err1 := semver.Parse(o.canonical)
		w, err2 := semver.Parse(o.resolved)
		if err1 != nil || err2 != nil {
			continue
		}
		if c.Compare(w) > 0 {
			newer++
		} else {
			older++
		}
	}
	fmt.Printf("\ndisagreement direction\n")
	fmt.Printf("  canonical newer than lockfile   %d   (lockfile kept an already installed version)\n", newer)
	fmt.Printf("  canonical older than lockfile   %d   (unexpected; investigate)\n", older)

	fmt.Printf("\nsample disagreements\n")
	shown := 0
	for _, o := range all {
		if o.verdict != disagree || shown >= 12 {
			continue
		}
		fmt.Printf("  %-22s %-16s %s  range %-18s lockfile %-14s canonical %s\n",
			o.repo, o.dep, o.when.Format("2006-01-02"), o.rangeStr, o.resolved, o.canonical)
		shown++
	}
}

// ---------- data sources ----------

type github struct{ token string }

func githubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (g *github) get(url string, into any) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// lockfileSnapshots samples commits that touched package-lock.json, spread across the file's history
// rather than clustered at the most recent ones, so the measurement covers several years.
func (g *github) lockfileSnapshots(repo string, n int) ([]snapshot, error) {
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits?path=package-lock.json&per_page=100", repo)
	if err := g.get(url, &commits); err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("no package-lock.json history")
	}

	idx := spread(len(commits), n)
	var out []snapshot
	for _, i := range idx {
		var content struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		u := fmt.Sprintf("https://api.github.com/repos/%s/contents/package-lock.json?ref=%s", repo, commits[i].SHA)
		if err := g.get(u, &content); err != nil {
			continue
		}
		if content.Encoding != "base64" {
			continue
		}
		body, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
		if err != nil {
			continue
		}
		out = append(out, snapshot{
			sha:  commits[i].SHA,
			when: commits[i].Commit.Committer.Date,
			body: body,
		})
	}
	return out, nil
}

// spread picks n indices distributed across [0,total).
func spread(total, n int) []int {
	if n >= total {
		out := make([]int, total)
		for i := range out {
			out[i] = i
		}
		return out
	}
	out := make([]int, 0, n)
	for i := range n {
		out = append(out, i*(total-1)/max(1, n-1))
	}
	return slices.Compact(out)
}

type release struct {
	version   semver.Version
	published time.Time
}

type depsDev struct {
	mu    sync.Mutex
	cache map[string][]release
}

// timeline returns a package's releases with publish times, from deps.dev. One call per package
// yields the whole history, which is why full registry packuments are not needed here.
func (d *depsDev) timeline(name string) []release {
	d.mu.Lock()
	if r, ok := d.cache[name]; ok {
		d.mu.Unlock()
		return r
	}
	d.mu.Unlock()

	var body struct {
		Versions []struct {
			VersionKey struct {
				Version string `json:"version"`
			} `json:"versionKey"`
			PublishedAt time.Time `json:"publishedAt"`
		} `json:"versions"`
	}
	url := "https://api.deps.dev/v3alpha/systems/npm/packages/" + strings.ReplaceAll(name, "/", "%2F")
	resp, err := http.Get(url)
	var out []release
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&body) == nil {
			for _, v := range body.Versions {
				ver, err := semver.Parse(v.VersionKey.Version)
				if err != nil {
					continue
				}
				out = append(out, release{version: ver, published: v.PublishedAt})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].published.Before(out[j].published) })

	d.mu.Lock()
	d.cache[name] = out
	d.mu.Unlock()
	return out
}
