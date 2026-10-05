package vcs

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// SnapshotChange records tree changes independently of textual patch hunks.
// TypeChanged distinguishes transitions such as regular files becoming symlinks.
type SnapshotChange struct {
	Path        string
	Deleted     bool
	TypeChanged bool
}

// OpenSnapshot opens a read-only backend. Every jj invocation, including root
// discovery and Diff, ignores the working copy and uses --at-operation=@ to
// reject divergent operation heads rather than automatically merging them.
func OpenSnapshot(ctx context.Context, cwd, kind string) (*Backend, error) {
	return openBackend(ctx, cwd, kind, true)
}

// ResolveRevision pins a revision to exactly one immutable commit object ID.
func (b *Backend) ResolveRevision(ctx context.Context, rev string) (string, error) {
	if err := validRevision(rev); err != nil {
		return "", err
	}
	if strings.TrimSpace(rev) == "" {
		return "", errors.New("snapshot revision must not be empty")
	}
	var out string
	var err error
	snapshot := *b
	snapshot.readOnly = true
	if b.Kind == "git" {
		out, err = snapshot.run(ctx, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
		out = strings.TrimSpace(out)
	} else {
		out, err = snapshot.run(ctx, "log", "--no-graph", "--revisions", rev, "--template", `commit_id ++ "\0"`)
		if err == nil {
			ids, e := nulFields(out)
			if e != nil {
				return "", e
			}
			if len(ids) != 1 {
				return "", fmt.Errorf("snapshot revision %q resolves to %d commits; expected exactly one", rev, len(ids))
			}
			out = ids[0]
		}
	}
	if err != nil {
		return "", err
	}
	decoded, err := hex.DecodeString(out)
	if err != nil || (len(decoded) != 20 && len(decoded) != 32) {
		return "", fmt.Errorf("%s returned an invalid immutable commit ID for %q", b.Kind, rev)
	}
	if b.Kind == "git" {
		if err := snapshot.ensureGitRevisionUnambiguous(ctx, rev, out); err != nil {
			return "", err
		}
	}
	return out, nil
}

// Git resolves ambiguous branch/tag names successfully and reports the problem
// only on stderr. Inspect the canonical symbolic reference for the base token
// instead, preserving existing ancestor, peeling, and reflog expressions.
func (b *Backend) ensureGitRevisionUnambiguous(ctx context.Context, rev, id string) error {
	end := len(rev)
	if i := strings.IndexAny(rev, "^~:"); i >= 0 {
		end = i
	}
	if i := strings.Index(rev, "@{"); i >= 0 && i < end {
		end = i
	}
	token := rev[:end]
	if token == "" {
		return nil
	} // Implicit reflog or commit-message search.
	if token == "@" {
		token = "HEAD"
	}
	if strings.EqualFold(token, id) {
		return nil
	} // Exact immutable object ID.
	canonical, err := b.run(ctx, "-c", "core.warnAmbiguousRefs=true", "rev-parse", "--verify", "--symbolic-full-name", "--end-of-options", token)
	if err != nil {
		return err
	}
	canonical = strings.TrimSpace(canonical)
	if strings.Trim(token, "0123456789abcdefABCDEF") != "" {
		if canonical != "" {
			return nil
		}
		return fmt.Errorf("ambiguous Git snapshot revision %q", rev)
	}
	// Hexadecimal tokens can mean both an object abbreviation and a symbolic
	// ref. Canonical symbolic output alone does not identify that ambiguity.
	candidates := []string{token, "refs/" + token, "refs/tags/" + token, "refs/heads/" + token, "refs/remotes/" + token, "refs/remotes/" + token + "/HEAD"}
	args := append([]string{"for-each-ref", "--format=%(refname)", "--"}, candidates...)
	refs, err := b.run(ctx, args...)
	if err != nil {
		return err
	}
	names := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		names[candidate] = true
	}
	refCount := 0
	for _, name := range strings.Split(refs, "\n") {
		if names[name] {
			refCount++
		}
	}
	objects, err := b.run(ctx, "rev-parse", "--disambiguate="+token)
	if err != nil {
		return err
	}
	objectCount := len(strings.Fields(objects))
	if refCount > 1 || objectCount > 1 || (objectCount > 0 && (refCount > 0 || canonical != "")) {
		return fmt.Errorf("ambiguous Git snapshot revision %q", rev)
	}
	if canonical != "" || (refCount == 0 && objectCount == 1) {
		return nil
	}
	return fmt.Errorf("ambiguous Git snapshot revision %q", rev)
}

func snapshotPath(p string) error {
	drivePrefix := len(p) >= 2 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':'
	if p == "" || drivePrefix || strings.ContainsRune(p, '\\') || path.IsAbs(p) || strings.ContainsRune(p, 0) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Errorf("invalid snapshot path %q", p)
	}
	return nil
}

// jj string literals accept byte escapes rather than Go's Unicode escapes.
func jjLiteral(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case c < 32 || c == 127:
			fmt.Fprintf(&out, "\\x%02x", c)
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('"')
	return out.String()
}

func nulFields(out string) ([]string, error) {
	if out == "" {
		return nil, nil
	}
	if !strings.HasSuffix(out, "\x00") {
		return nil, errors.New("invalid unterminated snapshot metadata")
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

// ReadFileAt reads regular blobs from the pinned tree, never the working copy.
// A missing path wraps fs.ErrNotExist; other kinds and unsafe paths are errors.
func (b *Backend) ReadFileAt(ctx context.Context, rev, p string) ([]byte, error) {
	if err := snapshotPath(p); err != nil {
		return nil, err
	}
	id, err := b.ResolveRevision(ctx, rev)
	if err != nil {
		return nil, err
	}
	snapshot := *b
	snapshot.readOnly = true
	if b.Kind == "git" {
		out, err := snapshot.run(ctx, "ls-tree", "-z", id, "--", ":(literal)"+p)
		if err != nil {
			return nil, err
		}
		entries, err := nulFields(out)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("snapshot %s path %q: %w", id, p, fs.ErrNotExist)
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("snapshot path %q is not one regular blob", p)
		}
		metadata, name, ok := strings.Cut(entries[0], "\t")
		fields := strings.Fields(metadata)
		if !ok || name != p || len(fields) != 3 {
			return nil, fmt.Errorf("invalid Git snapshot metadata for %q", p)
		}
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return nil, fmt.Errorf("snapshot path %q is not a regular blob", p)
		}
		data, err := snapshot.run(ctx, "cat-file", "blob", fields[2])
		if err != nil {
			return nil, err
		}
		return []byte(data), nil
	}
	fileset := "root-file:" + jjLiteral(p)
	out, err := snapshot.run(ctx, "file", "list", "--revision", id, "--template", `path ++ "\0" ++ file_type ++ "\0"`, "--", fileset)
	if err != nil {
		return nil, err
	}
	entries, err := nulFields(out)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("snapshot %s path %q: %w", id, p, fs.ErrNotExist)
	}
	if len(entries) != 2 || entries[0] != p || entries[1] != "file" {
		return nil, fmt.Errorf("snapshot path %q is not one regular blob", p)
	}
	return snapshot.readJJFileAtID(ctx, id, p)
}

// The caller verifies the immutable revision and regular entry before reading.
func (b *Backend) readJJFileAtID(ctx context.Context, id, p string) ([]byte, error) {
	data, err := b.run(ctx, "file", "show", "--revision", id, "--template", `""`, "--", "root-file:"+jjLiteral(p))
	if err != nil {
		return nil, err
	}
	return []byte(data), nil
}

// DirectiveFilesAt discovers directive-bearing regular files at a pinned head.
// Git uses one fixed-string grep; jj searches native snapshot blobs.
func (b *Backend) DirectiveFilesAt(ctx context.Context, rev, needle string) ([]string, error) {
	if strings.ContainsRune(needle, 0) {
		return nil, errors.New("snapshot needle must not contain NUL")
	}
	id, err := b.ResolveRevision(ctx, rev)
	if err != nil {
		return nil, err
	}
	snapshot := *b
	snapshot.readOnly = true
	if b.Kind == "git" {
		// Contracts in otherwise binary-marked blobs must remain discoverable.
		out, err := snapshot.runWithOutputLimit(ctx, 128<<20, "grep", "--text", "-l", "-z", "--full-name", "--fixed-strings", "-e", needle, id, "--")
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				return nil, nil
			}
			return nil, err
		}
		entries, err := nulFields(out)
		if err != nil {
			return nil, err
		}
		var files []string
		prefix := id + ":"
		for _, entry := range entries {
			if !strings.HasPrefix(entry, prefix) {
				return nil, errors.New("invalid Git snapshot grep metadata")
			}
			p := strings.TrimPrefix(entry, prefix)
			if err := snapshotPath(p); err != nil {
				return nil, err
			}
			files = append(files, p)
		}
		return files, nil
	}
	out, err := snapshot.runWithOutputLimit(ctx, 128<<20, "file", "list", "--revision", id, "--template", `path ++ "\0" ++ file_type ++ "\0"`)
	if err != nil {
		return nil, err
	}
	entries, err := nulFields(out)
	if err != nil {
		return nil, err
	}
	if len(entries)%2 != 0 {
		return nil, errors.New("invalid jj snapshot file metadata")
	}
	var files []string
	for i := 0; i < len(entries); i += 2 {
		p, kind := entries[i], entries[i+1]
		if err := snapshotPath(p); err != nil {
			return nil, err
		}
		if kind == "conflict" {
			return nil, fmt.Errorf("snapshot path %q is conflicted", p)
		}
		if kind != "file" {
			continue
		}
		data, err := snapshot.readJJFileAtID(ctx, id, p)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(data, []byte(needle)) {
			files = append(files, p)
		}
	}
	return files, nil
}

// ChangesAt returns authoritative changed-path evidence between pinned trees,
// including binary changes, empty deletions, and mode-only modifications.
func (b *Backend) ChangesAt(ctx context.Context, base, head string) ([]SnapshotChange, error) {
	baseID, err := b.ResolveRevision(ctx, base)
	if err != nil {
		return nil, err
	}
	headID, err := b.ResolveRevision(ctx, head)
	if err != nil {
		return nil, err
	}
	snapshot := *b
	snapshot.readOnly = true
	var out string
	if b.Kind == "git" {
		out, err = snapshot.runWithOutputLimit(ctx, 128<<20, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=none", "--name-status", "-z", baseID, headID, "--")
	} else {
		out, err = snapshot.runWithOutputLimit(ctx, 128<<20, "diff", "--from", baseID, "--to", headID, "--template", `status ++ "\0" ++ source.path() ++ "\0" ++ target.path() ++ "\0" ++ if(source.file_type() != target.file_type(), "1", "0") ++ "\0"`)
	}
	if err != nil {
		return nil, err
	}
	entries, err := nulFields(out)
	if err != nil {
		return nil, err
	}
	var changes []SnapshotChange
	if b.Kind == "git" {
		if len(entries)%2 != 0 {
			return nil, errors.New("invalid Git changed-path metadata")
		}
		for i := 0; i < len(entries); i += 2 {
			status, p := entries[i], entries[i+1]
			if err := snapshotPath(p); err != nil {
				return nil, err
			}
			if status != "A" && status != "M" && status != "D" && status != "T" {
				return nil, fmt.Errorf("unsupported Git snapshot status %q", status)
			}
			changes = append(changes, SnapshotChange{Path: p, Deleted: status == "D", TypeChanged: status == "T"})
		}
	} else {
		if len(entries)%4 != 0 {
			return nil, errors.New("invalid jj changed-path metadata")
		}
		for i := 0; i < len(entries); i += 4 {
			status, source, target, typeChanged := entries[i], entries[i+1], entries[i+2], entries[i+3]
			if err := snapshotPath(target); err != nil {
				return nil, err
			}
			if typeChanged != "0" && typeChanged != "1" {
				return nil, errors.New("invalid jj file type metadata")
			}
			switch status {
			case "removed":
				changes = append(changes, SnapshotChange{Path: target, Deleted: true})
			case "added", "modified", "copied", "renamed":
				changes = append(changes, SnapshotChange{Path: target, TypeChanged: status == "modified" && typeChanged == "1"})
				if status == "renamed" {
					if err := snapshotPath(source); err != nil {
						return nil, err
					}
					changes = append(changes, SnapshotChange{Path: source, Deleted: true})
				}
			default:
				return nil, fmt.Errorf("unsupported jj snapshot status %q", status)
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}
