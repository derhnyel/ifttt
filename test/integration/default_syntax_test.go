package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultLINTWithoutPrefixConfiguration(t *testing.T) {
	for _, config := range []string{"", "output:\n  format: json\n", "directives:\n  prefix: '   '\n"} {
		t.Run(config, func(t *testing.T) {
			r := newDefaultRepo(t)
			if config != "" {
				r.write(t, ".ifttt-lint.yaml", config)
			}
			if err := os.Mkdir(filepath.Join(r.dir, "docs"), 0700); err != nil {
				t.Fatal(err)
			}
			source := "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//docs/api.md:API)\n"
			target := "<!-- LINT.IfChange(API) -->\nAPI version: 1\n<!-- LINT.ThenChange() -->\n"
			r.write(t, "source.go", source)
			r.write(t, "docs/api.md", target)
			r.git(t, "add", ".")
			r.git(t, "commit", "-qm", "standard LINT baseline")
			requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
			r.write(t, "source.go", strings.Replace(source, "api = 1", "api = 2", 1))
			output := requireCode(t, r, "", 1, "--vcs=git", "--format=json", "--strict=true")
			if !strings.Contains(output, "then_label_missing") {
				t.Fatalf("default LINT dependency not checked: %s", output)
			}
			r.write(t, "docs/api.md", strings.Replace(target, "version: 1", "version: 2", 1))
			requireCode(t, r, "", 0, "--vcs=git", "--strict=true")
		})
	}
}

func TestDefaultLINTAuxiliaryCommands(t *testing.T) {
	r := newDefaultRepo(t)
	requireCode(t, r, "", 0, "scaffold", "--source", "source.go", "--target", "target.go#API")
	for name, want := range map[string]string{
		"source.go": "LINT.ThenChange(//target.go:API)",
		"target.go": "LINT.IfChange(API)",
	} {
		data, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("default scaffold %s: %s, %v", name, data, err)
		}
	}
	requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "ignore", "add", "--file", "source.go", "--rule", "then_label_missing")
	data, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
	if err != nil || !strings.Contains(string(data), `LINT.Ignore("then_label_missing")`) {
		t.Fatalf("default ignore: %s, %v", data, err)
	}
}

func TestDefaultLINTDoctorFixKeepsStandardTargetLists(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.go", "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.go:API, //target.go:API)\n")
	r.write(t, "target.go", "// LINT.IfChange(API)\nvar target = 1\n// LINT.ThenChange()\n")
	requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 1, "--doctor", "--fix", "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
	data, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
	if err != nil || !strings.Contains(string(data), "LINT.ThenChange(//target.go:API)") {
		t.Fatalf("doctor changed standard syntax: %s, %v", data, err)
	}
	requireCode(t, r, "", 0, "--doctor", "--fix", "--scan", ".", "--strict=true")
	after, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
	if err != nil || string(after) != string(data) {
		t.Fatalf("doctor fix was not idempotent: %s, %v", after, err)
	}
}

func TestDefaultLINTDoctorFixCreatesReferencedLabel(t *testing.T) {
	r := newDefaultRepo(t)
	if err := os.Mkdir(filepath.Join(r.dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	r.write(t, "src/source.go", "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.go:API)\n")
	r.write(t, "target.go", "var target = 1\n")
	requireCode(t, r, "", 1, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 1, "--doctor", "--fix", "--scan", ".", "--strict=true")
	data, err := os.ReadFile(filepath.Join(r.dir, "target.go"))
	if err != nil || !strings.Contains(string(data), "LINT.IfChange(API)") || !strings.Contains(string(data), "LINT.ThenChange()") {
		t.Fatalf("doctor did not scaffold native target label: %s, %v", data, err)
	}
	requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "--doctor", "--fix", "--scan", ".", "--strict=true")
}

func TestDefaultLINTDoctorFixSameFileLabel(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.go", "// LINT.IfChange(SOURCE)\nvar api = 1\n// LINT.ThenChange(:OTHER, :OTHER)\n")
	requireCode(t, r, "", 1, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 1, "--doctor", "--fix", "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "--doctor", "--fix", "--scan", ".", "--strict=true")
	data, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
	if err != nil || !strings.Contains(string(data), "LINT.ThenChange(:OTHER)") || !strings.Contains(string(data), "LINT.IfChange(OTHER)") {
		t.Fatalf("same-file dedupe lost scaffold: %s, %v", data, err)
	}
}

func TestDefaultLINTDoctorFixExtendedLabel(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.go", "// LINT.IfChange(SOURCE)\nvar api = 1\n// LINT.ThenChange(//target.go#shared label)\n")
	r.write(t, "target.go", "var target = 1\n")
	requireCode(t, r, "", 1, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 1, "--doctor", "--fix", "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "--scan", ".", "--strict=true")
	requireCode(t, r, "", 0, "--doctor", "--fix", "--scan", ".", "--strict=true")
	data, err := os.ReadFile(filepath.Join(r.dir, "target.go"))
	if err != nil || !strings.Contains(string(data), `LINT.Label("shared label")`) {
		t.Fatalf("extended label was not preserved: %s, %v", data, err)
	}
}
