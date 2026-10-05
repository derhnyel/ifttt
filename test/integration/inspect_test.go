package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// LINT.IfChange(directive_inspection)
func TestInspectUsesEditorBufferAndConfiguredSyntax(t *testing.T) {
	for _, prefix := range []string{"LINT", "CHECK"} {
		t.Run(prefix, func(t *testing.T) {
			r := newDefaultRepo(t)
			if prefix != "LINT" {
				r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: CHECK\n")
			}
			filename := filepath.Join(r.dir, "source.go")
			r.write(t, "source.go", "const disk = 1\n")
			buffer := "const example = `" + prefix + ".IfChange(EXAMPLE)`\n// See " + prefix + ".IfChange(PROSE)\n// ```\n// " + prefix + ".IfChange(FENCE)\n// ```\n"
			if prefix == "LINT" {
				buffer += "// LINT.IfChange(API)\nconst api = 1\n// LINT.ThenChange(//missing.go:API)\n"
			} else {
				buffer += "// CHECK.IfChange(\"API\")\nconst api = 1\n// CHECK.ThenChange(\"missing.go#API\")\n"
			}
			output := requireCode(t, r, buffer, 0, "inspect", "--stdin", filename)
			var report struct {
				Prefix, ParseError string
				Directives         []struct {
					Kind, Label string
					Line        int
				}
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatal(err)
			}
			if report.Prefix != prefix || report.ParseError != "" || len(report.Directives) != 2 || report.Directives[0].Kind != "IfChange" || report.Directives[0].Label != "API" || report.Directives[0].Line != 6 {
				t.Fatalf("incorrect inspected buffer: %s", output)
			}
			body, err := os.ReadFile(filename)
			if err != nil || string(body) != "const disk = 1\n" {
				t.Fatal("inspection must not write editor contents")
			}
			// Inspection must neither follow missing targets nor require a repository.
			if err := os.RemoveAll(filepath.Join(r.dir, ".git")); err != nil {
				t.Fatal(err)
			}
			requireCode(t, r, buffer, 0, "inspect", "--stdin", filename)
		})
	}
}
func TestInspectReturnsPartialDirectivesOnStructuralErrors(t *testing.T) {
	r := newDefaultRepo(t)
	output := requireCode(t, r, "// LINT.Label(\"A\")\nbody\n", 0, "inspect", "--stdin", "not-yet-saved.txt")
	var report struct {
		ParseError string
		Directives []struct{ Kind string }
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.ParseError == "" || len(report.Directives) != 1 || report.Directives[0].Kind != "Label" {
		t.Fatalf("partial inspection missing: %s", output)
	}
	requireCode(t, r, "", 1, "inspect")
	requireCode(t, r, "", 1, "inspect", "missing.txt")
}

func TestInspectCommentStyleMatchesLint(t *testing.T) {
	r := newDefaultRepo(t)
	output := requireCode(t, r, "## LINT.IfChange(A)\nbody\n## LINT.ThenChange()\n", 0, "inspect", "--stdin", "--comment-style", ".tmpl=##", "editor.tmpl")
	var report struct{ Directives []struct{ Kind string } }
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Directives) != 2 || report.Directives[0].Kind != "IfChange" {
		t.Fatalf("comment override not applied: %s", output)
	}
	for _, args := range [][]string{{"inspect", "--comment-style", "invalid", "source.go"}, {"inspect", "--stdin", "--fix", "source.go"}} {
		requireCode(t, r, "// LINT.IfChange(A)\n", 1, args...)
	}
}

// LINT.ThenChange(//cmd/ifttt/inspect.go:directive_inspection)
