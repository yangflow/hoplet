package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestArtifactPublish(t *testing.T) {
	work := t.TempDir()
	outside := t.TempDir()
	for _, p := range []string{filepath.Join(work, "site", "index.html"), filepath.Join(work, "site", "app.js"), filepath.Join(outside, "x.html")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "x.html"), filepath.Join(work, "link.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "out")); err != nil {
		t.Fatal(err)
	}
	src := fakeSource{workDir: work, got: &changes{}}
	set := New(src)
	caller := proto.Session{ID: "code0001", Mode: proto.ModeCode}

	r, err := set.Call(context.Background(), caller, "artifact_publish",
		json.RawMessage(`{"name":"site","entry":"site/index.html","files":["site/app.js"]}`))
	if err != nil || r.IsError || r.Text != `{"id":"art1","name":"site","kind":"html","version":2}` {
		t.Fatalf("publish %+v %v", r, err)
	}
	// An absolute path inside the working directory is accepted too.
	abs := filepath.Join(work, "site", "index.html")
	if r, err := set.Call(context.Background(), caller, "artifact_publish", json.RawMessage(`{"name":"a","entry":"`+abs+`"}`)); err != nil || r.IsError {
		t.Fatalf("absolute %+v %v", r, err)
	}
	want := []string{abs, filepath.Join(work, "site", "app.js")}
	got := src.got.publishes
	if len(got) != 2 || got[0].Session != "code0001" || got[0].By != proto.ArtifactByHarness || got[0].Entry != want[0] || !slices.Equal(got[0].Files, want[1:]) {
		t.Fatalf("publishes %+v", got)
	}

	for name, tc := range map[string]struct{ args, text string }{
		"no name":         {`{"entry":"site/index.html"}`, "name and entry are required"},
		"no entry":        {`{"name":"a"}`, "name and entry are required"},
		"unknown field":   {`{"name":"a","entry":"site/index.html","db":true}`, "bad arguments"},
		"dotdot":          {`{"name":"a","entry":"../x.html"}`, "outside your working directory"},
		"absolute out":    {`{"name":"a","entry":"` + filepath.Join(outside, "x.html") + `"}`, "outside your working directory"},
		"symlink out":     {`{"name":"a","entry":"link.html"}`, "outside your working directory"},
		"symlinked dir":   {`{"name":"a","entry":"out/x.html"}`, "outside your working directory"},
		"file dotdot":     {`{"name":"a","entry":"site/index.html","files":["site/../../x.js"]}`, "outside your working directory"},
		"missing":         {`{"name":"a","entry":"gone.html"}`, "gone.html"},
		"file in out dir": {`{"name":"a","entry":"site/index.html","files":["out/x.html"]}`, "outside your working directory"},
	} {
		r, err := set.Call(context.Background(), caller, "artifact_publish", json.RawMessage(tc.args))
		if err != nil || !r.IsError || !strings.Contains(r.Text, tc.text) {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
	if len(src.got.publishes) != 2 {
		t.Fatalf("failed calls published: %+v", src.got.publishes[2:])
	}

	// A failure from the service reaches the model.
	src.got.failure = &Failure{"unsupported entry type"}
	if r, err := set.Call(context.Background(), caller, "artifact_publish", json.RawMessage(`{"name":"a","entry":"site/index.html"}`)); err != nil || !r.IsError || r.Text != "unsupported entry type" {
		t.Fatalf("service failure %+v %v", r, err)
	}
	src.got.failure = errors.New("disk")
	if _, err := set.Call(context.Background(), caller, "artifact_publish", json.RawMessage(`{"name":"a","entry":"site/index.html"}`)); err == nil {
		t.Fatal("internal error reached the model")
	}
}
