package shim

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	in := Message{Type: MsgRecord, Offset: 42, Record: &Record{Kind: KindStdout, Data: "x"}}
	if err := writeFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Message
	if err := readFrame(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if out.Offset != 42 || out.Record == nil || out.Record.Data != "x" {
		t.Fatalf("round trip %+v", out)
	}
	huge := []byte{0xff, 0xff, 0xff, 0xff}
	if err := readFrame(bytes.NewReader(huge), &out); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized frame: %v", err)
	}
	if err := readFrame(bytes.NewReader([]byte{0, 0, 0, 5, '{'}), &out); err == nil {
		t.Fatal("truncated frame accepted")
	}
}
