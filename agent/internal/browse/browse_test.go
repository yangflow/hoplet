package browse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdirs(t *testing.T, base string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(base, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func names(l Listing) []string {
	out := []string{}
	for _, e := range l.Entries {
		out = append(out, e.Name)
	}
	return out
}

func TestListRootSortedDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "beta", "Alpha", "gamma", ".hidden")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "~", root} {
		l, err := List(root, path)
		if err != nil {
			t.Fatalf("%q: %v", path, err)
		}
		if want := []string{"Alpha", "beta", "gamma"}; !reflect.DeepEqual(names(l), want) {
			t.Fatalf("%q: entries %v, want %v", path, names(l), want)
		}
		if l.Parent != nil || l.Path != resolved(t, root) || l.Root != resolved(t, root) || l.Truncated {
			t.Fatalf("%q: %+v", path, l)
		}
	}
}

func TestListSubdirectoryHasParentAndGitFlag(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "work/repo/.git", "work/plain", "work/worktree")
	if err := os.WriteFile(filepath.Join(root, "work/worktree/.git"), []byte("gitdir: x"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := List(root, filepath.Join(root, "work"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range l.Entries {
		got[e.Name] = e.Git
	}
	if want := map[string]bool{"plain": false, "repo": true, "worktree": true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if l.Parent == nil || *l.Parent != resolved(t, root) {
		t.Fatalf("parent %v", l.Parent)
	}
}

func TestListBoundsEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxEntries+5; i++ {
		mkdirs(t, root, fmt.Sprintf("d%04d", i))
	}
	l, err := List(root, "~")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != MaxEntries || !l.Truncated {
		t.Fatalf("%d entries, truncated=%v", len(l.Entries), l.Truncated)
	}
	exact := t.TempDir()
	for i := 0; i < MaxEntries; i++ {
		mkdirs(t, exact, fmt.Sprintf("d%04d", i))
	}
	if l, _ := List(exact, "~"); l.Truncated || len(l.Entries) != MaxEntries {
		t.Fatalf("exactly %d entries must not be truncated", MaxEntries)
	}
}

func TestListRefusals(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	mkdirs(t, base, "root/inside", "outside")
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want error
	}{
		{"relative", "inside", ErrRelative},
		{"dot dot", "../outside", ErrRelative},
		{"home subpath", "~/inside", ErrRelative},
		{"sibling", filepath.Join(base, "outside"), ErrOutside},
		{"parent of root", base, ErrOutside},
		{"dot dot out", filepath.Join(root, "..", "outside"), ErrOutside},
		{"symlink out", filepath.Join(root, "escape"), ErrOutside},
		{"file", file, ErrNotDir},
		{"missing", filepath.Join(root, "nope"), ErrNotDir},
	}
	for _, tc := range cases {
		if _, err := List(root, tc.path); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	l, err := List(root, "~")
	if err != nil {
		t.Fatal(err)
	}
	// The link that leaves the root is not listed; the one that stays inside is.
	if want := []string{"alias", "inside"}; !reflect.DeepEqual(names(l), want) {
		t.Fatalf("entries %v, want %v", names(l), want)
	}
	if l, err := List(root, filepath.Join(root, "alias")); err != nil || l.Path != resolved(t, filepath.Join(root, "inside")) {
		t.Fatalf("inside link: %+v %v", l, err)
	}
}

func TestListUnreadableDirectoryIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mkdirs(t, root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := List(root, locked); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("got %v, want %v", err, ErrUnreadable)
	}
}
