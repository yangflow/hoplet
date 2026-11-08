package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, s := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArtifactCommands(t *testing.T) {
	h, _ := testHome(t)
	fakeBin, err := fake.BuildBinary(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	script, err := fake.WriteScript(h, fake.Script{Session: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	startDaemonProcess(t, h, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script)
	work := t.TempDir()
	id := strings.TrimSpace(mustCLI(t, "session", "new", "--harness", "fake", "--dir", work))
	short := id[:6]

	if out := mustCLI(t, "artifact", "list", short); out != "no artifacts\n" {
		t.Fatalf("empty list: %q", out)
	}

	// A file is the entry and names the artifact.
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"notes.md":            "# notes",
		"site/index.html":     "<img src=img/a.png>",
		"site/img/a.png":      "png",
		"site/.env":           "secret",
		"site/.git/HEAD":      "ref",
		"site/img/.cache/x":   "x",
		"bare/page.html":      "no index",
		"other/index.htm":     "htm",
		"unsupported/x.svg":   "<svg/>",
		"unsupported/x.unknw": "?",
	})
	var v proto.Artifact
	if err := json.Unmarshal([]byte(mustCLI(t, "artifact", "publish", short, filepath.Join(src, "notes.md"), "--json")), &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "notes.md" || v.Kind != proto.ArtifactMarkdown || v.Version != 1 || v.By != proto.ArtifactByCLI || v.Files != 1 {
		t.Fatalf("file publish %+v", v)
	}

	// A directory publishes index.html and its files, skipping dotfiles,
	// under the directory's name; a relative path works.
	t.Chdir(src)
	out := mustCLI(t, "artifact", "publish", short, "site")
	if !strings.HasPrefix(out, "published site v1 (html, 2 files) ") {
		t.Fatalf("dir publish: %q", out)
	}
	out = mustCLI(t, "artifact", "publish", short, "site", "--name", "notes2")
	if !strings.HasPrefix(out, "published notes2 v1 (html, 2 files) ") {
		t.Fatalf("--name: %q", out)
	}
	if out := mustCLI(t, "artifact", "publish", short, "site"); !strings.HasPrefix(out, "published site v2 ") {
		t.Fatalf("second version: %q", out)
	}

	out = mustCLI(t, "artifact", "list", short)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(out, "site ") || !strings.Contains(out, "v2") || !strings.Contains(out, "markdown") {
		t.Fatalf("list: %q", out)
	}
	var list []proto.Artifact
	if err := json.Unmarshal([]byte(mustCLI(t, "artifact", "list", short, "--json")), &list); err != nil || len(list) != 3 {
		t.Fatalf("list --json %+v %v", list, err)
	}

	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"artifact", "publish", short, "bare"}, 1, "has no index.html"},
		{[]string{"artifact", "publish", short, "other"}, 1, "has no index.html"},
		{[]string{"artifact", "publish", short, "unsupported/x.svg"}, 1, "unsupported entry type"},
		{[]string{"artifact", "publish", short, "missing.html"}, 1, "missing.html"},
		{[]string{"artifact", "publish", "zzzzzz", "notes.md"}, 1, "no session"},
		{[]string{"--host", "b", "artifact", "publish", short, "notes.md"}, 2, "publish only works on this machine"},
		{[]string{"artifact", "publish", short}, 2, ""},
		{[]string{"artifact", "list"}, 2, ""},
		{[]string{"artifact", "nope"}, 2, "unknown command"},
	} {
		code, _, errs := cli(t, tc.args...)
		if code != tc.code || !strings.Contains(errs, tc.want) {
			t.Errorf("%v: exit %d %q, want %d %q", tc.args, code, errs, tc.code, tc.want)
		}
	}
}
