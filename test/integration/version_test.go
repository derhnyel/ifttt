package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestVersionWithoutProject(t *testing.T) {
	for _, malformedConfig := range []bool{false, true} {
		name := "unconfigured"
		if malformedConfig {
			name = "malformed config"
		}
		t.Run(name, func(t *testing.T) {
			assertVersion(t, binary, malformedConfig, "ifttt dev (commit unknown)\n")
		})
	}
}

func TestVersionBuildMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ifttt")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags=-X main.version=v1.2.3 -X main.commit=abc1234", "-o", path, "./cmd/ifttt")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI with release metadata: %v\n%s", err, output)
	}
	assertVersion(t, path, true, "ifttt v1.2.3 (commit abc1234)\n")
}

func assertVersion(t *testing.T, path string, malformedConfig bool, want string) {
	t.Helper()
	dir := t.TempDir()
	if malformedConfig {
		if err := os.WriteFile(filepath.Join(dir, ".ifttt-lint.yaml"), []byte("output: [\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Keep stdin open without data: --version must exit without reading a diff.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir}
	if integrationCoverDir != "" && path == binary {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+integrationCoverDir)
	}
	cmd.Stdin = reader
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("--version outside Git without executables and with open stdin: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	if got := stdout.String(); got != want {
		t.Fatalf("--version stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("--version stderr = %q, want empty", got)
	}
}
