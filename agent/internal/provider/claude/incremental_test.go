package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

func scanOne(t *testing.T, c *Provider) provider.Source {
	t.Helper()
	srcs, err := c.Scan(context.Background())
	if err != nil || len(srcs) != 1 {
		t.Fatalf("scan: %d sources, err=%v", len(srcs), err)
	}
	return srcs[0]
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(data)
	f.Close()
}

func TestScanKeepsSourcesAndReadsNothingWhenUnchanged(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "-work"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "-work", "s.jsonl")
	appendFile(t, path, `{"type":"user","cwd":"/work","message":{"content":"first prompt of the day"}}`+"\n")
	c := New(root, nil)
	ctx := context.Background()
	first := scanOne(t, c)
	if provider.PeekTitle(ctx, first) != "first prompt of the day" || first.(interface{ Cwd() string }).Cwd() != "/work" {
		t.Fatal("title or cwd not read")
	}
	before := SessionBytesRead()
	again := scanOne(t, c)
	if again != first {
		t.Fatal("an unchanged file must keep its source")
	}
	provider.PeekTitle(ctx, again)
	again.(interface{ Cwd() string }).Cwd()
	if n := SessionBytesRead() - before; n != 0 {
		t.Fatalf("unchanged scan read %d bytes", n)
	}

	// A small file that grew can have a new title: an ai-title line lands after the first prompt.
	appendFile(t, path, `{"type":"ai-title","aiTitle":"Named later"}`+"\n")
	if got := provider.PeekTitle(ctx, scanOne(t, c)); got != "Named later" {
		t.Fatalf("title after growth: %q", got)
	}

	// A file replaced under the same name drops the cached values.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, `{"type":"user","cwd":"/other","message":{"content":"a different session"}}`+"\n")
	replaced := scanOne(t, c)
	if provider.PeekTitle(ctx, replaced) != "a different session" || replaced.(interface{ Cwd() string }).Cwd() != "/other" {
		t.Fatal("replaced file kept stale title or cwd")
	}

	// Same size with a new mtime is a rewrite in place.
	data, _ := os.ReadFile(path)
	rewritten := strings.Replace(string(data), "a different", "the changed", 1)
	if len(rewritten) != len(data) {
		t.Fatal("fixture sizes differ")
	}
	if err := os.WriteFile(path, []byte(rewritten), 0600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	if got := provider.PeekTitle(ctx, scanOne(t, c)); got != "the changed session" {
		t.Fatalf("title after in-place rewrite: %q", got)
	}
}

// Once the title window is full, growth cannot change the title and costs no head read.
func TestTitleIsKeptWhenGrowthCannotChangeIt(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "-work"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "-work", "s.jsonl")
	appendFile(t, path, `{"type":"user","message":{"content":"the opening prompt"}}`+"\n")
	appendFile(t, path, strings.Repeat(`{"type":"assistant","message":{"content":"`+strings.Repeat("z", 1000)+`"}}`+"\n", titlePeek/1000))
	c := New(root, nil)
	ctx := context.Background()
	if provider.PeekTitle(ctx, scanOne(t, c)) != "the opening prompt" {
		t.Fatal("title")
	}
	appendFile(t, path, `{"type":"ai-title","aiTitle":"beyond the window"}`+"\n")
	before := SessionBytesRead()
	if got := provider.PeekTitle(ctx, scanOne(t, c)); got != "the opening prompt" || SessionBytesRead() != before {
		t.Fatalf("title %q, read %d bytes", got, SessionBytesRead()-before)
	}
}
