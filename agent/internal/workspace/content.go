package workspace

import (
	"bytes"
	"context"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// imageTypes are the media types the viewer shows as an image, by file extension.
var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

// binarySniffBytes is how much of a file git looks at for a NUL byte to call it binary.
const binarySniffBytes = 8000

func imageType(name string) string {
	return imageTypes[strings.ToLower(path.Ext(name))]
}

// readLimit is how many bytes classifyFile needs of a file: an image candidate whole, anything else its text
// window, plus one byte that tells whether there is more.
func readLimit(name string, size int64) int {
	if imageType(name) != "" && size <= maxImageBytes {
		return maxImageBytes + 1
	}
	return maxFileBytes + 1
}

// readPrefix reads at most limit bytes of r.
func readPrefix(r io.Reader, limit int) ([]byte, error) {
	buf := make([]byte, limit)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:n], nil
}

// classifyFile turns the first bytes of a file (as readLimit asked for) into a File. An image is recognized
// by its extension and its content; text is UTF-8 without a NUL byte in its window; anything else is binary.
func classifyFile(name string, size int64, raw []byte) File {
	if media := imageType(name); media != "" && len(raw) <= maxImageBytes && int64(len(raw)) == size && isImage(media, raw) {
		return File{Kind: "image", Size: size, MediaType: media, Data: raw}
	}
	text := raw
	truncated := len(text) > maxFileBytes
	if truncated {
		text = text[:maxFileBytes]
	}
	if bytes.IndexByte(text, 0) >= 0 || !utf8.Valid(trimPartialRune(text, truncated)) {
		return File{Kind: "binary", Size: size}
	}
	return File{Kind: "text", Size: size, Text: string(text), Truncated: truncated}
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of a window that cut the file short.
func trimPartialRune(b []byte, cut bool) []byte {
	if !cut {
		return b
	}
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return b
		}
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

// isImage checks the content of a file whose extension names media.
func isImage(media string, raw []byte) bool {
	switch media {
	case "image/png":
		return bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff})
	case "image/gif":
		return bytes.HasPrefix(raw, []byte("GIF87a")) || bytes.HasPrefix(raw, []byte("GIF89a"))
	case "image/webp":
		return len(raw) >= 12 && bytes.Equal(raw[:4], []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP"))
	case "image/svg+xml":
		head := raw[:min(len(raw), 4096)]
		return utf8.Valid(raw) && bytes.IndexByte(raw, 0) < 0 && bytes.Contains(bytes.ToLower(head), []byte("<svg"))
	}
	return false
}

// blobSize is the size of the object spec names.
func blobSize(ctx context.Context, dir, spec string) (int64, error) {
	out, err := gitLine(ctx, dir, "cat-file", "-s", spec)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(out, 10, 64)
}

// gitPrefix runs a git command and returns at most limit bytes of its output; the command is stopped once
// they are read.
func gitPrefix(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := gitCommand(ctx, dir, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	raw, readErr := readPrefix(out, limit)
	full := len(raw) == limit
	if full || readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil && !full {
		return nil, wrap(subcommand(args), waitErr, nil)
	}
	return raw, nil
}
