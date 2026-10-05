package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"

	"github.com/derhnyel/ifttt/internal/cache"
)

const gitHubScheme = "github"

// GitHubRemote describes configuration for a GitHub repository integration.
type GitHubRemote struct {
	Repo       string
	DefaultRef string
	Token      string
	BaseURL    string
}

type githubProvider struct {
	owner          string
	repo           string
	defaultRef     string
	client         *github.Client
	ttl            time.Duration
	cacheNamespace string

	mu    sync.Mutex
	cache map[string]githubCacheEntry
}

type githubCacheEntry struct {
	data    []byte
	fetched time.Time
	etag    string
}

type githubCacheStats struct {
	memoryHits  atomic.Int64
	diskHits    atomic.Int64
	downloads   atomic.Int64
	notModified atomic.Int64
}

var remoteCacheStats githubCacheStats

// GitHubFactory registers GitHub providers for multiple repositories.
type GitHubFactory struct {
	providers map[string]*githubProvider
}

// NewGitHubFactory constructs a provider factory for the supplied remotes.
func NewGitHubFactory(remotes []GitHubRemote) (*GitHubFactory, error) {
	if len(remotes) == 0 {
		return nil, nil
	}
	providers := make(map[string]*githubProvider)
	for _, remote := range remotes {
		owner, repo, err := splitRepo(remote.Repo)
		if err != nil {
			return nil, err
		}
		client, err := newGitHubClient(remote)
		if err != nil {
			return nil, err
		}
		defaultRef := remote.DefaultRef
		if defaultRef == "" {
			defaultRef = "main"
		}
		// Scope private cache entries by API host, repository and credential identity.
		namespace := sha256.Sum256([]byte(client.BaseURL.String() + "\x00" + remote.Repo + "\x00" + strings.TrimSpace(remote.Token)))
		providers[strings.ToLower(remote.Repo)] = &githubProvider{
			owner:          owner,
			repo:           repo,
			defaultRef:     defaultRef,
			cacheNamespace: fmt.Sprintf("%x", namespace),
			client:         client,
			ttl:            time.Minute,
			cache:          make(map[string]githubCacheEntry),
		}
	}
	return &GitHubFactory{providers: providers}, nil
}

func newGitHubClient(remote GitHubRemote) (*github.Client, error) {
	ctx := context.Background()
	token := strings.TrimSpace(remote.Token)
	var httpClient *http.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		httpClient = oauth2.NewClient(ctx, ts)
	} else {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	httpClient.Timeout = 10 * time.Second
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("github: too many redirects")
		}
		if len(via) > 0 && (req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme) {
			return errors.New("github: refusing redirect to another origin")
		}
		return nil
	}
	baseURL := strings.TrimSpace(remote.BaseURL)
	if baseURL == "" || baseURL == "https://api.github.com" {
		return github.NewClient(httpClient), nil
	}
	apiURL := ensureTrailingSlash(baseURL)
	return github.NewEnterpriseClient(apiURL, apiURL, httpClient)
}

func ensureTrailingSlash(raw string) string {
	if strings.HasSuffix(raw, "/") {
		return raw
	}
	return raw + "/"
}

func (f *GitHubFactory) Match(target string) bool {
	return strings.HasPrefix(target, gitHubScheme+"://")
}

func (f *GitHubFactory) Provider(target string) (FileProvider, string, error) {
	info, err := parseGitHubTarget(target)
	if err != nil {
		return nil, "", err
	}
	key := strings.ToLower(info.owner + "/" + info.repo)
	prov, ok := f.providers[key]
	if !ok {
		return nil, "", fmt.Errorf("no github remote configured for %s", key)
	}
	return prov, target, nil
}

func (p *githubProvider) ReadFile(target string) ([]byte, error) {
	info, err := parseGitHubTarget(target)
	if err != nil {
		return nil, err
	}
	if info.ref == "" {
		info.ref = p.defaultRef
	}
	cacheKey := p.cacheNamespace + ":" + info.ref + ":" + info.path

	var entry githubCacheEntry
	var ok bool
	p.mu.Lock()
	if entry, ok = p.cache[cacheKey]; ok {
		if time.Since(entry.fetched) < p.ttl {
			remoteCacheStats.memoryHits.Add(1)
			data := entry.data
			p.mu.Unlock()
			return data, nil
		}
	}
	p.mu.Unlock()
	var etag string
	if ok {
		etag = entry.etag
	} else {
		var diskEntry cache.RemoteEntry
		if err := cache.LoadRemote(cacheKey, &diskEntry); err == nil {
			remoteCacheStats.diskHits.Add(1)
			entry = githubCacheEntry{data: diskEntry.Data, fetched: diskEntry.FetchedAt, etag: diskEntry.ETag}
			if time.Since(entry.fetched) < p.ttl {
				remoteCacheStats.memoryHits.Add(1)
				p.mu.Lock()
				p.putCache(cacheKey, entry)
				p.mu.Unlock()
				return entry.data, nil
			}
			etag = entry.etag
			p.mu.Lock()
			p.putCache(cacheKey, entry)
			p.mu.Unlock()
		}
	}
	data, newEtag, notModified, err := p.fetch(info, etag)
	if err != nil {
		return nil, err
	}
	if notModified {
		remoteCacheStats.notModified.Add(1)
		if entry.data == nil {
			return nil, fmt.Errorf("github: received 304 but no cached data for %s", info.path)
		}
		entry.fetched = time.Now()
		p.mu.Lock()
		p.putCache(cacheKey, entry)
		p.mu.Unlock()
		_ = cache.StoreRemote(cacheKey, &cache.RemoteEntry{Data: entry.data, ETag: entry.etag, FetchedAt: entry.fetched})
		return entry.data, nil
	}
	remoteCacheStats.downloads.Add(1)
	entry = githubCacheEntry{data: data, fetched: time.Now(), etag: newEtag}
	p.mu.Lock()
	p.putCache(cacheKey, entry)
	p.mu.Unlock()
	_ = cache.StoreRemote(cacheKey, &cache.RemoteEntry{Data: data, ETag: newEtag, FetchedAt: entry.fetched})
	return data, nil
}

func (p *githubProvider) fetch(info githubTarget, etag string) ([]byte, string, bool, error) {
	ctx := context.Background()
	path := fmt.Sprintf("repos/%s/%s/contents/%s", info.owner, info.repo, info.path)
	req, err := p.client.NewRequest("GET", path, nil)
	if err != nil {
		return nil, "", false, err
	}
	q := req.URL.Query()
	if info.ref != "" {
		q.Set("ref", info.ref)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := p.client.BareDo(ctx, req)
	if resp != nil && resp.Response != nil {
		defer resp.Response.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.Response != nil {
			if resp.Response.StatusCode == http.StatusNotFound {
				return nil, "", false, fsErrNotExist(info.path)
			}
			if resp.Response.StatusCode == http.StatusNotModified {
				return nil, etag, true, nil
			}
		}
		return nil, "", false, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Response.Body, (16<<20)+1))
	if err != nil {
		return nil, "", false, err
	}
	if len(body) > 16<<20 {
		return nil, "", false, errors.New("github file exceeds 16 MiB limit")
	}
	newEtag := resp.Response.Header.Get("ETag")
	return body, newEtag, false, nil
}

func (p *githubProvider) Stat(string) (FileStat, error) {
	return FileStat{}, ErrStatUnsupported
}

func splitRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("repo must be owner/repo, got %s", repo)
	}
	return parts[0], parts[1], nil
}

type githubTarget struct {
	owner string
	repo  string
	path  string
	ref   string
}

func parseGitHubTarget(target string) (githubTarget, error) {
	u, err := url.Parse(target)
	if err != nil {
		return githubTarget{}, err
	}
	if u.Scheme != gitHubScheme {
		return githubTarget{}, fmt.Errorf("unsupported scheme %s", u.Scheme)
	}
	owner := strings.TrimSpace(u.Host)
	if owner == "" {
		return githubTarget{}, errors.New("github target missing owner")
	}
	cleanPath := strings.TrimPrefix(u.Path, "/")
	segments := strings.Split(cleanPath, "/")
	if len(segments) < 2 {
		return githubTarget{}, errors.New("github target must include repo and file path")
	}
	repo := segments[0]
	filePath := path.Clean(strings.Join(segments[1:], "/"))
	if repo == "" || filePath == "." || filePath == ".." || strings.HasPrefix(filePath, "../") {
		return githubTarget{}, errors.New("github target missing file path")
	}
	ref := u.Query().Get("ref")
	return githubTarget{owner: owner, repo: repo, path: filePath, ref: ref}, nil
}

func fsErrNotExist(name string) error {
	return fmt.Errorf("%s: %w", name, fs.ErrNotExist)
}

func githubCacheSnapshot() map[string]int64 {
	mhits := remoteCacheStats.memoryHits.Swap(0)
	dhits := remoteCacheStats.diskHits.Swap(0)
	dloads := remoteCacheStats.downloads.Swap(0)
	nmods := remoteCacheStats.notModified.Swap(0)
	total := mhits + dhits + dloads + nmods
	if total == 0 {
		return nil
	}
	return map[string]int64{
		"memory_hits":  mhits,
		"disk_hits":    dhits,
		"downloads":    dloads,
		"not_modified": nmods,
	}
}

// putCache is called with mu held; bound long-running editor/watch sessions.
func (p *githubProvider) putCache(key string, entry githubCacheEntry) {
	if len(p.cache) >= 128 {
		if _, exists := p.cache[key]; !exists {
			var oldest string
			var stamp time.Time
			for k, v := range p.cache {
				if oldest == "" || v.fetched.Before(stamp) {
					oldest, stamp = k, v.fetched
				}
			}
			delete(p.cache, oldest)
		}
	}
	p.cache[key] = entry
}
