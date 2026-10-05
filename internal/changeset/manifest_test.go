package changeset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestStrictDecoding(t *testing.T) {
	valid := "version: 1\nrepositories:\n- repo: Acme/Api\n  path: ./checkout\n  base: main\n  head: feature\n"
	for _, body := range []string{valid + "typo: true\n", valid + "---\nversion: 1\n", strings.Replace(valid, "  head: feature", "  head: feature\n  extra: true", 1), strings.Replace(valid, "version: 1", "version: 99", 1), strings.Replace(valid, "  head: feature", "  head: ''", 1), strings.Replace(valid, "Acme/Api", "../api", 1), valid + "version: 1\n", strings.Repeat("x", (1<<20)+1)} {
		p := filepath.Join(t.TempDir(), "changes.yaml")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(p); err == nil {
			t.Fatalf("accepted invalid manifest: %.150s", body)
		}
	}
	p := filepath.Join(t.TempDir(), "changes.yaml")
	if err := os.WriteFile(p, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Repositories) != 1 || m.Repositories[0].Repo != "acme/api" || m.Repositories[0].Path != filepath.Join(filepath.Dir(p), "checkout") || m.Repositories[0].VCS != "auto" {
		t.Fatalf("incorrect manifest resolution: %+v", m)
	}
}
