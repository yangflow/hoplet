package sync

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func TestUploadSnapshotSendsOrderedChunksOnTheBulkLane(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.cs.Store(cs)
	archive := make([]byte, 2*SnapshotChunkBytes+12345)
	rand.New(rand.NewSource(1)).Read(archive)
	up := SnapshotUpload{
		SessionID: "s1", SnapshotID: "snap1", CreatedAt: 42, BaseCommit: strings.Repeat("a", 40),
		OriginURL: "git@example.test:o/r.git", TreeHash: strings.Repeat("b", 40),
		ByteSize: int64(len(archive)), SHA256: strings.Repeat("c", 64),
	}
	if err := c.UploadSnapshot(context.Background(), up, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for seq := 0; seq < 3; seq++ {
		msg := read()
		if msg["type"] != TypeWorkspaceSnapshot || int(msg["seq"].(float64)) != seq {
			t.Fatalf("chunk %d = %#v", seq, msg)
		}
		if msg["last"].(bool) != (seq == 2) {
			t.Fatalf("chunk %d last = %v", seq, msg["last"])
		}
		for key, want := range map[string]any{"sessionId": "s1", "snapshotId": "snap1", "createdAt": float64(42), "originUrl": up.OriginURL, "byteSize": float64(len(archive)), "sha256": up.SHA256} {
			if msg[key] != want {
				t.Fatalf("chunk %d %s = %v", seq, key, msg[key])
			}
		}
		data, err := base64.StdEncoding.DecodeString(msg["data"].(string))
		if err != nil || len(data) > SnapshotChunkBytes {
			t.Fatalf("chunk %d data: %d bytes, %v", seq, len(data), err)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, archive) {
		t.Fatal("the chunks do not add up to the archive")
	}
	if laneOf(WorkspaceSnapshot{}) != LaneBulk {
		t.Fatal("snapshot chunks belong on the bulk lane")
	}
}

func TestUploadSnapshotNeedsAConnectionAndAnExactStream(t *testing.T) {
	c := NewClient(nil, "dev", "test", Hooks{})
	err := c.UploadSnapshot(context.Background(), SnapshotUpload{SessionID: "s1", ByteSize: 3}, strings.NewReader("abc"))
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}

	c, cs, _ := dialTestConn(t)
	c.cs.Store(cs)
	short := c.UploadSnapshot(context.Background(), SnapshotUpload{SessionID: "s1", SnapshotID: "x", ByteSize: 10}, strings.NewReader("abc"))
	if short == nil {
		t.Fatal("a stream shorter than declared was accepted")
	}
	long := c.UploadSnapshot(context.Background(), SnapshotUpload{SessionID: "s1", SnapshotID: "x", ByteSize: 3}, strings.NewReader("abcdef"))
	if long == nil {
		t.Fatal("a stream longer than declared was accepted")
	}
	if err := c.UploadSnapshot(context.Background(), SnapshotUpload{SessionID: "s1", SnapshotID: "x"}, strings.NewReader("")); err == nil {
		t.Fatal("an empty snapshot was accepted")
	}
}

func TestCursorKnowsOnlySyncedSessions(t *testing.T) {
	c := NewClient(nil, "dev", "test", Hooks{})
	c.cursors["a"] = 10
	c.cursors["b"] = 0
	if n, ok := c.Cursor("a"); !ok || n != 10 {
		t.Fatalf("a = %d %v", n, ok)
	}
	for _, id := range []string{"b", "missing"} {
		if _, ok := c.Cursor(id); ok {
			t.Fatalf("%s is not synced", id)
		}
	}
}
