package ingest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// checkpoint records which packages have been written, so an interrupted run resumes rather than
// starting over. It is a plain append-only list of names: the ingest is idempotent, since every
// write is a MERGE keyed by a deterministic id, so the worst a duplicate costs is a repeated write.
//
// Correctness rests on ordering. A package is recorded only after its rows have been flushed and
// acknowledged, so a crash can lose the record of work that was done, never claim work that was not.
type checkpoint struct {
	mu   sync.Mutex
	file *os.File
	done map[string]bool
}

func openCheckpoint(path string) (*checkpoint, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create checkpoint dir: %w", err)
	}

	done := map[string]bool{}
	existing, err := os.Open(path)
	switch {
	case err == nil:
		scan := bufio.NewScanner(existing)
		// Package names are short, but the default 64 KB token limit is worth raising rather than
		// having a corrupt line silently truncate the resume set.
		scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scan.Scan() {
			if name := scan.Text(); name != "" {
				done[name] = true
			}
		}
		existing.Close()
		if err := scan.Err(); err != nil {
			return nil, fmt.Errorf("read checkpoint %s: %w", path, err)
		}
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("open checkpoint %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint %s for append: %w", path, err)
	}
	return &checkpoint{file: f, done: done}, nil
}

func (c *checkpoint) has(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done[name]
}

// record marks a package complete and flushes it to disk immediately. Buffering would make the
// checkpoint claim more progress than the graph holds after a kill.
func (c *checkpoint) record(names []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, name := range names {
		if _, err := fmt.Fprintln(c.file, name); err != nil {
			return fmt.Errorf("write checkpoint: %w", err)
		}
		c.done[name] = true
	}
	return c.file.Sync()
}

func (c *checkpoint) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.done)
}

func (c *checkpoint) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.Close()
}
