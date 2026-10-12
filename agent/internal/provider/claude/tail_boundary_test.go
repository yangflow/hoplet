package claude

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

func TestClaudeBoundaryTail(t *testing.T) {
	const budget = 1 << 20
	for _, size := range []int{budget - 1, budget} {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		tail := bytes.Repeat([]byte("x"), size-1)
		if err := os.WriteFile(path, tail, 0600); err != nil {
			t.Fatal(err)
		}
		src := &claudeSource{file: sessionFile{Path: path}}
		got, err := src.Read(context.Background(), 0, budget)
		if err != nil || len(got) != 0 {
			t.Fatalf("unfinished tail: bytes=%d err=%v", len(got), err)
		}
		complete := append(tail, '\n')
		if err := os.WriteFile(path, append(complete, []byte("{}\n")...), 0600); err != nil {
			t.Fatal(err)
		}
		got, err = src.Read(context.Background(), 0, budget)
		if err != nil || !bytes.Equal(got, complete) {
			t.Fatalf("completed tail: bytes=%d want=%d err=%v", len(got), size, err)
		}
		next, err := src.Read(context.Background(), int64(len(got)), budget)
		if err != nil || string(next) != "{}\n" {
			t.Fatalf("following line: bytes=%d err=%v", len(next), err)
		}
	}
}

func TestClaudeOversizedTailAndPrefix(t *testing.T) {
	limit := int(provider.MaxLineBytes)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	prefix := []byte("{}\n")
	tail := bytes.Repeat([]byte("x"), limit)
	if err := os.WriteFile(path, append(prefix, tail...), 0600); err != nil {
		t.Fatal(err)
	}
	src := &claudeSource{file: sessionFile{Path: path}}
	got, err := src.Read(context.Background(), 0, 1<<20)
	if err != nil || !bytes.Equal(got, prefix) {
		t.Fatalf("prefix: bytes=%d err=%v", len(got), err)
	}
	for _, newline := range []bool{false, true} {
		if newline {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Write([]byte("\n{}\n"))
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err = src.Read(context.Background(), int64(len(prefix)), 1<<20)
		if len(got) != 0 || !errors.Is(err, provider.ErrLineTooLarge) || !strings.Contains(err.Error(), "offset 3") {
			t.Fatalf("blocked tail: bytes=%d err=%v", len(got), err)
		}
	}
}

func BenchmarkClaudeOversizedRead(b *testing.B) {
	path := filepath.Join(b.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	if err = f.Truncate(128 << 20); err != nil {
		b.Fatal(err)
	}
	f.Close()
	src := &claudeSource{file: sessionFile{Path: path}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := src.Read(context.Background(), 0, 1<<30)
		if len(data) != 0 || !errors.Is(err, provider.ErrLineTooLarge) {
			b.Fatalf("bytes=%d err=%v", len(data), err)
		}
	}
}
