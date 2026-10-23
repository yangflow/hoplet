package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// childWorktree makes a repository with one commit, a Hoplet worktree on branch "feat" tied to the fixture
// session, and returns the base commit, the worktree and the finders that place the session in it.
func childWorktree(t *testing.T, committed map[string]string) (base string, wt NewWorktree, fs []provider.Finder) {
	t.Helper()
	clone, _ := inspectRepo(t, "", committed)
	ctx := context.Background()
	wt, err := CreateNewWorktree(ctx, clone, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Mark(ctx, "read-boundary"); err != nil {
		t.Fatal(err)
	}
	run(t, wt.Path, "git", "config", "user.email", "t@t.test")
	run(t, wt.Path, "git", "config", "user.name", "t")
	return wt.Base, wt, workspaceReadFixture(t, wt.Dir)
}

func TestReadChangesOfAWorktreeCountCommittedWork(t *testing.T) {
	base, wt, fs := childWorktree(t, map[string]string{"keep.txt": "k\n", "edit.txt": "1\n2\n"})
	ctx := context.Background()
	put(t, wt.Path, "committed.txt", "c1\nc2\n")
	put(t, wt.Path, "edit.txt", "1\nTWO\n")
	run(t, wt.Path, "git", "add", "-A")
	run(t, wt.Path, "git", "commit", "-m", "work")
	put(t, wt.Path, "late.txt", "l\n")
	put(t, wt.Path, "-dash.txt", "d\n")
	put(t, wt.Path, "sp ace\nnl.txt", "x\n")
	put(t, wt.Path, "ünï.txt", "u\n")

	got, err := ReadChanges(ctx, fs, "read-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if got.Shared || got.Branch != "feat" || got.Base != base || got.Total != len(got.Files) {
		t.Fatalf("scope: %+v", got)
	}
	want := map[string]string{
		"committed.txt": "added", "edit.txt": "modified", "late.txt": "untracked",
		"-dash.txt": "untracked", "sp ace\nnl.txt": "untracked", "ünï.txt": "untracked",
	}
	if len(got.Files) != len(want) {
		t.Fatalf("files: %+v", got.Files)
	}
	for name, status := range want {
		c := changeOf(got.Files, name)
		if c == nil || c.Status != status {
			t.Fatalf("%q: %+v", name, c)
		}
	}
	if c := changeOf(got.Files, "committed.txt"); c.Added == nil || *c.Added != 2 {
		t.Fatalf("committed counts: %+v", c)
	}

	// The diff against the base shows the committed part; against HEAD it would not.
	d, err := ReadDiffPage(ctx, fs, "read-boundary", "committed.txt", DiffPage{Base: true})
	if err != nil || d.Status != "added" || d.Text != "@@ -0,0 +1,2 @@\n+c1\n+c2\n" {
		t.Fatalf("base diff: %+v %v", d, err)
	}
	if _, err := ReadDiff(ctx, fs, "read-boundary", "committed.txt"); err == nil || err.Error() != "not-changed" {
		t.Fatalf("head diff: %v", err)
	}
	d, err = ReadDiffPage(ctx, fs, "read-boundary", "ünï.txt", DiffPage{Base: true})
	if err != nil || d.Status != "untracked" || d.Text != "@@ -0,0 +1 @@\n+u\n" {
		t.Fatalf("unicode name: %+v %v", d, err)
	}
	d, err = ReadDiffPage(ctx, fs, "read-boundary", "-dash.txt", DiffPage{Base: true})
	if err != nil || d.Text != "@@ -0,0 +1 @@\n+d\n" {
		t.Fatalf("dash name: %+v %v", d, err)
	}
	d, err = ReadDiffPage(ctx, fs, "read-boundary", "sp ace\nnl.txt", DiffPage{Base: true})
	if err != nil || d.Text != "@@ -0,0 +1 @@\n+x\n" {
		t.Fatalf("newline name: %+v %v", d, err)
	}
}

func TestReadChangesOfASharedDirectoryAndOfNoRepository(t *testing.T) {
	dir, fs := inspectRepo(t, "", map[string]string{"a.txt": "a\n"})
	put(t, dir, "a.txt", "b\n")
	got, err := ReadChanges(context.Background(), fs, "read-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Shared || got.Base == "" || got.Total != 1 || got.Files[0].Path != "a.txt" {
		t.Fatalf("shared: %+v", got)
	}
	// Base on a shared directory is HEAD.
	if d, err := ReadDiffPage(context.Background(), fs, "read-boundary", "a.txt", DiffPage{Base: true}); err != nil || d.Status != "modified" {
		t.Fatalf("shared base diff: %+v %v", d, err)
	}
	plain := t.TempDir()
	put(t, plain, "a.txt", "x")
	if _, err := ReadChanges(context.Background(), workspaceReadFixture(t, plain), "read-boundary"); err == nil || err.Error() != "not-a-git-repository" {
		t.Fatalf("no repository: %v", err)
	}
}

func TestReadChangesStopsAtTheFileCap(t *testing.T) {
	_, wt, fs := childWorktree(t, map[string]string{"a.txt": "a\n"})
	for i := 0; i < MaxChangeFiles+25; i++ {
		put(t, wt.Path, fmt.Sprintf("gen/f%04d.txt", i), "x\n")
	}
	got, err := ReadChanges(context.Background(), fs, "read-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != MaxChangeFiles || got.Total != MaxChangeFiles+25 || got.Files[0].Path != "gen/f0000.txt" {
		t.Fatalf("files=%d total=%d first=%q", len(got.Files), got.Total, got.Files[0].Path)
	}
}

func TestReadChangesMarksWithoutABaseFallBackToHead(t *testing.T) {
	_, wt, fs := childWorktree(t, map[string]string{"a.txt": "a\n"})
	run(t, wt.Clone, "git", "config", "--local", "--unset", "branch.feat."+markerBase)
	put(t, wt.Path, "n.txt", "n\n")
	run(t, wt.Path, "git", "add", "-A")
	run(t, wt.Path, "git", "commit", "-m", "work")
	put(t, wt.Path, "m.txt", "m\n")
	got, err := ReadChanges(context.Background(), fs, "read-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if got.Shared || got.Branch != "feat" || got.Total != 1 || got.Files[0].Path != "m.txt" {
		t.Fatalf("got %+v", got)
	}
}

func TestReadChangesRefusesABaseThatIsNotACommit(t *testing.T) {
	_, wt, fs := childWorktree(t, map[string]string{"a.txt": "a\n"})
	run(t, wt.Clone, "git", "config", "--local", "branch.feat."+markerBase, "--output=/tmp/hoplet-never")
	put(t, wt.Path, "m.txt", "m\n")
	got, err := ReadChanges(context.Background(), fs, "read-boundary")
	if err != nil || got.Total != 1 {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := os.Stat("/tmp/hoplet-never"); err == nil {
		t.Fatal("a recorded base was taken as an option")
	}
}

func TestReadDiffPageWalksTheWholeDiff(t *testing.T) {
	line := strings.Repeat("z", 99) + "\n"
	n := 2000 // about 200 KB: several 32 KiB pages
	dir, fs := inspectRepo(t, "", map[string]string{"big.txt": "first\n"})
	put(t, dir, "big.txt", strings.Repeat(line, n))
	put(t, dir, "fresh.txt", strings.Repeat(line, n))
	for _, name := range []string{"big.txt", "fresh.txt"} {
		whole, err := ReadDiff(context.Background(), fs, "read-boundary", name)
		if err != nil || whole.Truncated {
			t.Fatalf("%s whole: %+v %v", name, whole.Truncated, err)
		}
		var text strings.Builder
		var cursor int64
		pages := 0
		for {
			d, err := ReadDiffPage(context.Background(), fs, "read-boundary", name, DiffPage{From: cursor, Limit: 32 << 10})
			if err != nil {
				t.Fatalf("%s at %d: %v", name, cursor, err)
			}
			if len(d.Text) > 32<<10 || d.Size != whole.Size {
				t.Fatalf("%s: len=%d size=%d", name, len(d.Text), d.Size)
			}
			text.WriteString(d.Text)
			pages++
			if d.Next == nil {
				if d.Truncated {
					t.Fatalf("%s: last page truncated", name)
				}
				break
			}
			if !d.Truncated || !strings.HasSuffix(d.Text, "\n") || *d.Next != cursor+int64(len(d.Text)) {
				t.Fatalf("%s: page %+v", name, d)
			}
			cursor = *d.Next
		}
		if pages < 4 || text.String() != whole.Text {
			t.Fatalf("%s: pages=%d joined=%d whole=%d", name, pages, text.Len(), len(whole.Text))
		}
	}
}

func TestReadDiffPageCursorsAndLongLines(t *testing.T) {
	dir, fs := inspectRepo(t, "", map[string]string{"a.txt": "a\n"})
	put(t, dir, "a.txt", "a\nb\nc\n")
	read := func(page DiffPage) (Diff, error) {
		return ReadDiffPage(context.Background(), fs, "read-boundary", "a.txt", page)
	}
	whole, _ := read(DiffPage{})
	// A cursor inside a line, past the end or negative is refused; one at the end is an empty last page.
	for _, from := range []int64{1, 5, whole.Size + 1, -1} {
		if _, err := read(DiffPage{From: from}); err == nil || err.Error() != "invalid-cursor" {
			t.Fatalf("from %d: %v", from, err)
		}
	}
	if d, err := read(DiffPage{From: whole.Size}); err != nil || d.Text != "" || d.Next != nil || d.Truncated {
		t.Fatalf("at the end: %+v %v", d, err)
	}

	// One line longer than the page is cut at a character, and the next page starts after it.
	long := strings.Repeat("é", 1500) + "\n"
	put(t, dir, "a.txt", "a\n"+long+"tail\n")
	first, err := read(DiffPage{Limit: 1024})
	if err != nil || !first.Truncated || first.Next == nil || len(first.Text) > 1024 {
		t.Fatalf("long: %+v %v", first, err)
	}
	if !strings.HasPrefix(first.Text, "@@ ") {
		t.Fatalf("long text: %q", first.Text[:10])
	}
	for _, r := range first.Text {
		if r == '�' {
			t.Fatalf("a character was cut in the middle")
		}
	}
	var rest strings.Builder
	cursor := *first.Next
	for pages := 0; ; pages++ {
		d, err := read(DiffPage{From: cursor, Limit: 1024})
		if err != nil || pages > 10 {
			t.Fatalf("after the long line: %v", err)
		}
		rest.WriteString(d.Text)
		if d.Next == nil {
			break
		}
		cursor = *d.Next
	}
	if !strings.Contains(rest.String(), "+tail\n") {
		t.Fatalf("the pages lost the line after the long one")
	}
}

func TestReadDiffPageOfBinaryAndOutsidePaths(t *testing.T) {
	dir, fs := inspectRepo(t, "", map[string]string{"pic.bin": "\x00\x01"})
	put(t, dir, "pic.bin", "\x00\x01\x02")
	d, err := ReadDiffPage(context.Background(), fs, "read-boundary", "pic.bin", DiffPage{Base: true})
	if err != nil || d.Kind != "binary" || d.Text != "" || d.Next != nil {
		t.Fatalf("binary: %+v %v", d, err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiffPage(context.Background(), fs, "read-boundary", "../x", DiffPage{Base: true}); err == nil || err.Error() != "path-escape" {
		t.Fatalf("escape: %v", err)
	}
	if _, err := ReadDiffPage(context.Background(), fs, "read-boundary", "link", DiffPage{Base: true}); err == nil {
		t.Fatalf("link: %v", err)
	}
}
