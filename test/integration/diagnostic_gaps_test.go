package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeSpacedTargetsAndBackslashContinuations(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			if _, err := exec.LookPath(backend); err != nil {
				t.Skip(err)
			}
			r := newRepo(t)
			if backend == "jj" {
				r.jj(t, "git", "init", "--colocate")
			}
			r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
			text := "// LINT.IfChange(API)\nold\n// LINT.ThenChange( \\\n// //target file.go:API, \\\n// //other.go:API)\n"
			target := "// LINT.IfChange(API)\nold\n// LINT.ThenChange()\n"
			r.write(t, "source.go", text)
			r.write(t, "target file.go", target)
			r.write(t, "other.go", target)
			if backend == "git" {
				r.git(t, "add", ".")
				r.git(t, "commit", "-qm", "baseline")
			} else {
				r.jj(t, "describe", "-m", "baseline")
				r.jj(t, "new")
			}
			requireCode(t, r, "", 0, "--vcs="+backend, "--", "source.go")
			r.write(t, "source.go", strings.Replace(text, "old", "new", 1))
			out := requireCode(t, r, "", 1, "--vcs="+backend, "--format=json")
			if strings.Count(out, `"ruleId": "then_label_missing"`) != 2 {
				t.Fatalf("both continued targets must be enforced: %s", out)
			}
			r.write(t, "target file.go", strings.Replace(target, "old", "new", 1))
			out = requireCode(t, r, "", 1, "--vcs="+backend, "--format=json")
			if strings.Count(out, `"ruleId": "then_label_missing"`) != 1 {
				t.Fatalf("one target change must not satisfy the other: %s", out)
			}
			r.write(t, "other.go", strings.Replace(target, "old", "new", 1))
			requireCode(t, r, "", 0, "--vcs="+backend, "--format=json")
		})
	}
}

func TestNativeUnconfiguredURLsRejectLocalAliases(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
	for _, scheme := range []string{"https", "github", "sftp"} {
		t.Run(scheme, func(t *testing.T) {
			raw := scheme + "://example.invalid/target.go"
			if runtime.GOOS != "windows" {
				alias := scheme + ":/example.invalid"
				if err := os.MkdirAll(filepath.Join(r.dir, alias), 0700); err != nil {
					t.Fatal(err)
				}
				r.write(t, alias+"/target.go", "int local_alias;\n")
			}
			r.write(t, "source.go", "// LINT.IfChange(API)\nbody\n// LINT.ThenChange("+raw+")\n")
			out := requireCode(t, r, "", 1, "--vcs=git", "--format=json", "--", "source.go")
			if !strings.Contains(out, "configured provider") || !strings.Contains(out, `"ruleId": "invalid_target_path"`) {
				t.Fatalf("URL was treated as a local file: %s", out)
			}
		})
	}
}

func TestNativeMissingLabelledTargetReportsOneDependencyError(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
	r.write(t, "source.go", "// LINT.IfChange(API)\nbody\n// LINT.ThenChange(//missing.go:API)\n")
	out := requireCode(t, r, "", 1, "--vcs=git", "--format=json", "--", "source.go")
	var report struct {
		Errors []struct {
			File, TargetPath, TargetLabel string
		}
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 1 || report.Errors[0].File != "source.go" || report.Errors[0].TargetPath != "missing.go" || report.Errors[0].TargetLabel != "API" {
		t.Fatalf("dependency error lost or duplicated: %s", out)
	}
}

func TestNativeDuplicateLabelsReportAmbiguity(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
	r.write(t, "source.go", "// LINT.IfChange(SRC)\nbody\n// LINT.ThenChange(//target.go:API)\n")
	r.write(t, "target.go", "// LINT.IfChange(API)\none\n// LINT.ThenChange()\n// LINT.IfChange(API)\ntwo\n// LINT.ThenChange()\n")
	out := requireCode(t, r, "", 1, "--vcs=git", "--format=json", "--", "source.go")
	if !strings.Contains(out, `"ruleId": "label_ambiguous"`) || !strings.Contains(out, `"ruleId": "duplicate_label"`) || strings.Contains(out, `"ruleId": "label_missing"`) {
		t.Fatalf("duplicate labels misreported: %s", out)
	}
	var report struct {
		Errors []struct{ RuleID, Summary, Resolution string }
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Errors {
		if f.RuleID == "label_ambiguous" && (f.Summary == "" || !strings.Contains(f.Resolution, "Rename")) {
			t.Fatalf("ambiguity needs an actionable explanation: %s", out)
		}
	}
	before, err := os.ReadFile(filepath.Join(r.dir, "target.go"))
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, r, "", 1, "--vcs=git", "--fix", "--", "source.go")
	after, err := os.ReadFile(filepath.Join(r.dir, "target.go"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("autofix added another ambiguous label: %s, %v", after, err)
	}
}
