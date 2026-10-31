package harness

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExecStdio(t *testing.T) {
	ctx := context.Background()
	e, err := StartExec(ctx, []string{"/bin/sh", "-c", "echo a; echo b >&2; read x; echo got:$x; exit 4"}, os.Environ(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.WriteLine([]byte("z")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []string
	var exit *Line
	timeout := time.After(5 * time.Second)
	for exit == nil {
		select {
		case l, ok := <-e.Lines():
			if !ok {
				t.Fatal("lines closed before exit")
			}
			switch l.Kind {
			case Stdout:
				stdout = append(stdout, string(l.Data))
			case Stderr:
				stderr = append(stderr, string(l.Data))
			case Exit:
				exit = &l
			}
		case <-timeout:
			t.Fatal("no exit")
		}
	}
	if len(stdout) != 2 || stdout[0] != "a" || stdout[1] != "got:z" {
		t.Fatalf("stdout %q", stdout)
	}
	if len(stderr) != 1 || stderr[0] != "b" {
		t.Fatalf("stderr %q", stderr)
	}
	if exit.Code != 4 {
		t.Fatalf("exit code %d", exit.Code)
	}
	if _, ok := <-e.Lines(); ok {
		t.Fatal("lines not closed after exit")
	}
	if _, err := StartExec(ctx, nil, nil, ""); err == nil {
		t.Fatal("empty command accepted")
	}
}
