package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

const (
	// saveTimeout bounds saving a baseline after the work itself ran out of
	// time.
	saveTimeout = 10 * time.Second
	// A response must fit the connection's frame limits: at most
	// maxResponseBytes as JSON encodes it, maxResponseFiles files per
	// repository, maxResponseTotalFiles in all, and maxResponseSkipped
	// skipped files per repository.
	maxResponseBytes      = 6 << 20
	maxResponseFiles      = 5000
	maxResponseTotalFiles = 20000
	maxResponseSkipped    = 1000
	// defaultDiffTimeout bounds a diff request from its arrival, and a diff
	// computation from its start.
	defaultDiffTimeout = 60 * time.Second
	// defaultPackLimit is how many packs an object directory may hold before
	// they are consolidated, which takes at most repackTimeout.
	defaultPackLimit = 50
	repackTimeout    = 10 * time.Minute
)

// BaselineStore keeps session baselines; *store.Store implements it.
type BaselineStore interface {
	SaveBaseline(ctx context.Context, session proto.SessionID, b store.Baseline) error
	Baseline(ctx context.Context, session proto.SessionID) (store.Baseline, bool, error)
}

// Service owns baselines and diffs for sessions. Home is Hoplet's data
// directory, which holds the object directories. Snapshots of one worktree
// are serialized. A Service must not be copied after first use; Close it when
// done.
type Service struct {
	Git   Git
	Home  string
	Store BaselineStore
	Now   func() time.Time // time.Now when nil

	// Overrides of the limits above and of the deadline, for tests (0: the
	// defaults).
	maxBytes, maxFiles, maxTotalFiles, packLimit int
	diffTimeout                                  time.Duration

	sweepOnce sync.Once
	mu        sync.Mutex
	locks     map[string]*sync.Mutex // by index
	active    map[string]int         // writers (snapshots, repacks) per object directory
	repacking map[string]bool
	sessions  map[proto.SessionID]*sessionFlights
	bg        sync.WaitGroup // background repacks
	bgCtx     context.Context
	bgCancel  context.CancelFunc
	closed    bool

	// Test hooks.
	onJoin        func()
	beforeDiff    func(ctx context.Context)
	afterSnapshot func()
	onRepack      func()
}

// sessionFlights are the diff computations of one session: at most one runs,
// and requests arriving meanwhile wait in pending flights, one per distinct
// request, each shared by every request that asks for it before it starts.
// A request never shares a computation that began before it was made, so it
// never gets a result older than the edit that prompted it.
type sessionFlights struct {
	running *flight
	pending []*flight
}

// flight is a diff computation that identical requests share.
type flight struct {
	key     string
	session proto.SessionID
	dir     string
	params  proto.DiffParams
	done    chan struct{}
	started bool
	cancel  context.CancelFunc
	refs    int
	res     proto.SessionDiff
	err     error
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// sweep removes what a crashed daemon left in the object directories:
// temporary files and a stale lock on Hoplet's index (the daemon is a
// singleton, so no one else holds it).
func (s *Service) sweep() {
	s.sweepOnce.Do(func() {
		dirs, _ := filepath.Glob(filepath.Join(s.Home, "objects", "*"))
		for _, d := range dirs {
			_ = os.RemoveAll(filepath.Join(d, "tmp"))
			sweepTemp(d)
			locks, _ := filepath.Glob(filepath.Join(d, "index-*.lock"))
			for _, l := range locks {
				_ = os.Remove(l)
			}
		}
	})
}

// lock serializes work on one Hoplet index.
func (s *Service) lock(index string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	l, ok := s.locks[index]
	if !ok {
		l = &sync.Mutex{}
		s.locks[index] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// enter marks an object directory as being written to, and returns the
// function that ends it. The leftovers of a killed import or repack are swept
// while nothing else is writing there, decided under the same lock.
func (s *Service) enter(objects string) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = map[string]int{}
	}
	if s.active[objects]++; s.active[objects] == 1 {
		sweepTemp(objects)
	}
	return func() {
		s.mu.Lock()
		s.active[objects]--
		s.mu.Unlock()
	}
}

// repackSoon consolidates the packs of an object directory in the
// background when there are too many: on a context of its own, which a
// request's end must not cut short, at most one per directory. Close waits
// for it.
func (s *Service) repackSoon(objects string) {
	if packCount(objects) <= orDefault(s.packLimit, defaultPackLimit) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repacking[objects] || s.closed {
		return
	}
	if s.bgCtx == nil {
		s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
		s.repacking = map[string]bool{}
	}
	s.repacking[objects] = true
	s.active[objects]++ // writing: nothing is swept meanwhile
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		if s.onRepack != nil {
			s.onRepack()
		}
		ctx, cancel := context.WithTimeout(s.bgCtx, repackTimeout)
		defer cancel()
		_ = s.Git.consolidate(ctx, objects) // best effort
		s.mu.Lock()
		s.active[objects]--
		delete(s.repacking, objects)
		s.mu.Unlock()
	}()
}

// Close stops background work and waits for it.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.bgCancel != nil {
		s.bgCancel()
	}
	s.mu.Unlock()
	s.bg.Wait()
}

// CaptureBaseline records what every git repository under dir contains, as
// the starting point of a session's diffs, and returns the function that
// saves it for a session. A session's baseline is captured before the
// session exists, so that nothing the session does can come before it. A
// repository that cannot be recorded gets a row saying why.
//
// The baseline is saved even when git is missing, dir cannot be read or time
// ran out part way, with the reason in its Error; in those cases the reason
// is returned too. Otherwise the error is non-nil only when nothing could be
// saved.
func (s *Service) CaptureBaseline(ctx context.Context, dir string) (save func(ctx context.Context, session proto.SessionID) error) {
	s.sweep()
	b := store.Baseline{TakenAt: s.now().UTC()}
	var failure error
	if s.Git.Bin == "" {
		failure = ErrNoGit
	} else {
		failure = s.snapshotAll(ctx, dir, &b)
	}
	if failure != nil {
		b.Error = failure.Error()
	}
	return func(ctx context.Context, session proto.SessionID) error {
		// ctx may be spent; saving must not depend on it.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
		defer cancel()
		if err := s.Store.SaveBaseline(saveCtx, session, b); err != nil {
			return errors.Join(failure, fmt.Errorf("workspace: save baseline: %w", err))
		}
		return failure
	}
}

// snapshotAll adds a row to b for every repository under dir. It returns
// why it stopped early, if it did.
func (s *Service) snapshotAll(ctx context.Context, dir string, b *store.Baseline) error {
	repos, err := s.Git.Discover(ctx, dir)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("workspace: baseline stopped after %d of %d repositories: %w", len(b.Repos), len(repos), err)
		}
		row := store.BaselineRepo{Path: r.Path}
		if r.Err != "" {
			row.Error = r.Err
		} else if snap, err := s.snapshot(ctx, r); err != nil {
			row.Error = err.Error()
		} else {
			row.Tree, row.Skipped = snap.Tree, snap.Skipped
		}
		b.Repos = append(b.Repos, row)
	}
	return nil
}

// snapshot snapshots r under the lock of its index. A lock file left by a
// killed git is ours (the daemon is a singleton and holds the mutex), so it
// is removed first. If Hoplet's index cannot be read it is discarded and the
// snapshot made again from scratch; any other error keeps it, as it holds the
// progress of large snapshots.
func (s *Service) snapshot(ctx context.Context, r Repo) (Snapshot, error) {
	objects, err := ObjectDir(ctx, s.Home, r, s.Git)
	if err != nil {
		return Snapshot{}, err
	}
	index := indexPath(objects, r)
	defer s.lock(index)()
	_ = os.Remove(index + ".lock")
	leave := s.enter(objects)
	defer leave()
	snap, err := s.Git.Snapshot(ctx, r, objects)
	if err != nil && ctx.Err() == nil && errors.As(err, new(*indexError)) {
		_ = os.Remove(index)
		snap, err = s.Git.Snapshot(ctx, r, objects)
	}
	if err == nil {
		s.repackSoon(objects)
	}
	return snap, err
}

// mergeSkipped returns the skipped files of the baseline and of now, once per
// path (now's entry wins), by path.
func mergeSkipped(base, now []SkippedFile) []SkippedFile {
	byPath := map[string]SkippedFile{}
	for _, f := range slices.Concat(base, now) {
		byPath[f.Path] = f
	}
	var out []SkippedFile
	for _, p := range slices.Sorted(maps.Keys(byPath)) {
		out = append(out, byPath[p])
	}
	return out
}

// Diff works out what the session changed in each git repository under dir
// since its baseline. A repository that cannot be diffed is reported with
// state error; the call itself fails only for an unknown session or an
// unusable git. At most one computation runs per session; identical requests
// that arrive while it runs share the next one, which starts after it, so
// no one gets a result computed before their request. A computation stops
// when no caller waits for it, and in any case after a minute.
// The response is bounded: all repositories together fit the byte budget as
// JSON encodes them (a repository past it gets no patch, then no file list,
// marked Truncated / FilesTruncated), and a repository lists a limited number
// of files and skipped files.
func (s *Service) Diff(ctx context.Context, session proto.SessionID, dir string, p proto.DiffParams) (proto.SessionDiff, error) {
	if s.Git.Bin == "" {
		return proto.SessionDiff{}, ErrNoGit
	}
	// The limit counts from the request's arrival, queue wait included.
	ctx, cancelTimeout := context.WithTimeout(ctx, orDefault(s.diffTimeout, defaultDiffTimeout))
	defer cancelTimeout()
	key := strings.Join([]string{dir, p.Repo, strconv.FormatBool(p.Patch)}, "\x00")
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[proto.SessionID]*sessionFlights{}
	}
	sf := s.sessions[session]
	if sf == nil {
		sf = &sessionFlights{}
		s.sessions[session] = sf
	}
	var f *flight
	for _, q := range sf.pending {
		if q.key == key {
			f = q
		}
	}
	if f == nil {
		f = &flight{key: key, session: session, dir: dir, params: p, done: make(chan struct{})}
		if sf.running == nil {
			s.start(sf, f)
		} else {
			sf.pending = append(sf.pending, f)
		}
	}
	f.refs++
	s.mu.Unlock()
	if s.onJoin != nil {
		s.onJoin()
	}
	select {
	case <-f.done:
		return f.res, f.err
	case <-ctx.Done():
		s.mu.Lock()
		if f.refs--; f.refs == 0 {
			if f.started {
				f.cancel() // no one waits for it; the next one need not wait either
			} else {
				sf.pending = slices.DeleteFunc(sf.pending, func(q *flight) bool { return q == f })
			}
		}
		s.mu.Unlock()
		return proto.SessionDiff{}, ctx.Err()
	}
}

// start runs f as the session's computation; s.mu is held. When it ends, the
// next pending one starts.
func (s *Service) start(sf *sessionFlights, f *flight) {
	f.started = true
	cctx, cancel := context.WithTimeout(context.Background(), orDefault(s.diffTimeout, defaultDiffTimeout))
	f.cancel = cancel
	sf.running = f
	go func() {
		defer cancel()
		res, err := s.diff(cctx, f.session, f.dir, f.params)
		s.mu.Lock()
		f.res, f.err = res, err
		sf.running = nil
		if len(sf.pending) > 0 {
			next := sf.pending[0]
			sf.pending = sf.pending[1:]
			s.start(sf, next)
		} else if s.sessions[f.session] == sf {
			delete(s.sessions, f.session)
		}
		s.mu.Unlock()
		close(f.done)
	}()
}

func (s *Service) diff(ctx context.Context, session proto.SessionID, dir string, p proto.DiffParams) (proto.SessionDiff, error) {
	if s.beforeDiff != nil {
		s.beforeDiff(ctx)
	}
	s.sweep()
	b, ok, err := s.Store.Baseline(ctx, session)
	if err != nil {
		return proto.SessionDiff{}, err
	}
	out := proto.SessionDiff{Session: session, Repos: []proto.RepoDiff{}}
	if !ok {
		out.BaselineError = "no baseline was taken for this session"
		return out, nil
	}
	taken := b.TakenAt
	out.BaselineAt, out.BaselineError = &taken, b.Error

	found, err := s.Git.Discover(ctx, dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return proto.SessionDiff{}, err
	}
	nowRepos := map[string]Repo{}
	for _, r := range found {
		nowRepos[r.Path] = r
	}
	rows := map[string]store.BaselineRepo{}
	paths := slices.Collect(maps.Keys(nowRepos))
	for _, r := range b.Repos {
		rows[r.Path] = r
		if _, seen := nowRepos[r.Path]; !seen {
			paths = append(paths, r.Path)
		}
	}
	slices.Sort(paths)
	budget, filesLeft := orDefault(s.maxBytes, maxResponseBytes), orDefault(s.maxTotalFiles, maxResponseTotalFiles)
	for _, path := range paths {
		if p.Repo != "" && p.Repo != path {
			continue
		}
		if err := ctx.Err(); err != nil {
			return proto.SessionDiff{}, fmt.Errorf("workspace: diff: %w", err)
		}
		var cur *Repo
		if r, ok := nowRepos[path]; ok {
			cur = &r
		}
		var row *store.BaselineRepo
		if r, ok := rows[path]; ok {
			row = &r
		}
		rd := s.repoDiff(ctx, path, cur, row, b.Error, p.Patch, budget, min(orDefault(s.maxFiles, maxResponseFiles), filesLeft))
		size := fit(&rd, budget)
		budget -= size
		filesLeft -= len(rd.Files)
		out.Repos = append(out.Repos, rd)
	}
	return out, nil
}

// fit makes rd's JSON encoding at most budget bytes where it can: it cuts the
// patch (at a line end, in proportion to what does not fit), then drops it,
// then the file list, and then everything but a minimal entry (path, parent,
// branch, state, error, sha and the truncation flags). It returns the
// encoded size.
func fit(rd *proto.RepoDiff, budget int) int {
	size := encodedSize(rd)
	if size <= budget {
		return size
	}
	if rd.Patch != "" {
		enc, keep := encodedSize(rd.Patch), 0
		if over := size - budget; over < enc {
			keep = len(rd.Patch) * (enc - over) / enc * 9 / 10 // a margin: escapes vary
		}
		rd.Patch, rd.Truncated = rd.Patch[:keep], true
		if i := strings.LastIndexByte(rd.Patch, '\n'); i >= 0 {
			rd.Patch = rd.Patch[:i+1]
		} else {
			rd.Patch = ""
		}
		if size = encodedSize(rd); size <= budget {
			return size
		}
		rd.Patch = ""
		if size = encodedSize(rd); size <= budget {
			return size
		}
	}
	if len(rd.Files) > 0 {
		rd.Files, rd.FilesTruncated = []proto.FileDiff{}, true
		if size = encodedSize(rd); size <= budget {
			return size
		}
	}
	rd.SkippedTruncated = rd.SkippedTruncated || len(rd.Skipped) > 0
	rd.Skipped = nil
	rd.Path, rd.Parent, rd.Branch = clip(rd.Path, 256), clip(rd.Parent, 256), clip(rd.Branch, 256)
	rd.Error = clip(rd.Error, 200)
	return encodedSize(rd)
}

// clip cuts s to n bytes, if longer, at a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}

// encodedSize is the length of v as the daemon encodes it.
func encodedSize(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

// repoDiff diffs one repository. cur is nil when it is gone, row nil when
// the baseline has none for it; baselineErr is why the baseline is
// incomplete; budget is the patch bytes left for the response and fileLimit
// the files this repository may list.
func (s *Service) repoDiff(ctx context.Context, path string, cur *Repo, row *store.BaselineRepo, baselineErr string, patch bool, budget, fileLimit int) proto.RepoDiff {
	rd := proto.RepoDiff{Path: path, State: proto.RepoOK, Files: []proto.FileDiff{}}
	fail := func(err error) proto.RepoDiff {
		rd.State, rd.Error = proto.RepoError, err.Error()
		return rd
	}
	if cur == nil {
		rd.State = proto.RepoMissing
		return rd
	}
	rd.Parent = cur.Parent
	switch {
	case cur.Err != "":
		return fail(errors.New(cur.Err))
	case row != nil && row.Error != "":
		return fail(fmt.Errorf("baseline unavailable: %s", row.Error))
	case row == nil && baselineErr != "":
		return fail(fmt.Errorf("baseline unavailable: %s", baselineErr))
	}
	objects, err := ObjectDir(ctx, s.Home, *cur, s.Git)
	if err != nil {
		return fail(err)
	}
	var base string
	var baseSkipped []SkippedFile
	if row != nil {
		base, baseSkipped = row.Tree, row.Skipped
	} else {
		rd.State = proto.RepoNew
		if base, err = s.Git.EmptyTree(ctx, *cur, objects); err != nil {
			return fail(err)
		}
	}
	snap, err := s.snapshot(ctx, *cur)
	if err != nil {
		return fail(err)
	}
	if s.afterSnapshot != nil {
		s.afterSnapshot()
	}
	g := s.Git
	g.MaxPatch = min(g.maxPatch(), max(budget, 0))
	switch {
	case patch && budget > 0:
		rd.Files, rd.Patch, rd.Truncated, err = g.Diff(ctx, *cur, objects, base, snap.Tree)
	default:
		rd.Files, err = g.DiffFiles(ctx, *cur, objects, base, snap.Tree)
		rd.Truncated = patch && len(rd.Files) > 0
	}
	if err != nil {
		if row != nil && !g.hasTree(ctx, *cur, objects, base) {
			return fail(errors.New("the baseline is no longer available (Hoplet's object store was rebuilt)"))
		}
		return fail(err)
	}
	if len(rd.Files) > fileLimit {
		rd.Files, rd.FilesTruncated = rd.Files[:fileLimit], true
	}
	rd.Skipped = mergeSkipped(baseSkipped, snap.Skipped)
	if len(rd.Skipped) > maxResponseSkipped {
		rd.Skipped, rd.SkippedTruncated = rd.Skipped[:maxResponseSkipped], true
	}
	rd.Branch = s.Git.branch(ctx, *cur)
	h := sha256.New()
	for _, part := range []string{base, snap.Tree, rd.Branch, string(rd.State), skippedKey(rd.Skipped)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	rd.SHA = hex.EncodeToString(h.Sum(nil))
	return rd
}

func skippedKey(in []proto.SkippedFile) string {
	var parts []string
	for _, f := range in {
		parts = append(parts, f.Path+"\t"+f.Reason+"\t"+strconv.FormatInt(f.Size, 10))
	}
	return strings.Join(parts, "\n")
}
