package changeset

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/engine"
	"github.com/derhnyel/ifttt/internal/vcs"
)

type snapshot struct {
	entry      Repository
	backend    *vcs.Backend
	base, head string
	changes    map[string]*core.FileChanges
	files      *snapshotFiles
}
type snapshotEntry struct {
	data []byte
	err  error
}
type snapshotFiles struct {
	ctx        context.Context
	snapshot   *snapshot
	mu         sync.Mutex
	cache      map[string]snapshotEntry
	bytes      int
	readErrors []error
}

func (p *snapshotFiles) localPath(name string) (string, error) {
	if filepath.IsAbs(name) {
		relative, err := filepath.Rel(p.snapshot.backend.Root, name)
		if err != nil {
			return "", err
		}
		name = relative
	}
	name = filepath.ToSlash(filepath.Clean(name))
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("snapshot target %q is outside repository %s", name, p.snapshot.entry.Repo)
	}
	return name, nil
}
func (p *snapshotFiles) ReadFile(name string) ([]byte, error) {
	relative, err := p.localPath(name)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	cached, ok := p.cache[relative]
	p.mu.Unlock()
	if ok {
		return cached.data, cached.err
	}
	data, err := p.snapshot.backend.ReadFileAt(p.ctx, p.snapshot.head, relative)
	p.mu.Lock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		p.readErrors = append(p.readErrors, err)
	}
	if _, exists := p.cache[relative]; !exists && len(p.cache) < 4096 && len(data) <= (8<<20)-p.bytes {
		p.cache[relative] = snapshotEntry{data, err}
		p.bytes += len(data)
	}
	p.mu.Unlock()
	return data, err
}
func (*snapshotFiles) Stat(string) (engine.FileStat, error) {
	return engine.FileStat{}, engine.ErrStatUnsupported
}

func (p *snapshotFiles) evidenceError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return firstEvidenceError(p.readErrors)
}

type boundTarget struct {
	snapshot *snapshot
	path     string
	err      error
}
type snapshotFactory struct {
	ctx           context.Context
	repositories  map[string]*snapshot
	mu            sync.Mutex
	targets       map[string]boundTarget
	bindingErrors []error
}

func (*snapshotFactory) Match(target string) bool { return strings.Contains(target, "://") }
func (f *snapshotFactory) Provider(target string) (engine.FileProvider, string, error) {
	bound := f.lookup(target)
	if bound.err != nil {
		return nil, "", bound.err
	}
	return bound.snapshot.files, bound.path, nil
}
func (f *snapshotFactory) changes(target string) (*core.FileChanges, error) {
	bound := f.lookup(target)
	if bound.err != nil {
		return nil, bound.err
	}
	return bound.snapshot.changes[bound.path], nil
}
func (f *snapshotFactory) lookup(target string) boundTarget {
	f.mu.Lock()
	cached, ok := f.targets[target]
	f.mu.Unlock()
	if ok {
		return cached
	}
	bound := f.resolve(target)
	f.mu.Lock()
	if cached, ok = f.targets[target]; ok {
		bound = cached
	} else {
		f.targets[target] = bound
		if bound.err != nil {
			f.bindingErrors = append(f.bindingErrors, bound.err)
		}
	}
	f.mu.Unlock()
	return bound
}
func (f *snapshotFactory) resolve(target string) boundTarget {
	fail := func(err error) boundTarget {
		return boundTarget{err: fmt.Errorf("invalid change-set target %q: %w", target, err)}
	}
	u, err := url.Parse(target)
	if err != nil {
		return fail(err)
	}
	if u.Scheme != "github" || u.User != nil || u.Host == "" || u.Host != u.Hostname() || u.Fragment != "" {
		return fail(errors.New("expected github://owner/repo/path"))
	}
	segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segments) < 2 {
		return fail(errors.New("repository and file path are required"))
	}
	identity := strings.ToLower(u.Host + "/" + segments[0])
	repository := f.repositories[identity]
	if repository == nil {
		return fail(fmt.Errorf("repository %s is not declared in the change set", identity))
	}
	name := strings.Join(segments[1:], "/")
	if name == "" || path.Clean(name) != name || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00") {
		return fail(errors.New("target must be a contained repository file"))
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fail(err)
	}
	for key, list := range values {
		if key != "ref" || len(list) != 1 || list[0] == "" {
			return fail(errors.New("only a single nonempty ref query is supported"))
		}
	}
	ref := values.Get("ref")
	if ref != "" && ref != repository.head && ref != repository.entry.Head {
		id, err := repository.backend.ResolveRevision(f.ctx, ref)
		if err != nil {
			return fail(err)
		}
		if id != repository.head {
			return fail(fmt.Errorf("ref %s conflicts with selected head %s", ref, repository.head))
		}
	}
	return boundTarget{snapshot: repository, path: name}
}
func (f *snapshotFactory) evidenceError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return firstEvidenceError(f.bindingErrors)
}

func firstEvidenceError(evidence []error) error {
	if len(evidence) == 0 {
		return nil
	}
	// Binding and snapshot read order may depend on engine worker scheduling.
	messages := make([]string, 0, len(evidence))
	for _, err := range evidence {
		messages = append(messages, err.Error())
	}
	sort.Strings(messages)
	return errors.New(messages[0])
}

var _ engine.FileProvider = (*snapshotFiles)(nil)
