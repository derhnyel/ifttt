package diff

import (
	"io"
	"strings"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

func TestParseBasic(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/file.txt b/file.txt",
		"index 000..111 100644",
		"--- a/file.txt",
		"+++ b/file.txt",
		"@@ -1,2 +1,2 @@",
		"-old",
		"+new",
	}, "\n") + "\n"
	p := New(strings.NewReader(diff))
	fp, err := p.NextFile()
	if err != nil || fp == nil {
		t.Fatalf("parse err: %v", err)
	}
	if core.NormalizePath(fp.NewPath) != "file.txt" {
		t.Fatalf("bad path: %q", fp.NewPath)
	}
}

func TestMalformedNonemptyInput(t *testing.T) {
	p := New(strings.NewReader("not a diff\n"))
	if _, err := p.NextFile(); err == nil || err == io.EOF {
		t.Fatalf("invalid input accepted: %v", err)
	}
}

func TestDeletedPatchPreserved(t *testing.T) {
	p := New(strings.NewReader("diff --git a/target.go b/target.go\n--- a/target.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n"))
	patch, err := p.NextFile()
	if err != nil || patch == nil || patch.OldPath != "target.go" || patch.NewPath != "/dev/null" {
		t.Fatalf("deletion discarded: %+v %v", patch, err)
	}
}

// Regressions for upstream issues #41, #37, and #36.
func TestUpstreamDiffRegressions(t *testing.T) {
	cases := []struct {
		name, patch string
		hunks       int
	}{
		{"SQL deletion resembles file header", "diff --git a/a.sql b/a.sql\n--- a/a.sql\n+++ b/a.sql\n@@ -1,2 +1 @@\n--- comment\n SELECT 1;\n", 1},
		{"permission only", "diff --git a/script.sh b/script.sh\nold mode 100644\nnew mode 100755\n", 0},
		{"no newline marker inside hunk", "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := New(strings.NewReader(c.patch))
			f, err := p.NextFile()
			if err != nil || f == nil || len(f.Chunks) != c.hunks {
				t.Fatalf("patch=%+v err=%v", f, err)
			}
			if _, err = p.NextFile(); err != io.EOF {
				t.Fatalf("end=%v", err)
			}
		})
	}
}

func TestDeletedSQLFileAfterAnotherPatch(t *testing.T) {
	p := New(strings.NewReader("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/query.sql b/query.sql\ndeleted file mode 100644\n--- a/query.sql\n+++ /dev/null\n@@ -1,2 +0,0 @@\n--- SQL comment\n-SELECT 1;\n"))
	if _, err := p.NextFile(); err != nil {
		t.Fatal(err)
	}
	f, err := p.NextFile()
	if err != nil || f == nil || f.NewPath != "/dev/null" || len(f.Chunks) != 1 || len(f.Chunks[0].Lines) != 2 {
		t.Fatalf("%+v %v", f, err)
	}
}
func TestTrailingNewlineOnlyPatch(t *testing.T) {
	p := New(strings.NewReader("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-same\n\\ No newline at end of file\n+same\n"))
	f, err := p.NextFile()
	if err != nil || f == nil || len(f.Chunks) != 1 || len(f.Chunks[0].Lines) != 2 {
		t.Fatalf("%+v %v", f, err)
	}
}

// LINT.IfChange(combined_headers)
func TestCombinedHeaderTextInsideHunksRemainsUnified(t *testing.T) {
	for _, marker := range []string{"diff --cc ", "diff --combined "} {
		t.Run(marker, func(t *testing.T) {
			patch := "diff --git a/example.txt b/example.txt\n--- a/example.txt\n+++ b/example.txt\n@@ -1,2 +1,2 @@\n " + marker + "context\n-old " + marker + "text\n+new " + marker + "text\n"
			p := New(strings.NewReader(patch))
			f, err := p.NextFile()
			if err != nil || f == nil || len(f.Chunks) != 1 {
				t.Fatalf("literal header text suppressed a unified patch: %+v %v", f, err)
			}
			if _, err = p.NextFile(); err != io.EOF {
				t.Fatalf("end = %v, want EOF", err)
			}
		})
	}
}

func TestCombinedHeaderDetectionAcrossReadBoundaries(t *testing.T) {
	for _, prefix := range []string{"", "ordinary line\n", "ordinary line without newline "} {
		for _, marker := range []string{"diff --cc ", "diff --combined "} {
			for _, size := range []int{1, 2, 7, 19} {
				d := &combinedDetector{r: strings.NewReader(prefix + marker + "file\n")}
				buf := make([]byte, size)
				for {
					_, err := d.Read(buf)
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				want := prefix == "" || strings.HasSuffix(prefix, "\n")
				if d.found != want {
					t.Fatalf("prefix=%q marker=%q chunk=%d found=%v want=%v", prefix, marker, size, d.found, want)
				}
			}
		}
	}
}

// LINT.ThenChange()
