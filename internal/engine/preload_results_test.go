package engine

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/derhnyel/ifttt/internal"
)

func TestPreloadWorkersRetainAllResultsAndErrors(t *testing.T) {
	files := []string{"a", "b", "broken", "c", "d", "e"}
	wantError := errors.New("unreadable source")
	for _, workers := range []int{-1, 0, 1, 2, 20} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			var active, peak, calls atomic.Int32
			getter := func(path string) ([]core.LintDirective, error) {
				calls.Add(1)
				current := active.Add(1)
				for {
					previous := peak.Load()
					if previous >= current || peak.CompareAndSwap(previous, current) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				active.Add(-1)
				if path == "broken" {
					return nil, wantError
				}
				return []core.LintDirective{{Kind: core.IfChange, Name: path}}, nil
			}
			loaded := preloadDirectivesWithGetter(files, workers, getter)
			if len(loaded) != len(files) || calls.Load() != int32(len(files)) || active.Load() != 0 {
				t.Fatalf("lost or repeated reads: results=%d calls=%d active=%d", len(loaded), calls.Load(), active.Load())
			}
			limit := max(1, workers)
			if limit > len(files) {
				limit = len(files)
			}
			if peak.Load() < 1 || peak.Load() > int32(limit) {
				t.Fatalf("peak readers=%d, limit=%d", peak.Load(), limit)
			}
			for _, path := range files {
				result := loaded[path]
				if path == "broken" {
					if !errors.Is(result.err, wantError) {
						t.Fatalf("lost error: %v", result.err)
					}
				} else if result.err != nil || len(result.dirs) != 1 || result.dirs[0].Name != path {
					t.Fatalf("incorrect result for %s: %+v", path, result)
				}
			}
		})
	}
}

func TestEmptyPreloadDoesNotReadFiles(t *testing.T) {
	loaded := preloadDirectivesWithGetter(nil, 2, func(string) ([]core.LintDirective, error) {
		t.Fatal("empty preload called getter")
		return nil, nil
	})
	if len(loaded) != 0 {
		t.Fatalf("unexpected empty-input results: %v", loaded)
	}
}
