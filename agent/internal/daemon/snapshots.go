package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	gosync "sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// snapshotRetry is how often sessions whose snapshot could not be sent yet are tried again.
const snapshotRetry = 2 * time.Second

// errNotSynced means the core does not hold the session yet; its snapshot waits for the first append.
var errNotSynced = errors.New("session not synced yet")

// uploader is the part of the sync client the snapshotter uses.
type uploader interface {
	UploadSnapshot(ctx context.Context, up sync.SnapshotUpload, r io.Reader) error
	Cursor(sessionID string) (int64, bool)
}

// snapshotter makes the workspace snapshot of a session after each completed turn and uploads it to the
// core (docs/spec.md#remote-control, step 7). Only the latest state matters, so a session is marked once
// however many turns ended since its last upload; snapshots are made one at a time.
type snapshotter struct {
	dir    string // where archives are made; they are removed after the upload
	locate runtime.Locator
	up     uploader

	mu     gosync.Mutex
	dirty  map[string]bool
	warned map[string]error
	wake   chan struct{}
	// busy serializes snapshots of the same session between the loop and a handoff.
	busy gosync.Mutex
}

func newSnapshotter(home string, locate runtime.Locator, up uploader) *snapshotter {
	dir := filepath.Join(home, "handoff", "snapshots")
	// Archives of an earlier run that did not finish are of no use.
	_ = os.RemoveAll(dir)
	return &snapshotter{
		dir:    dir,
		locate: locate,
		up:     up,
		dirty:  map[string]bool{},
		warned: map[string]error{},
		wake:   make(chan struct{}, 1),
	}
}

// mark records that a turn of the session ended. It never blocks.
func (s *snapshotter) mark(sessionID string) {
	s.mu.Lock()
	s.dirty[sessionID] = true
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run uploads the snapshots of marked sessions until ctx ends.
func (s *snapshotter) run(ctx context.Context) {
	ticker := time.NewTicker(snapshotRetry)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		s.mu.Lock()
		var ids []string
		for id := range s.dirty {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			s.settle(id, s.snapshot(ctx, id))
		}
	}
}

// settle clears a session's mark unless the snapshot should be tried again, and reports a failure once.
func (s *snapshotter) settle(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err == nil:
		delete(s.dirty, id)
		delete(s.warned, id)
	case errors.Is(err, errNotSynced), errors.Is(err, sync.ErrNotConnected), errors.Is(err, workspace.ErrWorkspaceMoved):
		// Try again at the next tick.
	default:
		// A session outside a repository, a tree over the limits or a refused upload will not change by
		// trying again; the next completed turn marks the session anew.
		delete(s.dirty, id)
		if prev := s.warned[id]; prev == nil || prev.Error() != err.Error() {
			s.warned[id] = err
			log.Printf("[agent] no snapshot for %s: %v", shortID(id), err)
		}
	}
}

// snapshot makes the snapshot of the session's working tree and uploads it.
func (s *snapshotter) snapshot(ctx context.Context, sessionID string) error {
	_, err := s.capture(ctx, sessionID)
	return err
}

// capture makes and uploads the snapshot and returns it; the archive file is gone when it returns.
func (s *snapshotter) capture(ctx context.Context, sessionID string) (*workspace.Snapshot, error) {
	s.busy.Lock()
	defer s.busy.Unlock()
	loc, err := s.locate(sessionID)
	if err != nil {
		return nil, err
	}
	if _, ok := s.up.Cursor(sessionID); !ok {
		return nil, errNotSynced
	}
	snap, err := workspace.CreateSnapshot(ctx, loc.Dir, s.dir)
	if err != nil {
		return nil, err
	}
	defer os.Remove(snap.Path)
	f, err := os.Open(snap.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	err = s.up.UploadSnapshot(ctx, sync.SnapshotUpload{
		SessionID:  sessionID,
		SnapshotID: snap.ID,
		CreatedAt:  snap.Meta.CreatedAt,
		BaseCommit: snap.Meta.BaseCommit,
		OriginURL:  snap.Meta.OriginURL,
		TreeHash:   snap.Meta.TreeHash,
		ByteSize:   snap.Size,
		SHA256:     snap.SHA256,
	}, f)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
