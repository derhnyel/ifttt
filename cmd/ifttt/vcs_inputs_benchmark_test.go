package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkTrackedFileSelection(b *testing.B) {
	root := os.Getenv("IFTTT_BENCHMARK_REPOSITORY")
	if root == "" {
		b.Skip("set IFTTT_BENCHMARK_REPOSITORY to an existing checkout")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		b.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.Chdir(previous) })
	output, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		b.Fatal(err)
	}
	tracked := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	selected, err := expandTrackedFiles([]string{"**/*"}, tracked)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64(len(selected)), "files")
	for i := 0; i < b.N; i++ {
		files, err := expandTrackedFiles([]string{"**/*"}, tracked)
		if err != nil || len(files) != len(selected) {
			b.Fatalf("selection changed: %d files, %v", len(files), err)
		}
	}
}
