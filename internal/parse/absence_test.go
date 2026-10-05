package parse

import (
	"errors"
	"strings"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/comments"
)

func TestPrefixFreeBuiltinAvoidsFullTextAllocation(t *testing.T) {
	data := []byte(strings.Repeat("const value = 42;\n", 64<<10))
	p := Provider{ReadFile: func(string) ([]byte, error) { return data, nil }}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dirs, err := p.Parse("ordinary.go")
			if err != nil || len(dirs) != 0 {
				b.Fatalf("unexpected parse result: %v, %v", dirs, err)
			}
		}
	})
	// Source bytes belong to the loader. A directive-free scan should not
	// allocate another megabyte of text merely to establish absence.
	if bytes := result.AllocedBytesPerOp(); bytes > 4096 {
		t.Fatalf("prefix-free source allocated %d bytes per parse; want <=4096", bytes)
	}
}

func TestAbsenceCheckPreservesCustomExtractor(t *testing.T) {
	const ext = ".absence-custom"
	comments.RegisterLanguage(ext, func(string, string) []comments.Block {
		return []comments.Block{{Start: 7, Text: core.CurrentDirectiveSyntax().TokenIfChange}}
	})
	// Restore fallback extraction for this otherwise unused extension.
	t.Cleanup(func() { comments.RegisterLanguage(ext, nil) })
	dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return []byte("encoded input"), nil }}).Parse("source" + ext)
	if err != nil || len(dirs) != 1 || dirs[0].Kind != core.IfChange || dirs[0].Line != 7 {
		t.Fatalf("custom extraction changed: %v, %v", dirs, err)
	}
}

func TestAbsenceCheckReadsConfiguredPrefixAndWholeFile(t *testing.T) {
	previous := core.CurrentDirectiveSyntax().Prefix
	t.Cleanup(func() { core.SetDirectivePrefix(previous) })
	for _, prefix := range []string{"SENTRY", "LINT", "CUSTOM"} {
		t.Run(prefix, func(t *testing.T) {
			core.SetDirectivePrefix(prefix)
			data := []byte(strings.Repeat("ordinary\n", 2048) + string([]byte{0xa0}) + "\n// " + prefix + ".IfChange\n")
			dirs, err := (Provider{ReadFile: func(string) ([]byte, error) { return data, nil }}).Parse("source.go")
			if err != nil || len(dirs) != 1 || dirs[0].Kind != core.IfChange || dirs[0].Line != 2050 {
				t.Fatalf("late configured prefix was lost: %v, %v", dirs, err)
			}
		})
	}
}

func TestAbsenceCheckPreservesReadErrors(t *testing.T) {
	want := errors.New("unreadable source")
	_, err := (Provider{ReadFile: func(string) ([]byte, error) { return nil, want }}).Parse("ordinary.go")
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want read error", err)
	}
}
