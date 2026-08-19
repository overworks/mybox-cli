package resolve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// EnvCacheHome overrides where the path cache is stored.
const EnvCacheHome = "MYBOX_CACHE_HOME"

// DefaultTTL is how long a cached path stays trusted. Resources can be moved or
// renamed from the web UI without the CLI hearing about it, so entries expire
// rather than living forever.
const DefaultTTL = 24 * time.Hour

// cacheVersion is bumped when the on-disk shape changes, so an old file is
// discarded instead of misread.
const cacheVersion = 1

// Entry is one remembered path lookup.
type Entry struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	At   int64  `json:"at"` // Unix seconds
}

// Cache remembers path-to-ID lookups for one account.
//
// It exists to conserve the API's per-minute call budget: without it, every
// command that takes a path re-walks the folder tree from the root. A miss is
// always safe, so the cache fails silently rather than blocking work.
type Cache struct {
	path    string
	ttl     time.Duration
	entries map[string]Entry
	dirty   bool
	// disabled short-circuits every operation, for --no-cache.
	disabled bool
	now      func() time.Time
}

// CacheDir returns the cache directory for a token fingerprint. Keying by
// fingerprint keeps two accounts' path maps from colliding.
func CacheDir(fingerprint string) (string, error) {
	if home := os.Getenv(EnvCacheHome); home != "" {
		return filepath.Join(home, fingerprint), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not locate a cache directory: %w", err)
	}
	return filepath.Join(dir, "mybox", fingerprint), nil
}

// NewDisabledCache returns a cache that never stores or returns anything.
func NewDisabledCache() *Cache {
	return &Cache{disabled: true, entries: map[string]Entry{}, now: time.Now}
}

// LoadCache reads the cache for a token fingerprint. A missing or unreadable
// file yields an empty cache rather than an error: a cold cache only costs API
// calls, and refusing to run would be worse.
func LoadCache(fingerprint string, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	c := &Cache{ttl: ttl, entries: map[string]Entry{}, now: time.Now}

	dir, err := CacheDir(fingerprint)
	if err != nil {
		c.disabled = true
		return c
	}
	c.path = filepath.Join(dir, "paths.json")

	raw, err := os.ReadFile(c.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// An unreadable cache is not worth failing over; start cold.
			c.entries = map[string]Entry{}
		}
		return c
	}

	var file struct {
		Version int              `json:"version"`
		Entries map[string]Entry `json:"entries"`
	}
	if json.Unmarshal(raw, &file) != nil || file.Version != cacheVersion {
		return c
	}
	if file.Entries != nil {
		c.entries = file.Entries
	}
	return c
}

// Get returns a cached entry if it exists and has not expired.
func (c *Cache) Get(path string) (Entry, bool) {
	if c == nil || c.disabled {
		return Entry{}, false
	}
	e, ok := c.entries[path]
	if !ok {
		return Entry{}, false
	}
	if c.now().Sub(time.Unix(e.At, 0)) > c.ttl {
		delete(c.entries, path)
		c.dirty = true
		return Entry{}, false
	}
	return e, true
}

// Put records a lookup.
func (c *Cache) Put(path, id, typ string) {
	if c == nil || c.disabled || path == "" || id == "" {
		return
	}
	c.entries[path] = Entry{ID: id, Type: typ, At: c.now().Unix()}
	c.dirty = true
}

// Invalidate drops a path and everything beneath it. Renaming or moving a folder
// changes the path of every descendant, so a targeted delete is not enough.
func (c *Cache) Invalidate(path string) {
	if c == nil || c.disabled || path == "" {
		return
	}
	for cached := range c.entries {
		if IsAncestor(path, cached) {
			delete(c.entries, cached)
			c.dirty = true
		}
	}
}

// Clear empties the cache.
func (c *Cache) Clear() {
	if c == nil || c.disabled {
		return
	}
	if len(c.entries) > 0 {
		c.entries = map[string]Entry{}
		c.dirty = true
	}
}

// Len reports how many entries are held.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

// Path reports the cache file location, or "" when caching is disabled.
func (c *Cache) Path() string {
	if c == nil || c.disabled {
		return ""
	}
	return c.path
}

// Save writes the cache back to disk if anything changed.
//
// Failures are returned but callers generally log and continue: a cache that
// could not be written costs API calls next time, nothing more.
func (c *Cache) Save() error {
	if c == nil || c.disabled || !c.dirty || c.path == "" {
		return nil
	}
	c.pruneExpired()

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create the cache directory: %w", err)
	}
	raw, err := json.Marshal(struct {
		Version int              `json:"version"`
		Entries map[string]Entry `json:"entries"`
	}{Version: cacheVersion, Entries: c.entries})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".paths-*.json")
	if err != nil {
		return fmt.Errorf("could not create a temporary cache file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// The cache maps paths to IDs for one account; keep it owner-only like the
	// config it is derived from.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("could not save the cache: %w", err)
	}
	c.dirty = false
	return nil
}

// pruneExpired drops stale entries so the file cannot grow without bound.
func (c *Cache) pruneExpired() {
	cutoff := c.now().Add(-c.ttl).Unix()
	for path, e := range c.entries {
		if e.At < cutoff {
			delete(c.entries, path)
		}
	}
}
