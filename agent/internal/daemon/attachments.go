package daemon

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// The limits of prompt images (docs/spec.md#hoplet, ADR 0017); shared/src/runtime.ts holds the same numbers
// for the core and the web UI. The store keeps at most storeMaxFiles images and storeMaxBytes together, each for
// at most storeTTL; the oldest go first.
const (
	maxPromptImages     = 4
	maxPromptImageBytes = 4 << 20
	maxPromptImagesSize = 8 << 20
	storeMaxFiles       = 64
	storeMaxBytes       = 64 << 20
	storeTTL            = time.Hour
)

// extOf maps the image types a prompt may carry to the file extension they are stored under.
var extOf = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// attachmentID is the opaque ID rule (shared/src/ids.ts, OPAQUE_ID_PATTERN): it is a file name here.
var attachmentID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var (
	errInvalidImage      = errors.New("invalid-image")
	errMissingAttachment = errors.New("attachment-missing")
)

// attachments keeps the images uploaded for coming prompts in <hoplet_home>/attachments, one file per image
// named by its ID and type, readable by the agent's user only. An image arrives in chunks and is written to a
// hidden file until its last chunk.
type attachments struct {
	dir     string
	now     func() time.Time
	mu      gosync.Mutex
	pending map[string]*upload // images whose last chunk has not arrived, by ID
}

// upload is an image being received: the chunk expected next and the bytes so far.
type upload struct {
	mediaType string
	next      int64
	size      int
	started   time.Time
}

func newAttachments(dir string) *attachments {
	return &attachments{dir: dir, now: time.Now, pending: map[string]*upload{}}
}

// put takes one chunk of an image (sync.SnapshotChunkBytes at most, in order from 0) and reports whether the
// image is complete: after the last chunk it checks the whole image as the core did (at most 4 MiB, first
// bytes of the declared type) and stores it under id. Any failure drops what was received of the image.
func (a *attachments) put(id, mediaType string, seq int64, last bool, data string) (done bool, err error) {
	ext, ok := extOf[mediaType]
	if !ok || !attachmentID.MatchString(id) {
		return false, errInvalidImage
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tmp := filepath.Join(a.dir, "."+id+ext)
	defer func() {
		if err != nil || done {
			delete(a.pending, id)
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	chunk, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(chunk) > sync.SnapshotChunkBytes {
		return false, errInvalidImage
	}
	up := a.pending[id]
	if seq == 0 {
		if err := os.MkdirAll(a.dir, 0o700); err != nil {
			return false, err
		}
		a.prune()
		up = &upload{mediaType: mediaType, started: a.now()}
		a.pending[id] = up
		if err := os.WriteFile(tmp, nil, 0o600); err != nil {
			return false, err
		}
	}
	if up == nil || up.next != seq || up.mediaType != mediaType {
		return false, errInvalidImage
	}
	up.next++
	up.size += len(chunk)
	if up.size > maxPromptImageBytes {
		return false, errInvalidImage
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, err
	}
	_, werr := f.Write(chunk)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil || !last {
		return false, werr
	}
	head := make([]byte, 512)
	f, err = os.Open(tmp)
	if err != nil {
		return false, err
	}
	n, _ := f.Read(head)
	_ = f.Close()
	if up.size == 0 || http.DetectContentType(head[:n]) != mediaType {
		return false, errInvalidImage
	}
	return true, os.Rename(tmp, filepath.Join(a.dir, id+ext))
}

// take reads the images of a prompt in order and checks their count and total size. The files stay until they
// expire or the store's limits push them out, so a prompt that was refused or whose start failed can be sent
// again with the same IDs.
func (a *attachments) take(ids []string) ([]runtime.Image, error) {
	if len(ids) > maxPromptImages {
		return nil, fmt.Errorf("%w: more than %d images", errInvalidImage, maxPromptImages)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]runtime.Image, 0, len(ids))
	total := 0
	for _, id := range ids {
		path, mediaType := a.find(id)
		if path == "" {
			return nil, errMissingAttachment
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, errMissingAttachment
		}
		total += len(raw)
		if total > maxPromptImagesSize {
			return nil, fmt.Errorf("%w: the images exceed %d bytes", errInvalidImage, maxPromptImagesSize)
		}
		out = append(out, runtime.Image{MediaType: mediaType, Data: raw})
	}
	return out, nil
}

// find returns the file of an unexpired image and its type, or "".
func (a *attachments) find(id string) (string, string) {
	if !attachmentID.MatchString(id) {
		return "", ""
	}
	for mediaType, ext := range extOf {
		path := filepath.Join(a.dir, id+ext)
		if info, err := os.Stat(path); err == nil && a.now().Sub(info.ModTime()) <= storeTTL {
			return path, mediaType
		}
	}
	return "", ""
}

// prune removes expired images, uploads that stalled for longer than the TTL and their leftovers, then the
// oldest images until one more of the largest size fits the limits. The caller holds the lock.
func (a *attachments) prune() {
	for id, up := range a.pending {
		if a.now().Sub(up.started) > storeTTL {
			delete(a.pending, id)
		}
	}
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return
	}
	type stored struct {
		path string
		size int64
		at   time.Time
	}
	var kept []stored
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(a.dir, e.Name())
		if strings.HasPrefix(e.Name(), ".") {
			id := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "."), filepath.Ext(e.Name()))
			if a.pending[id] == nil {
				_ = os.Remove(path)
			}
			continue
		}
		if a.now().Sub(info.ModTime()) > storeTTL {
			_ = os.Remove(path)
			continue
		}
		kept = append(kept, stored{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].at.Before(kept[j].at) })
	for len(kept) > 0 && (len(kept)+1 > storeMaxFiles || total+maxPromptImageBytes > storeMaxBytes) {
		_ = os.Remove(kept[0].path)
		total -= kept[0].size
		kept = kept[1:]
	}
}

// prompt is a runtime.Prompt with the images of ids.
func (a *attachments) prompt(text string, ids []string) (runtime.Prompt, error) {
	images, err := a.take(ids)
	if err != nil {
		return runtime.Prompt{}, err
	}
	return runtime.Prompt{Text: text, Images: images}, nil
}
