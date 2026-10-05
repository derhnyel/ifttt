package engine

import (
	"fmt"
	"testing"

	core "github.com/derhnyel/ifttt/internal"
)

func TestPreloadHasBoundedSchedulingAllocations(t *testing.T) {
	files := make([]string, 1000)
	for i := range files {
		files[i] = fmt.Sprintf("source_%d.go", i)
	}
	getter := func(string) ([]core.LintDirective, error) { return nil, nil }
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			loaded := preloadDirectivesWithGetter(files, 2, getter)
			if len(loaded) != len(files) {
				b.Fatalf("lost preload results: %d", len(loaded))
			}
		}
	})
	// Include the result map, but leave no budget for allocating a new worker
	// closure/goroutine for each of these otherwise allocation-free reads.
	if bytes := result.AllocedBytesPerOp(); bytes > 160<<10 {
		t.Fatalf("preload scheduling allocated %d bytes; want <=%d", bytes, 160<<10)
	}
}
