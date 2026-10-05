package scan

import (
	"bytes"
	core "github.com/derhnyel/ifttt/internal"
	ilog "github.com/derhnyel/ifttt/internal/log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LINT.IfChange(scan_local_artifacts)
// DefaultSkippedDirs excludes VCS metadata and dependency/cache directories.
var DefaultSkippedDirs = []string{".git", ".jj", ".hg", ".svn", "node_modules", "vendor", ".cache", ".gocache", ".venv", "__pycache__"}

// LINT.ThenChange(//internal/scan/scan_test.go:scan_local_artifacts, //test/integration/cli_test.go:scan_local_artifacts, //docs/cli.md:scan_local_artifacts)

// FindDirectiveFiles walks root and returns files containing the given needle (e.g. "LINT.").
// It processes files concurrently using workers; concurrency<=0 uses two workers.
// Discovery includes hidden files, respects Git ignores and excludes symlinks.
func FindDirectiveFiles(root, needle string, concurrency int, skipDirs []string, excludes ...string) ([]string, error) {
	return FindWithExclusions(root, needle, concurrency, skipDirs, Exclusions{Patterns: excludes})
}

// Exclusions keeps patterns in their configuration directory's coordinates.
// BaseDir defaults to the process directory for explicit CLI policies.
type Exclusions struct {
	BaseDir  string
	Patterns []string
}

func FindWithExclusions(root, needle string, concurrency int, skipDirs []string, excludes Exclusions) (files []string, err error) {
	if needle == "" {
		return nil, nil
	}
	if concurrency <= 0 {
		concurrency = core.DefaultParallelism
		if concurrency < 1 {
			concurrency = 1
		}
	}

	if concurrency > 64 {
		concurrency = 64
	}
	if skipDirs == nil {
		skipDirs = DefaultSkippedDirs
	}
	started := time.Now()
	ilog.Debug("scan: discovering directive files", "root", root, "skip_dirs", skipDirs)
	defer func() {
		ilog.Debug("scan: discovery finished", "root", root, "files", len(files), "duration", time.Since(started), "error", err)
	}()

	skipSet := make(map[string]struct{}, len(skipDirs))
	for _, name := range skipDirs {
		skipSet[filepath.Base(filepath.Clean(name))] = struct{}{}
	}

	if files, ok, gitErr := tryGit(root, needle, concurrency, skipSet, excludes); ok {
		sort.Strings(files)
		return files, gitErr
	}
	if files, ok := tryRipgrep(root, needle, skipSet, excludes.Patterns...); ok {
		// Ensure sorting to keep deterministic output
		sort.Strings(files)
		return files, nil
	}
	policy, err := loadIgnorePolicy(root)
	if err != nil {
		return nil, err
	}
	excluded := fileExclusionsAt(excludes.BaseDir, excludes.Patterns)

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string, concurrency*2)
	files = make([]string, 0)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if containsNeedle(path, needle) {
					mu.Lock()
					files = append(files, path)
					mu.Unlock()
				}
			}
		}()
	}

	err = filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() && path != root {
			if _, skip := skipSet[de.Name()]; skip {
				return filepath.SkipDir
			}
		}
		if excluded(path, de.IsDir()) {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		ignored, err := policy.ignored(path, de.IsDir())
		if err != nil {
			return err
		}
		if ignored {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if de.IsDir() {
			return nil
		}
		if de.Type().IsRegular() {
			jobs <- path
		}
		return nil
	})

	close(jobs)
	wg.Wait()

	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

var scanBuffers = sync.Pool{New: func() any { b := make([]byte, 64*1024); return &b }}

func containsNeedle(path, needle string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buffer := scanBuffers.Get().(*[]byte)
	defer scanBuffers.Put(buffer)
	storage := *buffer
	pattern := []byte(needle)
	if len(pattern) >= len(storage) {
		storage = make([]byte, len(pattern)*2)
	}
	carry := 0
	for {
		n, err := f.Read(storage[carry:])
		end := carry + n
		if bytes.Contains(storage[:end], pattern) {
			return true
		}
		if err != nil || n == 0 {
			break
		}
		carry = len(pattern) - 1
		if carry > end {
			carry = end
		}
		copy(storage[:carry], storage[end-carry:end])
	}

	return false
}

func tryRipgrep(root, needle string, skipSet map[string]struct{}, excludes ...string) ([]string, bool) {
	if base, err := repositoryBoundary(root); err == nil {
		if _, err := os.Stat(filepath.Join(base, ".git")); err == nil {
			return nil, false
		}
		if _, err := os.Stat(filepath.Join(base, ".jj")); err == nil {
			return nil, false
		}
	}
	for _, pattern := range excludes {
		if !strings.Contains(pattern, "#") && !strings.Contains(pattern, "://") {
			return nil, false
		}
	}
	// rg always excludes .git metadata. Use the walker for explicit overrides.
	if _, skip := skipSet[".git"]; !skip {
		return nil, false
	}
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return nil, false
	}
	args := []string{"--null", "--files-with-matches", "--fixed-strings", "--hidden", "--no-require-git", "--no-ignore-dot"}
	skipped := make([]string, 0, len(skipSet))
	for dir := range skipSet {
		skipped = append(skipped, dir)
	}
	sort.Strings(skipped)
	for _, dir := range skipped {
		args = append(args, "--glob", "!**/"+escapeGlob(filepath.ToSlash(dir))+"/**")
	}

	args = append(args, "--", needle, root)
	cmd := exec.Command(rgPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil, true
		}
		return nil, false
	}
	split := bytes.Split(out, []byte{0})
	files := make([]string, 0, len(split))
	for _, b := range split {
		if len(b) == 0 {
			continue
		}
		path := string(b)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, false
		}
		skipped := false
		for _, dir := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
			if _, skip := skipSet[dir]; skip {
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		files = append(files, path)
	}
	return files, true
}

// Git applies its index and standard excludes before opening files.
// Tracked files remain eligible even when a later ignore rule matches them.
func tryGit(root, needle string, concurrency int, skipSet map[string]struct{}, excludes Exclusions) ([]string, bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false, nil
	}
	probe := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	if out, err := probe.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, false, nil
	}
	// Configured globs use the CLI grammar. Filter Git's eligible path list in Go,
	// rather than translating them into Git's different pathspec grammar.
	var source []string
	for _, pattern := range excludes.Patterns {
		if !strings.Contains(pattern, "#") && !strings.Contains(pattern, "://") {
			source = append(source, pattern)
		}
	}
	if len(source) > 0 {
		out, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", ".").Output()
		if err != nil {
			return nil, true, err
		}
		return searchPaths(root, needle, nulPaths(out), concurrency, skipSet, fileExclusionsAt(excludes.BaseDir, source)), true, nil
	}
	args := []string{"-C", root, "-c", "grep.fullName=false", "grep", "--threads=" + strconv.Itoa(concurrency), "--text", "-l", "-z", "--fixed-strings", "-e", needle, "--", "."}
	for dir := range skipSet {
		args = append(args, ":(glob,exclude)**/"+escapeGlob(dir)+"/**")
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, true, err
		}
	}
	var files []string
	for _, name := range nulPaths(out) {
		files = append(files, filepath.Join(root, name))
	}
	// --exclude-standard on grep also excludes matching tracked paths. Keep the
	// tracked needle query separate from the ignored-aware untracked listing.
	out, err = exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z", "--", ".").Output()
	if err != nil {
		return nil, true, err
	}
	files = append(files, searchPaths(root, needle, nulPaths(out), concurrency, skipSet, fileExclusions(nil))...)
	return files, true, nil
}

func escapeGlob(value string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[").Replace(value)
}

func nulPaths(data []byte) []string {
	var paths []string
	for _, name := range bytes.Split(data, []byte{0}) {
		if len(name) > 0 {
			paths = append(paths, filepath.FromSlash(string(name)))
		}
	}
	return paths
}

func searchPaths(root, needle string, paths []string, concurrency int, skipSet map[string]struct{}, excluded func(string, bool) bool) []string {
	var files []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string, concurrency*2)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if containsNeedle(path, needle) {
					mu.Lock()
					files = append(files, path)
					mu.Unlock()
				}
			}
		}()
	}
	for _, name := range paths {
		path := filepath.Join(root, name)
		skip := excluded(path, false)
		for _, dir := range strings.Split(filepath.Dir(name), string(filepath.Separator)) {
			if _, found := skipSet[dir]; found {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() {
			jobs <- path
		}
	}
	close(jobs)
	wg.Wait()
	return files
}
