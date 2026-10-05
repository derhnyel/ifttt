package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// LINT.IfChange(match_contract)
func matchSection(label, body string) string {
	return "// LINT.IfChange(" + label + ")\n" + body + "\n// LINT.ThenChange()\n"
}
func assertMatchRule(t *testing.T, output, rule string) {
	t.Helper()
	var report struct {
		Errors []struct{ RuleID, TargetPath, TargetLabel string }
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if rule == "" {
		if len(report.Errors) != 0 {
			t.Fatalf("unexpected findings: %s", output)
		}
		return
	}
	for _, f := range report.Errors {
		if f.RuleID == rule {
			return
		}
	}
	t.Fatalf("missing %s: %s", rule, output)
}
func TestMatchExactAndRegex(t *testing.T) {
	for _, tc := range []struct{ name, left, right, args, rule string }{
		{"equal", "value = 1", "value = 1", "", ""},
		{"unequal", "value = 1", "value = 2", "", "match_mismatch"},
		{"whitespace", "value = 1", " value = 1", "", "match_mismatch"},
		{"empty", "", "", "", ""},
		{"crlf", "same\r\nnext", "same\nnext", "", ""},
		{"capture", "const version = \"1.2\"", "version: 1.2", `, '([0-9]+\.[0-9]+)'`, ""},
		{"whole match", "const version = \"1.2\"", "version: 1.2", `, '[0-9]+\.[0-9]+'`, ""},
		{"ordered", "1.2 then 3.4", "3.4 then 1.2", `, '([0-9]+\.[0-9]+)'`, "match_mismatch"},
		{"no left match", "none", "version: 1.2", `, '[0-9]+\.[0-9]+'`, "match_no_match"},
		{"no right match", "version: 1.2", "none", `, '[0-9]+\.[0-9]+'`, "match_no_match"},
		{"invalid regex", "one", "one", `, '['`, "match_pattern"},
		{"unsupported lookaround", "one", "one", `, '(?=one)'`, "match_pattern"},
		{"multiple captures", "1.2", "1.2", `, '([0-9]+)\.([0-9]+)'`, "match_pattern"},
		{"empty capture", "one", "one", `, '(z*)'`, "match_no_match"},
		{"escaped JSON", "version=1.2", "version=1.2", `, "([0-9]+\\.[0-9]+)"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDefaultRepo(t)
			r.write(t, "source.txt", matchSection("A", tc.left)+`// LINT.Match(":A", "//target.txt:B"`+tc.args+")\n")
			r.write(t, "target.txt", matchSection("B", tc.right))
			code := 0
			if tc.rule != "" {
				code = 1
			}
			output := requireCode(t, r, "", code, "--format=json", "source.txt")
			assertMatchRule(t, output, tc.rule)
		})
	}
}
func TestMatchUnchangedGitAndJJ(t *testing.T) {
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			r := newDefaultRepo(t)
			if kind == "jj" {
				if _, err := exec.LookPath("jj"); err != nil {
					t.Skip("jj unavailable")
				}
				r.jj(t, "git", "init", "--colocate")
			}
			r.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match(\":A\", \"//target.txt:B\")\n")
			r.write(t, "target.txt", matchSection("B", "two"))
			r.write(t, "unrelated.txt", "old\n")
			r.git(t, "add", ".")
			r.git(t, "commit", "-qm", "baseline mismatch")
			if kind == "jj" {
				r.jj(t, "new")
			}
			for _, args := range [][]string{{"--vcs", kind}, {"--scan", "."}, {"--vcs", kind, "--fix"}} {
				output := requireCode(t, r, "", 1, append(args, "--format=json")...)
				assertMatchRule(t, output, "match_mismatch")
			}
			r.write(t, "unrelated.txt", "new\n")
			output := requireCode(t, r, "", 1, "--vcs", kind, "--format=json")
			assertMatchRule(t, output, "match_mismatch")
			r.write(t, "target.txt", matchSection("B", "one"))
			assertMatchRule(t, requireCode(t, r, "", 0, "--vcs", kind, "--format=json"), "")
		})
	}
}
func TestMatchEndpointErrorsAndNoFix(t *testing.T) {
	for _, tc := range []struct{ name, target, selector, rule string }{
		{"missing label", matchSection("OTHER", "one"), "//target.txt:B", "match_label_missing"},
		{"ambiguous label", matchSection("B", "one") + matchSection("B", "one"), "//target.txt:B", "match_label_ambiguous"},
		{"incomplete label", "// LINT.IfChange(B)\none\n", "//target.txt:B", "match_target_error"},
		{"missing file", "", "//missing.txt:B", "match_target_error"},
		{"no selector", "one\n", "//target.txt", "match_invalid"},
		{"non-strict target", matchSection("B", "one"), "target.txt:B", "invalid_target_path"},
		{"outside workspace", "", "//../outside.txt:B", "match_target_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDefaultRepo(t)
			r.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match(\":A\", \""+tc.selector+"\")\n")
			r.write(t, "target.txt", tc.target)
			before, _ := os.ReadDir(r.dir)
			output := requireCode(t, r, "", 1, "--fix", "--format=json", "source.txt")
			assertMatchRule(t, output, tc.rule)
			body, _ := os.ReadFile(filepath.Join(r.dir, "target.txt"))
			if string(body) != tc.target {
				t.Fatal("Match must not modify targets")
			}
			after, _ := os.ReadDir(r.dir)
			if len(before) != len(after) {
				t.Fatal("Match must not scaffold missing files")
			}
		})
	}
	for _, args := range []string{`":A"`, `":A", "//target.txt:B", ''`, `":A", "//target.txt:B", 'x', 'y'`} {
		r := newDefaultRepo(t)
		r.write(t, ".ifttt-lint.yaml", "rules:\n  unknown_directive: ignore\n")
		// Malformed Match must fail even when unknown directives are ignored.
		r.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match("+args+")\n")
		output := requireCode(t, r, "", 1, "--format=json", "source.txt")
		assertMatchRule(t, output, "match_invalid")
	}
}
func TestMatchSnapshotIgnoresDirtyFiles(t *testing.T) {
	a, b := newDefaultRepo(t), newDefaultRepo(t)
	b.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: CUSTOM\n")
	customSection := func(body string) string { return "// CUSTOM.Label(\"B\")\n" + body + "\n// CUSTOM.EndLabel\n" }
	a.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match(\":A\", \"github://acme/target/target.txt#B\")\n")
	b.write(t, "target.txt", customSection("two"))
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	manifest := changeSetManifest(t, a, b, aBase, aBase, bBase, bBase)
	b.write(t, "target.txt", customSection("one"))
	output := requireCode(t, a, "", 1, "--change-set", manifest, "--format=json")
	assertMatchRule(t, output, "match_mismatch")
	if !strings.Contains(output, aBase) {
		t.Fatalf("snapshot identity missing: %s", output)
	}
	bHead := changeSetCommit(t, b)
	manifest = changeSetManifest(t, a, b, aBase, aBase, bBase, bHead)
	b.write(t, "target.txt", customSection("dirty mismatch"))
	assertMatchRule(t, requireCode(t, a, "", 0, "--change-set", manifest, "--format=json"), "")
}

func TestMatchEmptyReviewAndWatch(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match(\":A\", \"//target.txt:B\")\n")
	r.write(t, "target.txt", matchSection("B", "two"))
	changeSetCommit(t, r)
	r.git(t, "commit", "--allow-empty", "-qm", "NO_IFTTT=source edits are suppressed")
	assertMatchRule(t, requireCode(t, r, "", 1, "review", "--vcs=git", "--format=json", "HEAD"), "match_mismatch")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "watch", "--vcs=git", "--interval=50ms", "--format=json")
	cmd.Dir = r.dir
	cmd.Env = r.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	decoder := json.NewDecoder(stdout)
	var output json.RawMessage
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("watch must lint empty diff: %v", err)
	}
	assertMatchRule(t, string(output), "match_mismatch")
}

func TestMatchRejectsMalformedEndpoint(t *testing.T) {
	for _, suffix := range []string{"// LINT.Mistake()\n", "// LINT.Match(\":B\")\n"} {
		r := newDefaultRepo(t)
		r.write(t, ".ifttt-lint.yaml", "rules:\n  unknown_directive: error\n")
		r.write(t, "source.txt", matchSection("A", "same")+"// LINT.Match(\":A\", \"//target.txt:B\")\n")
		r.write(t, "target.txt", matchSection("B", "same")+suffix)
		output := requireCode(t, r, "", 1, "--format=json", "source.txt")
		assertMatchRule(t, output, "match_target_error")
	}
}

func TestMatchPreservesBlankLines(t *testing.T) {
	r := newDefaultRepo(t)
	r.write(t, "source.txt", "// LINT.IfChange(A)\n// LINT.ThenChange()\n// LINT.Match(\":A\", \"//target.txt:B\")\n")
	r.write(t, "target.txt", matchSection("B", ""))
	assertMatchRule(t, requireCode(t, r, "", 1, "--format=json", "source.txt"), "match_mismatch")
}
func TestMatchDiscoveryChecksRealDirectivesOnly(t *testing.T) {
	for _, body := range []string{
		"const example = `LINT.Match(\":A\", \"//target.txt:B\")`\n// LINT.IfChange(A)\none\n// LINT.ThenChange(//missing.txt)\n",
		"// ```\n// LINT.Match(\":A\", \"//target.txt:B\")\n// ```\n// LINT.IfChange(A)\none\n// LINT.ThenChange(//missing.txt)\n",
	} {
		r := newDefaultRepo(t)
		r.write(t, "source.go", body)
		changeSetCommit(t, r)
		assertMatchRule(t, requireCode(t, r, "", 0, "--vcs=git", "--format=json"), "")
	}
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "jj" {
				if _, err := exec.LookPath("jj"); err != nil {
					t.Skip("jj unavailable")
				}
			}
			r := newDefaultRepo(t)
			r.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match(\":A\", \"//target.txt:B\")\n\x00binary\n")
			r.write(t, "target.txt", matchSection("B", "two"))
			changeSetCommit(t, r)
			if kind == "jj" {
				r.jj(t, "git", "init", "--colocate")
				r.jj(t, "new")
			}
			assertMatchRule(t, requireCode(t, r, "", 1, "--vcs", kind, "--format=json"), "match_mismatch")
		})
	}
}

func TestMatchTargetUnknownPolicyAndSuppression(t *testing.T) {
	for _, policy := range []string{"warn", "ignore", "error"} {
		t.Run(policy, func(t *testing.T) {
			r := newDefaultRepo(t)
			r.write(t, ".ifttt-lint.yaml", "rules:\n  unknown_directive: "+policy+"\n")
			r.write(t, "source.txt", matchSection("A", "same")+"// LINT.Match(\":A\", \"//target.txt:B\")\n")
			r.write(t, "target.txt", matchSection("B", "same")+"// LINT.Mistake()\n")
			code := 0
			rule := ""
			if policy == "error" {
				code = 1
				rule = "match_target_error"
			} else if policy == "warn" {
				rule = "unknown_directive"
			}
			assertMatchRule(t, requireCode(t, r, "", code, "--format=json", "source.txt"), rule)
			if policy == "warn" {
				r.write(t, "target.txt", matchSection("B", "same")+"// LINT.Mistake()\n// LINT.Ignore(\"unknown_directive\")\n")
				assertMatchRule(t, requireCode(t, r, "", 0, "--format=json", "source.txt"), "")
			}
		})
	}
}

// LINT.ThenChange(//internal/engine/match.go:match_contract, //internal/parse/match.go:match_contract, //README.md:match_contract)
