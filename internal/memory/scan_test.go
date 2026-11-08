package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/store"
)

func (f *fixture) edit(from, to string) {
	f.t.Helper()
	data, err := os.ReadFile(f.file())
	if err != nil {
		f.t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		f.t.Fatalf("%q not in file", from)
	}
	if err := os.WriteFile(f.file(), []byte(strings.Replace(string(data), from, to, 1)), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan() {
	f.t.Helper()
	if err := f.svc.Scan(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) history() []Version {
	f.t.Helper()
	h, err := f.svc.History(context.Background(), testID)
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) problems() []store.MemoryProblem {
	f.t.Helper()
	p, err := f.svc.Problems(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) profile() string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, profileFile))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func TestScanRecordsHandEdit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit("o1", "原文", 0)
	f.edit("原文", "手改")
	f.scan()
	h := f.history()
	if len(h) != 2 || h[0].Version != 2 || h[0].Actor != ActorFile {
		t.Fatalf("%+v", h)
	}
	e, err := f.svc.Get(ctx, testID)
	if err != nil || e.Version != 2 || e.Body != "手改" {
		t.Fatalf("%+v, %v", e, err)
	}
	f.scan()
	if got := f.history(); len(got) != 2 {
		t.Fatalf("rescan added versions: %+v", got)
	}
}

func TestScanInvalidFileThenFixed(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit("o1", "原文", 0)
	f.edit("status: active", "status: gone")
	broken, err := os.ReadFile(f.file())
	if err != nil {
		t.Fatal(err)
	}
	f.scan()
	if p := f.problems(); len(p) != 1 || p[0].Path != "preference/"+testID+".md" {
		t.Fatalf("%+v", p)
	}
	if got, _ := os.ReadFile(f.file()); string(got) != string(broken) {
		t.Fatal("invalid file was changed")
	}
	if got, err := f.svc.Search(ctx, store.MemoryQuery{}); err != nil || len(got) != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
	f.edit("status: gone", "status: stale")
	f.scan()
	if p := f.problems(); len(p) != 0 {
		t.Fatalf("%+v", p)
	}
	e, err := f.svc.Get(ctx, testID)
	if err != nil || e.Status != StatusStale || e.Version != 2 {
		t.Fatalf("%+v, %v", e, err)
	}
}

func TestScanDeletedFile(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit("o1", "原文", 0)
	if err := os.Remove(f.file()); err != nil {
		t.Fatal(err)
	}
	f.scan()
	if _, err := f.svc.Get(ctx, testID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if p := f.problems(); len(p) != 1 || !strings.Contains(p[0].Err, "missing") {
		t.Fatalf("%+v", p)
	}
	if h := f.history(); len(h) != 1 {
		t.Fatalf("audit lost: %+v", h)
	}
}

func TestScanBadPaths(t *testing.T) {
	f := newFixture(t)
	valid, err := Encode(testEntry())
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"fact/" + testID + ".md", "preference/" + otherID + ".md", testID + ".md", "preference/sub/" + testID + ".md"} {
		p := filepath.Join(f.root, entriesDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, valid, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.scan()
	if p := f.problems(); len(p) != 4 {
		t.Fatalf("%+v", p)
	}
	if got, err := f.svc.Search(context.Background(), store.MemoryQuery{}); err != nil || len(got) != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestScanIgnoresOtherFiles(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, entriesDir, ".DS_Store"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.scan()
	if p := f.problems(); len(p) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestScanRebuildsLostIndex(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit("o1", "第一版", 0)
	f.commit("o2", "第二版", 1)
	if err := f.st.ReplaceMemoryIndex(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	f.scan()
	e, err := f.svc.Get(ctx, testID)
	if err != nil || e.Version != 2 {
		t.Fatalf("%+v, %v", e, err)
	}
	if h := f.history(); len(h) != 2 {
		t.Fatalf("rebuild recorded an edit: %+v", h)
	}
}

func TestScanAdoptsNewFile(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	data, err := Encode(testEntry())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.file()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.file(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.scan()
	e, err := f.svc.Get(ctx, testID)
	if err != nil || e.Version != 1 {
		t.Fatalf("%+v, %v", e, err)
	}
	if h := f.history(); len(h) != 1 || h[0].Actor != ActorFile {
		t.Fatalf("%+v", h)
	}
}

func TestOpenAfterScanCrashSettles(t *testing.T) {
	f := newFixture(t)
	f.commit("o1", "原文", 0)
	f.edit("原文", "手改")
	f.svc.hook = func(p string) error {
		if p == "after-begin" {
			return errCrash
		}
		return nil
	}
	if err := f.svc.Scan(context.Background()); !errors.Is(err, errCrash) {
		t.Fatalf("got %v", err)
	}
	f.reopen() // the failed attempt must not block the edit being recorded
	if h := f.history(); len(h) != 2 || h[0].Actor != ActorFile {
		t.Fatalf("%+v", h)
	}
	if f.body() != "手改" {
		t.Fatal(f.body())
	}
}

func TestProfileListsActiveOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit("o1", "喜欢直达\n细节", 0)
	if got := f.profile(); !strings.Contains(got, testID+" 喜欢直达") || strings.Contains(got, "细节") || !strings.Contains(got, "Generated") {
		t.Fatal(got)
	}
	stale := newEntry("喜欢直达")
	stale.Status = StatusStale
	if _, err := f.svc.Commit(ctx, Op{ID: "o2", Entry: stale, ExpectedVersion: 1, Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	if got := f.profile(); strings.Contains(got, testID) {
		t.Fatal(got)
	}
}

func TestProfileFailureDoesNotFailCommit(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.root, profileFile)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil { // a directory blocks the rename
		t.Fatal(err)
	}
	f.commit("o1", "原文", 0)
	if f.body() != "原文" {
		t.Fatal(f.body())
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	f.commit("o2", "第二版", 1)
	if got := f.profile(); !strings.Contains(got, "第二版") {
		t.Fatal(got)
	}
}
