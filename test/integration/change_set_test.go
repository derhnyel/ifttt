package integration

import (
	"bytes"
	"encoding/json"
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
	a, b, aBase, bBase := changeSetFixture(t)
	a.write(t, "source.go", source("github://acme/target/target.go", "new"))
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
	if len(report.Errors) != 1 || report.Errors[0].Repository != "acme/source" || report.Errors[0].HeadRevision != aHead || report.Errors[0].BaseRevision != aBase || report.Errors[0].File != filepath.Join(canonicalRoot, "source.go") || report.Errors[0].TargetPath != "github://acme/target/target.go" || report.Errors[0].RuleID != "then_label_missing" {
		t.Fatalf("missing repository-aware dependency diagnostic: %s", output)
	}
	b.write(t, "target.go", target("new"))
	bHead := changeSetCommit(t, b)
	manifest := changeSetManifest(t, a, b, aBase, aHead, bBase, bHead)
	a.write(t, "source.go", "// SENTRY.IfChange(\"broken working file\")\n")
	b.write(t, "target.go", "dirty working contents without labels\n")
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
func TestChangeSetExplicitRefsAndMissingEvidence(t *testing.T) {
	a, b, aBase, bBase := changeSetFixture(t)
	b.write(t, "target.go", target("new"))
	bHead := changeSetCommit(t, b)
	for _, tc := range []struct {
		uri  string
		code int
	}{
		{"github://acme/target/target.go?ref=" + bHead, 0},
		{"github://acme/target/target.go?ref=" + bBase, 2},
		{"github://acme/absent/target.go", 2},
		{"github://acme/target/target.go?typo=HEAD", 2},
		{"github://acme/target/../outside.go", 2},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			a.write(t, "source.go", source(tc.uri, "new"))
			aHead := changeSetCommit(t, a)
			requireCode(t, a, "", tc.code, "--change-set", changeSetManifest(t, a, b, aBase, aHead, bBase, bHead), "--format=json")
		})
	}
	invalid := changeSetManifest(t, a, b, aBase, "does-not-exist", bBase, bHead)
	requireCode(t, a, "", 2, "--change-set", invalid)
	requireCode(t, a, "", 2, "--change-set", invalid, "--warn")
}
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
