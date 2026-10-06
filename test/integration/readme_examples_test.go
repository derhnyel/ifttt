package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Read the actual fenced examples so changes to the published instructions
// exercise the CLI, rather than a separately maintained copy of each fixture.
func documentedExample(t *testing.T, name string) string {
	t.Helper()
	var example string
	for _, file := range []string{"cli.md", "directives.md", "cross-repository.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "<!-- example: "+name+" -->") {
			if example != "" {
				t.Fatalf("documented example %q is duplicated", name)
			}
			example = string(data)
		}
	}
	_, rest, ok := strings.Cut(example, "<!-- example: "+name+" -->")
	if !ok {
		t.Fatalf("documented example %q is missing", name)
	}
	_, rest, ok = strings.Cut(rest, "```")
	if !ok {
		t.Fatalf("documented example %q has no fence", name)
	}
	_, rest, _ = strings.Cut(rest, "\n")
	body, _, ok := strings.Cut(rest, "\n```")
	if !ok {
		t.Fatalf("documented example %q has no closing fence", name)
	}
	return body + "\n"
}

func writeDocumentedExample(t *testing.T, r repo, path, name string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(r.dir, path)), 0700); err != nil {
		t.Fatal(err)
	}
	body := documentedExample(t, name)
	r.write(t, path, body)
	return body
}

func newExampleRepo(t *testing.T, backend string) repo {
	t.Helper()
	r := newDefaultRepo(t)
	if backend == "jj" {
		if _, err := exec.LookPath("jj"); err != nil {
			t.Skip("jj unavailable")
		}
		r.jj(t, "git", "init", "--colocate")
	}
	return r
}

func commitExample(t *testing.T, r repo, backend string) string {
	t.Helper()
	head := changeSetCommit(t, r)
	if backend == "jj" {
		r.jj(t, "new", head)
	}
	return head
}

func exampleChangeSetManifest(t *testing.T, backend string, a, b repo, aBase, aHead, bBase, bHead string) string {
	t.Helper()
	return combinationManifest(t, []map[string]string{
		{"repo": "acme/source", "path": a.dir, "vcs": backend, "base": aBase, "head": aHead},
		{"repo": "acme/target", "path": b.dir, "vcs": backend, "base": bBase, "head": bHead},
	})
}

// LINT.IfChange(readme_unlabelled)
func TestDocumentedUnlabelledLinks(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		for _, opening := range []string{"LINT.IfChange", "LINT.IfChange()"} {
			t.Run(backend+"/"+opening, func(t *testing.T) {
				r := newExampleRepo(t, backend)
				source := strings.Replace(documentedExample(t, "unlabelled-api"), "LINT.IfChange", opening, 1)
				r.write(t, "api.go", source)
				r.write(t, "guide.md", "API version: 2\n")
				commitExample(t, r, backend)
				r.write(t, "api.go", strings.Replace(source, "apiVersion = 2", "apiVersion = 3", 1))
				out := requireCode(t, r, "", 1, "--vcs", backend, "--format=json")
				if !strings.Contains(out, "then_missing") || !strings.Contains(out, "guide.md") {
					t.Fatalf("unlabelled source must require a guide edit: %s", out)
				}
				r.write(t, "guide.md", "API version: 3\n")
				requireCode(t, r, "", 0, "--vcs", backend)
			})
		}
	}
}

// LINT.ThenChange(//docs/directives.md:readme_unlabelled)

// LINT.IfChange(change_triggers)
func TestDocumentedChangeTriggers(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			source := documentedExample(t, "behavior-source")
			target := documentedExample(t, "behavior-target")
			crlfSource := strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\n", "\r\n")
			removeBody := strings.NewReplacer("const retryBudget = 3\r\n", "", "const retryBudget = 3\n", "")
			if removeBody.Replace(source) == source || removeBody.Replace(crlfSource) == crlfSource {
				t.Fatal("body-removal fixture must remove a line with either line ending")
			}
			changed := strings.Replace(source, "retryBudget = 3", "retryBudget = 5", 1)
			commentEdit := strings.Replace(source, "const retryBudget", "// Review retry failures.\nconst retryBudget", 1)
			for _, tc := range []struct {
				name, baseline, source, target, rule string
				args                                 []string
				code                                 int
			}{
				{name: "unchanged", source: source, target: target},
				{name: "body edit", source: changed, target: target, code: 1, rule: "then_label_missing"},
				{name: "body removal", source: removeBody.Replace(source), target: target, code: 1, rule: "then_label_missing"},
				{name: "CRLF body removal", baseline: crlfSource, source: removeBody.Replace(crlfSource), target: target, code: 1, rule: "then_label_missing"},
				{name: "outside block", source: source + "// Service log policy.\n", target: target},
				{name: "target list only", source: strings.Replace(source, "//ops.md:RETRIES", "//ops.md:RETRIES, //backup.md:RETRIES", 1), target: target},
				{name: "body and new target", source: strings.Replace(changed, "//ops.md:RETRIES", "//ops.md:RETRIES, //backup.md:RETRIES", 1), target: strings.Replace(target, "3 retries", "5 retries", 1), code: 1, rule: "then_label_missing"},
				{name: "label rename only", source: strings.Replace(source, "IfChange(RETRIES)", "IfChange(RENAMED)", 1), target: target},
				{name: "label rename and body", source: strings.Replace(changed, "IfChange(RETRIES)", "IfChange(RENAMED)", 1), target: target, code: 1, rule: "then_label_missing"},
				{name: "new block initial body", baseline: "const retryBudget = 3\n", source: changed, target: target},
				{name: "target edit outside label", source: changed, target: strings.Replace(target, "service log", "audit log", 1), code: 1, rule: "then_label_missing"},
				{name: "paired edits", source: changed, target: strings.Replace(target, "3 retries", "5 retries", 1)},
				{name: "target-only edit", source: source, target: strings.Replace(target, "3 retries", "5 retries", 1)},
				{name: "comment body edit", source: commentEdit, target: target, code: 1, rule: "then_label_missing"},
				{name: "code-only comment edit", source: commentEdit, target: target, args: []string{"--code-only"}},
				{name: "scan changed body", source: changed, target: target, args: []string{"--scan", "."}},
				{name: "stale target label", source: source, target: strings.Replace(target, "IfChange(RETRIES)", "IfChange(RENAMED)", 1), code: 1, rule: "label_missing"},
				{name: "invalid metadata-only target", source: strings.Replace(source, "//ops.md:RETRIES", "//missing.md:RETRIES", 1), target: target, code: 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := newExampleRepo(t, backend)
					baseline := tc.baseline
					if baseline == "" {
						baseline = source
					}
					r.write(t, "retry.go", baseline)
					r.write(t, "ops.md", target)
					r.write(t, "backup.md", target)
					commitExample(t, r, backend)
					r.write(t, "retry.go", tc.source)
					r.write(t, "ops.md", tc.target)
					args := append([]string{"--vcs", backend, "--format=json"}, tc.args...)
					out := requireCode(t, r, "", tc.code, args...)
					if tc.rule != "" {
						var report struct{ Errors []struct{ RuleID string } }
						if err := json.Unmarshal([]byte(out), &report); err != nil {
							t.Fatal(err)
						}
						found := false
						for _, finding := range report.Errors {
							found = found || finding.RuleID == tc.rule
						}
						if !found {
							t.Fatalf("expected %s: %s", tc.rule, out)
						}
					}
				})
			}
		})
	}
}

// LINT.ThenChange(//docs/directives.md:change_triggers)

// LINT.IfChange(readme_nested)
func TestDocumentedNestedLinks(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			r := newExampleRepo(t, backend)
			source := writeDocumentedExample(t, r, "api.go", "nested-api")
			guide := writeDocumentedExample(t, r, "docs/api.md", "nested-guide")
			r.write(t, "client.go", "const clientVersion = 2\n")
			commitExample(t, r, backend)
			requireCode(t, r, "", 0, "--scan", ".")
			r.write(t, "api.go", strings.Replace(source, "apiVersion = 2", "apiVersion = 3", 1))
			out := requireCode(t, r, "", 1, "--vcs", backend, "--format=json")
			if !strings.Contains(out, "VERSION") || !strings.Contains(out, "client.go") {
				t.Fatalf("nested and outer dependencies must both apply: %s", out)
			}
			r.write(t, "docs/api.md", strings.Replace(guide, "version: 2", "version: 3", 1))
			r.write(t, "client.go", "const clientVersion = 3\n")
			requireCode(t, r, "", 0, "--vcs", backend)
			local := writeDocumentedExample(t, r, "local.go", "same-file")
			commitExample(t, r, backend)
			r.write(t, "local.go", strings.Replace(local, "serverPort = 8080", "serverPort = 8081", 1))
			requireCode(t, r, "", 1, "--vcs", backend)
			r.write(t, "local.go", strings.ReplaceAll(local, "8080", "8081"))
			requireCode(t, r, "", 0, "--vcs", backend)
		})
	}
}

// LINT.ThenChange(//docs/directives.md:readme_nested)

// LINT.IfChange(readme_conditional)
func TestDocumentedConditionalRules(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			r := newExampleRepo(t, backend)
			source := writeDocumentedExample(t, r, "api.go", "conditional-api")
			for _, file := range []string{"client.go", "tests.go", "guide.md"} {
				comment := "// LINT.IfChange(API)\nversion = 2\n// LINT.ThenChange()\n"
				if file == "guide.md" {
					comment = "<!-- LINT.IfChange(API) -->\nversion: 2\n<!-- LINT.ThenChange() -->\n"
				}
				r.write(t, file, comment)
			}
			for _, file := range []string{"migration-a.sql", "migration-b.sql", "generated.go"} {
				r.write(t, file, "version = 2\n")
			}
			commitExample(t, r, backend)
			r.write(t, "api.go", strings.Replace(source, "apiVersion = 2", "apiVersion = 3", 1))
			out := requireCode(t, r, "", 1, "--vcs", backend, "--format=json")
			if !strings.Contains(out, "require_any") || !strings.Contains(out, "require_all") || !strings.Contains(out, "then_label_missing") {
				t.Fatalf("conditional requirements were not checked: %s", out)
			}
			for _, file := range []string{"client.go", "tests.go", "guide.md", "migration-a.sql"} {
				data, err := os.ReadFile(filepath.Join(r.dir, file))
				if err != nil {
					t.Fatal(err)
				}
				r.write(t, file, strings.ReplaceAll(string(data), "2", "3"))
			}
			requireCode(t, r, "", 0, "--vcs", backend)
			r.write(t, "generated.go", "version = 3\n")
			out = requireCode(t, r, "", 1, "--vcs", backend, "--format=json")
			if !strings.Contains(out, "forbid") {
				t.Fatalf("forbidden edit was not reported: %s", out)
			}
		})
	}
}

// LINT.ThenChange(//docs/directives.md:readme_conditional)

// LINT.IfChange(readme_matching)
func TestDocumentedMatching(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			r := newExampleRepo(t, backend)
			writeDocumentedExample(t, r, "message.txt", "match-message")
			copy := writeDocumentedExample(t, r, "copy.txt", "match-copy")
			writeDocumentedExample(t, r, "version.go", "match-version")
			version := writeDocumentedExample(t, r, "package.yaml", "match-package")
			commitExample(t, r, backend)
			requireCode(t, r, "", 0, "--vcs", backend)
			r.write(t, "copy.txt", strings.Replace(copy, "Try again.", "Try later.", 1))
			assertMatchRule(t, requireCode(t, r, "", 1, "--vcs", backend, "--format=json"), "match_mismatch")
			r.write(t, "copy.txt", copy)
			r.write(t, "package.yaml", strings.Replace(version, "1.2.3", "1.2.4", 1))
			assertMatchRule(t, requireCode(t, r, "", 1, "--vcs", backend, "--format=json"), "match_mismatch")
		})
	}
}

// LINT.ThenChange(//docs/directives.md:readme_matching)

// LINT.IfChange(readme_suppression)
func TestDocumentedSuppression(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			r := newExampleRepo(t, backend)
			source := writeDocumentedExample(t, r, "api.go", "suppression-api")
			r.write(t, "guide.md", "API guide\n")
			commitExample(t, r, backend)
			r.write(t, "api.go", strings.ReplaceAll(source, "= 2", "= 3"))
			out := requireCode(t, r, "", 1, "--vcs", backend, "--format=json")
			var report struct {
				Errors, Suppressed []struct{ RuleID string }
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Errors) != 1 || len(report.Suppressed) != 1 || report.Errors[0].RuleID != "then_missing" {
				t.Fatalf("Enable must restore the rule after the experimental block: %s", out)
			}
			r.write(t, "api.go", "// LINT.Ignore(\"then_missing\")\n"+strings.ReplaceAll(source, "= 2", "= 3"))
			requireCode(t, r, "", 0, "--vcs", backend)
		})
	}
}

// LINT.ThenChange(//docs/directives.md:readme_suppression)

// LINT.IfChange(readme_configuration)
func TestDocumentedConfiguration(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			r := newExampleRepo(t, backend)
			writeDocumentedExample(t, r, ".ifttt-lint.yaml", "configuration")
			source := writeDocumentedExample(t, r, "api.go", "same-file")
			commitExample(t, r, backend)
			requireCode(t, r, "", 0, "--scan", ".")
			r.write(t, "api.go", strings.Replace(source, "serverPort = 8080", "serverPort = 8081", 1))
			requireCode(t, r, "", 1, "--vcs", backend)
			r.write(t, "api.go", strings.ReplaceAll(source, "8080", "8081"))
			requireCode(t, r, "", 0, "--vcs", backend, "--format=json")

			// Check the documented custom grammar with real labelled targets.
			r.write(t, ".ifttt-lint.yaml", "directives: {prefix: SPEC}\n")
			custom := writeDocumentedExample(t, r, "custom.go", "custom-prefix")
			for _, file := range []string{"guide.md", "tests.go"} {
				body := "// SPEC.Label(\"API\")\nversion = 2\n// SPEC.EndLabel\n"
				if file == "guide.md" {
					body = "<!-- SPEC.Label(\"API\") -->\nversion: 2\n<!-- SPEC.EndLabel -->\n"
				}
				r.write(t, file, body)
			}
			commitExample(t, r, backend)
			requireCode(t, r, "", 0, "--vcs", backend, "custom.go")
			r.write(t, "custom.go", strings.Replace(custom, "version = 2", "version = 3", 1))
			out := requireCode(t, r, "", 1, "--vcs", backend, "--format=json")
			if !strings.Contains(out, "guide.md") || !strings.Contains(out, "tests.go") {
				t.Fatalf("custom-prefix target list was not checked: %s", out)
			}

			// A child config disables inherited code-only behavior and adds a local ignore.
			r = newExampleRepo(t, backend)
			parent := strings.Replace(documentedExample(t, "configuration"), "code_only: false", "code_only: true", 1)
			r.write(t, ".ifttt-lint.yaml", parent)
			writeDocumentedExample(t, r, "examples/.ifttt-lint.yaml", "child-configuration")
			local := writeDocumentedExample(t, r, "examples/local.go", "same-file")
			if err := os.MkdirAll(filepath.Join(r.dir, "examples", "generated"), 0700); err != nil {
				t.Fatal(err)
			}
			r.write(t, "examples/generated/broken.go", "// LINT.IfChange(BROKEN)\n")
			commitExample(t, r, backend)
			child := repo{dir: filepath.Join(r.dir, "examples"), env: r.env}
			requireCode(t, child, "", 0, "--scan", ".")
			requireCode(t, child, "", 0, "--doctor")
			requireCode(t, child, "", 0, "--scan", ".", "--ignore", "generated/**")
			r.write(t, "examples/local.go", strings.Replace(local, "const serverPort", "// Server setting.\nconst serverPort", 1))
			requireCode(t, child, "", 1, "--vcs", backend, "--format=json")
			r.write(t, "examples/generated/broken.go", "// Ordinary generated content.\n")
			for _, pattern := range []string{"**/*.go", "*.go", "**/local.go"} {
				t.Run(pattern, func(t *testing.T) {
					r.write(t, ".ifttt-lint.yaml", "ignores: ['"+pattern+"']\n")
					r.write(t, "examples/.ifttt-lint.yaml", "{}\n")
					r.write(t, "examples/local.go", "// LINT.IfChange(BROKEN)\n")
					requireCode(t, child, "", 0, "--scan", ".")
					requireCode(t, child, "", 0, "--doctor")
				})
			}
		})
	}
}

// LINT.ThenChange(//docs/cli.md:readme_configuration, //cmd/ifttt/main.go:readme_configuration)

// LINT.IfChange(readme_crossrepo)
func TestDocumentedCrossRepositoryCombination(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "jj" {
				if _, err := exec.LookPath("jj"); err != nil {
					t.Skip("jj unavailable")
				}
			}
			// Use the documented sibling layout and run from outside both repositories.
			workspace := t.TempDir()
			a, b := newDefaultRepo(t), newDefaultRepo(t)
			for name, r := range map[string]*repo{"api": &a, "contracts": &b} {
				dir := filepath.Join(workspace, name)
				if err := os.Rename(r.dir, dir); err != nil {
					t.Fatal(err)
				}
				r.dir = dir
				if backend == "jj" {
					r.jj(t, "git", "init", "--colocate")
				}
			}
			source := writeDocumentedExample(t, a, "source.go", "crossrepo-api")
			target := writeDocumentedExample(t, b, "contract.go", "crossrepo-contract")
			writeDocumentedExample(t, b, ".ifttt-lint.yaml", "crossrepo-config")
			aBase, bBase := commitExample(t, a, backend), commitExample(t, b, backend)
			manifest := strings.ReplaceAll(documentedExample(t, "crossrepo-manifest"), "vcs: git", "vcs: "+backend)
			run := func(aHead, bHead string, code int) string {
				t.Helper()
				body := strings.NewReplacer("<api-base-commit>", aBase, "<api-head-commit>", aHead,
					"<contracts-base-commit>", bBase, "<contracts-head-commit>", bHead).Replace(manifest)
				path := filepath.Join(workspace, ".ifttt-changes.yaml")
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				return requireCode(t, repo{dir: workspace, env: a.env}, "", code, "--change-set", ".ifttt-changes.yaml", "--format=json")
			}
			run(aBase, bBase, 0)
			a.write(t, "source.go", strings.Replace(source, "apiVersion = 2", "apiVersion = 3", 1))
			aHead := commitExample(t, a, backend)
			assertMatchRule(t, run(aHead, bBase, 1), "match_mismatch")
			b.write(t, "contract.go", strings.Replace(target, "contractVersion = 2", "contractVersion = 3", 1))
			run(aHead, bBase, 1) // A dirty target cannot satisfy the committed comparison.
			bHead := commitExample(t, b, backend)
			run(aHead, bHead, 0)
			// Reverse conditional links also require the source repository to change.
			b.write(t, "contract.go", strings.Replace(target, "contractVersion = 2", "contractVersion = 4", 1))
			bHead = commitExample(t, b, backend)
			out := run(aBase, bHead, 1)
			if !strings.Contains(out, "require_all_missing") {
				t.Fatalf("reverse cross-repository dependency was not checked: %s", out)
			}
		})
	}
}

// LINT.ThenChange(//docs/cross-repository.md:readme_crossrepo)
