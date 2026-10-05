package engine

import (
	"path/filepath"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

func TestChangedLinesKeepRepositoryPaths(t *testing.T) {
	for _, tc := range []struct {
		name, oldPath, newPath, file, oldFile string
		deleted, renamed                      bool
	}{
		{"modified", "a/a/nested/source.go", "b/a/nested/source.go", "a/nested/source.go", "a/nested/source.go", false, false},
		{"created", "/dev/null", "b/docs/new.md", "docs/new.md", "/dev/null", false, false},
		{"deleted", "a/docs/old.md", "/dev/null", "docs/old.md", "docs/old.md", true, false},
		{"renamed", "a/docs/old.md", "b/docs/new.md", "docs/new.md", "docs/old.md", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hunk := "@@ -1 +1 @@\n-old\n+new\n"
			if tc.oldPath == "/dev/null" {
				hunk = "@@ -0,0 +1 @@\n+new\n"
			} else if tc.deleted {
				hunk = "@@ -1 +0,0 @@\n-old\n"
			}
			patch := "--- " + tc.oldPath + "\n+++ " + tc.newPath + "\n" + hunk
			changes, err := ParseChangedLines(patch)
			if err != nil {
				t.Fatal(err)
			}
			change := changes[tc.file]
			if len(changes) != 1 || change == nil {
				t.Fatalf("repository path %q missing from changes: %+v", tc.file, changes)
			}
			if change.File != tc.file || change.OldFile != tc.oldFile || change.Deleted != tc.deleted || change.Renamed != tc.renamed {
				t.Fatalf("incorrect change identity: %+v", change)
			}
		})
	}
}

func TestStructuralAndSelectedSourcesKeepDiagnosticPaths(t *testing.T) {
	files := map[string]string{"nested/source.go": "// SENTRY.IfChange(\"X\")\nbody\n// SENTRY.ThenChange(\"missing.go\")\n"}
	result, code, _ := runLintWithSetup(t, files, nil, Options{
		StructuralFiles: []string{filepath.FromSlash("nested/source.go")},
		SourceFiles:     []string{filepath.FromSlash("nested/source.go")},
	})
	if code != 1 || len(result.Findings) != 1 || result.Findings[0].File != "nested/source.go" || result.Findings[0].TargetPath != "nested/missing.go" {
		t.Fatalf("incorrect repository diagnostic paths: code=%d findings=%+v", code, result.Findings)
	}
}

func TestDeletedSnapshotTargetUsesCanonicalReverseCandidate(t *testing.T) {
	provider := &memoryFileProvider{files: map[string][]byte{
		"nested/source.go": []byte("// SENTRY.IfChange(\"X\")\nbody\n// SENTRY.ThenChange(\"target.go\")\n"),
	}}
	result, code := Lint("", Options{
		Files: provider,
		RevisionChanges: map[string]*core.FileChanges{
			"nested/target.go": {File: "nested/target.go", Deleted: true, ContentChanged: true},
		},
		ReverseCandidates: []string{filepath.FromSlash("nested/source.go")},
	})
	if code != 1 || len(result.Findings) != 1 || result.Findings[0].File != "nested/source.go" || result.Findings[0].TargetPath != "nested/target.go" {
		t.Fatalf("stale snapshot reference missed: code=%d findings=%+v", code, result.Findings)
	}
}
