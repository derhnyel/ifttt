package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binary string
var integrationCoverDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ifttt-integration-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "ifttt")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	args := []string{"build", "-o", binary}
	if raw := os.Getenv("IFTTT_INTEGRATION_COVER_DIR"); raw != "" {
		integrationCoverDir, err = filepath.Abs(raw)
		if err != nil {
			panic(err)
		}
		if err := os.MkdirAll(integrationCoverDir, 0700); err != nil {
			panic(err)
		}
		args = append(args, "-cover", "-covermode=atomic", "-coverpkg=github.com/derhnyel/ifttt/...")
	}
	args = append(args, "./cmd/ifttt")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build CLI: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type repo struct {
	dir string
	env []string
}

// newRepo explicitly selects the legacy grammar used by the quoted-label fixtures.
// Default syntax tests use newDefaultRepo without a prefix configuration.
func newRepo(t *testing.T) repo {
	t.Helper()
	r := newDefaultRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\n")
	return r
}

func newDefaultRepo(t *testing.T) repo {
	t.Helper()
	r := repo{dir: t.TempDir()}
	isolated := t.TempDir()
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") && !strings.HasPrefix(v, "IFTTT_CACHE_DIR=") && !strings.HasPrefix(v, "XDG_CONFIG_HOME=") && !strings.HasPrefix(v, "GOCOVERDIR=") {
			r.env = append(r.env, v)
		}
	}
	if integrationCoverDir != "" {
		r.env = append(r.env, "GOCOVERDIR="+integrationCoverDir)
	}
	r.env = append(r.env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(isolated, "gitconfig"), "IFTTT_CACHE_DIR="+filepath.Join(isolated, "cache"), "XDG_CONFIG_HOME="+isolated)
	r.git(t, "init", "-q")
	r.git(t, "config", "user.name", "Integration Test")
	r.git(t, "config", "user.email", "integration@example.invalid")
	return r
}
func (r repo) git(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
func (r repo) write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func (r repo) run(t *testing.T, input string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	cmd.Stdin = strings.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	return out.String(), stderr.String(), code
}
func source(target, value string) string {
	return "// SENTRY.IfChange(\"shared label\")\nvar value = \"" + value + "\"\n// SENTRY.ThenChange(\"" + target + "#shared label\")\n"
}

// Chromium contains text fixtures whose non-UTF-8 bytes occur beyond an
// initial binary probe. Contracts still use ASCII directives and line ranges.
func TestNativeNonUTF8HTMLContractsBeyondProbeWindow(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
	body := strings.Repeat("ordinary content ", 600) + string([]byte{0xa0})
	html := func(then, value string) string {
		return "<!-- LINT.IfChange(API) -->\n" + body + value + "\n<!-- LINT.ThenChange(" + then + ") -->\n"
	}
	r.write(t, "source.html", html("//target.html:API", "old"))
	r.write(t, "target.html", html("", "old"))
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "non-UTF-8 baseline")
	r.write(t, "source.html", html("//target.html:API", "new"))
	requireCode(t, r, "", 0, "--vcs=git", "--strict=true", "--", "source.html")
	out := requireCode(t, r, "", 1, "--vcs=git", "--diff=HEAD", "--format=json")
	if !strings.Contains(out, `"ruleId": "then_label_missing"`) {
		t.Fatalf("non-UTF-8 source lost its co-change contract: %s", out)
	}
	r.write(t, "target.html", html("", "new"))
	requireCode(t, r, "", 0, "--vcs=git", "--diff=HEAD", "--format=json")
}
func target(value string) string {
	return "// SENTRY.Label(\"shared label\")\nvar target = \"" + value + "\"\n// SENTRY.EndLabel\n"
}
func fixture(t *testing.T) repo {
	t.Helper()
	r := newRepo(t)
	r.write(t, "source file.go", source("target file.go", "old"))
	r.write(t, "target file.go", target("old"))
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	return r
}
func requireCode(t *testing.T, r repo, input string, want int, args ...string) string {
	t.Helper()
	out, stderr, code := r.run(t, input, args...)
	if code != want {
		t.Fatalf("args %v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, stderr)
	}
	return out
}

func TestCoChangeAndOutputContracts(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	diff := r.git(t, "diff")
	out := requireCode(t, r, diff, 1, "--format=json", "-")
	var payload struct {
		Errors []struct {
			RuleID   string `json:"ruleId"`
			File     string `json:"file"`
			Line     int    `json:"line"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 1 || payload.Errors[0].RuleID != "then_label_missing" || payload.Errors[0].File != "source file.go" || payload.Errors[0].Line < 1 || payload.Errors[0].Severity != "error" || !strings.Contains(payload.Errors[0].Message, "target file.go") {
		t.Fatalf("unexpected JSON contract: %s", out)
	}
	r.write(t, "input.diff", diff)
	requireCode(t, r, "", 1, "--format=json", "input.diff")
	var sarif struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
			} `json:"results"`
		} `json:"runs"`
	}
	out = requireCode(t, r, diff, 1, "--format=sarif", "-")
	if err := json.Unmarshal([]byte(out), &sarif); err != nil {
		t.Fatal(err)
	}
	if sarif.Version != "2.1.0" || len(sarif.Runs) != 1 || len(sarif.Runs[0].Results) != 1 || sarif.Runs[0].Results[0].RuleID != "then_label_missing" {
		t.Fatalf("invalid SARIF: %s", out)
	}
	var dls struct {
		Items []struct {
			File        string `json:"file"`
			Diagnostics []struct {
				Code     string `json:"code"`
				Severity int    `json:"severity"`
				Range    struct {
					Start struct {
						Line int `json:"line"`
					} `json:"start"`
				} `json:"range"`
			} `json:"diagnostics"`
		} `json:"items"`
	}
	out = requireCode(t, r, diff, 1, "--format=dls", "-")
	if err := json.Unmarshal([]byte(out), &dls); err != nil {
		t.Fatal(err)
	}
	if len(dls.Items) != 1 || dls.Items[0].File != "source file.go" || len(dls.Items[0].Diagnostics) != 1 || dls.Items[0].Diagnostics[0].Code != "then_label_missing" || dls.Items[0].Diagnostics[0].Severity != 1 || dls.Items[0].Diagnostics[0].Range.Start.Line != 2 {
		t.Fatalf("invalid DLS contract: %s", out)
	}
	requireCode(t, r, diff, 0, "-w", "--format=json", "-")
	r.write(t, "target file.go", target("new"))
	out = requireCode(t, r, r.git(t, "diff"), 0, "--format=json", "-")
	if !strings.Contains(out, `"errors": []`) {
		t.Fatalf("clean JSON: %s", out)
	}
}
func TestMalformedConfigurationAndInputs(t *testing.T) {
	for _, tc := range []struct {
		name, input, config string
		args                []string
	}{{"malformed diff", "not a diff\n", "", []string{"-"}}, {"invalid output", "", "", []string{"--format=invalid", "-"}}, {"invalid YAML", "", "rules: [\n", []string{"-"}}, {"missing diff file", "", "", []string{"missing.diff"}}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			if tc.config != "" {
				r.write(t, ".ifttt-lint.yaml", tc.config)
			}
			requireCode(t, r, tc.input, 2, tc.args...)
		})
	}
}
func TestCodeOnlyAndMetadataChanges(t *testing.T) {
	t.Run("comment only", func(t *testing.T) {
		r := fixture(t)
		r.write(t, "source file.go", strings.Replace(source("target file.go", "old"), "var value", "// explanatory comment\nvar value", 1))
		requireCode(t, r, r.git(t, "diff"), 0, "--code-only", "-")
	})
	t.Run("new directive in existing file", func(t *testing.T) {
		r := newRepo(t)
		r.write(t, "source.go", "var value = \"old\"\n")
		r.write(t, "target file.go", target("old"))
		r.git(t, "add", ".")
		r.git(t, "commit", "-qm", "baseline")
		r.write(t, "source.go", source("target file.go", "old"))
		requireCode(t, r, r.git(t, "diff"), 0, "-")
	})
	t.Run("metadata only", func(t *testing.T) {
		r := fixture(t)
		r.write(t, "source file.go", strings.Replace(source("target file.go", "old"), "IfChange(\"shared label\")", "IfChange(\"renamed label\")", 1))
		requireCode(t, r, r.git(t, "diff"), 0, "-")
	})
}

func TestExplicitFalseConfigurationFlags(t *testing.T) {
	r := fixture(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\nverbose: true\nrules:\n  code_only: true\n")
	r.write(t, "source file.go", strings.Replace(source("target file.go", "old"), "var value", "// explanatory comment\nvar value", 1))
	diff := r.git(t, "diff")
	for _, tc := range []struct {
		name  string
		args  []string
		code  int
		debug bool
	}{
		{"inherited", nil, 0, true},
		{"explicit false", []string{"--code-only=false", "--v=false"}, 1, false},
		{"verbose alias false", []string{"--code-only=false", "--verbose=false"}, 1, false},
		{"explicit true", []string{"--code-only=true", "--v=true"}, 0, true},
		{"verbose alias true", []string{"--code-only=true", "--verbose"}, 0, true},
		{"warn alias", []string{"--code-only=false", "--warn"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, tc.args...), "--format=json", "-")
			out, stderr, code := r.run(t, diff, args...)
			if code != tc.code || strings.Contains(stderr, "level=DEBUG") != tc.debug {
				t.Fatalf("exit=%d debug=%v; want exit=%d debug=%v\nstdout: %s\nstderr: %s", code, strings.Contains(stderr, "level=DEBUG"), tc.code, tc.debug, out, stderr)
			}
		})
	}
}

func TestGoogleProseAndParenthesizedTargets(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\nrules:\n  unknown_directive: error\n")
	src := "// LINT.IfChange(example) is described in docs\n// LINT.ThenChange(//example(1).go) describes an example\n// LINT.IfChange(source)\nvar value = 1\n// LINT.ThenChange(\n// //target(1).go:mirror,\n// )\n"
	dst := "// LINT.IfChange(mirror)\nvar mirror = 1\n// LINT.ThenChange()\n"
	r.write(t, "source.go", src)
	r.write(t, "target(1).go", dst)
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	requireCode(t, r, "", 0, "--strict=true", "--format=json", "source.go")
	r.write(t, "source.go", strings.Replace(src, "value = 1", "value = 2", 1))
	out := requireCode(t, r, "", 1, "--vcs=git", "--strict=true", "--format=json")
	if strings.Contains(out, "unknown_directive") || !strings.Contains(out, "then_label_missing") {
		t.Fatalf("expected only real contract finding: %s", out)
	}
	r.write(t, "target(1).go", strings.Replace(dst, "mirror = 1", "mirror = 2", 1))
	requireCode(t, r, "", 0, "--vcs=git", "--strict=true", "--format=json")
}

func TestAuxiliaryCommandsUseDirectiveConfiguration(t *testing.T) {
	t.Run("configured jump", func(t *testing.T) {
		r := newRepo(t)
		r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
		r.write(t, "target.go", "// LINT.IfChange(mirror)\nvar mirror = 1\n// LINT.ThenChange()\n")
		r.env = append(r.env, "PATH=", "EDITOR=")
		requireCode(t, r, "", 0, "jump", "target.go#mirror")
	})
	t.Run("valid Google scaffold", func(t *testing.T) {
		r := newRepo(t)
		r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\nrules:\n  unknown_directive: error\n")
		requireCode(t, r, "", 0, "scaffold", "--source", "source.go", "--target", "target.go#mirror", "--label", "mirror")
		requireCode(t, r, "", 0, "--strict=true", "--format=json", "source.go", "target.go")
	})
	t.Run("configured ignore and blame", func(t *testing.T) {
		r := newRepo(t)
		r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: CUSTOM\n")
		r.write(t, "source.go", "// CUSTOM.IfChange\nvar value = 1\n// CUSTOM.ThenChange()\n")
		r.git(t, "add", ".")
		r.git(t, "commit", "-qm", "baseline")
		out := requireCode(t, r, "", 0, "blame", "source.go")
		if !strings.Contains(out, "unlabeled IfChange directives") {
			t.Fatalf("configured directives missed: %s", out)
		}
		requireCode(t, r, "", 0, "ignore", "add", "--file", "source.go", "--rule", "all")
		data, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
		if err != nil || !bytes.Contains(data, []byte("CUSTOM.Ignore(\"all\")")) {
			t.Fatalf("ignore prefix: %s, %v", data, err)
		}
	})
	for _, args := range [][]string{
		{"jump", "source.go#mirror"},
		{"scaffold", "--source", "source.go", "--target", "target.go#mirror"},
		{"ignore", "add", "--file", "source.go"},
		{"blame", "source.go"},
	} {
		t.Run("malformed config "+args[0], func(t *testing.T) {
			r := newRepo(t)
			r.write(t, ".ifttt-lint.yaml", "directives: [\n")
			r.write(t, "source.go", "// SENTRY.Label(\"mirror\")\n// SENTRY.EndLabel\n")
			before, _ := os.ReadFile(filepath.Join(r.dir, "source.go"))
			r.env = append(r.env, "EDITOR=", "PATH=")
			_, stderr, code := r.run(t, "", args...)
			if code == 0 || !strings.Contains(stderr, ".ifttt-lint.yaml") {
				t.Fatalf("config error ignored: exit=%d stderr=%s", code, stderr)
			}
			after, _ := os.ReadFile(filepath.Join(r.dir, "source.go"))
			if !bytes.Equal(before, after) {
				t.Fatal("malformed config caused source mutation")
			}
			if _, err := os.Stat(filepath.Join(r.dir, "target.go")); !os.IsNotExist(err) {
				t.Fatalf("malformed config created target: %v", err)
			}
		})
	}
}

func TestScaffoldAndIgnoreUseRegisteredCommentStyles(t *testing.T) {
	for _, prefix := range []string{"SENTRY", "LINT"} {
		for _, tc := range []struct{ ext, open, close string }{
			{".html", "<!-- ", " -->"},
			{".md", "<!-- ", " -->"},
			{".css", "/* ", " */"},
		} {
			t.Run(prefix+tc.ext, func(t *testing.T) {
				r := newRepo(t)
				r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: "+prefix+"\nrules:\n  unknown_directive: error\n")
				src, dst := "source"+tc.ext, "target"+tc.ext
				requireCode(t, r, "", 0, "scaffold", "--source", src, "--target", dst+"#mirror", "--label", "mirror")
				for _, name := range []string{src, dst} {
					data, err := os.ReadFile(filepath.Join(r.dir, name))
					if err != nil || !bytes.Contains(data, []byte(tc.open+prefix+".")) || !bytes.Contains(data, []byte(tc.close)) {
						t.Fatalf("incorrect %s comment syntax: %s, %v", name, data, err)
					}
				}
				requireCode(t, r, "", 0, "--strict=true", "--format=json", src, dst)
				requireCode(t, r, "", 0, "ignore", "add", "--file", src, "--rule", "all")
				data, _ := os.ReadFile(filepath.Join(r.dir, src))
				if !bytes.Contains(data, []byte(tc.open+prefix+".Ignore(\"all\")"+tc.close)) {
					t.Fatalf("incorrect ignore comment syntax: %s", data)
				}
			})
		}
	}
}

func TestJumpPrintLocationUsesParsedDirectives(t *testing.T) {
	for _, prefix := range []string{"SENTRY", "LINT", "CUSTOM"} {
		t.Run(prefix, func(t *testing.T) {
			r := newRepo(t)
			r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: "+prefix+"\n")
			start, end := prefix+".Label(\"mirror\")", prefix+".EndLabel"
			if prefix == "LINT" {
				start, end = "LINT.IfChange(mirror)", "LINT.ThenChange()"
			}
			r.write(t, "-target.go", strings.Join([]string{
				"var example = `// " + start + "`",
				"// Documentation mentions " + start,
				"// ```",
				"// " + start,
				"// " + end,
				"// ```",
				"// " + start,
				"var mirror = 1",
				"// " + end,
			}, "\n"))
			r.env = append(r.env, "PATH=", "EDITOR=never-call-editor")
			out := requireCode(t, r, "", 0, "jump", "--print-location", "--", "-target.go", "mirror")
			var location struct {
				File string `json:"file"`
				Line int    `json:"line"`
			}
			if err := json.Unmarshal([]byte(out), &location); err != nil {
				t.Fatal(err)
			}
			if !filepath.IsAbs(location.File) || filepath.Base(location.File) != "-target.go" || location.Line != 8 {
				t.Fatalf("wrong parsed target location: %+v", location)
			}
			out = requireCode(t, r, "", 0, "jump", "--print-location", "--", "-target.go")
			if err := json.Unmarshal([]byte(out), &location); err != nil || location.Line != 1 {
				t.Fatalf("file-only location: %s, %v", out, err)
			}
			out, stderr, code := r.run(t, "", "jump", "--print-location", "--", "-target.go", "missing")
			if code != 1 || strings.TrimSpace(out) != "" || !strings.Contains(stderr, "not found") {
				t.Fatalf("missing label incorrectly succeeded: exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
			out, _, code = r.run(t, "", "jump", "--print-location", "missing.go")
			if code != 1 || strings.TrimSpace(out) != "" {
				t.Fatalf("missing target incorrectly succeeded: exit=%d stdout=%s", code, out)
			}
		})
	}
}

func TestScaffoldRejectsInvalidContractsBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, target, label string
	}{
		{"quoted SENTRY label", "SENTRY", "target.go", "bad\"label"},
		{"quoted SENTRY target", "SENTRY", "target\"bad.go", "mirror"},
		{"same SENTRY file", "SENTRY", "./source.go", "mirror"},
		{"same Google file", "LINT", "./source.go", "mirror"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: "+tc.prefix+"\n")
			r.write(t, "source.go", "existing content\n")
			before, err := os.ReadDir(r.dir)
			if err != nil {
				t.Fatal(err)
			}
			out, stderr, code := r.run(t, "", "scaffold", "--source", "source.go", "--target", tc.target, "--label", tc.label)
			if code == 0 {
				t.Fatalf("invalid contract accepted: stdout=%s stderr=%s", out, stderr)
			}
			data, _ := os.ReadFile(filepath.Join(r.dir, "source.go"))
			if string(data) != "existing content\n" {
				t.Fatalf("invalid contract mutated source: %s", data)
			}
			// Inspect entries because invalid target names cannot be statted on Windows.
			after, err := os.ReadDir(r.dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("invalid contract changed directory entries: before=%v after=%v", before, after)
			}
			for i := range before {
				if before[i].Name() != after[i].Name() {
					t.Fatalf("invalid contract changed directory entries: before=%v after=%v", before, after)
				}
			}
		})
	}
}

func TestReviewAndWatchExplicitFalseCodeOnly(t *testing.T) {
	for _, mode := range []string{"review", "watch"} {
		for _, tc := range []struct {
			name, flag string
			findings   int
		}{
			{"inherited", "", 0},
			{"explicit true", "--code-only=true", 0},
			{"explicit false", "--code-only=false", 1},
		} {
			t.Run(mode+" "+tc.name, func(t *testing.T) {
				r := fixture(t)
				r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\nrules:\n  code_only: true\n")
				r.write(t, "source file.go", strings.Replace(source("target file.go", "old"), "var value", "// explanatory comment\nvar value", 1))
				args := []string{mode, "--vcs=git", "--format=json"}
				if tc.flag != "" {
					args = append(args, tc.flag)
				}
				var result struct {
					Errors []struct {
						RuleID string `json:"ruleId"`
					} `json:"errors"`
				}
				if mode == "review" {
					r.git(t, "add", "source file.go")
					r.git(t, "commit", "-qm", "comment only change")
					out := requireCode(t, r, "", tc.findings, append(args, "HEAD")...)
					if err := json.Unmarshal([]byte(out), &result); err != nil {
						t.Fatal(err)
					}
				} else {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, append(args, "--interval=25ms")...)
					cmd.Dir, cmd.Env = r.dir, r.env
					stdout, err := cmd.StdoutPipe()
					if err != nil {
						t.Fatal(err)
					}
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := cmd.Process.Signal(os.Interrupt); err != nil {
							cancel()
						}
						_ = cmd.Wait()
					}()
					if err := json.NewDecoder(stdout).Decode(&result); err != nil {
						t.Fatal(err)
					}
				}
				if len(result.Errors) != tc.findings || (tc.findings == 1 && result.Errors[0].RuleID != "then_label_missing") {
					t.Fatalf("expected %d co-change findings, got %+v", tc.findings, result.Errors)
				}
			})
		}
	}
}
func TestDeletedTargetDoesNotSatisfyReference(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	if err := os.Remove(filepath.Join(r.dir, "target file.go")); err != nil {
		t.Fatal(err)
	}
	requireCode(t, r, r.git(t, "diff"), 1, "--format=json", "-")
}

// Decode successive JSON objects from the real long-running watch process. A
// bounded context also kills the process if a regression prevents another run.
func TestWatchRepeatedEditsWithIdenticalGitStatus(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "first"))
	initialStatus := r.git(t, "status", "--porcelain")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "watch", "--interval=25ms", "--format=json")
	cmd.Dir = r.dir
	cmd.Env = r.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// A graceful stop flushes instrumented CLI coverage; the context remains
		// the deadline if the process does not honor interruption.
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			cancel()
		}
		_ = cmd.Wait()
		cancel()
	}()
	decoder := json.NewDecoder(stdout)
	for i := 0; i < 2; i++ {
		var result struct {
			Errors []struct {
				RuleID string `json:"ruleId"`
			} `json:"errors"`
		}
		if err := decoder.Decode(&result); err != nil {
			t.Fatalf("watch run %d: %v", i, err)
		}
		if len(result.Errors) != 1 || result.Errors[0].RuleID != "then_label_missing" {
			t.Fatalf("watch run %d: %+v", i, result)
		}
		if i == 0 {
			r.write(t, "source file.go", source("target file.go", "second"))
			if current := r.git(t, "status", "--porcelain"); current != initialStatus {
				t.Fatalf("status unexpectedly changed: %q != %q", current, initialStatus)
			}
		}
	}
}

func TestRenamedTargetRequiresUpdatedReference(t *testing.T) {
	r := fixture(t)
	r.git(t, "mv", "target file.go", "renamed target.go")
	r.write(t, "source file.go", source("target file.go", "new"))
	requireCode(t, r, r.git(t, "diff", "HEAD"), 1, "--format=json", "-")
	r.write(t, "source file.go", source("renamed target.go", "new"))
	r.write(t, "renamed target.go", target("new"))
	requireCode(t, r, r.git(t, "diff", "HEAD"), 0, "--format=json", "-")
}
func TestDoctorFixIsIdempotentAndPreservesCode(t *testing.T) {
	r := fixture(t)
	before, _ := os.ReadFile(filepath.Join(r.dir, "target file.go"))
	requireCode(t, r, "", 1, "--doctor", "--fix", "--scan", ".")
	first, _ := os.ReadFile(filepath.Join(r.dir, "source file.go"))
	if !bytes.Contains(first, []byte(`var value = "old"`)) {
		t.Fatalf("doctor altered code: %s", first)
	}
	requireCode(t, r, "", 0, "--doctor", "--fix", "--scan", ".")
	second, _ := os.ReadFile(filepath.Join(r.dir, "source file.go"))
	if !bytes.Equal(first, second) {
		t.Fatal("doctor fix is not idempotent")
	}
	after, _ := os.ReadFile(filepath.Join(r.dir, "target file.go"))
	if !bytes.Equal(before, after) {
		t.Fatal("doctor changed existing target label")
	}
}

func TestScanValidatesStructureAndTargets(t *testing.T) {
	r := fixture(t)
	out := requireCode(t, r, "", 0, "--scan", ".", "--format=json")
	var clean struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &clean); err != nil {
		t.Fatalf("scan must emit selected output: %v (%s)", err, out)
	}
	if len(clean.Errors) != 0 {
		t.Fatalf("valid repository: %s", out)
	}
	r.write(t, "orphan.go", "// SENTRY.IfChange(\"orphan\")\nvar orphan = 1\n")
	out = requireCode(t, r, "", 1, "--scan", ".", "--format=json")
	if !strings.Contains(out, "orphan_if") {
		t.Fatalf("scan missed orphan: %s", out)
	}
	requireCode(t, r, "", 0, "--scan", ".", "--ignore", "orphan.go", "--format=json")
	if err := os.Remove(filepath.Join(r.dir, "orphan.go")); err != nil {
		t.Fatal(err)
	}
	r.write(t, "target file.go", "var target = 1\n")
	out = requireCode(t, r, "", 1, "--scan", ".", "--format=json")
	if !strings.Contains(out, "label_missing") {
		t.Fatalf("scan missed target label: %s", out)
	}
}
func TestDoctorHonorsSelectedOutputAndWarn(t *testing.T) {
	r := newRepo(t)
	r.write(t, "orphan.go", "// SENTRY.ThenChange(\"target.go\")\n")
	out := requireCode(t, r, "", 1, "--doctor", "--scan", ".", "--format=json")
	var parsed struct {
		Errors []struct {
			RuleID string `json:"ruleId"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("doctor JSON: %v (%s)", err, out)
	}
	if len(parsed.Errors) != 1 || parsed.Errors[0].RuleID != "orphan_then" {
		t.Fatalf("doctor findings: %s", out)
	}
	requireCode(t, r, "", 0, "--doctor", "--scan", ".", "-w", "--format=json")
}

func TestColocatedJujutsuMetadataDoesNotAffectProvidedGitDiff(t *testing.T) {
	r := fixture(t)
	if err := os.Mkdir(filepath.Join(r.dir, ".jj"), 0700); err != nil {
		t.Fatal(err)
	}
	r.write(t, "source file.go", source("target file.go", "new"))
	out := requireCode(t, r, r.git(t, "diff"), 1, "--format=json", "-")
	if !strings.Contains(out, "then_label_missing") {
		t.Fatalf("missing finding in colocated workspace: %s", out)
	}
}

func TestConfiguredOutputFileAndFailure(t *testing.T) {
	r := fixture(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\noutput:\n  format: json\n  path: findings.json\n")
	r.write(t, "source file.go", source("target file.go", "new"))
	out := requireCode(t, r, r.git(t, "diff"), 1, "-")
	if out != "" {
		t.Fatalf("configured file output leaked to stdout: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(r.dir, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || !bytes.Contains(data, []byte("then_label_missing")) {
		t.Fatalf("invalid output artifact: %s", data)
	}
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\noutput:\n  format: json\n  path: absent/findings.json\n")
	requireCode(t, r, r.git(t, "diff"), 2, "-")
}

func TestPythonDocstringsConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         int
	}{{"default", "", 1}, {"disabled", "languages:\n  python_docstrings: false\n", 0}, {"enabled", "languages:\n  python_docstrings: true\n", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.write(t, "orphan.py", "\"\"\"\nSENTRY.IfChange(\"docstring\")\n\"\"\"\nvalue = 1\n")
			if tc.config != "" {
				r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\n"+tc.config)
			}
			requireCode(t, r, "", tc.want, "--scan", ".", "--format=json")
		})
	}
}

func TestNestedConfigurationPreservesInheritedOutputAndPythonSetting(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\nlanguages:\n  python_docstrings: false\noutput:\n  format: json\n")
	if err := os.Mkdir(filepath.Join(r.dir, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	r.write(t, "child/.ifttt-lint.yaml", "parallelism: 2\n")
	r.write(t, "child/orphan.py", "\"\"\"\nSENTRY.IfChange(\"docstring\")\n\"\"\"\nvalue = 1\n")
	r.dir = filepath.Join(r.dir, "child")
	out := requireCode(t, r, "", 0, "--scan", ".")
	if !json.Valid([]byte(out)) || !strings.Contains(out, `"errors": []`) {
		t.Fatalf("child lost inherited output/docstring configuration: %s", out)
	}
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\nlanguages:\n  python_docstrings: true\n")
	requireCode(t, r, "", 1, "--scan", ".")
}

func TestDoctorSupportsNestedContracts(t *testing.T) {
	r := newRepo(t)
	r.write(t, "nested.go", "// SENTRY.IfChange(\"outer\")\n// SENTRY.IfChange(\"inner\")\nvar value = 1\n// SENTRY.ThenChange(\"target.go\")\n// SENTRY.ThenChange(\"target.go\")\n")
	r.write(t, "target.go", "var target = 1\n")
	requireCode(t, r, "", 0, "--doctor", "--scan", ".", "--format=json")
	r.write(t, "nested.go", "// SENTRY.IfChange(\"outer\")\n// SENTRY.IfChange(\"inner\")\nvar value = 1\n// SENTRY.ThenChange(\"target.go\")\n")
	out := requireCode(t, r, "", 1, "--doctor", "--scan", ".", "--format=json")
	if !strings.Contains(out, "orphan_if") || !strings.Contains(out, "outer") {
		t.Fatalf("doctor lost outer orphan: %s", out)
	}
}

func TestReviewIgnoresGitColorAndExternalDiffConfiguration(t *testing.T) {
	r := fixture(t)
	requireCode(t, r, "", 0, "--doctor", "--scan", ".")
	r.write(t, "source file.go", source("target file.go", "new"))
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "source change")
	r.git(t, "config", "color.ui", "always")
	r.git(t, "config", "diff.external", "this-command-must-never-be-executed")
	out := requireCode(t, r, "", 1, "review", "--format=json", "HEAD")
	if !json.Valid([]byte(out)) || !strings.Contains(out, "then_label_missing") {
		t.Fatalf("configured Git output broke review: %s", out)
	}
}

func TestStructuredTargetAndFixWithApostrophePath(t *testing.T) {
	r := newRepo(t)
	name := "owner's target.go"
	r.write(t, "source.go", source(name, "old"))
	r.write(t, name, target("old"))
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "baseline")
	r.write(t, "source.go", source(name, "new"))
	diff := r.git(t, "diff")
	originalSource, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := requireCode(t, r, diff, 1, "--format=json", "-")
	var result struct {
		Errors []struct {
			TargetPath  string `json:"targetPath"`
			TargetLabel string `json:"targetLabel"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 1 || result.Errors[0].TargetPath != name || result.Errors[0].TargetLabel != "shared label" {
		t.Fatalf("structured target lost: %s", out)
	}
	requireCode(t, r, diff, 1, "--fix", "-")
	data, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("TODO(ifttt)")) {
		t.Fatalf("apostrophe fix failed: %s", data)
	}
	requireCode(t, r, diff, 1, "--fix", "-")
	second, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil || !bytes.Equal(data, second) {
		t.Fatalf("fix is not idempotent: %v", err)
	}
	afterSource, err := os.ReadFile(filepath.Join(r.dir, "source.go"))
	if err != nil || !bytes.Equal(originalSource, afterSource) {
		t.Fatalf("fix altered triggering source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "owner")); !os.IsNotExist(err) {
		t.Fatalf("fix created truncated target: %v", err)
	}
}

func TestNativeGitModesAndStructuralFiles(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	requireCode(t, r, "", 1, "--vcs=git", "--format=json")
	requireCode(t, r, "", 0, "--vcs=git", "--staged", "--format=json")
	requireCode(t, r, "", 0, "--format=json", "source file.go")
	requireCode(t, r, "", 0, "--files=*.go", "--format=json")
	r.git(t, "add", "source file.go")
	requireCode(t, r, "", 1, "--vcs=git", "--staged", "--format=json")
	r.git(t, "commit", "-qm", "change source")
	requireCode(t, r, "", 1, "--vcs=git", "--diff=HEAD~1..HEAD", "--format=json")
	requireCode(t, r, "", 1, "review", "--vcs=git", "--format=json", "HEAD")
	requireCode(t, r, "", 2, "--vcs=invalid")
	requireCode(t, r, "", 2, "--vcs=git", "--diff=--output=bad")
	requireCode(t, r, "", 2, "--vcs=git", "--diff=HEAD", "--staged")
}

func TestNativeSuppressionPreservesStructureAndDeletion(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "change source\n\nNO_IFTTT=follow up")
	requireCode(t, r, "", 0, "--vcs=git", "--diff=HEAD~1..HEAD", "--format=json")
	requireCode(t, r, "", 0, "review", "--vcs=git", "--format=json", "HEAD")
	r.git(t, "rm", "target file.go")
	r.git(t, "commit", "-qm", "delete target\n\nNO_IFTTT=intentional")
	requireCode(t, r, "", 1, "--vcs=git", "--diff=HEAD~1..HEAD", "--format=json")
	requireCode(t, r, "", 1, "--format=json", "source file.go")
}

func TestNativeJJDiffReviewAndSelection(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj executable unavailable")
	}
	r := fixture(t)
	r.jj(t, "git", "init", "--colocate")
	r.write(t, "source file.go", source("target file.go", "new"))
	requireCode(t, r, "", 1, "--vcs=jj", "--format=json")
	requireCode(t, r, "", 1, "--vcs=auto", "--diff=@", "--format=json")
	requireCode(t, r, "", 1, "review", "--vcs=jj", "--format=json", "@")
	requireCode(t, r, "", 0, "--vcs=jj", "--diff=@", "--format=json", "target file.go")
	requireCode(t, r, "", 2, "--vcs=jj", "--staged")
	r.jj(t, "describe", "-m", "source edit\n\nNO_IFTTT=follow up")
	requireCode(t, r, "", 0, "--vcs=jj", "--diff=@", "--format=json")
	if err := os.Remove(filepath.Join(r.dir, "target file.go")); err != nil {
		t.Fatal(err)
	}
	requireCode(t, r, "", 1, "--vcs=jj", "--diff=@", "--format=json")
}
func (r repo) jj(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "jj", args...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jj %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestWatchRepeatedEditsWithNativeJJ(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj unavailable")
	}
	r := fixture(t)
	r.jj(t, "git", "init", "--colocate")
	r.write(t, "source file.go", source("target file.go", "first"))
	initialStatus := r.git(t, "status", "--porcelain")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "watch", "--vcs=jj", "--interval=25ms", "--format=json")
	cmd.Dir = r.dir
	cmd.Env = r.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// A graceful stop flushes instrumented CLI coverage; the context remains
		// the deadline if the process does not honor interruption.
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			cancel()
		}
		_ = cmd.Wait()
		cancel()
	}()
	decoder := json.NewDecoder(stdout)
	for i := 0; i < 2; i++ {
		var result struct {
			Errors []struct {
				RuleID string `json:"ruleId"`
			} `json:"errors"`
		}
		if err := decoder.Decode(&result); err != nil {
			t.Fatalf("watch run %d: %v", i, err)
		}
		if len(result.Errors) != 1 || result.Errors[0].RuleID != "then_label_missing" {
			t.Fatalf("watch run %d: %+v", i, result)
		}
		if i == 0 {
			r.write(t, "source file.go", source("target file.go", "second"))
			if current := r.git(t, "status", "--porcelain"); current != initialStatus {
				t.Fatalf("status unexpectedly changed: %q != %q", current, initialStatus)
			}
		}
	}
}

func TestNativeSelectedSourcesAndNestedRoot(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	requireCode(t, r, "", 0, "--vcs=git", "--diff=HEAD", "--format=json", "target file.go")
	if err := os.Mkdir(filepath.Join(r.dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	nested := r
	nested.dir = filepath.Join(r.dir, "nested")
	requireCode(t, nested, "", 1, "--vcs=git", "--format=json")
	requireCode(t, nested, "", 0, "--format=json", "../source file.go")
	requireCode(t, r, "", 2, "--files=[", "--format=json")
	requireCode(t, r, "", 2, "--files=missing.go", "--format=json")
}

func TestJSONWorkspaceRootFollowsNativeRepository(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	nested := r
	nested.dir = filepath.Join(r.dir, "nested")
	if err := os.Mkdir(nested.dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		root string
		code int
	}{
		{"native", []string{"--vcs=git", "--format=json"}, r.dir, 1},
		{"stdin", []string{"--format=json", "-"}, nested.dir, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := requireCode(t, nested, "", tc.code, tc.args...)
			var payload struct {
				WorkspaceRoot string `json:"workspaceRoot"`
				Errors        []struct {
					File string `json:"file"`
				} `json:"errors"`
			}
			if err := json.Unmarshal([]byte(output), &payload); err != nil {
				t.Fatal(err)
			}
			want, err := filepath.EvalSymlinks(tc.root)
			if err != nil {
				t.Fatal(err)
			}
			if payload.WorkspaceRoot != want || !filepath.IsAbs(payload.WorkspaceRoot) {
				t.Fatalf("workspaceRoot = %q, want %q: %s", payload.WorkspaceRoot, want, output)
			}
			if tc.name == "native" {
				if len(payload.Errors) != 1 || filepath.Join(payload.WorkspaceRoot, payload.Errors[0].File) != filepath.Join(want, "source file.go") {
					t.Fatalf("finding does not resolve against native repository root: %s", output)
				}
			}
		})
	}
}

func TestUpstreamThreadAndFormatAliases(t *testing.T) {
	r := fixture(t)
	requireCode(t, r, "", 0, "--threads=0", "--format=plain", "source file.go")
	requireCode(t, r, "", 0, "-t=1", "--format=pretty", "source file.go")
}

func TestExplicitBackendWithExternalPatchOrStructuralInputs(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	diff := r.git(t, "diff")
	requireCode(t, r, diff, 1, "--vcs=git", "--format=json", "-")
	requireCode(t, r, "", 0, "--vcs=git", "--format=json", "source file.go")
}

func TestExplicitStrictGooglePaths(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
	r.write(t, "source.go", "// LINT.IfChange\nvar a = 1\n// LINT.ThenChange(target.go)\n")
	r.write(t, "target.go", "var b = 1\n")
	requireCode(t, r, "", 0, "--strict=false", "--format=json", "source.go")
	requireCode(t, r, "", 1, "--strict=true", "--format=json", "source.go")
	r.write(t, "source.go", "// LINT.IfChange\nvar a = 1\n// LINT.ThenChange(//target.go)\n")
	requireCode(t, r, "", 0, "--strict=true", "--format=json", "source.go")
}

func TestUpstreamShortFlags(t *testing.T) {
	r := fixture(t)
	r.write(t, "source file.go", source("target file.go", "new"))
	requireCode(t, r, "", 1, "--vcs=git", "-d=HEAD", "-f=json")
	requireCode(t, r, "", 0, "--vcs=git", "-d=HEAD", "-f=json", "-i=source file.go")
}

func TestJJWatchRejectsStagingBeforePolling(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj unavailable")
	}
	r := fixture(t)
	r.jj(t, "git", "init", "--colocate")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "watch", "--vcs=jj", "--staged", "--interval=1h")
	cmd.Dir = r.dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(out), "staging") {
		t.Fatalf("unsupported staged watch did not fail immediately: %v %s", err, out)
	}
}

func TestStructuralFlagsAfterFiles(t *testing.T) {
	r := fixture(t)
	out := requireCode(t, r, "", 0, "source file.go", "--format=json", "--vcs=git")
	if !json.Valid([]byte(out)) {
		t.Fatalf("interspersed flags did not produce JSON: %s", out)
	}
}

func TestLocalReferencesCannotEscapeRepository(t *testing.T) {
	for _, mode := range []string{"parent", "absolute", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			r := newRepo(t)
			outside := filepath.Join(t.TempDir(), "outside.go")
			if err := os.WriteFile(outside, []byte(target("unchanged")), 0600); err != nil {
				t.Fatal(err)
			}
			target := outside
			if mode == "parent" {
				var err error
				target, err = filepath.Rel(r.dir, outside)
				if err != nil {
					t.Fatal(err)
				}
			} else if mode == "symlink" {
				if err := os.Symlink(outside, filepath.Join(r.dir, "linked.go")); err != nil {
					t.Skip(err)
				}
				target = "linked.go"
			}
			r.write(t, "source.go", source(target, "old"))
			r.git(t, "add", ".")
			r.git(t, "commit", "-qm", "initial")
			r.write(t, "source.go", source(target, "new"))
			output, stderr, code := r.run(t, "", "--vcs=git", "--format=json")
			if code != 1 || !strings.Contains(output+stderr, "outside workspace") {
				t.Fatalf("outside local reference must be rejected explicitly: code=%d stdout=%s stderr=%s", code, output, stderr)
			}
		})
	}
}

func TestCrossLanguageRootRelativeContracts(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			if _, err := exec.LookPath(backend); err != nil {
				t.Skip(err)
			}
			r := newRepo(t)
			if backend == "jj" {
				r.jj(t, "git", "init", "--colocate")
			}
			if err := os.MkdirAll(filepath.Join(r.dir, "app"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(r.dir, "schema"), 0700); err != nil {
				t.Fatal(err)
			}
			r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
			sql := "-- LINT.IfChange(contract)\nSELECT 1;\n-- LINT.ThenChange(//app/client.ts:contract)\n"
			ts := "// LINT.IfChange(contract)\nconst version = 1;\n// LINT.ThenChange(//schema/query.sql:contract)\n"
			r.write(t, "schema/query.sql", sql)
			r.write(t, "app/client.ts", ts)
			if backend == "git" {
				r.git(t, "add", ".")
				r.git(t, "commit", "-qm", "baseline")
			} else {
				r.jj(t, "describe", "-m", "baseline")
				r.jj(t, "new")
			}
			r.write(t, "schema/query.sql", strings.Replace(sql, "SELECT 1", "SELECT 2", 1))
			nested := r
			nested.dir = filepath.Join(r.dir, "app")
			requireCode(t, nested, "", 1, "--vcs="+backend, "--strict=true", "--format=json")
			r.write(t, "app/client.ts", strings.Replace(ts, "version = 1", "version = 2", 1))
			requireCode(t, nested, "", 0, "--vcs="+backend, "--strict=true", "--format=json")
		})
	}
}

func TestNativeRootPathsAcrossCommentFamilies(t *testing.T) {
	r := newRepo(t)
	r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
	groups := []struct{ names, open, close string }{
		{"a.c a.h a.cpp a.cc a.cxx a.hpp a.hxx a.hh a.cs a.dart a.go a.groovy a.gradle a.java a.js a.jsx a.mjs a.cjs a.kt a.kts a.m a.mm a.php a.phtml a.proto a.rs a.scala a.sc a.scss a.swift a.ts a.tsx a.mts a.cts", "//", ""},
		{"a.cmake CMakeLists.txt Dockerfile Dockerfile.dev a.dockerfile a.ex a.exs a.gn a.gni a.graphql a.gql a.mk a.mak Makefile makefile GNUmakefile a.nix a.pl a.pm a.t a.ps1 a.psm1 a.psd1 a.py a.pyi a.pyw a.r a.rb a.rake a.gemspec Rakefile Gemfile a.sh a.bash a.zsh a.ksh a.bzl BUILD BUILD.bazel WORKSPACE a.tf a.hcl a.tfvars a.toml a.yaml a.yml", "#", ""},
		{"a.clj a.cljs a.cljc a.edn a.lisp a.cl a.el a.scm a.rkt", ";", ""}, {"a.hs a.lua a.sql", "--", ""}, {"a.tex a.sty a.cls", "%", ""}, {"a.css", "/*", "*/"},
		{"a.html a.htm a.xhtml a.md a.mdx a.markdown a.xml a.xsd a.xsl a.xslt a.svg a.vue a.svelte", "<!--", "-->"}, {"a.tpl a.gotmpl a.gohtml a.tmpl", "{{/*", "*/}}"},
	}
	bodies := map[string]string{}
	for _, group := range groups {
		for _, name := range strings.Fields(group.names) {
			if name == "makefile" {
				if err := os.Mkdir(filepath.Join(r.dir, "lowercase"), 0700); err != nil {
					t.Fatal(err)
				}
				name = "lowercase/makefile"
			}
			body := group.open + " LINT.IfChange(contract) " + group.close + "\nvalue = 1\n" + group.open + " LINT.ThenChange(//target.go:shared) " + group.close + "\n"
			bodies[name] = body
			r.write(t, name, body)
		}
	}
	label := "// LINT.IfChange(shared)\nvar target = 1\n// LINT.ThenChange()\n"
	r.write(t, "target.go", label)
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "language matrix baseline")
	for name, body := range bodies {
		r.write(t, name, strings.Replace(body, "value = 1", "value = 2", 1))
	}
	output := requireCode(t, r, "", 1, "--vcs=git", "--strict=true", "--format=json")
	var result struct {
		Errors []struct {
			File   string `json:"file"`
			RuleID string `json:"ruleId"`
		}
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, finding := range result.Errors {
		if _, ok := bodies[finding.File]; !ok || finding.RuleID != "then_label_missing" || seen[finding.File] {
			t.Fatalf("unexpected language finding %+v", finding)
		}
		seen[finding.File] = true
	}
	if len(seen) != len(bodies) {
		t.Fatalf("only %d/%d filenames enforced: %s", len(seen), len(bodies), output)
	}
	r.write(t, "target.go", strings.Replace(label, "target = 1", "target = 2", 1))
	requireCode(t, r, "", 0, "--vcs=git", "--strict=true", "--format=json")
}

func TestNativeGoTypeScriptMarkdownChainFromNestedDirectory(t *testing.T) {
	assertSource := func(t *testing.T, output, want string) {
		t.Helper()
		var report struct {
			Errors []struct {
				File string `json:"file"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Errors) != 1 || report.Errors[0].File != want {
			t.Fatalf("expected exactly one finding in %s: %s", want, output)
		}
	}
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			if _, err := exec.LookPath(backend); err != nil {
				t.Skip(err)
			}
			r := newRepo(t)
			if backend == "jj" {
				r.jj(t, "git", "init", "--colocate")
			}
			for _, dir := range []string{"src", "app/nested", "docs"} {
				if err := os.MkdirAll(filepath.Join(r.dir, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
			goSource := "// LINT.IfChange(API)\nvar version = 1\n// LINT.ThenChange(//app/client.ts:API)\n"
			tsSource := "// LINT.IfChange(API)\nconst version = 1;\n// LINT.ThenChange(//docs/api.md:API)\n"
			markdown := "<!-- LINT.IfChange(API) -->\nAPI version 1\n<!-- LINT.ThenChange() -->\n"
			r.write(t, "src/api.go", goSource)
			r.write(t, "app/client.ts", tsSource)
			r.write(t, "docs/api.md", markdown)
			if backend == "git" {
				r.git(t, "add", ".")
				r.git(t, "commit", "-qm", "chain baseline")
			} else {
				r.jj(t, "describe", "-m", "chain baseline")
				r.jj(t, "new")
			}
			nested := r
			nested.dir = filepath.Join(r.dir, "app/nested")
			r.write(t, "src/api.go", strings.Replace(goSource, "version = 1", "version = 2", 1))
			output := requireCode(t, nested, "", 1, "--vcs="+backend, "--strict=true", "--format=json")
			assertSource(t, output, "src/api.go")
			r.write(t, "app/client.ts", strings.Replace(tsSource, "version = 1", "version = 2", 1))
			output = requireCode(t, nested, "", 1, "--vcs="+backend, "--strict=true", "--format=json")
			assertSource(t, output, "app/client.ts")
			r.write(t, "docs/api.md", strings.Replace(markdown, "version 1", "version 2", 1))
			requireCode(t, nested, "", 0, "--vcs="+backend, "--strict=true", "--format=json")
		})
	}
}

func TestLocalContractsRejectAnotherGitRepositoryEvenWhenBothChange(t *testing.T) {
	for _, mode := range []string{"parent", "absolute", "symlink", "directory-symlink"} {
		t.Run(mode, func(t *testing.T) {
			r := newRepo(t)
			external := newRepo(t)
			external.write(t, "target.go", target("old"))
			external.git(t, "add", ".")
			external.git(t, "commit", "-qm", "external baseline")
			path := filepath.Join(external.dir, "target.go")
			switch mode {
			case "parent":
				var err error
				path, err = filepath.Rel(r.dir, path)
				if err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(path, filepath.Join(r.dir, "linked.go")); err != nil {
					t.Skip(err)
				}
				path = "linked.go"
			case "directory-symlink":
				if err := os.Symlink(external.dir, filepath.Join(r.dir, "linked")); err != nil {
					t.Skip(err)
				}
				path = "linked/target.go"
			}
			r.write(t, "source.go", source(path, "old"))
			r.git(t, "add", ".")
			r.git(t, "commit", "-qm", "local baseline")
			r.write(t, "source.go", source(path, "new"))
			external.write(t, "target.go", target("new"))
			if external.git(t, "diff") == "" {
				t.Fatal("external repo must also change")
			}
			output := requireCode(t, r, "", 1, "--vcs=git", "--format=json")
			if !strings.Contains(output, "outside workspace") {
				t.Fatalf("cross-repository local reference not rejected: %s", output)
			}
		})
	}
}

// A delegating executable observes the native Git query boundary without
// replacing Git's results or relying on internal engine instrumentation.
func TestNativeReverseDiscoveryUsesOneNeedleQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable wrapper")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"deleted-targets", "renamed-labels", "ordinary-body"} {
		t.Run(mode, func(t *testing.T) {
			r := newRepo(t)
			r.write(t, ".gitignore", "ignored/\n")
			const count = 24
			for i := 0; i < count; i++ {
				r.write(t, fmt.Sprintf("source%02d.go", i), source(fmt.Sprintf("target%02d.go", i), "old"))
				r.write(t, fmt.Sprintf("target%02d.go", i), target("old"))
			}
			r.write(t, "ignored-source.go", source("target00.go", "old"))
			r.write(t, "unrelated.go", "// SENTRY.ThenChange(\"nonexistent.go\")\n")
			r.write(t, "plain.go", "var plain = 1\n")
			r.git(t, "add", ".")
			r.git(t, "commit", "-qm", "reverse query baseline")
			if err := os.Mkdir(filepath.Join(r.dir, "ignored"), 0700); err != nil {
				t.Fatal(err)
			}
			r.write(t, "ignored/invalid.go", "// SENTRY.ThenChange(\"nonexistent.go\")\n")
			switch mode {
			case "deleted-targets":
				for i := 0; i < count; i++ {
					if err := os.Remove(filepath.Join(r.dir, fmt.Sprintf("target%02d.go", i))); err != nil {
						t.Fatal(err)
					}
				}
			case "renamed-labels":
				for i := 0; i < count; i++ {
					r.write(t, fmt.Sprintf("target%02d.go", i), strings.Replace(target("old"), "shared label", "renamed label", 1))
				}
			case "ordinary-body":
				r.write(t, "source00.go", source("target00.go", "new"))
				r.write(t, "plain.go", "var plain = 2\n")
			}
			wrapper := t.TempDir()
			log := filepath.Join(wrapper, "queries.log")
			quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'" }
			script := "#!/bin/sh\nif [ \"$1\" = grep ]; then printf '%s\\n' \"$*\" >> \"$IFTTT_GIT_QUERY_LOG\"; fi\nexec " + quote(realGit) + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			var env []string
			for _, entry := range r.env {
				if !strings.HasPrefix(entry, "PATH=") {
					env = append(env, entry)
				}
			}
			r.env = append(env, "PATH="+wrapper+string(os.PathListSeparator)+os.Getenv("PATH"), "IFTTT_GIT_QUERY_LOG="+log)
			output := requireCode(t, r, "", 1, "--vcs=git", "--format=json", "--ignore=ignored-source.go")
			var result struct {
				Errors []struct {
					File string `json:"file"`
				}
			}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			expected := count
			if mode == "ordinary-body" {
				expected = 1
			}
			sources := map[string]bool{}
			for _, finding := range result.Errors {
				if strings.HasPrefix(finding.File, "source") {
					sources[finding.File] = true
					continue
				}
				if mode == "deleted-targets" && strings.HasPrefix(finding.File, "target") {
					continue
				}
				t.Fatalf("unrelated/ignored file affected local checking: %+v", finding)
			}
			if len(sources) != expected {
				t.Fatalf("referencing sources=%d want %d: %s", len(sources), expected, output)
			}
			queries, err := os.ReadFile(log)
			if mode == "ordinary-body" {
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(queries) != 0 {
					t.Fatalf("ordinary edit unexpectedly queried repository: %s", queries)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(queries) != "grep -I -l -z --fixed-strings -e SENTRY. -- .\n" {
				t.Fatalf("expected exactly one directive-only query independent of changed paths: %q", queries)
			}
		})
	}
}

func TestLiteralStructuralSelectionAvoidsRepositoryEnumeration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable wrapper")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	r := fixture(t)
	for i := 0; i < 30; i++ {
		r.write(t, fmt.Sprintf("unrelated%02d.go", i), "var unrelated = 1\n")
	}
	r.git(t, "add", ".")
	r.git(t, "commit", "-qm", "unrelated tracked files")
	wrapper := t.TempDir()
	log := filepath.Join(wrapper, "commands.log")
	escaped := "'" + strings.ReplaceAll(realGit, "'", "'\"'\"'") + "'"
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$IFTTT_GIT_QUERY_LOG\"\nexec " + escaped + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, entry := range r.env {
		if !strings.HasPrefix(entry, "PATH=") {
			env = append(env, entry)
		}
	}
	r.env = append(env, "PATH="+wrapper+string(os.PathListSeparator)+os.Getenv("PATH"), "IFTTT_GIT_QUERY_LOG="+log)
	for _, tc := range []struct {
		name, selection string
		enumerations    int
	}{{"literal", "source file.go", 0}, {"glob", "*file.go", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			requireCode(t, r, "", 0, "--vcs=git", "--format=json", "--files="+tc.selection)
			commands, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if actual := strings.Count(string(commands), "ls-files\n"); actual != tc.enumerations {
				t.Fatalf("selection %q enumerated repository %d times; want %d: %s", tc.selection, actual, tc.enumerations, commands)
			}
		})
	}
}

func TestNestedGitTargetsValidateButCannotProveParentCochange(t *testing.T) {
	for _, mode := range []string{"independent", "submodule"} {
		t.Run(mode, func(t *testing.T) {
			r := newRepo(t)
			external := newRepo(t)
			target := "// LINT.IfChange(API)\nvar target = 1\n// LINT.ThenChange()\n"
			external.write(t, "target.go", target)
			external.git(t, "add", ".")
			external.git(t, "commit", "-qm", "child baseline")
			child := external
			if mode == "submodule" {
				r.git(t, "-c", "protocol.file.allow=always", "submodule", "add", "-q", external.dir, "nested")
				child.dir = filepath.Join(r.dir, "nested")
			} else {
				child.dir = filepath.Join(r.dir, "nested")
				if err := os.Mkdir(child.dir, 0700); err != nil {
					t.Fatal(err)
				}
				child.git(t, "init", "-q")
				child.git(t, "config", "user.name", "Integration Test")
				child.git(t, "config", "user.email", "integration@example.invalid")
				child.write(t, "target.go", target)
				child.git(t, "add", ".")
				child.git(t, "commit", "-qm", "nested baseline")
			}
			r.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
			source := "// LINT.IfChange(API)\nvar source = 1\n// LINT.ThenChange(//nested/target.go:API)\n"
			r.write(t, "source.go", source)
			r.git(t, "add", "source.go", ".ifttt-lint.yaml")
			r.git(t, "commit", "-qm", "parent baseline")
			requireCode(t, r, "", 0, "--vcs=git", "--strict=true", "--format=json", "source.go")
			r.write(t, "source.go", strings.Replace(source, "source = 1", "source = 2", 1))
			child.write(t, "target.go", strings.Replace(target, "target = 1", "target = 2", 1))
			if child.git(t, "diff", "--", "target.go") == "" {
				t.Fatal("target must independently change")
			}
			parentPaths := r.git(t, "diff", "HEAD", "--name-only")
			if strings.Contains(parentPaths, "nested/target.go") {
				t.Fatalf("parent diff unexpectedly contains child file: %s", parentPaths)
			}
			output := requireCode(t, r, "", 1, "--vcs=git", "--diff=HEAD", "--strict=true", "--format=json")
			var report struct {
				Errors []struct {
					File   string `json:"file"`
					RuleID string `json:"ruleId"`
				}
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Errors) != 1 || report.Errors[0].File != "source.go" || report.Errors[0].RuleID != "then_label_missing" {
				t.Fatalf("parent diff falsely established child cochange: %s", output)
			}
		})
	}
}
