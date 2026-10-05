package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

// LINT.IfChange(match_parser_tests)
func TestMatchArgumentsAndCommentContexts(t *testing.T) {
	prefix := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	defer core.SetDirectivePrefix(prefix)
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"escaped quote", `// LINT.Match(":A", "//owner\"s.txt:B", "(\\d+)")`, true},
		{"raw regex", `// LINT.Match(':A', '//target.txt:B', 'version="([^"]+)"')`, true},
		{"block", `/* LINT.Match(":A", "//target.txt:B") */`, true},
		{"html", `<!-- LINT.Match(":A", "//target.txt:B") -->`, true},
		{"missing close", `// LINT.Match(":A", "//target.txt:B"`, false},
		{"trailing comma", `// LINT.Match(":A", "//target.txt:B",)`, false},
		{"missing quote", `// LINT.Match(":A", "//target.txt:B)`, false},
		{"invalid escape", `// LINT.Match(":A", "//target.txt:B", "\d+")`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.go")
			if tc.name == "html" {
				path = filepath.Join(t.TempDir(), "source.md")
			}
			if err := os.WriteFile(path, []byte(tc.body+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			dirs, err := (Provider{}).Parse(path)
			if err != nil || len(dirs) != 1 || dirs[0].Kind != core.Match || (dirs[0].Error == "") != tc.valid {
				t.Fatalf("unexpected parse: %+v %v", dirs, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "example.go")
	content := "const example = `LINT.Match(\":A\", \"//target:B\")`\n// See LINT.Match(\":A\", \"//target:B\")\n// ```\n// LINT.Match(\":A\", \"//target:B\")\n// ```\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	dirs, err := (Provider{}).Parse(path)
	if err != nil || len(dirs) != 0 {
		t.Fatalf("examples are not contracts: %+v %v", dirs, err)
	}
	d := parseMatch(`Match(":A", "//target.txt:B", "(\\d+)")`, 1)
	if !strings.Contains(d.Pattern, `\d`) {
		t.Fatalf("regex backslash was lost: %+v", d)
	}
}

// LINT.ThenChange(//internal/parse/match.go:match_contract, //internal/parse/parser.go:match_dispatch)
