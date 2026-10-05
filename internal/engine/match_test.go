package engine

import (
	"fmt"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

// LINT.IfChange(match_engine_tests)
func TestMatchOpaqueSourceAndCustomPrefix(t *testing.T) {
	prefix := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("CHECK")
	defer core.SetDirectivePrefix(prefix)
	files := &memoryFileProvider{files: map[string][]byte{
		"source.go": []byte("// CHECK.Label(\"A\")\none\n// CHECK.EndLabel\n// CHECK.Match(\"#A\", \"target.go#B\")\n"),
		"target.go": []byte("// CHECK.Label(\"B\")\ntwo\n// CHECK.EndLabel\n"),
	}}
	result, code := Lint("", Options{Files: files, RevisionChanges: map[string]*core.FileChanges{"source.go": {Opaque: true}}, MatchCandidates: []string{"source.go"}})
	if code != 1 || !hasRule(result, "source.go", "match_mismatch") {
		t.Fatalf("opaque evidence must not skip content checks: %+v", result)
	}
}

// LINT.ThenChange(//internal/engine/match.go:match_contract)

func BenchmarkMatchContracts(b *testing.B) {
	prefix := core.CurrentDirectiveSyntax().Prefix
	core.SetDirectivePrefix("LINT")
	b.Cleanup(func() { core.SetDirectivePrefix(prefix) })
	provider := &memoryFileProvider{files: map[string][]byte{}}
	sources := make([]string, 100)
	for i := range sources {
		sources[i] = fmt.Sprintf("source_%d.txt", i)
		provider.files[sources[i]] = []byte(fmt.Sprintf("// LINT.IfChange(A)\nversion=1.2.3\n// LINT.ThenChange()\n// LINT.Match(\":A\", \"//target_%d.txt:B\", '([0-9.]+)')\n", i))
		provider.files[fmt.Sprintf("target_%d.txt", i)] = []byte("// LINT.IfChange(B)\nversion: 1.2.3\n// LINT.ThenChange()\n")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, code := ValidateFiles(sources, Options{Files: provider, StrictPaths: true})
		if code != 0 {
			b.Fatalf("unexpected Match result: %+v", result)
		}
	}
}
