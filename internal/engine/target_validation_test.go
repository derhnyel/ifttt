package engine

import (
	"fmt"
	core "github.com/derhnyel/ifttt/internal"
	"path/filepath"
	"strings"
	"testing"
)

func metadataTargetDiff(target string) []string {
	return []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,3 +1,3 @@", " // SENTRY.IfChange(\"SRC\")", " body", "-// SENTRY.ThenChange(\"old.go\")", "+// SENTRY.ThenChange(\"" + target + "\")"}
}
func hasRule(res Result, file, rule string) bool {
	for _, f := range res.Findings {
		if f.File == file && f.RuleID == rule {
			return true
		}
	}
	return false
}
func TestUnchangedTargetParseErrorSurfaced(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"SRC\")\nbody\n// SENTRY.ThenChange(\"target.go#LBL\")\n", "target.go": "// SENTRY.Label(\"LBL\")\nbody\n"}
	res, code, _ := runLintWithSetup(t, files, metadataTargetDiff("target.go#LBL"), Options{})
	if code != 1 || !hasRule(res, "target.go", "error") {
		t.Fatalf("target parse error was lost: %+v", res.Findings)
	}
}
func TestUnchangedTargetUnknownPolicy(t *testing.T) {
	for _, policy := range []string{"error", "warn", "ignore"} {
		t.Run(policy, func(t *testing.T) {
			files := map[string]string{"source.go": "// SENTRY.IfChange(\"SRC\")\nbody\n// SENTRY.ThenChange(\"target.go#LBL\")\n", "target.go": "// SENTRY.Label(\"LBL\")\nbody\n// SENTRY.EndLabel\n// SENTRY.Mistake()\n"}
			res, code, _ := runLintWithSetup(t, files, metadataTargetDiff("target.go#LBL"), Options{UnknownPolicy: policy})
			wantCode := 0
			if policy == "error" {
				wantCode = 1
			}
			if code != wantCode || hasRule(res, "target.go", "unknown_directive") != (policy != "ignore") {
				t.Fatalf("policy=%s got code=%d %+v", policy, code, res.Findings)
			}
			if policy == "warn" && len(res.Findings) > 0 && res.Findings[0].Severity != "warning" {
				t.Fatalf("wrong severity: %+v", res.Findings)
			}
		})
	}
}
func TestUnchangedTargetStructureSurfaced(t *testing.T) {
	for _, tc := range []struct{ src, rule string }{
		{"// SENTRY.IfChange(\"LBL\")\nbody\n", "orphan_if"},
		{"// SENTRY.ThenChange(\"unused.go\")\n", "orphan_then"},
		{"// SENTRY.Label(\"LBL\")\nbody\n// SENTRY.EndLabel\n// SENTRY.Label(\"LBL\")\nbody\n// SENTRY.EndLabel\n", "duplicate_label"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			files := map[string]string{"source.go": "// SENTRY.IfChange(\"SRC\")\nbody\n// SENTRY.ThenChange(\"target.go#LBL\")\n", "target.go": tc.src}
			res, code, _ := runLintWithSetup(t, files, metadataTargetDiff("target.go#LBL"), Options{})
			if code != 1 || !hasRule(res, "target.go", tc.rule) {
				t.Fatalf("target structure lost: %+v", res.Findings)
			}
		})
	}
}
func TestThenFindingsCarryExactTargetMetadata(t *testing.T) {
	for _, tc := range []struct {
		target string
		labels map[string]core.LineRange
		files  map[string][]byte
		want   string
	}{
		{"missing'file.go", nil, map[string][]byte{}, "then_missing"},
		{"target'file.go", nil, map[string][]byte{"target'file.go": []byte("body")}, "label_missing"},
		{"target'file.go", map[string]core.LineRange{"LBL": {StartLine: 2, EndLine: 3}}, map[string][]byte{"target'file.go": []byte("body")}, "then_label_missing"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			target := core.TargetRef{Path: tc.target, Label: "LBL"}
			var out []core.Finding
			evalThenChange(pairInfo{src: "source.go", ifLine: 1, thenLine: 3, target: target}, &core.FileChanges{AddedLines: map[int]bool{2: true}}, nil, tc.labels, &memoryFileProvider{files: tc.files}, nil, false, &out)
			if len(out) != 1 || out[0].RuleID != tc.want {
				t.Fatalf("got %+v", out)
			}
			if out[0].TargetPath != tc.target || out[0].TargetLabel != "LBL" {
				t.Fatalf("lost target metadata: %+v", out[0])
			}
		})
	}
}
func TestChangedTargetParseErrorNotDuplicated(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"SRC\")\nbody\n// SENTRY.ThenChange(\"target.go#LBL\")\n", "target.go": "// SENTRY.Label(\"LBL\")\nchanged\n"}
	diff := append(metadataTargetDiff("target.go#LBL"), "diff --git a/target.go b/target.go", "--- a/target.go", "+++ b/target.go", "@@ -1,2 +1,2 @@", " // SENTRY.Label(\"LBL\")", "-old", "+changed")
	res, _, _ := runLintWithSetup(t, files, diff, Options{})
	count := 0
	for _, f := range res.Findings {
		if f.File == "target.go" && f.RuleID == "error" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("got %d duplicate parse errors: %+v", count, res.Findings)
	}
}

func TestGoogleEmptyThenChangeRequiresNamedBlock(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	for _, tc := range []struct {
		prefix, label string
		want          bool
	}{{"LINT", "", true}, {"LINT", "API", false}, {"SENTRY", "", false}} {
		t.Run(tc.prefix+tc.label, func(t *testing.T) {
			core.SetDirectivePrefix(tc.prefix)
			begin := "// " + tc.prefix + ".IfChange"
			if tc.label != "" {
				begin += "(" + tc.label + ")"
			}
			files := map[string]string{"source.go": begin + "\nbody\n// " + tc.prefix + ".ThenChange()\n"}
			result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"source.go"}})
			if hasRule(result, "source.go", "empty_then") != tc.want || (code == 1) != (tc.want || tc.prefix == "SENTRY") {
				t.Fatalf("prefix=%s label=%q code=%d findings=%+v", tc.prefix, tc.label, code, result.Findings)
			}
		})
	}
}

func TestReferencedGoogleEmptyUnlabelledBlockIsInvalid(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	dirs := []core.LintDirective{{Kind: core.IfChange, Line: 1}, {Kind: core.ThenChange, Line: 3}}
	findings, valid := validateTargetDirectives("target.go", dirs, "ignore")
	if valid || len(findings) != 1 || findings[0].RuleID != "empty_then" || findings[0].Line != 3 {
		t.Fatalf("empty target block accepted: valid=%v findings=%+v", valid, findings)
	}
	dirs[0].Label = "API"
	findings, valid = validateTargetDirectives("target.go", dirs, "error")
	if !valid || len(findings) != 0 {
		t.Fatalf("named Google target block rejected: %+v", findings)
	}
}

func TestGoogleSingleSlashTargetIsProjectRootRelative(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "api.md")
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	for _, tc := range []struct{ prefix, raw, want string }{{"LINT", "/docs/api.md:API", "docs/api.md"}, {"LINT", "//docs/api.md:API", "docs/api.md"}, {"LINT", "api.md:API", "nested/api.md"}, {"SENTRY", absolute + "#API", absolute}} {
		t.Run(tc.prefix+tc.raw, func(t *testing.T) {
			core.SetDirectivePrefix(tc.prefix)
			actual := resolveTarget("nested/source.go", tc.raw)
			if actual.Path != filepath.FromSlash(tc.want) || actual.Label != "API" {
				t.Fatalf("resolved %q as %+v, want %q#API", tc.raw, actual, tc.want)
			}
		})
	}
}

func TestGoogleSingleSlashTargetRespectsStrictMode(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	files := map[string]string{"nested/source.go": "// LINT.IfChange(API)\nbody\n// LINT.ThenChange(/docs/api.md:API)\n", "docs/api.md": "<!-- LINT.IfChange(API) -->\nAPI\n<!-- LINT.ThenChange() -->\n"}
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprint(strict), func(t *testing.T) {
			result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"nested/source.go"}, StrictPaths: strict})
			if (code == 1) != strict || hasRule(result, "nested/source.go", "invalid_target_path") != strict {
				t.Fatalf("strict=%v code=%d findings=%+v", strict, code, result.Findings)
			}
		})
	}
}

func TestGooglePermissiveDirectoryPathsAndExplicitRelativePaths(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	for _, tc := range []struct{ prefix, raw, want string }{{"LINT", "docs/api.md:API", "docs/api.md"}, {"LINT", `docs\api.md:API`, "docs/api.md"}, {"LINT", "./docs/api.md:API", "nested/docs/api.md"}, {"LINT", "../docs/api.md:API", "docs/api.md"}, {"LINT", "api.md:API", "nested/api.md"}, {"SENTRY", "docs/api.md#API", "nested/docs/api.md"}} {
		t.Run(tc.prefix+tc.raw, func(t *testing.T) {
			core.SetDirectivePrefix(tc.prefix)
			actual := resolveTarget("nested/source.go", tc.raw)
			if actual.Path != filepath.FromSlash(tc.want) || actual.Label != "API" {
				t.Fatalf("resolved %q as %+v, want %q#API", tc.raw, actual, tc.want)
			}
		})
	}
}

func TestGoogleDirectoryTargetResolutionSharedByStructureAndCochange(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	for _, raw := range []string{"docs/api.md:API", "/docs/api.md:API"} {
		t.Run(raw, func(t *testing.T) {
			source := "// LINT.IfChange(API)\nversion = 2\n// LINT.ThenChange(" + raw + ")\n"
			files := map[string]string{"nested/source.go": source, "docs/api.md": "<!-- LINT.IfChange(API) -->\nAPI version 1\n<!-- LINT.ThenChange() -->\n"}
			result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"nested/source.go"}})
			if code != 0 || len(result.Findings) != 0 {
				t.Fatalf("structural target resolution failed: %+v", result.Findings)
			}
			result, code, _ = runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"nested/source.go"}, StrictPaths: true})
			if code != 1 || !hasRule(result, "nested/source.go", "invalid_target_path") {
				t.Fatalf("strict mode accepted non-// path: %+v", result.Findings)
			}
			diff := []string{"diff --git a/nested/source.go b/nested/source.go", "--- a/nested/source.go", "+++ b/nested/source.go", "@@ -1,3 +1,3 @@", " // LINT.IfChange(API)", "-version = 1", "+version = 2", " // LINT.ThenChange(" + raw + ")"}
			result, code, _ = runLintWithSetup(t, files, diff, Options{})
			if code != 1 || !hasRule(result, "nested/source.go", "then_label_missing") {
				t.Fatalf("unchanged root target did not fail cochange: %+v", result.Findings)
			}
			files["docs/api.md"] = strings.Replace(files["docs/api.md"], "version 1", "version 2", 1)
			diff = append(diff, "diff --git a/docs/api.md b/docs/api.md", "--- a/docs/api.md", "+++ b/docs/api.md", "@@ -1,3 +1,3 @@", " <!-- LINT.IfChange(API) -->", "-API version 1", "+API version 2", " <!-- LINT.ThenChange() -->")
			result, code, _ = runLintWithSetup(t, files, diff, Options{})
			if code != 0 || len(result.Findings) != 0 {
				t.Fatalf("root target cochange did not satisfy block: %+v", result.Findings)
			}
		})
	}
}

func TestGoogleDriveAbsoluteTargetsRejectedOnEveryHost(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	for _, raw := range []string{`C:\outside.go`, `C:/outside.go`} {
		t.Run(raw, func(t *testing.T) {
			if got := resolveTarget("nested/source.go", raw).Path; got != filepath.Clean(raw) {
				t.Fatalf("drive absolute path converted to local path: %q", got)
			}
			files := map[string]string{"source.go": "// LINT.IfChange(API)\nbody\n// LINT.ThenChange(" + raw + ")\n"}
			result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"source.go"}, UnknownPolicy: "ignore"})
			if code != 1 || !hasRule(result, "source.go", "invalid_target_path") {
				t.Fatalf("drive absolute source target accepted: %+v", result.Findings)
			}
			dirs := []core.LintDirective{{Kind: core.IfChange, Line: 1, Label: "API"}, {Kind: core.ThenChange, Line: 3, List: []string{raw}}}
			findings, valid := validateTargetDirectives("target.go", dirs, "ignore")
			if valid || len(findings) != 1 || findings[0].RuleID != "invalid_target_path" {
				t.Fatalf("referenced target accepted drive absolute path: %+v", findings)
			}
		})
	}
}

func TestIgnoredLabelTargetsDoNotProduceReadErrors(t *testing.T) {
	for _, ignore := range []string{filepath.FromSlash("<INTERNAL>/**"), filepath.FromSlash("<INTERNAL>/release/metadata.sh") + "#LBL"} {
		t.Run(ignore, func(t *testing.T) {
			target := "<INTERNAL>/release/metadata.sh#LBL"
			files := map[string]string{"source.go": "// SENTRY.IfChange(\"SRC\")\nchanged\n// SENTRY.ThenChange(\"" + target + "\")\n"}
			diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,3 +1,3 @@", " // SENTRY.IfChange(\"SRC\")", "-old", "+changed", " // SENTRY.ThenChange(\"" + target + "\")"}
			result, code, _ := runLintWithSetup(t, files, diff, Options{Ignores: []string{ignore}})
			if code != 0 || len(result.Findings) != 0 {
				t.Fatalf("ignored label target generated findings: %+v", result.Findings)
			}
			result, code, _ = runLintWithSetup(t, files, diff, Options{})
			if code != 1 || !hasRule(result, "source.go", "then_missing") {
				t.Fatalf("nonignored missing target passed: %+v", result.Findings)
			}
		})
	}
}

func TestValidGoogleLabelSurvivesUnrelatedTargetStructureErrors(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	for _, tc := range []struct{ rule, extra string }{{"orphan_if", "// LINT.IfChange(OTHER)\nunfinished\n"}, {"orphan_then", "// LINT.ThenChange()\n"}, {"duplicate_label", "// LINT.IfChange(DUP)\nother\n// LINT.ThenChange()\n// LINT.IfChange(DUP)\nother\n// LINT.ThenChange()\n"}} {
		t.Run(tc.rule, func(t *testing.T) {
			files := map[string]string{"source.go": "// LINT.IfChange(SRC)\nchanged\n// LINT.ThenChange(//target.go:LBL)\n", "target.go": "// LINT.IfChange(LBL)\nchanged target\n// LINT.ThenChange()\n" + tc.extra}
			diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,3 +1,3 @@", " // LINT.IfChange(SRC)", "-old", "+changed", " // LINT.ThenChange(//target.go:LBL)", "diff --git a/target.go b/target.go", "--- a/target.go", "+++ b/target.go", "@@ -1,3 +1,3 @@", " // LINT.IfChange(LBL)", "-old target", "+changed target", " // LINT.ThenChange()"}
			result, code, _ := runLintWithSetup(t, files, diff, Options{})
			if code != 1 || !hasRule(result, "target.go", tc.rule) || hasRule(result, "source.go", "label_missing") || hasRule(result, "source.go", "then_label_missing") {
				t.Fatalf("valid changed label lost to unrelated structural error: %+v", result.Findings)
			}
		})
	}
}

func TestDuplicateGoogleLabelCannotSatisfyReference(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	files := map[string]string{"source.go": "// LINT.IfChange(SRC)\nbody\n// LINT.ThenChange(//target.go:LBL)\n", "target.go": "// LINT.IfChange(LBL)\nfirst\n// LINT.ThenChange()\n// LINT.IfChange(LBL)\nsecond\n// LINT.ThenChange()\n"}
	result, code, _ := runLintWithSetup(t, files, nil, Options{StructuralFiles: []string{"source.go"}})
	if code != 1 || !hasRule(result, "target.go", "duplicate_label") || !hasRule(result, "source.go", "label_ambiguous") {
		t.Fatalf("ambiguous label satisfied reference: %+v", result.Findings)
	}
}
