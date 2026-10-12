package provider

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CompleteLength is the size of the file up to and including its last line feed: the bytes sync sends. A
// last line without one is still being written. A file whose last 32 MiB hold no line feed is an error.
func CompleteLength(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	const block = 64 << 10
	buf := make([]byte, block)
	for end := size; end > 0 && size-end <= MaxLineBytes; {
		start := max(end-block, 0)
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	if size == 0 {
		return 0, nil
	}
	return 0, fmt.Errorf("no complete line in the last %d bytes of %s", MaxLineBytes, filepath.Base(path))
}
