package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ifttt "github.com/derhnyel/ifttt/internal"
)

// These legacy fixtures exercise quoted SENTRY directives and array targets.
// Select their syntax explicitly; default-syntax behavior is covered separately.
func TestMain(m *testing.M) {
	ifttt.SetDirectivePrefix("SENTRY")
	os.Exit(m.Run())
}

func TestThenChangeMultiLineArray(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "file.go")
	src := `// SENTRY.ThenChange([
//   "a.go#L1",
//   'b.go',
// ])
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 directive, got %d (%+v)", len(dirs), dirs)
	}
	dir := dirs[0]
	if dir.Kind != ifttt.ThenChange {
		t.Fatalf("expected ThenChange, got %s", dir.Kind)
	}
	if len(dir.List) != 2 {
		t.Fatalf("expected 2 items, got %d (%v)", len(dir.List), dir.List)
	}
	if dir.List[0] != "a.go#L1" || dir.List[1] != "b.go" {
		t.Fatalf("unexpected list: %v", dir.List)
	}
}

func TestParseBareIfChangeCallReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	src := `// SENTRY.IfChange()
// plain comment`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Kind != "Unknown" {
		t.Fatalf("expected malformed directive, got %v", dirs)
	}
}

func TestParseUnmatchedEndLabelError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.go")
	src := `// SENTRY.EndLabel`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pr := Provider{}
	_, err := pr.Parse(path)
	if err == nil {
		t.Fatalf("expected error for unmatched EndLabel")
	}
	if !strings.Contains(err.Error(), "unmatched EndLabel") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseWithCustomPrefix(t *testing.T) {
	previous := ifttt.CurrentDirectiveSyntax().Prefix
	defer ifttt.SetDirectivePrefix(previous)
	ifttt.SetDirectivePrefix("CUSTOM")
	dir := t.TempDir()
	path := filepath.Join(dir, "prefixed.go")
	src := `// CUSTOM.IfChange("LBL")
// CUSTOM.ThenChange("target.go")`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse custom prefix: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directives, got %d", len(dirs))
	}
	if dirs[0].Kind != ifttt.IfChange || dirs[1].Kind != ifttt.ThenChange {
		t.Fatalf("unexpected directives: %+v", dirs)
	}
}

func TestParseAllDirectiveKinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "all.go")
	content := strings.Join([]string{
		"// SENTRY.IfChange(\"A\")",
		"// SENTRY.ThenChange([\"target.go#LBL\", \"other.go\"])",
		"// SENTRY.Label(\"LBL\")",
		"// SENTRY.RequireAny([\"rule1\", \"rule2\"])",
		"// SENTRY.RequireAll(['a', 'b'])",
		"// SENTRY.ForbidChange(\"foo.go\")",
		"// SENTRY.Disable(\"rule\")",
		"// SENTRY.Enable(\"rule\")",
		"// SENTRY.Ignore(\"then_missing\")",
		"// SENTRY.EndLabel",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(dirs) != 10 {
		t.Fatalf("expected 10 directives, got %d (%+v)", len(dirs), dirs)
	}
	wantKinds := []string{
		ifttt.IfChange,
		ifttt.ThenChange,
		ifttt.Label,
		ifttt.RequireAny,
		ifttt.RequireAll,
		ifttt.Forbid,
		ifttt.Disable,
		ifttt.Enable,
		ifttt.Ignore,
		ifttt.EndLabel,
	}
	for i, kind := range wantKinds {
		if dirs[i].Kind != kind {
			t.Fatalf("directive %d: expected %s, got %s", i, kind, dirs[i].Kind)
		}
	}
	if dirs[0].Label != "A" {
		t.Fatalf("expected IfChange label 'A', got %q", dirs[0].Label)
	}
	if dirs[1].Target != "" || len(dirs[1].List) != 2 {
		t.Fatalf("expected ThenChange list, got %+v", dirs[1])
	}
	if dirs[1].List[0] != "target.go#LBL" || dirs[1].List[1] != "other.go" {
		t.Fatalf("unexpected then list: %+v", dirs[1].List)
	}
	if dirs[2].Name != "LBL" {
		t.Fatalf("expected label name LBL, got %q", dirs[2].Name)
	}
	if len(dirs[3].List) != 2 || dirs[3].List[0] != "rule1" {
		t.Fatalf("unexpected RequireAny list: %+v", dirs[3].List)
	}
	if dirs[5].Target != "foo.go" {
		t.Fatalf("expected ForbidChange target foo.go, got %q", dirs[5].Target)
	}
	if dirs[6].Label != "rule" || dirs[7].Label != "rule" {
		t.Fatalf("expected disable/enable labels, got %+v %+v", dirs[6], dirs[7])
	}
}

func TestParseBlockCommentDirectives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "block.c")
	content := strings.Join([]string{
		"/*",
		"SENTRY.IfChange(\"B\")",
		"SENTRY.ThenChange(\"target.c\")",
		"*/",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse block: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directives, got %d (%+v)", len(dirs), dirs)
	}
	if dirs[0].Kind != ifttt.IfChange || dirs[1].Kind != ifttt.ThenChange {
		t.Fatalf("unexpected kinds: %+v", dirs)
	}
}

func TestParseThenChangeArrayMultilineComplex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "complex.go")
	content := strings.Join([]string{
		"// SENTRY.ThenChange([",
		"//   \"foo.go#FOO\",",
		"//   'bar.go#BAR',",
		"//   \"baz.go\"",
		"// ])",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected single directive, got %d", len(dirs))
	}
	d := dirs[0]
	if d.Kind != ifttt.ThenChange {
		t.Fatalf("expected ThenChange, got %s", d.Kind)
	}
	if len(d.List) != 3 || d.List[0] != "foo.go#FOO" || d.List[1] != "bar.go#BAR" || d.List[2] != "baz.go" {
		t.Fatalf("unexpected list contents: %+v", d.List)
	}
}

func TestParseRequireAnyMultiline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.py")
	content := strings.Join([]string{
		"\"\"\"",
		"SENTRY.RequireAny([",
		"    \"rule-a\",",
		"    \"rule-b\"",
		"])",
		"\"\"\"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Kind != ifttt.RequireAny {
		t.Fatalf("expected RequireAny directive, got %+v", dirs)
	}
	if len(dirs[0].List) != 2 || dirs[0].List[1] != "rule-b" {
		t.Fatalf("unexpected list contents: %+v", dirs[0].List)
	}
}

func TestParseNestedLabelStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested.go")
	content := strings.Join([]string{
		"// SENTRY.Label(\"OUTER\")",
		"// SENTRY.IfChange(\"A\")",
		"// SENTRY.ThenChange(\"outer.go\")",
		"// SENTRY.Label(\"INNER\")",
		"// SENTRY.IfChange(\"B\")",
		"// SENTRY.ThenChange(\"inner.go\")",
		"// SENTRY.EndLabel",
		"// SENTRY.EndLabel",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pr := Provider{}
	dirs, err := pr.Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(dirs) != 8 {
		t.Fatalf("expected 8 directives, got %d", len(dirs))
	}
	var outer, inner bool
	for _, d := range dirs {
		if d.Kind == ifttt.Label && d.Name == "OUTER" {
			outer = true
		}
		if d.Kind == ifttt.Label && d.Name == "INNER" {
			inner = true
		}
	}
	if !outer || !inner {
		t.Fatalf("missing nested labels in %+v", dirs)
	}
}

func TestParseAcrossLanguages(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		file    string
		content string
		kind    string
	}{
		{
			name: "GoLine",
			file: "main.go",
			content: `// SENTRY.IfChange("GO")
// SENTRY.ThenChange("target.go")`,
			kind: ifttt.ThenChange,
		},
		{
			name: "GoBlock",
			file: "main.java",
			content: strings.Join([]string{
				"/* SENTRY.Label(\"LBL\") */",
				"/* SENTRY.EndLabel */",
			}, "\n"),
			kind: ifttt.Label,
		},
		{
			name:    "PythonHash",
			file:    "module.py",
			content: `# SENTRY.IfChange("PY")`,
			kind:    ifttt.IfChange,
		},
		{
			name: "PythonTriple",
			file: "doc.py",
			content: `"""
SENTRY.ThenChange("py_target.go")
"""`,
			kind: ifttt.ThenChange,
		},
		{
			name:    "Shell",
			file:    "script.sh",
			content: `# SENTRY.ForbidChange("shell.txt")`,
			kind:    ifttt.Forbid,
		},
		{
			name:    "SQL",
			file:    "query.sql",
			content: `-- SENTRY.RequireAny(["r1"])`,
			kind:    ifttt.RequireAny,
		},
		{
			name:    "HTML",
			file:    "page.html",
			content: `<!-- SENTRY.Disable("rule") -->`,
			kind:    ifttt.Disable,
		},
		{
			name:    "RustDoc",
			file:    "lib.rs",
			content: `/// SENTRY.Enable("rule")`,
			kind:    ifttt.Enable,
		},
		{
			name:    "Lua",
			file:    "file.lua",
			content: `-- SENTRY.ThenChange("lua_target.lua")`,
			kind:    ifttt.ThenChange,
		},
	}

	pr := Provider{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write %s: %v", tc.file, err)
			}
			dirs, err := pr.Parse(path)
			if err != nil {
				t.Fatalf("parse %s: %v", tc.file, err)
			}
			if len(dirs) == 0 {
				t.Fatalf("expected directives for %s", tc.file)
			}
			found := false
			for _, d := range dirs {
				if d.Kind == tc.kind {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected kind %s in %s, got %+v", tc.kind, tc.file, dirs)
			}
		})
	}
}

func TestGoogleStyleLintSyntax(t *testing.T) {
	old := ifttt.CurrentDirectiveSyntax().Prefix
	defer ifttt.SetDirectivePrefix(old)
	ifttt.SetDirectivePrefix("LINT")
	p := filepath.Join(t.TempDir(), "source.go")
	source := "// LINT.IfChange(API)\nconst x = 1\n// LINT.ThenChange(\n// //docs/api.md:API,\n// :OTHER,\n// )\n"
	if err := os.WriteFile(p, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	dirs, err := (Provider{}).Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || dirs[0].Label != "API" || len(dirs[1].List) != 2 || dirs[1].List[0] != "//docs/api.md:API" {
		t.Fatalf("Google syntax not parsed: %+v", dirs)
	}
}

func TestDirectiveInsideStringIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "source.go")
	source := "package main\nvar example = `// SENTRY.IfChange(\"FAKE\")`\n"
	if err := os.WriteFile(p, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	dirs, err := (Provider{}).Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Fatalf("parsed directive inside string: %+v", dirs)
	}
}

func TestGoogleMultilineHashComments(t *testing.T) {
	old := ifttt.CurrentDirectiveSyntax().Prefix
	defer ifttt.SetDirectivePrefix(old)
	ifttt.SetDirectivePrefix("LINT")
	p := filepath.Join(t.TempDir(), "source.py")
	source := "# LINT.IfChange(API)\nx = 1\n# LINT.ThenChange(\n# //docs/api.md:API,\n# :OTHER,\n# )\n"
	if err := os.WriteFile(p, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	dirs, err := (Provider{}).Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || len(dirs[1].List) != 2 {
		t.Fatalf("multiline hash syntax failed: %+v", dirs)
	}
}
