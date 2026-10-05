package engine

import (
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

func TestUnmatchedURLsNeverUseLocalProvider(t *testing.T) {
	base := &memoryFileProvider{files: map[string][]byte{}}
	github, err := NewGitHubFactory([]GitHubRemote{{Repo: "owner/repo", DefaultRef: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, factories := range [][]FileProviderFactory{nil, {nil, github}} {
		for _, target := range []string{"https://example.invalid/file.go", "github://other/repo/file.go", "sftp://example.invalid/file.go"} {
			provider, _, err := fileProviderForPath(base, factories, target)
			if err == nil || provider != nil {
				t.Fatalf("unmatched URL %q reached local provider: %T, %v", target, provider, err)
			}
		}
	}
	provider, actual, err := fileProviderForPath(base, []FileProviderFactory{github}, "github://owner/repo/file.go")
	if err != nil || provider == nil || provider == base || actual != "github://owner/repo/file.go" {
		t.Fatalf("configured remote rejected: %T, %s, %v", provider, actual, err)
	}
	provider, actual, err = fileProviderForPath(base, nil, "local.go")
	if err != nil || provider != base || actual != "local.go" {
		t.Fatalf("local provider changed: %T, %s, %v", provider, actual, err)
	}
}

func TestMissingLabelledTargetHasOneSourceDiagnostic(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	t.Cleanup(func() { core.SetDirectivePrefix(old) })
	for _, raw := range []string{"//missing.go:API", "https://example.invalid/target.go#API"} {
		files := map[string]string{"source.go": "// LINT.IfChange(SRC)\nbody\n// LINT.ThenChange(" + raw + ")\n"}
		result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"source.go"}})
		if code != 1 || len(result.Findings) != 1 || result.Findings[0].File != "source.go" || result.Findings[0].TargetLabel != "API" {
			t.Fatalf("missing target lost or duplicated: %+v", result.Findings)
		}
	}
}

func TestAmbiguousTargetLabelHasExplicitDiagnostic(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	t.Cleanup(func() { core.SetDirectivePrefix(old) })
	files := map[string]string{
		"source.go": "// LINT.IfChange(SRC)\nbody\n// LINT.ThenChange(//target.go:API)\n",
		"target.go": "// LINT.IfChange(API)\none\n// LINT.ThenChange()\n// LINT.IfChange(API)\ntwo\n// LINT.ThenChange()\n",
	}
	result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"source.go"}})
	if code != 1 || !hasRule(result, "source.go", "label_ambiguous") || hasRule(result, "source.go", "label_missing") || !hasRule(result, "target.go", "duplicate_label") {
		t.Fatalf("ambiguity misreported: %+v", result.Findings)
	}
	for _, f := range result.Findings {
		if f.RuleID == "label_ambiguous" && (f.TargetPath != "target.go" || f.TargetLabel != "API") {
			t.Fatalf("ambiguity lost target metadata: %+v", f)
		}
	}
}

func TestConditionalTargetReadErrorsRemainWithoutThenChange(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("SENTRY")
	t.Cleanup(func() { core.SetDirectivePrefix(old) })
	for _, rule := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
		argument := "[\"missing.go#API\"]"
		if rule == "ForbidChange" {
			argument = "\"missing.go#API\""
		}
		files := map[string]string{"source.go": "// SENTRY." + rule + "(" + argument + ")\n"}
		result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"source.go"}})
		if code != 1 || !hasRule(result, "missing.go", "error") {
			t.Fatalf("%s lost its target read error: %+v", rule, result.Findings)
		}
	}
}
