package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// A first line longer than the read window comes back alone, up to provider.MaxLineBytes including its LF.
func TestLongLineReadsAlone(t *testing.T) {
	const budget = 1 << 20
	for _, size := range []int{budget, budget + 1, 20 << 20, int(provider.MaxLineBytes)} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			line := []byte(`{"text":"` + strings.Repeat("x", size-12) + "\"}\n")
			if len(line) != size {
				t.Fatalf("fixture size %d", len(line))
			}
			if err := os.WriteFile(path, append(line, []byte("{}\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			src := &claudeSource{file: sessionFile{Path: path}}
			got, err := src.Read(context.Background(), 0, budget)
			if err != nil || !bytes.Equal(got, line) {
				t.Fatalf("line: bytes=%d err=%v", len(got), err)
			}
			next, err := src.Read(context.Background(), int64(size), budget)
			if err != nil || string(next) != "{}\n" {
				t.Fatalf("following line: %q err=%v", next, err)
			}
			// A small window still yields the whole first line, never a part of it.
			got, err = src.Read(context.Background(), 0, budget/2)
			if err != nil || !bytes.Equal(got, line) {
				t.Fatalf("small window: bytes=%d err=%v", len(got), err)
			}
		})
	}
}

func TestLineAboveLimitIsRefusedWithoutContent(t *testing.T) {
	for _, withLF := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		data := bytes.Repeat([]byte("x"), int(provider.MaxLineBytes))
		if withLF {
			data = append(data, '\n') // 32 MiB of content plus the LF is one byte over
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		src := &claudeSource{file: sessionFile{Path: path}}
		for _, window := range []int64{1 << 20, 1 << 30} {
			got, err := src.Read(context.Background(), 0, window)
			var tooLarge *provider.LineTooLargeError
			if len(got) != 0 || !errors.Is(err, provider.ErrLineTooLarge) || !errors.As(err, &tooLarge) || tooLarge.Offset != 0 {
				t.Fatalf("withLF=%v window=%d: bytes=%d err=%v", withLF, window, len(got), err)
			}
			if !strings.Contains(err.Error(), "offset 0, limit 33554432") || strings.Contains(err.Error(), "xxx") {
				t.Fatalf("message: %v", err)
			}
		}
	}
}

// An unfinished long line is read once; until the file grows, further reads touch no bytes.
func TestUnfinishedLongLineWaitsForGrowth(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "p")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 5<<20), 0600); err != nil {
		t.Fatal(err)
	}
	c := New(root, nil)
	scan := func() provider.Source {
		srcs, err := c.Scan(context.Background())
		if err != nil || len(srcs) != 1 {
			t.Fatalf("scan: %d %v", len(srcs), err)
		}
		return srcs[0]
	}
	src := scan()
	if got, err := src.Read(context.Background(), 0, 1<<20); len(got) != 0 || err != nil {
		t.Fatalf("unfinished: %d %v", len(got), err)
	}
	before := SessionBytesRead()
	src = scan()
	if got, err := src.Read(context.Background(), 0, 1<<20); len(got) != 0 || err != nil || SessionBytesRead() != before {
		t.Fatalf("repeat read: %d %v, bytes read %d", len(got), err, SessionBytesRead()-before)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n")
	f.Close()
	src = scan()
	if got, err := src.Read(context.Background(), 0, 1<<20); err != nil || len(got) != 5<<20+1 {
		t.Fatalf("completed: %d %v", len(got), err)
	}
}
