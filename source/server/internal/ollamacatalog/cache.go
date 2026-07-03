// Package ollamacatalog — cache.go: on-disk cache of the fetched catalog
// so the CLI's model-picker page doesn't hit ollama.com on every open.
//
// Storage: a single JSON file at a caller-provided path (typically
// ~/.config/cercano/catalog-cache.json). One writer at a time; concurrent
// readers are safe because writes go through an atomic tempfile-and-
// rename (writeFile below) and readers just do a one-shot Unmarshal.
//
// Freshness policy: TTL is the caller's choice — the cache itself just
// records fetched_at and lets the caller decide "stale enough to
// refresh?" via IsStale(now, ttl). A default TTL of 24h is what the
// UI plans to use, but the cache doesn't force it, so tests can inject
// short TTLs and CLIs can expose a "refresh now" that ignores TTL.
package ollamacatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Cache is the on-disk cache format written to and read from
// catalog-cache.json. Field names are stable — external tools (grep,
// jq) can rely on them.
type Cache struct {
	// FetchedAt is when this cache was last written. Zero value means
	// "no cache" (see Load).
	FetchedAt time.Time `json:"fetched_at"`
	// Source records where the catalog came from — helpful if we ever
	// support alternative sources (a mirror, a local file) and want to
	// distinguish stale cache from a source-switched cache.
	Source string `json:"source"`
	// Models is the fetched list (family names + tags, if we've walked
	// tags into the cache).
	Models []Model `json:"models"`
	// Estimates maps "name:tag" refs to resolved RAM-estimation
	// numbers (see estimate.go). Nil on caches written by older
	// builds — treated as empty.
	Estimates map[string]Estimate `json:"estimates,omitempty"`
}

// IsStale reports whether the cache should be refreshed. A zero
// FetchedAt (meaning "no cache") is always considered stale so the
// first ListModels call blocks on a real fetch.
func (c *Cache) IsStale(now time.Time, ttl time.Duration) bool {
	if c == nil || c.FetchedAt.IsZero() {
		return true
	}
	return now.Sub(c.FetchedAt) > ttl
}

// Load reads the JSON cache at path. Missing file returns (nil, nil) —
// callers treat "no cache" the same as "empty cache" and refresh. A
// corrupt cache file returns an error; the caller decides whether to
// overwrite it (typical for the "next refresh" path) or bail.
func Load(path string) (*Cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ollamacatalog: read cache %s: %w", path, err)
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("ollamacatalog: parse cache %s: %w", path, err)
	}
	return &c, nil
}

// Save atomically writes the cache to path. Parent directory is created
// if needed. Write goes through a temp file + rename so a partial write
// during a crash never leaves a corrupt catalog for the next process.
func (c *Cache) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ollamacatalog: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// writeFile writes bytes to path atomically via a temp file in the same
// directory (so the rename is on the same filesystem). If the temp file
// can't be renamed, we clean it up on the failure path.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// If anything below fails, remove the temp file so we don't leak.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	success = true
	return nil
}
