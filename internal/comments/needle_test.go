package comments

import (
	"reflect"
	"testing"
)

func TestNeedleDoesNotChangeOrdinaryExtraction(t *testing.T) {
	load := func(string) ([]byte, error) { return []byte("// ordinary comment\n"), nil }
	blocks, err := ExtractWithLoader("source.go", load)
	if err != nil || len(blocks) != 1 || blocks[0].Text != " ordinary comment" {
		t.Fatalf("ordinary comment extraction changed: %v, %v", blocks, err)
	}
	filtered, err := ExtractWithNeedle("source.go", "LINT.", load)
	if err != nil || len(filtered) != 0 {
		t.Fatalf("absent needle should skip built-in extraction: %v, %v", filtered, err)
	}
}

func TestNeedlePreservesOverriddenBuiltinExtractor(t *testing.T) {
	registryMu.RLock()
	extractor, literal, format := registry[".go"], literalExtractors[".go"], commentFormats[".go"]
	registryMu.RUnlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry[".go"], literalExtractors[".go"], commentFormats[".go"] = extractor, literal, format
		registryMu.Unlock()
	})
	RegisterLanguage(".GO", func(string, string) []Block {
		return []Block{{Start: 3, Text: "LINT.IfChange()"}}
	})
	blocks, err := ExtractWithNeedle("source.go", "LINT.", func(string) ([]byte, error) { return []byte("encoded input"), nil })
	if err != nil || !reflect.DeepEqual(blocks, []Block{{Start: 3, Text: "LINT.IfChange()"}}) {
		t.Fatalf("overriding a literal extractor lost transformed comments: %v, %v", blocks, err)
	}
}

func TestNeedleRetainsAllBlocksForFilenameAndFallbackExtraction(t *testing.T) {
	for _, path := range []string{"CMakeLists.txt", "source.unknown"} {
		t.Run(path, func(t *testing.T) {
			load := func(string) ([]byte, error) {
				return []byte("# ordinary\n# LINT.IfChange()\n# another comment\n"), nil
			}
			want, err := ExtractWithLoader(path, load)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ExtractWithNeedle(path, "LINT.", load)
			if err != nil || len(got) == 0 || !reflect.DeepEqual(got, want) {
				t.Fatalf("matching needle must retain all blocks: %v != %v (%v)", got, want, err)
			}
		})
	}
}
