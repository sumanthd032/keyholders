// Package registry fetches the public data the graph is built from: version timelines and resolved
// dependency graphs from deps.dev, per-version documents from the npm registry, and the download
// ranking from ecosyste.ms.
//
// Full packuments are deliberately not fetched. They average 4.2 MB and reach 30 MB for `next`,
// which puts 50,000 packages at 213 GB against a measured link ceiling of 4.3 MB/s. The abbreviated
// packument is 2.4x smaller but drops both `time` and `maintainers`, which are the temporal and
// human layers, so it is useless here. The three endpoints below carry the same information at
// roughly 10 KB per package.
package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrNotFound is returned for a 404. Packages are unpublished and deps.dev does not cover every
// name, so a missing document is an ordinary outcome during a bulk ingest rather than a failure.
var ErrNotFound = errors.New("not found")

// userAgent identifies the client to the services being used. ecosyste.ms answers 403 to a request
// carrying a default library user agent, and the npm registry asks for contact details in its
// acceptable use policy, so this is a courtesy and a requirement at the same time.
const userAgent = "keyholders/0.1 (+https://github.com/sumanthd032/keyholders)"

// DefaultRate is the sustained request rate the ingest holds itself to. Measured: throughput
// plateaus at about 30 requests/s and 4.3 MB/s, identical at 16 and 32 workers, so it is bandwidth
// bound rather than concurrency bound. Asking for more only produces 429s.
const DefaultRate = 30

// Client is a rate-limited HTTP client with an on-disk cache. One instance is shared by every
// source client, so the rate limit applies across all of them rather than per host.
type Client struct {
	http  *http.Client
	cache *cache
	lim   *limiter
}

func New(cacheDir string, requestsPerSecond float64) (*Client, error) {
	c, err := newCache(cacheDir)
	if err != nil {
		return nil, err
	}
	return &Client{
		http:  &http.Client{Timeout: 60 * time.Second},
		cache: c,
		lim:   newLimiter(requestsPerSecond),
	}, nil
}

// Get returns the body for url, serving it from the cache when a stored copy is younger than
// maxAge. A stored copy older than that is revalidated with its ETag, which usually comes back 304
// and costs a request but no bandwidth. Pass maxAge of 0 to always revalidate.
func (c *Client) Get(ctx context.Context, url string, maxAge time.Duration) ([]byte, error) {
	entry, err := c.cache.get(url)
	if err != nil {
		return nil, err
	}
	if entry != nil {
		if entry.NotFound {
			return nil, fmt.Errorf("%s: %w", url, ErrNotFound)
		}
		if maxAge > 0 && time.Since(entry.Fetched) < maxAge {
			return entry.Body, nil
		}
	}

	body, err := c.fetch(ctx, url, entry)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Remember the 404 so a resumed ingest does not re-ask for every unpublished package.
			if err := c.cache.putNotFound(url); err != nil {
				return nil, err
			}
		}
		// A stale copy beats failing the whole ingest when the service is briefly unavailable.
		if entry != nil && !errors.Is(err, ErrNotFound) {
			return entry.Body, nil
		}
		return nil, err
	}
	return body, nil
}

// maxAttempts covers a rate limit response plus a transient server error without turning a genuine
// outage into a long silent stall.
const maxAttempts = 4

func (c *Client) fetch(ctx context.Context, url string, cached *entry) ([]byte, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if err := c.lim.wait(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request for %s: %w", url, err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		if cached != nil && cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("get %s: %w", url, err)
			if err := backoff(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}

		switch {
		case resp.StatusCode == http.StatusNotModified:
			resp.Body.Close()
			if err := c.cache.touch(url, cached); err != nil {
				return nil, err
			}
			return cached.Body, nil

		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("%s: %w", url, ErrNotFound)

		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			lastErr = fmt.Errorf("get %s: %s", url, resp.Status)
			if err := backoff(ctx, attempt, retryAfter); err != nil {
				return nil, err
			}
			continue

		case resp.StatusCode != http.StatusOK:
			resp.Body.Close()
			return nil, fmt.Errorf("get %s: %s", url, resp.Status)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read %s: %w", url, err)
			if err := backoff(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}

		if err := c.cache.put(url, body, resp.Header.Get("ETag")); err != nil {
			return nil, err
		}
		return body, nil
	}
	return nil, lastErr
}

func backoff(ctx context.Context, attempt int, retryAfter time.Duration) error {
	d := retryAfter
	if d == 0 {
		d = time.Duration(1<<attempt) * 500 * time.Millisecond
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// limiter spaces requests evenly rather than allowing a burst then a stall. An even cadence is what
// keeps a long ingest below the rate ceiling: a token bucket that permits a burst of 30 gets the
// first second of every minute rejected and then backs off, which is slower overall.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newLimiter(requestsPerSecond float64) *limiter {
	if requestsPerSecond <= 0 {
		requestsPerSecond = DefaultRate
	}
	return &limiter{interval: time.Duration(float64(time.Second) / requestsPerSecond)}
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	at := l.next
	l.next = l.next.Add(l.interval)
	l.mu.Unlock()

	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
