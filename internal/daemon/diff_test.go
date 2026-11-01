package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/client"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/workspace"
	"github.com/yangflow/hoplet/proto"
)

// requireGit skips the test unless git 2.38 or newer is on the PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := workspace.FindGit([]string{"PATH=" + os.Getenv("PATH")}); err != nil {
		t.Skipf("git 2.38 or newer is needed: %v", err)
	}
}

// gitIn runs git in dir for test setup.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestSessionDiff(t *testing.T) {
	requireGit(t)
	h, script := newHome(t, fake.Script{Session: "n1"})
	work := filepath.Join(h, "work")
	gitIn(t, work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "init")
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)

	d, err := c.Diff(timeout(t), proto.DiffParams{Session: sess.ID})
	if err != nil || d.BaselineAt == nil || d.BaselineError != "" || len(d.Repos) != 1 || len(d.Repos[0].Files) != 0 {
		t.Fatalf("diff before any change: %+v %v", d, err)
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err = c.Diff(timeout(t), proto.DiffParams{Session: sess.ID, Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	r := d.Repos[0]
	if r.Path != "." || r.State != proto.RepoOK || r.Branch != "main" || len(r.Files) != 2 ||
		r.Files[0].Path != "a.txt" || r.Files[0].Additions != 1 || r.Files[1].Status != proto.FileStatusAdded ||
		!strings.Contains(r.Patch, "+two") || r.SHA == "" {
		t.Fatalf("diff %+v", r)
	}
	if _, err := os.Stat(filepath.Join(h, "objects")); err != nil {
		t.Fatalf("Hoplet's object directory: %v", err)
	}
	if _, err := c.Diff(timeout(t), proto.DiffParams{Session: "nope"}); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("unknown session: %v", err)
	}
	// Doctor reports the git it found.
	if g := checksByName(mustDoctor(t, c))["git"]; g.Status != proto.CheckOK || !strings.HasPrefix(g.Detail, "version 2.") {
		t.Fatalf("git check %+v", g)
	}
}

func TestDiffWithoutGit(t *testing.T) {
	h, script := newHome(t, fake.Script{Session: "n1"})
	cfg := testConfig(h, script)
	cfg.Env = env.Static{"PATH=" + t.TempDir()}
	start(t, cfg)
	c := dial(t, h, "device:a")
	sess := create(t, c, h) // a session is still created
	if _, err := c.Diff(timeout(t), proto.DiffParams{Session: sess.ID}); apiCode(err) != proto.CodeUnsupported {
		t.Fatalf("diff without git: %v", err)
	}
	g := checksByName(mustDoctor(t, c))["git"]
	if g.Status != proto.CheckFail || !strings.Contains(g.Detail, "diffs need git 2.38 or newer") {
		t.Fatalf("git check %+v", g)
	}
}

func mustDoctor(t *testing.T, c *client.Client) proto.DoctorReport {
	t.Helper()
	r, err := c.Doctor(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
