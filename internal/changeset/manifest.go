// Package changeset verifies dependency edits across explicit repository snapshots.
package changeset

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest selects one immutable comparison per declared repository.
type Manifest struct {
	Version      int          `yaml:"version"`
	Repositories []Repository `yaml:"repositories"`
}
type Repository struct {
	Repo string `yaml:"repo"`
	Path string `yaml:"path"`
	VCS  string `yaml:"vcs"`
	Base string `yaml:"base"`
	Head string `yaml:"head"`
}

var repoIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func LoadManifest(filename string) (Manifest, error) {
	var manifest Manifest
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return manifest, err
	}
	f, err := os.Open(absolute)
	if err != nil {
		return manifest, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return manifest, err
	}
	if len(data) > 1<<20 {
		return manifest, errors.New("change-set manifest exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("invalid change-set manifest: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return manifest, errors.New("change-set manifest must contain exactly one YAML document")
	}
	if manifest.Version != 1 {
		return manifest, fmt.Errorf("unsupported change-set version %d; expected 1", manifest.Version)
	}
	if len(manifest.Repositories) == 0 {
		return manifest, errors.New("change-set must declare at least one repository")
	}
	seen := map[string]bool{}
	for i := range manifest.Repositories {
		r := &manifest.Repositories[i]
		r.Repo = strings.ToLower(strings.TrimSpace(r.Repo))
		if !repoIdentity.MatchString(r.Repo) {
			return manifest, fmt.Errorf("invalid repository identity %q; expected owner/name", r.Repo)
		}
		if seen[r.Repo] {
			return manifest, fmt.Errorf("duplicate change-set repository %q", r.Repo)
		}
		seen[r.Repo] = true
		r.Path = strings.TrimSpace(r.Path)
		r.Base = strings.TrimSpace(r.Base)
		r.Head = strings.TrimSpace(r.Head)
		if r.Path == "" || r.Base == "" || r.Head == "" {
			return manifest, fmt.Errorf("repository %s requires path, base and head", r.Repo)
		}
		if !filepath.IsAbs(r.Path) {
			r.Path = filepath.Join(filepath.Dir(absolute), r.Path)
		}
		r.Path = filepath.Clean(r.Path)
		r.VCS = strings.ToLower(strings.TrimSpace(r.VCS))
		if r.VCS == "" {
			r.VCS = "auto"
		}
		if r.VCS != "auto" && r.VCS != "git" && r.VCS != "jj" {
			return manifest, fmt.Errorf("repository %s has unsupported VCS %q", r.Repo, r.VCS)
		}
	}
	return manifest, nil
}
