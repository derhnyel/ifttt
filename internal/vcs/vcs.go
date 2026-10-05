// Package vcs supplies Git-format patches and repository metadata for native
// Git and Jujutsu workflows. Shell commands are never constructed from revisions.
package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Request struct {
	Revision, Base string
	Staged, Review bool
}
type Backend struct {
	Kind, Root string
	readOnly   bool
}

func Detect(cwd string) (string, string, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".jj")); err == nil && info.IsDir() {
			return "jj", dir, nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "git", dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", errors.New("no Git or Jujutsu repository found; use a diff file or stdin outside a repository")
}
func Open(ctx context.Context, cwd, kind string) (*Backend, error) {
	return openBackend(ctx, cwd, kind, false)
}
func openBackend(ctx context.Context, cwd, kind string, readOnly bool) (*Backend, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "auto" {
		var err error
		kind, _, err = Detect(cwd)
		if err != nil {
			return nil, err
		}
	}
	if kind != "git" && kind != "jj" {
		return nil, fmt.Errorf("vcs must be auto, git or jj, got %q", kind)
	}
	b := &Backend{Kind: kind, Root: cwd, readOnly: readOnly}
	var root string
	var err error
	if kind == "git" {
		root, err = b.run(ctx, "rev-parse", "--show-toplevel")
	} else {
		root, err = b.run(ctx, "root")
	}
	if err != nil {
		return nil, err
	}
	b.Root = strings.TrimRight(root, "\r\n")
	if b.Root == "" {
		return nil, fmt.Errorf("%s returned an empty repository root", kind)
	}
	return b, nil
}
func validRevision(rev string) error {
	if strings.HasPrefix(strings.TrimSpace(rev), "-") || strings.ContainsRune(rev, 0) {
		return fmt.Errorf("invalid revision %q", rev)
	}
	return nil
}
func (b *Backend) Diff(ctx context.Context, r Request) (string, error) {
	if err := validRevision(r.Revision); err != nil {
		return "", err
	}
	if err := validRevision(r.Base); err != nil {
		return "", err
	}
	if r.Staged && (r.Revision != "" || r.Base != "" || r.Review) {
		return "", errors.New("staged mode cannot be combined with a revision or base")
	}
	if b.Kind == "jj" {
		if r.Staged {
			return "", errors.New("Jujutsu has no staging area; use a revision or working-copy diff")
		}
		args := []string{"diff", "--git"}
		if r.Base != "" {
			rev := r.Revision
			if rev == "" {
				rev = "@"
			}
			args = append(args, "--from", r.Base, "--to", rev)
		} else {
			rev := r.Revision
			if rev == "" {
				rev = "@"
			}
			args = append(args, "--revisions", rev)
		}
		out, err := b.run(ctx, args...)
		if err != nil && strings.Contains(r.Revision, "HEAD") {
			return "", fmt.Errorf("%w; Git refs require --vcs git; jj expects revsets such as main..@", err)
		}
		return out, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=all", "--src-prefix=a/", "--dst-prefix=b/", "--binary"}
	if b.readOnly {
		// Attributes such as -diff must not hide contract lines in snapshots.
		args = append(args, "--text")
	}
	rev := r.Revision
	if r.Base != "" {
		if rev == "" {
			rev = "HEAD"
		}
		rev = r.Base + ".." + rev
	}
	if r.Review && rev == "" {
		rev = "HEAD"
	}
	if r.Review && r.Base == "" && !strings.Contains(rev, "..") {
		return b.run(ctx, "show", "--format=", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=all", "--src-prefix=a/", "--dst-prefix=b/", "--binary", rev, "--")
	}
	if r.Staged {
		args = append(args, "--cached")
	}
	if rev != "" {
		args = append(args, rev)
	}
	args = append(args, "--")
	return b.run(ctx, args...)
}
func (b *Backend) Files(ctx context.Context) ([]string, error) {
	var out string
	var err error
	if b.Kind == "git" {
		out, err = b.runWithOutputLimit(ctx, 128<<20, "ls-files", "-z")
	} else {
		out, err = b.runWithOutputLimit(ctx, 128<<20, "file", "list", "--template", `path ++ "\0"`)
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			p = filepath.FromSlash(p)
			info, statErr := os.Lstat(filepath.Join(b.Root, p))
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return nil, statErr
			}
			if info.IsDir() {
				continue
			} // A Git submodule entry is not a source blob.
			files = append(files, p)
		}
	}
	return files, nil
}
func (b *Backend) Messages(ctx context.Context, r Request) (string, error) {
	if r.Staged {
		return "", nil
	}
	rev := r.Revision
	if b.Kind == "jj" {
		if r.Base != "" {
			if rev == "" {
				rev = "@"
			}
			rev = r.Base + ".." + rev
		}
		if rev == "" {
			rev = "@"
		}
		if err := validRevision(rev); err != nil {
			return "", err
		}
		return b.run(ctx, "log", "--no-graph", "--template", `description ++ "\n"`, "--revisions", rev)
	}
	if rev == "" && !r.Review {
		return "", nil
	}
	if rev == "" {
		rev = "HEAD"
	}
	if r.Base != "" {
		rev = r.Base + ".." + rev
	}
	if err := validRevision(rev); err != nil {
		return "", err
	}
	if strings.Contains(rev, "..") {
		return b.run(ctx, "log", "--format=%B", rev, "--")
	}
	// A lone revision diffs its tree against the working copy, but suppression
	// should cover only that revision, not unrelated historical descriptions.
	return b.run(ctx, "log", "-1", "--format=%B", rev, "--")
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	limitErr error
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Len() int       { return b.buffer.Len() }
func (b *boundedBuffer) String() string { return b.buffer.String() }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.limitErr = fmt.Errorf("VCS output exceeds %d MiB limit", b.limit>>20)
		if b.cancel != nil {
			b.cancel()
		}
		return 0, b.limitErr
	}
	return b.buffer.Write(p)
}
func (b *Backend) run(ctx context.Context, args ...string) (string, error) {
	return b.runWithOutputLimit(ctx, 32<<20, args...)
}

// Tracked path lists can exceed patch/output budgets in large repositories.
// Their larger bound remains explicit; other VCS commands retain 32 MiB.
func (b *Backend) runWithOutputLimit(ctx context.Context, outputLimit int, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if b.Kind == "jj" {
		if b.readOnly {
			// Ignore working-copy snapshots and reject divergent operation heads.
			// Loading the default latest view would otherwise merge and write an op.
			args = append([]string{"--ignore-working-copy", "--at-operation=@"}, args...)
		}
		args = append([]string{"--no-pager", "--color", "never"}, args...)
	}
	cmd := exec.CommandContext(ctx, b.Kind, args...)
	cmd.Dir = b.Root
	cmd.WaitDelay = time.Second
	stdout := &boundedBuffer{limit: outputLimit, cancel: cancel}
	stderr := &boundedBuffer{limit: 1 << 20, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0")
	if b.readOnly && b.Kind == "git" {
		cmd.Env = append(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1")
	}
	err := cmd.Run()
	if stdout.limitErr != nil {
		return "", stdout.limitErr
	}
	if stderr.limitErr != nil {
		return "", stderr.limitErr
	}
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return "", fmt.Errorf("%s %s failed: %w: %s", b.Kind, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

var _ io.Writer = (*boundedBuffer)(nil)

// DirectiveFiles performs one fixed-string needle query for Git repositories.
// Git tracks the ignore policy; jj's tracked-file listing is searched in one pass.
func (b *Backend) DirectiveFiles(ctx context.Context, needle string) ([]string, error) {
	if b.Kind == "git" {
		output, err := b.run(ctx, "grep", "-I", "-l", "-z", "--fixed-strings", "-e", needle, "--", ".")
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				return nil, nil
			}
			return nil, err
		}
		var files []string
		for _, p := range strings.Split(output, "\x00") {
			if p != "" {
				files = append(files, filepath.FromSlash(p))
			}
		}
		return files, nil
	}
	tracked, err := b.Files(ctx)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range tracked {
		target := filepath.Join(b.Root, p)
		resolved, e := filepath.EvalSymlinks(target)
		if e != nil {
			return nil, e
		}
		root, e := filepath.EvalSymlinks(b.Root)
		if e != nil {
			return nil, e
		}
		relative, e := filepath.Rel(root, resolved)
		if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		data, e := os.ReadFile(target)
		if e != nil {
			return nil, e
		}
		if bytes.Contains(data, []byte(needle)) {
			files = append(files, p)
		}
	}
	return files, nil
}
