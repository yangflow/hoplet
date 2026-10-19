package workspace

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
)

const testSession = "11111111-2222-4333-8444-555555555566"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// chunks feeds data into a transfer part in pieces of `piece` bytes, gzip-compressed when asked.
func feed(t *testing.T, tr *Transfer, part string, data []byte, piece int, gz bool) {
	t.Helper()
	for seq, off := 0, 0; off < len(data); seq, off = seq+1, off+piece {
		end := min(off+piece, len(data))
		encoding, text := "base64", base64.StdEncoding.EncodeToString(data[off:end])
		if gz {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			zw.Write(data[off:end])
			zw.Close()
			encoding, text = "gzip+base64", base64.StdEncoding.EncodeToString(buf.Bytes())
		}
		if err := tr.Write(part, seq, end == len(data), encoding, text); err != nil {
			t.Fatalf("%s chunk %d: %v", part, seq, err)
		}
	}
}

func header(snapshot, session []byte) TransferHeader {
	return TransferHeader{
		HandoffID: "h-1", SessionID: testSession, Provider: "claude",
		SnapshotBytes: int64(len(snapshot)), SnapshotSHA256: sum(snapshot),
		SessionBytes: int64(len(session)), SessionSHA256: sum(session),
	}
}

type fakeProjects struct {
	mu      sync.Mutex
	watched map[string][]string
	created map[string]bool
	failOn  string
}

func newFakeProjects() *fakeProjects {
	return &fakeProjects{watched: map[string][]string{}, created: map[string]bool{}}
}

func (f *fakeProjects) Watch(path, sessionID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "watch" {
		return false, errors.New("projects.json is read-only")
	}
	_, had := f.watched[path]
	f.watched[path] = append(f.watched[path], sessionID)
	return !had, nil
}

func (f *fakeProjects) Unwatch(path, sessionID string, removePath bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.watched, path)
	return nil
}

func (f *fakeProjects) has(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.watched[path]) > 0
}

func (f *fakeProjects) any() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.watched) > 0
}

type target struct {
	fixture
	home, store string
	projects    *fakeProjects
	inst        *Installer
	session     []byte
	snapshot    []byte
	snap        *Snapshot
}

// newTarget makes a source with a messy tree, snapshots it, and sets up the target machine's state.
func newTarget(t *testing.T) *target {
	t.Helper()
	f := newFixture(t)
	f.dirty(t)
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(snap.Path)
	if err != nil {
		t.Fatal(err)
	}
	tg := &target{
		fixture: f, home: t.TempDir(), store: t.TempDir(), projects: newFakeProjects(), snap: snap, snapshot: raw,
		session: []byte(`{"type":"user","cwd":"/elsewhere","message":{"role":"user","content":"字"}}` + "\n" + strings.Repeat(`{"n":1}`+"\n", 5000)),
	}
	tg.inst = &Installer{
		Home: tg.home, Stores: claudeOnly(tg.store), Projects: tg.projects,
		Watched: func() []string { return []string{filepath.Dir(f.source), f.target} },
	}
	return tg
}

func (tg *target) transfer(t *testing.T, mutate func(*TransferHeader)) *Transfer {
	t.Helper()
	h := header(tg.snapshot, tg.session)
	if mutate != nil {
		mutate(&h)
	}
	tr, err := BeginTransfer(tg.home, h)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, tr, PartSnapshot, tg.snapshot, ChunkBytes/4, false)
	feed(t, tr, PartSession, tg.session, 16<<10, true)
	if !tr.Complete() {
		t.Fatal("transfer incomplete")
	}
	return tr
}

func (tg *target) worktree() string { return tg.target + "-11111111" }

func TestInstallPutsTheSessionIntoANewWorktreeOfTheClone(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	ctx := context.Background()
	inst, err := tg.inst.Install(ctx, tg.transfer(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	// Records hold real paths; the fixture lives under a temp dir that may be a symlink.
	realWorktree, _ := filepath.EvalSymlinks(tg.worktree())
	realClone, _ := filepath.EvalSymlinks(tg.target)
	if inst.Worktree != realWorktree || inst.Branch != "hoplet/11111111" || inst.Clone != realClone {
		t.Fatalf("installed = %+v", inst)
	}
	sameTrees(t, tg.source, tg.worktree())

	// The session file: byte for byte, in the store directory of the worktree's real path.
	want := filepath.Join(tg.store, claudeprovider.DirName(tg.worktree()), testSession+".jsonl")
	got, err := os.ReadFile(want)
	if err != nil || !bytes.Equal(got, tg.session) {
		t.Fatalf("session file at %s: %v", want, err)
	}
	if inst.SessionFile != want || !tg.projects.has(inst.Cwd) {
		t.Fatalf("record = %+v", inst)
	}
	if entries, _ := os.ReadDir(filepath.Join(tg.home, "handoff")); len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Fatalf("staging was not removed: %v", entries)
	}

	v, err := tg.inst.Verify(ctx, "h-1")
	if err != nil {
		t.Fatal(err)
	}
	if v.SessionBytes != int64(len(tg.session)) || v.SessionSHA256 != sum(tg.session) || v.TreeHash != tg.snap.Meta.TreeHash {
		t.Fatalf("verification = %+v", v)
	}

	if err := tg.inst.Finish("h-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tg.inst.Find("h-1"); ok {
		t.Fatal("the record outlived the commit")
	}
	if _, err := os.Stat(tg.worktree()); err != nil {
		t.Fatal("a committed installation lost its worktree")
	}
}

func TestInstallSubdirectoryRunsInThatDirectory(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	snap, err := CreateSnapshot(context.Background(), filepath.Join(tg.source, "src"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tg.snap = snap
	tg.snapshot, _ = os.ReadFile(snap.Path)
	inst, err := tg.inst.Install(context.Background(), tg.transfer(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(inst.Cwd, filepath.Join("-11111111", "src")) {
		t.Fatalf("cwd = %s", inst.Cwd)
	}
	if _, err := os.Stat(filepath.Join(tg.store, claudeprovider.DirName(inst.Cwd), testSession+".jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestAbortRemovesEverythingTheInstallationAdded(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	ctx := context.Background()
	if _, err := tg.inst.Install(ctx, tg.transfer(t, nil)); err != nil {
		t.Fatal(err)
	}
	if err := tg.inst.Abort(ctx, "h-1"); err != nil {
		t.Fatal(err)
	}
	assertUntouched(t, tg)
	// Repeating is harmless.
	if err := tg.inst.Abort(ctx, "h-1"); err != nil {
		t.Fatal(err)
	}
}

func assertUntouched(t *testing.T, tg *target) {
	t.Helper()
	if _, err := os.Lstat(tg.worktree()); !os.IsNotExist(err) {
		t.Fatal("the worktree stayed")
	}
	if got := output(t, tg.target, "git", "branch", "--list", "hoplet/*"); got != "" {
		t.Fatalf("the branch stayed: %s", got)
	}
	if got := output(t, tg.target, "git", "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Fatalf("worktrees: %s", got)
	}
	if tg.projects.any() {
		t.Fatal("the project entry stayed")
	}
	var left []string
	filepath.WalkDir(tg.store, func(p string, d os.DirEntry, err error) error {
		if err == nil && p != tg.store {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Fatalf("Claude store not empty: %v", left)
	}
	if recs := tg.inst.Records(); len(recs) != 0 {
		t.Fatalf("records: %+v", recs)
	}
}

func TestInstallFailureRemovesWhatWasInstalled(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		prepare func(*target)
		want    string
	}{
		"projects.json cannot be written": {func(tg *target) { tg.projects.failOn = "watch" }, "read-only"},
		"the session file exists": {func(tg *target) {
			dir := filepath.Join(tg.store, claudeprovider.DirName(tg.worktree()))
			// The directory name depends on the worktree path, which does not exist yet: the realpath of
			// a not-yet-existing path is the path itself, so create the same name the install will use.
			real, _ := filepath.EvalSymlinks(filepath.Dir(tg.worktree()))
			dir = filepath.Join(tg.store, claudeprovider.DirName(filepath.Join(real, filepath.Base(tg.worktree()))))
			os.MkdirAll(dir, 0o700)
			os.WriteFile(filepath.Join(dir, testSession+".jsonl"), []byte("theirs\n"), 0o600)
		}, "exist"},
	} {
		t.Run(name, func(t *testing.T) {
			tg := newTarget(t)
			tc.prepare(tg)
			_, err := tg.inst.Install(context.Background(), tg.transfer(t, nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if _, statErr := os.Lstat(tg.worktree()); !os.IsNotExist(statErr) {
				t.Fatal("the worktree stayed")
			}
			if got := output(t, tg.target, "git", "branch", "--list", "hoplet/*"); got != "" {
				t.Fatalf("the branch stayed: %s", got)
			}
			if tg.projects.any() {
				t.Fatal("the project entry stayed")
			}
			if recs := tg.inst.Records(); len(recs) != 0 {
				t.Fatalf("records: %+v", recs)
			}
			if name == "the session file exists" {
				dir := filepath.Join(tg.store)
				var found []byte
				filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
					if err == nil && !d.IsDir() {
						found, _ = os.ReadFile(p)
					}
					return nil
				})
				if string(found) != "theirs\n" {
					t.Fatalf("a session file that was not ours was removed: %q", found)
				}
			}
		})
	}
}

func TestInstallRefusesWithoutAnyChangeWhenTheCloneIsMissing(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	tg.inst.Watched = func() []string { return nil }
	_, err := tg.inst.Install(context.Background(), tg.transfer(t, nil))
	if !errors.Is(err, ErrNoClone) {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, tg)

	// A target path names the clone instead.
	hdr := func(h *TransferHeader) { h.TargetPath = tg.target }
	if _, err := tg.inst.Install(context.Background(), tg.transfer(t, hdr)); err != nil {
		t.Fatalf("with a target path: %v", err)
	}
}

func TestInstallRefusesACorruptSnapshotBeforeTouchingAnything(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	bad := bytes.Clone(tg.snapshot)
	bad[len(bad)/2] ^= 0xff
	tg.snapshot = bad
	_, err := tg.inst.Install(context.Background(), tg.transfer(t, nil))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, tg)
}

func TestRecoverRemovesAnInterruptedInstallationAndKeepsAFinishedOne(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	ctx := context.Background()
	// A finished installation waits for the core's word.
	if _, err := tg.inst.Install(ctx, tg.transfer(t, nil)); err != nil {
		t.Fatal(err)
	}
	pending, err := (&Installer{Home: tg.home, Stores: claudeOnly(tg.store), Projects: tg.projects, Watched: tg.inst.Watched}).Recover(ctx)
	if err != nil || len(pending) != 1 || pending[0].HandoffID != "h-1" || pending[0].State != StateInstalled {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if _, err := os.Stat(tg.worktree()); err != nil {
		t.Fatal("a finished installation was removed at startup")
	}

	// One that stopped in the middle is removed: worktree, branch, session file, project entry.
	rec, _ := tg.inst.Find("h-1")
	rec.State = StateInstalling
	if err := tg.inst.save(rec); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(tg.home, "handoff", "transfer-h-1"), 0o700)
	pending, err = tg.inst.Recover(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	assertUntouched(t, tg)
	if _, err := os.Stat(filepath.Join(tg.home, "handoff", "transfer-h-1")); !os.IsNotExist(err) {
		t.Fatal("transfer staging stayed")
	}
}

func TestRecoverLeavesADirectoryItDidNotMake(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	mine := tg.worktree()
	os.MkdirAll(mine, 0o755)
	os.WriteFile(filepath.Join(mine, "theirs.txt"), []byte("keep"), 0o644)
	rec := Record{HandoffID: "h-2", SessionID: testSession, State: StateInstalling, Clone: tg.target, Worktree: mine, Branch: "hoplet/11111111"}
	if err := tg.inst.save(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.inst.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(mine, "theirs.txt")); string(got) != "keep" {
		t.Fatal("a directory that is not a registered worktree was removed")
	}
	if recs := tg.inst.Records(); len(recs) != 0 {
		t.Fatalf("the record stayed: %+v", recs)
	}
}

func TestRecordsWithBadIdentifiersAreIgnored(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "handoff"), 0o700)
	os.WriteFile(filepath.Join(home, "handoff", "x.json"), []byte(`{"handoffId":"../x","sessionId":"s"}`), 0o600)
	inst := &Installer{Home: home, Projects: newFakeProjects()}
	if recs := inst.Records(); len(recs) != 0 {
		t.Fatalf("records = %+v", recs)
	}
}

func TestTransferBoundsEveryChunkBeforeAndWhileDecoding(t *testing.T) {
	t.Parallel()
	snapshot := bytes.Repeat([]byte("s"), 3000)
	session := bytes.Repeat([]byte("j"), 5000)
	newT := func(t *testing.T) *Transfer {
		tr, err := BeginTransfer(t.TempDir(), header(snapshot, session))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(tr.Close)
		return tr
	}
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	gz := func(b []byte) string {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write(b)
		zw.Close()
		return b64(buf.Bytes())
	}

	t.Run("a chunk over 1 MiB", func(t *testing.T) {
		big := bytes.Repeat([]byte("x"), ChunkBytes+1)
		if err := newT(t).Write(PartSession, 0, false, "base64", b64(big)); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("a gzip chunk that expands past 1 MiB", func(t *testing.T) {
		bomb := gz(make([]byte, 64<<20))
		if len(bomb) > maxEncodedChunk {
			t.Skip("compressed bomb larger than a chunk")
		}
		if err := newT(t).Write(PartSession, 0, false, "gzip+base64", bomb); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("text longer than a chunk can be", func(t *testing.T) {
		if err := newT(t).Write(PartSession, 0, false, "base64", strings.Repeat("A", maxEncodedChunk+4)); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("bad encodings", func(t *testing.T) {
		tr := newT(t)
		for _, c := range [][2]string{{"base64", ""}, {"base64", "***"}, {"gzip+base64", b64([]byte("not gzip"))}, {"rot13", b64([]byte("x"))}} {
			if err := tr.Write(PartSession, 0, false, c[0], c[1]); err == nil {
				t.Fatalf("%v accepted", c)
			}
		}
	})
	t.Run("order", func(t *testing.T) {
		tr := newT(t)
		if err := tr.Write(PartSession, 1, false, "base64", b64(session[:10])); err == nil {
			t.Fatal("seq 1 first")
		}
		if err := tr.Write(PartSession, 0, false, "base64", b64(session[:10])); err != nil {
			t.Fatal(err)
		}
		if err := tr.Write(PartSession, 0, false, "base64", b64(session[:10])); err == nil {
			t.Fatal("repeated seq")
		}
		if err := tr.Write("other", 0, false, "base64", b64(session[:10])); err == nil {
			t.Fatal("unknown part")
		}
	})
	t.Run("more than announced", func(t *testing.T) {
		if err := newT(t).Write(PartSession, 0, true, "base64", b64(append(session, 'x'))); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("shorter than announced", func(t *testing.T) {
		if err := newT(t).Write(PartSession, 0, true, "base64", b64(session[:100])); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("the last chunk missing", func(t *testing.T) {
		if err := newT(t).Write(PartSession, 0, false, "base64", b64(session)); err == nil {
			t.Fatal("a part that is full but not last was accepted")
		}
	})
	t.Run("checksum", func(t *testing.T) {
		wrong := bytes.Repeat([]byte("k"), len(session))
		if err := newT(t).Write(PartSession, 0, true, "base64", b64(wrong)); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("a finished part takes no more", func(t *testing.T) {
		tr := newT(t)
		if err := tr.Write(PartSession, 0, true, "base64", b64(session)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Write(PartSession, 1, true, "base64", b64(session)); err == nil {
			t.Fatal("accepted")
		}
	})
}

func TestTransferHeaderLimits(t *testing.T) {
	t.Parallel()
	ok := header([]byte("a"), []byte("b"))
	for name, mutate := range map[string]func(*TransferHeader){
		"snapshot over 64 MiB":    func(h *TransferHeader) { h.SnapshotBytes = MaxSnapshotCompressed + 1 },
		"empty snapshot":          func(h *TransferHeader) { h.SnapshotBytes = 0 },
		"negative session":        func(h *TransferHeader) { h.SessionBytes = -1 },
		"session over the bound":  func(h *TransferHeader) { h.SessionBytes = MaxSessionBytes + 1 },
		"handoff id with a slash": func(h *TransferHeader) { h.HandoffID = "../escape" },
		"empty handoff id":        func(h *TransferHeader) { h.HandoffID = "" },
		"long handoff id":         func(h *TransferHeader) { h.HandoffID = strings.Repeat("a", 65) },
		"checksum case":           func(h *TransferHeader) { h.SessionSHA256 = strings.ToUpper(h.SessionSHA256) },
		"short checksum":          func(h *TransferHeader) { h.SnapshotSHA256 = "abc" },
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			h := ok
			mutate(&h)
			if _, err := BeginTransfer(home, h); err == nil {
				t.Fatal("accepted")
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Fatalf("created %v", entries)
			}
		})
	}
	at := ok
	at.SnapshotBytes = MaxSnapshotCompressed
	tr, err := BeginTransfer(t.TempDir(), at)
	if err != nil {
		t.Fatalf("exactly 64 MiB: %v", err)
	}
	tr.Close()
}

func TestResolveClone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	// A linked worktree of the target clone resolves to the clone itself.
	linked := filepath.Join(filepath.Dir(f.target), "linked")
	run(t, f.target, "git", "worktree", "add", "-b", "other", linked)
	for name, tc := range map[string]struct {
		watched []string
		path    string
		origin  string
		want    string
		err     error
	}{
		"by origin":                        {watched: []string{f.target}, origin: f.origin, want: f.target},
		"through a linked worktree":        {watched: []string{linked}, origin: f.origin, want: f.target},
		"a subdirectory":                   {watched: []string{filepath.Join(f.target, "src")}, origin: f.origin, want: f.target},
		"origin spelled as a file URL":     {watched: []string{f.target}, origin: "file://" + f.origin, want: f.target},
		"the first match wins":             {watched: []string{t.TempDir(), f.target, f.source}, origin: f.origin, want: f.target},
		"another origin falls to the path": {watched: []string{f.target}, origin: "git@example.test:o/other.git", path: f.source, want: f.source},
		"no origin, a path":                {path: f.target, want: f.target},
		"nothing matches":                  {watched: []string{f.target}, origin: "git@example.test:o/other.git", err: ErrNoClone},
		"a path that is not a repository":  {path: t.TempDir(), err: ErrNoClone},
		"a relative path":                  {path: "relative/dir", err: ErrNoClone},
		"a missing path":                   {path: filepath.Join(t.TempDir(), "missing"), err: ErrNoClone},
		"nothing at all":                   {err: ErrNoClone},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveClone(ctx, tc.watched, tc.path, tc.origin)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			want, _ := filepath.EvalSymlinks(tc.want)
			if err != nil || got != want {
				t.Fatalf("got %q (%v), want %q", got, err, want)
			}
		})
	}
}

func TestFindCloneLooksOnlyAtWatchedProjects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	want, _ := filepath.EvalSymlinks(f.target)
	if got := FindClone(ctx, []string{t.TempDir(), f.target}, f.origin); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := FindClone(ctx, []string{f.target}, "git@example.test:o/other.git"); got != "" {
		t.Fatalf("another origin: got %q", got)
	}
	if got := FindClone(ctx, []string{f.target}, ""); got != "" {
		t.Fatalf("no origin: got %q", got)
	}
}

func TestOriginsLoseTheirCredentials(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://user:token@github.com/o/r.git": "https://github.com/o/r.git",
		"https://ghp_secret@github.com/o/r.git": "https://github.com/o/r.git",
		"ssh://git@host.test:22/o/r":            "ssh://host.test:22/o/r",
		"git@github.com:o/r.git":                "git@github.com:o/r.git",
		"/srv/git/r.git":                        "/srv/git/r.git",
		"https://github.com/o/r.git":            "https://github.com/o/r.git",
	} {
		if got := withoutUserinfo(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()
	same := [][]string{
		{"git@github.com:org/repo.git", "https://github.com/org/repo", "ssh://git@github.com:22/org/repo.git", "https://GitHub.com/org/repo/", "https://user:pw@github.com/org/repo.git"},
		{"/srv/git/repo.git", "file:///srv/git/repo", "/srv/git//repo/"},
	}
	for _, group := range same {
		want := normalizeOrigin(group[0])
		for _, u := range group {
			if got := normalizeOrigin(u); got != want || got == "" {
				t.Errorf("%q -> %q, want %q", u, got, want)
			}
		}
	}
	if normalizeOrigin("git@github.com:org/a.git") == normalizeOrigin("git@github.com:org/b.git") {
		t.Error("different repositories compare equal")
	}
	if normalizeOrigin("https://github.com/org/repo") == normalizeOrigin("https://gitlab.com/org/repo") {
		t.Error("different hosts compare equal")
	}
	if normalizeOrigin("  ") != "" {
		t.Error("blank origin")
	}
}

// claudeOnly is the stores of an agent that has Claude Code's store under dir.
func claudeOnly(dir string) func(string) provider.Store {
	claude := claudeprovider.New(dir, nil)
	return func(name string) provider.Store {
		if name == "claude" {
			return claude
		}
		return nil
	}
}

func TestInstallRefusesAVendorWithoutAStore(t *testing.T) {
	t.Parallel()
	tg := newTarget(t)
	tr := tg.transfer(t, func(h *TransferHeader) { h.Provider = "codex" })
	if _, err := tg.inst.Install(context.Background(), tr); err == nil || !strings.Contains(err.Error(), "vendor") {
		t.Fatalf("err = %v", err)
	}
}
