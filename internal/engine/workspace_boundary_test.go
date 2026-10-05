package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceProviderRejectsMissingTargetThroughExternalSymlink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	provider := workspaceFileProvider{root: root}
	_, err := provider.ReadFile(filepath.Join(root, "linked", "missing.go"))
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("boundary not enforced: %v", err)
	}
}

func TestWorkspaceProviderAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.go")
	if err := os.WriteFile(target, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	data, err := (&workspaceFileProvider{root: root}).ReadFile(filepath.Join(root, "linked.go"))
	if err != nil || string(data) != "content" {
		t.Fatalf("internal target: %q %v", data, err)
	}
}

func TestWorkspaceReadsConsistentWithinInvocation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "contract.go")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := workspaceFileProvider{root: root}
	first, err := provider.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	repeated, err := provider.ReadFile(path)
	if err != nil || string(repeated) != string(first) {
		t.Fatalf("repeated read changed invocation snapshot: %q %v", repeated, err)
	}
	fresh := workspaceFileProvider{root: root}
	current, err := fresh.ReadFile(path)
	if err != nil || string(current) != "modified" {
		t.Fatalf("new invocation must read fresh contents: %q %v", current, err)
	}
}

func TestWorkspaceSnapshotDoesNotRetainOversizedFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", workspaceReadBudget+1)), 0600); err != nil {
		t.Fatal(err)
	}
	provider := workspaceFileProvider{root: root}
	data, err := provider.ReadFile(path)
	if err != nil || len(data) != workspaceReadBudget+1 {
		t.Fatalf("large read: %d %v", len(data), err)
	}
	if provider.cachedBytes != 0 {
		t.Fatalf("oversized contents retained: %d", provider.cachedBytes)
	}
	if err := os.WriteFile(path, []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = provider.ReadFile(path)
	if err != nil || string(data) != "updated" {
		t.Fatalf("uncached file should be read again: %q %v", data, err)
	}
}
