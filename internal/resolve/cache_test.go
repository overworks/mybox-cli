package resolve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
)

// tempCache points the cache at an isolated directory and returns a fixed clock
// the test can advance.
func tempCache(t *testing.T) (*Cache, *time.Time) {
	t.Helper()
	t.Setenv(EnvCacheHome, t.TempDir())

	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	c := LoadCache("fp", DefaultTTL)
	c.now = func() time.Time { return now }
	return c, &now
}

func TestCachePutGet(t *testing.T) {
	c, _ := tempCache(t)

	c.Put("/문서", "DOC", api.TypeFolder)
	e, ok := c.Get("/문서")
	if !ok {
		t.Fatal("Get returned no entry after Put")
	}
	if e.ID != "DOC" || e.Type != api.TypeFolder {
		t.Errorf("entry = %+v", e)
	}
	if _, ok := c.Get("/사진"); ok {
		t.Error("Get returned an entry that was never stored")
	}
}

func TestCacheEntriesExpire(t *testing.T) {
	c, now := tempCache(t)
	c.Put("/문서", "DOC", api.TypeFolder)

	*now = now.Add(DefaultTTL - time.Minute)
	if _, ok := c.Get("/문서"); !ok {
		t.Error("entry expired before its TTL")
	}

	*now = now.Add(2 * time.Minute)
	if _, ok := c.Get("/문서"); ok {
		t.Error("entry survived past its TTL")
	}
}

func TestCacheInvalidateRemovesDescendants(t *testing.T) {
	c, _ := tempCache(t)
	c.Put("/문서", "DOC", api.TypeFolder)
	c.Put("/문서/2026", "Y", api.TypeFolder)
	c.Put("/문서/2026/회의록.pdf", "M", api.TypeFile)
	c.Put("/문서2026", "OTHER", api.TypeFolder) // name prefix, not a path prefix
	c.Put("/사진", "PIC", api.TypeFolder)

	// Renaming or moving /문서 changes the path of everything beneath it.
	c.Invalidate("/문서")

	for _, gone := range []string{"/문서", "/문서/2026", "/문서/2026/회의록.pdf"} {
		if _, ok := c.Get(gone); ok {
			t.Errorf("%s survived invalidation", gone)
		}
	}
	for _, kept := range []string{"/문서2026", "/사진"} {
		if _, ok := c.Get(kept); !ok {
			t.Errorf("%s was invalidated but should not have been", kept)
		}
	}
}

func TestCacheInvalidateRootClearsEverything(t *testing.T) {
	c, _ := tempCache(t)
	c.Put("/문서", "DOC", api.TypeFolder)
	c.Put("/사진", "PIC", api.TypeFolder)

	c.Invalidate("/")
	if c.Len() != 0 {
		t.Errorf("Len() = %d after invalidating the root, want 0", c.Len())
	}
}

func TestCacheSurvivesRoundTrip(t *testing.T) {
	t.Setenv(EnvCacheHome, t.TempDir())

	first := LoadCache("fp", DefaultTTL)
	first.Put("/문서", "DOC", api.TypeFolder)
	if err := first.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	second := LoadCache("fp", DefaultTTL)
	e, ok := second.Get("/문서")
	if !ok || e.ID != "DOC" {
		t.Errorf("entry after reload = %+v, ok = %v", e, ok)
	}
}

func TestCacheIsKeyedByFingerprint(t *testing.T) {
	t.Setenv(EnvCacheHome, t.TempDir())

	a := LoadCache("account-a", DefaultTTL)
	a.Put("/문서", "A_DOC", api.TypeFolder)
	if err := a.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A second account must not see the first account's IDs.
	b := LoadCache("account-b", DefaultTTL)
	if _, ok := b.Get("/문서"); ok {
		t.Error("a different account read the first account's cache")
	}
}

func TestCacheFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCacheHome, dir)

	c := LoadCache("fp", DefaultTTL)
	c.Put("/문서", "DOC", api.TypeFolder)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "fp", "paths.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %04o, want 0600", perm)
	}
}

func TestCacheSaveIsANoopWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCacheHome, dir)

	c := LoadCache("fp", DefaultTTL)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fp", "paths.json")); err == nil {
		t.Error("Save wrote a file even though nothing changed")
	}
}

func TestCacheDropsExpiredEntriesOnSave(t *testing.T) {
	t.Setenv(EnvCacheHome, t.TempDir())

	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	c := LoadCache("fp", DefaultTTL)
	c.now = func() time.Time { return now }
	c.Put("/오래된", "OLD", api.TypeFolder)

	now = now.Add(2 * DefaultTTL)
	c.Put("/새것", "NEW", api.TypeFolder)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Without pruning, a long-lived cache would grow without bound.
	reloaded := LoadCache("fp", DefaultTTL)
	if _, ok := reloaded.entries["/오래된"]; ok {
		t.Error("an expired entry was written back to disk")
	}
	if _, ok := reloaded.entries["/새것"]; !ok {
		t.Error("a fresh entry was pruned")
	}
}

func TestCorruptCacheFileStartsCold(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCacheHome, dir)
	if err := os.MkdirAll(filepath.Join(dir, "fp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fp", "paths.json"), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A damaged cache costs API calls, never a failed command.
	c := LoadCache("fp", DefaultTTL)
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
	c.Put("/문서", "DOC", api.TypeFolder)
	if err := c.Save(); err != nil {
		t.Errorf("Save after a corrupt load: %v", err)
	}
}

func TestOldCacheVersionIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCacheHome, dir)
	if err := os.MkdirAll(filepath.Join(dir, "fp"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"version":0,"entries":{"/문서":{"id":"STALE","type":"folder","at":9999999999}}}`
	if err := os.WriteFile(filepath.Join(dir, "fp", "paths.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c := LoadCache("fp", DefaultTTL)
	if _, ok := c.Get("/문서"); ok {
		t.Error("an entry from an older cache format was trusted")
	}
}

func TestDisabledCacheStoresNothing(t *testing.T) {
	c := NewDisabledCache()

	c.Put("/문서", "DOC", api.TypeFolder)
	if _, ok := c.Get("/문서"); ok {
		t.Error("a disabled cache returned an entry")
	}
	if c.Path() != "" {
		t.Errorf("Path() = %q, want empty for a disabled cache", c.Path())
	}
	if err := c.Save(); err != nil {
		t.Errorf("Save on a disabled cache: %v", err)
	}
}

func TestNilCacheIsSafe(t *testing.T) {
	var c *Cache

	if _, ok := c.Get("/문서"); ok {
		t.Error("a nil cache returned an entry")
	}
	c.Put("/문서", "DOC", api.TypeFolder)
	c.Invalidate("/문서")
	c.Clear()
	if c.Len() != 0 || c.Path() != "" {
		t.Error("a nil cache reported state")
	}
	if err := c.Save(); err != nil {
		t.Errorf("Save on a nil cache: %v", err)
	}
}
