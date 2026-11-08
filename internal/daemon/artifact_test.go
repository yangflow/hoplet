package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// readArtifact reads a version's file to the end, chunk by chunk.
func readArtifact(t *testing.T, c *client.Client, p proto.ArtifactsReadParams) []byte {
	t.Helper()
	var out []byte
	for {
		p.Offset = int64(len(out))
		chunk, err := c.ReadArtifact(timeout(t), p)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk.Data) > proto.MaxArtifactChunk {
			t.Fatalf("chunk of %d bytes", len(chunk.Data))
		}
		out = append(out, chunk.Data...)
		if int64(len(out)) == chunk.Size {
			return out
		}
		if len(chunk.Data) == 0 {
			t.Fatalf("no progress at %d of %d", len(out), chunk.Size)
		}
	}
}

// bigFile is content over two read chunks.
func bigFile() []byte {
	return bytes.Repeat([]byte("artifact-"), proto.MaxArtifactChunk/9*2+100)
}

func writeFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for rel, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestArtifactPublishThroughTools checks a session publishing through its
// tool bridge: the version is recorded with an event in its log, listed,
// and read back whole; a publish from this machine adds a version.
func TestArtifactPublishThroughTools(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	big := bigFile()
	writeFiles(t, sess.Directory, map[string][]byte{"out/index.html": []byte("<script src=app.js></script>"), "out/app.js": big})
	w := watch(t, c, sess.ID, 0)
	w.next()

	tc := toolsOf(t, d, h, sess.ID)
	r := callTool(t, tc, "artifact_publish", `{"name":"report","entry":"out/index.html","files":["out/app.js"]}`)
	var got struct {
		ID      proto.ArtifactID   `json:"id"`
		Name    string             `json:"name"`
		Kind    proto.ArtifactKind `json:"kind"`
		Version int                `json:"version"`
	}
	if err := json.Unmarshal([]byte(r.Text), &got); r.IsError || err != nil || got.Name != "report" || got.Kind != proto.ArtifactHTML || got.Version != 1 {
		t.Fatalf("artifact_publish %+v %v", r, err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeArtifactPublished) == 1 })
	for _, ev := range w.events {
		if ev.Type == proto.TypeArtifactPublished {
			p, _ := ev.Decode()
			want := proto.ArtifactPublished{Artifact: got.ID, Name: "report", Kind: proto.ArtifactHTML, Version: 1, By: proto.ArtifactByHarness}
			if p != want {
				t.Fatalf("event %+v", p)
			}
		}
	}
	if b := readArtifact(t, c, proto.ArtifactsReadParams{Artifact: got.ID, Path: "app.js"}); !bytes.Equal(b, big) {
		t.Fatalf("read back %d bytes, want %d", len(b), len(big))
	}

	// Failures reach the model.
	for args, want := range map[string]string{
		`{"name":"x","entry":"../x.html"}`:     "outside your working directory",
		`{"name":"x","entry":"out/app.js"}`:    "unsupported entry type",
		`{"name":"a/b","entry":"out/app.js"}`:  "may not contain /",
		`{"name":"x","entry":"out/none.html"}`: "none.html",
	} {
		if r := callTool(t, tc, "artifact_publish", args); !r.IsError || !bytes.Contains([]byte(r.Text), []byte(want)) {
			t.Errorf("%s: %+v", args, r)
		}
	}

	// This machine's client publishes by path too.
	writeFiles(t, sess.Directory, map[string][]byte{"out/index.html": []byte("v2")})
	a, err := c.PublishArtifact(timeout(t), proto.ArtifactsPublishParams{Session: sess.ID, Name: "report", Entry: filepath.Join(sess.Directory, "out/index.html")})
	if err != nil || a.ID != got.ID || a.Version != 2 || a.By != proto.ArtifactByCLI || a.Files != 1 {
		t.Fatalf("publish v2 %+v %v", a, err)
	}
	list, err := c.Artifacts(timeout(t), sess.ID)
	if err != nil || len(list) != 1 || list[0].Version != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	vs, err := c.ArtifactVersions(timeout(t), got.ID)
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 || vs[1].By != proto.ArtifactByHarness || vs[1].Files != 2 {
		t.Fatalf("versions %+v %v", vs, err)
	}
	if _, err := c.ArtifactVersions(timeout(t), "nope"); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("versions of a missing artifact: %v", err)
	}
	if b := readArtifact(t, c, proto.ArtifactsReadParams{Artifact: got.ID, Version: 1, Path: "app.js"}); !bytes.Equal(b, big) {
		t.Fatal("version 1 changed")
	}
	if _, err := c.ReadArtifact(timeout(t), proto.ArtifactsReadParams{Artifact: got.ID, Path: "app.js"}); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("v2 app.js: %v", err)
	}
	if _, err := c.ReadArtifact(timeout(t), proto.ArtifactsReadParams{Artifact: got.ID, Path: "../v1/app.js"}); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("escape: %v", err)
	}
	if _, err := c.ReadArtifact(timeout(t), proto.ArtifactsReadParams{Artifact: got.ID, Offset: 3}); apiCode(err) != proto.CodeInvalid {
		t.Fatalf("past the end: %v", err)
	}
	if _, err := c.PublishArtifact(timeout(t), proto.ArtifactsPublishParams{Session: sess.ID, Name: "x", Entry: filepath.Join(sess.Directory, "out/app.js")}); apiCode(err) != proto.CodeInvalid {
		t.Fatalf("unsupported kind: %v", err)
	}
	if _, err := c.Artifacts(timeout(t), "nope"); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("missing session: %v", err)
	}
	if list, err := c.Artifacts(timeout(t), create(t, c, h).ID); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty list %#v %v", list, err)
	}
}

// TestArtifactsOverNoise checks that a paired device reads artifacts but
// cannot publish them.
func TestArtifactsOverNoise(t *testing.T) {
	b := startB(t, fake.Script{}, limits{})
	sess := create(t, b.local, b.home)
	big := bigFile()
	writeFiles(t, sess.Directory, map[string][]byte{"site/index.html": []byte("hi"), "site/big.bin": big})
	entry := filepath.Join(sess.Directory, "site/index.html")
	a, err := b.local.PublishArtifact(timeout(t), proto.ArtifactsPublishParams{Session: sess.ID, Name: "site", Entry: entry,
		Files: []string{filepath.Join(sess.Directory, "site/big.bin")}})
	if err != nil {
		t.Fatal(err)
	}

	c := dialRemote(t, b.pairWith(t, newIdentity(t), "phone"))
	if _, err := c.PublishArtifact(timeout(t), proto.ArtifactsPublishParams{Session: sess.ID, Name: "x", Entry: entry}); apiCode(err) != proto.CodeUnauthorized {
		t.Fatalf("publish over Noise: %v", err)
	}
	list, err := c.Artifacts(timeout(t), sess.ID)
	if err != nil || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list over Noise %+v %v", list, err)
	}
	if vs, err := c.ArtifactVersions(timeout(t), a.ID); err != nil || len(vs) != 1 || vs[0].ID != a.ID {
		t.Fatalf("versions over Noise %+v %v", vs, err)
	}
	if got := readArtifact(t, c, proto.ArtifactsReadParams{Artifact: a.ID, Path: "big.bin"}); !bytes.Equal(got, big) {
		t.Fatalf("read over Noise: %d bytes, want %d", len(got), len(big))
	}
}
