package comments

import (
	"strings"
	"testing"
)

func TestFormattedDirectiveIsARecognizedComment(t *testing.T) {
	fixtures := map[string]string{
		"source.go":      "// LINT.IfChange(contract)",
		"page.html":      "<!-- LINT.IfChange(contract) -->",
		"docs.md":        "<!-- LINT.IfChange(contract) -->",
		"style.css":      "/* LINT.IfChange(contract) */",
		"types.clj":      "; LINT.IfChange(contract)",
		"paper.tex":      "% LINT.IfChange(contract)",
		"query.sql":      "-- LINT.IfChange(contract)",
		"CMakeLists.txt": "# LINT.IfChange(contract)",
		"Dockerfile.dev": "# LINT.IfChange(contract)",
		"BUILD.bazel":    "# LINT.IfChange(contract)",
		"view.tmpl":      "{{/* LINT.IfChange(contract) */}}",
	}
	for path, want := range fixtures {
		t.Run(path, func(t *testing.T) {
			got := FormatComment(path, "LINT.IfChange(contract)")
			if got != want {
				t.Fatalf("comment %q, want %q", got, want)
			}
			blocks, err := ExtractWithLoader(path, func(string) ([]byte, error) { return []byte(got), nil })
			if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0].Text, "LINT.IfChange(contract)") {
				t.Fatalf("formatted comment unrecognized: %v %v", blocks, err)
			}
		})
	}
}

func TestFormattedCommentHonorsCustomLineStyle(t *testing.T) {
	RegisterLineComment(".fmtconfig", "!!")
	got := FormatComment("config.fmtconfig", "LINT.IfChange(contract)")
	if got != "!! LINT.IfChange(contract)" {
		t.Fatalf("custom style: %q", got)
	}
}
