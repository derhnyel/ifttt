package out_test

import (
	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/out"
	"strings"
	"testing"
)

func TestSnapshotMetadataSurvivesReporters(t *testing.T) {
	finding := core.Finding{File: "/checkout/source.go", Line: 3, RuleID: "then_missing", Severity: "error", Message: "missing change", Repository: "acme/api", BaseRevision: "base-commit", HeadRevision: "head-commit"}
	for name, writer := range map[string]core.ResultWriter{"json": out.JSON{}, "sarif": out.SARIF{}, "text": out.Text{}, "dls": out.DiagnosticLS{}} {
		t.Run(name, func(t *testing.T) {
			text := captureStdout(t, func() {
				if err := writer.Write([]core.Finding{finding}, nil); err != nil {
					t.Fatal(err)
				}
			})
			for _, want := range []string{finding.Repository, finding.BaseRevision, finding.HeadRevision} {
				if !strings.Contains(text, want) {
					t.Fatalf("lost revision identity %q: %s", want, text)
				}
			}
		})
	}
}
