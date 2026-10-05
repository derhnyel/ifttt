package scan

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	core "github.com/derhnyel/ifttt/internal"
	gitconfig "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// ignorePolicy loads rules only in directories the walker actually visits.
// Pruning an ignored directory also prevents child rules from re-including it.
type ignorePolicy struct {
	base         string
	rules        map[string][]gitignore.Pattern
	rootIgnored  bool
	tracked      map[string]bool
	trackedDirs  map[string]bool
	excludedDirs map[string]bool
}

func loadIgnorePolicy(root string) (*ignorePolicy, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	base, err := repositoryBoundary(root)
	if err != nil {
		return nil, err
	}
	policy := &ignorePolicy{base: base, rules: make(map[string][]gitignore.Pattern), tracked: map[string]bool{}, trackedDirs: map[string]bool{}, excludedDirs: map[string]bool{}}
	gitDir := filepath.Join(base, ".git")
	if info, err := os.Stat(gitDir); err == nil && !info.IsDir() {
		data, err := os.ReadFile(gitDir)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(string(data), "gitdir: ") {
			return nil, errors.New("invalid Git worktree metadata")
		}
		gitDir = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(base, gitDir)
		}
	}
	file, err := os.Open(filepath.Join(gitDir, "index"))
	if err == nil {
		var trackedIndex index.Index
		err = index.NewDecoder(file).Decode(&trackedIndex)
		file.Close()
		if err != nil {
			return nil, err
		}
		for _, entry := range trackedIndex.Entries {
			name := filepath.FromSlash(entry.Name)
			if err := policy.addTracked(name); err != nil {
				return nil, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(base, ".jj", "repo")); err == nil {
		if jj, err := exec.LookPath("jj"); err == nil {
			cmd := exec.Command(jj, "--ignore-working-copy", "--at-operation=@", "file", "list", "--template", `path ++ "\0"`)
			cmd.Dir = base
			out, err := cmd.Output()
			if err != nil {
				return nil, err
			}
			for _, name := range nulPaths(out) {
				if err := policy.addTracked(name); err != nil {
					return nil, err
				}
			}
		}
	}
	if _, err := os.Stat(gitDir); err == nil {
		commonDir := gitDir
		if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			commonDir = strings.TrimSpace(string(data))
			if !filepath.IsAbs(commonDir) {
				commonDir = filepath.Join(gitDir, commonDir)
			}
		}
		patterns, err := standardIgnorePatterns(commonDir)
		if err != nil {
			return nil, err
		}
		policy.rules[filepath.Dir(base)] = patterns
	}
	// Ancestor rules apply when a subdirectory is the selected scan root.
	var parents []string
	for dir := filepath.Dir(absRoot); absRoot != base; dir = filepath.Dir(dir) {
		parents = append(parents, dir)
		if dir == base {
			break
		}
	}
	for i := len(parents) - 1; i >= 0; i-- {
		if ignored, err := policy.ignored(parents[i], true); err != nil {
			return nil, err
		} else if ignored {
			policy.rootIgnored = true
			break
		}
	}
	return policy, nil
}

func fileExclusions(excludes []string) func(string, bool) bool {
	type exclusion struct {
		rx      *regexp.Regexp
		subtree bool
	}
	var patterns []exclusion
	cwd, _ := os.Getwd()
	for _, raw := range excludes {
		if !strings.Contains(raw, "#") && !strings.Contains(raw, "://") {
			patterns = append(patterns, exclusion{core.CompileGlob(raw), strings.HasSuffix(raw, "/**")})
		}
	}
	return func(path string, isDir bool) bool {
		if filepath.IsAbs(path) {
			if rel, err := filepath.Rel(cwd, path); err == nil {
				path = rel
			}
		}
		path = filepath.ToSlash(filepath.Clean(path))
		base := filepath.Base(path)
		for _, pattern := range patterns {
			if isDir {
				if pattern.subtree && (pattern.rx.MatchString(path+"/") || pattern.rx.MatchString(base+"/")) {
					return true
				}
				continue
			}
			if pattern.rx.MatchString(path) || pattern.rx.MatchString(base) {
				return true
			}
		}
		return false
	}
}

func repositoryBoundary(root string) (string, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	for {
		for _, metadata := range []string{".git", ".jj"} {
			if _, err := os.Stat(filepath.Join(base, metadata)); err == nil {
				return base, nil
			}
		}
		parent := filepath.Dir(base)
		if parent == base {
			return base, nil
		}
		base = parent
	}
}

func (p *ignorePolicy) addTracked(name string) error {
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.Clean(name) != name {
		return fmt.Errorf("invalid tracked path %q", name)
	}
	p.tracked[name] = true
	for dir := filepath.Dir(name); dir != "."; dir = filepath.Dir(dir) {
		p.trackedDirs[dir] = true
	}
	return nil
}

func standardIgnorePatterns(gitDir string) ([]gitignore.Pattern, error) {
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	excludesFile := filepath.Join(xdg, "git", "ignore")
	configPaths := []string{filepath.Join(xdg, "git", "config"), filepath.Join(home, ".gitconfig"), filepath.Join(gitDir, "config")}
	if global := os.Getenv("GIT_CONFIG_GLOBAL"); global != "" {
		configPaths = []string{global, filepath.Join(gitDir, "config")}
	}
	for _, path := range configPaths {
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var cfg gitconfig.Config
		err = gitconfig.NewDecoder(file).Decode(&cfg)
		file.Close()
		if err != nil {
			return nil, err
		}
		if value := cfg.Section("core").Option("excludesfile"); value != "" {
			excludesFile = value
		}
	}
	if strings.HasPrefix(excludesFile, "~/") {
		excludesFile = filepath.Join(home, excludesFile[2:])
	}
	var patterns []gitignore.Pattern
	for _, path := range []string{excludesFile, filepath.Join(gitDir, "info", "exclude")} {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
				patterns = append(patterns, gitignore.ParsePattern(line, nil))
			}
		}
	}
	return patterns, nil
}

func (p *ignorePolicy) ignored(path string, isDir bool) (bool, error) {
	if p.rootIgnored {
		return true, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(p.base, abs)
	if err != nil {
		return false, err
	}
	rules := p.rules[filepath.Dir(abs)]
	if !isDir && p.tracked[rel] {
		return false, nil
	}
	if rel != "." && (p.excludedDirs[filepath.Dir(abs)] || gitignore.NewMatcher(rules).Match(strings.Split(filepath.ToSlash(rel), "/"), isDir)) {
		if !isDir || !p.trackedDirs[rel] {
			return true, nil
		}
		p.excludedDirs[abs] = true
	}
	if isDir {
		ignorePath := filepath.Join(abs, ".gitignore")
		info, statErr := os.Lstat(ignorePath)
		var data []byte
		if statErr == nil && info.Mode().IsRegular() {
			data, err = os.ReadFile(ignorePath)
		} else if statErr != nil {
			err = statErr
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		var domain []string
		if rel != "." {
			domain = strings.Split(filepath.ToSlash(rel), "/")
		}
		// Copy the inherited slice so sibling rules cannot replace each other.
		rules = append([]gitignore.Pattern(nil), rules...)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
				rules = append(rules, gitignore.ParsePattern(line, domain))
			}
		}
		p.rules[abs] = rules
	}
	return false, nil
}
