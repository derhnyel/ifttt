package changeset

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/engine"
	"github.com/derhnyel/ifttt/internal/vcs"
)

// Run validates every source repository against the same pinned change set.
// No command reads source contents from the working tree or contacts the network.
func Run(ctx context.Context, manifest Manifest, options engine.Options) (engine.Result, int, error) {
	result := engine.Result{Stats: map[string]any{}}
	repositories, err := prepare(ctx, manifest)
	if err != nil {
		return result, 2, err
	}
	factory := &snapshotFactory{ctx: ctx, repositories: map[string]*snapshot{}, targets: map[string]boundTarget{}}
	reverse := false
	for _, repository := range repositories {
		factory.repositories[repository.entry.Repo] = repository
		for _, change := range repository.changes {
			if change.Deleted || change.Renamed || change.TypeChanged || change.Opaque || removedLabel(change) {
				reverse = true
			}
		}
	}
	code := 0
	metadata := make([]map[string]string, 0, len(repositories))
	for _, repository := range repositories {
		opts := options
		opts.Files = repository.files
		opts.Factories = []engine.FileProviderFactory{factory}
		opts.RevisionChanges = repository.changes
		opts.DependencyChanges = factory.changes
		opts.ReverseCandidates = []string{}
		opts.Repository = nil
		opts.Fix = false
		opts.SourceFiles = nil
		opts.StructuralFiles = nil
		if reverse {
			candidates, err := repository.backend.DirectiveFilesAt(ctx, repository.head, core.CurrentDirectiveSyntax().PrefixDot)
			if err != nil {
				return result, 2, fmt.Errorf("repository %s reverse discovery: %w", repository.entry.Repo, err)
			}
			opts.ReverseCandidates = candidates
		}
		res, current := engine.Lint("", opts)
		if current > code {
			code = current
		}
		for _, collection := range []*[]core.Finding{&res.Findings, &res.Suppressed} {
			for i := range *collection {
				finding := &(*collection)[i]
				owner := repository
				name := finding.File
				if strings.Contains(name, "://") {
					bound := factory.lookup(name)
					if bound.err != nil {
						return result, 2, bound.err
					}
					owner = bound.snapshot
					name = bound.path
				}
				if !filepath.IsAbs(name) {
					name = filepath.Join(owner.backend.Root, filepath.FromSlash(name))
				}
				finding.File = name
				finding.Repository = owner.entry.Repo
				finding.BaseRevision = owner.base
				finding.HeadRevision = owner.head
				if finding.TargetPath != "" && !strings.Contains(finding.TargetPath, "://") && !filepath.IsAbs(finding.TargetPath) {
					finding.TargetPath = filepath.Join(owner.backend.Root, finding.TargetPath)
				}
			}
		}
		result.Findings = append(result.Findings, res.Findings...)
		result.Suppressed = append(result.Suppressed, res.Suppressed...)
		metadata = append(metadata, map[string]string{"repo": repository.entry.Repo, "root": repository.backend.Root, "vcs": repository.backend.Kind, "base": repository.base, "head": repository.head})
	}
	for _, repository := range repositories {
		if err := repository.files.evidenceError(); err != nil {
			return result, 2, fmt.Errorf("repository %s snapshot read: %w", repository.entry.Repo, err)
		}
	}
	for _, finding := range result.Findings {
		if finding.RuleID == "change_evidence_error" {
			code = 2
		}
	}
	if err := factory.evidenceError(); err != nil {
		return result, 2, err
	}
	result.Findings = deduplicate(result.Findings)
	result.Suppressed = deduplicate(result.Suppressed)
	result.Stats["change_set_version"] = manifest.Version
	result.Stats["repositories"] = metadata
	return result, code, nil
}
func prepare(ctx context.Context, manifest Manifest) ([]*snapshot, error) {
	var repositories []*snapshot
	var roots []os.FileInfo
	// Pin every endpoint before obtaining any source diff or file contents.
	for _, entry := range manifest.Repositories {
		backend, err := vcs.OpenSnapshot(ctx, entry.Path, entry.VCS)
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", entry.Repo, err)
		}
		root, err := filepath.EvalSymlinks(backend.Root)
		if err != nil {
			return nil, err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		requested, err := os.Stat(entry.Path)
		if err != nil {
			return nil, err
		}
		if !os.SameFile(info, requested) {
			return nil, fmt.Errorf("repository %s path must name its checkout root %s", entry.Repo, root)
		}
		for _, existing := range roots {
			if os.SameFile(info, existing) {
				return nil, fmt.Errorf("duplicate change-set checkout %s", root)
			}
		}
		roots = append(roots, info)
		backend.Root = root
		base, err := backend.ResolveRevision(ctx, entry.Base)
		if err != nil {
			return nil, fmt.Errorf("repository %s base: %w", entry.Repo, err)
		}
		head, err := backend.ResolveRevision(ctx, entry.Head)
		if err != nil {
			return nil, fmt.Errorf("repository %s head: %w", entry.Repo, err)
		}
		repository := &snapshot{entry: entry, backend: backend, base: base, head: head}
		repository.files = &snapshotFiles{ctx: ctx, snapshot: repository, cache: map[string]snapshotEntry{}}
		repositories = append(repositories, repository)
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].entry.Repo < repositories[j].entry.Repo })
	for _, repository := range repositories {
		patch, err := repository.backend.Diff(ctx, vcs.Request{Base: repository.base, Revision: repository.head})
		if err != nil {
			return nil, fmt.Errorf("repository %s diff: %w", repository.entry.Repo, err)
		}
		changes, err := engine.ParseChangedLines(patch)
		if err != nil {
			return nil, fmt.Errorf("repository %s diff: %w", repository.entry.Repo, err)
		}
		paths, err := repository.backend.ChangesAt(ctx, repository.base, repository.head)
		if err != nil {
			return nil, fmt.Errorf("repository %s changed paths: %w", repository.entry.Repo, err)
		}
		for _, item := range paths {
			name := item.Path
			change := changes[name]
			if change == nil {
				change = &core.FileChanges{File: name}
				changes[name] = change
			}
			change.ContentChanged = true
			change.Deleted = item.Deleted
			change.TypeChanged = item.TypeChanged
			if !change.Deleted && !change.TypeChanged && len(change.AddedLines)+len(change.RemovedLines) == 0 {
				// Native binary diffs and mode-only changes need explicit blob evidence.
				head, err := repository.files.ReadFile(name)
				if err != nil {
					return nil, fmt.Errorf("repository %s head file %s: %w", repository.entry.Repo, name, err)
				}
				base, err := repository.backend.ReadFileAt(ctx, repository.base, item.Path)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("repository %s base file %s: %w", repository.entry.Repo, name, err)
				}
				change.ContentChanged = !bytes.Equal(base, head) || errors.Is(err, fs.ErrNotExist)
				change.Opaque = change.ContentChanged
			}
		}
		repository.changes = changes
	}
	return repositories, nil
}
func removedLabel(change *core.FileChanges) bool {
	syntax := core.CurrentDirectiveSyntax()
	for _, text := range change.RemovedInNew {
		if strings.Contains(text, syntax.TokenIfChange) || strings.Contains(text, syntax.TokenLabel) {
			return true
		}
	}
	return false
}
func deduplicate(findings []core.Finding) []core.Finding {
	// Complete comparable findings retain severity, suppression and target identity.
	seen := map[core.Finding]bool{}
	unique := make([]core.Finding, 0, len(findings))
	for _, finding := range findings {
		if !seen[finding] {
			seen[finding] = true
			unique = append(unique, finding)
		}
	}
	sort.Slice(unique, func(i, j int) bool {
		a, b := unique[i], unique[j]
		if a.Repository != b.Repository {
			return a.Repository < b.Repository
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.Message != b.Message {
			return a.Message < b.Message
		}
		return a.TargetPath+a.TargetLabel < b.TargetPath+b.TargetLabel
	})
	return unique
}
