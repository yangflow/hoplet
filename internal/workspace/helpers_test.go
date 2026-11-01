package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// testEnv is the environment of git in tests: the PATH, and an empty home
// so that no user configuration applies.
func testEnv(t testing.TB) []string {
	t.Helper()
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
}

// newGit returns the git under test, or skips the test when git 2.38 or
// newer is missing.
func newGit(t testing.TB) Git {
	t.Helper()
	g, err := FindGit(testEnv(t))
	if err != nil {
		t.Skipf("git 2.38 or newer is needed: %v", err)
	}
	return g
}

// git runs plain git in dir, as the user would, and returns its output.
func git(t testing.TB, g Git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(g.Bin, append([]string{"-c", "maintenance.auto=false", "-c", "gc.auto=0", "-C", dir}, args...)...)
	cmd.Env = append(slices.Clone(g.Env), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// initRepo creates a repository at dir with the given files, committed.
func initRepo(t testing.TB, g Git, dir string, files map[string]string, extra ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, g, dir, append([]string{"init", "-q", "-b", "main"}, extra...)...)
	for name, content := range files {
		write(t, dir, name, content)
	}
	if len(files) > 0 {
		git(t, g, dir, "add", "-A")
		git(t, g, dir, "commit", "-q", "-m", "init")
	}
}

func write(t testing.TB, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertGitUntouched records every file and directory under the repository's
// git dir and common git dir (and its .git file, if any) with size, mode,
// modification time and content hash, and returns a function that fails the
// test when anything differs: nothing created, changed or removed. Call it
// before the code under test runs and the result after: defer
// assertGitUntouched(t, g, repo)().
func assertGitUntouched(t testing.TB, g Git, repoAbs string) func() {
	t.Helper()
	roots := []string{filepath.Join(repoAbs, ".git")}
	out := git(t, g, repoAbs, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	roots = append(roots, strings.Fields(out)...)
	record := func() map[string]string {
		state := map[string]string{}
		for _, root := range roots {
			err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				fi, err := d.Info()
				if err != nil {
					return err
				}
				if fi.IsDir() {
					state[p] = fmt.Sprintf("dir %o %d", fi.Mode(), fi.ModTime().UnixNano())
					return nil
				}
				sum := ""
				if fi.Mode().IsRegular() {
					b, err := os.ReadFile(p)
					if err != nil {
						return err
					}
					sum = fmt.Sprintf("%x", sha256.Sum256(b))
				}
				state[p] = fmt.Sprintf("file %o %d %d %s", fi.Mode(), fi.Size(), fi.ModTime().UnixNano(), sum)
				return nil
			})
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("record %s: %v", root, err)
			}
		}
		return state
	}
	before := record()
	return func() {
		t.Helper()
		after := record()
		for p, v := range before {
			if w, ok := after[p]; !ok {
				t.Errorf("git dir: %s was removed", p)
			} else if w != v {
				t.Errorf("git dir: %s changed: %s -> %s", p, v, w)
			}
		}
		for p := range after {
			if _, ok := before[p]; !ok {
				t.Errorf("git dir: %s was created", p)
			}
		}
	}
}

// guard is assertGitUntouched for several repositories at once.
func guard(t testing.TB, g Git, repos ...string) func() {
	t.Helper()
	var checks []func()
	for _, r := range repos {
		checks = append(checks, assertGitUntouched(t, g, r))
	}
	return func() {
		t.Helper()
		for _, c := range checks {
			c()
		}
	}
}

// fixture is a Service over a real store with one session.
type fixture struct {
	svc  *Service
	st   *store.Store
	home string
	id   proto.SessionID
}

func newFixture(t testing.TB, g Git) fixture {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	id := proto.SessionID("s1")
	_, err = st.CreateSession(context.Background(), proto.Session{
		ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "fake"}, Directory: "/work",
		Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe,
	})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	return fixture{svc: &Service{Git: g, Home: home, Store: st, Now: func() time.Time { return now }}, st: st, home: home, id: id}
}

func (f fixture) baseline(t testing.TB, dir string) {
	t.Helper()
	if err := f.svc.CaptureBaseline(context.Background(), dir)(context.Background(), f.id); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) diff(t testing.TB, dir string, p proto.DiffParams) proto.SessionDiff {
	t.Helper()
	p.Session = f.id
	d, err := f.svc.Diff(context.Background(), f.id, dir, p)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// repoByPath finds a repository in a diff.
func repoByPath(t testing.TB, d proto.SessionDiff, path string) proto.RepoDiff {
	t.Helper()
	for _, r := range d.Repos {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("no repo %q in %+v", path, d.Repos)
	return proto.RepoDiff{}
}

// fileSummary renders a file list as "status:path" items, sorted by path.
func fileSummary(files []proto.FileDiff) []string {
	out := []string{}
	for _, f := range files {
		s := string(f.Status) + ":" + f.Path
		if f.OldPath != "" {
			s += "<-" + f.OldPath
		}
		out = append(out, s)
	}
	return out
}

func expectFiles(t testing.TB, got []proto.FileDiff, want ...string) {
	t.Helper()
	if g := fileSummary(got); !slices.Equal(g, want) {
		t.Fatalf("files %v, want %v", g, want)
	}
}

// tryInit makes an empty repository with the object format, failing instead
// of stopping the test.
func tryInit(g Git, dir, format string) (string, error) {
	cmd := exec.Command(g.Bin, "-C", dir, "init", "-q", "-b", "main", "--object-format="+format)
	cmd.Env = g.Env
	out, err := cmd.CombinedOutput()
	return string(out), err
}
