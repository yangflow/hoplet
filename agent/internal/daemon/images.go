package daemon

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// The limits of images that go with a prompt (docs/spec.md#hoplet); shared/src/rest/sessions.ts holds the
// same numbers for the core and the web UI.
const (
	maxPromptImages     = 4
	maxPromptImageBytes = 4 << 20
	maxPromptImagesSize = 8 << 20
)

// promptMediaTypes are the image types a prompt may carry; each is recognized by its first bytes.
var promptMediaTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// errInvalidImage is the reason sent back for an image the agent will not pass on.
var errInvalidImage = errors.New("invalid-image")

// promptOf checks a prompt's images again, as the core did, and decodes them: at most 4, each at most 4 MiB
// and together at most 8 MiB, each of a declared type whose bytes are that type.
func promptOf(text string, images []sync.PromptImage) (runtime.Prompt, error) {
	p := runtime.Prompt{Text: text}
	if len(images) > maxPromptImages {
		return p, fmt.Errorf("%w: more than %d images", errInvalidImage, maxPromptImages)
	}
	total := 0
	for i, img := range images {
		if !promptMediaTypes[img.MediaType] {
			return p, fmt.Errorf("%w: image %d has type %q", errInvalidImage, i+1, img.MediaType)
		}
		data, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return p, fmt.Errorf("%w: image %d is not base64", errInvalidImage, i+1)
		}
		if len(data) == 0 || len(data) > maxPromptImageBytes {
			return p, fmt.Errorf("%w: image %d is %d bytes", errInvalidImage, i+1, len(data))
		}
		if http.DetectContentType(data) != img.MediaType {
			return p, fmt.Errorf("%w: image %d is not %s", errInvalidImage, i+1, img.MediaType)
		}
		total += len(data)
		if total > maxPromptImagesSize {
			return p, fmt.Errorf("%w: the images exceed %d bytes", errInvalidImage, maxPromptImagesSize)
		}
		p.Images = append(p.Images, runtime.Image{MediaType: img.MediaType, Data: data})
	}
	return p, nil
}
