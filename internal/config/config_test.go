package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadReadsConfig(t *testing.T) {
	dir := t.TempDir()
	content := `
parallelism: "auto"
verbose: true
ignores: ["a.go"]
skip_directories: [".git", "vendor"]
remotes:
  - type: github
    repo: derhnyel/ifttt
    default_ref: main
    token_env: TEST_TOKEN
directives:
  prefix: "CUSTOM"
rules:
  unknown_directive: warn
  combined_diff: error
  code_only: true
output:
  format: json
`
	if err := os.WriteFile(filepath.Join(dir, ".ifttt-lint.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Parallelism != "auto" || !cfg.Verbose {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if len(cfg.Ignores) != 1 || cfg.Ignores[0] != "a.go" {
		t.Fatalf("unexpected ignores: %+v", cfg.Ignores)
	}
	if got, want := cfg.SkipDirs, []string{".git", "vendor"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected skip dirs: %+v", cfg.SkipDirs)
	}
	if cfg.Rules.UnknownDirective != "warn" || cfg.Rules.CombinedDiff != "error" || !cfg.Rules.CodeOnly {
		t.Fatalf("unexpected rules: %+v", cfg.Rules)
	}
	if cfg.Output.Format != "json" {
		t.Fatalf("expected format json, got %s", cfg.Output.Format)
	}
	if cfg.Directives.Prefix != "CUSTOM" {
		t.Fatalf("expected directive prefix CUSTOM, got %q", cfg.Directives.Prefix)
	}
	if len(cfg.Remotes) != 1 {
		t.Fatalf("expected one remote, got %+v", cfg.Remotes)
	}
	remote := cfg.Remotes[0]
	if remote.Type != "github" || remote.Repo != "derhnyel/ifttt" || remote.DefaultRef != "main" || remote.TokenEnv != "TEST_TOKEN" {
		t.Fatalf("unexpected remote config: %+v", remote)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
	if cfg.Directives.Prefix != "LINT" {
		t.Fatalf("expected default config on missing file")
	}
	if cfg.Rules.CombinedDiff != "skip_with_note" {
		t.Fatalf("expected default combined diff policy skip_with_note, got %q", cfg.Rules.CombinedDiff)
	}
}

func TestMergeOverridesLower(t *testing.T) {
	high := Config{Parallelism: "2", Verbose: true}
	high.Ignores = []string{"a"}
	high.SkipDirs = []string{"vendor"}
	high.Directives.Prefix = "CUSTOM"
	high.Output.Format = "sarif"
	high.Remotes = []Remote{{Type: "github", Repo: "org/high", DefaultRef: "main"}}

	low := Config{Parallelism: "1"}
	low.Ignores = []string{"b"}
	low.SkipDirs = []string{".git"}
	low.Directives.Prefix = "SENTRY"
	low.Output.Format = "text"
	low.Remotes = []Remote{{Type: "github", Repo: "org/low", DefaultRef: "dev"}}

	got := Merge(high, low)
	if got.Parallelism != "2" || !got.Verbose {
		t.Fatalf("unexpected merged basics: %+v", got)
	}
	if len(got.Ignores) != 2 || got.Ignores[0] != "b" || got.Ignores[1] != "a" {
		t.Fatalf("expected combined ignores, got %+v", got.Ignores)
	}
	if len(got.SkipDirs) != 2 || got.SkipDirs[0] != ".git" || got.SkipDirs[1] != "vendor" {
		t.Fatalf("expected combined skip dirs, got %+v", got.SkipDirs)
	}
	if got.Output.Format != "sarif" {
		t.Fatalf("expected output format sarif, got %s", got.Output.Format)
	}
	if got.Directives.Prefix != "CUSTOM" {
		t.Fatalf("expected directive prefix from high, got %q", got.Directives.Prefix)
	}
	if len(got.Remotes) != 2 || got.Remotes[0].Repo != "org/low" || got.Remotes[1].Repo != "org/high" {
		t.Fatalf("expected merged remotes, got %+v", got.Remotes)
	}
}

func TestValidateDefaultsAndErrors(t *testing.T) {
	cfg := Config{}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate default: %v", err)
	}
	if cfg.Directives.Prefix != "LINT" {
		t.Fatalf("expected default prefix LINT, got %q", cfg.Directives.Prefix)
	}
	if len(cfg.SkipDirs) == 0 {
		t.Fatalf("expected default skip directories to be populated")
	}

	cfg.Output.Format = "xml"
	if err := Validate(&cfg); err == nil {
		t.Fatalf("expected error for invalid format")
	}

	cfg.Output.Format = "text"
	cfg.Rules.UnknownDirective = "bad"
	if err := Validate(&cfg); err == nil {
		t.Fatalf("expected error for invalid unknown_directive")
	}

	cfg.Rules.UnknownDirective = "warn"
	cfg.Rules.CombinedDiff = "oops"
	if err := Validate(&cfg); err == nil {
		t.Fatalf("expected error for invalid combined_diff")
	}
}

func TestLoadCascadingConfigs(t *testing.T) {
	repo := t.TempDir()
	rootCfg := `
skip_directories: ["third_party"]
ignores: ["root.txt"]
remotes:
  - type: github
    repo: root/org
    default_ref: main
`
	if err := os.WriteFile(filepath.Join(repo, ".ifttt-lint.yaml"), []byte(rootCfg), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
	subDir := filepath.Join(repo, "apps", "service")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	subCfg := `
ignores: ["local.go#LBL"]
skip_directories: ["build"]
remotes:
  - type: github
    repo: child/org
    default_ref: dev
`
	if err := os.WriteFile(filepath.Join(subDir, ".ifttt-lint.yaml"), []byte(subCfg), 0o644); err != nil {
		t.Fatalf("write sub config: %v", err)
	}

	cfg, err := Load(subDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load cascading: %v", err)
	}
	if cfg.BaseDir != repo {
		t.Fatalf("expected base dir %s, got %s", repo, cfg.BaseDir)
	}
	if !contains(cfg.SkipDirs, "third_party") || !contains(cfg.SkipDirs, filepath.ToSlash(filepath.Join("apps", "service", "build"))) {
		t.Fatalf("expected combined skip dirs, got %+v", cfg.SkipDirs)
	}
	wantIgnore := filepath.ToSlash(filepath.Join("apps", "service", "local.go#LBL"))
	if !contains(cfg.Ignores, "root.txt") || !contains(cfg.Ignores, wantIgnore) {
		t.Fatalf("expected combined ignores, got %+v", cfg.Ignores)
	}
	if !containsRemote(cfg.Remotes, "root/org") || !containsRemote(cfg.Remotes, "child/org") {
		t.Fatalf("expected merged remotes, got %+v", cfg.Remotes)
	}
}

func TestLoadCascadingBooleanOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, child string
		want        bool
	}{
		{"explicit false", "verbose: false\nrules:\n  code_only: false\n", false},
		{"explicit true", "verbose: true\nrules:\n  code_only: true\n", true},
		{"omitted", "output:\n  format: json\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			child := filepath.Join(root, "child")
			leaf := filepath.Join(child, "leaf")
			if err := os.MkdirAll(leaf, 0755); err != nil {
				t.Fatal(err)
			}
			for dir, body := range map[string]string{
				root:  "verbose: true\nrules:\n  code_only: true\n",
				child: tc.child,
				leaf:  "output:\n  format: text\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, ".ifttt-lint.yaml"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load(leaf)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Verbose != tc.want || cfg.Rules.CodeOnly != tc.want {
				t.Fatalf("verbose=%v code_only=%v; want both %v", cfg.Verbose, cfg.Rules.CodeOnly, tc.want)
			}
		})
	}
}

func contains(list []string, val string) bool {
	for _, v := range list {
		if v == val {
			return true
		}
	}
	return false
}

func containsRemote(remotes []Remote, repo string) bool {
	for _, r := range remotes {
		if r.Repo == repo {
			return true
		}
	}
	return false
}

func TestDiagnosticOutputFormats(t *testing.T) {
	for _, format := range []string{"diagnostic-ls", "diagnostic", "dls"} {
		var cfg Config
		cfg.Output.Format = format
		if err := Validate(&cfg); err != nil {
			t.Errorf("format %s rejected: %v", format, err)
		}
	}
}

func TestInvalidParallelism(t *testing.T) {
	for _, value := range []string{"0", "-2", "garbage"} {
		c := Config{Parallelism: value}
		if Validate(&c) == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
func TestMergePreservesInheritedOutputAndRules(t *testing.T) {
	var parent, child Config
	parent.Output.Format = "json"
	parent.Output.Path = "report.json"
	parent.Rules.UnknownDirective = "ignore"
	child.Directives.Prefix = "LINT"
	got := Merge(child, parent)
	if got.Output.Format != "json" || got.Output.Path != "report.json" || got.Rules.UnknownDirective != "ignore" {
		t.Fatalf("lost inherited values: %+v", got)
	}
	off := false
	parent.Languages.PythonDocstrings = &off
	got = Merge(child, parent)
	if got.PythonDocstringsEnabled() {
		t.Fatal("lost explicit false")
	}
}

func TestValidateDirectivePrefix(t *testing.T) {
	for _, tc := range []struct{ prefix, want string }{
		{"", "LINT"}, {" \t\n", "LINT"}, {"LINT", "LINT"},
		{"SENTRY", "SENTRY"}, {"CUSTOM", "CUSTOM"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			cfg := Config{}
			cfg.Directives.Prefix = tc.prefix
			if err := Validate(&cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Directives.Prefix != tc.want {
				t.Fatalf("prefix = %q, want %q", cfg.Directives.Prefix, tc.want)
			}
		})
	}
}

func TestDecodeSnapshotConfiguration(t *testing.T) {
	cfg, err := Decode(nil)
	if err != nil || cfg.Directives.Prefix != "LINT" || !cfg.PythonDocstringsEnabled() {
		t.Fatalf("snapshot defaults: %#v, %v", cfg, err)
	}
	for _, content := range []string{"directives: [", "parallelism: invalid", "rules:\n  unknown_directive: typo", "directives:\n  prefix: LINT\n---\ndirectives:\n  prefix: CUSTOM\n", strings.Repeat(" ", (1<<20)+1)} {
		if _, err := Decode([]byte(content)); err == nil {
			t.Fatalf("invalid snapshot config accepted: %.80s", content)
		}
	}
}

func TestSnapshotConfigurationNormalizesRootPaths(t *testing.T) {
	body := "ignores: [./source.go, 'github://acme/target/target.go#API']\nskip_directories: [./generated]\n"
	root := filepath.Join(t.TempDir(), "checkout")
	body += "output:\n  format: json\n"
	cfg, err := Decode([]byte(body), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ignores) != 2 || cfg.Ignores[0] != "source.go" || cfg.Ignores[1] != "github://acme/target/target.go#API" || len(cfg.SkipDirs) != 1 || cfg.SkipDirs[0] != "generated" {
		t.Fatalf("snapshot paths differ from root config semantics: %#v", cfg)
	}
	absolute, err := Decode([]byte("ignores: ["+strconv.Quote(filepath.Join(root, "source.go"))+"]\n"), root)
	if err != nil || len(absolute.Ignores) != 1 || absolute.Ignores[0] != "source.go" {
		t.Fatalf("absolute snapshot path: %#v, %v", absolute, err)
	}
}
