package out_test

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
	out "github.com/derhnyel/ifttt/internal/out"
)

func TestTextWrite(t *testing.T) {
	findings := []core.Finding{
		{RuleID: "rule1", File: "a.go", Line: 5, Message: "msg1"},
		{RuleID: "rule2", File: "b.go", Line: 7, Message: "msg2"},
	}
	suppressed := []core.Finding{
		{RuleID: "rule3", File: "c.go", Line: 9, Message: "msg3"},
	}
	w := out.Text{}
	output := captureStdout(t, func() {
		if err := w.Write(findings, suppressed); err != nil {
			t.Fatalf("Text.Write: %v", err)
		}
	})
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != len(findings)+len(suppressed) {
		t.Fatalf("expected %d lines, got %d (%q)", len(findings)+len(suppressed), len(lines), output)
	}
	for i, f := range findings {
		expect := "[" + f.RuleID + "] " + f.File + ":" + strconv.Itoa(f.Line) + " " + f.Message
		if lines[i] != expect {
			t.Fatalf("line %d = %q, want %q", i, lines[i], expect)
		}
	}
	last := lines[len(lines)-1]
	expectSuppressed := "[suppressed:" + suppressed[0].RuleID + "] " + suppressed[0].File + ":" + strconv.Itoa(suppressed[0].Line) + " " + suppressed[0].Message
	if last != expectSuppressed {
		t.Fatalf("suppressed line = %q, want %q", last, expectSuppressed)
	}
}

func TestJSONWrite(t *testing.T) {
	fs := []core.Finding{{RuleID: "r", Severity: "error"}}
	suppressed := []core.Finding{{RuleID: "sr", Severity: "error"}}
	w := out.JSON{}
	output := captureStdout(t, func() {
		if err := w.Write(fs, suppressed); err != nil {
			t.Fatalf("JSON.Write: %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("json decode: %v; raw=%q", err, output)
	}
	if _, exists := decoded["workspaceRoot"]; exists {
		t.Fatalf("default JSON writer unexpectedly adds workspaceRoot: %s", output)
	}
	errorsVal, ok := decoded["errors"].([]any)
	if !ok || len(errorsVal) != 1 {
		t.Fatalf("unexpected errors payload: %#v", decoded["errors"])
	}
	suppressedVal, ok := decoded["suppressed"].([]any)
	if !ok || len(suppressedVal) != 1 {
		t.Fatalf("unexpected suppressed payload: %#v", decoded["suppressed"])
	}
}

func TestJSONWorkspaceRootUsesWriteTimeDirectory(t *testing.T) {
	w := out.JSON{IncludeWorkspaceRoot: true}
	t.Chdir(t.TempDir())
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() {
		if err := w.Write(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	var decoded struct {
		WorkspaceRoot string `json:"workspaceRoot"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.WorkspaceRoot != want {
		t.Fatalf("workspaceRoot = %q, want %q", decoded.WorkspaceRoot, want)
	}
}

func TestSARIFWrite(t *testing.T) {
	fs := []core.Finding{{RuleID: "r1", Severity: "error", File: "a.go", Line: 2, Message: "msg"}}
	suppressed := []core.Finding{{RuleID: "r2", Severity: "error", File: "b.go", Line: 4, Message: "msg2"}}
	w := out.SARIF{}
	output := captureStdout(t, func() {
		if err := w.Write(fs, suppressed); err != nil {
			t.Fatalf("SARIF.Write: %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if decoded["version"] != "2.1.0" {
		t.Fatalf("expected SARIF version, got %v", decoded["version"])
	}
	runs, ok := decoded["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("unexpected runs: %#v", decoded["runs"])
	}
	run := runs[0].(map[string]any)
	results, ok := run["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("unexpected results: %#v", run["results"])
	}
	result := results[0].(map[string]any)
	if result["ruleId"] != "r1" {
		t.Fatalf("unexpected ruleId: %v", result["ruleId"])
	}
	suppResult := results[1].(map[string]any)
	if props, ok := suppResult["properties"].(map[string]any); !ok || props["suppressed"] != true {
		t.Fatalf("expected suppressed properties, got %#v", suppResult["properties"])
	}
	if _, ok := suppResult["suppressions"]; !ok {
		t.Fatalf("expected suppressions entry on suppressed result")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(data)
}
