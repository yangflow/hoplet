package sessions

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

type fakeSrc struct {
	id, dir, cwd, provider string
}

func (s fakeSrc) ID() string                          { return s.id }
func (s fakeSrc) Dir() string                         { return s.dir }
func (s fakeSrc) Provider() string                    { return s.provider }
func (s fakeSrc) Cwd() string                         { return s.cwd }
func (s fakeSrc) InProject(p string) bool             { return s.cwd == p }
func (s fakeSrc) Size(context.Context) (int64, error) { return 0, nil }
func (s fakeSrc) Read(context.Context, int64, int64) ([]byte, error) {
	return nil, nil
}

func TestImportListRemove(t *testing.T) {
	dir := t.TempDir()
	srcs := []provider.Source{
		fakeSrc{id: "keep", cwd: "/Users/me/app", provider: "claude"},
		fakeSrc{id: "skip", cwd: "/Users/me/app", provider: "claude"},
	}
	if err := projects.RunCLI(dir, []string{"add", "/Users/me/app"}, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RunCLI(dir, []string{"list"}, srcs, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "keep") || !strings.Contains(out, "pending") || !strings.Contains(out, "STATE") {
		t.Fatalf("sessions list: %q", out)
	}
	if err := RunCLI(dir, []string{"import", "keep"}, srcs, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := RunCLI(dir, []string{"list"}, srcs, &buf); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	if !strings.Contains(out, "imported") || !strings.Contains(out, "keep") {
		t.Fatalf("after import: %q", out)
	}
	if err := RunCLI(dir, []string{"remove", "keep"}, srcs, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	file, err := projects.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Items) != 1 || len(file.Items[0].Imported) != 0 {
		t.Fatalf("import cleared, path kept: %+v", file.Items)
	}
}

func TestImportByPath(t *testing.T) {
	dir := t.TempDir()
	srcs := []provider.Source{
		fakeSrc{id: "keep", cwd: "/Users/me/app", provider: "claude"},
	}
	if err := projects.RunCLI(dir, []string{"add", "/Users/me/app"}, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	if err := RunCLI(dir, []string{"import", "/Users/me/app", "keep"}, srcs, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	file, err := projects.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Items[0].Imported) != 1 || file.Items[0].Imported[0] != "keep" {
		t.Fatalf("%+v", file.Items)
	}
}

func TestBareShowsHelp(t *testing.T) {
	var buf bytes.Buffer
	if err := RunCLI(t.TempDir(), []string{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "import") {
		t.Fatalf("sessions help: %q", buf.String())
	}
}
