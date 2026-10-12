package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeDirName(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	for name, tc := range map[string]struct{ dir, want string }{
		"plain":         {"proj", "proj"},
		"dots":          {"a.b.c", "a-b-c"},
		"spaces":        {"my project", "my-project"},
		"hyphen kept":   {"a-b", "a-b"},
		"underscore":    {"a_b", "a-b"},
		"BMP non-ASCII": {"名前", "--"},
		"astral":        {"x😀y", "x--y"},
		"digits":        {"v2", "v2"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, tc.dir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Skip(err)
			}
			got := DirName(dir)
			prefix := DirName(real) + "-"
			if !strings.HasPrefix(got, prefix) || got[len(prefix):] != tc.want {
				t.Fatalf("DirName(%q) = %q, want %q%q", dir, got, prefix, tc.want)
			}
		})
	}
}

func TestClaudeDirNameUsesTheRealPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	os.Mkdir(target, 0o755)
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if DirName(link) != DirName(target) {
		t.Fatalf("%q vs %q", DirName(link), DirName(target))
	}
	if DirName("/no/such/dir") != "-no-such-dir" {
		t.Fatalf("a missing path is encoded as given: %q", DirName("/no/such/dir"))
	}
}

// The paths and names below were measured with Claude Code itself; keep them byte for byte, since the hash and
// the 200-character boundary depend on every character.
func TestClaudeDirNameTruncatesAndHashesLongPaths(t *testing.T) {
	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"space and dots": {
			"/private/tmp/baton-spike135/sp ace.d/my.repo",
			"-private-tmp-baton-spike135-sp-ace-d-my-repo",
		},
		"unicode BMP": {
			"/private/tmp/baton-spike135/项目/café",
			"-private-tmp-baton-spike135----caf-",
		},
		"emoji": {
			"/private/tmp/baton-spike135/😀/e😀m",
			"-private-tmp-baton-spike135----e--m",
		},
		"exactly 200 chars": {
			"/private/tmp/baton-spike135/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			"-private-tmp-baton-spike135-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		},
		"300 chars": {
			"/private/tmp/baton-spike135/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd/dddddddddddddddddddddddddddddddddddddddddddddddddd/e",
			"-private-tmp-baton-spike135-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd-dddddddddddddddddddddddddddddddddddddddddddddddddd-b8inn5",
		},
		"201 chars": {
			"/private/tmp/baton-spike135/ddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			"-private-tmp-baton-spike135-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd-j13xu",
		},
		"negative hash example": {
			"/tmp/test0",
			"-tmp-test0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := DirName(tc.path)
			if got != tc.want {
				t.Fatalf("DirName(%q)\n   got %q\n  want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestInstallClaudeSessionWritesByteForByteAndNeverReplaces(t *testing.T) {
	store := t.TempDir()
	cwd := t.TempDir()
	staged := filepath.Join(t.TempDir(), "staged")
	content := append([]byte("{\"a\":1}\n"), bytes.Repeat([]byte{0, 0xff, '\n'}, 1000)...)
	os.WriteFile(staged, content, 0o600)

	path, err := installSession(store, cwd, "s-1", staged)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(store, DirName(cwd), "s-1.jsonl"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatal("content differs")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if _, err := installSession(store, cwd, "s-1", staged); err == nil {
		t.Fatal("replaced a session file")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatal("the existing file changed")
	}
	for _, id := range []string{"../x", "a/b", "", "a b"} {
		if _, err := installSession(store, cwd, id, staged); err == nil {
			t.Fatalf("id %q accepted", id)
		}
	}

	removeSession(store, path)
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("the emptied directory stayed")
	}
	outside := filepath.Join(t.TempDir(), "keep.jsonl")
	os.WriteFile(outside, []byte("x"), 0o600)
	removeSession(store, outside)
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("a file outside the store was removed")
	}
}

func TestInstallClaudeSessionUsesClaudeDirName(t *testing.T) {
	// Verify that installSession uses DirName to determine the store subdirectory.
	// This is important for long paths where DirName applies truncation and hashing.
	store := t.TempDir()
	cwd := t.TempDir()
	staged := filepath.Join(t.TempDir(), "staged")
	content := []byte("{\"test\":1}\n")
	if err := os.WriteFile(staged, content, 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := installSession(store, cwd, "s-test", staged)
	if err != nil {
		t.Fatalf("installSession failed: %v", err)
	}

	// Verify the file was installed under the directory returned by DirName
	dirName := DirName(cwd)
	if want := filepath.Join(store, dirName, "s-test.jsonl"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}

	// Verify the content was written correctly
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatal("content differs")
	}
}

func TestFindResolvesTheWorkingDirectoryOfARelocatedSession(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "repo-1234abcd")
	os.MkdirAll(worktree, 0o755)
	dirName := DirName(worktree)
	os.MkdirAll(filepath.Join(root, dirName), 0o755)
	// The lines still name the directory of the machine the session came from.
	line := `{"type":"user","cwd":"/home/other/repo","sessionId":"s9"}` + "\n"
	os.WriteFile(filepath.Join(root, dirName, "s9.jsonl"), []byte(line), 0o600)

	c := New(root, nil)
	if got, ok := c.Find("s9"); !ok || got.Cwd != "/home/other/repo" {
		t.Fatalf("without a hint the recorded directory is all there is: %+v", got)
	}
	c.SetProjectHints(func() []string { return []string{"/somewhere/else", worktree} })
	got, ok := c.Find("s9")
	if !ok || got.Cwd != worktree {
		t.Fatalf("with the watched worktree: %+v", got)
	}

	// Once Claude Code has run there, the latest line says so, hint or not.
	appended := line + `{"type":"user","cwd":"` + worktree + `","sessionId":"s9"}` + "\n"
	os.WriteFile(filepath.Join(root, dirName, "s9.jsonl"), []byte(appended), 0o600)
	c.SetProjectHints(nil)
	if got, _ := c.Find("s9"); got.Cwd != worktree {
		t.Fatalf("latest recorded directory: %+v", got)
	}
}

func TestFindKeepsTheRecordedDirectoryWhenItIsTheStoreOwner(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	dirName := DirName(cwd)
	os.MkdirAll(filepath.Join(root, dirName), 0o755)
	os.WriteFile(filepath.Join(root, dirName, "s1.jsonl"), []byte(`{"cwd":"`+cwd+`"}`+"\n"), 0o600)
	c := New(root, nil)
	c.SetProjectHints(func() []string { return []string{t.TempDir()} })
	if got, _ := c.Find("s1"); got.Cwd != cwd {
		t.Fatalf("cwd = %q, want %q", got.Cwd, cwd)
	}
}

// Two agents on one machine share Claude Code's store: after a handoff the session is in the store directory
// of the source's directory and of the target's worktree. Each agent must find its own.
func TestFindPrefersTheCopyInAWatchedProjectWhenTheStoreHoldsTwo(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "repo-1234abcd")
	os.MkdirAll(worktree, 0o755)
	line := `{"type":"user","cwd":"` + source + `","sessionId":"s7"}` + "\n"
	for _, dir := range []string{DirName(source), DirName(worktree)} {
		os.MkdirAll(filepath.Join(root, dir), 0o755)
		os.WriteFile(filepath.Join(root, dir, "s7.jsonl"), []byte(line), 0o600)
	}
	// Whatever order the scan lists them in, the watched worktree wins.
	c := New(root, nil)
	c.SetProjectHints(func() []string { return []string{worktree} })
	got, ok := c.Find("s7")
	if !ok || got.Cwd != worktree || filepath.Dir(got.NativePath) != filepath.Join(root, DirName(worktree)) {
		t.Fatalf("found %+v", got)
	}
	c.SetProjectHints(func() []string { return []string{source} })
	if got, _ := c.Find("s7"); got.Cwd != source {
		t.Fatalf("the source's agent found %+v", got)
	}
}

// A session handed off from another machine is installed in the store directory of its worktree here, while
// its lines still record the working directory of the machine it came from.
func TestSessionInTheStoreDirectoryOfAPathIsInThatProject(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "repo-1234abcd")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, id := range map[string]string{DirName(worktree): "moved", "-some-other-dir": "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","cwd":"/home/other/repo","message":{"content":"hi"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, dir, id+".jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srcs, err := New(root, nil).Scan(context.Background())
	if err != nil || len(srcs) != 2 {
		t.Fatalf("scan: %d %v", len(srcs), err)
	}
	for _, src := range srcs {
		want := src.ID() == "moved"
		if got := src.InProject(filepath.Clean(worktree)); got != want {
			t.Errorf("%s in the worktree: %v", src.ID(), got)
		}
		if !src.InProject("/home/other/repo") {
			t.Errorf("%s not in the directory it records", src.ID())
		}
	}
}
