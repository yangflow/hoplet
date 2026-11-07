package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/yangflow/hoplet/internal/noise"
)

const (
	idA noise.HostID = "abcdefghijklmnopqrstuvwxyz"
	idB noise.HostID = "234567abcdefghijklmnopqrst"
)

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		string(idA): true, string(idB): true,
		"": false, "abc": false, strings.ToUpper(string(idA)): false,
		"abcdefghijklmnopqrstuvwxy1": false, string(idA) + "a": false,
	} {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestAllowDenyList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "relay")
	if ids, err := List(dir); err != nil || len(ids) != 0 {
		t.Fatalf("missing file: %v %v", ids, err)
	}
	for _, id := range []noise.HostID{idA, idB, idA} {
		if err := Allow(dir, id); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := List(dir)
	if err != nil || !slices.Equal(ids, []noise.HostID{idA, idB}) {
		t.Fatalf("list %v %v", ids, err)
	}
	fi, err := os.Stat(filepath.Join(dir, allowName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", fi.Mode().Perm(), err)
	}
	if err := Deny(dir, idA); err != nil {
		t.Fatal(err)
	}
	if ids, _ := List(dir); !slices.Equal(ids, []noise.HostID{idB}) {
		t.Fatalf("after deny %v", ids)
	}
	if err := Deny(dir, idA); err == nil || !strings.Contains(err.Error(), "not in the allowlist") {
		t.Fatalf("deny twice: %v", err)
	}
	if err := Allow(dir, "nope"); err == nil {
		t.Fatal("allowed a malformed ID")
	}
}

func TestListRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
		in   string
	}{
		"open mode": {string(idA) + "\n", 0o644, "accessible to other users"},
		"bad line":  {string(idA) + "\n\nnot-an-id\n", 0o600, "line 3"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, allowName)
		if err := os.WriteFile(path, []byte(tc.body), tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		if _, err := List(dir); err == nil || !strings.Contains(err.Error(), tc.in) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.in)
		}
		if err := Allow(dir, idB); err == nil {
			t.Errorf("%s: allow wrote over a bad file", name)
		}
	}
}

// TestMatchOwner checks what a root write does to the allowlist, as far as
// a test without root can: the file gets the directory's owner and group.
func TestMatchOwner(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := matchOwner(f, dir); err != nil {
		t.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, want := fi.Sys().(*syscall.Stat_t), di.Sys().(*syscall.Stat_t)
	if got.Uid != want.Uid || got.Gid != want.Gid {
		t.Fatalf("owner %d:%d, directory %d:%d", got.Uid, got.Gid, want.Uid, want.Gid)
	}
	if err := matchOwner(f, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing directory gave an owner")
	}
}
