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
