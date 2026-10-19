package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// rolloutName matches a rollout file name without its extension; the capture is the thread ID.
var rolloutName = regexp.MustCompile(`^rollout-.+-([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})$`)

const (
	plainExt = ".jsonl"
	zstExt   = ".jsonl.zst"
)

// rolloutFile is one rollout as the latest scan saw it. Codex keeps exactly one of the two encodings, but a
// rename in progress can leave both.
type rolloutFile struct {
	key   string // thread ID from the file name
	base  string // path with the .jsonl extension
	dir   string // YYYY/MM/DD under the sessions directory
	plain os.FileInfo
	zst   os.FileInfo
}

func (f rolloutFile) current() string {
	if f.plain != nil {
		return f.base
	}
	return f.base + ".zst"
}

// scanFiles enumerates rollouts under root (sessions/YYYY/MM/DD). It stats files and reads no session bytes.
func scanFiles(root string) []rolloutFile {
	byBase := map[string]*rolloutFile{}
	var order []string
	for _, ext := range []string{plainExt, zstExt} {
		matches, err := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*"+ext))
		if err != nil {
			continue
		}
		for _, path := range matches {
			name := strings.TrimSuffix(filepath.Base(path), ext)
			m := rolloutName.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			fi, err := os.Stat(path)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			base := strings.TrimSuffix(path, ".zst")
			f := byBase[base]
			if f == nil {
				rel, _ := filepath.Rel(root, filepath.Dir(path))
				f = &rolloutFile{key: m[1], base: base, dir: filepath.ToSlash(rel)}
				byBase[base] = f
				order = append(order, base)
			}
			if ext == plainExt {
				f.plain = fi
			} else {
				f.zst = fi
			}
		}
	}
	out := make([]rolloutFile, 0, len(order))
	for _, b := range order {
		out = append(out, *byBase[b])
	}
	return out
}

// firstLine is the part of a rollout's first line (`session_meta`) the provider uses.
type firstLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"payload"`
}

// readMeta reads the thread ID and cwd from the first line of the rollout at path, decompressing it when it is
// a .zst. The line is about 70 KB, so it is read to its LF rather than through a small line buffer. ok is
// false while the line is incomplete or is not a session_meta with a usable ID.
func readMeta(path string) (id, cwd string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return "", "", false
		}
		defer dec.Close()
		r = dec
	}
	br := bufio.NewReaderSize(io.LimitReader(countingReader{r}, provider.MaxLineBytes), 128<<10)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return "", "", false
	}
	var fl firstLine
	if json.Unmarshal(bytes.TrimSpace(line), &fl) != nil || fl.Type != "session_meta" {
		return "", "", false
	}
	if !provider.ValidSessionID(fl.Payload.ID) {
		return "", "", false
	}
	return fl.Payload.ID, fl.Payload.Cwd, true
}

var sessionBytesRead atomic.Int64

// SessionBytesRead is the number of bytes this process has read from rollout files, decompressed bytes
// included. It instruments the rule that a scan of unchanged sessions reads none.
func SessionBytesRead() int64 { return sessionBytesRead.Load() }

type countingReader struct{ r io.Reader }

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	sessionBytesRead.Add(int64(n))
	return n, err
}
