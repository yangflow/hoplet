package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// DirName is the name of the directory under Claude Code's projects store that holds the sessions
// run in the working directory path: the real path with every character other than an ASCII letter or digit
// replaced by `-` (one `-` per UTF-16 code unit, as Claude Code counts them). If the sanitized name is
// longer than 200 characters, it is truncated to 200 characters followed by `-` and the base-36 absolute
// value of a 32-bit hash of the realpath computed over its UTF-16 representation.
func DirName(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = filepath.Clean(path)
	}
	var b strings.Builder
	for _, r := range real {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) <= 200 {
		return s
	}
	// Truncate to 200 chars and append hash of the realpath
	h := hashInt32UTF16(real)
	ah := int64(h)
	if ah < 0 {
		ah = -ah
	}
	return s[:200] + "-" + strconv.FormatInt(ah, 36)
}

// hashInt32UTF16 computes a Java-style int32 hash over the UTF-16 representation of s.
// h = h*31 + unit, with 32-bit wraparound.
func hashInt32UTF16(s string) int32 {
	var h int32
	for _, u := range utf16.Encode([]rune(s)) {
		h = h*31 + int32(u)
	}
	return h
}

// installSession writes the staged session file into the store root as the session sessionID of the
// working directory cwd, byte for byte, and returns the path. It never replaces a file that is already
// there. Everything is created through a rooted handle on the store, so a name cannot leave it.
func installSession(root, cwd, sessionID, staged string) (string, error) {
	if !provider.ValidSessionID(sessionID) {
		return "", errors.New("invalid-session-id")
	}
	rel := filepath.Join(DirName(cwd), sessionID+".jsonl")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	store, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer store.Close()
	if err := store.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return "", err
	}
	src, err := os.Open(staged)
	if err != nil {
		return "", err
	}
	defer src.Close()
	out, err := store.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, src)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = store.Remove(rel)
		return "", errors.Join(copyErr, closeErr)
	}
	return filepath.Join(root, rel), nil
}

// removeSession removes a session file installSession wrote, and its directory when that is
// empty. A path outside the store is left alone.
func removeSession(root, path string) {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || filepath.Dir(rel) == "." {
		return
	}
	store, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer store.Close()
	_ = store.Remove(rel)
	_ = store.Remove(filepath.Dir(rel)) // fails while other sessions are in it
}

// lastCwd returns the last non-empty cwd recorded in the final 256 KiB of a session file: the
// directory the latest turn ran in.
func lastCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	const window = 256 << 10
	start := max(info.Size()-window, 0)
	buf := make([]byte, info.Size()-start)
	n, _ := f.ReadAt(buf, start)
	sessionBytesRead.Add(int64(n))
	sc := bufio.NewScanner(bytes.NewReader(buf[:n]))
	sc.Buffer(make([]byte, 0, 64<<10), window)
	last := ""
	for sc.Scan() {
		var o struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &o) == nil && o.Cwd != "" {
			last = o.Cwd
		}
	}
	return last
}
