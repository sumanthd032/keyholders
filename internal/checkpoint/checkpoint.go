// Package checkpoint records which units of work have been committed, so an interrupted run resumes
// rather than starting over.
package checkpoint

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// File is a plain append-only list of completed names. Both passes that use it are idempotent,
// since every write is a MERGE keyed by a deterministic id, so the worst a duplicate costs is a
// repeated write.
//
// Correctness rests on ordering. A name is recorded only after its rows have been flushed and
// acknowledged, so a crash can lose the record of work that was done, never claim work that was not.
type File struct {
	mu   sync.Mutex
	file *os.File
	done map[string]bool
}

// Open reads any existing checkpoint at path and opens it for appending.
func Open(path string) (*File, error) {
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
	return &File{file: f, done: done}, nil
}

// Has reports whether a name was already committed.
func (c *File) Has(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done[name]
}

// Record marks names complete and flushes to disk immediately. Buffering would make the checkpoint
// claim more progress than the graph holds after a kill.
func (c *File) Record(names []string) error {
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

// Count is how many names have been committed across all runs.
func (c *File) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.done)
}

func (c *File) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.file.Close()
}
