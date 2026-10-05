package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/derhnyel/ifttt/internal"
)

// These legacy fixtures exercise quoted SENTRY directives and array targets.
// Select their syntax explicitly; default-syntax behavior is covered separately.
func TestMain(m *testing.M) {
	core.SetDirectivePrefix("SENTRY")
	os.Exit(m.Run())
}

func runLintWithSetup(t *testing.T, files map[string]string, diffLines []string, opts Options) (Result, int, string) {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	diffText := strings.Join(diffLines, "\n")
	if !strings.HasSuffix(diffText, "\n") {
		diffText += "\n"
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	res, code := Lint(diffText, opts)
	return res, code, dir
}

func TestThenChangeLabelPass(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"A\")",
			"y",
			"// SENTRY.ThenChange(\"target.go#LBL\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// SENTRY.Label(\"LBL\")",
			"line1",
			"line2 changed",
			"line3",
			"// SENTRY.EndLabel",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"A\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go#LBL\")",
		"",
		"diff --git a/target.go b/target.go",
		"--- a/target.go",
		"+++ b/target.go",
		"@@ -1,5 +1,5 @@",
		" // SENTRY.Label(\"LBL\")",
		" line1",
		"-line2",
		"+line2 changed",
		" line3",
		" // SENTRY.EndLabel",
	}, Options{})
	if code != 0 {
		var b strings.Builder
		for _, f := range res.Findings {
			b.WriteString(f.Message)
			b.WriteByte('\n')
		}
		t.Fatalf("unexpected failures: \n%s", b.String())
	}
}

func TestThenChangeRequiresTargetChange(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"A\")",
			"y",
			"// SENTRY.ThenChange(\"target.go\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// target file with no edits",
			"line1",
			"line2",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"A\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go\")",
	}, Options{})

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d (findings=%+v)", code, res.Findings)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected single finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.RuleID != "then_missing" {
		t.Fatalf("expected rule 'then_missing', got %q", f.RuleID)
	}
	want := "expected changes in 'target.go' but none found"
	if f.Message != want {
		t.Fatalf("unexpected message: %q", f.Message)
	}
}

func TestSkipDirsControlsEvaluation(t *testing.T) {
	files := map[string]string{
		".git/hooks/pre-commit": strings.Join([]string{
			"// SENTRY.IfChange",
			"content",
			"// SENTRY.ThenChange(\"pre-commit-target.go\")",
			"",
		}, "\n"),
		".git/hooks/pre-commit-target.go": strings.Join([]string{
			"// target file",
			"line1",
			"line2",
			"",
		}, "\n"),
	}
	diff := []string{
		"diff --git a/.git/hooks/pre-commit b/.git/hooks/pre-commit",
		"--- a/.git/hooks/pre-commit",
		"+++ b/.git/hooks/pre-commit",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange",
		"-content",
		"+content updated",
		" // SENTRY.ThenChange(\"target.go\")",
	}

	if res, code, _ := runLintWithSetup(t, files, diff, Options{}); code != 0 || len(res.Findings) != 0 {
		t.Fatalf("expected default skip dirs to ignore .git, got code=%d findings=%v", code, res.Findings)
	}

	res, code, _ := runLintWithSetup(t, files, diff, Options{SkipDirs: []string{}})
	if code == 0 {
		t.Fatalf("expected non-zero exit code without skip dirs, got %d", code)
	}
	found := false
	for _, f := range res.Findings {
		if f.RuleID == "then_missing" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected then_missing finding, got %v", res.Findings)
	}
}

func TestBatchEvaluationPerTarget(t *testing.T) {
	files := map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"A\")",
			"old",
			"// SENTRY.ThenChange(\"target.go\")",
			"// SENTRY.IfChange(\"B\")",
			"stable",
			"// SENTRY.ThenChange(\"target.go\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// target file",
			"unchanged",
			"",
		}, "\n"),
	}
	diff := []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,6 +1,6 @@",
		" // SENTRY.IfChange(\"A\")",
		"-old",
		"+updated",
		" // SENTRY.ThenChange(\"target.go\")",
		" // SENTRY.IfChange(\"B\")",
		" stable",
		" // SENTRY.ThenChange(\"target.go\")",
	}

	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d", code)
	}
	if len(res.Findings) != 1 || res.Findings[0].RuleID != "then_missing" {
		t.Fatalf("expected single then_missing finding, got %+v", res.Findings)
	}
}

func TestApplyFixes_InsertPlaceholderWhenTargetMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	target := filepath.Join(dir, "target.go")
	findings := []core.Finding{
		{RuleID: "then_missing", Message: fmt.Sprintf("expected changes in '%s' but none found", target)},
	}
	actions, errs := applyFixes(findings)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(actions) != 1 || !strings.Contains(actions[0], "inserted placeholder") {
		t.Fatalf("expected placeholder action, got %v", actions)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if !strings.Contains(string(data), "TODO(iflint)") {
		t.Fatalf("placeholder not inserted: %s", string(data))
	}
}

func TestApplyFixes_CreateLabelBlock(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	target := filepath.Join(dir, "target.go")
	findings := []core.Finding{
		{RuleID: "then_label_missing", Message: fmt.Sprintf("expected changes in '%s#LBL' (10-20) but none found", target)},
	}
	actions, errs := applyFixes(findings)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(actions) != 1 || !strings.Contains(actions[0], "created label") {
		t.Fatalf("expected create label action, got %v", actions)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if !strings.Contains(string(data), "Label(\"LBL\")") || !strings.Contains(string(data), "EndLabel") {
		t.Fatalf("label block not created: %s", string(data))
	}
}

func TestLintFixModeAppliesPlaceholder(t *testing.T) {
	res, code, dir := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"A\")",
			"y",
			"// SENTRY.ThenChange(\"target.go\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// target file with no edits",
			"line1",
			"line2",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"A\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go\")",
	}, Options{Fix: true})
	_ = dir

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got 0")
	}
	if len(res.Findings) == 0 {
		t.Fatalf("expected findings, got none (stats=%v)", res.Stats)
	}
	applied, ok := res.Stats["fix_applied"].([]string)
	if !ok || len(applied) == 0 {
		t.Fatalf("expected fixes to apply, stats=%v", res.Stats)
	}
	targetPath := filepath.Join(dir, "target.go")
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("expected target.go to be present: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected placeholder to be written")
	}
}

func TestApplyFixes_NoDuplicateLabel(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	target := filepath.Join(dir, "target.go")
	content := strings.Join([]string{
		"// SENTRY.Label(\"LBL\")",
		"body",
		"// SENTRY.EndLabel",
		"",
	}, "\n")
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	findings := []core.Finding{
		{RuleID: "label_missing", Message: fmt.Sprintf("label 'LBL' not found in '%s'", target)},
	}
	actions, errs := applyFixes(findings)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(actions) != 0 {
		t.Fatalf("expected no action for existing label, got %v", actions)
	}
}

func TestLabelRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.go")
	content := strings.Join([]string{
		"// SENTRY.Label(\"LBL\")",
		"body",
		"// SENTRY.EndLabel",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ranges, err := LabelRanges(path)
	if err != nil {
		t.Fatalf("LabelRanges: %v", err)
	}
	r, ok := ranges["LBL"]
	if !ok {
		t.Fatalf("missing range for LBL")
	}
	if r.StartLine != 2 {
		t.Fatalf("unexpected start line: %d", r.StartLine)
	}
}

func TestIfChangeBareWithoutThen(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange",
			"y",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,2 +1,2 @@",
		" // SENTRY.IfChange",
		"-x",
		"+y",
	}, Options{})

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d", code)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected single finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.RuleID != "orphan_if" {
		t.Fatalf("expected rule 'orphan_if', got %q", f.RuleID)
	}
	if f.Message != "missing ThenChange after IfChange" {
		t.Fatalf("unexpected message: %q", f.Message)
	}
}

func TestThenChangeMissingTargetFileFails(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"A\")",
			"y",
			"// SENTRY.ThenChange(\"missing.go\")",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"A\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"missing.go\")",
	}, Options{})

	if code == 0 || len(res.Findings) != 1 || res.Findings[0].RuleID != "then_missing" {
		t.Fatalf("missing target passed: %d %+v", code, res.Findings)
	}
}

func TestIfChangeLabelIgnored(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"LBL\")",
			"y",
			"// SENTRY.ThenChange(\"target.go#LBL\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// SENTRY.Label(\"LBL\")",
			"line1",
			"line2",
			"// SENTRY.EndLabel",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"LBL\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go#LBL\")",
	}, Options{Ignores: []string{"target.go#LBL"}})

	if code != 0 {
		t.Fatalf("expected zero exit code when ignored, got %d with findings %+v", code, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", res.Findings)
	}
}

func TestThenChangeLabelMissing(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"LBL\")",
			"y",
			"// SENTRY.ThenChange(\"target.go#LBL\")",

			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// no directive labels here",
			"old",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"LBL\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go#LBL\")",
		"",
		"diff --git a/target.go b/target.go",
		"--- a/target.go",
		"+++ b/target.go",
		"@@ -1,1 +1,1 @@",
		"-old",
		"+changed",
	}, Options{})

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d", code)
	}
	if len(res.Findings) != 1 || res.Findings[0].RuleID != "label_missing" {
		t.Fatalf("expected label_missing finding, got %+v", res.Findings)
	}
}

func TestThenChangeLabelRangeNoChange(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"LBL\")",
			"y",
			"// SENTRY.ThenChange(\"target.go#LBL\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// SENTRY.Label(\"LBL\")",
			"inside",
			"// SENTRY.EndLabel",
			"outside change",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"LBL\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go#LBL\")",
		"",
		"diff --git a/target.go b/target.go",
		"--- a/target.go",
		"+++ b/target.go",
		"@@ -4,1 +4,1 @@",
		"-outside change",
		"+outside change updated",
	}, Options{})

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d", code)
	}
	if len(res.Findings) != 1 || res.Findings[0].RuleID != "then_label_missing" {
		t.Fatalf("expected then_label_missing finding, got %+v", res.Findings)
	}
}

func TestLintDiffParseError(t *testing.T) {
	diff := strings.Join([]string{
		"diff --cc foo.go",
		"index 123..456",
	}, "\n") + "\n"

	res, code := Lint(diff, Options{})
	if code != 1 {
		t.Fatalf("expected non-zero exit code, got %d", code)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected single finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.RuleID != "diff_parse_error" {
		t.Fatalf("expected diff_parse_error, got %q", f.RuleID)
	}
	if !strings.Contains(f.Message, "combined diffs were present") {
		t.Fatalf("unexpected message: %q", f.Message)
	}
}

func TestLintNoChanges(t *testing.T) {
	res, code := Lint("", Options{})
	if code != 0 {
		t.Fatalf("expected zero exit code, got %d", code)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", res.Findings)
	}
}

func TestThenChangeMultipleTargetsMissingOne(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange",
			"updated",
			"// SENTRY.ThenChange([\"target.go\", \"other.go\"])",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"existing",
			"changed",
			"",
		}, "\n"),
		"other.go": "untouched\n",
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange",
		"-old",
		"+updated",
		" // SENTRY.ThenChange([\"target.go\", \"other.go\"])",
		"",
		"diff --git a/target.go b/target.go",
		"--- a/target.go",
		"+++ b/target.go",
		"@@ -1,2 +1,2 @@",
		" existing",
		"-unchanged",
		"+changed",
	}, Options{})

	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d (findings=%+v)", code, res.Findings)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected one finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, "other.go") {
		t.Fatalf("expected message to mention missing target, got %q", res.Findings[0].Message)
	}
}

func TestThenChangeMultipleTargetsAllChanged(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange",
			"updated",
			"// SENTRY.ThenChange([\"target.go\", \"other.go\"])",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"existing",
			"changed",
			"",
		}, "\n"),
		"other.go": strings.Join([]string{
			"existing",
			"changed too",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange",
		"-old",
		"+updated",
		" // SENTRY.ThenChange([\"target.go\", \"other.go\"])",
		"",
		"diff --git a/target.go b/target.go",
		"--- a/target.go",
		"+++ b/target.go",
		"@@ -1,2 +1,2 @@",
		" existing",
		"-unchanged",
		"+changed",
		"",
		"diff --git a/other.go b/other.go",
		"--- a/other.go",
		"+++ b/other.go",
		"@@ -1,2 +1,2 @@",
		" existing",
		"-old",
		"+changed too",
	}, Options{})

	if code != 0 || len(res.Findings) != 0 {
		t.Fatalf("expected success, got code=%d findings=%+v", code, res.Findings)
	}
}

func TestLintReportsParseError(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"bad.go": strings.Join([]string{
			"// SENTRY.EndLabel",
			"content",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/bad.go b/bad.go",
		"--- a/bad.go",
		"+++ b/bad.go",
		"@@ -0,0 +1,2 @@",
		"+// SENTRY.EndLabel",
		"+content",
	}, Options{})

	if code == 0 {
		t.Fatalf("expected failure from parse error, got code %d", code)
	}
	if len(res.Findings) != 1 || res.Findings[0].RuleID != "error" {
		t.Fatalf("expected single error finding, got %+v", res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, "unmatched EndLabel") {
		t.Fatalf("unexpected error message: %q", res.Findings[0].Message)
	}
}

func TestLintIgnoresMatchingPattern(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"ignored.go": strings.Join([]string{
			"// SENTRY.IfChange",
			"updated",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/ignored.go b/ignored.go",
		"--- a/ignored.go",
		"+++ b/ignored.go",
		"@@ -1,2 +1,2 @@",
		" // SENTRY.IfChange",
		"-old",
		"+updated",
	}, Options{Ignores: []string{"ignored.go"}})

	if code != 0 || len(res.Findings) != 0 {
		t.Fatalf("expected ignored file to produce no findings, code=%d findings=%+v", code, res.Findings)
	}
}

func TestLintWithCustomFileProvider(t *testing.T) {
	provider := &memoryFileProvider{files: map[string][]byte{}}
	provider.files[core.NormalizePath("source.go")] = []byte(strings.Join([]string{
		"package main",
		"// SENTRY.IfChange(\"LBL\")",
		"func source() {}",
		"// SENTRY.ThenChange(\"target.go#LBL\")",
	}, "\n") + "\n")
	provider.files[core.NormalizePath("target.go")] = []byte(strings.Join([]string{
		"package main",
		"// SENTRY.Label(\"LBL\")",
		"var target = 1",
		"// SENTRY.EndLabel",
	}, "\n") + "\n")

	diff := strings.Join([]string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -2,3 +2,3 @@",
		" // SENTRY.IfChange(\"LBL\")",
		"-func source() {}",
		"+func source() { println(1) }",
		" // SENTRY.ThenChange(\"target.go#LBL\")",
		"",
		"diff --git a/target.go b/target.go",
		"--- a/target.go",
		"+++ b/target.go",
		"@@ -2,3 +2,3 @@",
		" // SENTRY.Label(\"LBL\")",
		"-var target = 1",
		"+var target = 2",
		" // SENTRY.EndLabel",
	}, "\n") + "\n"

	res, code := Lint(diff, Options{Files: provider})
	if code != 0 {
		t.Fatalf("expected zero exit code, got %d\nfindings: %+v", code, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", res.Findings)
	}
	if provider.reads == 0 {
		t.Fatalf("expected file provider reads to increment")
	}
}

func TestIgnoreDirectiveSuppressesRule(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.Ignore(\"then_missing\")",
			"// SENTRY.IfChange(\"A\")",
			"x",
			"// SENTRY.ThenChange(\"target.go\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"package main",
			"const untouched = true",
			"",
		}, "\n"),
	}, []string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,4 +1,4 @@",
		" // SENTRY.Ignore(\"then_missing\")",
		" // SENTRY.IfChange(\"A\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go\")",
	}, Options{})

	if code != 0 {
		t.Fatalf("expected success exit code when rule suppressed, got %d", code)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no active findings, got %+v", res.Findings)
	}
	if len(res.Suppressed) != 1 {
		t.Fatalf("expected single suppressed finding, got %d: %+v", len(res.Suppressed), res.Suppressed)
	}
	s := res.Suppressed[0]
	if s.RuleID != "then_missing" {
		t.Fatalf("expected suppressed rule 'then_missing', got %q", s.RuleID)
	}
	if !s.Suppressed {
		t.Fatalf("expected finding to be marked suppressed")
	}
	if s.File != "source.go" {
		t.Fatalf("expected suppressed finding to refer to source.go, got %q", s.File)
	}
}

func mapKeys[K comparable, V any](m map[K]V) []K {
	var out []K
	for k := range m {
		out = append(out, k)
	}
	return out
}

type memoryFileProvider struct {
	files map[string][]byte
	stats int
	reads int
	mu    sync.Mutex
}

func (m *memoryFileProvider) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	key := core.NormalizePath(path)
	data, ok := m.files[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *memoryFileProvider) Stat(path string) (FileStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats++
	key := core.NormalizePath(path)
	data, ok := m.files[key]
	if !ok {
		return FileStat{}, os.ErrNotExist
	}
	return FileStat{Path: key, Size: int64(len(data)), ModTime: time.Unix(0, 0)}, nil
}

func BenchmarkLintLargeWorkspace(b *testing.B) {
	dir := b.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		b.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		b.Fatalf("chdir: %v", err)
	}
	b.Cleanup(func() { _ = os.Chdir(oldwd) })

	const files = 200
	var diffLines []string
	for i := 0; i < files; i++ {
		sourceName := fmt.Sprintf("src_%03d.go", i)
		targetName := fmt.Sprintf("target_%03d.go", i)
		label := fmt.Sprintf("LBL%d", i)

		source := strings.Join([]string{
			"package main",
			fmt.Sprintf("// SENTRY.IfChange(\"%s\")", label),
			"val := \"old\"",
			fmt.Sprintf("// SENTRY.ThenChange(\"%s#%s\")", targetName, label),
		}, "\n") + "\n"
		target := strings.Join([]string{
			"package main",
			fmt.Sprintf("// SENTRY.Label(\"%s\")", label),
			"value := \"before\"",
			"// SENTRY.EndLabel",
		}, "\n") + "\n"

		if err := os.WriteFile(sourceName, []byte(source), 0o644); err != nil {
			b.Fatalf("write %s: %v", sourceName, err)
		}
		if err := os.WriteFile(targetName, []byte(target), 0o644); err != nil {
			b.Fatalf("write %s: %v", targetName, err)
		}

		diffLines = append(diffLines,
			fmt.Sprintf("diff --git a/%s b/%s", sourceName, sourceName),
			fmt.Sprintf("--- a/%s", sourceName),
			fmt.Sprintf("+++ b/%s", sourceName),
			"@@ -2,2 +2,2 @@",
			fmt.Sprintf(" // SENTRY.IfChange(\"%s\")", label),
			"-val := \"old\"",
			"+val := \"new\"",
			fmt.Sprintf(" // SENTRY.ThenChange(\"%s#%s\")", targetName, label),
			"",
			fmt.Sprintf("diff --git a/%s b/%s", targetName, targetName),
			fmt.Sprintf("--- a/%s", targetName),
			fmt.Sprintf("+++ b/%s", targetName),
			"@@ -2,2 +2,2 @@",
			fmt.Sprintf(" // SENTRY.Label(\"%s\")", label),
			"-value := \"before\"",
			"+value := \"after\"",
			" // SENTRY.EndLabel",
			"",
		)
	}

	diffText := strings.Join(diffLines, "\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, code := Lint(diffText, Options{}); code != 0 {
			b.Fatalf("unexpected non-zero code %d", code)
		}
	}
}

func TestParseChangedLinesSkipsFilesWithoutChanges(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/foo.txt b/foo.txt",
		"index 1234567..1234567 100644",
		"--- a/foo.txt",
		"+++ b/foo.txt",
	}, "\n") + "\n"

	changes, err := ParseChangedLines(diff)
	if err != nil {
		t.Fatalf("ParseChangedLines returned error: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no entries, got %d (%v)", len(changes), changes)
	}
}

func TestParseChangedLinesNormalizesQuotedPath(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git \"a/foo bar.go\" \"b/foo bar.go\"",
		"--- \"a/foo bar.go\"",
		"+++ \"b/foo bar.go\"",
		"@@ -1 +1 @@",
		"-old",
		"+new",
	}, "\n") + "\n"

	changes, err := ParseChangedLines(diff)
	if err != nil {
		t.Fatalf("ParseChangedLines returned error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change entry, got %d (%v)", len(changes), changes)
	}
	if _, ok := changes["foo bar.go"]; !ok {
		t.Fatalf("expected normalized key \"foo bar.go\", got keys %v", mapKeys(changes))
	}
}

func BenchmarkFixMode(b *testing.B) {
	b.ReportAllocs()
	dir := b.TempDir()
	files := map[string]string{
		"source.go": strings.Join([]string{
			"// SENTRY.IfChange(\"A\")",
			"y",
			"// SENTRY.ThenChange(\"target.go\")",
			"",
		}, "\n"),
		"target.go": strings.Join([]string{
			"// target file with no edits",
			"line1",
			"line2",
			"",
		}, "\n"),
	}
	diff := strings.Join([]string{
		"diff --git a/source.go b/source.go",
		"--- a/source.go",
		"+++ b/source.go",
		"@@ -1,3 +1,3 @@",
		" // SENTRY.IfChange(\"A\")",
		"-x",
		"+y",
		" // SENTRY.ThenChange(\"target.go\")",
	}, "\n") + "\n"

	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			b.Fatalf("write %s: %v", name, err)
		}
	}

	oldwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	for i := 0; i < b.N; i++ {
		res, code := Lint(diff, Options{Fix: true})
		if code == 0 {
			b.Fatalf("expected non-zero exit code")
		}
		if applied, ok := res.Stats["fix_applied"].([]string); !ok || len(applied) == 0 {
			b.Fatalf("expected fix to apply, stats=%v", res.Stats)
		}
	}
}

func TestDirectiveCacheIncludesPrefix(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	dir := t.TempDir()
	path := filepath.Join(dir, "prefix.go")
	if err := os.WriteFile(path, []byte("// SENTRY.Label(\"ONE\")\n// SENTRY.EndLabel\n// OTHER.Label(\"TWO\")\n// OTHER.EndLabel\n"), 0644); err != nil {
		t.Fatal(err)
	}
	core.SetDirectivePrefix("SENTRY")
	first, err := loadDirectives(localFileProvider{}, path)
	if err != nil {
		t.Fatal(err)
	}
	core.SetDirectivePrefix("OTHER")
	second, err := loadDirectives(localFileProvider{}, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 || second[0].Name != "TWO" {
		t.Fatalf("prefix switch reused stale directives: %v", second)
	}
}

func TestCombinedWarnPolicy(t *testing.T) {
	diff := "diff --cc source.go\nindex abc,def..123\n--- a/source.go\n+++ b/source.go\n@@@ -1,1 -1,1 +1,1 @@@\n++changed\n"
	result, code := Lint(diff, Options{CombinedDiffPolicy: CombinedDiffWarn})
	if code != 0 || result.Stats["combined_diff"] != "skipped" {
		t.Fatalf("warn policy did not report the skipped combined diff: code=%d stats=%v findings=%+v", code, result.Stats, result.Findings)
	}
}

func TestRequireAnyMissingTargetFailsClosed(t *testing.T) {
	res, code, _ := runLintWithSetup(t, map[string]string{"source.go": "// SENTRY.RequireAny([\"target.go\"])\n"}, []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1 +1 @@", "-// old", "+// SENTRY.RequireAny([\"target.go\"])"}, Options{})
	if code == 0 || len(res.Findings) == 0 {
		t.Fatal("missing RequireAny target silently passed")
	}
}

func TestLintRootRelativeTarget(t *testing.T) {
	old := core.CurrentDirectiveSyntax().Prefix
	defer core.SetDirectivePrefix(old)
	core.SetDirectivePrefix("LINT")
	target := resolveTarget("nested/source.go", "//docs/api.md:API")
	if target.Path != "docs/api.md" || target.Label != "API" {
		t.Fatalf("wrong target: %+v", target)
	}
	same := resolveTarget("nested/source.go", ":API")
	if same.Path != "nested/source.go" {
		t.Fatalf("wrong same-file target: %+v", same)
	}
}

func TestRemovedLinesProjectedIntoCurrentBlock(t *testing.T) {
	headers := strings.Repeat("header\n", 10)
	files := map[string]string{"source.go": headers + "// SENTRY.IfChange(\"X\")\nkept\n// SENTRY.ThenChange(\"target.go\")\n", "target.go": "unchanged\n"}
	diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,4 +1,13 @@"}
	for i := 0; i < 10; i++ {
		diff = append(diff, "+header")
	}
	diff = append(diff, " // SENTRY.IfChange(\"X\")", "-removed body", " kept", " // SENTRY.ThenChange(\"target.go\")")
	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code == 0 || len(res.Findings) == 0 {
		t.Fatal("body deletion missed after earlier lines were inserted")
	}
}
