package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
)

func finders(claudeRoot string, exclude []string) []provider.Finder {
	return []provider.Finder{
		claudeprovider.New(claudeRoot, exclude),
	}
}

func TestListGitTree(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--template=")
	run(t, dir, "git", "config", "user.email", "t@t.test")
	run(t, dir, "git", "config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "init")
	run(t, dir, "git", "remote", "add", "origin", "git@example.test:o/ws.git")

	root := t.TempDir()
	enc := "-tmp-ws"
	sess := filepath.Join(root, enc)
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "sess-ws.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := List(context.Background(), finders(root, nil), "sess-ws")
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit == "" {
		t.Fatal("empty commit")
	}
	if got.Origin != "git@example.test:o/ws.git" {
		t.Fatalf("origin = %q", got.Origin)
	}
	if !contains(got.Paths, "README.md") || !contains(got.Paths, "src/a.go") {
		t.Fatalf("paths = %#v", got.Paths)
	}
	if got.Truncated {
		t.Fatal("truncated")
	}
}

func TestListWithoutCommit(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--template=")
	run(t, dir, "git", "config", "user.email", "t@t.test")
	run(t, dir, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	enc := "-tmp-uncommitted"
	sess := filepath.Join(root, enc)
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "sess-uc.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := List(context.Background(), finders(root, nil), "sess-uc")
	if err != nil {
		t.Fatal(err)
	}
	// Without a commit there is nothing to compare with: no paths and no changes.
	if got.Git || got.Commit != "" || len(got.Paths) != 0 || len(got.Changes) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestListNotGitIsEmpty(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	enc := "-tmp-plain"
	sess := filepath.Join(root, enc)
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "sess-ng.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := List(context.Background(), finders(root, nil), "sess-ng")
	if err != nil {
		t.Fatal(err)
	}
	if got.Git || got.Commit != "" || len(got.Paths) != 0 || len(got.Changes) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--template=")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	enc := "-tmp-read"
	sess := filepath.Join(root, enc)
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "sess-rd.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	text, truncated, err := readText(finders(root, nil), "sess-rd", "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if truncated || text != "# hi\n" {
		t.Fatalf("text=%q truncated=%v", text, truncated)
	}
}

func TestReadEscapesDotDot(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--template=")
	root := t.TempDir()
	enc := "-tmp-esc"
	sess := filepath.Join(root, enc)
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "sess-esc.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := readText(finders(root, nil), "sess-esc", "../secret")
	if err == nil || err.Error() != "path-escape" {
		t.Fatalf("err = %v", err)
	}
}

func TestListUnknownSession(t *testing.T) {
	_, err := List(context.Background(), finders(t.TempDir(), nil), "missing")
	if err == nil || err.Error() != "session-not-on-this-device" {
		t.Fatalf("err = %v", err)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		"GIT_TEMPLATE_DIR=",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// readText reads a worktree file and returns its text, as a text file has it.
func readText(fs []provider.Finder, sessionID, rel string) (string, bool, error) {
	file, err := Read(context.Background(), fs, sessionID, rel, "")
	return file.Text, file.Truncated, err
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func workspaceReadFixture(t testing.TB, dir string) []provider.Finder {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + dir + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "fixture", "read-boundary.jsonl"), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	return finders(root, nil)
}

func TestReadSymlinkBoundary(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "fixture.txt"), []byte("outside-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("inside-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(dir, filepath.Join(outside, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]string{"direct": filepath.Join(outside, "fixture.txt"), "relative": relative, "nested/out": outside, "chain": "relative", "internal": "inside.txt", "nested/internal": "../inside.txt"}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	fs := workspaceReadFixture(t, dir)
	for _, path := range []string{"direct", "relative", "nested/out/fixture.txt", "chain"} {
		t.Run(path, func(t *testing.T) {
			text, _, err := readText(fs, "read-boundary", path)
			if err == nil || text != "" {
				t.Fatalf("outside read returned %q, error %v", text, err)
			}
		})
	}
	for _, path := range []string{"inside.txt", "internal", "nested/internal"} {
		text, truncated, err := readText(fs, "read-boundary", path)
		if err != nil || truncated || text != "inside-fixture" {
			t.Fatalf("%s: text=%q truncated=%v error=%v", path, text, truncated, err)
		}
	}
}

func TestReadBoundedText(t *testing.T) {
	dir := t.TempDir()
	fs := workspaceReadFixture(t, dir)
	for _, size := range []int{0, maxFileBytes, maxFileBytes + 1, 16 * maxFileBytes} {
		if err := os.WriteFile(filepath.Join(dir, "text"), []byte(strings.Repeat("a", size)), 0600); err != nil {
			t.Fatal(err)
		}
		text, truncated, err := readText(fs, "read-boundary", "text")
		want := size
		if want > maxFileBytes {
			want = maxFileBytes
		}
		if err != nil || len(text) != want || truncated != (size > maxFileBytes) {
			t.Fatalf("size=%d len=%d truncated=%v err=%v", size, len(text), truncated, err)
		}
	}
}

func TestReadAbsoluteInternalLinkRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "text"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "text"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	text, _, err := readText(workspaceReadFixture(t, dir), "read-boundary", "link")
	if err == nil || text != "" {
		t.Fatalf("text=%q error=%v", text, err)
	}
}

func TestReadBinaryWindow(t *testing.T) {
	dir := t.TempDir()
	fs := workspaceReadFixture(t, dir)
	// The window is the first maxFileBytes; a NUL byte after it does not make the file binary.
	for _, offset := range []int{0, maxFileBytes - 1, maxFileBytes, maxFileBytes + 1} {
		raw := []byte(strings.Repeat("a", maxFileBytes+2))
		raw[offset] = 0
		if err := os.WriteFile(filepath.Join(dir, "text"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		file, err := Read(context.Background(), fs, "read-boundary", "text", "")
		text, truncated := file.Text, file.Truncated
		if offset < maxFileBytes {
			if err != nil || file.Kind != "binary" || file.Size != int64(len(raw)) || text != "" {
				t.Fatalf("offset=%d kind=%s size=%d err=%v", offset, file.Kind, file.Size, err)
			}
		} else if err != nil || !truncated || len(text) != maxFileBytes {
			t.Fatalf("offset=%d len=%d truncated=%v err=%v", offset, len(text), truncated, err)
		}
	}
}

func TestReadDuringLinkReplacement(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	for path, content := range map[string]string{filepath.Join(dir, "inside"): "inside", filepath.Join(outside, "fixture"): "outside-fixture"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs := workspaceReadFixture(t, dir)
	if err := os.Symlink("inside", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	ready := make(chan struct{})
	failures := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, target := range []string{"inside", filepath.Join(outside, "fixture")} {
				if err := os.Symlink(target, filepath.Join(dir, "next")); err != nil {
					failures <- err
					if first {
						close(ready)
					}
					return
				}
				if err := os.Rename(filepath.Join(dir, "next"), filepath.Join(dir, "link")); err != nil {
					failures <- err
					if first {
						close(ready)
					}
					return
				}
				if first {
					close(ready)
					first = false
				}
			}
		}
	}()
	<-ready
	defer func() {
		close(stop)
		wg.Wait()
		select {
		case err := <-failures:
			t.Error(err)
		default:
		}
	}()
	for i := 0; i < 500; i++ {
		text, _, err := readText(fs, "read-boundary", "link")
		if err == nil && text != "inside" {
			t.Fatalf("outside read: %q", text)
		}
	}
}

func TestReadRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	text, _, err := readText(workspaceReadFixture(t, dir), "read-boundary", "folder")
	if err == nil || err.Error() != "not-regular-file" || text != "" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func BenchmarkReadFileSize(b *testing.B) {
	for _, size := range []int{maxFileBytes, 64 * maxFileBytes} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			dir := b.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "text"), []byte(strings.Repeat("a", size)), 0600); err != nil {
				b.Fatal(err)
			}
			fs := workspaceReadFixture(b, dir)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				text, truncated, err := readText(fs, "read-boundary", "text")
				if err != nil || len(text) != maxFileBytes || truncated != (size > maxFileBytes) {
					b.Fatalf("len=%d truncated=%v err=%v", len(text), truncated, err)
				}
			}
		})
	}
}
