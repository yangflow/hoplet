package claude

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// sessionFile is one session JSONL under the Claude projects root.
type sessionFile struct {
	SessionID string // file name without .jsonl
	Path      string
	Dir       string // project directory name under projects/ (encoded path)
	Size      int64
	ModTime   time.Time
	info      os.FileInfo // identity (device and inode) for replacement checks
}

// scanFiles enumerates session files under claude_projects. It stats files and reads no session bytes.
// Directory names are encoded project paths ('-' stands for '/'); used to exclude temp dirs.
func scanFiles(root string, excludePrefixes []string) []sessionFile {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []sessionFile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		decoded := strings.ReplaceAll(name, "-", "/")
		if pathExcluded(decoded, excludePrefixes) {
			continue
		}
		files, err := filepath.Glob(filepath.Join(root, name, "*.jsonl"))
		if err != nil {
			continue
		}
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil {
				continue
			}
			base := filepath.Base(f)
			out = append(out, sessionFile{
				SessionID: strings.TrimSuffix(base, ".jsonl"),
				Path:      f,
				Dir:       name,
				Size:      fi.Size(),
				ModTime:   fi.ModTime(),
				info:      fi,
			})
		}
	}
	return out
}

// firstCwd returns the first non-empty cwd field in a Claude session file.
func firstCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(countingReader{f})
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for n := 0; n < 200 && sc.Scan(); n++ {
		var o struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &o) != nil || o.Cwd == "" {
			continue
		}
		return o.Cwd
	}
	return ""
}

func pathExcluded(decodedPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(decodedPath, p) {
			return true
		}
	}
	return false
}

var sessionBytesRead atomic.Int64

// SessionBytesRead is the number of session file bytes this process has read through the providers. It
// instruments the rule that a scan of unchanged sessions reads none.
func SessionBytesRead() int64 { return sessionBytesRead.Load() }

type countingReader struct{ r io.Reader }

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	sessionBytesRead.Add(int64(n))
	return n, err
}
