package scan

import (
	"bytes"
	core "github.com/derhnyel/ifttt/internal"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FindDirectiveFiles walks root and returns files containing the given needle (e.g. "LINT.").
// It processes files concurrently using workers; concurrency<=0 uses two workers.
// Discovery includes hidden and ignored files consistently across both backends.
var DefaultSkippedDirs = []string{".git", ".jj", ".hg", ".svn", "node_modules", "vendor", "build", "dist", ".cache", ".venv", "__pycache__", ".idea", ".vscode", ".tox", ".eggs", ".pytest_cache", ".mypy_cache", ".ruff_cache"}

func FindDirectiveFiles(root, needle string, concurrency int, skipDirs []string) ([]string, error) {
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

	skipSet := make(map[string]struct{}, len(skipDirs))
	for _, name := range skipDirs {
		skipSet[name] = struct{}{}
	}

	if files, ok := tryRipgrep(root, needle, skipSet); ok {
		// Ensure sorting to keep deterministic output
		sort.Strings(files)
		return files, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string, concurrency*2)
	files := make([]string, 0)

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

	err := filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			if path != root {
				if _, skip := skipSet[de.Name()]; skip {
					return filepath.SkipDir
				}
			}
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

func tryRipgrep(root, needle string, skipSet map[string]struct{}) ([]string, bool) {
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return nil, false
	}
	args := []string{"--null", "--files-with-matches", "--fixed-strings", "--hidden", "--no-ignore"}
	skipped := make([]string, 0, len(skipSet))
	for dir := range skipSet {
		skipped = append(skipped, dir)
	}
	sort.Strings(skipped)
	for _, dir := range skipped {
		args = append(args, "--glob", "!**/"+filepath.ToSlash(dir)+"/**")
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
