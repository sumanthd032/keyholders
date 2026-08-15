// Package config resolves the small number of settings that differ between a developer machine and
// any other environment. Everything else is a constant in the package that owns it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the runtime environment: where HydraDB is, and where the cache and resume state live.
type Config struct {
	BoltURI  string
	BoltUser string
	// BoltToken is validated by HydraDB as the password. The username is ignored provided it is
	// non-empty, which is why BoltUser has a default that is never configured in practice.
	BoltToken string

	// CacheDir holds fetched registry responses keyed by URL. It is content addressed and safe to
	// delete; deleting it only costs a refetch.
	CacheDir string

	// StateDir holds ingest checkpoints. Deleting it makes the next ingest start from the top.
	StateDir string
}

// defaultToken matches what the Makefile writes into deploy/hydradb-data/auth-token, so a checkout
// that has only run `make up` needs no environment at all.
const defaultToken = "local-development-token-32-bytes"

// Load reads the environment, falling back to the local development deployment.
func Load() (Config, error) {
	c := Config{
		BoltURI:  env("KEYHOLDERS_BOLT_URI", "bolt://127.0.0.1:7687"),
		BoltUser: env("KEYHOLDERS_BOLT_USER", "neo4j"),
		CacheDir: env("KEYHOLDERS_CACHE_DIR", filepath.Join("var", "cache")),
		StateDir: env("KEYHOLDERS_STATE_DIR", filepath.Join("var", "state")),
	}

	token, err := loadToken()
	if err != nil {
		return Config{}, err
	}
	c.BoltToken = token

	for _, dir := range []string{c.CacheDir, c.StateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Config{}, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return c, nil
}

// loadToken prefers the environment, then the token file the compose deployment shares with the
// container, then the development default.
func loadToken() (string, error) {
	if t := os.Getenv("KEYHOLDERS_BOLT_TOKEN"); t != "" {
		return t, nil
	}

	path := env("KEYHOLDERS_BOLT_TOKEN_FILE", filepath.Join("deploy", "hydradb-data", "auth-token"))
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		t := strings.TrimSpace(string(b))
		if t == "" {
			return "", fmt.Errorf("token file %s is empty", path)
		}
		return t, nil
	case os.IsNotExist(err):
		return defaultToken, nil
	default:
		return "", fmt.Errorf("read token file %s: %w", path, err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
