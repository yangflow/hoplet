package daemon

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/sync"
)

// The first bytes of each image type a prompt may carry.
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00")
	webpBytes = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// pngOf is a PNG signature followed by zeros, size bytes in all.
func pngOf(size int) []byte {
	out := make([]byte, size)
	copy(out, pngBytes)
	return out
}

// putAll sends an image in chunks of at most sync.SnapshotChunkBytes, as the core does.
func putAll(a *attachments, id, mediaType string, raw []byte) error {
	n := (len(raw) + sync.SnapshotChunkBytes - 1) / sync.SnapshotChunkBytes
	if n == 0 {
		n = 1
	}
	for seq := 0; seq < n; seq++ {
		end := min((seq+1)*sync.SnapshotChunkBytes, len(raw))
		done, err := a.put(id, mediaType, int64(seq), seq == n-1, b64(raw[seq*sync.SnapshotChunkBytes:end]))
		if err != nil {
			return err
		}
		if done != (seq == n-1) {
			return fmt.Errorf("chunk %d of %d: done = %v", seq, n, done)
		}
	}
	return nil
}

func newTestAttachments(t *testing.T) (*attachments, *time.Time) {
	t.Helper()
	now := time.Now()
	a := newAttachments(filepath.Join(t.TempDir(), "attachments"))
	a.now = func() time.Time { return now }
	return a, &now
}

func TestAttachmentsStoreTheFourTypesAndBuildAPrompt(t *testing.T) {
	a, _ := newTestAttachments(t)
	for id, img := range map[string]struct {
		mt   string
		data []byte
	}{"p": {"image/png", pngBytes}, "j": {"image/jpeg", jpegBytes}, "g": {"image/gif", gifBytes}, "w": {"image/webp", webpBytes}} {
		if err := putAll(a, id, img.mt, img.data); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	info, err := os.Stat(filepath.Join(a.dir, "p.png"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stored file: %v %v", info, err)
	}
	p, err := a.prompt("look", []string{"w", "p"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "look" || len(p.Images) != 2 || p.Images[0].MediaType != "image/webp" || !bytes.Equal(p.Images[1].Data, pngBytes) {
		t.Fatalf("prompt = %+v", p)
	}
	// A prompt can be sent again with the same images, until they expire.
	if _, err := a.prompt("again", []string{"p"}); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentsRefuseWhatTheLimitsExclude(t *testing.T) {
	a, _ := newTestAttachments(t)
	refused := map[string]struct {
		id, mediaType string
		raw           []byte
	}{
		"a type not taken":     {"x", "image/svg+xml", []byte("<svg/>")},
		"bytes of another":     {"x", "image/png", jpegBytes},
		"empty":                {"x", "image/png", nil},
		"over 4 MiB":           {"x", "image/png", pngOf(maxPromptImageBytes + 1)},
		"an ID that is a path": {"../x", "image/png", pngBytes},
	}
	for name, c := range refused {
		if err := putAll(a, c.id, c.mediaType, c.raw); !errors.Is(err, errInvalidImage) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := a.put("x", "image/png", 0, true, "%%%"); !errors.Is(err, errInvalidImage) {
		t.Errorf("not base64: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := putAll(a, id, "image/png", pngOf(3<<20)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.prompt("x", []string{"a", "b", "c", "d", "e"}); !errors.Is(err, errInvalidImage) {
		t.Errorf("five images: %v", err)
	}
	if _, err := a.prompt("x", []string{"a", "b", "c"}); !errors.Is(err, errInvalidImage) {
		t.Errorf("over 8 MiB together: %v", err)
	}
	if _, err := a.prompt("x", []string{"a", "b"}); err != nil {
		t.Errorf("6 MiB together: %v", err)
	}
	if _, err := a.prompt("x", []string{"nope"}); !errors.Is(err, errMissingAttachment) {
		t.Errorf("unknown: %v", err)
	}
}

func TestAttachmentsExpireAndKeepWithinTheStoreLimits(t *testing.T) {
	a, now := newTestAttachments(t)
	if err := putAll(a, "old", "image/png", pngBytes); err != nil {
		t.Fatal(err)
	}
	// An image older than the TTL is gone, for a prompt and from the disk at the next upload.
	*now = now.Add(storeTTL + time.Minute)
	if _, err := a.prompt("x", []string{"old"}); !errors.Is(err, errMissingAttachment) {
		t.Fatalf("expired: %v", err)
	}
	if err := putAll(a, "new", "image/png", pngBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "old.png")); !os.IsNotExist(err) {
		t.Fatalf("the expired file is still there: %v", err)
	}
	if err := os.Chtimes(filepath.Join(a.dir, "new.png"), *now, *now); err != nil {
		t.Fatal(err)
	}

	// Beyond the file count, the oldest go first: "new" is older than every image put after it.
	for i := 0; i < storeMaxFiles+3; i++ {
		*now = now.Add(time.Second)
		id := fmt.Sprintf("img%d", i)
		if err := putAll(a, id, "image/png", pngBytes); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(a.dir, id+".png"), *now, *now); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(a.dir)
	if len(entries) != storeMaxFiles {
		t.Fatalf("%d files, want %d", len(entries), storeMaxFiles)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "new.png")); !os.IsNotExist(err) {
		t.Fatal("the oldest image was kept")
	}
}

func TestAttachmentsArriveInChunksInOrder(t *testing.T) {
	a, _ := newTestAttachments(t)
	big := pngOf(3*sync.SnapshotChunkBytes + 5)
	// Another image uploaded meanwhile leaves this one's chunks alone.
	if _, err := a.put("big", "image/png", 0, false, b64(big[:sync.SnapshotChunkBytes])); err != nil {
		t.Fatal(err)
	}
	if err := putAll(a, "small", "image/png", pngBytes); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 3; seq++ {
		end := min((seq+1)*sync.SnapshotChunkBytes, len(big))
		done, err := a.put("big", "image/png", int64(seq), seq == 3, b64(big[seq*sync.SnapshotChunkBytes:end]))
		if err != nil || done != (seq == 3) {
			t.Fatalf("chunk %d: done %v err %v", seq, done, err)
		}
	}
	p, err := a.prompt("x", []string{"big", "small"})
	if err != nil || !bytes.Equal(p.Images[0].Data, big) {
		t.Fatalf("assembled image: %v", err)
	}

	// A chunk out of order, a chunk larger than a snapshot chunk, or a chunk without a start drops the image.
	if _, err := a.put("o", "image/png", 0, false, b64(pngBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.put("o", "image/png", 2, true, b64(pngBytes)); !errors.Is(err, errInvalidImage) {
		t.Fatalf("out of order: %v", err)
	}
	if _, err := a.put("o", "image/png", 1, true, b64(pngBytes)); !errors.Is(err, errInvalidImage) {
		t.Fatalf("after a refusal the image is gone: %v", err)
	}
	if _, err := a.put("l", "image/png", 0, true, b64(pngOf(sync.SnapshotChunkBytes+1))); !errors.Is(err, errInvalidImage) {
		t.Fatalf("oversized chunk: %v", err)
	}
	entries, _ := os.ReadDir(a.dir)
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Fatalf("leftover %s", e.Name())
		}
	}
}
