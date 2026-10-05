package changeset

import (
	"errors"
	"testing"
)

func TestSnapshotReadErrorIsDeterministic(t *testing.T) {
	first, second := errors.New("snapshot first.go is a symlink"), errors.New("snapshot second.go is a symlink")
	for _, order := range [][]error{{second, first}, {first, second}} {
		files := &snapshotFiles{readErrors: order}
		if err := files.evidenceError(); err == nil || err.Error() != first.Error() {
			t.Fatalf("worker order changed selected failure: %v", err)
		}
	}
	if err := (&snapshotFiles{}).evidenceError(); err != nil {
		t.Fatalf("empty evidence reported an error: %v", err)
	}
}
