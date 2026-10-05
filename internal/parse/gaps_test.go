package parse

import (
	"reflect"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

func TestGoogleTargetSpacesAndContinuations(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	t.Cleanup(func() { core.SetDirectivePrefix(old) })
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"spaces", "// LINT.ThenChange(Google-internal path, //docs/file name.md:API)", []string{"Google-internal path", "//docs/file name.md:API"}},
		{"continuation", "// LINT.ThenChange( \\\n// first.go, \\\n// second file.go:API)", []string{"first.go", "second file.go:API"}},
		{"block", "/*\n * LINT.ThenChange(first.go,\\\n * second.go)\n */", []string{"first.go", "second.go"}},
		{"windows", `// LINT.ThenChange(//docs\file name.md:API)`, []string{`//docs\file name.md:API`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(tc.text), nil }}).Parse("source.go")
			if err != nil || len(dirs) != 1 || dirs[0].Kind != core.ThenChange || !reflect.DeepEqual(dirs[0].List, tc.want) {
				t.Fatalf("targets=%+v, error=%v; want %v", dirs, err, tc.want)
			}
		})
	}
}

func TestGoogleContinuationsDoNotHideMalformedListsOrDirectives(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	t.Cleanup(func() { core.SetDirectivePrefix(old) })
	for _, text := range []string{
		"// LINT.ThenChange(\"quoted.go\")",
		"// LINT.ThenChange(first.go,\\\n// :invalid label)",
		"// LINT.ThenChange(first.go,\\\n// LINT.IfChange(API)",
	} {
		dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(text), nil }}).Parse("source.go")
		if err != nil || len(dirs) == 0 || dirs[0].Kind != core.Unknown {
			t.Fatalf("malformed list became valid: %+v, %v", dirs, err)
		}
	}
}

func TestGoogleContinuationCannotConsumeCommentFence(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	t.Cleanup(func() { core.SetDirectivePrefix(old) })
	for _, fence := range []string{"```", "~~~"} {
		text := "// LINT.ThenChange(first.go,\\\n// " + fence + "\n// example.go)\n// LINT.IfChange(EXAMPLE)\n// LINT.ThenChange(fake.go)\n// " + fence + "\n// LINT.IfChange(REAL)\n// LINT.ThenChange(real.go)\n"
		dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(text), nil }}).Parse("source.go")
		if err != nil || len(dirs) != 3 || dirs[0].Kind != core.Unknown || dirs[1].Label != "REAL" || dirs[2].List[0] != "real.go" {
			t.Fatalf("fenced examples became active: %+v, %v", dirs, err)
		}
	}
}
