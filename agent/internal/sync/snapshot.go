package sync

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"
)

// SnapshotUpload describes a workspace snapshot archive that is about to be sent.
type SnapshotUpload struct {
	SessionID  string
	SnapshotID string
	CreatedAt  int64
	BaseCommit string
	OriginURL  string
	TreeHash   string
	ByteSize   int64
	SHA256     string
}

// snapshotChunkTimeout is how long one chunk may take to leave: a 1 MiB chunk is about 1.4 MB on the wire.
const snapshotChunkTimeout = 60 * time.Second

// Cursor is the number of session bytes the core holds, as far as this connection knows, and whether the
// core knows the session at all.
func (c *Client) Cursor(sessionID string) (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.cursors[sessionID]
	return n, ok && n > 0
}

// UploadSnapshot sends the archive read from r as `workspace.snapshot` chunks of at most SnapshotChunkBytes
// bytes on the bulk lane, in order. It returns ErrNotConnected without a connection and stops when the
// connection changes or ctx ends. r must yield exactly up.ByteSize bytes.
func (c *Client) UploadSnapshot(ctx context.Context, up SnapshotUpload, r io.Reader) error {
	cs := c.cs.Load()
	if cs == nil {
		return ErrNotConnected
	}
	if up.ByteSize <= 0 {
		return errors.New("empty snapshot")
	}
	buf := make([]byte, SnapshotChunkBytes)
	var sent int64
	for seq := 0; sent < up.ByteSize; seq++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.cs.Load() != cs {
			return errConnChanged
		}
		want := min(int64(len(buf)), up.ByteSize-sent)
		if _, err := io.ReadFull(r, buf[:want]); err != nil {
			return fmt.Errorf("read snapshot: %w", err)
		}
		sent += want
		msg := WorkspaceSnapshot{
			V:          ProtocolVersion,
			Type:       TypeWorkspaceSnapshot,
			SessionID:  up.SessionID,
			SnapshotID: up.SnapshotID,
			CreatedAt:  up.CreatedAt,
			BaseCommit: up.BaseCommit,
			OriginURL:  up.OriginURL,
			TreeHash:   up.TreeHash,
			ByteSize:   up.ByteSize,
			SHA256:     up.SHA256,
			Seq:        seq,
			Last:       sent == up.ByteSize,
			Data:       base64.StdEncoding.EncodeToString(buf[:want]),
		}
		if err := c.writeMsgTimeout(cs, msg, snapshotChunkTimeout); err != nil {
			return fmt.Errorf("send snapshot: %w", err)
		}
	}
	var extra [1]byte
	if n, _ := r.Read(extra[:]); n > 0 {
		return errors.New("snapshot is longer than declared")
	}
	return nil
}
