package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureBytes is n deterministic bytes that git takes for binary content.
func fixtureBytes(n int, seed uint32) []byte {
	b := make([]byte, n)
	for i := range b {
		seed = seed*1664525 + 1013904223
		b[i] = byte(seed >> 24)
	}
	b[0] = 0
	return b
}

// TestSnapshotFixtureForTheCore makes the snapshot that core/test/snapshot-read.test.ts reads: a session in
// the subdirectory app/ with modified, added, deleted, binary (literal and delta) and untracked paths, diffs
// past the 256 KiB cap, a name with a space and a non-ASCII letter, a name past 100 bytes, and changes
// outside app/. With HOPLET_SNAPSHOT_FIXTURE set it copies the archive there and its metadata beside it as
// JSON.
func TestSnapshotFixtureForTheCore(t *testing.T) {
	dir := t.TempDir()
	output(t, dir, "git", "init", "-q", "-b", "main")
	output(t, dir, "git", "config", "user.email", "t@example.test")
	output(t, dir, "git", "config", "user.name", "t")
	long := "app/" + strings.Repeat("d", 60) + "/" + strings.Repeat("e", 60)
	big := fixtureBytes(40000, 7)
	committed := map[string][]byte{
		"app/edit.txt":      []byte("one\ntwo\nthree\n"),
		"app/gone.txt":      []byte("a\nb\n"),
		"app/pic.bin":       fixtureBytes(64, 1),
		"app/big.bin":       big,
		"app/del.bin":       []byte("\x00abc"),
		"app/same.txt":      []byte("s\n"),
		"app/café x.txt":    []byte("old\n"),
		long + "/deep.txt":  []byte("deep\n"),
		"other.txt":         []byte("o\n"),
		"app/sub/keep.txt":  []byte("k\n"),
		"app/sub/lines.txt": []byte(strings.Repeat("line\n", 10)),
		"app/long.txt":      []byte(strings.Repeat(strings.Repeat("a", 99)+"\n", 3000)),
	}
	for name, data := range committed {
		write(t, dir, filepath.FromSlash(name), data, 0o644)
	}
	output(t, dir, "git", "add", "-A")
	output(t, dir, "git", "commit", "-q", "-m", "base")

	changed := append([]byte(nil), big...)
	for i := 20000; i < 20010; i++ {
		changed[i] ^= 0xff
	}
	changed = append(changed, "tail"...)
	for name, data := range map[string][]byte{
		"app/edit.txt":           []byte("one\n2\nthree\n"),
		"app/pic.bin":            fixtureBytes(100, 2),
		"app/big.bin":            changed,
		"app/café x.txt":         []byte("new\n"),
		"app/added.txt":          []byte("1\n2\n"),
		"app/new.txt":            []byte("x\ny"),
		"app/new.bin":            []byte("\x00\x01"),
		long + "/fresh.txt":      []byte("f\n"),
		"other.txt":              []byte("changed\n"),
		"outside-new.txt":        []byte("n\n"),
		"app/sub/lines.txt":      []byte(strings.Repeat("line\n", 4) + "LINE\n" + strings.Repeat("line\n", 5)),
		"app/empty-untracked.md": nil,
		"app/long.txt":           []byte(strings.Repeat(strings.Repeat("b", 99)+"\n", 3000)),
		"app/huge.txt":           []byte(strings.Repeat(strings.Repeat("c", 99)+"\n", 3000)),
	} {
		write(t, dir, filepath.FromSlash(name), data, 0o644)
	}
	for _, name := range []string{"app/gone.txt", "app/del.bin"} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	output(t, dir, "git", "add", "app/added.txt")

	snap, err := CreateSnapshot(context.Background(), filepath.Join(dir, "app"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Subdir != "app" {
		t.Fatalf("subdir %q", snap.Meta.Subdir)
	}
	dest := os.Getenv("HOPLET_SNAPSHOT_FIXTURE")
	if dest == "" {
		return
	}
	raw, err := os.ReadFile(snap.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	meta, err := json.Marshal(snap.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".json", meta, 0o600); err != nil {
		t.Fatal(err)
	}
}
