package verified

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestLockExcludesAnotherHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".lock") // directory created by Lock
	unlock, err := Lock(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	told := 0
	if u, err := Lock(cancelled, path, func() { told++ }); !errors.Is(err, context.Canceled) || u != nil || told != 1 {
		t.Fatalf("a held lock: unlock %v, err %v, told %d", u != nil, err, told)
	}
	unlock()
	told = 0
	u, err := Lock(cancelled, path, func() { told++ })
	if err != nil || told != 0 {
		t.Fatalf("a free lock: err %v, told %d", err, told)
	}
	u()
}
