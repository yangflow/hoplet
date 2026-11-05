package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestDiff(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	c := startFake(t, func(f *fakeConn) {
		fr := f.read()
		if fr.Method != proto.MethodSessionsDiff || string(fr.Params) != `{"session":"s1","repo":"api","patch":true}` {
			t.Errorf("request %s %s", fr.Method, fr.Params)
		}
		f.ok(fr.ID, proto.SessionDiff{Session: "s1", BaselineAt: &at, Repos: []proto.RepoDiff{{
			Path: "api", State: proto.RepoOK, Files: []proto.FileDiff{{Path: "a.go", Status: proto.FileStatusAdded, Additions: 3}}, Patch: "diff", SHA: "h",
		}}})
		next := f.read()
		f.fail(next.ID, proto.CodeNotFound)
	})
	d, err := c.Diff(bg(), proto.DiffParams{Session: "s1", Repo: "api", Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"session":"s1","baseline_at":"2026-11-01T09:00:00Z","repos":[{"path":"api","state":"ok","files":[{"path":"a.go","status":"added","additions":3,"deletions":0}],"patch":"diff","sha":"h"}]}`
	if b, _ := json.Marshal(d); string(b) != want {
		t.Fatalf("diff %s", b)
	}
	if _, err := c.Diff(bg(), proto.DiffParams{Session: "nope"}); err == nil {
		t.Fatal("diff of an unknown session succeeded")
	}
}

func TestFiles(t *testing.T) {
	c := startFake(t, func(f *fakeConn) {
		fr := f.read()
		if fr.Method != proto.MethodFilesList || string(fr.Params) != `{"session":"s1","path":"lib","ignored":true}` {
			t.Errorf("request %s %s", fr.Method, fr.Params)
		}
		f.ok(fr.ID, proto.FileList{Session: "s1", Path: "lib", Entries: []proto.FileEntry{{Path: "lib/a.go", Kind: proto.FileKindFile, Size: 3}}})
		fr = f.read()
		if fr.Method != proto.MethodFilesRead || string(fr.Params) != `{"session":"s1","path":"lib/a.go","baseline":true}` {
			t.Errorf("request %s %s", fr.Method, fr.Params)
		}
		f.ok(fr.ID, proto.FileContent{Session: "s1", Path: "lib/a.go", Text: "abc", Size: 3})
	})
	l, err := c.Files(bg(), proto.FilesListParams{Session: "s1", Path: "lib", Ignored: true})
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Path != "lib/a.go" {
		t.Fatalf("list %+v %v", l, err)
	}
	r, err := c.ReadFile(bg(), proto.FilesReadParams{Session: "s1", Path: "lib/a.go", Baseline: true})
	if err != nil || r.Text != "abc" {
		t.Fatalf("read %+v %v", r, err)
	}
}
