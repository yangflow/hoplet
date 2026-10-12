package daemon

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	gosync "sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

type fakeUploader struct {
	mu      gosync.Mutex
	cursors map[string]bool
	failing error
	got     []sync.SnapshotUpload
	files   []string
}

func (f *fakeUploader) Cursor(id string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 1, f.cursors[id]
}

func (f *fakeUploader) UploadSnapshot(_ context.Context, up sync.SnapshotUpload, r io.Reader) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing != nil {
		return f.failing
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	file := filepath.Join(os.TempDir(), "uploaded-"+up.SnapshotID)
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		return err
	}
	f.got = append(f.got, up)
	f.files = append(f.files, file)
	return nil
}

func (f *fakeUploader) uploads() []sync.SnapshotUpload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sync.SnapshotUpload(nil), f.got...)
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TEMPLATE_DIR=", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repoWithChange(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "a.txt")
	gitIn(t, dir, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newTestSnapshotter(t *testing.T, dir string, up *fakeUploader) *snapshotter {
	t.Helper()
	locate := func(id string) (runtime.Location, error) { return runtime.Location{Dir: dir}, nil }
	s := newSnapshotter(t.TempDir(), locate, up)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s
}

func TestSnapshotterUploadsAfterATurnAndRemovesTheArchive(t *testing.T) {
	dir := repoWithChange(t)
	up := &fakeUploader{cursors: map[string]bool{"s1": true}}
	s := newTestSnapshotter(t, dir, up)
	s.mark("s1")
	waitFor(t, "an upload", func() bool { return len(up.uploads()) == 1 })

	got := up.uploads()[0]
	if got.SessionID != "s1" || got.ByteSize <= 0 || len(got.SHA256) != 64 || got.TreeHash == "" {
		t.Fatalf("upload = %+v", got)
	}
	archive, err := workspace.OpenArchive(up.files[0], t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Files) != 1 || archive.Files[0].Name != "new.txt" {
		t.Fatalf("files = %+v", archive.Files)
	}
	if entries, _ := os.ReadDir(s.dir); len(entries) != 0 {
		t.Fatalf("archives left behind: %v", entries)
	}
	// Marked once and uploaded: nothing more is sent without another turn.
	time.Sleep(2*snapshotRetry + 200*time.Millisecond)
	if n := len(up.uploads()); n != 1 {
		t.Fatalf("%d uploads", n)
	}
}

func TestSnapshotterWaitsForTheSessionToBeSynced(t *testing.T) {
	dir := repoWithChange(t)
	up := &fakeUploader{cursors: map[string]bool{}}
	s := newTestSnapshotter(t, dir, up)
	s.mark("s1")
	time.Sleep(300 * time.Millisecond)
	if n := len(up.uploads()); n != 0 {
		t.Fatalf("uploaded before the core knew the session: %d", n)
	}
	up.mu.Lock()
	up.cursors["s1"] = true
	up.mu.Unlock()
	waitFor(t, "the upload after the first append", func() bool { return len(up.uploads()) == 1 })
}

func TestSnapshotterRetriesWhileDisconnectedAndDropsWhatCannotWork(t *testing.T) {
	dir := repoWithChange(t)
	up := &fakeUploader{cursors: map[string]bool{"s1": true, "plain": true}, failing: sync.ErrNotConnected}
	s := newTestSnapshotter(t, dir, up)
	s.mark("s1")
	time.Sleep(300 * time.Millisecond)
	up.mu.Lock()
	up.failing = nil
	up.mu.Unlock()
	waitFor(t, "the retry after reconnecting", func() bool { return len(up.uploads()) == 1 })

	// A session outside a repository is dropped, not retried forever.
	plain := t.TempDir()
	locate := func(id string) (runtime.Location, error) { return runtime.Location{Dir: plain}, nil }
	s2 := newSnapshotter(t.TempDir(), locate, up)
	s2.mark("plain")
	err := s2.snapshot(context.Background(), "plain")
	if err == nil {
		t.Fatal("a directory without a repository was snapshotted")
	}
	s2.settle("plain", err)
	s2.mu.Lock()
	defer s2.mu.Unlock()
	if s2.dirty["plain"] {
		t.Fatal("the mark stayed")
	}
}
