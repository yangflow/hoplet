package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFirstSnapshotRate measures the first snapshot of many small files and
// of a few large ones. It runs only with HOPLET_PERF=1: it writes a lot.
func TestFirstSnapshotRate(t *testing.T) {
	if os.Getenv("HOPLET_PERF") == "" {
		t.Skip("set HOPLET_PERF=1 to measure")
	}
	g := newGit(t)
	for name, tc := range map[string]struct{ files, size int }{
		"60000 files of 1 KiB": {60000, 1 << 10},
		"200 files of 10 MiB":  {200, 10 << 20},
	} {
		repo := t.TempDir()
		initRepo(t, g, repo, map[string]string{"a": "1\n"})
		body := strings.Repeat("x", tc.size)
		for i := 0; i < tc.files; i++ {
			write(t, repo, fmt.Sprintf("d%03d/f%05d.txt", i%300, i), body[:tc.size-8]+fmt.Sprintf("%08d", i))
		}
		r := Repo{Path: ".", Abs: repo}
		objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if _, err := g.Snapshot(context.Background(), r, objects); err != nil {
			t.Fatal(err)
		}
		first := time.Since(start)
		start = time.Now()
		if _, err := g.Snapshot(context.Background(), r, objects); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: first snapshot %v (%.0f files/s, %.0f MB/s), second %v", name, first,
			float64(tc.files)/first.Seconds(), float64(tc.files*tc.size)/1e6/first.Seconds(), time.Since(start))
	}
}
