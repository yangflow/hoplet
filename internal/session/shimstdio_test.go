package session

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
)

// TestShimStdioLines runs a real shim in-process and checks that chunked
// records come back as whole lines with increasing offsets, non-UTF-8 bytes
// survive, stdin is echoed as a Stdin line, and the exit carries its code.
func TestShimStdioLines(t *testing.T) {
	dir, err := os.MkdirTemp("", "ss")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := `read x; head -c 3145728 /dev/zero | tr '\0' a; echo; printf '\377\376\n'; echo oops >&2; echo "got:$x"; exit 7`
	done := make(chan error, 1)
	go func() {
		done <- shim.Run(ctx, shim.Config{Dir: dir, WorkDir: dir, Command: []string{"/bin/sh", "-c", script}, Env: []string{}})
	}()
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	c, err := dialWhenReady(dctx, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewShimStdio(c)
	defer s.Close()
	if err := s.WriteLine(ctx, []byte("z")); err != nil {
		t.Fatal(err)
	}
	var got []harness.Line
	timeout := time.After(15 * time.Second)
	for {
		select {
		case l, ok := <-s.Lines():
			if !ok {
				goto checked
			}
			got = append(got, l)
		case <-timeout:
			t.Fatal("timed out")
		}
	}
checked:
	var last int64
	var kinds []harness.LineKind
	var stdout [][]byte
	for _, l := range got {
		if l.Offset <= last {
			t.Fatalf("offset %d not after %d", l.Offset, last)
		}
		last = l.Offset
		kinds = append(kinds, l.Kind)
		if l.Kind == harness.Stdout {
			stdout = append(stdout, l.Data)
		}
	}
	if len(stdout) != 3 || len(stdout[0]) != 3<<20 || bytes.Count(stdout[0], []byte("a")) != 3<<20 {
		t.Fatalf("stdout lines %d, first %d bytes", len(stdout), len(stdout[0]))
	}
	if !bytes.Equal(stdout[1], []byte{0xff, 0xfe}) || string(stdout[2]) != "got:z" {
		t.Fatalf("stdout tail %q %q", stdout[1], stdout[2])
	}
	exit := got[len(got)-1]
	if exit.Kind != harness.Exit || exit.Code != 7 {
		t.Fatalf("exit %+v", exit)
	}
	if !strings.Contains(kindString(kinds), "stdin") || !strings.Contains(kindString(kinds), "stderr") {
		t.Fatalf("kinds %v", kinds)
	}
	if err := s.Ack(exit.Offset); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shim did not exit after the ack")
	}
}

func kindString(ks []harness.LineKind) string {
	names := map[harness.LineKind]string{harness.Stdout: "stdout", harness.Stderr: "stderr", harness.Stdin: "stdin", harness.StdinError: "stdin_error", harness.Exit: "exit"}
	var b strings.Builder
	for _, k := range ks {
		b.WriteString(names[k] + " ")
	}
	return b.String()
}

func TestAssemblerSplitsAtMaxLine(t *testing.T) {
	var a assembler
	chunk := bytes.Repeat([]byte("x"), shim.ChunkSize)
	var lines []harness.Line
	// Chunks that fill MaxLine exactly while the line continues flush at once.
	n := harness.MaxLine / shim.ChunkSize
	for i := range n {
		lines = append(lines, a.add(shim.Record{Kind: shim.KindStdout, B: chunk, More: true}, int64(i+1))...)
	}
	if len(lines) != 1 || len(lines[0].Data) != harness.MaxLine || lines[0].Offset != 0 {
		t.Fatalf("a full buffer must flush at once, without an offset: %d lines", len(lines))
	}
	lines = a.add(shim.Record{Kind: shim.KindStdout, Data: "tail"}, int64(n+1))
	if len(lines) != 1 || string(lines[0].Data) != "tail" || lines[0].Offset != int64(n+1) {
		t.Fatalf("continuation line %+v", lines)
	}
	// A line of exactly MaxLine ends in one Line, with no empty trailer.
	a = assembler{}
	for i := range n - 1 {
		if got := a.add(shim.Record{Kind: shim.KindStdout, B: chunk, More: true}, int64(i+1)); len(got) != 0 {
			t.Fatalf("early flush at chunk %d", i)
		}
	}
	lines = a.add(shim.Record{Kind: shim.KindStdout, B: chunk}, 99)
	if len(lines) != 1 || len(lines[0].Data) != harness.MaxLine {
		t.Fatalf("exact fit: %d lines", len(lines))
	}
}

func TestAssemblerKeepsKindsApartAndMapsExit(t *testing.T) {
	var a assembler
	if got := a.add(shim.Record{Kind: shim.KindStdout, Data: "a", More: true}, 1); len(got) != 0 {
		t.Fatal("partial emitted")
	}
	got := a.add(shim.Record{Kind: shim.KindStderr, Data: "e"}, 2)
	if len(got) != 1 || string(got[0].Data) != "e" {
		t.Fatalf("stderr %+v", got)
	}
	got = a.add(shim.Record{Kind: shim.KindStdout, Data: "b"}, 3)
	if len(got) != 1 || string(got[0].Data) != "ab" {
		t.Fatalf("stdout %+v", got)
	}
	got = a.add(shim.Record{Kind: shim.KindExit, Signal: "TERM"}, 4)
	if len(got) != 1 || got[0].Code != -1 || got[0].Signal != "TERM" {
		t.Fatalf("exit %+v", got)
	}
}

func TestStdinCode(t *testing.T) {
	for _, c := range []string{shim.CodeStdinBusy, shim.CodeTooLarge, shim.CodeBadRequest, shim.CodeExited} {
		if !stdinCode(c) {
			t.Errorf("%s should be a stdin error", c)
		}
	}
	for _, c := range []string{shim.CodeCorrupt, shim.CodeRecordTooLarge, shim.CodeVersion, shim.CodeBadOffset, "weird"} {
		if stdinCode(c) {
			t.Errorf("%s must end the connection", c)
		}
	}
}
