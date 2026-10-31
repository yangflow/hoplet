package store

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

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
