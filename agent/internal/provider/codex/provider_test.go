package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

const threadA = "01a14adf-8185-7132-97c5-ad6f176a3265"

// rollout builds a synthetic rollout: a session_meta line carrying about 70 KB of base instructions, then n
// response_item lines.
func rollout(id, cwd string, n int) []byte {
	var b bytes.Buffer
	meta, _ := json.Marshal(map[string]any{
		"timestamp": "2026-10-18T01:18:44.000Z", "ordinal": 0, "type": "session_meta",
		"payload": map[string]any{
			"id": id, "session_id": id, "cwd": cwd, "originator": "codex_cli", "source": "cli",
			"base_instructions": map[string]any{"text": strings.Repeat("You are Codex. ", 4700)},
		},
	})
	b.Write(meta)
	b.WriteByte('\n')
	b.Write(appendLines(1, n))
	return b.Bytes()
}

func appendLines(from, n int) []byte {
	var b bytes.Buffer
	for i := from; i < from+n; i++ {
		line, _ := json.Marshal(map[string]any{
			"timestamp": "2026-10-18T01:19:00.000Z", "ordinal": i, "type": "response_item",
			"payload": map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": fmt.Sprintf("line %d héllo 你好 %s", i, strings.Repeat("x", i%50))}}},
		})
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func compress(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	enc, err := zstd.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func rolloutPath(root, id string) string {
	return filepath.Join(root, "2026", "10", "18", "rollout-2026-10-18T01-18-44-"+id+".jsonl")
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanOne(t *testing.T, p *Provider) provider.Source {
	t.Helper()
	srcs, err := p.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 {
		t.Fatalf("scan found %d sources, want 1", len(srcs))
	}
	return srcs[0]
}

// drain reads src from offset with a small window until nothing new, as the sync client does.
func drain(t *testing.T, src provider.Source, offset, window int64) ([]byte, int64) {
	t.Helper()
	var out []byte
	for {
		got, err := src.Read(context.Background(), offset, window)
		if err != nil {
			t.Fatalf("read at %d: %v", offset, err)
		}
		if len(got) == 0 {
			return out, offset
		}
		if got[len(got)-1] != '\n' {
			t.Fatalf("read at %d did not end at a line", offset)
		}
		out = append(out, got...)
		offset += int64(len(got))
	}
}

func TestScanReadsIDAndCwdFromFirstLine(t *testing.T) {
	root := t.TempDir()
	data := rollout(threadA, "/work/proj", 3)
	write(t, rolloutPath(root, threadA), data)
	// The file name's ID is not trusted over the first line.
	src := scanOne(t, New(root, nil))
	if src.ID() != threadA || src.Cwd() != "/work/proj" || src.Dir() != "2026/10/18" {
		t.Fatalf("got id %q cwd %q dir %q", src.ID(), src.Cwd(), src.Dir())
	}
	if !src.InProject("/work/proj") || src.InProject("/work/other") {
		t.Fatal("InProject does not follow the cwd")
	}
	if !provider.ValidSessionID(src.ID()) {
		t.Fatal("thread id is not a valid session id")
	}
	if size, _ := src.Size(context.Background()); size != int64(len(data)) {
		t.Fatalf("size %d, want %d", size, len(data))
	}
	if len(data) < 64<<10 {
		t.Fatalf("fixture first line is only %d bytes", len(data))
	}
}

func TestScanSkipsUnreadyAndExcluded(t *testing.T) {
	root := t.TempDir()
	write(t, rolloutPath(root, threadA), []byte(`{"type":"session_meta","payload":{"id":"`+threadA+`"`)) // no LF yet
	other := "01a14adf-8185-7132-97c5-ad6f176a3266"
	write(t, rolloutPath(root, other), rollout(other, "/tmp/scratch", 1))
	write(t, filepath.Join(root, "2026", "10", "18", "notes.jsonl"), []byte("{}\n"))
	srcs, _ := New(root, []string{"/tmp/"}).Scan(context.Background())
	if len(srcs) != 0 {
		t.Fatalf("got %d sources", len(srcs))
	}
	// Once the first turn finishes writing the line, the session appears.
	write(t, rolloutPath(root, threadA), rollout(threadA, "/work/proj", 1))
	p := New(root, []string{"/tmp/"})
	if src := scanOne(t, p); src.ID() != threadA {
		t.Fatalf("got %q", src.ID())
	}
}

func TestReadPlainAndCompressedYieldTheSameBytes(t *testing.T) {
	data := rollout(threadA, "/work/proj", 400)
	for _, window := range []int64{4 << 10, 64 << 10, 1 << 20} {
		plainRoot, zstRoot := t.TempDir(), t.TempDir()
		write(t, rolloutPath(plainRoot, threadA), data)
		write(t, rolloutPath(zstRoot, threadA)+".zst", compress(t, data))

		got, end := drain(t, scanOne(t, New(plainRoot, nil)), 0, window)
		if !bytes.Equal(got, data) || end != int64(len(data)) {
			t.Fatalf("plain, window %d: read %d of %d bytes", window, len(got), len(data))
		}
		zsrc := scanOne(t, New(zstRoot, nil))
		if size, _ := zsrc.Size(context.Background()); size != -1 {
			t.Fatalf("size of an unread .zst = %d, want -1", size)
		}
		got, end = drain(t, zsrc, 0, window)
		if !bytes.Equal(got, data) || end != int64(len(data)) {
			t.Fatalf("zst, window %d: read %d of %d bytes", window, len(got), len(data))
		}
	}
}

func TestCompressedLengthIsKnownAfterARead(t *testing.T) {
	root := t.TempDir()
	data := rollout(threadA, "/work/proj", 20)
	write(t, rolloutPath(root, threadA)+".zst", compress(t, data))
	p := New(root, nil)
	src := scanOne(t, p)
	drain(t, src, 0, 1<<20)
	if size, _ := src.Size(context.Background()); size != int64(len(data)) {
		t.Fatalf("size %d, want %d", size, len(data))
	}
	// An unchanged compressed session reads no bytes on a rescan or at its end.
	before := SessionBytesRead()
	src = scanOne(t, p)
	if got, err := src.Read(context.Background(), int64(len(data)), 1<<20); err != nil || len(got) != 0 {
		t.Fatalf("read at the end: %d bytes, %v", len(got), err)
	}
	if _, err := src.Read(context.Background(), int64(len(data))+1, 1<<20); !errors.Is(err, provider.ErrBeyondEnd) {
		t.Fatalf("read beyond the end: %v", err)
	}
	if n := SessionBytesRead() - before; n != 0 {
		t.Fatalf("rescan read %d bytes", n)
	}
}

func TestCursorSurvivesCompressionAndDecompressionInPlace(t *testing.T) {
	root := t.TempDir()
	path := rolloutPath(root, threadA)
	data := rollout(threadA, "/work/proj", 100)
	write(t, path, data)
	p := New(root, nil)
	src := scanOne(t, p)

	// Part of the session syncs from the plain file.
	var synced []byte
	part, cursor := drain(t, src, 0, 8<<10)
	synced = append(synced, part...)

	// Codex compresses it in place; the next scan sees only the .zst. Lines are added while it is compressed
	// elsewhere (a resume decompresses first), so here the file only changes encoding.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	write(t, path+".zst", compress(t, data))
	src = scanOne(t, p)
	more := appendLines(100, 10)
	// Codex resumes: it decompresses to the same prefix and appends.
	if err := os.Remove(path + ".zst"); err != nil {
		t.Fatal(err)
	}
	data = append(data, more...)
	write(t, path, data)
	part, cursor = drain(t, scanOne(t, p), cursor, 8<<10)
	synced = append(synced, part...)

	// A cursor in the middle of the data continues from a compressed file as well.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	write(t, path+".zst", compress(t, data))
	mid := int64(len(rollout(threadA, "/work/proj", 40)))
	tail, end := drain(t, scanOne(t, p), mid, 4<<10)
	if !bytes.Equal(tail, data[mid:]) || end != int64(len(data)) {
		t.Fatalf("resume from %d in a .zst: got %d bytes, want %d", mid, len(tail), len(data)-int(mid))
	}
	_ = src
	if !bytes.Equal(synced, data) || cursor != int64(len(data)) {
		t.Fatalf("synced %d bytes, cursor %d, want %d", len(synced), cursor, len(data))
	}
}

func TestBothEncodingsPresentDuringARename(t *testing.T) {
	root := t.TempDir()
	path := rolloutPath(root, threadA)
	data := rollout(threadA, "/work/proj", 50)
	write(t, path+".zst", compress(t, data))
	// The .jsonl is still being written back: shorter than the cursor, so the .zst serves the read.
	write(t, path, rollout(threadA, "/work/proj", 10))
	src := scanOne(t, New(root, nil))
	cursor := int64(len(rollout(threadA, "/work/proj", 30)))
	got, end := drain(t, src, cursor, 1<<20)
	if !bytes.Equal(got, data[cursor:]) || end != int64(len(data)) {
		t.Fatal("bytes differ across the encodings")
	}
	// Whichever file is missing at the moment of the read, the other serves it.
	if err := os.Remove(path + ".zst"); err != nil {
		t.Fatal(err)
	}
	write(t, path, data)
	if got, _ := drain(t, src, 0, 1<<20); !bytes.Equal(got, data) {
		t.Fatal("plain after the .zst vanished differs")
	}
}

func TestShrunkPlainFileIsBeyondEnd(t *testing.T) {
	root := t.TempDir()
	data := rollout(threadA, "/work/proj", 10)
	write(t, rolloutPath(root, threadA), data)
	src := scanOne(t, New(root, nil))
	if _, err := src.Read(context.Background(), int64(len(data))+10, 1<<20); !errors.Is(err, provider.ErrBeyondEnd) {
		t.Fatalf("got %v", err)
	}
}

func TestIncompleteTailIsDeferredAndFirstLineAboveWindowIsReturned(t *testing.T) {
	for _, zst := range []bool{false, true} {
		root := t.TempDir()
		data := rollout(threadA, "/work/proj", 2)
		tail := []byte(`{"type":"response_item","pay`)
		full := append(append([]byte(nil), data...), tail...)
		if zst {
			write(t, rolloutPath(root, threadA)+".zst", compress(t, full))
		} else {
			write(t, rolloutPath(root, threadA), full)
		}
		got, end := drain(t, scanOne(t, New(root, nil)), 0, 1<<10) // window far below the 70 KB first line
		if !bytes.Equal(got, data) || end != int64(len(data)) {
			t.Fatalf("zst=%v: read %d bytes, want %d", zst, len(got), len(data))
		}
	}
}

func TestUnchangedScanReadsNothing(t *testing.T) {
	root := t.TempDir()
	write(t, rolloutPath(root, threadA), rollout(threadA, "/work/proj", 5))
	p := New(root, nil)
	scanOne(t, p)
	before := SessionBytesRead()
	scanOne(t, p)
	scanOne(t, p)
	if n := SessionBytesRead() - before; n != 0 {
		t.Fatalf("rescans read %d bytes", n)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	write(t, rolloutPath(root, threadA)+".zst", compress(t, rollout(threadA, "/work/proj", 2)))
	p := New(root, nil)
	found, ok := p.Find(threadA)
	if !ok || found.Cwd != "/work/proj" || found.NativePath != rolloutPath(root, threadA)+".zst" {
		t.Fatalf("got %+v %v", found, ok)
	}
	if _, ok := p.Find("01a14adf-0000-7000-8000-000000000000"); ok {
		t.Fatal("found a session that does not exist")
	}
	if _, ok := p.Find("../x"); ok {
		t.Fatal("found an invalid id")
	}
}

func TestIsAStore(t *testing.T) {
	var p provider.Provider = New(t.TempDir(), nil)
	if _, ok := p.(provider.Store); !ok {
		t.Fatal("the codex provider does not declare handoff")
	}
	if p.Name() != "codex" {
		t.Fatalf("name %q", p.Name())
	}
}
