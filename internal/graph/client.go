package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/sumanthd032/keyholders/internal/config"
)

// Client is a Bolt connection to HydraDB. One client per process is enough: writes serialise behind
// a per-cell writer lease, so extra sessions contend rather than parallelise. Measured at sixteen
// concurrent sessions performing no better than one.
type Client struct {
	driver neo4j.DriverWithContext
}

// Open connects and verifies the credential. The driver defers its handshake, so connecting proves
// nothing about the token; only a statement that reaches the server does.
func Open(ctx context.Context, cfg config.Config) (*Client, error) {
	// The token is validated as the password and the username is ignored provided it is non-empty.
	drv, err := neo4j.NewDriverWithContext(cfg.BoltURI, neo4j.BasicAuth(cfg.BoltUser, cfg.BoltToken, ""))
	if err != nil {
		return nil, fmt.Errorf("bolt driver for %s: %w", cfg.BoltURI, err)
	}

	c := &Client{driver: drv}
	if _, err := c.Query(ctx, "MATCH (n {id: 0}) RETURN n.id AS id", nil); err != nil {
		drv.Close(ctx)
		return nil, fmt.Errorf("bolt handshake with %s: %w", cfg.BoltURI, err)
	}
	return c, nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.driver.Close(ctx)
}

// Query runs one statement and collects its records. HydraDB takes one statement per request and
// has no explicit multi-statement transaction, so there is nothing to compose here.
func (c *Client) Query(ctx context.Context, stmt string, params map[string]any) ([]*neo4j.Record, error) {
	sess := c.driver.NewSession(ctx, neo4j.SessionConfig{})
	defer sess.Close(ctx)

	res, err := sess.Run(ctx, stmt, params)
	if err != nil {
		return nil, err
	}
	return res.Collect(ctx)
}

// MaxBatch is HydraDB's admission control ceiling on UNWIND rows, from ClientConfig.max_parameters.
// It has no environment binding, so on the published image it cannot be raised.
const MaxBatch = 1024

// DefaultBatch is the measured throughput optimum on a graph large enough to be compacting.
// Measured at 60,000 edges per run: 4,426 edges/s at 256 rows against 3,354 at 1,024. On a small
// graph the order reverses, which is why the number comes from the larger run.
const DefaultBatch = 256

// WriteBatch runs stmt once per chunk of rows, binding each chunk as $rows. The statement must be a
// single UNWIND form: HydraDB accepts a list of maps only as UNWIND input, and only over the client
// transport. Returns the number of rows written.
func (c *Client) WriteBatch(ctx context.Context, stmt string, rows []map[string]any, batch int) (int, error) {
	if batch <= 0 {
		batch = DefaultBatch
	}
	if batch > MaxBatch {
		return 0, fmt.Errorf("batch size %d exceeds the HydraDB admission limit of %d", batch, MaxBatch)
	}

	sess := c.driver.NewSession(ctx, neo4j.SessionConfig{})
	defer sess.Close(ctx)

	written := 0
	for start := 0; start < len(rows); start += batch {
		end := min(start+batch, len(rows))

		chunk := make([]any, 0, end-start)
		for _, r := range rows[start:end] {
			chunk = append(chunk, r)
		}

		if err := runChunk(ctx, sess, stmt, chunk); err != nil {
			return written, fmt.Errorf("rows %d-%d of %d: %w", start, end, len(rows), err)
		}
		written += end - start
	}
	return written, nil
}

// runChunk retries transient failures, which is safe because every batch statement we issue is a
// MERGE and a MERGE that changes nothing still commits at the same cost as the original write.
func runChunk(ctx context.Context, sess neo4j.SessionWithContext, stmt string, chunk []any) error {
	const attempts = 3

	var err error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		var res neo4j.ResultWithContext
		if res, err = sess.Run(ctx, stmt, map[string]any{"rows": chunk}); err == nil {
			if _, err = res.Consume(ctx); err == nil {
				return nil
			}
		}
		if !retryable(err) {
			return err
		}
	}
	return err
}

// retryable distinguishes a genuine transient failure from admission control, which HydraDB also
// reports as a transient error. Admission control rejections are deterministic: the same statement
// with the same row count will be rejected again, so retrying only delays the real error.
func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if strings.Contains(err.Error(), "rejected by admission control") {
		return false
	}
	return neo4j.IsRetryable(err) || neo4j.IsConnectivityError(err)
}
