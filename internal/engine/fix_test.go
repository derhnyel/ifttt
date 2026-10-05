package engine

import (
	"fmt"
	core "github.com/derhnyel/ifttt/internal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureLabelBlockSkipsExistingLabel(t *testing.T) {
	dir := t.TempDir()
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
	if err := ensureLabelBlock(target, "LBL"); err != nil {
		t.Fatalf("ensureLabelBlock: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Count(string(data), "Label(\"LBL\")") != 1 {
		t.Fatalf("label duplicated: %s", string(data))
	}
}

func TestApplyFixesRejectsOutsideWorkspace(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.go")
	actions, errs := applyFixes([]core.Finding{{RuleID: "then_missing", Message: fmt.Sprintf("expected changes in '%s' but none found", outside)}})
	if len(actions) != 0 || len(errs) != 1 {
		t.Fatalf("unsafe fix accepted: %v %v", actions, errs)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file created: %v", err)
	}
}
func TestEnsureLabelBlockUsesPythonComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.py")
	if err := ensureLabelBlock(path, "LBL"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# SENTRY.Label") || strings.Contains(string(data), "//") {
		t.Fatalf("invalid python: %s", data)
	}
}

func TestLabelFixRejectsOutsideWorkspace(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.go")
	actions, errs := applyFixes([]core.Finding{{RuleID: "then_label_missing", Message: fmt.Sprintf("expected changes in '%s#LBL' but none found", outside)}})
	if len(actions) != 0 || len(errs) != 1 {
		t.Fatalf("unsafe labelled fix accepted: %v %v", actions, errs)
	}
}

func TestHTMLFixUsesHTMLComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "target.html")
	if err := ensureLabelBlock(p, "LBL"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<!-- SENTRY.Label(\"LBL\") -->") {
		t.Fatalf("invalid HTML placeholder: %s", data)
	}
}

func TestLabelPlaceholderInsideExistingRangeAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.go")
	original := "// SENTRY.Label(\"LBL\")\nbody\n// SENTRY.EndLabel\n"
	if err := os.WriteFile(path, []byte(original), 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ensureLabelPlaceholder(path, "LBL"); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "TODO(iflint)") != 1 || strings.Index(text, "TODO(iflint)") > strings.Index(text, "SENTRY.EndLabel") {
		t.Fatalf("bad placeholder: %s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("lost permissions: %v", info.Mode())
	}
}
