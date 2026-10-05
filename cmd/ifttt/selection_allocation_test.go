package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTrackedSelectionAvoidsRepeatedPatternAndStatAllocations(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.Mkdir("nested", 0700); err != nil {
		t.Fatal(err)
	}
	tracked := make([]string, 512)
	for i := range tracked {
		tracked[i] = fmt.Sprintf("nested/source_%04d.go", i)
		if err := os.WriteFile(tracked[i], []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Compare against the platform's actual allocation cost for one status
	// check per file, rather than assuming a Linux/macOS/Windows structure size.
	statAllocations := testing.AllocsPerRun(3, func() {
		for _, path := range tracked {
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		}
	})
	cleanAllocations := testing.AllocsPerRun(3, func() {
		for _, path := range tracked {
			if filepath.Clean(path) == "" {
				t.Fatal("empty normalized path")
			}
		}
	})
	selectionAllocations := testing.AllocsPerRun(3, func() {
		files, err := expandTrackedFiles([]string{"**/*.go"}, tracked)
		if err != nil || len(files) != len(tracked) {
			t.Fatalf("selected %d files: %v", len(files), err)
		}
	})
	// Budget for path splitting, result growth and the set; do not allocate
	// another status check and another pattern split for every matched file.
	budget := statAllocations + cleanAllocations + float64(len(tracked))*1.25 + 256
	if selectionAllocations > budget {
		t.Fatalf("selection allocated %.0f times; budget %.0f including %.0f for file status", selectionAllocations, budget, statAllocations)
	}
}
