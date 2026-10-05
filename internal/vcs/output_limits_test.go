package vcs

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Exercise os/exec's real pipe copying: direct Writer.Write tests do not cover
// optimized io.Copy paths such as a promoted bytes.Buffer.ReadFrom method.
func TestVCSCommandOutputBounds(t *testing.T) {
	if stream := os.Getenv("IFTTT_TEST_VCS_OUTPUT"); stream != "" {
		file, size := os.Stdout, 33<<20
		if stream == "stderr" {
			file, size = os.Stderr, 2<<20
		}
		block := []byte(strings.Repeat("x", 64<<10))
		for written := 0; written < size; written += len(block) {
			if _, err := file.Write(block); err != nil {
				os.Exit(0)
			}
		}
		os.Exit(0)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			t.Setenv("IFTTT_TEST_VCS_OUTPUT", stream)
			backend := Backend{Kind: os.Args[0], Root: t.TempDir()}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := backend.run(ctx, "-test.run=^TestVCSCommandOutputBounds$")
			if err == nil || !strings.Contains(err.Error(), "output exceeds") {
				t.Fatalf("oversized %s must report the output bound, got %v", stream, err)
			}
		})
	}
}
