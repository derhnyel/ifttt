package scan

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestFindDirectiveFiles(t *testing.T) {
	dir := t.TempDir()
	makeFile := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	makeFile("a.txt", "no directives here")
	makeFile("b.txt", "SENTRY.IfChange")
	makeFile("sub/c.txt", "prefix SENTRY.ThenChange\n")
	makeFile("skip/d.txt", "SENTRY.Label")

	files, err := FindDirectiveFiles(dir, "SENTRY.", runtime.NumCPU(), []string{"skip", ".git", ".hg", ".svn"})
	if err != nil {
		t.Fatalf("FindDirectiveFiles: %v", err)
	}
	want := map[string]bool{
		filepath.Join(dir, "b.txt"):     true,
		filepath.Join(dir, "sub/c.txt"): true,
	}
	if len(files) != len(want) {
		t.Fatalf("expected %d matches, got %d (%v)", len(want), len(files), files)
	}
	for _, f := range files {
		if !want[f] {
			t.Fatalf("unexpected file %s in result", f)
		}
	}
}

func TestFindDirectiveFilesEmptyNeedle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.go"), []byte("// LINT.IfChange(API)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := FindDirectiveFiles(dir, "", 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
}

func TestFindDirectiveFilesSkipDefaultsAndOverride(t *testing.T) {
	dir := t.TempDir()
	makeFile := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	makeFile(".git/config", "# SENTRY.IfChange")

	files, err := FindDirectiveFiles(dir, "SENTRY.", runtime.NumCPU(), nil)
	if err != nil {
		t.Fatalf("FindDirectiveFiles nil skip: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected default skips to exclude .git, got %v", files)
	}

	files, err = FindDirectiveFiles(dir, "SENTRY.", runtime.NumCPU(), []string{})
	if err != nil {
		t.Fatalf("FindDirectiveFiles empty skip: %v", err)
	}
	if len(files) != 1 || files[0] != filepath.Join(dir, ".git", "config") {
		t.Fatalf("expected .git file when skip dirs empty, got %v", files)
	}
}

func TestFindDirectiveFilesUsesRipgrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("custom rg shim uses shell script")
	}

	dir := t.TempDir()
	makeFile := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	makeFile("a.txt", "no directives here")
	makeFile("b.txt", "SENTRY.IfChange")
	makeFile("sub/c.txt", "prefix SENTRY.ThenChange\n")

	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	scriptPath := filepath.Join(binDir, "rg")
	script := "#!/bin/sh\nfor root do :; done\nprintf \"%s\\0%s\\0\" \"$root/b.txt\" \"$root/sub/c.txt\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	pathEnv := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", pathEnv)

	files, err := FindDirectiveFiles(dir, "SENTRY.", runtime.NumCPU(), nil)
	if err != nil {
		t.Fatalf("FindDirectiveFiles: %v", err)
	}
	want := map[string]bool{
		filepath.Join(dir, "b.txt"):     true,
		filepath.Join(dir, "sub/c.txt"): true,
	}
	if len(files) != len(want) {
		t.Fatalf("expected %d matches, got %d (%v)", len(want), len(files), files)
	}
	for _, f := range files {
		if !want[f] {
			t.Fatalf("unexpected file %s in result", f)
		}
	}
}

func BenchmarkFindDirectiveFilesFallback(b *testing.B) {
	dir := createBenchmarkDir(b, 5000, 10)
	originalPath := os.Getenv("PATH")
	b.Cleanup(func() { os.Setenv("PATH", originalPath) })
	os.Setenv("PATH", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FindDirectiveFiles(dir, "SENTRY.", runtime.NumCPU(), nil); err != nil {
			b.Fatalf("FindDirectiveFiles fallback: %v", err)
		}
	}
}

func BenchmarkFindDirectiveFilesRipgrep(b *testing.B) {
	if _, err := exec.LookPath("rg"); err != nil {
		b.Skip("ripgrep unavailable")
	}
	dir := createBenchmarkDir(b, 5000, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		files, err := FindDirectiveFiles(dir, "SENTRY.", runtime.NumCPU(), nil)
		if err != nil || len(files) != 500 {
			b.Fatalf("matches=%d err=%v", len(files), err)
		}
	}
}

func createBenchmarkDir(tb testing.TB, files int, directiveEvery int) string {
	tb.Helper()
	dir := tb.TempDir()
	for i := 0; i < files; i++ {
		name := filepath.Join(dir, "dir", fmt.Sprintf("file_%05d.txt", i))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			tb.Fatalf("mkdir: %v", err)
		}
		content := "plain text"
		if directiveEvery > 0 && i%directiveEvery == 0 {
			content = "SENTRY.IfChange\n" + content
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			tb.Fatalf("write file: %v", err)
		}
	}
	return dir
}

func TestRipgrepLiteralNeedleAndNestedSkips(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	dir := t.TempDir()
	for name, data := range map[string]string{"match.txt": "SENTRY.IfChange", "false.txt": "SENTRYxIfChange", "skip/nested/file.txt": "SENTRY.IfChange"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, ok := tryRipgrep(dir, "SENTRY.", map[string]struct{}{"skip": {}, ".git": {}})
	if !ok {
		t.Fatal("ripgrep failed")
	}
	if len(files) != 1 || files[0] != filepath.Join(dir, "match.txt") {
		t.Fatalf("unexpected matches: %v", files)
	}
}

func TestDiscoveryBackendsAgreeOnHiddenFilesIgnoresAndSymlinks(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	root := t.TempDir()
	for name, data := range map[string]string{".hidden.go": "SENTRY.IfChange", "ignored.go": "SENTRY.IfChange", ".gitignore": "ignored.go\n", "normal.go": "SENTRY.IfChange"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "normal.go"), filepath.Join(root, "link.go")); err != nil {
		t.Skip(err)
	}
	fast, err := FindDirectiveFiles(root, "SENTRY.", 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	fallback, err := FindDirectiveFiles(root, "SENTRY.", 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(fast) != fmt.Sprint(fallback) || len(fast) != 2 {
		t.Fatalf("rg=%v walk=%v", fast, fallback)
	}
}

func TestDiscoveryIgnoreRulesWithEachBackend(t *testing.T) {
	for _, backend := range []string{"git", "ripgrep", "walk"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			for name, data := range map[string]string{
				".gitignore":          "cache/\n*.tmp\n!keep.tmp\n/root-only.go\n",
				"cache/artifact.go":   "LINT.IfChange",
				"drop.tmp":            "LINT.IfChange",
				"keep.tmp":            "LINT.IfChange",
				"root-only.go":        "LINT.IfChange",
				"nested/root-only.go": "LINT.IfChange",
				"nested/.gitignore":   "*.go\n!keep.go\n",
				"nested/keep.go":      "LINT.IfChange",
				"source.go":           "LINT.IfChange",
				".hidden.go":          "LINT.IfChange",
			} {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if backend == "git" {
				if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
					t.Fatalf("git init: %v %s", err, out)
				}
			} else if backend == "ripgrep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable")
				}
			} else {
				t.Setenv("PATH", "")
			}
			var files []string
			var err error
			if backend == "ripgrep" {
				var ok bool
				files, ok = tryRipgrep(root, "LINT.", map[string]struct{}{".git": {}})
				if !ok {
					t.Fatal("ripgrep failed; fallback would hide a backend regression")
				}
			} else {
				files, err = FindDirectiveFiles(root, "LINT.", 2, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, path := range files {
				rel, _ := filepath.Rel(root, path)
				got = append(got, filepath.ToSlash(rel))
			}
			sort.Strings(got)
			if fmt.Sprint(got) != fmt.Sprint([]string{".hidden.go", "keep.tmp", "nested/keep.go", "source.go"}) {
				t.Fatalf("%s discovery ignored rules: %v", backend, got)
			}
		})
	}
}

func TestDiscoveryPreservesIgnoredTrackedFiles(t *testing.T) {
	for _, backend := range []string{"git", "ripgrep-only", "walk"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range map[string]string{"tracked/source.go": "LINT.IfChange", "tracked/untracked.go": "LINT.IfChange"} {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"init", "-q"}, {"add", "tracked/source.go"}} {
				if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			}
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("tracked/\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if backend == "ripgrep-only" {
				rg, err := exec.LookPath("rg")
				if err != nil {
					t.Skip("ripgrep unavailable")
				}
				bin := t.TempDir()
				name := "rg"
				if runtime.GOOS == "windows" {
					name += ".exe"
				}
				if err := os.Symlink(rg, filepath.Join(bin, name)); err != nil {
					t.Skip(err)
				}
				t.Setenv("PATH", bin)
			} else if backend == "walk" {
				t.Setenv("PATH", "")
			}
			files, err := FindDirectiveFiles(root, "LINT.", 2, nil)
			if err != nil || len(files) != 1 || files[0] != filepath.Join(root, "tracked/source.go") {
				t.Fatalf("tracked contract omitted or ignored untracked file included: %v %v", files, err)
			}
		})
	}
}

func TestDiscoveryConfiguredGlobGrammarAndExtraIgnoreFiles(t *testing.T) {
	for _, backend := range []string{"git", "ripgrep", "walk"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			for _, name := range []string{"a.go", "[ab].go", "{a,b}.go"} {
				if err := os.WriteFile(name, []byte("LINT.IfChange"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(".ignore", []byte("a.go\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if backend == "git" {
				if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			} else if backend == "walk" {
				t.Setenv("PATH", "")
			}
			files, err := FindDirectiveFiles(".", "LINT.", 2, nil, "[ab].go", "{a,b}.go")
			if err != nil || len(files) != 1 || files[0] != "a.go" {
				t.Fatalf("native glob syntax changed CLI exclusions: %v %v", files, err)
			}
			files, err = FindDirectiveFiles(".", "LINT.", 2, nil)
			if err != nil || len(files) != 3 {
				t.Fatalf(".ignore must not change the Git-ignore policy: %v %v", files, err)
			}
		})
	}
}

func TestDiscoveryStandardGitExcludesWithAndWithoutGit(t *testing.T) {
	for _, backend := range []string{"git", "walk"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git: %v %s", err, out)
			}
			global := filepath.Join(t.TempDir(), "ignore")
			if err := os.WriteFile(global, []byte("global.go\n"), 0600); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(config, []byte("[core]\nexcludesfile = "+filepath.ToSlash(global)+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", config)
			if err := os.WriteFile(filepath.Join(root, ".git/info/exclude"), []byte("local.go\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"global.go", "local.go", "source.go"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("LINT.IfChange"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if backend == "walk" {
				t.Setenv("PATH", "")
			}
			files, err := FindDirectiveFiles(root, "LINT.", 2, nil)
			if err != nil || len(files) != 1 || filepath.Base(files[0]) != "source.go" {
				t.Fatalf("standard excludes changed across backends: %v %v", files, err)
			}
		})
	}
}

func TestFallbackFindsDirectivesAcrossBufferBoundaryAndLongLine(t *testing.T) {
	t.Setenv("PATH", "")
	for _, offset := range []int{65534, 3 * 1024 * 1024} {
		root := t.TempDir()
		path := filepath.Join(root, "long.go")
		data := append(bytes.Repeat([]byte("x"), offset), []byte("SENTRY.IfChange")...)
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		files, err := FindDirectiveFiles(root, "SENTRY.", 1, nil)
		if err != nil || len(files) != 1 {
			t.Fatalf("offset=%d files=%v err=%v", offset, files, err)
		}
	}
}

func TestDefaultScanSkipsJujutsuMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".jj", "metadata.go"), []byte("// LINT.IfChange(metadata)"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := FindDirectiveFiles(root, "LINT.", 0, nil)
	if err != nil || len(files) != 0 {
		t.Fatalf("metadata files %v: %v", files, err)
	}
}

// LINT.IfChange(scan_local_artifacts)
func TestDefaultScanSkipsLocalArtifactsWithBothBackends(t *testing.T) {
	for _, backend := range []string{"ripgrep", "walk"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "ripgrep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable")
				}
			} else {
				t.Setenv("PATH", "")
			}
			root := t.TempDir()
			for _, name := range []string{".gocache", "node_modules"} {
				if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name, "artifact.go"), []byte("// LINT.IfChange(artifact)\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, ".source.go")
			if err := os.WriteFile(path, []byte("// LINT.IfChange(source)\n"), 0600); err != nil {
				t.Fatal(err)
			}
			files, err := FindDirectiveFiles(root, "LINT.", 2, nil)
			if err != nil || len(files) != 1 || files[0] != path {
				t.Fatalf("default scan includes local artifacts: files=%v err=%v", files, err)
			}
			files, err = FindDirectiveFiles(root, "LINT.", 2, []string{})
			if err != nil || len(files) != 3 {
				t.Fatalf("explicit override must allow artifact directories: files=%v err=%v", files, err)
			}
		})
	}
}

// LINT.ThenChange(//internal/scan/scan.go:scan_local_artifacts)
