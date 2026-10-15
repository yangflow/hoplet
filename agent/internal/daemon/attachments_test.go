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
		if err := a.put(id, img.mt, b64(img.data)); err != nil {
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
	refused := map[string][3]string{
		"a type not taken":     {"x", "image/svg+xml", b64([]byte("<svg/>"))},
		"bytes of another":     {"x", "image/png", b64(jpegBytes)},
		"not base64":           {"x", "image/png", "%%%"},
		"empty":                {"x", "image/png", ""},
		"over 4 MiB":           {"x", "image/png", b64(pngOf(maxPromptImageBytes + 1))},
		"an ID that is a path": {"../x", "image/png", b64(pngBytes)},
	}
	for name, c := range refused {
		if err := a.put(c[0], c[1], c[2]); !errors.Is(err, errInvalidImage) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := a.put(id, "image/png", b64(pngOf(3<<20))); err != nil {
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
	if err := a.put("old", "image/png", b64(pngBytes)); err != nil {
		t.Fatal(err)
	}
	// An image older than the TTL is gone, for a prompt and from the disk at the next upload.
	*now = now.Add(storeTTL + time.Minute)
	if _, err := a.prompt("x", []string{"old"}); !errors.Is(err, errMissingAttachment) {
		t.Fatalf("expired: %v", err)
	}
	if err := a.put("new", "image/png", b64(pngBytes)); err != nil {
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
		if err := a.put(id, "image/png", b64(pngBytes)); err != nil {
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
