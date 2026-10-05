package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileStat provides minimal metadata about a file used for caching.
type FileStat struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// FileProvider abstracts file access so alternative backends (e.g., remote blobs)
// can supply source/target contents without touching the local filesystem.
type FileProvider interface {
	ReadFile(path string) ([]byte, error)
	Stat(path string) (FileStat, error)
}

var ErrStatUnsupported = errors.New("file provider stat unsupported")

var errUnsupportedRemoteTarget = errors.New("URL targets are not supported without a configured provider")

// Keep read/provider errors distinct from syntax errors. A dependent source
// reports its unreadable target, so target parsing must not repeat that error.
type directiveReadError struct{ err error }

func (e *directiveReadError) Error() string { return e.err.Error() }
func (e *directiveReadError) Unwrap() error { return e.err }

type FileProviderFactory interface {
	Match(target string) bool
	Provider(target string) (FileProvider, string, error)
}

type localFileProvider struct{}

func (localFileProvider) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (localFileProvider) Stat(path string) (FileStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileStat{}, err
	}
	return FileStat{
		Path:    path,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

// workspaceFileProvider scopes local source/target reads to the invocation root.
// Explicit remote providers remain separate; local symlinks cannot cross roots.
const (
	workspaceReadBudget  = 8 << 20
	workspaceReadEntries = 4096
)

// This provider lives for one lint invocation. Its bounded snapshot avoids
// reading a source/target again during presence, parsing and target evaluation.
type workspaceFileProvider struct {
	root                        string
	once                        sync.Once
	absoluteRoot, canonicalRoot string
	initErr                     error
	mu                          sync.Mutex
	contents                    map[string][]byte
	cachedBytes                 int
}

func (p *workspaceFileProvider) initialize() {
	p.absoluteRoot, p.initErr = filepath.Abs(p.root)
	if p.initErr != nil {
		return
	}
	p.canonicalRoot, p.initErr = filepath.EvalSymlinks(p.absoluteRoot)
	p.contents = make(map[string][]byte)
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (p *workspaceFileProvider) resolve(target string) (string, error) {
	p.once.Do(p.initialize)
	if p.initErr != nil {
		return "", p.initErr
	}
	absolute := filepath.Clean(target)
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(p.absoluteRoot, absolute)
	}
	// Start at the already resolved root for ordinary in-workspace paths, so
	// system ancestors and the current working directory are not re-read.
	if withinRoot(p.absoluteRoot, absolute) {
		relative, _ := filepath.Rel(p.absoluteRoot, absolute)
		current := p.canonicalRoot
		parts := strings.Split(relative, string(filepath.Separator))
		for i, part := range parts {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				for _, remaining := range parts[i+1:] {
					current = filepath.Join(current, remaining)
				}
				break
			}
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				current, err = filepath.EvalSymlinks(current)
				if err != nil {
					return "", err
				}
			}
			if !withinRoot(p.canonicalRoot, current) {
				return "", fmt.Errorf("local target %q is outside workspace", target)
			}
		}
		absolute = current
	} else {
		// Absolute system aliases may still identify a file inside the workspace.
		ancestor := absolute
		var suffix []string
		for {
			if _, err := os.Lstat(ancestor); err == nil {
				break
			} else if !os.IsNotExist(err) {
				return "", err
			}
			suffix = append(suffix, filepath.Base(ancestor))
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return "", fmt.Errorf("cannot resolve local target %q", target)
			}
			ancestor = parent
		}
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err != nil {
			return "", err
		}
		absolute = resolved
		for i := len(suffix) - 1; i >= 0; i-- {
			absolute = filepath.Join(absolute, suffix[i])
		}
	}
	if !withinRoot(p.canonicalRoot, absolute) {
		return "", fmt.Errorf("local target %q is outside workspace", target)
	}
	return absolute, nil
}

func (p *workspaceFileProvider) ReadFile(target string) ([]byte, error) {
	p.once.Do(p.initialize)
	if p.initErr != nil {
		return nil, p.initErr
	}
	key := filepath.Clean(target)
	p.mu.Lock()
	data, ok := p.contents[key]
	p.mu.Unlock()
	if ok {
		return data, nil
	}
	resolved, err := p.resolve(target)
	if err != nil {
		return nil, err
	}
	data, err = os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if previous, ok := p.contents[key]; ok {
		data = previous
	} else if len(p.contents) < workspaceReadEntries && len(data) <= workspaceReadBudget-p.cachedBytes {
		p.contents[key] = data
		p.cachedBytes += len(data)
	}
	p.mu.Unlock()
	return data, nil
}
func (p *workspaceFileProvider) Stat(target string) (FileStat, error) {
	resolved, err := p.resolve(target)
	if err != nil {
		return FileStat{}, err
	}
	return localFileProvider{}.Stat(resolved)
}
