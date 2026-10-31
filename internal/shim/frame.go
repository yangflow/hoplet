package shim

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrame bounds one frame's payload.
const MaxFrame = 16 << 20

// ErrFrameTooLarge is returned for frames over MaxFrame.
var ErrFrameTooLarge = errors.New("shim: frame too large")

// frameGrowStart is how much readFrame allocates before the payload has
// proved it is really that long.
const frameGrowStart = 64 << 10

// writeFrame encodes v as one length-prefixed JSON frame.
func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("shim: encode frame: %w", err)
	}
	n := len(b)
	if n > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4, 4+n)
	// n <= MaxFrame, so the mask never changes the value; it states the bound.
	binary.BigEndian.PutUint32(buf, uint32(n)&0xffffffff)
	buf = append(buf, b...)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("shim: write frame: %w", err)
	}
	return nil
}

// readFrame decodes one length-prefixed JSON frame into v. It returns
// io.EOF only when the stream ends cleanly between frames. The payload
// buffer grows as bytes arrive, so a bogus length costs nothing.
func readFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if n > MaxFrame {
		return ErrFrameTooLarge
	}
	b := make([]byte, 0, min(n, frameGrowStart))
	for len(b) < n {
		if len(b) == cap(b) {
			b = append(b, 0)[:len(b)]
		}
		m, err := r.Read(b[len(b):min(cap(b), n)])
		b = b[:len(b)+m]
		if err != nil && len(b) < n {
			return fmt.Errorf("shim: read frame: %w", io.ErrUnexpectedEOF)
		}
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("shim: decode frame: %w", err)
	}
	return nil
}
