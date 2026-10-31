package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestBlobs(t *testing.T) {
	s := newStore(t)
	ref, err := s.PutBlob(strings.NewReader("tool output"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Size != 11 || len(ref.SHA256) != 64 {
		t.Fatalf("ref %+v", ref)
	}
	again, err := s.PutBlob(strings.NewReader("tool output"))
	if err != nil || again != ref {
		t.Fatalf("same content: %+v, %v", again, err)
	}
	rc, err := s.OpenBlob(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if got := readAll(t, rc); got != "tool output" {
		t.Fatalf("content %q", got)
	}
	missing := proto.BlobRef{SHA256: strings.Repeat("0", 64)}
	if _, err := s.OpenBlob(missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
	if _, err := s.OpenBlob(proto.BlobRef{SHA256: "../../etc/passwd"}); err == nil {
		t.Fatal("bad reference accepted")
	}
	tmps, _ := filepath.Glob(filepath.Join(s.dir, "blobs", ".tmp-*"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left behind: %v", tmps)
	}
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBlobRejectsBadReferences(t *testing.T) {
	s := newStore(t)
	ref, err := s.PutBlob(strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	upper := proto.BlobRef{SHA256: strings.ToUpper(ref.SHA256), Size: ref.Size}
	if _, err := s.OpenBlob(upper); err == nil {
		t.Fatal("uppercase digest accepted")
	}
	wrongSize := proto.BlobRef{SHA256: ref.SHA256, Size: ref.Size + 1}
	if _, err := s.OpenBlob(wrongSize); err == nil {
		t.Fatal("size mismatch accepted")
	}
}

func TestPutBlobKeepsExistingFile(t *testing.T) {
	s := newStore(t)
	ref, err := s.PutBlob(strings.NewReader("same bytes"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(s.blobPath(ref))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutBlob(strings.NewReader("same bytes")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(s.blobPath(ref))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("second PutBlob replaced the stored file")
	}
}

func TestGCBlobs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	put := func(content string) proto.BlobRef {
		t.Helper()
		ref, err := s.PutBlob(strings.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	raw, out, orphan, young := put("raw"), put("output"), put("orphan"), put("young")
	appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{
		{Payload: proto.Thinking{Text: "x"}, Raw: &raw},
		{Payload: proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "bash", Summary: "ls", Status: proto.ToolDone, Output: &out}},
	}})
	old := time.Now().Add(-2 * time.Hour)
	for _, ref := range []proto.BlobRef{raw, out, orphan} {
		if err := os.Chtimes(s.blobPath(ref), old, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.GCBlobs(ctx, time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v; want 1", n, err)
	}
	for ref, wantPresent := range map[proto.BlobRef]bool{raw: true, out: true, orphan: false, young: true} {
		_, err := os.Stat(s.blobPath(ref))
		if present := err == nil; present != wantPresent {
			t.Fatalf("blob %s present=%v, want %v", ref.SHA256[:8], present, wantPresent)
		}
	}
}
