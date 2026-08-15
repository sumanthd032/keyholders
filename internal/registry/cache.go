package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// entry is one cached response. The body is stored beside the metadata rather than inside it, so a
// multi-megabyte document does not have to be base64 encoded through JSON on every read.
type entry struct {
	URL      string    `json:"url"`
	ETag     string    `json:"etag,omitempty"`
	Fetched  time.Time `json:"fetched"`
	NotFound bool      `json:"not_found,omitempty"`

	Body []byte `json:"-"`
}

// cache is a content-addressed directory of fetched responses. It exists so that an interrupted
// ingest resumes without refetching, and so that a rerun during development costs nothing. It is
// safe to delete: the only cost is the refetch.
type cache struct {
	dir string
}

func newCache(dir string) (*cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir %s: %w", dir, err)
	}
	return &cache{dir: dir}, nil
}

// paths fans entries out over 256 subdirectories. A single directory holding a million small files
// makes every lookup slow on most filesystems, and a bulk ingest creates exactly that many.
func (c *cache) paths(url string) (meta, body string) {
	sum := sha256.Sum256([]byte(url))
	key := hex.EncodeToString(sum[:])
	dir := filepath.Join(c.dir, key[:2])
	return filepath.Join(dir, key+".json"), filepath.Join(dir, key+".body")
}

// get returns the stored entry, or nil when nothing is cached.
func (c *cache) get(url string) (*entry, error) {
	metaPath, bodyPath := c.paths(url)

	raw, err := os.ReadFile(metaPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache meta %s: %w", metaPath, err)
	}

	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		// A truncated entry from a killed process is not worth failing over; refetching is cheap.
		return nil, nil
	}
	if e.NotFound {
		return &e, nil
	}

	e.Body, err = os.ReadFile(bodyPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache body %s: %w", bodyPath, err)
	}
	return &e, nil
}

func (c *cache) put(url string, body []byte, etag string) error {
	metaPath, bodyPath := c.paths(url)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return fmt.Errorf("create cache shard: %w", err)
	}
	// Body first: a meta file without its body reads as a miss, whereas a body without meta is
	// simply ignored. Ordering it this way means a crash never produces a claim we cannot honour.
	if err := writeAtomic(bodyPath, body); err != nil {
		return err
	}
	return c.writeMeta(metaPath, entry{URL: url, ETag: etag, Fetched: time.Now()})
}

func (c *cache) putNotFound(url string) error {
	metaPath, _ := c.paths(url)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return fmt.Errorf("create cache shard: %w", err)
	}
	return c.writeMeta(metaPath, entry{URL: url, Fetched: time.Now(), NotFound: true})
}

// touch records that a revalidation confirmed the stored body, so the next run inside maxAge skips
// the request entirely instead of asking again.
func (c *cache) touch(url string, e *entry) error {
	metaPath, _ := c.paths(url)
	return c.writeMeta(metaPath, entry{URL: url, ETag: e.ETag, Fetched: time.Now()})
}

func (c *cache) writeMeta(path string, e entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode cache meta: %w", err)
	}
	return writeAtomic(path, raw)
}

// writeAtomic keeps a killed process from leaving a half-written file that later reads as valid.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}
