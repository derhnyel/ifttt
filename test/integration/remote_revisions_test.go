package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// LINT.IfChange(remote_revision_refs)
func TestRemoteRevisionSelection(t *testing.T) {
	for _, tc := range []struct{ name, configured, query, expected string }{
		{"implicit_main", "", "", "main"},
		{"configured_default", "release", "", "release"},
		{"explicit_branch", "release", "?ref=feature/contracts", "feature/contracts"},
		{"explicit_tag", "release", "?ref=v2.0.0", "v2.0.0"},
		{"explicit_sha", "release", "?ref=0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 16)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				ref := request.URL.Query().Get("ref")
				select {
				case requests <- ref:
				default:
				}
				if request.URL.Path != "/api/v3/repos/acme/contracts/contents/docs/api.md" || ref != tc.expected {
					http.Error(w, "unexpected path or revision", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("<!-- LINT.IfChange(API) -->\nAPI version: 1\n<!-- LINT.ThenChange() -->\n"))
			}))
			defer server.Close()
			r := newDefaultRepo(t)
			r.write(t, ".ifttt-lint.yaml", "remotes:\n  - type: github\n    repo: acme/contracts\n    default_ref: '"+tc.configured+"'\n    base_url: "+server.URL+"/\n")
			r.write(t, "source.go", "// LINT.IfChange(API)\nconst version = 1\n// LINT.ThenChange(github://acme/contracts/docs/api.md"+tc.query+"#API)\n")
			requireCode(t, r, "", 0, "--vcs", "git", "source.go", "--format=json")
			select {
			case ref := <-requests:
				if ref != tc.expected {
					t.Fatalf("requested ref %q, want %q", ref, tc.expected)
				}
			default:
				t.Fatal("CLI never requested the remote snapshot")
			}
			// The selected snapshot still has to contain the requested label.
			r.write(t, "source.go", "// LINT.IfChange(API)\nconst version = 1\n// LINT.ThenChange(github://acme/contracts/docs/api.md"+tc.query+"#MISSING)\n")
			out := requireCode(t, r, "", 1, "--vcs", "git", "source.go", "--format=json")
			if !strings.Contains(out, "label_missing") {
				t.Fatalf("missing remote label was not reported: %s", out)
			}
		})
	}
}

// LINT.ThenChange(//README.md:remote_revision_refs, //internal/engine/engine.go:remote_revision_refs)
