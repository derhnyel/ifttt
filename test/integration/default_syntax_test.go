package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LINT.IfChange(strict_remote_paths)
func TestDefaultStrictPathsAllowConfiguredRemoteReferences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/repos/acme/contracts/contents/docs/api.md") || request.URL.Query().Get("ref") != "main" {
			http.Error(w, "unexpected remote request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("<!-- LINT.IfChange(API) -->\nAPI version 1\n<!-- LINT.ThenChange() -->\n"))
	}))
	defer server.Close()
	r := newDefaultRepo(t)
	r.write(t, ".ifttt-lint.yaml", "remotes:\n  - type: github\n    repo: acme/contracts\n    default_ref: main\n    base_url: "+server.URL+"/\n")
	r.write(t, "source.go", "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(github://acme/contracts/docs/api.md#API)\n")
	for _, strict := range []string{"--strict=true", "--strict=false"} {
		requireCode(t, r, "", 0, "--format=json", strict, "source.go")
	}
	r.write(t, "source.go", "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(github://acme/contracts/docs/api.md#UNKNOWN)\n")
	output := requireCode(t, r, "", 1, "--format=json", "source.go")
	if !strings.Contains(output, "label_missing") {
		t.Fatalf("remote target label was not validated: %s", output)
	}
}

// LINT.ThenChange(//internal/engine/engine.go:strict_remote_paths)

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

// LINT.IfChange(replaced_block)
func TestRenamedGuardStillChecksChangedBodyWithRepeatedCloser(t *testing.T) {
	r := newDefaultRepo(t)
	source := "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.go:shared_label)\n"
	r.write(t, "source.go", source)
	r.write(t, "target.go", "// LINT.IfChange(shared_label)\nvar target = 1\n// LINT.ThenChange()\n")
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	changed := strings.Replace(strings.Replace(source, "IfChange(API)", "IfChange(API_RENAMED)", 1), "api = 1", "api = 2", 1)
	changed += "// LINT.IfChange(shared_label)\n// LINT.ThenChange(//target.go:shared_label)\n"
	r.write(t, "source.go", changed)
	for _, args := range [][]string{{"--vcs", "git", "--format=json"}, {"--format=json", "-"}, {"--vcs", "git", "--code-only", "--format=json"}} {
		output := requireCode(t, r, r.git(t, "diff"), 1, args...)
		if !strings.Contains(output, "then_label_missing") {
			t.Fatalf("renamed existing guard skipped: %s", output)
		}
		var report struct{ Errors []struct{ Line int } }
		if err := json.Unmarshal([]byte(output), &report); err != nil || len(report.Errors) != 1 || report.Errors[0].Line != 3 {
			t.Fatalf("finding must belong to the existing guard: %s (%v)", output, err)
		}
	}
}

func TestRenamedBlockCommentGuardKeepsItsOwnFinding(t *testing.T) {
	r := newDefaultRepo(t)
	source := "/*\n * LINT.IfChange(API)\n */\nvar api = 1\n/*\n * LINT.ThenChange(//target.go:A)\n */\n"
	r.write(t, "source.go", source)
	r.write(t, "target.go", "// LINT.IfChange(A)\nvar target = 1\n// LINT.ThenChange()\n")
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	changed := strings.Replace(strings.Replace(source, "IfChange(API)", "IfChange(RENAMED)", 1), "api = 1", "api = 2", 1)
	changed += "/*\n * LINT.IfChange(NEW)\n */\n/*\n * LINT.ThenChange(//target.go:A)\n */\n"
	r.write(t, "source.go", changed)
	output := requireCode(t, r, "", 1, "--vcs", "git", "--format=json")
	var report struct{ Errors []struct{ Line int } }
	if err := json.Unmarshal([]byte(output), &report); err != nil || len(report.Errors) != 1 || report.Errors[0].Line != 6 {
		t.Fatalf("block-comment finding must belong to the existing guard: %s (%v)", output, err)
	}
}

func TestEditedGuardDoesNotActivateNewNeighbour(t *testing.T) {
	for _, prepend := range []bool{false, true} {
		t.Run(fmt.Sprint(prepend), func(t *testing.T) {
			r := newDefaultRepo(t)
			source := "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target-a.go:A)\n"
			r.write(t, "source.go", source)
			for _, label := range []string{"A", "B", "C"} {
				r.write(t, "target-"+strings.ToLower(label)+".go", "// LINT.IfChange("+label+")\nvar target = 1\n// LINT.ThenChange()\n")
			}
			r.git(t, "add", ".")
			r.git(t, "commit", "-qm", "baseline")
			changed := strings.Replace(strings.Replace(source, "IfChange(API)", "IfChange(RENAMED)", 1), "api = 1", "api = 2", 1)
			changed = strings.Replace(changed, "//target-a.go:A)", "//target-a.go:A, //target-c.go:C)", 1)
			newBlock := "// LINT.IfChange(NEW)\nvar fresh = 1\n// LINT.ThenChange(//target-b.go:B)\n"
			if prepend {
				changed = newBlock + changed
			} else {
				changed += newBlock
			}
			r.write(t, "source.go", changed)
			r.write(t, "target-a.go", "// LINT.IfChange(A)\nvar target = 2\n// LINT.ThenChange()\n")
			r.write(t, "target-c.go", "// LINT.IfChange(C)\nvar target = 2\n// LINT.ThenChange()\n")
			requireCode(t, r, "", 0, "--vcs", "git", "--format=json")
		})
	}
}

func TestPrependedNewBlockCannotHideRenamedGuardDependency(t *testing.T) {
	r := newDefaultRepo(t)
	source := "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target-a.go:A)\n"
	r.write(t, "source.go", source)
	for _, label := range []string{"A", "B"} {
		r.write(t, "target-"+strings.ToLower(label)+".go", "// LINT.IfChange("+label+")\nvar target = 1\n// LINT.ThenChange()\n")
	}
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	changed := "// LINT.IfChange(NEW)\nvar fresh = 1\n// LINT.ThenChange(//target-b.go:B)\n" +
		strings.Replace(strings.Replace(source, "IfChange(API)", "IfChange(RENAMED)", 1), "api = 1", "api = 2", 1)
	r.write(t, "source.go", changed)
	for _, editNewTarget := range []bool{false, true} {
		if editNewTarget {
			r.write(t, "target-b.go", "// LINT.IfChange(B)\nvar target = 2\n// LINT.ThenChange()\n")
		}
		for _, args := range [][]string{{"--vcs", "git", "--format=json"}, {"--format=json", "-"}, {"--vcs", "git", "--code-only", "--format=json"}} {
			output := requireCode(t, r, r.git(t, "diff"), 1, args...)
			var report struct {
				Errors []struct {
					RuleID, TargetPath, TargetLabel string
					Line                            int
				}
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil || len(report.Errors) != 1 ||
				report.Errors[0].RuleID != "then_label_missing" || report.Errors[0].Line != 6 ||
				!strings.HasSuffix(report.Errors[0].TargetPath, "target-a.go") || report.Errors[0].TargetLabel != "A" {
				t.Fatalf("finding must identify the renamed original block and target A: %s (%v)", output, err)
			}
		}
	}
	r.write(t, "target-a.go", "// LINT.IfChange(A)\nvar target = 2\n// LINT.ThenChange()\n")
	requireCode(t, r, "", 0, "--vcs", "git", "--format=json")
}

func TestNewGuardDoesNotEnforceCoChangesForItsInitialContent(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.go", "var api = 1\n")
	r.write(t, "target.go", "var target = 1\n")
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	r.write(t, "source.go", "// LINT.IfChange(API)\nvar api = 2\n// LINT.ThenChange(//target.go)\n")
	requireCode(t, r, "", 0, "--vcs", "git", "--format=json")
}

func TestRenamedGuardCanChangeTargetsWithoutActivatingNewGuard(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.go", "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target-x.go:X)\n")
	for _, label := range []string{"X", "Y"} {
		r.write(t, "target-"+strings.ToLower(label)+".go", "// LINT.IfChange("+label+")\nvar target = 1\n// LINT.ThenChange()\n")
	}
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	r.write(t, "source.go", "// LINT.IfChange(RENAMED)\nvar api = 2\n// LINT.ThenChange(//target-y.go:Y)\n// LINT.IfChange(NEW)\nvar fresh = 1\n// LINT.ThenChange(//target-x.go:X)\n")
	r.write(t, "target-y.go", "// LINT.IfChange(Y)\nvar target = 2\n// LINT.ThenChange()\n")
	requireCode(t, r, "", 0, "--vcs", "git", "--format=json")
	// A new neighbour can copy the prior body without inheriting its dependency.
	r.write(t, "source.go", "// LINT.IfChange(RENAMED)\nvar api = 2\n// LINT.ThenChange(//target-y.go:Y)\n// LINT.IfChange(NEW)\nvar api = 1\n// LINT.ThenChange(//target-x.go:X)\n")
	requireCode(t, r, "", 0, "--vcs", "git", "--format=json")
	r.write(t, "target-y.go", "// LINT.IfChange(Y)\nvar target = 1\n// LINT.ThenChange()\n")
	output := requireCode(t, r, "", 1, "--vcs", "git", "--format=json")
	if !strings.Contains(output, `"targetLabel": "Y"`) || strings.Contains(output, `"targetLabel": "X"`) {
		t.Fatalf("copying the previous body must not hide the edited original: %s", output)
	}
}

func TestSnapshotRenamedCustomGuardKeepsDependency(t *testing.T) {
	a, b := newRepo(t), newDefaultRepo(t)
	before := "// SENTRY.IfChange(\"API\")\none\n// SENTRY.ThenChange(\"github://acme/target/target.go#API\")\n"
	a.write(t, "source.go", before)
	b.write(t, "target.go", "// LINT.IfChange(API)\none\n// LINT.ThenChange()\n")
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	after := strings.Replace(strings.Replace(before, "IfChange(\"API\")", "IfChange(\"RENAMED\")", 1), "\none\n", "\ntwo\n", 1)
	after += "// SENTRY.IfChange(\"NEW\")\n// SENTRY.ThenChange(\"github://acme/target/target.go#API\")\n"
	a.write(t, "source.go", after)
	aHead := changeSetCommit(t, a)
	output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
	assertMatchRule(t, output, "then_label_missing")
}

// LINT.ThenChange(//internal/engine/engine.go:replaced_block, //internal/engine/guards.go:replaced_block)

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
