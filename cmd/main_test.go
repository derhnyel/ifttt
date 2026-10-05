package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/comments"
	"github.com/derhnyel/ifttt/internal/config"
	eng "github.com/derhnyel/ifttt/internal/engine"
	"github.com/derhnyel/ifttt/internal/vcs"
)

func TestDoctorScaffoldPreservesFilesystemOctalLikeNames(t *testing.T) {
	previous := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	t.Cleanup(func() { core.SetDirectivePrefix(previous) })
	root := filepath.Join(t.TempDir(), `numeric\001`)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("// LINT.IfChange(SRC)\nbody\n// LINT.ThenChange(//target.go:API)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runDoctor(root, nil, true); err != nil {
		t.Fatal(err)
	}
	ranges, err := eng.LabelRanges(filepath.Join(root, "target.go"))
	if err != nil || ranges["API"].StartLine == 0 {
		t.Fatalf("doctor changed a filesystem path into an escape: %+v, %v", ranges, err)
	}
}

func TestDeriveLabel(t *testing.T) {
	cases := map[string]string{
		"foo/bar.go":     "BAR",
		"baz-qux.ts":     "BAZ_QUX",
		"../a/b.c":       "B",
		"":               "LABEL",
		"file.with.dots": "FILE_WITH",
	}
	for path, want := range cases {
		if got := deriveLabel(path); got != want {
			t.Fatalf("deriveLabel(%q)=%q want %q", path, got, want)
		}
	}
}

func TestApplyCommentStyleOverrides(t *testing.T) {
	resetCommentPrefixOverrides()
	t.Cleanup(resetCommentPrefixOverrides)
	if err := applyCommentStyleOverrides([]string{".tmpl=##"}); err != nil {
		t.Fatalf("applyCommentStyleOverrides: %v", err)
	}
	if got := comments.FormatComment("view.tmpl", "LINT.IfChange(API)"); got != "## LINT.IfChange(API)" {
		t.Fatalf("comment override was not applied to generated directives: %q", got)
	}
	// invalid forms should error
	if err := applyCommentStyleOverrides([]string{"badformat"}); err == nil {
		t.Fatalf("expected error for invalid override")
	}
}

func TestParseRemoteFlagValue_Shorthand(t *testing.T) {
	remote, err := parseRemoteFlagValue("acme/repo@stable")
	if err != nil {
		t.Fatalf("parseRemoteFlagValue: %v", err)
	}
	if remote.Repo != "acme/repo" || remote.DefaultRef != "stable" || remote.TokenEnv != "" {
		t.Fatalf("unexpected remote parsed: %+v", remote)
	}
}

func TestParseRemoteFlagValue_KeyValues(t *testing.T) {
	t.Setenv("CUSTOM_TOKEN", "secret") // ensure env set for future steps
	raw := "type=github,repo=org/service,default_ref=prod,token_env=CUSTOM_TOKEN,base_url=https://gh.example/api/"
	remote, err := parseRemoteFlagValue(raw)
	if err != nil {
		t.Fatalf("parseRemoteFlagValue: %v", err)
	}
	if remote.Repo != "org/service" || remote.DefaultRef != "prod" || remote.TokenEnv != "CUSTOM_TOKEN" || remote.BaseURL != "https://gh.example/api/" {
		t.Fatalf("unexpected remote parsed: %+v", remote)
	}
}

func TestRunScaffoldSourceOnly(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := runScaffold([]string{
			"--source", "src.go",
			"--target", "target.go#LBL",
			"--source-only",
		}); err != nil {
			t.Fatalf("runScaffold: %v", err)
		}
	})
	sourceData, err := os.ReadFile(filepath.Join(dir, "src.go"))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if !strings.Contains(string(sourceData), "// LINT.IfChange(LBL)") || !strings.Contains(string(sourceData), "// LINT.ThenChange(//target.go:LBL)") {
		t.Fatalf("expected scaffolded directives in source:\n%s", sourceData)
	}
	if _, err := os.Stat(filepath.Join(dir, "target.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target.go should not exist when source-only, err=%v", err)
	}
}

func TestRunScaffoldTargetOnly(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := runScaffold([]string{
			"--source", "src.go",
			"--target", "target.go#LBL",
			"--target-only",
		}); err != nil {
			t.Fatalf("runScaffold: %v", err)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "src.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source.go should not exist when target-only, err=%v", err)
	}
	targetData, err := os.ReadFile(filepath.Join(dir, "target.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(targetData), "// LINT.IfChange(LBL)") || !strings.Contains(string(targetData), "// LINT.ThenChange()") {
		t.Fatalf("expected scaffolded directives in target:\n%s", targetData)
	}
}

func TestRunJumpOutputsHintForMissingLabel(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	target := filepath.Join(dir, "target.go")
	content := strings.Join([]string{
		"package main",
		"// LINT.IfChange(EXISTING)",
		"// LINT.ThenChange()",
	}, "\n")
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	err := runJump([]string{target + "#MISSING"})
	if err == nil || !strings.Contains(err.Error(), "iflint scaffold") {
		t.Fatalf("expected scaffold hint, got %v", err)
	}
}

func TestRunIgnoreAddAppendsDirective(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	file := filepath.Join(dir, "main.go")
	if err := runIgnoreAdd([]string{"--file", file, "--rule", "all"}); err != nil {
		t.Fatalf("runIgnoreAdd: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `// LINT.Ignore("all")`) {
		t.Fatalf("expected ignore directive, got:\n%s", data)
	}
}

func TestRunIgnoreAddInsertsAtLine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runIgnoreAdd([]string{"--file", file, "--rule", "then_missing", "--line", "2"}); err != nil {
		t.Fatalf("runIgnoreAdd: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %v", lines)
	}
	if !strings.Contains(lines[1], `LINT.Ignore("then_missing")`) {
		t.Fatalf("expected directive inserted before line 2, got %v", lines)
	}
}

func TestCommitDiffIncludesChanges(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, repo, "tracked.txt", "hello\n")
	gitCommit(t, repo, "tracked.txt", "initial commit")
	writeFile(t, repo, "tracked.txt", "hello world\n")
	gitCommit(t, repo, "tracked.txt", "second commit")
	withWorkingDir(t, repo, func() {
		diff, err := nativeCommitDiff("HEAD", "")
		if err != nil {
			t.Fatalf("commitDiff: %v", err)
		}
		if !strings.Contains(diff, "hello world") {
			t.Fatalf("diff missing new content: %s", diff)
		}
		diffRange, err := nativeCommitDiff("HEAD~1..HEAD", "")
		if err != nil {
			t.Fatalf("commitDiff range: %v", err)
		}
		if diffRange == "" {
			t.Fatalf("expected diff for explicit range")
		}
	})
}

func TestRunBlameReportsUnlabeled(t *testing.T) {
	previous := core.CurrentDirectiveSyntax().Prefix
	t.Cleanup(func() { core.SetDirectivePrefix(previous) })
	repo := initGitRepo(t)
	// This fixture intentionally exercises the legacy bare IfChange syntax.
	writeFile(t, repo, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\n")
	writeFile(t, repo, "main.go", strings.Join([]string{
		"package main",
		"// SENTRY.IfChange",
		"func main() {}",
	}, "\n"))
	gitCommit(t, repo, "main.go", "add unlabeled")
	withWorkingDir(t, repo, func() {
		output := captureStdout(t, func() {
			if err := runBlame([]string{"main.go"}); err != nil {
				t.Fatalf("runBlame: %v", err)
			}
		})
		if !strings.Contains(output, "unlabeled IfChange") {
			t.Fatalf("expected output to mention unlabeled directive, got: %s", output)
		}
	})
}

func BenchmarkLintAndReportLargeDiff(b *testing.B) {
	previous := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("SENTRY")
	b.Cleanup(func() { core.SetDirectivePrefix(previous) })
	dir := b.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		b.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		b.Fatalf("chdir: %v", err)
	}
	b.Cleanup(func() { _ = os.Chdir(oldwd) })

	const files = 100
	var diffLines []string
	for i := 0; i < files; i++ {
		src := fmt.Sprintf("src_%03d.go", i)
		tgt := fmt.Sprintf("target_%03d.go", i)
		label := fmt.Sprintf("LBL%d", i)
		sourceContent := strings.Join([]string{
			"package main",
			fmt.Sprintf("// SENTRY.IfChange(\"%s\")", label),
			"value := \"old\"",
			fmt.Sprintf("// SENTRY.ThenChange(\"%s#%s\")", tgt, label),
		}, "\n") + "\n"
		targetContent := strings.Join([]string{
			"package main",
			fmt.Sprintf("// SENTRY.Label(\"%s\")", label),
			"value := \"before\"",
			"// SENTRY.EndLabel",
		}, "\n") + "\n"
		writeFile(b, dir, src, sourceContent)
		writeFile(b, dir, tgt, targetContent)
		diffLines = append(diffLines,
			fmt.Sprintf("diff --git a/%s b/%s", src, src),
			fmt.Sprintf("--- a/%s", src),
			fmt.Sprintf("+++ b/%s", src),
			"@@ -2,2 +2,2 @@",
			fmt.Sprintf(" // SENTRY.IfChange(\"%s\")", label),
			"-value := \"old\"",
			"+value := \"new\"",
			fmt.Sprintf(" // SENTRY.ThenChange(\"%s#%s\")", tgt, label),
			"",
			fmt.Sprintf("diff --git a/%s b/%s", tgt, tgt),
			fmt.Sprintf("--- a/%s", tgt),
			fmt.Sprintf("+++ b/%s", tgt),
			"@@ -2,2 +2,2 @@",
			fmt.Sprintf(" // SENTRY.Label(\"%s\")", label),
			"-value := \"before\"",
			"+value := \"after\"",
			" // SENTRY.EndLabel",
			"",
		)
	}
	diff := strings.Join(diffLines, "\n")
	opts := eng.Options{Parallelism: -1}
	writer := noopWriter{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, _ := lintAndReport(diff, opts)
		if err := writer.Write(res.Findings, res.Suppressed); err != nil {
			b.Fatalf("lintAndReport: %v", err)
		}
	}
}

type noopWriter struct{}

func (noopWriter) Write(a, b []core.Finding) error { return nil }

func initGitRepo(tb testing.TB) string {
	dir := tb.TempDir()
	gitCmd(tb, dir, "init")
	gitCmd(tb, dir, "config", "user.name", "Test User")
	gitCmd(tb, dir, "config", "user.email", "test@example.com")
	return dir
}

func gitCmd(tb testing.TB, dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v failed: %v (%s)", args, err, out)
	}
	return string(out)
}

func gitCommit(tb testing.TB, dir, file, message string) {
	gitCmd(tb, dir, "add", file)
	gitCmd(tb, dir, "commit", "-m", message)
}

func writeFile(tb testing.TB, dir, name, content string) {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tb.Fatalf("write %s: %v", name, err)
	}
}

func captureStdout(tb testing.TB, fn func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		tb.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	buf, err := io.ReadAll(r)
	if err != nil {
		tb.Fatalf("read: %v", err)
	}
	return string(buf)
}

func withWorkingDir(tb testing.TB, dir string, fn func()) {
	old, err := os.Getwd()
	if err != nil {
		tb.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		tb.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(old) }()
	fn()
}

func TestWatchRejectsNonpositiveInterval(t *testing.T) {
	for _, interval := range []string{"0s", "-1s"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("interval %s panicked: %v", interval, r)
				}
			}()
			if err := runWatch([]string{"--interval", interval}); err == nil {
				t.Errorf("accepted interval %s", interval)
			}
		}()
	}
}

func TestReviewRejectsMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(".ifttt-lint.yaml", []byte("output: ["), 0644); err != nil {
		t.Fatal(err)
	}
	err := runReview(nil)
	if err == nil || !strings.Contains(err.Error(), ".ifttt-lint.yaml") {
		t.Fatalf("config error discarded: %v", err)
	}
}

func TestBlameLineUsesValidArguments(t *testing.T) {
	dir := initGitRepo(t)
	writeFile(t, dir, "source.go", "package main\n")
	gitCommit(t, dir, "source.go", "initial")
	t.Chdir(dir)
	if _, err := gitBlameLine("source.go", 1); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorFixRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	_, err := ensureLabelScaffold(root, core.TargetRef{Path: outside, Label: "LBL"}, core.CurrentDirectiveSyntax())
	if err == nil {
		t.Fatal("doctor fix wrote outside scan root")
	}
}

func TestOptionsHonorConfiguredCodeOnly(t *testing.T) {
	var cfg config.Config
	cfg.Rules.CodeOnly = true
	if !optionsFromConfig(cfg, nil).CodeOnly {
		t.Fatal("configured code_only ignored")
	}
}

func TestOptionsHonorConfiguredParallelism(t *testing.T) {
	var cfg config.Config
	cfg.Parallelism = "3"
	if got := optionsFromConfig(cfg, nil).Parallelism; got != 3 {
		t.Fatalf("configured parallelism ignored: got %d", got)
	}
	cfg.Parallelism = "auto"
	if got := optionsFromConfig(cfg, nil).Parallelism; got != -1 {
		t.Fatalf("auto parallelism: got %d", got)
	}
}

func TestGitDiffCommandsRejectOptionLikeRevisions(t *testing.T) {
	for _, request := range []vcs.Request{{Revision: "--output=unwanted"}, {Revision: "HEAD", Base: "--output=unwanted"}} {
		backend := vcs.Backend{Kind: "git", Root: "."}
		if _, err := backend.Diff(context.Background(), request); err == nil {
			t.Fatal("option-like revision accepted")
		}
	}
}
func nativeCommitDiff(rev, base string) (string, error) {
	backend, err := vcs.Open(context.Background(), ".", "git")
	if err != nil {
		return "", err
	}
	return backend.Diff(context.Background(), vcs.Request{Revision: rev, Base: base, Review: true})
}

func TestStructuralInputSelection(t *testing.T) {
	selected, patch, err := classifyInputs([]string{"source.go", "*.py"}, nil, false)
	if err != nil || patch != "" || len(selected) != 2 {
		t.Fatalf("source selection: %v %q %v", selected, patch, err)
	}
	_, patch, err = classifyInputs([]string{"input.diff"}, nil, false)
	if err != nil || patch != "input.diff" {
		t.Fatalf("patch selection: %q %v", patch, err)
	}
	if _, _, err = classifyInputs([]string{"input.diff", "source.go"}, nil, false); err == nil {
		t.Fatal("mixed inputs accepted")
	}
	_, _, err = classifyInputs([]string{"-"}, []string{"source.go"}, false)
	if err == nil {
		t.Fatal("explicit source plus stdin accepted")
	}
}
