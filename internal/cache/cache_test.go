package cache

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeEntry struct {
	Value string `json:"value"`
}

func TestStoreAndLoad(t *testing.T) {
	setupCacheDir(t)

	var e fakeEntry
	hash := "abc123"
	want := fakeEntry{Value: "hello"}
	if err := Store(hash, want); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := Load(hash, &e); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e != want {
		t.Fatalf("got %+v, want %+v", e, want)
	}
}

func TestLoadNotFound(t *testing.T) {
	setupCacheDir(t)

	var e fakeEntry
	err := Load("missing", &e)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func setupCacheDir(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("IFLINT_CACHE_DIR", filepath.Join(tmp, "cache"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	// ensure darwin path exists too
	_ = os.MkdirAll(filepath.Join(tmp, "Library", "Caches"), 0o755)
}

func TestPathBearingCacheKeys(t *testing.T) {
	setupCacheDir(t)
	key := "directives:../../repo/nested/file.go"
	want := fakeEntry{Value: "isolated"}
	if err := Store(key, want); err != nil {
		t.Fatal(err)
	}
	var got fakeEntry
	if err := Load(key, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v", got)
	}
	name, err := keyName(key)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := dir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(name) != dir {
		t.Fatalf("cache escaped directory: %s", name)
	}
}
