package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func changeSetCommit(t *testing.T, r repo) string {
	t.Helper()
	r.git(t, "add", ".")
	r.git(t, "commit", "--allow-empty", "-qm", "change-set revision")
	return strings.TrimSpace(r.git(t, "rev-parse", "HEAD"))
}
func changeSetManifest(t *testing.T, a, b repo, aBase, aHead, bBase, bHead string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "changes.yaml")
	data, err := yaml.Marshal(map[string]any{"version": 1, "repositories": []map[string]string{
		{"repo": "acme/source", "path": a.dir, "vcs": "git", "base": aBase, "head": aHead},
		{"repo": "acme/target", "path": b.dir, "vcs": "git", "base": bBase, "head": bHead},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func changeSetFixture(t *testing.T) (repo, repo, string, string) {
	t.Helper()
	a, b := newRepo(t), newRepo(t)
	a.write(t, "source.go", source("github://acme/target/target.go", "old"))
	b.write(t, "target.go", target("old"))
	return a, b, changeSetCommit(t, a), changeSetCommit(t, b)
}
func TestChangeSetPairedSnapshots(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	if err := os.Mkdir(filepath.Join(b.dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	const targetPath = "github://acme/target/nested/target.go"
	a.write(t, "source.go", source(targetPath, "old"))
	b.write(t, "nested/target.go", target("old"))
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	a.write(t, "source.go", source(targetPath, "new"))
	aHead := changeSetCommit(t, a)
	output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
	var report struct {
		Errors []struct{ File, Repository, BaseRevision, HeadRevision, TargetPath, RuleID string }
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 1 || report.Errors[0].Repository != "acme/source" || report.Errors[0].HeadRevision != aHead || report.Errors[0].BaseRevision != aBase || report.Errors[0].File != filepath.Join(canonicalRoot, "source.go") || report.Errors[0].TargetPath != targetPath || report.Errors[0].RuleID != "then_label_missing" {
		t.Fatalf("missing repository-aware dependency diagnostic: %s", output)
	}
	b.write(t, "nested/target.go", target("new"))
	bHead := changeSetCommit(t, b)
	manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bHead)
	a.write(t, "source.go", "// SENTRY.IfChange(\"broken working file\")\n")
	b.write(t, "nested/target.go", "dirty working contents without labels\n")
	requireCode(t, a, "", 0, "--change-set", manifest, "--format=json")
}
func TestChangeSetUnrelatedTargetEditFails(t *testing.T) {
	a, b, aBase, bBase := changeSetFixture(t)
	a.write(t, "source.go", source("github://acme/target/target.go", "new"))
	aHead := changeSetCommit(t, a)
	b.write(t, "target.go", target("old")+"// unrelated change outside the label\n")
	bHead := changeSetCommit(t, b)
	requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
}
func TestChangeSetRemoteRemovalFindsUnchangedSource(t *testing.T) {
	for _, kind := range []string{"delete", "label"} {
		t.Run(kind, func(t *testing.T) {
			a, b, aBase, bBase := changeSetFixture(t)
			if kind == "delete" {
				if err := os.Remove(filepath.Join(b.dir, "target.go")); err != nil {
					t.Fatal(err)
				}
			} else {
				b.write(t, "target.go", strings.ReplaceAll(target("old"), "shared label", "renamed label"))
			}
			bHead := changeSetCommit(t, b)
			output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aBase, bBase, bHead), "--format=json")
			if !strings.Contains(output, "source.go") || !strings.Contains(output, "acme/source") {
				t.Fatalf("stale cross-repo reference missed: %s", output)
			}
		})
	}
}
func TestChangeSetRejectsInvalidManifestAndModes(t *testing.T) {
	a, b, aBase, bBase := changeSetFixture(t)
	path := changeSetManifest(t, a, b, aBase, aBase, bBase, bBase)
	for _, args := range [][]string{{"--fix"}, {"--staged"}, {"--diff", "HEAD"}, {"--scan", "."}, {"source.go"}, {"-"}, {"--files", "source.go"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) { requireCode(t, a, "", 2, append([]string{"--change-set", path}, args...)...) })
	}
	for _, body := range []string{"version: 1\nrepositories: []\n", "version: 99\nrepositories: []\n", "version: 1\nunknown: true\nrepositories: []\n", "version: 1\nrepositories:\n- repo: acme/source\n  path: nowhere\n  base: HEAD\n  head: HEAD\n"} {
		t.Run(body, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			requireCode(t, a, "", 2, "--change-set", path)
		})
	}
}

func TestChangeSetBidirectionalCycleAndIdenticalPaths(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	a.write(t, "source.go", source("github://acme/target/source.go", "old"))
	b.write(t, "source.go", source("github://acme/source/source.go", "old"))
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	a.write(t, "source.go", source("github://acme/target/source.go", "new"))
	aHead := changeSetCommit(t, a)
	requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
	b.write(t, "source.go", source("github://acme/source/source.go", "new"))
	bHead := changeSetCommit(t, b)
	requireCode(t, a, "", 0, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
}
func TestChangeSetConditionalRules(t *testing.T) {
	for _, rule := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
		t.Run(rule, func(t *testing.T) {
			a, b := newRepo(t), newRepo(t)
			argument := `["github://acme/target/target.go#shared label"]`
			if rule == "ForbidChange" {
				argument = `"github://acme/target/target.go#shared label"`
			}
			before := "// SENTRY." + rule + "(" + argument + ")\nvar source = 1\n"
			a.write(t, "source.go", before)
			b.write(t, "target.go", target("old"))
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			a.write(t, "source.go", strings.Replace(before, "source = 1", "source = 2", 1))
			aHead := changeSetCommit(t, a)
			unchangedCode, changedCode := 1, 0
			if rule == "ForbidChange" {
				unchangedCode, changedCode = 0, 1
			}
			requireCode(t, a, "", unchangedCode, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
			b.write(t, "target.go", target("new"))
			bHead := changeSetCommit(t, b)
			requireCode(t, a, "", changedCode, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
		})
	}
}

// LINT.IfChange(conditional_target_structure)
func TestChangeSetConditionalConfigurationChangesValidateTargets(t *testing.T) {
	for _, rule := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
		for _, kind := range []string{"prefix", "python", "policy"} {
			for _, sourceChanged := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/source-changed=%v", rule, kind, sourceChanged), func(t *testing.T) {
					a, b := newDefaultRepo(t), newDefaultRepo(t)
					path := "target.go"
					if kind == "policy" {
						if err := os.Mkdir(filepath.Join(b.dir, ".vscode"), 0700); err != nil {
							t.Fatal(err)
						}
						path = ".vscode/target.go"
					}
					targetText := "// LINT.IfChange(API)\none\n// LINT.ThenChange()\n"
					config := "directives:\n  prefix: CUSTOM\n"
					if kind == "python" {
						path = "target.py"
						targetText = "\"\"\"\nLINT.IfChange(API)\none\nLINT.ThenChange()\n\"\"\"\n"
						config = "languages:\n  python_docstrings: false\n"
					} else if kind == "policy" {
						config = "rules:\n  unknown_directive: warn\n"
					}
					remote := "github://acme/target/" + path
					argument := fmt.Sprintf("[%q]", remote+"#API")
					if rule == "ForbidChange" {
						argument = fmt.Sprintf("%q", remote+"#API")
					}
					before := "// LINT." + rule + "(" + argument + ")\nvar source = 1\n"
					a.write(t, "source.go", before)
					b.write(t, path, targetText)
					aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
					aHead := aBase
					if sourceChanged {
						a.write(t, "source.go", strings.Replace(before, "source = 1", "source = 2", 1))
						aHead = changeSetCommit(t, a)
					}
					// Only the committed configuration changes in the target repository.
					b.write(t, ".ifttt-lint.yaml", config)
					bHead := changeSetCommit(t, b)
					wantCode := 0
					if kind != "policy" || (sourceChanged && rule != "ForbidChange") {
						wantCode = 1
					}
					output := requireCode(t, a, "", wantCode, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
					if kind == "policy" {
						// Snapshot exclusions keep repository coordinates from either checkout.
						requireCode(t, b, "", wantCode, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
					}
					var report struct {
						Errors []struct{ Repository, RuleID, TargetPath, TargetLabel string }
					}
					if err := json.Unmarshal([]byte(output), &report); err != nil {
						t.Fatal(err)
					}
					if kind != "policy" {
						found := false
						for _, f := range report.Errors {
							if f.RuleID == "label_missing" && f.Repository == "acme/source" && f.TargetPath == remote && f.TargetLabel == "API" {
								found = true
							}
						}
						if !found {
							t.Fatalf("missing structural target diagnostic: %s", output)
						}
					}
					if !sourceChanged && strings.Contains(output, "require_") {
						t.Fatalf("unchanged source required a target edit: %s", output)
					}
					if strings.Contains(output, "forbid_change") {
						t.Fatalf("target configuration fabricated a forbidden body edit: %s", output)
					}
				})
			}
		}
	}
}

// LINT.ThenChange(//internal/engine/rules.go:conditional_target_structure, //internal/engine/rules.go:conditional_source_trigger, //internal/engine/engine.go:conditional_target_structure)

// LINT.IfChange(remote_revision_refs)
func TestChangeSetExplicitRefsAndMissingEvidence(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			a, b, aBase, bBase := changeSetFixture(t)
			if backend == "jj" {
				if _, err := exec.LookPath("jj"); err != nil {
					t.Skip("jj unavailable")
				}
				b.jj(t, "git", "init", "--colocate")
			}
			b.write(t, "target.go", target("new"))
			bHead := commitExample(t, b, backend)
			b.git(t, "branch", "feature/contracts", bHead)
			b.git(t, "tag", "v2.0.0", bHead)
			b.git(t, "branch", "old-contract", bBase)
			b.git(t, "tag", "v1.0.0", bBase)
			if backend == "jj" {
				// Snapshot checks do not import Git refs or alter jj operations.
				b.jj(t, "git", "import")
			}
			manifest := func(aHead string) string {
				return combinationManifest(t, []map[string]string{
					{"repo": "acme/source", "path": a.dir, "vcs": "git", "base": aBase, "head": aHead},
					{"repo": "acme/target", "path": b.dir, "vcs": backend, "base": bBase, "head": bHead},
				})
			}
			for _, tc := range []struct {
				uri  string
				code int
			}{
				{"github://acme/target/target.go", 0},
				{"github://acme/target/target.go?ref=feature/contracts", 0},
				{"github://acme/target/target.go?ref=v2.0.0", 0},
				{"github://acme/target/target.go?ref=" + bHead, 0},
				{"github://acme/target/target.go?ref=old-contract", 2},
				{"github://acme/target/target.go?ref=v1.0.0", 2},
				{"github://acme/target/target.go?ref=" + bBase, 2},
				{"github://acme/target/target.go?ref=missing-branch", 2},
				{"github://acme/absent/target.go", 2},
				{"github://acme/target/target.go?typo=HEAD", 2},
				{"github://acme/target/../outside.go", 2},
			} {
				t.Run(tc.uri, func(t *testing.T) {
					a.write(t, "source.go", source(tc.uri, "new"))
					aHead := changeSetCommit(t, a)
					requireCode(t, a, "", tc.code, "--change-set", manifest(aHead), "--format=json")
				})
			}
			invalid := manifest("does-not-exist")
			requireCode(t, a, "", 2, "--change-set", invalid)
			requireCode(t, a, "", 2, "--change-set", invalid, "--warn")
		})
	}
}

// LINT.ThenChange(//README.md:remote_revision_refs)

func TestChangeSetForeignDiagnosticOwnership(t *testing.T) {
	a, b, aBase, bBase := changeSetFixture(t)
	a.write(t, "source.go", source("github://acme/target/target.go", "new"))
	aHead := changeSetCommit(t, a)
	b.write(t, "target.go", target("new")+target("duplicate"))
	bHead := changeSetCommit(t, b)
	output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
	var report struct {
		Errors []struct{ File, Repository, HeadRevision, RuleID string }
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(b.dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Errors {
		if f.RuleID == "duplicate_label" {
			found = true
			if f.Repository != "acme/target" || f.HeadRevision != bHead || f.File != filepath.Join(canonical, "target.go") {
				t.Fatalf("foreign diagnostic misattributed: %s", output)
			}
		}
	}
	if !found || !strings.Contains(output, "label_ambiguous") {
		t.Fatalf("ambiguity not diagnosed: %s", output)
	}
}
func TestChangeSetBinaryChangesAndEmptyDeletion(t *testing.T) {
	for _, kind := range []string{"binary", "empty-delete"} {
		t.Run(kind, func(t *testing.T) {
			a, b := newRepo(t), newRepo(t)
			src := "// SENTRY.IfChange(\"SRC\")\nvar source = 1\n// SENTRY.ThenChange(\"github://acme/target/data.bin\")\n"
			a.write(t, "source.go", src)
			contents := ""
			if kind == "binary" {
				contents = "\x00old binary\x00"
			}
			b.write(t, "data.bin", contents)
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			aHead := aBase
			expected := 1
			if kind == "binary" {
				a.write(t, "source.go", strings.Replace(src, "source = 1", "source = 2", 1))
				aHead = changeSetCommit(t, a)
				b.write(t, "data.bin", "\x00new binary\x00")
				expected = 0
			} else {
				if err := os.Remove(filepath.Join(b.dir, "data.bin")); err != nil {
					t.Fatal(err)
				}
			}
			bHead := changeSetCommit(t, b)
			requireCode(t, a, "", expected, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
		})
	}
}
func TestChangeSetNativeJJReadOnly(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj unavailable")
	}
	a, b, aBase, bBase := changeSetFixture(t)
	a.write(t, "source.go", source("github://acme/target/target.go", "new"))
	aHead := changeSetCommit(t, a)
	b.write(t, "target.go", target("new"))
	bHead := changeSetCommit(t, b)
	a.jj(t, "git", "init", "--colocate")
	b.jj(t, "git", "init", "--colocate")
	manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bHead)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("vcs: git"), []byte("vcs: jj"))
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Establish operation baselines before writing dirty contents.
	opA := a.jj(t, "--ignore-working-copy", "op", "log", "--limit", "1", "--no-graph", "--template", `id`)
	opB := b.jj(t, "--ignore-working-copy", "op", "log", "--limit", "1", "--no-graph", "--template", `id`)
	a.write(t, "source.go", "uncommitted source\n")
	b.write(t, "target.go", "uncommitted target\n")
	requireCode(t, a, "", 0, "--change-set", manifest, "--format=json")
	if a.jj(t, "--ignore-working-copy", "op", "log", "--limit", "1", "--no-graph", "--template", `id`) != opA || b.jj(t, "--ignore-working-copy", "op", "log", "--limit", "1", "--no-graph", "--template", `id`) != opB {
		t.Fatal("change-set inspection modified jj operations")
	}
}

func TestChangeSetOpaqueGitDiffCannotHideContracts(t *testing.T) {
	for _, kind := range []string{"source", "renamed-target"} {
		t.Run(kind, func(t *testing.T) {
			a, b, aBase, bBase := changeSetFixture(t)
			aHead, bHead := aBase, bBase
			if kind == "source" {
				a.write(t, ".gitattributes", "source.go -diff\n")
				aBase = changeSetCommit(t, a)
				a.write(t, "source.go", source("github://acme/target/target.go", "new"))
				aHead = changeSetCommit(t, a)
			} else {
				b.write(t, ".gitattributes", "target.go -diff\n")
				bBase = changeSetCommit(t, b)
				b.write(t, "target.go", strings.ReplaceAll(target("old"), "shared label", "renamed"))
				bHead = changeSetCommit(t, b)
			}
			requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
		})
	}
}

func TestChangeSetBinaryMarkedIncomingSourcesRemainVisible(t *testing.T) {
	for _, kind := range []string{"attributes", "nul", "jj-nul"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "jj-nul" {
				if _, err := exec.LookPath("jj"); err != nil {
					t.Skip("jj unavailable")
				}
			}
			a, b, _, bBase := changeSetFixture(t)
			if kind == "attributes" {
				a.write(t, ".gitattributes", "source.go -diff\n")
			} else {
				a.write(t, "source.go", source("github://acme/target/target.go", "old")+"\x00")
			}
			aHead := changeSetCommit(t, a)
			if err := os.Remove(filepath.Join(b.dir, "target.go")); err != nil {
				t.Fatal(err)
			}
			bHead := changeSetCommit(t, b)
			manifest := changeSetManifest(t, a, b, aHead, aHead, bBase, bHead)
			if kind == "jj-nul" {
				a.jj(t, "git", "init", "--colocate")
				data, err := os.ReadFile(manifest)
				if err != nil {
					t.Fatal(err)
				}
				data = bytes.Replace(data, []byte("vcs: git"), []byte("vcs: jj"), 1)
				if err := os.WriteFile(manifest, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			output := requireCode(t, a, "", 1, "--change-set", manifest, "--format=json")
			if !strings.Contains(output, "source.go") {
				t.Fatalf("incoming source disappeared: %s", output)
			}
		})
	}
}
func TestChangeSetUnreadableSnapshotFailsClosed(t *testing.T) {
	a, b, _, bBase := changeSetFixture(t)
	a.write(t, ".gitattributes", "source.go -diff\n")
	before := source("github://acme/target/target.go", "old") + strings.Repeat("// padding\n", 3_100_000)
	a.write(t, "source.go", before)
	aBase := changeSetCommit(t, a)
	a.write(t, "source.go", strings.Replace(before, `value = "old"`, `value = "new"`, 1))
	aHead := changeSetCommit(t, a)
	_, stderr, code := a.run(t, "", "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
	if code != 2 || !strings.Contains(stderr, "limit") {
		t.Fatalf("unreadable source did not fail closed: exit=%d stderr=%s", code, stderr)
	}
}
func TestChangeSetConflictsWithEarlyExplain(t *testing.T) {
	a := newRepo(t)
	requireCode(t, a, "", 2, "--change-set", "nonexistent.yaml", "--explain", "then_missing")
	requireCode(t, a, "", 2, "--change-set", "")
}

func TestChangeSetDefaultLINTCrossLanguageContract(t *testing.T) {
	a, b := newDefaultRepo(t), newDefaultRepo(t)
	before := "// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(github://acme/target/docs/api.md#API)\n"
	beforeTarget := "<!-- LINT.IfChange(API) -->\nAPI version: 1\n<!-- LINT.ThenChange() -->\n"
	a.write(t, "source.go", before)
	if err := os.Mkdir(filepath.Join(b.dir, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	b.write(t, "docs/api.md", beforeTarget)
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	a.write(t, "source.go", strings.Replace(before, "api = 1", "api = 2", 1))
	aHead := changeSetCommit(t, a)
	output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json", "--strict=true")
	if !strings.Contains(output, "then_label_missing") {
		t.Fatalf("strict LINT remote target not evaluated: %s", output)
	}
	b.write(t, "docs/api.md", strings.Replace(beforeTarget, "version: 1", "version: 2", 1))
	bHead := changeSetCommit(t, b)
	requireCode(t, a, "", 0, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json", "--strict=true")
}

func TestChangeSetCheckoutBoundaries(t *testing.T) {
	a, b, aBase, bBase := changeSetFixture(t)
	for _, kind := range []string{"duplicate-id", "duplicate-root", "symlink-alias", "nested-root"} {
		t.Run(kind, func(t *testing.T) {
			manifest := changeSetManifest(t, a, b, aBase, aBase, bBase, bBase)
			data, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := yaml.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			entries := m["repositories"].([]any)
			second := entries[1].(map[string]any)
			switch kind {
			case "duplicate-id":
				second["repo"] = "ACME/SOURCE"
			case "duplicate-root":
				second["path"] = a.dir
			case "symlink-alias":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(a.dir, alias); err != nil {
					t.Fatal(err)
				}
				second["path"] = alias
			case "nested-root":
				nested := filepath.Join(b.dir, "nested")
				if err := os.Mkdir(nested, 0700); err != nil {
					t.Fatal(err)
				}
				second["path"] = nested
			}
			data, err = yaml.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifest, data, 0600); err != nil {
				t.Fatal(err)
			}
			requireCode(t, a, "", 2, "--change-set", manifest)
		})
	}
}

func TestChangeSetModeOnlyEditDoesNotSatisfyContract(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	before := "// SENTRY.IfChange(\"SRC\")\nvar source = 1\n// SENTRY.ThenChange(\"github://acme/target/target.go\")\n"
	a.write(t, "source.go", before)
	b.write(t, "target.go", target("old"))
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	a.write(t, "source.go", strings.Replace(before, "source = 1", "source = 2", 1))
	aHead := changeSetCommit(t, a)
	b.git(t, "update-index", "--chmod=+x", "target.go")
	b.git(t, "commit", "-qm", "mode only")
	bHead := strings.TrimSpace(b.git(t, "rev-parse", "HEAD"))
	requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
}

func TestChangeSetMixedBackendsOpaqueRegionsFailClosed(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj unavailable")
	}
	for _, kind := range []string{"source", "target", "conditional-target"} {
		t.Run(kind, func(t *testing.T) {
			a, b := newRepo(t), newRepo(t)
			before := source("github://acme/target/target.go", "old")
			if kind == "conditional-target" {
				before = "// SENTRY.RequireAny([\"github://acme/target/target.go#shared label\"])\nvar source = 1\n"
			}
			if kind == "source" {
				before += "\x00"
			}
			a.write(t, "source.go", before)
			b.write(t, "target.go", target("old")+"\x00")
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			after := strings.Replace(before, `value = "old"`, `value = "new"`, 1)
			if kind == "conditional-target" {
				after = strings.Replace(before, "source = 1", "source = 2", 1)
			}
			a.write(t, "source.go", after)
			aHead := changeSetCommit(t, a)
			b.write(t, "target.go", target("new")+"\x00")
			bHead := changeSetCommit(t, b)
			jjRepo := b
			if kind == "source" {
				jjRepo = a
			}
			jjRepo.jj(t, "git", "init", "--colocate")
			manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bHead)
			data, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := yaml.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			index := 1
			if kind == "source" {
				index = 0
			}
			m["repositories"].([]any)[index].(map[string]any)["vcs"] = "jj"
			data, err = yaml.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifest, data, 0600); err != nil {
				t.Fatal(err)
			}
			output := requireCode(t, a, "", 2, "--change-set", manifest, "--format=json", "--warn")
			if !strings.Contains(output, "change_evidence_error") {
				t.Fatalf("opaque region not diagnosed: %s", output)
			}
			requireCode(t, a, "", 2, "--change-set", manifest, "--format=json", "--list-suppressed")
		})
	}
}

// LINT.IfChange(snapshot_config)
func TestChangeSetMixedCommittedPrefixes(t *testing.T) {
	for _, backend := range []string{"git", "jj"} {
		t.Run(backend, func(t *testing.T) {
			for _, customSource := range []bool{true, false} {
				t.Run(fmt.Sprint(customSource), func(t *testing.T) {
					a, b := newExampleRepo(t, backend), newExampleRepo(t, backend)
					standard := func(remote, value string) string {
						return "// LINT.IfChange(API)\nvar value = \"" + value + "\"\n// LINT.ThenChange(" + remote + "#API)\n"
					}
					custom := func(remote, value string) string {
						return "// SENTRY.IfChange(\"API\")\nvar value = \"" + value + "\"\n// SENTRY.ThenChange(\"" + remote + "#API\")\n"
					}
					sourceText, targetText := standard, custom
					customRepo := b
					if customSource {
						sourceText, targetText, customRepo = custom, standard, a
					}
					customRepo.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\n")
					a.write(t, "source.go", sourceText("github://acme/target/target.go", "old"))
					b.write(t, "target.go", targetText("github://acme/source/source.go", "old"))
					aBase, bBase := commitExample(t, a, backend), commitExample(t, b, backend)
					a.write(t, "source.go", sourceText("github://acme/target/target.go", "new"))
					aHead := commitExample(t, a, backend)
					output := requireCode(t, a, "", 1, "--change-set", exampleChangeSetManifest(t, backend, a, b, aBase, aHead, bBase, bBase), "--format=json")
					if !strings.Contains(output, `"ruleId":"then_label_missing"`) && !strings.Contains(output, `"ruleId": "then_label_missing"`) {
						t.Fatalf("wrong missing dependency: %s", output)
					}
					b.write(t, "target.go", targetText("github://acme/source/source.go", "new"))
					bHead := commitExample(t, b, backend)
					manifest := exampleChangeSetManifest(t, backend, a, b, aBase, aHead, bBase, bHead)
					// Dirty configs and files must not change committed evidence.
					a.write(t, ".ifttt-lint.yaml", "directives: [\n")
					b.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: WRONG\n")
					a.write(t, ".gitignore", "source.go\n")
					b.write(t, ".gitignore", "target.go\n")
					requireCode(t, a, "", 0, "--change-set", manifest, "--format=json")
					requireCode(t, b, "", 0, "--change-set", manifest, "--format=json")
				})
			}
		})
	}
}

// LINT.ThenChange(//internal/changeset/run.go:snapshot_config)

func TestChangeSetMixedPrefixReverseReferences(t *testing.T) {
	for _, customSource := range []bool{true, false} {
		for _, remove := range []bool{true, false} {
			t.Run(fmt.Sprintf("custom-source=%v/delete=%v", customSource, remove), func(t *testing.T) {
				a, b := newDefaultRepo(t), newDefaultRepo(t)
				sourceText := "// LINT.IfChange(SOURCE)\none\n// LINT.ThenChange(github://acme/target/target.go#API)\n"
				targetText := "// SENTRY.Label(\"API\")\none\n// SENTRY.EndLabel\n"
				customRepo := b
				if customSource {
					customRepo = a
					sourceText = "// SENTRY.IfChange(\"SOURCE\")\none\n// SENTRY.ThenChange(\"github://acme/target/target.go#API\")\n"
					targetText = "// LINT.IfChange(API)\none\n// LINT.ThenChange()\n"
				}
				customRepo.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\n")
				a.write(t, "source.go", sourceText)
				b.write(t, "target.go", targetText)
				aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
				if remove {
					b.git(t, "rm", "target.go")
				} else {
					b.write(t, "target.go", strings.ReplaceAll(targetText, "API", "RENAMED"))
				}
				bHead := changeSetCommit(t, b)
				output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aBase, bBase, bHead), "--format=json")
				if !strings.Contains(output, "source.go") || (!strings.Contains(output, "then_missing") && !strings.Contains(output, "label_missing")) {
					t.Fatalf("incoming reference missed: %s", output)
				}
			})
		}
	}
}

func TestChangeSetMatchUsesTargetConfiguration(t *testing.T) {
	for _, policy := range []string{"ignore", "warn", "error"} {
		t.Run(policy, func(t *testing.T) {
			a, b := newDefaultRepo(t), newDefaultRepo(t)
			a.write(t, "source.txt", matchSection("A", "one")+"// LINT.Match(\":A\", \"github://acme/target/target.txt#B\")\n")
			b.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: CUSTOM\nrules:\n  unknown_directive: "+policy+"\n")
			b.write(t, "target.txt", "// CUSTOM.Label(\"B\")\none\n// CUSTOM.EndLabel\n// CUSTOM.Unknown()\n")
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			manifest := changeSetManifest(t, a, b, aBase, aBase, bBase, bBase)
			code := 0
			if policy == "error" {
				code = 1
			}
			output := requireCode(t, a, "", code, "--change-set", manifest, "--format=json")
			if policy == "error" {
				assertMatchRule(t, output, "match_target_error")
			} else if strings.Contains(output, "match_mismatch") || strings.Contains(output, "match_label_missing") {
				t.Fatalf("target prefix ignored: %s", output)
			}
			if policy == "warn" && (!strings.Contains(output, "warning") || !strings.Contains(output, "acme/target")) {
				t.Fatalf("target warning ownership: %s", output)
			}
			b.write(t, "target.txt", "// CUSTOM.Label(\"B\")\ntwo\n// CUSTOM.EndLabel\n")
			bHead := changeSetCommit(t, b)
			output = requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aBase, bBase, bHead), "--format=json")
			assertMatchRule(t, output, "match_mismatch")
		})
	}
}

func TestChangeSetCommittedPolicyAndExplicitOverride(t *testing.T) {
	a, b := newDefaultRepo(t), newDefaultRepo(t)
	a.write(t, ".ifttt-lint.yaml", "rules:\n  code_only: true\nparallelism: '1'\n")
	a.write(t, "source.go", "// LINT.IfChange(API)\n// old comment\n// LINT.ThenChange(github://acme/target/target.go#API)\n")
	b.write(t, "target.go", "// LINT.IfChange(API)\nunchanged\n// LINT.ThenChange()\n")
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	a.write(t, "source.go", "// LINT.IfChange(API)\n// new comment\n// LINT.ThenChange(github://acme/target/target.go#API)\n")
	aHead := changeSetCommit(t, a)
	manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bBase)
	a.write(t, ".ifttt-lint.yaml", "rules:\n  code_only: false\n")
	requireCode(t, a, "", 0, "--change-set", manifest, "--format=json")
	for _, threads := range []string{"--p", "--parallel", "--threads", "--t"} {
		requireCode(t, a, "", 1, "--change-set", manifest, "--format=json", "--code-only=false", threads, "2")
	}
	for _, ignore := range []string{"--ignore", "-i"} {
		requireCode(t, a, "", 0, "--change-set", manifest, "--format=json", "--code-only=false", ignore, "source.go")
	}
	// A target config edit must never count as an edit to the linked body.
	b.write(t, ".ifttt-lint.yaml", "rules:\n  unknown_directive: warn\n")
	bHead := changeSetCommit(t, b)
	output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json", "--code-only=false")
	assertMatchRule(t, output, "then_label_missing")
	a.write(t, ".ifttt-lint.yaml", "ignores: [./source.go]\n")
	aIgnored := changeSetCommit(t, a)
	requireCode(t, a, "", 0, "--change-set", changeSetManifest(t, a, b, aBase, aIgnored, bBase, bHead), "--format=json")
}

// LINT.IfChange(snapshot_ignore_policy)
func TestChangeSetIgnorePolicyUsesEachCommittedRepository(t *testing.T) {
	for _, policy := range []string{"ignores: [skipped/**]\n", "skip_directories: [skipped]\n"} {
		t.Run(strings.TrimSpace(policy), func(t *testing.T) {
			a, b := newDefaultRepo(t), newDefaultRepo(t)
			for _, r := range []repo{a, b} {
				for _, dir := range []string{"skipped", ".vscode", ".local"} {
					if err := os.Mkdir(filepath.Join(r.dir, dir), 0700); err != nil {
						t.Fatal(err)
					}
				}
				r.write(t, ".gitignore", ".local/\n")
				r.write(t, ".ifttt-lint.yaml", policy)
				r.write(t, "skipped/source.go", "// LINT.IfChange(SKIP)\none\n// LINT.ThenChange()\n")
				r.write(t, ".vscode/source.go", "// LINT.IfChange(EDITOR)\none\n// LINT.ThenChange()\n")
			}
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			for _, r := range []repo{a, b} {
				r.write(t, "skipped/source.go", "// LINT.IfChange(BROKEN)\n")
			}
			aHead, bHead := changeSetCommit(t, a), changeSetCommit(t, b)
			manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bHead)
			for _, r := range []repo{a, b} {
				r.write(t, ".ifttt-lint.yaml", "ignores: [.vscode/**]\n")
				r.write(t, ".local/artifact.go", "// LINT.IfChange(BROKEN)\n")
			}
			requireCode(t, a, "", 0, "--change-set", manifest, "--format=json")
			// A dirty or committed Git ignore rule cannot hide a tracked contract.
			b.write(t, ".vscode/source.go", "// LINT.IfChange(BROKEN)\n")
			b.write(t, ".ifttt-lint.yaml", policy)
			b.write(t, ".gitignore", ".local/\n.vscode/\n")
			bHead = changeSetCommit(t, b)
			out := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
			if !strings.Contains(out, "orphan_if") || !strings.Contains(out, "acme/target") || strings.Contains(out, "skipped/source.go") || strings.Contains(out, "artifact.go") {
				t.Fatalf("snapshot ignore policy: %s", out)
			}
		})
	}
}

// LINT.ThenChange(//internal/changeset/run.go:snapshot_ignore_policy, //docs/cross-repository.md:snapshot_ignore_policy)

func TestChangeSetRejectsCommittedInvalidConfiguration(t *testing.T) {
	for _, body := range []string{"directives: [\n", "parallelism: invalid\n", "rules:\n  unknown_directive: typo\n"} {
		t.Run(body, func(t *testing.T) {
			a, b := newDefaultRepo(t), newDefaultRepo(t)
			a.write(t, ".ifttt-lint.yaml", body)
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			a.write(t, ".ifttt-lint.yaml", "")
			_, stderr, code := a.run(t, "", "--change-set", changeSetManifest(t, a, b, aBase, aBase, bBase, bBase), "--format=json")
			if code != 2 || !strings.Contains(stderr, "acme/source") || !strings.Contains(stderr, ".ifttt-lint.yaml") {
				t.Fatalf("invalid committed config: exit %d, %s", code, stderr)
			}
		})
	}
}

func TestChangeSetPythonSettingsBelongToTarget(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			a, b := newRepo(t), newDefaultRepo(t)
			a.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: SENTRY\nlanguages:\n  python_docstrings: false\n")
			b.write(t, ".ifttt-lint.yaml", fmt.Sprintf("languages:\n  python_docstrings: %v\n", enabled))
			a.write(t, "source.go", "// SENTRY.IfChange(\"SOURCE\")\none\n// SENTRY.ThenChange(\"github://acme/target/target.py#API\")\n")
			b.write(t, "target.py", "\"\"\"\nLINT.IfChange(API)\none\nLINT.ThenChange()\n\"\"\"\n")
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			a.write(t, "source.go", "// SENTRY.IfChange(\"SOURCE\")\ntwo\n// SENTRY.ThenChange(\"github://acme/target/target.py#API\")\n")
			aHead := changeSetCommit(t, a)
			output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
			rule := "label_missing"
			if enabled {
				rule = "then_label_missing"
			}
			assertMatchRule(t, output, rule)
		})
	}
}

func TestChangeSetMixedPrefixesWithJJ(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj unavailable")
	}
	a, b := newDefaultRepo(t), newRepo(t)
	a.write(t, "source.go", "// LINT.IfChange(API)\none\n// LINT.ThenChange(github://acme/target/target.go#API)\n")
	b.write(t, "target.go", "// SENTRY.Label(\"API\")\none\n// SENTRY.EndLabel\n")
	aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
	a.write(t, "source.go", "// LINT.IfChange(API)\ntwo\n// LINT.ThenChange(github://acme/target/target.go#API)\n")
	aHead := changeSetCommit(t, a)
	b.jj(t, "git", "init", "--colocate")
	manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bBase)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["repositories"].([]any)[1].(map[string]any)["vcs"] = "jj"
	data, err = yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	op := b.jj(t, "--ignore-working-copy", "op", "log", "--limit", "1", "--no-graph", "--template", `id`)
	b.write(t, ".ifttt-lint.yaml", "directives: [\n")
	output := requireCode(t, a, "", 1, "--change-set", manifest, "--format=json")
	assertMatchRule(t, output, "then_label_missing")
	if op != b.jj(t, "--ignore-working-copy", "op", "log", "--limit", "1", "--no-graph", "--template", `id`) {
		t.Fatal("snapshot config read changed jj operations")
	}
}

func TestChangeSetConfigurationBoundaries(t *testing.T) {
	for _, symlink := range []bool{true, false} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			a, b := newDefaultRepo(t), newDefaultRepo(t)
			if symlink {
				a.write(t, "real-config.yaml", "directives:\n  prefix: WRONG\n")
				if err := os.Symlink("real-config.yaml", filepath.Join(a.dir, ".ifttt-lint.yaml")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(filepath.Dir(a.dir), ".ifttt-lint.yaml"), []byte("directives: [\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(a.dir, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
				a.write(t, "nested/.ifttt-lint.yaml", "directives: [\n")
				a.write(t, "nested/source.go", "// LINT.IfChange(API)\none\n// LINT.ThenChange(github://acme/target/target.go#API)\n")
				b.write(t, "target.go", "// LINT.IfChange(API)\none\n// LINT.ThenChange()\n")
			}
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			if symlink {
				_, stderr, code := a.run(t, "", "--change-set", changeSetManifest(t, a, b, aBase, aBase, bBase, bBase))
				if code != 2 || !strings.Contains(stderr, "regular blob") {
					t.Fatalf("symlink config accepted: exit %d, %s", code, stderr)
				}
			} else {
				a.write(t, "nested/source.go", "// LINT.IfChange(API)\ntwo\n// LINT.ThenChange(github://acme/target/target.go#API)\n")
				aHead := changeSetCommit(t, a)
				output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bBase), "--format=json")
				assertMatchRule(t, output, "then_label_missing")
			}
		})
	}
}

// LINT.IfChange(snapshot_config_changes)
func TestChangeSetConfigurationChangesRecheckIncomingReferences(t *testing.T) {
	for _, kind := range []string{"prefix", "python"} {
		t.Run(kind, func(t *testing.T) {
			a, b := newDefaultRepo(t), newDefaultRepo(t)
			path := "target.go"
			if kind == "prefix" {
				b.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: CUSTOM\n")
				b.write(t, path, "// CUSTOM.Label(\"API\")\none\n// CUSTOM.EndLabel\n")
			} else {
				path = "target.py"
				b.write(t, path, "\"\"\"\nLINT.IfChange(API)\none\nLINT.ThenChange()\n\"\"\"\n")
			}
			a.write(t, "source.go", "// LINT.IfChange(SOURCE)\none\n// LINT.ThenChange(github://acme/target/"+path+"#API)\n")
			aBase, bBase := changeSetCommit(t, a), changeSetCommit(t, b)
			if kind == "prefix" {
				b.write(t, ".ifttt-lint.yaml", "directives:\n  prefix: LINT\n")
				b.write(t, path, "// LINT.IfChange(RENAMED)\none\n// LINT.ThenChange()\n")
			} else {
				b.write(t, ".ifttt-lint.yaml", "languages:\n  python_docstrings: false\n")
			}
			bHead := changeSetCommit(t, b)
			output := requireCode(t, a, "", 1, "--change-set", changeSetManifest(t, a, b, aBase, aBase, bBase, bHead), "--format=json")
			assertMatchRule(t, output, "label_missing")
		})
	}
}

// LINT.ThenChange(//internal/changeset/run.go:snapshot_config_changes)
