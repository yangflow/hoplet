package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// inspectRepo makes a repository with one commit (the files of committed), writes a session whose working
// directory is cwd (relative to the repository, "" for its root) and returns the repository and the finders.
func inspectRepo(t *testing.T, cwd string, committed map[string]string) (string, []provider.Finder) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "--template=")
	run(t, dir, "git", "config", "user.email", "t@t.test")
	run(t, dir, "git", "config", "user.name", "t")
	for name, content := range committed {
		put(t, dir, name, content)
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "init")
	work := filepath.Join(dir, filepath.FromSlash(cwd))
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, workspaceReadFixture(t, work)
}

// put writes a file of a test working tree.
func put(t *testing.T, dir, name, content string) {
	t.Helper()
	write(t, dir, filepath.FromSlash(name), []byte(content), 0o644)
}

func changeOf(changes []Change, name string) *Change {
	for i := range changes {
		if changes[i].Path == name {
			return &changes[i]
		}
	}
	return nil
}

func counts(c *Change) string {
	if c == nil {
		return "absent"
	}
	if c.Binary {
		return c.Status + " binary"
	}
	if c.Added == nil || c.Removed == nil {
		return c.Status + " uncounted"
	}
	return fmt.Sprintf("%s +%d -%d", c.Status, *c.Added, *c.Removed)
}

const pngHeader = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"

func TestListChanges(t *testing.T) {
	dir, fs := inspectRepo(t, "", map[string]string{
		"keep.txt":   "same\n",
		"edit.txt":   "one\ntwo\nthree\n",
		"gone.txt":   "a\nb\n",
		"pic.bin":    "\x00\x01\x02",
		"staged.txt": "x\n",
	})
	put(t, dir, "edit.txt", "one\n2\nthree\nfour\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "pic.bin", "\x00\x01\x03\x04")
	put(t, dir, "new.txt", "a\nb\nc")
	put(t, dir, "new.bin", "\x00zz")
	put(t, dir, "big.txt", strings.Repeat("x\n", maxCountBytes))
	put(t, dir, "added.txt", "1\n2\n")
	run(t, dir, "git", "add", "added.txt")
	put(t, dir, ".gitignore", "ignored.txt\n")
	put(t, dir, "ignored.txt", "nope\n")

	got, err := List(context.Background(), fs, "read-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Git || got.Commit == "" {
		t.Fatalf("git=%v commit=%q", got.Git, got.Commit)
	}
	want := map[string]string{
		"edit.txt":    "modified +2 -1",
		"gone.txt":    "deleted +0 -2",
		"pic.bin":     "modified binary",
		"new.txt":     "untracked +3 -0",
		"new.bin":     "untracked binary",
		"big.txt":     "untracked uncounted",
		"added.txt":   "added +2 -0",
		".gitignore":  "untracked +1 -0",
		"keep.txt":    "absent",
		"ignored.txt": "absent",
	}
	for name, w := range want {
		if c := counts(changeOf(got.Changes, name)); c != w {
			t.Errorf("%s: %s, want %s", name, c, w)
		}
	}
	if len(got.Changes) != 8 {
		t.Errorf("changes = %+v", got.Changes)
	}
	for i := 1; i < len(got.Changes); i++ {
		if got.Changes[i-1].Path >= got.Changes[i].Path {
			t.Fatalf("not sorted: %+v", got.Changes)
		}
	}
	if !contains(got.Paths, "new.txt") || contains(got.Paths, "ignored.txt") {
		t.Fatalf("paths = %v", got.Paths)
	}
}

func TestListChangesRelativeToWorkingDirectory(t *testing.T) {
	dir, fs := inspectRepo(t, "app", map[string]string{"app/main.go": "package main\n", "other.txt": "o\n"})
	put(t, dir, "app/main.go", "package main\n\nfunc main() {}\n")
	put(t, dir, "app/sub/new.go", "package sub\n")
	put(t, dir, "other.txt", "changed\n")
	put(t, dir, "outside.txt", "x\n")

	got, err := List(context.Background(), fs, "read-boundary")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range got.Changes {
		names = append(names, c.Path+":"+c.Status)
	}
	if strings.Join(names, ",") != "main.go:modified,sub/new.go:untracked" {
		t.Fatalf("changes = %v", names)
	}
	if !contains(got.Paths, "main.go") || contains(got.Paths, "other.txt") {
		t.Fatalf("paths = %v", got.Paths)
	}
}

func TestReadKinds(t *testing.T) {
	dir, fs := inspectRepo(t, "", map[string]string{"README.md": "# old\n"})
	put(t, dir, "README.md", "# new\n")
	png := pngHeader + strings.Repeat("\x00", 32)
	put(t, dir, "logo.png", png)
	put(t, dir, "fake.png", "not a png")
	svg := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`
	put(t, dir, "icon.svg", svg)
	put(t, dir, "blob.dat", "ab\x00cd")
	put(t, dir, "latin1.txt", "caf\xe9\n")
	put(t, dir, "huge.png", pngHeader+strings.Repeat("\x00", maxImageBytes))
	put(t, dir, "cut.txt", strings.Repeat("a", maxFileBytes-1)+"é")

	cases := []struct {
		name, kind, media string
		size              int
	}{
		{"README.md", "text", "", 6},
		{"logo.png", "image", "image/png", len(png)},
		{"fake.png", "text", "", 9},
		{"icon.svg", "image", "image/svg+xml", len(svg)},
		{"blob.dat", "binary", "", 5},
		{"latin1.txt", "binary", "", 5},
		{"huge.png", "binary", "", len(pngHeader) + maxImageBytes},
		{"cut.txt", "text", "", maxFileBytes + 1},
	}
	for _, c := range cases {
		file, err := Read(context.Background(), fs, "read-boundary", c.name, "worktree")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if file.Kind != c.kind || file.MediaType != c.media || file.Size != int64(c.size) {
			t.Errorf("%s: kind=%s media=%s size=%d", c.name, file.Kind, file.MediaType, file.Size)
		}
		if c.name == "logo.png" && string(file.Data) != png {
			t.Errorf("%s: data differs", c.name)
		}
	}
	cut, _ := Read(context.Background(), fs, "read-boundary", "cut.txt", "")
	if !cut.Truncated || len(cut.Text) != maxFileBytes {
		t.Fatalf("cut: truncated=%v len=%d", cut.Truncated, len(cut.Text))
	}
	if _, err := Read(context.Background(), fs, "read-boundary", ".git/config", ""); err == nil || err.Error() != "path-escape" {
		t.Fatalf(".git read: %v", err)
	}
	if _, err := Read(context.Background(), fs, "read-boundary", "README.md", "tomorrow"); err == nil || err.Error() != "invalid-at" {
		t.Fatalf("at: %v", err)
	}
}

func TestReadAtHead(t *testing.T) {
	dir, fs := inspectRepo(t, "app", map[string]string{"app/a.txt": "committed\n", "app/logo.png": pngHeader})
	put(t, dir, "app/a.txt", "edited\n")
	put(t, dir, "app/new.txt", "new\n")

	file, err := Read(context.Background(), fs, "read-boundary", "a.txt", "head")
	if err != nil || file.Kind != "text" || file.Text != "committed\n" || file.Size != 10 {
		t.Fatalf("head: %+v %v", file, err)
	}
	file, err = Read(context.Background(), fs, "read-boundary", "a.txt", "")
	if err != nil || file.Text != "edited\n" {
		t.Fatalf("worktree: %+v %v", file, err)
	}
	file, err = Read(context.Background(), fs, "read-boundary", "logo.png", "head")
	if err != nil || file.Kind != "image" || !bytes.Equal(file.Data, []byte(pngHeader)) {
		t.Fatalf("head image: kind=%s %v", file.Kind, err)
	}
	if _, err := Read(context.Background(), fs, "read-boundary", "new.txt", "head"); err == nil || err.Error() != "not-in-head" {
		t.Fatalf("untracked at head: %v", err)
	}
	if _, err := Read(context.Background(), fs, "read-boundary", ".", "head"); err == nil {
		t.Fatal("directory at head read")
	}

	plain := t.TempDir()
	put(t, plain, "a.txt", "x")
	if _, err := Read(context.Background(), workspaceReadFixture(t, plain), "read-boundary", "a.txt", "head"); err == nil || err.Error() != "not-a-git-repository" {
		t.Fatalf("head without git: %v", err)
	}
}

func TestReadDiff(t *testing.T) {
	dir, fs := inspectRepo(t, "", map[string]string{
		"edit.txt": "one\ntwo\nthree\n",
		"gone.txt": "a\nb\n",
		"pic.bin":  "\x00\x01\x02",
		"same.txt": "s\n",
		"a*.txt":   "star\n",
	})
	put(t, dir, "edit.txt", "one\n2\nthree\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "pic.bin", "\x00\x01\x02\x03\x04")
	put(t, dir, "new.txt", "x\ny")
	put(t, dir, "one.txt", "only\n")
	put(t, dir, "new.bin", "\x00\x00")
	put(t, dir, "empty.txt", "")
	put(t, dir, "a*.txt", "star!\n")
	put(t, dir, "ab.txt", "not matched by the pattern\n")

	read := func(name string) Diff {
		t.Helper()
		d, err := ReadDiff(context.Background(), fs, "read-boundary", name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return d
	}
	if d := read("edit.txt"); d.Status != "modified" || d.Kind != "text" ||
		d.Text != "@@ -1,3 +1,3 @@\n one\n-two\n+2\n three\n" || d.Size != int64(len(d.Text)) || d.Truncated {
		t.Fatalf("edit: %+v", d)
	}
	if d := read("gone.txt"); d.Status != "deleted" || d.Text != "@@ -1,2 +0,0 @@\n-a\n-b\n" {
		t.Fatalf("gone: %+v", d)
	}
	if d := read("pic.bin"); d.Kind != "binary" || d.Status != "modified" || d.OldSize == nil || *d.OldSize != 3 || d.NewSize == nil || *d.NewSize != 5 {
		t.Fatalf("pic: %+v", d)
	}
	if d := read("new.txt"); d.Status != "untracked" || d.Text != "@@ -0,0 +1,2 @@\n+x\n+y\n\\ No newline at end of file\n" || d.Size != int64(len(d.Text)) {
		t.Fatalf("new: %+v", d)
	}
	if d := read("one.txt"); d.Text != "@@ -0,0 +1 @@\n+only\n" {
		t.Fatalf("one: %q", d.Text)
	}
	if d := read("new.bin"); d.Kind != "binary" || d.OldSize != nil || d.NewSize == nil || *d.NewSize != 2 {
		t.Fatalf("new.bin: %+v", d)
	}
	if d := read("empty.txt"); d.Kind != "text" || d.Text != "" || d.Size != 0 {
		t.Fatalf("empty: %+v", d)
	}
	if d := read("a*.txt"); d.Text != "@@ -1 +1 @@\n-star\n+star!\n" {
		t.Fatalf("literal path: %q", d.Text)
	}
	if _, err := ReadDiff(context.Background(), fs, "read-boundary", "same.txt"); err == nil || err.Error() != "not-changed" {
		t.Fatalf("same: %v", err)
	}
	if _, err := ReadDiff(context.Background(), fs, "read-boundary", "../x"); err == nil || err.Error() != "path-escape" {
		t.Fatalf("escape: %v", err)
	}
}

func TestReadDiffTruncates(t *testing.T) {
	line := strings.Repeat("z", 99) + "\n"
	lines := maxDiffBytes/len(line) + 100
	dir, fs := inspectRepo(t, "", map[string]string{"big.txt": "first\n"})
	put(t, dir, "big.txt", strings.Repeat(line, lines))
	put(t, dir, "fresh.txt", strings.Repeat(line, lines))
	for _, name := range []string{"big.txt", "fresh.txt"} {
		d, err := ReadDiff(context.Background(), fs, "read-boundary", name)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Truncated || len(d.Text) > maxDiffBytes || !strings.HasSuffix(d.Text, "\n") || d.Size <= maxDiffBytes {
			t.Fatalf("%s: truncated=%v len=%d size=%d", name, d.Truncated, len(d.Text), d.Size)
		}
		if !strings.HasPrefix(d.Text, "@@ ") || d.Size < int64(lines*(len(line)+1)) {
			t.Fatalf("%s: size=%d head=%q", name, d.Size, d.Text[:20])
		}
	}
}

func TestPickPathsKeepsChangesAndTheTopOfTheTree(t *testing.T) {
	var names []string
	for i := 0; i < maxPaths+50; i++ {
		names = append(names, fmt.Sprintf("gen/f%03d.txt", i))
	}
	names = append(names, "deep/a/b/changed.ts", "README.md", "src/main.ts", "gone.md")
	changes := []Change{{Path: "deep/a/b/changed.ts", Status: "modified"}, {Path: "gone.md", Status: "deleted"}}
	paths, truncated := pickPaths(names, changes)
	if !truncated || len(paths) != maxPaths {
		t.Fatalf("truncated=%v len=%d", truncated, len(paths))
	}
	if paths[0] != "deep/a/b/changed.ts" || paths[1] != "README.md" {
		t.Fatalf("order: %v", paths[:3])
	}
	if slices.Contains(paths, "gone.md") || !slices.Contains(paths, "src/main.ts") {
		t.Fatalf("a deleted path is listed or src/main.ts is cut: %v", paths[:5])
	}
}
