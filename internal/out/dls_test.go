package out

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

func TestDiagnosticLSWrite(t *testing.T) {
	writer := DiagnosticLS{}
	active := []core.Finding{{
		RuleID:   "then_missing",
		Severity: "error",
		File:     "source.go",
		Line:     7,
		Message:  "msg",
		Summary:  "sum",
	}}
	suppressed := []core.Finding{{
		RuleID:     "label_missing",
		Severity:   "warning",
		File:       "target.go",
		Line:       3,
		Message:    "suppressed",
		Suppressed: true,
	}}
	out := captureStdout(t, func() {
		if err := writer.Write(active, suppressed); err != nil {
			t.Fatalf("Write: %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one active item, got %#v", payload["items"])
	}
	first := items[0].(map[string]any)
	if first["file"] != "source.go" {
		t.Fatalf("expected file key to be source.go, got %v", first["file"])
	}
	diags := first["diagnostics"].([]any)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %d", len(diags))
	}
	diag := diags[0].(map[string]any)
	if diag["code"] != "then_missing" || diag["severity"].(float64) != 1 {
		t.Fatalf("unexpected diagnostic payload: %#v", diag)
	}
	if _, ok := payload["suppressed"]; !ok {
		t.Fatalf("expected suppressed key present")
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
		w.Close()
	}()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	<-done
	os.Stdout = old
	return string(data)
}
