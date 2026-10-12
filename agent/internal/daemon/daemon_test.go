package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

type fakeSrc struct {
	id, cwd string
}

func (s fakeSrc) ID() string                          { return s.id }
func (s fakeSrc) Dir() string                         { return s.cwd }
func (s fakeSrc) Cwd() string                         { return s.cwd }
func (s fakeSrc) InProject(p string) bool             { return s.cwd == p }
func (s fakeSrc) Size(context.Context) (int64, error) { return 0, nil }
func (s fakeSrc) Read(context.Context, int64, int64) ([]byte, error) {
	return nil, nil
}

func TestPollFailClosedOnBadJSON(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "projects.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newStore(&config.Config{HopletHome: home}, &projects.File{Items: []projects.Item{
		{Path: "/Users/me/app", Imported: []string{"a"}},
	}})
	srcs := []provider.Source{fakeSrc{id: "a", cwd: "/Users/me/app"}}
	if got := st.poll(srcs); got != nil {
		t.Fatalf("bad json must sync nothing, got %d", len(got))
	}
}

func TestPollAllowlist(t *testing.T) {
	home := t.TempDir()
	file := &projects.File{Items: []projects.Item{
		{Path: "/Users/me/app", Imported: []string{"keep"}},
	}}
	if err := file.Save(home); err != nil {
		t.Fatal(err)
	}
	st := newStore(&config.Config{HopletHome: home}, file)
	srcs := []provider.Source{
		fakeSrc{id: "keep", cwd: "/Users/me/app"},
		fakeSrc{id: "skip", cwd: "/Users/me/app"},
		fakeSrc{id: "other", cwd: "/Users/me/other"},
	}
	got := st.poll(srcs)
	if len(got) != 1 || got[0].ID() != "keep" {
		t.Fatalf("got %+v", got)
	}
}

func TestShortIDPrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abc", "abc"},                   // 3 chars - shorter than 8
		{"abcd", "abcd"},                 // 4 chars - shorter than 8
		{"abcdefgh", "abcdefgh"},         // 8 chars - exactly 8
		{"abcdefghi", "abcdefgh"},        // 9 chars - longer than 8
		{"abcdefghijklmnop", "abcdefgh"}, // 16 chars - much longer than 8
		{"", ""},                         // empty string
		{"a", "a"},                       // 1 char
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shortIDPrefix(tt.input)
			if got != tt.expected {
				t.Errorf("shortIDPrefix(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestProjectsAddChecksTheDirectory(t *testing.T) {
	home := t.TempDir()
	st := newStore(&config.Config{HopletHome: home}, &projects.File{})
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "repo")
	link := filepath.Join(base, "link")
	file := filepath.Join(base, "file")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative/dir", filepath.Join(base, "missing"), file} {
		if _, _, err := st.projectsAdd(context.Background(), bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	_, listed, err := st.projectsAdd(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Path != dir {
		t.Fatalf("listed = %+v, want the resolved %s", listed, dir)
	}
}

func TestProjectsListReportsTheRealPath(t *testing.T) {
	home := t.TempDir()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "repo")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := (&projects.File{Items: []projects.Item{{Path: link, Imported: []string{}}}}).Save(home); err != nil {
		t.Fatal(err)
	}
	st := newStore(&config.Config{HopletHome: home}, &projects.File{})
	_, listed, err := st.projectsList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Path != dir {
		t.Fatalf("listed = %+v, want the resolved %s", listed, dir)
	}
	if got, err := st.watched(dir); err != nil || got != dir {
		t.Fatalf("watched(%s) = %q, %v", dir, got, err)
	}
	if _, _, err := st.projectsRemove(context.Background(), dir); err != nil {
		t.Fatalf("remove by the real path: %v", err)
	}
}
