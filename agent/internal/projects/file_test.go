package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

type fakeSrc struct {
	id, dir, cwd string
	kept         string // the project directory the vendor keeps the session with, when not cwd
}

func (s fakeSrc) ID() string                          { return s.id }
func (s fakeSrc) Dir() string                         { return s.dir }
func (s fakeSrc) Cwd() string                         { return s.cwd }
func (s fakeSrc) InProject(p string) bool             { return Clean(s.cwd) == p || s.kept != "" && s.kept == p }
func (s fakeSrc) Size(context.Context) (int64, error) { return 0, nil }
func (s fakeSrc) Read(context.Context, int64, int64) ([]byte, error) {
	return nil, nil
}

func TestMissingFileSyncsNothing(t *testing.T) {
	dir := t.TempDir()
	file, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	srcs := []provider.Source{
		fakeSrc{id: "a", cwd: "/Users/me/app"},
		fakeSrc{id: "b", cwd: "/Users/me/other"},
	}
	if got := file.Sync(srcs); len(got) != 0 {
		t.Fatalf("missing file synced %d", len(got))
	}
}

func TestEmptyListSyncsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	srcs := []provider.Source{fakeSrc{id: "a", cwd: "/Users/me/app"}}
	if got := file.Sync(srcs); len(got) != 0 {
		t.Fatalf("empty opt-in synced %d", len(got))
	}
}

func TestRejectsStringProjectEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"projects":["/Users/me/app"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected reject")
	}
}

func TestRejectsMissingPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"projects":[{"imported":["a"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected reject")
	}
}

func TestAddThenImport(t *testing.T) {
	dir := t.TempDir()
	file := &File{}
	if err := file.Add("/Users/me/app"); err != nil {
		t.Fatal(err)
	}
	srcs := []provider.Source{
		fakeSrc{id: "keep", cwd: "/Users/me/app"},
		fakeSrc{id: "skip", cwd: "/Users/me/app"},
		fakeSrc{id: "other", cwd: "/Users/me/other"},
	}
	if got := file.Sync(srcs); len(got) != 0 {
		t.Fatalf("add must not append, got %d", len(got))
	}
	pending := file.Sessions("/Users/me/app", srcs)
	if len(pending) != 2 {
		t.Fatalf("pending %d", len(pending))
	}
	if err := file.Import("/Users/me/app", []string{"keep"}); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(dir); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Sync(srcs)
	if len(got) != 1 || got[0].ID() != "keep" {
		t.Fatalf("imported %+v", got)
	}
	if err := reloaded.ClearImports("/Users/me/app", []string{"keep"}); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Sync(srcs); len(got) != 0 {
		t.Fatalf("after clear imports synced %d", len(got))
	}
	if err := reloaded.RemovePath("/Users/me/app"); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Items) != 0 {
		t.Fatalf("project still listed: %+v", reloaded.Items)
	}
}

func TestInvalidImportBatchDoesNotMutateMarks(t *testing.T) {
	for _, id := range []string{"../escape", " id ", "", "%2f", "中文"} {
		f := &File{Items: []Item{{Path: "/synthetic", Imported: []string{"keep"}}}}
		if err := f.Import("/synthetic", []string{"new", id}); err == nil {
			t.Fatal("invalid import accepted")
		}
		if len(f.Items[0].Imported) != 1 || f.Items[0].Imported[0] != "keep" {
			t.Fatal("import partially mutated marks")
		}
		if err := f.ClearImports("/synthetic", []string{"keep", id}); err == nil {
			t.Fatal("invalid removal accepted")
		}
		if len(f.Items[0].Imported) != 1 {
			t.Fatal("removal mutated marks")
		}
	}
}

func TestLinkedProjectMatchesSessionsInTheRealDirectory(t *testing.T) {
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
	file := &File{Items: []Item{{Path: link, Imported: []string{"a"}}}}
	srcs := []provider.Source{fakeSrc{id: "a", cwd: dir}, fakeSrc{id: "b", cwd: dir}}
	pend := file.Sessions(link, srcs)
	if len(pend) != 2 || pend[0].Cwd != dir || !pend[0].Imported || pend[1].Imported {
		t.Fatalf("sessions = %+v", pend)
	}
	if got := file.Sync(srcs); len(got) != 1 || got[0].ID() != "a" {
		t.Fatalf("sync = %+v", got)
	}
	if err := file.Add(dir); err != nil || len(file.Items) != 1 {
		t.Fatalf("adding the real path watched it twice: %+v, %v", file.Items, err)
	}
	if err := file.Import(dir, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if err := file.RemovePath(dir); err != nil || len(file.Items) != 0 {
		t.Fatalf("remove by the real path: %+v, %v", file.Items, err)
	}
}
