package vcs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func snapshotTools(t *testing.T) {
	t.Helper()
	tools, err := filepath.Abs("../../build/tools")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type snapshotFixture struct{ root, base, head, path string }

func makeSnapshotFixture(t *testing.T, kind string) snapshotFixture {
	t.Helper()
	return makeSnapshotFixtureWithIndexMetadata(t, kind, runtime.GOOS == "windows")
}

func makeSnapshotFixtureWithIndexMetadata(t *testing.T, kind string, indexMetadata bool) snapshotFixture {
	t.Helper()
	snapshotTools(t)
	root := t.TempDir()
	nativeJJ := kind == "jj" && !indexMetadata
	if !nativeJJ {
		command(t, root, "git", "init", "-q")
		command(t, root, "git", "config", "user.name", "Test")
		command(t, root, "git", "config", "user.email", "test@example.invalid")
	} else {
		command(t, root, "jj", "git", "init")
		command(t, root, "jj", "config", "set", "--repo", "user.name", "Test")
		command(t, root, "jj", "config", "set", "--repo", "user.email", "test@example.invalid")
	}
	path := "a space\n[abc].go"
	if indexMetadata {
		// Windows disallows control characters in filenames. Unicode still
		// exercises Git's byte quoting and both backends' NUL-delimited paths.
		path = "a space [abc]☃.go"
		command(t, root, "git", "config", "core.filemode", "false")
		command(t, root, "git", "config", "core.symlinks", "false")
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(path, "// LINT.IfChange(old)\nold\n")
	write("become-link", "regular\n")
	write("binary", "\x00\x01\x02")
	write("empty", "")
	write("mode", "unchanged\n")
	write("plain", "plain\n")
	commit := func(label string) string {
		t.Helper()
		if !nativeJJ {
			command(t, root, "git", "add", ".")
			if indexMetadata && label == "head" {
				// Commit real symlink and executable entries without requiring
				// symlink privileges or Unix permission bits on the filesystem.
				blob := strings.TrimSpace(command(t, root, "git", "hash-object", "-w", "become-link"))
				command(t, root, "git", "update-index", "--cacheinfo", "120000", blob, "become-link")
				command(t, root, "git", "update-index", "--chmod=+x", "mode")
			}
			command(t, root, "git", "commit", "-qm", label)
			return strings.TrimSpace(command(t, root, "git", "rev-parse", "HEAD"))
		}
		command(t, root, "jj", "describe", "-m", label)
		return command(t, root, "jj", "--ignore-working-copy", "log", "--no-graph", "-r", "@", "-T", "commit_id")
	}
	base := commit("base")
	if nativeJJ {
		command(t, root, "jj", "new")
	}
	write(path, "// LINT.IfChange(head)\nhead\n")
	for _, path := range []string{"binary", "empty", "become-link"} {
		if err := os.Remove(filepath.Join(root, path)); err != nil {
			t.Fatal(err)
		}
	}
	if indexMetadata {
		write("become-link", "plain")
	} else {
		if err := os.Symlink("plain", filepath.Join(root, "become-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, "mode"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	head := commit("head")
	if kind == "jj" && indexMetadata {
		command(t, root, "jj", "git", "init", "--colocate")
		command(t, root, "jj", "config", "set", "--repo", "user.name", "Test")
		command(t, root, "jj", "config", "set", "--repo", "user.email", "test@example.invalid")
		command(t, root, "jj", "--ignore-working-copy", "edit", head)
	}
	write(path, "dirty, no directives\n")
	write("plain", "// LINT.IfChange(dirty)\n")
	write("untracked", "// LINT.IfChange(untracked)\n")
	return snapshotFixture{root: root, base: base, head: head, path: path}
}

func TestSnapshotsUseCommittedTreesWithoutMutatingWorkingCopy(t *testing.T) {
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			f := makeSnapshotFixture(t, kind)
			ctx := context.Background()
			operation := ""
			if kind == "jj" {
				operation = command(t, f.root, "jj", "--ignore-working-copy", "op", "log", "--no-graph", "-n", "1", "-T", "id")
			}
			b, err := OpenSnapshot(ctx, f.root, "auto")
			if err != nil {
				t.Fatal(err)
			}
			if b.Kind != kind {
				t.Fatalf("kind: %s", b.Kind)
			}
			rev := "HEAD"
			if kind == "jj" {
				rev = "@"
			}
			id, err := b.ResolveRevision(ctx, rev)
			if err != nil || id != f.head {
				t.Fatalf("resolve: %q %v", id, err)
			}
			data, err := b.ReadFileAt(ctx, id, f.path)
			if err != nil || string(data) != "// LINT.IfChange(head)\nhead\n" {
				t.Fatalf("read: %q %v", data, err)
			}
			files, err := b.DirectiveFilesAt(ctx, id, "LINT.")
			if err != nil || !reflect.DeepEqual(files, []string{f.path}) {
				t.Fatalf("files: %q %v", files, err)
			}
			files, err = b.DirectiveFilesAt(ctx, id, "absent needle")
			if err != nil || len(files) != 0 {
				t.Fatalf("empty files: %q %v", files, err)
			}
			_, err = b.Diff(ctx, Request{Base: f.base, Revision: id})
			if err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(filepath.Join(f.root, f.path))
			if err != nil || string(data) != "dirty, no directives\n" {
				t.Fatalf("working copy mutated: %q %v", data, err)
			}
			if kind == "jj" {
				after := command(t, f.root, "jj", "--ignore-working-copy", "op", "log", "--no-graph", "-n", "1", "-T", "id")
				if after != operation {
					t.Fatalf("operation changed: %s -> %s", operation, after)
				}
			}
		})
	}
}

func TestSnapshotChangesIncludeBinaryEmptyDeletionAndModeChanges(t *testing.T) {
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			f := makeSnapshotFixture(t, kind)
			b, err := OpenSnapshot(context.Background(), f.root, kind)
			if err != nil {
				t.Fatal(err)
			}
			changes, err := b.ChangesAt(context.Background(), f.base, f.head)
			if err != nil {
				t.Fatal(err)
			}
			want := []SnapshotChange{{Path: f.path}, {Path: "become-link", TypeChanged: true}, {Path: "binary", Deleted: true}, {Path: "empty", Deleted: true}, {Path: "mode"}}
			if !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes: %#v want %#v", changes, want)
			}
		})
	}
}

// Exercise the Windows fixture strategy on every platform so its imported jj
// trees cannot silently lose symlink, executable, or quoted-path coverage.
func TestSnapshotIndexMetadataPreservesPortableFixtureCoverage(t *testing.T) {
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			f := makeSnapshotFixtureWithIndexMetadata(t, kind, true)
			ctx := context.Background()
			b, err := OpenSnapshot(ctx, f.root, "auto")
			if err != nil {
				t.Fatal(err)
			}
			if b.Kind != kind {
				t.Fatalf("kind: %s, want %s", b.Kind, kind)
			}
			rev := "HEAD"
			if kind == "jj" {
				rev = "@"
			}
			if id, err := b.ResolveRevision(ctx, rev); err != nil || id != f.head {
				t.Fatalf("head: %q, want %s: %v", id, f.head, err)
			}
			if data, err := b.ReadFileAt(ctx, f.head, f.path); err != nil || string(data) != "// LINT.IfChange(head)\nhead\n" {
				t.Fatalf("quoted path: %q %v", data, err)
			}
			if files, err := b.DirectiveFilesAt(ctx, f.head, "LINT."); err != nil || !reflect.DeepEqual(files, []string{f.path}) {
				t.Fatalf("directive paths: %q %v", files, err)
			}
			changes, err := b.ChangesAt(ctx, f.base, f.head)
			want := []SnapshotChange{{Path: f.path}, {Path: "become-link", TypeChanged: true}, {Path: "binary", Deleted: true}, {Path: "empty", Deleted: true}, {Path: "mode"}}
			if err != nil || !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes: %#v, want %#v: %v", changes, want, err)
			}
			if _, err := b.ReadFileAt(ctx, f.head, "become-link"); err == nil {
				t.Fatal("symlink accepted")
			}
			for _, id := range []string{f.base, f.head} {
				if data, err := b.ReadFileAt(ctx, id, "mode"); err != nil || string(data) != "unchanged\n" {
					t.Fatalf("mode-only change has changed contents: %q %v", data, err)
				}
			}
		})
	}
}

func TestSnapshotReadsRejectNonRegularMissingAndTraversal(t *testing.T) {
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			f := makeSnapshotFixture(t, kind)
			b, err := OpenSnapshot(context.Background(), f.root, kind)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			data, err := b.ReadFileAt(ctx, f.base, "become-link")
			if err != nil || string(data) != "regular\n" {
				t.Fatalf("base regular: %q %v", data, err)
			}
			if _, err = b.ReadFileAt(ctx, f.head, "become-link"); err == nil {
				t.Fatal("symlink accepted")
			}
			if _, err = b.ReadFileAt(ctx, f.head, "missing"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("missing: %v", err)
			}
			for _, path := range []string{"../plain", "/plain", "./plain", "dir/../plain", "plain\x00"} {
				if _, err = b.ReadFileAt(ctx, f.head, path); err == nil {
					t.Fatalf("unsafe path %q accepted", path)
				}
			}
			badRevs := []string{"--output=bad", "bad\x00rev", "nonexistent-revision"}
			if kind == "jj" {
				badRevs = append(badRevs, "@ | @-", "none()")
			} else {
				badRevs = append(badRevs, "HEAD..HEAD~1")
			}
			for _, rev := range badRevs {
				if _, err = b.ResolveRevision(ctx, rev); err == nil {
					t.Fatalf("bad revision %q accepted", rev)
				}
			}
			if _, err = b.DirectiveFilesAt(ctx, "nonexistent-revision", "LINT."); err == nil {
				t.Fatal("query error hidden")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err = b.ReadFileAt(cancelled, f.head, "plain"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
}

func TestSnapshotGitlinksAndSubmoduleIgnoreConfig(t *testing.T) {
	for _, kind := range []string{"git", "jj"} {
		t.Run(kind, func(t *testing.T) {
			snapshotTools(t)
			root := t.TempDir()
			command(t, root, "git", "init", "-q")
			command(t, root, "git", "config", "user.name", "Test")
			command(t, root, "git", "config", "user.email", "test@example.invalid")
			if err := os.WriteFile(filepath.Join(root, "source"), []byte("base\n"), 0644); err != nil {
				t.Fatal(err)
			}
			command(t, root, "git", "add", ".")
			command(t, root, "git", "commit", "-qm", "base")
			base := strings.TrimSpace(command(t, root, "git", "rev-parse", "HEAD"))
			command(t, root, "git", "update-index", "--add", "--cacheinfo", "160000", base, "module")
			command(t, root, "git", "commit", "-qm", "gitlink")
			head := strings.TrimSpace(command(t, root, "git", "rev-parse", "HEAD"))
			command(t, root, "git", "config", "diff.ignoreSubmodules", "all")
			if kind == "jj" {
				command(t, root, "jj", "git", "init", "--colocate")
			}
			b, err := OpenSnapshot(context.Background(), root, kind)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.ReadFileAt(context.Background(), head, "module"); err == nil {
				t.Fatal("gitlink accepted as a blob")
			}
			changes, err := b.ChangesAt(context.Background(), base, head)
			want := []SnapshotChange{{Path: "module"}}
			if err != nil || !reflect.DeepEqual(changes, want) {
				t.Fatalf("gitlink change hidden: %#v %v", changes, err)
			}
		})
	}
}

func TestSnapshotPathsRejectPortableTraversal(t *testing.T) {
	for _, p := range []string{"", ".", "..", "../plain", "/plain", "./plain", "dir/../plain", "plain\x00", `dir\..\plain`, `C:\plain`, "C:/plain", "C:plain"} {
		if err := snapshotPath(p); err == nil {
			t.Errorf("unsafe portable path %q accepted", p)
		}
	}
	for _, p := range []string{"plain", "dir/entry", "a space\n[abc].go", "quote\"file", "-filename", "control\tfile"} {
		if err := snapshotPath(p); err != nil {
			t.Errorf("valid snapshot path %q: %v", p, err)
		}
	}
}

func TestSnapshotJJConflictsFailClosed(t *testing.T) {
	snapshotTools(t)
	root := t.TempDir()
	command(t, root, "jj", "git", "init")
	command(t, root, "jj", "config", "set", "--repo", "user.name", "Test")
	command(t, root, "jj", "config", "set", "--repo", "user.email", "test@example.invalid")
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "conflict"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	id := func() string {
		return command(t, root, "jj", "--ignore-working-copy", "log", "--no-graph", "-r", "@", "-T", "commit_id")
	}
	write("base\n")
	command(t, root, "jj", "describe", "-m", "base")
	base := id()
	command(t, root, "jj", "new", base)
	write("left\n")
	command(t, root, "jj", "describe", "-m", "left")
	left := id()
	command(t, root, "jj", "new", base)
	write("right\n")
	command(t, root, "jj", "describe", "-m", "right")
	right := id()
	command(t, root, "jj", "new", left, right)
	head := id()
	b, err := OpenSnapshot(context.Background(), root, "jj")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadFileAt(context.Background(), head, "conflict"); err == nil {
		t.Fatal("conflict accepted as regular file")
	}
	if _, err := b.DirectiveFilesAt(context.Background(), head, "LINT."); err == nil {
		t.Fatal("conflict hidden by directive query")
	}
}

func TestSnapshotGitCommitIDsIgnoreMutableReplacementRefs(t *testing.T) {
	f := makeSnapshotFixture(t, "git")
	command(t, f.root, "git", "replace", f.head, f.base)
	b, err := OpenSnapshot(context.Background(), f.root, "git")
	if err != nil {
		t.Fatal(err)
	}
	data, err := b.ReadFileAt(context.Background(), f.head, f.path)
	if err != nil || string(data) != "// LINT.IfChange(head)\nhead\n" {
		t.Fatalf("pinned ID followed mutable replacement: %q %v", data, err)
	}
}

// Loading jj's latest repository view can merge divergent operation heads even
// when --ignore-working-copy is set. Snapshot commands must not create that op.
func TestSnapshotJJDivergentOperationsNeverMerge(t *testing.T) {
	actions := []struct {
		name string
		call func(context.Context, *Backend, snapshotFixture) error
	}{
		{"open", func(ctx context.Context, _ *Backend, f snapshotFixture) error {
			_, err := OpenSnapshot(ctx, f.root, "jj")
			return err
		}},
		{"resolve", func(ctx context.Context, b *Backend, f snapshotFixture) error {
			_, err := b.ResolveRevision(ctx, f.head)
			return err
		}},
		{"read", func(ctx context.Context, b *Backend, f snapshotFixture) error {
			_, err := b.ReadFileAt(ctx, f.head, "plain")
			return err
		}},
		{"directives", func(ctx context.Context, b *Backend, f snapshotFixture) error {
			_, err := b.DirectiveFilesAt(ctx, f.head, "LINT.")
			return err
		}},
		{"changes", func(ctx context.Context, b *Backend, f snapshotFixture) error {
			_, err := b.ChangesAt(ctx, f.base, f.head)
			return err
		}},
		{"diff", func(ctx context.Context, b *Backend, f snapshotFixture) error {
			_, err := b.Diff(ctx, Request{Base: f.base, Revision: f.head})
			return err
		}},
	}
	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			f := makeSnapshotFixture(t, "jj")
			operation := command(t, f.root, "jj", "--ignore-working-copy", "op", "log", "--no-graph", "-n", "1", "-T", "id")
			command(t, f.root, "jj", "--at-operation", operation, "describe", "-m", "left")
			command(t, f.root, "jj", "--at-operation", operation, "describe", "-m", "right")
			heads, err := os.ReadDir(filepath.Join(f.root, ".jj", "repo", "op_heads", "heads"))
			if err != nil {
				t.Fatal(err)
			}
			if len(heads) != 2 {
				t.Fatalf("fixture needs two divergent operation heads, got %d", len(heads))
			}
			operationFiles := func() map[string]string {
				t.Helper()
				files := make(map[string]string)
				for _, directory := range []string{"op_heads", "op_store"} {
					base := filepath.Join(f.root, ".jj", "repo", directory)
					err := filepath.WalkDir(base, func(p string, entry fs.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if entry.IsDir() {
							return nil
						}
						data, err := os.ReadFile(p)
						if err != nil {
							return err
						}
						relative, err := filepath.Rel(f.root, p)
						if err != nil {
							return err
						}
						files[relative] = string(data)
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				return files
			}
			before := operationFiles()
			b := &Backend{Kind: "jj", Root: f.root, readOnly: true}
			err = action.call(context.Background(), b, f)
			after := operationFiles()
			if !reflect.DeepEqual(before, after) {
				t.Errorf("snapshot %s modified jj operation files (before %d files, after %d)", action.name, len(before), len(after))
			}
			if action.name != "open" && err == nil {
				t.Errorf("snapshot %s accepted divergent operation heads", action.name)
			}
		})
	}
}

func TestSnapshotGitAmbiguousSymbolicRefsFailClosed(t *testing.T) {
	f := makeSnapshotFixture(t, "git")
	command(t, f.root, "git", "tag", "snapshot-name", f.base)
	command(t, f.root, "git", "branch", "snapshot-name", f.head)
	b, err := OpenSnapshot(context.Background(), f.root, "git")
	if err != nil {
		t.Fatal(err)
	}
	for _, warnings := range []string{"true", "false"} {
		t.Run("warnAmbiguousRefs="+warnings, func(t *testing.T) {
			command(t, f.root, "git", "config", "core.warnAmbiguousRefs", warnings)
			for _, rev := range []string{"snapshot-name", "snapshot-name^0", "snapshot-name^{commit}"} {
				if id, err := b.ResolveRevision(context.Background(), rev); err == nil {
					t.Errorf("ambiguous revision %q accepted as %s", rev, id)
				}
			}
			for rev, want := range map[string]string{"refs/heads/snapshot-name": f.head, "refs/tags/snapshot-name": f.base, "refs/heads/snapshot-name~1": f.base, "HEAD~1": f.base, "HEAD": f.head, "@": f.head, f.head[:10]: f.head} {
				id, err := b.ResolveRevision(context.Background(), rev)
				if err != nil || id != want {
					t.Errorf("unambiguous revision %q: got %s, want %s: %v", rev, id, want, err)
				}
			}
		})
	}
}

func TestSnapshotGitHexNamedAmbiguousRefsFailClosed(t *testing.T) {
	f := makeSnapshotFixture(t, "git")
	command(t, f.root, "git", "tag", "deadbeef", f.base)
	command(t, f.root, "git", "branch", "deadbeef", f.head)
	command(t, f.root, "git", "config", "core.warnAmbiguousRefs", "false")
	b, err := OpenSnapshot(context.Background(), f.root, "git")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := b.ResolveRevision(context.Background(), "deadbeef"); err == nil {
		t.Fatalf("hex-named ambiguous revision accepted as %s", id)
	}
}

func TestSnapshotGitObjectAbbreviationAndRefCollisionFailsClosed(t *testing.T) {
	for _, sameCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("sameCommit=%t", sameCommit), func(t *testing.T) {
			f := makeSnapshotFixture(t, "git")
			token := f.head[:10]
			branchTarget := f.base
			if sameCommit {
				branchTarget = f.head
			}
			command(t, f.root, "git", "branch", token, branchTarget)
			b, err := OpenSnapshot(context.Background(), f.root, "git")
			if err != nil {
				t.Fatal(err)
			}
			for _, warnings := range []string{"true", "false"} {
				command(t, f.root, "git", "config", "core.warnAmbiguousRefs", warnings)
				if id, err := b.ResolveRevision(context.Background(), token); err == nil {
					t.Errorf("object/ref collision %q accepted as %s (warnings %s)", token, id, warnings)
				}
			}
			for rev, want := range map[string]string{f.head: f.head, "refs/heads/" + token: branchTarget, "refs/heads/" + token + "^0": branchTarget} {
				id, err := b.ResolveRevision(context.Background(), rev)
				if err != nil || id != want {
					t.Errorf("explicit revision %q: got %s want %s: %v", rev, id, want, err)
				}
			}
		})
	}
}

func TestSnapshotReusesVerifiedCommitIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command recorder")
	}
	f := makeSnapshotFixture(t, "git")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	recorder := t.TempDir()
	log := filepath.Join(recorder, "commands")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$IFTTT_TEST_COMMAND_LOG\"\nexec \"$IFTTT_TEST_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(recorder, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IFTTT_TEST_COMMAND_LOG", log)
	t.Setenv("IFTTT_TEST_REAL_GIT", realGit)
	t.Setenv("PATH", recorder+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend, err := OpenSnapshot(context.Background(), f.root, "git")
	if err != nil {
		t.Fatal(err)
	}
	id, err := backend.ResolveRevision(context.Background(), f.head)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		data, err := backend.ReadFileAt(context.Background(), id, "plain")
		if err != nil || string(data) != "plain\n" {
			t.Fatalf("snapshot read: %q, %v", data, err)
		}
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(commands), "rev-parse --verify"); count != 1 {
		t.Fatalf("immutable ID resolved %d times; expected once: %s", count, commands)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.ResolveRevision(cancelled, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached ID ignored cancellation: %v", err)
	}
	command(t, f.root, "git", "branch", "--force", "moving", f.base)
	first, err := backend.ResolveRevision(context.Background(), "moving")
	if err != nil {
		t.Fatal(err)
	}
	command(t, f.root, "git", "branch", "--force", "moving", f.head)
	second, err := backend.ResolveRevision(context.Background(), "moving")
	if err != nil || first != f.base || second != f.head {
		t.Fatalf("mutable name cached: %s, %s, %v", first, second, err)
	}
	if _, err := backend.ResolveRevision(context.Background(), strings.Repeat("f", 40)); err == nil {
		t.Fatal("unverified ID was trusted")
	}
}
