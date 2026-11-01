package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/workspace"
	"github.com/yangflow/hoplet/proto"
)

func TestPrintDiff(t *testing.T) {
	d := proto.SessionDiff{BaselineError: "timed out", Repos: []proto.RepoDiff{
		{Path: ".", Branch: "main", State: proto.RepoOK, Patch: "diff --git a/x b/x\n+x\n", Truncated: true,
			Files: []proto.FileDiff{
				{Path: "x", Status: proto.FileStatusModified, Additions: 1},
				{Path: "d.dat", Status: proto.FileStatusModified, Filter: "lfs"},
			},
			Skipped: []proto.SkippedFile{{Path: "big.bin", Size: 99, Reason: "too large"}, {Path: "secret", Reason: "unreadable"}}},
		{Path: "api", State: proto.RepoNew},
		{Path: "web", State: proto.RepoError, Error: "boom"},
		{Path: "old", State: proto.RepoMissing},
		{Path: "huge", State: proto.RepoOK, Files: []proto.FileDiff{}, FilesTruncated: true, Truncated: true},
	}}
	var out, errb bytes.Buffer
	testApp(&out, &errb).printDiff(d, false)
	want := "baseline: timed out\n" +
		"== . (main) ok\ndiff --git a/x b/x\n+x\nd.dat: changed through filter lfs, no patch shown\n(patch truncated)\nskipped (too large): big.bin, 99 bytes\nskipped (unreadable): secret\n" +
		"== api new\nno files\n" +
		"== web error\nerror: boom\n" +
		"== old missing\nthe repository is gone\n" +
		"== huge ok\n(file list omitted: too many files or response too large)\n(patch truncated)\n"
	if out.String() != want {
		t.Fatalf("patch output:\n%s\nwant:\n%s", out.String(), want)
	}
	out.Reset()
	testApp(&out, &errb).printDiff(d, true)
	if !strings.Contains(out.String(), "== . (main) ok\n+1 -0 modified x\n+0 -0 modified d.dat (filter lfs)\n(patch truncated)\nskipped") {
		t.Fatalf("stat output:\n%s", out.String())
	}
	out.Reset()
	testApp(&out, &errb).printDiff(proto.SessionDiff{}, false)
	if out.String() != "no repositories\n" {
		t.Fatalf("empty output %q", out.String())
	}
	for _, tc := range []struct {
		f    proto.FileDiff
		want string
	}{
		{proto.FileDiff{Path: "a", Status: proto.FileStatusAdded, Additions: 3}, "+3 -0 added a"},
		{proto.FileDiff{Path: "b", OldPath: "a", Status: proto.FileStatusRenamed}, "+0 -0 renamed a -> b"},
		{proto.FileDiff{Path: "i.png", Status: proto.FileStatusAdded, Binary: true}, "+- -- added i.png"},
	} {
		if got := statLine(tc.f); got != tc.want {
			t.Errorf("statLine = %q, want %q", got, tc.want)
		}
	}
}

// TestSessionDiffInTheTerminal runs `session diff` against a real daemon
// process and a real git repository.
func TestSessionDiffInTheTerminal(t *testing.T) {
	if _, err := workspace.FindGit([]string{"PATH=" + os.Getenv("PATH")}); err != nil {
		t.Skipf("git 2.38 or newer is needed: %v", err)
	}
	h, _ := testHome(t)
	work := filepath.Join(h, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", work, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn("add", "-A")
	gitIn("commit", "-q", "-m", "init")
	fakeBin, err := fake.BuildBinary(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	script, err := fake.WriteScript(h, fake.Script{Session: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	startDaemonProcess(t, h, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script)
	id := strings.TrimSpace(mustCLI(t, "session", "new", "--harness", "fake", "--dir", work))
	short := id[:6]

	if out := mustCLI(t, "session", "diff", short); out != "== . (main) ok\nno changes\n" {
		t.Fatalf("diff before changes: %q", out)
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := mustCLI(t, "session", "diff", short, "--stat"); out != "== . (main) ok\n+1 -0 modified a.txt\n+1 -0 added b.txt\n" {
		t.Fatalf("diff --stat: %q", out)
	}
	out := mustCLI(t, "session", "diff", short)
	if !strings.HasPrefix(out, "== . (main) ok\ndiff --git a/a.txt b/a.txt\n") || !strings.Contains(out, "+two\n") || !strings.Contains(out, "+++ b/b.txt\n") {
		t.Fatalf("diff: %q", out)
	}
	var d proto.SessionDiff
	if err := json.Unmarshal([]byte(mustCLI(t, "session", "diff", short, "--json", "--repo", ".")), &d); err != nil ||
		len(d.Repos) != 1 || len(d.Repos[0].Files) != 2 || d.Repos[0].Patch == "" {
		t.Fatalf("diff --json: %+v %v", d, err)
	}
	if out := mustCLI(t, "session", "diff", short, "--repo", "nope"); out != "no repositories\n" {
		t.Fatalf("diff --repo nope: %q", out)
	}
	if code, _, errs := cli(t, "session", "diff", "zzzzzz"); code != 1 || !strings.Contains(errs, "zzzzzz") {
		t.Fatalf("unknown session: exit %d, %q", code, errs)
	}
	if code, _, _ := cli(t, "session", "diff"); code != 2 {
		t.Fatalf("diff without an ID: exit %d", code)
	}
}
