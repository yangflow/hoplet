package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestDiffWithoutBaseline(t *testing.T) {
	g := newGit(t)
	f := newFixture(t, g)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	defer guard(t, g, repo)()
	d := f.diff(t, repo, proto.DiffParams{})
	if d.BaselineAt != nil || d.BaselineError == "" || len(d.Repos) != 0 {
		t.Fatalf("diff %+v", d)
	}
	if _, err := f.svc.Diff(context.Background(), "nope", t.TempDir(), proto.DiffParams{}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown session: %v", err)
	}
}

func TestMissingGit(t *testing.T) {
	g := newGit(t)
	f := newFixture(t, g)
	f.svc.Git = Git{}
	dir := t.TempDir()
	if err := f.svc.CaptureBaseline(context.Background(), dir)(context.Background(), f.id); err == nil {
		t.Fatal("baseline without git succeeded")
	}
	b, ok, err := f.st.Baseline(context.Background(), f.id)
	if err != nil || !ok || b.Error == "" {
		t.Fatalf("the failure was not recorded: %+v %v %v", b, ok, err)
	}
	if _, err := f.svc.Diff(context.Background(), f.id, dir, proto.DiffParams{}); err == nil {
		t.Fatal("diff without git succeeded")
	}
}

func TestBaselineRecordsFailures(t *testing.T) {
	g := newGit(t)
	g.snapshotLimit = 50
	root := t.TempDir()
	initRepo(t, g, filepath.Join(root, "ok"), map[string]string{"a": "1\n"})
	// A repository that cannot be snapshotted (too large), and a directory
	// whose .git is not a git dir, which is no repository at all.
	initRepo(t, g, filepath.Join(root, "broken"), map[string]string{"big": strings.Repeat("x", 100)})
	if err := os.MkdirAll(filepath.Join(root, "empty", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, g)
	defer guard(t, g, filepath.Join(root, "ok"), filepath.Join(root, "broken"))()
	f.baseline(t, root)
	b, _, _ := f.st.Baseline(context.Background(), f.id)
	if len(b.Repos) != 2 || b.Repos[0].Path != "broken" || b.Repos[0].Error == "" || b.Repos[1].Error != "" {
		t.Fatalf("baseline %+v", b)
	}
	d := f.diff(t, root, proto.DiffParams{})
	br := repoByPath(t, d, "broken")
	if br.State != proto.RepoError || !strings.Contains(br.Error, "baseline unavailable") {
		t.Fatalf("broken repo %+v", br)
	}
	if ok := repoByPath(t, d, "ok"); ok.State != proto.RepoOK {
		t.Fatalf("ok repo %+v", ok)
	}

	// A baseline that ran out of time keeps what it had, and says so; the
	// repositories it did not reach are errors, not new.
	f2 := newFixture(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f2.svc.CaptureBaseline(ctx, root)(ctx, f2.id); err == nil {
		t.Fatal("cancelled baseline reported success")
	}
	b2, ok2, _ := f2.st.Baseline(context.Background(), f2.id)
	if !ok2 || b2.Error == "" {
		t.Fatalf("incomplete baseline not recorded: %+v", b2)
	}
	d2 := f2.diff(t, root, proto.DiffParams{})
	if d2.BaselineError == "" || repoByPath(t, d2, "ok").State != proto.RepoError {
		t.Fatalf("diff after an incomplete baseline: %+v", d2)
	}
}

func TestConcurrentDiffs(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	write(t, repo, "b", "3\n")
	check := guard(t, g, repo)
	var wg sync.WaitGroup
	shas := make([]string, 8)
	errs := make([]error, 8)
	for i := range shas {
		wg.Go(func() {
			d, err := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{Patch: true})
			if err != nil {
				errs[i] = err
				return
			}
			shas[i] = d.Repos[0].SHA
			if len(d.Repos[0].Files) != 2 || d.Repos[0].Error != "" {
				errs[i] = fmt.Errorf("repo %+v", d.Repos[0])
			}
		})
	}
	wg.Wait()
	check()
	for i := range shas {
		if errs[i] != nil || shas[i] == "" || shas[i] != shas[0] {
			t.Fatalf("diff %d: %q %v (first %q)", i, shas[i], errs[i], shas[0])
		}
	}
	// Private indexes and temporary files do not pile up.
	entries, err := os.ReadDir(filepath.Join(f.home, "objects"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("object dirs: %v %v", entries, err)
	}
	if left, _ := os.ReadDir(filepath.Join(f.home, "objects", entries[0].Name(), "tmp")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestIdenticalDiffRequestsShareTheNextComputation(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	defer guard(t, g, repo)()

	const callers = 4
	joined := make(chan struct{}, callers)
	release := make(chan struct{})
	var computed atomic.Int32
	f.svc.onJoin = func() { joined <- struct{}{} }
	f.svc.beforeDiff = func(context.Context) {
		computed.Add(1)
		<-release
	}
	var wg sync.WaitGroup
	shas := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			d, err := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{Patch: true})
			errs[i] = err
			if err == nil {
				shas[i] = d.Repos[0].SHA
			}
		})
	}
	for range callers {
		<-joined
	}
	close(release)
	wg.Wait()
	// The first request runs alone; the others, which arrived meanwhile,
	// share the one after it.
	if computed.Load() != 2 {
		t.Fatalf("%d computations for %d identical requests, want 2", computed.Load(), callers)
	}
	for i := range shas {
		if errs[i] != nil || shas[i] == "" || shas[i] != shas[0] {
			t.Fatalf("caller %d: %q %v", i, shas[i], errs[i])
		}
	}
}

// TestRequestDuringAComputationSeesLaterEdits: a request never gets a result
// computed from a snapshot older than the request.
func TestRequestDuringAComputationSeesLaterEdits(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "a", "first edit\n")
	defer guard(t, g, repo)()

	snapshotted := make(chan struct{})
	release := make(chan struct{})
	joined := make(chan struct{}, 2)
	var runs atomic.Int32
	f.svc.onJoin = func() { joined <- struct{}{} }
	f.svc.afterSnapshot = func() {
		if runs.Add(1) == 1 { // the first computation has its snapshot; hold it
			close(snapshotted)
			<-release
		}
	}
	first := make(chan proto.SessionDiff, 1)
	go func() {
		d, _ := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{Patch: true})
		first <- d
	}()
	<-snapshotted
	write(t, repo, "a", "second edit\n") // after the first computation's snapshot
	second := make(chan proto.SessionDiff, 1)
	go func() {
		d, _ := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{Patch: true})
		second <- d
	}()
	<-joined
	<-joined
	close(release)
	if d := <-first; !strings.Contains(d.Repos[0].Patch, "+first edit") {
		t.Fatalf("first result:\n%s", d.Repos[0].Patch)
	}
	if d := <-second; !strings.Contains(d.Repos[0].Patch, "+second edit") {
		t.Fatalf("a request made after the edit got an older result:\n%s", d.Repos[0].Patch)
	}
}

// TestCancelledWaiterDoesNotCancelOthers: two callers share one pending
// flight behind a running one; the one that leaves does not cancel it.
func TestCancelledWaiterDoesNotCancelOthers(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	joined := make(chan struct{}, 3)
	release := make(chan struct{})
	var computed atomic.Int32
	f.svc.onJoin = func() { joined <- struct{}{} }
	f.svc.beforeDiff = func(ctx context.Context) {
		computed.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	blocker := make(chan struct{})
	go func() { // runs first, with other parameters
		_, _ = f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{Patch: true})
		close(blocker)
	}()
	<-joined
	ctx, cancel := context.WithCancel(context.Background())
	gone := make(chan error, 1)
	go func() {
		_, err := f.svc.Diff(ctx, f.id, repo, proto.DiffParams{})
		gone <- err
	}()
	stays := make(chan proto.SessionDiff, 1)
	go func() {
		d, _ := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{})
		stays <- d
	}()
	<-joined
	<-joined
	cancel()
	if err := <-gone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	close(release)
	if d := <-stays; len(d.Repos) != 1 || len(d.Repos[0].Files) != 1 {
		t.Fatalf("the other waiter got %+v", d)
	}
	<-blocker
	if computed.Load() != 2 {
		t.Fatalf("%d computations, want the running one and one shared by both waiters", computed.Load())
	}
}

// TestQueuedRequestTimesOutFromItsArrival: a request that waits behind a
// computation that does not finish fails with a deadline error after the
// limit, counted from when the request arrived, not from when its own
// computation would start.
func TestQueuedRequestTimesOutFromItsArrival(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	f.svc.diffTimeout = 300 * time.Millisecond
	joined := make(chan struct{}, 2)
	release := make(chan struct{})
	f.svc.onJoin = func() { joined <- struct{}{} }
	f.svc.beforeDiff = func(context.Context) { <-release } // the running one never ends on its own
	defer close(release)
	running := make(chan error, 1)
	go func() {
		_, err := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{Patch: true})
		running <- err
	}()
	<-joined
	queued := make(chan error, 1)
	go func() {
		// Other parameters: it waits for its own computation, after the running one.
		_, err := f.svc.Diff(context.Background(), f.id, repo, proto.DiffParams{})
		queued <- err
	}()
	<-joined
	for name, ch := range map[string]chan error{"running": running, "queued": queued} {
		select {
		case err := <-ch:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s request: %v", name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s request did not time out", name)
		}
	}
}

func TestHopletIndexRecoversAndIsSwept(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	objects, err := ObjectDir(context.Background(), f.home, Repo{Path: ".", Abs: repo}, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath(objects, Repo{Abs: repo}), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "a", "2\n")
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:a")

	// A new service (a new daemon) removes what a crash left.
	if err := os.MkdirAll(filepath.Join(objects, "tmp", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath(objects, Repo{Abs: repo})+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f2 := newFixture(t, g)
	f2.svc.Home, f2.svc.Store = f.home, f.st
	f2.baseline(t, repo)
	if exists(filepath.Join(objects, "tmp", "x")) || exists(indexPath(objects, Repo{Abs: repo})+".lock") {
		t.Fatal("leftovers of a crash were not removed")
	}
}

func TestStaleLockIsRemovedWithoutLosingTheIndex(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n", "b": "2\n", "c": "3\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	objects, err := ObjectDir(context.Background(), f.home, Repo{Path: ".", Abs: repo}, g)
	if err != nil {
		t.Fatal(err)
	}
	lock := indexPath(objects, Repo{Abs: repo}) + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var hashed []string
	g.hashed = func(p []string) { hashed = append(hashed, p...) }
	f.svc.Git = g
	write(t, repo, "a", "changed\n")
	defer guard(t, g, repo)()
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:a")
	if exists(lock) {
		t.Fatal("the stale lock is still there")
	}
	if len(hashed) != 1 {
		t.Fatalf("hashed %v: the index was thrown away", hashed)
	}
}

// TestIndexIsKeptOnOtherErrors: only a damaged index is thrown away.
func TestIndexIsKeptOnOtherErrors(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	objects, err := ObjectDir(context.Background(), f.home, Repo{Path: ".", Abs: repo}, g)
	if err != nil {
		t.Fatal(err)
	}
	index := indexPath(objects, Repo{Abs: repo})
	before, err := os.ReadFile(index)
	if err != nil || len(before) == 0 {
		t.Fatalf("index: %d bytes %v", len(before), err)
	}
	script := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\ncase \"$*\" in *write-tree*) echo 'fatal: something transient' >&2; exit 128;; esac\nexec '" + g.Bin + "' \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	failing := g
	failing.Bin = script
	f.svc.Git = failing
	write(t, repo, "a", "2\n")
	if d := f.diff(t, repo, proto.DiffParams{}); d.Repos[0].State != proto.RepoError {
		t.Fatalf("diff %+v", d.Repos[0])
	}
	after, err := os.ReadFile(index)
	if err != nil || len(after) == 0 {
		t.Fatalf("the index was thrown away: %v", err)
	}
}

func TestImportLeftoversAreSwept(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	objects, err := ObjectDir(context.Background(), f.home, Repo{Path: ".", Abs: repo}, g)
	if err != nil {
		t.Fatal(err)
	}
	leftovers := []string{
		filepath.Join(objects, "pack", "tmp_pack_abc123"),
		filepath.Join(objects, "pack", "tmp_idx_abc123"),
		filepath.Join(objects, "pack", ".tmp-1234-pack-abc123.pack"),
		filepath.Join(objects, "fi.git", "fast_import_crash_42"),
	}
	plant := func() {
		for _, l := range leftovers {
			if err := os.MkdirAll(filepath.Dir(l), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(l, []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	gone := func(when string) {
		t.Helper()
		for _, l := range leftovers {
			if exists(l) {
				t.Fatalf("%s is still there %s", l, when)
			}
		}
	}
	// At the start of a new Service (a new daemon).
	plant()
	f2 := newFixture(t, g)
	f2.svc.Home, f2.svc.Store = f.home, f.st
	f2.baseline(t, repo)
	gone("after the service started")
	// And before each snapshot.
	plant()
	write(t, repo, "a", "2\n")
	f2.diff(t, repo, proto.DiffParams{})
	gone("after a snapshot")
}

func TestPacksAreConsolidated(t *testing.T) {
	g := newGit(t)
	files := map[string]string{}
	for i := 0; i < 450; i++ {
		files["d/f"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('A'+(i/26)%26))+string(rune('a'+(i/676)))] = "content " + strings.Repeat("y", i) + "\n"
	}
	repo := t.TempDir()
	initRepo(t, g, repo, files)
	g.filesPerChunk = 150 // rounds of more than 100 objects each make a pack
	f := newFixture(t, g)
	f.svc.packLimit = 2
	defer guard(t, g, repo)()
	ctx, cancel := context.WithCancel(context.Background())
	if err := f.svc.CaptureBaseline(ctx, repo)(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	cancel()        // the repack runs on a context of its own
	f.svc.bg.Wait() // ... and is waited for, not cancelled
	defer f.svc.Close()
	b, _, err := f.st.Baseline(context.Background(), f.id)
	if err != nil || len(b.Repos) != 1 {
		t.Fatalf("baseline %+v %v", b, err)
	}
	snap := Snapshot{Tree: b.Repos[0].Tree}
	objects, err := ObjectDir(context.Background(), f.home, Repo{Path: ".", Abs: repo}, g)
	if err != nil {
		t.Fatal(err)
	}
	if packs, _ := filepath.Glob(filepath.Join(objects, "pack", "*.pack")); len(packs) != 1 {
		t.Fatalf("%d packs after consolidating", len(packs))
	}
	// Everything the tree names is still there.
	env := append(os.Environ(), "GIT_DIR="+filepath.Join(objects, "fi.git"), "GIT_OBJECT_DIRECTORY="+objects)
	run := func(stdin string, args ...string) string {
		cmd := exec.Command(g.Bin, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(run("", "ls-tree", "-r", snap.Tree)), "\n") {
		f := strings.Fields(line)
		ids = append(ids, f[2])
	}
	if len(ids) != 450 {
		t.Fatalf("%d files in the tree", len(ids))
	}
	if out := run(strings.Join(ids, "\n")+"\n", "cat-file", "--batch-check"); strings.Contains(out, "missing") {
		t.Fatalf("objects are missing after the repack:\n%s", out)
	}
}

// TestDiffStopsWhenNoOneWaits: a computation whose last waiter left is cancelled.
func TestDiffStopsWhenNoOneWaits(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	defer guard(t, g, repo)()
	started, ended := make(chan struct{}), make(chan struct{})
	f.svc.beforeDiff = func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(ended)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := f.svc.Diff(ctx, f.id, repo, proto.DiffParams{})
		errc <- err
	}()
	<-started
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("diff: %v", err)
	}
	<-ended // the computation was cancelled too
}

// TestRebuiltObjectStoreIsReported: a baseline whose objects are gone says so.
func TestRebuiltObjectStoreIsReported(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	if err := os.RemoveAll(filepath.Join(f.home, "objects")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "a", "2\n")
	r := f.diff(t, repo, proto.DiffParams{}).Repos[0]
	if r.State != proto.RepoError || r.Error != "the baseline is no longer available (Hoplet's object store was rebuilt)" {
		t.Fatalf("repo %+v", r)
	}
}

// TestCloseStopsAndAwaitsRepacks: one repack runs per object directory at a
// time, Close cancels it and waits, and none starts afterwards.
func TestCloseStopsAndAwaitsRepacks(t *testing.T) {
	g := newGit(t)
	files := map[string]string{}
	for i := 0; i < 450; i++ {
		files["d/f"+strconv.Itoa(i)] = "content " + strings.Repeat("y", i) + "\n"
	}
	repo := t.TempDir()
	initRepo(t, g, repo, files)
	// A git whose repack never ends by itself.
	script := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\ncase \"$*\" in *repack*) exec sleep 60;; esac\nexec '" + g.Bin + "' \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	slow := g
	slow.Bin, slow.filesPerChunk = script, 150 // rounds of more than 100 objects each make a pack
	f := newFixture(t, slow)
	f.svc.packLimit = 1
	started := make(chan struct{}, 8)
	f.svc.onRepack = func() { started <- struct{}{} }
	defer guard(t, g, repo)()

	f.baseline(t, repo)
	<-started
	write(t, repo, "d/new", "more\n")
	f.diff(t, repo, proto.DiffParams{}) // snapshots again while the repack runs
	closed := make(chan struct{})
	go func() {
		f.svc.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return")
	}
	write(t, repo, "d/newer", "more\n")
	f.diff(t, repo, proto.DiffParams{}) // and after Close
	f.svc.mu.Lock()
	busy, repacking := f.svc.active, len(f.svc.repacking)
	f.svc.mu.Unlock()
	for dir, n := range busy {
		if n != 0 {
			t.Fatalf("%s is still marked busy: %d", dir, n)
		}
	}
	if repacking != 0 || len(started) != 0 {
		t.Fatalf("repacking %d, further repacks started: %d", repacking, len(started))
	}
}
