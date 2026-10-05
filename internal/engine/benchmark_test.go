package engine

import (
	"fmt"
	core "github.com/derhnyel/ifttt/internal"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise full structure and label resolution without Git setup
// in the timed region. This isolates the engine from CLI/backend startup costs.
func BenchmarkStructuralContracts(b *testing.B) {
	prefix := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	b.Cleanup(func() { core.SetDirectivePrefix(prefix) })
	root := b.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.Chdir(previous) })
	sources := make([]string, 1000)
	for i := range sources {
		sources[i] = fmt.Sprintf("source_%d.go", i)
		source := fmt.Sprintf("// LINT.IfChange(contract)\nvar value = 1\n// LINT.ThenChange(//target_%d.go:contract)\n", i)
		target := "// LINT.IfChange(contract)\nvar value = 1\n// LINT.ThenChange()\n"
		for path, text := range map[string]string{sources[i]: source, fmt.Sprintf("target_%d.go", i): target} {
			if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, code := ValidateFiles(sources, Options{})
		if code != 0 {
			b.Fatalf("unexpected findings: %d", len(result.Findings))
		}
	}
}

// IFLINT_BENCHMARK_REPOSITORY selects an existing checkout for repeatable
// structural CPU/allocation profiles without cloning in the timed region.
func BenchmarkStructuralRepository(b *testing.B) {
	root := os.Getenv("IFLINT_BENCHMARK_REPOSITORY")
	if root == "" {
		b.Skip("set IFLINT_BENCHMARK_REPOSITORY to a checkout")
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
	prefix := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	b.Cleanup(func() { core.SetDirectivePrefix(prefix) })
	output, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		b.Fatal(err)
	}
	var sources []string
	for _, path := range strings.Split(string(output), "\x00") {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			b.Fatal(err)
		}
		if info.Mode().IsRegular() {
			sources = append(sources, path)
		}
	}
	baseline, code := ValidateFiles(sources, Options{Parallelism: 2})
	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64(len(sources)), "files")
	b.ReportMetric(float64(len(baseline.Findings)), "findings")
	for i := 0; i < b.N; i++ {
		result, status := ValidateFiles(sources, Options{Parallelism: 2})
		if status != code || len(result.Findings) != len(baseline.Findings) {
			b.Fatalf("structural result changed: status=%d, findings=%d", status, len(result.Findings))
		}
	}
}
