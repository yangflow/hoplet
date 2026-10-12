package projects

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

func TestProjectsAddListRemove(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := RunCLI(dir, []string{"add", "/Users/me/app"}, &buf); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := RunCLI(dir, []string{"list"}, &buf); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "/Users/me/app") || strings.Contains(got, "pending") {
		t.Fatalf("projects list should be paths only: %q", got)
	}
	if err := RunCLI(dir, []string{"remove", "/Users/me/app"}, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	file, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Items) != 0 {
		t.Fatalf("expected empty, got %+v", file.Items)
	}
}

func TestCLIBareShowsHelp(t *testing.T) {
	var buf bytes.Buffer
	if err := RunCLI(t.TempDir(), []string{}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Available Commands") || strings.Contains(out, "\n  import") {
		t.Fatalf("projects help should not list import: %q", out)
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	if err := RunCLI(t.TempDir(), []string{"wat"}, bytes.NewBuffer(nil)); err == nil {
		t.Fatal("expected usage error")
	}
}

func TestSessionsDedupesIDs(t *testing.T) {
	file := &File{Items: []Item{{Path: "/Users/me/app"}}}
	srcs := []provider.Source{
		fakeSrc{id: "a", cwd: "/Users/me/app"},
		fakeSrc{id: "a", cwd: "/Users/me/app"},
	}
	got := file.Sessions("/Users/me/app", srcs)
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
}
