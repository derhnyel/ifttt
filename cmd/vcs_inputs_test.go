package main

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"testing"
)

func TestRecursiveGlobSelection(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.MkdirAll("deep/nested", 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"root.go", "deep/nested/source.go"} {
		if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := expandSelectedFiles([]string{"**/*.go"})
	if err != nil || len(files) != 2 {
		t.Fatalf("recursive selection: %v %v", files, err)
	}
}

func TestSelectionRetainsLiteralErrorsAndGlobRegularFileChecks(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.Mkdir("directory.go", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("live.go", []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := expandTrackedFiles([]string{"missing.go"}, nil); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing literal lost its error: %v", err)
	}
	if _, err := expandTrackedFiles([]string{"directory.go"}, nil); err == nil {
		t.Fatal("directory literal was accepted")
	}
	files, err := expandTrackedFiles([]string{"*.go"}, []string{"missing.go", "directory.go", "live.go"})
	if err != nil || !reflect.DeepEqual(files, []string{"live.go"}) {
		t.Fatalf("nonregular or absent glob candidates survived: %v, %v", files, err)
	}
	t.Run("symlinks", func(t *testing.T) {
		if err := os.Symlink("live.go", "linked.go"); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.Symlink("directory.go", "linked-directory.go"); err != nil {
			t.Fatal(err)
		}
		files, err := expandTrackedFiles([]string{"*.go"}, []string{"linked.go", "linked-directory.go", "live.go"})
		if err != nil || !reflect.DeepEqual(files, []string{"linked.go", "live.go"}) {
			t.Fatalf("symlink selection changed: %v, %v", files, err)
		}
	})
}

func TestTrackedGlobExcludesUntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	for _, file := range []string{"tracked.go", "untracked.go"} {
		if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := expandTrackedFiles([]string{"*.go"}, []string{"tracked.go"})
	if err != nil || len(files) != 1 || files[0] != "tracked.go" {
		t.Fatalf("tracked selection: %v %v", files, err)
	}
}
