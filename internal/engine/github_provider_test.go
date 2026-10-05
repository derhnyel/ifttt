package engine

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v60/github"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGitHubFactoryProvidesRemote(t *testing.T) {
	t.Setenv("IFTTT_CACHE_DIR", t.TempDir())
	requestCount := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		if ref := req.URL.Query().Get("ref"); ref != "main" {
			t.Fatalf("expected ref main, got %q", ref)
		}
		if accept := req.Header.Get("Accept"); accept != "application/vnd.github.v3.raw" {
			t.Fatalf("expected raw accept header, got %q", accept)
		}
		if strings.Contains(req.URL.Path, "/missing.go") {
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if !strings.Contains(req.URL.Path, "/repos/owner/repo/contents/file.go") {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		headers := make(http.Header)
		headers.Set("ETag", "W/\"abc123\"")
		body := "// SENTRY.Label(\"LBL\")\n"
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	remotes := []GitHubRemote{{
		Repo:       "owner/repo",
		DefaultRef: "main",
		Token:      "abc",
		BaseURL:    "https://api.github.com",
	}}

	factory, err := NewGitHubFactory(remotes)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if factory == nil {
		t.Fatalf("expected factory")
	}

	target := "github://owner/repo/file.go?ref=main"
	if !factory.Match(target) {
		t.Fatalf("Match returned false")
	}
	provider, actual, err := factory.Provider(target)
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if actual != target {
		t.Fatalf("expected actual path %q, got %q", target, actual)
	}

	gp, ok := provider.(*githubProvider)
	if !ok {
		t.Fatalf("unexpected provider type %T", provider)
	}
	gp.client = github.NewClient(&http.Client{Transport: transport})

	data, err := provider.ReadFile(actual)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "SENTRY.Label") {
		t.Fatalf("unexpected data: %s", data)
	}
	if _, err := provider.Stat(actual); !errors.Is(err, ErrStatUnsupported) {
		t.Fatalf("expected ErrStatUnsupported, got %v", err)
	}

	// second call should hit cache
	if _, err := provider.ReadFile(actual); err != nil {
		t.Fatalf("ReadFile second call: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("expected single HTTP request, got %d", requestCount)
	}

	_, err = provider.ReadFile("github://owner/repo/missing.go?ref=main")
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestGitHubFactoryUsesDefaultRefWhenMissing(t *testing.T) {
	t.Setenv("IFTTT_CACHE_DIR", t.TempDir())
	called := false
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if ref := req.URL.Query().Get("ref"); ref != "stable" {
			t.Fatalf("expected ref stable, got %q", ref)
		}
		headers := make(http.Header)
		headers.Set("ETag", "W/\"tag\"")
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader("data"))}, nil
	})
	factory, err := NewGitHubFactory([]GitHubRemote{{
		Repo:       "acme/repo",
		DefaultRef: "stable",
		Token:      "",
		BaseURL:    "https://api.github.com",
	}})
	if err != nil {
		t.Fatalf("NewGitHubFactory: %v", err)
	}
	provider, actual, err := factory.Provider("github://acme/repo/foo.go")
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if actual != "github://acme/repo/foo.go" {
		t.Fatalf("unexpected actual path %q", actual)
	}
	gp := provider.(*githubProvider)
	gp.client = github.NewClient(&http.Client{Transport: transport})
	if _, err := provider.ReadFile(actual); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !called {
		t.Fatalf("expected request")
	}
}

func TestGitHubDiskCacheIsolatedByRepository(t *testing.T) {
	t.Setenv("IFTTT_CACHE_DIR", t.TempDir())
	for _, repo := range []string{"first", "second"} {
		factory, err := NewGitHubFactory([]GitHubRemote{{Repo: "isolation/" + repo, DefaultRef: "main"}})
		if err != nil {
			t.Fatal(err)
		}
		provider, target, err := factory.Provider("github://isolation/" + repo + "/unique-cache-isolation.go")
		if err != nil {
			t.Fatal(err)
		}
		provider.(*githubProvider).client = github.NewClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(repo))}, nil
		})})
		data, err := provider.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != repo {
			t.Fatalf("repository %s received cached contents %q", repo, data)
		}
	}
}

func TestAuthenticatedGitHubClientHasTimeout(t *testing.T) {
	client, err := newGitHubClient(GitHubRemote{Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Client().Timeout <= 0 {
		t.Fatal("authenticated requests have no timeout")
	}
}

func TestGitHubRealHTTPAuthenticationRevalidationAndErrors(t *testing.T) {
	t.Setenv("IFTTT_CACHE_DIR", t.TempDir())
	var downloads, revalidations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("missing authentication: %q", r.Header.Get("Authorization"))
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("ref") != "release" {
			t.Errorf("ref: %s", r.URL.RawQuery)
		}
		if strings.Contains(r.URL.Path, "missing.go") {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(r.URL.Path, "denied.go") {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"message":"forbidden"}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/acme/repo/contents/file.go") {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/vnd.github.v3.raw" {
			t.Errorf("accept: %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("If-None-Match") == `"revision-one"` {
			revalidations.Add(1)
			w.WriteHeader(304)
			return
		}
		downloads.Add(1)
		w.Header().Set("ETag", `"revision-one"`)
		_, _ = io.WriteString(w, "remote contents")
	}))
	defer server.Close()
	factory, err := NewGitHubFactory([]GitHubRemote{{Repo: "acme/repo", DefaultRef: "release", Token: "secret-token", BaseURL: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	provider, target, err := factory.Provider("github://acme/repo/file.go")
	if err != nil {
		t.Fatal(err)
	}
	data, err := provider.ReadFile(target)
	if err != nil || string(data) != "remote contents" {
		t.Fatalf("initial fetch: %q %v", data, err)
	}
	if _, err := provider.ReadFile(target); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("memory cache did not avoid request")
	}
	provider.(*githubProvider).ttl = 0
	data, err = provider.ReadFile(target)
	if err != nil || string(data) != "remote contents" {
		t.Fatalf("revalidation: %q %v", data, err)
	}
	if revalidations.Load() != 1 {
		t.Fatal("expired cache did not send ETag")
	}
	_, err = provider.ReadFile("github://acme/repo/missing.go")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("404 should preserve not-exist: %v", err)
	}
	_, err = provider.ReadFile("github://acme/repo/denied.go")
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("403 incorrectly handled: %v", err)
	}
}

func TestGitHubDiskCacheSeparatesCredentialsAndAPIHosts(t *testing.T) {
	t.Setenv("IFTTT_CACHE_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, r.Header.Get("Authorization")) }))
	defer server.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "other-host") }))
	defer second.Close()
	for _, tc := range []struct{ token, host, want string }{{"first", server.URL, "Bearer first"}, {"second", server.URL, "Bearer second"}, {"first", second.URL, "other-host"}} {
		factory, err := NewGitHubFactory([]GitHubRemote{{Repo: "acme/repo", Token: tc.token, BaseURL: tc.host}})
		if err != nil {
			t.Fatal(err)
		}
		provider, target, err := factory.Provider("github://acme/repo/file.go")
		if err != nil {
			t.Fatal(err)
		}
		data, err := provider.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != tc.want {
			t.Fatalf("cache crossed credential/host boundary: want %q, got %q", tc.want, data)
		}
	}
}

func TestRemoteMemoryCacheBounded(t *testing.T) {
	p := &githubProvider{cache: make(map[string]githubCacheEntry)}
	for i := 0; i < 256; i++ {
		p.putCache(fmt.Sprint(i), githubCacheEntry{fetched: time.Unix(int64(i), 0)})
	}
	if len(p.cache) != 128 {
		t.Fatalf("unbounded cache: %d", len(p.cache))
	}
	if _, ok := p.cache["0"]; ok {
		t.Fatal("oldest entry retained")
	}
}
func TestInvalidGitHubTargets(t *testing.T) {
	for _, target := range []string{"github://owner//file", "github://owner/repo/../..", "github://owner/repo/"} {
		if _, err := parseGitHubTarget(target); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
}

func TestAuthenticatedRemoteDoesNotFollowRedirectToAnotherHost(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("Authorization") != "")
		w.Write([]byte("data"))
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	factory, err := NewGitHubFactory([]GitHubRemote{{Repo: "owner/repo", Token: "private-token", BaseURL: origin.URL}})
	if err != nil {
		t.Fatal(err)
	}
	provider, actual, err := factory.Provider("github://owner/repo/file.go?ref=redirect-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("IFTTT_CACHE_DIR", t.TempDir())
	if _, err = provider.ReadFile(actual); err == nil || leaked.Load() {
		t.Fatalf("redirect followed: err=%v credential leaked=%v", err, leaked.Load())
	}
}
