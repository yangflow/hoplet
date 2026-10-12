package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type entry struct {
	h    tar.Header
	data []byte
	// zeros makes the entry's content that many zero bytes instead of data, without holding them in memory.
	zeros int64
}

func validMeta() SnapshotMeta {
	return SnapshotMeta{
		Version:    snapshotFormat,
		BaseCommit: strings.Repeat("a", 40),
		OriginURL:  "git@example.test:org/repo.git",
		CreatedAt:  time.Now().UnixMilli(),
		TreeHash:   strings.Repeat("b", 40),
	}
}

func metaEntry(m SnapshotMeta) entry {
	raw, _ := json.Marshal(m)
	return entry{h: tar.Header{Name: metaName, Typeflag: tar.TypeReg, Mode: 0o600}, data: raw}
}

func diffEntry(data string) entry {
	return entry{h: tar.Header{Name: diffName, Typeflag: tar.TypeReg, Mode: 0o600}, data: []byte(data)}
}

func fileEntry(name string, data []byte, mode int64) entry {
	return entry{h: tar.Header{Name: filePrefix + name, Typeflag: tar.TypeReg, Mode: mode}, data: data}
}

func linkEntry(name, target string) entry {
	return entry{h: tar.Header{Name: filePrefix + name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777}}
}

func base(extra ...entry) []entry {
	return append([]entry{metaEntry(validMeta()), diffEntry("")}, extra...)
}

// writeTarGz writes the entries as a snapshot archive.
func writeTarGz(t *testing.T, path string, entries []entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.h
		h.Size = int64(len(e.data))
		if e.zeros > 0 {
			h.Size = e.zeros
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if e.zeros > 0 {
			if _, err := io.CopyN(tw, zeroReader{}, e.zeros); err != nil {
				t.Fatal(err)
			}
		} else if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func archiveOf(t *testing.T, entries []entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	writeTarGz(t, path, entries)
	return path
}

func TestBudgetReaderBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		size, limit int64
		wantErr     bool
	}{{9, 10, false}, {10, 10, false}, {11, 10, true}, {0, 0, false}, {1, 0, true}} {
		r := &budgetReader{r: bytes.NewReader(make([]byte, tc.size)), left: tc.limit}
		_, err := io.Copy(io.Discard, r)
		if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, ErrTooLarge)) {
			t.Fatalf("size %d limit %d: %v", tc.size, tc.limit, err)
		}
	}
}

func TestUncompressedLimitIsExact(t *testing.T) {
	t.Parallel()
	// meta (2 blocks) + empty diff (1) + bundle header (1) + the 2-block end marker.
	const fixed = 6 * 512
	accepted := archiveOf(t, base(entry{h: tar.Header{Name: bundleName, Typeflag: tar.TypeReg, Mode: 0o600}, zeros: MaxSnapshotUncompressed - fixed}))
	if err := scanArchive(accepted, func(entryKind, string, *tar.Header, io.Reader) error { return nil }); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	over := archiveOf(t, base(entry{h: tar.Header{Name: bundleName, Typeflag: tar.TypeReg, Mode: 0o600}, zeros: MaxSnapshotUncompressed - fixed + 512}))
	if err := scanArchive(over, func(entryKind, string, *tar.Header, io.Reader) error { return nil }); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one block over the limit: %v", err)
	}
}

func TestGzipBombIsRefusedWithBoundedMemory(t *testing.T) {
	t.Parallel()
	bomb := archiveOf(t, base(entry{h: tar.Header{Name: bundleName, Typeflag: tar.TypeReg, Mode: 0o600}, zeros: 3 * MaxSnapshotUncompressed / 2}))
	if info, _ := os.Stat(bomb); info.Size() > 8<<20 {
		t.Fatalf("test archive is not a bomb: %d bytes", info.Size())
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := OpenArchive(bomb, t.TempDir())
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	// The bomb expands to 384 MiB; refusing it must not allocate anything near that.
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
		t.Fatalf("allocated %d MiB while refusing", grown>>20)
	}
}

func TestCompressedLimitIsExact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := filepath.Join(dir, "at")
	over := filepath.Join(dir, "over")
	for path, size := range map[string]int64{at: MaxSnapshotCompressed, over: MaxSnapshotCompressed + 1} {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	// Both are zeros, not gzip: the one at the limit is read and found corrupt, the other is refused unread.
	if _, err := OpenArchive(at, t.TempDir()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("at the limit: %v", err)
	}
	if _, err := OpenArchive(over, t.TempDir()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestDeclaredSizeIsNotTrusted(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lying.tar.gz")
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	raw, _ := json.Marshal(validMeta())
	_ = tw.WriteHeader(&tar.Header{Name: metaName, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(raw))})
	_, _ = tw.Write(raw)
	_ = tw.WriteHeader(&tar.Header{Name: diffName, Typeflag: tar.TypeReg, Mode: 0o600, Size: 1 << 40})
	_, _ = tw.Write([]byte("short"))
	_ = gz.Close() // no tar end marker: the entry ends 1 TiB early
	f.Close()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := OpenArchive(path, t.TempDir())
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if after.TotalAlloc-before.TotalAlloc > 32<<20 {
		t.Fatal("allocated by the declared size")
	}
}

func TestDamagedArchivesAreRefused(t *testing.T) {
	t.Parallel()
	good := archiveOf(t, base(fileEntry("a.txt", bytes.Repeat([]byte("hello "), 5000), 0o644), fileEntry("b.bin", []byte{1, 2, 3}, 0o755)))
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenArchive(good, t.TempDir()); err != nil {
		t.Fatalf("the intact archive: %v", err)
	}
	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 0xff
	garbage := append(bytes.Clone(raw), []byte("trailing garbage")...)
	var second bytes.Buffer
	second.Write(raw)
	second.Write(raw) // a second gzip member
	cases := map[string][]byte{
		"empty":            {},
		"not gzip":         []byte("plain text, not an archive"),
		"truncated":        raw[:len(raw)-20],
		"flipped byte":     flipped,
		"trailing garbage": garbage,
		"second member":    second.Bytes(),
		"header only":      raw[:10],
	}
	for name, data := range cases {
		path := filepath.Join(t.TempDir(), "x.tar.gz")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenArchive(path, t.TempDir()); err == nil || !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMalformedStructureIsRefused(t *testing.T) {
	t.Parallel()
	badMeta := func(mutate func(*SnapshotMeta)) []entry {
		m := validMeta()
		mutate(&m)
		return []entry{metaEntry(m), diffEntry("")}
	}
	cases := map[string][]entry{
		"no entries":               {},
		"meta not first":           {diffEntry(""), metaEntry(validMeta())},
		"no meta":                  {diffEntry("")},
		"duplicate meta":           {metaEntry(validMeta()), metaEntry(validMeta())},
		"duplicate diff":           base(diffEntry("")),
		"duplicate file":           base(fileEntry("a", nil, 0o644), fileEntry("a", nil, 0o644)),
		"unknown entry":            base(entry{h: tar.Header{Name: "extra.txt", Typeflag: tar.TypeReg}}),
		"meta as link":             {{h: tar.Header{Name: metaName, Typeflag: tar.TypeSymlink, Linkname: "x"}}},
		"directory entry":          base(entry{h: tar.Header{Name: filePrefix + "d/", Typeflag: tar.TypeDir, Mode: 0o755}}),
		"hard link":                base(entry{h: tar.Header{Name: filePrefix + "h", Typeflag: tar.TypeLink, Linkname: "x"}}),
		"device":                   base(entry{h: tar.Header{Name: filePrefix + "dev", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}}),
		"fifo":                     base(entry{h: tar.Header{Name: filePrefix + "fifo", Typeflag: tar.TypeFifo}}),
		"bad object id":            badMeta(func(m *SnapshotMeta) { m.BaseCommit = "HEAD" }),
		"bad tree id":              badMeta(func(m *SnapshotMeta) { m.TreeHash = "xyz" }),
		"no time":                  badMeta(func(m *SnapshotMeta) { m.CreatedAt = 0 }),
		"subdir escapes":           badMeta(func(m *SnapshotMeta) { m.Subdir = "../x" }),
		"origin with newline":      badMeta(func(m *SnapshotMeta) { m.OriginURL = "a\nb" }),
		"unsupported format":       badMeta(func(m *SnapshotMeta) { m.Version = 2 }),
		"meta is not json":         {{h: tar.Header{Name: metaName, Typeflag: tar.TypeReg}, data: []byte("{")}},
		"meta has unknown field":   {{h: tar.Header{Name: metaName, Typeflag: tar.TypeReg}, data: []byte(`{"version":1,"x":1}`)}},
		"entry below a file":       base(fileEntry("a", nil, 0o644), fileEntry("a/b", nil, 0o644)),
		"file replaces a dir":      base(fileEntry("a/b", nil, 0o644), fileEntry("a", nil, 0o644)),
		"entry below a link":       base(linkEntry("l", "dir"), fileEntry("l/x", nil, 0o644)),
		"entry below a nested lnk": base(linkEntry("a/l", "."), fileEntry("a/l/x", nil, 0o644)),
	}
	for name, entries := range cases {
		path := archiveOf(t, entries)
		if _, err := OpenArchive(path, t.TempDir()); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPathTraversalAndGitMetadataAreRefused(t *testing.T) {
	t.Parallel()
	names := []string{
		"../evil", "a/../../evil", "a/../b", "/abs/path", ".", "a//b", "./a", "a/./b", "..",
		".git/config", ".git/hooks/pre-commit", "a/.git/config", ".GIT/config", ".Git", "sub/.git",
	}
	for _, name := range names {
		path := archiveOf(t, base(entry{h: tar.Header{Name: filePrefix + name, Typeflag: tar.TypeReg, Mode: 0o644}, data: []byte("x")}))
		if _, err := OpenArchive(path, t.TempDir()); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%q: err = %v", name, err)
		}
	}
	// Names outside untracked/ that try to reach the worktree or the stage.
	for _, name := range []string{"../snapshot.json", "untracked", "/untracked/a", "untracked/../tracked.diff", "tracked.diff/../x", "x/untracked/a"} {
		path := archiveOf(t, base(entry{h: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}, data: []byte("x")}))
		if _, err := OpenArchive(path, t.TempDir()); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%q: err = %v", name, err)
		}
	}
}

func TestSymlinkTargetsMustStayInside(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"", "/etc/passwd", "/", "../../outside", "a/../../../outside", "../..", ".git", "x/.git/hooks"} {
		path := archiveOf(t, base(linkEntry("d/l", target)))
		if _, err := OpenArchive(path, t.TempDir()); !errors.Is(err, ErrCorrupt) {
			t.Errorf("link to %q: err = %v", target, err)
		}
	}
	for _, target := range []string{"x", "../x", "..", ".", "../d", "a/../x"} {
		// d/l -> .. is the root itself; every one of these resolves inside it.
		want := true
		path := archiveOf(t, base(linkEntry("d/l", target)))
		_, err := OpenArchive(path, t.TempDir())
		if (err == nil) != want {
			t.Errorf("link to %q: err = %v", target, err)
		}
	}
}

func TestNothingIsWrittenForARefusedArchive(t *testing.T) {
	t.Parallel()
	// A valid prefix followed by a bad entry: inspection fails before anything reaches the worktree.
	path := archiveOf(t, base(
		fileEntry("good.txt", []byte("good"), 0o644),
		entry{h: tar.Header{Name: filePrefix + "../bad", Typeflag: tar.TypeReg}, data: []byte("bad")},
	))
	worktree := t.TempDir()
	stage := t.TempDir()
	if _, err := OpenArchive(path, stage); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(worktree); len(entries) != 0 {
		t.Fatalf("worktree touched: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(worktree), "bad")); !os.IsNotExist(err) {
		t.Fatal("a file escaped")
	}
}

func extractInto(t *testing.T, a *Archive, dir string) error {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	return a.ExtractUntracked(root)
}

func TestExtractionKeepsContentModesAndLinks(t *testing.T) {
	t.Parallel()
	binary := append([]byte{0, 0xff, 0xfe, 0}, bytes.Repeat([]byte{7, 0, 9}, 1000)...)
	path := archiveOf(t, base(
		fileEntry("exec.sh", []byte("#!/bin/sh\n"), 0o755),
		fileEntry("private.key", []byte("k"), 0o600),
		fileEntry("readonly.txt", []byte("r"), 0o444),
		fileEntry("deep/er/blob.bin", binary, 0o644),
		fileEntry("empty", nil, 0o644),
		linkEntry("deep/link", "er/blob.bin"),
		linkEntry("up", "deep"),
	))
	a, err := OpenArchive(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extractInto(t, a, dir); err != nil {
		t.Fatal(err)
	}
	for name, perm := range map[string]os.FileMode{"exec.sh": 0o755, "private.key": 0o600, "readonly.txt": 0o444, "deep/er/blob.bin": 0o644, "empty": 0o644} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != perm {
			t.Errorf("%s: %v %v", name, info, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "deep/er/blob.bin")); !bytes.Equal(got, binary) {
		t.Error("binary content changed")
	}
	if link, err := os.Readlink(filepath.Join(dir, "deep/link")); err != nil || link != "er/blob.bin" {
		t.Errorf("link = %q %v", link, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "up/er/blob.bin")); !bytes.Equal(got, binary) {
		t.Error("reading through an inside link failed")
	}
}

func TestExtractionCannotLeaveTheRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "worktree")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The worktree already holds a link that points out of it; the archive writes through that name.
	if err := os.Symlink("../outside", filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	path := archiveOf(t, base(fileEntry("escape/pwn.txt", []byte("pwn"), 0o644)))
	a, err := OpenArchive(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := extractInto(t, a, dir); err == nil {
		t.Fatal("extraction through an escaping link succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was written outside the root: %v", entries)
	}
}

func TestExtractionDoesNotOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "l"} {
		path := archiveOf(t, base(fileEntry(name, []byte("theirs"), 0o644)))
		a, err := OpenArchive(path, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := extractInto(t, a, dir); err == nil {
			t.Fatalf("%s: overwrote", name)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "mine" {
		t.Fatalf("content = %q", got)
	}
}

func TestLocalPath(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"a", "a/b", "dir/.gitignore", "dir/.github/x", "名前/ファイル", "a b/c d", "a\\b"} {
		if !localPath(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "/a", "a/", "a//b", "a/../b", "../a", "a/./b", "a\x00b", ".git", "x/.GIT/y", ".Git"} {
		if localPath(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
