package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func command(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "JJ_CONFIG=")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v %s", name, args, err, out)
	}
	return string(out)
}
func TestGitNativeWorkingStagedRevisionAndFiles(t *testing.T) {
	root := t.TempDir()
	command(t, root, "git", "init", "-q")
	command(t, root, "git", "config", "user.email", "test@example.invalid")
	command(t, root, "git", "config", "user.name", "Test")
	os.WriteFile(filepath.Join(root, "a space.go"), []byte("old\n"), 0644)
	command(t, root, "git", "add", ".")
	command(t, root, "git", "commit", "-qm", "initial")
	os.WriteFile(filepath.Join(root, "a space.go"), []byte("new\n"), 0644)
	b, err := Open(context.Background(), root, "auto")
	if err != nil || b.Kind != "git" {
		t.Fatalf("%+v %v", b, err)
	}
	patch, err := b.Diff(context.Background(), Request{})
	if err != nil || !strings.Contains(patch, "+new") {
		t.Fatalf("%s %v", patch, err)
	}
	patch, err = b.Diff(context.Background(), Request{Staged: true})
	if err != nil || patch != "" {
		t.Fatalf("%s %v", patch, err)
	}
	command(t, root, "git", "add", ".")
	command(t, root, "git", "commit", "-qm", "update\n\nNO_IFTTT=deferred")
	patch, err = b.Diff(context.Background(), Request{Review: true})
	if err != nil || !strings.Contains(patch, "+new") {
		t.Fatalf("%s %v", patch, err)
	}
	messages, err := b.Messages(context.Background(), Request{Review: true})
	if err != nil || !strings.Contains(messages, "NO_IFTTT=deferred") {
		t.Fatalf("%s %v", messages, err)
	}
	files, err := b.Files(context.Background())
	if err != nil || len(files) != 1 || files[0] != "a space.go" {
		t.Fatalf("%v %v", files, err)
	}
	if _, err = b.Diff(context.Background(), Request{Revision: "--output=bad"}); err == nil {
		t.Fatal("option-like revision accepted")
	}
}
func TestAutoDetectAncestorAndColocatedJJ(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0755)
	os.Mkdir(filepath.Join(root, ".jj"), 0755)
	nested := filepath.Join(root, "sub")
	os.Mkdir(nested, 0755)
	kind, _, err := Detect(nested)
	if err != nil || kind != "jj" {
		t.Fatalf("%s %v", kind, err)
	}
	if _, err := Open(context.Background(), nested, "invalid"); err == nil {
		t.Fatal("invalid backend accepted")
	}
}
func TestJJNativeRevisionsAndFiles(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not installed")
	}
	root := t.TempDir()
	command(t, root, "jj", "git", "init")
	command(t, root, "jj", "config", "set", "--repo", "user.name", "Test")
	command(t, root, "jj", "config", "set", "--repo", "user.email", "test@example.invalid")
	os.WriteFile(filepath.Join(root, "file.go"), []byte("old\n"), 0644)
	command(t, root, "jj", "describe", "-m", "initial")
	command(t, root, "jj", "new")
	os.WriteFile(filepath.Join(root, "file.go"), []byte("new\n"), 0644)
	command(t, root, "jj", "describe", "-m", "update\n\nNO_IFTTT=deferred")
	b, err := Open(context.Background(), root, "auto")
	if err != nil || b.Kind != "jj" {
		t.Fatalf("%+v %v", b, err)
	}
	patch, err := b.Diff(context.Background(), Request{Revision: "@"})
	if err != nil || !strings.Contains(patch, "+new") {
		t.Fatalf("%s %v", patch, err)
	}
	files, err := b.Files(context.Background())
	if err != nil || len(files) != 1 || files[0] != "file.go" {
		t.Fatalf("%v %v", files, err)
	}
	messages, err := b.Messages(context.Background(), Request{Revision: "@"})
	if err != nil || !strings.Contains(messages, "NO_IFTTT=deferred") {
		t.Fatalf("%s %v", messages, err)
	}
	if _, err = b.Diff(context.Background(), Request{Staged: true}); err == nil {
		t.Fatal("jj staging accepted")
	}
}

func TestGitUserDiffPrefixesAndDeletedTrackedFiles(t *testing.T) {
	root := t.TempDir()
	command(t, root, "git", "init", "-q")
	command(t, root, "git", "config", "user.email", "test@example.invalid")
	command(t, root, "git", "config", "user.name", "Test")
	path := filepath.Join(root, "file.go")
	os.WriteFile(path, []byte("old\n"), 0644)
	command(t, root, "git", "add", ".")
	command(t, root, "git", "commit", "-qm", "initial")
	command(t, root, "git", "config", "diff.srcPrefix", "old/")
	command(t, root, "git", "config", "diff.dstPrefix", "new/")
	os.WriteFile(path, []byte("new\n"), 0644)
	b, err := Open(context.Background(), root, "git")
	if err != nil {
		t.Fatal(err)
	}
	patch, err := b.Diff(context.Background(), Request{})
	if err != nil || !strings.Contains(patch, "--- a/file.go") || !strings.Contains(patch, "+++ b/file.go") {
		t.Fatalf("prefix config corrupted paths: %s %v", patch, err)
	}
	os.Remove(path)
	files, err := b.Files(context.Background())
	if err != nil || len(files) != 0 {
		t.Fatalf("deleted tracked files in structural list: %v %v", files, err)
	}
}

func TestGitDirectiveFilesFiltersIgnoredAndNonDirectiveFiles(t *testing.T) {
	root := t.TempDir()
	command(t, root, "git", "init", "-q")
	files := map[string]string{".gitignore": "ignored/\n", "source.go": "// LINT.IfChange(contract)\n", "plain.go": "package plain\n"}
	for path, text := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command(t, root, "git", "add", ".")
	if err := os.Mkdir(filepath.Join(root, "ignored"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored", "bad.go"), []byte("// LINT.IfChange(bad)"), 0600); err != nil {
		t.Fatal(err)
	}
	backend, err := Open(context.Background(), root, "git")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := backend.DirectiveFiles(context.Background(), "LINT.")
	if err != nil || len(hits) != 1 || hits[0] != "source.go" {
		t.Fatalf("hits %v: %v", hits, err)
	}
	hits, err = backend.DirectiveFiles(context.Background(), "NONEXISTENT.")
	if err != nil || len(hits) != 0 {
		t.Fatalf("empty hits %v: %v", hits, err)
	}
}
