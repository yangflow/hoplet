package shim

import (
	"bytes"
	"errors"
	"io"
	"strings"
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

func TestReadFrameLargePayloadAndLyingLength(t *testing.T) {
	var buf bytes.Buffer
	in := Message{Type: MsgRecord, Record: &Record{Kind: KindStdout, Data: strings.Repeat("q", 300<<10)}}
	if err := writeFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Message
	if err := readFrame(&buf, &out); err != nil || len(out.Record.Data) != 300<<10 {
		t.Fatalf("large frame: %v", err)
	}
	lying := append([]byte{0x00, 0xf0, 0x00, 0x00}, "{}"...) // claims 15 MiB, sends 2 bytes
	if err := readFrame(bytes.NewReader(lying), &out); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lying length: %v", err)
	}
}
