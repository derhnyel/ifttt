package parse

import (
	ifttt "github.com/derhnyel/ifttt/internal"
	"strings"
	"testing"
)

func TestStrictDirectiveParsing(t *testing.T) {
	for _, tc := range []struct {
		prefix, src, kind string
		count             int
	}{
		{"SENTRY", "// prose SENTRY.IfChange(\"x\")", "", 0},
		{"SENTRY", "// SENTRY.Mistake()", "Unknown", 1},
		{"SENTRY", "// SENTRY.ThenChange([unquoted])", "Unknown", 1},
		{"SENTRY", "// SENTRY.ThenChange([\"x\",,\"y\"])", "Unknown", 1},
		{"SENTRY", "// SENTRY.ThenChange([\"a,b\"])", ifttt.ThenChange, 1},
		{"SENTRY", "// SENTRY.IfChange(\"x')", "Unknown", 1},
		{"SENTRY", "// SENTRY.ThenChange(\"x\") junk", "Unknown", 1},
		{"SENTRY", "// SENTRY.ThenChange( [\n// \"x\"\n// ])", ifttt.ThenChange, 1},
		{"LINT", "# LINT.IfChange(123bad)", "Unknown", 1},
		{"LINT", "# LINT.IfChange()", ifttt.IfChange, 1},
		{"LINT", "# LINT.ThenChange()", ifttt.ThenChange, 1},
		{"LINT", "# LINT.ThenChange(:123bad)", "Unknown", 1},
		{"LINT", "# LINT.ThenChange(a:123bad)", "Unknown", 1},
		{"LINT", "# LINT.ThenChange(C:/path/file)", ifttt.ThenChange, 1},
		{"SENTRY", "// ```\n// SENTRY.IfChange(\"example\")\n// ```", "", 0},
	} {
		t.Run(tc.src, func(t *testing.T) {
			previous := ifttt.CurrentDirectiveSyntax().Prefix
			ifttt.SetDirectivePrefix(tc.prefix)
			defer ifttt.SetDirectivePrefix(previous)
			ds, e := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(tc.src), nil }}).Parse("a.py")
			if tc.src[:2] == "//" {
				ds, e = (Provider{ReadFile: func(string) ([]byte, error) { return []byte(tc.src), nil }}).Parse("a.go")
			}
			if e != nil || len(ds) != tc.count || (len(ds) > 0 && ds[0].Kind != tc.kind) {
				t.Fatalf("got %+v, %v", ds, e)
			}
		})
	}
}
func TestMultilineArrayIgnoresBracketsInsideTargets(t *testing.T) {
	ds, e := (Provider{ReadFile: func(string) ([]byte, error) {
		return []byte("// SENTRY.ThenChange([\n// \"target[1].go\",\n// \"second.go\"\n// ])"), nil
	}}).Parse("a.go")
	if e != nil || len(ds) != 1 || ds[0].Kind != ifttt.ThenChange || len(ds[0].List) != 2 {
		t.Fatalf("got %+v, %v", ds, e)
	}
}

func TestGoogleCompletedDirectivesIgnoreProse(t *testing.T) {
	previous := ifttt.CurrentDirectiveSyntax().Prefix
	ifttt.SetDirectivePrefix("LINT")
	t.Cleanup(func() { ifttt.SetDirectivePrefix(previous) })
	for _, text := range []string{
		"LINT.IfChange(label) see docs",
		"LINT.IfChange marks the start",
		"LINT.ThenChange() marks the end",
		"LINT.ThenChange(//path(1).go) marks the end",
		"LINT.ThenChange(\n// //path(1).go,\n// //second.go\n// ) marks the end",
	} {
		t.Run(text, func(t *testing.T) {
			dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte("// " + text), nil }}).Parse("a.go")
			if err != nil || len(dirs) != 0 {
				t.Fatalf("prose produced directives: %+v, %v", dirs, err)
			}
		})
	}
	for _, text := range []string{"LINT.ThenChange(", "LINT.IfChange(123bad)", "LINT.Unknown()"} {
		dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte("// " + text), nil }}).Parse("a.go")
		if err != nil || len(dirs) != 1 || dirs[0].Kind != ifttt.Unknown {
			t.Fatalf("malformed directive %q was hidden: %+v, %v", text, dirs, err)
		}
	}
}

func TestGoogleThenChangeParenthesesInTargets(t *testing.T) {
	previous := ifttt.CurrentDirectiveSyntax().Prefix
	ifttt.SetDirectivePrefix("LINT")
	t.Cleanup(func() { ifttt.SetDirectivePrefix(previous) })
	for _, text := range []string{
		"LINT.ThenChange(//path(1).go, //second(2).go:label)",
		"LINT.ThenChange(\n// //path(1).go,\n// //second(2).go:label,\n// )",
	} {
		dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte("// " + text), nil }}).Parse("a.go")
		if err != nil || len(dirs) != 1 || dirs[0].Kind != ifttt.ThenChange || len(dirs[0].List) != 2 || dirs[0].List[0] != "//path(1).go" || dirs[0].List[1] != "//second(2).go:label" {
			t.Fatalf("parenthesized targets: %+v, %v", dirs, err)
		}
	}
}
func TestCommentFencesRequireMatchingDelimiter(t *testing.T) {
	ds, e := (Provider{ReadFile: func(string) ([]byte, error) {
		return []byte("// ````\n// ~~~\n// SENTRY.IfChange(\"fake\")\n// ```\n// SENTRY.IfChange(\"fake2\")\n// ````\n// SENTRY.IfChange(\"real\")"), nil
	}}).Parse("a.go")
	if e != nil || len(ds) != 1 || ds[0].Label != "real" {
		t.Fatalf("got %+v, %v", ds, e)
	}
}
func FuzzParserDirectiveLines(f *testing.F) {
	f.Add("// SENTRY.IfChange(\"x\")\n// SENTRY.ThenChange([\"target.go\"])")
	f.Fuzz(func(t *testing.T, src string) {
		ds, e := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(src), nil }}).Parse("fuzz.go")
		if e != nil {
			return
		}
		for _, d := range ds {
			if d.Line < 1 || d.Line > 1+strings.Count(src, "\n") {
				t.Fatalf("invalid line %d", d.Line)
			}
			if d.Kind == ifttt.Unknown && (d.Name == "" || d.Label == "") {
				t.Fatalf("missing diagnostic: %+v", d)
			}
		}
	})
}
