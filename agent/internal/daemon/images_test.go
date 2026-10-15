package daemon

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/sync"
)

// The first bytes of each image type a prompt may carry.
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00")
	webpBytes = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
)

func image(mediaType string, data []byte) sync.PromptImage {
	return sync.PromptImage{MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data)}
}

func TestPromptOfDecodesTheFourTypes(t *testing.T) {
	p, err := promptOf("look", []sync.PromptImage{
		image("image/png", pngBytes), image("image/jpeg", jpegBytes),
		image("image/gif", gifBytes), image("image/webp", webpBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "look" || len(p.Images) != 4 || !bytes.Equal(p.Images[0].Data, pngBytes) || p.Images[3].MediaType != "image/webp" {
		t.Fatalf("prompt = %+v", p)
	}
	if p, err := promptOf("plain", nil); err != nil || len(p.Images) != 0 {
		t.Fatalf("no images: %+v %v", p, err)
	}
}

func TestPromptOfRefusesWhatTheLimitsExclude(t *testing.T) {
	big := append(append([]byte(nil), pngBytes...), make([]byte, maxPromptImageBytes)...)
	half := append(append([]byte(nil), pngBytes...), make([]byte, 3<<20)...)
	cases := map[string][]sync.PromptImage{
		"five images":      {image("image/png", pngBytes), image("image/png", pngBytes), image("image/png", pngBytes), image("image/png", pngBytes), image("image/png", pngBytes)},
		"a type not taken": {image("image/svg+xml", []byte("<svg/>"))},
		"bytes of another": {image("image/png", jpegBytes)},
		"not base64":       {{MediaType: "image/png", Data: "%%%"}},
		"empty":            {image("image/png", nil)},
		"over 4 MiB":       {image("image/png", big)},
		"over 8 MiB":       {image("image/png", half), image("image/png", half), image("image/png", half)},
	}
	for name, images := range cases {
		if _, err := promptOf("x", images); !errors.Is(err, errInvalidImage) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
